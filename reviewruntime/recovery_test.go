package reviewruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAmbiguousWritesObserveCommittedStateWithoutReplay(t *testing.T) {
	for _, stage := range []string{"admission", "judgment", "star", "close"} {
		t.Run(stage, func(t *testing.T) {
			f := fixture()
			f.addIssue(1, reviewBody("project"), f.requester.Owner)
			f.agentOutput = `[{"issue":1,"verdict":"PASS","comment":"Public evidence"}]`
			c := f.automatedCommand(t)
			base := c.run
			lost := false
			c.run = func(ctx context.Context, program string, args ...string) ([]byte, error) {
				b, err := base(ctx, program, args...)
				command := strings.Join(args, " ")
				match := stage == "admission" && strings.Contains(command, "body=## Request admitted") || stage == "judgment" && strings.Contains(command, "body=## Review Result:") || stage == "star" && strings.Contains(command, "--method PUT user/starred/") || stage == "close" && strings.Contains(command, "state=closed")
				if !lost && err == nil && match {
					lost = true
					return nil, errors.New("response lost after server committed")
				}
				return b, err
			}
			if err := c.execute(context.Background(), []string{"--agent", "codex"}); err != nil {
				t.Fatal(err)
			}
			if !lost || f.state(1) != "closed" || len(f.bodies(1)) != 2 || f.agentCalls != 1 || !f.starred {
				t.Fatalf("did not recover exact committed state: %+v", f)
			}
			if err := c.execute(context.Background(), []string{"--agent", "codex"}); err != nil {
				t.Fatal(err)
			}
			if len(f.bodies(1)) != 2 || f.agentCalls != 1 {
				t.Fatal("retry duplicated semantic judgment")
			}
		})
	}
}

func TestAmbiguousCommentWithoutObservableProofRefusesBoundedly(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "unreadable"}[committed], func(t *testing.T) {
			f := fixture()
			f.addIssue(1, reviewBody("project"), f.requester.Owner)
			c := f.command(t)
			base := c.run
			writes := 0
			after := false
			c.run = func(ctx context.Context, program string, args ...string) ([]byte, error) {
				command := strings.Join(args, " ")
				if strings.Contains(command, "--method POST") {
					writes++
					after = true
					if committed {
						_, _ = base(ctx, program, args...)
					}
					return nil, errors.New("ambiguous write")
				}
				if committed && after && strings.Contains(command, "/comments?") {
					return nil, errors.New("read unavailable")
				}
				return base(ctx, program, args...)
			}
			err := c.commentOnce(context.Background(), f.node.FullName, f.node.Owner.ID, 1, "formal exact body")
			if err == nil || !strings.Contains(err.Error(), "EXTERNAL_STATE_UNAVAILABLE") || writes != 1 {
				t.Fatalf("error %v writes %d", err, writes)
			}
		})
	}
}

func TestAmbiguousReReviewLifecycleAppendDoesNotDuplicate(t *testing.T) {
	f := fixture()
	f.addIssue(1, reviewBody("project"), f.requester.Owner)
	f.issues[0].State = "closed"
	f.comments[1] = []reviewComment{admissionFor(t, f.target, "project"), priorJudgment(t, f.target, "PASS")}
	f.addIssue(2, reviewBody("project"), f.requester.Owner)
	f.head = testSHA2
	c := f.command(t)
	base := c.run
	lost := false
	c.run = func(ctx context.Context, program string, args ...string) ([]byte, error) {
		b, err := base(ctx, program, args...)
		if !lost && err == nil && strings.Contains(strings.Join(args, " "), `"type":"RE_REVIEW_REQUESTED"`) {
			lost = true
			return nil, errors.New("acknowledgement lost")
		}
		return b, err
	}
	if err := c.admitOpen(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !lost || len(f.bodies(1)) != 3 || f.state(1) != "open" || f.state(2) != "closed" {
		t.Fatal("lifecycle did not converge")
	}
	if err := c.admitOpen(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.bodies(1)) != 3 || len(f.bodies(2)) != 1 {
		t.Fatal("duplicate lifecycle on retry")
	}
}
