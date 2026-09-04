---
name: transcript-triage
description: "Route transcript, metric, event, replay, and live-view reports through coordinate-first triage."
metadata:
  author: TGPSKI
  version: "1.0"
compatibility: "flow-indicator session artifacts and local transcript sources"
---

# Transcript triage

Investigate an observed output discrepancy without asserting hidden model state.
Store the portable incident artifact at incidents/YYYY-MM-DD-incident-slug.md.

## Prerequisites

- Preserve the session directory and source transcript; event JSONL files are
  append-only evidence.
- Read docs/EVENTS.md, docs/COMMANDS.md, docs/INFERENCE.md, and docs/PROFILES.md.

## Entry point

Ask for the symptom, session identifier, and artifact path. Read the artifact,
not local changes or chat history, to detect progress.

## Progress detection

| Artifact state | Route |
|---|---|
| Missing | @references/phase-01-intake-coordinates.md |
| Intake exists, coordinate verdict missing | @references/phase-01-intake-coordinates.md |
| COORDINATE MISMATCH | @references/phase-03-remediate-artifact.md |
| COORDINATES VERIFIED and hypotheses missing | @references/phase-02-investigate.md |
| Hypotheses or findings present, actions open | @references/phase-02-investigate.md |
| Findings and prioritized actions present | @references/phase-03-remediate-artifact.md |

## Coordinate map

| Coordinate | Instrument evidence | Common mismatch |
|---|---|---|
| Session and harness | source.json and inspect | A handle from another harness or session. |
| Stream ID and projection | session directory, summary.json, metrics.json, report.md, timeline.csv | A rendered projection compared with an event or another stream. |
| Source sequence and offset | source.json, inspect output, source transcript | A turn number or byte offset from another source version. |
| Profile and rule hash | classified events, configuration, profile overlay | Baseline compared with an overlay or another heuristic table. |
| Classifier identity | classified and semantic completion events | Endpoint, model, or prompt identity changed. |
| Semantic replay | replay --semantic-session and retained completions | Live marker projection compared with a strict retained semantic replay. |

## Route

| Phase | Module | Artifact outcome |
|---|---|---|
| 1 | @references/phase-01-intake-coordinates.md | Evidence-labelled intake and coordinate verdict. |
| 2 | @references/phase-02-investigate.md | Source-order timeline and discriminated findings. |
| 3 | @references/phase-03-remediate-artifact.md | Preserved evidence, prioritized actions, portable report. |
