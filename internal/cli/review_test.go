package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const testSHA1 = "1111111111111111111111111111111111111111"
const testSHA2 = "2222222222222222222222222222222222222222"

func reviewBody(name string) string {
	return "### Repository name\n\n" + name + "\n\n### Invitation message\n\n_No response_\n"
}
func protocolFixture(t *testing.T) reviewContract {
	t.Helper()
	p, e := loadReviewContract()
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestReviewRequestParsing(t *testing.T) {
	p := protocolFixture(t)
	for _, body := range []string{"ordinary issue", "### Repository name\n\n", reviewBody("other/project"), reviewBody("https://github.com/a/b"), reviewBody("../bad"), reviewBody("my-repo") + "\n### Extra\nvalue", reviewBody("my-repo") + "\n### Repository name\nother"} {
		if _, err := parseRequest(body, p); err == nil {
			t.Errorf("accepted invalid request %q", body)
		}
	}
	for _, body := range []string{reviewBody("my-repo"), "### Repository name\r\n\r\nmy-repo\r\n"} {
		if got, err := parseRequest(body, p); err != nil || got != "my-repo" {
			t.Errorf("valid request: %q, %v", got, err)
		}
	}
}

type fakeReview struct {
	node         reviewRepository
	requester    reviewRepository
	target       reviewRepository
	issues       []reviewIssue
	comments     map[int][]reviewComment
	head, policy string
	calls        []string
	failOn       string
}

func fixture() *fakeReview {
	node := reviewRepository{ID: 11, FullName: "reviewer/shoal-station", DefaultBranch: "main", Fork: true, Owner: reviewUser{ID: 1, Login: "reviewer", Type: "User"}, Parent: &struct {
		ID int64 `json:"id"`
	}{ID: rootID}}
	requester := reviewRepository{ID: 12, FullName: "alice/shoal-station", Fork: true, Owner: reviewUser{ID: 2, Login: "alice", Type: "User"}, Parent: &struct {
		ID int64 `json:"id"`
	}{ID: rootID}}
	target := reviewRepository{ID: 55, FullName: "alice/project", Name: "project", DefaultBranch: "main", Owner: requester.Owner}
	return &fakeReview{node: node, requester: requester, target: target, head: testSHA1, policy: testSHA1, comments: map[int][]reviewComment{}}
}
func (f *fakeReview) addIssue(number int, body string, user reviewUser) {
	f.issues = append(f.issues, reviewIssue{Number: number, State: "open", Body: body, User: user})
}
func (f *fakeReview) run(_ context.Context, program string, args ...string) ([]byte, error) {
	command := program + " " + strings.Join(args, " ")
	f.calls = append(f.calls, command)
	if f.failOn != "" && strings.Contains(command, f.failOn) {
		return nil, errors.New("injected failure")
	}
	if program == "git" {
		if strings.HasSuffix(command, " remote") {
			return []byte("origin\n"), nil
		}
		if strings.HasSuffix(command, "remote get-url origin") {
			return []byte("https://github.com/reviewer/shoal-station.git\n"), nil
		}
	}
	if program != "gh" {
		return nil, fmt.Errorf("unexpected command %s", command)
	}
	if reflect.DeepEqual(args, []string{"auth", "status"}) {
		return []byte("ok"), nil
	}
	if len(args) < 2 || args[0] != "api" {
		return nil, fmt.Errorf("unexpected gh command %s", command)
	}
	if args[1] == "--method" {
		endpoint := args[3]
		field := args[5]
		var number int
		if _, e := fmt.Sscanf(endpoint, "repos/reviewer/shoal-station/issues/%d", &number); e != nil {
			return nil, e
		}
		if strings.HasSuffix(endpoint, "/comments") {
			if _, e := fmt.Sscanf(endpoint, "repos/reviewer/shoal-station/issues/%d/comments", &number); e != nil {
				return nil, e
			}
			f.comments[number] = append(f.comments[number], reviewComment{ID: int64(100 + len(f.calls)), Body: strings.TrimPrefix(field, "body="), User: f.node.Owner})
		} else {
			for i := range f.issues {
				if f.issues[i].Number == number {
					f.issues[i].State = strings.TrimPrefix(field, "state=")
				}
			}
		}
		return []byte(`{}`), nil
	}
	paged := args[1] == "--paginate"
	endpoint := args[1]
	if paged {
		endpoint = args[3]
	}
	var value any
	switch {
	case endpoint == "user":
		value = f.node.Owner
	case endpoint == "repos/reviewer/shoal-station":
		value = f.node
	case strings.HasPrefix(endpoint, "repos/reviewer/shoal-station/issues?"):
		value = [][]reviewIssue{f.issues}
	case strings.HasPrefix(endpoint, "repos/reviewer/shoal-station/issues/") && strings.Contains(endpoint, "/comments?"):
		var n int
		_, _ = fmt.Sscanf(endpoint, "repos/reviewer/shoal-station/issues/%d", &n)
		value = [][]reviewComment{f.comments[n]}
	case strings.HasPrefix(endpoint, "users/bob/repos?"):
		value = [][]reviewRepository{{{ID: 13, FullName: "bob/shoal-station", Fork: true, Owner: reviewUser{ID: 3, Login: "bob", Type: "User"}, Parent: &struct {
			ID int64 `json:"id"`
		}{ID: rootID}}}}
	case endpoint == "repos/bob/shoal-station":
		value = reviewRepository{ID: 13, FullName: "bob/shoal-station", Fork: true, Owner: reviewUser{ID: 3, Login: "bob", Type: "User"}, Parent: &struct {
			ID int64 `json:"id"`
		}{ID: rootID}}
	case strings.HasPrefix(endpoint, "users/reviewer/repos?"):
		value = [][]reviewRepository{{f.node}}
	case strings.HasPrefix(endpoint, "users/alice/repos?"):
		value = [][]reviewRepository{{f.requester}}
	case endpoint == "repos/alice/shoal-station":
		value = f.requester
	case endpoint == "repos/alice/other":
		other := f.target
		other.ID = 56
		other.FullName = "alice/other"
		other.Name = "other"
		value = other
	case endpoint == "repos/alice/missing":
		return nil, errors.New("HTTP 404: Not Found")
	case endpoint == "repos/reviewer/project":
		own := f.target
		own.Owner = f.node.Owner
		value = own
	case endpoint == "repos/bob/project":
		value = f.target
	case endpoint == "repos/alice/project" || endpoint == "repos/alice/renamed":
		value = f.target
	case strings.HasPrefix(endpoint, "repos/bob/project/branches/") || strings.HasPrefix(endpoint, "repos/alice/project/branches/") || strings.HasPrefix(endpoint, "repos/alice/renamed/branches/"):
		value = map[string]any{"commit": map[string]any{"sha": f.head}}
	case strings.HasPrefix(endpoint, "repos/reviewer/shoal-station/commits?"):
		value = []map[string]string{{"sha": f.policy}}
	default:
		return nil, fmt.Errorf("unexpected GitHub endpoint: %s", endpoint)
	}
	return json.Marshal(value)
}
func (f *fakeReview) command(t *testing.T) reviewCommand {
	t.Helper()
	return reviewCommand{run: f.run, dir: ".", protocol: protocolFixture(t), commentsCache: map[int][]reviewComment{}}
}
func (f *fakeReview) process(t *testing.T) {
	t.Helper()
	if err := f.command(t).execute(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}
func (f *fakeReview) bodies(n int) []string {
	var b []string
	for _, c := range f.comments[n] {
		b = append(b, c.Body)
	}
	return b
}
func (f *fakeReview) state(n int) string {
	for _, i := range f.issues {
		if i.Number == n {
			return i.State
		}
	}
	return ""
}

func TestReviewScansAndRejectsInvalidWithoutSemanticSideEffects(t *testing.T) {
	f := fixture()
	f.addIssue(1, "General discussion", f.requester.Owner)
	f.addIssue(2, reviewBody("project"), f.requester.Owner)
	f.addIssue(3, reviewBody("project"), f.node.Owner)
	f.process(t)
	if f.state(1) != "closed" || !strings.Contains(strings.Join(f.bodies(1), ""), "INVALID_REQUEST") {
		t.Fatal("nonconforming Issue was ignored")
	}
	if f.state(2) != "open" || !strings.Contains(strings.Join(f.bodies(2), ""), protocolFixture(t).Admission.Marker) {
		t.Fatal("first request did not become pending canonical thread")
	}
	if f.state(3) != "closed" {
		t.Fatal("self review not closed")
	}
	for _, c := range f.calls {
		if strings.Contains(c, "starred") || strings.Contains(c, "agent") {
			t.Fatal("admission performed semantic side effect")
		}
	}
	f.process(t)
	if len(f.bodies(2)) != 1 {
		t.Fatalf("pending thread was not idempotent: %v", f.bodies(2))
	}
}
func TestMembershipRequiresDirectPersonalForkAndAuthor(t *testing.T) {
	cases := []struct {
		name  string
		alter func(*fakeReview)
	}{
		{"no fork", func(f *fakeReview) { f.requester.Fork = false }},
		{"root itself", func(f *fakeReview) { f.requester.ID = rootID }},
		{"organization", func(f *fakeReview) { f.requester.Owner.Type = "Organization" }},
		{"downstream source root", func(f *fakeReview) { f.requester.Parent.ID = 99 }},
		{"different owner", func(f *fakeReview) { f.requester.Owner.ID = 3 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture()
			tc.alter(f)
			f.addIssue(1, reviewBody("project"), reviewUser{ID: 2, Login: "alice", Type: "User"})
			f.process(t)
			if f.state(1) != "closed" || len(f.bodies(1)) != 1 || !strings.Contains(f.bodies(1)[0], "INVALID_REQUEST") {
				t.Fatalf("invalid membership admitted: %+v", f)
			}
		})
	}
}
func TestDuplicateAndReReviewBasis(t *testing.T) {
	for _, tc := range []struct{ name, head, policy, reason string }{{"duplicate", testSHA1, testSHA1, "NO_NEW_REVIEW_BASIS"}, {"target", testSHA2, testSHA1, "TARGET_CHANGED"}, {"policy", testSHA1, testSHA2, "POLICY_CHANGED"}, {"both", testSHA2, testSHA2, "TARGET_AND_POLICY_CHANGED"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture()
			f.addIssue(1, reviewBody("project"), f.requester.Owner)
			f.comments[1] = []reviewComment{{ID: 10, Body: encodeRecord(protocolFixture(t).Admission.Marker, admissionRecord{11, 55, "project"}), User: f.node.Owner}, {ID: 11, Body: encodeRecord("shoal-review-event:v1", reviewEvent{Type: "REVIEWED", ReviewerNodeID: 11, TargetRepositoryID: 55, TargetRepositoryFullName: "alice/project", TargetDefaultBranch: "main", TargetCommit: testSHA1, ReviewPolicyPath: "README.md", ReviewPolicyCommit: testSHA1, Verdict: "PASS", ActualStarState: boolPtr(true), ReviewedAt: "2026-09-24T00:00:00Z"}), User: f.node.Owner}}
			f.issues[0].State = "closed"
			f.addIssue(2, reviewBody("project"), f.requester.Owner)
			f.head = tc.head
			f.policy = tc.policy
			f.process(t)
			if f.state(2) != "closed" || !strings.Contains(strings.Join(f.bodies(2), ""), tc.reason) && tc.reason == "NO_NEW_REVIEW_BASIS" {
				t.Fatalf("trigger not resolved: %v", f.bodies(2))
			}
			if tc.reason == "NO_NEW_REVIEW_BASIS" {
				if f.state(1) != "closed" || len(f.bodies(1)) != 2 {
					t.Fatalf("canonical thread mutated: %v", f.bodies(1))
				}
			}
			if tc.reason != "NO_NEW_REVIEW_BASIS" {
				if f.state(1) != "open" || len(f.bodies(1)) != 3 || !strings.Contains(f.bodies(1)[2], tc.reason) || !strings.Contains(f.bodies(2)[0], "/issues/1") {
					t.Fatalf("re-review not routed: %+v", f)
				}
			}
		})
	}
}
func boolPtr(b bool) *bool { return &b }

func TestCanonicalIdentitySurvivesTargetRename(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("renamed"), f.requester.Owner)
	f.target.FullName = "alice/renamed"
	f.target.Name = "renamed"
	f.comments[1] = []reviewComment{{ID: 10, Body: encodeRecord(protocolFixture(t).Admission.Marker, admissionRecord{11, 55, "renamed"}), User: f.node.Owner}, {ID: 11, Body: encodeRecord("shoal-review-event:v1", reviewEvent{Type: "REVIEWED", ReviewerNodeID: 11, TargetRepositoryID: 55, TargetRepositoryFullName: "alice/project", TargetDefaultBranch: "main", TargetCommit: testSHA1, ReviewPolicyPath: "README.md", ReviewPolicyCommit: testSHA1, Verdict: "PASS", ActualStarState: boolPtr(true), ReviewedAt: "2026-09-24T00:00:00Z"}), User: f.node.Owner}}
	f.issues[0].State = "closed"
	f.addIssue(2, reviewBody("renamed"), f.requester.Owner)
	f.process(t)
	if f.state(1) != "closed" || f.state(2) != "closed" || !strings.Contains(f.bodies(2)[0], "NO_NEW_REVIEW_BASIS") {
		t.Fatalf("rename created second lifecycle: %+v", f)
	}
}

