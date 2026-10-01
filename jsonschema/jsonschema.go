// Package jsonschema validates skill inputs against the JSON Schema subset our schemas use.
//
// Unknown keywords are an ERROR, not ignored: a validator that silently skips a keyword it does not
// understand certifies documents it never checked.
package jsonschema

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

var annotations = map[string]bool{"$schema": true, "$id": true, "title": true, "description": true, "examples": true, "default": true}

// Validate returns the violations of instance against schema; empty means valid. A schema that uses an
// unsupported keyword returns an error.
func Validate(schema, instance json.RawMessage) ([]string, error) {
	var s map[string]any
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil, fmt.Errorf("schema is not JSON: %w", err)
	}
	var v any
	if err := json.Unmarshal(instance, &v); err != nil {
		return []string{"input is not valid JSON: " + err.Error()}, nil
	}
	var errs []string
	if err := check(v, s, "$", &errs); err != nil {
		return nil, err
	}
	return errs, nil
}

func check(v any, s map[string]any, path string, errs *[]string) error {
	for k := range s {
		switch k {
		case "type", "enum", "const", "required", "properties", "additionalProperties", "items", "minItems",
			"minLength", "minimum", "pattern":
		default:
			if !annotations[k] {
				return fmt.Errorf("unsupported schema keyword %q at %s", k, path)
			}
		}
	}
	add := func(f string, a ...any) { *errs = append(*errs, fmt.Sprintf(f, a...)) }
	if t, ok := s["type"]; ok {
		var want []string
		switch x := t.(type) {
		case string:
			want = []string{x}
		case []any:
			for _, e := range x {
				want = append(want, fmt.Sprint(e))
			}
		}
		match := false
		for _, w := range want {
			if typeIs(v, w) {
				match = true
			}
		}
		if !match {
			add("%s: expected %v, got %T", path, want, v)
			return nil
		}
	}
	if e, ok := s["enum"].([]any); ok {
		found := false
		for _, x := range e {
			if x == v {
				found = true
			}
		}
		if !found {
			add("%s: %v is not one of %v", path, v, e)
		}
	}
	if c, ok := s["const"]; ok && c != v {
		add("%s: expected %v", path, c)
	}
	switch x := v.(type) {
	case string:
		if n, ok := s["minLength"].(float64); ok && float64(len([]rune(x))) < n {
			add("%s: shorter than %v", path, n)
		}
		if p, ok := s["pattern"].(string); ok {
			re, err := regexp.Compile(p)
			if err != nil {
				return fmt.Errorf("bad pattern at %s: %w", path, err)
			}
			if !re.MatchString(x) {
				add("%s: %q does not match %s", path, x, p)
			}
		}
	case float64:
		if n, ok := s["minimum"].(float64); ok && x < n {
			add("%s: below minimum %v", path, n)
		}
	case []any:
		if n, ok := s["minItems"].(float64); ok && float64(len(x)) < n {
			add("%s: fewer than %v items", path, n)
		}
		if it, ok := s["items"].(map[string]any); ok {
			for i, e := range x {
				if err := check(e, it, fmt.Sprintf("%s[%d]", path, i), errs); err != nil {
					return err
				}
			}
		}
	case map[string]any:
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if _, present := x[fmt.Sprint(r)]; !present {
					add("%s: missing required %q", path, fmt.Sprint(r))
				}
			}
		}
		props, _ := s["properties"].(map[string]any)
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if sub, ok := props[k].(map[string]any); ok {
				if err := check(x[k], sub, path+"."+k, errs); err != nil {
					return err
				}
			} else if s["additionalProperties"] == false {
				add("%s: unexpected property %q", path, k)
			}
		}
	}
	return nil
}

func typeIs(v any, t string) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == float64(int64(f))
	}
	return false
}
