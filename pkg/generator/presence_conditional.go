package generator

import (
	"fmt"
	"slices"
	"strings"

	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// presenceClause is a subschema that applies to an object where some of its
// properties are present: an entry of dependentSchemas, or of a draft-07
// dependencies, keyed on its property, or an if/then/else whose if only lists
// required, keyed on those properties. Its then applies where they are all
// present, and its else where they are not.
type presenceClause struct {
	keys      []string
	then, els branchChecks

	// thenUnchecked and elseUnchecked name the keywords of the then and the
	// else that compile into no check.
	thenUnchecked, elseUnchecked []string

	// label names the clause in warnings: `dependentSchemas (k)`,
	// `dependencies (k)`, `its own if/then/else` or `allOf[i]`. keyword is
	// dependentSchemas or dependencies for an entry of one, and "" for an
	// if/then/else; own marks t's own if/then/else.
	label   string
	keyword string
	own     bool
}

// compiles reports whether the clause compiles into a check.
func (c presenceClause) compiles() bool {
	return c.then.any() || c.els.any()
}

// handled reports whether the struct declared for the clause's object stands
// for the clause: it compiles, or asks nothing that a check would miss.
func (c presenceClause) handled() bool {
	return c.compiles() || len(c.thenUnchecked) == 0 && len(c.elseUnchecked) == 0
}

// gaps describes, for a warning, what the clause leaves unchecked: what a
// clause that compiles leaves out, or, for an allOf element, that it compiles
// into nothing. One of t's own or an entry of dependentSchemas or dependencies
// that compiles into nothing is reported with the keywords nothing checks.
func (c presenceClause) gaps() []string {
	if !c.compiles() {
		if c.handled() || c.own || c.keyword != "" {
			return nil
		}

		return []string{c.label + "'s if/then/else not compiled"}
	}

	describe := func(side string, keywords []string) string {
		if len(keywords) == 0 {
			return ""
		}

		where := c.label + "'s " + side
		switch {
		case c.own:
			where = "its own " + side
		case c.keyword != "":
			where = c.label
		}

		return where + ": " + strings.Join(keywords, ", ") + " not checked"
	}

	return appendNonEmpty(nil, describe("then", c.thenUnchecked), describe("else", c.elseUnchecked))
}

// contextLabels returns the phrases the clause's errors give for when its then
// and its else apply.
func (c presenceClause) contextLabels() (string, string) {
	if len(c.keys) == 1 {
		return "when " + c.keys[0] + " is present", "when " + c.keys[0] + " is absent"
	}

	keys := strings.Join(c.keys, ", ")

	return "when " + keys + " are present", "when " + keys + " are not all present"
}

// presenceKeys returns the properties an `if` requires, when it asks nothing
// else of an object: besides `required`, at most `type: object` and
// annotations. Such an `if` holds where they are all present.
func presenceKeys(ifc *schemas.Type) ([]string, bool) {
	if ifc == nil || len(ifc.Required) == 0 || !onlyRequired(ifc) {
		return nil, false
	}

	keys := make([]string, 0, len(ifc.Required))
	for _, key := range ifc.Required {
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}

	return keys, true
}

// isPresenceConditional reports whether elem, an allOf element or a schema's
// own conditional, is an if/then/else keyed on presence, with a then or an
// else.
func isPresenceConditional(elem *schemas.Type) bool {
	if elem == nil || (elem.Then == nil && elem.Else == nil) {
		return false
	}

	_, ok := presenceKeys(elem.If)

	return ok
}

// isPresenceElement reports whether elem, an element of an allOf, is a
// presence-keyed if/then/else that declares nothing besides, so that the
// struct of the object that holds the allOf can check it.
func isPresenceElement(elem *schemas.Type) bool {
	return isPresenceConditional(elem) && onlyConditional(elem)
}

// checkedSchemaDependencies returns the dependentSchemas of t's own and the
// schema dependencies of its draft-07 `dependencies` that the struct declared
// for t checks, as checkedPropertyDependencies does for property
// dependencies: where the struct stands for t, each where the draft that the
// document declares defines the keyword.
func (g *schemaGenerator) checkedSchemaDependencies(
	t *schemas.Type,
) (map[string]*schemas.Type, map[string]*schemas.Type) {
	dependentSchemas, dependencies := schemaDependencies(t)
	if len(dependentSchemas) == 0 && len(dependencies) == 0 || g.config.OnlyModels || !g.structStandsFor(t) {
		return nil, nil
	}

	switch {
	case g.declaresDraft2019OrLater(t):
		dependencies = nil
	case g.declaresDraftBefore2019(t):
		dependentSchemas = nil
	}

	return dependentSchemas, dependencies
}

// presenceClauses returns the presence-keyed clauses that the struct declared
// for t checks, in order: those of its dependentSchemas, of its draft-07
// dependencies, its own if/then/else, and its allOf's. It returns none where
// the struct does not stand for t.
func (g *schemaGenerator) presenceClauses(t *schemas.Type) []presenceClause {
	if g.config.OnlyModels || !g.structStandsFor(t) {
		return nil
	}

	dependentSchemas, dependencies := g.checkedSchemaDependencies(t)

	clauses := g.dependencyClauses(dependentSchemas, keywordDependentSchemas)
	clauses = append(clauses, g.dependencyClauses(dependencies, keywordDependencies)...)

	if c, ok := g.ownPresenceClause(t); ok {
		clauses = append(clauses, c)
	}

	return append(clauses, g.allOfPresenceClauses(t)...)
}

// dependencyClauses returns the clauses of deps, the schemas keyword keys on
// properties, in property order. A schema that admits every value asks
// nothing, and has no clause.
func (g *schemaGenerator) dependencyClauses(deps map[string]*schemas.Type, keyword string) []presenceClause {
	clauses := make([]presenceClause, 0, len(deps))

	for _, name := range sortedKeys(deps) {
		if !constrains(deps[name]) {
			continue
		}

		c := g.compilePresenceClause([]string{name}, deps[name], nil)
		c.label, c.keyword = keyword+" ("+name+")", keyword
		clauses = append(clauses, c)
	}

	return clauses
}

// ownPresenceClause returns t's own if/then/else, when it is keyed on
// presence. The caller checks that t is no merge, whose conditionals come
// from its branches.
func (g *schemaGenerator) ownPresenceClause(t *schemas.Type) (presenceClause, bool) {
	own := &schemas.Type{If: t.If, Then: t.Then, Else: t.Else}
	if !isPresenceConditional(own) {
		return presenceClause{}, false
	}

	keys, _ := presenceKeys(t.If)

	c := g.compilePresenceClause(keys, t.Then, t.Else)
	c.label, c.own = "its own if/then/else", true

	return c, true
}

// allOfPresenceClauses returns the clauses of t's allOf elements that are
// presence-keyed if/then/elses and declare nothing besides.
func (g *schemaGenerator) allOfPresenceClauses(t *schemas.Type) []presenceClause {
	var clauses []presenceClause

	for i, elem := range t.AllOf {
		if !isPresenceElement(elem) {
			continue
		}

		keys, _ := presenceKeys(elem.If)

		c := g.compilePresenceClause(keys, elem.Then, elem.Else)
		c.label = fmt.Sprintf("allOf[%d]", i)
		clauses = append(clauses, c)
	}

	return clauses
}

// compilePresenceClause compiles a clause keyed on keys, whose then and else
// are as given. A then of false, written there or where its $refs lead,
// forbids the keys together, and an else of false requires them all.
func (g *schemaGenerator) compilePresenceClause(keys []string, then, els *schemas.Type) presenceClause {
	c := presenceClause{keys: keys}

	if g.admitsNothing(then, g.schema, g.schemaFileName) {
		c.then.forbidden = [][]string{keys}
	} else {
		c.then, c.thenUnchecked = g.compileBranch(then, branchOptions{presence: true})
	}

	if g.admitsNothing(els, g.schema, g.schemaFileName) {
		c.els.required = keys
	} else {
		c.els, c.elseUnchecked = g.compileBranch(els, branchOptions{presence: true})
	}

	return c
}

// presenceClauseGaps returns what the presence-keyed clauses of t that
// compile leave unchecked, and those of its dependentSchemas and draft-07
// dependencies that compile into no check, by keyword.
func (g *schemaGenerator) presenceClauseGaps(t *schemas.Type) ([]string, map[string][]string) {
	var (
		gaps      []string
		uncovered = map[string][]string{}
	)

	for _, c := range g.presenceClauses(t) {
		gaps = append(gaps, c.gaps()...)

		if c.keyword != "" && !c.handled() {
			uncovered[c.keyword] = append(uncovered[c.keyword], c.keys[0])
		}
	}

	return gaps, uncovered
}

// presenceConditionalValidator checks the presence-keyed clauses of an
// object: where the properties a clause is keyed on are all present, the
// checks of its then, and where they are not, those of its else.
//
// It reads the raw map before the typed decode, as the conditional
// discriminator does, since the struct cannot tell an absent property from a
// zero one, and a narrowing asks something of the value as written, not of
// the field it decodes into.
type presenceConditionalValidator struct {
	declName string
	clauses  []presenceClause

	// numberHelper and typeHelper name the file's raw value helpers, which a
	// leaf calls.
	numberHelper, typeHelper string
}

func (v *presenceConditionalValidator) desc() *validatorDesc {
	d := &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: true,
	}

	for _, c := range v.clauses {
		for _, leaf := range slices.Concat(c.then.leaves, c.els.leaves) {
			d.imports = append(d.imports, leaf.imports()...)
		}
	}

	return d
}

