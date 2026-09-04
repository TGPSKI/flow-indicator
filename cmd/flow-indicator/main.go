// Command flow-indicator measures how much operator effort a stream of
// interaction records spends steering rather than advancing work.
//
// It reads a transcript, appends events, projects state, and prints metrics.
// It never acts on the stream it measures.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/harness"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
	"github.com/TGPSKI/flow-indicator/internal/profile"
	"github.com/TGPSKI/flow-indicator/internal/render"
	"github.com/TGPSKI/flow-indicator/internal/state"
	"github.com/TGPSKI/flow-indicator/internal/store"
	"github.com/TGPSKI/flow-indicator/internal/stream"
	"github.com/TGPSKI/flow-indicator/pkg/panel"
)

const usage = `flow-indicator: a local meter for operator steering cost.

start here:
  flow-indicator sessions         every agent session on this machine
  flow-indicator watch --last     follow the most recent one
  flow-indicator watch --current  follow the one for this directory
  flow-indicator watch --pane auto  the agent pane beside this one
  flow-indicator watch 019ffd20   follow one by name

commands:
  watch       follow a session live and draw the meter
  instances   list live watch instances as JSON
  sessions    list discovered sessions across every harness
  replay      read a finished session and write its projections
  report      print the stored report for a session
  inspect     draw the meter as it stood at one turn
  replay-set  replay a manifest of sessions into one table
  corpus      survey sessions and seal a corpus
  label       emit units for a labeler to fill in
  annotate    run a model over the units, write candidate labels
  calibrate   score the rules against labels
  config      create or show the effective configuration
  version     the build this binary came from

harnesses: claude-code, codex, opencode, qwen, generic
  Rarely named. sessions and watch find them, and a session named
  by identifier carries its own harness.

common flags:
  --config <path>     configuration file (XDG config path)
  --data-dir <path>   storage root (XDG data path)
  --profile <path>    marker lexicon overlay; the default is
                      profile.json beside the configuration file

flow-indicator help <command>   flags and detail for one command
`

// helpColumns is the width every help page is written to fit.
//
// The meter runs in a side pane, and so does the help an operator reads while
// the pane is open. Text wider than the pane wraps mid-word, which is how the
// flag list became unreadable in the terminal it was written for.
const helpColumns = 72

