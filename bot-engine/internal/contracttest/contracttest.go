// Package contracttest validates JSON against the shared schemas in
// contracts/ (BL-16). It implements the JSON Schema subset those schemas use:
// type, enum, const, required, properties, additionalProperties: false, items,
// minimum, maximum, exclusiveMinimum, minLength, pattern, format: date-time,
// oneOf and $ref to a sibling schema file. agent-gateway's test_contracts.py
// checks the same schemas with the reference jsonschema library.
//
// Test-only: imported from _test files, never from production code.
package contracttest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var dateTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)

// Dir returns the repository's contracts directory.
func Dir(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, "contracts", "schemas")); err == nil && fi.IsDir() {
			return filepath.Join(dir, "contracts")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("contracts/ not found above the working directory")
		}
		dir = parent
	}
}

// Example returns the raw bytes of contracts/examples/<name>.
func Example(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(Dir(t), "examples", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Validate fails t unless doc conforms to contracts/schemas/<schema>.
func Validate(t testing.TB, schema string, doc []byte) {
	t.Helper()
	if errs := Check(t, schema, doc); len(errs) > 0 {
		t.Fatalf("%s violations:\n  %s\ndocument: %s", schema, strings.Join(errs, "\n  "), doc)
	}
}

// Check returns the violations of doc against schema (empty when valid).
func Check(t testing.TB, schema string, doc []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []string{"invalid JSON: " + err.Error()}
	}
	c := &checker{t: t, dir: filepath.Join(Dir(t), "schemas"), cache: map[string]map[string]any{}}
	return c.check(c.load(schema), v, "$")
}

type checker struct {
	t     testing.TB
	dir   string
	cache map[string]map[string]any
}

func (c *checker) load(name string) map[string]any {
	if s, ok := c.cache[name]; ok {
		return s
	}
	b, err := os.ReadFile(filepath.Join(c.dir, name))
	if err != nil {
		c.t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		c.t.Fatalf("%s: %v", name, err)
	}
	c.cache[name] = s
	return s
}

func (c *checker) check(s map[string]any, v any, path string) []string {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, path+": "+fmt.Sprintf(format, a...)) }

	if ref, ok := s["$ref"].(string); ok {
		return c.check(c.load(ref), v, path)
	}
	if t, ok := s["type"]; ok && !typeMatches(t, v) {
		add("type %s, want %v", jsonType(v), t)
		return errs
	}
	if enum, ok := s["enum"].([]any); ok && !contains(enum, v) {
		add("%v is not one of %v", v, enum)
	}
	if cst, ok := s["const"]; ok && !equal(cst, v) {
		add("%v, want const %v", v, cst)
	}
	switch x := v.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		for _, r := range asStrings(s["required"]) {
			if _, ok := x[r]; !ok {
				add("missing required %q", r)
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if ps, ok := props[k].(map[string]any); ok {
				errs = append(errs, c.check(ps, x[k], path+"."+k)...)
			} else if s["additionalProperties"] == false {
				add("unexpected property %q", k)
			}
		}
	case []any:
		if items, ok := s["items"].(map[string]any); ok {
			for i, item := range x {
				errs = append(errs, c.check(items, item, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	case json.Number:
		f, _ := x.Float64()
		if m, ok := num(s["minimum"]); ok && f < m {
			add("%v < minimum %v", f, m)
		}
		if m, ok := num(s["maximum"]); ok && f > m {
			add("%v > maximum %v", f, m)
		}
		if m, ok := num(s["exclusiveMinimum"]); ok && f <= m {
			add("%v <= exclusiveMinimum %v", f, m)
		}
	case string:
		if m, ok := num(s["minLength"]); ok && float64(len([]rune(x))) < m {
			add("shorter than %v", m)
		}
		if p, ok := s["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(x) {
			add("%q does not match %s", x, p)
		}
		if s["format"] == "date-time" && !dateTime.MatchString(x) {
			add("%q is not an RFC 3339 date-time", x)
		}
	}
	if branches, ok := s["oneOf"].([]any); ok {
		matched := 0
		for _, b := range branches {
			if len(c.check(b.(map[string]any), v, path)) == 0 {
				matched++
			}
		}
		if matched != 1 {
			add("matches %d oneOf branches, want exactly 1", matched)
		}
	}
	return errs
}

func typeMatches(t any, v any) bool {
	if list, ok := t.([]any); ok {
		for _, one := range list {
			if typeMatches(one, v) {
				return true
			}
		}
		return false
	}
	want, _ := t.(string)
	got := jsonType(v)
	return got == want || (want == "number" && got == "integer")
}

func jsonType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case json.Number:
		if _, err := x.Int64(); err == nil {
			return "integer"
		}
		return "number"
	}
	return fmt.Sprintf("%T", v)
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

func asStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []any, v any) bool {
	for _, x := range list {
		if equal(x, v) {
			return true
		}
	}
	return false
}

func equal(schemaValue, v any) bool {
	if n, ok := v.(json.Number); ok {
		f, _ := n.Float64()
		sv, isNum := schemaValue.(float64)
		return isNum && sv == f
	}
	return schemaValue == v
}
