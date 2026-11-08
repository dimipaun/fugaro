// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// This file is M10's record tests: what one identity's calls come to, who may
// read it, and where it is written. The writer's own tests are audit_test.go's.

// auditLauncher is the second person these tests need: an identity the
// installation admits as a launcher and not as an operator, which is what makes
// "a launcher does not read the record" decidable at one server rather than by
// two builds of one rule (D933).
const auditLauncher = "user:launcher-2@example.com"

// answering is a tool whose answer is fixed text, which is all a test of the
// record needs of a handler: something the door can record having served.
func answering(text string) Handler {
	return func(context.Context, Call) (any, error) { return text, nil }
}

// quoted is one string as JSON, so an identity written into an arguments object
// is escaped by the encoder and not by hand.
func quoted(t *testing.T, s string) string {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("encoding %q: %v", s, err)
	}
	return string(data)
}

// auditCallBody is one tools/call request, built rather than spliced so that a
// tool's arguments are JSON and not a string that happens to look like it.
func auditCallBody(t *testing.T, name, args string) string {
	t.Helper()
	if args == "" {
		args = "{}"
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  methodToolsCall,
		"params": map[string]any{
			"name":      name,
			"arguments": json.RawMessage(args),
		},
	})
	if err != nil {
		t.Fatalf("writing a tools/call request: %v", err)
	}
	return string(body)
}

// auditRecordOf asks for one identity's record with a token that may call the
// tool, and reads the record out of the tool's own answer.
func auditRecordOf(t *testing.T, ts *resourceTest, token, identity string) (AuditRecord, string) {
	t.Helper()
	body := auditCallBody(t, "get_audit_record", `{"identity":`+quoted(t, identity)+`}`)
	ans := wantResult(t, ts.rpc(t, token, body))
	text, isError := wantToolText(t, ans)
	if isError {
		t.Fatalf("reading the record of %s answered the tool's failure: %s", identity, text)
	}
	var rec AuditRecord
	if err := json.Unmarshal([]byte(text), &rec); err != nil {
		t.Fatalf("the record is not the shape this tool declares: %v (%s)", err, text)
	}
	return rec, text
}

// TestAuditRecordIsBounded is the bound as D935's record has it: one identity's
// record keeps the newest MaxAuditEntries calls and drops the oldest, so a
// caller can grow its own record and no other's (the amendment of 2026-11-04).
//
// What it does not pin is the line, which is the copy a caller cannot flush:
// TestTheLoggingLineIsTheDurableCopy is that row, and the two are one claim
// read twice — the record is a view of something that is kept elsewhere.
func TestAuditRecordIsBounded(t *testing.T) {
	ts := newResourceServer(t)
	ts.serve(t, "get_task_status", answering("run-7 is implementing"))
	ts.serve(t, "list_tasks", answering("two runs"))
	clientID, tokens := ts.runFlow(t, testClientRedirect)

	// More calls than the bound, in two names, so that which entries were kept
	// is decidable: sixty-four of the first and two of the second leaves a
	// record whose head is the two, whose tail is still the first, and whose
	// length is the bound rather than the number of calls.
	for i := 0; i < MaxAuditEntries; i++ {
		wantResult(t, ts.rpc(t, tokens.AccessToken, auditCallBody(t, "get_task_status", "")))
	}
	for i := 0; i < 2; i++ {
		wantResult(t, ts.rpc(t, tokens.AccessToken, auditCallBody(t, "list_tasks", "")))
	}

	rec, err := ts.store.Store.GetAudit(context.Background(), testIdentity)
	if err != nil {
		t.Fatalf("reading the record the calls left: %v", err)
	}
	if rec.Identity != testIdentity {
		t.Errorf("the record says it belongs to %q; want %q: the key is a hash, and the object is where a reader learns whose it is", rec.Identity, testIdentity)
	}
	if len(rec.Entries) != MaxAuditEntries {
		t.Fatalf("the record holds %d entries after %d calls; want the bound %d", len(rec.Entries), MaxAuditEntries+2, MaxAuditEntries)
	}
	if head, second := rec.Entries[0], rec.Entries[1]; head.Tool != "list_tasks" || second.Tool != "list_tasks" {
		t.Errorf("the record's two newest entries are %q and %q; want the two last calls, newest first", head.Tool, second.Tool)
	}
	if last := rec.Entries[len(rec.Entries)-1]; last.Tool != "get_task_status" {
		t.Errorf("the record's oldest entry is %q; want the first tool, whose oldest calls are what the bound dropped", last.Tool)
	}

	newest := rec.Entries[0]
	if newest.Outcome != OutcomeOK || newest.Reason != "" {
		t.Errorf("a served call is recorded as outcome %q reason %q; want %q and no reason", newest.Outcome, newest.Reason, OutcomeOK)
	}
	if newest.ClientID != clientID {
		t.Errorf("the entry names the client %q; want %q", newest.ClientID, clientID)
	}
	if newest.FamilyID == "" {
		t.Error("the entry names no family: the family is what a revocation ends, and the record is where a person's own surface reads which one to end")
	}
	if !newest.At.Equal(testNow) {
		t.Errorf("the entry is stamped %s; want the instant the door read its clock, %s", newest.At, testNow)
	}
	if len(newest.Scopes) == 0 {
		t.Error("the entry names no scopes: a reader has to know what the token could do, not only what it did")
	}

	// A record is one person's: an identity no call has been served for has an
	// empty answer rather than somebody else's entries.
	other, err := ts.store.Store.GetAudit(context.Background(), auditLauncher)
	if err != nil {
		t.Fatalf("reading the record of an identity that called nothing: %v", err)
	}
	if len(other.Entries) != 0 {
		t.Errorf("the record of an identity that called nothing holds %d entries", len(other.Entries))
	}
	if other.Identity != auditLauncher {
		t.Errorf("the empty record names %q; want the identity it was read for", other.Identity)
	}
}