// commandHelp is the detail for one command, printed by help <command>.
//
// The front page answers "what do I type"; these answer "what does it do". A
// single page carrying both was ninety lines, and the three commands an
// operator actually starts with were below the fold.
var commandHelp = map[string]string{
	"version": `flow-indicator version — the build this binary came from.

  flow-indicator version        also --version, -v

make build and make install stamp the version and the commit.
A build made without them reports dev and the revision the
toolchain recorded, marked modified when the tree was dirty.
`,
	"instances": `flow-indicator instances — list live watch instances as JSON.

  flow-indicator instances --json

  --json                  required; there is no human-readable form
  --data-dir <path>       storage root

Reads local watch records only.
It does not read sessions, source, agents or models.
`,
	"watch": `flow-indicator watch — follow a session live, drawing the meter.

  flow-indicator watch --last         the most recent session
  flow-indicator watch --current      the session for this directory
  flow-indicator watch --pane auto    the agent pane sharing this tab
  flow-indicator watch <session-id>   one named session, any harness
  flow-indicator watch --adapter <name> <file>
  <stream> | flow-indicator watch --adapter generic

An identifier comes from flow-indicator sessions. Any prefix long
enough to be unique works, and it carries its own harness, so there
is no path to find. All three forms print the session they chose,
then replay it from the beginning to build state before following.
Bootstrap reads the source and never writes to it: about 0.2 s on a
6.5 MB session.

  --last            the most recently written session on this machine
  --current         the session recorded for this working directory
  --pane <id|auto>  the session the agent in a herdr pane is running;
                    auto finds the agent pane sharing this pane's tab
  --tail-only       follow from the end, building no state first
  --full            the one-screen audit view, not the side pane
  --no-color        no ANSI attributes (NO_COLOR is honoured)
  --width <n>       side-pane inner width in columns
  --adapter <name>  narrow discovery, or decode a file as this
  --root <path>     directory action targets are relative to
  --from-start      with a named file, read from the beginning
  --offset <n>      with a named file, start at a byte offset
  --stream-id <id>  override the stream identifier
  --force           replace an existing session directory
  --herdr-pane <id|auto>
                    also push the phase into this herdr pane's
                    sidebar as metadata tokens, with a TTL; the
                    row exists only for a promoted agent pane.
                    auto finds the agent pane sharing this tab
  --herdr-workspace <id|auto>
                    push the same tokens onto a workspace's
                    space row, which every workspace has.
                    auto is the workspace this process runs in
  --config <path>   configuration file
  --data-dir <path> storage root
  --profile <path>  marker lexicon overlay

A --pane watch is keyed to the pane, not to the session it first
resolved: it re-resolves every five seconds and follows the new
session when the agent restarts. A pane whose herdr integration has
not reported a session yet is waited on, not failed.

opencode has no file to tail: its newest message grows in place, so
it is followed by re-query and that message is held back until it is
finished. A record the projector consumed cannot be amended.

Ctrl-C records that observation stopped, where it reached, and what
was left open. It concludes nothing: an open repair stays open.
`,
	"sessions": `flow-indicator sessions — list sessions across every harness.

Each harness hides its transcripts differently: Codex under a dated
directory named by UUID, opencode as rows in SQLite under a generated
key. This prints them in one table, newest first, with a handle short
enough to retype and hand to watch.

Sub-agent threads are left out, and so is any transcript that records
no working directory: both are streams no operator is sitting in.

At most three rows come from one project, so a tool that spawns a
session per task cannot fill the page.

  --all               every session, not the most recent few
  --here              only sessions for this working directory
  --adapter <name>    only one harness
`,
	"replay": `flow-indicator replay — read a finished session, write projections.

  flow-indicator replay --adapter generic <file>
  flow-indicator replay --adapter codex <rollout.jsonl>
  flow-indicator replay --adapter opencode <db-path>#<session-id>

  --adapter <name>    harness that wrote the source
  --root <path>       directory action targets are relative to
  --stream-id <id>    override the stream identifier
  --semantic-session <dir>
                      select retained semantic results
                      for strict replay
  --force             replace an existing session directory
  --quiet             write files without the summary line
  --config <path>     configuration file
  --data-dir <path>   storage root
  --profile <path>    marker lexicon overlay
`,
	"report": `flow-indicator report — print the stored report for a session.

  --session <stream-id>   the session to report on
  --config <path>         configuration file
  --data-dir <path>       storage root
  --profile <path>        marker lexicon overlay
`,
	"inspect": `flow-indicator inspect — draw the meter as it stood at one turn.

  --session <stream-id>   the session to inspect
  --turn <n>              the turn to draw
  --micro                 draw the side-pane meter, not the audit view
  --no-color              no ANSI attributes
  --width <n>             inner width in columns, with --micro
  --config <path>         configuration file
  --data-dir <path>       storage root
  --profile <path>        marker lexicon overlay
`,
	"replay-set": `flow-indicator replay-set — replay a manifest into one table.

  --manifest <file>   one source path per line
  --output <file>     where the rows are written
  --anchors <file>    where regime anchors are written
  --adapter <name>    harness that wrote the sources
  --config <path>     configuration file
  --data-dir <path>   storage root
  --profile <path>    marker lexicon overlay
`,
	"corpus": `flow-indicator corpus — survey sessions and seal a corpus.

Surveys a directory of harness sessions and, with --build, writes the
manifest that seals the selected ones. Sessions are referenced where
they lie and sealed by hash; --copy duplicates them instead.

The dev/holdout split is a function of each session identifier,
stratified by project: nobody chooses which sessions the holdout
gets, and a session already in the manifest keeps the split it had.

  --scan <dir>          directory to survey; empty uses the
                        harness's own default location
  --harness <name>      which harness to survey (default claude-code)
  --build --out <dir>   write the manifest sealing the selection
  --copy                copy sessions in instead of referencing them
  --per-project <n>     at most this many sessions from one project
  --min-turns <n>       fewest operator turns a session must carry
  --max-bytes <n>       skip sessions larger than this
  --quiet-for <dur>     skip sessions written more recently than this
`,
	"label": `flow-indicator label — emit the units of one family for a labeler.

Prints the source text and no verdict. A labeler shown what the build
decided agrees with the build, and labels produced that way measure
the agreement rather than the rule.

  --manifest <file>   the sealed corpus
  --family <name>     obligation, obligation_pair, recovery, pollution
  --session <id>      one session instead of the whole split
  --split dev|holdout which side to emit (default dev)
  --sample-per-mille <n>
                      emit this many operator turns per thousand;
                      0 emits all of them
`,
	"annotate": `flow-indicator annotate — run a model, write candidate labels.

A model judgement is not ground truth: it is a pass an adjudicator
accepts or rejects, and the files name the model so a score can say
what judged the corpus.

Two passes run per family, differing in order and wording, so their
agreement is evidence about the codebook, not about the sampler.

  --manifest <file>   the sealed corpus
  --out <dir>         where candidate labels are written
  --family <name>     one family, or empty for all of them
  --split dev|holdout which side to annotate (default dev)
  --sample-per-mille <n>
                      annotate this many operator turns per thousand
  --endpoint <url>    OpenAI-compatible chat completions endpoint
  --model <name>      model to send
`,
	"calibrate": `flow-indicator calibrate — score the rules against labels.

Replays the labeled sessions on one side of the corpus split and
scores what the rules said against what a labeler judged.

Every run prints the artifact, corpus and label hashes it ran under,
because a score that cannot name its own subject is not evidence.

  --labels <dir>        the label files
  --manifest <file>     the sealed corpus
  --split dev|holdout   which side to score (default dev)
  --classifier <mode>   heuristic or configured (default heuristic)
  --reason <text>       why the holdout is opened; required for it
  --json                write the run record as JSON
  --config <path>       configuration file
  --data-dir <path>     storage root
  --profile <path>      marker lexicon overlay
`,
	"config": `flow-indicator config — create or show configuration.

  flow-indicator config init
  flow-indicator config show
  flow-indicator config init --config ./flow-indicator.json
  flow-indicator config show --config ./flow-indicator.json

init writes the complete default configuration, creating parent
directories when needed. It refuses to overwrite a file.

show prints the complete effective configuration. With no file at
the default path, it prints the built-in defaults.

  --config <path>   configuration file; default is the XDG path
`,
}

// help prints the detail for one command.
func help(args []string) {
	if len(args) == 0 {
		fmt.Print(usage)
		return
	}
	name := args[0]
	detail, ok := commandHelp[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "flow-indicator: no command %q\n\n%s", name, usage)
		os.Exit(2)
	}
	fmt.Print(detail)
}

// version and commit are the release this binary was built from. The Makefile
// sets both with -ldflags; a build that does not says "dev" rather than claiming
// a release it is not, and falls back to the revision the toolchain stamped in.
var (
	version = "dev"
	commit  = ""
)

