package screens

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/config"
	"github.com/cuppojoe/feather/internal/tui/shared"
)

// PickerResult captures the outcome of the profile picker.
type PickerResult struct {
	Selected  *config.Profile
	Cancelled bool
}

// PickerModel is a small bubbletea program that prompts the user to pick a
// profile from a list. Used pre-TUI when multiple profiles match a spec,
// and as the splash screen at startup. When `splash` is true the rainbow
// ASCII logo is rendered above the picker box.
type PickerModel struct {
	title       string
	subtitle    string
	profiles    []*config.Profile
	defaultName string
	splash      bool
	cursor      int
	width       int
	height      int
	result      PickerResult
	done        bool
	rowYs       []int // screen-relative Y for each profile row, set by View
}

// NewPicker creates a picker prompting selection from the given list.
func NewPicker(title, subtitle string, profiles []*config.Profile) *PickerModel {
	return &PickerModel{
		title:    title,
		subtitle: subtitle,
		profiles: profiles,
	}
}

// NewSplash creates the startup splash: rainbow logo above a profile picker.
// defaultName is highlighted in the list and the cursor lands on it.
func NewSplash(profiles []*config.Profile, defaultName string) *PickerModel {
	m := &PickerModel{
		title:       "Choose a profile",
		profiles:    profiles,
		defaultName: defaultName,
		splash:      true,
	}
	for i, p := range profiles {
		if p.Name == defaultName {
			m.cursor = i
			break
		}
	}
	return m
}

func (m *PickerModel) Init() tea.Cmd { return nil }

func (m *PickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.cursor > 0 {
				m.cursor--
			}
		case tea.MouseButtonWheelDown:
			if m.cursor < len(m.profiles)-1 {
				m.cursor++
			}
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionRelease {
				for i, y := range m.rowYs {
					if msg.Y == y {
						m.cursor = i
						m.result.Selected = m.profiles[i]
						m.done = true
						return m, tea.Quit
					}
				}
			}
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.result.Cancelled = true
			m.done = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.profiles)-1 {
				m.cursor++
			}
		case "enter":
			if len(m.profiles) > 0 {
				m.result.Selected = m.profiles[m.cursor]
				m.done = true
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m *PickerModel) View() string {
	if m.done {
		return ""
	}
	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render(m.title))
	b.WriteString("\n")
	if m.subtitle != "" {
		b.WriteString(shared.DimStyle.Render(m.subtitle))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Record the screen-relative Y of each profile row so mouse clicks can
	// be resolved without re-deriving the layout. The first profile lives
	// at builder line count + box offset (border+top-padding = 2) + logo
	// offset (logo lines + 1 blank = 6) when in splash mode.
	firstProfileBuilderRow := strings.Count(b.String(), "\n")
	screenOffset := 2 // box border-top + padding-top
	if m.splash {
		screenOffset += len(shared.LogoLines) + 1
	}
	m.rowYs = m.rowYs[:0]
	for i := range m.profiles {
		m.rowYs = append(m.rowYs, screenOffset+firstProfileBuilderRow+i)
	}

	for i, p := range m.profiles {
		cursor := "  "
		name := p.Name
		if i == m.cursor {
			cursor = shared.CursorStyle.Render("> ")
			name = shared.SelectedStyle.Render(name)
		} else {
			name = shared.NormalStyle.Render(name)
		}
		markers := ""
		if p.Name == m.defaultName {
			markers = " " + shared.WarningStyle.Render("★")
		}
		line := fmt.Sprintf("%s%s%s", cursor, name, markers)
		if p.SpecPath != "" {
			line += "  " + shared.DimStyle.Render(p.SpecPath)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render("[↑/↓] move  [enter] select  [esc] cancel"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(shared.ColorPrimary).
		Padding(1, 2).
		Render(b.String())

	if !m.splash {
		return box
	}

	// Splash: rainbow logo centred above the picker, all centred on screen.
	width := m.width
	if width <= 0 {
		width = 80
	}
	logo := shared.RenderLogo(width)
	stack := lipgloss.JoinVertical(lipgloss.Center, logo, "", box)
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(stack)
}

// Result returns the selection (only valid after the program exits).
func (m *PickerModel) Result() PickerResult { return m.result }

// PromptCreate asks whether to create a new profile for an unmatched spec.
// Returns (createIt, profileName, cancelled).
type CreatePromptResult struct {
	Create    bool
	Name      string
	Cancelled bool
}

type CreateModel struct {
	specPath    string
	defaultName string
	input       string
	stage       int // 0=confirm, 1=name
	cursor      int
	result      CreatePromptResult
	done        bool
}

func NewCreatePrompt(specPath, defaultName string) *CreateModel {
	return &CreateModel{
		specPath:    specPath,
		defaultName: defaultName,
		input:       defaultName,
	}
}

func (m *CreateModel) Init() tea.Cmd { return nil }

func (m *CreateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch m.stage {
		case 0:
			switch km.String() {
			case "y", "Y", "enter":
				m.stage = 1
			case "n", "N", "esc", "ctrl+c", "q":
				m.result.Cancelled = true
				m.done = true
				return m, tea.Quit
			}
		case 1:
			switch km.String() {
			case "enter":
				name := strings.TrimSpace(m.input)
				if name == "" {
					name = m.defaultName
				}
				m.result.Create = true
				m.result.Name = name
				m.done = true
				return m, tea.Quit
			case "esc", "ctrl+c":
				m.result.Cancelled = true
				m.done = true
				return m, tea.Quit
			case "backspace":
				if len(m.input) > 0 {
					m.input = m.input[:len(m.input)-1]
				}
			default:
				if len(km.String()) == 1 {
					m.input += km.String()
				}
			}
		}
	}
	return m, nil
}

func (m *CreateModel) View() string {
	if m.done {
		return ""
	}
	var b strings.Builder
	b.WriteString(shared.TitleStyle.Render("No matching profile"))
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(fmt.Sprintf("Spec: %s", m.specPath)))
	b.WriteString("\n\n")
	if m.stage == 0 {
		b.WriteString("Create a new profile for this spec? ")
		b.WriteString(shared.SelectedStyle.Render("[Y/n]"))
	} else {
		b.WriteString("Profile name: ")
		b.WriteString(shared.SelectedStyle.Render(m.input + "_"))
		b.WriteString("\n")
		b.WriteString(shared.DimStyle.Render("[enter] confirm  [esc] cancel"))
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(shared.ColorPrimary).
		Padding(1, 2).
		Render(b.String())
}

func (m *CreateModel) Result() CreatePromptResult { return m.result }
