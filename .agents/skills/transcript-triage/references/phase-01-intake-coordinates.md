---
name: transcript-triage-intake
parent: transcript-triage
description: "Capture evidence and verify transcript coordinates before code investigation."
---

# Phase 1: intake and coordinates

**Carry forward from prior phases**: symptom, session identifier, and incident artifact path.

**Inspect**:

    flow-indicator inspect --session /selected/session --turn /reported/turn
    flow-indicator report --session /selected/session

Read source.json, observed.jsonl, classified.jsonl, transitions.jsonl,
derived.jsonl, configuration, profile overlay, and semantic completion
provenance. Label each fact DEFINITIVE, CONFIG, SOCIAL, or ANECDOTAL and
STATED, INFERRED, or MISSING.

| Status | Action |
|---|---|
| Reporter command, path, or coordinate is missing | Record MISSING and request it; do not enter Phase 2. |
| Session, harness, stream, source sequence or offset differs | Record COORDINATE MISMATCH and route to Phase 3. |
| Projection is compared directly with an event | Record COORDINATE MISMATCH; compare the projection's source events. |
| Profile/rule hash differs | Record COORDINATE MISMATCH and the two hashes. |
| Classifier identity or semantic replay provenance differs | Record COORDINATE MISMATCH and the two identities. |
| All coordinates match | Record COORDINATES VERIFIED and route to Phase 2. |

**Decide**:

Ask only for missing reporter methodology, source path, and exact observed
output. State inferred facts with the cheapest check that could falsify each.

**Generate**:

Write intake, evidence, and coordinate tables to the incident artifact. A
mismatch closes code-level investigation; it does not rewrite source or events.

## Artifact Checkpoint

**File**: incidents/YYYY-MM-DD-incident-slug.md

**Sections completed**:

- Evidence-labelled facts
- Reporter and instrument coordinate comparison
- COORDINATE MISMATCH or COORDINATES VERIFIED

## PR Checkpoint

**Title**: [triage] Record transcript coordinates

**Files to include**:

- incidents/YYYY-MM-DD-incident-slug.md
