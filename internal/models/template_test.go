package models

import (
	"strings"
	"testing"
)

func TestResolveFlat(t *testing.T) {
	in := map[string]string{
		"org":  "kyle-test",
		"gvc":  "demo",
		"port": "43200",
	}
	out, err := Resolve(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for k, v := range in {
		if out[k] != v {
			t.Errorf("flat %q: got %q, want %q", k, out[k], v)
		}
	}
}

func TestResolveNested(t *testing.T) {
	in := map[string]string{
		"host":    "api.${env}.example.com",
		"env":     "staging",
		"baseURL": "https://${host}/v1",
	}
	out, err := Resolve(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantHost := "api.staging.example.com"
	if out["host"] != wantHost {
		t.Errorf("host: got %q, want %q", out["host"], wantHost)
	}
	wantBase := "https://api.staging.example.com/v1"
	if out["baseURL"] != wantBase {
		t.Errorf("baseURL: got %q, want %q", out["baseURL"], wantBase)
	}
}

func TestResolveCycleSelf(t *testing.T) {
	in := map[string]string{
		"a": "${a}",
	}
	_, err := Resolve(in)
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle detected") || !strings.Contains(err.Error(), "a → a") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveCycleTwoStep(t *testing.T) {
	in := map[string]string{
		"a": "x-${b}",
		"b": "y-${a}",
	}
	_, err := Resolve(in)
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cycle detected") {
		t.Errorf("missing cycle prefix: %v", err)
	}
	// Path mentions both names.
	if !(strings.Contains(msg, "a") && strings.Contains(msg, "b")) {
		t.Errorf("cycle path should name both 'a' and 'b': %v", err)
	}
}

func TestResolveCycleThreeStep(t *testing.T) {
	in := map[string]string{
		"a": "${b}",
		"b": "${c}",
		"c": "${a}",
	}
	_, err := Resolve(in)
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"a", "b", "c", "→"} {
		if !strings.Contains(msg, want) {
			t.Errorf("cycle message %q missing %q", msg, want)
		}
	}
}

func TestResolveUndefinedReferencePassthrough(t *testing.T) {
	in := map[string]string{
		"greeting": "hi ${name}",
	}
	out, err := Resolve(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Unknown references should pass through literally so the user
	// notices the typo.
	if out["greeting"] != "hi ${name}" {
		t.Errorf("got %q, want %q", out["greeting"], "hi ${name}")
	}
}

func TestResolveDoesNotMutateInput(t *testing.T) {
	in := map[string]string{
		"a": "value",
		"b": "${a}-suffix",
	}
	want := map[string]string{
		"a": "value",
		"b": "${a}-suffix",
	}
	_, err := Resolve(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for k, v := range want {
		if in[k] != v {
			t.Errorf("Resolve mutated input[%q]: got %q, want %q", k, in[k], v)
		}
	}
}

func TestSubstituteSinglePass(t *testing.T) {
	values := map[string]string{
		"host": "api.example.com",
		"path": "/v1",
	}
	got := Substitute("https://${host}${path}/things", values)
	want := "https://api.example.com/v1/things"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSubstituteUnknownStays(t *testing.T) {
	got := Substitute("${unknown}/path", map[string]string{})
	if got != "${unknown}/path" {
		t.Errorf("got %q, want %q", got, "${unknown}/path")
	}
}

func TestSubstituteDoesNotChainAcrossPasses(t *testing.T) {
	// Substitute is single-pass; if a value contains another ${ref}, it
	// is NOT re-expanded. Callers who need that should run Resolve first.
	values := map[string]string{
		"a": "${b}",
		"b": "final",
	}
	got := Substitute("${a}", values)
	if got != "${b}" {
		t.Errorf("got %q, want %q", got, "${b}")
	}
}
