package runner_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runner"
)

// objectsContaining lists the bucket objects under the run's prefix whose
// content contains needle.
func objectsContaining(t *testing.T, h *harness, needle string) []string {
	t.Helper()
	ctx := context.Background()
	it := h.bucket.List(nil)
	var out []string
	for obj, err := it.Next(ctx); err == nil; obj, err = it.Next(ctx) {
		data, err := h.bucket.ReadAll(ctx, obj.Key)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), needle) {
			out = append(out, obj.Key)
		}
	}
	return out
}

// TestVerifyRecordsInResultAreRedacted: failed test names come
// from reports the agent can write, so a secret in one is redacted in
// result.json and verify/<n>.json.
func TestVerifyRecordsInResultAreRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	// The model key happens to be part of a failing test's name.
	const secret = "Suite.beta"
	h.deps.Env = append(filterEnv(h.deps.Env, "ANTHROPIC_API_KEY"), "ANTHROPIC_API_KEY="+secret)
	h.fails(t, "beta")
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Verify) == 0 || len(rec.Verify[0].Failed) == 0 {
		t.Fatalf("no failed tests recorded: %+v", rec.Verify)
	}
	if keys := objectsContaining(t, h, secret); len(keys) > 0 {
		t.Fatalf("the secret is stored unredacted in %v", keys)
	}
}

// TestMountedSecretsAreRedactedFromTheStart: every secret the
// job mounts is named in FUGARO_SECRET_ENVS and redacted, whether or not
// the fugaro.yaml at the task's ref declares it.
func TestMountedSecretsAreRedactedFromTheStart(t *testing.T) {
	h := newHarness(t, "", nil)
	const mounted = "mounted-but-undeclared-value"
	h.deps.Env = append(h.deps.Env, "RENAMED_TOKEN="+mounted, runner.SecretEnvsVar+"=RENAMED_TOKEN,,UNSET_TOKEN")
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		// The agent reads it from the runner's /proc/<pid>/environ.
		_, _ = req.Transcript.Write([]byte(`{"type":"assistant","text":"` + mounted + `"}` + "\n"))
		_, _ = req.Stderr.Write([]byte(mounted + "\n"))
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, leak, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if keys := objectsContaining(t, h, mounted); len(keys) > 0 {
		t.Fatalf("the mounted secret is stored unredacted in %v", keys)
	}
}

// TestGitsOwnCloneFailureIsRedacted: FUGARO_REMOTE is a launcher's to set and
// a remote URL may carry a credential in a query parameter (?access_token=,
// Bitbucket's forge shape) or in a fragment, and git's own message about a
// remote it could not reach quotes the URL it was given, secret and all
// ("fatal: unable to access 'http://host/repo.git?access_token=…/'"). That text
// is not one this package words: redact() strips it only because bootstrap's
// addRemoteSecrets calls registered the credential first, before the run's
// record is claimed and long before the clone — and every surface the text
// reaches is asserted, the error Run returns included, because internal/cli
// prints that one (exec.go hands it to the command's error and
// cmd/fugaro/main.go writes it to the job's stderr).
//
// The named tests of the redaction stop before git ever runs — the admission
// refusals (TestRunnerRefusesARemoteWithoutQuotingItsCredential) never open the
// provider, and remoteDisplay is this package's own wording — so this is the
// end-to-end case: the clone really is attempted against a local server that
// hangs up on it, git really does fail, and the failure is carried into the
// run's record, the result.json beside it, and the error Run returns.
func TestGitsOwnCloneFailureIsRedacted(t *testing.T) {
	const secret = "ATBBsecret"
	// A server that hangs up on every request: git reports the URL it was
	// given, and nothing here leaves the machine.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hj, ok := w.(http.Hijacker); ok {
			if c, _, err := hj.Hijack(); err == nil {
				_ = c.Close()
			}
		}
	}))
	t.Cleanup(srv.Close)
	for _, tc := range []struct {
		name, remote string
		// Every spelling that must not survive into what the run publishes or
		// returns: the decoded secret always, and for the escaped case the
		// spelling git actually echoes as well.
		leaks []string
	}{
		// The two places a forge writes a token in, both handed to git
		// verbatim and both echoed back by git verbatim.
		{"a query parameter", srv.URL + "/remote.git?access_token=" + secret, []string{secret}},
		{"a fragment", srv.URL + "/remote.git#access_token=" + secret, []string{secret}},
		// And the escaped spelling: git echoes the URL as written, so a
		// redactor holding only the decoded value would miss it.
		{"an escaped query parameter", srv.URL + "/remote.git?access_token=A%54BBsecret", []string{secret, "A%54BBsecret"}},
		// A remote net/url refuses and git still reads and echoes whole: the
		// password holds a "/", which is "invalid port" to url.Parse, and git
		// hands back the URL with the userinfo and the query in it. The
		// fallback that reads the string as written (addRemoteSecretRaw) is
		// what covers this one — the parse is how the three places are found,
		// not a condition on registering them.
		{
			"an unparseable remote",
			strings.Replace(srv.URL, "http://", "http://user:ghp_a/b@", 1) + "/remote.git?access_token=" + secret,
			[]string{secret, "ghp_a/b", "user:ghp_a/b"},
		},
		// A bare token: no parameter name in front of it — a fragment that is
		// nothing but a value, which is the shape remoteDisplay's own test
		// (project_internal_test.go) takes for a credential. There is no name
		// to recognise, so the whole thing is what has to be registered.
		{"a bare-token fragment", srv.URL + "/remote.git#" + secret, []string{secret}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "", nil)
			h.deps.ProviderKind = "github"
			h.deps.Remote = tc.remote
			rec, err := h.run(t, implement("feature"))
			if err == nil {
				t.Fatalf("the clone must fail: rec = %+v", rec)
			}
			if rec == nil {
				t.Fatal("the run wrote no record")
			}
			// Git's own words, with the URL redacted: this is what makes the
			// assertions below about the clone and not about some earlier
			// refusal.
			if !strings.Contains(rec.Reason, "unable to access") {
				t.Fatalf("the record does not carry git's own failure, so nothing here saw it: %q", rec.Reason)
			}
			for _, leak := range tc.leaks {
				if strings.Contains(rec.Reason, leak) {
					t.Errorf("the run's record quotes the remote's credential (%q): %q", leak, rec.Reason)
				}
				// The error Run returns, which internal/cli prints.
				if strings.Contains(err.Error(), leak) {
					t.Errorf("the error Run returns quotes the remote's credential (%q), and it is printed to the job's stderr: %v", leak, err)
				}
				// Every object the run wrote, result.json among them.
				if keys := objectsContaining(t, h, leak); len(keys) > 0 {
					t.Errorf("the remote's credential (%q) is stored unredacted in %v", leak, keys)
				}
			}
			if data, err := h.bucket.ReadAll(context.Background(), "runs/"+repoSlug+"/"+runID+"/result.json"); err != nil {
				t.Errorf("reading result.json: %v", err)
			} else if !strings.Contains(string(data), "[REDACTED]") {
				t.Errorf("result.json does not carry the redacted failure: %s", data)
			}
		})
	}
}
