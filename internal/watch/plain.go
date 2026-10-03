package watch

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// PlainOptions tune RenderPlain.
type PlainOptions struct {
	// ASCII replaces the warning sign and the middle dot with "!" and "|".
	ASCII bool
	// Repo, when set, is the repository name the view was narrowed to (the
	// project line then still shows the whole project).
	Repo string
}

// RenderPlain writes v as one readable text frame: lines of words and
// numbers, no escape sequences and no cursor movement, so it is the same in a
// pipe, a log and a terminal. Meaning is in words (KILLED, SILENT, LOST, OVER,
// FAST, NOTIONAL), never in colour. Every string of v is already safe
// (Build sanitised it); nothing here adds a control character but newlines.
func RenderPlain(w io.Writer, project string, v View, o PlainOptions) error {
	warn, sep := "⚠", " · "
	if o.ASCII {
		warn, sep = "!", " | "
	}
	var b strings.Builder
	fmt.Fprintf(&b, "project %s%smode %s%sday %s%s%s\n", project, sep, v.Mode, sep, dashIfEmpty(v.Day), sep, ConnText(v.Conn, warn))
	if v.Conn.Kind != ConnLive && v.Conn.Kind != ConnPolling && v.Conn.Age > 0 {
		fmt.Fprintf(&b, "%s the figures below are the last ones received, %s ago\n", warn, durText(v.Conn.Age))
	}

	p := v.Project
	fmt.Fprintf(&b, "total  %s%s%s%s\n", barText(p.Bar, p.Counted), sep, spendText(p.Spent, p.Notional), burnSuffix(p.Burn, sep, warn))
	fmt.Fprintf(&b, "       %d runs%s%.1f run-hours\n", p.Runs, sep, p.RunHours)
	if p.Kill.On {
		fmt.Fprintf(&b, "       %s\n", killText(p.Kill, warn, "PROJECT KILLED"))
	}
	if o.Repo != "" {
		fmt.Fprintf(&b, "(narrowed to repository %s)\n", o.Repo)
	}

	for _, r := range v.Repos {
		fmt.Fprintf(&b, "\n%s  %s%s%s%s\n", r.Name, barText(r.Bar, r.Counted), sep, spendText(r.Spent, r.Notional), burnSuffix(r.Burn, sep, warn))
		if r.Kill.On {
			fmt.Fprintf(&b, "  %s\n", killText(r.Kill, warn, "KILLED"))
		}
		if len(r.Runs) == 0 {
			continue
		}
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  RUN\tTITLE\tSTAGE\tROUND\tAGE\tSPENT\tFLAGS")
		for _, run := range r.Runs {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n", run.Run, run.Title, run.Stage, run.Round, ageText(run), runSpend(run), flags(run, warn))
		}
		tw.Flush()
	}
	if len(v.Repos) == 0 {
		b.WriteString("\nno repository has spend, runs or a kill switch today\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
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

// USD is an amount for people: cents, or four places below a cent.
func USD(m budget.Micros) string {
	if m > 0 && m < 10_000 {
		return fmt.Sprintf("$%.4f", m.USD())
	}
	return fmt.Sprintf("$%.2f", m.USD())
}

func barText(b Bar, counted budget.Micros) string {
	if !b.Has {
		return "counted " + USD(counted) + ", no cap"
	}
	return fmt.Sprintf("counted %s of %s (%.0f%%)", USD(b.Used), USD(b.Cap), b.Percent)
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
	var f []string
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
