package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

type rolloverFixture struct {
	*historyFixture
	fs  *gcpfake.Firestore
	now time.Time
}

var rollTestNow = time.Date(2026, 10, 20, 0, 30, 0, 0, time.UTC)

func newRolloverFixture(t *testing.T) *rolloverFixture {
	t.Helper()
	f := &rolloverFixture{historyFixture: newHistoryFixture(t), fs: gcpfake.NewFirestore(t), now: rollTestNow}
	f.fs.Set("meta", "installation", map[string]any{"project": "aurora", "version": int64(1)})
	historyTest.firestore = f.fs.URL
	historyTest.now = func() time.Time { return f.now }
	historyTest.bucket = func(ctx context.Context, name string) (*blobx.Bucket, error) {
		return blobx.Wrap(memblob.OpenBucket(nil)), nil
	}
	t.Setenv("FUGARO_RUNS_BUCKET", "aurora-runs")
	t.Setenv("FUGARO_FIRESTORE_DB", "(default)")
	return f
}

func (f *rolloverFixture) seed(d int64, run string, spent int64) {
	k := budget.DayKey(d)
	f.db.Set("spend/"+k+"/global", map[string]any{"spent": spent, "counted": spent, "calls": 1})
	f.db.Set("spend/"+k+"/repos/"+appSlug, map[string]any{"spent": spent, "counted": spent, "calls": 1})
	f.db.Set("spend/"+k+"/runs/"+appSlug+"/"+run, map[string]any{"reserved": spent, "spent": spent})
	f.db.Set("outcomes/"+k+"/"+appSlug+"/"+run, map[string]any{"status": "succeeded", "requestedBy": "dimi@example.invalid"})
}

func TestRolloverMovesAndPrunes(t *testing.T) {
	f := newRolloverFixture(t)
	today := budget.Day(f.now)
	f.seed(today-1, "20261019-100000-aaaa", 1_000_000)  // provisional
	f.seed(today-12, "20261008-100000-bbbb", 2_000_000) // archived and pruned
	out, _, err := execute(t, "budget", "history", "--rollover")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := f.fs.Value("spendDaily", budget.DayDate(today-12)+"_"+appSlug); !ok || got["final"] != true || got["spentMicros"] != int64(2_000_000) {
		t.Fatalf("doc = %v", got)
	}
	if f.db.Value("spend/"+budget.DayKey(today-12)) != nil || f.db.Value("spend/"+budget.DayKey(today-1)) == nil {
		t.Fatal("wrong days pruned")
	}
	if !strings.Contains(out, "2 days") || !strings.Contains(out, "pruned") || strings.Count(out, "\n") != 3 {
		t.Fatalf("output = %q", out)
	}
}

func TestRolloverNoFirestoreDatabaseIsExit0(t *testing.T) {
	f := newRolloverFixture(t)
	f.fs.RemoveDatabase()
	f.seed(budget.Day(f.now)-12, "20261008-100000-bbbb", 2_000_000)
	before := f.db.Value("")
	out, stderr, err := execute(t, "budget", "history", "--rollover")
	if err != nil || !strings.Contains(out, "no Firestore database") || !strings.Contains(stderr, "WARNING") || !strings.Contains(stderr, "no Firestore database") {
		t.Fatalf("err = %v, out = %q, stderr = %q", err, out, stderr)
	}
	if f.db.Value("spend/"+budget.DayKey(budget.Day(f.now)-12)) == nil || len(before.(map[string]any)) != len(f.db.Value("").(map[string]any)) {
		t.Fatal("the database was touched")
	}
}

// TestRolloverNoFirestoreWarningNamesAProjectTheFlagTakes: the warning is a
// remedy a person pastes into their own terminal, and --firebase-project takes
// only the installation's own GCP project (checkFirebaseProject), so the job's
// own Firebase project is the one value the printed command may not carry: on
// the fixture's shape — a job whose two names differ, which is the only shape
// that reaches this warning with a database the gate above let through — that
// command is refused before it reads anything. The ordinary installation, whose
// two names are one, keeps the command it always printed.
func TestRolloverNoFirestoreWarningNamesAProjectTheFlagTakes(t *testing.T) {
	for _, tc := range []struct{ name, fp, gcp string }{
		{"the job's Firebase project is the installation's GCP project", "proj-1234", "proj-1234"},
		{"a Firebase project of its own, from before 2026-10-23", "aurora-fp", "proj-1234"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRolloverFixture(t)
			t.Setenv("FUGARO_FIREBASE_PROJECT", tc.fp)
			t.Setenv("FUGARO_GCP_PROJECT", tc.gcp)
			f.fs.RemoveDatabase()
			_, stderr, err := execute(t, "budget", "history", "--rollover")
			if err != nil {
				t.Fatalf("err = %v, stderr = %q", err, stderr)
			}
			if !strings.Contains(stderr, "no Firestore database") {
				t.Fatalf("no warning: %q", stderr)
			}
			if !strings.Contains(stderr, "fugaro update --firebase-project "+tc.gcp) {
				t.Errorf("the warning does not name the project --firebase-project takes (%s): %q", tc.gcp, stderr)
			}
			if tc.fp != tc.gcp && strings.Contains(stderr, "--firebase-project "+tc.fp) {
				t.Errorf("the warning names the Firebase project --firebase-project refuses (%s): %q", tc.fp, stderr)
			}
		})
	}
}

