package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// fakeUser replaces the credentials' owner and counts the lookups.
func fakeUser(t *testing.T, email string, err error) *int {
	t.Helper()
	n := new(int)
	old := authenticatedUser
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { *n++; return email, err }
	t.Cleanup(func() { authenticatedUser = old })
	return n
}

// freshEngine is the engine of a first run: the config file to write is new.
func freshEngine(t *testing.T, r *initRig, o *initOptions) *initEngine {
	t.Helper()
	e := rigEngine(t, r, o)
	e.old = nil
	return e
}

func runInstallation(t *testing.T, e *initEngine) (*initflow.Result, string) {
	t.Helper()
	var out strings.Builder
	e.r.w = &out
	opts := e.options()
	opts.Out = &out
	res, err := initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e}, newInstallationStage(e)}, opts)
	if err != nil {
		t.Fatal(err)
	}
	return res, out.String()
}

func members(v any) []string {
	var out []string
	for _, m := range v.([]any) {
		out = append(out, m.(string))
	}
	return out
}

// A new project config grants the launcher and operator roles to the person
// running init, before the plan; the config records them.
func TestFirstRunDefaultsMembersToTheUser(t *testing.T) {
	r := newInitRig(t)
	n := fakeUser(t, "me@example.com", nil)
	e := freshEngine(t, r, &initOptions{yes: true})
	res, out := runInstallation(t, e)
	if res.Failed != nil || res.Stages[1].State != initflow.Changed || *n != 1 {
		t.Fatalf("%+v, lookups %d\n%s", res.Stages, *n, out)
	}
	v := r.tfvars(t)
	if got := members(v["launchers"]); !slices.Equal(got, []string{"user:me@example.com"}) {
		t.Errorf("launchers %v", got)
	}
	if got := members(v["operators"]); !slices.Equal(got, []string{"user:me@example.com"}) {
		t.Errorf("operators %v", got)
	}
	if !strings.Contains(out, "launchers: user:me@example.com (you); operators: user:me@example.com (you); change with --launcher/--operator") {
		t.Errorf("not said:\n%s", out)
	}
	data, err := os.ReadFile(e.path)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse(data)
	if err != nil || !slices.Equal(lc.Terraform.Launchers, []string{"user:me@example.com"}) || !slices.Equal(lc.Terraform.Operators, []string{"user:me@example.com"}) {
		t.Fatalf("the config does not record them: %v\n%s", err, data)
	}
}

// Flags win, and any of them leaves the defaults alone (no lookup at all).
// The launchers an apply grants are the launchers and the operators together.
func TestMemberFlagsOverrideTheDefault(t *testing.T) {
	for name, tc := range map[string]struct {
		o         initOptions
		launchers []string // the granted launchers (launchers and operators together)
		operators []string
	}{
		"--launcher": {initOptions{yes: true, launchers: []string{"user:ann@example.com"}, launchersChanged: true},
			[]string{"user:ann@example.com"}, []string{}},
		"--operator": {initOptions{yes: true, operators: []string{"group:ops@example.com"}, operatorsChanged: true},
			[]string{"group:ops@example.com"}, []string{"group:ops@example.com"}},
		"both": {initOptions{yes: true, launchers: []string{"user:a@example.com"}, operators: []string{"user:b@example.com"}, launchersChanged: true, operatorsChanged: true},
			[]string{"user:a@example.com", "user:b@example.com"}, []string{"user:b@example.com"}},
		"--launcher '' (nobody)": {initOptions{yes: true, launchers: []string{}, launchersChanged: true}, []string{}, []string{}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newInitRig(t)
			n := fakeUser(t, "me@example.com", nil)
			o := tc.o
			e := freshEngine(t, r, &o)
			res, out := runInstallation(t, e)
			if res.Failed != nil || *n != 0 || strings.Contains(out, "(you)") {
				t.Fatalf("%+v, lookups %d\n%s", res.Failed, *n, out)
			}
			v := r.tfvars(t)
			if got := members(v["launchers"]); !slices.Equal(got, tc.launchers) {
				t.Errorf("launchers %v, want %v", got, tc.launchers)
			}
			if got := members(v["operators"]); !slices.Equal(got, tc.operators) {
				t.Errorf("operators %v, want %v", got, tc.operators)
			}
		})
	}
}

// An existing config is never changed implicitly: no lookup, no default.
func TestExistingConfigIsNotDefaulted(t *testing.T) {
	r := newInitRig(t)
	n := fakeUser(t, "me@example.com", nil)
	e := rigEngine(t, r, &initOptions{yes: true}) // old is the file's content
	res, out := runInstallation(t, e)
	if res.Failed != nil || *n != 0 || strings.Contains(out, "(you)") {
		t.Fatalf("%+v, lookups %d\n%s", res.Failed, *n, out)
	}
	if got := members(r.tfvars(t)["launchers"]); len(got) != 0 {
		t.Errorf("launchers %v", got)
	}
}

