package reviewruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func hostFixture(t *testing.T, run runner, agent Agent) (*Runtime, *[]string) {
	t.Helper()
	roles := new([]string)
	transport := func(role string) GitHub {
		return GitHubFunc(func(ctx context.Context, r Request) ([]byte, error) {
			*roles = append(*roles, role+" "+r.Method+" "+r.Endpoint)
			if role == "read" && r.Method != "GET" {
				t.Fatal("read authority received mutation")
			}
			if role == "station" && (r.Method != "PATCH" || strings.Contains(r.Endpoint, "starred") || strings.Contains(r.Endpoint, "comments")) {
				t.Fatal("station received personal operation")
			}
			if role == "personal" && !(r.Endpoint == "user" || strings.HasPrefix(r.Endpoint, "user/starred/") || r.Method == "POST" && strings.HasSuffix(r.Endpoint, "/comments")) {
				t.Fatal("unexpected personal operation")
			}
			return localAPI(ctx, run, r)
		})
	}
	runtime, err := New(Dependencies{Reads: transport("read"), Lifecycle: transport("station"), Personal: transport("personal"), Git: func(ctx context.Context, args ...string) ([]byte, error) { return run(ctx, "git", args...) }, Agent: agent}, Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return runtime, roles
}
func judgmentAgent(t *testing.T, calls *int, verdict string) Agent {
	return AgentFunc(func(_ context.Context, w SemanticWork) ([]byte, error) {
		*calls++
		if len(w.Items) == 0 || len(w.Items) > 5 || w.ReviewerNodeID != 11 || w.PolicyPath != "README.md" {
			t.Fatalf("invalid semantic work: %+v", w)
		}
		var results []agentResult
		for _, item := range w.Items {
			if item.TargetRepositoryID != 55 || item.TargetCommit == "" || item.PolicyCommit == "" || item.Request == "" {
				t.Fatalf("basis missing: %+v", item)
			}
			results = append(results, agentResult{item.Issue, verdict, "Specific evidence."})
		}
		return json.Marshal(results)
	})
}
func semanticState(t *testing.T, f *fakeReview) string {
	t.Helper()
	var bodies []string
	for _, comment := range f.comments[1] {
		var event reviewEvent
		if decodeRecord(comment.Body, "shoal-review-event:v1", &event) {
			event.ReviewedAt = ""
			b, _ := json.Marshal(event)
			bodies = append(bodies, string(b))
		} else {
			bodies = append(bodies, comment.Body)
		}
	}
	return f.state(1) + "|" + strings.Join(bodies, "|")
}
func TestPublicRuntimeLocalAndHostLifecycleParity(t *testing.T) {
	for _, maintenance := range []bool{false, true} {
		for _, verdict := range []string{"PASS", "FAIL"} {
			t.Run(map[bool]string{false: "review", true: "re-review"}[maintenance]+"/"+verdict, func(t *testing.T) {
				local := newReReviewFake()
				host := newReReviewFake()
				for _, f := range []*reReviewFake{local, host} {
					f.addIssue(1, reviewBody("project"), f.requester.Owner)
					if maintenance {
						f.issues[0].State = "closed"
						f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), priorJudgment(t, f.target, "PASS")}
						f.heads[55] = testSHA2
					}
					f.stars[f.target.FullName] = true
					f.starred = true
				}
				dir := t.TempDir()
				local.resultFile = filepath.Join(dir, ".shoal", "review-results.json")
				local.agentOutput = `[{"issue":1,"verdict":"` + verdict + `","comment":"Specific evidence."}]`
				l, err := newLocal("codex", Options{Directory: dir}, local.run)
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				h, roles := hostFixture(t, host.run, judgmentAgent(t, &calls, verdict))
				var lr, hr Result
				if maintenance {
					lr = l.ReReview(context.Background())
					hr = h.ReReview(context.Background())
				} else {
					lr = l.Review(context.Background())
					hr = h.Review(context.Background())
				}
				if len(lr.Faults) > 0 || len(hr.Faults) > 0 || lr.Status != "COMPLETED" || !reflect.DeepEqual(lr, hr) {
					t.Fatalf("results differ: %+v %+v", lr, hr)
				}
				if semanticState(t, local.fakeReview) != semanticState(t, host.fakeReview) || local.stars[local.target.FullName] != host.stars[host.target.FullName] || calls != 1 {
					t.Fatalf("lifecycle parity failed: %v", *roles)
				}
				all := strings.Join(*roles, "|")
				if !strings.Contains(all, "personal POST") || (!maintenance && !strings.Contains(all, "station PATCH")) || !strings.Contains(all, "personal GET user/starred/") {
					t.Fatalf("role evidence missing: %s", all)
				}
			})
		}
	}
}
func TestHostRefusalBeforeAgentAndLifecycle(t *testing.T) {
	for _, maintenance := range []bool{false, true} {
		for _, kind := range []string{"station", "history", "external", "dirty", "identity"} {
			t.Run(kind+map[bool]string{false: "/review", true: "/re-review"}[maintenance], func(t *testing.T) {
				f := fixture()
				f.addIssue(1, reviewBody("project"), f.requester.Owner)
				switch kind {
				case "station":
					f.managed[summaryPath] = []byte("unsupported")
					f.canonical[summaryPath] = []byte("new unsupported")
				case "history":
					f.comments[1] = []reviewComment{{ID: 1, User: f.node.Owner, Body: "shoal-review-event:v999\n{}"}}
				case "external":
					f.failOn = "contents/"
				case "dirty":
					f.dirty = true
				}
				calls := 0
				h, roles := hostFixture(t, f.run, judgmentAgent(t, &calls, "PASS"))
				if kind == "identity" {
					h.dependencies.Personal = GitHubFunc(func(context.Context, Request) ([]byte, error) { return []byte(`{"id":999,"type":"Bot"}`), nil })
				}
				var result Result
				if maintenance {
					result = h.ReReview(context.Background())
				} else {
					result = h.Review(context.Background())
				}
				if len(result.Faults) == 0 || result.EffectAttempts != 0 || calls != 0 || f.state(1) != "open" || f.starred {
					t.Fatalf("unsafe refusal: %+v %v", result, *roles)
				}
				for _, call := range *roles {
					if strings.Contains(call, " POST ") || strings.Contains(call, " PATCH ") || strings.Contains(call, " PUT ") || strings.Contains(call, " DELETE ") {
						t.Fatalf("mutated during refusal: %v", *roles)
					}
				}
			})
		}
	}
}
func TestHostAgentFailuresLeaveWorkPending(t *testing.T) {
	for _, raw := range []string{"", `null`, `[{"issue":1,"verdict":"PASS","comment":"one"},{"issue":1,"verdict":"PASS","comment":"two"}]`, `[{"issue":999,"verdict":"PASS","comment":"foreign"}]`, `[{"issue":1,"verdict":"MAYBE","comment":"invalid"}]`, `[{"issue":1,"verdict":"FAIL","comment":""}]`, `unavailable`} {
		t.Run(raw, func(t *testing.T) {
			f := fixture()
			f.addIssue(1, reviewBody("project"), f.requester.Owner)
			h, _ := hostFixture(t, f.run, AgentFunc(func(context.Context, SemanticWork) ([]byte, error) {
				if raw == "unavailable" {
					return nil, errors.New("quota")
				}
				return []byte(raw), nil
			}))
			r := h.Review(context.Background())
			if len(r.Faults) == 0 || f.state(1) != "open" || f.starred || len(f.bodies(1)) != 1 {
				t.Fatalf("failure became judgment: %+v %v", r, f.calls)
			}
			wanted := "AGENT_RESULT_INVALID"
			if raw == "unavailable" {
				wanted = "AGENT_UNAVAILABLE"
			}
			if r.Faults[0].Code != wanted {
				t.Fatalf("wrong failure: %+v", r)
			}
		})
	}
}
func TestPersonalAuthorityNeverFallsBack(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	calls := 0
	h, roles := hostFixture(t, f.run, judgmentAgent(t, &calls, "PASS"))
	h.dependencies.Personal = GitHubFunc(func(ctx context.Context, r Request) ([]byte, error) {
		if r.Endpoint == "user" {
			return localAPI(ctx, f.run, r)
		}
		return nil, errors.New("Reviewer grant revoked")
	})
	r := h.Review(context.Background())
	if len(r.Faults) == 0 || calls != 0 || len(f.bodies(1)) != 0 || f.starred || f.state(1) != "open" {
		t.Fatalf("personal authority fallback: %+v %v", r, *roles)
	}
	h.dependencies.Personal = nil
	_, err := New(h.dependencies, Options{})
	if err == nil {
		t.Fatal("missing personal authority accepted")
	}
}
func TestHostSuccessfulEventFailedCloseRecovery(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	calls := 0
	h, _ := hostFixture(t, f.run, judgmentAgent(t, &calls, "PASS"))
	f.failOn = "--method PATCH"
	first := h.Review(context.Background())
	if first.Status != "PARTIAL" || calls != 1 || len(f.bodies(1)) != 2 || f.state(1) != "open" {
		t.Fatalf("partial result: %+v", first)
	}
	f.failOn = ""
	second := h.Review(context.Background())
	if len(second.Faults) > 0 || calls != 1 || len(f.bodies(1)) != 2 || f.state(1) != "closed" {
		t.Fatalf("recovery replayed semantic judgment: %+v", second)
	}
	third := h.Review(context.Background())
	if third.Status != "NO_CHANGES" {
		t.Fatalf("no-op: %+v", third)
	}
}
func TestHostReadBackRejectsWrongCommentAuthorAndUnappliedState(t *testing.T) {
	for _, kind := range []string{"author", "state"} {
		t.Run(kind, func(t *testing.T) {
			f := fixture()
			f.addIssue(1, reviewBody("project"), f.requester.Owner)
			calls := 0
			h, _ := hostFixture(t, f.run, judgmentAgent(t, &calls, "PASS"))
			transport := GitHubFunc(func(ctx context.Context, r Request) ([]byte, error) {
				if kind == "state" && r.Method == "PATCH" {
					return []byte(`{}`), nil
				}
				data, err := localAPI(ctx, f.run, r)
				if kind == "author" && r.Method == "POST" {
					for i := range f.comments[1] {
						f.comments[1][i].User.ID = 999
					}
				}
				return data, err
			})
			if kind == "author" {
				h.dependencies.Personal = transport
			} else {
				h.dependencies.Lifecycle = transport
			}
			r := h.Review(context.Background())
			if len(r.Faults) == 0 || f.state(1) != "open" {
				t.Fatalf("accepted unapplied effect: %+v", r)
			}
			if kind == "author" && calls != 0 {
				t.Fatal("semantic execution followed unverified admission")
			}
		})
	}
}
func TestLocalUnknownAgentBeforeEffects(t *testing.T) {
	calls := 0
	_, err := newLocal("unknown", Options{}, func(context.Context, string, ...string) ([]byte, error) { calls++; return nil, nil })
	if err == nil || calls != 0 {
		t.Fatal("unsupported local adapter ran effects")
	}
}

