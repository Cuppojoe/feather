package shared

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/aymanbagabas/go-osc52/v2"
	"golang.design/x/clipboard"
	wlcopy "wayland-go-copy"
)

var nativeInitErr = clipboard.Init()

func tryNativeClipboard(text string) (string, bool) {
	if nativeInitErr != nil {
		return "", false
	}
	clipboard.Write(clipboard.FmtText, []byte(text))
	return "golang.design/x/clipboard", true
}

// CopyResult describes how a clipboard copy succeeded (or failed).
type CopyResult struct {
	Method string
	Err    error
}

// clipboardState holds the long-lived state that the Wayland fallback needs.
// Wayland selection ownership requires the owning process to stay alive and
// serve paste requests on demand, so we keep a goroutine running and cancel
// the previous one whenever a new copy starts.
var clipboardState struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// CopyToClipboard runs the three-layer fallback:
//  1. OSC 52 — only on terminals known to support clipboard writes.
//  2. golang.design/x/clipboard — X11 via libX11 (dlopen at runtime),
//     macOS Cocoa, or Win32.
//  3. wayland-go-copy — pure-Go Wayland wire-protocol client; takes over on
//     Wayland-only sessions where layer 2 can't find an X server.
func CopyToClipboard(text string) CopyResult {
	// Layer 1: OSC 52, only when the terminal definitely supports writes.
	if method, ok := osc52IfSupported(text); ok {
		return CopyResult{Method: method}
	}

	// Layer 2: golang.design/x/clipboard (when compiled in).
	if method, ok := tryNativeClipboard(text); ok {
		return CopyResult{Method: method}
	}

	// Layer 3: Wayland direct wire protocol.
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if err := startWlcopy(text); err != nil {
			return CopyResult{Err: fmt.Errorf("wlcopy: %w", err)}
		}
		return CopyResult{Method: "wayland"}
	}

	return CopyResult{Err: fmt.Errorf("no clipboard backend available")}
}

// startWlcopy launches wlcopy.Copy in a goroutine, replacing any previous
// holder. It waits briefly to see if the call errors immediately (e.g.
// "no clipboard path available") and surfaces that as a failure; otherwise
// the goroutine keeps the selection until feather exits or another client
// takes the clipboard.
func startWlcopy(text string) error {
	clipboardState.mu.Lock()
	defer clipboardState.mu.Unlock()

	if clipboardState.cancel != nil {
		clipboardState.cancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	clipboardState.cancel = cancel

	errCh := make(chan error, 1)
	go func() { errCh <- wlcopy.Copy(ctx, text) }()

	// Give the lib a short window to discover unsupported globals / fail
	// to dial the socket. If it's still serving after that we declare
	// success and let it run.
	select {
	case err := <-errCh:
		// Errored before we could return — propagate.
		clipboardState.cancel = nil
		return err
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

// osc52IfSupported writes the OSC 52 sequence and returns true when the
// current terminal definitely supports clipboard writes. VTE-based terminals
// (GNOME Terminal, Tilix, Terminator) disable OSC 52 writes by default, so
// they are deliberately excluded.
func osc52IfSupported(text string) (string, bool) {
	if env := os.Getenv("TMUX"); env != "" {
		fmt.Fprint(os.Stderr, osc52.New(text).Tmux())
		return "osc52 (tmux)", true
	}
	if os.Getenv("STY") != "" { // GNU screen
		fmt.Fprint(os.Stderr, osc52.New(text).Screen())
		return "osc52 (screen)", true
	}

	known := []string{
		"KITTY_PID",    // kitty
		"WEZTERM_PANE", // wezterm
		"WT_SESSION",   // Windows Terminal
		"GHOSTTY_RESOURCES_DIR",
		"ALACRITTY_LOG", // alacritty leaves this only when launched with --log-file, unreliable
	}
	for _, k := range known {
		if os.Getenv(k) != "" {
			fmt.Fprint(os.Stderr, osc52.New(text))
			return "osc52", true
		}
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "ghostty", "vscode":
		fmt.Fprint(os.Stderr, osc52.New(text))
		return "osc52", true
	}
	if term := os.Getenv("TERM"); term == "foot" || term == "foot-extra" {
		fmt.Fprint(os.Stderr, osc52.New(text))
		return "osc52", true
	}
	return "", false
}
