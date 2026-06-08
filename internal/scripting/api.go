package scripting

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"strings"

	"github.com/grafana/sobek"

	"github.com/cuppojoe/feather/internal/http"
)

// install builds the `feather` root object and registers it as the single
// global. Everything the script can do flows through it.
func install(rt *sobek.Runtime, ctx context.Context, env *Env, res *Result) {
	root := rt.NewObject()

	// State.
	root.Set("request", buildRequest(rt, env))
	root.Set("response", buildResponse(rt, env))
	root.Set("context", buildContext(rt, env))

	// console is available both as feather.console and as the standard
	// top-level `console` global, so scripts can use the familiar
	// console.log/warn/error(...) and have the lines land in the Console tab.
	console := buildConsole(rt, env, res)
	root.Set("console", console)

	// Control flow.
	root.Set("abort", func(call sobek.FunctionCall) sobek.Value {
		reason := ""
		if len(call.Arguments) > 0 {
			reason = call.Arguments[0].String()
		}
		panic(rt.ToValue(&abortSignal{reason: reason}))
	})

	// HTTP wrapper (very simple, stdlib-backed). See fetch.go.
	root.Set("fetch", buildFetch(rt, ctx))

	// Metadata (read-only).
	meta := rt.NewObject()
	prof := rt.NewObject()
	prof.Set("name", env.Profile)
	meta.Set("profile", prof)
	meta.Set("tag", env.Tag)
	if env.Endpoint != nil {
		ep := rt.NewObject()
		ep.Set("method", env.Endpoint.Method)
		ep.Set("path", env.Endpoint.Path)
		root.Set("endpoint", ep)
	} else {
		root.Set("endpoint", sobek.Null())
	}
	root.Set("profile", prof)
	root.Set("tag", env.Tag)
	root.Set("phase", string(env.Phase))
	root.Set("scope", string(env.Scope))

	rt.Set("feather", root)
	rt.Set("console", console)
}

// --- request --------------------------------------------------------------

// buildRequest exposes the Go *http.Request as a JS object. Mutations on the
// JS object are not live; they are read back by syncRequestBack after the
// script returns.
func buildRequest(rt *sobek.Runtime, env *Env) sobek.Value {
	if env.Request == nil {
		return sobek.Null()
	}
	r := env.Request
	obj := rt.NewObject()
	obj.Set("method", r.Method)
	obj.Set("path", r.Path)
	obj.Set("headers", goStringMapToJS(rt, r.Headers))
	obj.Set("queryParams", goStringMapToJS(rt, r.QueryParams))
	if r.Body == nil {
		obj.Set("body", sobek.Null())
	} else {
		obj.Set("body", string(r.Body))
	}
	return obj
}

func syncRequestBack(rt *sobek.Runtime, env *Env) {
	if env.Request == nil || env.Phase != PhasePre {
		return
	}
	val := rt.Get("feather")
	if val == nil {
		return
	}
	root := val.ToObject(rt)
	reqVal := root.Get("request")
	if reqVal == nil || sobek.IsNull(reqVal) || sobek.IsUndefined(reqVal) {
		return
	}
	obj := reqVal.ToObject(rt)

	if v := obj.Get("method"); v != nil {
		if s := v.String(); s != "" {
			env.Request.Method = strings.ToUpper(s)
		}
	}
	if v := obj.Get("path"); v != nil {
		env.Request.Path = v.String()
	}
	if v := obj.Get("headers"); v != nil {
		env.Request.Headers = jsToGoStringMap(rt, v)
	}
	if v := obj.Get("queryParams"); v != nil {
		env.Request.QueryParams = jsToGoStringMap(rt, v)
	}
	if v := obj.Get("body"); v != nil {
		if sobek.IsNull(v) || sobek.IsUndefined(v) {
			env.Request.Body = nil
		} else {
			env.Request.Body = []byte(v.String())
		}
	}
}

// --- response -------------------------------------------------------------

func buildResponse(rt *sobek.Runtime, env *Env) sobek.Value {
	if env.Response == nil {
		return sobek.Null()
	}
	r := env.Response
	obj := rt.NewObject()
	obj.Set("status", r.StatusCode)
	obj.Set("statusText", r.Status)
	obj.Set("headers", goHeaderToJS(rt, r.Headers))
	obj.Set("body", string(r.Body))
	obj.Set("duration", r.Duration.Milliseconds())
	return obj
}

