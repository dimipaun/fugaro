package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/runner"
)

// releaseSeal is a release base image whose runner reads the sealed token
// (sealSince): the budget fixtures' build record, so a launch that mints is
// not refused by the seal's image gate.
const releaseSeal = "ghcr.io/dimipaun/fugaro-base:" + sealSince

// markerKey is the seal key a launch handed its execution: the marker's value,
// strictly parsed. A launch that minted and handed anything else is a failure.
func markerKey(t *testing.T, env map[string]string) []byte {
	t.Helper()
	v, ok := env[runner.BudgetTokenEnv]
	if !ok {
		t.Fatalf("the launch carries no %s", runner.BudgetTokenEnv)
	}
	key, err := token.ParseKey(v)
	if err != nil {
		t.Fatalf("%s in the launch is not a seal key: %v", runner.BudgetTokenEnv, err)
	}
	return key
}

// lastLaunchKey is the seal key of the fixture's last jobs.run.
func lastLaunchKey(t *testing.T, f *cloudFixture) []byte {
	t.Helper()
	calls := f.run.RunRequests()
	if len(calls) == 0 {
		t.Fatal("nothing was launched")
	}
	return markerKey(t, calls[len(calls)-1].Env)
}

// unsealed opens the run's token object with key, failing the test if it is
// not a sealed envelope that opens under it.
func unsealed(t *testing.T, key []byte, data []byte, run string) string {
	t.Helper()
	if !token.Sealed(data) {
		t.Fatalf("the token object of %s is not sealed (%d bytes)", run, len(data))
	}
	tok, err := token.Unseal(key, token.ObjectKey(appSlug, run), data)
	if err != nil {
		t.Fatalf("the token object of %s does not open under the launch's key: %v", run, err)
	}
	return string(tok)
}

// launchedToken is the custom token the fixture's last launch sealed for run.
func launchedToken(t *testing.T, f *cloudFixture, run string) string {
	t.Helper()
	raw := readToken(t, f, run)
	if raw == "" {
		t.Fatalf("no token object for %s", run)
	}
	return unsealed(t, lastLaunchKey(t, f), []byte(raw), run)
}

// TestLaunchSealsTheTokenAndHandsItsKeyToTheExecution is §7.1's launcher half:
// the object in the bucket is a sealed envelope, not a JWT and not anything a
// reader of the bucket can use, and the key that opens it is the marker's
// value in this one execution's override (D15). The token inside is the run's
// own identity.
//
// Mutation (run, restored): pass a nil key to PutObject in mintBudgetToken —
// the plaintext fallback — and this test fails on "is not sealed".
func TestLaunchSealsTheTokenAndHandsItsKeyToTheExecution(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	var env map[string]string
	var atLaunch string
	f.run.OnRun = func(c gcpfake.RunCall) { env, atLaunch = c.Env, readToken(t, f.cloudFixture, budgetRun) }
	out, errOut, err := runBudget(t)
	if err != nil {
		t.Fatal(err)
	}
	if atLaunch == "" {
		t.Fatal("the sealed object was not in the bucket when jobs.run was called")
	}
	if strings.HasPrefix(atLaunch, "ey") {
		t.Fatal("the token object reads as a JWT: the launcher wrote it plaintext")
	}
	key := markerKey(t, env)
	tok := unsealed(t, key, []byte(atLaunch), budgetRun)
	// Nothing of the token is readable in the object: not the token, and
	// not its claims segment.
	if parts := strings.Split(tok, "."); strings.Contains(atLaunch, tok) || len(parts) != 3 || strings.Contains(atLaunch, parts[1]) {
		t.Fatal("the token object carries the token readable")
	}
	if c := claimsOf(t, f, tok); c["fs"] != appSlug || c["fr"] != budgetRun || c["fp"] != "aurora" {
		t.Fatalf("claims = %v", c)
	}
	// Neither the token nor the key is printed by the launcher.
	for _, secret := range []string{tok, token.EncodeKey(key)} {
		if strings.Contains(out+errOut, secret) {
			t.Fatal("the launcher printed the token or its key")
		}
	}
}

