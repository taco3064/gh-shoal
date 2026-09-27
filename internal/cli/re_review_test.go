package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type reReviewFake struct {
	*fakeReview
	targets map[int64]reviewRepository
	heads   map[int64]string
	stars   map[string]bool
}

func newReReviewFake() *reReviewFake {
	base := fixture()
	return &reReviewFake{
		fakeReview: base,
		targets:    map[int64]reviewRepository{base.target.ID: base.target},
		heads:      map[int64]string{base.target.ID: base.head},
		stars:      map[string]bool{base.target.FullName: base.starred},
	}
}

func (f *reReviewFake) run(ctx context.Context, program string, args ...string) ([]byte, error) {
	command := program + " " + strings.Join(args, " ")
	if f.failOn != "" && strings.Contains(command, f.failOn) {
		return nil, fmt.Errorf("injected failure")
	}
	if program == "gh" && len(args) >= 2 && args[0] == "api" {
		if args[1] == "--method" && len(args) >= 4 && strings.HasPrefix(args[3], "user/starred/") {
			fullName := strings.TrimPrefix(args[3], "user/starred/")
			f.calls = append(f.calls, command)
			f.stars[fullName] = args[2] == "PUT"
			if fullName == f.target.FullName {
				f.starred = f.stars[fullName]
			}
			return nil, nil
		}
		endpoint := args[1]
		if strings.HasPrefix(endpoint, "repositories/") {
			id, err := strconv.ParseInt(strings.TrimPrefix(endpoint, "repositories/"), 10, 64)
			if err != nil {
				return nil, err
			}
			target, ok := f.targets[id]
			if !ok {
				return nil, fmt.Errorf("HTTP 404: Not Found")
			}
			f.calls = append(f.calls, command)
			return json.Marshal(target)
		}
		if strings.HasPrefix(endpoint, "user/starred/") {
			fullName := strings.TrimPrefix(endpoint, "user/starred/")
			f.calls = append(f.calls, command)
			if f.stars[fullName] {
				return nil, nil
			}
			return nil, fmt.Errorf("HTTP 404: Not Found")
		}
		for id, target := range f.targets {
			prefix := "repos/" + target.FullName + "/branches/"
			if strings.HasPrefix(endpoint, prefix) {
				f.calls = append(f.calls, command)
				return json.Marshal(map[string]any{"commit": map[string]any{"sha": f.heads[id]}})
			}
		}
	}
	return f.fakeReview.run(ctx, program, args...)
}

func (f *reReviewFake) command(t *testing.T) reviewCommand {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".shoal"), 0700); err != nil {
		t.Fatal(err)
	}
	f.resultFile = filepath.Join(dir, ".shoal", "review-results.json")
	return reviewCommand{run: f.run, dir: dir, protocol: protocolFixture(t), commentsCache: map[int][]reviewComment{}}
}

func priorJudgment(t *testing.T, target reviewRepository, verdict string) reviewComment {
	starred := verdict == "PASS"
	return reviewComment{
		ID:   11,
		User: reviewUser{ID: 1, Login: "reviewer", Type: "User"},
		Body: encodeRecord(protocolFixture(t).Event.Marker, reviewEvent{
			Type:                     "REVIEWED",
			ReviewerNodeID:           11,
			TargetRepositoryID:       target.ID,
			TargetRepositoryFullName: target.FullName,
			TargetDefaultBranch:      target.DefaultBranch,
			TargetCommit:             testSHA1,
			ReviewPolicyPath:         "README.md",
			ReviewPolicyCommit:       testSHA1,
			Verdict:                  verdict,
			ActualStarState:          &starred,
			ReviewedAt:               "2026-09-24T00:00:00Z",
		}),
	}
}

