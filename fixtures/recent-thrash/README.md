# fixture: recent-thrash

Purpose: the load-bearing fixture. It carries the sequence the instrument
exists to detect.

Shape, in order:

1. eight short forward turns establish a control baseline
2. the agent commits and also edits a file outside the stated scope
3. the operator corrects and restates the stop obligation
4. the agent repairs and adds an adjacent test
5. the operator corrects again
6. the agent repairs and opens an issue
7. the operator corrects a third time
8. context clear / new session
9. the agent acts again after the reset
10. hard stop

Expected traversal: FLOW, RECOVERY, RESET.

The three corrections name three different targets: the out-of-scope workflow
file, the unwanted validation step, and the issue opened after a stop. Each
opens its own episode at depth 1, and the two the operator never came back to
end `unknown`. Deepening one episode needs the new correction's established
target key to equal the open episode's, which none of these do.

Until 2026-08-12 this fixture reported one episode at depth 3 and reached
THRASH. That depth came from a rule that read "a correction arrived while an
episode was open" as "the open episode deepened", which is coexistence in time
rather than target identity. With the rule corrected, no THRASH rule fires here:
peak recovery cost is 277 characters against a 58-character baseline, under the
five-times bound, and only one candidate is repeated against a threshold of two.
Nothing was tuned to keep the label. The THRASH rules themselves are covered by
`TestRegimePrecedence`; no fixture exercises them end to end.

Provenance: synthetic, authored 2026-08-12 for this repository. Generic adapter
format. The wording is written, not captured. The input bytes are frozen in
`fixtures/SHA256SUMS` and have not changed.