func TestUntrustedCommentsDoNotEstablishCanonicalThread(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.comments[1] = []reviewComment{{ID: 10, Body: encodeRecord(protocolFixture(t).Admission.Marker, admissionRecord{11, 55, "project"}), User: f.requester.Owner}}
	f.process(t)
	if f.state(1) != "open" || len(f.bodies(1)) != 2 || f.comments[1][1].User.ID != f.node.Owner.ID {
		t.Fatalf("requester-authored admission treated as trusted: %+v", f.comments[1])
	}
}

func TestRetryAfterReReviewEventAppend(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.comments[1] = []reviewComment{{ID: 10, Body: encodeRecord(protocolFixture(t).Admission.Marker, admissionRecord{11, 55, "project"}), User: f.node.Owner}, {ID: 11, Body: encodeRecord("shoal-review-event:v1", reviewEvent{Type: "REVIEWED", ReviewerNodeID: 11, TargetRepositoryID: 55, TargetRepositoryFullName: "alice/project", TargetDefaultBranch: "main", TargetCommit: testSHA1, ReviewPolicyPath: "README.md", ReviewPolicyCommit: testSHA1, Verdict: "PASS", ActualStarState: boolPtr(true), ReviewedAt: "2026-09-24T00:00:00Z"}), User: f.node.Owner}}
	f.issues[0].State = "closed"
	f.addIssue(2, reviewBody("project"), f.requester.Owner)
	f.head = testSHA2
	f.failOn = "-f state=open"
	if err := f.command(t).execute(context.Background(), nil); err == nil {
		t.Fatal("expected injected failure")
	}
	if len(f.bodies(1)) != 3 || f.state(2) != "open" {
		t.Fatalf("unexpected partial state: %+v", f)
	}
	f.failOn = ""
	f.process(t)
	if len(f.bodies(1)) != 3 || f.state(1) != "open" || f.state(2) != "closed" {
		t.Fatalf("retry duplicated event or failed redirect: %+v", f)
	}
}

