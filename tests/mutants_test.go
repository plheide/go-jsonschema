package tests_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	// maxDocuments caps the documents per schema. Seeds come first, then mutants breadth first, so the nodes nearest
	// the root are always tried.
	maxDocuments = 300
	// maxSeedDepth bounds how deep a seed goes, which also stops recursive schemas.
	maxSeedDepth = 6
	// maxSeedSchemas bounds the subschemas a seed is built from. A big schema's seed would otherwise grow with each
	// property and $ref it reaches, and each mutant is a copy of the seed.
	maxSeedSchemas = 500
	// maxSeedItems bounds the items of an array in a seed.
	maxSeedItems = 3
	// maxExampleLen bounds how much of a value a report's example shows.
	maxExampleLen = 40
)

// formatSamples holds a valid value for each format the seeds know.
var formatSamples = map[string]string{
	"date-time":             "2020-01-01T00:00:00Z",
	"date":                  "2020-01-01",
	"time":                  "12:00:00Z",
	"duration":              "P1D",
	"email":                 "a@example.com",
	"idn-email":             "a@example.com",
	"hostname":              "example.com",
	"idn-hostname":          "example.com",
	"ipv4":                  "192.0.2.1",
	"ipv6":                  "2001:db8::1",
	"uri":                   "https://example.com/a",
	"iri":                   "https://example.com/a",
	"uri-reference":         "/a",
	"iri-reference":         "/a",
	"uri-template":          "https://example.com/{a}",
	"json-pointer":          "/a",
	"relative-json-pointer": "0",
	"regex":                 "^a$",
	"uuid":                  "123e4567-e89b-12d3-a456-426614174000",
}

// mutant is one document the differential test tries, with the kind of change that made it and where.
type mutant struct {
	kind    string
	example string
	doc     json.RawMessage
}

// schemaFile is a parsed schema document, with its numbers kept as written.
type schemaFile struct {
	path string
	root any
}

// schemaFiles loads each schema file once.
type schemaFiles struct {
	byPath map[string]*schemaFile
}

func newSchemaFiles() *schemaFiles {
	return &schemaFiles{byPath: map[string]*schemaFile{}}
}

