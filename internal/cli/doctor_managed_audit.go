package cli

import (
	"context"
	"fmt"
	"time"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// doctorManagedAudit is the check M7 adds (D1014, spec §6): the last managed
// agent apply and the day's count of records, read through the ONE parser the
// audit family has, so what this prints and what an object holds cannot drift.
//
// Why the reader is not written here. managed.ListAuditRecords is the reader
// the record's own writer ships beside, and a second parse in the surface that
// prints would be the pair that drifts silently: a field the writer adds and
// this check never sees prints as nothing, and a field whose meaning moved
// prints a wrong figure with no error. doctor calls the parser and renders what
// it returns.
//
// Why a project that is not managed gets no check at all, rather than a line
// saying no apply was recorded. A managed project is the only one that writes a
// record (D1025): its ordinary typed confirmations are already a person's
// decision at a terminal, and a log of them is a data path this design does not
// create. So "no managed apply was recorded" is a sentence about a state that
// cannot exist in an unmanaged project, and printing it for every installation
// would put a pointer line on a clean screen — the thing the owner's rule of
// 2026-10-31 forbids. The marks are read first for the same reason the commands
// in the gate's set read them: whether this project is managed is a fact about
// the project and never about the machine running doctor.
//
// Why a read that failed is a warning and never an empty day. "Could not list
// the records" and "there are none" look identical in an output that prints a
// count, and the first is the state an owner must not read as the second: a
// managed project whose records cannot be read is a project whose trail is
// unknown. It carries the unread marker, so no view files it as a measurement.
//
// What this check is NOT, and where the rest of D1014's read is owed. spec §6
// gives `fugaro report` a section listing the records of the period it already
// covers, read through this same parser. That section is a HANDOFF to the
// report's own owner and is deliberately not in this file: the report is an
// `ee` command (ee/report) that reaches core only through internal/cli/api.go,
// and api.go is reserved by the 2026-10-30 rulings — the reader is reachable
// (managed.ListAuditRecords is exported) but the *storage client* a moved
// command would call it with is not, and this task adds no symbol there. The
// report's owner adds one (a *storage.Service beside Cloud.RunsBucket, or a
// Cloud method over ListAuditRecords) and writes the section; nothing about the
// parser has to change for it.

// doctorManagedAuditNow is the day the check reads and the day it measures the
// last apply's age against. It is a package variable for the reason budgetNow
// and managedGateNow are: a record is filed under its own UTC date, so a test
// that wants a record read back has to be able to say which day it filed it
// under, and one that let the wall clock decide would flake at UTC midnight.
var doctorManagedAuditNow = time.Now

// doctorManagedAuditID is the check's ID. It is a measurement when nothing is
// wrong — an admission and a refusal alike are the gate working, not a fault —
// so it prints under --all and in --json, and the default view neither prints
// nor counts it.
const doctorManagedAuditID = "managed-audit"

// doctorManagedAudit is the check: nil when there is nothing it can say about
// this installation (a test configuration with no storage endpoint, a project
// that is not managed), and otherwise one check that never fails doctor on the
// state it reports — only on a read that did not answer.
func doctorManagedAudit(ctx context.Context, lc *localcfg.Config, c *crm.Service) *doctorCheck {
	if lc.Endpoints.NoAuth && lc.Endpoints.Storage == "" {
		// Fakes only, and no fake for Cloud Storage: an empty endpoint would
		// send the call to Google. The same guard doctorRunsBucketIAM makes.
		return nil
	}
	bucket := lc.RunsBucketName()
	if bucket == "" {
		// No bucket to list the family in: the read's own name for it is
		// derived from the GCP project, and an installation whose config names
		// neither has nothing for this check to read.
		return nil
	}
	s, err := storage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Storage)...)
	if err != nil {
		return doctorManagedAuditUnread(bucket, "the audit records could not be read: "+oneLineCLI(err.Error()))
	}
	marks, err := managed.ReadMarkFacts(ctx, s, c, lc.Name, lc.GCPProject)
	if err != nil {
		return doctorManagedAuditUnread(bucket, fmt.Sprintf("could not read the creation fact of %s, so whether this project is managed — and so whether a managed apply has ever been recorded — could not be checked: %s",
			pluginwire.Printable(lc.Name), oneLineCLI(err.Error())))
	}
	if !marks.Managed {
		return nil
	}
	now := doctorManagedAuditNow().UTC()
	today := managedmode.AuditDate(now)
	records, err := managedmode.ListAuditRecords(ctx, s, bucket, today)
	if err != nil {
		return doctorManagedAuditUnread(bucket, "could not read the managed audit records of gs://"+bucket+": "+oneLineCLI(err.Error()))
	}
	last := newestAuditRecord(records)
	if last == nil {
		// Yesterday is read too, and only when today holds nothing: the last
		// managed apply is the thing this check exists to name, and a session
		// that ran it twenty minutes before UTC midnight is still the last one
		// an owner reads about. The count stays the day's own, which is the
		// question "did anything run today" and not a second one about the
		// record shown.
		yesterday := managedmode.AuditDate(now.AddDate(0, 0, -1))
		older, err := managedmode.ListAuditRecords(ctx, s, bucket, yesterday)
		if err != nil {
			return doctorManagedAuditUnread(bucket, "could not read the managed audit records of gs://"+bucket+" for "+yesterday+": "+oneLineCLI(err.Error()))
		}
		last = newestAuditRecord(older)
	}
	if last == nil {
		return &doctorCheck{ID: doctorManagedAuditID, OK: true, Severity: "info",
			Problem: fmt.Sprintf("no managed agent apply has been recorded for %s yet", pluginwire.Printable(lc.Name)),
			Fix:     "nothing to do: a managed apply files its record under gs://" + bucket + "/" + managedmode.AuditPrefix}
	}
	age := managedAuditAge(now.Sub(last.At))
	problem := fmt.Sprintf("the last managed agent apply is %s at %s (%s ago): %s, plan digest %s; %s filed in the last day",
		pluginwire.Printable(last.Command), last.At.UTC().Format(time.RFC3339), age, last.Verdict,
		pluginwire.Printable(last.PlanDigest), auditRecordCount(len(records)))
	return &doctorCheck{ID: doctorManagedAuditID, OK: true, Severity: "info", Problem: problem,
		Fix: "to read the day's records: gcloud storage ls gs://" + bucket + "/" + managedmode.AuditPrefix + today + "/"}
}

