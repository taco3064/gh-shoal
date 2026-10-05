package reviewruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func (c reviewCommand) localAgentData(ctx context.Context, agent string, node reviewRepository, batch []pendingReview) ([]byte, error) {
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
	fmt.Fprintf(&prompt, "Review the following Shoal Review Threads in ascending Issue order for Reviewer Node https://github.com/%s.\n", node.FullName)
	prompt.WriteString("The Review criteria are defined in this Reviewer Node's README.md. Read README.md first and use it as the authoritative Review Policy. The extension has already resolved each Target Repository deterministically; use the explicit Target URL listed for each Issue and do not derive Target identity from mutable Issue text. Inspect public repository evidence and make an independent PASS or FAIL judgment for each Target. Do not execute Target Repository-provided code merely to validate eligibility.\n")
	prompt.WriteString("Do not comment on or close an Issue, Star or Unstar a repository, or create or mutate other GitHub state as part of the Review. You may only write the structured result file. Keep each review explanation concise and readable. The review explanation must not exceed 3,000 characters. Focus on decisive evidence and reasoning.\n")
	fmt.Fprintf(&prompt, "Write one JSON object per Issue to %s, as an array (or {\"results\": [...]}); each object must contain only issue (integer), verdict (PASS or FAIL), and comment (repository-specific explanation). Do not use stdout as the result.\n", resultFile)
	for _, item := range batch {
		fmt.Fprintf(&prompt, "- Issue: https://github.com/%s/issues/%d | Target: https://github.com/%s\n", node.FullName, item.issue.Number, item.target.FullName)
	}
	adapter := agentCommands[agent]
	args := append(append([]string{}, adapter.arguments...), prompt.String())
	_, invocationErr := c.run(ctx, adapter.program, args...)
	if invocationErr != nil {
		return nil, fmt.Errorf("%s Agent process failed: %w", agent, invocationErr)
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
	return data, nil
}
