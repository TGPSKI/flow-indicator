# Fixtures

Five synthetic streams, replayed by `go test ./cmd/flow-indicator`. Each holds
an `input.jsonl` in the generic adapter's format and an `expect.json` naming the
properties that fixture exists to prove.

## What these are

Implementation regression fixtures. They pin behaviour the implementation
already has, so a change that alters it has to be deliberate.

They are not an independent validation corpus. `recent-thrash` was edited during
the first pass after its metric behaviour had been observed, which is what the
distinction turns on: a fixture written to produce an outcome cannot then be
evidence for that outcome. Independent evidence comes from the naturalistic
sessions, which are recorded interaction nobody shaped.

## Frozen inputs

`SHA256SUMS` holds the hash of every `input.jsonl` as of the second pass.
`TestFixtureInputsAreFrozen` fails if any input changes.

The rule the freeze enforces: when a fixture exposes a result that looks wrong,
fix or question the implementation. Do not reshape the input until the number
comes out differently. A new case is a new fixture.

`expect.json` is not frozen. It records what the implementation is expected to
claim, so a deliberate narrowing of a semantic claim changes it — with the
reason in the pass notes.
