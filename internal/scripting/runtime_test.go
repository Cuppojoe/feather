package scripting

import (
	"context"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuppojoe/feather/internal/http"
	"github.com/cuppojoe/feather/internal/models"
	"github.com/cuppojoe/feather/internal/openapi"
)

func TestRequestMutationsPropagate(t *testing.T) {
	req := &http.Request{
		Method:      "GET",
		Path:        "/items",
		Headers:     map[string]string{"X-Old": "yes"},
		QueryParams: map[string]string{},
	}
	env := &Env{
		Phase:    PhasePre,
		Scope:    ScopeProfile,
		Profile:  "default",
		Endpoint: &openapi.Endpoint{Method: "GET", Path: "/items"},
		Request:  req,
		Context:  models.NewContext(),
	}
	code := `
		feather.request.method = "post";
		feather.request.path = "/items/changed";
		feather.request.headers["X-New"] = "yes";
		feather.request.queryParams["limit"] = "10";
		feather.request.body = JSON.stringify({hi: "there"});
	`
	res := Run(context.Background(), code, env, time.Second)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if req.Method != "POST" {
		t.Errorf("method: %s", req.Method)
	}
	if req.Path != "/items/changed" {
		t.Errorf("path: %s", req.Path)
	}
	if req.Headers["X-New"] != "yes" {
		t.Errorf("X-New header missing: %#v", req.Headers)
	}
	if req.QueryParams["limit"] != "10" {
		t.Errorf("limit param missing: %#v", req.QueryParams)
	}
	if !strings.Contains(string(req.Body), `"hi"`) {
		t.Errorf("body: %s", req.Body)
	}
}

func TestConsoleCaptured(t *testing.T) {
	env := &Env{
		Phase:    PhasePre,
		Scope:    ScopeProfile,
		Profile:  "default",
		Endpoint: &openapi.Endpoint{Method: "GET", Path: "/"},
		Request:  &http.Request{Method: "GET", Path: "/"},
		Context:  models.NewContext(),
	}
	res := Run(context.Background(), `
		feather.console.log("hello", 42);
		feather.console.warn("careful");
		feather.console.error("nope");
	`, env, time.Second)
	if res.Err != nil {
		t.Fatalf("err: %v", res.Err)
	}
	if len(res.Logs) != 3 {
		t.Fatalf("logs: %#v", res.Logs)
	}
	if res.Logs[0].Level != LogInfo || !strings.Contains(res.Logs[0].Message, "hello") {
		t.Errorf("log[0]: %#v", res.Logs[0])
	}
	if res.Logs[1].Level != LogWarn || res.Logs[2].Level != LogError {
		t.Errorf("levels: %v %v", res.Logs[1].Level, res.Logs[2].Level)
	}
}

// TestGlobalConsoleCaptured verifies the standard top-level console.* global
// works (not just feather.console) and its lines land in res.Logs.
func TestGlobalConsoleCaptured(t *testing.T) {
	env := &Env{
		Phase:    PhasePre,
		Scope:    ScopeProfile,
		Profile:  "default",
		Endpoint: &openapi.Endpoint{Method: "GET", Path: "/"},
		Request:  &http.Request{Method: "GET", Path: "/"},
		Context:  models.NewContext(),
	}
	res := Run(context.Background(), `
		console.log("hello", 42);
		console.warn("careful");
		console.error("nope");
	`, env, time.Second)
	if res.Err != nil {
		t.Fatalf("err: %v", res.Err)
	}
	if len(res.Logs) != 3 {
		t.Fatalf("logs: %#v", res.Logs)
	}
	if res.Logs[0].Level != LogInfo || !strings.Contains(res.Logs[0].Message, "hello") {
		t.Errorf("log[0]: %#v", res.Logs[0])
	}
	if res.Logs[1].Level != LogWarn || res.Logs[2].Level != LogError {
		t.Errorf("levels: %v %v", res.Logs[1].Level, res.Logs[2].Level)
	}
}