// printVersion names the binary and the build it came from.
//
// A calibration run record already carries the artifact's hash, which is what
// makes a score reproducible. This is the human-readable half: an operator
// reporting a problem can say which build produced it without hashing anything.
func printVersion() {
	revision, modified := commit, ""
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if revision == "" {
					revision = s.Value
				}
			case "vcs.modified":
				if s.Value == "true" {
					modified = " (modified)"
				}
			}
		}
	}
	fmt.Printf("flow-indicator %s", version)
	if revision != "" {
		fmt.Printf(" %s%s", revision[:min(12, len(revision))], modified)
	}
	fmt.Println()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "replay":
		err = replay(os.Args[2:])
	case "replay-set":
		err = replaySet(os.Args[2:])
	case "watch":
		err = watch(os.Args[2:])
	case "instances":
		err = instances(os.Args[2:])
	case "report":
		err = report(os.Args[2:])
	case "inspect":
		err = inspect(os.Args[2:])
	case "calibrate":
		err = calibrateCmd(os.Args[2:])
	case "label":
		err = labelCmd(os.Args[2:])
	case "corpus":
		err = corpusCmd(os.Args[2:])
	case "annotate":
		err = annotateCmd(os.Args[2:])
	case "sessions":
		err = sessionsCmd(os.Args[2:])
	case "config":
		err = configCmd(os.Args[2:])
	case "-h", "--help", "help":
		help(os.Args[2:])
		return
	case "-v", "--version", "version":
		printVersion()
		return
	default:
		fmt.Fprintf(os.Stderr, "flow-indicator: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// commonFlags are the flags every subcommand accepts.
type commonFlags struct {
	configPath  string
	dataDir     string
	profilePath string
}

func (c *commonFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.configPath, "config", "", "configuration file path")
	fs.StringVar(&c.dataDir, "data-dir", "", "storage root")
	fs.StringVar(&c.profilePath, "profile", "", "marker lexicon overlay; default is profile.json beside the configuration file")
}

// rules resolves the marker lexicon in force: the shipped baseline, with an
// operator overlay merged over it.
//
// The overlay lives beside the configuration file, so naming a configuration
// file names the overlay that goes with it and a test run cannot pick up the
// operator's own lexicon. An overlay named with --profile that does not exist
// is an error; the default one not existing is not, because most operators
// never write one.
func (c *commonFlags) rules() (*profile.Ruleset, error) {
	dir := filepath.Dir(c.configPath)
	if c.configPath == "" {
		path, err := config.Path()
		if err != nil {
			return nil, err
		}
		dir = filepath.Dir(path)
	}
	p, err := profile.Resolve(c.profilePath, dir)
	if err != nil {
		return nil, err
	}
	return profile.Compile(p)
}

// load resolves configuration and the storage root.
func (c *commonFlags) load() (config.Config, string, error) {
	var cfg config.Config
	var err error
	if c.configPath != "" {
		cfg, err = config.Load(c.configPath)
	} else {
		cfg, err = config.LoadDefaultPath()
	}
	if err != nil {
		return config.Config{}, "", err
	}
	root := c.dataDir
	if root == "" {
		root, err = config.DataPath()
		if err != nil {
			return config.Config{}, "", err
		}
	}
	return cfg, root, nil
}

// classifierFor selects the classifier for either strict replay or live watch.
//
// Every tier reads the same ruleset. The marker tier is the whole of the
// heuristic classifier and the fast half of the other two, so handing one tier
// an overlay and another the baseline would put two lexicons in one session.
func classifierFor(cfg config.Config, rules *profile.Ruleset, live bool) (classify.Classifier, error) {
	markers := classify.Heuristic{Rules: rules}
	switch cfg.Classifier.Mode {
	case config.ModeNone:
		return classify.None{}, nil
	case config.ModeHeuristic:
		return markers, nil
	case config.ModeOpenAI:
		return semanticClassifierFor(cfg, rules)
	case config.ModeHybrid:
		semantic, err := semanticClassifierFor(cfg, rules)
		if err != nil {
			return nil, err
		}
		if !live {
			return semantic, nil
		}
		hybrid, err := classify.NewHybridWithDeadline(semantic, cfg.Classifier.Workers, cfg.Classifier.MaxQueue, time.Duration(cfg.Classifier.LiveDeadlineMS)*time.Millisecond)
		if err != nil {
			return nil, err
		}
		hybrid.Markers = markers
		return hybrid, nil
	default:
		return nil, fmt.Errorf("config: classifier.mode %q is not supported", cfg.Classifier.Mode)
	}
}

func semanticClassifierFor(cfg config.Config, rules *profile.Ruleset) (classify.Classifier, error) {
	switch cfg.Classifier.Mode {
	case config.ModeOpenAI, config.ModeHybrid:
		cls := classify.NewOpenAI(cfg.Classifier.Endpoint, cfg.Classifier.Model, os.Getenv("FLOW_INDICATOR_API_KEY"))
		cls.Markers = classify.Heuristic{Rules: rules}
		return cls, nil
	default:
		return nil, fmt.Errorf("config: persisted semantic replay requires classifier.mode openai-compatible or hybrid, got %q", cfg.Classifier.Mode)
	}
}

func readSemanticCompletions(dir string) ([]classify.Completion, error) {
	events, err := store.ReadKind(dir, event.KindSemanticCompleted)
	if err != nil {
		return nil, err
	}
	completions := make([]classify.Completion, 0, len(events))
	for _, e := range events {
		var completion classify.Completion
		if err := json.Unmarshal(e.Payload, &completion); err != nil {
			return nil, fmt.Errorf("replay: decode semantic completion %s: %w", e.ID, err)
		}
		completions = append(completions, completion)
	}
	return completions, nil
}

// preflightPersistedReplay proves every eligible semantic input has one exact,
// retained completion before replay opens or replaces an output session.
func preflightPersistedReplay(cfg config.Config, streamID string, records []stream.Record, semantic classify.Classifier, completions []classify.Completion) error {
	persisted, err := classify.NewPersisted(semantic, completions)
	if err != nil {
		return err
	}
	projector := state.New(cfg, persisted, streamID)
	for _, rec := range records {
		projector.Push(context.Background(), rec)
	}
	projector.Finish()
	if err := persisted.Err(); err != nil {
		return fmt.Errorf("replay: select persisted semantic result: %w", err)
	}
	return nil
}