// contendingObjects is one store's view of a bucket with other calls of the
// same person in it: while rounds remain, a read of the record is followed by
// another call's append to it, which moves the generation this store's write is
// matched against. The interleave is made deterministic rather than left to a
// scheduler — a goroutine test would be red or green by machine — and it is the
// ordinary shape of a client that runs tools in parallel, which is what the
// record's own retry exists for.
type contendingObjects struct {
	objects
	at      string
	rounds  int
	contend func()
}

func (o *contendingObjects) read(ctx context.Context, key string) ([]byte, int64, error) {
	data, gen, err := o.objects.read(ctx, key)
	if err == nil && key == o.at && o.rounds > 0 {
		o.rounds--
		o.contend()
	}
	return data, gen, err
}

// TestAContendedRecordKeepsTheEntry is the fourth call of a client that runs
// four tools at once. The record is one object and every call rewrites it, so a
// call whose write loses a round re-reads and writes again — and how many
// rounds it needs is how many calls of that person land first, which is the
// client's business and not something a small count can bound: at three
// attempts, four calls at once lose one entry, and lose it quietly, because the
// door answers the call either way (auditCall).
func TestAContendedRecordKeepsTheEntry(t *testing.T) {
	ctx := context.Background()
	b := newFakeBucket(t)

	// The competing calls write through a store of their own over the same
	// bucket, so what this store loses its round to is a real write to the same
	// object and not a stub that says "changed".
	other := newBucketStore(b)
	record := AuditEntry{Tool: "list_tasks", Outcome: OutcomeOK}
	if err := other.AppendAudit(ctx, testIdentity, record); err != nil {
		t.Fatalf("the record's first entry: %v", err)
	}

	key, err := auditKey(testIdentity)
	if err != nil {
		t.Fatalf("the key of %s's record: %v", testIdentity, err)
	}
	contended := &contendingObjects{objects: bucketObjects{b: b}, at: key, rounds: 3}
	contended.contend = func() {
		if err := other.AppendAudit(ctx, testIdentity, record); err != nil {
			t.Errorf("another call's own entry: %v", err)
		}
	}

	own := AuditEntry{Tool: "get_task_status", Outcome: OutcomeOK}
	if err := newRecords(contended).AppendAudit(ctx, testIdentity, own); err != nil {
		t.Fatalf("an entry into a record three other calls were writing at once: %v", err)
	}
	rec, err := other.GetAudit(ctx, testIdentity)
	if err != nil {
		t.Fatalf("reading the record the contended call wrote into: %v", err)
	}
	if len(rec.Entries) != 5 {
		t.Fatalf("the record holds %d entries after one call contended with three; want all five calls:\n%+v", len(rec.Entries), rec.Entries)
	}
	if rec.Entries[0].Tool != own.Tool {
		t.Errorf("the record's newest entry is %q; want the call that had to re-read and write again", rec.Entries[0].Tool)
	}
	if rec.Incomplete {
		t.Error("the record says it is short after every entry it was given was written: a record that cried wolf would send an operator to the log for nothing")
	}
}

