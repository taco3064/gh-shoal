package reviewruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type pendingReview struct {
	issue        reviewIssue
	target       reviewRepository
	policy, head string
}
type agentResult struct {
	Issue   int    `json:"issue"`
	Verdict string `json:"verdict"`
	Comment string `json:"comment"`
}

func (c reviewCommand) automated(ctx context.Context, agent string) error {
	if c.effects == nil {
		c.effects = new(int)
	}
	// Check before admission: admission itself may comment and close Issues.
	status, err := c.run(ctx, "git", "-C", c.dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("cannot inspect Station working tree: %w", err)
	}
	if len(status) != 0 {
		return errors.New("review requires a clean Station index and working tree")
	}
	admissionErr := c.admitOpen(ctx)
	// Preflight failures abort the entire run before completed-thread recovery.
	var refusal diagnostic
	if errors.As(admissionErr, &refusal) {
		return admissionErr
	}
	node, err := c.currentReviewerNode(ctx)
	if err != nil {
		return err
	}
	var pages [][]reviewIssue
	if err = c.pages(ctx, "repos/"+node.FullName+"/issues?state=open&per_page=100", &pages); err != nil {
		return err
	}
	var queue []pendingReview
	var problems []error
	if admissionErr != nil {
		problems = append(problems, admissionErr)
	}
	for _, page := range pages {
		for _, issue := range page {
			if issue.PullRequest != nil || issue.State != "open" {
				continue
			}
			name, e := parseRequest(issue.Body, c.protocol)
			if e != nil {
				continue
			} // Admission already explained and closed invalid requests.
			comments, e := c.comments(ctx, node.FullName, issue.Number)
			if e != nil {
				problems = append(problems, fmt.Errorf("Issue #%d: %w", issue.Number, e))
				continue
			}
			var record *admissionRecord
			completed := false
			var completedTargetID int64
			pendingReReview := false
			for _, comment := range comments {
				if comment.User.ID != node.Owner.ID {
					continue
				}
				var a admissionRecord
				if decodeRecord(comment.Body, c.protocol.Admission.Marker, &a) && a.ReviewerNodeID == node.ID && a.TargetRepositoryID > 0 && strings.EqualFold(a.RepositoryName, name) {
					record = &a
				}
				var event reviewEvent
				if decodeRecord(comment.Body, c.protocol.Event.Marker, &event) && validEvent(event, c.protocol, node.ID, event.TargetRepositoryID) && event.ReviewerNodeID == node.ID {
					if event.Type == "REVIEWED" && event.ActualStarState != nil && *event.ActualStarState == (event.Verdict == "PASS") {
						completed = true
						completedTargetID = event.TargetRepositoryID
						pendingReReview = false
					}
					if event.Type == c.protocol.Event.LifecycleType {
						pendingReReview = true
					}
				}
			}
			if pendingReReview {
				continue
			} // Re-review judgment belongs to the later maintenance milestone.
			if completed && (record == nil || record.TargetRepositoryID != completedTargetID) {
				problems = append(problems, fmt.Errorf("Issue #%d: completed Review identity conflicts with admission", issue.Number))
				continue
			}
			if completed { // A successful comment followed by a failed close is retryable without a second judgment.
				if e = c.close(ctx, node.FullName, issue.Number); e != nil {
					problems = append(problems, fmt.Errorf("Issue #%d: close completed Review: %w", issue.Number, e))
				}
				continue
			}
			if record == nil {
				continue
			}
			target, exists, e := c.target(ctx, issue.User.Login, name)
			if e != nil || !exists || target.ID != record.TargetRepositoryID || target.Owner.ID != issue.User.ID {
				problems = append(problems, fmt.Errorf("Issue #%d: cannot verify admitted Target identity: %v", issue.Number, e))
				continue
			}
			queue = append(queue, pendingReview{issue: issue, target: target})
		}
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i].issue.Number < queue[j].issue.Number })
	for first := 0; first < len(queue); first += 5 {
		last := first + 5
		if last > len(queue) {
			last = len(queue)
		}
		batch := queue[first:last]
		// A judgment begins with a fresh snapshot of the public default branch
		// and the last commit modifying the Reviewer-owned Review Policy.
		ready := make([]pendingReview, 0, len(batch))
		for _, item := range batch {
			item.head, err = c.targetHead(ctx, item.target)
			if err == nil {
				item.policy, err = c.policyCommit(ctx, node)
			}
			if err != nil {
				problems = append(problems, fmt.Errorf("Issue #%d: cannot snapshot Review basis: %w", item.issue.Number, err))
				continue
			}
			ready = append(ready, item)
		}
		if len(ready) == 0 {
			continue
		}
		results, e := c.runAgent(ctx, agent, node, ready)
		if e != nil {
			problems = append(problems, e)
			break // Operational Agent failure is not a judgment; preserve remaining work.
		}
		for _, item := range ready {
			result, ok := results[item.issue.Number]
			if !ok {
				problems = append(problems, Fault{"AGENT_RESULT_INVALID", fmt.Sprintf("Issue #%d: missing or unusable Agent result", item.issue.Number)})
				continue
			}
			if e = c.complete(ctx, node, item, result); e != nil {
				problems = append(problems, fmt.Errorf("Issue #%d: %w", item.issue.Number, e))
			}
		}
	}
	if len(problems) == 0 && *c.effects == 0 {
		report(c.out, "NO_CHANGES", "No pending Review work", "No lifecycle mutation or semantic judgment was needed.")
	}
	return errors.Join(problems...)
}

