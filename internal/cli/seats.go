package cli

// fugaro seats: the advisory seat estimate (subscriptions S4.1; rulings D439,
// D433, T14).
//
// A seat is a contract pricing rule of the Subscription Terms -- anyone who
// launches jobs, by any means (D433) -- and nothing in the product can count
// one: the licence's seat is a person, no surface knows who a person is, and
// the identities that are recorded are self-declared (F7, roles.go's
// untrusted recorded identity). So this command publishes the estimate the
// ruling asks for, from the installation's own core data and nothing else:
//
//   - the two IAM lists this machine's config holds, terraform.launchers and
//     terraform.operators: the members `fugaro setup` writes and `fugaro
//     update` applies (localcfg's member rule);
//   - the requesters of the runs the last 90 days minted, each run's
//     task.json requested_by. T14's "every launcher" is what makes this
//     source necessary rather than the launch records: an agent launch
//     records the configured person there, and an agent with no configured
//     identity is invisible (F7).
//
// It is core MIT: it reads only core data, sends nothing and stores nothing --
// this file has no write path of any kind, and no key, licence or server is
// consulted.
//
// Two properties shape it, and each has a test:
//
//   - An identity is counted once, however many sources name them, and the
//     sources are named beside them so a reader can see why. A person's row
//     is counted; an identity neither list can size is listed apart and
//     counted nowhere: a group: or domain: member names an unknown number of
//     people (the ruling's own words), and a serviceAccount: member is an
//     account, not a person.
//   - Nothing is guessed. The window is the bucket's own run IDs, so it
//     agrees with runs ls; an identity is the text a source wrote, folded for
//     comparison and printed as the key the estimate counts; a task.json this
//     read cannot use is said in the output rather than read as an identity.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// seatsWindow is how far back the estimate reads run requesters: the ruling's
// own 90 days (D439, T14).
const seatsWindow = 90 * 24 * time.Hour

// The three sources a row's SEEN AS column names, in the order every row
// lists them.
const (
	seatsLauncher  = "launcher"
	seatsOperator  = "operator"
	seatsRequester = "requester"
)

// The two reasons a member is listed AND NOT COUNTED. They are the
// document's own values, so a consumer reads the reason rather than matching
// the sentence a table prints beside it.
const (
	// seatsUnknownSize: a group: or domain: member names an unknown number of
	// people (T14: groups count as unknown). One seat is not a safe reading of
	// it, and neither is none.
	seatsUnknownSize = "unknown_size"
	// seatsAccount: a serviceAccount: member is one identity on the list and
	// no person, so a licensed seat -- the licence's "one person" -- is not
	// what it holds. It is listed, because the ruling's estimate counts every
	// launcher and a member dropped silently is the one outcome nobody can
	// check.
	seatsAccount = "account"
)

type seatsOptions struct {
	cloud  cloudOptions
	asJSON bool
}

// seatsPersonRow is one identity the estimate counts.
type seatsPersonRow struct {
	Person  string   `json:"person"`
	Sources []string `json:"sources"`
}

// seatsFlaggedRow is one identity on a list the estimate cannot count.
type seatsFlaggedRow struct {
	Member string `json:"member"`
	// Why is seatsUnknownSize or seatsAccount.
	Why     string   `json:"why"`
	Sources []string `json:"sources"`
}

// seatsDoc is fugaro seats --json's document.
type seatsDoc struct {
	Project string `json:"project"`
	// Days, Since and Until are the window the requesters were read over,
	// measured back from the clock runs ls reads its own window from.
	Days  int    `json:"days"`
	Since string `json:"since"`
	Until string `json:"until"`
	// People are the identities the estimate counts, one row each, and
	// NotCounted the ones it cannot: the two tables a human reader is shown.
	People     []seatsPersonRow  `json:"people"`
	NotCounted []seatsFlaggedRow `json:"not_counted"`
	// Unreadable counts the runs of the window whose task.json this read
	// could not use. Whoever requested those runs is counted nowhere, so the
	// estimate is a lower bound and says so.
	Unreadable int `json:"unreadable"`
	// Notes are the lines a human reader is shown above the tables.
	Notes []string `json:"notes"`
}

