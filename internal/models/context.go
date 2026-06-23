package models

import (
	"github.com/cuppojoe/feather/internal/openapi"
)

// Context holds the current state for path substitution and user-managed
// session variables. Scripts read and write the same Values map via
// feather.environment.{get,set,delete}, so anything the user stores (tokens,
// session IDs, anything else) lives here.
type Context struct {
	Values  map[string]string // User-defined values: org=epoch, gvc=demo
	BaseURL string            // API base URL override
}

// NewContext creates a new empty context
func NewContext() *Context {
	return &Context{
		Values: make(map[string]string),
	}
}

// Set sets a context value
func (c *Context) Set(key, value string) {
	c.Values[key] = value
}

// Get retrieves a context value
func (c *Context) Get(key string) string {
	return c.Values[key]
}

// Delete removes a context value
func (c *Context) Delete(key string) {
	delete(c.Values, key)
}

// SubstitutePath replaces {variable} placeholders in a path using context values
// Returns the substituted path and a list of missing variables
func (c *Context) SubstitutePath(path string) (string, []string) {
	return openapi.SubstitutePath(path, c.Values)
}

// Clone creates a copy of the context
func (c *Context) Clone() *Context {
	newCtx := &Context{
		Values:  make(map[string]string),
		BaseURL: c.BaseURL,
	}
	for k, v := range c.Values {
		newCtx.Values[k] = v
	}
	return newCtx
}
