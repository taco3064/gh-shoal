package cli

import (
 "context"
 "encoding/base64"
 "encoding/json"
 "errors"
 "os"
 "path/filepath"
 "runtime"
 "strings"
 "testing"
)

func TestDirectForkMembership(t *testing.T){
 node:=repository{ID:99,Fork:true}
 node.Parent=&struct{ID int64 `json:"id"`}{ID:rootID}
 node.Source=&struct{ID int64 `json:"id"`}{ID:rootID}
 node.Owner.ID=42;node.Owner.Type="User"
 c:=initCommand{}
 if err:=c.validateNode(node,42);err!=nil{t.Fatal(err)}
 cases:=map[string]repository{}
 root:=node;root.ID=rootID;cases["root"]=root
 downstream:=node;downstream.Parent=&struct{ID int64 `json:"id"`}{ID:88};cases["downstream despite root source"]=downstream
 org:=node;org.Owner.Type="Organization";cases["organization"]=org
 unrelated:=node;unrelated.Fork=false;cases["unrelated"]=unrelated
 other:=node;other.Owner.ID=8;cases["different authenticated owner"]=other
 for name,r:=range cases{t.Run(name,func(t *testing.T){if c.validateNode(r,42)==nil{t.Fatal("must reject")}})}
}
func TestRepositoryLocator(t *testing.T){
 for _,raw:=range []string{"https://github.com/example/station.git","git@github.com:example/station.git"}{name,err:=repositoryLocator(raw);if err!=nil||name!="example/station"{t.Fatalf("%q: %q %v",raw,name,err)}}
 for _,raw:=range []string{"https://evil.com/example/station.git","https://evil.com@github.com/example/station.git","file:///example/station","https://github.com/example/station/../../bad"}{if _,err:=repositoryLocator(raw);err==nil{t.Errorf("accepted %q",raw)}}
}
func TestUnsafeManagedPath(t *testing.T){
 dir:=t.TempDir();if err:=os.Symlink(t.TempDir(),filepath.Join(dir,".github"));err!=nil{t.Fatal(err)}
 if safePath(dir,managedPaths[0])==nil{t.Fatal("symlinked parent accepted")}
}
func TestPreconditionStopsBeforeMutation(t *testing.T){
 cases:=[]struct{name,branch,status string}{ {"wrong branch","topic",""},{"dirty index or working tree","main"," M README.md\x00"} }
 for _,tt:=range cases{t.Run(tt.name,func(t *testing.T){var calls []string
  c:=initCommand{dir:t.TempDir(),run:func(_ context.Context,program string,args ...string)([]byte,error){calls=append(calls,program+" "+strings.Join(args," "));if strings.Contains(strings.Join(args," "),"symbolic-ref"){return []byte(tt.branch),nil};if strings.Contains(strings.Join(args," "),"status"){return []byte(tt.status),nil};return nil,errors.New("unexpected")}}
  if err:=c.execute(context.Background(),nil);err==nil{t.Fatal("expected error")}
  for _,call:=range calls{if strings.HasPrefix(call,"gh ")||strings.Contains(call,"push")||strings.Contains(call,"commit"){t.Fatalf("unexpected side effect: %s",call)}}
 })}
}

