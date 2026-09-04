---
name: calibration-loop
description: "Route corpus sealing, candidate annotation, adjudication, and calibration through merged artifacts."
metadata:
  author: TGPSKI
  version: "1.0"
compatibility: "flow-indicator and local corpus, label, and run-record paths"
---

# Calibration loop

## Prerequisites

- Sync the upstream primary branch. Local changes, unmerged branches, and chat
  history do not advance this workflow.
- Read docs/CALIBRATION.md and docs/COMMANDS.md before selecting a phase.
- Keep corpus and label artifacts private; they can contain transcript text.

## Entry point

Ask for one calibration root directory. Inspect its artifacts on the merged
upstream primary branch.

## Progress detection

| Merged artifact status | Progress |
|---|---|
| No corpus/manifest.json | Phase 1: corpus preparation. |
| corpus/manifest.json, no candidates/dev-*-*.jsonl | Phase 2: label and candidate annotation. |
| Candidate files, no labels/dev-*.jsonl | Phase 3: adjudication. |
| labels/dev-*.jsonl, no runs/dev-*.json | Phase 4: dev calibration and holdout review. |
| runs/dev-*.json present | Phase 4: inspect whether a reason supports a holdout opening. |

## Determine phase

| Status | Action |
|---|---|
| Artifact exists only in the working tree or an unmerged branch | Ignore it for routing; start from merged artifacts. |
| More than one incomplete phase has artifacts | Route to the earliest incomplete phase. |
| Artifact hash or manifest validation fails | Stop; preserve the artifact and repair its provenance before continuing. |
| Phase output is merged and complete | Route to the next phase. |

## Design principles

1. Ownership: the operator selects corpus, endpoint, adjudicator, classifier,
   and any holdout reason; the workflow records those choices.
2. Source of truth: the sealed manifest, human label files, and calibration run
   records decide progress and provenance.
3. Ask less, infer more: derive paths, splits, hashes, and prior outputs from
   merged artifacts before asking.
4. Prefer simple: use dev, the heuristic classifier, and human labels until a
   documented reason selects more.

## Route to phase

| Phase | Module | Outcome |
|---|---|---|
| 1 | @references/phase-01-corpus.md | A sealed manifest. |
| 2 | @references/phase-02-label-and-annotate.md | Blind units and model candidates. |
| 3 | @references/phase-03-adjudicate.md | Human ground-truth labels. |
| 4 | @references/phase-04-calibrate.md | Provenanced dev run and holdout decision. |