func replay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	var common commonFlags
	common.bind(fs)
	adapterName := fs.String("adapter", "generic", "adapter or harness name: "+strings.Join(harness.Names(), ", "))
	sessionRoot := fs.String("root", "", "working directory action targets are made relative to, for sources that record none")
	streamID := fs.String("stream-id", "", "override the stream identifier")
	semanticSession := fs.String("semantic-session", "", "session directory holding persisted semantic completions for strict replay")
	force := fs.Bool("force", false, "replace an existing session directory")
	quiet := fs.Bool("quiet", false, "write files without printing the summary line")
	operands, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(operands) != 1 {
		return errors.New("replay: exactly one source file is required")
	}
	path := operands[0]

	cfg, root, err := common.load()
	if err != nil {
		return err
	}
	rules, err := common.rules()
	if err != nil {
		return err
	}
	abs := path
	if !strings.Contains(path, "#") {
		if abs, err = filepath.Abs(path); err != nil {
			return fmt.Errorf("replay: resolve %s: %w", path, err)
		}
	}
	// Every source goes through the harness layer, so a session from any of the
	// harnesses replays into the same records and the same rules.
	records, err := harness.OpenPath(*adapterName, abs, *sessionRoot)
	if err != nil {
		return err
	}

	id := *streamID
	if id == "" {
		id = streamIDOf(records, path)
	}
	cls, err := classifierFor(cfg, rules, false)
	if err != nil {
		return err
	}
	if *semanticSession != "" {
		semantic, err := semanticClassifierFor(cfg, rules)
		if err != nil {
			return err
		}
		completions, err := readSemanticCompletions(*semanticSession)
		if err != nil {
			return fmt.Errorf("replay: read semantic session %s: %w", *semanticSession, err)
		}
		if err := preflightPersistedReplay(cfg, id, records, semantic, completions); err != nil {
			return err
		}
		cls, err = classify.NewPersisted(semantic, completions)
		if err != nil {
			return err
		}
		outputDir, err := filepath.Abs(store.SessionDir(root, id))
		if err != nil {
			return fmt.Errorf("replay: resolve output session: %w", err)
		}
		inputDir, err := filepath.Abs(*semanticSession)
		if err != nil {
			return fmt.Errorf("replay: resolve semantic session: %w", err)
		}
		if outputDir == inputDir {
			return errors.New("replay: --semantic-session must differ from the output session; choose --stream-id for the replay")
		}
	}
	session, err := store.OpenSession(root, id, *force)
	if err != nil {
		return err
	}
	projector := state.New(cfg, cls, id)

	ctx := context.Background()
	for _, rec := range records {
		if err := session.AppendAll(projector.Push(ctx, rec)); err != nil {
			session.Close()
			return err
		}
	}
	if err := session.AppendAll(projector.Finish()); err != nil {
		session.Close()
		return err
	}
	if err := session.Close(); err != nil {
		return err
	}
	if err := session.WriteSource(store.Source{
		StreamID: id,
		Adapter:  *adapterName,
		Path:     abs,
		Records:  len(records),
	}); err != nil {
		return err
	}
	if err := writeProjections(session.Dir); err != nil {
		return err
	}
	if !*quiet {
		loaded, err := render.Load(session.Dir)
		if err != nil {
			return err
		}
		fmt.Println(render.Compact(render.LiveView{
			StreamID:   id,
			Turn:       loaded.Final().TurnIndex,
			Regime:     loaded.FinalRegime(),
			Snapshot:   loaded.Final(),
			Thresholds: cfg.Thresholds,
		}))
		fmt.Printf("session %s\n", session.Dir)
	}
	return nil
}

// writeProjections rebuilds the disposable files from the event log.
func writeProjections(dir string) error {
	loaded, err := render.Load(dir)
	if err != nil {
		return err
	}
	if err := store.WriteJSON(filepath.Join(dir, store.FileSummary), loaded.Summarize()); err != nil {
		return err
	}
	if err := store.WriteJSON(filepath.Join(dir, store.FileMetricsJSON), loaded.Snapshots); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, store.FileReport), []byte(loaded.Report()), 0o600); err != nil {
		return fmt.Errorf("store: write %s: %w", store.FileReport, err)
	}
	if err := os.WriteFile(filepath.Join(dir, store.FileTimeline), []byte(loaded.Timeline()), 0o600); err != nil {
		return fmt.Errorf("store: write %s: %w", store.FileTimeline, err)
	}
	return nil
}

