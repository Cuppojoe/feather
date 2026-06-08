package openapi

import (
	"regexp"
	"strings"
)

var pathVarRegex = regexp.MustCompile(`\{([^}]+)\}`)

// ExtractPathVariables extracts all {variable} names from a path
func ExtractPathVariables(path string) []string {
	matches := pathVarRegex.FindAllStringSubmatch(path, -1)
	var vars []string
	for _, match := range matches {
		if len(match) > 1 {
			vars = append(vars, match[1])
		}
	}
	return vars
}

// SubstitutePath replaces {variable} placeholders with values from the context
// Returns the substituted path and a list of missing variables
func SubstitutePath(path string, values map[string]string) (string, []string) {
	var missing []string
	result := pathVarRegex.ReplaceAllStringFunc(path, func(match string) string {
		// Extract variable name without braces
		varName := strings.Trim(match, "{}")
		if val, ok := values[varName]; ok && val != "" {
			return val
		}
		missing = append(missing, varName)
		return match // Keep original placeholder if not found
	})
	return result, missing
}

// HasPathVariables checks if a path contains any {variable} placeholders
func HasPathVariables(path string) bool {
	return pathVarRegex.MatchString(path)
}

// GetPathVariables returns all unique path variables from a set of paths
func GetPathVariables(paths []string) []string {
	seen := make(map[string]bool)
	var result []string

	for _, path := range paths {
		for _, v := range ExtractPathVariables(path) {
			if !seen[v] {
				seen[v] = true
				result = append(result, v)
			}
		}
	}

	return result
}