func newSeatsCmd() *cobra.Command {
	var o seatsOptions
	cmd := &cobra.Command{
		Use:   "seats",
		Short: "Estimate how many people a subscription's seat rule would count",
		Long: "Estimate how many people a subscription's seat rule would count on this\n" +
			"installation, from the installation's own data alone: the people on this\n" +
			"machine's launcher and operator lists, plus everyone who requested a run in the\n" +
			"last 90 days, each counted once.\n\n" +
			"The estimate is ADVISORY. A seat is a contract pricing rule of the Subscription\n" +
			"Terms -- anyone who launches jobs, by any means (console, mobile, CLI, an agent)\n" +
			"-- and nothing in the product can count one: the licence's seat is one person,\n" +
			"no surface knows who a person is, and every identity a run records is\n" +
			"self-declared. So this command reads and prints, and that is all: it stores\n" +
			"nothing, sends nothing, and consults no key, licence or server. It is core MIT\n" +
			"and reads only core data.\n\n" +
			"The three sources, and what each one is worth:\n\n" +
			"  - terraform.launchers and terraform.operators in this machine's own project\n" +
			"    config: the two lists fugaro setup writes and fugaro update applies. They are\n" +
			"    the operator's record of who may launch, and members the console or an agent\n" +
			"    launch records elsewhere are not in them.\n" +
			"  - The requesters of the runs the last 90 days minted, read from each run's\n" +
			"    task.json in every repository of the installation's bucket. Every launcher\n" +
			"    counts, an agent launch is recorded under the configured person, and an agent\n" +
			"    with no configured identity is invisible here.\n\n" +
			"What the estimate cannot tell, it lists and does not guess:\n\n" +
			"  - A group: or domain: member of either list names an unknown number of people.\n" +
			"    It is listed apart, with the lists that hold it, and counted nowhere: one\n" +
			"    seat is not a safe reading of a group, and neither is none.\n" +
			"  - A serviceAccount: member is one identity and no person, so it is listed and\n" +
			"    not counted: the licence's seat is one person, and this command never decides\n" +
			"    that a person holds a robot's account.\n" +
			"  - A run's requester is a value the launching CLI wrote down and nothing\n" +
			"    verifies, and an agent launch with no configured identity records none: the\n" +
			"    window counts whoever the records name.\n" +
			"  - The window is the last 90 days of run IDs, so a person whose runs are all\n" +
			"    older is invisible unless a list names them. The estimate over the whole\n" +
			"    installation is therefore a lower bound, never a ceiling.\n\n" +
			"One identity is one row. The lists and the runs spell an address\n" +
			"independently -- one is an IAM member a person was written to, the other is\n" +
			"what the launching CLI typed -- so the comparison folds case, and the row's key\n" +
			"is the text printed: two texts that print alike are one person, and the\n" +
			"estimate is the number of rows.\n\n" +
			"--json prints {project, days, since, until, people[], not_counted[],\n" +
			"unreadable, notes}; people carry {person, sources[]} and not_counted {member,\n" +
			"why, sources[]}, sources from launcher, operator and requester, and why is\n" +
			"unknown_size or account.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runSeats(cmd, &o) },
	}
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runSeats(cmd *cobra.Command, o *seatsOptions) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	// The window is measured from lsNow, the clock every run-window listing in
	// this package is measured from: a run ID carries its mint time, so the
	// window and the runs in it agree with runs ls, and one seam pins both
	// (runid_test.go).
	until := lsNow().UTC()
	since := until.Add(-seatsWindow)
	slugs, err := runstore.ListSlugs(ctx, env.bucket.Bucket)
	if err != nil {
		return remote(err)
	}
	requesters, unreadable, err := seatsRequesters(ctx, env, slugs, since)
	if err != nil {
		return err
	}
	doc := seatsDoc{
		Project:    env.lc.Name,
		Days:       int(seatsWindow / (24 * time.Hour)),
		Since:      since.Format("2006-01-02"),
		Until:      until.Format("2006-01-02"),
		Unreadable: unreadable,
	}
	doc.People, doc.NotCounted = seatsEstimate(env.lc.Terraform.Operators, env.lc.Terraform.Admins, requesters)
	return writeSeats(cmd.OutOrStdout(), doc, o.asJSON)
}

