package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// activeProfile is the profile name used by no-arg cache/history helpers.
// It defaults to "default" so legacy paths still work; main() sets it on
// startup once the active profile is resolved.
var activeProfile = "default"

// SetActiveProfile assigns the active profile name for subsequent
// cache/history reads and writes that don't take an explicit name.
func SetActiveProfile(name string) {
	if name == "" {
		return
	}
	activeProfile = name
}

// ActiveProfile returns the currently active profile name.
func ActiveProfile() string {
	return activeProfile
}

func profileHistoryPath(profile string) (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sanitizeName(profile)+".history.json"), nil
}

// HistoryEntry represents a history entry for requests
type HistoryEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	StatusCode  int       `json:"status_code"`
	Duration    string    `json:"duration"`
	RequestBody string    `json:"request_body,omitempty"`
}

// History holds request history
type History struct {
	Entries []HistoryEntry `json:"entries"`
}

// HistoryPath returns the path to the active profile's history file.
func HistoryPath() (string, error) {
	return profileHistoryPath(activeProfile)
}

// LoadHistory loads request history from disk
func LoadHistory() (*History, error) {
	path, err := HistoryPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &History{}, nil
		}
		return nil, fmt.Errorf("reading history: %w", err)
	}

	var history History
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, fmt.Errorf("parsing history: %w", err)
	}

	return &history, nil
}

// Save saves request history to disk
func (h *History) Save() error {
	path, err := HistoryPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating history directory: %w", err)
	}

	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling history: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing history: %w", err)
	}

	return nil
}

// AddEntry adds a new history entry (keeping last 100)
func (h *History) AddEntry(entry HistoryEntry) {
	h.Entries = append(h.Entries, entry)

	if len(h.Entries) > 100 {
		h.Entries = h.Entries[len(h.Entries)-100:]
	}
}

// Clear removes all history entries
func (h *History) Clear() {
	h.Entries = nil
}