func TestInvalidJudgmentDoesNotCreateReviewBasis(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.comments[1] = []reviewComment{{ID: 10, Body: encodeRecord(protocolFixture(t).Admission.Marker, admissionRecord{11, 55, "project"}), User: f.node.Owner}, {ID: 11, Body: encodeRecord("shoal-review-event:v1", reviewEvent{Type: "REVIEWED", ReviewerNodeID: 11, TargetRepositoryID: 55, TargetCommit: testSHA1, ReviewPolicyCommit: testSHA1}), User: f.node.Owner}}
	f.issues[0].State = "closed"
	f.addIssue(2, reviewBody("project"), f.requester.Owner)
	f.head = testSHA2
	f.process(t)
	if len(f.bodies(1)) != 2 || !strings.Contains(f.bodies(2)[0], "NO_NEW_REVIEW_BASIS") {
		t.Fatalf("invalid judgment granted re-review eligibility: %+v", f)
	}
}

func TestDamagedCanonicalPreventsSecondThread(t *testing.T) {
	f := fixture()
	f.addIssue(1, "deleted payload", f.requester.Owner)
	f.comments[1] = []reviewComment{{ID: 10, Body: encodeRecord(protocolFixture(t).Admission.Marker, admissionRecord{11, 55, "project"}), User: f.node.Owner}}
	f.issues[0].State = "closed"
	f.addIssue(2, reviewBody("project"), f.requester.Owner)
	err := f.command(t).execute(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "damaged request payload") || f.state(2) != "open" || len(f.bodies(2)) != 0 {
		t.Fatalf("corrupted canonical created another thread: %v %+v", err, f)
	}
}