// TestRetryReSealsUnderANewKey is D17: a second mint of the same run — a
// --retry, or a takeover of a stale claim — draws a new key, seals under it
// and replaces the earlier attempt's object, so the earlier key opens nothing
// and no key is ever in front of two executions of the run.
//
// Mutation (run, restored): cache the key per run in mintBudgetToken (reuse
// the first attempt's key on the second mint) — the "same key" check fails.
func TestRetryReSealsUnderANewKey(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	env := memEnv(t, f.cloudFixture)
	spec := raceSpec(t, env)
	ctx := context.Background()
	name := token.ObjectKey(appSlug, spec.RunID)

	key1, err := env.mintBudgetToken(ctx, appSlug, spec, nil)
	if err != nil || key1 == nil {
		t.Fatalf("first mint: %v, %v", key1, err)
	}
	obj1, _, err := env.bucket.Read(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	key2, err := env.mintBudgetToken(ctx, appSlug, spec, nil)
	if err != nil || key2 == nil {
		t.Fatalf("second mint: %v, %v", key2, err)
	}
	if bytes.Equal(key1, key2) {
		t.Fatal("the retry re-used the first attempt's key: one key in front of two executions (D17)")
	}
	obj2, _, err := env.bucket.Read(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(obj1, obj2) {
		t.Fatal("the retry left the first attempt's object in place")
	}
	if _, err := token.Unseal(key1, name, obj2); !errors.Is(err, token.ErrUnseal) {
		t.Fatalf("the first attempt's key opens the retry's object: %v", err)
	}
	claimsOf(t, f, unsealed(t, key2, obj2, spec.RunID))
	if n := len(f.iam.SignCalls()); n != 2 {
		t.Fatalf("sign calls = %d, want a fresh mint per attempt", n)
	}
}

// echoBackend refuses every launch with an error that quotes the launch's
// own environment: what a platform error that echoes the request would do.
// The launcher must not pass the key through to its own error.
type echoBackend struct {
	backend.Backend
	rejected bool
	saw      []string
}

func (e *echoBackend) Launch(_ context.Context, spec backend.LaunchSpec) (backend.ExecutionRef, error) {
	e.saw = append(append(e.saw, spec.Env...), spec.SecretEnv...)
	if e.rejected {
		return backend.ExecutionRef{}, fmt.Errorf("refused request env=%v: %w", e.saw, backend.ErrRejected)
	}
	return backend.ExecutionRef{}, fmt.Errorf("timed out sending env=%v", e.saw)
}

// TestTheLaunchersErrorsNeverCarryTheKey: the key travels in the launch
// request, and a backend error that quotes the request would put it in the
// launcher's own error, then on the operator's terminal and in whatever
// records the command's output. The launcher redacts it from both the refused
// and the ambiguous error, and the refusal still unwraps to ErrRejected (the
// claim and the object are dropped on it).
//
// Mutation (run, restored): return the backend's error unredacted from
// launchRun — both subtests fail on "carries the key".
func TestTheLaunchersErrorsNeverCarryTheKey(t *testing.T) {
	for _, rejected := range []bool{true, false} {
		t.Run(fmt.Sprintf("rejected=%v", rejected), func(t *testing.T) {
			f := newLaunchBudget(t, "enforce")
			env := memEnv(t, f.cloudFixture)
			eb := &echoBackend{Backend: env.be, rejected: rejected}
			env.be = eb
			spec := raceSpec(t, env)
			_, err := launchRun(context.Background(), env, appSlug, spec, time.Now())
			if err == nil {
				t.Fatal("want an error")
			}
			var key string
			for _, kv := range eb.saw {
				if v, ok := strings.CutPrefix(kv, runner.BudgetTokenEnv+"="); ok {
					key = v
				}
			}
			if _, perr := token.ParseKey(key); perr != nil {
				t.Fatalf("the launch carried no seal key: %v", eb.saw)
			}
			if strings.Contains(err.Error(), key) {
				t.Fatalf("the launcher's error carries the key: %v", err)
			}
			if rejected != errors.Is(err, backend.ErrRejected) {
				t.Fatalf("errors.Is(err, ErrRejected) = %v, want %v: %v", !rejected, rejected, err)
			}
			_, _, rerr := env.bucket.Read(context.Background(), token.ObjectKey(appSlug, spec.RunID))
			if rejected && !errors.Is(rerr, blobx.ErrNotExist) {
				t.Fatalf("a refused launch left its sealed object behind: %v", rerr)
			}
		})
	}
}

// TestAKeyThatCannotBeDrawnRefusesTheLaunch: a failed draw of the key is a
// refusal, never a plaintext object, and nothing of the launch remains — no
// object, no execution, the claim released.
func TestAKeyThatCannotBeDrawnRefusesTheLaunch(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	prev := newSealKey
	newSealKey = func() ([]byte, error) { return nil, errors.New("no entropy") }
	t.Cleanup(func() { newSealKey = prev })
	if _, _, err := runBudget(t); err == nil || !strings.Contains(err.Error(), "seal key") {
		t.Fatalf("err = %v, want the seal key's refusal", err)
	}
	if launchedAny(f) || readToken(t, f.cloudFixture, budgetRun) != "" {
		t.Fatal("a launch whose key could not be drawn launched or left an object")
	}
	newSealKey = prev
	if _, _, err := runBudget(t); err != nil {
		t.Fatalf("the claim was not released: %v", err)
	}
}

// TestTheSealGateRefusesAnImageThatPredatesTheSeal is D19's ordering, as a
// gate: a launch that seals hands the execution a key in the marker, and a
// runner from before the seal echoes the marker's value into its bootstrap
// error (§7.4) and reads the token from the old place. So a launch that mints
// is refused against a job image whose runner predates sealSince, before the
// claim, the mint or the object, with the fix named; a launch that mints
// nothing is not judged at all.
//
// Mutation (run, restored): drop the seal gate from finishLaunch — the "0.8.1"
// and "no record" subtests launch and fail.
// Mutation (run, restored): return nil from sealDevRefusal — the
// "development image older than the seal" subtest launches and fails.
func TestTheSealGateRefusesAnImageThatPredatesTheSeal(t *testing.T) {
	for _, tc := range []struct {
		name, base string
		record     bool
		builtAt    time.Time
		sealed     bool // the record says its base's runner reads the sealed token
		refused    string
		warns      string
	}{
		{"a release before the seal", "ghcr.io/dimipaun/fugaro-base:0.8.1", true, time.Time{}, false, "release 0.8.1", ""},
		{"no build record", "", false, time.Time{}, false, "has no build record", ""},
		{"the seal's release", releaseSeal, true, time.Time{}, false, "", ""},
		// A development image built before the seal's runner half existed
		// cannot carry it: refused, naming the image and the seal (the
		// owner's answer to open question 2, 2026-10-17).
		{"a development image older than the seal", devSealBase, true, sealRunnerIntroduced.Add(-time.Hour), false, "predates the seal", ""},
		// One built after may still run an older base: whether it supports
		// the seal cannot be shown, so it warns and is not refused.
		{"a development image built after the seal", devSealBase, true, sealRunnerIntroduced.Add(time.Hour), false, "", "not a release base image"},
		{"a development image of unknown age", devSealBase, true, time.Time{}, false, "", "not a release base image"},
		// A record whose base's own fugaro says its runner reads the sealed
		// token (it is the base's binary that renders the record) is shown to
		// support the seal: no refusal and no warning.
		{"a development image whose base says it reads the seal", devSealBase, true, sealRunnerIntroduced.Add(time.Hour), true, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchBudget(t, "enforce")
			if err := removeBuildRecord(f.cloudFixture); err != nil {
				t.Fatal(err)
			}
			if tc.record {
				writeBuildRecordAt(t, f.cloudFixture, appSlug, "web", tc.base, tc.builtAt, tc.sealed)
			}
			_, errOut, err := runBudget(t)
			if tc.refused == "" {
				if err != nil {
					t.Fatal(err)
				}
				if tc.warns != "" && !strings.Contains(errOut, tc.warns) {
					t.Fatalf("stderr = %q, want a warning that says %q", errOut, tc.warns)
				}
				// The warning says what is at stake and what to do, not only
				// that nothing was checked.
				for _, want := range []string{"peer runs of the repository can read", "fugaro image build --repo acme/app --workflow web"} {
					if tc.warns != "" && !strings.Contains(errOut, want) {
						t.Fatalf("stderr = %q, want the warning to say %q", errOut, want)
					}
				}
				if tc.warns == "" && strings.Contains(errOut, "sealed budget token") {
					t.Fatalf("stderr = %q, want no seal warning", errOut)
				}
				return
			}
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error", err)
			}
			wants := []string{tc.refused, "fugaro " + sealSince, "seal", "fugaro image refresh"}
			if tc.base == devSealBase {
				wants = []string{tc.refused, devSealBase, "fugaro image build"}
			}
			for _, want := range wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if launchedAny(f) || readToken(t, f.cloudFixture, budgetRun) != "" || len(f.iam.SignCalls()) != 0 {
				t.Fatal("a refused launch minted, sealed or launched")
			}
		})
	}
	t.Run("a launch that mints nothing is not judged", func(t *testing.T) {
		f := newCloudFixture(t)
		writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.8.1")
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", budgetRun, "Add a feature"); err != nil {
			t.Fatal(err)
		}
	})
}