// seatsRequesters reads the requester each run of the window recorded: every
// repository of the installation's bucket, the runs the window minted, and
// each run's task.json requested_by. It returns the texts as they were
// recorded, empty ones left out -- a run that recorded no requester names
// nobody -- and the count of runs whose task.json could not be used.
//
// The read is the runs bucket's own listing plus one object per run, the same
// shape runs ls has, and it is unbounded the way runs ls is: the window is the
// ruling's 90 days and takes no flag, and an estimate that dropped runs of it
// silently would be a count of something nobody asked for.
//
// A run whose task.json is ABSENT is not unreadable: nothing recorded a
// requester for it. A task.json that is there and cannot be used is one run's
// problem, not the window's -- it never makes the estimate unproducible -- and
// it is counted so the output can say the estimate is a lower bound by that
// many runs, the way runs ls reports a corrupt object of one run.
//
// "Cannot be used" is the object and the spec, not only the encoding: the
// object is read raw and parsed here rather than through ReadTask's own error,
// so a task.json this build cannot DECODE -- a field a newer CLI wrote, which
// Parse's DisallowUnknownFields refuses, or a spec failing its own rules --
// is that run's unreadable task, exactly as runs ls reads it (runs.go: an
// unparseable task.json is the row that says so, and the listing goes on). The
// run's own agent can write this object (runs/<slug>/ is its account's), so
// one such object read as fatal would let a single run fail the estimate for
// the whole window.
func seatsRequesters(ctx context.Context, env *cloudEnv, slugs []string, since time.Time) (requesters []string, unreadable int, err error) {
	type ref struct{ slug, id string }
	var refs []ref
	for _, slug := range slugs {
		ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slug, since)
		if err != nil {
			return nil, 0, remote(err)
		}
		for _, id := range ids {
			refs = append(refs, ref{slug, id})
		}
	}
	found := make([]string, len(refs))
	bad := make([]bool, len(refs))
	errs := make([]error, len(refs))
	parallel(len(refs), func(i int) {
		raw, err := runstore.Open(env.bucket.Bucket, refs[i].slug, refs[i].id).ReadFile(ctx, "task.json")
		switch {
		case errors.Is(err, runstore.ErrNotFound):
			// No task.json: this directory holds no run to read a requester
			// from, which is not the same as one nobody can read.
		case err != nil:
			if corruptObject(err) {
				bad[i] = true
				return
			}
			errs[i] = err
		default:
			spec, perr := task.Parse(raw)
			if perr != nil {
				// The object is there and this build cannot use it: a decode
				// failure is this run's unreadable task, never the estimate's
				// failure.
				bad[i] = true
				return
			}
			found[i] = spec.RequestedBy
		}
	})
	for _, e := range errs {
		if e != nil {
			return nil, 0, remote(e)
		}
	}
	for i, who := range found {
		if who != "" {
			requesters = append(requesters, who)
		}
		if bad[i] {
			unreadable++
		}
	}
	return requesters, unreadable, nil
}

// seatsEstimate merges the three sources into the document's two tables. The
// input lists are read as they are written: a member text for the two config
// lists, and a run's own recorded text for a requester.
func seatsEstimate(launchers, operators, requesters []string) (people []seatsPersonRow, notCounted []seatsFlaggedRow) {
	peopleAt := map[string]int{}
	flaggedAt := map[string]int{}
	add := func(member, source string) {
		text, why := seatsIdentity(member)
		if text == "" {
			return
		}
		key := strings.ToLower(text)
		if why != "" {
			i, ok := flaggedAt[key]
			if !ok {
				notCounted = append(notCounted, seatsFlaggedRow{Member: text, Why: why})
				i = len(notCounted) - 1
				flaggedAt[key] = i
			}
			if !slices.Contains(notCounted[i].Sources, source) {
				notCounted[i].Sources = append(notCounted[i].Sources, source)
			}
			return
		}
		i, ok := peopleAt[key]
		if !ok {
			people = append(people, seatsPersonRow{Person: text})
			i = len(people) - 1
			peopleAt[key] = i
		}
		if !slices.Contains(people[i].Sources, source) {
			people[i].Sources = append(people[i].Sources, source)
		}
	}
	for _, m := range launchers {
		add(m, seatsLauncher)
	}
	for _, m := range operators {
		add(m, seatsOperator)
	}
	for _, r := range requesters {
		add(r, seatsRequester)
	}
	// One order of rows, so two installations with the same sources print the
	// same table however their lists happen to be written.
	slices.SortFunc(people, func(a, b seatsPersonRow) int {
		return strings.Compare(strings.ToLower(a.Person), strings.ToLower(b.Person))
	})
	slices.SortFunc(notCounted, func(a, b seatsFlaggedRow) int {
		return strings.Compare(strings.ToLower(a.Member), strings.ToLower(b.Member))
	})
	return people, notCounted
}

// seatsIdentity is member as the estimate reads it: the text a row is keyed
// and printed by, and why it is listed and not counted ("" for a person).
//
// The text is sanitised to one terminal-safe line and capped, so a member or
// a recorded requester can neither rewrite an operator's screen nor print
// something other than the key the estimate counted -- what a source wrote is
// untrusted text (roles.go), and this command decides nothing by it.
func seatsIdentity(member string) (text, why string) {
	safe := cellOneLine(member)
	kind, rest, ok := strings.Cut(safe, ":")
	if !ok {
		// A bare text: a run's requester as the launching CLI wrote it, which
		// fugaro run fills with the caller's own address. One identity, one
		// person.
		return clipRunes(safe, maxNameCellRunes), ""
	}
	switch kind {
	case "user":
		return clipRunes(rest, maxNameCellRunes), ""
	case "group", "domain":
		return clipRunes(safe, maxNameCellRunes), seatsUnknownSize
	case "serviceAccount":
		return clipRunes(safe, maxNameCellRunes), seatsAccount
	}
	// A text with a colon in it and a member rule localcfg does not admit:
	// localcfg's member rule is user:, group:, domain: and serviceAccount:
	// and nothing else, so this is not a member of either list. It is counted
	// as the one identity it prints as rather than given a reason nobody
	// wrote.
	return clipRunes(safe, maxNameCellRunes), ""
}