func pendingReReview(t *testing.T, target reviewRepository, requestIssue int) reviewComment {
	return reviewComment{
		ID:   12,
		User: reviewUser{ID: 1, Login: "reviewer", Type: "User"},
		Body: encodeRecord(protocolFixture(t).Event.Marker, reviewEvent{
			Type:                    "RE_REVIEW_REQUESTED",
			ReviewerNodeID:          11,
			TargetRepositoryID:      target.ID,
			RequestIssueNumber:      requestIssue,
			EligibilityTargetCommit: testSHA2,
			ReviewPolicyCommit:      testSHA1,
			Reason:                  "TARGET_CHANGED",
		}),
	}
}

func admissionFor(t *testing.T, target reviewRepository, name string) reviewComment {
	return reviewComment{ID: 10, User: reviewUser{ID: 1, Login: "reviewer", Type: "User"}, Body: encodeRecord(protocolFixture(t).Admission.Marker, admissionRecord{ReviewerNodeID: 11, TargetRepositoryID: target.ID, RepositoryName: name})}
}

func TestReReviewRequiresExplicitSupportedAgentBeforeSideEffects(t *testing.T) {
	f := newReReviewFake()
	c := f.command(t)
	for _, args := range [][]string{nil, {"--agent", "unknown"}, {"codex"}} {
		if err := c.executeReReview(context.Background(), args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("command caused side effects before Agent validation: %v", f.calls)
	}
}

func TestReReviewClosedThreadChangedBasisUsesSharedPipeline(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.issues[0].State = "closed"
	f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), priorJudgment(t, f.target, "PASS")}
	f.heads[f.target.ID] = testSHA2
	f.agentOutput = `[{"issue":1,"verdict":"PASS","comment":"Fresh evidence."}]`
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	bodies := strings.Join(f.bodies(1), "\n")
	if !strings.Contains(bodies, `"type":"RE_REVIEWED"`) || !strings.Contains(bodies, `"targetCommit":"`+testSHA2+`"`) || !strings.Contains(bodies, "Fresh evidence.") {
		t.Fatalf("missing Re-review Judgment: %s", bodies)
	}
	if f.state(1) != "closed" || !f.stars[f.target.FullName] {
		t.Fatalf("completion did not converge: state=%s stars=%v", f.state(1), f.stars)
	}
	calls := strings.Join(f.calls, "\n")
	if !strings.Contains(calls, "Target: https://github.com/alice/project") {
		t.Fatalf("Agent did not receive deterministic Target identity: %s", calls)
	}
}

func TestReReviewNoNewBasisRepairsDriftWithoutAgent(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.issues[0].State = "closed"
	f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), priorJudgment(t, f.target, "PASS")}
	f.stars[f.target.FullName] = false
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	if f.agentCalls != 0 || !f.stars[f.target.FullName] || len(f.bodies(1)) != 2 {
		t.Fatalf("drift repair invoked semantic Review or wrote a Judgment: agent=%d stars=%v bodies=%v", f.agentCalls, f.stars, f.bodies(1))
	}
}

func TestReReviewBasisReversionClosesPendingThreadWithoutJudgment(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), priorJudgment(t, f.target, "PASS"), pendingReReview(t, f.target, 2)}
	f.stars[f.target.FullName] = true
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	if f.agentCalls != 0 || f.state(1) != "closed" || len(f.bodies(1)) != 3 {
		t.Fatalf("basis reversion did not return thread to completed state: agent=%d state=%s bodies=%v", f.agentCalls, f.state(1), f.bodies(1))
	}
}

func TestReReviewDoesNotFabricateMissingHistoricalJudgment(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.issues[0].State = "closed"
	f.comments[1] = []reviewComment{admissionFor(t, f.target, "project")}
	f.stars[f.target.FullName] = true
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	if f.agentCalls != 0 || len(f.bodies(1)) != 1 || !f.stars[f.target.FullName] {
		t.Fatalf("missing history was reconstructed or mutated: calls=%v bodies=%v", f.calls, f.bodies(1))
	}
}

