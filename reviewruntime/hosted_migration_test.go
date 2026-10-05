package reviewruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Auxiliary bytes are controlled migration evidence, never compatibility authority.
func syncFixture() map[string][]byte {
	files := realManagedFixture()
	files[hostedPath] = []byte("name: Controlled Hosted fixture\non:\n  workflow_call:\n")
	return files
}

func TestSynchronizationAndCompatibilityHaveDistinctPathContracts(t *testing.T) {
	if !reflect.DeepEqual(managedPaths, []string{requestFormPath, summaryPath, hostedPath}) {
		t.Fatal("synchronization lost a managed surface")
	}
	if !reflect.DeepEqual(compatibilityPaths, []string{requestFormPath, summaryPath}) {
		t.Fatal("auxiliary Hosted surface became base compatibility")
	}
	for _, aux := range [][]byte{nil, []byte("stale or disabled Hosted capability")} {
		files := realManagedFixture()
		if aux != nil {
			files[hostedPath] = aux
		}
		capability, err := loadCapability(protocolFixture(t))
		if err != nil || !capability.supports(files) {
			t.Fatalf("optional Hosted affected base support: %v", err)
		}
	}
	// Read failures on the auxiliary path cannot poison the base snapshot, either
	// on the Reviewer Node or the canonical Root.
	for _, repository := range []string{"reviewer/shoal-station", "root/shoal-station"} {
		var read []string
		files, err := committedCompatibilityFiles(context.Background(), func(_ context.Context, endpoint string, result any) error {
			if strings.Contains(endpoint, hostedPath) {
				t.Fatal("base compatibility read the auxiliary surface")
			}
			if strings.Contains(endpoint, "/git/ref/") {
				return json.Unmarshal([]byte(`{"object":{"sha":"`+strings.Repeat("a", 40)+`"}}`), result)
			}
			for _, path := range compatibilityPaths {
				if strings.Contains(endpoint, "/contents/"+path+"?ref=") {
					read = append(read, path)
					b, _ := json.Marshal(map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString(realManagedFixture()[path])})
					return json.Unmarshal(b, result)
				}
			}
			return errors.New("auxiliary boundary unavailable")
		}, repository, "main")
		if err != nil || !reflect.DeepEqual(read, compatibilityPaths) || len(files) != 2 {
			t.Fatalf("base reads escaped contract: %v %v", read, err)
		}
	}
}

func TestHostedManagedPathRejectsRedirection(t *testing.T) {
	for _, parent := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, hostedPath)
		if parent {
			path = filepath.Dir(path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), path); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if safePath(dir, hostedPath) == nil {
			t.Fatal("Hosted path redirection accepted")
		}
	}
}

func TestHostedSynchronizationAndRollback(t *testing.T) {
	for _, initial := range []string{"missing", "stale"} {
		for _, failure := range []string{"", "verification", "commit", "push"} {
			t.Run(initial+"/"+failure, func(t *testing.T) {
				ctx := context.Background()
				dir := t.TempDir()
				remote := filepath.Join(t.TempDir(), "remote.git")
				git := func(args ...string) string {
					t.Helper()
					out, err := systemRun(ctx, "git", append([]string{"-C", dir}, args...)...)
					if err != nil {
						t.Fatal(err)
					}
					return strings.TrimSpace(string(out))
				}
				if _, err := systemRun(ctx, "git", "init", "--bare", remote); err != nil {
					t.Fatal(err)
				}
				git("init", "-b", "main")
				git("config", "user.name", "Controlled Reviewer")
				git("config", "user.email", "fixture@example.com")
				contents := syncFixture()
				original := []byte("stale reusable bytes\n")
				for _, path := range compatibilityPaths {
					full := filepath.Join(dir, path)
					if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(full, contents[path], 0644); err != nil {
						t.Fatal(err)
					}
				}
				if initial == "stale" {
					if err := os.WriteFile(filepath.Join(dir, hostedPath), original, 0755); err != nil {
						t.Fatal(err)
					}
				}
				policy := []byte("Reviewer-owned README policy\n")
				if err := os.WriteFile(filepath.Join(dir, "README.md"), policy, 0644); err != nil {
					t.Fatal(err)
				}
				git("add", ".")
				git("commit", "-m", "Controlled pre-init fork")
				git("remote", "add", "origin", remote)
				git("push", "origin", "HEAD:refs/heads/main")
				pre := git("rev-parse", "HEAD")
				commits, pushes := 0, 0
				c := initCommand{dir: dir, run: func(ctx context.Context, program string, args ...string) ([]byte, error) {
					operation := strings.Join(args, " ")
					if strings.Contains(operation, " commit ") {
						commits++
					}
					if strings.Contains(operation, " push ") {
						pushes++
					}
					fail := failure == "verification" && strings.Contains(operation, " diff --cached ") || failure == "commit" && strings.Contains(operation, " commit ") || failure == "push" && strings.Contains(operation, " push ")
					if fail {
						return nil, errors.New("controlled synchronization failure")
					}
					return systemRun(ctx, program, args...)
				}}
				err := c.sync(ctx, pre, "origin", contents)
				if (err != nil) != (failure != "") {
					t.Fatalf("unexpected synchronization result: %v", err)
				}
				actual, readErr := os.ReadFile(filepath.Join(dir, hostedPath))
				if failure == "" {
					if readErr != nil || !bytes.Equal(actual, contents[hostedPath]) {
						t.Fatal("Hosted did not converge to exact canonical bytes")
					}
					if git("rev-parse", "HEAD") == pre || commits != 1 || pushes != 1 {
						t.Fatal("migration did not commit and push once")
					}
					if git("rev-parse", "HEAD") != git("ls-remote", "origin", "refs/heads/main")[:40] {
						t.Fatal("migration did not reach remote")
					}
				} else {
					if initial == "missing" {
						if !os.IsNotExist(readErr) {
							t.Fatal("rollback left a new auxiliary file")
						}
					} else {
						info, e := os.Stat(filepath.Join(dir, hostedPath))
						if readErr != nil || e != nil || !bytes.Equal(actual, original) || info.Mode().Perm() != 0755 {
							t.Fatal("rollback lost original auxiliary bytes or mode")
						}
					}
					if git("rev-parse", "HEAD") != pre || git("ls-remote", "origin", "refs/heads/main")[:40] != pre {
						t.Fatal("failed migration changed local or remote identity")
					}
				}
				if git("status", "--porcelain") != "" {
					t.Fatal("migration or recovery left a dirty index")
				}
				after, _ := os.ReadFile(filepath.Join(dir, "README.md"))
				if !bytes.Equal(after, policy) {
					t.Fatal("migration changed Reviewer policy")
				}
			})
		}
	}
}
