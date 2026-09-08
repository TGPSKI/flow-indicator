---
name: user-onboarding
description: "Guide a local flow-indicator installation through configuration, privacy, profiles, and a first measured session; also use for later configuration updates."
metadata:
  author: TGPSKI
  version: "1.0"
compatibility: "A local flow-indicator binary and local agent-session stores"
---

# flow-indicator onboarding

Set up or update one local installation, verify its effective configuration,
and measure one session. This workflow observes local files and sessions. It
does not alter an agent session. Endpoint contact requires the operator's
explicit selection.

## Prerequisites

- Run this from a shell that can invoke the installed flow-indicator binary.
- Keep a finished transcript for replay, or an agent session running for watch.
- Use docs/COMMANDS.md, docs/PROFILES.md, and docs/INFERENCE.md as sources of
  truth for commands and configuration.

## Step 1: Inspect the installation and local state

**Inspect**:

    command -v flow-indicator
    flow-indicator version
    flow-indicator config show
    flow-indicator sessions --here

The config-init output establishes the default configuration directory. Inspect
an existing overlay beside the selected configuration; its default name is
profile.json. Read the overlay before proposing a change.

| Status | Action |
|---|---|
| flow-indicator is on PATH and version succeeds | Record the binary path and version; continue. |
| flow-indicator is absent or version fails | Stop and ask the operator to install or repair the local binary. |
| config show prints built-in defaults and no configuration file is present | Continue to Step 2 on the new-installation path. |
| config show prints an effective configuration from an existing file | Record its path and continue to Step 2 on the update path. |
| sessions --here lists sessions | Record each harness and handle; continue to Step 4. |
| sessions --here finds no session | Continue to Step 4 and use broader discovery. |
| profile.json beside the selected configuration is present | Read it, record its name and version, and continue to Step 3. |
| No profile overlay is present | Record that the shipped baseline is in force; continue to Step 3. |

**Decide**:

Ask only for these operator choices:

1. Create a new default configuration, inspect a named configuration, or update an existing configuration.
2. Retain the default privacy settings or change retained text, snippets, or snippet length.

**Generate**:

Do not write configuration in this step. The config show output is the
effective-configuration reference for the selected path.

## Step 2: Create or update configuration safely

**Inspect**:

Use the selected path and inspect its complete effective form before editing:

    flow-indicator config show
    flow-indicator config show --config /selected/path/config.json

| Status | Action |
|---|---|
| Default configuration is absent and the operator selected default-path creation | Run flow-indicator config init, record the printed path, then run flow-indicator config show. |
| Named configuration is absent and the operator selected named-path creation | Run flow-indicator config init --config /selected/path/config.json, record the printed path, then run flow-indicator config show --config /selected/path/config.json. |
| Selected configuration exists and the operator selected inspection only | Leave the file unchanged; retain the config show output. |
| Selected configuration exists and the operator selected an update | Copy it to an operator-named backup, edit the existing file in place, then validate with flow-indicator config show --config /selected/path/config.json. Do not run config init: it refuses existing files. |
| config show rejects the selected configuration | Stop, report the validation error, and retain the original file or backup until the operator selects a correction. |

**Decide**:

Present the observed effective values and ask whether to retain or change them:

| Configuration fact | Default | Operator decision |
|---|---:|---|
| privacy.store_text | false | Retain full record text in session artifacts. |
| privacy.store_snippets | true | Retain short source snippets for inspection. |
| privacy.snippet_chars | 160 | Maximum retained snippet size. |
| classifier mode | heuristic | Use observations only, local markers, strict semantic replay, or hybrid live classification. |

**Generate**:

Use the complete JSON printed by config show as the update source; do not
construct a partial replacement. The loader rejects unknown keys and invalid
values. Run the matching config show command after every edit and retain its
output as the verified effective configuration.

## Step 3: Choose an optional profile and semantic classifier

**Inspect**:

Read docs/PROFILES.md before changing a profile and docs/INFERENCE.md before
selecting a semantic mode. Inspect the existing overlay from Step 1 without
creating one merely to inspect it.

