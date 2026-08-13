# fixture: large-data-dump

Purpose: a stream where two operator turns are very long and both are
legitimate. Turn 5 pastes a full test log, turn 7 pastes a config file. Neither
is a correction, neither repeats an obligation, and no repair episode opens.

Load-bearing property: character count alone must not move the regime. This
fixture fails if length by itself produces DRIFT or THRASH.

Provenance: synthetic, authored 2026-08-12 for this repository. Generic adapter
format. The pasted log and config are written for the fixture.
