package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// Reading the audit family: ONE parser, and the one listing a day's records
// with it (M6, D1014).
//
// Why the parser is here, in the package that writes the record, and not beside
// the surface that prints it: spec §6 asks for the check `fugaro doctor` gains
// and the section `fugaro report` gains to read "the same object the record's
// reader parses, so what doctor prints and what the file holds cannot drift".
// Two parsers — one a struct here and one built ad hoc where a surface renders
// it — is exactly the pair that drifts, and the drift is silent: a field the
// writer adds and a reader never sees prints as an empty column, and a field
// whose meaning moved prints a wrong figure with no error. So the record's shape
// is decoded in one function, strictly, and every surface reads what it returns.
//
// Why the decode is strict. A record is a document somebody else can write —
// the bucket is unversioned and any project-level storage writer can rewrite an
// object (this package's own header says so) — so a field this build does not
// know is a field whose meaning it cannot judge, and ignoring it would let an
// object carry a claim nothing acts on. The version is checked for the same
// reason the marker's is: a later build's record read as this one's shape is the
// mutation the version exists to stop, and the strict shape is what makes the
// version's promise true rather than decorative.

// ParseAuditRecord reads one record's bytes: strictly, by this build's shape,
// and through the same validation the writer asks, so a record that could not
// have been written by this build is not read by it either.
//
// The error names what is wrong and never prints the object whole: a record can
// hold a plan's rules, which quote a plan, so everything from it goes through
// the bounded sanitiser on the way into a sentence.
func ParseAuditRecord(data []byte) (AuditRecord, error) {
	if len(data) > auditMaxBytes {
		return AuditRecord{}, fmt.Errorf("an audit record of %d bytes is larger than a record can be (%d), so this is not one", len(data), auditMaxBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var r AuditRecord
	if err := dec.Decode(&r); err != nil {
		return AuditRecord{}, fmt.Errorf("an audit record could not be read: %w", safetext.FieldErr(err))
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return AuditRecord{}, fmt.Errorf("an audit record carries a second document after the first, so this is not one")
	}
	if err := r.Validate(); err != nil {
		return AuditRecord{}, err
	}
	return r, nil
}

// ListAuditRecords reads one UTC date's records, in the listing's own order,
// parsed by the one parser: what a check and a report both call, so neither can
// print a record the other would refuse.
//
// A day nothing was filed under is no records and no error. That is not
// leniency: every ordinary project has days with no managed apply, and a read
// that failed for them would fail the surface that asked — which is the
// difference between "nothing was recorded" and "the records could not be
// read", and only the second is a reason to say so.
//
// One object that is not a record is an error for the whole day rather than a
// skipped line: a day's trail is read as a whole, and a reader that printed the
// three records it could parse out of four would be reporting a project's
// history with a hole in it that nothing in the output shows.
func ListAuditRecords(ctx context.Context, s *storage.Service, bucket, date string) ([]AuditRecord, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: a record is read with the credentials of the account running the surface that shows it", ErrNoAuditReader)
	}
	if err := checkAuditDate(date); err != nil {
		return nil, err
	}
	prefix := AuditPrefix + date + "/"
	var out []AuditRecord
	page := ""
	for {
		call := s.Objects.List(bucket).Prefix(prefix).Context(ctx)
		if page != "" {
			call = call.PageToken(page)
		}
		list, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("reading the audit records of %s for %s: %w", date, safetext.Field(bucket), safetext.FieldErr(err))
		}
		for _, o := range list.Items {
			r, err := readAuditObject(ctx, s, bucket, o.Name)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		if list.NextPageToken == "" {
			return out, nil
		}
		page = list.NextPageToken
	}
}

// readAuditObject reads one object of the family by name and parses it. The
// name is checked before the read: a listing of the family that answers a name
// outside it is a listing this program did not make, and reading it would be
// reading an object nothing here wrote.
func readAuditObject(ctx context.Context, s *storage.Service, bucket, name string) (AuditRecord, error) {
	if !strings.HasPrefix(name, AuditPrefix) || !strings.HasSuffix(name, ".json") {
		return AuditRecord{}, fmt.Errorf("the listing of gs://%s answered %s, which is not an object of the audit family (%s<date>/<id>.json), so it is not read",
			safetext.Field(bucket), safetext.Field(name), AuditPrefix)
	}
	resp, err := s.Objects.Get(bucket, name).Context(ctx).Download()
	if err != nil {
		return AuditRecord{}, fmt.Errorf("reading gs://%s/%s: %w", safetext.Field(bucket), safetext.Field(name), safetext.FieldErr(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, auditMaxBytes+1))
	if err != nil {
		return AuditRecord{}, fmt.Errorf("reading gs://%s/%s: %w", safetext.Field(bucket), safetext.Field(name), safetext.FieldErr(err))
	}
	r, err := ParseAuditRecord(data)
	if err != nil {
		return AuditRecord{}, fmt.Errorf("gs://%s/%s: %w", safetext.Field(bucket), safetext.Field(name), err)
	}
	return r, nil
}

// checkAuditDate refuses a date that is not one the family can hold: the exact
// spelling a writer makes, since a prefix built from anything else ("2026-11-3",
// "2026-11-03/../runs") asks a listing about a directory no record is ever in —
// and a read that answered "none" for a misspelled date would look exactly like
// a day nothing happened.
func checkAuditDate(date string) error {
	at, err := time.Parse(AuditDateLayout, date)
	if err != nil || at.Format(AuditDateLayout) != date {
		return fmt.Errorf("the audit records are filed under a UTC date spelled %s (2026-11-03), and %q is not one", AuditDateLayout, safetext.Field(date))
	}
	return nil
}
