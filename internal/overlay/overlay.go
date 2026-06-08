// Package overlay implements a per-profile, feather-native overlay that
// augments a parsed OpenAPI spec. An overlay can override properties of
// existing operations (summary, description, a saved request body, default
// parameter values, headers) and add brand-new operations — so a profile can
// start with no spec at all and build a collection entirely in the overlay.
//
// Overlays are stored as YAML at ~/.feather/overlays/<profile>.yaml. Callers
// resolve the path via config.OverlayPath and pass it to Load/Save; this
// package intentionally does not import config (avoids an import cycle).
package overlay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Overlay is the root document.
type Overlay struct {
	// Categories is the persistent tag tree. Categories listed here appear in
	// navigation even when they contain no requests yet.
	Categories []string `yaml:"categories,omitempty"`
	// Operations holds overrides for existing operations, keyed by OpKey
	// ("METHOD path", e.g. "POST /orgs/{org}/items").
	Operations map[string]*OpOverride `yaml:"operations,omitempty"`
	// Added holds brand-new operations contributed by the user.
	Added []AddedOp `yaml:"added,omitempty"`
	// Scripts holds JS hooks that fire around requests. Profile-level
	// scripts apply to every request under this profile; tag-level scripts
	// apply to every request whose endpoint is in that tag.
	Scripts ScriptsSection `yaml:"scripts,omitempty"`
}

// Scripts is one (pre, post) JavaScript pair. Either field may be empty.
type Scripts struct {
	Pre  string `yaml:"pre,omitempty"`
	Post string `yaml:"post,omitempty"`
}

// IsEmpty reports whether neither phase has a script body.
func (s Scripts) IsEmpty() bool { return s.Pre == "" && s.Post == "" }

// ScriptsSection groups profile- and tag-scoped scripts. Operation-scoped
// scripts live on the OpOverride itself.
type ScriptsSection struct {
	Profile Scripts            `yaml:"profile,omitempty"`
	Tags    map[string]Scripts `yaml:"tags,omitempty"`
	// TimeoutMs caps wall-clock time per script run. 0 means "use default".
	TimeoutMs int `yaml:"timeoutMs,omitempty"`
}

// OpOverride alters properties of an existing (or added) operation.
type OpOverride struct {
	Summary       string            `yaml:"summary,omitempty"`
	Description   string            `yaml:"description,omitempty"`
	Tag           string            `yaml:"tag,omitempty"` // re-categorize an imported request
	BodyExample   string            `yaml:"bodyExample,omitempty"`
	ParamDefaults map[string]string `yaml:"paramDefaults,omitempty"`
	Headers       map[string]string `yaml:"headers,omitempty"`
	Scripts       Scripts           `yaml:"scripts,omitempty"`
}

// AddedOp defines a new operation that exists only in the overlay.
type AddedOp struct {
	Method      string            `yaml:"method"`
	Path        string            `yaml:"path"`
	Summary     string            `yaml:"summary,omitempty"`
	Description string            `yaml:"description,omitempty"`
	Tag         string            `yaml:"tag,omitempty"`
	BodyExample string            `yaml:"bodyExample,omitempty"`
	Parameters  []AddedParam      `yaml:"parameters,omitempty"`
	Headers     map[string]string `yaml:"headers,omitempty"`
}

// AddedParam is a path or query parameter for an added operation.
type AddedParam struct {
	Name     string `yaml:"name"`
	In       string `yaml:"in"` // "path" | "query"
	Required bool   `yaml:"required,omitempty"`
}

// New returns an empty, ready-to-use overlay.
func New() *Overlay {
	return &Overlay{Operations: map[string]*OpOverride{}}
}

// OpKey builds the canonical "METHOD path" key used to address an operation.
func OpKey(method, path string) string {
	return strings.ToUpper(method) + " " + path
}

// Get returns the override for an operation, or nil if none is recorded.
func (o *Overlay) Get(method, path string) *OpOverride {
	if o == nil || o.Operations == nil {
		return nil
	}
	return o.Operations[OpKey(method, path)]
}

// EffectiveOverride returns the override the request builder should use for an
// operation: a saved Operations override wins; otherwise an added operation's
// own body/headers are surfaced as an override so they prefill too.
func (o *Overlay) EffectiveOverride(method, path string) *OpOverride {
	if o == nil {
		return nil
	}
	if ovr := o.Get(method, path); ovr != nil {
		return ovr
	}
	key := OpKey(method, path)
	for i := range o.Added {
		a := o.Added[i]
		if OpKey(a.Method, a.Path) != key {
			continue
		}
		if a.BodyExample == "" && len(a.Headers) == 0 {
			return nil
		}
		return &OpOverride{BodyExample: a.BodyExample, Headers: a.Headers}
	}
	return nil
}

// SetOverride upserts the override for an operation.
func (o *Overlay) SetOverride(method, path string, ov OpOverride) {
	if o.Operations == nil {
		o.Operations = map[string]*OpOverride{}
	}
	cp := ov
	o.Operations[OpKey(method, path)] = &cp
}

// AppendAdded records a new operation.
func (o *Overlay) AppendAdded(op AddedOp) {
	o.Added = append(o.Added, op)
}

// HasAdded reports whether an added operation already exists for method+path.
func (o *Overlay) HasAdded(method, path string) bool {
	return o.addedIndex(method, path) >= 0
}