// TestRolloverRefusesAnotherInstallationsDatabase: the rollover's one write to
// the Realtime Database is destructive — a day node that is moved out is then
// DELETED — so which database it may do that to is asked of the database and
// not of the two names in its environment. Those names are Terraform's, not a
// config file's, but an installation created before 2026-10-23 may have had a
// Firebase project of its own, other than its GCP project, and the job carries
// both (history.tf). infra.DB.Check is the gate every writer into the budget
// database asks — the rules stage and the policy migration of `fugaro update`
// ask it through the same predicate — and a database carrying ANOTHER
// installation's mark is not this installation's, so nothing is moved and
// nothing is pruned. It is a refusal and not a skip, unlike those stages: this
// job is the whole of what it does, and its sibling the sweep refuses a
// database that is not the project's for the same reason.
func TestRolloverRefusesAnotherInstallationsDatabase(t *testing.T) {
	f := newRolloverFixture(t)
	d := budget.Day(f.now) - 12
	f.seed(d, "20261008-100000-bbbb", 2_000_000)
	before := len(f.db.Value("").(map[string]any))
	f.db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": "workspace-a", "gcp_project": "proj-9999", "version": 1})

	_, _, err := execute(t, "budget", "history", "--rollover")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("another installation's database: err = %v, want exit 1", err)
	}
	for _, want := range []string{"workspace-a", "proj-9999", "nothing was moved"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if f.db.Value("spend/"+budget.DayKey(d)) == nil {
		t.Error("a day was pruned out of another installation's database")
	}
	if len(f.fs.IDs("spendDaily")) != 0 {
		t.Errorf("a day was archived out of another installation's database: %v", f.fs.IDs("spendDaily"))
	}
	if after := len(f.db.Value("").(map[string]any)); after != before {
		t.Errorf("another installation's database changed: %d root keys, was %d", after, before)
	}
}

// TestRolloverRefusesAnUnmarkedDatabase: the other half of the same state. An
// EMPTY database passes infra.DB.Check whatever project its URL belongs to,
// which is why the answer is asked and not read off the pair — a database with
// no mark is not this installation's, however little of it there is to lose,
// and the job says so rather than pruning in it. The pair itself is right here
// (the URL is the fake's own), so the check is the only thing standing between
// the rollover and the database.
func TestRolloverRefusesAnUnmarkedDatabase(t *testing.T) {
	f := newRolloverFixture(t)
	f.db.Set("fugaro/project", nil)
	f.db.Set(budget.PathMark, nil)

	_, _, err := execute(t, "budget", "history", "--rollover")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("an unmarked database: err = %v, want exit 1", err)
	}
	for _, want := range []string{budget.PathMark, "empty", "not this installation's", "nothing was moved"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if got := f.db.Value(""); got != nil {
		t.Errorf("the unmarked database was written in: %v", got)
	}
}

func TestRolloverRefusalsAreExit1(t *testing.T) {
	f := newRolloverFixture(t)
	d := budget.Day(f.now) - 12
	f.seed(d, "20261008-100000-bbbb", 2_000_000)
	f.db.Set("fugaro/project", "birch")
	if _, _, err := execute(t, "budget", "history", "--rollover"); ExitCode(err) != ExitUserError {
		t.Fatalf("foreign rtdb: err = %v", err)
	}
	f.db.Set("fugaro/project", "aurora")
	f.fs.Set("meta", "installation", map[string]any{"project": "birch"})
	if _, _, err := execute(t, "budget", "history", "--rollover"); ExitCode(err) != ExitUserError {
		t.Fatalf("foreign firestore: err = %v", err)
	}
	if f.db.Value("spend/"+budget.DayKey(d)) == nil || len(f.fs.IDs("spendDaily")) != 0 {
		t.Fatal("something was moved")
	}
	for _, args := range [][]string{{"--force"}, {"--day", "yesterday"}, {"--day", "2099-01-01"}} {
		_, _, err := execute(t, append([]string{"budget", "history", "--rollover"}, args...)...)
		if ExitCode(err) != ExitUserError {
			t.Errorf("%v: err = %v, want exit 1", args, err)
		}
	}
	if _, _, err := execute(t, "budget", "history", "--sweep", "--force"); ExitCode(err) != ExitUserError {
		t.Errorf("sweep --force: err = %v", err)
	}
}

