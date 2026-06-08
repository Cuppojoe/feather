package scripting

import (
	"bytes"
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"time"

	"github.com/grafana/sobek"
)

// buildFetch returns a sobek function bound to ctx that performs one HTTP
// round-trip synchronously and returns a plain JS object:
//
//	{ status, statusText, headers, body, duration }
//
// Transport-level failures throw a JS Error; non-2xx responses do not. The
// per-call timeout is capped at 30 s and bounded by the script's outer
// context so the overall script-timeout still wins.
func buildFetch(rt *sobek.Runtime, ctx context.Context) func(call sobek.FunctionCall) sobek.Value {
	const maxTimeout = 30 * time.Second
	return func(call sobek.FunctionCall) sobek.Value {
		if len(call.Arguments) == 0 {
			panic(rt.NewTypeError("feather.fetch: url is required"))
		}
		url := call.Arguments[0].String()

		method := stdhttp.MethodGet
		var headers map[string]string
		var body []byte
		timeout := 10 * time.Second
		if len(call.Arguments) > 1 {
			optsV := call.Arguments[1]
			if optsV != nil && !sobek.IsNull(optsV) && !sobek.IsUndefined(optsV) {
				opts := optsV.ToObject(rt)
				if v := opts.Get("method"); v != nil && !sobek.IsUndefined(v) {
					if s := v.String(); s != "" {
						method = s
					}
				}
				if v := opts.Get("headers"); v != nil && !sobek.IsUndefined(v) {
					headers = jsToGoStringMap(rt, v)
				}
				if v := opts.Get("body"); v != nil && !sobek.IsUndefined(v) && !sobek.IsNull(v) {
					body = []byte(v.String())
				}
				if v := opts.Get("timeoutMs"); v != nil && !sobek.IsUndefined(v) {
					ms := time.Duration(v.ToInteger()) * time.Millisecond
					if ms > 0 {
						timeout = ms
					}
				}
			}
		}
		if timeout > maxTimeout {
			timeout = maxTimeout
		}

		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := stdhttp.NewRequestWithContext(callCtx, method, url, reader)
		if err != nil {
			panic(rt.NewGoError(fmt.Errorf("feather.fetch: %w", err)))
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		client := &stdhttp.Client{Timeout: timeout}
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			panic(rt.NewGoError(fmt.Errorf("feather.fetch: %w", err)))
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			panic(rt.NewGoError(fmt.Errorf("feather.fetch: %w", err)))
		}
		took := time.Since(start)

		out := rt.NewObject()
		out.Set("status", resp.StatusCode)
		out.Set("statusText", resp.Status)
		out.Set("headers", goHeaderToJS(rt, resp.Header))
		out.Set("body", string(raw))
		out.Set("duration", took.Milliseconds())
		return out
	}
}
