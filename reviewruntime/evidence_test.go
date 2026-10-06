package reviewruntime

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalEvidenceRoundTrip(t *testing.T) {
	p := protocolFixture(t)
	event := reviewEvent{Type: "REVIEWED", ReviewerNodeID: 11, TargetRepositoryID: 55,
		TargetRepositoryFullName: "alice/project", TargetDefaultBranch: "main", TargetCommit: testSHA1,
		ReviewPolicyPath: "README.md", ReviewPolicyCommit: testSHA2, Verdict: "PASS", ActualStarState: boolPtr(true),
		ReviewedAt: "2026-10-06T00:00:00Z", Explanation: "Policy checked.\n</details><!-- shoal-evidence:v999:start -->"}
	comments := []string{encodeRecord(p.Admission.Marker, admissionRecord{11, 55, "project"}, "alice")}
	for _, kind := range []string{"REVIEWED", "RE_REVIEWED", "STAR_REVOKED", "REVOKED_EXTERNALLY"} {
		e := event
		e.Type = kind
		if strings.HasPrefix(kind, "STAR_") || kind == "REVOKED_EXTERNALLY" {
			e.Verdict, e.ActualStarState = "FAIL", boolPtr(false)
		}
		body := encodeRecord(p.Event.Marker, e)
		if body != encodeRecord(p.Event.Marker, e) {
			t.Fatal("nondeterministic comment")
		}
		var decoded reviewEvent
		if !decodeRecord(body, p.Event.Marker, &decoded) || !reflect.DeepEqual(e, decoded) {
			t.Fatalf("round-trip changed judgment: %#v", decoded)
		}
		machine := strings.Index(body, p.Evidence.StartSentinel)
		if !decodeRecord("Human prose FAIL\n"+body[machine:], p.Event.Marker, &decoded) || !reflect.DeepEqual(e, decoded) {
			t.Fatal("prose changed authority")
		}
		comments = append(comments, body)
	}
	for _, kind := range []string{"RE_REVIEW_REQUESTED", "STALE_DETECTED", "ENDORSEMENT_DRIFT"} {
		e := reviewEvent{Type: kind, ReviewerNodeID: 11, TargetRepositoryID: 55, RequestIssueNumber: 2,
			EligibilityTargetCommit: testSHA1, ReviewPolicyCommit: testSHA2, Reason: "TARGET_CHANGED"}
		body := encodeRecord(p.Event.Marker, e)
		var decoded reviewEvent
		if !decodeRecord(body, p.Event.Marker, &decoded) || !reflect.DeepEqual(e, decoded) {
			t.Fatal("round-trip changed lifecycle")
		}
		comments = append(comments, body)
	}
	// Export actual runtime output for the independently compiled Platform parser
	// and human renderer parity check. Normal unit tests perform no file writes.
	if path := os.Getenv("SHOAL_EVIDENCE_PARITY_OUTPUT"); path != "" {
		data, err := json.Marshal(comments)
		if err != nil || os.WriteFile(path, data, 0600) != nil {
			t.Fatal("cannot export parity evidence")
		}
	}
}

func TestEvidenceRejectsAmbiguityWithoutProseAuthority(t *testing.T) {
	p := protocolFixture(t)
	s, err := loadCapability(p)
	if err != nil {
		t.Fatal(err)
	}
	body := encodeRecord(p.Admission.Marker, admissionRecord{11, 55, "project"}, "alice")
	for _, broken := range []string{body + body, p.Evidence.StartSentinel + body, body + p.Evidence.EndSentinel,
		body + "<!-- shoal-evidence:broken", strings.ReplaceAll(body, "shoal-evidence:v1:", "shoal-evidence:v2:"),
		strings.Replace(body, `"formatVersion":1`, `"formatVersion":99`, 1),
		strings.Replace(body, `"formatVersion":1`, `"formatVersion":1,"formatVersion":1`, 1),
		strings.Replace(body, `"reviewerNodeId":11`, `"reviewerNodeId":12,"reviewerNodeId":11`, 1),
		strings.Replace(body, `"reviewerNodeId":11`, `"reviewerNodeId":12,"reviewerNode\u0049d":11`, 1),
		strings.Replace(body, `"presentation":`, `"foreign":`, 1),
		strings.Replace(body, p.Evidence.EndSentinel, "", 1),
		p.Evidence.StartSentinel + "\n{\n" + p.Evidence.EndSentinel} {
		var out admissionRecord
		if decodeRecord(broken, p.Admission.Marker, &out) || !incompatibleEvidence(broken, s) {
			t.Fatalf("malformed/unsupported envelope accepted: %s", broken)
		}
	}
	for _, prose := range []string{"Review Result: PASS", "REVIEWED", `{"verdict":"PASS"}`,
		"shoal-review-event:v1\n{}", "<!-- fake heading --> PASS", "Explanation: protocolVersion 999"} {
		var out reviewEvent
		if decodeRecord(prose, p.Event.Marker, &out) || incompatibleEvidence(prose, s) {
			t.Fatalf("prose gained authority: %s", prose)
		}
	}
}

func TestHumanFirstCanonicalCallerCapability(t *testing.T) {
	s, err := loadCapability(protocolFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	files := realManagedFixture()
	caller, err := managedFixtureFS.ReadFile("testdata/reviewer-summary-human-first.yml")
	if err != nil || hashBytes(caller) != "0dee3b797307225e38b475e0456cff6434ca53c4b0580fb3f4a11dfdc5eece35" {
		t.Fatal("Stage A fixture drift")
	}
	files[summaryPath] = caller
	if !s.supports(files) || s.SummaryWorkflows[hashBytes(caller)].ActionCommit != "47e1c3ab5762d66e6f49c3f2a15c133a9785679c" {
		t.Fatal("new exact caller unsupported")
	}
	files[summaryPath] = append(append([]byte{}, caller...), ' ')
	if s.supports(files) {
		t.Fatal("drift accepted")
	}
}
