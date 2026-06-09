package panels

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/config"
	"github.com/cuppojoe/feather/internal/tui/shared"
)

// profileView is the current sub-view of the profile modal.
type profileView int

const (
	profileViewList profileView = iota
	profileViewDetails
)

// editField identifies which detail field is being edited.
type editField int

const (
	editNone editField = iota
	editName
	editSpec
	editBaseURL
	// editCopy is "typing the name for a new profile created from the
	// currently selected one". Only reachable from the list view via 'y';
	// commits via config.CopyProfile rather than mutating the source.
	editCopy
)

// editableFields lists the detail-view rows the cursor can land on, in order.
var editableFields = []editField{editName, editSpec, editBaseURL}

// ProfilePanel is a modal picker that lets the user switch profiles in-TUI.
type ProfilePanel struct {
	profiles      []*config.Profile
	activeName    string
	defaultName   string
	cursor        int
	detailsCursor int // index into editableFields when in details view
	expanded      bool
	focused       bool
	keys          shared.KeyMap
	view          profileView
	confirmDel    bool   // true if a delete is pending confirmation
	statusMsg     string // transient message shown under the list
	editing       editField
	input         textinput.Model
	clickMap      shared.ClickMap
	footerHints   shared.HintBar // clickable close hint in the title bar
}

// ProfilePanelResult communicates the user's choice back to the app.
type ProfilePanelResult struct {
	Switch      bool   // user picked a (different) profile
	ProfileName string // selected/renamed profile name
	Cmd         tea.Cmd
}

func NewProfilePanel(active string, keys shared.KeyMap) *ProfilePanel {
	ti := textinput.New()
	ti.CharLimit = 500
	p := &ProfilePanel{activeName: active, keys: keys, input: ti}
	p.reload()
	return p
}

func (p *ProfilePanel) reload() {
	all, _ := config.ListProfiles()
	p.profiles = all
	if idx, err := config.LoadIndex(); err == nil {
		p.defaultName = idx.DefaultProfile
	}
	if p.cursor >= len(p.profiles) {
		p.cursor = len(p.profiles) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	// On first reload, snap the cursor to the active profile so the user
	// lands on something familiar.
	if p.cursor == 0 {
		for i, prof := range p.profiles {
			if prof.Name == p.activeName {
				p.cursor = i
				break
			}
		}
	}
}

func (p *ProfilePanel) SetActive(name string) { p.activeName = name }

func (p *ProfilePanel) IsExpanded() bool { return p.expanded }
func (p *ProfilePanel) IsFocused() bool  { return p.focused }
func (p *ProfilePanel) IsEditing() bool  { return p.editing != editNone }

func (p *ProfilePanel) SetFocused(focused bool) { p.focused = focused }

func (p *ProfilePanel) Toggle() {
	p.expanded = !p.expanded
	if p.expanded {
		p.reload()
		p.view = profileViewList
		p.confirmDel = false
		p.statusMsg = ""
		p.editing = editNone
	}
}

func (p *ProfilePanel) selected() *config.Profile {
	if p.cursor < 0 || p.cursor >= len(p.profiles) {
		return nil
	}
	return p.profiles[p.cursor]
}

func (p *ProfilePanel) Update(msg tea.Msg) ProfilePanelResult {
	if !p.expanded {
		return ProfilePanelResult{}
	}

	// Field text-input mode owns the input completely.
	if p.editing != editNone {
		return p.updateFieldEdit(msg)
	}

	// Mouse: route through the same per-view handler as the keyboard does.
	if mouse, ok := msg.(tea.MouseMsg); ok {
		return p.updateMouse(mouse)
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return ProfilePanelResult{}
	}

	if p.view == profileViewDetails {
		return p.updateDetails(km)
	}
	return p.updateList(km)
}

// updateMouse handles wheel scroll and left-click hit-tests via clickMap.
// Action IDs are caller-defined: list view uses profile index (>= 0),
// details view uses editField constants. Clicking is equivalent to pressing
// Enter on the same row, so we synthesise a key event afterwards.
func (p *ProfilePanel) updateMouse(msg tea.MouseMsg) ProfilePanelResult {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if p.view == profileViewList && p.cursor > 0 {
			p.cursor--
		}
	case tea.MouseButtonWheelDown:
		if p.view == profileViewList && p.cursor < len(p.profiles)-1 {
			p.cursor++
		}
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionRelease {
			return ProfilePanelResult{}
		}
		// Clicking the title-bar close hint closes the modal.
		if k, ok := p.footerHints.HitKey(msg.X, msg.Y); ok && k == "esc" {
			if p.editing == editNone {
				p.expanded = false
			}
			return ProfilePanelResult{}
		}
		id, ok := p.clickMap.Hit(msg.X, msg.Y)
		if !ok {
			return ProfilePanelResult{}
		}
		if p.view == profileViewList {
			if id >= 0 && id < len(p.profiles) {
				p.cursor = id
				sel := p.profiles[id]
				res := ProfilePanelResult{
					Switch:      sel.Name != p.activeName,
					ProfileName: sel.Name,
				}
				p.expanded = false
				return res
			}
		} else {
			// Details view: id is an editField sentinel.
			field := editField(id)
			sel := p.selected()
			if sel != nil {
				p.detailsCursor = indexOfField(field)
				p.startFieldEdit(field, currentFieldValue(sel, field))
			}
		}
	}
	return ProfilePanelResult{}
}

