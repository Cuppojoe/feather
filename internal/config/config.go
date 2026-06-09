package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Profile represents a named configuration tied to a specific OpenAPI spec.
// Variables live exclusively in Environments — the profile just remembers
// which one is active so switching profiles also restores their last
// chosen variable bundle.
//
// Auth is intentionally absent — users assemble their own auth flow with
// pre-request scripts in the Scripts modal (feather.context for storage,
// feather.fetch for token endpoints, feather.request.headers to inject).
type Profile struct {
	Name              string `yaml:"name"`
	SpecPath          string `yaml:"spec_path"`
	BaseURL           string `yaml:"base_url,omitempty"`
	ActiveEnvironment string `yaml:"active_environment,omitempty"`
}

// Config is a backwards-compatibility alias for Profile so existing call sites
// that pass *Config keep compiling. New code should prefer *Profile directly.
type Config = Profile

// Index is the top-level state at ~/.feather/config.yaml — it tracks the
// default profile name. Profiles themselves live in ~/.feather/profiles/.
type Index struct {
	DefaultProfile string `yaml:"default_profile,omitempty"`
}

// FeatherDir returns the root directory for feather state (~/.feather).
func FeatherDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	return filepath.Join(homeDir, ".feather"), nil
}

// ProfilesDir returns the directory that holds profile YAML files.
func ProfilesDir() (string, error) {
	root, err := FeatherDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "profiles"), nil
}

// CacheDir returns the directory that holds per-profile cache/history files.
func CacheDir() (string, error) {
	root, err := FeatherDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "cache"), nil
}

// OverlaysDir returns the directory that holds per-profile overlay files.
func OverlaysDir() (string, error) {
	root, err := FeatherDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "overlays"), nil
}

// OverlayPath returns the YAML overlay path for a named profile.
func OverlayPath(name string) (string, error) {
	dir, err := OverlaysDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sanitizeName(name)+".yaml"), nil
}

// SpecsDir returns the directory that holds vendored (locally-copied) specs.
func SpecsDir() (string, error) {
	root, err := FeatherDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "specs"), nil
}

// ProfileSpecDir returns a named profile's folder for its vendored spec.
func ProfileSpecDir(name string) (string, error) {
	dir, err := SpecsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sanitizeName(name)), nil
}

// IndexPath returns the path to the top-level index file.
func IndexPath() (string, error) {
	root, err := FeatherDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "config.yaml"), nil
}

// ProfilePath returns the YAML path for a named profile.
func ProfilePath(name string) (string, error) {
	dir, err := ProfilesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sanitizeName(name)+".yaml"), nil
}

// NewProfile creates a new empty profile.
func NewProfile(name string) *Profile {
	return &Profile{Name: name}
}

// NewConfig is kept for backwards compatibility with existing call sites.
func NewConfig() *Profile {
	return NewProfile("")
}

// LoadProfile reads a profile by name.
func LoadProfile(name string) (*Profile, error) {
	path, err := ProfilePath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading profile %q: %w", name, err)
	}
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing profile %q: %w", name, err)
	}
	if p.Name == "" {
		p.Name = name
	}
	return &p, nil
}

// Save persists this profile to ~/.feather/profiles/<name>.yaml.
func (p *Profile) Save() error {
	if p.Name == "" {
		return fmt.Errorf("profile has no name")
	}
	path, err := ProfilePath(p.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating profiles dir: %w", err)
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshaling profile: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing profile: %w", err)
	}
	return nil
}

// SaveDefault is kept so existing call sites continue to work — for a profile
// it writes to its own file.
func (p *Profile) SaveDefault() error {
	return p.Save()
}