func TestMissingTargetIsInvalidRequest(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("missing"), f.requester.Owner)
	f.process(t)
	if f.state(1) != "closed" || len(f.bodies(1)) != 1 || !strings.Contains(f.bodies(1)[0], "INVALID_REQUEST") {
		t.Fatalf("missing target admitted: %+v", f)
	}
}

func TestGitHubFailureDoesNotCloseRequest(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.failOn = "repos/alice/project"
	if err := f.command(t).execute(context.Background(), nil); err == nil {
		t.Fatal("expected API failure")
	}
	if f.state(1) != "open" || len(f.bodies(1)) != 0 {
		t.Fatalf("transient API failure treated as invalid request: %+v", f)
	}
}

func TestPendingEventDoesNotReplaceLastJudgmentBasis(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.comments[1] = []reviewComment{
		{ID: 10, Body: encodeRecord(protocolFixture(t).Admission.Marker, admissionRecord{11, 55, "project"}), User: f.node.Owner},
		{ID: 11, Body: encodeRecord(protocolFixture(t).Event.Marker, reviewEvent{Type: "REVIEWED", ReviewerNodeID: 11, TargetRepositoryID: 55, TargetRepositoryFullName: "alice/project", TargetDefaultBranch: "main", TargetCommit: testSHA1, ReviewPolicyPath: "README.md", ReviewPolicyCommit: testSHA1, Verdict: "PASS", ActualStarState: boolPtr(true), ReviewedAt: "2026-09-24T00:00:00Z"}), User: f.node.Owner},
		{ID: 12, Body: encodeRecord(protocolFixture(t).Event.Marker, reviewEvent{Type: "RE_REVIEW_REQUESTED", ReviewerNodeID: 11, TargetRepositoryID: 55, RequestIssueNumber: 2, EligibilityTargetCommit: testSHA2, ReviewPolicyCommit: testSHA1, Reason: "TARGET_CHANGED"}), User: f.node.Owner},
	}
	f.issues[0].State = "open"
	f.addIssue(2, reviewBody("project"), f.requester.Owner)
	f.issues[1].State = "closed"
	f.addIssue(3, reviewBody("project"), f.requester.Owner)
	f.head = testSHA2
	f.policy = testSHA2
	f.process(t)
	if f.state(3) != "closed" || len(f.bodies(1)) != 4 || !strings.Contains(f.bodies(1)[3], "TARGET_AND_POLICY_CHANGED") {
		t.Fatalf("pending event became judgment baseline: %+v", f)
	}
}

