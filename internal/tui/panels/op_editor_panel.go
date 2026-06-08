package panels

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/openapi"
	"github.com/cuppojoe/feather/internal/overlay"
	"github.com/cuppojoe/feather/internal/tui/shared"
)

// Row kinds in the op editor. Fixed fields first, then dynamic query-param and
// header rows interleaved with their "add" actions.
type editRowKind int

const (
	rowMethod editRowKind = iota
	rowPath
	rowCategory
	rowSummary
	rowDescription
	rowQueryParam
	rowAddQuery
	rowHeader
	rowAddHeader
)

// editRow describes one focusable line in the editor.
type editRow struct {
	kind editRowKind
	idx  int // index into queryParams/headers for rowQueryParam/rowHeader
}

// OpEditorPanel is a modal form for creating or editing an overlay request.
// For an imported request, method/path are the override target and are shown
// read-only, and query parameters (spec-owned) aren't structurally editable.
type OpEditorPanel struct {
	expanded bool
	keys     shared.KeyMap

	created    bool // editing/creating an overlay request vs an imported one
	origMethod string
	origPath   string

	method, path, category, summary, description string
	queryParams                                  []string // names
	headers                                      []string // "key: value"

	rows    []editRow
	cursor  int
	editing bool
	input   textinput.Model
	status  string
}

// NewOpEditorPanel constructs the editor (closed).
func NewOpEditorPanel(keys shared.KeyMap) *OpEditorPanel {
	ti := textinput.New()
	ti.CharLimit = 400
	return &OpEditorPanel{keys: keys, input: ti}
}

func (p *OpEditorPanel) IsExpanded() bool { return p.expanded }

// IsEditing reports that the editor owns keyboard input while open.
func (p *OpEditorPanel) IsEditing() bool { return p.expanded }

// OpenCreate opens the editor for a brand-new overlay request.
func (p *OpEditorPanel) OpenCreate(defaultTag string) {
	p.reset()
	p.created = true
	p.method = "GET"
	p.category = defaultTag
	if p.category == "" {
		p.category = "Custom"
	}
	p.rebuildRows()
	p.cursor = 0
}

// OpenEdit opens the editor for an existing request. created indicates whether
// the request is overlay-created (fully editable) or imported (override only).
func (p *OpEditorPanel) OpenEdit(ep *openapi.Endpoint, ovr *overlay.OpOverride, created bool) {
	p.reset()
	p.created = created
	p.origMethod = ep.Method
	p.origPath = ep.Path
	p.method = ep.Method
	p.path = ep.Path
	p.summary = ep.Summary
	p.description = ep.Description
	if len(ep.Tags) > 0 {
		p.category = ep.Tags[0]
	}
	// Query params come from the endpoint definition (path params are derived
	// from the path and shown read-only).
	for _, prm := range ep.Parameters {
		if prm.In == "query" {
			p.queryParams = append(p.queryParams, prm.Name)
		}
	}
	if ovr != nil {
		if ovr.Summary != "" {
			p.summary = ovr.Summary
		}
		if ovr.Description != "" {
			p.description = ovr.Description
		}
		if ovr.Tag != "" {
			p.category = ovr.Tag
		}
		for k, v := range ovr.Headers {
			p.headers = append(p.headers, k+": "+v)
		}
	}
	p.rebuildRows()
	p.cursor = 0
}

func (p *OpEditorPanel) reset() {
	p.expanded = true
	p.editing = false
	p.status = ""
	p.created = false
	p.origMethod, p.origPath = "", ""
	p.method, p.path, p.category, p.summary, p.description = "", "", "", "", ""
	p.queryParams = nil
	p.headers = nil
	p.input.Blur()
}

// rebuildRows recomputes the focusable row list from current state and kind.
func (p *OpEditorPanel) rebuildRows() {
	rows := []editRow{{kind: rowMethod}, {kind: rowPath}, {kind: rowCategory}, {kind: rowSummary}, {kind: rowDescription}}
	if p.created {
		for i := range p.queryParams {
			rows = append(rows, editRow{kind: rowQueryParam, idx: i})
		}
		rows = append(rows, editRow{kind: rowAddQuery})
	}
	for i := range p.headers {
		rows = append(rows, editRow{kind: rowHeader, idx: i})
	}
	rows = append(rows, editRow{kind: rowAddHeader})
	p.rows = rows
	if p.cursor >= len(rows) {
		p.cursor = len(rows) - 1
	}
}

// editable reports whether the current row can be edited inline.
func (p *OpEditorPanel) editable(r editRow) bool {
	switch r.kind {
	case rowMethod, rowPath:
		return p.created // imported: identity is fixed
	case rowAddQuery, rowAddHeader:
		return false
	default:
		return true
	}
}

