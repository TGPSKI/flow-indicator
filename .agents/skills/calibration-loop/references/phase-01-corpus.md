---
name: calibration-loop-corpus
parent: calibration-loop
description: "Survey and seal a session-level calibration corpus."
---

# Phase 1: corpus preparation

**Carry forward from prior phases**: calibration root.

**Inspect**:

    flow-indicator corpus --harness claude-code --scan /selected/session/store

| Status | Action |
|---|---|
| No merged manifest exists | Continue with the selected corpus root. |
| A merged manifest exists | Return to the router. |
| Survey has no eligible sessions | Stop; ask for another local session store. |

**Decide**:

Ask for the harness, scan directory, and corpus root. Present documented
selection bounds; do not select sessions by outcome.

**Generate**:

    flow-indicator corpus --harness /selected/harness --scan /selected/store --build --out /calibration-root/corpus

The manifest seals source hashes and assigns session-level dev and holdout
splits. Do not edit it after sealing.

## Validate

    flow-indicator label --manifest /calibration-root/corpus/manifest.json --family obligation --split dev > /tmp/calibration-units.jsonl

## PR Checkpoint

**Title**: [calibration] Seal corpus

**Files to include**:

- /calibration-root/corpus/manifest.json

**Next phase**: @phase-02-label-and-annotate.md
