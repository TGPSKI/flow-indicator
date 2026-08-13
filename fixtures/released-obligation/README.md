# fixture: released-obligation

Purpose: a stream where the operator withdraws a requirement and later states it
again. Nothing goes wrong in it: no correction, no repair episode, one epoch.
The obligation surface is the whole subject.

Load-bearing property: the unresolved inventory falls at least once. Turn 4
withdraws the scope rule by restating it under a release marker, and turn 6
states it again. The candidate leaves the inventory and comes back under the
same identity, so `obligation_release_events` and `obligation_revival_events`
are both 1 while `obligation_repeat_events` stays 0 — a re-assertion after a
withdrawal is not a restatement of something never withdrawn.

This is the property no other fixture holds. Before resolution existed the
inventory only ever grew, and the count stopped carrying information after the
first few turns.

Provenance: synthetic, authored 2026-08-13 for this repository. Generic adapter
format. Not derived from a captured session. The three naturalistic sessions in
the corpus contain no operator release language at all, which is why this
fixture is synthetic rather than a capture.
