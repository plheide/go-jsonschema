package generator

import (
	"slices"

	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// keywordDependentRequired and keywordDependentSchemas are the keywords that
// 2019-09 split keywordDependencies, draft-07's dual-form keyword, into.
const (
	keywordDependentRequired = "dependentRequired"
	keywordDependentSchemas  = "dependentSchemas"
	keywordDependencies      = "dependencies"
)

// dependentRequiredValidator checks an object's dependentRequired and the
// property dependencies of its draft-07 `dependencies`: where a property is
// present, each property they list must be present too.
//
// It reads the raw map before the typed decode, as the presence counts do,
// since the struct cannot tell an absent property from a zero one.
type dependentRequiredValidator struct {
	declName     string
	dependencies map[string][]string
}

func (v *dependentRequiredValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: true,
	}
}

// generate writes, for each property in name order, the checks of what it
// requires. A null decodes to a nil raw map, which holds no property, so it
// passes, as it passes the required checks.
func (v *dependentRequiredValidator) generate(out *codegen.Emitter, _ string) error {
	for _, name := range sortedKeys(v.dependencies) {
		dependents := v.dependencies[name]
		if len(dependents) == 0 {
			continue
		}

		out.Printlnf(`if _, ok := %s[%q]; ok {`, varNameRawMap, name)
		out.Indent(1)

		for _, dependent := range dependents {
			out.Printlnf(`if _, ok := %s[%q]; !ok {`, varNameRawMap, dependent)
			out.Indent(1)
			out.Printlnf(`return fmt.Errorf("field %s in %s: required when %s is present")`,
				goStringText(dependent), v.declName, goStringText(name))
			out.Indent(-1)
			out.Printlnf(`}`)
		}

		out.Indent(-1)
		out.Printlnf(`}`)
	}

	return nil
}

// dependentRequiredOf returns the property dependencies that the struct
// declared for t checks, those of dependentRequired and of a draft-07
// `dependencies` together (see checkedPropertyDependencies).
func (g *schemaGenerator) dependentRequiredOf(t *schemas.Type) map[string][]string {
	return mergeDependencies(g.checkedPropertyDependencies(t))
}

// checkedPropertyDependencies returns the dependentRequired of t's own and
// the property dependencies of its draft-07 `dependencies` that the struct
// declared for t checks: where the struct stands for t (see structStandsFor),
// each where the draft that the document declares defines the keyword.
// dependentRequired came with 2019-09, which dropped `dependencies`; a
// document that declares no draft has both checked.
func (g *schemaGenerator) checkedPropertyDependencies(t *schemas.Type) (map[string][]string, map[string][]string) {
	dependentRequired, dependencies := propertyDependencies(t)
	if len(dependentRequired) == 0 && len(dependencies) == 0 || g.config.OnlyModels || !g.structStandsFor(t) {
		return nil, nil
	}

	switch {
	case g.declaresDraft2019OrLater(t):
		dependencies = nil
	case g.declaresDraftBefore2019(t):
		dependentRequired = nil
	}

	return dependentRequired, dependencies
}

// propertyDependencies returns t's own dependentRequired and the property
// dependencies of its draft-07 `dependencies`, each nil where t declares none.
func propertyDependencies(t *schemas.Type) (map[string][]string, map[string][]string) {
	if t.DependentRequiredFromDependencies {
		return nil, t.DependenciesRequired
	}

	return t.DependentRequired, t.DependenciesRequired
}

// schemaDependencies returns t's own dependentSchemas and the schema
// dependencies of its draft-07 `dependencies`, each nil where t declares none.
func schemaDependencies(t *schemas.Type) (map[string]*schemas.Type, map[string]*schemas.Type) {
	if t.DependentSchemasFromDependencies {
		return nil, t.DependenciesSchemas
	}

	return t.DependentSchemas, t.DependenciesSchemas
}

// mergeDependencies returns the property dependencies of a and b together:
// for each property, the properties a lists, then those that b adds. Where
// one is empty, it returns the other.
func mergeDependencies(a, b map[string][]string) map[string][]string {
	if len(b) == 0 {
		return a
	}

	if len(a) == 0 {
		return b
	}

	merged := make(map[string][]string, len(a)+len(b))
	for name, dependents := range a {
		merged[name] = slices.Clone(dependents)
	}

	for name, dependents := range b {
		for _, dependent := range dependents {
			if !slices.Contains(merged[name], dependent) {
				merged[name] = append(merged[name], dependent)
			}
		}
	}

	return merged
}
