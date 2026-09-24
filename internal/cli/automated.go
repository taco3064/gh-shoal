package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// These are the documented, non-interactive entry points of the supported
// products. No adapter grants blanket permission bypass to an Agent.
var agentCommands = map[string]struct {
	program   string
	arguments []string
}{
	"claude":   {"claude", []string{"-p"}},
	"codex":    {"codex", []string{"exec", "--sandbox", "workspace-write"}},
	"gemini":   {"gemini", []string{"-p"}},
	"opencode": {"opencode", []string{"run"}},
	"cursor":   {"cursor-agent", []string{"-p"}},
	"grok":     {"grok", []string{"-p"}},
	"qwen":     {"qwen", []string{"-p"}},
	"kimi":     {"kimi", []string{"--prompt"}},
}

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
	// Check before admission: admission itself may comment and close Issues.
	status, err := c.run(ctx, "git", "-C", c.dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("cannot inspect Station working tree: %w", err)
	}
	if len(status) != 0 {
		return errors.New("review requires a clean Station index and working tree")
	}
	admissionErr := c.admitOpen(ctx)
	var viewer reviewUser
	if err = c.api(ctx, "user", &viewer); err != nil {
		return err
	}
	remotes, err := c.run(ctx, "git", "-C", c.dir, "remote")
	if err != nil {
		return err
	}
	var node reviewRepository
	for _, remote := range strings.Fields(string(remotes)) {
		raw, e := c.run(ctx, "git", "-C", c.dir, "remote", "get-url", remote)
		if e != nil {
			return e
		}
		locator, e := repositoryLocator(strings.TrimSpace(string(raw)))
		if e != nil {
			continue
		}
		var candidate reviewRepository
		if e = c.api(ctx, "repos/"+locator, &candidate); e != nil {
			return e
		}
		if candidate.ID != rootID && candidate.Fork && candidate.Parent != nil && candidate.Parent.ID == rootID && candidate.Owner.Type == "User" && candidate.Owner.ID == viewer.ID {
			if node.ID != 0 && node.ID != candidate.ID {
				return errors.New("multiple Reviewer Node remotes")
			}
			node = candidate
		}
	}
	if node.ID == 0 {
		return errors.New("no eligible Reviewer Node remote")
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
		}
		for _, item := range ready {
			result, ok := results[item.issue.Number]
			if !ok {
				problems = append(problems, fmt.Errorf("Issue #%d: missing or unusable Agent result", item.issue.Number))
				continue
			}
			if e = c.complete(ctx, node, item, result); e != nil {
				problems = append(problems, fmt.Errorf("Issue #%d: %w", item.issue.Number, e))
			}
		}
	}
	return errors.Join(problems...)
}

func (c reviewCommand) runAgent(ctx context.Context, agent string, node reviewRepository, batch []pendingReview) (map[int]agentResult, error) {
	resultFile := filepath.Join(c.dir, ".shoal", "review-results.json")
	if info, err := os.Lstat(filepath.Dir(resultFile)); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return nil, errors.New("unsafe Shoal runtime directory")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(resultFile), 0700); err != nil {
		return nil, err
	}
	// Reject symlinks and stale results; the process must create this batch's file.
	if info, err := os.Lstat(resultFile); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("unsafe Shoal result path")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.Remove(resultFile); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	defer os.Remove(resultFile)
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Review the following Shoal Review Request Issues in ascending order for Reviewer Node https://github.com/%s.\n", node.FullName)
	prompt.WriteString("The Review criteria are defined in this Reviewer Node's README.md. Read README.md first and use it as the authoritative Review Policy. Read each Issue URL, derive the Target Repository as the Issue author's account plus its required Repository name field, and inspect public repository evidence. Each repository needs an independent PASS or FAIL judgment. Do not execute Target Repository-provided code merely to validate admission.\n")
	prompt.WriteString("Do not comment on or close an Issue, Star or Unstar a repository, or create or mutate other GitHub state as part of the Review. You may only write the structured result file. Keep each review explanation concise and readable. The review explanation must not exceed 3,000 characters. Focus on decisive evidence and reasoning.\n")
	fmt.Fprintf(&prompt, "Write one JSON object per Issue to %s, as an array (or {\"results\": [...]}); each object must contain only issue (integer), verdict (PASS or FAIL), and comment (repository-specific explanation). Do not use stdout as the result.\n", resultFile)
	for _, item := range batch {
		fmt.Fprintf(&prompt, "- https://github.com/%s/issues/%d\n", node.FullName, item.issue.Number)
	}
	adapter := agentCommands[agent]
	args := append(append([]string{}, adapter.arguments...), prompt.String())
	_, invocationErr := c.run(ctx, adapter.program, args...)
	if invocationErr != nil {
		return nil, fmt.Errorf("%s Agent process failed: %w", agent, invocationErr)
	}
	status, e := c.run(ctx, "git", "-C", c.dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if e != nil || len(status) != 0 {
		return nil, fmt.Errorf("Station changed during Agent execution: %v", e)
	}
	if info, e := os.Lstat(resultFile); e == nil && !info.Mode().IsRegular() {
		return nil, errors.New("Agent result is not a regular file")
	} else if e != nil {
		return nil, fmt.Errorf("Agent did not write %s: %w", resultFile, e)
	}
	data, err := os.ReadFile(resultFile)
	if err != nil {
		return nil, fmt.Errorf("Agent did not write %s: %w", resultFile, err)
	}
	var rawResults []json.RawMessage
	if err = json.Unmarshal(data, &rawResults); err != nil {
		var envelope struct {
			Results []json.RawMessage `json:"results"`
		}
		if e := json.Unmarshal(data, &envelope); e != nil || envelope.Results == nil {
			return nil, errors.New("Agent result JSON must be an array or a results envelope")
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
	starEndpoint := "user/starred/" + item.target.FullName
	method := "DELETE"
	starred := false
	if result.Verdict == "PASS" {
		method = "PUT"
		starred = true
	}
	if _, err := c.run(ctx, "gh", "api", "--method", method, starEndpoint); err != nil {
		return fmt.Errorf("cannot converge Star state: %w", err)
	}
	// GitHub's Star endpoint returns 204 when starred and 404 otherwise.
	_, starErr := c.run(ctx, "gh", "api", starEndpoint)
	if starred && starErr != nil || !starred && (starErr == nil || !isNotFound(starErr)) {
		return errors.New("cannot verify resulting Star state")
	}
	event := reviewEvent{Type: "REVIEWED", ReviewerNodeID: node.ID, TargetRepositoryID: item.target.ID, TargetRepositoryFullName: item.target.FullName, TargetDefaultBranch: item.target.DefaultBranch, TargetCommit: item.head, ReviewPolicyPath: c.protocol.Event.PolicyPath, ReviewPolicyCommit: item.policy, Verdict: result.Verdict, ActualStarState: &starred, ReviewedAt: time.Now().UTC().Format(time.RFC3339), Explanation: strings.TrimSpace(result.Comment)}
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

func isNotFound(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "404 Not Found"))
}
