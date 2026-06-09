package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Environment is a swappable named bundle of variables — the same role
// Postman environments play. One environment is "active" per profile at a
// time; its values feed every templated header / base URL / script
// reference.
//
// Each value is an EnvValue (Value + Sensitive). Plain YAML scalars are
// accepted on read for ergonomic hand-authoring; on save, sensitive
// entries are written as the explicit object form while plain entries
// stay as bare scalars so a typical env file is still compact.
//
// Environments live under ~/.feather/environments/<name>.yaml. They are
// global — multiple profiles can share the same environment.
type Environment struct {
	Name   string              `yaml:"name"`
	Values map[string]EnvValue `yaml:"values,omitempty"`
}

// EnvValue carries one variable's value along with a sensitive flag.
// Display surfaces honour the flag by masking the value behind bullets
// (e.g. the env modal's K/V editor) so secrets aren't shown unmasked
// just by glancing at the modal.
type EnvValue struct {
	Value     string `yaml:"value"`
	Sensitive bool   `yaml:"sensitive,omitempty"`
}

// UnmarshalYAML lets EnvValue be written either as a bare scalar (the
// common case — most variables aren't sensitive) or as an object with
// `value:` and `sensitive:` fields.
//
//	apiToken:
//	  value: eyJ…
//	  sensitive: true
//	org: kyle-test-org
func (v *EnvValue) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		v.Value = node.Value
		v.Sensitive = false
		return nil
	}
	// Decode through a synonym type so we don't recurse into ourselves.
	type plain EnvValue
	var p plain
	if err := node.Decode(&p); err != nil {
		return err
	}
	*v = EnvValue(p)
	return nil
}

// MarshalYAML emits a bare scalar for non-sensitive values to keep
// environment files compact, and the explicit object form when the
// value is sensitive so the flag survives the round-trip.
func (v EnvValue) MarshalYAML() (any, error) {
	if !v.Sensitive {
		return v.Value, nil
	}
	return map[string]any{
		"value":     v.Value,
		"sensitive": true,
	}, nil
}

// PlainValues returns the underlying string map suitable for feeding into
// the runtime context (template substitution, header values, etc.).
func (e *Environment) PlainValues() map[string]string {
	if e == nil {
		return nil
	}
	out := make(map[string]string, len(e.Values))
	for k, v := range e.Values {
		out[k] = v.Value
	}
	return out
}

// SensitiveKeys returns the names of variables marked sensitive,
// alphabetised for stable iteration.
func (e *Environment) SensitiveKeys() []string {
	if e == nil {
		return nil
	}
	var out []string
	for k, v := range e.Values {
		if v.Sensitive {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// EnvironmentsDir returns the directory that holds environment YAML files.
func EnvironmentsDir() (string, error) {
	root, err := FeatherDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "environments"), nil
}

// EnvironmentPath returns the YAML path for a named environment.
func EnvironmentPath(name string) (string, error) {
	dir, err := EnvironmentsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sanitizeName(name)+".yaml"), nil
}

// NewEnvironment returns an empty named environment with no variables.
func NewEnvironment(name string) *Environment {
	return &Environment{Name: name, Values: map[string]EnvValue{}}
}

// LoadEnvironment reads an environment by name.
func LoadEnvironment(name string) (*Environment, error) {
	path, err := EnvironmentPath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading environment %q: %w", name, err)
	}
	var env Environment
	if err := yaml.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("parsing environment %q: %w", name, err)
	}
	if env.Name == "" {
		env.Name = name
	}
	if env.Values == nil {
		env.Values = map[string]EnvValue{}
	}
	return &env, nil
}

// Save writes the environment to ~/.feather/environments/<name>.yaml.
func (e *Environment) Save() error {
	if e.Name == "" {
		return fmt.Errorf("environment has no name")
	}
	path, err := EnvironmentPath(e.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating environments dir: %w", err)
	}
	data, err := yaml.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshaling environment: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing environment: %w", err)
	}
	return nil
}

// ListEnvironments returns all environments on disk, alphabetised.
func ListEnvironments() ([]*Environment, error) {
	dir, err := EnvironmentsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading environments dir: %w", err)
	}
	var out []*Environment
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".yaml") {
			continue
		}
		name := strings.TrimSuffix(ent.Name(), ".yaml")
		env, err := LoadEnvironment(name)
		if err != nil {
			continue
		}
		out = append(out, env)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteEnvironment removes the named environment from disk.
func DeleteEnvironment(name string) error {
	path, err := EnvironmentPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing environment: %w", err)
	}
	return nil
}

// RenameEnvironment moves an environment YAML from oldName to newName.
// Refuses to clobber an existing target.
func RenameEnvironment(oldName, newName string) error {
	if strings.TrimSpace(oldName) == "" || strings.TrimSpace(newName) == "" {
		return fmt.Errorf("environment names cannot be empty")
	}
	if sanitizeName(oldName) == sanitizeName(newName) {
		return nil
	}
	newPath, err := EnvironmentPath(newName)
	if err != nil {
		return err
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("environment %q already exists", newName)
	}
	env, err := LoadEnvironment(oldName)
	if err != nil {
		return fmt.Errorf("loading source environment: %w", err)
	}
	env.Name = newName
	if err := env.Save(); err != nil {
		return fmt.Errorf("saving renamed environment: %w", err)
	}
	oldPath, err := EnvironmentPath(oldName)
	if err != nil {
		return err
	}
	if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing old environment: %w", err)
	}
	return nil
}

// CopyEnvironment duplicates an environment under a new name, deep-copying
// its Values map so subsequent edits don't bleed across the source.
func CopyEnvironment(srcName, dstName string) error {
	if strings.TrimSpace(srcName) == "" || strings.TrimSpace(dstName) == "" {
		return fmt.Errorf("environment names cannot be empty")
	}
	if sanitizeName(srcName) == sanitizeName(dstName) {
		return fmt.Errorf("source and destination resolve to the same file")
	}
	newPath, err := EnvironmentPath(dstName)
	if err != nil {
		return err
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("environment %q already exists", dstName)
	}
	src, err := LoadEnvironment(srcName)
	if err != nil {
		return fmt.Errorf("loading source environment: %w", err)
	}
	values := make(map[string]EnvValue, len(src.Values))
	for k, v := range src.Values {
		values[k] = v
	}
	dst := &Environment{Name: dstName, Values: values}
	return dst.Save()
}
