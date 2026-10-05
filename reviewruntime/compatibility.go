package reviewruntime

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

//go:embed protocol/capability.json
var capabilityBytes []byte

type summaryContract struct {
	ProtocolVersion      int `json:"protocolVersion"`
	SummarySchemaVersion int `json:"summarySchemaVersion"`
}
type workflowCapability struct {
	ActionCommit    string          `json:"actionCommit"`
	ReviewerSummary summaryContract `json:"reviewerSummary"`
}
type capability struct {
	NetworkRoot struct {
		RepositoryID int64 `json:"repositoryId"`
	} `json:"networkRoot"`
	RequestFormPath                   string                        `json:"requestFormPath"`
	SummaryWorkflowPath               string                        `json:"summaryWorkflowPath"`
	ReviewProtocolVersion             string                        `json:"reviewProtocolVersion"`
	AdmissionMarker                   string                        `json:"admissionMarker"`
	EventMarker                       string                        `json:"eventMarker"`
	SourceFiles                       map[string]string             `json:"sourceFiles"`
	RequestFormDigests                []string                      `json:"requestFormDigests"`
	SummaryWorkflows                  map[string]workflowCapability `json:"summaryWorkflows"`
	SupportedReviewerSummaryContracts []summaryContract             `json:"supportedReviewerSummaryContracts"`
}

func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func loadCapability(p reviewContract) (capability, error) {
	var s capability
	if err := json.Unmarshal(capabilityBytes, &s); err != nil {
		return s, errors.New("invalid installed compatibility snapshot")
	}
	b, err := reviewContractFS.ReadFile("protocol/review-v1.json")
	if err != nil || s.SourceFiles["protocol/review-v1.json"] != hashBytes(b) || s.NetworkRoot.RepositoryID != rootID || s.RequestFormPath != requestFormPath || s.SummaryWorkflowPath != summaryPath || s.ReviewProtocolVersion != p.ProtocolVersion || s.AdmissionMarker != p.Admission.Marker || s.EventMarker != p.Event.Marker {
		return s, errors.New("installed Protocol contract does not match the proven capability snapshot")
	}
	return s, nil
}
func (s capability) supports(contents map[string][]byte) bool {
	form := hashBytes(contents[s.RequestFormPath])
	formOK := false
	for _, allowed := range s.RequestFormDigests {
		if allowed == form {
			formOK = true
		}
	}
	workflow, ok := s.SummaryWorkflows[hashBytes(contents[s.SummaryWorkflowPath])]
	if !formOK || !ok || !commitSHA.MatchString(workflow.ActionCommit) {
		return false
	}
	for _, contract := range s.SupportedReviewerSummaryContracts {
		if contract == workflow.ReviewerSummary {
			return true
		}
	}
	return false
}

type diagnostic struct{ Class, Surface, Action string }

func (d diagnostic) Error() string { return fmt.Sprintf("%s: %s. %s", d.Class, d.Surface, d.Action) }
func unavailable(surface string) error {
	return diagnostic{"EXTERNAL_STATE_UNAVAILABLE", surface, "Required state could not be verified; retry after it is observable. No blind write retry was performed."}
}
func upgrade(surface string) error {
	return diagnostic{"CLI_UPGRADE_REQUIRED", surface, "Upgrade the official gh-shoal extension before retrying."}
}
func report(w io.Writer, class, surface, action string) {
	if w != nil {
		fmt.Fprintln(w, diagnostic{class, surface, action})
	}
}

type apiReader func(context.Context, string, any) error

