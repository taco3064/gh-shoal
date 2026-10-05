// Package reviewruntime owns the deterministic Shoal Review and Re-review
// lifecycle. Hosts supply effects and semantic judgment, never lifecycle rules.
package reviewruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Request is an HTTP-shaped GitHub operation. Paginate requires a complete JSON
// array of pages, including an empty page when the collection is empty.
type Request struct {
	Method   string            `json:"method"`
	Endpoint string            `json:"endpoint"`
	Fields   map[string]string `json:"fields,omitempty"`
	Paginate bool              `json:"paginate,omitempty"`
}
type GitHub interface {
	Do(context.Context, Request) ([]byte, error)
}
type GitHubFunc func(context.Context, Request) ([]byte, error)

func (f GitHubFunc) Do(ctx context.Context, r Request) ([]byte, error) { return f(ctx, r) }

// APIError distinguishes observable absence from unavailable external state.
// Hosts must not turn permission, transport or incomplete-page failures into 404.
type APIError struct {
	Status  int
	Message string
}

func (e APIError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message) }

// Dependencies deliberately has no authority fallback. Personal handles /user,
// Star reads/writes and Reviewer-authored comments; Lifecycle handles Issue state
// transitions. Reads handles authoritative public repository/evidence reads.
// Git performs only local station inspection; it receives no GitHub request.
type Dependencies struct {
	Reads     GitHub
	Lifecycle GitHub
	Personal  GitHub
	Git       func(context.Context, ...string) ([]byte, error)
	Agent     Agent
}
type Options struct {
	Directory string
	Out       io.Writer
}
type WorkItem struct {
	Issue              int    `json:"issue"`
	Request            string `json:"request"`
	TargetRepositoryID int64  `json:"targetRepositoryId"`
	TargetFullName     string `json:"targetFullName"`
	TargetCommit       string `json:"targetCommit"`
	PolicyCommit       string `json:"policyCommit"`
}

// SemanticWork carries immutable basis references and bounded request work.
// Evidence acquisition belongs to the Agent adapter. It must use these exact
// references and must not execute untrusted Target code.
type SemanticWork struct {
	ReviewerNodeID       int64      `json:"reviewerNodeId"`
	ReviewerNodeFullName string     `json:"reviewerNodeFullName"`
	PolicyPath           string     `json:"policyPath"`
	Items                []WorkItem `json:"items"`
}

// Judge returns untrusted JSON: an array (or {"results": [...]}) of objects
// containing issue, verdict (PASS|FAIL), and comment. Errors are operational,
// never semantic FAIL. No mutation dependency is passed to the Agent.
type Agent interface {
	Judge(context.Context, SemanticWork) ([]byte, error)
}
type AgentFunc func(context.Context, SemanticWork) ([]byte, error)

func (f AgentFunc) Judge(ctx context.Context, w SemanticWork) ([]byte, error) { return f(ctx, w) }

type Fault struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func (f Fault) Error() string { return f.Code + ": " + f.Detail }

// Result preserves partial success. EffectAttempts counts attempted writes,
// not completed judgments or a promise that a write reached GitHub.
type Result struct {
	Status         string  `json:"status"`
	EffectAttempts int     `json:"effectAttempts"`
	Faults         []Fault `json:"faults,omitempty"`
}
type Runtime struct {
	dependencies Dependencies
	options      Options
}