// TestTheSealGateJudgesACloneJobImage is sealGate's `checkout: clone` branch:
// such a job is never built, so it has no build record to judge — the image a
// clone launch runs is the installation's own base_images.base ref, and that
// ref is the judge (checkCloneImageSince). The branch is reached by a launch
// whose workflow is `checkout: clone`, which no other test drives, so the gate
// is called directly with the spec such a launch carries.
//
// Two things the branch must get right: a release older than the seal is
// refused (the fall of the seal is the key in the marker, which that runner
// echoes into the run's record), and a development base is warned about *once*,
// by the seal's own warning, which carries the stake and the cure — falling
// through to checkCloneImageSince as well would print two warnings saying one
// thing.
//
// Mutation (run, restored): return nil from the clone branch before
// checkCloneImageSince — "a release before the seal" launches.
// Mutation (run, restored): let a development base fall through to
// checkCloneImageSince as well as the seal's warning — "a development base"
// warns twice and fails on the count.
func TestTheSealGateJudgesACloneJobImage(t *testing.T) {
	previousRelease := "ghcr.io/dimipaun/fugaro-base:0.8.1"
	for _, tc := range []struct {
		name, base string
		refused    string
		warns      string
	}{
		{"a release before the seal", previousRelease, "release 0.8.1", ""},
		{"the seal's release", releaseSeal, "", ""},
		{"a development base", devSealBase, "", "is not a release base image"},
		{"no base image", "", "is not set", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchBudget(t, "enforce")
			if tc.base != "" {
				f.appendConfig(t, "base_images: {base: "+tc.base+"}\n")
			}
			env := memEnv(t, f.cloudFixture)
			spec := raceSpec(t, env)
			var warn bytes.Buffer
			err := env.sealGate(context.Background(), appSlug, spec, "web", true, &warn)
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("err = %v, want no refusal (stderr %q)", err, warn.String())
				}
				if tc.warns != "" && !strings.Contains(warn.String(), tc.warns) {
					t.Fatalf("stderr = %q, want a warning that says %q", warn.String(), tc.warns)
				}
				if n := strings.Count(warn.String(), "not a release base image"); n > 1 {
					t.Fatalf("stderr = %q: the base image is said not to be a release %d times, want once", warn.String(), n)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.refused) {
				t.Fatalf("err = %v, want a refusal saying %q", err, tc.refused)
			}
			if warn.Len() != 0 {
				t.Fatalf("a refused launch warned too (%q): the gate refuses or warns, never both", warn.String())
			}
		})
	}
}

