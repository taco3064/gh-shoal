package reviewruntime_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/taco3064/gh-shoal/reviewruntime"
)

// This consumer uses only the public package, not CLI handlers or private types.
func TestExternalConsumerInvokesBothRuntimeOperations(t *testing.T) {
	for _, maintenance := range []bool{false, true} {
		t.Run(fmt.Sprint(maintenance), func(t *testing.T) {
			semanticCalls, effects := 0, 0
			jsonBytes := func(v any) ([]byte, error) { return json.Marshal(v) }
			reads := reviewruntime.GitHubFunc(func(_ context.Context, r reviewruntime.Request) ([]byte, error) {
				if r.Method != "GET" {
					t.Fatal("read authority received write")
				}
				switch {
				case r.Endpoint == "repos/reviewer/shoal-station":
					return []byte(`{"id":11,"full_name":"reviewer/shoal-station","default_branch":"main","has_issues":true,"fork":true,"parent":{"id":1379044983},"owner":{"id":1,"login":"reviewer","type":"User"}}`), nil
				case strings.Contains(r.Endpoint, "/git/ref/heads/"):
					return []byte(`{"object":{"sha":"1111111111111111111111111111111111111111"}}`), nil
				case strings.Contains(r.Endpoint, "/contents/"):
					path := "testdata/review-request.yml"
					if strings.Contains(r.Endpoint, "reviewer-summary.yml") {
						path = "testdata/reviewer-summary-current.yml"
					}
					b, err := os.ReadFile(path)
					if err != nil {
						return nil, err
					}
					return jsonBytes(map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString(b)})
				case strings.Contains(r.Endpoint, "/issues?"):
					if !r.Paginate {
						t.Fatal("incomplete collection request")
					}
					return []byte(`[[]]`), nil
				}
				return nil, fmt.Errorf("unexpected read: %+v", r)
			})
			mutation := reviewruntime.GitHubFunc(func(context.Context, reviewruntime.Request) ([]byte, error) {
				effects++
				return nil, fmt.Errorf("unexpected effect")
			})
			personal := reviewruntime.GitHubFunc(func(_ context.Context, r reviewruntime.Request) ([]byte, error) {
				if r.Endpoint == "user" && r.Method == "GET" {
					return []byte(`{"id":1,"type":"User"}`), nil
				}
				return mutation.Do(context.Background(), r)
			})
			runtime, err := reviewruntime.New(reviewruntime.Dependencies{Reads: reads, Lifecycle: mutation, Personal: personal, Agent: reviewruntime.AgentFunc(func(context.Context, reviewruntime.SemanticWork) ([]byte, error) {
				semanticCalls++
				return nil, fmt.Errorf("unexpected semantic work")
			}), Git: func(_ context.Context, args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				switch {
				case strings.Contains(joined, "status --porcelain"):
					return nil, nil
				case strings.Contains(joined, "remote get-url"):
					return []byte("https://github.com/reviewer/shoal-station.git"), nil
				case strings.HasSuffix(joined, " remote"):
					return []byte("origin"), nil
				}
				return nil, fmt.Errorf("unexpected Git: %s", joined)
			}}, reviewruntime.Options{Directory: "."})
			if err != nil {
				t.Fatal(err)
			}
			result := runtime.Review(context.Background())
			if maintenance {
				result = runtime.ReReview(context.Background())
			}
			if result.Status != "NO_CHANGES" || len(result.Faults) > 0 || effects != 0 || semanticCalls != 0 {
				t.Fatalf("external consumer failed: %+v", result)
			}
		})
	}
}