// RenameProfile moves a profile YAML (and its cache files) from oldName to
// newName. If the old profile was the default it updates the index. Returns
// an error if newName already exists or either name sanitises to empty.
func RenameProfile(oldName, newName string) error {
	if strings.TrimSpace(oldName) == "" || strings.TrimSpace(newName) == "" {
		return fmt.Errorf("profile names cannot be empty")
	}
	if sanitizeName(oldName) == sanitizeName(newName) {
		return nil
	}

	// Refuse if target already exists.
	newPath, err := ProfilePath(newName)
	if err != nil {
		return err
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("profile %q already exists", newName)
	}

	// Load, rewrite under the new name, persist, then delete the old YAML.
	prof, err := LoadProfile(oldName)
	if err != nil {
		return fmt.Errorf("loading source profile: %w", err)
	}
	prof.Name = newName
	if err := prof.Save(); err != nil {
		return fmt.Errorf("saving new profile: %w", err)
	}

	oldPath, err := ProfilePath(oldName)
	if err != nil {
		return err
	}
	if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing old profile: %w", err)
	}

	// Best-effort rename of cache files.
	if oldHist, err1 := profileHistoryPath(oldName); err1 == nil {
		if newHist, err2 := profileHistoryPath(newName); err2 == nil {
			_ = os.Rename(oldHist, newHist)
		}
	}

	// Update the default-profile index if it pointed at the old name.
	idx, err := LoadIndex()
	if err == nil && idx.DefaultProfile == oldName {
		idx.DefaultProfile = newName
		_ = idx.Save()
	}

	return nil
}

// CopyProfile duplicates an existing profile under a new name. The new
// profile gets a fresh copy of the profile YAML (renamed), the overlay
// YAML (if present), and the vendored spec directory (if present, with
// SpecPath repointed at the new copy). Per-session caches (history) are
// intentionally NOT copied — the new profile starts with a clean slate.
//
// Returns an error if either name is empty, the names sanitise to the
// same on-disk identifier, or the target profile already exists.
func CopyProfile(srcName, dstName string) error {
	if strings.TrimSpace(srcName) == "" || strings.TrimSpace(dstName) == "" {
		return fmt.Errorf("profile names cannot be empty")
	}
	if sanitizeName(srcName) == sanitizeName(dstName) {
		return fmt.Errorf("source and destination resolve to the same file")
	}

	// Refuse if the target already exists.
	newPath, err := ProfilePath(dstName)
	if err != nil {
		return err
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("profile %q already exists", dstName)
	}

	src, err := LoadProfile(srcName)
	if err != nil {
		return fmt.Errorf("loading source profile: %w", err)
	}

	dst := &Profile{
		Name:              dstName,
		SpecPath:          src.SpecPath,
		BaseURL:           src.BaseURL,
		ActiveEnvironment: src.ActiveEnvironment,
	}

	// If the source's spec is vendored under its own profile dir, copy
	// the whole dir under the new profile and repoint SpecPath at the
	// new location. Unvendored specs are shared by reference.
	if IsVendored(src) {
		oldSpecDir, err := ProfileSpecDir(srcName)
		if err != nil {
			return err
		}
		newSpecDir, err := ProfileSpecDir(dstName)
		if err != nil {
			return err
		}
		if err := copyDir(oldSpecDir, newSpecDir); err != nil {
			return fmt.Errorf("copying vendored spec: %w", err)
		}
		// Rebase SpecPath onto the new dir, preserving the basename.
		if rel, err := filepath.Rel(oldSpecDir, src.SpecPath); err == nil {
			dst.SpecPath = filepath.Join(newSpecDir, rel)
		}
	}

	if err := dst.Save(); err != nil {
		return fmt.Errorf("saving new profile: %w", err)
	}

	// Best-effort overlay copy — the overlay carries the user's
	// customisations (scripts, body examples, param defaults) and is the
	// most valuable thing to bring forward. A failure here doesn't roll
	// back the new profile; the user can re-author the overlay.
	if oldOv, err := OverlayPath(srcName); err == nil {
		if data, readErr := os.ReadFile(oldOv); readErr == nil {
			if newOv, err := OverlayPath(dstName); err == nil {
				_ = os.MkdirAll(filepath.Dir(newOv), 0o755)
				_ = os.WriteFile(newOv, data, 0o644)
			}
		}
	}

	return nil
}

// copyDir recursively copies a directory tree from src to dst. Used by
// CopyProfile to duplicate vendored spec folders.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// Delete removes a profile from disk along with its cache files.
func DeleteProfile(name string) error {
	path, err := ProfilePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing profile: %w", err)
	}
	// Best-effort cache cleanup
	if hp, err := profileHistoryPath(name); err == nil {
		_ = os.Remove(hp)
	}
	// Best-effort removal of the vendored spec folder and overlay.
	if sd, err := ProfileSpecDir(name); err == nil {
		_ = os.RemoveAll(sd)
	}
	if op, err := OverlayPath(name); err == nil {
		_ = os.Remove(op)
	}
	return nil
}