func (c reviewCommand) runAgent(ctx context.Context, agent string, node reviewRepository, batch []pendingReview) (map[int]agentResult, error) {
	if c.agent == nil {
		return nil, Fault{"AGENT_UNAVAILABLE", "semantic Agent dependency is required"}
	}
	data, err := c.agent.Judge(ctx, semanticWork(node, batch))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", Fault{Code: "AGENT_UNAVAILABLE", Detail: err.Error()}, err)
	}
	status, inspectErr := c.run(ctx, "git", "-C", c.dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if inspectErr != nil || len(status) != 0 {
		return nil, Fault{"EXECUTION_FAILED", fmt.Sprintf("Station changed during Agent execution: %v", inspectErr)}
	}
	return validateAgentResults(data, batch)
}

func validateAgentResults(data []byte, batch []pendingReview) (map[int]agentResult, error) {
	var err error

	var rawResults []json.RawMessage
	if err = json.Unmarshal(data, &rawResults); err != nil {
		var envelope struct {
			Results []json.RawMessage `json:"results"`
		}
		if e := json.Unmarshal(data, &envelope); e != nil || envelope.Results == nil {
			return nil, Fault{"AGENT_RESULT_INVALID", "Agent result JSON must be an array or a results envelope"}
		}
		rawResults = envelope.Results
	}
	allowed := map[int]bool{}
	for _, item := range batch {
		allowed[item.issue.Number] = true
	}
	parsed := map[int]agentResult{}
	duplicates := map[int]bool{}
	for _, raw := range rawResults {
		var result agentResult
		if json.Unmarshal(raw, &result) != nil {
			continue
		}
		if !allowed[result.Issue] || result.Verdict != "PASS" && result.Verdict != "FAIL" || strings.TrimSpace(result.Comment) == "" {
			continue
		}
		if _, exists := parsed[result.Issue]; exists {
			duplicates[result.Issue] = true
		}
		parsed[result.Issue] = result
	}
	for number := range duplicates {
		delete(parsed, number)
	}
	return parsed, nil
}

func (c reviewCommand) complete(ctx context.Context, node reviewRepository, item pendingReview, result agentResult) error {
	return c.completeJudgment(ctx, node, item, result, "REVIEWED")
}

func (c reviewCommand) completeJudgment(ctx context.Context, node reviewRepository, item pendingReview, result agentResult, eventType string) error {
	if !isJudgmentType(eventType, c.protocol) {
		return errors.New("unsupported Review Judgment Event type")
	}
	starred, err := c.convergeStar(ctx, item.target, result.Verdict)
	if err != nil {
		return err
	}
	event := reviewEvent{Type: eventType, ReviewerNodeID: node.ID, TargetRepositoryID: item.target.ID, TargetRepositoryFullName: item.target.FullName, TargetDefaultBranch: item.target.DefaultBranch, TargetCommit: item.head, ReviewPolicyPath: c.protocol.Event.PolicyPath, ReviewPolicyCommit: item.policy, Verdict: result.Verdict, ActualStarState: &starred, ReviewedAt: time.Now().UTC().Format(time.RFC3339), Explanation: strings.TrimSpace(result.Comment)}
	if !validEvent(event, c.protocol, node.ID, item.target.ID) {
		return errors.New("constructed Review Event is invalid")
	}
	// One public comment contains the protocol event and the Agent's explanation.
	if err := c.commentOnce(ctx, node.FullName, node.Owner.ID, item.issue.Number, encodeRecord(c.protocol.Event.Marker, event)); err != nil {
		return fmt.Errorf("cannot append Review Result: %w", err)
	}
	if err := c.close(ctx, node.FullName, item.issue.Number); err != nil {
		return fmt.Errorf("cannot close completed Review Thread: %w", err)
	}
	return nil
}

func (c reviewCommand) convergeStar(ctx context.Context, target reviewRepository, verdict string) (bool, error) {
	starEndpoint := "user/starred/" + target.FullName
	desired := verdict == "PASS"
	if !desired && verdict != "FAIL" {
		return false, errors.New("invalid Review verdict")
	}
	_, stateErr := c.run(ctx, "gh", "api", starEndpoint)
	actual := stateErr == nil
	if stateErr != nil && !isNotFound(stateErr) {
		return false, fmt.Errorf("cannot inspect current Star state: %w", stateErr)
	}
	if actual != desired {
		method := "DELETE"
		if desired {
			method = "PUT"
		}
		if c.effects != nil {
			*c.effects++
		}
		_, _ = c.run(ctx, "gh", "api", "--method", method, starEndpoint)
		// A lost acknowledgement never authorizes another write. The verified
		// observable state below decides whether this mutation converged.
	}
	_, verifyErr := c.run(ctx, "gh", "api", starEndpoint)
	if desired && verifyErr != nil || !desired && (verifyErr == nil || !isNotFound(verifyErr)) {
		return false, unavailable("resulting Star state; mutation was not replayed")
	}
	return desired, nil
}

func isNotFound(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "404 Not Found"))
}