func watch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	var common commonFlags
	common.bind(fs)
	adapterName := fs.String("adapter", "claude-code", "adapter or harness name: "+strings.Join(harness.Names(), ", "))
	sessionRoot := fs.String("root", "", "working directory action targets are made relative to, for sources that record none")
	streamID := fs.String("stream-id", "", "override the stream identifier")
	herdrPane := fs.String("herdr-pane", "", "report the phase into this herdr pane's sidebar row as metadata tokens; auto finds the agent pane sharing this tab")
	herdrWorkspace := fs.String("herdr-workspace", "", "report the phase into this herdr workspace's space row as metadata tokens; auto is this process's workspace")
	current := fs.Bool("current", false, "follow the session recorded for this working directory")
	last := fs.Bool("last", false, "follow the most recently written session on this machine")
	pane := fs.String("pane", "", "follow the agent session in this herdr pane; auto finds the agent pane sharing this pane's tab")
	tailOnly := fs.Bool("tail-only", false, "follow from the end instead of building state first")
	fromStart := fs.Bool("from-start", false, "read the file from the beginning instead of its end")
	full := fs.Bool("full", false, "draw the one-screen view instead of the side-pane meter")
	noColor := fs.Bool("no-color", false, "draw without ANSI attributes")
	width := fs.Int("width", 0, "side-pane inner width in columns")
	offset := fs.Int64("offset", stream.TailFromEnd, "byte offset to start reading at; state is built from that byte forward, not restored")
	force := fs.Bool("force", false, "replace an existing session directory")
	operands, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	cfg, root, err := common.load()
	if err != nil {
		return err
	}
	// Every source goes through the harness layer, so a session from any of
	// the harnesses is followed by the same loop under the same rules.
	if _, err := harness.New(*adapterName); err != nil {
		return err
	}
	rules, err := common.rules()
	if err != nil {
		return err
	}
	liveMode := *current || *last || *pane != "" || len(operands) > 0
	cls, err := classifierFor(cfg, rules, liveMode)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	herdrScope, herdrID, err := resolveHerdrTarget(*herdrPane, *herdrWorkspace)
	if err != nil {
		return err
	}
	view := display{micro: !*full, color: useColor(*noColor), width: *width, herdrScope: herdrScope, herdrID: herdrID}

	// --adapter carries a default, so its value cannot say whether the operator
	// chose it. Only a flag actually passed narrows a search or overrides an
	// inferred harness.
	explicitHarness := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "adapter" {
			explicitHarness = true
		}
	})
	only := ""
	if explicitHarness {
		only = *adapterName
	}

	// A session chosen through discovery is announced and replayed from its beginning: a
	// pane that starts halfway through a conversation with no history shows a
	// regime derived from a fragment. That is bootstrap, not analysis — the
	// source is read, never copied and never written to. A bare path keeps the
	// older behaviour of starting at the end, because a path is not always a
	// session and its offsets are the caller's to choose.
	switch {
	case *pane != "":
		if *current || *last {
			return errors.New("watch: --pane already names the session's pane; drop --current/--last")
		}
		if len(operands) > 0 {
			return errors.New("watch: --pane finds the session itself; do not also name one")
		}
		// The watch is keyed to the pane, not to the session first resolved:
		// an agent restart gives the pane a new session, and the meter must
		// follow it rather than tail a file nothing writes to any more. A
		// monitor re-resolves the pane and ends the observation when it
		// positively names a different session; the loop then resolves and
		// follows that one.
		first := true
		for {
			found, err := paneSessionWait(ctx, paneWaitInterval, func() (harness.Session, error) {
				return paneSession(*pane)
			})
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return err
			}
			if only != "" && only != found.Harness {
				return fmt.Errorf("watch: pane runs %s; --adapter %s contradicts it", found.Harness, only)
			}
			// --tail-only speaks to the first, possibly large, session. A
			// session that appeared mid-watch is new and is replayed whole.
			start := announce(found, *tailOnly && first)
			first = false
			var changed atomic.Bool
			watchCtx, cancelWatch := context.WithCancel(ctx)
			go watchPaneChange(watchCtx, cancelWatch, func() (string, error) {
				_, id, err := paneTarget(*pane)
				return id, err
			}, found.ID, paneRecheckInterval, &changed)
			err = watchFile(watchCtx, cfg, root, found.Harness, cls, found.Locator, idOr(*streamID, found.ID), found.Root, start, true, view)
			cancelWatch()
			if !changed.Load() {
				return err
			}
		}

	case *current, *last:
		var found harness.Session
		if *current {
			found, err = currentSession(only, operands)
		} else {
			found, err = lastSession(only)
		}
		if err != nil {
			return err
		}
		start := announce(found, *tailOnly)
		// The session directory is rebuilt from the source every time, so
		// replacing what a previous run left there loses nothing.
		return watchFile(ctx, cfg, root, found.Harness, cls, found.Locator, idOr(*streamID, found.ID), found.Root, start, true, view)

	case len(operands) == 0:
		return watchStdin(ctx, cfg, root, *adapterName, cls, *streamID, *sessionRoot, *force, view)
	}

	found, err := resolveSession(operands[0], *adapterName, explicitHarness)
	if err != nil {
		return err
	}
	sourceRoot := *sessionRoot
	if sourceRoot == "" {
		sourceRoot = found.Root
	}
	// An identifier could only have come from discovery, so it is treated as a
	// chosen session. A path is left on its old footing.
	if found.Locator != operands[0] || !strings.Contains(operands[0], string(filepath.Separator)) {
		start := announce(found, *tailOnly)
		return watchFile(ctx, cfg, root, found.Harness, cls, found.Locator, idOr(*streamID, found.ID), sourceRoot, start, true, view)
	}
	start := *offset
	if *fromStart {
		start = 0
	}
	return watchFile(ctx, cfg, root, found.Harness, cls, found.Locator, idOr(*streamID, found.ID), sourceRoot, start, *force, view)
}

// idOr is the stream identifier to store a session under: what the operator
// asked for, else what the harness calls it.
//
// The harness name is not the file name. A Codex rollout is a file called
// rollout-<timestamp>-<uuid>.jsonl holding a session whose identifier is the
// uuid alone, and storing it under the file stem gave one session two names —
// the one watch printed and the one report would have had to be told.
func idOr(override, harnessID string) string {
	if override != "" {
		return override
	}
	return harnessID
}

// display is how the live path draws. The side-pane meter is the default; the
// one-screen view is behind --full.
type display struct {
	micro bool
	color bool
	width int
	// herdrScope and herdrID name the sidebar row that should carry the
	// phase, already resolved from --herdr-pane / --herdr-workspace; herdr
	// draws a pane row only for a promoted agent, a space row for every
	// workspace. An empty id means the meter draws only into its own
	// terminal.
	herdrScope string
	herdrID    string
}

// herdrPaneID is the pane the instance record names, when the reporter
// targets one.
func (d display) herdrPaneID() string {
	if d.herdrScope == "pane" {
		return d.herdrID
	}
	return ""
}

