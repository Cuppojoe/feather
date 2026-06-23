package http

import (
	"encoding/json"

	"github.com/cuppojoe/feather/internal/openapi"
)

// RequestBuilder helps build API requests from endpoint definitions
type RequestBuilder struct {
	endpoint *openapi.Endpoint
	values   map[string]string // Path and query parameter values
	extras   map[string]string // Ad-hoc query params not declared on the endpoint
	body     []byte
	headers  map[string]string
}

// NewRequestBuilder creates a new request builder for an endpoint
func NewRequestBuilder(endpoint *openapi.Endpoint) *RequestBuilder {
	return &RequestBuilder{
		endpoint: endpoint,
		values:   make(map[string]string),
		headers:  make(map[string]string),
	}
}

// SetValue sets a parameter value (path or query)
func (rb *RequestBuilder) SetValue(name, value string) *RequestBuilder {
	rb.values[name] = value
	return rb
}

// SetValues sets multiple parameter values
func (rb *RequestBuilder) SetValues(values map[string]string) *RequestBuilder {
	for k, v := range values {
		rb.values[k] = v
	}
	return rb
}

// SetExtraQueryParams supplies ad-hoc query parameters that aren't declared
// on the endpoint's OpenAPI definition. They are appended to the final URL
// alongside spec-defined params; if a key collides with a spec-defined
// query param, the spec value wins so user-typed overrides on the Params
// tab don't get silently shadowed by an ad-hoc row of the same name.
func (rb *RequestBuilder) SetExtraQueryParams(extras map[string]string) *RequestBuilder {
	rb.extras = extras
	return rb
}

// SetBody sets the request body as JSON
func (rb *RequestBuilder) SetBody(body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	rb.body = data
	return nil
}

// SetBodyBytes sets the raw request body
func (rb *RequestBuilder) SetBodyBytes(body []byte) *RequestBuilder {
	rb.body = body
	return rb
}

// SetHeader sets a custom header
func (rb *RequestBuilder) SetHeader(name, value string) *RequestBuilder {
	rb.headers[name] = value
	return rb
}

// Build creates a Request from the builder state
func (rb *RequestBuilder) Build() (*Request, []string) {
	// Substitute path variables
	path, missing := openapi.SubstitutePath(rb.endpoint.Path, rb.values)

	// Build query parameters
	queryParams := make(map[string]string)
	for _, param := range rb.endpoint.Parameters {
		if param.In == "query" {
			if val, ok := rb.values[param.Name]; ok && val != "" {
				queryParams[param.Name] = val
			}
		}
	}
	// Ad-hoc params are appended last so users can drop arbitrary keys via
	// the Params tab without rewiring the spec. Spec-defined params take
	// precedence on a name collision (already-filled key is left alone).
	for k, v := range rb.extras {
		if v == "" {
			continue
		}
		if _, ok := queryParams[k]; ok {
			continue
		}
		queryParams[k] = v
	}

	return &Request{
		Method:      rb.endpoint.Method,
		Path:        path,
		QueryParams: queryParams,
		Headers:     rb.headers,
		Body:        rb.body,
	}, missing
}

// GetPathParams returns all path parameters for the endpoint
func (rb *RequestBuilder) GetPathParams() []openapi.Parameter {
	var params []openapi.Parameter
	for _, p := range rb.endpoint.Parameters {
		if p.In == "path" {
			params = append(params, p)
		}
	}
	return params
}

// GetQueryParams returns all query parameters for the endpoint
func (rb *RequestBuilder) GetQueryParams() []openapi.Parameter {
	var params []openapi.Parameter
	for _, p := range rb.endpoint.Parameters {
		if p.In == "query" {
			params = append(params, p)
		}
	}
	return params
}

// RequiresBody returns true if the endpoint expects a request body
func (rb *RequestBuilder) RequiresBody() bool {
	return rb.endpoint.RequestBody != nil
}

// GetRequestBodySchema returns the schema for the request body
func (rb *RequestBuilder) GetRequestBodySchema() *openapi.Schema {
	if rb.endpoint.RequestBody == nil {
		return nil
	}

	// Prefer application/json
	if content, ok := rb.endpoint.RequestBody.Content["application/json"]; ok {
		return content.Schema
	}

	// Fall back to first available content type
	for _, content := range rb.endpoint.RequestBody.Content {
		return content.Schema
	}

	return nil
}

// Validate checks if all required parameters have values
func (rb *RequestBuilder) Validate() []string {
	var missing []string

	for _, param := range rb.endpoint.Parameters {
		if param.Required {
			if val, ok := rb.values[param.Name]; !ok || val == "" {
				missing = append(missing, param.Name)
			}
		}
	}

	return missing
}

// ResolvedPath returns the path with substituted values
func (rb *RequestBuilder) ResolvedPath() (string, []string) {
	return openapi.SubstitutePath(rb.endpoint.Path, rb.values)
}
