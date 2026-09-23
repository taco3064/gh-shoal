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

	os.Exit(app.Run(os.Args[1:]))
}