// devSealBase is a development base image (not a release): its age is the
// build record's built_at, the only thing the seal gate can tell from it.
const devSealBase = "europe-docker.pkg.dev/p/r/fugaro-base:dev-abc"

// writeBuildRecordAt is writeBuildRecord with the record's built_at.
func writeBuildRecordAt(t *testing.T, f *cloudFixture, slug, workflow, baseRef string, builtAt time.Time, sealed bool) {
	t.Helper()
	data, err := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/app", Workflow: workflow, BaseRef: baseRef, FugaroVersion: "0.5.0", BuiltAt: builtAt, SealedBudgetToken: sealed})
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, imagecheck.RecordKey(slug, workflow), string(data))
}

// removeBuildRecord deletes the fixture's build record of acme/app's web
// workflow, the one newLaunchBudget writes.
func removeBuildRecord(f *cloudFixture) error {
	return os.Remove(filepath.Join(f.dir, "runs", filepath.FromSlash(imagecheck.RecordKey(appSlug, "web"))))
}

// TestSealRedactRemovesBothSpellings: sealRedact takes the key out of an
// error in its marker spelling (unpadded base64url) and as its raw bytes, and
// the result still unwraps to the original (a refusal stays ErrRejected).
// The key here is printable on purpose, so its raw spelling can be found in
// the text at all.
//
// Mutation (run, restored): drop string(key) from sealRedact's list — the raw
// subtest fails.
func TestSealRedactRemovesBothSpellings(t *testing.T) {
	key := []byte(strings.Repeat("0123456789abcdef", 2))
	for name, spelled := range map[string]string{"encoded": token.EncodeKey(key), "raw": string(key)} {
		t.Run(name, func(t *testing.T) {
			err := sealRedact(fmt.Errorf("refused env=FUGARO_BUDGET_TOKEN=%s: %w", spelled, backend.ErrRejected), key)
			if strings.Contains(err.Error(), spelled) {
				t.Fatalf("the redacted error carries the key's %s spelling: %v", name, err)
			}
			if !errors.Is(err, backend.ErrRejected) {
				t.Fatalf("the redacted error lost its kind: %v", err)
			}
		})
	}
	if sealRedact(nil, key) != nil {
		t.Fatal("a nil error is not nil after redaction")
	}
}

