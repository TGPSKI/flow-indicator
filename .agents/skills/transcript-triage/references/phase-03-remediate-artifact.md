---
name: transcript-triage-remediate
parent: transcript-triage
description: "Preserve evidence and record remediation for a transcript triage finding."
---

# Phase 3: remediation and artifact

**Carry forward from prior phases**: coordinate verdict, evidence tables,
timeline, findings, and artifact path.

**Inspect**:

| Finding status | Action |
|---|---|
| COORDINATE MISMATCH | Record the corrected command, session, projection, or provenance; no code remediation. |
| Finding requires a code or documentation change | Preserve source and event files, create a scoped follow-up, and record validation evidence. |
| Evidence cannot establish a cause | Record unknown and the next discriminating check; do not claim a cause. |
| Remediation would rewrite event files | Stop; append later interpretation or regenerate disposable projections instead. |

**Decide**:

Choose the smallest action that addresses the finding without changing
append-only evidence. Rank actions Critical, High, or Medium.

**Generate**:

Complete the portable artifact with executive summary, evidence tables,
timeline, findings with exposure and remediation, and prioritized open actions.

## Artifact Checkpoint

**File**: incidents/YYYY-MM-DD-incident-slug.md

**Sections completed**:

- Executive summary
- Findings, evidence, exposure, and remediation
- Timeline and prioritized actions

## PR Checkpoint

**Title**: [triage] Record transcript incident artifact

**Files to include**:

- incidents/YYYY-MM-DD-incident-slug.md
