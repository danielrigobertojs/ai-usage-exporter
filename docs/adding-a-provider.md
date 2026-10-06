## Adding a provider

A provider is a `Descriptor` (where a tool's logs live) plus a `Parse`
implementation (how to turn one of those files into `model.UsageEvent`
values). Nothing else in the exporter needs to change: `Discover` already
knows how to expand any `Descriptor`'s roots, and the registry already knows
how to list and look up any `Provider` by ID.

The `internal/provider/fake` package exists for exactly this: it is the
minimal provider used as the worked example below. Read it alongside these
five steps.

### 1. Declare the descriptor

```go
// internal/provider/fake/fake.go
func (fakeProvider) Descriptor() provider.Descriptor {
	return provider.Descriptor{
		ID:          "fake",                 // kebab-case, this is the `tool` label value forever
		DisplayName: "Fake Provider",
		Kind:        provider.SourceJSONL,
		Roots: []provider.RootSpec{
			{Base: provider.BaseHome, Rel: ".fake", Glob: "*.jsonl"},
		},
		Capabilities: provider.Capabilities{HasTokens: true, HasToolCalls: true},
	}
}
```

`ID` is a public contract once a provider ships: it becomes the `tool`
label on every metric, so changing it later breaks dashboards. `Roots` is a
closed list of known locations - never a filesystem walk - and `Glob` is
matched with `doublestar`, not `filepath.Glob`, so patterns like `*/*.jsonl`
or `**/*.jsonl` both work regardless of host OS. If the real tool can be
relocated by an environment variable (like Codex's `CODEX_HOME`), list it
in `HomeEnv`; `Discover` then uses that value instead of the normal
`Base` resolution for every root in the descriptor.

Call `Descriptor{}.Validate()` in a test before writing anything else: it
rejects an empty or non-kebab-case `ID`, a descriptor with no roots, and a
root with an empty `Glob`.

### 2. Implement `Parse`

```go
// internal/provider/fake/fake.go
func (p fakeProvider) Parse(ctx context.Context, src provider.Source, emit func(model.UsageEvent) error) error {
	for i := 0; i < p.eventsPerSource; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		evt := model.UsageEvent{ /* ... */ }
		if err := emit(evt); err != nil {
			return err
		}
	}
	return nil
}
```

`Parse` reads exactly one `Source` and calls `emit` for each event it finds.
Three rules are non-negotiable, because every provider shares the same
aggregator and the same privacy contract downstream:

- **Stream, don't load.** Use `bufio.Scanner` (with an enlarged buffer if the
  format allows long lines) or an incremental SQLite query - never read the
  whole file into memory. Real agent sessions run from hundreds of MB to a
  few GB.
- **Metadata only.** Never put prompt text, model output, or tool output
  into any field of `model.UsageEvent`. Write a test that injects a sentinel
  string (e.g. `SENTINEL-PROMPT-TEXT`) into every content field of a fixture
  and asserts the sentinel never appears in any emitted event - every
  existing provider test does this and new ones must too.
- **A malformed line is not a fatal error.** Skip it and keep parsing the
  rest of the file; only propagate an error if `emit` itself returns one or
  `ctx` is done.

### 3. Register it

```go
reg := provider.NewRegistry()
if err := reg.Register(fake.New(n)); err != nil {
	// only happens on a duplicate ID - a programming error, not a runtime one
}
```

`Register` errors on a duplicate ID instead of silently overwriting it.
`Registry.All()` returns providers in registration order; `Registry.Get(id)`
looks one up by its `tool` label value.

### 4. Add a fixture

Fixtures live under `internal/providers/<tool>/testdata/` (plural
`providers`, to leave room for `internal/provider`, the shared package).
Hand-write them, anonymized: every content field gets the literal string
`"REDACTED"`, never a real prompt or a real file path. Cover at least: a
normal file, one with the tool's known edge case (Claude Code's
compaction/`/resume` duplicate `uuid`, Codex's cumulative `last_*`
counters), and one with malformed/truncated lines mixed with valid ones.

### 5. Add the parsing test

Table-driven, against the fixture: assert the exact events `Parse` emits
(tokens, model, timestamp, `EventKey`), assert a malformed line doesn't
abort the scan, and assert the sentinel-content test from step 2. Then run:

```sh
go test ./internal/providers/<tool>/... -race -cover
```

Aim for the same bar the rest of the exporter holds: `>= 85%` coverage, and
no test touching the real filesystem or network - `fstest.MapFS` (or a
literal fixture file under `testdata/`) stands in for both.
