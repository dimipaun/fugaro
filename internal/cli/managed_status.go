package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro managed status (the desktop console's managed screens, D1472 to
// D1477) is the read those screens are built on: one CLOSED document about one
// project, holding what the two marks say and the bounded subset of the newest
// audit record.
//
// Why a command of its own rather than two members on doctor's document
// (D1473). doctor's --json is a list of checks whose shape is
// id/ok/problem/fix, and there is no room in it for records; a screen wants one
// document about one project, which is what coordinator status --json already
// is for the coordinator. Adding members to doctorOutput would also change a
// document every other consumer reads, for a screen's benefit.
//
// Why the read's own facts and not a sentence (D1472). The managed state is
// available today only as prose: doctor's managed-audit check carries problem
// and fix sentences, and a screen that parsed them would break on the next
// rewording of either — silently, drawing a wrong state rather than nothing.
// So every fact a screen draws is a field here, computed from the same read
// (managed.ReadMarkFacts, the one reader of the marker) rather than by a
// second one.
//
// Why the marks' Version, Reason and Generation are all exposed (D1474).
// managed.MarkFacts already carries them: the version is what a reader compares
// a project's creation fact against later, the generation is what makes "the
// same project, later" checkable, and the reason is the read's own sentence for
// a project that is NOT managed — the answer a screen owes an operator who
// expected one, and one the CLI has already computed.
//
// Why the trail is a bounded subset and never argv (D1476). argv is the argv
// the write ran as: a path, a root name or a title the operator did not mean to
// publish, on a surface anyone at the machine can read. The subset is the five
// fields that answer "who admitted what" — the time, the command, the verdict,
// the identity and the plan digest — and it is also what makes a row stable
// across CLI versions.
//
// Why two days and not a date control (D1477). The read is the newest record of
// the current UTC day, plus the previous day when that day holds nothing: the
// shape doctor's check already reads, so a session that ran an apply twenty
// minutes before midnight is still the last one an operator reads about. A date
// the operator picks would be an argument on the console's own argv row, a new
// bound on the read and an empty state for a day with nothing in it, for a
// trail this document can already print.
//
// Why the last update's retirement is in this document (F21, D1484, D1485).
// The managed screens' Project tab draws what the last update retired by itself
// and the runs it walked past while it did, and nothing else on the machine
// remembers that once the run is over: the summary is printed once and the plan
// file is overwritten by the next update. The record of it is CM1's
// (localcfg's update.json, in the state directory beside the remembered
// checkouts), and this is where a screen reads it — through the argv the marks
// are already read with, so a console spawns nothing extra for it, which is what
// F21 means by filing the record as one of the managed screens' subjects. What
// is carried is the record's own entry, member for member, with the record's
// version and the state of a record that could not be read beside it.
//
// What it never says (D1475). "Revoked" is a run-time fact of a write gate and
// is persisted nowhere; a project whose label or marker was taken off is "not
// managed", which is a different claim. A state the CLI does not have must not
// be drawn as though it did, and a screen is not the place to invent one.
//
// What it is not. It is a read of the caller's own credentials, so a coding
// agent's session runs it with no refusal (a writer is refused for what it
// changes), and it needs no checkout to answer, since --project and --config
// name the installation. It writes no cloud object and keeps no cache of its
// own. It IS one of the reads the desktop console spawns, and that is a file
// as much as a row: selectProject resolves the working directory as a checkout
// on the way to the installation, and the resolution remembers the checkout it
// ran in (recordCheckout, project.go) unless the command's own path stands on
// consoleReadPaths — which this command's does, added by the managed screens'
// CM3 with the design's §5.1 rule 6 row that names this argv, so a console
// paint of this document records no checkout and the operator's state file
// keeps the directory a person opened rather than one the console resolved on
// its way here. The roles a request for this document needs are the runs
// bucket's viewer and the project's viewer, the ones every read of the marks
// already takes. And it is not a tamper-proof trail: the bucket is unversioned
// and any project-level storage writer can rewrite an object in it.

// managedStatusNow is the day the trail is read about and the clock the read's
// own two-day window is measured from. It is a package variable for the reason
// doctorManagedAuditNow is: a record is filed under its own UTC date, so a test
// that wants a record read back has to be able to say which day it filed it
// under, and one that let the wall clock decide would flake at UTC midnight.
var managedStatusNow = time.Now

// managedStatusDoc is the document: the marks' own facts, the trail, and the
// machine's own record of the project's last update.
//
// The shape is CLOSED, and a consumer reads it that way: a member this build
// does not write is not in it, and a field whose meaning has to change gets a
// new name rather than a second meaning under the old one (the rule every wire
// shape in this program keeps). The absent-or-empty members below say what
// their emptiness means, one by one, because a missing figure read as a zero is
// the failure this document exists to avoid.
type managedStatusDoc struct {
	// Project and GCPProject are the installation the read was aimed at, as
	// the selected project config names them.
	Project    string `json:"project"`
	GCPProject string `json:"gcp_project"`
	// Managed is the marks' answer: true only when the creation fact object and
	// the project's own label agree (managed.ReadMarkFacts).
	Managed bool `json:"managed"`
	// Reason is the read's own sentence for a project that is not managed, and
	// "" when it is. It is bounded text, already sanitised by the read.
	Reason string `json:"reason"`
	// Version is the creation fact's version (1, 2 or 3), and 0 when the read
	// believed no object — which is a fact about this project and not an
	// unknown about the read: an unmanaged project has no version.
	Version int `json:"version"`
	// Generation is the creation fact object's own generation, in decimal, and
	// "" when the read placed no object. It is a string and not a number
	// because the record spells it the same way (managed's own auditGeneration)
	// and because the two facts it carries are different ones: the object's
	// generations start at 1, so a 0 here would read as a generation.
	Generation string `json:"generation"`
	// Audit is the bounded subset of the newest record of the window, and null
	// when the window holds no record — or when the trail was not read at all,
	// which is the unmanaged project below.
	Audit *managedStatusAudit `json:"audit"`
	// AuditDay is the UTC day audit_count counts and audit belongs to, spelled
	// as the family spells a date directory (2006-01-02). It is "" exactly when
	// no record was found in the window, or when the project is not managed and
	// the trail was not read: a day with nothing in it is no day to name.
	AuditDay string `json:"audit_day"`
	// AuditCount is how many records are filed under AuditDay, and 0 in both of
	// the cases above.
	AuditCount int `json:"audit_count"`
	// AuditState is the sentence for a read that did not answer, and "" for one
	// that did. It is what tells an unread trail from an empty day — the two
	// look identical in a count, and only one of them is a reason to say so —
	// and it is set only when the project IS managed: a project that is not
	// managed files no record at all (D1025), so there is no trail to read and
	// no state to report about it.
	AuditState string `json:"audit_state"`
	// Retirements is what the last plain `fugaro update` of this project
	// retired by itself, from the machine's own record of that update (CM1,
	// localcfg.UpdateProject), and null when this machine has recorded no
	// update for the project. Null is not "nothing was retired": a record that
	// DID retire nothing is an entry whose `retire` is `[]`, and the two are
	// different facts about the machine (D1484) — see RetirementsState for the
	// third answer.
	Retirements *managedStatusRetirements `json:"retirements"`
	// RetirementsState is the sentence for a record that could not be read, and
	// "" for one that was. It is AuditState's own rule one subject over, and
	// for the same reason: a record that is corrupt, over the bound, not a
	// regular file or of a version this build does not write leaves no entry to
	// show, and a screen that read that absence as "no update recorded yet"
	// would make a claim about the machine from a failure to read it. Unlike
	// AuditState it is not gated on the marks: this record is written by a
	// plain update of any project, managed or not.
	RetirementsState string `json:"retirements_state"`
}

// managedStatusRetirements is the retirement half of the document: one
// project's entry in the machine's record of the last update, with the record's
// own version beside it.
//
// The entry is the RECORD'S OWN type, embedded rather than retyped into a
// second struct with the same nine tags: that second struct is the pair that
// drifts silently, and what the console reads here is a document this program
// already writes (D1485 — a member added to the record must arrive here or the
// console has been told nothing about it, and the docs test that walks this
// shape is what makes that a failure rather than a surprise). The version is
// what says which record it was read from, and it is neither of the record's
// own members: a consumer compares it against the version it knows before it
// draws a row.
type managedStatusRetirements struct {
	// Version is the version of the record this entry came from
	// (localcfg.UpdateRecordVersion today). A record of another version does
	// not reach a consumer as an entry at all — it is RetirementsState — so
	// this member is the one a reader has when it has an entry.
	Version int `json:"version"`
	// UpdateProject is the project's own entry: the time its retirement
	// settled, the set it retired (`[]` for one that retired nothing, never
	// null) and the runs the registry held while it did — absent when the
	// number is not known, and never a zero in its place (D1483).
	localcfg.UpdateProject
}

// managedStatusAudit is the bounded subset of one audit record (D1476): the
// time, the command, the verdict, the identity and the plan digest.
//
// The shape is five fields BECAUSE argv is not one of them, and that is the
// whole of the bound: the record itself carries the argv the write ran as, the
// rules the gate fired and the marks that were in force, and none of them
// reaches a surface anybody at the machine can read.
type managedStatusAudit struct {
	// At is when the command ran, in UTC and as RFC 3339, from the record's own
	// time rather than from the listing's order.
	At string `json:"at"`
	// Command is the command as a person types it ("fugaro config publish"),
	// which is the record's own name for it and not its argv.
	Command string `json:"command"`
	// Verdict is the gate's own answer about the judged plan: "admitted" or
	// "refused". A refusal is a record an owner reads most, so it is shown
	// beside an admission and never instead of it.
	Verdict string `json:"verdict"`
	// Identity is the account the write ran as: the deployer account for a
	// managed apply, the operator's own elsewhere.
	Identity string `json:"identity"`
	// PlanDigest is the sha256 of the exact document the gate judged, so a
	// reader can compare the record against a plan rather than believe a
	// summary of it.
	PlanDigest string `json:"plan_digest"`
}

// newManagedStatusCmd is `fugaro managed status`.
func newManagedStatusCmd() *cobra.Command {
	var o cloudOptions
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print a managed project's marks and its newest audit record",
		Long: `Print whether the selected project is managed, and what its audit trail
records.

This is the read a screen of a managed project is built on: one closed document
about one project, holding the two marks' own answer and the bounded subset of
the newest audit record — never a sentence to be parsed, whose next rewording
would change what a screen draws.

The document also carries this machine's record of the project's last plain
"fugaro update": what that update retired by itself, when it settled the
retirement and how many runs the registry held while it did, with the record's
own version beside it — or a sentence instead of the entry when that record
cannot be read, so a record that is corrupt is never drawn as a project nothing
was recorded for. It is a local read, and it is made whatever the marks answer:
a project that is not managed runs updates too.

It reads the same two facts a managed apply reads, and changes no cloud object:
whether the project is managed is the creation fact in its runs bucket and the
fugaro_managed label on the project itself, which have to agree, and the trail
is the records the managed commands file under gs://<runs bucket>/fugaro/managed-audit/.

The trail is the newest record of the current UTC day, and of the previous day
when today holds nothing, with the day and that day's count beside it. A day
whose records could not be read is said rather than shown as an empty one: the
two are different facts and only one of them is a reason to look further. A
project that is not managed files no record at all, so its trail is not read and
nothing is reported about it — and a project whose marker or label was removed
is "not managed", never "revoked", which is a run-time decision of the gate and
is recorded nowhere.

What it prints is bounded on purpose: the record's time, its command, the gate's
verdict, the account the write ran as and the plan digest. The record's argv is
never printed, by this command or by anything that reads this document: it holds
paths and titles an operator did not mean to publish.

--json prints the document, which the desktop console reads as bytes: see the
desktop console's JSON contract, section 6.21, for every member and what its
emptiness means.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return managedStatus(cmd, o, asJSON)
		},
	}
	addCloudFlags(cmd, &o)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

// managedStatus is newManagedStatusCmd's body. The order is the reads': who is
// answering (a project that cannot be selected is refused before anything opens
// the cloud), what the marks say, and only then the trail — which is read for a
// managed project alone.
func managedStatus(cmd *cobra.Command, o cloudOptions, asJSON bool) error {
	ctx := cmd.Context()
	// A credential leak first, before anything opens the cloud: GODEBUG is
	// read before main runs and cannot be unset in time (refuseHTTP2Debug).
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return err
	}
	for _, w := range lc.Warnings() {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", safetext.Field(w))
	}
	doc := managedStatusDoc{Project: lc.Name, GCPProject: lc.GCPProject}
	s, c, err := managedStatusClients(ctx, lc)
	if err != nil {
		return err
	}
	// The one reader of the marker (managed.ReadMarkFacts): whether this
	// project is managed is a fact about the project, proven by the bucket's
	// project number and its label, and never about the machine running this.
	// A read that could not be made at all is an error and not an answer — the
	// document's whole subject would be unknown — so it exits non-zero rather
	// than printing a document that says nothing.
	marks, err := managed.ReadMarkFacts(ctx, s, c, lc.Name, lc.GCPProject)
	if err != nil {
		return remote(fmt.Errorf("reading the creation fact of project %s in GCP project %s: %s",
			safetext.Field(lc.Name), safetext.Field(lc.GCPProject), safetext.Field(err.Error())))
	}
	doc.Managed, doc.Reason, doc.Version = marks.Managed, marks.Reason, marks.Version
	doc.Generation = managedStatusGeneration(marks.Generation)
	if marks.Managed {
		// The trail is read only here, and that is the design and not an
		// optimisation: a managed project is the only one that files a record
		// (D1025), so an object left in the bucket for a project whose marks
		// do not say so is not a fact about that project. Reading it anyway
		// would let a trail outlive the marks that justify it.
		doc.AuditDay, doc.AuditCount, doc.AuditState, doc.Audit = managedStatusTrail(ctx, s, lc.RunsBucketName(), managedStatusNow().UTC())
	}
	doc.Retirements, doc.RetirementsState = managedStatusRetirementsOf(lc.Name)
	return managedStatusPrint(cmd.OutOrStdout(), doc, asJSON)
}

// managedStatusRetirementsOf is the retirement half of the document, read from
// the machine's own record of the last update (CM1, localcfg.LoadUpdateRecord).
//
// It is a LOCAL read, and it is made whatever the marks answer: the record is
// written by a plain `fugaro update` of any project, and a project that is not
// managed ran one too. It reaches no cloud object, which is also why it is the
// one subject of this document that a machine with no cloud at all can answer.
//
// The three answers are kept apart because they are three facts. An entry is
// the record and its version. Null with no state is "this machine has recorded
// no update for the project": a record file that is not there is an empty
// document and not an error (LoadUpdateRecord's own rule), so a machine that
// has never updated has nothing to report and nothing wrong. Null WITH a state
// is a record that is there and could not be read — JSON this build cannot
// parse, a byte count over the bound, a file that is not a regular file, or a
// version this build does not write — and drawing that as the first would be a
// claim about the machine made out of a failure to read it, which is D1483's
// rule for the runs and AuditState's for the trail.
//
// The record's spelling of an empty set is `[]`, and a nil one is normalised to
// it rather than passed on as JSON null: the console reads this member as a list
// or as a shape it does not know, and "the last update retired nothing" is a set
// that was written.
func managedStatusRetirementsOf(project string) (*managedStatusRetirements, string) {
	rec, err := localcfg.LoadUpdateRecord(os.Getenv)
	if err != nil {
		path, pathErr := localcfg.UpdateRecordPath(os.Getenv)
		if pathErr != nil {
			path = localcfg.UpdateRecordFile
		}
		return nil, "could not read the last update's record at " + safetext.Field(path) + ": " + oneLineCLI(err.Error())
	}
	up, ok := rec.Projects[project]
	if !ok {
		return nil, ""
	}
	if up.Retire == nil {
		up.Retire = []localcfg.UpdateRetirement{}
	}
	return &managedStatusRetirements{Version: rec.Version, UpdateProject: up}, ""
}

// managedStatusClients are the two clients this read needs and no others:
// Storage for the creation fact and the trail, Resource Manager for the
// project's own label and its number. They are built from the installation's
// own endpoints when it is a fake (a test's) and from Google's otherwise, the
// way the other readers of the same two facts build them.
//
// A no_auth installation that names no storage or Resource Manager endpoint has
// no cloud to read the marks from at all: there is nothing to be managed there,
// and the sentence says which two fields are missing rather than sending a call
// to Google.
func managedStatusClients(ctx context.Context, lc *localcfg.Config) (*storage.Service, *crm.Service, error) {
	if lc.Endpoints.NoAuth && (lc.Endpoints.Storage == "" || lc.Endpoints.ResourceManager == "") {
		return nil, nil, userErr("this installation sets no_auth and names no storage or Resource Manager endpoint, so there is no cloud to read project %s's marks from: set endpoints.storage and endpoints.resource_manager in its project config, or run this against an installation",
			safetext.Field(lc.Name))
	}
	s, err := storage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Storage)...)
	if err != nil {
		return nil, nil, remote(err)
	}
	c, err := crm.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.ResourceManager)...)
	if err != nil {
		return nil, nil, remote(err)
	}
	return s, c, nil
}

// managedStatusGeneration is the creation fact's generation as the document
// spells it: the number in decimal, and "" for one nothing read. The rule is
// the audit record's own (managed's auditGeneration), and for its reason: an
// object's generations start at 1, so a 0 would be read as a generation rather
// than as the absence of one.
func managedStatusGeneration(g int64) string {
	if g <= 0 {
		return ""
	}
	return strconv.FormatInt(g, 10)
}

// managedStatusTrail is the trail half of the document: the newest record of
// today, or of yesterday when today holds nothing (D1477), with the day and the
// count beside it — and a state sentence, never an empty day, for a read that
// did not answer.
//
// The records are parsed by managed.ListAuditRecords, the reader the record's
// own writer ships: a second parse here would be the pair that drifts silently,
// printing an empty column for a field the writer added and a wrong figure for
// one whose meaning moved.
func managedStatusTrail(ctx context.Context, s *storage.Service, bucket string, now time.Time) (day string, count int, state string, last *managedStatusAudit) {
	if bucket == "" {
		return "", 0, "the audit records could not be read: this installation's project config names no runs bucket, and the trail of a managed project is filed under gs://fugaro-runs-<gcp_project>", nil
	}
	today := managedmode.AuditDate(now)
	records, err := managedmode.ListAuditRecords(ctx, s, bucket, today)
	if err != nil {
		return "", 0, managedStatusUnread(bucket, today, err), nil
	}
	day, found, dayRecords := today, newestAuditRecord(records), records
	if found == nil {
		yesterday := managedmode.AuditDate(now.AddDate(0, 0, -1))
		older, err := managedmode.ListAuditRecords(ctx, s, bucket, yesterday)
		if err != nil {
			return "", 0, managedStatusUnread(bucket, yesterday, err), nil
		}
		if found = newestAuditRecord(older); found != nil {
			day, dayRecords = yesterday, older
		}
	}
	if found == nil {
		// Two days read, nothing filed in either, and no error: an ordinary
		// project's ordinary day. The day member stays empty rather than naming
		// today, so a consumer never draws a count against a day the read has
		// nothing to say about.
		return "", 0, "", nil
	}
	return day, len(dayRecords), "", &managedStatusAudit{
		At:      found.At.UTC().Format(time.RFC3339),
		Command: safetext.Field(found.Command),
		Verdict: string(found.Verdict),
		// The record is an object somebody else can rewrite — the bucket is
		// unversioned and any project-level storage writer reaches it — so its
		// text is bounded on the way into the document. The digest is not: the
		// parser refuses a record whose digest is not the sha256 it claims.
		Identity:   safetext.Field(found.Identity),
		PlanDigest: found.PlanDigest,
	}
}

// managedStatusUnread is what a trail that could not be listed says: the day it
// was asked about, the bucket, and the read's own bounded sentence. It is the
// one shape that must not be an empty day — "nothing was recorded" and "the
// records could not be read" are told apart by this member and by nothing else.
func managedStatusUnread(bucket, day string, err error) string {
	return "could not read the managed audit records of gs://" + safetext.Field(bucket) + " for " + safetext.Field(day) + ": " + oneLineCLI(err.Error())
}

// managedStatusPrint writes the document, or the lines a person reads.
//
// The two renderings carry the same bound: neither ever prints the record's
// argv, and the text form names the account and the plan digest because an
// operator at a terminal is asking who admitted what. The retirement half is
// the document's alone (the update that wrote the record printed its own
// summary when it ran), so no line here is added or changed by it: what a person
// reads from this command is the marks and the trail.
func managedStatusPrint(w io.Writer, doc managedStatusDoc, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	name, gcp := doc.Project, doc.GCPProject
	if name == "" {
		name = "(no project)"
	}
	if gcp == "" {
		gcp = "(no GCP project)"
	}
	if !doc.Managed {
		fmt.Fprintf(w, "%s (%s): not managed\n", safetext.Field(name), safetext.Field(gcp))
		if doc.Reason != "" {
			fmt.Fprintf(w, "  %s\n", doc.Reason)
		}
		return nil
	}
	fmt.Fprintf(w, "%s (%s): managed\n", safetext.Field(name), safetext.Field(gcp))
	generation := doc.Generation
	if generation == "" {
		generation = "unknown"
	}
	fmt.Fprintf(w, "  creation fact: version %d, generation %s\n", doc.Version, safetext.Field(generation))
	switch {
	case doc.AuditState != "":
		fmt.Fprintf(w, "  %s\n", doc.AuditState)
	case doc.Audit == nil:
		// The window is named, and not "no managed apply has been recorded":
		// this read has looked at two days and no further, and a project whose
		// last apply is a week old would be told none was ever recorded.
		fmt.Fprint(w, "  no managed agent apply recorded in the last two days\n")
	default:
		fmt.Fprintf(w, "  last apply: %s %s (%s, %s)\n",
			safetext.Field(doc.Audit.At), safetext.Field(doc.Audit.Command), safetext.Field(doc.Audit.Verdict), safetext.Field(doc.Audit.Identity))
		fmt.Fprintf(w, "  plan digest: %s\n", doc.Audit.PlanDigest)
		fmt.Fprintf(w, "  %s on %s\n", auditRecordCount(doc.AuditCount), safetext.Field(doc.AuditDay))
	}
	return nil
}
