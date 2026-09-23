package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRootHelpWithoutCommands(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer
	app := New(Options{Name: "gh shoal", Version: "test", Out: &out, Err: &err})

	if code := app.Run(nil); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	got := out.String()
	for _, want := range []string{"Shoal GitHub CLI extension", "Usage:", "gh shoal [command]", "--help", "--version"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected help to contain %q, got:\n%s", want, got)
		}
	}

	for _, deferred := range []string{"init", "review", "re-review"} {
		if strings.Contains(got, deferred) {
			t.Fatalf("deferred command %q must not appear in shipped root help:\n%s", deferred, got)
		}
	}

	if err.String() != "" {
		t.Fatalf("expected stderr to be empty, got %q", err.String())
	}
}

func TestHelpFlagSucceeds(t *testing.T) {
	var out bytes.Buffer
	app := New(Options{Name: "gh shoal", Version: "test", Out: &out})

	if code := app.Run([]string{"--help"}); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("expected usage output, got:\n%s", out.String())
	}
}

func TestUnknownDeferredCommandIsNotAPlaceholder(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer
	app := New(Options{Name: "gh shoal", Version: "test", Out: &out, Err: &err})

	if code := app.Run([]string{"init"}); code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}

	got := err.String()
	if !strings.Contains(got, `unknown command "init"`) {
		t.Fatalf("expected unknown command error, got:\n%s", got)
	}
	if strings.Contains(strings.ToLower(got), "not implemented") {
		t.Fatalf("deferred command must not ship as NotImplemented placeholder:\n%s", got)
	}
}

func TestCommandCanBeRegisteredAndDispatched(t *testing.T) {
	var out bytes.Buffer
	app := New(Options{Name: "gh shoal", Version: "test", Out: &out})

	called := false
	err := app.Register(Command{
		Name:    "probe",
		Summary: "test-only command",
		Run: func(_ context.Context, args []string) error {
			called = true
			if len(args) != 1 || args[0] != "ok" {
				t.Fatalf("unexpected args: %#v", args)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("register command: %v", err)
	}

	if code := app.Run([]string{"probe", "ok"}); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatal("expected registered command to be dispatched")
	}
}

func TestRegisteredCommandAppearsInHelp(t *testing.T) {
	var out bytes.Buffer
	app := New(Options{Name: "gh shoal", Version: "test", Out: &out})

	err := app.Register(Command{
		Name:    "probe",
		Summary: "test-only command",
		Run: func(context.Context, []string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("register command: %v", err)
	}

	if code := app.Run([]string{"--help"}); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	got := out.String()
	if !strings.Contains(got, "probe") {
		t.Fatalf("expected registered command in help, got:\n%s", got)
	}
}

func TestCommandErrorReturnsFailure(t *testing.T) {
	var errOut bytes.Buffer
	app := New(Options{Name: "gh shoal", Version: "test", Err: &errOut})

	err := app.Register(Command{
		Name:    "probe",
		Summary: "test-only command",
		Run: func(context.Context, []string) error {
			return errors.New("probe failed")
		},
	})
	if err != nil {
		t.Fatalf("register command: %v", err)
	}

	if code := app.Run([]string{"probe"}); code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(errOut.String(), "probe failed") {
		t.Fatalf("expected command error on stderr, got %q", errOut.String())
	}
}
