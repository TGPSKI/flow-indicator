---
name: calibration-loop-calibrate
parent: calibration-loop
description: "Score a sealed corpus with provenance and control holdout openings."
---

# Phase 4: calibration and holdout review

**Carry forward from prior phases**: manifest, human labels, family, candidate
provenance, and prior calibration run records.

**Inspect**:

    flow-indicator calibrate --labels /calibration-root/labels --manifest /calibration-root/corpus/manifest.json --split dev --classifier heuristic --json

| Status | Action |
|---|---|
| No dev run record exists | Run the selected dev classifier and save its JSON result. |
| Dev record exists under the same manifest, ruleset, labels, and classifier identity | Compare it with the next run; continue only with a stated change. |
| Rule, profile, label set, manifest, or classifier identity changed | Record the changed provenance; treat scores as a new comparison. |
| Operator supplies a holdout reason | Run holdout once with --reason and save the record. |
| Operator supplies no holdout reason | Keep holdout sealed; do not run a holdout score. |
| A fixture is proposed as a tuning target | Stop; improve a general rule only on independently adjudicated dev evidence. |

**Decide**:

Ask for classifier path, the single change under evaluation, and a holdout
reason. A configured classifier needs endpoint and model provenance; heuristic
needs its rule hash. Do not ask for a holdout reason when dev evidence does not
justify an opening.

**Generate**:

    flow-indicator calibrate --labels /calibration-root/labels --manifest /calibration-root/corpus/manifest.json --split dev --classifier /selected/classifier --json > /calibration-root/runs/dev-/selected/run.json
    flow-indicator calibrate --labels /calibration-root/labels --manifest /calibration-root/corpus/manifest.json --split holdout --classifier /selected/classifier --reason "/operator/reason" --json > /calibration-root/runs/holdout-/selected/run.json

The holdout command belongs only to the holdout-reason status row. Do not
change classifier rules in this workflow.

## Validate

    flow-indicator calibrate --labels /calibration-root/labels --manifest /calibration-root/corpus/manifest.json --split dev

## PR Checkpoint

**Title**: [calibration] Record calibration review

**Files to include**:

- /calibration-root/runs/dev-/selected/run.json
- /calibration-root/runs/holdout-/selected/run.json

**Next phase**: Return to @../SKILL.md