func (r *schemaFiles) load(path string) (*schemaFile, error) {
	if f, ok := r.byPath[path]; ok {
		return f, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	f := &schemaFile{path: path}
	if err := dec.Decode(&f.root); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	r.byPath[path] = f

	return f, nil
}

// resolve follows a $ref made in f: to a fragment of f, or to a file beside it with an optional fragment. A URL or
// an anchor resolves to nothing; the seed then leaves that part out.
func (r *schemaFiles) resolve(f *schemaFile, ref string) (*schemaFile, any, bool) {
	file, fragment, _ := strings.Cut(ref, "#")

	target := f

	if file != "" {
		if strings.Contains(file, "://") {
			return nil, nil, false
		}

		var err error
		if target, err = r.load(filepath.Join(filepath.Dir(f.path), file)); err != nil {
			return nil, nil, false
		}
	}

	v, ok := pointerValue(target.root, fragment)

	return target, v, ok
}

func pointerValue(root any, fragment string) (any, bool) {
	if fragment == "" {
		return root, true
	}

	if !strings.HasPrefix(fragment, "/") {
		return nil, false
	}

	v := root

	for seg := range strings.SplitSeq(fragment[1:], "/") {
		if unescaped, err := url.PathUnescape(seg); err == nil {
			seg = unescaped
		}

		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")

		switch c := v.(type) {
		case map[string]any:
			next, ok := c[seg]
			if !ok {
				return nil, false
			}

			v = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(c) {
				return nil, false
			}

			v = c[i]
		default:
			return nil, false
		}
	}

	return v, true
}

// documentsFor returns a schema's seeds, which are its examples, its default and two documents built from it (one
// with every property, one with the required ones only), followed by their mutants, without duplicates.
func documentsFor(files *schemaFiles, f *schemaFile, schema any) []mutant {
	var seeds []any

	if s, ok := schema.(map[string]any); ok {
		examples, _ := s["examples"].([]any)
		seeds = append(seeds, examples[:min(len(examples), maxSeedItems)]...)

		if d, ok := s["default"]; ok {
			seeds = append(seeds, d)
		}
	}

	for _, requiredOnly := range []bool{false, true} {
		g := seeder{files: files}
		seeds = append(seeds, g.synth(f, schema, 0, requiredOnly))
	}

	var docs []mutant

	seen := map[string]bool{}

	// add keeps a document it hasn't seen, and reports whether there is room for more.
	add := func(kind, example string, v any) bool {
		if len(docs) >= maxDocuments {
			return false
		}

		if data, err := json.Marshal(v); err == nil && !seen[string(data)] {
			seen[string(data)] = true
			docs = append(docs, mutant{kind: kind, example: example, doc: data})
		}

		return len(docs) < maxDocuments
	}

	for i, s := range seeds {
		add("seed", fmt.Sprintf("seed %d", i), s)
	}

	for _, s := range seeds {
		if len(docs) >= maxDocuments {
			break
		}

		mutateBreadthFirst(s, add)
	}

	return docs
}

type node struct {
	path  []string
	value any
}

// mutateBreadthFirst hands each mutant of a seed to add, the root's first, then each level's in turn, until add has
// no room for more.
func mutateBreadthFirst(seed any, add func(kind, example string, v any) bool) {
	queue := []node{{value: seed}}

	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]

		at := "/" + strings.Join(n.path, "/")

		for _, m := range mutations(n.value) {
			var example string

			if m.key != "" {
				m.value = withoutKey(n.value, m.key)
				example = strings.TrimSuffix(at, "/") + "/" + m.key + " deleted"
			} else {
				example = at + " = " + shortJSON(m.value)
			}

			if !add(m.kind, example, replaceAt(seed, n.path, m.value)) {
				return
			}
		}

		switch c := n.value.(type) {
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(c)) {
				queue = append(queue, node{path: append(slices.Clone(n.path), k), value: c[k]})
			}
		case []any:
			for i, v := range c {
				queue = append(queue, node{path: append(slices.Clone(n.path), strconv.Itoa(i)), value: v})
			}
		}
	}
}

// mutation replaces a node with value. key names the property a "delete key" mutation removes. Its value is built
// only when it is tried: building every deletion first would copy the object once per key.
type mutation struct {
	kind  string
	value any
	key   string
}

// mutations lists the changes tried at one node: a null, a value of the wrong type, and changes by the node's kind.
func mutations(v any) []mutation {
	ms := []mutation{{kind: "null"}, {kind: "wrong type", value: wrongType(v)}}

	switch c := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(c)) {
			ms = append(ms, mutation{kind: "delete key", key: k})
		}

		m := maps.Clone(c)
		m["zzUnknown"] = "x"
		ms = append(ms, mutation{kind: "add unknown key", value: m})
	case []any:
		ms = append(ms, mutation{kind: "empty array", value: []any{}})

		if len(c) > 0 {
			ms = append(ms, mutation{kind: "duplicate first item", value: append(slices.Clone(c), c[0])})
		}

		ms = append(ms, mutation{kind: "append null", value: append(slices.Clone(c), nil)})
	case string:
		ms = append(ms,
			mutation{kind: "empty string", value: ""},
			mutation{kind: "5000 characters", value: strings.Repeat("a", 5000)},
			mutation{kind: "off-enum string", value: "zzOffEnum"},
		)
	case json.Number:
		ms = append(ms,
			mutation{kind: "negative", value: json.Number("-1")},
			mutation{kind: "0.5", value: json.Number("0.5")},
			mutation{kind: "0", value: json.Number("0")},
			mutation{kind: "1e300", value: json.Number("1e300")},
		)

		if !strings.ContainsAny(string(c), ".eE") {
			ms = append(ms,
				mutation{kind: "N.0", value: c + ".0"},
				mutation{kind: "Ne0", value: c + "e0"},
				mutation{kind: "0e99", value: json.Number("0e99")},
				mutation{kind: "2^53+1", value: json.Number("9007199254740993")},
				mutation{kind: "2^63", value: json.Number("9223372036854775808")},
				mutation{kind: "2^64", value: json.Number("18446744073709551616")},
			)
		}
	}

	return ms
}