// Adopting an installation keeps its own launchers: the default is not made.
func TestAdoptKeepsTheInstallationsMembers(t *testing.T) {
	r := newInitRig(t)
	r.installationState(t)
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"launchers": []string{"group:eng@example.com"}})}
	r.save(t)
	n := fakeUser(t, "me@example.com", nil)
	e := adoptEngine(t, r, &initOptions{yes: true})
	runInstallation(t, e)
	data, err := os.ReadFile(e.path)
	if err != nil || *n != 0 || !strings.Contains(string(data), "group:eng@example.com") || strings.Contains(string(data), "me@example.com") {
		t.Fatalf("lookups %d, err %v\n%s", *n, err, data)
	}
}

// With no person to grant (a service account, no credentials) the run says
// which flags to pass and applies nothing.
func TestNoUserNeedsTheFlags(t *testing.T) {
	for name, err := range map[string]error{"service account": errServiceAccount, "no credentials": errors.New("no Google credentials")} {
		t.Run(name, func(t *testing.T) {
			r := newInitRig(t)
			fakeUser(t, "", err)
			e := freshEngine(t, r, &initOptions{yes: true})
			res, out := runInstallation(t, e)
			if len(res.Left) != 1 || res.Left[0].Stage != "installation" || res.ExitCode() != 1 {
				t.Fatalf("%+v\n%s", res, out)
			}
			lf := res.Left[0]
			if len(lf.Commands) != 1 || !strings.Contains(lf.Commands[0], "--launcher user:<your-email>") || !strings.Contains(lf.Commands[0], "--operator user:<your-email>") || strings.Contains(lf.Commands[0], "\n") {
				t.Errorf("left %+v", lf)
			}
			if !strings.Contains(lf.Text, "nobody can run anything") || n(r.ran(t, "apply")) != 0 {
				t.Errorf("left %+v, calls %q", lf, r.calls(t))
			}
		})
	}
}

func n(c [][]string) int { return len(c) }

// The review screen names what was defaulted.
func TestReviewScreenNamesDefaultedMembers(t *testing.T) {
	r := newInitRig(t)
	fakeUser(t, "me@example.com", nil)
	r.markedRuns(t, initProjectName, initProject)
	e := freshEngine(t, r, &initOptions{})
	e.old = nil
	var out strings.Builder
	e.r.w = &out
	if err := e.defaultMembers(t.Context()); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	e.reviewScreen(t.Context(), "test")
	if !strings.Contains(out.String(), "launchers:      user:me@example.com (default: you)") {
		t.Errorf("\n%s", out.String())
	}
}

// userEmail: what the userinfo endpoint may answer.
func TestUserEmail(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
		err    string
		sa     bool
	}{
		"a user":            {200, `{"email":"Me@Example.com","email_verified":true}`, "me@example.com", "", false},
		"unverified":        {200, `{"email":"me@example.com","email_verified":false}`, "", "not verified", false},
		"no email":          {200, `{"sub":"1"}`, "", "no email", false},
		"service account":   {200, `{"email":"a@proj.iam.gserviceaccount.com","email_verified":true}`, "", "", true},
		"not json":          {200, `x`, "", "no email", false},
		"denied":            {401, `{}`, "", "401", false},
		"a list in a field": {200, `{"email":"a@b.com,c@d.com","email_verified":true}`, "", "not an email", false},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			got, err := userEmail(t.Context(), srv.Client(), srv.URL)
			switch {
			case tc.sa:
				if !errors.Is(err, errServiceAccount) {
					t.Fatalf("%q, %v", got, err)
				}
			case tc.err != "":
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("%q, %v", got, err)
				}
			case err != nil || got != tc.want:
				t.Fatalf("%q, %v", got, err)
			}
		})
	}
}

// no_auth (every fake run) never asks Google who you are.
func TestDefaultUserLookupNeverRunsWithFakeAuth(t *testing.T) {
	lc := &localcfg.Config{Endpoints: localcfg.Endpoints{NoAuth: true}}
	if _, err := authenticatedUser(t.Context(), lc); err == nil {
		t.Fatal("a lookup with no_auth succeeded")
	}
}

// init --firebase on a first run gets the same default (its first apply is
// the installation), unless the installation exists.
func TestFirstFirebaseRunDefaultsMembers(t *testing.T) {
	r := newFBRig(t)
	r.script["plan"] = map[string]any{"exit": 2}
	r.save(t)
	n := fakeUser(t, "me@example.com", nil)
	e := freshEngine(t, r.initRig, &initOptions{yes: true, firebase: fpID})
	var out strings.Builder
	e.r.w = &out
	opts := e.options()
	opts.Out = &out
	if _, err := initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e}, newFirebaseStage(e)}, opts); err != nil {
		t.Fatal(err)
	}
	if got := members(r.tfvars(t)["launchers"]); *n != 1 || !slices.Equal(got, []string{"user:me@example.com"}) {
		t.Fatalf("lookups %d, launchers %v\n%s", *n, got, out.String())
	}
}

// defaultMembers itself refuses an existing config, whoever calls it.
func TestDefaultMembersRefusesAnExistingConfig(t *testing.T) {
	r := newInitRig(t)
	n := fakeUser(t, "me@example.com", nil)
	e := rigEngine(t, r, &initOptions{})
	before := e.spec
	if err := e.defaultMembers(t.Context()); err != nil || *n != 0 || e.defaulted != "" || e.r.o.launchersChanged || !slices.Equal(e.spec.Launchers, before.Launchers) {
		t.Fatalf("err %v, lookups %d, defaulted %q", err, *n, e.defaulted)
	}
}
