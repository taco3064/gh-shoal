package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/taco3064/gh-shoal/reviewruntime"
)

func NewInit() Handler     { return reviewruntime.NewInit() }
func NewReview() Handler   { return localReview(false) }
func NewReReview() Handler { return localReview(true) }
func localReview(maintenance bool) Handler {
	return func(ctx context.Context, args []string) error {
		name := "review"
		if maintenance {
			name = "re-review"
		}
		if len(args) != 2 || args[0] != "--agent" {
			return fmt.Errorf("usage: gh shoal %s --agent <agent>", name)
		}
		runtime, err := reviewruntime.NewLocal(args[1], reviewruntime.Options{Directory: ".", Out: os.Stdout})
		if err != nil {
			return err
		}
		var result reviewruntime.Result
		if maintenance {
			result = runtime.ReReview(ctx)
		} else {
			result = runtime.Review(ctx)
		}
		var problems []error
		for _, fault := range result.Faults {
			problems = append(problems, fault)
		}
		return errors.Join(problems...)
	}
}