// unwritableAuditRecord is one identity's record as a store that never lets it
// be written: every attempt at that object is answered errChanged, which is
// what a record contended past the loop's bound amounts to. The marker beside
// the record is a different key and is written as usual.
type unwritableAuditRecord struct {
	objects
	at string
}

func (o unwritableAuditRecord) create(ctx context.Context, key string, data []byte) (int64, error) {
	if key == o.at {
		return 0, errChanged
	}
	return o.objects.create(ctx, key, data)
}

func (o unwritableAuditRecord) replace(ctx context.Context, key string, data []byte, gen int64) (int64, error) {
	if key == o.at {
		return 0, errChanged
	}
	return o.objects.replace(ctx, key, data, gen)
}

// TestARecordThatCouldNotTakeAnEntrySaysSo is what is left when the entry is
// lost: the door answers the call anyway — the line is the copy that cannot be
// lost (the amendment of 2026-11-04) — so the record is the only thing that can
// tell its reader that it is short, and a record that reads as a whole history
// of a person while missing one of their calls is the answer an operator acts
// on wrongly.
func TestARecordThatCouldNotTakeAnEntrySaysSo(t *testing.T) {
	ctx := context.Background()
	b := newFakeBucket(t)
	key, err := auditKey(testIdentity)
	if err != nil {
		t.Fatalf("the key of %s's record: %v", testIdentity, err)
	}
	store := newRecords(unwritableAuditRecord{objects: bucketObjects{b: b}, at: key})

	if err := store.AppendAudit(ctx, testIdentity, AuditEntry{Tool: "get_task_status", Outcome: OutcomeOK}); !errors.Is(err, ErrConflict) {
		t.Fatalf("an entry the record would not take is answered %v; want ErrConflict, which is the door's own complaint (auditCall)", err)
	}

	rec, err := store.GetAudit(ctx, testIdentity)
	if err != nil {
		t.Fatalf("reading a record an entry was lost from: %v", err)
	}
	if len(rec.Entries) != 0 {
		t.Fatalf("the record of a lost entry holds %d entries; want the entry gone, which is what makes the record's own word the only report of it", len(rec.Entries))
	}
	if !rec.Incomplete {
		t.Fatal("a record an entry was lost from reads as a whole history of that person")
	}
	// The tool answers the record itself, so the claim has to be in the JSON an
	// operator's own reader parses and not only in the struct a Go caller sees.
	answer, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("encoding the record as the tool answers it: %v", err)
	}
	if !strings.Contains(string(answer), `"incomplete":true`) {
		t.Errorf("the record the tool serves is %s; want the loss stated in it", answer)
	}

	// And it is this record's state and not a word the answer always carries:
	// another person's record, which took every entry it was given, says the
	// opposite, and the marker is one identity's and not the store's.
	whole := newBucketStore(b)
	if err := whole.AppendAudit(ctx, auditLauncher, AuditEntry{Tool: "get_task_status", Outcome: OutcomeOK}); err != nil {
		t.Fatalf("the second person's entry: %v", err)
	}
	other, err := whole.GetAudit(ctx, auditLauncher)
	if err != nil {
		t.Fatalf("reading the second person's record: %v", err)
	}
	if other.Incomplete {
		t.Error("a record that took every entry it was given says it is short: the marker is being read as the store's rather than as one record's")
	}
	answer, err = json.Marshal(other)
	if err != nil {
		t.Fatalf("encoding the second person's record: %v", err)
	}
	if strings.Contains(string(answer), `"incomplete":true`) {
		t.Errorf("the second person's record is %s; want no loss in it", answer)
	}
}

