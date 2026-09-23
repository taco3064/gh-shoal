package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

type Handler func(context.Context, []string) error

type Command struct {
	Name        string
	Summary     string
	Description string
	Run         Handler
}

type Options struct {
	Name    string
	Version string
	Out     io.Writer
	Err     io.Writer
}

type CLI struct {
	name     string
	version  string
	out      io.Writer
	err      io.Writer
	commands map[string]Command
}

func New(options Options) *CLI {
	name := strings.TrimSpace(options.Name)
	if name == "" {
		name = "gh shoal"
	}

	version := strings.TrimSpace(options.Version)
	if version == "" {
		version = "dev"
	}

	out := options.Out
	if out == nil {
		out = io.Discard
	}

	err := options.Err
	if err == nil {
		err = io.Discard
	}

	return &CLI{
		name:     name,
		version:  version,
		out:      out,
		err:      err,
		commands: map[string]Command{},
	}
}

func (app *CLI) Register(command Command) error {
	name := strings.TrimSpace(command.Name)
	if name == "" {
		return fmt.Errorf("command name is required")
	}
	if strings.Contains(name, " ") {
		return fmt.Errorf("command name %q must not contain spaces", name)
	}
	if command.Run == nil {
		return fmt.Errorf("command %q requires a handler", name)
	}
	if _, exists := app.commands[name]; exists {
		return fmt.Errorf("command %q is already registered", name)
	}

	command.Name = name
	app.commands[name] = command
	return nil
}

func (app *CLI) Run(args []string) int {
	code, err := app.run(context.Background(), args)
	if err != nil {
		fmt.Fprintln(app.err, err)
	}
	return code
}

func (app *CLI) run(ctx context.Context, args []string) (int, error) {
	if len(args) == 0 {
		app.printHelp(app.out)
		return 0, nil
	}

	switch args[0] {
	case "-h", "--help", "help":
		app.printHelp(app.out)
		return 0, nil
	case "--version":
		fmt.Fprintf(app.out, "%s %s\n", app.name, app.version)
		return 0, nil
	}

	command, exists := app.commands[args[0]]
	if !exists {
		fmt.Fprintf(app.err, "unknown command %q\n\n", args[0])
		app.printHelp(app.err)
		return 1, nil
	}

	if err := command.Run(ctx, args[1:]); err != nil {
		return 1, err
	}

	return 0, nil
}

func (app *CLI) printHelp(w io.Writer) {
	fmt.Fprintf(w, "Shoal GitHub CLI extension\n\n")
	fmt.Fprintf(w, "Usage:\n  %s [command]\n\n", app.name)
	fmt.Fprintf(w, "Options:\n  -h, --help     Show help\n  --version      Show version\n")

	if len(app.commands) == 0 {
		fmt.Fprintln(w)
		return
	}

	fmt.Fprintln(w, "\nCommands:")
	for _, command := range app.sortedCommands() {
		fmt.Fprintf(w, "  %-12s %s\n", command.Name, command.Summary)
	}
	fmt.Fprintln(w)
}

func (app *CLI) sortedCommands() []Command {
	commands := make([]Command, 0, len(app.commands))
	for _, command := range app.commands {
		commands = append(commands, command)
	}
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Name < commands[j].Name
	})
	return commands
}
