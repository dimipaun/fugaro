package watch

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/runview"
)

// PlainOptions tune RenderPlain.
type PlainOptions struct {
	// ASCII replaces the warning sign and the middle dot with "!" and "|".
	ASCII bool
	// Repo, when set, is the repository name the view was narrowed to (the
	// project line then still shows the whole project).
	Repo string
	// PRNote is a one-line reason the ready list's pull request states could
	// not be read from the operator's gh; it is printed once, under the ready
	// section, and never carries the gh output itself.
	PRNote string
}

// RenderPlain writes v as one readable text frame: lines of words and
// numbers, no escape sequences and no cursor movement, so it is the same in a
// pipe, a log and a terminal. Meaning is in words (KILLED, SILENT, LOST, OVER,
// FAST, NOTIONAL), never in colour. Every string of v is already safe
// (Build sanitised it); nothing here adds a control character but newlines.
//
// ready is ReadyRowsOf the view before the finished-run filter hid anything
// (design generic-tool §10.1): a ready PR stays listed past --keep and
// --keep-count, which bound how much history the rest of the frame shows,
// not what still needs a look. The caller resolves it, since by the time
// RenderPlain runs, v may already be filtered and the run's own block gone.
// The caller also hands it through PRState first, so a pull request the gh of
// the operator's machine says is merged or closed is not listed here at all.
func RenderPlain(w io.Writer, project string, v View, ready []ReadyRow, o PlainOptions) error {
	warn, sep := "⚠", " · "
	if o.ASCII {
		warn, sep = "!", " | "
	}
	var b strings.Builder
	fmt.Fprintf(&b, "project %s%smode %s%sday %s%s%s\n", project, sep, v.Mode, sep, dashIfEmpty(v.Day), sep, ConnText(v.Conn, warn))
	if v.Conn.Kind != ConnLive && v.Conn.Kind != ConnPolling && v.Conn.Age > 0 {
		fmt.Fprintf(&b, "%s the figures below are the last ones received, %s ago\n", warn, durText(v.Conn.Age))
	}
	if v.QueuedNote != "" {
		fmt.Fprintf(&b, "%s %s\n", warn, v.QueuedNote)
	}

	p := v.Project
	fmt.Fprintf(&b, "total  %s%s%s%s\n", barText(p.Bar, p.Counted, warn), sep, spendText(p.Spent, p.Notional), burnSuffix(p.Burn, sep, warn))
	fmt.Fprintf(&b, "       %d runs%s%.1f run-hours\n", p.Runs, sep, p.RunHours)
	if p.Kill.On {
		fmt.Fprintf(&b, "       %s\n", killText(p.Kill, warn, "PROJECT KILLED"))
	}
	if o.Repo != "" {
		fmt.Fprintf(&b, "(narrowed to repository %s)\n", o.Repo)
	}

	for _, r := range v.Repos {
		fmt.Fprintf(&b, "\n%s  %s%s%s%s\n", r.Name, barText(r.Bar, r.Counted, warn), sep, spendText(r.Spent, r.Notional), burnSuffix(r.Burn, sep, warn))
		if r.Kill.On {
			fmt.Fprintf(&b, "  %s\n", killText(r.Kill, warn, "KILLED"))
		}
		if len(r.Runs) > 0 {
			tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "  RUN\tTITLE\tSTAGE\tROUND\tAGE\tSPENT\tBY\tFLAGS")
			for _, run := range r.Runs {
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", run.Run, run.Title, run.Stage, run.Round, ageText(run), runSpend(run), launcherText(run), flags(run, warn))
			}
			tw.Flush()
		}
		if len(r.Finished) > 0 {
			ftw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
			fmt.Fprintln(ftw, "  FINISHED\tOUTCOME\tAGE\tBY\tPR")
			for _, f := range r.Finished {
				fmt.Fprintf(ftw, "  %s\t%s\t%s\t%s\t%s\n", f.Run, finishedOutcomeText(f), ageText(f), launcherText(f), dashIfEmpty(f.PRURL))
			}
			ftw.Flush()
		}
	}
	if len(v.Repos) == 0 {
		b.WriteString("\nno repository has spend, runs or a kill switch today\n")
	}
	if len(ready) > 0 {
		fmt.Fprintf(&b, "\nReady for your review (%d)\n", len(ready))
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, item := range ready {
			fmt.Fprintf(tw, "  %s\t#%d\t%s\t%s\n", item.Repo, item.Run.PRNumber, dashIfEmpty(item.Run.PRURL), dash(item.Run.Title))
		}
		tw.Flush()
	}
	if o.PRNote != "" {
		fmt.Fprintf(&b, "%s\n", o.PRNote)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// finishedOutcomeText is a finished run's status for the plain table: its
// status word, plus the outcome when it is ready, a draft that passed
// review, or an artifact run (whose deliverable is in the runs bucket and
// which opens no pull request, so its bare stage word would mislead)
// (design generic-tool §10.1, artifact-runs.md §3).
func finishedOutcomeText(f RunRow) string {
	word := strings.ToUpper(f.Stage)
	switch f.Outcome {
	case "ready":
		word = "READY FOR REVIEW"
	case "draft":
		word += ", passed review"
	case "artifacts":
		word = "ARTIFACTS, NO PR"
	}
	return word
}

// ConnText is the connection as the header says it. warn is the warning
// sign ("⚠" or "!").
func ConnText(c Connection, warn string) string {
	switch c.Kind {
	case ConnStale:
		return fmt.Sprintf("%s live data stale (%s)", warn, durText(c.Age))
	case ConnOffline:
		return fmt.Sprintf("%s OFFLINE (reconnecting, %s)", warn, c.Reason)
	case ConnRefused:
		return warn + " access refused: you need the Viewer role"
	case ConnPolling:
		return "polling"
	}
	return "live"
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// launcherText is a row's BY cell: who started the run, in the short form a
// column has room for (the part before the @ of an address, the whole value for
// anything else — a service account name, a CI runner), or "-" for a run whose
// launch.json recorded no launcher. Several people start runs on one project at
// once, so the column is always there, and the dash is a fact ("not recorded"),
// never a placeholder for one.
//
// run.LaunchedBy is already sanitised (safetext.Field, in the row's own
// builder and again in MergeFacts) and already capped at safetext.FieldMax, so
// shortening it here cannot unsanitise it: LauncherShort only drops the domain
// of an address.
func launcherText(run RunRow) string {
	by := runview.LauncherShort(run.LaunchedBy)
	if r := []rune(by); len(r) > launcherCellRunes {
		by = string(r[:launcherCellRunes]) + "…"
	}
	return dashIfEmpty(by)
}

// launcherCellRunes bounds the BY cell, as fugaro runs ls bounds its own
// (maxNameCellRunes): the sanitiser's cap is far wider than a column.
const launcherCellRunes = 64

// USD is an amount for people: cents, or four places below a cent.
func USD(m budget.Micros) string {
	if m > 0 && m < 10_000 {
		return fmt.Sprintf("$%.4f", m.USD())
	}
	return fmt.Sprintf("$%.2f", m.USD())
}

// warnPercent is budget.WarnShare as the percentage a Bar carries. Every
// surface that prints a cap reads it: the colour (render.go's pctColour) and
// the warning marker both frames write.
const warnPercent = budget.WarnShare * 100

// capWarn is the warning marker a cap figure carries at or past warnPercent.
// The interactive screen and the plain frames both write it, since neither
// colour nor the colour-capable screen is always there; the rounded figure is
// not the test — 79.9% draws "(80%)" and no mark.
func capWarn(pct float64, warn string) string {
	if pct >= warnPercent {
		return " " + warn
	}
	return ""
}

func barText(b Bar, counted budget.Micros, warn string) string {
	if !b.Has {
		return "counted " + USD(counted) + ", no cap"
	}
	return fmt.Sprintf("counted %s of %s (%.0f%%%s)", USD(b.Used), USD(b.Cap), b.Percent, capWarn(b.Percent, warn))
}

func spendText(spent, notional budget.Micros) string {
	s := "spent " + USD(spent)
	if notional > 0 {
		s += " + " + USD(notional) + " NOTIONAL (subscription, not capped)"
	}
	return s
}

func burnSuffix(b Burn, sep, warn string) string {
	if !b.Known {
		return sep + "burn ..."
	}
	s := fmt.Sprintf("%sburn %s/min", sep, USD(b.PerMin))
	if b.Fast {
		s += " " + warn + " FAST"
	}
	return s
}

func killText(k KillState, warn, what string) string {
	at := "-"
	if !k.At.IsZero() {
		at = k.At.UTC().Format("2006-01-02 15:04Z")
	}
	if k.Unreadable {
		return fmt.Sprintf("%s %s %s", warn, what, k.Reason)
	}
	return fmt.Sprintf("%s %s by %s at %s %q", warn, what, dashIfEmpty(k.By), at, k.Reason)
}

func durText(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func ageText(r RunRow) string {
	if !r.HasAge {
		return "-"
	}
	return durText(r.Age)
}

func runSpend(r RunRow) string {
	if !r.HasSpent {
		return "-"
	}
	if r.Notional {
		return USD(r.Spent) + " NOTIONAL"
	}
	return USD(r.Spent)
}

func flags(r RunRow, warn string) string {
	if r.Queued {
		if r.Stuck {
			return fmt.Sprintf("%s not started after %d min", warn, int(r.Age/time.Minute))
		}
		return "QUEUED"
	}
	var f []string
	// Waiting leads: it is the reason the run is doing no work at all, and
	// the one flag that says when it will (D152's "waiting, expected resume
	// time").
	if r.Waiting {
		f = append(f, "waiting for a credential, expected resume "+stampText(r.WaitingUntil))
	}
	switch r.Health {
	case HealthSilent:
		f = append(f, "SILENT")
	case HealthLost:
		f = append(f, "LOST (sweeper pending)")
	}
	switch r.Deadline {
	case DeadlineNear:
		f = append(f, warn+" near deadline")
	case DeadlineOver:
		f = append(f, "OVER deadline")
	}
	if r.Halted != "" {
		f = append(f, "halted: "+r.Halted)
	}
	return strings.Join(f, ", ")
}
