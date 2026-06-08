package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/config"
	"github.com/cuppojoe/feather/internal/openapi"
	"github.com/cuppojoe/feather/internal/overlay"
	"github.com/cuppojoe/feather/internal/tui"
	"github.com/cuppojoe/feather/internal/tui/screens"
)

var (
	version = "dev"
)

func main() {
	specPath := flag.String("spec", "", "Path to OpenAPI specification file (JSON)")
	baseURL := flag.String("url", "", "Override base URL for API requests")
	profileFlag := flag.String("profile", "", "Profile name to load (see ~/.feather/profiles)")
	setDefault := flag.Bool("set-default", false, "Set the loaded profile as the default")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showVersion {
		fmt.Printf("feather %s\n", version)
		os.Exit(0)
	}

	if err := config.MigrateLegacyConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: legacy config migration failed: %v\n", err)
	}

	spec := *specPath
	if spec == "" && flag.NArg() > 0 {
		spec = flag.Arg(0)
	}

	// Loop so the TUI can request a profile switch (which restarts the app).
	nextProfile := *profileFlag
	nextSpec := spec
	for {
		profile, err := resolveProfile(nextProfile, nextSpec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if profile == nil {
			// User cancelled selection.
			os.Exit(0)
		}

		if *setDefault {
			if err := config.SetDefaultProfile(profile.Name); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not set default profile: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "Set default profile to %q\n", profile.Name)
			}
			*setDefault = false
		}

		// CLI spec override wins over profile's stored spec_path.
		effectiveSpec := profile.SpecPath
		if nextSpec != "" {
			effectiveSpec = nextSpec
		}

		// A profile may have no spec at all — it then starts from an empty
		// spec and builds a collection entirely in its overlay.
		var parsedSpec *openapi.ParsedSpec
		if effectiveSpec == "" {
			parsedSpec = &openapi.ParsedSpec{
				Info:    openapi.SpecInfo{Title: profile.Name},
				Schemas: map[string]*openapi.Schema{},
			}
		} else {
			parsedSpec, err = openapi.ParseFile(effectiveSpec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error parsing OpenAPI spec: %v\n", err)
				os.Exit(1)
			}
			// Persist the (possibly new) spec path on the profile.
			if profile.SpecPath != effectiveSpec {
				profile.SpecPath = effectiveSpec
				_ = profile.Save()
			}
		}

		if *baseURL != "" {
			profile.BaseURL = *baseURL
		}

		config.SetActiveProfile(profile.Name)

		// Load the profile's overlay. NewApp keeps the pristine spec as a base
		// and applies the overlay to produce the effective spec, so it can
		// rebuild after in-app edits.
		ov := overlay.New()
		if ovPath, perr := config.OverlayPath(profile.Name); perr == nil {
			if loaded, lerr := overlay.Load(ovPath); lerr == nil {
				ov = loaded
			} else {
				fmt.Fprintf(os.Stderr, "Warning: could not load overlay: %v\n", lerr)
			}
		}

		app := tui.NewApp(parsedSpec, profile, ov)
		p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
		if _, err := p.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error running application: %v\n", err)
			os.Exit(1)
		}

		// Did the user request a profile switch?
		switchTo := app.SwitchToProfile()
		if switchTo == "" {
			return
		}
		nextProfile = switchTo
		nextSpec = "" // honour the new profile's spec
	}
}

// resolveProfile picks the profile to load based on CLI args.
//
// Precedence:
//  1. --profile <name> wins
//  2. --spec / positional <path> filters by affinity (1=auto, >1=picker, 0=create prompt)
//  3. otherwise load the default profile
func resolveProfile(profileName, specPath string) (*config.Profile, error) {
	if profileName != "" {
		p, err := config.LoadProfile(profileName)
		if err != nil {
			return nil, fmt.Errorf("loading profile %q: %w", profileName, err)
		}
		return p, nil
	}

	if specPath != "" {
		matches, err := config.FindProfilesBySpec(specPath)
		if err != nil {
			return nil, err
		}
		switch len(matches) {
		case 1:
			return matches[0], nil
		case 0:
			return promptCreateProfile(specPath)
		default:
			return promptPickProfile(
				"Multiple profiles match this spec",
				fmt.Sprintf("Spec: %s", specPath),
				matches,
			)
		}
	}

	// No --profile, no --spec → show the splash screen so the user always
	// gets a chance to pick a profile (or land on the default).
	all, err := config.ListProfiles()
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		fmt.Fprintln(os.Stderr, "No profiles configured. Run with an OpenAPI spec to create one:")
		fmt.Fprintln(os.Stderr, "    feather <path-to-openapi.json>")
		fmt.Fprintln(os.Stderr, "    feather --spec <path-to-openapi.json>")
		os.Exit(1)
	}
	defaultName := ""
	if idx, err := config.LoadIndex(); err == nil {
		defaultName = idx.DefaultProfile
	}
	return promptSplash(all, defaultName)
}

func promptPickProfile(title, subtitle string, profiles []*config.Profile) (*config.Profile, error) {
	picker := screens.NewPicker(title, subtitle, profiles)
	if _, err := tea.NewProgram(picker, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
		return nil, err
	}
	res := picker.Result()
	if res.Cancelled {
		return nil, nil
	}
	return res.Selected, nil
}

// promptSplash runs the rainbow-logo splash + profile picker at startup.
func promptSplash(profiles []*config.Profile, defaultName string) (*config.Profile, error) {
	splash := screens.NewSplash(profiles, defaultName)
	if _, err := tea.NewProgram(splash, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
		return nil, err
	}
	res := splash.Result()
	if res.Cancelled {
		return nil, nil
	}
	return res.Selected, nil
}

func promptCreateProfile(specPath string) (*config.Profile, error) {
	suggested := config.SuggestProfileName(specPath)
	prompt := screens.NewCreatePrompt(specPath, suggested)
	if _, err := tea.NewProgram(prompt, tea.WithAltScreen()).Run(); err != nil {
		return nil, err
	}
	res := prompt.Result()
	if res.Cancelled || !res.Create {
		return nil, nil
	}

	// Ensure the chosen name is unique; if it collides, append a numeric suffix.
	name := res.Name
	if _, err := config.LoadProfile(name); err == nil {
		for i := 2; ; i++ {
			candidate := fmt.Sprintf("%s-%d", name, i)
			if _, err := config.LoadProfile(candidate); err != nil {
				name = candidate
				break
			}
		}
	}

	p := config.NewProfile(name)
	p.SpecPath = specPath
	if err := p.Save(); err != nil {
		return nil, fmt.Errorf("saving new profile: %w", err)
	}

	// If this is the very first profile, make it the default.
	idx, err := config.LoadIndex()
	if err == nil && idx.DefaultProfile == "" {
		_ = config.SetDefaultProfile(name)
	}

	return p, nil
}