var _ fileHelperValidator = new(presenceConditionalValidator)

// useFileHelpers declares the file's raw value helpers, where a leaf calls
// them.
func (v *presenceConditionalValidator) useFileHelpers(o *output, typeName string) {
	for _, c := range v.clauses {
		if slices.ContainsFunc(slices.Concat(c.then.leaves, c.els.leaves), func(n namedNarrowing) bool {
			return n.usesHelpers()
		}) {
			v.numberHelper, v.typeHelper = o.rawValueHelpers(typeName)

			return
		}
	}
}

// generate writes each clause's checks. A null decodes to a nil raw map, which
// holds no property, so it passes, as it passes the required checks.
func (v *presenceConditionalValidator) generate(out *codegen.Emitter, _ string) error {
	for _, c := range v.clauses {
		thenLabel, elseLabel := c.contextLabels()

		if len(c.keys) == 1 {
			out.Printlnf(`if _, ok := %s[%q]; ok {`, varNameRawMap, c.keys[0])
		} else {
			out.Printlnf(`{`)
			out.Indent(1)
			out.Printlnf(`keysPresent := 0`)

			for _, key := range c.keys {
				out.Printlnf(`if _, ok := %s[%q]; ok {`, varNameRawMap, key)
				out.Indent(1)
				out.Printlnf(`keysPresent++`)
				out.Indent(-1)
				out.Printlnf(`}`)
			}

			out.Printlnf(`if keysPresent == %d {`, len(c.keys))
		}

		out.Indent(1)
		v.emitChecks(out, c.then, thenLabel)
		out.Indent(-1)

		if c.els.any() {
			out.Printlnf(`} else if %s != nil {`, varNameRawMap)
			out.Indent(1)
			v.emitChecks(out, c.els, elseLabel)
			out.Indent(-1)
		}

		out.Printlnf(`}`)

		if len(c.keys) > 1 {
			out.Indent(-1)
			out.Printlnf(`}`)
		}
	}

	return nil
}