// envRecorder keeps the two environment lists apart: a backend that wraps the
// fixture's own and records what the launcher handed it, then launches.
type envRecorder struct {
	backend.Backend
	env, secretEnv []string
}

func (b *envRecorder) Launch(ctx context.Context, spec backend.LaunchSpec) (backend.ExecutionRef, error) {
	b.env, b.secretEnv = spec.Env, spec.SecretEnv
	return b.Backend.Launch(ctx, spec)
}

// TestTheSealKeyTravelsAsASecretEnvEntry: the marker is a SECRET entry
// (backend.LaunchSpec.SecretEnv, D15), never a plain one, and that is the whole
// carrier's safety. A plain entry is part of the execution's ordinary
// environment: it is visible in the job's definition and in every listing of
// it, and no formatter redacts it — while a secret entry is carried only where
// the design accepts its audience, and a backend that cannot carry one where
// only the execution and the override's own audience read it refuses the
// launch (the Batch backend's guard) rather than put the key in a job
// definition. internal/backend/backend.go's LaunchSpec comments say exactly
// that (Env is plain and carries no secret; the marker is SecretEnv's), and the
// fact is pinned here, where a launcher that followed the old comment — the
// marker in Env — would break it.
//
// Mutation (verified, run and restored): set lspec.Env instead of
// lspec.SecretEnv in internal/cli/run.go's launch — this fails on the plain
// list carrying the marker.
func TestTheSealKeyTravelsAsASecretEnvEntry(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	env := memEnv(t, f.cloudFixture)
	rec := &envRecorder{Backend: env.be}
	env.be = rec
	spec := raceSpec(t, env)
	// launchRun, not the command: the command builds its own backend from the
	// fixture, so only a direct launch hands the spec to a recorder.
	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(rec.env) != 0 {
		t.Errorf("the launch put %v in the plain Env list: the budget marker's value is the launch's seal key, and a plain entry is readable through the job's definition by every launcher (backend.LaunchSpec.SecretEnv)", rec.env)
	}
	var key string
	for _, kv := range rec.secretEnv {
		if v, ok := strings.CutPrefix(kv, runner.BudgetTokenEnv+"="); ok {
			key = v
		}
	}
	if key == "" {
		t.Fatalf("the seal key is not among the launch's secret entries: %v", rec.secretEnv)
	}
	if _, err := token.ParseKey(key); err != nil {
		t.Fatalf("the marker's secret value is not a seal key: %v", err)
	}
}