// Update handles input while the editor is open.
func (p *OpEditorPanel) Update(msg tea.Msg) OpEditorResult {
	if !p.expanded {
		return OpEditorResult{}
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return OpEditorResult{}
	}

	if p.editing {
		switch key.String() {
		case "enter":
			p.commitInput()
			p.editing = false
			p.input.Blur()
		case "esc":
			p.editing = false
			p.input.Blur()
		default:
			var cmd tea.Cmd
			p.input, cmd = p.input.Update(msg)
			return OpEditorResult{Cmd: cmd}
		}
		return OpEditorResult{}
	}

	switch key.String() {
	case "esc":
		p.expanded = false
		return OpEditorResult{}
	case "ctrl+s":
		return p.submit()
	case "up", "shift+tab":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "tab":
		if p.cursor < len(p.rows)-1 {
			p.cursor++
		}
	case "d":
		p.deleteRow()
	case "enter":
		p.activateRow()
	}
	return OpEditorResult{}
}

// activateRow edits the focused row, or runs its "add" action.
func (p *OpEditorPanel) activateRow() {
	r := p.rows[p.cursor]
	switch r.kind {
	case rowAddQuery:
		p.queryParams = append(p.queryParams, "")
		p.rebuildRows()
		for i, row := range p.rows {
			if row.kind == rowQueryParam && row.idx == len(p.queryParams)-1 {
				p.cursor = i
				break
			}
		}
		p.startEdit("")
	case rowAddHeader:
		p.headers = append(p.headers, "")
		p.rebuildRows()
		for i, row := range p.rows {
			if row.kind == rowHeader && row.idx == len(p.headers)-1 {
				p.cursor = i
				break
			}
		}
		p.startEdit("")
	default:
		if p.editable(r) {
			p.startEdit(p.rowValue(r))
		}
	}
}

func (p *OpEditorPanel) startEdit(val string) {
	p.editing = true
	p.input.SetValue(val)
	p.input.CursorEnd()
	p.input.Focus()
}

// deleteRow removes the focused query-param or header row.
func (p *OpEditorPanel) deleteRow() {
	r := p.rows[p.cursor]
	switch r.kind {
	case rowQueryParam:
		p.queryParams = append(p.queryParams[:r.idx], p.queryParams[r.idx+1:]...)
		p.rebuildRows()
	case rowHeader:
		p.headers = append(p.headers[:r.idx], p.headers[r.idx+1:]...)
		p.rebuildRows()
	}
}

// rowValue returns the current string for an editable row.
func (p *OpEditorPanel) rowValue(r editRow) string {
	switch r.kind {
	case rowMethod:
		return p.method
	case rowPath:
		return p.path
	case rowCategory:
		return p.category
	case rowSummary:
		return p.summary
	case rowDescription:
		return p.description
	case rowQueryParam:
		return p.queryParams[r.idx]
	case rowHeader:
		return p.headers[r.idx]
	}
	return ""
}

// commitInput writes the edited input value back into the focused row.
func (p *OpEditorPanel) commitInput() {
	r := p.rows[p.cursor]
	v := strings.TrimSpace(p.input.Value())
	switch r.kind {
	case rowMethod:
		p.method = strings.ToUpper(v)
	case rowPath:
		p.path = v
	case rowCategory:
		p.category = v
	case rowSummary:
		p.summary = v
	case rowDescription:
		p.description = v
	case rowQueryParam:
		p.queryParams[r.idx] = v
	case rowHeader:
		p.headers[r.idx] = v
	}
}

// submit validates and returns the resulting overlay entry.
func (p *OpEditorPanel) submit() OpEditorResult {
	method := strings.ToUpper(strings.TrimSpace(p.method))
	path := strings.TrimSpace(p.path)
	category := strings.TrimSpace(p.category)
	if category == "" {
		category = "Custom"
	}
	if p.created {
		if method == "" {
			p.status = "Method is required"
			return OpEditorResult{}
		}
		if path == "" || !strings.HasPrefix(path, "/") {
			p.status = "Path must start with /"
			return OpEditorResult{}
		}
	}

	res := OpEditorResult{
		Save:       true,
		Created:    p.created,
		OrigMethod: p.origMethod,
		OrigPath:   p.origPath,
		Method:     method,
		Path:       path,
	}

	if p.created {
		op := overlay.AddedOp{
			Method:      method,
			Path:        path,
			Tag:         category,
			Summary:     strings.TrimSpace(p.summary),
			Description: strings.TrimSpace(p.description),
			Headers:     p.headerMap(),
		}
		for _, v := range openapi.ExtractPathVariables(path) {
			op.Parameters = append(op.Parameters, overlay.AddedParam{Name: v, In: "path", Required: true})
		}
		for _, q := range p.queryParams {
			if q = strings.TrimSpace(q); q != "" {
				op.Parameters = append(op.Parameters, overlay.AddedParam{Name: q, In: "query"})
			}
		}
		res.Added = op
	} else {
		res.Override = overlay.OpOverride{
			Summary:     strings.TrimSpace(p.summary),
			Description: strings.TrimSpace(p.description),
			Tag:         category,
			Headers:     p.headerMap(),
		}
	}
	p.expanded = false
	return res
}

