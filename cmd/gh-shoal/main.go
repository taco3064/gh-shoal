package main

import (
	"os"

	"github.com/taco3064/gh-shoal/internal/cli"
)

var version = "dev"

func main() {
	app := cli.New(cli.Options{
		Name:    "gh shoal",
		Version: version,
		Out:     os.Stdout,
		Err:     os.Stderr,
	})

	if err := app.Register(cli.Command{Name: "init", Summary: "Repair and synchronize the Reviewer Node", Run: cli.NewInit()}); err != nil {
        panic(err)
    }
	os.Exit(app.Run(os.Args[1:]))
}