func (p *ProfilePanel) updateList(km tea.KeyMsg) ProfilePanelResult {
	// esc cancels a pending delete first, then closes the modal.
	if km.String() == "esc" {
		if p.confirmDel {
			p.confirmDel = false
			p.statusMsg = ""
			return ProfilePanelResult{}
		}
		p.expanded = false
		return ProfilePanelResult{}
	}

	// Any non-x key cancels a pending delete.
	if p.confirmDel && km.String() != "x" {
		p.confirmDel = false
		p.statusMsg = ""
	}

	switch {
	case key.Matches(km, p.keys.Up):
		if p.cursor > 0 {
			p.cursor--
			p.statusMsg = ""
		}
	case key.Matches(km, p.keys.Down):
		if p.cursor < len(p.profiles)-1 {
			p.cursor++
			p.statusMsg = ""
		}
	case km.String() == "i", km.String() == "right":
		if p.selected() != nil {
			p.view = profileViewDetails
			p.detailsCursor = 0
		}
	case km.String() == "*":
		if sel := p.selected(); sel != nil {
			if err := config.SetDefaultProfile(sel.Name); err != nil {
				p.statusMsg = shared.ErrorStyle.Render("Could not set default: " + err.Error())
			} else {
				p.defaultName = sel.Name
				p.statusMsg = shared.SuccessStyle.Render(fmt.Sprintf("Default is now %q", sel.Name))
			}
		}
	case km.String() == "y":
		// Duplicate the highlighted profile. Drops the user into a name
		// prompt prefilled with "<source>-copy" so a single press is the
		// fastest path to a working scratch profile; they can edit the
		// suggested name in-place before pressing Enter.
		if sel := p.selected(); sel != nil {
			p.confirmDel = false
			p.startFieldEdit(editCopy, sel.Name+"-copy")
		}
	case km.String() == "x":
		sel := p.selected()
		if sel == nil {
			break
		}
		if !p.confirmDel {
			p.confirmDel = true
			p.statusMsg = shared.WarningStyle.Render(
				fmt.Sprintf("Press x again to delete %q (esc to cancel)", sel.Name))
			break
		}
		// Second press: actually delete.
		p.confirmDel = false
		if sel.Name == p.activeName {
			p.statusMsg = shared.ErrorStyle.Render("Cannot delete the active profile")
			break
		}
		if err := config.DeleteProfile(sel.Name); err != nil {
			p.statusMsg = shared.ErrorStyle.Render("Delete failed: " + err.Error())
			break
		}
		if sel.Name == p.defaultName {
			_ = config.SetDefaultProfile("")
		}
		p.reload()
		p.statusMsg = shared.SuccessStyle.Render(fmt.Sprintf("Deleted %q", sel.Name))
	case key.Matches(km, p.keys.Enter):
		if sel := p.selected(); sel != nil {
			res := ProfilePanelResult{
				Switch:      sel.Name != p.activeName,
				ProfileName: sel.Name,
			}
			p.expanded = false
			return res
		}
	}
	return ProfilePanelResult{}
}

