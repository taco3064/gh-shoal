package cli

import (
	"context"
	"embed"
	"strings"
	"testing"
)

//go:embed testdata/*
var managedFixtureFS embed.FS

func realManagedFixture() map[string][]byte {
	form, _ := managedFixtureFS.ReadFile("testdata/review-request.yml")
	workflow, _ := managedFixtureFS.ReadFile("testdata/reviewer-summary-current.yml")
	return map[string][]byte{managedPaths[0]: form, summaryPath: workflow}
}

func TestInstalledCapabilityUsesExplicitRealContracts(t *testing.T) {
	s, err := loadCapability(protocolFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"current", "older"} {
		files := realManagedFixture()
		if name == "older" {
			files[summaryPath], _ = managedFixtureFS.ReadFile("testdata/reviewer-summary-older.yml")
		}
		if !s.supports(files) {
			t.Fatalf("official %s generation rejected", name)
		}
		files[summaryPath] = append(append([]byte{}, files[summaryPath]...), '\n')
		if s.supports(files) {
			t.Fatal("byte-different equivalent YAML accepted")
		}
	}
	p := protocolFixture(t)
	p.ProtocolVersion = "999"
	if _, err := loadCapability(p); err == nil {
		t.Fatal("unknown runtime Protocol accepted")
	}
}

func TestReviewCompatibilityPreflightRefusesBeforeAnySideEffects(t *testing.T) {
	for _, command := range []string{"review", "re-review"} {
		for _, kind := range []string{"byte drift", "missing file", "Issues disabled", "canonical newer", "unavailable", "unknown event", "unknown admission", "unsupported tuple"} {
			t.Run(command+"/"+kind, func(t *testing.T) {
				f := fixture()
				f.addIssue(1, "invalid request that would otherwise close", f.requester.Owner)
				want := "REPAIRABLE_STATION_DRIFT"
				switch kind {
				case "byte drift":
					f.managed[summaryPath] = append(f.managed[summaryPath], '\n')
				case "missing file":
					delete(f.managed, managedPaths[0])
				case "Issues disabled":
					f.node.HasIssues = boolPtr(false)
				case "canonical newer":
					f.managed[summaryPath] = []byte("new unsupported")
					f.canonical[summaryPath] = []byte("new unsupported")
					want = "CLI_UPGRADE_REQUIRED"
				case "unavailable":
					f.failOn = "/contents/"
					want = "EXTERNAL_STATE_UNAVAILABLE"
				default:
					want = "INCOMPATIBLE_PROTOCOL_EVIDENCE"
					body := "shoal-review-event:v999\n{}"
					if kind == "unknown admission" {
						body = "shoal-review-admission:v999\n{}"
					}
					if kind == "unsupported tuple" {
						body = "shoal-review-event:v1\n{\"protocolVersion\":999}"
					}
					f.comments[1] = []reviewComment{{ID: 10, Body: body, User: f.node.Owner}}
				}
				c := f.automatedCommand(t)
				var err error
				if command == "review" {
					err = c.execute(context.Background(), []string{"--agent", "codex"})
				} else {
					err = c.executeReReview(context.Background(), []string{"--agent", "codex"})
				}
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("got %v, want %s", err, want)
				}
				if f.agentCalls != 0 || f.starred || f.state(1) != "open" {
					t.Fatal("refusal reached side effects")
				}
				for _, call := range f.calls {
					if strings.Contains(call, "--method") {
						t.Fatalf("unsafe mutation %s", call)
					}
				}
			})
		}
	}
}

func TestSupportedOlderStationRunsExistingReviewAndReReview(t *testing.T) {
	for _, command := range []string{"review", "re-review"} {
		t.Run(command, func(t *testing.T) {
			f := fixture()
			f.managed[summaryPath], _ = managedFixtureFS.ReadFile("testdata/reviewer-summary-older.yml")
			if command == "review" {
				f.addIssue(1, reviewBody("project"), f.requester.Owner)
				f.agentOutput = `[{"issue":1,"verdict":"PASS","comment":"Approved evidence"}]`
				if err := f.automatedCommand(t).execute(context.Background(), []string{"--agent", "codex"}); err != nil {
					t.Fatal(err)
				}
				if !f.starred || f.agentCalls != 1 || f.state(1) != "closed" {
					t.Fatal("older supported Review failed")
				}
			} else {
				r := newReReviewFake()
				r.managed[summaryPath] = f.managed[summaryPath]
				r.addIssue(1, reviewBody("project"), r.requester.Owner)
				r.issues[0].State = "closed"
				r.comments[1] = []reviewComment{admissionFor(t, r.target, "project"), priorJudgment(t, r.target, "PASS")}
				r.stars[r.target.FullName] = true
				if err := r.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