func TestHostAmbiguousWritesObserveBeforeReplay(t *testing.T) {
	for _, kind := range []string{"star", "comment", "state"} {
		t.Run(kind, func(t *testing.T) {
			f := fixture()
			f.addIssue(1, reviewBody("project"), f.requester.Owner)
			calls, writes := 0, 0
			h, _ := hostFixture(t, f.run, judgmentAgent(t, &calls, "PASS"))
			effect := GitHubFunc(func(ctx context.Context, r Request) ([]byte, error) {
				data, err := localAPI(ctx, f.run, r)
				ambiguous := kind == "star" && r.Method == "PUT" || kind == "comment" && r.Method == "POST" || kind == "state" && r.Method == "PATCH"
				if ambiguous && err == nil {
					writes++
					return nil, errors.New("lost acknowledgement")
				}
				return data, err
			})
			if kind == "state" {
				h.dependencies.Lifecycle = effect
			} else {
				h.dependencies.Personal = effect
			}
			result := h.Review(context.Background())
			if len(result.Faults) > 0 || calls != 1 || f.state(1) != "closed" || !f.starred {
				t.Fatalf("lost-ack recovery: %+v", result)
			}
			want := 1
			if kind == "comment" {
				want = 2
			}
			if writes != want {
				t.Fatalf("ambiguous write replayed: %d", writes)
			}
		})
	}
}
func TestUnavailableAgentStopsFurtherBatches(t *testing.T) {
	f := fixture()
	for n := 1; n <= 7; n++ {
		f.addIssue(n, reviewBody("project-"+fmt.Sprint(n)), f.requester.Owner)
	}
	calls := 0
	h, _ := hostFixture(t, f.run, AgentFunc(func(context.Context, SemanticWork) ([]byte, error) { calls++; return nil, errors.New("unavailable") }))
	result := h.Review(context.Background())
	if len(result.Faults) == 0 || calls != 1 {
		t.Fatalf("continued unavailable Agent: %+v calls=%d", result, calls)
	}
	for n := 1; n <= 7; n++ {
		if f.state(n) != "open" || len(f.bodies(n)) != 1 {
			t.Fatal("pending work changed into judgment")
		}
	}
}

