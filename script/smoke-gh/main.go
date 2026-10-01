// Controlled GitHub/Agent boundary for installed-extension smoke. No network I/O.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type state struct {
	Node           map[string]any              `json:"node"`
	Root           map[string]any              `json:"root"`
	Managed        map[string]string           `json:"managed"`
	Canonical      map[string]string           `json:"canonical"`
	Issues         []map[string]any            `json:"issues"`
	Comments       map[string][]map[string]any `json:"comments"`
	Writes         int                         `json:"writes"`
	AgentCalls     int                         `json:"agentCalls"`
	Starred        bool                        `json:"starred"`
	WorkflowActive bool                        `json:"workflowActive"`
	Unavailable    string                      `json:"unavailable"`
	Lost           string                      `json:"lost"`
	LostDone       bool                        `json:"lostDone"`
}

const sha = "1111111111111111111111111111111111111111"

func main() {
	if strings.HasPrefix(filepath.Base(os.Args[0]), "git") {
		if strings.Contains(strings.Join(os.Args[1:], " "), "remote get-url") {
			fmt.Println("https://github.com/reviewer/shoal-station.git")
			return
		}
		cmd := exec.Command(os.Getenv("SHOAL_REAL_GIT"), os.Args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			os.Exit(1)
		}
		return
	}

	path := os.Getenv("SHOAL_SMOKE_STATE")
	b, err := os.ReadFile(path)
	must(err)
	var s state
	must(json.Unmarshal(b, &s))
	save := func() { b, err := json.Marshal(s); must(err); must(os.WriteFile(path, b, 0600)) }
	fail := func(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
	reply := func(v any) { b, err := json.Marshal(v); must(err); fmt.Println(string(b)) }
	if strings.HasPrefix(filepath.Base(os.Args[0]), "codex") {
		s.AgentCalls++
		save()
		must(os.MkdirAll(".shoal", 0700))
		must(os.WriteFile(filepath.Join(".shoal", "review-results.json"), []byte(`[{"issue":1,"verdict":"PASS","comment":"Controlled public evidence"}]`), 0600))
		return
	}
	args := os.Args[1:]
	if strings.Join(args, " ") == "auth status" {
		return
	}
	if len(args) < 2 || args[0] != "api" {
		fail("unsupported controlled gh command")
	}
	paged := args[1] == "--paginate"
	method := "GET"
	endpoint := args[1]
	if paged {
		endpoint = args[3]
	}
	if args[1] == "--method" {
		method = args[2]
		endpoint = args[3]
	}
	if s.Unavailable != "" && strings.Contains(endpoint, s.Unavailable) {
		fail("controlled required read unavailable")
	}
	node := s.Node["full_name"].(string)
	root := s.Root["full_name"].(string)
	if method != "GET" {
		s.Writes++
		switch {
		case strings.HasPrefix(endpoint, "user/starred/"):
			s.Starred = method == "PUT"
		case endpoint == "repos/"+node:
			s.Node["has_issues"] = true
		case strings.HasSuffix(endpoint, "/enable"):
			s.WorkflowActive = true
		case strings.Contains(endpoint, "/issues/"):
			n := strings.Split(strings.TrimPrefix(endpoint, "repos/"+node+"/issues/"), "/")[0]
			field := strings.TrimPrefix(args[5], "body=")
			if strings.HasSuffix(endpoint, "/comments") {
				s.Comments[n] = append(s.Comments[n], map[string]any{"id": 100 + s.Writes, "body": field, "user": s.Node["owner"]})
			} else {
				for _, issue := range s.Issues {
					if strconv.Itoa(int(issue["number"].(float64))) == n {
						issue["state"] = strings.TrimPrefix(args[5], "state=")
					}
				}
			}
		default:
			fail("unexpected controlled mutation: " + endpoint)
		}
		lost := !s.LostDone && s.Lost != "" && strings.Contains(strings.Join(args, " "), s.Lost)
		if lost {
			s.LostDone = true
		}
		save()
		if lost {
			fail("controlled acknowledgement lost after mutation")
		}
		reply(map[string]any{})
		return
	}
	switch {
	case endpoint == "user":
		reply(s.Node["owner"])
	case endpoint == "repositories/1379044983":
		reply(s.Root)
	case endpoint == "repos/"+node:
		reply(s.Node)
	case endpoint == "repositories/55":
		reply(target())
	case endpoint == "repos/alice/project":
		reply(target())
	case strings.HasPrefix(endpoint, "repos/"+node+"/actions/workflows/"):
		status := "disabled_fork"
		if s.WorkflowActive {
			status = "active"
		}
		reply(map[string]any{"id": 123, "path": ".github/workflows/reviewer-summary.yml", "state": status})
	case strings.Contains(endpoint, "/git/ref/heads/"):
		reply(map[string]any{"object": map[string]string{"sha": sha}})
	case strings.Contains(endpoint, "/contents/"):
		files := s.Managed
		prefix := "repos/" + node + "/contents/"
		if strings.HasPrefix(endpoint, "repos/"+root+"/contents/") && node != root {
			files = s.Canonical
			prefix = "repos/" + root + "/contents/"
		}
		p, _, _ := strings.Cut(strings.TrimPrefix(endpoint, prefix), "?ref=")
		content, ok := files[p]
		if !ok {
			fail("HTTP 404: Not Found")
		}
		reply(map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))})
	case strings.Contains(endpoint, "/issues?"):
		reply([]any{s.Issues})
	case strings.Contains(endpoint, "/comments?"):
		n := strings.Split(strings.TrimPrefix(endpoint, "repos/"+node+"/issues/"), "/")[0]
		comments := s.Comments[n]
		if comments == nil {
			comments = []map[string]any{}
		}
		reply([]any{comments})
	case strings.Contains(endpoint, "/issues/"):
		n := strings.TrimPrefix(endpoint, "repos/"+node+"/issues/")
		for _, issue := range s.Issues {
			if strconv.Itoa(int(issue["number"].(float64))) == n {
				reply(issue)
				return
			}
		}
		fail("HTTP 404: Not Found")
	case strings.HasPrefix(endpoint, "users/alice/repos?"):
		reply([]any{[]any{requester()}})
	case endpoint == "repos/alice/shoal-station":
		reply(requester())
	case strings.Contains(endpoint, "/branches/"):
		reply(map[string]any{"commit": map[string]string{"sha": sha}})
	case strings.Contains(endpoint, "/commits?"):
		reply([]any{map[string]string{"sha": sha}})
	case strings.HasPrefix(endpoint, "user/starred/"):
		if !s.Starred {
			fail("HTTP 404: Not Found")
		}
	default:
		fail("unexpected controlled GitHub read: " + endpoint)
	}
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func requester() map[string]any {
	return map[string]any{"id": 12, "full_name": "alice/shoal-station", "fork": true, "owner": map[string]any{"id": 2, "login": "alice", "type": "User"}, "parent": map[string]int{"id": 1379044983}}
}
func target() map[string]any {
	return map[string]any{"id": 55, "full_name": "alice/project", "name": "project", "default_branch": "main", "owner": map[string]any{"id": 2, "login": "alice", "type": "User"}}
}
