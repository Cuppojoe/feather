package shared

import (
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

// EditExternalMsg is delivered through bubbletea when an external-editor
// session ends. Caller is a free-form tag the originating component uses to
// route the message back to itself.
type EditExternalMsg struct {
	Caller   string
	ReadOnly bool
	Content  string
	Err      error
}

// OpenInEditor returns a tea.Cmd that pauses the bubbletea program, runs
// $EDITOR (defaulting to vi) on a temp file pre-filled with content, and
// then resumes the program with an EditExternalMsg carrying the file's
// post-edit contents.
//
// When readOnly is true, the editor is launched with `-R` (vi/vim view
// mode) so writes are blocked; the returned Content still reflects what
// was on disk at exit time, but callers should ignore it.
//
// extension (e.g. ".json") sets the temp file suffix so editors pick the
// right syntax highlighter.
func OpenInEditor(caller, content, extension string, readOnly bool) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	f, err := os.CreateTemp("", "feather-*"+extension)
	if err != nil {
		return func() tea.Msg {
			return EditExternalMsg{Caller: caller, ReadOnly: readOnly, Err: err}
		}
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return func() tea.Msg {
			return EditExternalMsg{Caller: caller, ReadOnly: readOnly, Err: err}
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return func() tea.Msg {
			return EditExternalMsg{Caller: caller, ReadOnly: readOnly, Err: err}
		}
	}

	args := []string{}
	if readOnly {
		args = append(args, "-R")
	}
	args = append(args, f.Name())

	cmd := exec.Command(editor, args...)
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		defer os.Remove(f.Name())
		if runErr != nil {
			return EditExternalMsg{Caller: caller, ReadOnly: readOnly, Err: runErr}
		}
		data, readErr := os.ReadFile(f.Name())
		if readErr != nil {
			return EditExternalMsg{Caller: caller, ReadOnly: readOnly, Err: readErr}
		}
		return EditExternalMsg{Caller: caller, ReadOnly: readOnly, Content: string(data)}
	})
}