// headerMap parses the "key: value" header rows into a map (skipping blanks).
func (p *OpEditorPanel) headerMap() map[string]string {
	var m map[string]string
	for _, h := range p.headers {
		k, v, found := strings.Cut(h, ":")
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if !found {
			v = ""
		}
		if m == nil {
			m = map[string]string{}
		}
		m[k] = strings.TrimSpace(v)
	}
	return m
}

// ViewModal renders the editor.
func (p *OpEditorPanel) ViewModal(screenWidth, screenHeight int) string {
	modalWidth := min(76, screenWidth-8)
	if modalWidth < 24 {
		modalWidth = 24
	}
	contentWidth := modalWidth - 6

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(shared.ColorPrimary).
		Width(contentWidth).Align(lipgloss.Center)
	dividerStyle := lipgloss.NewStyle().Foreground(shared.ColorBorder)
	labelStyle := lipgloss.NewStyle().Foreground(shared.ColorMuted).Width(12)

	titleText := "Edit Request"
	if p.created && p.origMethod == "" {
		titleText = "New Request"
	}
	title := titleStyle.Render(titleText)
	divider := dividerStyle.Render(strings.Repeat("─", contentWidth))

	var lines []string
	for i, r := range p.rows {
		focused := i == p.cursor
		cursor := "  "
		ls := labelStyle
		if focused {
			cursor = shared.CursorStyle.Render("> ")
			ls = ls.Foreground(shared.ColorPrimary).Bold(true)
		}

		switch r.kind {
		case rowAddQuery:
			lines = append(lines, cursor+shared.DimStyle.Render("+ add query param"))
			continue
		case rowAddHeader:
			lines = append(lines, cursor+shared.DimStyle.Render("+ add header"))
			continue
		}

		label, value := p.rowLabelValue(r)
		if focused && p.editing {
			p.input.Width = contentWidth - 14
			lines = append(lines, cursor+ls.Render(label)+" "+p.input.View())
			continue
		}
		valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB"))
		if !p.editable(r) {
			valStyle = shared.DimStyle
		}
		if value == "" {
			value = shared.DimStyle.Render("(empty)")
		} else {
			value = valStyle.Render(shared.TruncateWithEllipsis(value, contentWidth-15))
		}
		lines = append(lines, cursor+ls.Render(label)+" "+value)

		// Show derived path params under the Path row.
		if r.kind == rowPath {
			if pv := openapi.ExtractPathVariables(p.path); len(pv) > 0 {
				lines = append(lines, "    "+shared.DimStyle.Render("path params: "+strings.Join(pv, ", ")))
			}
		}
	}
	body := strings.Join(lines, "\n")

	status := ""
	if p.status != "" {
		status = lipgloss.NewStyle().Foreground(shared.ColorError).
			Width(contentWidth).Align(lipgloss.Center).Render(p.status)
	}

	hintItems := []shared.Hint{
		{Key: "enter", Label: "edit"},
		{Key: "d", Label: "del row"},
		{Key: "ctrl+s", Label: "save"},
		{Key: "esc", Label: "cancel"},
	}
	hint := lipgloss.NewStyle().Foreground(shared.ColorMuted).Width(contentWidth).Align(lipgloss.Center).
		Render(hintText(hintItems))

	content := lipgloss.JoinVertical(lipgloss.Left, title, divider, body, status, hint)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(shared.ColorPrimary).
		Padding(1, 2).
		Width(modalWidth).
		Render(content)
}

func (p *OpEditorPanel) rowLabelValue(r editRow) (string, string) {
	switch r.kind {
	case rowMethod:
		return "Method", p.method
	case rowPath:
		return "Path", p.path
	case rowCategory:
		return "Category", p.category
	case rowSummary:
		return "Summary", p.summary
	case rowDescription:
		return "Description", p.description
	case rowQueryParam:
		return "Query", p.queryParams[r.idx]
	case rowHeader:
		return "Header", p.headers[r.idx]
	}
	return "", ""
}

// hintText renders a simple "[key] label • ..." hint line (non-clickable; the
// editor captures all keys while open).
func hintText(items []shared.Hint) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("[%s] %s", it.Key, it.Label))
	}
	return strings.Join(parts, "  •  ")
}

// OpEditorResult communicates the editor outcome to the app.
type OpEditorResult struct {
	Save       bool
	Created    bool
	OrigMethod string
	OrigPath   string
	Method     string // request identity (for imported override key)
	Path       string
	Added      overlay.AddedOp    // when Created
	Override   overlay.OpOverride // when imported
	Cmd        tea.Cmd
}