func TestCanonicalNoDiffDoesNotMutateActionsOrGit(t *testing.T){
 dir:=t.TempDir()
 contents:=map[string][]byte{}
 for _,path:=range managedPaths{
  contents[path]=[]byte("canonical "+path+"\n")
  full:=filepath.Join(dir,path)
  if err:=os.MkdirAll(filepath.Dir(full),0755);err!=nil{t.Fatal(err)}
  if err:=os.WriteFile(full,contents[path],0644);err!=nil{t.Fatal(err)}
 }
 readme:=[]byte("Reviewer-owned policy\n")
 if err:=os.WriteFile(filepath.Join(dir,"README.md"),readme,0644);err!=nil{t.Fatal(err)}
 head:=strings.Repeat("a",40)
 var calls []string
 c:=initCommand{dir:dir,run:func(_ context.Context,program string,args ...string)([]byte,error){
  call:=program+" "+strings.Join(args," ");calls=append(calls,call)
  if program=="git"{
   if len(args)<3||args[0]!="-C"||args[1]!=dir{t.Fatalf("unexpected git invocation: %s",call)}
   switch strings.Join(args[2:]," "){
   case "symbolic-ref --quiet --short HEAD":return []byte("main\n"),nil
   case "status --porcelain=v1 -z --untracked-files=all":return nil,nil
   case "rev-parse HEAD":return []byte(head),nil
   case "remote":return []byte("origin"),nil
   case "remote get-url origin":return []byte("https://github.com/reviewer/shoal-station.git"),nil
   case "ls-remote --exit-code origin refs/heads/main":return []byte(head+"\trefs/heads/main"),nil
   }
  }
  if program=="gh"{
   if strings.Join(args," ")=="auth status"{return nil,nil}
   if len(args)==2&&args[0]=="api"{
    switch args[1]{
    case "user":return []byte(`{"id":42}`),nil
    case "repos/reviewer/shoal-station":return []byte(`{"id":99,"full_name":"reviewer/shoal-station","fork":true,"default_branch":"main","has_issues":true,"owner":{"id":42,"type":"User"},"parent":{"id":1379044983}}`),nil
    case "repositories/1379044983":return []byte(`{"id":1379044983,"full_name":"root/shoal-station","default_branch":"main"}`),nil
    case "repos/root/shoal-station/git/ref/heads/main":return []byte(`{"object":{"sha":"`+head+`"}}`),nil
    }
    prefix:="repos/root/shoal-station/contents/"
    if strings.HasPrefix(args[1],prefix){
     path,_,ok:=strings.Cut(strings.TrimPrefix(args[1],prefix),"?ref=")
     if !ok||!strings.HasSuffix(args[1],"?ref="+head)||contents[path]==nil{t.Fatalf("unexpected canonical path: %s",args[1])}
     return json.Marshal(map[string]string{"type":"file","encoding":"base64","content":base64.StdEncoding.EncodeToString(contents[path])})
    }
   }
  }
  t.Fatalf("unexpected invocation: %s",call);return nil,nil
 }}
 if err:=c.execute(context.Background(),nil);err!=nil{t.Fatal(err)}
 for _,call:=range calls{
  if strings.Contains(call,"actions/")||strings.Contains(call,"--method")||strings.Contains(call," commit ")||strings.Contains(call," push "){t.Errorf("unexpected side effect or Actions readiness inference: %s",call)}
 }
 after,err:=os.ReadFile(filepath.Join(dir,"README.md"));if err!=nil||string(after)!=string(readme){t.Fatalf("README changed: %v",err)}
}

