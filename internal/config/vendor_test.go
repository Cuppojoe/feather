package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVendorSpec(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// A source spec living outside ~/.feather.
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "openapi.json")
	if err := os.WriteFile(src, []byte(`{"openapi":"3.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &Profile{Name: "My API", SpecPath: src}
	if IsVendored(p) {
		t.Fatal("external spec should not report vendored")
	}

	dst, err := VendorSpec(p)
	if err != nil {
		t.Fatalf("VendorSpec: %v", err)
	}

	// The profile now points at a local copy under ~/.feather/specs/<profile>/.
	specsDir, _ := SpecsDir()
	if !strings.HasPrefix(dst, specsDir) {
		t.Fatalf("vendored path %q not under %q", dst, specsDir)
	}
	if p.SpecPath != dst {
		t.Fatalf("profile SpecPath not repointed: %q", p.SpecPath)
	}
	if !IsVendored(p) {
		t.Fatal("vendored spec should report vendored")
	}

	// Contents copied verbatim.
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != `{"openapi":"3.0.0"}` {
		t.Fatalf("vendored contents wrong: %q err=%v", got, err)
	}

	// The profile was persisted with the new path.
	loaded, err := LoadProfile("My API")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if loaded.SpecPath != dst {
		t.Fatalf("persisted SpecPath %q != %q", loaded.SpecPath, dst)
	}

	// Re-vendoring is a no-op (already local).
	again, err := VendorSpec(p)
	if err != nil || again != dst {
		t.Fatalf("re-vendor should be a no-op: %q err=%v", again, err)
	}
}

func TestVendorSpecNoSpec(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := VendorSpec(&Profile{Name: "x"}); err == nil {
		t.Fatal("expected error vendoring a profile with no spec")
	}
}
