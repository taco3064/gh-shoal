package reviewruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
)

type reviewUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}
type reviewRepository struct {
	ID            int64      `json:"id"`
	HasIssues     *bool      `json:"has_issues"`
	FullName      string     `json:"full_name"`
	Name          string     `json:"name"`
	Fork          bool       `json:"fork"`
	DefaultBranch string     `json:"default_branch"`
	Owner         reviewUser `json:"owner"`
	Parent        *struct {
		ID int64 `json:"id"`
	} `json:"parent"`
}
type reviewIssue struct {
	Number      int              `json:"number"`
	State       string           `json:"state"`
	Body        string           `json:"body"`
	User        reviewUser       `json:"user"`
	PullRequest *json.RawMessage `json:"pull_request"`
}
type reviewComment struct {
	ID   int64      `json:"id"`
	Body string     `json:"body"`
	User reviewUser `json:"user"`
}
type reviewCommand struct {
	run           runner
	agent         Agent
	dir           string
	protocol      reviewContract
	commentsCache map[int][]reviewComment
	out           io.Writer
	effects       *int
}

func (c reviewCommand) api(ctx context.Context, endpoint string, out any) error {
	b, err := c.run(ctx, "gh", "api", endpoint)
	if err != nil {
		if isNotFound(err) {
			return err
		}
		return unavailable("required GitHub API read")
	}
	if err = json.Unmarshal(b, out); err != nil {
		return unavailable("required GitHub API response")
	}
	return nil
}
func (c reviewCommand) pages(ctx context.Context, endpoint string, out any) error {
	b, err := c.run(ctx, "gh", "api", "--paginate", "--slurp", endpoint)
	if err != nil {
		return unavailable("required paginated GitHub read")
	}
	if err = json.Unmarshal(b, out); err != nil {
		return unavailable("required paginated GitHub response")
	}
	return nil
}
func (c reviewCommand) write(ctx context.Context, endpoint, method, field, value string) error {
	if c.effects != nil {
		*c.effects++
	}
	_, err := c.run(ctx, "gh", "api", "--method", method, endpoint, "-f", field+"="+value)
	return err
}
func (c reviewCommand) comments(ctx context.Context, node string, number int) ([]reviewComment, error) {
	if cached, ok := c.commentsCache[number]; ok {
		return cached, nil
	}
	var pages [][]reviewComment
	err := c.pages(ctx, issueEndpoint(node, number)+"/comments?per_page=100", &pages)
	var all []reviewComment
	for _, page := range pages {
		all = append(all, page...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	if err == nil && c.commentsCache != nil {
		c.commentsCache[number] = all
	}
	return all, err
}
func (c reviewCommand) commentOnce(ctx context.Context, node string, authorID int64, number int, body string) error {
	comments, err := c.comments(ctx, node, number)
	if err != nil {
		return err
	}
	for _, comment := range comments {
		if comment.User.ID == authorID && comment.Body == body {
			return nil
		}
	}
	err = c.write(ctx, issueEndpoint(node, number)+"/comments", "POST", "body", body)
	delete(c.commentsCache, number)
	if err != nil {
		// A POST may have committed despite a lost acknowledgement. Observe
		// once, never blindly post again under an ambiguous result.
		observed, readErr := c.comments(ctx, node, number)
		if readErr == nil {
			for _, comment := range observed {
				if comment.User.ID == authorID && comment.Body == body {
					return nil
				}
			}
		}
		return unavailable("ambiguous Review comment write; inspect the thread before retrying")
	}
	observed, readErr := c.comments(ctx, node, number)
	if readErr == nil {
		for _, comment := range observed {
			if comment.User.ID == authorID && comment.Body == body {
				return nil
			}
		}
	}
	return unavailable("Review comment read-back / Reviewer authorship")
}
func (c reviewCommand) close(ctx context.Context, node string, number int) error {
	return c.transition(ctx, node, number, "closed")
}
func (c reviewCommand) reopen(ctx context.Context, node string, number int) error {
	return c.transition(ctx, node, number, "open")
}

func (c reviewCommand) transition(ctx context.Context, node string, number int, state string) error {
	// Read before every convergence write, including retries of completed threads.
	var current reviewIssue
	if c.api(ctx, issueEndpoint(node, number), &current) != nil || current.Number != number || current.State == "" {
		return unavailable("Review Thread state")
	}
	if current.State == state {
		return nil
	}
	err := c.write(ctx, issueEndpoint(node, number), "PATCH", "state", state)
	if err != nil {
		var observed reviewIssue
		if c.api(ctx, issueEndpoint(node, number), &observed) == nil && observed.Number == number && observed.State == state {
			return nil
		}
		return unavailable("ambiguous Review Thread transition; inspect the thread before retrying")
	}
	var observed reviewIssue
	if c.api(ctx, issueEndpoint(node, number), &observed) != nil || observed.Number != number || observed.State != state {
		return unavailable("Review Thread transition read-back")
	}
	return nil
}

// admitOpen retains the milestone [05] admission path as the single source of
// truth. Automated Review consumes its public admission records afterwards.
func (c reviewCommand) admitOpen(ctx context.Context) error {
	node, err := c.currentReviewerNode(ctx)
	if err != nil {
		return err
	}
	if err = c.stationPreflight(ctx, node); err != nil {
		return err
	}
	var pages [][]reviewIssue
	endpoint := "repos/" + node.FullName + "/issues?state=all&per_page=100"
	if err = c.pages(ctx, endpoint, &pages); err != nil {
		return err
	}
	var issues []reviewIssue
	for _, page := range pages {
		for _, issue := range page {
			if issue.PullRequest == nil {
				issues = append(issues, issue)
			}
		}
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	if err = c.historyPreflight(ctx, node, issues); err != nil {
		return err
	}
	var problems []error
	for _, issue := range issues {
		if issue.State != "open" {
			continue
		}
		if err = c.admit(ctx, node, issue, issues); err != nil {
			problems = append(problems, fmt.Errorf("Issue #%d: %w", issue.Number, err))
		}
	}
	return errors.Join(problems...)
}

func (c reviewCommand) invalid(ctx context.Context, node reviewRepository, issue reviewIssue, reason string) error {
	if err := c.commentOnce(ctx, node.FullName, node.Owner.ID, issue.Number, invalidExplanation(reason)); err != nil {
		return err
	}
	return c.close(ctx, node.FullName, issue.Number)
}
func (c reviewCommand) requesterNode(ctx context.Context, user reviewUser) (reviewRepository, error) {
	var empty reviewRepository
	if user.ID <= 0 || !bareRepository.MatchString(user.Login) {
		return empty, errors.New("requester identity is invalid")
	}
	var root reviewRepository
	if err := c.api(ctx, fmt.Sprintf("repositories/%d", rootID), &root); err != nil {
		return empty, err
	}
	if reviewerNodeMembership(root) && root.ID == rootID && root.Owner.ID == user.ID {
		return root, nil
	}
	var pages [][]reviewRepository
	if err := c.pages(ctx, "users/"+user.Login+"/repos?type=owner&per_page=100", &pages); err != nil {
		return empty, err
	}
	for _, page := range pages {
		for _, item := range page {
			if !item.Fork || item.Owner.ID != user.ID || !repoName.MatchString(item.FullName) {
				continue
			}
			var repo reviewRepository
			if err := c.api(ctx, "repos/"+item.FullName, &repo); err != nil {
				return empty, err
			}
			if reviewerNodeMembership(repo) && repo.ID != rootID && repo.Owner.ID == user.ID {
				return repo, nil
			}
		}
	}
	return empty, nil
}
func (c reviewCommand) target(ctx context.Context, login, name string) (reviewRepository, bool, error) {
	var repo reviewRepository
	if !bareRepository.MatchString(login) || !bareRepository.MatchString(name) {
		return repo, false, nil
	}
	err := c.api(ctx, "repos/"+login+"/"+name, &repo)
	if err != nil {
		// A missing repository is an invalid request; rate limits and authorization failures abort processing.
		if strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "404 Not Found") {
			return repo, false, nil
		}
		return repo, false, err
	}
	if repo.ID <= 0 || !strings.EqualFold(repo.Owner.Login, login) || repo.Owner.ID <= 0 || repo.DefaultBranch == "" {
		return repo, false, nil
	}
	return repo, true, nil
}

func (c reviewCommand) admit(ctx context.Context, node reviewRepository, issue reviewIssue, history []reviewIssue) error {
	name, err := parseRequest(issue.Body, c.protocol)
	if err != nil {
		return c.invalid(ctx, node, issue, "Review Request payload is invalid: "+err.Error())
	}
	requester, err := c.requesterNode(ctx, issue.User)
	if err != nil {
		return err
	}
	if requester.ID == 0 {
		return c.invalid(ctx, node, issue, "the Issue author has no Personal Account-owned Network Root or direct fork Reviewer Node")
	}
	target, exists, err := c.target(ctx, issue.User.Login, name)
	if err != nil {
		return err
	}
	if !exists {
		return c.invalid(ctx, node, issue, "the requested repository does not exist under the Issue author's account")
	}
	if target.Owner.ID != issue.User.ID {
		return c.invalid(ctx, node, issue, "the requested repository is not owned by the Issue author")
	}
	if node.Owner.ID == target.Owner.ID {
		return c.invalid(ctx, node, issue, "a Reviewer cannot review their own repository")
	}
	canonical, found, err := c.findCanonical(ctx, node, target.ID, history)
	if err != nil {
		return err
	}
	if !found {
		record := admissionRecord{ReviewerNodeID: node.ID, TargetRepositoryID: target.ID, RepositoryName: name}
		return c.commentOnce(ctx, node.FullName, node.Owner.ID, issue.Number, encodeRecord(c.protocol.Admission.Marker, record))
	}
	if canonical.Number == issue.Number {
		return nil
	} // Pending canonical threads are scanned again on every invocation.
	previous, pending, accepted, err := c.lastBasis(ctx, node, canonical, target.ID, issue.Number)
	if err != nil {
		return err
	}
	if accepted {
		return c.finishRedirect(ctx, node, issue, canonical)
	}
	if previous == nil {
		return c.rejectDuplicate(ctx, node, issue)
	}
	currentTarget, err := c.targetHead(ctx, target)
	if err != nil {
		return err
	}
	currentPolicy, err := c.policyCommit(ctx, node)
	if err != nil {
		return err
	}
	// A second trigger with the same pending eligibility basis cannot request
	// the same re-review twice. Classification below still uses the last judgment.
	if pending != nil && strings.EqualFold(pending.EligibilityTargetCommit, currentTarget) && strings.EqualFold(pending.ReviewPolicyCommit, currentPolicy) {
		return c.rejectDuplicate(ctx, node, issue)
	}
	targetChanged := !strings.EqualFold(previous.TargetCommit, currentTarget)
	policyChanged := !strings.EqualFold(previous.ReviewPolicyCommit, currentPolicy)
	if !targetChanged && !policyChanged {
		return c.rejectDuplicate(ctx, node, issue)
	}
	reason := "TARGET_CHANGED"
	if policyChanged {
		reason = "POLICY_CHANGED"
	}
	if targetChanged && policyChanged {
		reason = "TARGET_AND_POLICY_CHANGED"
	}
	event := reviewEvent{Type: c.protocol.Event.LifecycleType, ReviewerNodeID: node.ID, TargetRepositoryID: target.ID, RequestIssueNumber: issue.Number, EligibilityTargetCommit: currentTarget, ReviewPolicyCommit: currentPolicy, Reason: reason}
	if err = c.commentOnce(ctx, node.FullName, node.Owner.ID, canonical.Number, encodeRecord(c.protocol.Event.Marker, event)); err != nil {
		return err
	}
	return c.finishRedirect(ctx, node, issue, canonical)
}
func (c reviewCommand) rejectDuplicate(ctx context.Context, node reviewRepository, issue reviewIssue) error {
	if err := c.commentOnce(ctx, node.FullName, node.Owner.ID, issue.Number, "NO_NEW_REVIEW_BASIS: This target has an existing review thread and no new eligible review basis. This request is closed."); err != nil {
		return err
	}
	return c.close(ctx, node.FullName, issue.Number)
}
func (c reviewCommand) finishRedirect(ctx context.Context, node reviewRepository, issue, canonical reviewIssue) error {
	if canonical.State != "open" {
		if err := c.reopen(ctx, node.FullName, canonical.Number); err != nil {
			return err
		}
	}
	body := fmt.Sprintf("Re-review request accepted. Further review will continue in the canonical review thread: https://github.com/%s/issues/%d", node.FullName, canonical.Number)
	if err := c.commentOnce(ctx, node.FullName, node.Owner.ID, issue.Number, body); err != nil {
		return err
	}
	return c.close(ctx, node.FullName, issue.Number)
}

func (c reviewCommand) findCanonical(ctx context.Context, node reviewRepository, targetID int64, history []reviewIssue) (reviewIssue, bool, error) {
	var zero reviewIssue
	for _, candidate := range history {
		comments, err := c.comments(ctx, node.FullName, candidate.Number)
		if err != nil {
			return zero, false, err
		}
		// An admission record binds one Issue to exactly one Target ID. A result
		// comment on the same Issue must never replace that recorded identity.
		var recorded *admissionRecord
		for _, comment := range comments {
			if comment.User.ID != node.Owner.ID {
				continue
			}
			var record admissionRecord
			if decodeRecord(comment.Body, c.protocol.Admission.Marker, &record) && record.ReviewerNodeID == node.ID && record.TargetRepositoryID > 0 {
				if recorded != nil && (recorded.TargetRepositoryID != record.TargetRepositoryID || !strings.EqualFold(recorded.RepositoryName, record.RepositoryName)) {
					return zero, false, fmt.Errorf("conflicting admission records on Issue #%d", candidate.Number)
				}
				copy := record
				recorded = &copy
			}
		}
		if recorded != nil {
			if recorded.TargetRepositoryID == targetID {
				return c.validateCanonical(ctx, candidate, recorded.RepositoryName)
			}
			continue
		}
		// Manual Review need not have a CLI admission record. Its judgment can
		// establish a thread only when the original request still resolves to the
		// same stable ID; an unverifiable old locator cannot be silently guessed.
		for _, comment := range comments {
			if comment.User.ID != node.Owner.ID {
				continue
			}
			var event reviewEvent
			if !decodeRecord(comment.Body, c.protocol.Event.Marker, &event) || !validEvent(event, c.protocol, node.ID, targetID) || event.Type == c.protocol.Event.LifecycleType {
				continue
			}
			validated, _, err := c.validateCanonical(ctx, candidate, "")
			if err != nil {
				return zero, false, err
			}
			name, _ := parseRequest(validated.Body, c.protocol)
			original, exists, err := c.target(ctx, validated.User.Login, name)
			if err != nil {
				return zero, false, err
			}
			if !exists {
				return zero, false, fmt.Errorf("cannot verify unrecorded canonical Issue #%d after its original Target locator disappeared", validated.Number)
			}
			if original.ID == targetID && original.Owner.ID == validated.User.ID && strings.EqualFold(event.TargetRepositoryFullName, validated.User.Login+"/"+name) {
				return validated, true, nil
			}
		}
	}
	return zero, false, nil
}

func (c reviewCommand) validateCanonical(ctx context.Context, issue reviewIssue, originalName string) (reviewIssue, bool, error) {
	name, err := parseRequest(issue.Body, c.protocol)
	if err != nil {
		return reviewIssue{}, false, fmt.Errorf("existing canonical Issue #%d has a damaged request payload: %w", issue.Number, err)
	}
	if originalName != "" && !strings.EqualFold(name, originalName) {
		return reviewIssue{}, false, fmt.Errorf("existing canonical Issue #%d no longer matches its original request payload", issue.Number)
	}
	member, err := c.requesterNode(ctx, issue.User)
	if err != nil {
		return reviewIssue{}, false, err
	}
	if member.ID == 0 {
		return reviewIssue{}, false, fmt.Errorf("existing canonical Issue #%d has invalid requester membership", issue.Number)
	}
	return issue, true, nil
}
func (c reviewCommand) lastBasis(ctx context.Context, node reviewRepository, canonical reviewIssue, targetID int64, trigger int) (*reviewEvent, *reviewEvent, bool, error) {
	comments, err := c.comments(ctx, node.FullName, canonical.Number)
	if err != nil {
		return nil, nil, false, err
	}
	var lastJudgment, pending *reviewEvent
	accepted := false
	for _, comment := range comments {
		if comment.User.ID != node.Owner.ID {
			continue
		}
		var event reviewEvent
		if !decodeRecord(comment.Body, c.protocol.Event.Marker, &event) || !validEvent(event, c.protocol, node.ID, targetID) {
			continue
		}
		if event.Type == c.protocol.Event.LifecycleType {
			if event.RequestIssueNumber == trigger {
				accepted = true
			}
			copy := event
			pending = &copy
			continue
		}
		copy := event
		lastJudgment = &copy
		pending = nil
	}
	return lastJudgment, pending, accepted, nil
}
func (c reviewCommand) targetHead(ctx context.Context, target reviewRepository) (string, error) {
	var branch struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	endpoint := "repos/" + target.FullName + "/branches/" + url.PathEscape(target.DefaultBranch)
	if err := c.api(ctx, endpoint, &branch); err != nil {
		return "", err
	}
	if !commitSHA.MatchString(branch.Commit.SHA) {
		return "", errors.New("invalid target default branch HEAD")
	}
	return branch.Commit.SHA, nil
}
func (c reviewCommand) policyCommit(ctx context.Context, node reviewRepository) (string, error) {
	var commits []struct {
		SHA string `json:"sha"`
	}
	endpoint := "repos/" + node.FullName + "/commits?path=" + url.QueryEscape(c.protocol.Event.PolicyPath) + "&sha=" + url.QueryEscape(node.DefaultBranch) + "&per_page=1"
	if err := c.api(ctx, endpoint, &commits); err != nil {
		return "", err
	}
	for _, commit := range commits {
		if commitSHA.MatchString(commit.SHA) {
			return commit.SHA, nil
		}
	}
	return "", errors.New("README.md has no valid last-modifying commit")
}
