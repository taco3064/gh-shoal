package reviewruntime

import (
	"context"
	"embed"
	"encoding/json"
	"strings"
	"testing"
)

//go:embed testdata/*
var managedFixtureFS embed.FS

func realManagedFixture() map[string][]byte {
	form, _ := managedFixtureFS.ReadFile("testdata/review-request.yml")
	workflow, _ := managedFixtureFS.ReadFile("testdata/reviewer-summary-current.yml")
	return map[string][]byte{requestFormPath: form, summaryPath: workflow}
}

func TestInstalledCapabilityUsesExplicitRealContracts(t *testing.T) {
	s, err := loadCapability(protocolFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"current", "previous", "older"} {
		files := realManagedFixture()
		if name != "current" {
			files[summaryPath], _ = managedFixtureFS.ReadFile("testdata/reviewer-summary-" + name + ".yml")
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

func TestRefreshPreservesShippedCapabilityAndRejectsUnknownBindings(t *testing.T) {
	oldBytes, err := managedFixtureFS.ReadFile("testdata/capability-v0.6.0.json")
	if err != nil || hashBytes(oldBytes) != "85cdccabd9ffca8e7e758ebdb3d75f4abb240d86f3f67ad65265b7484a7a930d" {
		t.Fatal("historical shipped capability bytes changed")
	}
	var old capability
	if err := json.Unmarshal(oldBytes, &old); err != nil {
		t.Fatal(err)
	}
	s, err := loadCapability(protocolFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for digest, binding := range old.SummaryWorkflows {
		if s.SummaryWorkflows[digest] != binding {
			t.Fatalf("legacy binding changed: %s", digest)
		}
	}
	files := realManagedFixture()
	if old.supports(files) || !s.supports(files) {
		t.Fatal("Phase 3 support did not come exclusively from refreshed capability")
	}
	digest := hashBytes(files[summaryPath])
	binding := s.SummaryWorkflows[digest]
	if binding.ReviewerSummary != (summaryContract{1, 2}) {
		t.Fatal("Phase 3 fixture is not bound to Protocol 1 / Schema 2")
	}
	for _, contract := range []summaryContract{{1, 999}, {999, 2}} {
		modified := binding
		modified.ReviewerSummary = contract
		s.SummaryWorkflows[digest] = modified
		if s.supports(files) {
			t.Fatal("unknown Workflow / Summary contract accepted")
		}
	}
	binding.ActionCommit = "mutable-tag"
	s.SummaryWorkflows[digest] = binding
	if s.supports(files) {
		t.Fatal("non-immutable Action identity accepted")
	}
}

func TestFinalHostedCallerRefreshUsesAcceptedPlatformAuthority(t *testing.T) {
	oldBytes, err := managedFixtureFS.ReadFile("testdata/capability-v0.9.0.json")
	if err != nil || hashBytes(oldBytes) != "28ccae7b338b2a9e3c64141d1231d652dacbc3ead19d29a2397d1c87ed58629c" {
		t.Fatal("historical published capability bytes changed")
	}
	var old capability
	if err := json.Unmarshal(oldBytes, &old); err != nil {
		t.Fatal(err)
	}
	refreshed, err := loadCapability(protocolFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	caller, err := managedFixtureFS.ReadFile("testdata/reviewer-summary-final-hosted.yml")
	if err != nil || hashBytes(caller) != "b9162cae864bbd6e00745346f37f701fe5c003d3367cc3dc37c6fb394f9d8105" {
		t.Fatal("final caller frozen bytes changed")
	}
	files := realManagedFixture()
	files[summaryPath] = caller
	if old.supports(files) || !refreshed.supports(files) {
		t.Fatal("final caller support did not come from accepted Platform refresh")
	}
	for digest, binding := range old.SummaryWorkflows {
		// This preliminary candidate was owner-published without Platform acceptance;
		// its presence in the released snapshot cannot create compatibility authority.
		if digest == "11259fa4266e6369dfbdcf6195c6d975377e63ea32f1f6c5989261cc472afead" {
			continue
		}
		if refreshed.SummaryWorkflows[digest] != binding {
			t.Fatalf("accepted legacy binding changed: %s", digest)
		}
	}
	binding := refreshed.SummaryWorkflows[hashBytes(caller)]
	if binding.ActionCommit != "4918e1afe85f15f8fe263eaf2866cd02a1f70a62" || binding.ReviewerSummary != (summaryContract{1, 2}) {
		t.Fatal("final caller lost exact Summary Action / contract binding")
	}
	files[summaryPath] = []byte(strings.ReplaceAll(string(caller), binding.ActionCommit, strings.Repeat("b", 40)))
	if refreshed.supports(files) {
		t.Fatal("substituted Summary Action was accepted")
	}
	files[summaryPath] = caller
	for _, contract := range []summaryContract{{1, 999}, {999, 2}} {
		changed := binding
		changed.ReviewerSummary = contract
		refreshed.SummaryWorkflows[hashBytes(caller)] = changed
		if refreshed.supports(files) {
			t.Fatal("unknown final caller contract accepted")
		}
	}
	refreshed.SummaryWorkflows[hashBytes(caller)] = binding
	for _, aux := range [][]byte{nil, []byte("stale auxiliary bytes")} {
		files[hostedPath] = aux
		if !refreshed.supports(files) {
			t.Fatal("Hosted auxiliary changed base readiness")
		}
	}
	files[summaryPath] = append(append([]byte(nil), caller...), ' ')
	if refreshed.supports(files) {
		t.Fatal("one-byte final caller drift was accepted")
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
					delete(f.managed, requestFormPath)
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
