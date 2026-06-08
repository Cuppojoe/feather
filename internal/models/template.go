package models

import (
	"fmt"
	"regexp"
	"strings"
)

// refPattern matches a `${name}` placeholder. Names contain anything other
// than '}' and are trimmed of surrounding whitespace before lookup.
var refPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// Resolve returns a new map with each value's `${otherName}` references
// substituted by following the reference graph between entries of `raw`.
// Cycles are detected and returned as errors that name every entry on the
// offending path. References to names that aren't in `raw` are left as
// literal `${name}` so the user can see what didn't resolve.
//
// The input map is not mutated.
func Resolve(raw map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(raw))
	visiting := make(map[string]bool)
	visited := make(map[string]bool)

	var visit func(name string, stack []string) (string, error)
	visit = func(name string, stack []string) (string, error) {
		if visited[name] {
			return out[name], nil
		}
		if visiting[name] {
			// We've come back to a name that's already on the recursion
			// stack — find its position and print the back-edge so the
			// user can see which entries form the loop.
			for i, n := range stack {
				if n == name {
					path := append(append([]string{}, stack[i:]...), name)
					return "", fmt.Errorf(
						"cycle detected in context values: %s",
						strings.Join(path, " → "))
				}
			}
			return "", fmt.Errorf("cycle detected involving %q", name)
		}
		rawVal, ok := raw[name]
		if !ok {
			// Reference to an undefined name — leave the placeholder so
			// the caller can spot the typo.
			return "${" + name + "}", nil
		}
		visiting[name] = true
		defer delete(visiting, name)
		nextStack := append(stack, name)

		var firstErr error
		result := refPattern.ReplaceAllStringFunc(rawVal, func(m string) string {
			if firstErr != nil {
				return m
			}
			ref := strings.TrimSpace(m[2 : len(m)-1])
			v, err := visit(ref, nextStack)
			if err != nil {
				firstErr = err
				return m
			}
			return v
		})
		if firstErr != nil {
			return "", firstErr
		}
		out[name] = result
		visited[name] = true
		return result, nil
	}

	for name := range raw {
		if _, err := visit(name, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Substitute does a single-pass `${name}` replacement using values.
// Unknown references are left untouched so the user can see what didn't
// resolve. Pair this with Resolve on the source map first when you want
// nested references between values to expand before substitution.
func Substitute(s string, values map[string]string) string {
	return refPattern.ReplaceAllStringFunc(s, func(m string) string {
		ref := strings.TrimSpace(m[2 : len(m)-1])
		if v, ok := values[ref]; ok {
			return v
		}
		return m
	})
}
