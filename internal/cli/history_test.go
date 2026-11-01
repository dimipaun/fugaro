package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

type historyFixture struct {
	db  *gcpfake.RTDB
	run *gcpfake.Run
	idt *gcpfake.IdentityToolkit
	job string
}

// newHistoryFixture points the history command at fakes and sets the
// environment the history job gets from Terraform.
func newHistoryFixture(t *testing.T) *historyFixture {
	t.Helper()
	f := &historyFixture{db: gcpfake.NewRTDB(t), run: gcpfake.NewRun(t)}
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	f.idt = gcpfake.NewIdentityToolkit(t, gcpfake.NewIAMCredentials(t), "key", "aurora-fp")
	f.job = gcp.JobName(appSlug, "web")
	f.run.AddJob(f.job, "1", "2Gi")
	f.db.Set("fugaro/project", "aurora")
	// The installation's own mark, as init writes it: the rollover moves days
	// out of, and then deletes nodes in, the database its environment names,
	// and it may only do that to this installation's own — read from the mark
	// (history_rollover.go). The fixture's Firebase project is deliberately
	// not its GCP project (fp-1234 vs proj-1234), the shape of an installation
	// created before 2026-10-23 and the only one where the pair decides.
	f.db.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "version": 1, "project": "aurora", "gcp_project": "proj-1234"})
	old := historyTest
	historyTest = &historyWiring{noAuth: true, runURL: f.run.URL + "/", identityURL: f.idt.URL, now: func() time.Time { return time.Now() }}
	t.Cleanup(func() { historyTest = old })
	for k, v := range map[string]string{"FUGARO_PROJECT": "aurora", "FUGARO_GCP_PROJECT": "proj-1234", "FUGARO_REGION": "us-east5",
		"FUGARO_FIREBASE_PROJECT": "aurora-fp", "FUGARO_RTDB_URL": f.db.URL} {
		t.Setenv(k, v)
	}
	return f
}

func (f *historyFixture) entry(run string, age time.Duration) {
	f.db.Set("agents/"+appSlug+"/"+run, map[string]any{"repo": "acme/app", "workflow": "web", "requestedBy": "dimi@example.invalid",
		"startedAt": time.Now().Add(-age).UnixMilli(), "updatedAt": time.Now().Add(-age).UnixMilli()})
}

func TestHistorySweepRemovesCrashedRun(t *testing.T) {
	f := newHistoryFixture(t)
	f.entry("20261002-090000-aaaa", time.Hour)
	f.entry("20261002-090100-bbbb", time.Hour)
	f.run.SetState(f.run.StartWithEnv(f.job, map[string]string{"FUGARO_RUN": appSlug + "/20261002-090100-bbbb"}), backend.StateRunning)
	out, _, err := execute(t, "budget", "history", "--sweep")
	if err != nil {
		t.Fatal(err)
	}
	if f.db.Value("agents/"+appSlug+"/20261002-090000-aaaa") != nil || f.db.Value("agents/"+appSlug+"/20261002-090100-bbbb") == nil {
		t.Fatalf("registry = %v", f.db.Value("agents"))
	}
	if !strings.Contains(out, "1 removed") || !strings.Contains(out, "1 kept") {
		t.Fatalf("output = %q", out)
	}
}

// TestHistoryPrintsPairingNodesDeleted: the sweep deletes the pairing/<uid>
// node of the p~ user it deletes (decision 2), and this line is what an
// operator reads afterwards. A node that is gone and not counted is a deletion
// nobody can see — the pairing tree is empty whether one node went or none did
// — and the payload the node held is the whole reason that deletion is in the
// sweep's remit.
func TestHistoryPrintsPairingNodesDeleted(t *testing.T) {
	f := newHistoryFixture(t)
	const uid = "p~AAAAAAAAAAAAAAAAAAAAAAAAAA"
	f.db.Set("pairing/"+uid, map[string]any{"delivery": map[string]any{"v": 1, "sealed": "AAAA"}})
	f.idt.AddUser(uid, time.Now().Add(-2*time.Hour))

	out, _, err := execute(t, "budget", "history", "--sweep")
	if err != nil {
		t.Fatal(err)
	}
	if f.db.Value("pairing/"+uid) != nil {
		t.Fatal("the pairing node survived the pass that deleted its user")
	}
	if !strings.Contains(out, "1 users deleted, 1 pairing nodes deleted") {
		t.Fatalf("output = %q, want the pairing node it deleted counted beside the users", out)
	}
}

func TestHistoryNeedsExactlyOneMode(t *testing.T) {
	newHistoryFixture(t)
	for _, args := range [][]string{{"budget", "history"}, {"budget", "history", "--sweep", "--rollover"}} {
		if _, _, err := execute(t, args...); err == nil || ExitCode(err) != ExitUserError {
			t.Errorf("%v: err = %v, want exit 1", args, err)
		}
	}
}

