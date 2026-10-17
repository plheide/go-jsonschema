package tests_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	"github.com/plheide/go-jsonschema/pkg/generator"
	testDefaultLiterals "github.com/plheide/go-jsonschema/tests/data/miscWithDefaults/defaultLiterals"
)

// TestDefaultLiterals decodes a document without keys into a type whose
// defaults are written as literals of their fields' Go types: a named map, a
// struct found field by field through JSON names, with pointer, nested struct
// and list fields, and a list of structs. The defaults no value of the
// field's type can hold were left out, so those fields stay at zero.
func TestDefaultLiterals(t *testing.T) {
	t.Parallel()

	for name, decode := range map[string]func([]byte, any) error{
		"json": json.Unmarshal,
		"yaml": yamlv3.Unmarshal,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var v testDefaultLiterals.DefaultLiterals
			require.NoError(t, decode([]byte("{}"), &v))

			assert.Equal(t, testDefaultLiterals.Labels{"team": "core", "tier": "1"}, v.Labels)

			assert.Equal(t, "localhost", v.Server.HostName)
			require.NotNil(t, v.Server.Port)
			assert.Equal(t, 8080, *v.Server.Port)
			require.NotNil(t, v.Server.Note)
			assert.Equal(t, "primary", *v.Server.Note)
			assert.Equal(t, []string{"a", "b"}, v.Server.Aliases)
			require.NotNil(t, v.Server.Tls)
			require.NotNil(t, v.Server.Tls.Enabled)
			assert.True(t, *v.Server.Tls.Enabled)

			require.Len(t, v.Servers, 2)
			assert.Equal(t, "a", v.Servers[0].HostName)
			assert.Nil(t, v.Servers[0].Port)
			require.NotNil(t, v.Servers[1].Port)
			assert.Equal(t, 1, *v.Servers[1].Port)

			// The largest float64 below 2^63 fits an int; 2^63 itself does not.
			assert.Equal(t, 9223372036854774784, v.Big)
			assert.Zero(t, v.Huge)

			assert.False(t, v.Enabled)
			assert.Zero(t, v.Count)
			assert.Zero(t, v.Ratio)
			assert.Nil(t, v.Tags)
			assert.Nil(t, v.Codes)
			assert.True(t, v.Started.IsZero())
			assert.Zero(t, v.MismatchedServer)
			assert.Zero(t, v.UnknownKeyServer)
		})
	}
}

// TestDefaultLiteralsWarnings checks that each default left out is reported
// once, by its JSON value, its field and the Go type it is not a value of,
// and that the defaults written are not reported.
func TestDefaultLiteralsWarnings(t *testing.T) {
	t.Parallel()

	abs, err := filepath.Abs("./data/miscWithDefaults/defaultLiterals/defaultLiterals.json")
	require.NoError(t, err)

	var (
		mu       sync.Mutex
		warnings []string
	)

	cfg := basicConfig
	cfg.Warner = func(msg string) {
		mu.Lock()
		defer mu.Unlock()

		warnings = append(warnings, msg)
	}

	gen, err := generator.New(cfg)
	require.NoError(t, err)
	require.NoError(t, gen.DoFile(abs))

	// Defaults are written, and so checked, with the code.
	_, err = gen.Sources()
	require.NoError(t, err)

	var leftOut []string

	for _, w := range warnings {
		if strings.HasSuffix(w, "; it is left out") {
			leftOut = append(leftOut, w)
		}
	}

	assert.ElementsMatch(t, []string{
		`default "true" for field "enabled" is not a value of Go type bool; it is left out`,
		`default "5" for field "count" is not a value of Go type int; it is left out`,
		// JSON writes 2^63 in its shortest form.
		`default 9223372036854776000 for field "huge" is not a value of Go type int; it is left out`,
		`default "half" for field "ratio" is not a value of Go type float64; it is left out`,
		`default "a" for field "tags" is not a value of Go type []string; it is left out`,
		`default [1,2] for field "codes" is not a value of Go type []string; it is left out`,
		`default "2020-01-01T00:00:00Z" for field "started" is not a value of Go type time.Time; it is left out`,
		`default {"host_name":"x","port":"80"} for field "mismatchedServer" is not a value of Go type Server; it is left out`,
		`default {"colour":"red","host_name":"x"} for field "unknownKeyServer" is not a value of Go type Server; it is left out`,
	}, leftOut)
}
