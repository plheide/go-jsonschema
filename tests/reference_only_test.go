package tests_test

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/plheide/go-jsonschema/pkg/generator"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// TestCrossPackageReferenceOnly maps two packages to no output, for another run
// to write. Nothing is written for them, and the schema that refers to them
// imports both, as when this run writes them too: its file is the golden of
// TestCrossPackageAllOfInlined. Before, the second such package failed, as
// mapped to the same file as the first.
func TestCrossPackageReferenceOnly(t *testing.T) {
	t.Parallel()

	const (
		dir    = "./data/crossPackageAllOfInlined"
		module = "github.com/plheide/go-jsonschema/tests/data/crossPackageAllOfInlined"
	)

	cfg := basicConfig
	cfg.StrictAdditionalProperties = generator.StrictAdditionalPropertiesRespectSchema
	cfg.SchemaMappings = []generator.SchemaMapping{
		{SchemaID: "https://example.com/schema", PackageName: module + "/schema", OutputName: "schema.go"},
		{SchemaID: "https://example.com/header", PackageName: module + "/header", ImportAlias: "headerv1"},
		{SchemaID: "https://example.com/order", PackageName: module + "/order", ImportAlias: "orderv1"},
	}

	golden, err := os.ReadFile(dir + "/schema/schema.go")
	require.NoError(t, err)

	// The unwritten packages' own schemas may be given as well, first, so that
	// their types are named as their own run names them. They are still written
	// nowhere.
	for _, files := range [][]string{
		{dir + "/schema/schema.json"},
		{dir + "/header/header.json", dir + "/order/order.json", dir + "/schema/schema.json"},
	} {
		g, err := generator.New(cfg)
		require.NoError(t, err)

		for _, file := range files {
			require.NoError(t, g.DoFile(file))
		}

		sources, err := g.Sources()
		require.NoError(t, err)

		assert.Equal(t, []string{"schema.go"}, slices.Collect(maps.Keys(sources)), files)
		assert.Equal(t, string(golden), string(sources["schema.go"]), files)
	}
}

// TestReferenceOnlyNameCollision: a type whose name collides in a package that
// this run writes no file for is named here, but the run that writes the
// package names it too, perhaps otherwise, so the warning says so.
func TestReferenceOnlyNameCollision(t *testing.T) {
	t.Parallel()

	var warnings []string

	cfg := basicConfig
	cfg.Warner = func(message string) { warnings = append(warnings, message) }
	cfg.SchemaMappings = []generator.SchemaMapping{
		{SchemaID: "https://example.com/first", PackageName: "example.com/shared"},
		{SchemaID: "https://example.com/second", PackageName: "example.com/shared"},
	}

	g, err := generator.New(cfg)
	require.NoError(t, err)

	for name, document := range map[string]string{
		"first.json":  `{"$id": "https://example.com/first", "$defs": {"Item": {"type": "object"}}}`,
		"second.json": `{"$id": "https://example.com/second", "$defs": {"Item": {"type": "string"}}}`,
	} {
		schema, err := schemas.FromJSONReader(strings.NewReader(document))
		require.NoError(t, err)
		require.NoError(t, g.AddFile(name, schema))
	}

	_, err = g.Sources()
	require.NoError(t, err)

	assert.Contains(t, warnings, `Multiple types map to the name "Item"; declaring duplicate as "Item_1" instead. `+
		`This run writes no file for package "example.com/shared", so the run that writes it must give the type `+
		`the same name`)
}
