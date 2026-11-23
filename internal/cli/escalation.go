package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro escalation (docs/outer-loop-escalation, E13): what the ladder has done
// to the project's stalled pull requests, read-only.
//
// The ladder's ledger lives on each work item of the project's orchestrator
// state (E7), so this family reads that node and nothing writes it: the read
// goes through orchestrator.ReadState, which is the read half of the node's
// client and holds no lease and no ETag, so this command has no way to change
// the node even by mistake (D1241's fence, kept by the type rather than by a
// promise).
//
// The ledger holds the rung, its reason, the counter the ladder keeps and the
// transitions. It holds no money and no clock of the item's own: cost and time
// per pull request come from the item's own run records (D2038, F7), which are
// the runs the state's intents name, read out of the installation's runs
// bucket. A run of the item's this release cannot read makes both figures
// unknown rather than a smaller sum: a figure missing one of its terms reads as
// a cheaper, younger item than the one a person is looking at (D1049's rule at
// this surface).

// escalationDocVersion is the version of the documents this family prints with
// --json (schemas/escalation.schema.json). A field never changes meaning across
// it: a new one is additive, and a renamed one is a new version.
const escalationDocVersion = 1

// escalationOptions are the two subcommands' flags.
type escalationOptions struct {
	cloudOptions
	// name is --name: whose orchestrator's state the ladder's ledger is read
	// out of (D3206, D3207). Empty is the project's own.
	name string
	json bool
}

func newEscalationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "escalation",
		Short: "Read what the escalation ladder has done to the project's pull requests",
		Long: "Read the project's escalation ladder: the rung each stalled pull request has\n" +
			"reached, why it got there, how long it has been going, what it has cost and\n" +
			"every rung transition it took (docs/outer-loop-escalation).\n\n" +
			"ls lists every work item the ladder has acted on; show prints one of them in\n" +
			"full, its transitions oldest first. A work item is the pull request the\n" +
			"project's orchestrator is driving, so a row here is one pull request.\n\n" +
			"Both commands only read: the ledger is the daemon's to write, and this command\n" +
			"holds no lease, no ETag and no write path to the state node. The age and the\n" +
			"cost are the item's own run records read out of the runs bucket, which is\n" +
			"where the ladder's own reading of them comes from too; a run this machine\n" +
			"cannot read leaves both unknown rather than short.",
	}
	cmd.AddCommand(newEscalationLsCmd(), newEscalationShowCmd())
	return cmd
}

func newEscalationLsCmd() *cobra.Command {
	var o escalationOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the pull requests the escalation ladder has acted on",
		Long: "List every work item of the project's orchestrator that carries an escalation\n" +
			"ledger, with the rung it is at, the reason it entered it, how long it has been\n" +
			"going, how many of the ladder's own runs it has spent and what its own runs\n" +
			"have cost.\n\n" +
			"--json prints a versioned document (schemas/escalation.schema.json). An item\n" +
			"whose runs this machine cannot all read has no age and no cost, which the\n" +
			"document writes as null and the table as unknown.\n\n" +
			"--name reads one of the project's named orchestrators instead of the project's\n" +
			"own: `--name night` reads the ledger of `<project>~night`'s items.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runEscalationLs(cmd, &o) },
	}
	f := cmd.Flags()
	addOrchestratorNameFlag(cmd, &o.name)
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