func New(d Dependencies, o Options) (*Runtime, error) {
	if d.Reads == nil || d.Lifecycle == nil || d.Personal == nil || d.Git == nil || d.Agent == nil {
		return nil, Fault{"DEPENDENCY_UNAVAILABLE", "Reads, Lifecycle, Personal, Git and Agent are required; no ambient authority fallback is allowed"}
	}
	if o.Directory == "" {
		o.Directory = "."
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	return &Runtime{d, o}, nil
}
func (r *Runtime) Review(ctx context.Context) Result   { return r.execute(ctx, false) }
func (r *Runtime) ReReview(ctx context.Context) Result { return r.execute(ctx, true) }
func (r *Runtime) execute(ctx context.Context, maintenance bool) Result {
	p, err := loadReviewContract()
	if err != nil {
		return Result{Status: "REFUSED", Faults: faults(err)}
	}
	effects := new(int)
	c := reviewCommand{run: r.run, dir: r.options.Directory, out: r.options.Out, protocol: p, agent: r.dependencies.Agent, commentsCache: map[int][]reviewComment{}, effects: effects}
	// Share the effect counter across the core's value receivers so the machine
	// result observes the complete invocation.
	if maintenance {
		err = c.reReview(ctx, "")
	} else {
		err = c.automated(ctx, "")
	}
	result := Result{Status: "COMPLETED", EffectAttempts: *effects, Faults: faults(err)}
	if err != nil {
		result.Status = "REFUSED"
		if *effects > 0 {
			result.Status = "PARTIAL"
		}
	} else if *effects == 0 {
		result.Status = "NO_CHANGES"
	}
	return result
}
func faults(err error) []Fault {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var result []Fault
		for _, e := range joined.Unwrap() {
			result = append(result, faults(e)...)
		}
		return result
	}
	var f Fault
	if errors.As(err, &f) {
		return []Fault{f}
	}
	var d diagnostic
	if errors.As(err, &d) {
		return []Fault{{d.Class, d.Surface + ". " + d.Action}}
	}
	return []Fault{{"EXECUTION_FAILED", err.Error()}}
}
func semanticWork(node reviewRepository, batch []pendingReview) SemanticWork {
	work := SemanticWork{ReviewerNodeID: node.ID, ReviewerNodeFullName: node.FullName, PolicyPath: "README.md"}
	for _, item := range batch {
		work.Items = append(work.Items, WorkItem{item.issue.Number, item.issue.Body, item.target.ID, item.target.FullName, item.head, item.policy})
	}
	return work
}
func (r *Runtime) run(ctx context.Context, program string, args ...string) ([]byte, error) {
	if program == "git" {
		return r.dependencies.Git(ctx, args...)
	}
	if program == "gh" && len(args) == 2 && args[0] == "auth" && args[1] == "status" {
		// Hosted authority is verified by the subsequent Personal /user read and
		// stable owner binding, not local gh authentication or a bot token.
		return nil, nil
	}
	request, err := parseAPI(program, args)
	if err != nil {
		return nil, err
	}
	authority := r.dependencies.Reads
	if request.Endpoint == "user" || strings.HasPrefix(request.Endpoint, "user/starred/") || request.Method != "GET" && strings.HasSuffix(request.Endpoint, "/comments") {
		authority = r.dependencies.Personal
	} else if request.Method != "GET" {
		authority = r.dependencies.Lifecycle
	}
	data, err := authority.Do(ctx, request)
	return data, err
}
func parseAPI(program string, args []string) (Request, error) {
	r := Request{Method: "GET"}
	if program != "gh" || len(args) == 0 || args[0] != "api" {
		return r, fmt.Errorf("unsupported runtime operation %s", program)
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--method":
			i++
			if i >= len(args) {
				return r, errors.New("missing HTTP method")
			}
			r.Method = args[i]
		case "--paginate":
			r.Paginate = true
		case "--slurp":
		case "-f":
			i++
			if i >= len(args) {
				return r, errors.New("missing field")
			}
			k, v, ok := strings.Cut(args[i], "=")
			if !ok {
				return r, errors.New("invalid field")
			}
			if r.Fields == nil {
				r.Fields = map[string]string{}
			}
			r.Fields[k] = v
		default:
			if r.Endpoint != "" {
				return r, errors.New("multiple endpoints")
			}
			r.Endpoint = args[i]
		}
	}
	if r.Endpoint == "" {
		return r, errors.New("missing endpoint")
	}
	return r, nil
}