// TestOperatorSeesTheRecordAndALauncherDoesNot is D933's split held at the
// tool: an operator reads one person's record, a launcher is refused, and the
// refusal is itself recorded — a call that reached a tool is an event whatever
// the answer was, and an attempt to read the record is exactly the event an
// operator wants to see.
func TestOperatorSeesTheRecordAndALauncherDoesNot(t *testing.T) {
	ts := newResourceServer(t, WithRoster(rosterOf(Members{
		Launchers: []string{auditLauncher},
		Operators: []string{testIdentity},
	})))
	ts.serve(t, "get_task_status", answering("run-7 is implementing"))
	if err := ts.tools.Register(AuditRecordTool(ts.store)); err != nil {
		t.Fatalf("registering the audit tool: %v", err)
	}

	_, tokens := ts.runFlow(t, testClientRedirect)

	// The operator's own call, so that the record it reads below is not empty
	// for want of anything to hold.
	wantResult(t, ts.rpc(t, tokens.AccessToken, auditCallBody(t, "get_task_status", "")))

	rec, _ := auditRecordOf(t, ts, tokens.AccessToken, testIdentity)
	if len(rec.Entries) != 1 || rec.Entries[0].Tool != "get_task_status" {
		t.Fatalf("the operator read %+v; want the one call it just made", rec.Entries)
	}

	// The launcher holds both scopes — it is admitted to the task tools — so
	// what refuses it below is the tool's own rule and not the door's.
	launcher := ts.seededToken(t, auditLauncher)
	ans := wantResult(t, ts.rpc(t, launcher, auditCallBody(t, "get_audit_record", `{"identity":`+quoted(t, testIdentity)+`}`)))
	refusal, isError := wantToolText(t, ans)
	if !isError {
		t.Fatalf("a launcher read the record of %s: %s", testIdentity, refusal)
	}
	if !strings.Contains(refusal, "operator") {
		t.Errorf("the launcher's refusal reads %q; want it to say whose tool this is, because a launcher that cannot tell the rule from a fault keeps asking", refusal)
	}
	if strings.Contains(refusal, "get_task_status") {
		t.Errorf("the launcher's refusal carries the record's content: %q", refusal)
	}

	// The attempt is in the launcher's own record, which the operator reads:
	// the record is where a call that was not the person's own becomes visible,
	// beside the family that made it.
	launcherRec, _ := auditRecordOf(t, ts, tokens.AccessToken, auditLauncher)
	if len(launcherRec.Entries) == 0 {
		t.Fatal("the launcher's refused call left no entry: a call that reached a tool is an event whatever the answer was")
	}
	attempt := launcherRec.Entries[0]
	if attempt.Tool != "get_audit_record" || attempt.Outcome != OutcomeRefused || attempt.Reason != ReasonToolRefused {
		t.Errorf("the launcher's attempt is recorded as %s %s %s; want get_audit_record %s %s", attempt.Tool, attempt.Outcome, attempt.Reason, OutcomeRefused, ReasonToolRefused)
	}
	if attempt.ClientID != "client-that-minted-it" {
		t.Errorf("the attempt names the client %q; want the registration the launcher's token was seeded with", attempt.ClientID)
	}
}

