package schemas

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/plheide/go-jsonschema/pkg/yamlutils"
)

func FromJSONFile(fileName string) (*Schema, error) {
	f, err := os.Open(fileName)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	defer func() {
		_ = f.Close()
	}()

	return FromJSONReader(f)
}

func FromJSONReader(r io.Reader) (*Schema, error) {
	var schema Schema
	if err := json.NewDecoder(r).Decode(&schema); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return &schema, nil
}

func FromYAMLFile(fileName string) (*Schema, error) {
	f, err := os.Open(fileName)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	defer func() {
		_ = f.Close()
	}()

	return FromYAMLReader(f)
}

func FromYAMLReader(r io.Reader) (*Schema, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML: %w", err)
	}

	// Marshal to JSON first because YAML decoder doesn't understand JSON tags.
	var m map[string]any

	if err := yaml.Unmarshal(data, &m); err != nil {
		// A boolean schema, true or false, is no mapping.
		var b bool
		if yaml.Unmarshal(data, &b) != nil {
			return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
		}

		return FromJSONReader(strings.NewReader(strconv.FormatBool(b)))
	}

	yamlutils.FixMapKeys(m)

	value, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}

	var schema Schema

	if err = json.Unmarshal(value, &schema); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return &schema, nil
}