func wrongType(v any) any {
	switch v.(type) {
	case string:
		return json.Number("12345")
	case json.Number:
		return "12345"
	case bool:
		return "true"
	default:
		return "x"
	}
}

// withoutKey returns a copy of the object v without its property key.
func withoutKey(v any, key string) any {
	m := maps.Clone(v.(map[string]any))
	delete(m, key)

	return m
}

// replaceAt returns root with the node at path replaced by v, copying only the containers on the way.
func replaceAt(root any, path []string, v any) any {
	if len(path) == 0 {
		return v
	}

	switch c := root.(type) {
	case map[string]any:
		m := maps.Clone(c)
		m[path[0]] = replaceAt(c[path[0]], path[1:], v)

		return m
	case []any:
		i, err := strconv.Atoi(path[0])
		if err != nil || i >= len(c) {
			return root
		}

		a := slices.Clone(c)
		a[i] = replaceAt(c[i], path[1:], v)

		return a
	default:
		return root
	}
}

func shortJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "?"
	}

	if len(data) > maxExampleLen {
		return string(data[:maxExampleLen]) + "…"
	}

	return string(data)
}

// seeder builds a document from a schema: a value for every keyword it knows, so that most checks see a valid
// value before a mutant changes it. It needn't be valid: the reference validator judges every document.
type seeder struct {
	files *schemaFiles
	// schemas counts the subschemas the seed is built from.
	schemas int
}

func (g *seeder) synth(f *schemaFile, schema any, depth int, requiredOnly bool) any {
	if depth > maxSeedDepth || g.schemas >= maxSeedSchemas {
		return nil
	}

	g.schemas++

	s, ok := schema.(map[string]any)
	if !ok {
		if b, ok := schema.(bool); ok && b {
			return map[string]any{}
		}

		return nil
	}

	if ref, ok := s["$ref"].(string); ok {
		if target, sub, ok := g.files.resolve(f, ref); ok {
			return g.synth(target, sub, depth+1, requiredOnly)
		}

		return nil
	}

	for _, key := range []string{"const", "default"} {
		if v, ok := s[key]; ok {
			return v
		}
	}

	for _, key := range []string{"enum", "examples"} {
		if list, ok := s[key].([]any); ok && len(list) > 0 {
			return list[0]
		}
	}

	v := g.synthOwn(f, s, depth, requiredOnly)

	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		branches, _ := s[key].([]any)

		for i, b := range branches {
			if key != "allOf" && i > 0 {
				break
			}

			v = mergeSeeds(v, g.synth(f, b, depth+1, requiredOnly))
		}
	}

	return v
}

func (g *seeder) synthOwn(f *schemaFile, s map[string]any, depth int, requiredOnly bool) any {
	switch schemaType(s) {
	case "object":
		return g.synthObject(f, s, depth, requiredOnly)
	case "array":
		return g.synthArray(f, s, depth, requiredOnly)
	case "string":
		return synthString(s)
	case "integer":
		return synthNumber(s, true)
	case "number":
		return synthNumber(s, false)
	case "boolean":
		return true
	default:
		return nil
	}
}