func TestAbortStopsChain(t *testing.T) {
	env := &Env{
		Phase: PhasePre, Scope: ScopeProfile, Profile: "p",
		Endpoint: &openapi.Endpoint{Method: "GET", Path: "/"},
		Request:  &http.Request{Method: "GET", Path: "/"},
		Context:  models.NewContext(),
	}
	res := Run(context.Background(), `feather.abort("nope");`, env, time.Second)
	if !res.Aborted {
		t.Fatalf("expected aborted, got %#v", res)
	}
	if res.Reason != "nope" {
		t.Fatalf("reason: %q", res.Reason)
	}
}

func TestContextGetSet(t *testing.T) {
	ctx := models.NewContext()
	env := &Env{
		Phase: PhasePre, Scope: ScopeProfile, Profile: "p",
		Endpoint: &openapi.Endpoint{Method: "GET", Path: "/"},
		Request:  &http.Request{Method: "GET", Path: "/"},
		Context:  ctx,
	}
	Run(context.Background(), `
		feather.environment.set("k", "v1");
	`, env, time.Second)
	if ctx.Get("k") != "v1" {
		t.Fatalf("expected v1, got %q", ctx.Get("k"))
	}
	res := Run(context.Background(), `
		if (feather.environment.get("k") !== "v1") feather.abort("read failed");
		feather.environment.delete("k");
	`, env, time.Second)
	if res.Aborted {
		t.Fatalf("aborted: %s", res.Reason)
	}
	if ctx.Get("k") != "" {
		t.Fatalf("expected empty after delete, got %q", ctx.Get("k"))
	}
}

func TestTimeoutInterrupts(t *testing.T) {
	env := &Env{
		Phase: PhasePre, Scope: ScopeProfile, Profile: "p",
		Endpoint: &openapi.Endpoint{Method: "GET", Path: "/"},
		Request:  &http.Request{Method: "GET", Path: "/"},
		Context:  models.NewContext(),
	}
	res := Run(context.Background(), `while (true) {}`, env, 50*time.Millisecond)
	if res.Err == nil {
		t.Fatalf("expected timeout error")
	}
	if res.Took > 500*time.Millisecond {
		t.Errorf("took too long: %s", res.Took)
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.Method != "POST" {
			t.Errorf("method: %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Echo", "ok")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("got: " + string(body)))
	}))
	defer srv.Close()

	env := &Env{
		Phase: PhasePost, Scope: ScopeProfile, Profile: "p",
		Endpoint: &openapi.Endpoint{Method: "GET", Path: "/"},
		Request:  &http.Request{Method: "GET", Path: "/"},
		Response: &http.Response{},
		Context:  models.NewContext(),
	}
	res := Run(context.Background(), `
		var r = feather.fetch("`+srv.URL+`", {
			method: "POST",
			headers: { "Content-Type": "text/plain" },
			body: "hello",
		});
		if (r.status !== 201) feather.abort("status=" + r.status);
		if (r.body !== "got: hello") feather.abort("body=" + r.body);
		if (!r.headers["X-Echo"]) feather.abort("missing X-Echo");
	`, env, time.Second)
	if res.Err != nil {
		t.Fatalf("err: %v", res.Err)
	}
	if res.Aborted {
		t.Fatalf("aborted: %s", res.Reason)
	}
}

func TestResolveOrder(t *testing.T) {
	// Build an overlay with all three scopes populated.
	// (uses overlay package — easier to test indirectly via real lookup.)
}

func TestResponseMutationsPropagate(t *testing.T) {
	resp := &http.Response{StatusCode: 200, Status: "200 OK", Body: []byte("old")}
	env := &Env{
		Phase: PhasePost, Scope: ScopeOperation, Profile: "p",
		Endpoint: &openapi.Endpoint{Method: "GET", Path: "/"},
		Request:  &http.Request{Method: "GET", Path: "/"},
		Response: resp,
		Context:  models.NewContext(),
	}
	res := Run(context.Background(), `
		feather.response.status = 418;
		feather.response.body = "teapot";
	`, env, time.Second)
	if res.Err != nil {
		t.Fatalf("err: %v", res.Err)
	}
	if resp.StatusCode != 418 || string(resp.Body) != "teapot" {
		t.Fatalf("mutations not propagated: %d %q", resp.StatusCode, resp.Body)
	}
}