// emitChecks writes the checks of a then or else: those a value-keyed branch
// has, then the leaves and the presence counts.
func (v *presenceConditionalValidator) emitChecks(out *codegen.Emitter, checks branchChecks, contextLabel string) {
	branch := &conditionalDiscriminatorValidator{declName: v.declName}
	branch.emitChecks(out, checks, goStringText(contextLabel), "")

	leaves := rawNarrowingEmitter{declName: v.declName, numberHelper: v.numberHelper, typeHelper: v.typeHelper}
	for _, leaf := range checks.leaves {
		leaves.emitProperty(out, leaf.name, leaf.rawNarrowing, contextLabel)
	}

	for _, count := range checks.counts {
		emitPresenceCount(out, v.declName, count.keyword, count.branches, contextLabel)
	}
}

// compiledPresenceClauses returns the presence-keyed clauses of t that compile
// into checks.
func (g *schemaGenerator) compiledPresenceClauses(t *schemas.Type) []presenceClause {
	return slices.DeleteFunc(g.presenceClauses(t), func(c presenceClause) bool { return !c.compiles() })
}

// compilesOwnPresence reports whether the struct declared for t stands for t's
// own if/then/else, keyed on presence: the validator checks it, or it asks
// nothing a check would miss.
func (g *schemaGenerator) compilesOwnPresence(t *schemas.Type) bool {
	if g.config.OnlyModels || !g.structStandsFor(t) {
		return false
	}

	c, ok := g.ownPresenceClause(t)

	return ok && c.handled()
}