// The history account can reach any Firebase project it is granted, so the
// sweep checks the database is this Fugaro project's before writing.
func TestHistorySweepRefusesForeignDatabase(t *testing.T) {
	f := newHistoryFixture(t)
	f.entry("20261002-090000-aaaa", time.Hour)
	for _, mark := range []any{"birch", nil, 7} {
		f.db.Set("fugaro/project", mark)
		_, _, err := execute(t, "budget", "history", "--sweep")
		if err == nil || ExitCode(err) != ExitUserError {
			t.Errorf("mark %v: err = %v, want exit 1", mark, err)
		}
	}
	if f.db.Value("agents/"+appSlug+"/20261002-090000-aaaa") == nil {
		t.Fatal("a foreign database was written")
	}
}

// The name is not the whole answer. /fugaro/project is a string two
// installations can both carry — two GCP projects, one project name — and the
// mark records the GCP project beside it, which is the fact that tells them
// apart. The sweep deletes registry nodes and Firebase users, so a database the
// name alone would pass and the mark refuses is refused, by the same predicate
// the stages of `fugaro update` and the rollover ask (budgetDBUnadopted).
func TestHistorySweepRefusesADatabaseTheNameAloneWouldPass(t *testing.T) {
	f := newHistoryFixture(t)
	f.entry("20261002-090000-aaaa", time.Hour)
	f.db.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "version": 1, "project": "aurora", "gcp_project": "proj-9999"})

	_, _, err := execute(t, "budget", "history", "--sweep")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a database marked in another GCP project: err = %v, want exit 1", err)
	}
	for _, want := range []string{"proj-9999", "proj-1234", "nothing was swept"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if f.db.Value("agents/"+appSlug+"/20261002-090000-aaaa") == nil {
		t.Fatal("the sweep ran in another GCP project's database")
	}
}

func TestHistoryMissingEnvironment(t *testing.T) {
	newHistoryFixture(t)
	for _, k := range []string{"FUGARO_PROJECT", "FUGARO_GCP_PROJECT", "FUGARO_REGION", "FUGARO_FIREBASE_PROJECT", "FUGARO_RTDB_URL"} {
		t.Setenv(k, "")
		_, _, err := execute(t, "budget", "history", "--sweep")
		if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), k) {
			t.Errorf("without %s: err = %v", k, err)
		}
		t.Setenv(k, map[string]string{"FUGARO_PROJECT": "aurora", "FUGARO_GCP_PROJECT": "proj-1234", "FUGARO_REGION": "us-east5", "FUGARO_FIREBASE_PROJECT": "aurora-fp", "FUGARO_RTDB_URL": "http://127.0.0.1:1"}[k])
	}
}

func TestHistoryOutageIsExit2(t *testing.T) {
	f := newHistoryFixture(t)
	f.run.Refuse(http.StatusServiceUnavailable, "UNAVAILABLE", "", "down")
	_, _, err := execute(t, "budget", "history", "--sweep")
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v, want exit 2", err)
	}
}

func TestHistoryIsHidden(t *testing.T) {
	out, _, err := execute(t, "budget", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "history") {
		t.Fatalf("budget --help lists history:\n%s", out)
	}
}

func TestHistoryRefusesDatabaseOfAnotherFirebaseProject(t *testing.T) {
	f := newHistoryFixture(t)
	f.entry("20261002-090000-aaaa", time.Hour)
	t.Setenv("FUGARO_RTDB_URL", "https://other-fp-default-rtdb.firebaseio.com")
	_, _, err := execute(t, "budget", "history", "--sweep")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not Firebase project aurora-fp's") {
		t.Fatalf("err = %v, want a refusal naming the project", err)
	}
	if f.db.Value("agents/"+appSlug+"/20261002-090000-aaaa") == nil {
		t.Fatal("the sweep ran anyway")
	}
	if err := checkDatabaseHost("https://aurora-fp-default-rtdb.us-central1.firebasedatabase.app", "aurora-fp"); err != nil {
		t.Fatalf("the project's own database was refused: %v", err)
	}
}

// A database that can't be read is a remote error (exit 2), and nothing is
// written.
func TestHistoryRegistryUnreadableIsExit2(t *testing.T) {
	f := newHistoryFixture(t)
	f.entry("20261002-090000-aaaa", time.Hour)
	f.db.Refuse(http.StatusServiceUnavailable, "UNAVAILABLE", "", "down")
	_, _, err := execute(t, "budget", "history", "--sweep")
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v, want exit 2", err)
	}
	for _, r := range f.db.Requests() {
		if r.Method == http.MethodPatch || r.Method == http.MethodPut {
			t.Fatalf("wrote %s %s", r.Method, r.Path)
		}
	}
}