func TestEnsureIssues(t *testing.T) {
	node := repository{ID: 99, FullName: "reviewer/shoal-station"}
	for _, tc := range []struct {
		name       string
		responses  []string
		patchError bool
		wantError  bool
		wantWrites int
	}{
		{name: "already enabled", responses: []string{`{"id":99,"has_issues":true}`}},
		{name: "disabled then enabled", responses: []string{`{"id":99,"has_issues":false}`, `{"id":99,"has_issues":true}`}, wantWrites: 1},
		{name: "write fails", responses: []string{`{"id":99,"has_issues":false}`}, patchError: true, wantError: true, wantWrites: 1},
		{name: "still disabled", responses: []string{`{"id":99,"has_issues":false}`, `{"id":99,"has_issues":false}`}, wantError: true, wantWrites: 1},
		{name: "verification unavailable", responses: []string{`{"id":99,"has_issues":false}`, `{"id":99}`}, wantError: true, wantWrites: 1},
		{name: "wrong repository identity", responses: []string{`{"id":100,"has_issues":false}`}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, writes := 0, 0
			c := initCommand{run: func(_ context.Context, program string, args ...string) ([]byte, error) {
				if program != "gh" || len(args) < 2 || args[0] != "api" {
					t.Fatalf("unexpected command: %s %v", program, args)
				}
				if len(args) == 2 && args[1] == "repos/reviewer/shoal-station" {
					if reads >= len(tc.responses) {
						t.Fatal("unexpected repository read")
					}
					out := tc.responses[reads]
					reads++
					return []byte(out), nil
				}
				expected := []string{"api", "--method", "PATCH", "repos/reviewer/shoal-station", "-F", "has_issues=true"}
				if strings.Join(args, "\x00") != strings.Join(expected, "\x00") {
					t.Fatalf("unexpected mutation (may modify unrelated settings): %v", args)
				}
				writes++
				if tc.patchError {
					return nil, errors.New("permission denied")
				}
				return []byte(`{"has_issues":true}`), nil
			}}
			err := c.ensureIssues(context.Background(), node)
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, want error = %v", err, tc.wantError)
			}
			if writes != tc.wantWrites {
				t.Fatalf("writes = %d, want %d", writes, tc.wantWrites)
			}
		})
	}
}

func TestInitRetryAfterIssuesFailureKeepsSuccessfulSync(t *testing.T) {
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
	git("config", "user.name", "Reviewer")
	git("config", "user.email", "reviewer@example.com")
	contents := map[string][]byte{}
	for _, path := range managedPaths {
		contents[path] = []byte("canonical " + path + "\n")
		p := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("outdated"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	readme := []byte("My review policy\n")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), readme, 0644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "fork state")
	git("remote", "add", "origin", remote)
	git("push", "origin", "HEAD:refs/heads/main")
	pre := git("rev-parse", "HEAD")
	head := strings.Repeat("a", 40)
	issuesEnabled := false
	failPatch := true
	writes := 0
	c := initCommand{dir: dir, run: func(ctx context.Context, program string, args ...string) ([]byte, error) {
		if program == "git" {
			if len(args) >= 5 && args[2] == "remote" && args[3] == "get-url" {
				return []byte("https://github.com/reviewer/shoal-station.git"), nil
			}
			return systemRun(ctx, program, args...)
		}
		if program != "gh" {
			t.Fatalf("unexpected program: %s", program)
		}
		if strings.Join(args, " ") == "auth status" {
			return nil, nil
		}
		if len(args) == 2 && args[0] == "api" {
			switch args[1] {
			case "user":
				return []byte(`{"id":42}`), nil
			case "repos/reviewer/shoal-station":
				return json.Marshal(map[string]any{"id": 99, "has_issues": issuesEnabled, "full_name": "reviewer/shoal-station", "fork": true, "owner": map[string]any{"id": 42, "type": "User"}, "parent": map[string]any{"id": rootID}})
			case "repositories/1379044983":
				return []byte(`{"id":1379044983,"full_name":"root/shoal-station","default_branch":"main"}`), nil
			case "repos/root/shoal-station/git/ref/heads/main":
				return []byte(`{"object":{"sha":"` + head + `"}}`), nil
			}
			prefix := "repos/root/shoal-station/contents/"
			if strings.HasPrefix(args[1], prefix) {
				path, _, ok := strings.Cut(strings.TrimPrefix(args[1], prefix), "?ref=")
				if !ok || !strings.HasSuffix(args[1], "?ref="+head) || contents[path] == nil {
					t.Fatalf("unexpected source %s", args[1])
				}
				return json.Marshal(map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString(contents[path])})
			}
		}
		expected := []string{"api", "--method", "PATCH", "repos/reviewer/shoal-station", "-F", "has_issues=true"}
		if strings.Join(args, "\x00") != strings.Join(expected, "\x00") {
			t.Fatalf("unexpected mutation: %v", args)
		}
		writes++
		if failPatch {
			return nil, errors.New("permission denied")
		}
		issuesEnabled = true
		return []byte(`{"has_issues":true}`), nil
	}}
	if err := c.execute(ctx, nil); err == nil {
		t.Fatal("first init should fail at Issues enablement")
	}
	synced := git("rev-parse", "HEAD")
	if synced == pre {
		t.Fatal("successful managed-file sync was rolled back")
	}
	if got := git("status", "--porcelain"); got != "" {
		t.Fatalf("dirty after failure: %s", got)
	}
	if got := git("ls-remote", "origin", "refs/heads/main"); !strings.HasPrefix(got, synced+"\t") {
		t.Fatalf("successful sync not pushed: %s", got)
	}
	failPatch = false
	if err := c.execute(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := git("rev-parse", "HEAD"); got != synced {
		t.Fatalf("retry created unnecessary commit: %s", got)
	}
	if writes != 2 || !issuesEnabled {
		t.Fatalf("Issues retry failed: writes=%d enabled=%v", writes, issuesEnabled)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "README.md")); err != nil || string(got) != string(readme) {
		t.Fatalf("README changed: %v", err)
	}
}