func (g *seeder) synthObject(f *schemaFile, s map[string]any, depth int, requiredOnly bool) any {
	props, _ := s["properties"].(map[string]any)
	required := map[string]bool{}

	if list, ok := s["required"].([]any); ok {
		for _, r := range list {
			if name, ok := r.(string); ok {
				required[name] = true
			}
		}
	}

	m := map[string]any{}

	for _, k := range slices.Sorted(maps.Keys(props)) {
		if requiredOnly && !required[k] {
			continue
		}

		if v := g.synth(f, props[k], depth+1, requiredOnly); v != nil || required[k] {
			m[k] = v
		}
	}

	for name := range required {
		if _, ok := m[name]; !ok {
			m[name] = "x"
		}
	}

	return m
}

func (g *seeder) synthArray(f *schemaFile, s map[string]any, depth int, requiredOnly bool) any {
	if tuple, ok := s["items"].([]any); ok {
		arr := make([]any, 0, min(len(tuple), maxSeedItems))
		for _, item := range tuple[:min(len(tuple), maxSeedItems)] {
			arr = append(arr, g.synth(f, item, depth+1, requiredOnly))
		}

		return arr
	}

	items, ok := s["items"]
	if !ok {
		return []any{}
	}

	n := 1
	if minItems, ok := number(s["minItems"]); ok {
		n = int(min(max(minItems, 1), maxSeedItems))
	}

	if maxItems, ok := number(s["maxItems"]); ok && maxItems < float64(n) {
		n = int(maxItems)
	}

	if unique, _ := s["uniqueItems"].(bool); unique {
		n = min(n, 1)
	}

	arr := []any{}
	for range n {
		arr = append(arr, g.synth(f, items, depth+1, requiredOnly))
	}

	return arr
}

func synthString(s map[string]any) any {
	if format, ok := s["format"].(string); ok {
		if v, ok := formatSamples[format]; ok {
			return v
		}
	}

	n := 1
	if minLength, ok := number(s["minLength"]); ok {
		n = int(max(minLength, 1))
	}

	if maxLength, ok := number(s["maxLength"]); ok && maxLength < float64(n) {
		n = int(maxLength)
	}

	return strings.Repeat("a", n)
}

func synthNumber(s map[string]any, integer bool) any {
	v := 1.0

	if m, ok := number(s["minimum"]); ok {
		v = m
	}

	if m, ok := number(s["exclusiveMinimum"]); ok {
		v = m + 1
	}

	if m, ok := number(s["maximum"]); ok && v > m {
		v = m
	}

	if m, ok := number(s["multipleOf"]); ok && m > 0 {
		v = math.Ceil(v/m) * m
	}

	if integer {
		return json.Number(strconv.FormatFloat(math.Round(v), 'f', -1, 64))
	}

	return json.Number(strconv.FormatFloat(v, 'g', -1, 64))
}

// schemaType is the type a seed takes for a schema: its declared type, the first one that is not null for a list,
// or the type its keywords imply.
func schemaType(s map[string]any) string {
	switch t := s["type"].(type) {
	case string:
		return t
	case []any:
		for _, x := range t {
			if name, ok := x.(string); ok && name != "null" {
				return name
			}
		}
	}

	for _, implied := range []struct {
		name     string
		keywords []string
	}{
		{"object", []string{"properties", "additionalProperties", "required", "patternProperties"}},
		{"array", []string{"items", "minItems", "maxItems", "uniqueItems"}},
		{"string", []string{"minLength", "maxLength", "pattern", "format"}},
		{"number", []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"}},
	} {
		for _, k := range implied.keywords {
			if _, ok := s[k]; ok {
				return implied.name
			}
		}
	}

	return ""
}

func number(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}

	f, err := n.Float64()

	return f, err == nil
}

// mergeSeeds combines the seeds of a schema and of its allOf or first anyOf/oneOf branch: objects key by key, and
// otherwise the first one that is set.
func mergeSeeds(a, b any) any {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)

	if aok && bok {
		m := maps.Clone(am)
		for k, v := range bm {
			m[k] = mergeSeeds(m[k], v)
		}

		return m
	}

	if a == nil {
		return b
	}

	return a
}