func newEscalationShowCmd() *cobra.Command {
	var o escalationOptions
	cmd := &cobra.Command{
		Use:   "show ITEM",
		Short: "Show one pull request's escalation ledger and its transitions",
		Long: "Show the escalation ledger of one work item of the project's orchestrator,\n" +
			"named by the id `fugaro orchestrator state` prints it under: the rung, the\n" +
			"reason it entered it, how long it has been going, the ladder runs it has\n" +
			"spent, what its own runs have cost, and every transition oldest first, with\n" +
			"the head each one was decided at.\n\n" +
			"--json prints a versioned document (schemas/escalation.schema.json). An item\n" +
			"whose runs this machine cannot all read has no age and no cost, which the\n" +
			"document writes as null and the table as unknown.\n\n" +
			"--name reads one of the project's named orchestrators instead of the project's\n" +
			"own: `--name night` reads an item of `<project>~night`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runEscalationShow(cmd, &o, args[0]) },
	}
	f := cmd.Flags()
	addOrchestratorNameFlag(cmd, &o.name)
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

// escalationItem is one pull request's ledger as both documents carry it: the
// item, the rung it is at, the reason it entered it, and the item's own runs
// read as two figures.
//
// Reason is the last transition's, which is why the item is at the rung it is
// at; the transitions carry every reason before it.
type escalationItem struct {
	ID     string            `json:"id"`
	Repo   string            `json:"repo"`
	Rung   escalation.Rung   `json:"rung"`
	Reason escalation.Reason `json:"reason"`
	// EnteredAt is when the item entered that rung, on the server's clock, as
	// the ledger's own stamp. The last transition's At, so the two cannot
	// disagree.
	EnteredAt string `json:"entered_at"`
	// AgeSeconds is how long the item has been going: the server's now minus
	// the earliest start among the item's own run records (D2038). Null when
	// one of those records could not be read, and null when none of them
	// carries a start, and never a smaller figure.
	AgeSeconds *int64 `json:"age_seconds"`
	// OuterAttempts is how many of the ladder's own runs the item has spent —
	// rung 0's rebases, the one rung 2 pass, the review of a rebased head and
	// the judge — and never a normal run of its recipe.
	OuterAttempts int `json:"outer_attempts"`
	// CostUSD is what the item's own runs have billed, the run records' own
	// figure (D2038). Null when one of those records could not be read, and
	// never a smaller sum.
	CostUSD *float64 `json:"cost_usd"`
	// Transitions is the ladder's history for the item, oldest first.
	Transitions []escalationTransition `json:"transitions"`
}

// escalationTransition is one rung move as both documents carry it: the rung
// the item left, the one it entered, why, when, and the head it was decided at.
type escalationTransition struct {
	From escalation.Rung `json:"from,omitempty"`
	To   escalation.Rung `json:"to"`
	// Reason is one of escalation.Reasons: a code, never a sentence, so the
	// ledger holds no finding text and no model (G8).
	Reason  escalation.Reason `json:"reason"`
	At      string            `json:"at"`
	HeadSHA string            `json:"head_sha,omitempty"`
}

// escalationLsDoc is the document `ls --json` prints.
type escalationLsDoc struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	Project string `json:"project"`
	// Now is the database server's clock the ages were measured against, so a
	// reader of the document can check the figures rather than trust them
	// (D1230: this machine's own clock is not what a stamp here may depend on).
	Now   string           `json:"now"`
	Items []escalationItem `json:"items"`
}

// escalationShowDoc is the document `show --json` prints.
type escalationShowDoc struct {
	Version int            `json:"version"`
	Kind    string         `json:"kind"`
	Project string         `json:"project"`
	Now     string         `json:"now"`
	Item    escalationItem `json:"item"`
}

// escalationView is what both subcommands read before they print: the
// orchestrator id `--name` resolved to, that id's state node, the server's clock
// and the runs bucket the item figures come from.
type escalationView struct {
	id     string
	state  orchestrator.State
	now    time.Time
	bucket *blob.Bucket
	env    *cloudEnv
}

// close releases the bucket the item figures were read out of. The view owns it
// rather than the reader that opened it, because the reads happen after the
// opener has returned.
func (v *escalationView) close() { v.env.Close() }

func runEscalationLs(cmd *cobra.Command, o *escalationOptions) error {
	ctx := cmd.Context()
	v, err := openEscalation(ctx, o.cloudOptions, o.name)
	if err != nil {
		return err
	}
	defer v.close()
	doc := escalationLsDoc{Version: escalationDocVersion, Kind: "ls", Project: v.id, Now: escalationStamp(v.now),
		Items: []escalationItem{}}
	for _, w := range v.state.WorkItems {
		if w.Escalation == nil {
			continue
		}
		item, err := v.item(ctx, w)
		if err != nil {
			return err
		}
		doc.Items = append(doc.Items, item)
	}
	slices.SortFunc(doc.Items, func(a, b escalationItem) int { return strings.Compare(a.ID, b.ID) })
	w := cmd.OutOrStdout()
	if o.json {
		return writeJSON(w, doc)
	}
	if len(doc.Items) == 0 {
		fmt.Fprintln(w, "no pull request has entered the escalation ladder")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ITEM\tREPO\tRUNG\tREASON\tAGE\tATTEMPTS\tCOST")
	for _, it := range doc.Items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", safetext.Field(it.ID), safetext.Field(it.Repo),
			it.Rung, it.Reason, escalationAgeText(it.AgeSeconds), it.OuterAttempts, escalationCostText(it.CostUSD))
	}
	return tw.Flush()
}

