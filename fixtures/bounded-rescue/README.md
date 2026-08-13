# fixture: bounded-rescue

Purpose: one correction that ends. The agent edits a file outside the stated
scope, the operator corrects once, the agent reverts and adds nothing, the
operator accepts, and forward work resumes for twelve more turns.

Load-bearing property: repair depth stays at 1, the episode reaches durable
close, and THRASH is never entered. A stream with a correction in it is not by
itself a degraded stream.

Provenance: synthetic, authored 2026-08-12 for this repository. Generic adapter
format.