// useColor honours --no-color and the NO_COLOR convention, and stays off when
// stdout is not a terminal so a redirected capture holds plain text.
func useColor(noColor bool) bool {
	if noColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// watchFile follows one session while it is being written.
//
// The locator is a file path for the file-backed harnesses and a store
// reference for the others; only the former is made absolute.
func watchFile(ctx context.Context, cfg config.Config, root, harnessName string, cls classify.Classifier, locator, streamID, sessionRoot string, offset int64, force bool, view display) error {
	abs := locator
	if !strings.Contains(locator, "#") {
		var err error
		if abs, err = filepath.Abs(locator); err != nil {
			return fmt.Errorf("watch: resolve %s: %w", locator, err)
		}
	}

	id := streamID
	if id == "" {
		id = harness.IDOf(abs)
	}

	src, err := harness.Follow(harnessName, abs, sessionRoot, id, offset)
	if err != nil {
		return err
	}
	defer src.Close()
	session, err := store.OpenSession(root, id, force)
	if err != nil {
		return err
	}
	defer session.Close()
	emit, err := newInstanceEmitter(root, id, harnessName, abs, sessionRoot, view.herdrPaneID())
	if err != nil {
		return err
	}
	defer emit.Close()

	projector := state.New(cfg, cls, id)
	hybrid, _ := cls.(*classify.Hybrid)
	if hybrid != nil {
		defer hybrid.Close()
	}
	flushSemantic := func(final bool) error {
		if hybrid == nil {
			return nil
		}
		if final {
			if err := hybrid.Close(); err != nil {
				return err
			}
		}
		return appendSemanticCompletions(session, hybrid, final)
	}
	stop := func() error {
		if err := flushSemantic(true); err != nil {
			return err
		}
		return stopWatch(session, projector, abs, harnessName, src.Position(), src.Mark())
	}
	screen := panel.NewScreen(os.Stdout)
	// The view hides the cursor while it holds the screen; the terminal gets
	// it back however this returns.
	defer screen.Close()
	trail := newTrail()
	regimes := newRegimeTrail()
	live := liveness{last: time.Now()}
	moves := render.NewHighlights()

	herdr := newHerdrReporter(ctx, view.herdrScope, view.herdrID)
	defer herdr.Close()
	var lastRow herdrRow
	var lastPush time.Time

	draw := func() {
		// The sidebar row is offered from the same place the screen is drawn,
		// so the two never disagree. It is pushed when it changes, and again
		// on a slow cadence to hold its TTL open.
		row := herdrRow{
			phase:   string(projector.Regime()),
			turn:    fmt.Sprintf("turn %d", projector.Snapshot().TurnIndex),
			elapsed: panel.ShortSpan(live.elapsed()),
			trend:   live.trend.Text(),
		}
		if row != lastRow || time.Since(lastPush) > herdrRefresh {
			herdr.Report(row)
			lastRow, lastPush = row, time.Now()
		}
		// The instance record is a local operational projection beside the
		// session event log. A write failure never delays or changes the meter.
		_ = emit.Report(instanceState{
			Regime:   string(projector.Regime()),
			Snapshot: projector.Snapshot(),
			Elapsed:  panel.ShortSpan(live.elapsed()),
			Trend:    live.trend.Text(),
			Mark:     src.Mark(),
		})

		var semantic *classify.Operational
		if hybrid != nil {
			s := hybrid.Operational()
			semantic = &s
		}
		drawLive(screen, view, live, drawState{
			id:         id,
			regime:     string(projector.Regime()),
			snapshot:   projector.Snapshot(),
			trail:      trail.items(),
			regimes:    regimes.items(),
			thresholds: cfg.Thresholds,
			moves:      moves.Active(time.Now()),
			semantic:   semantic,
		})
	}
	// The first frame is drawn before any bytes arrive, so a session that is
	// quiet at startup shows a meter rather than an empty screen.
	draw()

	// The view repaints on a timer as well as on arrival. Without it the
	// screen holds the last frame for as long as the stream is quiet, and a
	// stalled observer is indistinguishable from a thinking agent.
	beat := time.NewTicker(heartbeat)
	defer beat.Stop()

	arrivals := readLive(ctx, src)
	for {
		select {
		case <-ctx.Done():
			// The observer was signalled, not the source. Nothing about the
			// interaction is concluded here.
			live.stopped = true
			draw()
			return stop()

		case <-beat.C:
			if err := flushSemantic(false); err != nil {
				return err
			}
			live.pulse++
			draw()

		case c, ok := <-arrivals:
			if !ok {
				live.stopped = true
				draw()
				return stop()
			}
			if c.err != nil {
				if errors.Is(c.err, context.Canceled) {
					live.stopped = true
					draw()
					return stop()
				}
				return c.err
			}
			for _, rec := range c.records {
				events := projector.Push(ctx, rec)
				if err := session.AppendAll(events); err != nil {
					return err
				}
				trail.add(events)
				live.observe(rec)
				live.readTrends(events)
				live.readEvents(events)
			}
			if err := flushSemantic(false); err != nil {
				return err
			}
			if err := session.Flush(); err != nil {
				return err
			}
			regimes.add(string(projector.Regime()))
			// The snapshot only moves on an operator turn, so this marks what
			// that turn changed rather than anything the heartbeat did.
			moves.Observe(projector.Snapshot(), time.Now())
			live.pulse++
			draw()
		}
	}
}

// heartbeat is how often the view repaints with no new bytes. It is the rate
// the liveness indicator advances at, so it is fast enough to read as running
// and slow enough to cost nothing.
const heartbeat = time.Second

// liveness is the observer's own view of the stream: what has arrived since
// the operator's last turn, and when. None of it is a claim about what the
// agent is doing, which the transcript does not carry.
type liveness struct {
	name    string
	records int
	waiting bool
	seen    bool
	last    time.Time
	pulse   int
	stopped bool
	// first and latest are the stream's own timestamps, not the observer's.
	// Their span is how long the session has run, which is not how long this
	// process has been watching it.
	first  time.Time
	latest time.Time
	// trend is the most recent band crossing the projector reported, and when
	// it landed. Age grades how the view draws it.
	trend   render.TrendNote
	trendAt time.Time
	// event is the newest notable transition, when it landed, and how many
	// came before it.
	event     string
	eventAt   time.Time
	eventsAll int
}

// readTrends keeps the most recent band crossing the projector reported.
//
// The event carries the bands as words, so the view reads them straight from
// the log rather than re-deriving a judgement the projector already made.
func (l *liveness) readTrends(events []event.Event) {
	for _, e := range events {
		if e.Kind != event.KindTrendEmerged {
			continue
		}
		var p struct {
			Metric    string `json:"metric"`
			From      string `json:"from_band"`
			To        string `json:"to_band"`
			Direction string `json:"direction"`
		}
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		l.trend = render.TrendNote{
			Metric: p.Metric, From: p.From, To: p.To,
			Degrading: p.Direction == "degrading",
		}
		l.trendAt = time.Now()
	}
}

// trendNote is the trend with its age filled in at draw time.
func (l liveness) trendNote() render.TrendNote {
	n := l.trend
	if !l.trendAt.IsZero() {
		n.Age = time.Since(l.trendAt)
	}
	return n
}

// eventNote is the newest transition with its age and backlog.
func (l liveness) eventNote() render.EventNote {
	if l.event == "" {
		return render.EventNote{}
	}
	return render.EventNote{Text: l.event, Age: time.Since(l.eventAt), Earlier: l.eventsAll}
}

// elapsed is the span of the stream seen so far.
func (l liveness) elapsed() time.Duration {
	if l.first.IsZero() || l.latest.Before(l.first) {
		return 0
	}
	return l.latest.Sub(l.first)
}

// observe folds one record into the liveness state.
func (l *liveness) observe(rec stream.Record) {
	l.last = time.Now()
	if !rec.Timestamp.IsZero() {
		if l.first.IsZero() {
			l.first = rec.Timestamp
		}
		l.latest = rec.Timestamp
	}
	if name := rec.Metadata[stream.MetaStreamName]; name != "" {
		l.name = name
	}
	if rec.IsOperatorTurn() {
		l.records, l.waiting, l.seen = 0, true, true
		return
	}
	// Only the agent's own records and its tool traffic count as work coming
	// back. Harness bookkeeping arrives on the same stream and would otherwise
	// read as progress.
	if rec.SpeakerClass == stream.SpeakerAgent || rec.SpeakerClass == stream.SpeakerTool {
		l.records++
		l.waiting = false
	}
}

// drawState is the projector output one frame is drawn from.
type drawState struct {
	id         string
	regime     string
	snapshot   metrics.Snapshot
	trail      []string
	regimes    []string
	thresholds config.Thresholds
	moves      map[string]render.Move
	semantic   *classify.Operational
}

// drawLive paints one frame in whichever view is selected.
func drawLive(screen *panel.Screen, view display, live liveness, s drawState) {
	if view.micro {
		screen.Paint(render.Micro(render.MicroView{
			Regime:   s.regime,
			Snapshot: s.snapshot,
			Turn:     s.snapshot.TurnIndex,
			Trail:    s.regimes,
			Trend:    live.trendNote(),
			Event:    live.eventNote(),
			Width:    microWidth(view),
			Color:    view.color,
			Status: render.Status{
				Records:  live.records,
				Since:    time.Since(live.last),
				Elapsed:  live.elapsed(),
				Waiting:  live.waiting,
				Seen:     live.seen,
				Pulse:    live.pulse,
				Stopped:  live.stopped,
				Semantic: liveSemantic(s.semantic),
			},
		}))
		return
	}
	screen.Paint(render.Live(render.LiveView{
		Name:       live.name,
		StreamID:   s.id,
		Turn:       s.snapshot.TurnIndex,
		Regime:     s.regime,
		Snapshot:   s.snapshot,
		Trail:      s.trail,
		Thresholds: s.thresholds,
		Width:      liveWidth(view),
		Color:      view.color,
		Moves:      s.moves,
		Trend:      live.trendNote(),
		Event:      live.eventNote(),
		Status: render.Status{
			Records:  live.records,
			Since:    time.Since(live.last),
			Elapsed:  live.elapsed(),
			Waiting:  live.waiting,
			Seen:     live.seen,
			Pulse:    live.pulse,
			Stopped:  live.stopped,
			Semantic: liveSemantic(s.semantic),
		},
	}))
}

func liveSemantic(s *classify.Operational) *classify.Operational { return s }

// appendSemanticCompletions persists deferred semantic evidence without
// applying it to the live projector. A later strict replay can select one
// completion per source sequence; the fast heuristic projection stays intact.
func appendSemanticCompletions(session *store.Session, hybrid *classify.Hybrid, final bool) error {
	appendOne := func(c classify.Completion) error {
		e := event.NewWithIdentity(c.StreamID, c.Seq, c.Epoch, c.Source,
			event.KindSemanticCompleted, event.ClassClassified, c.JobID, c)
		return session.Append(e)
	}
	if final {
		for c := range hybrid.Results() {
			if err := appendOne(c); err != nil {
				return err
			}
		}
		return nil
	}
	for {
		select {
		case c, ok := <-hybrid.Results():
			if !ok {
				return nil
			}
			if err := appendOne(c); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

// liveWidth is the width the one-screen view is drawn at: the flag when it was
// given, otherwise the terminal, re-read every frame so a resize is followed.
func liveWidth(view display) int {
	if view.width > 0 {
		return view.width
	}
	return panel.TerminalWidth(os.Stdout)
}

// microWidth keeps the side pane narrow. The pane is meant to sit beside an
// agent IDE, so the terminal is a ceiling rather than a target: detection only
// narrows it, never widens it past the renderer's own default.
func microWidth(view display) int {
	if view.width > 0 {
		return view.width
	}
	if w := panel.TerminalWidth(os.Stdout); w > 0 && w < render.MicroDefaultWidth {
		return w
	}
	return 0
}

// arrival is one delivery from the live source.
type arrival struct {
	records []stream.Record
	err     error
}

// readLive moves the blocking read off the draw loop, so the view can repaint
// on a timer while the source is quiet.
//
// The goroutine holds no state and ends with the context: it either delivers
// what the tail returned or observes cancellation, never both.
func readLive(ctx context.Context, s harness.Live) <-chan arrival {
	out := make(chan arrival)
	go func() {
		defer close(out)
		for {
			records, err := s.Next(ctx)
			select {
			case out <- arrival{records: records, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return out
}

// watchStdin reads a piped stream. There is no HTTP ingest.
func watchStdin(ctx context.Context, cfg config.Config, root, harnessName string, cls classify.Classifier, streamID, sessionRoot string, force bool, view display) error {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("watch: read stdin: %w", err)
	}
	records, err := harness.DecodeBytes(harnessName, "stdin", sessionRoot, streamID, raw)
	if err != nil {
		return err
	}
	id := streamID
	if id == "" {
		id = streamIDOf(records, "stdin")
	}
	session, err := store.OpenSession(root, id, force)
	if err != nil {
		return err
	}
	projector := state.New(cfg, cls, id)
	for _, rec := range records {
		if err := session.AppendAll(projector.Push(ctx, rec)); err != nil {
			session.Close()
			return err
		}
	}
	if err := session.AppendAll(projector.Finish()); err != nil {
		session.Close()
		return err
	}
	if err := session.Close(); err != nil {
		return err
	}
	if err := session.WriteSource(store.Source{
		StreamID: id, Adapter: harnessName, Path: "stdin",
		Bytes: int64(len(raw)), Records: len(records), Offset: int64(len(raw)),
	}); err != nil {
		return err
	}
	if err := writeProjections(session.Dir); err != nil {
		return err
	}
	fmt.Print(render.Live(render.LiveView{
		StreamID:   id,
		Turn:       projector.Snapshot().TurnIndex,
		Regime:     string(projector.Regime()),
		Snapshot:   projector.Snapshot(),
		Thresholds: cfg.Thresholds,
		Width:      liveWidth(view),
		Color:      view.color,
		Status:     render.Status{Stopped: true},
	}))
	return nil
}

// stopWatch closes the observer's own files after a signal.
//
// It does not call Finish. The source did not end; this process was told to
// exit. Everything left open stays open, and the event log records the byte
// offset observation reached so a later watch can start there.
func stopWatch(session *store.Session, projector *state.Projector, path, adapterName string, offset int64, mark string) error {
	if err := session.AppendAll(projector.StopObservation(offset, "observer received an interrupt or termination signal")); err != nil {
		return err
	}
	if err := session.Close(); err != nil {
		return err
	}
	if err := session.WriteSource(store.Source{
		StreamID: projector.StreamID(), Adapter: adapterName, Path: path, Offset: offset,
	}); err != nil {
		return err
	}
	if err := writeProjections(session.Dir); err != nil {
		return err
	}
	fmt.Printf("\nobservation stopped at %s; state left open is unresolved, not concluded\n", mark)
	fmt.Printf("session %s\n", session.Dir)
	return nil
}

func report(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	var common commonFlags
	common.bind(fs)
	sessionID := fs.String("session", "", "stream identifier")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return errors.New("report: --session is required")
	}
	_, root, err := common.load()
	if err != nil {
		return err
	}
	loaded, err := render.Load(store.SessionDir(root, *sessionID))
	if err != nil {
		return err
	}
	fmt.Print(loaded.Report())
	return nil
}

func inspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	var common commonFlags
	common.bind(fs)
	sessionID := fs.String("session", "", "stream identifier")
	turn := fs.Uint64("turn", 0, "turn number")
	micro := fs.Bool("micro", false, "draw the side-pane meter as it stood at that turn")
	noColor := fs.Bool("no-color", false, "draw without ANSI attributes")
	width := fs.Int("width", 0, "side-pane inner width in columns")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" || *turn == 0 {
		return errors.New("inspect: --session and --turn are required")
	}
	_, root, err := common.load()
	if err != nil {
		return err
	}
	loaded, err := render.Load(store.SessionDir(root, *sessionID))
	if err != nil {
		return err
	}
	if *micro {
		out, err := loaded.MicroAt(*turn, *width, useColor(*noColor))
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	}
	out, err := loaded.Inspect(*turn)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

// streamIDOf prefers the identifier the source carries, falling back to the
// file name.
func streamIDOf(records []stream.Record, path string) string {
	for _, r := range records {
		if r.StreamID != "" {
			return r.StreamID
		}
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// trail keeps the recent state transitions shown under the live view.
type trail struct {
	max    int
	items_ []string
}

func newTrail() *trail { return &trail{max: 6} }

// readEvents keeps the newest notable transition and counts the rest.
func (l *liveness) readEvents(events []event.Event) {
	for _, e := range events {
		label, ok := trailLabels[e.Kind]
		if !ok {
			continue
		}
		if l.event != "" {
			l.eventsAll++
		}
		l.event, l.eventAt = label, time.Now()
	}
}

func (t *trail) add(events []event.Event) {
	for _, e := range events {
		label, ok := trailLabels[e.Kind]
		if !ok {
			continue
		}
		t.items_ = append(t.items_, label)
		if len(t.items_) > t.max {
			t.items_ = t.items_[len(t.items_)-t.max:]
		}
	}
}

func (t *trail) items() []string { return t.items_ }

// trailLabels name the transitions worth showing in one line.
var trailLabels = map[string]string{
	event.KindCorrectionCandidate:    "correction",
	event.KindObligationRepeated:     "obligation repeat",
	event.KindObligationReleased:     "obligation released",
	event.KindObligationRevived:      "obligation back",
	event.KindRepairOpened:           "repair open",
	event.KindRepairDeepened:         "repair deeper",
	event.KindRepairProvisionalClose: "repair closed",
	event.KindRepairDurableClose:     "repair durable",
	event.KindRepairReset:            "repair reset",
	event.KindEpochAdvanced:          "reset",
	event.KindExpansionCandidate:     "expansion",
}

// regimeTrail keeps the bounded regime history the side pane shows. A regime
// repeated on consecutive turns is one entry: the trail is a trajectory, not a
// sample count.
type regimeTrail struct {
	max    int
	items_ []string
}

func newRegimeTrail() *regimeTrail { return &regimeTrail{max: 4} }

func (t *regimeTrail) add(regime string) {
	if regime == "" {
		return
	}
	if n := len(t.items_); n > 0 && t.items_[n-1] == regime {
		return
	}
	t.items_ = append(t.items_, regime)
	if len(t.items_) > t.max {
		t.items_ = t.items_[len(t.items_)-t.max:]
	}
}

func (t *regimeTrail) items() []string { return t.items_ }