// addedIndex returns the index of the added op matching method+path, or -1.
func (o *Overlay) addedIndex(method, path string) int {
	key := OpKey(method, path)
	for i, a := range o.Added {
		if OpKey(a.Method, a.Path) == key {
			return i
		}
	}
	return -1
}

// AddedFor returns a copy of the added op matching method+path, if any.
func (o *Overlay) AddedFor(method, path string) (AddedOp, bool) {
	if i := o.addedIndex(method, path); i >= 0 {
		return o.Added[i], true
	}
	return AddedOp{}, false
}

// UpdateAdded replaces the added op identified by its old method+path with op
// (whose method/path may differ). Appends when no match exists.
func (o *Overlay) UpdateAdded(oldMethod, oldPath string, op AddedOp) {
	if i := o.addedIndex(oldMethod, oldPath); i >= 0 {
		o.Added[i] = op
		return
	}
	o.Added = append(o.Added, op)
}

// RemoveAdded deletes the added op matching method+path (and any override
// recorded under the same key).
func (o *Overlay) RemoveAdded(method, path string) {
	if i := o.addedIndex(method, path); i >= 0 {
		o.Added = append(o.Added[:i], o.Added[i+1:]...)
	}
	o.RemoveOverride(method, path)
}

// RemoveOverride deletes the operations override for method+path.
func (o *Overlay) RemoveOverride(method, path string) {
	delete(o.Operations, OpKey(method, path))
}

// HasCategory reports whether name is a declared category.
func (o *Overlay) HasCategory(name string) bool {
	for _, c := range o.Categories {
		if c == name {
			return true
		}
	}
	return false
}

// AddCategory declares a persistent category if not already present.
func (o *Overlay) AddCategory(name string) {
	if name == "" || o.HasCategory(name) {
		return
	}
	o.Categories = append(o.Categories, name)
}

// RemoveCategory drops a declared category from the persistent list. It does
// not touch requests — callers decide what happens to a non-empty category.
func (o *Overlay) RemoveCategory(name string) {
	out := o.Categories[:0]
	for _, c := range o.Categories {
		if c != name {
			out = append(out, c)
		}
	}
	o.Categories = out
}

// RenameCategory renames a declared category and retags every added op that
// referenced it. Base-spec ops are retagged by the caller via Tag overrides.
func (o *Overlay) RenameCategory(oldName, newName string) {
	if oldName == "" || newName == "" || oldName == newName {
		return
	}
	for i, c := range o.Categories {
		if c == oldName {
			o.Categories[i] = newName
		}
	}
	for i := range o.Added {
		if o.Added[i].Tag == oldName {
			o.Added[i].Tag = newName
		}
	}
}

// Load reads an overlay from path. A missing file yields a fresh empty overlay.
func Load(path string) (*Overlay, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, fmt.Errorf("reading overlay: %w", err)
	}
	var o Overlay
	if err := yaml.Unmarshal(data, &o); err != nil {
		return nil, fmt.Errorf("parsing overlay %s: %w", path, err)
	}
	if o.Operations == nil {
		o.Operations = map[string]*OpOverride{}
	}
	return &o, nil
}

// ProfileScripts returns the profile-scoped scripts (zero value if unset).
func (o *Overlay) ProfileScripts() Scripts {
	if o == nil {
		return Scripts{}
	}
	return o.Scripts.Profile
}

// TagScripts returns the script pair for a tag, or zero when none is set.
func (o *Overlay) TagScripts(tag string) Scripts {
	if o == nil || o.Scripts.Tags == nil {
		return Scripts{}
	}
	return o.Scripts.Tags[tag]
}

// OperationScripts returns the script pair for an operation, or zero.
func (o *Overlay) OperationScripts(method, path string) Scripts {
	if o == nil {
		return Scripts{}
	}
	if ovr := o.Get(method, path); ovr != nil {
		return ovr.Scripts
	}
	return Scripts{}
}

// SetProfileScripts replaces the profile-scoped scripts.
func (o *Overlay) SetProfileScripts(s Scripts) { o.Scripts.Profile = s }

// SetTagScripts replaces the scripts for tag (empty erases the entry).
func (o *Overlay) SetTagScripts(tag string, s Scripts) {
	if s.IsEmpty() {
		delete(o.Scripts.Tags, tag)
		return
	}
	if o.Scripts.Tags == nil {
		o.Scripts.Tags = map[string]Scripts{}
	}
	o.Scripts.Tags[tag] = s
}

// SetOperationScripts replaces the scripts on an operation override,
// creating the override entry if necessary. Erasing both phases on an
// otherwise-empty override is a no-op (we don't manufacture overrides
// just to clear them).
func (o *Overlay) SetOperationScripts(method, path string, s Scripts) {
	if o.Operations == nil {
		o.Operations = map[string]*OpOverride{}
	}
	key := OpKey(method, path)
	ovr, ok := o.Operations[key]
	if !ok {
		if s.IsEmpty() {
			return
		}
		ovr = &OpOverride{}
		o.Operations[key] = ovr
	}
	ovr.Scripts = s
}

// Save writes the overlay to path, creating parent directories as needed.
func (o *Overlay) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating overlay dir: %w", err)
	}
	data, err := yaml.Marshal(o)
	if err != nil {
		return fmt.Errorf("encoding overlay: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing overlay: %w", err)
	}
	return nil
}
