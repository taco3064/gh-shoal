package reviewruntime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type reReviewCandidate struct {
	pendingReview
	previous         reviewEvent
	pendingLifecycle bool
}

func (c reviewCommand) reReview(ctx context.Context, agent string) error {
	if c.effects == nil {
		c.effects = new(int)
	}
	status, err := c.run(ctx, "git", "-C", c.dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("cannot inspect Station working tree: %w", err)
	}
	if len(status) != 0 {
		return errors.New("re-review requires a clean Station index and working tree")
	}

	node, err := c.resolveReviewerNode(ctx)
	if err != nil {
		return err
	}

	if err = c.stationPreflight(ctx, node); err != nil {
		return err
	}
	var pages [][]reviewIssue
	if err = c.pages(ctx, "repos/"+node.FullName+"/issues?state=all&per_page=100", &pages); err != nil {
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
	queue := make([]reReviewCandidate, 0)
	var problems []error
	for _, issue := range issues {
		candidate, ok, e := c.inspectReReviewCandidate(ctx, node, issue)
		if e != nil {
			problems = append(problems, fmt.Errorf("Issue #%d: %w", issue.Number, e))
			continue
		}
		if !ok {
			continue
		}

		// The only open semantic Re-review source is an already-admitted pending
		// RE_REVIEW_REQUESTED lifecycle. An open thread with no pending lifecycle
		// can be a retry after a completed Judgment whose close failed; converge
		// that completed state without inventing a new semantic Review.
		if candidate.issue.State == "open" && !candidate.pendingLifecycle {
			if e = c.reconcilePreviousJudgment(ctx, node, candidate); e != nil {
				problems = append(problems, fmt.Errorf("Issue #%d: %w", issue.Number, e))
			}
			continue
		}

		candidate.head, e = c.targetHead(ctx, candidate.target)
		if e == nil {
			candidate.policy, e = c.policyCommit(ctx, node)
		}
		if e != nil {
			problems = append(problems, fmt.Errorf("Issue #%d: cannot observe Review basis: %w", issue.Number, e))
			continue
		}

		if classifyReviewBasis(candidate.previous, candidate.head, candidate.policy) == "NO_NEW_REVIEW_BASIS" {
			if e = c.reconcilePreviousJudgment(ctx, node, candidate); e != nil {
				problems = append(problems, fmt.Errorf("Issue #%d: %w", issue.Number, e))
			}
			continue
		}
		queue = append(queue, candidate)
	}

	for first := 0; first < len(queue); first += 5 {
		last := first + 5
		if last > len(queue) {
			last = len(queue)
		}
		batch := queue[first:last]
		ready := make([]pendingReview, 0, len(batch))
		readyByIssue := make(map[int]reReviewCandidate, len(batch))
		for _, candidate := range batch {
			candidate.head, err = c.targetHead(ctx, candidate.target)
			if err == nil {
				candidate.policy, err = c.policyCommit(ctx, node)
			}
			if err != nil {
				problems = append(problems, fmt.Errorf("Issue #%d: cannot snapshot Review basis at judgment start: %w", candidate.issue.Number, err))
				continue
			}
			if classifyReviewBasis(candidate.previous, candidate.head, candidate.policy) == "NO_NEW_REVIEW_BASIS" {
				if e := c.reconcilePreviousJudgment(ctx, node, candidate); e != nil {
					problems = append(problems, fmt.Errorf("Issue #%d: %w", candidate.issue.Number, e))
				}
				continue
			}
			ready = append(ready, candidate.pendingReview)
			readyByIssue[candidate.issue.Number] = candidate
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
			if _, ok = readyByIssue[item.issue.Number]; !ok {
				problems = append(problems, fmt.Errorf("Issue #%d: Re-review candidate changed unexpectedly", item.issue.Number))
				continue
			}
			eventType := "RE_REVIEWED"
			if result.Verdict == "FAIL" {
				eventType = "STAR_REVOKED"
			}
			if e = c.completeJudgment(ctx, node, item, result, eventType); e != nil {
				problems = append(problems, fmt.Errorf("Issue #%d: %w", item.issue.Number, e))
			}
		}
	}
	if len(problems) == 0 && *c.effects == 0 {
		report(c.out, "NO_CHANGES", "Review Basis and endorsement state already converged", "No new judgment or lifecycle mutation was needed.")
	}
	return errors.Join(problems...)
}

func (c reviewCommand) resolveReviewerNode(ctx context.Context) (reviewRepository, error) {
	return c.currentReviewerNode(ctx)
}

func (c reviewCommand) inspectReReviewCandidate(ctx context.Context, node reviewRepository, issue reviewIssue) (reReviewCandidate, bool, error) {
	var zero reReviewCandidate
	comments, err := c.comments(ctx, node.FullName, issue.Number)
	if err != nil {
		return zero, false, err
	}

	var admittedTargetID int64
	var admittedRepositoryName string
	var eventTargetID int64
	var lastJudgment *reviewEvent
	var pending *reviewEvent
	for _, comment := range comments {
		if comment.User.ID != node.Owner.ID {
			continue
		}
		var record admissionRecord
		if decodeRecord(comment.Body, c.protocol.Admission.Marker, &record) && record.ReviewerNodeID == node.ID && record.TargetRepositoryID > 0 {
			if admittedTargetID != 0 && (admittedTargetID != record.TargetRepositoryID || !strings.EqualFold(admittedRepositoryName, record.RepositoryName)) {
				return zero, false, errors.New("conflicting admission records")
			}
			admittedTargetID = record.TargetRepositoryID
			admittedRepositoryName = record.RepositoryName
		}
		var event reviewEvent
		if !decodeRecord(comment.Body, c.protocol.Event.Marker, &event) || event.ReviewerNodeID != node.ID || event.TargetRepositoryID <= 0 {
			continue
		}
		if !validEvent(event, c.protocol, node.ID, event.TargetRepositoryID) {
			continue
		}
		if eventTargetID != 0 && eventTargetID != event.TargetRepositoryID {
			return zero, false, errors.New("conflicting formal Review Event Target identities")
		}
		eventTargetID = event.TargetRepositoryID
		if isJudgmentType(event.Type, c.protocol) && event.ActualStarState != nil && *event.ActualStarState == (event.Verdict == "PASS") {
			copy := event
			lastJudgment = &copy
			pending = nil
			continue
		}
		if event.Type == c.protocol.Event.LifecycleType {
			copy := event
			pending = &copy
		}
	}

	if lastJudgment == nil {
		return zero, false, nil
	}
	targetID := lastJudgment.TargetRepositoryID
	if admittedTargetID != 0 && admittedTargetID != targetID || eventTargetID != 0 && eventTargetID != targetID {
		return zero, false, errors.New("Review Thread Target identity is inconsistent")
	}

	// Reuse the delivered canonical-thread trust boundary. Recorded admission
	// binds the Issue body to its original repository name; manual formal Review
	// without a CLI admission record must still prove requester membership and
	// bind the original Issue locator to the same stable Target Repository ID.
	if admittedTargetID != 0 {
		if _, _, err := c.validateCanonical(ctx, issue, admittedRepositoryName); err != nil {
			return zero, false, err
		}
	} else {
		validated, _, err := c.validateCanonical(ctx, issue, "")
		if err != nil {
			return zero, false, err
		}
		name, err := parseRequest(validated.Body, c.protocol)
		if err != nil {
			return zero, false, err
		}
		original, exists, err := c.target(ctx, validated.User.Login, name)
		if err != nil {
			return zero, false, err
		}
		if !exists || original.ID != targetID || original.Owner.ID != validated.User.ID {
			return zero, false, errors.New("manual canonical Review Thread cannot be bound to its stable Target identity")
		}
	}

	var target reviewRepository
	if err := c.api(ctx, fmt.Sprintf("repositories/%d", targetID), &target); err != nil {
		return zero, false, fmt.Errorf("cannot resolve stable Target Repository identity: %w", err)
	}
	if target.ID != targetID || !repoName.MatchString(target.FullName) || target.DefaultBranch == "" {
		return zero, false, errors.New("resolved Target Repository identity is invalid")
	}
	return reReviewCandidate{pendingReview: pendingReview{issue: issue, target: target}, previous: *lastJudgment, pendingLifecycle: pending != nil}, true, nil
}

func classifyReviewBasis(previous reviewEvent, targetCommit, policyCommit string) string {
	targetChanged := !strings.EqualFold(previous.TargetCommit, targetCommit)
	policyChanged := !strings.EqualFold(previous.ReviewPolicyCommit, policyCommit)
	switch {
	case targetChanged && policyChanged:
		return "TARGET_AND_POLICY_CHANGED"
	case targetChanged:
		return "TARGET_CHANGED"
	case policyChanged:
		return "POLICY_CHANGED"
	default:
		return "NO_NEW_REVIEW_BASIS"
	}
}

func (c reviewCommand) reconcilePreviousJudgment(ctx context.Context, node reviewRepository, candidate reReviewCandidate) error {
	if _, err := c.convergeStar(ctx, candidate.target, candidate.previous.Verdict); err != nil {
		return fmt.Errorf("cannot reconcile endorsement state: %w", err)
	}
	if candidate.issue.State == "open" {
		if err := c.close(ctx, node.FullName, candidate.issue.Number); err != nil {
			return fmt.Errorf("cannot close Re-review Thread with no new Review basis: %w", err)
		}
	}
	return nil
}

func isJudgmentType(eventType string, p reviewContract) bool {
	for _, judgmentType := range p.Event.JudgmentTypes {
		if eventType == judgmentType {
			return true
		}
	}
	return false
}
