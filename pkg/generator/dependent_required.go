package generator

import (
	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// keywordDependentRequired is the keyword that dependentRequiredValidator
// checks, and the name a draft-07 `dependencies` of property lists is
// reported under. keywordDependencies is that draft-07 keyword.
const (
	keywordDependentRequired = "dependentRequired"
	keywordDependencies      = "dependencies"
)

// dependentRequiredValidator checks an object's dependentRequired, or the
// property dependencies of a draft-07 `dependencies`: where a property is
// present, each property it lists must be present too.
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
// declared for t checks: t's own dependentRequired, or the property
// dependencies of its draft-07 `dependencies`, where the struct stands for t
// (see structStandsFor) and the draft that the document declares defines the
// keyword. dependentRequired came with 2019-09, which dropped `dependencies`;
// a document that declares no draft has both checked.
func (g *schemaGenerator) dependentRequiredOf(t *schemas.Type) map[string][]string {
	if len(t.DependentRequired) == 0 || g.config.OnlyModels || !g.structStandsFor(t) {
		return nil
	}

	if t.DependentRequiredFromDependencies {
		if g.declaresDraft2019OrLater(t) {
			return nil
		}
	} else if g.declaresDraftBefore2019(t) {
		return nil
	}

	return t.DependentRequired
}