// doctorManagedAuditUnread is the check for a read that did not answer: a
// warning, so it fails --strict and prints in the default view, and carrying
// the unread marker so no view files it as a measurement beside a clean line.
func doctorManagedAuditUnread(bucket, problem string) *doctorCheck {
	return &doctorCheck{ID: doctorManagedAuditID, Severity: "warning", unread: true, Problem: problem,
		Fix: "rerun fugaro doctor with the runs bucket reachable and your own credentials available, or list them by hand: gcloud storage ls gs://" + bucket + "/" + managedmode.AuditPrefix}
}

// newestAuditRecord is the last apply of a day, by the record's own time rather
// than by the listing's order: the objects sort chronologically by name, and a
// reader that took the listing's word for it would be relying on that, while
// the record carries the time the command actually ran.
func newestAuditRecord(records []managedmode.AuditRecord) *managedmode.AuditRecord {
	var last *managedmode.AuditRecord
	for i := range records {
		if last == nil || records[i].At.After(last.At) {
			last = &records[i]
		}
	}
	return last
}

// auditRecordCount is the day's count as a sentence reads it.
func auditRecordCount(n int) string {
	switch n {
	case 0:
		return "no records"
	case 1:
		return "1 record"
	default:
		return fmt.Sprintf("%d records", n)
	}
}

// managedAuditAge is how old the last apply is, in the words doctor already
// renders an age with: humanAge's hours and minutes inside a day, ageDays'
// whole days beyond it. A record whose own time is in the future — a clock
// skew, or an object somebody else wrote, which this bucket permits — is "0s"
// and never a negative age.
func managedAuditAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d >= 24*time.Hour {
		return ageDays(d)
	}
	return humanAge(d)
}
