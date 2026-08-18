# Security Policy

## Reporting

Report a suspected vulnerability by opening a GitHub issue at
https://github.com/TGPSKI/flow-indicator/issues. If the report itself would
disclose sensitive data, open an issue that says only that you have a report and
wait to be contacted.

Expect acknowledgement within 7 days and an assessment within 14.

## Scope

`flow-indicator` is a local CLI. It reads transcript stores, writes session and
instance JSON under the user's data directory, writes operator-selected corpus,
labeling and replay-set outputs, shells out to two programs in four bounded
roles, and can post text to an operator-configured HTTP endpoint. Security
issues are:

- **Reading outside the selected source.** A named source opens only that source;
  without `--adapter`, a named path uses the command's default harness rather
  than scanning stores to infer one. `sessions`, `watch --current` and `watch
  --last` enumerate the four agent stores below unless `--adapter` selects one
  before discovery. `watch --pane` reads herdr and only the harness store mapped
  from the pane's agent. `corpus --scan` reads one `--harness`, under the named
  root or that harness's default. The default roots are `~/.claude/projects`,
  `~/.codex/sessions` (or `CODEX_HOME`), `~/.qwen/projects`, and opencode's
  SQLite store. Reading a transcript outside the named source or selected root
  is a vulnerability.
- **Configuration and stored results.** `replay`, `replay-set`, `watch` and
  `calibrate` read the named or default configuration and marker profile;
  `report` and `inspect` read configuration and an existing session under the
  selected data root. A named `--config`, `--profile` or `--data-dir` path is
  the boundary for that read.
- **Subprocess surface.** `sqlite3 -readonly` reads the opencode store;
  `herdr pane current` and `herdr agent list` resolve `watch --pane`;
  `herdr pane report-metadata` pushes the `--herdr-pane` sidebar row. The herdr
  calls are bounded at 3 s and the `sqlite3` read at 10 s; a store another
  process holds locked reports rather than hanging the meter. Passing operator-controlled or transcript-derived text
  into a subprocess argument in a way that changes the command is a
  vulnerability, as is invoking anything else.
- **Live instance records.** Any `watch` on a discovered session — a file, or
  opencode's store — writes one operational record; a piped `watch` writes none.
  The record holds liveness coordinates and a small derived metric subset, read
  back through `instances`. Transcript text or event data appearing in one is a
  vulnerability.
- **Writing outside the selected destination.** Session and instance artifacts
  stay under the data directory; `store.SanitizeID` reduces a source stream
  identifier to one safe path element. `corpus --build --out`, `annotate --out`,
  and `replay-set --output/--anchors` intentionally write to operator-selected
  paths. A write outside the applicable data root or named output is a
  vulnerability.
- **Unintended transcript disclosure.** By default only a 160-character snippet
  per record is stored (`privacy.store_snippets`, `privacy.snippet_chars`), and
  `privacy.store_text` must be set explicitly to store full text. Storing more
  than the configuration allows is a vulnerability.
- **Classifier egress.** In `openai-compatible` and `hybrid` modes, turn text,
  bounded prior operator text, unresolved obligation references and an open
  repair reference are sent to the configured endpoint. `annotate` posts corpus units to its own
  `--endpoint`, which defaults to `http://127.0.0.1:8000/v1/chat/completions`
  and is never `classifier.endpoint`; `calibrate --classifier configured` scores
  through the configured classifier and so posts every scored turn in those two
  modes. Sending anything in `none` or `heuristic` mode, or starting a request
  at an endpoint other than the one named for that command, is a vulnerability.
- **Credential handling.** The API key is read from `FLOW_INDICATOR_API_KEY`
  and sent as a bearer token to the endpoint the running command names — the
  configured classifier endpoint, or `annotate --endpoint`. It is never
  written to a session file, a log line, or an error message.
- **Mutating the measured session.** The tool opens source files read-only. A
  write to a followed session file is a vulnerability.

Out of scope: the security of the endpoint an operator configures, the contents
of transcripts an operator chooses to store, and file permissions on a directory
the operator has made world-readable. Session files and directories are created
with mode 0600 and 0700.

## Supported versions

Pre-1.0. Only the tip of `main` is supported.

## Known limitations

- Stored session data is not encrypted at rest. It is protected by filesystem
  permissions only. Encrypt the underlying volume if transcripts are sensitive.
- `openai-compatible` and `hybrid` modes send turn text to whatever endpoint is
  configured, including a remote one. The default mode is `heuristic`, which
  sends nothing.
- The model clients use Go's default redirect policy. An endpoint returning
  307 or 308 can resend the request body to its redirect target. Do not use an
  endpoint that redirects until redirect following is disabled.
- `sessions`, `--current` and `--last` enumerate every agent store unless
  `--adapter` selects one before discovery, so their output can name projects
  outside the current one.
