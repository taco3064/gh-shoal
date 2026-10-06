package reviewruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

type evidenceDocument struct {
	FormatVersion int               `json:"formatVersion"`
	Record        json.RawMessage   `json:"record"`
	Presentation  map[string]string `json:"presentation"`
}

var evidenceMarkers = regexp.MustCompile(`<!-- shoal-evidence:[^\r\n]*? -->`)

// decodeEvidence recognizes only the Platform-owned machine namespace. The
// boolean distinguishes ordinary prose from malformed/unsupported evidence.
func decodeEvidence(body string) (evidenceDocument, bool, error) {
	var result evidenceDocument
	prefixes := strings.Count(body, "<!-- shoal-evidence:")
	if prefixes == 0 {
		return result, false, nil
	}
	p, err := loadReviewContract()
	if err != nil {
		return result, true, err
	}
	markers := evidenceMarkers.FindAllString(body, -1)
	if prefixes != 2 || len(markers) != 2 || markers[0] != p.Evidence.StartSentinel || markers[1] != p.Evidence.EndSentinel {
		return result, true, errors.New("ambiguous or unsupported evidence sentinels")
	}
	start := strings.Index(body, p.Evidence.StartSentinel) + len(p.Evidence.StartSentinel)
	end := strings.Index(body, p.Evidence.EndSentinel)
	payload := strings.TrimSpace(body[start:end])
	if err := uniqueJSON(payload); err != nil {
		return result, true, err
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal([]byte(payload), &envelope) != nil || len(envelope) != 3 {
		return result, true, errors.New("invalid evidence envelope")
	}
	var version float64
	var record map[string]json.RawMessage
	var presentation map[string]any
	if json.Unmarshal(envelope["formatVersion"], &version) != nil || version != 1 ||
		json.Unmarshal(envelope["record"], &record) != nil || record == nil ||
		json.Unmarshal(envelope["presentation"], &presentation) != nil || presentation == nil {
		return result, true, errors.New("invalid evidence document fields")
	}
	result = evidenceDocument{FormatVersion: 1, Record: envelope["record"], Presentation: map[string]string{}}
	for key, value := range presentation {
		text, ok := value.(string)
		if !ok || (key != "explanation" && key != "requestAuthor") {
			return result, true, errors.New("invalid evidence presentation")
		}
		result.Presentation[key] = text
	}
	return result, true, nil
}

// JSON's usual last-key-wins behavior is not acceptable for formal evidence.
func uniqueJSON(payload string) error {
	d := json.NewDecoder(strings.NewReader(payload))
	d.UseNumber()
	var value func() error
	value = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		if delim != '{' && delim != '[' {
			return errors.New("unexpected JSON delimiter")
		}
		keys := map[string]bool{}
		for d.More() {
			if delim == '{' {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || keys[name] {
					return errors.New("duplicate JSON key")
				}
				keys[name] = true
			}
			if err := value(); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return errors.New("extra JSON value")
	}
	return nil
}

func encodeRecord(marker string, v any, requestAuthor ...string) string {
	p, err := loadReviewContract()
	if err != nil {
		panic(err) // Installed source correspondence is verified before execution.
	}
	presentation := map[string]string{}
	var human string
	switch record := v.(type) {
	case admissionRecord:
		if marker != p.Admission.Marker || len(requestAuthor) != 1 || !regexp.MustCompile(`^[A-Za-z0-9-]+$`).MatchString(requestAuthor[0]) {
			panic("Admission requires its authoritative Request author")
		}
		presentation["requestAuthor"] = requestAuthor[0]
		human = fmt.Sprintf("## Request admitted\n\nTarget: %s/%s\n\nThis Issue is the Canonical Review Thread for this Target (repository ID %d).", escapeEvidenceText(requestAuthor[0]), escapeEvidenceText(record.RepositoryName), record.TargetRepositoryID)
	case reviewEvent:
		if marker != p.Event.Marker {
			panic("Event requires the accepted Protocol contract")
		}
		if record.Verdict != "" {
			presentation["explanation"] = record.Explanation
			state := "not starred"
			if record.ActualStarState != nil && *record.ActualStarState {
				state = "starred"
			}
			human = fmt.Sprintf("## Review Result: %s\n\nEvent: %s\n\nTarget Repository: %s\n\nTarget commit: %s\n\nReview Policy commit: %s\n\nActual Star state: %s\n\nReview time: %s\n\n### Explanation\n\n%s", record.Verdict, record.Type, escapeEvidenceText(record.TargetRepositoryFullName), record.TargetCommit, record.ReviewPolicyCommit, state, escapeEvidenceText(record.ReviewedAt), escapeEvidenceText(record.Explanation))
		} else {
			human = fmt.Sprintf("## %s\n\nTarget repository ID: %d\n\nRequest Issue: #%d\n\nEligibility Target commit: %s\n\nReview Policy commit: %s\n\nReason: %s", record.Type, record.TargetRepositoryID, record.RequestIssueNumber, record.EligibilityTargetCommit, record.ReviewPolicyCommit, record.Reason)
		}
	default:
		panic("unsupported formal record")
	}
	record, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	payload, err := json.Marshal(evidenceDocument{1, record, presentation})
	if err != nil {
		panic(err)
	}
	return human + "\n\n<details>\n<summary>Formal Shoal evidence</summary>\n\n" + p.Evidence.StartSentinel + "\n" + string(payload) + "\n" + p.Evidence.EndSentinel + "\n\n</details>"
}

func decodeRecord(body, marker string, out any) bool {
	document, present, err := decodeEvidence(body)
	if !present || err != nil {
		return false
	}
	p, err := loadReviewContract()
	if err != nil {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(document.Record, &fields) != nil {
		return false
	}
	_, event := fields["type"]
	if (marker == p.Admission.Marker && event) || (marker == p.Event.Marker && !event) || (marker != p.Admission.Marker && marker != p.Event.Marker) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(document.Record))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return false
	}
	if event, ok := out.(*reviewEvent); ok {
		event.Explanation = document.Presentation["explanation"]
	}
	return true
}

func escapeEvidenceText(value string) string {
	value = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
	var result strings.Builder
	for _, char := range value {
		if strings.ContainsRune("\\`*_{}[]()#+.!|~-", char) {
			result.WriteByte('\\')
		}
		result.WriteRune(char)
	}
	return result.String()
}
