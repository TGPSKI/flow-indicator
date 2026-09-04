---
name: transcript-triage-investigate
parent: transcript-triage
description: "Build a source-order timeline and discriminate transcript-output hypotheses."
---

# Phase 2: investigation

**Carry forward from prior phases**: COORDINATES VERIFIED artifact and all
recorded source coordinates.

**Inspect**:

Build a source-order timeline from source.json and append-only events. Include
record sequence, source offset, observation, classification, transition,
derived metric, projection refresh, and semantic completion.

| Hypothesis | Prior | Discriminating check |
|---|---|---|
| Projection/render mismatch | Medium | Regenerate report and timeline from the stored event files. |
| Event classification mismatch | Medium | Inspect the source turn and classified event with profile/rule hash. |
| Source or adapter mismatch | Medium | Decode the recorded source coordinate through the selected harness. |
| Semantic-completion mismatch | Low | Compare completion input hash and identity with strict replay selection. |

| Evidence status | Action |
|---|---|
| Fewer than two hypotheses | Add a competing explanation before checking code. |
| Cheapest check can eliminate hypotheses | Run it and append the result to the timeline. |
| One hypothesis has definitive evidence | Record the finding and route to Phase 3. |
| Several hypotheses survive | Choose the next cheapest discriminating check. |
| No hypothesis survives | Record the gap and generate new hypotheses from the timeline. |

**Decide**:

Select the lowest-cost non-mutating check that eliminates the most hypotheses.

**Generate**:

Append the timeline, evidence-hypothesis table, checks, and findings to the
artifact. Preserve events; rerun projections rather than editing evidence.

## Artifact Checkpoint

**File**: incidents/YYYY-MM-DD-incident-slug.md

**Sections completed**:

- Source-order timeline
- At least two competing hypotheses
- Discriminating checks and findings

## PR Checkpoint

**Title**: [triage] Investigate transcript output

**Files to include**:

- incidents/YYYY-MM-DD-incident-slug.md