func runEscalationShow(cmd *cobra.Command, o *escalationOptions, id string) error {
	ctx := cmd.Context()
	v, err := openEscalation(ctx, o.cloudOptions, o.name)
	if err != nil {
		return err
	}
	defer v.close()
	w, ok := v.state.Item(id)
	if !ok {
		return userErr("no work item %s in the project's orchestrator: fugaro escalation ls lists the pull requests the ladder has acted on", safetext.Field(id))
	}
	if w.Escalation == nil {
		return userErr("work item %s has never entered the escalation ladder, so it has no rung and no transitions: fugaro escalation ls lists the pull requests that have", safetext.Field(id))
	}
	item, err := v.item(ctx, w)
	if err != nil {
		return err
	}
	doc := escalationShowDoc{Version: escalationDocVersion, Kind: "show", Project: v.id, Now: escalationStamp(v.now), Item: item}
	out := cmd.OutOrStdout()
	if o.json {
		return writeJSON(out, doc)
	}
	fmt.Fprintf(out, "item %s (%s): rung %s, reason %s, entered %s\n",
		safetext.Field(item.ID), safetext.Field(item.Repo), item.Rung, item.Reason, item.EnteredAt)
	fmt.Fprintf(out, "age %s, ladder runs %d, cost %s\n",
		escalationAgeText(item.AgeSeconds), item.OuterAttempts, escalationCostText(item.CostUSD))
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "AT\tFROM\tTO\tREASON\tHEAD")
	for _, tr := range item.Transitions {
		from := string(tr.From)
		if from == "" {
			from = "-"
		}
		head := tr.HeadSHA
		if head == "" {
			head = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", tr.At, from, tr.To, tr.Reason, head)
	}
	return tw.Flush()
}

// openEscalation reads the three things both subcommands print from: the
// project's config, the state node of the orchestrator `--name` resolved to, and
// the database's own clock.
//
// The node is read through orchestrator.ReadState, which holds no ETag and no
// lease: the reader has no write to make even if a later change here wanted
// one, which is what "read-only" means at this surface rather than a promise in
// a comment (D1241's reader, O16b's).
func openEscalation(ctx context.Context, o cloudOptions, name string) (*escalationView, error) {
	lc, db, err := openBudget(ctx, o)
	if err != nil {
		return nil, err
	}
	id, err := orchestratorIDFor(lc, name)
	if err != nil {
		return nil, err
	}
	state, err := orchestrator.ReadState(ctx, db, id, orchestrator.RoleOrchestrator)
	if err != nil {
		return nil, dbErr(lc, err, false)
	}
	now, ok := db.ServerNow()
	if !ok {
		return nil, userErr("the database answered without its own clock, and the age of an escalated pull request is measured against the server's time rather than this machine's (D1230)")
	}
	// The bucket is opened the way the daemon opens it for its own ladder
	// reading: the installation's own runs bucket, checked to be this project's
	// (openCloudFor's checkCloudName), so a misconfigured bucket_url reads no
	// other installation's records.
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		return nil, err
	}
	return &escalationView{id: id, state: state, now: now.UTC(), bucket: env.bucket.Bucket, env: env}, nil
}

