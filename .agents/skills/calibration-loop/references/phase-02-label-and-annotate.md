---
name: calibration-loop-label-and-annotate
parent: calibration-loop
description: "Emit blind label units and record model candidate labels."
---

# Phase 2: label and candidate annotation

**Carry forward from prior phases**: calibration root and sealed manifest path.

**Inspect**:

    flow-indicator label --manifest /calibration-root/corpus/manifest.json --family /selected/family --split dev

| Status | Action |
|---|---|
| Blind units and candidate files are absent | Continue. |
| Candidate files exist and human labels are absent | Preserve candidates; continue to Phase 3. |
| Human label files exist | Return to the router. |
| Manifest changed or cannot load | Stop; repair corpus provenance before emitting units. |

**Decide**:

Ask for family and sampling rate. Ask whether the operator selects an
OpenAI-compatible candidate endpoint. Candidate annotation is optional.

**Generate**:

    flow-indicator label --manifest /calibration-root/corpus/manifest.json --family /selected/family --split dev --sample-per-mille /selected/rate > /calibration-root/units/dev-/selected/family.jsonl
    flow-indicator annotate --manifest /calibration-root/corpus/manifest.json --out /calibration-root/candidates --family /selected/family --split dev --endpoint /selected/endpoint --model /selected/model

The second command runs only on the operator-selected endpoint. Its two passes
write candidate files under candidates; model output is never ground truth.

## Validate

    flow-indicator label --manifest /calibration-root/corpus/manifest.json --family /selected/family --split dev > /tmp/calibration-units.jsonl

## PR Checkpoint

**Title**: [calibration] Emit units and candidate labels

**Files to include**:

- /calibration-root/units/dev-/selected/family.jsonl
- /calibration-root/candidates/dev-/selected/family-*.jsonl

**Next phase**: @phase-03-adjudicate.md
