package openapi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// rawSpec represents the raw OpenAPI 3.0 JSON structure
type rawSpec struct {
	OpenAPI    string                 `json:"openapi"`
	Info       rawInfo                `json:"info"`
	Servers    []rawServer            `json:"servers"`
	Paths      map[string]rawPathItem `json:"paths"`
	Components rawComponents          `json:"components"`
	Tags       []rawTag               `json:"tags"`
}

type rawInfo struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

type rawServer struct {
	URL         string `json:"url"`
	Description string `json:"description"`
}

type rawTag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type rawPathItem struct {
	Get        *rawOperation `json:"get"`
	Post       *rawOperation `json:"post"`
	Put        *rawOperation `json:"put"`
	Patch      *rawOperation `json:"patch"`
	Delete     *rawOperation `json:"delete"`
	Options    *rawOperation `json:"options"`
	Head       *rawOperation `json:"head"`
	Parameters []rawParam    `json:"parameters"`
}

type rawOperation struct {
	Tags        []string               `json:"tags"`
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	OperationID string                 `json:"operationId"`
	Parameters  []rawParam             `json:"parameters"`
	RequestBody *rawRequestBody        `json:"requestBody"`
	Responses   map[string]rawResponse `json:"responses"`
	Deprecated  bool                   `json:"deprecated"`
}

type rawParam struct {
	Ref         string     `json:"$ref"`
	Name        string     `json:"name"`
	In          string     `json:"in"`
	Description string     `json:"description"`
	Required    bool       `json:"required"`
	Schema      *rawSchema `json:"schema"`
	Example     any        `json:"example"`
}

type rawRequestBody struct {
	Ref         string                `json:"$ref"`
	Description string                `json:"description"`
	Required    bool                  `json:"required"`
	Content     map[string]rawContent `json:"content"`
}

type rawContent struct {
	Schema  *rawSchema `json:"schema"`
	Example any        `json:"example"`
}

type rawResponse struct {
	Ref         string                `json:"$ref"`
	Description string                `json:"description"`
	Content     map[string]rawContent `json:"content"`
	Headers     map[string]rawHeader  `json:"headers"`
}

type rawHeader struct {
	Description string     `json:"description"`
	Schema      *rawSchema `json:"schema"`
}

type rawSchema struct {
	Ref                   string                `json:"$ref"`
	Type                  string                `json:"type"`
	Format                string                `json:"format"`
	Description           string                `json:"description"`
	Properties            map[string]*rawSchema `json:"properties"`
	Items                 *rawSchema            `json:"items"`
	Required              []string              `json:"required"`
	Enum                  []any                 `json:"enum"`
	Default               any                   `json:"default"`
	Example               any                   `json:"example"`
	AllOf                 []*rawSchema          `json:"allOf"`
	OneOf                 []*rawSchema          `json:"oneOf"`
	AnyOf                 []*rawSchema          `json:"anyOf"`
	AdditionalProperties  json.RawMessage       `json:"additionalProperties"`
	AdditionalPropsSchema *rawSchema            // Parsed from AdditionalProperties if it's a schema
	AdditionalPropsBool   *bool                 // Parsed from AdditionalProperties if it's a bool
	Nullable              bool                  `json:"nullable"`
	ReadOnly              bool                  `json:"readOnly"`
	WriteOnly             bool                  `json:"writeOnly"`
	MinLength             *int                  `json:"minLength"`
	MaxLength             *int                  `json:"maxLength"`
	Minimum               *float64              `json:"minimum"`
	Maximum               *float64              `json:"maximum"`
	Pattern               string                `json:"pattern"`
}

type rawComponents struct {
	Schemas       map[string]*rawSchema     `json:"schemas"`
	Parameters    map[string]rawParam       `json:"parameters"`
	RequestBodies map[string]rawRequestBody `json:"requestBodies"`
	Responses     map[string]rawResponse    `json:"responses"`
	// securitySchemes deliberately omitted — feather doesn't model auth.
}

// Parser handles OpenAPI spec parsing
type Parser struct {
	raw      *rawSpec
	schemas  map[string]*Schema
	resolved map[string]bool // Track resolved refs to avoid cycles
}

// ParseFile parses an OpenAPI spec from a file on disk. The format is
// chosen by extension: .yaml/.yml are parsed as YAML, everything else
// (including unknown/missing extensions) is parsed as JSON.
func ParseFile(path string) (*ParsedSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading spec file: %w", err)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return ParseYAML(data)
	}
	return Parse(data)
}

// Parse parses an OpenAPI spec from JSON bytes
func Parse(data []byte) (*ParsedSpec, error) {
	var raw rawSpec
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	p := &Parser{
		raw:      &raw,
		schemas:  make(map[string]*Schema),
		resolved: make(map[string]bool),
	}

	return p.parse()
}

