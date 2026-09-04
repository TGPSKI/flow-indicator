---
name: calibration-loop-adjudicate
parent: calibration-loop
description: "Create human-adjudicated labels without promoting model candidates."
---

# Phase 3: adjudication

**Carry forward from prior phases**: manifest, family, blind units, and optional
candidate-label paths.

**Inspect**:

Read docs/CALIBRATION.md classes for the selected family. Compare candidates
only as a review aid after the human adjudicator records each unit.

| Status | Action |
|---|---|
| Candidate passes agree | Record the agreement as codebook evidence; require human adjudication. |
| Candidate passes disagree | Send the unit to human adjudication; do not average a class. |
| Candidate files are absent | Adjudicate blind units directly. |
| Human label file is complete | Continue to Phase 4. |

**Decide**:

Ask for the human adjudicator and output label directory. Require the
adjudicator to record labels without the classifier verdict.

**Generate**:

Write human-adjudicated labels to /calibration-root/labels/dev-/selected/family.jsonl.
Keep model name, pass, and candidate provenance separate from human ground
truth. Preserve unlabelable only where the evidence cannot establish a class.

## Validate

    flow-indicator calibrate --labels /calibration-root/labels --manifest /calibration-root/corpus/manifest.json --split dev --json

## PR Checkpoint

**Title**: [calibration] Adjudicate labels

**Files to include**:

- /calibration-root/labels/dev-/selected/family.jsonl

**Next phase**: @phase-04-calibrate.md
