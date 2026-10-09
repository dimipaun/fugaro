package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// logsLookback is how far before the launch logs reads: the launch time is
// the CLI's clock, the entries' timestamps Cloud Run's.
const logsLookback = time.Minute

type logsOptions struct {
	cloud          cloudOptions
	follow, asJSON bool
	url            bool
}

// logLine is one entry as logs --json prints it.
type logLine struct {
	Time     time.Time `json:"time"`
	Severity string    `json:"severity"`
	Stage    string    `json:"stage,omitempty"`
	Stream   string    `json:"stream,omitempty"`
	Event    string    `json:"event,omitempty"`
	Message  string    `json:"message"`
}

func newLogsCmd() *cobra.Command {
	var o logsOptions
	cmd := &cobra.Command{
		Use:   "logs RUN",
		Short: "Print a run's logs from Cloud Logging",
		Long: "Print the logs of RUN (<repo-slug>/<run-id>, or a bare run ID), oldest first.\n\n" +
			"With -f, keep printing until the execution ends and its logs settle. Every\n" +
			"line is redacted of the credentials in this environment, on top of the\n" +
			"runner's own redaction.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runLogs(cmd, &o, args[0]) },
	}
	f := cmd.Flags()
	f.BoolVarP(&o.follow, "follow", "f", false, "keep printing new entries until the execution ends")
	f.BoolVar(&o.asJSON, "json", false, "print one JSON object per entry")
	f.BoolVar(&o.url, "url", false, "print the execution's Cloud console link instead of the logs")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runLogs(cmd *cobra.Command, o *logsOptions, ref string) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	_, l, _, err := locateLaunched(ctx, env, ref)
	if err != nil {
		return err
	}
	if o.url {
		if o.follow || o.asJSON {
			return userErr("--url prints only the link: it doesn't go with --follow or --json")
		}
		if l.LogURL == "" {
			return userErr("no console link was recorded for this run's execution")
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), oneLine(l.LogURL))
		return err
	}
	red := agent.RedactFunc(cliSecrets(os.Getenv)) // forms built once per command
	out := cmd.OutOrStdout()
	n := 0
	err = env.be.Logs(ctx, logQuery(l, o.follow), func(e backend.LogEntry) error {
		n++
		return printLogEntry(out, redactEntry(e, red), o.asJSON)
	})
	if err != nil && !(o.follow && errors.Is(err, context.Canceled)) {
		return remote(fmt.Errorf("reading the logs: %w", err))
	}
	if err == nil && n == 0 {
		emptyViewHint(cmd.ErrOrStderr(), env, l.LogURL)
	}
	return nil
}

// emptyViewHint says, when a read through the Fugaro log view found
// nothing, that a run from before log isolation logged only to _Default,
// which the view doesn't cover. url, when known, is the execution's Cloud
// console link, named so the hint points somewhere that still has the logs.
func emptyViewHint(w io.Writer, env *cloudEnv, url string) {
	if env.lc.LogView == "" {
		return
	}
	msg := "note: no log entries in the Fugaro log view. A run from before log isolation logged only to the project's _Default bucket, which fugaro logs and diagnose no longer read; the Cloud Run console's page for its execution shows them."
	if url != "" {
		msg += " The Cloud console's page for this execution: " + oneLine(url)
	}
	fmt.Fprintln(w, msg)
}

// logQuery reads l's execution from shortly before its launch.
func logQuery(l *runstore.Launch, follow bool) backend.LogQuery {
	q := backend.LogQuery{Execution: l.Execution, Follow: follow}
	if !l.LaunchedAt.IsZero() {
		q.Since = l.LaunchedAt.Add(-logsLookback)
	}
	return q
}