func syncResponseBack(rt *sobek.Runtime, env *Env) {
	if env.Response == nil || env.Phase != PhasePost {
		return
	}
	val := rt.Get("feather")
	if val == nil {
		return
	}
	root := val.ToObject(rt)
	resVal := root.Get("response")
	if resVal == nil || sobek.IsNull(resVal) || sobek.IsUndefined(resVal) {
		return
	}
	obj := resVal.ToObject(rt)

	if v := obj.Get("status"); v != nil {
		env.Response.StatusCode = int(v.ToInteger())
	}
	if v := obj.Get("statusText"); v != nil {
		env.Response.Status = v.String()
	}
	if v := obj.Get("headers"); v != nil {
		env.Response.Headers = jsToGoHeader(rt, v)
	}
	if v := obj.Get("body"); v != nil {
		env.Response.Body = []byte(v.String())
	}
}

// --- context --------------------------------------------------------------

func buildContext(rt *sobek.Runtime, env *Env) sobek.Value {
	obj := rt.NewObject()
	obj.Set("get", func(call sobek.FunctionCall) sobek.Value {
		if env.Context == nil || len(call.Arguments) == 0 {
			return sobek.Undefined()
		}
		return rt.ToValue(env.Context.Get(call.Arguments[0].String()))
	})
	obj.Set("set", func(call sobek.FunctionCall) sobek.Value {
		if env.Context == nil || len(call.Arguments) < 2 {
			return sobek.Undefined()
		}
		env.Context.Set(call.Arguments[0].String(), call.Arguments[1].String())
		return sobek.Undefined()
	})
	obj.Set("delete", func(call sobek.FunctionCall) sobek.Value {
		if env.Context == nil || len(call.Arguments) == 0 {
			return sobek.Undefined()
		}
		env.Context.Delete(call.Arguments[0].String())
		return sobek.Undefined()
	})
	return obj
}

// --- console --------------------------------------------------------------

func buildConsole(rt *sobek.Runtime, env *Env, res *Result) sobek.Value {
	obj := rt.NewObject()
	emit := func(level LogLevel) func(call sobek.FunctionCall) sobek.Value {
		return func(call sobek.FunctionCall) sobek.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, a := range call.Arguments {
				parts = append(parts, formatArg(a))
			}
			res.Logs = append(res.Logs, LogEntry{
				Phase:   env.Phase,
				Scope:   env.Scope,
				Tag:     env.Tag,
				Level:   level,
				Message: strings.Join(parts, " "),
			})
			return sobek.Undefined()
		}
	}
	obj.Set("log", emit(LogInfo))
	obj.Set("warn", emit(LogWarn))
	obj.Set("error", emit(LogError))
	return obj
}

// formatArg prints a sobek value the way console.log normally would —
// strings raw, objects via JSON.
func formatArg(v sobek.Value) string {
	if v == nil {
		return "undefined"
	}
	if sobek.IsNull(v) {
		return "null"
	}
	if sobek.IsUndefined(v) {
		return "undefined"
	}
	exp := v.Export()
	switch x := exp.(type) {
	case string:
		return x
	case nil:
		return "null"
	default:
		// Try JSON-ish encoding for objects/arrays.
		return fmt.Sprintf("%v", x)
	}
}

// --- conversion helpers ---------------------------------------------------

func goStringMapToJS(rt *sobek.Runtime, m map[string]string) sobek.Value {
	o := rt.NewObject()
	for k, v := range m {
		o.Set(k, v)
	}
	return o
}

func jsToGoStringMap(rt *sobek.Runtime, v sobek.Value) map[string]string {
	if v == nil || sobek.IsNull(v) || sobek.IsUndefined(v) {
		return nil
	}
	out := map[string]string{}
	obj := v.ToObject(rt)
	for _, key := range obj.Keys() {
		out[key] = obj.Get(key).String()
	}
	return out
}

func goHeaderToJS(rt *sobek.Runtime, h stdhttp.Header) sobek.Value {
	o := rt.NewObject()
	for k, vs := range h {
		arr := rt.NewArray()
		for i, v := range vs {
			arr.Set(fmt.Sprintf("%d", i), v)
		}
		o.Set(k, arr)
	}
	return o
}

func jsToGoHeader(rt *sobek.Runtime, v sobek.Value) stdhttp.Header {
	if v == nil || sobek.IsNull(v) || sobek.IsUndefined(v) {
		return nil
	}
	out := stdhttp.Header{}
	obj := v.ToObject(rt)
	for _, key := range obj.Keys() {
		val := obj.Get(key)
		exp := val.Export()
		switch x := exp.(type) {
		case []interface{}:
			for _, item := range x {
				out.Add(key, fmt.Sprintf("%v", item))
			}
		default:
			out.Set(key, val.String())
		}
	}
	return out
}

// Suppress "imported and not used" for http when only the type is referenced
// indirectly via Env (Go is fine, this is just a marker for the reader).
var _ = (*http.Request)(nil)