// IsVendored reports whether the profile's spec already lives under the
// vendored specs directory in ~/.feather.
func IsVendored(p *Profile) bool {
	if p == nil || p.SpecPath == "" {
		return false
	}
	dir, err := SpecsDir()
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(p.SpecPath)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dir, abs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// VendorSpec copies the profile's referenced spec into the profile's folder in
// ~/.feather and repoints SpecPath at the local copy, persisting the profile.
// It returns the new local path. Vendoring an already-vendored profile is a
// no-op.
func VendorSpec(p *Profile) (string, error) {
	if p.SpecPath == "" {
		return "", fmt.Errorf("profile %q has no spec to vendor", p.Name)
	}
	if IsVendored(p) {
		return p.SpecPath, nil
	}
	data, err := os.ReadFile(p.SpecPath)
	if err != nil {
		return "", fmt.Errorf("reading spec: %w", err)
	}
	dir, err := ProfileSpecDir(p.Name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating spec dir: %w", err)
	}
	base := filepath.Base(p.SpecPath)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "spec"
	}
	dst := filepath.Join(dir, base)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", fmt.Errorf("writing spec: %w", err)
	}
	p.SpecPath = dst
	if err := p.Save(); err != nil {
		return "", err
	}
	return dst, nil
}

// ListProfiles returns all known profiles loaded from ~/.feather/profiles.
func ListProfiles() ([]*Profile, error) {
	dir, err := ProfilesDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading profiles dir: %w", err)
	}
	var out []*Profile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		p, err := LoadProfile(name)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// FindProfilesBySpec returns profiles whose spec_path matches the given path.
// Comparison is done on resolved absolute paths.
func FindProfilesBySpec(specPath string) ([]*Profile, error) {
	target, err := resolveSpecPath(specPath)
	if err != nil {
		return nil, err
	}
	all, err := ListProfiles()
	if err != nil {
		return nil, err
	}
	var matches []*Profile
	for _, p := range all {
		resolved, err := resolveSpecPath(p.SpecPath)
		if err != nil {
			continue
		}
		if resolved == target {
			matches = append(matches, p)
		}
	}
	return matches, nil
}

// LoadIndex reads the top-level index file. Returns an empty index if absent.
func LoadIndex() (*Index, error) {
	path, err := IndexPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Index{}, nil
		}
		return nil, fmt.Errorf("reading index: %w", err)
	}
	var idx Index
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parsing index: %w", err)
	}
	return &idx, nil
}

// Save persists the index.
func (i *Index) Save() error {
	path, err := IndexPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating feather dir: %w", err)
	}
	data, err := yaml.Marshal(i)
	if err != nil {
		return fmt.Errorf("marshaling index: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing index: %w", err)
	}
	return nil
}

// LoadDefault loads the default profile if one is configured. Returns nil
// (without error) if no default is set.
func LoadDefault() (*Profile, error) {
	idx, err := LoadIndex()
	if err != nil {
		return nil, err
	}
	if idx.DefaultProfile == "" {
		return nil, nil
	}
	return LoadProfile(idx.DefaultProfile)
}

// SetDefaultProfile writes the default profile name to the index.
func SetDefaultProfile(name string) error {
	idx, err := LoadIndex()
	if err != nil {
		return err
	}
	idx.DefaultProfile = name
	return idx.Save()
}

// resolveSpecPath returns the canonical absolute path for affinity matching.
// Falls back to filepath.Clean(filepath.Abs(...)) if EvalSymlinks fails so we
// can still match profiles whose specs have since been removed.
func resolveSpecPath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty spec path")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return filepath.Clean(abs), nil
}

// sanitizeName makes a profile name safe to use as a filename.
func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ', r == '.', r == '/':
			b.WriteRune('-')
		}
	}
	out := b.String()
	if out == "" {
		out = "default"
	}
	return out
}

// SuggestProfileName derives a profile name from a spec file path.
func SuggestProfileName(specPath string) string {
	base := filepath.Base(specPath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return sanitizeName(base)
}
