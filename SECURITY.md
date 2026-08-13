# Security Policy

## Reporting

Report a suspected vulnerability by opening a GitHub issue at
https://github.com/TGPSKI/flow-indicator/issues. If the report itself would
disclose sensitive data, open an issue that says only that you have a report and
wait to be contacted.

Expect acknowledgement within 7 days and an assessment within 14.

## Scope

`flow-indicator` is a local CLI. It reads transcript files, writes JSON under
the user's data directory, shells out to two programs in four bounded roles, and optionally posts
turn text to an operator-configured HTTP endpoint. Security issues for this
project are:

- **Reading outside the named source.** With a named source, the tool opens
  exactly that file. `sessions`, `watch --current`, `watch --last`, `watch --pane` and
  `corpus --scan` instead enumerate every harness's session directory —
  `~/.claude/projects`, `~/.codex/sessions` (or `CODEX_HOME`),
  `~/.qwen/projects`, and opencode's SQLite store — plus the configuration
  directory, including `profile.json` when one is present, and the data
  directory. `--pane` narrows by session id after that enumeration, not before.
  Reading a transcript outside those roots, or outside a `--scan` path the
  operator named, is a vulnerability.
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
- **Writing outside the data directory.** Stream identifiers come from source
  files and are used as directory names; `store.SanitizeID` reduces them to a
  single safe path element. A path escape here is a vulnerability.
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
  modes. Sending anything in `none` or `heuristic` mode, or sending to an
  endpoint other than the one named for that command, is a vulnerability.
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
- `sessions` and `--current` enumerate every agent session on the machine to
  answer, so their output names projects outside the current one.