func committedCompatibilityFiles(ctx context.Context, api apiReader, name, branch string) (map[string][]byte, error) {
	if !repoName.MatchString(name) || branch == "" {
		return nil, unavailable("Station repository identity / default branch")
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if api(ctx, "repos/"+name+"/git/ref/heads/"+url.PathEscape(branch), &ref) != nil || !commitSHA.MatchString(ref.Object.SHA) {
		return nil, unavailable("Station default-branch snapshot")
	}
	contents := map[string][]byte{}
	for _, path := range compatibilityPaths {
		var f struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
			Type     string `json:"type"`
		}
		err := api(ctx, "repos/"+name+"/contents/"+path+"?ref="+ref.Object.SHA, &f)
		if isNotFound(err) {
			continue
		} // Observably absent is drift; a failed read is unknown.
		if err != nil {
			return nil, unavailable(path)
		}
		if f.Type != "file" || f.Encoding != "base64" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.ReplaceAll(f.Content, "\n", ""), "\r", ""))
		if err != nil {
			return nil, unavailable(path + " exact bytes")
		}
		contents[path] = b
	}
	return contents, nil
}

func (c reviewCommand) stationPreflight(ctx context.Context, node reviewRepository) error {
	s, err := loadCapability(c.protocol)
	if err != nil {
		return err
	}
	files, err := committedCompatibilityFiles(ctx, c.api, node.FullName, node.DefaultBranch)
	if err != nil {
		return err
	}
	if node.HasIssues != nil && *node.HasIssues && s.supports(files) {
		report(c.out, "SUPPORTED", "Station and installed Protocol capability", "Existing Review lifecycle rules apply.")
		return nil
	}
	if node.HasIssues == nil {
		return unavailable("Reviewer Node Issues availability")
	}
	var root reviewRepository
	if c.api(ctx, fmt.Sprintf("repositories/%d", rootID), &root) != nil || root.ID != rootID {
		return unavailable("canonical Network Root identity")
	}
	target, err := committedCompatibilityFiles(ctx, c.api, root.FullName, root.DefaultBranch)
	if err != nil {
		return err
	}
	if !s.supports(target) {
		return upgrade("current canonical Network Root generation")
	}
	if node.ID == rootID {
		return diagnostic{"REPAIRABLE_STATION_DRIFT", "Network Root managed surfaces / Issues", "Canonical maintainer repair is required; init only synchronizes direct forks."}
	}
	return diagnostic{"REPAIRABLE_STATION_DRIFT", "Reviewer Node managed surfaces / Issues", "Run gh shoal init from synchronized main before retrying Review."}
}

// Recognize formal envelopes before strict current-version decoding. Damaged
// current evidence remains malformed; an explicit unsupported contract is never
// coerced into the current shape. Arbitrary conversational text is not evidence.
func incompatibleEvidence(body string, s capability) bool {
	marker, payload, _ := strings.Cut(body, "\n")
	formal := strings.HasPrefix(marker, "shoal-review-event:v") || strings.HasPrefix(marker, "shoal-review-admission:v")
	if !formal || strings.ContainsAny(marker, " \t\r") {
		return false
	}
	if marker != s.EventMarker && marker != s.AdmissionMarker {
		return true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(payload), &fields) != nil {
		return false
	}
	for _, name := range []string{"protocolVersion", "schemaVersion", "eventSchemaVersion", "admissionSchemaVersion"} {
		if value, ok := fields[name]; ok {
			// Existing v1 envelopes have no version fields; when explicitly present,
			// only this exact contract identity is understood, never numeric ordering.
			if string(value) != `"`+s.ReviewProtocolVersion+`"` && string(value) != s.ReviewProtocolVersion {
				return true
			}
		}
	}
	return false
}
func (c reviewCommand) historyPreflight(ctx context.Context, node reviewRepository, issues []reviewIssue) error {
	s, err := loadCapability(c.protocol)
	if err != nil {
		return err
	}
	for _, issue := range issues {
		if issue.PullRequest != nil {
			continue
		}
		comments, err := c.comments(ctx, node.FullName, issue.Number)
		if err != nil {
			return unavailable("Review Thread comments")
		}
		for _, comment := range comments {
			if comment.User.ID == node.Owner.ID && incompatibleEvidence(comment.Body, s) {
				return diagnostic{"INCOMPATIBLE_PROTOCOL_EVIDENCE", fmt.Sprintf("formal evidence on Issue #%d", issue.Number), "Refusing to reinterpret or mutate unsupported evidence; inspect its contract with a compatible official Extension."}
			}
		}
	}
	return nil
}
