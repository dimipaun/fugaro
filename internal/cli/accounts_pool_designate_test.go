package cli

// accounts_pool_designate_test.go is PL5's own file: the acts a person makes
// for themselves through the installation's two services — recording their own
// designation for the project (the credentials service's designation route,
// PL4) and contributing their own token to the shared pool (the secrets intake,
// D3194/R9). Neither call takes a principal from the caller: both services key
// what they write on the identity the person's own Google ID token carries, and
// the wire types have no field a principal could be written in.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/routing"
)

// designateSeen is one designation call the command made, as the fake below
// recorded it.
type designateSeen struct {
	URL string
	Req credsvc.PoolDesignateRequest
}

// fakeDesignateCalls replaces the one seam the designation call goes through,
// and answers with the record the service would have written. No test mints a
// real identity token or reaches a service.
func fakeDesignateCalls(t *testing.T, answer routing.Designation) *[]designateSeen {
	t.Helper()
	seen := &[]designateSeen{}
	prev := accountsPoolDesignateDo
	accountsPoolDesignateDo = func(_ context.Context, url string, req credsvc.PoolDesignateRequest) (routing.Designation, error) {
		*seen = append(*seen, designateSeen{URL: url, Req: req})
		return answer, nil
	}
	t.Cleanup(func() { accountsPoolDesignateDo = prev })
	return seen
}

// TestDesignateIsTheCallersOwn is PL5's red step: `fugaro accounts pool
// designate <pool>` asks the credentials service's designation route (PL4) to
// record the caller's own designation for the project, from the person's own
// session. What the wire carries is the pool and nothing else, because a
// designation is a principal's statement about itself and the principal is the
// subject the service verified (R26).
func TestDesignateIsTheCallersOwn(t *testing.T) {
	intakeFixture(t)
	want := routing.Designation{
		Principal: routing.Principal{Kind: routing.PrincipalPerson, ID: "102938475610293847561"},
		Project:   "aurora",
		Pool:      "personal",
	}
	seen := fakeDesignateCalls(t, want)

	out, _, err := execute(t, "accounts", "pool", "designate", "personal")
	if err != nil {
		t.Fatalf("accounts pool designate: %v\n%s", err, out)
	}
	if len(*seen) != 1 {
		t.Fatalf("the command made %d calls, want one", len(*seen))
	}
	got := (*seen)[0]
	if got.URL != intakeTestCredentialsURL {
		t.Errorf("the designation asked %s, want the installation's own credentials service (%s): the address is read from the project's config and never from a flag", got.URL, intakeTestCredentialsURL)
	}
	if got.Req != (credsvc.PoolDesignateRequest{Pool: "personal"}) {
		t.Errorf("the designation sent %#v, want the pool alone: the principal is the identity the service verifies, and the request has no field for one (R26)", got.Req)
	}
	if !strings.Contains(out, "personal") {
		t.Errorf("the answer does not name the pool it recorded:\n%s", out)
	}

	// --json is the record the service wrote, and nothing this command added.
	*seen = (*seen)[:0]
	jout, _, err := execute(t, "accounts", "pool", "designate", "personal", "--json")
	if err != nil {
		t.Fatalf("accounts pool designate --json: %v\n%s", err, jout)
	}
	var doc routing.Designation
	if err := json.Unmarshal([]byte(jout), &doc); err != nil {
		t.Fatalf("--json printed %q, want the service's own record: %v", jout, err)
	}
	if doc != want {
		t.Errorf("--json printed %+v, want %+v", doc, want)
	}

	// A pool name the grammar does not build is refused before the service is
	// reached: the pool is a name, and a name is what the route resolves.
	*seen = (*seen)[:0]
	if _, _, err := execute(t, "accounts", "pool", "designate", "Not_A_Pool"); err == nil {
		t.Error("a pool name outside the grammar was accepted")
	}
	if len(*seen) != 0 {
		t.Errorf("a refused name reached the service: %#v", *seen)
	}

	// And the person's own session is the whole of who may run it: an agent's
	// session and a run's own process are refused before the token is minted.
	for _, env := range []string{"CLAUDECODE", "FUGARO_RUN"} {
		t.Run(env, func(t *testing.T) {
			intakeFixture(t)
			t.Setenv(env, "1")
			seen := fakeDesignateCalls(t, want)
			if _, _, err := execute(t, "accounts", "pool", "designate", "personal"); err == nil {
				t.Fatalf("a designation was recorded with %s set", env)
			} else if !strings.Contains(err.Error(), env) {
				t.Errorf("the refusal does not name the marker that refused it: %v", err)
			}
			if len(*seen) != 0 {
				t.Errorf("%s reached the service: the refusal is the client's own and it comes first", env)
			}
		})
	}
}