// ParseYAML parses an OpenAPI spec given as YAML by converting it to JSON
// and feeding the result to Parse. Going through JSON lets all of the raw
// types stay tagged with `json:"..."` only — no parallel YAML tags to
// drift out of sync.
func ParseYAML(data []byte) (*ParsedSpec, error) {
	var root any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}
	jsonBytes, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("converting YAML to JSON: %w", err)
	}
	return Parse(jsonBytes)
}

func (p *Parser) parse() (*ParsedSpec, error) {
	spec := &ParsedSpec{
		Info: SpecInfo{
			Title:       p.raw.Info.Title,
			Description: p.raw.Info.Description,
			Version:     p.raw.Info.Version,
		},
		Schemas: make(map[string]*Schema),
	}

	// Extract base URL from servers
	if len(p.raw.Servers) > 0 {
		spec.BaseURL = p.raw.Servers[0].URL
	}

	// Parse schemas first (needed for ref resolution)
	for name, rawSchema := range p.raw.Components.Schemas {
		spec.Schemas[name] = p.convertSchema(rawSchema)
	}
	p.schemas = spec.Schemas

	// Build tag descriptions map
	tagDescriptions := make(map[string]string)
	for _, tag := range p.raw.Tags {
		tagDescriptions[tag.Name] = tag.Description
	}

	// Parse paths and group by tags
	tagEndpoints := make(map[string][]Endpoint)
	pathVars := make(map[string]bool)

	for path, pathItem := range p.raw.Paths {
		// Extract path variables
		for _, v := range ExtractPathVariables(path) {
			pathVars[v] = true
		}

		// Parse each method
		methods := map[string]*rawOperation{
			"GET":     pathItem.Get,
			"POST":    pathItem.Post,
			"PUT":     pathItem.Put,
			"PATCH":   pathItem.Patch,
			"DELETE":  pathItem.Delete,
			"OPTIONS": pathItem.Options,
			"HEAD":    pathItem.Head,
		}

		for method, op := range methods {
			if op == nil {
				continue
			}

			endpoint := p.convertOperation(path, method, op, pathItem.Parameters)

			// Group by first tag, or "default" if no tags
			tag := "default"
			if len(endpoint.Tags) > 0 {
				tag = endpoint.Tags[0]
			}
			tagEndpoints[tag] = append(tagEndpoints[tag], endpoint)
		}
	}

	// Build sorted tag groups
	var tagNames []string
	for tag := range tagEndpoints {
		tagNames = append(tagNames, tag)
	}
	sort.Strings(tagNames)

	for _, tagName := range tagNames {
		endpoints := tagEndpoints[tagName]
		// Sort endpoints by path then method
		SortEndpoints(endpoints)

		spec.Tags = append(spec.Tags, TagGroup{
			Name:        tagName,
			Description: tagDescriptions[tagName],
			Endpoints:   endpoints,
		})
	}

	// Build sorted path variables list (org first, gvc second, then alphabetical)
	spec.PathVariables = sortPathVariables(pathVars)

	return spec, nil
}

func (p *Parser) convertOperation(path, method string, op *rawOperation, pathParams []rawParam) Endpoint {
	endpoint := Endpoint{
		Path:        path,
		Method:      method,
		OperationID: op.OperationID,
		Summary:     op.Summary,
		Description: op.Description,
		Tags:        op.Tags,
		Deprecated:  op.Deprecated,
		Responses:   make(map[string]*Response),
	}

	// Merge path-level and operation-level parameters
	allParams := append(pathParams, op.Parameters...)
	for _, param := range allParams {
		endpoint.Parameters = append(endpoint.Parameters, p.convertParameter(param))
	}

	// Convert request body
	if op.RequestBody != nil {
		endpoint.RequestBody = p.convertRequestBody(op.RequestBody)
	}

	// Convert responses
	for code, resp := range op.Responses {
		endpoint.Responses[code] = p.convertResponse(&resp)
	}

	return endpoint
}

func (p *Parser) convertParameter(param rawParam) Parameter {
	// Handle $ref
	if param.Ref != "" {
		refName := extractRefName(param.Ref)
		if refParam, ok := p.raw.Components.Parameters[refName]; ok {
			return p.convertParameter(refParam)
		}
	}

	return Parameter{
		Name:        param.Name,
		In:          param.In,
		Description: param.Description,
		Required:    param.Required,
		Schema:      p.convertSchema(param.Schema),
		Example:     param.Example,
	}
}