func TestTransferredTargetReusesStableCanonicalIdentity(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.comments[1] = []reviewComment{{ID: 10, Body: encodeRecord(protocolFixture(t).Admission.Marker, admissionRecord{11, 55, "project"}), User: f.node.Owner}, {ID: 11, Body: encodeRecord(protocolFixture(t).Event.Marker, reviewEvent{Type: "REVIEWED", ReviewerNodeID: 11, TargetRepositoryID: 55, TargetRepositoryFullName: "alice/project", TargetDefaultBranch: "main", TargetCommit: testSHA1, ReviewPolicyPath: "README.md", ReviewPolicyCommit: testSHA1, Verdict: "PASS", ActualStarState: boolPtr(true), ReviewedAt: "2026-09-24T00:00:00Z"}), User: f.node.Owner}}
	f.issues[0].State = "closed"
	f.target.FullName = "bob/project"
	f.target.Owner = reviewUser{ID: 3, Login: "bob", Type: "User"}
	f.addIssue(2, reviewBody("project"), f.target.Owner)
	f.process(t)
	if f.state(1) != "closed" || f.state(2) != "closed" || !strings.Contains(f.bodies(2)[0], "NO_NEW_REVIEW_BASIS") {
		t.Fatalf("transfer broke stable lifecycle: %+v", f)
	}
}