// TestContributeIsTheHoldersOwn: `fugaro accounts pool contribute` takes the
// caller's own token — read from stdin, never an argument — through the intake
// that already receives secret values, into a member of a pool an admin marked
// as taking contributions (D3194, R9). Running it with no --member marks the
// pool: the pool's own member is the mark, and only an admin may write it. The
// contributor withdraws their own member and an admin removes any.
func TestContributeIsTheHoldersOwn(t *testing.T) {
	intakeFixture(t)
	answer := func(c secretsIntakeCall) {
		switch out := c.Out.(type) {
		case *intake.SecretAnswer:
			*out = intake.SecretAnswer{ID: intakeTestID(intake.ScopeShared), Version: "2", Created: true}
		case *intake.DeleteAnswer:
			*out = intake.DeleteAnswer{Deleted: []string{intakeTestID(intake.ScopeShared)}}
		}
	}
	seen := fakeIntakeCalls(t, answer)

	out, _, err := executeStdin(t, intakeTestValue+"\n", "accounts", "pool", "contribute", "--to", "team", "--member", "alice")
	if err != nil {
		t.Fatalf("accounts pool contribute: %v\n%s", err, out)
	}
	if len(*seen) != 1 {
		t.Fatalf("the command made %d calls, want one", len(*seen))
	}
	got := (*seen)[0]
	if got.URL != intakeTestIntakeURL {
		t.Errorf("the contribution asked %s, want the installation's intake (%s)", got.URL, intakeTestIntakeURL)
	}
	if got.Call.Path != intake.CreatePath {
		t.Errorf("the contribution asked %q, want the intake's create route (%q)", got.Call.Path, intake.CreatePath)
	}
	want := intake.CreateRequest{
		Scope: intake.ScopeShared,
		Name:  "alice",
		Pool:  "team",
		Value: base64.StdEncoding.EncodeToString([]byte(intakeTestValue)),
	}
	if got.Call.Body != want {
		t.Errorf("the contribution sent %#v, want %#v", got.Call.Body, want)
	}
	if strings.Contains(out, intakeTestValue) {
		t.Errorf("the answer carries the value: %s", out)
	}
	if !strings.Contains(out, "alice") || !strings.Contains(out, "team") {
		t.Errorf("the answer does not name the member and the pool:\n%s", out)
	}

	// The mark: no --member means the pool's own member, which is the marker an
	// admin writes and every later contribution is admitted against.
	*seen = (*seen)[:0]
	if _, _, err := executeStdin(t, intakeTestValue+"\n", "accounts", "pool", "contribute", "--to", "team"); err != nil {
		t.Fatalf("accounts pool contribute (the mark): %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("the mark made %d calls, want one", len(*seen))
	}
	if body, ok := (*seen)[0].Call.Body.(intake.CreateRequest); !ok || body.Name != "team" || body.Pool != "team" {
		t.Errorf("the mark sent %#v, want the pool's own member (name and pool both team)", (*seen)[0].Call.Body)
	}

	// The withdrawal and the admin's removal are the same act, admitted to
	// different people: the intake decides whose member it is.
	for _, argv := range [][]string{
		{"accounts", "pool", "withdraw", "team", "--member", "alice"},
		{"accounts", "pool", "rm-member", "team", "--member", "alice"},
	} {
		*seen = (*seen)[:0]
		out, _, err := execute(t, argv...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(argv, " "), err, out)
		}
		if len(*seen) != 1 {
			t.Fatalf("%s made %d calls, want one", strings.Join(argv, " "), len(*seen))
		}
		if (*seen)[0].Call.Path != intake.DeletePath {
			t.Errorf("%s asked %q, want the intake's delete route (%q)", strings.Join(argv, " "), (*seen)[0].Call.Path, intake.DeletePath)
		}
		want := intake.DeleteRequest{Scope: intake.ScopeShared, Name: "alice", Pool: "team"}
		if (*seen)[0].Call.Body != want {
			t.Errorf("%s sent %#v, want %#v", strings.Join(argv, " "), (*seen)[0].Call.Body, want)
		}
	}

	// A member this command cannot name is refused before the intake is
	// reached: the member is the name the pool's container carries.
	*seen = (*seen)[:0]
	if _, _, err := execute(t, "accounts", "pool", "withdraw", "team"); err == nil {
		t.Error("a withdrawal with no member was accepted")
	}
	if len(*seen) != 0 {
		t.Errorf("a refused withdrawal reached the intake: %#v", *seen)
	}

	// The value is stdin's and never an argument: a second argument is refused
	// before anything is read, and nothing reaches the intake.
	*seen = (*seen)[:0]
	if _, _, err := execute(t, "accounts", "pool", "contribute", "--to", "team", "--member", "alice", intakeTestValue); err == nil {
		t.Error("a contribution with the value as an argument was accepted")
	}
	if len(*seen) != 0 {
		t.Errorf("a value typed as an argument reached the intake: %#v", *seen)
	}

	// And the person's own session is the whole of who may run it (F9, F12).
	for _, env := range []string{"CLAUDECODE", "FUGARO_RUN"} {
		t.Run(env, func(t *testing.T) {
			intakeFixture(t)
			t.Setenv(env, "1")
			seen := fakeIntakeCalls(t, answer)
			if _, _, err := executeStdin(t, intakeTestValue+"\n", "accounts", "pool", "contribute", "--to", "team", "--member", "alice"); err == nil {
				t.Fatalf("a token was contributed with %s set", env)
			} else if !strings.Contains(err.Error(), env) {
				t.Errorf("the refusal does not name the marker that refused it: %v", err)
			}
			if len(*seen) != 0 {
				t.Errorf("%s reached the intake: the refusal is the client's own and it comes first", env)
			}
		})
	}
}