// locateLaunched resolves ref and returns its store, its launch as the
// views see it (ownerLaunch) and its "<slug>/<run-id>". A run that never
// launched is a user error.
func locateLaunched(ctx context.Context, env *cloudEnv, ref string) (*runstore.Store, *runstore.Launch, string, error) {
	slug, id, err := locateRun(ctx, env, ref)
	if err != nil {
		return nil, nil, "", err
	}
	s := runstore.Open(env.bucket.Bucket, slug, id)
	l, err := ownerLaunch(ctx, env, s, id)
	if err != nil {
		return nil, nil, "", err
	}
	if l == nil || l.Execution == "" {
		return nil, nil, "", userErr("run %s/%s was never launched (see fugaro run --retry)", slug, id)
	}
	return s, l, slug + "/" + id, nil
}

// ownerLaunch is the run's launch as the views must see it: the
// execution result.json names, when set, since that is the one that owns
// the run (after a double launch, launch.json may name the duplicate);
// else launch.json's. nil means never launched. Unlike
// existingLaunch it never writes: it serves read-only views. An execution
// that is not of the run's own job is an error, never followed: the run's
// service account can write the objects that name it.
func ownerLaunch(ctx context.Context, env *cloudEnv, s *runstore.Store, id string) (*runstore.Launch, error) {
	l, err := absent(s.ReadLaunch(ctx))
	if err != nil {
		return nil, remote(err)
	}
	rec, err := absent(s.ReadRecord(ctx))
	if err != nil {
		return nil, remote(err)
	}
	owner := l
	if rec != nil && rec.Execution != "" {
		o := runstore.Launch{Version: 1, RunID: id, LaunchedAt: rec.StartedAt}
		if l != nil {
			o = *l
		}
		o.Execution = rec.Execution
		owner = &o
	}
	if owner == nil || owner.Execution == "" {
		return owner, nil
	}
	spec, _ := s.ReadTask(ctx) // unreadable: checkExecution refuses
	if err := env.checkExecution(owner.Execution, s.Slug(), spec); err != nil {
		return nil, remote(fmt.Errorf("run %s/%s: %w: %w", s.Slug(), id, errNotFollowing, err))
	}
	return owner, nil
}

// errNotFollowing marks ownerLaunch's refusal of an execution the run's
// objects name but checkExecution won't follow (another job, another
// region).
var errNotFollowing = errors.New("not following its execution")

// cliSecrets are the credential values in the CLI's own environment, the
// platform's reserved secrets' variables, which the runner redacts too.
// The CLI never sees a run's other secrets; the runner redacted those
// before publishing, so this is defence in depth.
func cliSecrets(getenv func(string) string) []string {
	var out []string
	for _, name := range config.ReservedSecrets {
		if v := getenv(name); v != "" {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}

// redactEntry redacts e's message and the string fields logs prints with
// red, an agent.RedactFunc built once per command.
func redactEntry(e backend.LogEntry, red func(string) string) logLine {
	field := func(k string) string {
		s, _ := e.Fields[k].(string)
		return red(s)
	}
	return logLine{Time: e.Time, Severity: red(e.Severity), Stage: field("stage"),
		Stream: field("stream"), Event: field("event"), Message: red(e.Message)}
}

// printLogEntry prints l as one JSON line, or as
// "15:04:05 INFO    [implement/agent] message".
func printLogEntry(w io.Writer, l logLine, asJSON bool) error {
	if asJSON {
		data, err := json.Marshal(l)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s\n", data)
		return err
	}
	_, err := fmt.Fprintln(w, humanLogLine(l))
	return err
}

// humanLogLine is l as logs prints it, in local time.
func humanLogLine(l logLine) string {
	tag := l.Stage
	if l.Stream == "agent" || l.Stream == "verify" {
		if tag == "" {
			tag = l.Stream
		} else {
			tag += "/" + l.Stream
		}
	}
	if tag != "" {
		tag = "[" + tag + "] "
	}
	sev := l.Severity
	if sev == "" {
		sev = "DEFAULT"
	}
	// A message's own newlines are indented, so it can't forge an entry.
	msg := strings.ReplaceAll(multiLine(l.Message), "\n", "\n    ")
	return fmt.Sprintf("%s %-7s %s%s", l.Time.Local().Format("15:04:05"), oneLine(sev), oneLine(tag), msg)
}