func TestEditedCanonicalNameCannotRedirectRecordedIdentity(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("other"), f.requester.Owner)
	f.comments[1] = []reviewComment{{ID: 10, Body: encodeRecord(protocolFixture(t).Admission.Marker, admissionRecord{11, 55, "project"}), User: f.node.Owner}}
	f.issues[0].State = "closed"
	f.addIssue(2, reviewBody("project"), f.requester.Owner)
	err := f.command(t).execute(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "original request payload") || f.state(2) != "open" {
		t.Fatalf("edited body redirected stable identity: %v %+v", err, f)
	}
}

func TestUnrelatedJudgmentCannotRebindAdmittedIssue(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("other"), f.requester.Owner)
	f.comments[1] = []reviewComment{
		{ID: 10, Body: encodeRecord(protocolFixture(t).Admission.Marker, admissionRecord{11, 56, "other"}), User: f.node.Owner},
		{ID: 11, Body: encodeRecord(protocolFixture(t).Event.Marker, reviewEvent{Type: "REVIEWED", ReviewerNodeID: 11, TargetRepositoryID: 55, TargetRepositoryFullName: "alice/project", TargetDefaultBranch: "main", TargetCommit: testSHA1, ReviewPolicyPath: "README.md", ReviewPolicyCommit: testSHA1, Verdict: "PASS", ActualStarState: boolPtr(true), ReviewedAt: "2026-09-24T00:00:00Z"}), User: f.node.Owner},
	}
	f.issues[0].State = "closed"
	f.addIssue(2, reviewBody("project"), f.requester.Owner)
	f.process(t)
	if f.state(2) != "open" || len(f.bodies(2)) != 1 || !strings.Contains(f.bodies(2)[0], protocolFixture(t).Admission.Marker) {
		t.Fatalf("unrelated judgment stole canonical identity: %+v", f)
	}
}

func TestUnrecordedJudgmentRequiresOriginalTargetBinding(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("other"), f.requester.Owner)
	f.comments[1] = []reviewComment{{ID: 11, Body: encodeRecord(protocolFixture(t).Event.Marker, reviewEvent{Type: "REVIEWED", ReviewerNodeID: 11, TargetRepositoryID: 55, TargetRepositoryFullName: "alice/other", TargetDefaultBranch: "main", TargetCommit: testSHA1, ReviewPolicyPath: "README.md", ReviewPolicyCommit: testSHA1, Verdict: "PASS", ActualStarState: boolPtr(true), ReviewedAt: "2026-09-24T00:00:00Z"}), User: f.node.Owner}}
	f.issues[0].State = "closed"
	f.addIssue(2, reviewBody("project"), f.requester.Owner)
	f.process(t)
	if f.state(2) != "open" || !strings.Contains(f.bodies(2)[0], protocolFixture(t).Admission.Marker) {
		t.Fatalf("unbound manual judgment stole canonical identity: %+v", f)
	}
}