func TestReReviewUsesStableTargetIDAfterRename(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.issues[0].State = "closed"
	f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), priorJudgment(t, f.target, "PASS")}
	renamed := f.target
	renamed.FullName = "alice/renamed"
	renamed.Name = "renamed"
	f.targets[renamed.ID] = renamed
	f.heads[renamed.ID] = testSHA2
	f.stars[renamed.FullName] = true
	f.agentOutput = `[{"issue":1,"verdict":"FAIL","comment":"Changed repository."}]`
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(f.calls, "\n")
	if !strings.Contains(calls, "repositories/55") || !strings.Contains(calls, "Target: https://github.com/alice/renamed") {
		t.Fatalf("stable identity was not used after rename: %s", calls)
	}
	bodies := strings.Join(f.bodies(1), "\n")
	if f.stars[renamed.FullName] || !strings.Contains(bodies, `"targetRepositoryFullName":"alice/renamed"`) || !strings.Contains(bodies, `"type":"STAR_REVOKED"`) {
		t.Fatalf("renamed Target did not converge through current identity: stars=%v bodies=%v", f.stars, f.bodies(1))
	}
}

func TestReReviewBatchesFiveAndIsolatesMissingResult(t *testing.T) {
	f := newReReviewFake()
	for n := 1; n <= 6; n++ {
		target := f.target
		target.ID = int64(100 + n)
		target.Name = fmt.Sprintf("project-%d", n)
		target.FullName = "alice/" + target.Name
		f.targets[target.ID] = target
		f.heads[target.ID] = testSHA2
		f.stars[target.FullName] = true
		f.addIssue(n, reviewBody(target.Name), f.requester.Owner)
		f.comments[n] = []reviewComment{admissionFor(t, target, target.Name), priorJudgment(t, target, "PASS"), pendingReReview(t, target, 1000+n)}
	}
	f.agentOutputs = []string{
		`[{"issue":1,"verdict":"PASS","comment":"1"},{"issue":2,"verdict":"PASS","comment":"2"},{"issue":3,"verdict":"PASS","comment":"3"},{"issue":4,"verdict":"PASS","comment":"4"}]`,
		`[{"issue":6,"verdict":"FAIL","comment":"6"}]`,
	}
	err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"})
	if err == nil || !strings.Contains(err.Error(), "Issue #5") {
		t.Fatalf("missing partial result was not reported: %v", err)
	}
	if f.agentCalls != 2 || f.state(5) != "open" {
		t.Fatalf("wrong batching/retry state: calls=%d issue5=%s", f.agentCalls, f.state(5))
	}
	for _, n := range []int{1, 2, 3, 4} {
		if f.state(n) != "closed" || !strings.Contains(strings.Join(f.bodies(n), "\n"), `"type":"RE_REVIEWED"`) {
			t.Fatalf("independent Issue #%d did not complete: state=%s bodies=%v", n, f.state(n), f.bodies(n))
		}
	}
	if f.state(6) != "closed" || !strings.Contains(strings.Join(f.bodies(6), "\n"), `"type":"STAR_REVOKED"`) {
		t.Fatalf("FAIL Re-review did not record STAR_REVOKED: state=%s bodies=%v", f.state(6), f.bodies(6))
	}
}

func TestReReviewIgnoresOlderInvalidFormalCommentWhenNewerJudgmentIsUsable(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.issues[0].State = "closed"
	invalid := reviewComment{ID: 9, User: f.node.Owner, Body: encodeRecord(protocolFixture(t).Event.Marker, reviewEvent{Type: "REVIEWED", ReviewerNodeID: 11, TargetRepositoryID: 55, TargetCommit: testSHA1})}
	f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), invalid, priorJudgment(t, f.target, "PASS")}
	f.heads[f.target.ID] = testSHA2
	f.agentOutput = `[{"issue":1,"verdict":"PASS","comment":"Still good."}]`
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.bodies(1), "\n"), `"type":"RE_REVIEWED"`) {
		t.Fatalf("older invalid comment poisoned usable history: %v", f.bodies(1))
	}
}

