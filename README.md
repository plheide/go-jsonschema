> [!NOTE]
> **This is a fork of [omissis/go-jsonschema](https://github.com/omissis/go-jsonschema).** It carries generator
> improvements that are offered upstream as
> [pull requests](https://github.com/omissis/go-jsonschema/pulls?q=is%3Apr+author%3Aplheide) but not merged yet,
> among them:
>
> * draft-07 tuple-form `items`, and both forms of `dependencies`;
> * `oneOf` generated as typed wrappers and dispatching holders instead of `interface{}`;
> * `x-enum-varnames` naming the generated enum constants.
>
> Almost all of it is offered upstream; [FORK.md](FORK.md) lists the exceptions. The fork is a staging area, not a
> competitor. [Differences from upstream](#differences-from-upstream) lists how its output differs, and
> [Beyond JSON Schema](#beyond-json-schema) what it reads and does that JSON Schema doesn't define.

**go-jsonschema is a tool to generate Go data types from [JSON Schema](http://json-schema.org/) definitions.**

This tool generates Go data types and structs that corresponds to definitions in the schema,
along with unmarshalling code that validates the input JSON according to the schema's validation rules.

## Badges

[![GitHub release](https://img.shields.io/github/v/release/plheide/go-jsonschema?include_prereleases&style=flat)](https://github.com/plheide/go-jsonschema/releases)
[![GitHub Workflow Status (event)](https://img.shields.io/github/actions/workflow/status/plheide/go-jsonschema/development.yaml?style=flat)](https://github.com/plheide/go-jsonschema/actions?workflow=development)
[![Go Reference](https://pkg.go.dev/badge/github.com/plheide/go-jsonschema.svg)](https://pkg.go.dev/github.com/plheide/go-jsonschema)
[![License](https://img.shields.io/github/license/plheide/go-jsonschema?style=flat)](/LICENSE.md)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/plheide/go-jsonschema?style=flat)](https://tip.golang.org/doc/go1.25)
[![GitHub all releases](https://img.shields.io/github/downloads/plheide/go-jsonschema/total?style=flat)](https://github.com/plheide/go-jsonschema/releases)
[![GitHub commit activity](https://img.shields.io/github/commit-activity/y/plheide/go-jsonschema?style=flat)](https://github.com/plheide/go-jsonschema/commits)

## Installing

* **Download**: Get a prebuilt binary from [GitHub releases](https://github.com/plheide/go-jsonschema/releases).

* **Install from source**: To install with Go 1.25+:

```shell
go install github.com/plheide/go-jsonschema@latest
```

* **Container image**: `plheide/go-jsonschema` on Docker Hub, or `ghcr.io/plheide/go-jsonschema`.
  Each release is tagged with its version, and only stable releases set `latest`, so name a version:

```shell
docker run --rm -v "$PWD:/work" -w /work plheide/go-jsonschema:v0.27.0 -p main schema.json
```

* **Homebrew**: the `omissis/go-jsonschema` tap installs upstream's releases, not this fork.

## Contributing

This project makes use of [go workspaces](https://go.dev/ref/mod#workspaces) in order to ease testing of the
generated code during development while keeping the codebase as tidy and maintainable as possible.
It's an unusual choice, but it allows to not only test the code-generation logic, but also the generated code itself.

## Usage

At its most basic:

```shell
go-jsonschema -p main schema.json
```

This will write a Go source file to standard output, declared under the package `main`.

The root type is named after the schema file, so `schema.json` gives `SchemaJson`.
`--schema-root-type URI=TYPE` names it explicitly. Each `--resolve-extension` is also
removed from the file name first: with `--resolve-extension .schema.json`,
`widget.schema.json` gives `Widget` rather than `WidgetSchemaJson`. The same applies to
another file a `$ref` points at as a whole, while a `$ref` to one of its definitions, such as
`lib.schema.json#/definitions/Thing`, names the type after the definition.

You can generate code for multiple schemas in one run, optionally writing to different files inside different packages:

```shell
$ go-jsonschema \
  --schema-package=https://example.com/schema1=github.com/myuser/myproject \
   --schema-output=https://example.com/schema1=schema1.go \
  --schema-package=https://example.com/schema2=github.com/myuser/myproject/stuff \
   --schema-output=https://example.com/schema2=stuff/schema2.go \
  schema1.json schema2.json
```

This will create `schema1.go` (declared as `package myproject`) and `stuff/schema2.go` (declared as `package stuff`).
If `schema1.json` refers to `schema2.json` or viceversa, the two Go files will import the other depended-on package.
Note the flag format:

```text
--schema-package=https://example.com/schema1=github.com/myuser/myproject \
                 ^                           ^
                 |                           |
                 schema $id                  full import URL
```

### Cross-tree `$ref`s without network access

When a consumer schema `$ref`s a canonical schema by its `$id` URL (e.g.
`https://schemas.example.com/header/v1/header.schema.json`), the loader will
attempt an HTTP fetch unless the URL is pre-resolved. In CI environments
behind a firewall — or when the canonical host is unreachable on principle —
this fails with `dial tcp: lookup ...: no such host`.

`--known-schema URL=PATH` pre-populates the loader cache from disk so
references to URL resolve to PATH's contents without any network attempt.
The schema is loaded for ref resolution only; no Go code is emitted for it.

```shell
$ go-jsonschema \
  --known-schema=https://schemas.example.com/header/v1/header.schema.json=./vendor-schemas/header.schema.json \
  --schema-package=https://schemas.example.com/header/v1/header.schema.json=example.com/lib/schemas/header/v1:headerv1 \
  --schema-package=https://schemas.example.com/jobs/v1/jobs.schema.json=example.com/jobs/v1:jobsv1 \
  consumer.schema.json
```

The `:ALIAS` suffix on `--schema-package` overrides the import alias used
in generated code (default: the package path's last segment). Set it when
two referenced packages share a last segment (e.g. both end in `/v1`) so
the generated file doesn't get an `import v1 "..."` collision. The alias
must be a valid non-keyword Go identifier; the parser splits the value on
the **last** colon. Library callers can set the same overrides via
`generator.Config.Cache` and `generator.SchemaMapping.ImportAlias`.

### Regenerating tests' golden files

It sometimes happen that new features or bug fixes to the library require regenerating the tests' golden files, here's how to do it:

```
export OVERWRITE_EXPECTED_GO_FILE="true"
make test
```

### Special types

In a few cases, special types are used to help with serializing/deserializing
data frrom JSON. Namely a custom types is provided for the following semantic
types:

* `SerializableDate`
* `SerializableTime`

These types are needed because there is no native type provided by Go which
properly handles them.

## Beyond JSON Schema

The generator reads a few inputs that are not part of JSON Schema, and makes a few choices that JSON Schema leaves
open. This section lists all of them, with the flags that turn them on or off. Everything else follows JSON Schema,
or is listed as unsupported under [Status](#status).

### Inputs that are not JSON Schema

| Input | Effect | From |
| --- | --- | --- |
| `goJSONSchema` | On a schema, `type` names a Go type to use instead of generating one, `imports` adds the imports it needs, and `nillable: true` says it needs no pointer. On a property, `identifier` names the field, `pointer` makes it a pointer (`true`) or a value (`false`), and `extraTags` adds struct tags. | upstream |
| `x-enum-varnames` | Names the constants generated for an enum's values, in `enum` order, as OpenAPI generators read it. | fork |
| Other `x-` keys | Become struct tags when mapped with `--extension-tag x-key=tag`, and are ignored otherwise. | fork, opt-in |
| `format: int32` and `format: int64` on an `integer` | Go `int32` and `int64`, the OpenAPI meaning of these formats, with `--openapi-int-formats`. Without it, an `integer` is an `int`, as upstream generates it. | fork, opt-in |
| `format: uuid` | Checked by `--validate-formats` in every draft, although JSON Schema defines it only from draft 2019-09. | fork, opt-in |
| `deprecated` given as a string, as schemas generated from TypeScript give it | Marks the field or type as deprecated, as `true` does, with the string as the text of its `Deprecated:` paragraph. A value that is neither a boolean nor a string is ignored. | fork |

### Choices JSON Schema leaves open

* **`default`** is only an annotation in JSON Schema. Generated code assigns it when the key is absent from the
  input: to struct fields, and in the fork also to fields that hold a union wrapper.
* **`readOnly: true`** properties are rejected when the input contains them, which JSON Schema allows.
  `--disable-readonly-validation` turns that off.
* **`deprecated: true`** adds a `Deprecated:` paragraph to the doc comment of the field or type, which gopls and
  staticcheck report wherever it is used. JSON Schema defines the keyword from draft 2019-09; the generator reads it
  in every draft.
* **Properties a schema allows without declaring them** are dropped on decode, unless the schema declares
  `additionalProperties`. In that case an `AdditionalProperties` field holds them.
* **`--strict-additional-properties=strict`** rejects undeclared properties on every object, including where the
  schema allows them. That is stricter than JSON Schema.
* **`--only-models`** generates the types without unmarshalers, so nothing is checked.
* **Numbers in untyped values**, `interface{}` fields and the values they hold, are decoded from JSON as `float64`,
  which loses integers beyond 2^53. `--use-number` decodes them as `json.Number`, which keeps them exact, and writes
  their defaults so too. Typed fields, enum values and `AdditionalProperties` are decoded as before, and so is YAML,
  since yaml.v3 decodes integers exactly.

### Checks that are off by default

Generated code skips these JSON Schema checks unless a flag turns them on, so its behavior stays the same as
upstream's:

| Check | Flag |
| --- | --- |
| `format` | `--validate-formats`, described [below](#opt-in-format-validation) |
| `additionalProperties: false` | `--strict-additional-properties=respect-schema`, described [below](#opt-in-additionalproperties-false-enforcement) |
| `null` where `type` excludes it | `--validate-null-types`. It checks properties, the elements of arrays, and declared types that are not objects. In YAML a document that is only `null` is not checked, since yaml.v3 does not pass one to `UnmarshalYAML`. |

### Fidelity warnings

When generated code doesn't check something a schema declares, the generator says so on stderr, once for each type:

* `schema fidelity: type X falls back to interface{} due to <reason>; declared but not enforced: <keywords>` means
  the type became `interface{}`.
* `schema fidelity: type X does not enforce all it declares: <reason>` means the type kept its shape but dropped a
  `oneOf`, an `if`/`then`/`else`, a `not` or a `false` schema, or the keywords of a property that an `anyOf` branch
  declares as well. A reason such as `minProperties, propertyNames not checked` names keywords that no generated
  code checks: `contains` (with `minContains` and `maxContains`), `dependentRequired` and `dependentSchemas` (draft
  07's `dependencies` counts as these), `maxProperties`, `minProperties`, `patternProperties`, `propertyNames`,
  `unevaluatedItems` and `unevaluatedProperties`. A keyword whose subschemas admit every value, such as
  `"patternProperties": {"^x-": {}}`, checks nothing and is not reported.

### Options for names, types and tags

`--capitalization`, `--collision-strategy`, `--disable-custom-types-for-maps`, `--disable-omitempty`,
`--disable-omitzero`, `--extra-imports`, `--min-sized-ints`, `--minimal-names`, `--struct-name-from-title`, `--tags`
and `Config.AliasSingleAllOfAnyOfRefs` choose how the generated code is named, typed and tagged.
`--collision-strategy` is the fork's; the others are upstream's.

## Differences from upstream

For the same schema and flags, the fork's output differs from upstream's v0.24 output as listed below. All of it is
offered upstream, apart from the few changes [FORK.md](FORK.md) lists.

### Types

| Schema | Upstream | Fork |
| --- | --- | --- |
| `"type": ["number", "null"]` | `type FooRate *float64`, a named pointer type, which can have no methods | `*float64` |
| `oneOf` of `string`, `number`, `boolean` and `null` variants, or `"type": ["string", "number"]` | `interface{}` | A struct holding one of the values, decoded by its JSON kind. Accessors such as `AsString()` read the value, and constructors such as `NewFooString(v)` build one |
| `oneOf` of objects told apart by a `const` property | `interface{}` | A struct with a field per variant, decoded by that property |
| Any other `oneOf` of objects | `interface{}` | A struct that tries each variant, and accepts the input only when exactly one matches |
| `oneOf` or `anyOf` of a `$ref`, an object or an array, and `{"type": "null"}` | `interface{}` for a `oneOf`, a copy of the schema for an `anyOf` | A pointer to the schema's own type |
| `anyOf` of an object with properties and a primitive or an array | The first branch's type: a primitive such as `*string`, which rejects every object, or a struct, which rejects the primitive | `interface{}`, with a fidelity warning |
| An enum listing `null` beside values of one type, such as `"enum": ["a", "b", null]` | A struct holding an `interface{}` | A pointer to an enum of that type |
| An enum with `x-enum-varnames: ["Low", "High"]` | Constants named after the type and the value, such as `FooLevelLo` | `Low` and `High` |
| `oneOf`, `anyOf`, `allOf` or `enum` at the root | No root type | The root type |

### Schemas that only the fork generates

* Draft-07 tuple-form `items`, which upstream fails to parse. The array is `[]interface{}`, and `additionalItems:
  false` limits its length.
* Draft-07 `dependencies` in the property-list form, such as `{"a": ["b"]}`, which upstream fails to parse inside a
  subschema. It is parsed, but not enforced.
* `$ref`s to nested definitions, such as `#/definitions/a/definitions/b`, in the same file or another one.

### Checks the fork adds

* An `if`/`then`/`else` keyed on one property's `const` or `enum`, in an `allOf` or on the object itself, checks each
  branch's `required` and a `not` of `required`, also where the property is optional or a nested object holds it, as
  described under [Validation](#validation).
* A `oneOf` or `anyOf` whose branches only list `required` checks how many of the listed property sets an object
  has: exactly one for a `oneOf`, at least one for an `anyOf`.
* An array type declared in `$defs` checks its own `minItems` and `maxItems`.
* A property whose `type` admits `null` but whose `enum` does not list it rejects a `null`.
* The keywords of the values an array or a map holds are checked, at every depth: `minLength`, `maxLength`,
  `pattern`, `minimum`, `maximum`, `multipleOf`, `const`, a nested array's own `minItems` and `maxItems`, and the
  others a property gets. Upstream checked them on a property only.
* `uniqueItems`, which compares items as JSON, so `1` and `1.0` are equal, as are objects with the same members.
* A `const` on a schema that declares no type, whose value is an `interface{}`.
* A struct merged from an `anyOf` checks the keywords of the object the `anyOf` is declared on: the properties it
  requires, or every branch requires, and those of its own properties. Upstream checked only the branches.

### Other differences

* A required property named `-` is decoded. Upstream tagged it `json:"-"`, which `encoding/json` skips.
* A required array or object whose type excludes `null` is written to JSON as `[]` or `{}` when it is nil. Upstream
  wrote `null`, which the schema rejects.
* A property with a `const` of a string, a number, an integer or a boolean has an exported constant, named after the
  struct and the field, such as `ConfigSchema` for a `$schema` property.
* An `integer` written with a fraction or an exponent, such as `2.0` or `1e2`, is decoded from JSON, as JSON Schema
  counts it as an integer. Upstream's code rejected it, as `encoding/json` does for a Go integer.
* A nested array is held to its own `minItems` and `maxItems`. Upstream held each level to the property's, which
  rejected valid input.
* A property name that a struct tag can't carry for `encoding/json`, such as `a"b`, stops generation with an error.
  Upstream generated a tag that decodes nothing.
* A `null` where a schema belongs, such as an entry of `allOf`, a property or a definition, stops generation with an
  error. Upstream crashed.
* A `default` is written as a literal of its field's Go type, or left out with a warning when no value of that type
  can hold it. Upstream's code for such a default did not compile.
* A `pattern` that Go's regexp cannot compile, such as a lookahead, is left unchecked with a warning. Upstream's
  check rejected every value.
* A `oneOf` branch whose `$ref` cannot be resolved is reported, as one in an `anyOf` or `allOf` is. Upstream dropped
  it without a word.
* Declarations come out in a fixed order. In upstream's output, an unrelated change could reorder them.
* A field or type whose schema sets `deprecated` has a `Deprecated:` paragraph in its doc comment, as described under
  [Choices JSON Schema leaves open](#choices-json-schema-leaves-open). Upstream ignores the keyword.
* The [fidelity warnings](#fidelity-warnings) above.
* `--version` prints the release.

## Status

While not finished, go-jsonschema can be used today. Aside from some minor features,
only specific validations remain to be fully implemented.

### Conformance

`TestConformanceDraft7` runs the required draft-07 tests of the
[JSON-Schema-Test-Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite) through generated code. It
decodes each test's document into the type generated from the test's schema, as JSON and as YAML, and compares the
verdict with the suite's. The report, [`tests/testdata/conformance/draft7.txt`](tests/testdata/conformance/draft7.txt),
lists every disagreement:

* a **missing check** accepts a document the suite marks invalid. It is **silent** when the generator gave no warning
  for the schema;
* a **wrong rejection** rejects a document the suite marks valid;
* a schema the generator rejects, or whose code doesn't compile, leaves its tests not run.

The suite tests one keyword at a time, mostly on schemas without a `type`. Real schemas rarely put such keywords on a
schema without a `type`: 28 of the 1,482 schemas in SchemaStore's catalog do. The disagreements fall into these
classes:

* **Keywords on a schema without a `type`.** Such a schema becomes `interface{}`, or no type at all, and nothing
  checks its keywords. This class holds most of the silent missing checks.
* **Objects without `type: object`.** A schema with `properties` but no `type` becomes a struct, so it rejects the
  non-objects that the suite lets through.
* **YAML.** yaml.v3 reads some documents differently from JSON. It decodes a number or a boolean into a string, and
  it can't read an escaped surrogate pair.
* **`$ref` forms the generator doesn't resolve.** These are a base URI that an `$id` sets, a location-independent
  identifier (`#foo`), a URN, `#` itself, and a pointer into `properties`.
* **Property names that a Go struct tag can't hold.** The generator rejects these schemas.
* **Defects,** such as:
  * a two-type union at the root that keeps only its last type;
  * counts written `2.0`, which fail to load;
  * an integer `multipleOf` below 1, whose code doesn't compile.

The suite is vendored in `tests/testdata/JSON-Schema-Test-Suite`. To update it, run
`scripts/update-test-suite.sh <commit>`, then regenerate the report with `OVERWRITE_EXPECTED_GO_FILE=true`.

`TestDifferentialFixtures` does the same for the fixtures under `tests/data`, which combine keywords as real schemas
do. It builds documents from each fixture and from each of its top-level definitions, then mutates them one node at
a time. It compares the generated code's verdicts with those of
[santhosh-tekuri/jsonschema](https://github.com/santhosh-tekuri/jsonschema), which passes the suite's required tests.
That validator reads a schema that declares draft-04 or draft-06 as draft-07, as the generator does. The report,
[`tests/testdata/differential/fixtures.txt`](tests/testdata/differential/fixtures.txt), groups the disagreements by
schema, keyword and kind of change.

### Validation

* Core ([RFC draft](http://json-schema.org/latest/json-schema-core.html))
  * [x] Data model (§4.2.1)
    * [x] `null`
    * [x] `boolean`
    * [x] `object`
    * [x] `array`
    * [x] `number`
      * [x] Option to use `json.Number` (`--use-number`, for untyped values)
    * [x] `string`
  * [ ] Location identifiers (§8.2.3)
    * [x] References against top-level names: `#/$defs/someName`
    * [x] References against nested names: `#/$defs/someName/$defs/someOtherName`
    * [x] References against top-level names in external files: `myschema.json#/$defs/someName`
    * [x] References against nested names: `myschema.json#/$defs/someName/$defs/someOtherName`
  * [x] Comments (§9)
* Validation ([RFC draft](http://json-schema.org/latest/json-schema-validation.html))
  * [ ] Schema annotations (§10)
    * [x] `description`
    * [x] `default` (for struct fields, and fields that hold a union wrapper)
    * [x] `readOnly` (present in the input: rejected, unless `--disable-readonly-validation` is set)
    * [ ] `writeOnly`
    * [ ] ~~`title`~~ (N/A)
    * [ ] ~~`examples`~~ (N/A)
  * [ ] General validation (§6.1)
    * [x] `enum`
    * [x] `type` (single)
    * [x] `type` (multiple; **note**: partial support, limited validation)
    * [x] `const`
  * [X] Numeric validation (§6.2)
    * [X] `multipleOf`
    * [X] `maximum`
    * [X] `exclusiveMaximum`
    * [X] `minimum`
    * [X] `exclusiveMinimum`
  * [X] String validation (§6.3)
    * [X] `maxLength`
    * [X] `minLength`
    * [X] `pattern`
  * [ ] Array validation (§6.4)
    * [X] `items` (its keywords are checked on every item, at every depth, as are those of a map's values; the
      draft-07 tuple form gives `[]interface{}`)
    * [x] `maxItems`
    * [x] `minItems`
    * [x] `uniqueItems`
    * [ ] `additionalItems` (only `false` after tuple-form `items` is checked, as a length limit)
    * [ ] `contains`, `minContains` and `maxContains`
    * [ ] `unevaluatedItems`
  * [ ] Object validation (§6.5)
    * [x] `required`
    * [x] `properties`
    * [x] `additionalProperties: false` (opt-in via `Config.StrictAdditionalProperties`)
    * [ ] `patternProperties`
    * [ ] `dependencies` (both forms are parsed, but not enforced), `dependentRequired` and `dependentSchemas`
    * [ ] `propertyNames`
    * [ ] `maxProperties`
    * [ ] `minProperties`
    * [ ] `unevaluatedProperties`
  * [x] Conditional subschemas (§6.6) — supported as `allOf` of `if[const|enum]/then[/else]` tagged-union dispatch on a single string discriminator property, which may be declared through a `$ref` and may be held by nested objects, such as `meta.status`; an `if`/`then`/`else` on the object itself counts as one more `allOf` element; the generated `Unmarshal{JSON,YAML}` peeks the discriminator and runs each branch's checks: its `required`, and a `not` of `required` (`not: {required: [a, b]}` forbids both together, `not: {anyOf: [{required: [a]}, {required: [b]}]}` either). Both `{const: "<V>"}` and `{enum: ["<V1>", "<V2>", …]}` forms are recognized; the if clause must contain only the single-property check, which it may also list in its `required` (other `required`/composition/validation keywords decline detection). Where the discriminator, or an object holding it, is absent, an if that requires it is false, so its `else` applies, and one that does not is true, vacuously, so its `then` applies. Other conditionals
    are not compiled, and a [fidelity warning](#fidelity-warnings) reports them
    * [x] `if`
    * [x] `then`
    * [x] `else`
  * [ ] Boolean subschemas (§6.7)
    * [x] `allOf` (merged into one type)
    * [x] `anyOf` (merged into one type that requires a match with at least one branch, and checks the keywords of
      the object it is declared on, except on a property a branch declares as well; a mix of object and non-object
      branches is `interface{}`; branches that only list `required` need at least one of their property sets present)
    * [x] `oneOf` (primitive variants — `string`, `number`, `boolean`, `null` — become a wrapper; object variants
      become a holder that dispatches on a `const` discriminator or tries each variant; branches that only list
      `required` need exactly one of their property sets present; **note**: `integer`-typed variants are not
      currently routed through the wrapper)
    * [ ] `not` (checked as a `not` of `required` in a conditional's `then` or `else`; any other is reported by a
      [fidelity warning](#fidelity-warnings))
  * [x] Multi-type unions (§4.2.1) — `{"type": ["string", "number", "boolean", "null"]}` is wire-equivalent to a primitive `oneOf` and routes through the same wrapper. Same primitive set as `oneOf`; same `integer` exclusion. The `["X", "null"]` shape continues to use `*X` for ergonomics; non-primitive types in the union (`object`, `array`) and per-value validation keywords on the union (`format`, `minimum`, etc.) decline detection and fall back to `interface{}`.
  * [ ] Semantic formats (§7.3)
    * [x] Dates and times
    * [x] Email addresses (opt-in via `Config.FormatValidation`)
    * [x] Hostnames (opt-in via `Config.FormatValidation`)
    * [x] IP addresses (`ipv4` and `ipv6`, typed as `net/netip.Addr`)
    * [x] Resource identifiers — `uri`, `uri-reference` (opt-in via `Config.FormatValidation`)
    * [ ] URI-template
    * [ ] JSON pointers
    * [x] Regex (opt-in via `Config.FormatValidation`)
    * [x] `uuid` (defined from draft 2019-09, checked in every draft; opt-in via `Config.FormatValidation`)

### Opt-in `format` validation

Setting `Config.FormatValidation.Enabled = true` emits a runtime check on
every `format` keyword listed below, including strings held in arrays and
maps (nested ones too); the error then names the element, as in `ids[1]` or
`byName["a"]`. The validator runs after the typed struct decode, so the
field's Go type is already its target form. Optional pointer fields are
skipped when nil, so absent values do not trigger validation.

| `format` | Strategy | Notes |
| --- | --- | --- |
| `uuid` | RE2 regex against the canonical 8-4-4-4-12 hex form (case-insensitive) | Defined by JSON Schema from draft 2019-09; checked in every draft. |
| `email` | `net/mail.ParseAddress` + reject if a display-name is present + require the parsed address to round-trip the input verbatim | Stricter than the Go stdlib default: only RFC 5321 `addr-spec` (`local@domain`) is accepted. Forms like `Alice <alice@example.com>` or `<bob@example.com>` are rejected. |
| `uri` | `net/url.Parse` + `IsAbs()` + RFC 3986 character-class regex | Rejects whitespace, control characters, and malformed percent-encoding (`%` not followed by two hex digits). Empty strings are rejected (not absolute). |
| `uri-reference` | `net/url.Parse` + RFC 3986 character-class regex | Same character-class check as `uri`. Empty strings, fragment-only refs (`#x`), and relative paths (`/a/b`) are accepted per RFC 3986. |
| `hostname` | RE2 regex (RFC 1123 labels) | |
| `regex` | `regexp.Compile` (Go's RE2 syntax) | |

The validation is opt-in to preserve existing behavior. To restrict
validation to a subset of keywords, set `Config.FormatValidation.AllowList`:

```go
cfg.FormatValidation = generator.FormatValidationConfig{
    Enabled:   true,
    AllowList: []string{"uuid", "email"}, // nil = all known formats
}
```

From the CLI, use `--validate-formats`:

```shell
# Validate every supported format keyword.
go-jsonschema --validate-formats=all -p main schema.json

# Validate only specific formats.
go-jsonschema --validate-formats=uuid,email -p main schema.json

# Explicit off (same as omitting the flag).
go-jsonschema --validate-formats=off -p main schema.json
```

Unknown format names are rejected at flag-parse time so a typo like
`uuid,emial` fails immediately rather than silently disabling email
validation.

The `uri` / `uri-reference` validation is **best-effort, not RFC-3986-perfect** —
no Go validator (stdlib or third-party) is — but it catches the common
cases that bare `url.Parse` accepts (whitespace, control chars, malformed
pct-encoding).

### Opt-in `additionalProperties: false` enforcement

`Config.StrictAdditionalProperties` controls whether unknown fields in JSON
or YAML input are rejected at unmarshal time. The three modes are:

| Mode | Behavior |
| --- | --- |
| `""` (off, default) | Silently drop unknown fields. Preserves historical behavior. |
| `"respect-schema"` | Reject unknown fields only for objects whose schema declares `additionalProperties: false`. Other objects continue to drop unknown fields silently. |
| `"strict"` | Reject unknown fields for every generated object type, regardless of what the schema declared. Skipped when the schema specifies a typed `additionalProperties` (a catch-all map field is generated instead). |

Enforcement runs against the raw decoded map, so JSON and YAML inputs are
checked uniformly. `patternProperties` schemas suppress enforcement with a
warning, since the generator has no first-class support for them yet.

```go
cfg.StrictAdditionalProperties = generator.StrictAdditionalPropertiesRespectSchema
```

From the CLI, use `--strict-additional-properties`:

```shell
# Respect the schema's additionalProperties: false declarations.
go-jsonschema --strict-additional-properties=respect-schema -p main schema.json

# Reject unknown fields for every object type.
go-jsonschema --strict-additional-properties=strict -p main schema.json

# Explicit off (same as omitting the flag).
go-jsonschema --strict-additional-properties=off -p main schema.json
```

Unknown mode names (e.g. `--strict-additional-properties=rstrict`) are
rejected at flag-parse time so a typo cannot silently fall back to "off".

## License

MIT license. See `LICENSE` file.
