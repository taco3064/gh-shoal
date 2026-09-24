package cli

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed protocol/review-v1.json
var reviewContractFS embed.FS

type reviewContract struct {
	ProtocolVersion string `json:"protocolVersion"`
	Request         struct {
		RepositoryHeading string `json:"repositoryHeading"`
		InvitationHeading string `json:"invitationHeading"`
		EmptyInvitation   string `json:"emptyInvitation"`
	} `json:"request"`
	Admission struct {
		Marker         string   `json:"marker"`
		RequiredFields []string `json:"requiredFields"`
	} `json:"admission"`
	Event struct {
		Marker                  string   `json:"marker"`
		PolicyPath              string   `json:"policyPath"`
		LifecycleType           string   `json:"lifecycleType"`
		JudgmentTypes           []string `json:"judgmentTypes"`
		RequiredLifecycleFields []string `json:"requiredLifecycleFields"`
		RequiredJudgmentFields  []string `json:"requiredJudgmentFields"`
	} `json:"event"`
}

func loadReviewContract() (reviewContract, error) {
	var p reviewContract
	b, err := reviewContractFS.ReadFile("protocol/review-v1.json")
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(b, &p)
	if err != nil || p.ProtocolVersion == "" || p.Request.RepositoryHeading == "" || p.Admission.Marker == "" || p.Event.Marker == "" || p.Event.PolicyPath != "README.md" {
		return p, errors.New("invalid embedded Shoal review protocol")
	}
	return p, nil
}

var bareRepository = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var commitSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// parseRequest accepts the observable Issue Form payload, regardless of which UI created it.
func parseRequest(body string, p reviewContract) (string, error) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	sections := map[string]string{}
	var heading string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "### ") {
			heading = strings.TrimSpace(strings.TrimPrefix(line, "### "))
			if heading != p.Request.RepositoryHeading && heading != p.Request.InvitationHeading {
				return "", errors.New("unexpected request field")
			}
			if _, exists := sections[heading]; exists {
				return "", errors.New("duplicate request field")
			}
			sections[heading] = ""
		} else if heading == "" {
			if strings.TrimSpace(line) != "" {
				return "", errors.New("request must use the Review Request fields")
			}
		} else {
			sections[heading] += line + "\n"
		}
	}
	name := strings.TrimSpace(sections[p.Request.RepositoryHeading])
	if !bareRepository.MatchString(name) || name == "." || name == ".." {
		return "", errors.New("Repository name must be one bare repository name")
	}
	return name, nil
}

type admissionRecord struct {
	ReviewerNodeID     int64  `json:"reviewerNodeId"`
	TargetRepositoryID int64  `json:"targetRepositoryId"`
	RepositoryName     string `json:"repositoryName"`
}

type reviewEvent struct {
	Type                     string `json:"type"`
	ReviewerNodeID           int64  `json:"reviewerNodeId"`
	TargetRepositoryID       int64  `json:"targetRepositoryId"`
	TargetRepositoryFullName string `json:"targetRepositoryFullName,omitempty"`
	TargetDefaultBranch      string `json:"targetDefaultBranch,omitempty"`
	TargetCommit             string `json:"targetCommit,omitempty"`
	ReviewPolicyPath         string `json:"reviewPolicyPath,omitempty"`
	ReviewPolicyCommit       string `json:"reviewPolicyCommit"`
	Verdict                  string `json:"verdict,omitempty"`
	ActualStarState          *bool  `json:"actualStarState,omitempty"`
	ReviewedAt               string `json:"reviewedAt,omitempty"`
	Explanation              string `json:"explanation,omitempty"`
	EligibilityTargetCommit  string `json:"eligibilityTargetCommit,omitempty"`
	RequestIssueNumber       int    `json:"requestIssueNumber,omitempty"`
	Reason                   string `json:"reason,omitempty"`
}

func encodeRecord(marker string, v any) string {
	b, _ := json.Marshal(v)
	return marker + "\n" + string(b)
}

func decodeRecord(body, marker string, out any) bool {
	prefix := marker + "\n"
	if !strings.HasPrefix(body, prefix) {
		return false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(body, prefix))
	d := json.NewDecoder(bytes.NewBufferString(payload))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return false
	}
	var extra any
	return errors.Is(d.Decode(&extra), io.EOF)
}

func validEvent(e reviewEvent, p reviewContract, nodeID, targetID int64) bool {
	if e.ReviewerNodeID != nodeID || e.TargetRepositoryID != targetID || !commitSHA.MatchString(e.ReviewPolicyCommit) {
		return false
	}
	for _, t := range p.Event.JudgmentTypes {
		if e.Type == t {
			_, validTime := time.Parse(time.RFC3339, e.ReviewedAt)
			return commitSHA.MatchString(e.TargetCommit) && repoName.MatchString(e.TargetRepositoryFullName) && e.TargetDefaultBranch != "" && e.ReviewPolicyPath == p.Event.PolicyPath && (e.Verdict == "PASS" || e.Verdict == "FAIL") && e.ActualStarState != nil && validTime == nil
		}
	}
	if e.Type == p.Event.LifecycleType {
		return commitSHA.MatchString(e.EligibilityTargetCommit) && e.RequestIssueNumber > 0 && (e.Reason == "TARGET_CHANGED" || e.Reason == "POLICY_CHANGED" || e.Reason == "TARGET_AND_POLICY_CHANGED")
	}
	return false
}

func encodeEndpoint(parts ...string) string {
	// Only numeric IDs and validated bare names reach the endpoint path.
	return strings.Join(parts, "/")
}

func issueEndpoint(node string, number int) string {
	return encodeEndpoint("repos", node, "issues", strconv.Itoa(number))
}

func invalidExplanation(reason string) string {
	return fmt.Sprintf("INVALID_REQUEST: %s. This Issue is closed without creating a Shoal review thread.", reason)
}