// item is one work item's ledger as the documents carry it.
func (v *escalationView) item(ctx context.Context, w orchestrator.WorkItem) (escalationItem, error) {
	e := w.Escalation
	item := escalationItem{
		ID: w.ID, Repo: w.Repo, Rung: e.Rung, EnteredAt: escalationStamp(e.EnteredAt),
		OuterAttempts: e.OuterAttempts,
		Transitions:   make([]escalationTransition, 0, len(e.Transitions)),
	}
	// The last transition is why the item is where it is, and the ledger's own
	// rule is that a ledger carries at least one.
	if n := len(e.Transitions); n > 0 {
		item.Reason = e.Transitions[n-1].Reason
	}
	for _, tr := range e.Transitions {
		item.Transitions = append(item.Transitions, escalationTransition{
			From: tr.From, To: tr.To, Reason: tr.Reason, At: escalationStamp(tr.At), HeadSHA: tr.HeadSHA,
		})
	}
	spend, err := v.spend(ctx, w)
	if err != nil {
		return escalationItem{}, err
	}
	if spend.read {
		cost := spend.cost
		item.CostUSD = &cost
		// A start after the server's now is a clock skew between the machine
		// that wrote the record and the database; it floors at zero rather than
		// reporting an item younger than nothing (the rule runstore's own
		// chainSeconds keeps). An item none of whose records carries a start
		// holds no age at all, and a zero would read as one that just began.
		if !spend.started.IsZero() {
			age := max(0, int64(v.now.Sub(spend.started)/time.Second))
			item.AgeSeconds = &age
		}
	}
	return item, nil
}

// escalationSpend is what one item's own runs say: the sum of what they billed
// and the earliest moment one of them started, or read false when one of them
// could not be read at all.
type escalationSpend struct {
	read    bool
	cost    float64
	started time.Time
}

// spend reads the item's own run records: the runs the state's intents name for
// it, which is the item's own recipe's attempts and not the ladder's runs — a
// run of another recipe declares no intent and lives in the ledger alone
// (E11).
//
// One record that cannot be read makes the whole reading unknown rather than a
// sum missing a term: the item's age and cost are what a person reads to judge
// whether the ladder is working, and a figure short by one run is the one wrong
// answer this command could give (D1049, D1051 at this surface).
func (v *escalationView) spend(ctx context.Context, w orchestrator.WorkItem) (escalationSpend, error) {
	out := escalationSpend{read: true}
	for _, runID := range itemRunIDs(v.state, w) {
		slug, run, err := runstore.Locate(ctx, v.bucket, runID)
		if err != nil {
			if errors.Is(err, runstore.ErrNotFound) {
				out.read = false
				continue
			}
			return escalationSpend{}, remote(err)
		}
		rec, err := runstore.Open(v.bucket, slug, run).ReadRecord(ctx)
		if err != nil {
			if errors.Is(err, runstore.ErrNotFound) {
				out.read = false
				continue
			}
			return escalationSpend{}, remote(err)
		}
		if rec == nil {
			out.read = false
			continue
		}
		out.cost += escalationBilled(rec)
		if !rec.StartedAt.IsZero() && (out.started.IsZero() || rec.StartedAt.Before(out.started)) {
			out.started = rec.StartedAt.UTC()
		}
	}
	return out, nil
}

// escalationBilled is what one run's record billed, as the reports read it: the
// record's cost breakdown total, which leaves out what a subscription paid, and
// the record's one model figure for a run that died before its cost settled and
// has no breakdown (internal/digest's billed, D1046).
func escalationBilled(rec *runstore.Record) float64 {
	if rec.Cost == nil {
		return rec.CostUSD
	}
	return rec.Cost.TotalUSD
}

// itemRunIDs are the runs the state records for one item: the run ids of its
// intents, oldest attempt first, and the run the item's own `run_id` names.
// They are deduplicated and ordered, so two readings of one node read one set
// of records and the figures cannot wobble between runs of this command.
func itemRunIDs(s orchestrator.State, w orchestrator.WorkItem) []string {
	var ids []string
	for _, it := range s.Intents {
		if orchestrator.IntentItem(it.ID) != w.ID || it.RunID == "" {
			continue
		}
		ids = append(ids, it.RunID)
	}
	if w.RunID != "" {
		ids = append(ids, w.RunID)
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// escalationStamp is a stamp of this document: second-precision UTC RFC 3339,
// the shape every --json document in this CLI writes and the schema pins.
func escalationStamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// escalationAgeText is an age as the tables print it. An unknown age is said
// rather than shown as a zero, which would read as "just started".
func escalationAgeText(seconds *int64) string {
	if seconds == nil {
		return "unknown"
	}
	return (time.Duration(*seconds) * time.Second).String()
}

// escalationCostText is a cost as the tables print it, and unknown for the same
// reason.
func escalationCostText(usd *float64) string {
	if usd == nil {
		return "unknown"
	}
	return fmt.Sprintf("$%.2f", *usd)
}