func TestHostAgentChangingStationCannotPublishResult(t *testing.T) {
	for _, maintenance := range []bool{false, true} {
		t.Run(fmt.Sprint(maintenance), func(t *testing.T) {
			f := newReReviewFake()
			f.addIssue(1, reviewBody("project"), f.requester.Owner)
			if maintenance {
				f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), priorJudgment(t, f.target, "FAIL"), pendingReReview(t, f.target, 2)}
				f.heads[55] = testSHA2
			}
			h, _ := hostFixture(t, f.run, AgentFunc(func(context.Context, SemanticWork) ([]byte, error) {
				f.dirty = true
				return []byte(`[{"issue":1,"verdict":"PASS","comment":"Specific evidence."}]`), nil
			}))
			var r Result
			if maintenance {
				r = h.ReReview(context.Background())
			} else {
				r = h.Review(context.Background())
			}
			wantComments := 1
			if maintenance {
				wantComments = 3
			}
			if len(r.Faults) == 0 || !strings.Contains(r.Faults[0].Detail, "Station changed") || f.starred || f.state(1) != "open" || len(f.bodies(1)) != wantComments {
				t.Fatalf("dirty Agent published result: %+v comments=%v", r, f.bodies(1))
			}
		})
	}
}
func TestPersonalStarAuthorityUnavailableAfterAdmission(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	calls := 0
	h, roles := hostFixture(t, f.run, judgmentAgent(t, &calls, "PASS"))
	h.dependencies.Personal = GitHubFunc(func(ctx context.Context, r Request) ([]byte, error) {
		if strings.HasPrefix(r.Endpoint, "user/starred/") {
			return nil, errors.New("Reviewer authority unavailable")
		}
		return localAPI(ctx, f.run, r)
	})
	result := h.Review(context.Background())
	if len(result.Faults) == 0 || calls != 1 || f.starred || f.state(1) != "open" || len(f.bodies(1)) != 1 {
		t.Fatalf("unavailable personal Star authority: %+v %v", result, *roles)
	}
	for _, role := range *roles {
		if strings.HasPrefix(role, "station ") {
			t.Fatalf("personal effect downgraded: %v", *roles)
		}
	}
}