func (p *ProfilePanel) updateDetails(km tea.KeyMsg) ProfilePanelResult {
	sel := p.selected()

	switch {
	case km.String() == "esc", km.String() == "left":
		if p.confirmDel {
			p.confirmDel = false
			p.statusMsg = ""
			return ProfilePanelResult{}
		}
		p.view = profileViewList
		p.statusMsg = ""
		return ProfilePanelResult{}
	case key.Matches(km, p.keys.Up):
		if p.detailsCursor > 0 {
			p.detailsCursor--
		}
		return ProfilePanelResult{}
	case key.Matches(km, p.keys.Down):
		if p.detailsCursor < len(editableFields)-1 {
			p.detailsCursor++
		}
		return ProfilePanelResult{}
	}

	switch km.String() {
	case "n":
		if sel != nil {
			p.detailsCursor = indexOfField(editName)
			p.startFieldEdit(editName, sel.Name)
		}
	case "s":
		if sel != nil {
			p.detailsCursor = indexOfField(editSpec)
			p.startFieldEdit(editSpec, sel.SpecPath)
		}
	case "u":
		if sel != nil {
			p.detailsCursor = indexOfField(editBaseURL)
			p.startFieldEdit(editBaseURL, sel.BaseURL)
		}
	case "v":
		if sel != nil {
			if config.IsVendored(sel) {
				p.statusMsg = shared.DimStyle.Render("Spec is already vendored")
			} else if sel.SpecPath == "" {
				p.statusMsg = shared.ErrorStyle.Render("No spec to vendor")
			} else if _, err := config.VendorSpec(sel); err != nil {
				p.statusMsg = shared.ErrorStyle.Render("Vendor failed: " + err.Error())
			} else {
				p.reload()
				p.statusMsg = shared.SuccessStyle.Render("Vendored spec into ~/.feather")
			}
		}
	case "*":
		if sel != nil {
			if err := config.SetDefaultProfile(sel.Name); err == nil {
				p.defaultName = sel.Name
				p.statusMsg = shared.SuccessStyle.Render(fmt.Sprintf("Default is now %q", sel.Name))
			}
		}
	case "x":
		if sel == nil {
			return ProfilePanelResult{}
		}
		if !p.confirmDel {
			p.confirmDel = true
			p.statusMsg = shared.WarningStyle.Render(
				fmt.Sprintf("Press x again to delete %q (esc to cancel)", sel.Name))
			return ProfilePanelResult{}
		}
		p.confirmDel = false
		if sel.Name == p.activeName {
			p.statusMsg = shared.ErrorStyle.Render("Cannot delete the active profile")
			return ProfilePanelResult{}
		}
		if err := config.DeleteProfile(sel.Name); err != nil {
			p.statusMsg = shared.ErrorStyle.Render("Delete failed: " + err.Error())
			return ProfilePanelResult{}
		}
		if sel.Name == p.defaultName {
			_ = config.SetDefaultProfile("")
		}
		p.reload()
		p.view = profileViewList
		p.statusMsg = shared.SuccessStyle.Render(fmt.Sprintf("Deleted %q", sel.Name))
	case "enter":
		// Edit the highlighted field, mirroring the context modal pattern.
		if sel != nil && p.detailsCursor >= 0 && p.detailsCursor < len(editableFields) {
			field := editableFields[p.detailsCursor]
			p.startFieldEdit(field, currentFieldValue(sel, field))
		}
	}

	// Any other key cancels a pending delete in details view.
	if p.confirmDel && km.String() != "x" && km.String() != "esc" {
		p.confirmDel = false
		p.statusMsg = ""
	}
	return ProfilePanelResult{}
}