func TestReReviewIgnoresInconsistentManualJudgment(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.issues[0].State = "closed"
	inconsistent := priorJudgment(t, f.target, "PASS")
	wrongState := false
	inconsistent.Body = encodeRecord(protocolFixture(t).Event.Marker, reviewEvent{
		Type:                     "REVIEWED",
		ReviewerNodeID:           11,
		TargetRepositoryID:       f.target.ID,
		TargetRepositoryFullName: f.target.FullName,
		TargetDefaultBranch:      f.target.DefaultBranch,
		TargetCommit:             testSHA1,
		ReviewPolicyPath:         "README.md",
		ReviewPolicyCommit:       testSHA1,
		Verdict:                  "PASS",
		ActualStarState:          &wrongState,
		ReviewedAt:               "2026-09-24T00:00:00Z",
	})
	f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), inconsistent}
	f.heads[f.target.ID] = testSHA2
	f.stars[f.target.FullName] = true
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	if f.agentCalls != 0 || len(f.bodies(1)) != 2 || !f.stars[f.target.FullName] {
		t.Fatalf("inconsistent manual Judgment became maintenance authority: calls=%v bodies=%v", f.calls, f.bodies(1))
	}
}

func TestReviewBasisClassification(t *testing.T) {
	previous := reviewEvent{TargetCommit: testSHA1, ReviewPolicyCommit: testSHA1}
	for _, tc := range []struct {
		name, target, policy, want string
	}{
		{"target only", testSHA2, testSHA1, "TARGET_CHANGED"},
		{"policy only", testSHA1, testSHA2, "POLICY_CHANGED"},
		{"both", testSHA2, testSHA2, "TARGET_AND_POLICY_CHANGED"},
		{"neither", testSHA1, testSHA1, "NO_NEW_REVIEW_BASIS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyReviewBasis(previous, tc.target, tc.policy); got != tc.want {
				t.Fatalf("classification = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestReReviewRequesterPendingUsesMaintenancePath(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), priorJudgment(t, f.target, "PASS"), pendingReReview(t, f.target, 2)}
	f.heads[f.target.ID] = testSHA2
	f.agentOutput = `[{"issue":1,"verdict":"PASS","comment":"Pending lifecycle reviewed."}]`
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	bodies := f.bodies(1)
	if f.agentCalls != 1 || f.state(1) != "closed" || len(bodies) != 4 || !strings.Contains(bodies[3], `"type":"RE_REVIEWED"`) {
		t.Fatalf("pending lifecycle did not converge through Re-review: agent=%d state=%s bodies=%v", f.agentCalls, f.state(1), bodies)
	}
	admissions := 0
	for _, body := range bodies {
		if strings.HasPrefix(body, protocolFixture(t).Admission.Marker+"\n") {
			admissions++
		}
	}
	if admissions != 1 {
		t.Fatalf("Re-review re-ran Initial Review admission: %v", bodies)
	}
}

func TestReReviewOpenThreadWithoutPendingDoesNotStartNewSemanticReview(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), priorJudgment(t, f.target, "PASS")}
	f.heads[f.target.ID] = testSHA2
	f.stars[f.target.FullName] = true
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	if f.agentCalls != 0 || f.state(1) != "closed" || len(f.bodies(1)) != 2 {
		t.Fatalf("ordinary open thread incorrectly became semantic Re-review: agent=%d state=%s bodies=%v", f.agentCalls, f.state(1), f.bodies(1))
	}
}

func TestReReviewStarMutationFailureLeavesPendingThreadRetryable(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), priorJudgment(t, f.target, "FAIL"), pendingReReview(t, f.target, 2)}
	f.heads[f.target.ID] = testSHA2
	f.agentOutput = `[{"issue":1,"verdict":"PASS","comment":"Now passes."}]`
	f.failOn = "--method PUT user/starred/alice/project"
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err == nil {
		t.Fatal("expected Star mutation failure")
	}
	if f.state(1) != "open" || len(f.bodies(1)) != 3 {
		t.Fatalf("failed side effect published completion: state=%s bodies=%v", f.state(1), f.bodies(1))
	}
	f.failOn = ""
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	if f.state(1) != "closed" || !f.stars[f.target.FullName] || !strings.Contains(strings.Join(f.bodies(1), "\n"), `"type":"RE_REVIEWED"`) {
		t.Fatalf("retry did not converge: state=%s stars=%v bodies=%v", f.state(1), f.stars, f.bodies(1))
	}
}

