# AGENTS.md

Guidance for coding agents (Claude Code, Codex, Cursor and others) working in this repository. `CLAUDE.md`
imports this file.

## Project

`go-jsonschema` is a CLI that generates Go types and unmarshallers from JSON Schema definitions. The CLI entry
point is `main.go`; all generation logic lives in `pkg/`.

This repository is a fork. Read [Working in this fork](#working-in-this-fork) before pushing anything.

## Common commands

Tests, lint, format, and build are all wrapped in scripts under `scripts/` and surfaced through the `Makefile`.
Run them from the repo root.

```shell
# Run the full test suite (race + coverage). Uses the go workspace described below.
make test

# Regenerate every golden file when generator output legitimately changes.
OVERWRITE_EXPECTED_GO_FILE=true make test

# Run a single top-level test (each Test* in tests/ usually walks a sub-tree of tests/data).
go test ./tests -run TestCore -v
go test ./tests -run TestFormatValidation/uuid -v   # subtests are named after the schema's path

# Lint and format Go (golangci-lint v2 + gofmt/gofumpt/goimports/gci).
make lint-go
make format-go

# Build a release snapshot via goreleaser.
make build
```

Tooling versions are pinned in `mise.toml`; read the file for current versions. CI uses the same pins, so a
local/CI mismatch (e.g. an older `golangci-lint` from `~/go/bin/` shadowing the mise-managed one) produces
green-locally / red-on-CI failures. Install dev tools with `make tools-go`. The lint, format, test and
dependency-upgrade scripts also have `*-docker` Make targets that run them in their pinned Docker image
(e.g. `make lint-go-docker`).

## Go workspace layout (intentional)

The repo uses a Go workspace (`go.work` is generated locally from `go.work.dist`) so that the `tests/` module can
both drive code generation **and** compile the resulting Go code in the same `go test` run. This is unusual but
deliberate: it validates the generated code, not only the generator output. The two modules are `.` (the generator
and library) and `./tests` (golden-file-driven tests).

When adding a new test fixture, the generated `.go` file becomes part of the `tests` module and must compile under
it.

## Architecture

The generator pipeline has three layers; understanding the boundary between them is key to making non-trivial
changes.

1. **`pkg/schemas`**: JSON Schema model and loader. Parses raw schema JSON/YAML into a `schemas.Schema` tree
   (`Schema`, `ObjectAsType`, `Type`, `Definitions`). `loaders.go` resolves `$ref`s across files; `reference.go`
   handles fragment paths. The model is intentionally close to the spec; don't mix codegen concerns in here.

2. **`pkg/codegen`**: Go-AST-like IR (`File`, `Package`, `Decl`, `TypeDecl`, `StructType`, `Import`, …) plus an
   `Emitter` that writes formatted Go source. Everything emitted to disk goes through `Decl.Generate(*Emitter)`.
   This layer knows nothing about JSON Schema.

3. **`pkg/generator`**: the bridge. The interesting files:
   - `generate.go`: `Generator` orchestrates per-file work, owns `outputs` (one `*output` per generated `.go`
     file) and the schema `Loader`.
   - `schema_generator.go`: `schemaGenerator` walks one schema, producing `codegen` decls. Recursion is guarded
     by `inScope` to handle cyclic refs; type names come from `nameScope` (`name_scope.go`) and are made unique by
     `output.uniqueTypeName`. `ref_path.go` resolves `$ref` JSON pointers into nested subschemas.
   - `validator.go`: runtime-validation strategies (`requiredValidator`, `stringValidator`, `numericValidator`,
     `arrayValidator`, `formatValidator`, `anyOfValidator`, …). Each implements `validator.generate`, which emits
     Go statements into the `Unmarshal*` body.
   - `oneof_primitive.go`, `oneof_discriminator.go`, `oneof_tryeach.go`: `oneOf` compiled into typed wrappers and
     holders that own their whole decode flow. `conditional_discriminator.go` does the same for the
     `allOf` + `if`/`then[/else]` tagged-union pattern.
   - `fidelity.go`: warnings when a composition falls back to `interface{}` and drops keywords.
   - `enum_varnames.go`, `extension_tags.go`: `x-enum-varnames` constant names and `x-` extensions as struct tags.
   - `unmarshal_body.go`: the default Unmarshal body (decode raw map → decode into `Plain` → run validators →
     assign).
   - `formatter.go`, `json_formatter.go`, `yaml_formatter.go`: per-type unmarshal/marshal methods. The YAML
     formatter is only added when `Config.ExtraImports` is true.
   - `config.go`: the public `Config` struct. New user-facing knobs go here and are wired up in `main.go`.

Output flow: `generator.New(cfg)` → `g.DoFile(path)` per schema → `g.Sources()` returns `map[filename][]byte` of
formatted Go (`go/format` is applied at the end of `Sources()`).

### Generator modes worth knowing

These are `Config` fields, each with its own test data under `tests/data/`:

- `FormatValidation` (`--validate-formats`): opt-in runtime checks of `format` keywords, all known formats or an
  allow list. Off by default to preserve historical behavior.
- `StrictAdditionalProperties` (`--strict-additional-properties`): whether `additionalProperties: false` becomes a
  runtime rejection. Off (default, unknown fields dropped silently), `respect-schema`, or `strict`.
- `ValidateNullTypes` (`--validate-null-types`): reject an explicit `null` where the schema's `type` excludes it.
- `CollisionStrategy` (`--collision-strategy`): `positional` (default) or `qualify`, which names each definition
  after its schema so type names don't depend on argument order.
- `ExtensionTags` (`--extension-tag x-foo=tag`): emit schema `x-` extensions as struct tags.
- `SchemaMappings` (`--schema-package URI=PACKAGE[:ALIAS]`): a schema's types go to that Go package, and `$ref`s
  into it import the package, under the alias if given. `--known-schema URL=PATH` loads a `$ref`'d URL from a local
  file instead of fetching it.
- `AliasSingleAllOfAnyOfRefs`: collapses single-ref `allOf`/`anyOf` into a Go type alias.
- `MinSizedInts` (`--min-sized-ints`): sizes int/uint from the schema's min/max.
- `MinimalNames` (`--minimal-names`): the shortest unique identifier, walking up the `nameScope`.
- `OnlyModels` (`--only-models`): structs only, no unmarshal/validation.
- `DisableOmitEmpty` / `DisableOmitZero`: struct tag generation (omitzero requires Go 1.24+).

## Coding patterns

These conventions aren't all enforced by the linter, but breaking them either snaps golden tests, leaks layers, or
quietly produces non-deterministic output.

- **Emit code via `codegen.Emitter`, not templates or `go/ast`.** All generated Go is built up with
  `Emitter.Printlnf` and `Indent(±1)`. `go/format` runs once at the end of `Generator.Sources()`. Don't reach for
  `text/template` when adding a new validator or formatter.

- **Validator authoring contract.** A new validator type must:
  - Implement `validator.generate(out *Emitter, format string) error` and `validator.desc() *validatorDesc`.
  - Be added to the `var (_ validator = new(...))` interface-assertion list at the top of `validator.go`;
    nothing else catches a missing entry.
  - Declare needed imports in `desc().imports` and package-level declarations (such as precompiled regex vars) in
    `desc().decls`, not via inline `AddImport`/`AddDecl` calls inside `generate()`. The orchestrator merges them
    and adds `fmt` automatically when `desc().hasError` is true.
  - Use `desc()` flags to declare *where* in the Unmarshal body it runs: `beforeJSONUnmarshal` runs against the
    raw map, the default position runs against the typed `Plain` struct, `requiresRawAfter` forces the raw map to
    be decoded even when no before-validator needs it, and `afterAdditionalProperties` runs once the
    `AdditionalProperties` map has been decoded.

- **Don't fork JSON/YAML body logic.** `unmarshal_body.go` is shared between formats via `unmarshalContext`;
  `json_formatter.go` and `yaml_formatter.go` are thin shells that pass a `decodeCall` closure. Behavior added to
  the shared body benefits both formats; behavior duplicated per formatter rots.

- **Layer separation is load-bearing.** `pkg/schemas` is JSON-Schema-only, with no `codegen` or `generator`
  imports. `pkg/codegen` is Go-IR-only, with no `schemas`-keyword logic and no decisions about what to emit.
  `pkg/generator` is the only place the two meet. A new feature usually lands as: keyword in `schemas/types.go` →
  decision in `generator/...` → optional new IR in `codegen/`.

- **Identifier and name handling go through helpers.**
  - Any Go identifier built from a JSON name: `internal/x/text.Caser.Identifierize` (handles Go keywords, leading
    non-letters, `--capitalization` overrides, case/separator splitting). Don't `strings.Title` or hand-roll.
  - Any new type name: build a `nameScope` (`name_scope.go`) and resolve via `output.uniqueTypeName`, so collision
    handling and `--minimal-names` shortening work. Don't `fmt.Sprintf("%s_%d", ...)` directly.

- **Sort before iterating maps that influence output.** Use `sortedKeys`, `sortDefinitionsByName`, `slices.Sort`,
  etc. before any `range` over a map whose order affects emitted code, declarations, or imports. `Package.Generate`
  already sorts decls and imports; new emission paths must keep output stable too.

- **Cycle-safe ref following.** `generateReferencedType` and `generateAnyOfType` wrap recursion in
  `detectCycle(t)` with a deferred cleanup; on a cycle, the result is wrapped via `codegen.WrapTypeInPointer`. New
  ref-following code must do the same; cycles in user schemas are routine.

- **Default values are rendered with `litter.Sdump`.** JSON numbers come out of `encoding/json` as `float64`, so
  integer defaults need an explicit integer cast before dumping (see `defaultValidator.dumpDefaultValueAssignment`).
  Pointer-to-integer defaults need a typed temp via `assignDefault` rather than `&literal`.

- **Pointer policy for struct fields.** In `generateStructFieldType`: required ⇒ value; optional + no default ⇒
  pointer; optional + default ⇒ value (so the default actually applies). Already-nillable types are never
  re-wrapped. The `goJSONSchema.pointer` extension overrides per field in either direction.

- **Sentinel constants for symbols shared with generated code.** Names that have to match between validators and
  the Unmarshal body live as constants: `varNameRawMap` (`raw`), `varNamePlainStruct` (`plain`), `typePlain`
  (`Plain`), `additionalProperties`, `interfaceTypeName`, `formatJSON`, `formatKeyword*`. Reuse them; don't
  hard-code the string.

- **Sentinel errors at the file top, wrapped at use.** Each file declares its errors as package-level
  `var Err... = errors.New(...)` (or unexported `err...`) and callers wrap with `%w`. golangci-lint's `err113`
  rejects ad-hoc `fmt.Errorf("...")` outside tests.

- **Helpers for stdlib/third-party live under `internal/x/<package-path>/`.** E.g. text helpers live in
  `internal/x/text`, mirroring the upstream import path. Documented in
  `docs/arch/0003-handling-libraries-extensions.md`.

## Test conventions

- Each `Test*` function in `tests/` configures a `generator.Config` and points `testExamples`/`testExampleFile`
  at a directory or schema in `tests/data/`. The framework walks `*.json` files, runs the generator, and diffs
  against a sibling golden `.go` file.
- A schema named `*.FAIL.json` is expected to make the generator return an error.
- Subtests are named after the schema's path under `tests/data/`, so `-run TestCore/some/path` works.
- Runtime tests (e.g. `tests/validation_test.go`) unmarshal input into the generated types and check the errors.
  A new fixture's package can only be imported once its golden file exists: generate first, then add the test.
- When intentionally changing generator output, use `OVERWRITE_EXPECTED_GO_FILE=true make test` and review the
  diff before committing; the generated files are checked in.
- New options should usually get their own `Test*` and a dedicated `tests/data/<feature>/` tree rather than being
  squeezed into `core` or `misc`.

## Working in this fork

`plheide/go-jsonschema` is a fork of `omissis/go-jsonschema`. [FORK.md](FORK.md) explains what it carries and how
it follows upstream. The rules that matter when changing it:

- **The module path is `github.com/plheide/go-jsonschema`.** Generated-code headers deliberately keep upstream's
  module path, so regenerating doesn't churn existing output; don't change them. The `fork-guard` workflow runs
  `scripts/fork/rewrite-module-path.sh` and fails if it changes anything, i.e. if upstream's module path appears
  outside those headers.
- **`main` changes only through reviewed pull requests**, merged with a merge commit by
  `scripts/fork/merge-layer.sh <pr>`. Never push to `main` directly.
- **`feat/*` branches head upstream pull requests.** Change them only to follow review. Never delete one (that
  closes its upstream pull request), and never merge `main` into one.
- **Never `git push --tags`.** Upstream's release tags would become the fork's newest versions and break
  `go install github.com/plheide/go-jsonschema@latest`. Push the one tag you mean. `scripts/fork/install-hooks.sh`
  installs a pre-push guard against these mistakes.
- **Releases are tagged from `main`.** `vX.Y.Z-rc.N` runs `prerelease.yaml`, `vX.Y.Z` runs `release.yaml`; both
  publish the binaries and the Docker Hub and GHCR images, and only a stable release moves `latest`.
- **To offer a change upstream,** cherry-pick it onto a branch based on `upstream-main` and run
  `scripts/fork/rewrite-module-path.sh --reverse`.