func (p *ProfilePanel) startFieldEdit(field editField, current string) {
	p.editing = field
	p.input.SetValue(current)
	p.input.CursorEnd()
	p.input.Focus()
	p.statusMsg = ""
}

func (p *ProfilePanel) cancelFieldEdit() {
	p.editing = editNone
	p.input.Blur()
	p.input.SetValue("")
}

// updateFieldEdit handles the text-input mode for editing a single field.
func (p *ProfilePanel) updateFieldEdit(msg tea.Msg) ProfilePanelResult {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			p.cancelFieldEdit()
			return ProfilePanelResult{}
		case "enter":
			return p.commitFieldEdit()
		}
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return ProfilePanelResult{Cmd: cmd}
}

// commitFieldEdit applies the in-progress edit to the selected profile.
func (p *ProfilePanel) commitFieldEdit() ProfilePanelResult {
	sel := p.selected()
	if sel == nil {
		p.cancelFieldEdit()
		return ProfilePanelResult{}
	}
	newValue := strings.TrimSpace(p.input.Value())
	field := p.editing
	p.cancelFieldEdit()

	switch field {
	case editName:
		if newValue == "" {
			p.statusMsg = shared.ErrorStyle.Render("Profile name cannot be empty")
			return ProfilePanelResult{}
		}
		if newValue == sel.Name {
			return ProfilePanelResult{}
		}
		wasActive := sel.Name == p.activeName
		if err := config.RenameProfile(sel.Name, newValue); err != nil {
			p.statusMsg = shared.ErrorStyle.Render("Rename failed: " + err.Error())
			return ProfilePanelResult{}
		}
		// Reload list and re-position cursor on the renamed profile.
		p.reload()
		for i, prof := range p.profiles {
			if prof.Name == newValue {
				p.cursor = i
				break
			}
		}
		if wasActive {
			// In-memory state still references the old name (a.config.Name,
			// active-profile cache key). Signal a restart to get back in sync.
			p.activeName = newValue
			res := ProfilePanelResult{Switch: true, ProfileName: newValue}
			p.expanded = false
			return res
		}
		p.statusMsg = shared.SuccessStyle.Render(
			fmt.Sprintf("Renamed to %q", newValue))
	case editSpec:
		if newValue == sel.SpecPath {
			return ProfilePanelResult{}
		}
		sel.SpecPath = newValue
		if err := sel.Save(); err != nil {
			p.statusMsg = shared.ErrorStyle.Render("Save failed: " + err.Error())
			return ProfilePanelResult{}
		}
		if sel.Name == p.activeName {
			// Spec is loaded at startup; reload to take effect.
			res := ProfilePanelResult{Switch: true, ProfileName: sel.Name}
			p.expanded = false
			return res
		}
		p.statusMsg = shared.SuccessStyle.Render("Spec path updated")
	case editBaseURL:
		if newValue == sel.BaseURL {
			return ProfilePanelResult{}
		}
		sel.BaseURL = newValue
		if err := sel.Save(); err != nil {
			p.statusMsg = shared.ErrorStyle.Render("Save failed: " + err.Error())
			return ProfilePanelResult{}
		}
		p.statusMsg = shared.SuccessStyle.Render("Base URL updated")
	case editCopy:
		if newValue == "" {
			p.statusMsg = shared.ErrorStyle.Render("Profile name cannot be empty")
			return ProfilePanelResult{}
		}
		if err := config.CopyProfile(sel.Name, newValue); err != nil {
			p.statusMsg = shared.ErrorStyle.Render("Copy failed: " + err.Error())
			return ProfilePanelResult{}
		}
		p.reload()
		// Land the cursor on the new copy so the user sees it immediately.
		for i, prof := range p.profiles {
			if prof.Name == newValue {
				p.cursor = i
				break
			}
		}
		p.statusMsg = shared.SuccessStyle.Render(
			fmt.Sprintf("Copied %q to %q", sel.Name, newValue))
	}
	return ProfilePanelResult{}
}

