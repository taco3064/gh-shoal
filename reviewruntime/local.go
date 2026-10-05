package reviewruntime

import (
	"context"
	"fmt"
	"sort"
)

// NewLocal binds every effect to the currently authenticated local gh identity.
// Hosted consumers must instead call New with explicitly separated authorities.
func NewLocal(agent string, options Options) (*Runtime, error) {
	return newLocal(agent, options, systemRun)
}
func newLocal(agent string, options Options, run runner) (*Runtime, error) {
	if _, ok := agentCommands[agent]; !ok {
		return nil, fmt.Errorf("unsupported Local AI Agent %q", agent)
	}
	if options.Directory == "" {
		options.Directory = "."
	}
	transport := GitHubFunc(func(ctx context.Context, r Request) ([]byte, error) { return localAPI(ctx, run, r) })
	semantic := localAgent(agent, options.Directory, run)
	return New(Dependencies{Reads: transport, Lifecycle: transport, Personal: transport, Git: func(ctx context.Context, args ...string) ([]byte, error) { return run(ctx, "git", args...) }, Agent: semantic}, options)
}
func localAPI(ctx context.Context, run runner, r Request) ([]byte, error) {
	args := []string{"api"}
	if r.Method != "GET" {
		args = append(args, "--method", r.Method)
	}
	if r.Paginate {
		args = append(args, "--paginate", "--slurp")
	}
	args = append(args, r.Endpoint)
	var keys []string
	for key := range r.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-f", key+"="+r.Fields[key])
	}
	return run(ctx, "gh", args...)
}

func localAgent(agent, directory string, run runner) Agent {
	return AgentFunc(func(ctx context.Context, w SemanticWork) ([]byte, error) {
		node := reviewRepository{ID: w.ReviewerNodeID, FullName: w.ReviewerNodeFullName}
		var batch []pendingReview
		for _, item := range w.Items {
			batch = append(batch, pendingReview{issue: reviewIssue{Number: item.Issue, Body: item.Request}, target: reviewRepository{ID: item.TargetRepositoryID, FullName: item.TargetFullName}, policy: item.PolicyCommit, head: item.TargetCommit})
		}
		return (reviewCommand{run: run, dir: directory}).localAgentData(ctx, agent, node, batch)
	})
}