| Status | Action |
|---|---|
| Operator retains the baseline lexicon | Leave profile.json absent or unchanged; use the baseline profile. |
| Operator selects a profile update and an overlay exists | Edit that overlay, preserving its required name; validate it with a local replay or watch using the selected configuration. |
| Operator selects a profile update and no overlay exists | Create profile.json beside the selected configuration using the shape and merge rules in docs/PROFILES.md; validate it with a local replay or watch using the selected configuration. |
| Classifier mode is none or heuristic | Do not set an endpoint or API key; continue to Step 4. |
| Operator selects openai-compatible, hybrid or deferred | Present the endpoint, model, mode, privacy, destination, and request contents from docs/INFERENCE.md; wait for explicit endpoint selection before configuration changes. |
| Operator selects a local semantic endpoint | Record the selected endpoint and model; configure them after the operator confirms the privacy choice. |
| Operator selects a cloud semantic endpoint | Record that eligible turn text, up to four prior operator turns, unresolved obligation candidates, and an open repair reference leave the machine; configure it after the operator confirms that destination. |
| Operator declines endpoint use | Retain none or heuristic; do not test an endpoint or send transcript text. |
| Operator supplies an API key for a selected endpoint | Set FLOW_INDICATOR_API_KEY through the operator's secret-management or shell environment; never put it in configuration, artifacts, logs, or workflow output. |

**Decide**:

Ask for an endpoint as part of the semantic-mode decision. Show the
baseline-versus-overlay state before asking about a profile change. Ask whether
the first measurement uses a finished session (replay) or an active session
(watch).

**Generate**:

Write only the profile overlay and configuration values the operator selects.
Do not probe, curl, or test a semantic endpoint. A first measurement using none
or heuristic does not send transcript text.

## Step 4: Discover and measure the first session

**Inspect**:

    flow-indicator sessions --here
    flow-indicator sessions

| Status | Action |
|---|---|
| sessions --here has one suitable session | Use its displayed handle and harness for the selected measurement. |
| sessions --here has multiple suitable sessions | Show the handles and ask the operator to select one; do not guess. |
| sessions --here is empty and sessions has suitable sessions | Show those handles and ask the operator to select one. |
| No discovered session is suitable and the operator has a finished generic transcript | Ask for the local path and use replay --adapter generic. |
| No suitable source is available | Stop after verified configuration; ask the operator to return with a finished transcript or active session. |
| Operator selected a finished session with default configuration and baseline profile | Run flow-indicator replay <handle> or flow-indicator replay --adapter generic /local/transcript.jsonl. |
| Operator selected a finished session with named configuration or profile overlay | Run the selected replay command with --config /selected/path/config.json and --profile /selected/path/profile.json. |
| Operator selected an active session with default configuration and baseline profile | Run flow-indicator watch <handle> or flow-indicator watch --current. Stop observation with Ctrl-C when the operator chooses; this records observation stopped without changing the session. |
| Operator selected an active session with named configuration or profile overlay | Run the selected watch command with --config /selected/path/config.json and --profile /selected/path/profile.json. Stop observation with Ctrl-C when the operator chooses; this records observation stopped without changing the session. |
| Operator selected openai-compatible | Use it for the selected finished-session replay after explicit endpoint selection. |
| Operator selected hybrid | Use it after explicit endpoint selection; completed semantic results update a named source-ordered projection. |
| Operator selected deferred | Use it after explicit endpoint selection; semantic results are retained without changing the live marker projection. |

**Decide**:

Ask the operator to select one displayed session or provide one local transcript
path. Confirm the measurement command before it runs when it contains semantic
classifier configuration.

**Generate**:

Run the selected command. Record the session directory and summary line. The
operator can inspect a replay narrative with:

    flow-indicator report --session /measured/stream-id

Treat unknown as unknown. Describe observed counts and classified candidates;
do not claim hidden agent or model state.

## Validate

    flow-indicator config show
    flow-indicator sessions --here

Validation succeeds when the effective configuration prints without error and
the first replay or watch has produced a measured session. These inspection
commands stay local; a semantic endpoint remains uncontacted until the operator
selects it and confirms the measurement command.

## PR Checkpoint

**Title**: [docs] Add user onboarding workflow

**Files to include**:

- .agents/skills/user-onboarding/SKILL.md

**Validation**: go test ./...