func judgmentComment(t *testing.T, target reviewRepository, eventType, verdict string, targetCommit, policyCommit string) reviewComment {
	starred := verdict == "PASS"
	return reviewComment{
		ID:   20,
		User: reviewUser{ID: 1, Login: "reviewer", Type: "User"},
		Body: encodeRecord(protocolFixture(t).Event.Marker, reviewEvent{
			Type:                     eventType,
			ReviewerNodeID:           11,
			TargetRepositoryID:       target.ID,
			TargetRepositoryFullName: target.FullName,
			TargetDefaultBranch:      target.DefaultBranch,
			TargetCommit:             targetCommit,
			ReviewPolicyPath:         "README.md",
			ReviewPolicyCommit:       policyCommit,
			Verdict:                  verdict,
			ActualStarState:          &starred,
			ReviewedAt:               "2026-09-27T05:30:00Z",
		}),
	}
}

func TestReReviewFailEmitsStarRevoked(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.issues[0].State = "closed"
	f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), priorJudgment(t, f.target, "PASS")}
	f.heads[f.target.ID] = testSHA2
	f.stars[f.target.FullName] = true
	f.agentOutput = `[{"issue":1,"verdict":"FAIL","comment":"No longer meets policy."}]`
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	bodies := strings.Join(f.bodies(1), "\n")
	if !strings.Contains(bodies, `"type":"STAR_REVOKED"`) || strings.Contains(bodies, `"type":"RE_REVIEWED","reviewerNodeId":11,"targetRepositoryId":55`) {
		t.Fatalf("FAIL Re-review used wrong Judgment type: %s", bodies)
	}
	if f.stars[f.target.FullName] {
		t.Fatal("FAIL Re-review did not remove Star")
	}
}

func TestReReviewLatestStarRevokedRemainsAuthoritative(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.issues[0].State = "closed"
	f.comments[1] = []reviewComment{
		admissionFor(t, f.target, "project"),
		priorJudgment(t, f.target, "PASS"),
		judgmentComment(t, f.target, "STAR_REVOKED", "FAIL", testSHA2, testSHA1),
	}
	f.heads[f.target.ID] = testSHA2
	f.policy = testSHA1
	f.stars[f.target.FullName] = false
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	if f.agentCalls != 0 || f.stars[f.target.FullName] {
		t.Fatalf("latest STAR_REVOKED was not authoritative: agent=%d stars=%v calls=%v", f.agentCalls, f.stars, f.calls)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "--method PUT user/starred/alice/project") {
			t.Fatalf("older PASS incorrectly restored Star: %v", f.calls)
		}
	}
}

func TestReReviewLatestRevokedExternallyIsUsableJudgment(t *testing.T) {
	f := newReReviewFake()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.issues[0].State = "closed"
	f.comments[1] = []reviewComment{
		admissionFor(t, f.target, "project"),
		priorJudgment(t, f.target, "PASS"),
		judgmentComment(t, f.target, "REVOKED_EXTERNALLY", "FAIL", testSHA2, testSHA1),
	}
	f.heads[f.target.ID] = testSHA2
	f.policy = testSHA1
	f.stars[f.target.FullName] = false
	if err := f.command(t).executeReReview(context.Background(), []string{"--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	if f.agentCalls != 0 || f.stars[f.target.FullName] {
		t.Fatalf("latest REVOKED_EXTERNALLY was not consumed as usable Judgment: agent=%d stars=%v", f.agentCalls, f.stars)
	}
}
