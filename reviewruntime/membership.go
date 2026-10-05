package reviewruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// reviewerNodeMembership uses GitHub's stable repository and owner IDs. The
// canonical Root is also a Reviewer Node only while personally owned.
func reviewerNodeMembership(node reviewRepository) bool {
	if node.ID <= 0 || node.Owner.ID <= 0 || node.Owner.Type != "User" {
		return false
	}
	if node.ID == rootID {
		return true
	}
	return node.Fork && node.Parent != nil && node.Parent.ID == rootID
}

func (c reviewCommand) currentReviewerNode(ctx context.Context) (reviewRepository, error) {
	var empty reviewRepository
	if _, err := c.run(ctx, "gh", "auth", "status"); err != nil {
		return empty, fmt.Errorf("gh auth is required: %w", err)
	}
	var viewer reviewUser
	if err := c.api(ctx, "user", &viewer); err != nil {
		return empty, err
	}
	if viewer.ID <= 0 {
		return empty, errors.New("authenticated Reviewer has no GitHub user ID")
	}
	remotes, err := c.run(ctx, "git", "-C", c.dir, "remote")
	if err != nil {
		return empty, err
	}
	var node reviewRepository
	for _, remote := range strings.Fields(string(remotes)) {
		raw, e := c.run(ctx, "git", "-C", c.dir, "remote", "get-url", remote)
		if e != nil {
			return empty, e
		}
		locator, e := repositoryLocator(strings.TrimSpace(string(raw)))
		if e != nil {
			continue
		}
		var candidate reviewRepository
		if e = c.api(ctx, "repos/"+locator, &candidate); e != nil {
			return empty, e
		}
		if reviewerNodeMembership(candidate) && candidate.Owner.ID == viewer.ID {
			if node.ID != 0 && node.ID != candidate.ID {
				return empty, errors.New("multiple Reviewer Node remotes")
			}
			node = candidate
		}
	}
	if node.ID == 0 || !repoName.MatchString(node.FullName) {
		return empty, errors.New("run from a Personal Account-owned Network Root or direct Reviewer Node fork owned by the authenticated Reviewer")
	}
	return node, nil
}