func (p *Parser) convertRequestBody(rb *rawRequestBody) *RequestBody {
	if rb == nil {
		return nil
	}

	// Handle $ref
	if rb.Ref != "" {
		refName := extractRefName(rb.Ref)
		if refRB, ok := p.raw.Components.RequestBodies[refName]; ok {
			return p.convertRequestBody(&refRB)
		}
	}

	body := &RequestBody{
		Description: rb.Description,
		Required:    rb.Required,
		Content:     make(map[string]*MediaType),
	}

	for mediaType, content := range rb.Content {
		body.Content[mediaType] = &MediaType{
			Schema:  p.convertSchema(content.Schema),
			Example: content.Example,
		}
	}

	return body
}

func (p *Parser) convertResponse(resp *rawResponse) *Response {
	if resp == nil {
		return nil
	}

	// Handle $ref
	if resp.Ref != "" {
		refName := extractRefName(resp.Ref)
		if refResp, ok := p.raw.Components.Responses[refName]; ok {
			return p.convertResponse(&refResp)
		}
	}

	response := &Response{
		Description: resp.Description,
		Content:     make(map[string]*MediaType),
		Headers:     make(map[string]*Header),
	}

	for mediaType, content := range resp.Content {
		response.Content[mediaType] = &MediaType{
			Schema:  p.convertSchema(content.Schema),
			Example: content.Example,
		}
	}

	for name, header := range resp.Headers {
		response.Headers[name] = &Header{
			Description: header.Description,
			Schema:      p.convertSchema(header.Schema),
		}
	}

	return response
}

func (p *Parser) convertSchema(s *rawSchema) *Schema {
	if s == nil {
		return nil
	}

	schema := &Schema{
		Ref:         s.Ref,
		Type:        s.Type,
		Format:      s.Format,
		Description: s.Description,
		Required:    s.Required,
		Enum:        s.Enum,
		Default:     s.Default,
		Example:     s.Example,
		Nullable:    s.Nullable,
		ReadOnly:    s.ReadOnly,
		WriteOnly:   s.WriteOnly,
		MinLength:   s.MinLength,
		MaxLength:   s.MaxLength,
		Minimum:     s.Minimum,
		Maximum:     s.Maximum,
		Pattern:     s.Pattern,
	}

	if s.Properties != nil {
		schema.Properties = make(map[string]*Schema)
		for name, prop := range s.Properties {
			schema.Properties[name] = p.convertSchema(prop)
		}
	}

	if s.Items != nil {
		schema.Items = p.convertSchema(s.Items)
	}

	// Handle additionalProperties which can be bool or schema
	if len(s.AdditionalProperties) > 0 {
		// Try to parse as a schema first
		var schemaVal rawSchema
		if err := json.Unmarshal(s.AdditionalProperties, &schemaVal); err == nil {
			// Check if it's actually a schema (has some fields) vs empty object
			if schemaVal.Type != "" || schemaVal.Ref != "" || schemaVal.Properties != nil ||
				schemaVal.Items != nil || schemaVal.AllOf != nil || schemaVal.OneOf != nil ||
				schemaVal.AnyOf != nil {
				schema.AdditionalProperties = p.convertSchema(&schemaVal)
			}
		}
		// If it's a boolean, we just leave AdditionalProperties as nil
		// (true means any additional props allowed, false means none - we don't model this)
	}

	for _, allOf := range s.AllOf {
		schema.AllOf = append(schema.AllOf, p.convertSchema(allOf))
	}

	for _, oneOf := range s.OneOf {
		schema.OneOf = append(schema.OneOf, p.convertSchema(oneOf))
	}

	for _, anyOf := range s.AnyOf {
		schema.AnyOf = append(schema.AnyOf, p.convertSchema(anyOf))
	}

	return schema
}

// extractRefName extracts the component name from a $ref string
// e.g., "#/components/schemas/Agent" -> "Agent"
func extractRefName(ref string) string {
	parts := strings.Split(ref, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

// SortEndpoints orders endpoints by path, then by HTTP method. Shared by the
// parser and the overlay apply step so synthesized operations sort the same way.
func SortEndpoints(endpoints []Endpoint) {
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Path != endpoints[j].Path {
			return endpoints[i].Path < endpoints[j].Path
		}
		return methodOrder(endpoints[i].Method) < methodOrder(endpoints[j].Method)
	})
}

// methodOrder returns an order value for HTTP methods
func methodOrder(method string) int {
	order := map[string]int{
		"GET":     1,
		"POST":    2,
		"PUT":     3,
		"PATCH":   4,
		"DELETE":  5,
		"OPTIONS": 6,
		"HEAD":    7,
	}
	if o, ok := order[method]; ok {
		return o
	}
	return 99
}

// sortPathVariables sorts path variables with org first, gvc second, then alphabetical
func sortPathVariables(vars map[string]bool) []string {
	var result []string
	for v := range vars {
		result = append(result, v)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i] < result[j]
	})

	return result
}
