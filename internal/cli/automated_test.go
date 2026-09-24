package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestReviewRequiresExplicitSupportedAgentBeforeSideEffects(t *testing.T) {
	f := fixture()
	c := f.automatedCommand(t)
	for _, args := range [][]string{nil, {"--agent", "/bin/sh"}, {"--agent", "unknown"}} {
		if err := c.execute(context.Background(), args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("command ran before Agent validation: %v", f.calls)
	}
	for name, adapter := range agentCommands {
		if name == "" || adapter.program == "" || len(adapter.arguments) == 0 {
			t.Fatalf("invalid adapter: %s: %+v", name, adapter)
		}
	}
}

func TestDirtyStationStopsBeforeAdmissionAndAgent(t *testing.T) {
	f := fixture()
	f.dirty = true
	f.addIssue(1, "ordinary Issue", f.requester.Owner)
	c := f.automatedCommand(t)
	if err := c.execute(context.Background(), []string{"--agent", "codex"}); err == nil || !strings.Contains(err.Error(), "clean Station") {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.state(1) != "open" || len(f.calls) != 1 {
		t.Fatalf("dirty Station caused review side effects: %v", f.calls)
	}
}

func TestAgentChangingStationCannotPublishResult(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.agentDirties = true
	f.agentOutput = `[{"issue":1,"verdict":"PASS","comment":"Evidence."}]`
	c := f.automatedCommand(t)
	if err := c.execute(context.Background(), []string{"--agent", "codex"}); err == nil || !strings.Contains(err.Error(), "Station changed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.state(1) != "open" || len(f.bodies(1)) != 1 || f.starred {
		t.Fatalf("dirty Agent published result: %v", f.calls)
	}
}

func TestAutomatedReviewPartialResultsAndRetry(t *testing.T) {
	f := fixture()
	f.addIssue(9, reviewBody("project"), f.requester.Owner)
	f.addIssue(10, "ordinary Issue", f.requester.Owner)
	c := f.automatedCommand(t)
	f.agentOutput = `[{"issue":9,"verdict":"PASS","comment":"Specific evidence."},{"issue":10,"verdict":"PASS","comment":"Wrong thread."}]`
	if err := c.execute(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	if f.state(9) != "closed" || f.state(10) != "closed" || !f.starred {
		t.Fatalf("unexpected state: %v", f.issues)
	}
	if len(f.bodies(9)) != 2 || !strings.Contains(f.bodies(9)[1], "Specific evidence.") || !strings.Contains(f.bodies(9)[1], `"targetCommit":"`+testSHA1+`"`) {
		t.Fatalf("missing protocol result: %v", f.bodies(9))
	}
	if !strings.Contains(strings.Join(f.bodies(10), " "), "INVALID_REQUEST") {
		t.Fatalf("invalid request admitted: %v", f.bodies(10))
	}
	if _, err := os.Stat(f.resultFile); !os.IsNotExist(err) {
		t.Fatalf("runtime result remained: %v", err)
	}
	var star, result, close int
	for i, call := range f.calls {
		if strings.Contains(call, "--method PUT user/starred/alice/project") {
			star = i
		}
		if strings.Contains(call, "--method POST repos/reviewer/shoal-station/issues/9/comments") && strings.Contains(call, "shoal-review-event:v1") {
			result = i
		}
		if strings.Contains(call, "--method PATCH repos/reviewer/shoal-station/issues/9 ") {
			close = i
		}
	}
	if star >= result || result >= close {
		t.Fatalf("side-effect order: %d %d %d: %v", star, result, close, f.calls)
	}
}

func TestPartialBatchLeavesMissingResultOpenAndRetries(t *testing.T) {
	f := fixture()
	// Different targets need separate stable IDs to be independent requests;
	// fake admission below is pre-seeded to test the batch processor directly.
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.agentOutput = `[{"issue":1,"verdict":"FAIL","comment":"Evidence."}]`
	c := f.automatedCommand(t)
	if err := c.execute(context.Background(), []string{"--agent", "claude"}); err != nil {
		t.Fatal(err)
	}
	if f.starred || f.state(1) != "closed" {
		t.Fatalf("FAIL did not converge: %v", f.calls)
	}
	if !strings.Contains(strings.Join(f.bodies(1), ""), `"actualStarState":false`) {
		t.Fatal("FAIL result missing actual state")
	}
}

func TestFIFOFivePerProcessAndPartialBatch(t *testing.T) {
	f := fixture()
	for n := 7; n >= 1; n-- {
		f.addIssue(n, reviewBody(fmt.Sprintf("project-%d", n)), f.requester.Owner)
	}
	c := f.automatedCommand(t)
	f.agentOutputs = []string{
		`[{"issue":1,"verdict":"PASS","comment":"One"},{"issue":2,"verdict":"PASS","comment":"Two"},{"issue":3,"verdict":"PASS","comment":"Three"},{"issue":4,"verdict":"PASS","comment":"Four"},{"issue":"5","verdict":"PASS","comment":"Invalid identity type"}]`,
		`[{"issue":6,"verdict":"PASS","comment":"Six"},{"issue":7,"verdict":"FAIL","comment":"Seven"}]`,
	}
	err := c.execute(context.Background(), []string{"--agent", "codex"})
	if err == nil || !strings.Contains(err.Error(), "Issue #5") {
		t.Fatalf("missing partial result failure: %v", err)
	}
	if f.agentCalls != 2 || f.state(5) != "open" {
		t.Fatalf("batching failed: calls %d; issues %v", f.agentCalls, f.issues)
	}
	for _, n := range []int{1, 2, 3, 4, 6, 7} {
		if f.state(n) != "closed" {
			t.Fatalf("Issue #%d not completed", n)
		}
	}
	var prompts []string
	for _, call := range f.calls {
		if strings.HasPrefix(call, "codex exec ") {
			prompts = append(prompts, call)
		}
	}
	if len(prompts) != 2 || strings.Contains(prompts[0], "/issues/6") || !strings.Contains(prompts[0], "/issues/1") || !strings.Contains(prompts[0], "/issues/5") || !strings.Contains(prompts[1], "/issues/6") || !strings.Contains(prompts[1], "/issues/7") {
		t.Fatalf("wrong FIFO batching: %v", prompts)
	}
}

func TestFailedStarAndAgentLeaveThreadRetryable(t *testing.T) {
	for _, failure := range []string{"--method PUT user/starred/alice/project", "codex exec"} {
		f := fixture()
		f.addIssue(1, reviewBody("project"), f.requester.Owner)
		f.agentOutput = `[{"issue":1,"verdict":"PASS","comment":"Specific evidence."}]`
		f.failOn = failure
		c := f.automatedCommand(t)
		if err := c.execute(context.Background(), []string{"--agent", "codex"}); err == nil {
			t.Fatalf("missing failure for %q", failure)
		}
		if f.state(1) != "open" || len(f.bodies(1)) != 1 {
			t.Fatalf("failure completed thread %q: %v", failure, f.bodies(1))
		}
		f.failOn = ""
		if err := c.execute(context.Background(), []string{"--agent", "codex"}); err != nil {
			t.Fatalf("retry failed: %v", err)
		}
		if f.state(1) != "closed" {
			t.Fatal("retry did not close thread")
		}
	}
}

func TestAgentResultRejectsDuplicateAndForeignIssue(t *testing.T) {
	f := fixture()
	c := f.automatedCommand(t)
	f.agentOutput = `[{"issue":1,"verdict":"PASS","comment":"A"},{"issue":1,"verdict":"FAIL","comment":"B"},{"issue":2,"verdict":"PASS","comment":"Foreign"}]`
	batch := []pendingReview{{issue: reviewIssue{Number: 1}}}
	results, err := c.runAgent(context.Background(), "grok", f.node, batch)
	if err != nil || len(results) != 0 {
		t.Fatalf("accepted adversarial batch: %v %v", results, err)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "grok -p ") {
		t.Fatal("wrong Grok invocation")
	}
}

func TestAllAgentAdaptersBuildInvocation(t *testing.T) {
	for name, adapter := range agentCommands {
		t.Run(name, func(t *testing.T) {
			f := fixture()
			c := f.automatedCommand(t)
			f.agentOutput = `[ { "issue": 1, "verdict": "PASS", "comment": "A" } ]`
			_, err := c.runAgent(context.Background(), name, f.node, []pendingReview{{issue: reviewIssue{Number: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(f.calls[0], fmt.Sprintf("%s %s ", adapter.program, strings.Join(adapter.arguments, " "))) {
				t.Fatalf("wrong adapter call: %s", f.calls[0])
			}
		})
	}
}

func TestMissingResultDoesNotUseAgentTerminalOutput(t *testing.T) {
	f := fixture()
	c := f.automatedCommand(t)
	f.resultFile = "" // fake process exits successfully but writes nothing.
	_, err := c.runAgent(context.Background(), "codex", f.node, []pendingReview{{issue: reviewIssue{Number: 1}}})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing JSON file, got %v", err)
	}
}
