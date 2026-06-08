package openapi

// ParsedSpec represents a parsed OpenAPI specification
type ParsedSpec struct {
	Info          SpecInfo
	BaseURL       string
	Tags          []TagGroup
	PathVariables []string // Unique path variables like "org", "gvc", "name"
	Schemas       map[string]*Schema
}

// SpecInfo contains basic API information
type SpecInfo struct {
	Title       string
	Description string
	Version     string
}

// TagGroup groups endpoints by their tag
type TagGroup struct {
	Name        string
	Description string
	Endpoints   []Endpoint
}

// Endpoint represents a single API endpoint
type Endpoint struct {
	Path        string
	Method      string
	OperationID string
	Summary     string
	Description string
	Tags        []string
	Parameters  []Parameter
	RequestBody *RequestBody
	Responses   map[string]*Response
	Deprecated  bool
}

// Parameter represents a path, query, header, or cookie parameter
type Parameter struct {
	Name        string
	In          string // "path", "query", "header", "cookie"
	Description string
	Required    bool
	Schema      *Schema
	Example     any
}

// RequestBody represents the request body specification
type RequestBody struct {
	Description string
	Required    bool
	Content     map[string]*MediaType
}

// MediaType represents a media type in request/response
type MediaType struct {
	Schema  *Schema
	Example any
}

// Response represents an API response
type Response struct {
	Description string
	Content     map[string]*MediaType
	Headers     map[string]*Header
}

// Header represents a response header
type Header struct {
	Description string
	Schema      *Schema
}

// Schema represents a JSON Schema
type Schema struct {
	Type                 string
	Format               string
	Description          string
	Properties           map[string]*Schema
	Items                *Schema
	Required             []string
	Enum                 []any
	Default              any
	Example              any
	Ref                  string // $ref pointer
	AllOf                []*Schema
	OneOf                []*Schema
	AnyOf                []*Schema
	AdditionalProperties *Schema
	Nullable             bool
	ReadOnly             bool
	WriteOnly            bool
	MinLength            *int
	MaxLength            *int
	Minimum              *float64
	Maximum              *float64
	Pattern              string
}