// ViewModal renders the picker as a centered modal.
func (p *ProfilePanel) ViewModal(screenWidth, screenHeight int) string {
	modalWidth := min(92, screenWidth-8)
	// Inner content area accounts for border (2) + padding (4).
	contentWidth := modalWidth - 6
	if contentWidth < 1 {
		contentWidth = 1
	}

	p.clickMap.Reset()
	var body string
	if p.view == profileViewDetails {
		body = p.renderDetails(contentWidth)
	} else {
		body = p.renderList(contentWidth)
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(shared.ColorPrimary).
		Padding(1, 2).
		Width(modalWidth).
		Render(body)
}

func (p *ProfilePanel) renderList(contentWidth int) string {
	var b strings.Builder
	b.WriteString(p.titleBar("Switch Profile", contentWidth))
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", contentWidth)))
	b.WriteString("\n")

	if len(p.profiles) == 0 {
		b.WriteString(shared.DimStyle.Render("No profiles configured."))
		b.WriteString("\n")
	} else {
		// titleBar=Y0, divider=Y1, first profile row=Y2.
		const firstRowY = 2
		for i, prof := range p.profiles {
			p.clickMap.AddRow(firstRowY+i, i)
			b.WriteString(p.renderListRow(i, prof, contentWidth))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	legend := shared.DimStyle.Render(
		shared.SuccessStyle.Render("●") + " active  " +
			shared.WarningStyle.Render("★") + " default",
	)
	b.WriteString(legend)
	b.WriteString("\n")

	// Inline "Copy <name> to: [input]" prompt — only shown while the user
	// is typing the new name. The input field is the same textinput.Model
	// reused for renaming fields in the details view.
	if p.editing == editCopy {
		if sel := p.selected(); sel != nil {
			b.WriteString("\n")
			b.WriteString(shared.DimStyle.Render("Copy "))
			b.WriteString(shared.NormalStyle.Render("\"" + sel.Name + "\""))
			b.WriteString(shared.DimStyle.Render(" to: "))
			b.WriteString(p.input.View())
			b.WriteString("\n")
		}
	}

	if p.statusMsg != "" {
		b.WriteString(p.statusMsg)
		b.WriteString("\n")
	}

	if p.editing == editCopy {
		b.WriteString(shared.DimStyle.Render(
			"[enter] confirm  [esc] cancel",
		))
	} else {
		b.WriteString(shared.DimStyle.Render(
			"[↑/↓] move  [enter] switch  [i/→] details  [y] copy  [*] default  [x] delete  [esc] cancel",
		))
	}
	return b.String()
}

func (p *ProfilePanel) renderListRow(idx int, prof *config.Profile, contentWidth int) string {
	cursor := "  "
	nameStyle := shared.NormalStyle
	if idx == p.cursor {
		cursor = shared.CursorStyle.Render("> ")
		nameStyle = shared.SelectedStyle
	}

	markers := []string{}
	if prof.Name == p.activeName {
		markers = append(markers, shared.SuccessStyle.Render("●"))
	}
	if prof.Name == p.defaultName {
		markers = append(markers, shared.WarningStyle.Render("★"))
	}
	markerStr := ""
	if len(markers) > 0 {
		markerStr = " " + strings.Join(markers, " ")
	}

	name := nameStyle.Render(prof.Name) + markerStr
	nameWidth := lipgloss.Width(cursor) + lipgloss.Width(name)
	specRoom := contentWidth - nameWidth - 2
	specPart := ""
	if prof.SpecPath != "" && specRoom > 4 {
		specPart = "  " + shared.DimStyle.Render(shared.TruncateWithEllipsis(prof.SpecPath, specRoom))
	}
	return cursor + name + specPart
}

func (p *ProfilePanel) renderDetails(contentWidth int) string {
	sel := p.selected()
	var b strings.Builder
	title := "Profile Details"
	if sel != nil {
		title = "Profile: " + sel.Name
	}
	b.WriteString(p.titleBar(title, contentWidth))
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", contentWidth)))
	b.WriteString("\n")

	if sel == nil {
		b.WriteString(shared.DimStyle.Render("No profile selected."))
		b.WriteString("\n")
	} else {
		// Status badges.
		var status []string
		if sel.Name == p.activeName {
			status = append(status, shared.SuccessStyle.Render("● active"))
		}
		if sel.Name == p.defaultName {
			status = append(status, shared.WarningStyle.Render("★ default"))
		}
		if config.IsVendored(sel) {
			status = append(status, shared.SuccessStyle.Render("vendored"))
		}
		if len(status) > 0 {
			b.WriteString(strings.Join(status, "  "))
			b.WriteString("\n")
		}

		p.writeEditableField(&b, "name", editName, sel.Name, contentWidth)
		p.writeEditableField(&b, "spec", editSpec, sel.SpecPath, contentWidth)
		p.writeEditableField(&b, "base url", editBaseURL, sel.BaseURL, contentWidth)

		// Active environment (read-only — switch via E from anywhere).
		b.WriteString(shared.DimStyle.Render("environment: "))
		if sel.ActiveEnvironment == "" {
			b.WriteString(shared.DimStyle.Render("(none)"))
		} else {
			b.WriteString(shared.NormalStyle.Render(sel.ActiveEnvironment))
		}
		b.WriteString("\n")

	}

	b.WriteString("\n")
	if p.statusMsg != "" {
		b.WriteString(p.statusMsg)
		b.WriteString("\n")
	}
	b.WriteString(shared.DimStyle.Render(
		"[↑/↓] move  [enter] edit  [v] vendor  [*] default  [x] delete  [esc/←] back",
	))
	return b.String()
}

func (p *ProfilePanel) writeEditableField(b *strings.Builder, label string, field editField, value string, contentWidth int) {
	// Register a click target on this row before writing it. The builder's
	// current newline count is the row we're about to write.
	p.clickMap.AddRow(strings.Count(b.String(), "\n"), int(field))

	isCursor := editableFields[p.detailsCursor] == field
	cursor := "  "
	labelStyle := shared.DimStyle
	if isCursor {
		cursor = shared.CursorStyle.Render("> ")
		labelStyle = shared.SelectedStyle
	}
	b.WriteString(cursor)
	b.WriteString(labelStyle.Render(label + ": "))
	if p.editing == field {
		b.WriteString(p.input.View())
	} else if value == "" {
		b.WriteString(shared.DimStyle.Render("(unset)"))
	} else {
		// Truncate so long paths/URLs stay on one row — wrapped rows
		// break click hit-testing.
		room := contentWidth - 2 /* cursor */ - lipgloss.Width(label) - 2 /* ": " */
		if room > 0 {
			value = shared.TruncateWithEllipsis(value, room)
		}
		b.WriteString(shared.NormalStyle.Render(value))
	}
	b.WriteString("\n")
}

func indexOfField(f editField) int {
	for i, ef := range editableFields {
		if ef == f {
			return i
		}
	}
	return 0
}

func currentFieldValue(prof *config.Profile, f editField) string {
	switch f {
	case editName:
		return prof.Name
	case editSpec:
		return prof.SpecPath
	case editBaseURL:
		return prof.BaseURL
	}
	return ""
}

func (p *ProfilePanel) titleBar(title string, contentWidth int) string {
	rendered := shared.TitleStyle.Render(title)
	// Clickable close hint at content row 0, right-aligned.
	closeItems := []shared.Hint{{Key: "esc", Label: "close"}}
	closeWidth := shared.HintsWidth(closeItems, true, "  ")
	closeStartCol := contentWidth - closeWidth
	if closeStartCol < 0 {
		closeStartCol = 0
	}
	closeHint := p.footerHints.Render(closeItems, 0, closeStartCol, true, "  ", shared.DimStyle)
	gap := max(0, contentWidth-lipgloss.Width(rendered)-closeWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		rendered,
		strings.Repeat(" ", gap),
		closeHint,
	)
}