// seatsNotes is the lines a human reader is shown above the tables: what the
// estimate is, what its columns mean, and what the read left out.
func seatsNotes(doc seatsDoc) []string {
	notes := []string{
		"advisory: an estimate, never a count of seats or a bill: it reads this installation's own data alone (the launcher and operator lists of this machine's config, and the requester each run of the window recorded), and it stores nothing and sends nothing",
		"sources:  SEEN AS names why an identity counts: launcher (terraform.launchers), operator (terraform.operators), requester (a run of the window whose task.json recorded it)",
	}
	if doc.Unreadable > 0 {
		notes = append(notes, fmt.Sprintf("read:     %d run %s of the window could not be read (an object this build cannot use), so whoever requested %s is counted nowhere: the estimate is a lower bound by that much",
			doc.Unreadable, plural(doc.Unreadable, "task", "tasks"), plural(doc.Unreadable, "that run", "those runs")))
	}
	return notes
}

// seatsWhyText is a row's why as the WHY column prints it. Every value the
// document can carry has a sentence here; a value this build does not know is
// printed as it is rather than folded into one of the two, which would be the
// output inventing a reason.
func seatsWhyText(why string) string {
	switch why {
	case seatsUnknownSize:
		return "an unknown number of people"
	case seatsAccount:
		return "an account, not a person"
	}
	return oneLine(why)
}

func writeSeats(w io.Writer, doc seatsDoc, asJSON bool) error {
	doc.People, doc.NotCounted = seatsPeopleRows(doc.People), seatsFlaggedRows(doc.NotCounted)
	doc.Notes = seatsNotes(doc)
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	fmt.Fprintf(w, "project:  %s\n", doc.Project)
	fmt.Fprintf(w, "window:   %s to %s (the last %d days)\n", doc.Since, doc.Until, doc.Days)
	estimate := fmt.Sprintf("%d %s", len(doc.People), plural(len(doc.People), "person", "people"))
	if n := len(doc.NotCounted); n > 0 {
		estimate += fmt.Sprintf(", plus %d listed and not counted", n)
	}
	fmt.Fprintf(w, "estimate: %s\n", estimate)
	for _, line := range doc.Notes {
		fmt.Fprintln(w, multiLine(line))
	}
	fmt.Fprintln(w)
	if len(doc.People) == 0 {
		// An estimate with nobody in it is said plainly, not shown as an
		// empty table a reader could take for a failure.
		fmt.Fprintln(w, "no person is counted: neither list names one and no run of the window recorded a requester")
	} else {
		rows := make([]string, len(doc.People))
		for i, p := range doc.People {
			rows[i] = p.Person + "\t" + strings.Join(p.Sources, ", ")
		}
		seatsTable(w, "PERSON\tSEEN AS", rows)
	}
	if len(doc.NotCounted) > 0 {
		// The second table is what the estimate cannot count, and why: a
		// group's size is unknowable from here, and an account is not a
		// person. Both are listed, so nothing a reader might have expected to
		// count is silently missing.
		rows := make([]string, len(doc.NotCounted))
		for i, f := range doc.NotCounted {
			rows[i] = f.Member + "\t" + strings.Join(f.Sources, ", ") + "\t" + seatsWhyText(f.Why)
		}
		fmt.Fprintln(w)
		seatsTable(w, "NOT COUNTED\tSEEN AS\tWHY", rows)
	}
	return nil
}

// seatsTable prints rows under header, one tabwriter table whose cells are
// already joined by tabs.
func seatsTable(w io.Writer, header string, rows []string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, header)
	for _, r := range rows {
		fmt.Fprintln(tw, r)
	}
	tw.Flush()
}

// seatsPeopleRows and seatsFlaggedRows are rows, or the empty list for nil, so
// an estimate with nobody in it prints [] in the document rather than null
// (the same rule nonNil keeps for the string lists other documents carry).
func seatsPeopleRows(rows []seatsPersonRow) []seatsPersonRow {
	if rows == nil {
		return []seatsPersonRow{}
	}
	return rows
}

func seatsFlaggedRows(rows []seatsFlaggedRow) []seatsFlaggedRow {
	if rows == nil {
		return []seatsFlaggedRow{}
	}
	return rows
}