// TestTheRecordIsNotInTheProjectsDatabase is D935's own row for the audit: the
// record is an object in the store's bucket, written through M2's Store, and
// the front door has no route at all to the project's Realtime Database, where
// the `mcpauth` node stays in the rules as a second barrier.
//
// The second half is a guard over this package's own source rather than a read
// against a database, and that is the assertion that can fail: this process
// holds no client to that database, so a fake opened here would be written to
// by nobody and would answer nothing however the front door behaved — the row
// would pass on the day a later task moved the audit back into the node. What
// is decidable is the claim underneath it, that the front door has no way there
// at all: no file of this package outside its tests imports the database client
// or the rules package that names `rules.PathMCPAuth`, so no path of this door
// can write into the node. A task that gives the front door a database client —
// to read the installation's published roster, say — edits this row and says
// why, and that edit is where an audit moving back into the node has to be
// argued (reconciliation.md F32: no rule keeps a launcher, an operator or a
// budget admin out of that node, which is why D935 takes the record out of it).
func TestTheRecordIsNotInTheProjectsDatabase(t *testing.T) {
	ts := newResourceServer(t)
	ts.serve(t, "get_task_status", answering("run-7 is implementing"))
	_, tokens := ts.runFlow(t, testClientRedirect)
	wantResult(t, ts.rpc(t, tokens.AccessToken, auditCallBody(t, "get_task_status", "")))

	key := auditPrefix + TokenHash(testIdentity)
	at := ts.store.objects(t)
	data, held := at[key]
	if !held {
		t.Fatalf("the store holds no object at %s: the audit record goes through M2's Store, and the keys are: %v", key, keysOf(at))
	}
	if !ts.counting.keys[key] {
		t.Errorf("%s is in the store and no call of the Store interface wrote it: every record this door writes arrives through the Store, and a writer that reached the bucket itself is one the store's grant cannot tell from the front door", key)
	}
	var rec AuditRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("the object at %s is not an audit record: %v (%s)", key, err, data)
	}
	if rec.Identity != testIdentity || len(rec.Entries) != 1 {
		t.Errorf("the object at %s is %+v; want %s's own record with the call it just made", key, rec, testIdentity)
	}
	if strings.Contains(key, testIdentity) {
		t.Errorf("the key %s carries the identity: a key is a path in a bucket, and an address in a path is an address in every listing, metric and error that names it", key)
	}

	sources := frontDoorSources(t)
	if _, read := sources["store.go"]; !read {
		t.Fatalf("the scan read %v and no store.go of this package, so it is not reading the front door's source", keysOf(sources))
	}
	for name, src := range sources {
		if routes := databaseRoutesIn(t, name, src); len(routes) > 0 {
			t.Errorf("%s imports %s: the audit record is the store's (D935), and a client to the project's database is the one route back into the `mcpauth` node the rules refuse whole", name, strings.Join(routes, ", "))
		}
	}

	// The scan is shown a file that does reach the database, so the loop above
	// cannot pass by reading nothing or by matching nothing — a guard nobody has
	// seen fail is not a guard.
	planted := []byte("package planted\n\nimport (\n\t_ \"github.com/dimipaun/fugaro/internal/rtdb\"\n)\n")
	if routes := databaseRoutesIn(t, "planted.go", planted); len(routes) != 1 {
		t.Errorf("the scan finds %v in a file that imports the database client: it would not fail on the day the front door gains one", routes)
	}
}

// projectDatabaseRoutes are the two imports that are a way into the project's
// Realtime Database: the client itself, and the rules package, whose
// rules.PathMCPAuth is the path of the `mcpauth` node this door's records were
// once kept under.
var projectDatabaseRoutes = []string{
	"github.com/dimipaun/fugaro/internal/rtdb",
	"github.com/dimipaun/fugaro/internal/budget/rules",
}

// frontDoorSources is this package's own source with the tests left out: the
// scan is about what the front door can reach, and a test file may hold whatever
// it needs to check that.
func frontDoorSources(t *testing.T) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package's own directory: %v", err)
	}
	out := map[string][]byte{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		out[name] = src
	}
	return out
}

// databaseRoutesIn parses one file and names the project-database imports it
// carries. It parses rather than searches the text, so a comment that mentions
// one of them — audit_record.go's own does — is not mistaken for a route.
func databaseRoutesIn(t *testing.T, name string, src []byte) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	var out []string
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("reading an import path of %s: %v", name, err)
		}
		for _, route := range projectDatabaseRoutes {
			if path == route {
				out = append(out, path)
			}
		}
	}
	return out
}

// keysOf is the store's keys, for the failure message that says where the
// record is not.
func keysOf(at map[string][]byte) []string {
	out := make([]string, 0, len(at))
	for key := range at {
		out = append(out, key)
	}
	return out
}
