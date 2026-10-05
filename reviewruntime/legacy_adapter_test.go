package reviewruntime

import (
	"context"
	"errors"
	"fmt"
)

func (c reviewCommand) execute(ctx context.Context, args []string) error {
	if len(args) != 2 || args[0] != "--agent" {
		return errors.New("usage: gh shoal review --agent <agent>")
	}
	if _, ok := agentCommands[args[1]]; !ok {
		return fmt.Errorf("unsupported Local AI Agent %q", args[1])
	}
	c.agent = localAgent(args[1], c.dir, c.run)
	return c.automated(ctx, args[1])
}

func (c reviewCommand) executeReReview(ctx context.Context, args []string) error {
	if len(args) != 2 || args[0] != "--agent" {
		return errors.New("usage: gh shoal re-review --agent <agent>")
	}
	if _, ok := agentCommands[args[1]]; !ok {
		return fmt.Errorf("unsupported Local AI Agent %q", args[1])
	}
	c.agent = localAgent(args[1], c.dir, c.run)
	return c.reReview(ctx, args[1])
}

func (c reviewCommand) withLocalAgent(name string) reviewCommand {
	c.agent = localAgent(name, c.dir, c.run)
	return c
}
