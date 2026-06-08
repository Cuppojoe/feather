package overlay

import (
	"path/filepath"
	"testing"

	"github.com/cuppojoe/feather/internal/openapi"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	o, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o == nil || len(o.Operations) != 0 || len(o.Added) != 0 {
		t.Fatalf("expected empty overlay, got %#v", o)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "p.yaml")
	o := New()
	o.SetOverride("post", "/items", OpOverride{
		Summary:       "Create",
		BodyExample:   `{"a":1}`,
		ParamDefaults: map[string]string{"org": "acme"},
		Headers:       map[string]string{"X-Foo": "bar"},
	})
	o.AppendAdded(AddedOp{Method: "get", Path: "/ping", Tag: "Custom"})

	if err := o.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	ovr := got.Get("POST", "/items")
	if ovr == nil || ovr.Summary != "Create" || ovr.BodyExample != `{"a":1}` {
		t.Fatalf("override not round-tripped: %#v", ovr)
	}
	if ovr.ParamDefaults["org"] != "acme" || ovr.Headers["X-Foo"] != "bar" {
		t.Fatalf("override maps not round-tripped: %#v", ovr)
	}
	if len(got.Added) != 1 || got.Added[0].Path != "/ping" {
		t.Fatalf("added not round-tripped: %#v", got.Added)
	}
}

func TestScriptsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.yaml")
	o := New()
	o.SetProfileScripts(Scripts{Pre: "// hi", Post: "// bye"})
	o.SetTagScripts("Workload", Scripts{Pre: "// tag"})
	o.SetOperationScripts("GET", "/items", Scripts{Post: "// op"})
	o.Scripts.TimeoutMs = 3000

	if err := o.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.ProfileScripts().Pre != "// hi" || got.ProfileScripts().Post != "// bye" {
		t.Fatalf("profile scripts: %#v", got.ProfileScripts())
	}
	if got.TagScripts("Workload").Pre != "// tag" {
		t.Fatalf("tag scripts: %#v", got.TagScripts("Workload"))
	}
	if got.OperationScripts("GET", "/items").Post != "// op" {
		t.Fatalf("op scripts: %#v", got.OperationScripts("GET", "/items"))
	}
	if got.Scripts.TimeoutMs != 3000 {
		t.Fatalf("timeout: %d", got.Scripts.TimeoutMs)
	}

	// Erasing a tag's scripts removes the entry rather than leaving an empty
	// map row.
	got.SetTagScripts("Workload", Scripts{})
	if _, ok := got.Scripts.Tags["Workload"]; ok {
		t.Fatalf("expected tag entry erased")
	}
}

func TestOpKeyNormalizesMethod(t *testing.T) {
	if OpKey("post", "/x") != "POST /x" {
		t.Fatalf("OpKey: got %q", OpKey("post", "/x"))
	}
}

// baseWith returns a base spec containing a single endpoint in tag "Items".
func baseWith(method, path, summary string) *openapi.ParsedSpec {
	return &openapi.ParsedSpec{
		Tags: []openapi.TagGroup{{
			Name:      "Items",
			Endpoints: []openapi.Endpoint{{Method: method, Path: path, Summary: summary}},
		}},
	}
}

// findEndpoint returns the endpoint with method+path and its tag, if present.
func findEndpoint(spec *openapi.ParsedSpec, method, path string) (openapi.Endpoint, string, bool) {
	for _, tg := range spec.Tags {
		for _, ep := range tg.Endpoints {
			if ep.Method == method && ep.Path == path {
				return ep, tg.Name, true
			}
		}
	}
	return openapi.Endpoint{}, "", false
}

func TestApplyDoesNotMutateBase(t *testing.T) {
	base := baseWith("POST", "/items", "orig")
	o := New()
	o.SetOverride("POST", "/items", OpOverride{Summary: "new summary"})
	got := Apply(base, o)

	if base.Tags[0].Endpoints[0].Summary != "orig" {
		t.Fatalf("base was mutated: %q", base.Tags[0].Endpoints[0].Summary)
	}
	ep, _, _ := findEndpoint(got, "POST", "/items")
	if ep.Summary != "new summary" {
		t.Fatalf("summary not overridden in result: %q", ep.Summary)
	}
}

