package shared

// ClickMap records clickable regions populated during a panel's render pass
// and resolved by the panel's mouse handler. It eliminates the brittle
// per-screen "headerHeight = N" offsets — instead the renderer tells the
// click handler exactly which screen rows correspond to which action.
//
// Typical usage:
//
//	cm := &shared.ClickMap{}
//	cm.AddRow(listStartRow + i, itemActionID(i))   // each list row
//	cm.AddRange(tabRowY, startCol, endCol, tabID)  // each tab
//
//	if id, ok := cm.Hit(msg.X, msg.Y); ok { ... }
//
// Action IDs are caller-defined ints (negative values are fine). Multiple
// regions on the same row are supported via AddRange; AddRow is the
// shorthand for "any X on this row".
type ClickMap struct {
	regions []clickRegion
}

type clickRegion struct {
	y          int
	xMin, xMax int // -1 in either bound means "any X"
	id         int
}

// Reset clears the map; call at the top of each View().
func (c *ClickMap) Reset() {
	c.regions = c.regions[:0]
}

// AddRow registers a click target spanning the entire row.
func (c *ClickMap) AddRow(y, id int) {
	c.regions = append(c.regions, clickRegion{y: y, xMin: -1, xMax: -1, id: id})
}

// AddRange registers a click target on row y between [xMin, xMax) — half-open
// so cumulative end-columns can be passed directly.
func (c *ClickMap) AddRange(y, xMin, xMax, id int) {
	c.regions = append(c.regions, clickRegion{y: y, xMin: xMin, xMax: xMax, id: id})
}

// Hit resolves (x, y) to a registered action id. Returns (id, true) on a
// match or (0, false) if no region covers the point. Later registrations
// take precedence over earlier ones on conflicts.
func (c *ClickMap) Hit(x, y int) (int, bool) {
	for i := len(c.regions) - 1; i >= 0; i-- {
		r := c.regions[i]
		if r.y != y {
			continue
		}
		if r.xMin == -1 || (x >= r.xMin && x < r.xMax) {
			return r.id, true
		}
	}
	return 0, false
}
