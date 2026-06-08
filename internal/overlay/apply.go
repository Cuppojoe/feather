package overlay

import (
	"sort"
	"strings"

	"github.com/cuppojoe/feather/internal/openapi"
)

// defaultTag is the tag group that holds added operations without an explicit
// tag of their own.
const defaultTag = "Custom"

// Apply returns a fresh spec produced by overlaying ov onto base. It does NOT
// mutate base, and is a pure function of (base, ov) — so re-running it after
// any overlay edit yields the correct, deduplicated result. Base operations
// receive summary/description overrides and may be re-tagged; added operations
// are synthesized; declared categories appear even when empty.
func Apply(base *openapi.ParsedSpec, ov *Overlay) *openapi.ParsedSpec {
	if base == nil {
		base = &openapi.ParsedSpec{}
	}
	out := &openapi.ParsedSpec{
		Info:    base.Info,
		BaseURL: base.BaseURL,
		Schemas: base.Schemas,
	}
	if ov == nil {
		ov = New()
	}

	groups := map[string][]openapi.Endpoint{}
	seen := map[string]bool{}
	ensure := func(tag string) { seen[tag] = true }

	// Track every category that should appear, including declared overlay
	// categories (which persist even when empty).
	for _, tg := range base.Tags {
		ensure(tg.Name)
	}
	for _, c := range ov.Categories {
		ensure(c)
	}

	pathVars := map[string]bool{}
	for _, v := range base.PathVariables {
		pathVars[v] = true
	}

	// Base endpoints: copy, apply field overrides, route to (possibly
	// overridden) tag.
	for _, tg := range base.Tags {
		for _, ep := range tg.Endpoints {
			e := ep // copy; pointer fields are read-only here
			tag := tg.Name
			if o := ov.Get(e.Method, e.Path); o != nil {
				if o.Summary != "" {
					e.Summary = o.Summary
				}
				if o.Description != "" {
					e.Description = o.Description
				}
				if o.Tag != "" {
					tag = o.Tag
				}
			}
			ensure(tag)
			groups[tag] = append(groups[tag], e)
		}
	}

	// Added operations.
	for _, a := range ov.Added {
		e := endpointFromAdded(a)
		tag := a.Tag
		if tag == "" {
			tag = defaultTag
		}
		ensure(tag)
		groups[tag] = append(groups[tag], e)
		for _, v := range openapi.ExtractPathVariables(a.Path) {
			pathVars[v] = true
		}
	}

	// Collect every candidate tag name (groups with endpoints + categories
	// ensured above) and emit them alphabetically, matching the parser. Keep a
	// group when it has endpoints or it's a declared category (so empty folders
	// persist, and spec tags emptied by re-tagging disappear).
	for name := range groups {
		seen[name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		eps := groups[name]
		if len(eps) == 0 && !ov.HasCategory(name) {
			continue
		}
		openapi.SortEndpoints(eps)
		out.Tags = append(out.Tags, openapi.TagGroup{Name: name, Endpoints: eps})
	}

	out.PathVariables = make([]string, 0, len(pathVars))
	for v := range pathVars {
		out.PathVariables = append(out.PathVariables, v)
	}
	sort.Strings(out.PathVariables)

	return out
}

// endpointFromAdded builds an openapi.Endpoint from an overlay AddedOp.
func endpointFromAdded(a AddedOp) openapi.Endpoint {
	ep := openapi.Endpoint{
		Method:      strings.ToUpper(a.Method),
		Path:        a.Path,
		Summary:     a.Summary,
		Description: a.Description,
	}
	tag := a.Tag
	if tag == "" {
		tag = defaultTag
	}
	ep.Tags = []string{tag}
	for _, p := range a.Parameters {
		ep.Parameters = append(ep.Parameters, openapi.Parameter{
			Name:     p.Name,
			In:       p.In,
			Required: p.Required,
			Schema:   &openapi.Schema{Type: "string"},
		})
	}
	// Attach a minimal JSON body so the Body tab is available for methods that
	// usually carry one, or whenever the user saved a body example.
	if a.BodyExample != "" || hasBody(ep.Method) {
		ep.RequestBody = &openapi.RequestBody{
			Content: map[string]*openapi.MediaType{
				"application/json": {Schema: &openapi.Schema{Type: "object"}},
			},
		}
	}
	return ep
}

func hasBody(method string) bool {
	switch strings.ToUpper(method) {
	case "POST", "PUT", "PATCH":
		return true
	default:
		return false
	}
}
