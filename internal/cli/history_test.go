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

func TestRolloverIsNotYetImplemented(t *testing.T) {
	newHistoryFixture(t)
	_, _, err := execute(t, "budget", "history", "--rollover")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "M9d") {
		t.Fatalf("err = %v, want exit 1 naming M9d", err)
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