func TestRolloverDayForce(t *testing.T) {
	f := newRolloverFixture(t)
	d := budget.Day(f.now) - 4
	f.seed(d, "20261016-100000-bbbb", 2_000_000)
	if _, _, err := execute(t, "budget", "history", "--rollover", "--day", budget.DayDate(d)); err != nil {
		t.Fatal(err)
	}
	f.fs.Set("spendDaily", budget.DayDate(d)+"_"+appSlug, map[string]any{"repo": "r", "slug": appSlug, "date": budget.DayDate(d),
		"version": int64(1), "final": true, "spentMicros": int64(5)})
	if _, _, err := execute(t, "budget", "history", "--rollover", "--day", budget.DayDate(d)); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.fs.Value("spendDaily", budget.DayDate(d)+"_"+appSlug); got["spentMicros"] != int64(5) {
		t.Fatal("--day rewrote a final document")
	}
	if _, _, err := execute(t, "budget", "history", "--rollover", "--day", budget.DayDate(d), "--force"); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.fs.Value("spendDaily", budget.DayDate(d)+"_"+appSlug); got["spentMicros"] != int64(2_000_000) {
		t.Fatalf("doc = %v", got)
	}
}

func TestRolloverNeedsFirestoreEnv(t *testing.T) {
	f := newRolloverFixture(t)
	for _, k := range []string{"FUGARO_PROJECT", "FUGARO_FIREBASE_PROJECT", "FUGARO_RTDB_URL", "FUGARO_RUNS_BUCKET", "FUGARO_FIRESTORE_DB"} {
		old := map[string]string{"FUGARO_PROJECT": "aurora", "FUGARO_FIREBASE_PROJECT": "aurora-fp", "FUGARO_RTDB_URL": f.db.URL,
			"FUGARO_RUNS_BUCKET": "aurora-runs", "FUGARO_FIRESTORE_DB": "(default)"}[k]
		t.Setenv(k, "")
		_, _, err := execute(t, "budget", "history", "--rollover")
		if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), k) {
			t.Errorf("without %s: err = %v", k, err)
		}
		t.Setenv(k, old)
	}
	t.Setenv("FUGARO_FIRESTORE_DB", "other")
	if _, _, err := execute(t, "budget", "history", "--rollover"); err == nil || ExitCode(err) != ExitUserError {
		t.Errorf("a named database: err = %v", err)
	}
}

func TestRolloverOutageIsExit2(t *testing.T) {
	f := newRolloverFixture(t)
	f.seed(budget.Day(f.now)-12, "20261008-100000-bbbb", 2_000_000)
	f.fs.Refuse(http.StatusServiceUnavailable, "UNAVAILABLE", "", "down")
	_, _, err := execute(t, "budget", "history", "--rollover")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if f.db.Value("spend/"+budget.DayKey(budget.Day(f.now)-12)) == nil {
		t.Fatal("the day was pruned during an outage")
	}
}

// A mismatch between the database's figures and a final document is exit 1.
func TestRolloverReadBackMismatchIsExit1(t *testing.T) {
	f := newRolloverFixture(t)
	d := budget.Day(f.now) - 12
	f.seed(d, "20261008-100000-bbbb", 2_000_000)
	f.fs.Set("spendDaily", budget.DayDate(d)+"_"+appSlug, map[string]any{"repo": "r", "slug": appSlug, "date": budget.DayDate(d),
		"version": int64(1), "final": true, "spentMicros": int64(5)})
	out, _, err := execute(t, "budget", "history", "--rollover")
	if ExitCode(err) != ExitUserError || f.db.Value("spend/"+budget.DayKey(d)) == nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "FAILED") || !strings.Contains(out, budget.DayDate(d)) {
		t.Fatalf("output = %q", out)
	}
}

func TestRolloverNoAuthNeverReachesRealFirestore(t *testing.T) {
	newRolloverFixture(t)
	historyTest.firestore = ""
	if _, _, err := execute(t, "budget", "history", "--rollover"); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
}