func TestSyncRepairsExecutableManagedFile(t *testing.T){
 if runtime.GOOS=="windows"{t.Skip("Git executable-bit tracking requires a Unix filesystem")}
 ctx:=context.Background();dir:=t.TempDir();remote:=filepath.Join(t.TempDir(),"remote.git")
 git:=func(args ...string)string{t.Helper();out,err:=systemRun(ctx,"git",append([]string{"-C",dir},args...)...);if err!=nil{t.Fatal(err)};return strings.TrimSpace(string(out))}
 if _,err:=systemRun(ctx,"git","init","--bare",remote);err!=nil{t.Fatal(err)}
 git("init","-b","main");git("config","user.name","Reviewer");git("config","user.email","reviewer@example.com")
 contents:=map[string][]byte{}
 for _,path:=range managedPaths{
  contents[path]=[]byte("canonical "+path+"\n")
  p:=filepath.Join(dir,path);if err:=os.MkdirAll(filepath.Dir(p),0755);err!=nil{t.Fatal(err)}
  if err:=os.WriteFile(p,contents[path],0644);err!=nil{t.Fatal(err)}
 }
 drift:=managedPaths[0];if err:=os.Chmod(filepath.Join(dir,drift),0755);err!=nil{t.Fatal(err)}
 readme:=[]byte("reviewer policy\n");if err:=os.WriteFile(filepath.Join(dir,"README.md"),readme,0644);err!=nil{t.Fatal(err)}
 git("add",".");git("commit","-m","fork state");git("remote","add","origin",remote);git("push","origin","HEAD:refs/heads/main")
 pre:=git("rev-parse","HEAD")
 c:=initCommand{dir:dir,run:systemRun}
 if err:=c.sync(ctx,pre,"origin",contents);err!=nil{t.Fatal(err)}
 if got:=git("status","--porcelain");got!=""{t.Fatalf("dirty after push: %q",got)}
 if got:=git("rev-parse","HEAD");got==pre{t.Fatal("mode repair did not create a commit")}
 if got:=git("ls-remote","origin","refs/heads/main");!strings.HasPrefix(got,git("rev-parse","HEAD")+"\t"){t.Fatalf("push did not update remote: %s",got)}
 if got:=git("ls-files","--stage",drift);!strings.HasPrefix(got,"100644 "){t.Fatalf("managed mode remains executable: %s",got)}
 info,err:=os.Stat(filepath.Join(dir,drift));if err!=nil||info.Mode().Perm()&0111!=0{t.Fatalf("working file remains executable: %v, %v",info,err)}
 b,err:=os.ReadFile(filepath.Join(dir,"README.md"));if err!=nil||string(b)!=string(readme){t.Fatalf("README changed: %v",err)}
}