func TestApplyTagOverrideRegroupsBaseOp(t *testing.T) {
	base := baseWith("GET", "/items", "")
	o := New()
	o.SetOverride("GET", "/items", OpOverride{Tag: "Moved"})
	got := Apply(base, o)

	_, tag, ok := findEndpoint(got, "GET", "/items")
	if !ok || tag != "Moved" {
		t.Fatalf("expected op under Moved, got tag=%q ok=%v", tag, ok)
	}
	// The now-empty spec tag "Items" should be gone (not a declared category).
	for _, tg := range got.Tags {
		if tg.Name == "Items" {
			t.Fatalf("emptied spec tag should be dropped: %#v", got.Tags)
		}
	}
}

func TestApplyAddedCreatesCustomGroup(t *testing.T) {
	o := New()
	o.AppendAdded(AddedOp{Method: "post", Path: "/custom/ping"}) // no tag → Custom
	got := Apply(&openapi.ParsedSpec{}, o)

	ep, tag, ok := findEndpoint(got, "POST", "/custom/ping")
	if !ok || tag != "Custom" {
		t.Fatalf("expected POST under Custom, got tag=%q ok=%v", tag, ok)
	}
	if ep.RequestBody == nil {
		t.Fatalf("POST added op should have a request body")
	}
}

func TestApplyPersistsEmptyCategory(t *testing.T) {
	o := New()
	o.AddCategory("Webhooks")
	got := Apply(&openapi.ParsedSpec{}, o)

	found := false
	for _, tg := range got.Tags {
		if tg.Name == "Webhooks" {
			found = true
			if len(tg.Endpoints) != 0 {
				t.Fatalf("expected empty category, got %d endpoints", len(tg.Endpoints))
			}
		}
	}
	if !found {
		t.Fatalf("declared category not present: %#v", got.Tags)
	}
}

func TestApplyAddedUnionsPathVariables(t *testing.T) {
	o := New()
	o.AppendAdded(AddedOp{Method: "get", Path: "/orgs/{org}/items/{id}", Tag: "Custom"})
	got := Apply(&openapi.ParsedSpec{}, o)

	want := map[string]bool{"org": true, "id": true}
	for _, v := range got.PathVariables {
		delete(want, v)
	}
	if len(want) != 0 {
		t.Fatalf("missing path vars %v; got %v", want, got.PathVariables)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	base := baseWith("GET", "/items", "")
	o := New()
	o.AppendAdded(AddedOp{Method: "POST", Path: "/ping", Tag: "Custom"})

	a := Apply(base, o)
	b := Apply(base, o)
	if len(a.Tags) != len(b.Tags) {
		t.Fatalf("non-deterministic tag count: %d vs %d", len(a.Tags), len(b.Tags))
	}
	count := 0
	for _, tg := range a.Tags {
		for _, ep := range tg.Endpoints {
			if ep.Path == "/ping" {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 /ping endpoint, got %d", count)
	}
}

func TestRenameCategoryRetagsAddedOps(t *testing.T) {
	o := New()
	o.AddCategory("Old")
	o.AppendAdded(AddedOp{Method: "GET", Path: "/a", Tag: "Old"})
	o.RenameCategory("Old", "New")

	if o.HasCategory("Old") || !o.HasCategory("New") {
		t.Fatalf("category not renamed: %#v", o.Categories)
	}
	if o.Added[0].Tag != "New" {
		t.Fatalf("added op not retagged: %q", o.Added[0].Tag)
	}
}

func TestRemoveAddedAndOverride(t *testing.T) {
	o := New()
	o.AppendAdded(AddedOp{Method: "GET", Path: "/a", Tag: "Custom"})
	o.SetOverride("GET", "/a", OpOverride{Summary: "x"})
	o.RemoveAdded("GET", "/a")

	if o.HasAdded("GET", "/a") {
		t.Fatal("added op not removed")
	}
	if o.Get("GET", "/a") != nil {
		t.Fatal("override should be removed alongside added op")
	}
}

func TestApplyEmptyBaseSpec(t *testing.T) {
	o := New()
	o.AppendAdded(AddedOp{Method: "GET", Path: "/a", Tag: "Z"})
	o.AppendAdded(AddedOp{Method: "GET", Path: "/b", Tag: "A"})
	got := Apply(&openapi.ParsedSpec{Schemas: map[string]*openapi.Schema{}}, o)

	if len(got.Tags) != 2 {
		t.Fatalf("expected 2 tag groups, got %d", len(got.Tags))
	}
	if got.Tags[0].Name != "A" || got.Tags[1].Name != "Z" {
		t.Fatalf("tags not sorted: %#v", got.Tags)
	}
}
