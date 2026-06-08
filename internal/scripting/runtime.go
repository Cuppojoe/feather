// Package scripting runs user-authored JavaScript hooks around HTTP
// requests. Each script execution gets a fresh sobek runtime (cheap, fully
// isolated), a hard wall-clock timeout, and a `feather` global that exposes
// the request, response, context, console, and a tiny synchronous HTTP
// wrapper. The console is also available as the standard top-level `console`
// global (console.log/warn/error), whose output flows to the Console tab in
// the request details panel. Scripts never escape this sandbox.
package scripting

import (
	"context"
	"fmt"
	"time"

	"github.com/grafana/sobek"

	"github.com/cuppojoe/feather/internal/http"
	"github.com/cuppojoe/feather/internal/models"
	"github.com/cuppojoe/feather/internal/openapi"
)

// Phase identifies whether a script runs before or after the HTTP call.
type Phase string

const (
	PhasePre  Phase = "pre"
	PhasePost Phase = "post"
)

// Scope identifies which configuration level a script came from.
type Scope string

const (
	ScopeProfile   Scope = "profile"
	ScopeTag       Scope = "tag"
	ScopeOperation Scope = "operation"
)

// DefaultTimeout is the hard cap on any single script run when the overlay
// doesn't specify one of its own.
const DefaultTimeout = 5 * time.Second

// Env is everything a single script run can see or touch.
type Env struct {
	Phase    Phase
	Scope    Scope
	Profile  string            // profile name
	Tag      string            // current endpoint tag, when relevant
	Endpoint *openapi.Endpoint // never modified by scripts
	Request  *http.Request     // mutable in pre; readable in post
	Response *http.Response    // nil in pre; mutable in post
	Context  *models.Context   // get/set/delete is mirrored back here
}

// LogLevel is the severity of a console.* call.
type LogLevel string

const (
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// LogEntry is a single line written by feather.console.
type LogEntry struct {
	Phase   Phase
	Scope   Scope
	Tag     string // for ScopeTag
	Level   LogLevel
	Message string
}

// Result reports what happened during one script run.
type Result struct {
	Phase   Phase
	Scope   Scope
	Tag     string
	Logs    []LogEntry
	Aborted bool   // true when the script called feather.abort()
	Reason  string // abort reason
	Err     error  // parse / runtime error (not a feather.abort)
	Took    time.Duration
}

// abortSignal is a sentinel embedded in the JS error path so the host can
// distinguish "script asked to abort the request" from "script threw".
type abortSignal struct{ reason string }

func (a *abortSignal) Error() string { return "script aborted: " + a.reason }

// Run executes one script. It returns a Result rather than an error so the
// host can collect logs even on failure.
func Run(parent context.Context, code string, env *Env, timeout time.Duration) Result {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	res := Result{Phase: env.Phase, Scope: env.Scope, Tag: env.Tag}
	if code == "" {
		return res
	}

	start := time.Now()
	defer func() { res.Took = time.Since(start) }()

	rt := sobek.New()
	rt.SetFieldNameMapper(sobek.TagFieldNameMapper("js", true))

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	// Wall-clock interrupt — fires from another goroutine. Sobek processes
	// the interrupt at the next opcode boundary.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				rt.Interrupt(fmt.Sprintf("script timeout (%s)", timeout))
			} else {
				rt.Interrupt(ctx.Err())
			}
		case <-done:
		}
	}()

	install(rt, ctx, env, &res)

	if _, err := rt.RunString(code); err != nil {
		var ab *abortSignal
		if ok := errorsAs(err, &ab); ok {
			res.Aborted = true
			res.Reason = ab.reason
			return res
		}
		res.Err = err
		return res
	}

	// Read back mutations the script made to feather.request /
	// feather.response so they propagate to the Go structs.
	syncRequestBack(rt, env)
	syncResponseBack(rt, env)
	return res
}

// errorsAs is errors.As without pulling in the standard package here; it
// unwraps sobek's Exception type, whose .Value() carries the original Go
// error when one was thrown via panic.
func errorsAs(err error, target **abortSignal) bool {
	type wrapper interface{ Value() sobek.Value }
	for cur := err; cur != nil; {
		if w, ok := cur.(wrapper); ok {
			if v := w.Value(); v != nil {
				if exp, ok := v.Export().(*abortSignal); ok {
					*target = exp
					return true
				}
			}
		}
		u, ok := cur.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		cur = u.Unwrap()
	}
	return false
}
