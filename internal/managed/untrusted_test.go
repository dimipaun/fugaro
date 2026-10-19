package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The sites this file is about are the ones whose text is not a plan's but the
// project's, the cloud's or the agent's own root: a project name read back from
// the marker, the identity an impersonation read answered with, a file name
// inside the root (and the file's own words under it), and the message of an
// API's or terraform's error. Task 3.2 sanitised the refusal's own fields
// (TestEveryManagedRefusalSiteBoundsUntrustedText); these are its carry-overs,
// and each class is fed a hostile value here and read back off the message a
// person — or the coding agent the refusal is aimed at — would see.
//
// The values are hostileValues()'s, so a class that passes one and not another
// is a failure and not a gap: an escape sequence, a bidi override that rewrites
// what the line says, a newline that forges a line of this program's output,
// and ten thousand characters that push the sentence off the screen.

// TestTheProjectsOwnNamesAreBoundedAtEverySiteThatPrintsThem: the project and
// its GCP project reach Request.what() from a configuration AND from the
// marker the cloud answers with, and what() is interpolated into the refusals
// of ready, fact and refuseSession, and into DeleteAdvice's bucket.
func TestTheProjectsOwnNamesAreBoundedAtEverySiteThatPrintsThem(t *testing.T) {
	for name, v := range hostileValues() {
		t.Run(name, func(t *testing.T) {
			// ready: a request with the names and no terraform to run.
			r := newApplyRig(t, plainRoot())
			req := r.request(t)
			req.Project, req.GCPProject = v, v
			req.Terraform = nil
			if _, err := Apply(context.Background(), req); !errors.Is(err, ErrRequest) {
				t.Fatalf("a request with no terraform got %v, want ready's refusal", err)
			} else {
				bounded(t, name+" in ready's refusal", err.Error(), 1)
			}

			// fact: the names, and a creation fact that is not managed.
			r = newApplyRig(t, plainRoot())
			req = r.request(t)
			req.Project, req.GCPProject = v, v
			req.Fact = Fact{Reason: "its creation fact was not believed"}
			if _, err := Apply(context.Background(), req); !errors.Is(err, ErrNotManaged) {
				t.Fatalf("an unmanaged project got %v, want fact's refusal", err)
			} else {
				bounded(t, name+" in fact's refusal", err.Error(), 1)
			}

			// refuseSession: the addresses of the session's own command line,
			// and the runs bucket the refusal points the owner at (built from
			// the GCP project).
			r = newApplyRig(t, plainRoot())
			req = r.request(t)
			req.Project, req.GCPProject = v, v
			req.Session = []string{v + "\n  and a forged line", "module.managed.x.y"}
			if _, err := Apply(context.Background(), req); !errors.Is(err, ErrSessionAddresses) {
				t.Fatalf("a session with addresses got %v, want the session's refusal", err)
			} else {
				bounded(t, name+" in refuseSession's refusal", err.Error(), 1)
			}

			// And the advice, which names the same bucket.
			bounded(t, name+" in DeleteAdvice", DeleteAdvice(v), 1)
		})
	}
}

// TestASpecsNamesAreBoundedInTheGatesOwnError: Gate reads the saved plan
// through a terraform whose failure carries the plan file's name and its own
// stderr, and it names the project in the same sentence.
func TestASpecsNamesAreBoundedInTheGatesOwnError(t *testing.T) {
	for name, v := range hostileValues() {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.script["show"] = map[string]any{"exit": 1, "stderr": v}
			plan := t.TempDir() + "/the plan"
			_, err := Gate(context.Background(), plan, Spec{Project: v, GCPProject: v, TF: r.tf(t)})
			if err == nil {
				t.Fatal("a plan terraform could not read was gated")
			}
			bounded(t, name+" in Gate's read error", err.Error(), 1)
		})
	}
}

// TestTheDeployersIdentitiesAreBounded: the deployer's refusals name the
// project, the account, the ambient identity the cloud answered with (Self)
// and the account the credential turned out to be for — all four are read from
// the cloud, and all four are printed.
func TestTheDeployersIdentitiesAreBounded(t *testing.T) {
	ctx := context.Background()
	for name, v := range hostileValues() {
		t.Run(name, func(t *testing.T) {
			// The grant is missing: the refusal names the caller's own identity.
			_, access, imp, w := deployRig()
			access.granted = false
			imp.self = v
			err := Deployer(ctx, gcpProject, access, imp, w.run)
			if !errors.Is(err, ErrNoImpersonationGrant) {
				t.Fatalf("a caller with no grant got %v", err)
			}
			bounded(t, name+" in the missing grant's refusal", err.Error(), 1)

			// The credential is not the deployer's: the refusal names what it
			// is for instead.
			_, access, imp, w = deployRig()
			imp.fallback = v
			err = Deployer(ctx, gcpProject, access, imp, w.run)
			if !errors.Is(err, ErrImpersonationFailed) {
				t.Fatalf("a credential for another identity got %v", err)
			}
			bounded(t, name+" in the wrong credential's refusal", err.Error(), 1)

			// The reads themselves failed, with an API's words in the error.
			for _, tc := range []struct {
				what string
				acc  *fakeAccess
				want error
			}{
				{"the existence read", &fakeAccess{existsErr: errors.New(v)}, ErrNoDeployerAccount},
				{"the policy read", &fakeAccess{exists: true, grantErr: errors.New(v)}, ErrNoImpersonationGrant},
			} {
				_, _, imp, w := deployRig()
				err := Deployer(ctx, gcpProject, tc.acc, imp, w.run)
				if !errors.Is(err, tc.want) {
					t.Fatalf("%s: got %v", tc.what, err)
				}
				bounded(t, name+" in "+tc.what+"'s error", err.Error(), 1)
			}

			// And the project itself, named before anything is read.
			err = Deployer(ctx, v, nil, nil, nil)
			if !errors.Is(err, ErrNoDeployerAccount) {
				t.Fatalf("a project with nothing to resolve it got %v", err)
			}
			bounded(t, name+" in the no-resolver refusal", err.Error(), 1)
		})
	}
}

// preflightFileNames are the hostile values that fit in a file name (255 bytes
// on Linux), which is where a file name is what carries the text: a JSON key
// inside the file carries the long one.
func preflightFileNames() map[string]string {
	out := map[string]string{}
	for name, v := range hostileValues() {
		if len(v) > 200 {
			continue
		}
		out[name] = v
	}
	return out
}

// TestPreflightBoundsTheRootsFileNamesAndItsFilesOwnText: a refusal of the
// preflight is a path inside the agent's own root plus a block of one of its
// files, and both are the agent's text. The JSON root is the sharpest case —
// its keys are arbitrary strings, and they are interpolated whole — and the
// file's name is in every refusal of that file.
func TestPreflightBoundsTheRootsFileNamesAndItsFilesOwnText(t *testing.T) {
	for name, v := range preflightFileNames() {
		t.Run(name+" in a file name", func(t *testing.T) {
			r := newApplyRig(t, map[string]string{v + ".tf.json": `{"a_block_a_managed_root_does_not_declare": {}}`})
			_, err := Apply(context.Background(), r.request(t))
			if !errors.Is(err, ErrPreflight) {
				t.Fatalf("a root with a refused block got %v", err)
			}
			bounded(t, name+" in the preflight's refusal", err.Error(), 1)
		})
	}
	for name, v := range hostileValues() {
		t.Run(name+" in a JSON key", func(t *testing.T) {
			key, err := json.Marshal(map[string]any{v: map[string]any{}})
			if err != nil {
				t.Fatal(err)
			}
			r := newApplyRig(t, map[string]string{"main.tf.json": string(key)})
			_, err = Apply(context.Background(), r.request(t))
			if !errors.Is(err, ErrPreflight) {
				t.Fatalf("a root with a refused key got %v", err)
			}
			bounded(t, name+" in the preflight's refusal", err.Error(), 1)
		})
	}
	// And a file that cannot be read at all: the failure is the scan's own
	// error, which carries the file's name and, under it, the file's own text.
	for name, v := range preflightFileNames() {
		t.Run(name+" in an unreadable file", func(t *testing.T) {
			r := newApplyRig(t, map[string]string{v + ".tf": "resource \"google_storage_bucket\" \"data\" {\n"})
			_, err := Apply(context.Background(), r.request(t))
			if !errors.Is(err, ErrPreflight) {
				t.Fatalf("a root with an unreadable file got %v", err)
			}
			bounded(t, name+" in the scan's error", err.Error(), 1)
		})
	}
}

// TestThePreflightsRefusalListIsCappedAndSaysWhatItLeftOut: a root of twenty
// five refused files is a refusal of twenty names and a count, and the count
// is the sentence's own — the cap is at preflightRefusalsMax and the names it
// lists end exactly there.
func TestThePreflightsRefusalListIsCappedAndSaysWhatItLeftOut(t *testing.T) {
	const refused = 25
	root := map[string]string{}
	for i := 1; i <= refused; i++ {
		root[fmt.Sprintf("f%02d.tf", i)] = "data \"google_compute_instance\" \"x\" {\n}\n"
	}
	r := newApplyRig(t, root)
	_, err := Apply(context.Background(), r.request(t))
	if !errors.Is(err, ErrPreflight) {
		t.Fatalf("a root of refused files got %v", err)
	}
	msg := err.Error()
	if got := strings.Count(msg, "a data source, which terraform reads at plan"); got != preflightRefusalsMax {
		t.Errorf("the refusal lists %d of the %d refusals, want the cap of %d:\n%s", got, refused, preflightRefusalsMax, msg)
	}
	if !strings.Contains(msg, "f01.tf") || !strings.Contains(msg, fmt.Sprintf("f%02d.tf", preflightRefusalsMax)) {
		t.Errorf("the refusal does not name the files up to the cap:\n%s", msg)
	}
	if beyond := fmt.Sprintf("f%02d.tf", preflightRefusalsMax+1); strings.Contains(msg, beyond) {
		t.Errorf("the refusal names %s, which is past the cap:\n%s", beyond, msg)
	}
	if want := fmt.Sprintf("and %d more refusal(s) in the same root, not listed here", refused-preflightRefusalsMax); !strings.Contains(msg, want) {
		t.Errorf("the refusal does not say what it left out (%q):\n%s", want, msg)
	}
	if applied := r.applied(t); len(applied) != 0 {
		t.Errorf("a refused root applied something: %v", applied)
	}
}

// TestAnApplysTerraformFailureIsOneBoundedLine: terraform's own output is the
// untrusted text this path prints the most of — a provider's words, the plan's,
// an error the root's configuration chose — and the exit code the command turns
// into its own exit status must survive the bound (errors.As).
func TestAnApplysTerraformFailureIsOneBoundedLine(t *testing.T) {
	for name, v := range hostileValues() {
		t.Run(name+" in the apply", func(t *testing.T) {
			r := newApplyRig(t, plainRoot())
			r.planWithChanges(t, bucket(planBucket))
			r.script["apply"] = map[string]any{"exit": 1, "stderr": v}
			_, err := Apply(context.Background(), r.request(t))
			if err == nil {
				t.Fatal("an apply terraform refused returned no error")
			}
			bounded(t, name+" in the apply's failure", err.Error(), 1)
			var ee *tf.ExitError
			if !errors.As(err, &ee) || ee.Code != 1 {
				t.Errorf("the exit code is no longer readable off the failure: %v", err)
			}
		})
		t.Run(name+" in the plan", func(t *testing.T) {
			r := newApplyRig(t, plainRoot())
			r.script["plan"] = map[string]any{"exit": 1, "stderr": v}
			_, err := Apply(context.Background(), r.request(t))
			if err == nil {
				t.Fatal("a plan terraform refused returned no error")
			}
			bounded(t, name+" in the plan's failure", err.Error(), 1)
		})
	}
}

// TestTheInventorysWarningIsBoundedWhereItIsPrinted: the fallback's sentence is
// printed by the apply itself (safetext.MultiLine, which keeps newlines), so a
// project name read back from the marker that carries one would be a forged
// line of this program's output under the warning.
func TestTheInventorysWarningIsBoundedWhereItIsPrinted(t *testing.T) {
	ctx := context.Background()
	for name, v := range hostileValues() {
		t.Run(name, func(t *testing.T) {
			// The fallback's own sentence, and the warning a foreign resource
			// produces, which names the project and the resources.
			inv := Inventory{Project: v, GCPProject: v}
			dec := inv.Check(ctx)
			if !dec.Fallback {
				t.Fatal("an inventory with no read did not fall back")
			}
			bounded(t, name+" in the inventory's fallback", dec.Why, 1)
			bounded(t, name+" in the inventory's warning", inv.foreignWhy([]Asset{{Name: v, Type: v}}), 1)

			// And through the apply, which is where the sentence is printed.
			r := newApplyRig(t, plainRoot())
			r.planWithChanges(t, bucket(planBucket))
			req := r.request(t)
			var out bytes.Buffer
			req.Out = &out
			req.Inventory = CheckedInventory(Inventory{Project: v, GCPProject: gcpProject}.Check(ctx))
			if _, err := Apply(ctx, req); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			// The warning, and the apply's own "applying ... as ..." line, and
			// the newline the buffer ends on.
			bounded(t, name+" as printed by the apply", out.String(), 3)
			if strings.Count(out.String(), safetext.Field(v)) != 1 {
				t.Errorf("the printed warning does not carry the project's name bounded once:\n%q", out.String())
			}
		})
	}
}

// TestTheRefusalsListingIsCappedAndSaysWhatItLeftOut: the addresses a session's
// own command line named are listed back in refuseSession's refusal, and a
// command line holds as many arguments as the session passed — so the listing
// is capped and the entries it did not show are counted. A listing that
// silently stopped would read as the whole list, and the refusal is what its
// reader acts on.
func TestTheRefusalsListingIsCappedAndSaysWhatItLeftOut(t *testing.T) {
	r := newApplyRig(t, plainRoot())
	req := r.request(t)
	const n = safetext.FieldListMax + 5
	for i := 0; i < n; i++ {
		req.Session = append(req.Session, fmt.Sprintf("google_storage_bucket.a%02d", i))
	}
	_, err := Apply(context.Background(), req)
	if !errors.Is(err, ErrSessionAddresses) {
		t.Fatalf("a session with addresses got %v, want the session's refusal", err)
	}
	msg := err.Error()
	bounded(t, "the capped listing", msg, 1)
	if note := safetext.FieldListNote(5); !strings.Contains(msg, note) {
		t.Errorf("the refusal does not say it left five out (%q):\n%q", note, msg)
	}
	if !strings.Contains(msg, "google_storage_bucket.a00") || !strings.Contains(msg, fmt.Sprintf("google_storage_bucket.a%02d", safetext.FieldListMax-1)) {
		t.Errorf("the refusal does not carry the entries inside the cap:\n%q", msg)
	}
	if last := fmt.Sprintf("google_storage_bucket.a%02d", n-1); strings.Contains(msg, last) {
		t.Errorf("the refusal carries %s, past the cap, so the listing is not capped:\n%q", last, msg)
	}
}

// TestTheVerdictsRefusalListingIsCappedAndSaysWhatItLeftOut: how many changes
// a plan refuses is the plan's to choose, so how many lines this refusal
// prints is whoever wrote the root's to choose. It names at most
// safetext.FieldListMax of them, says how many it did not, and its first line
// still counts every one of them — a listing that stopped at the cap without
// saying so would read as a plan that refuses twenty changes.
func TestTheVerdictsRefusalListingIsCappedAndSaysWhatItLeftOut(t *testing.T) {
	refusalsOf := func(n int) []Refusal {
		out := make([]Refusal, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, Refusal{
				Gate:    guardGate,
				Address: fmt.Sprintf("google_storage_bucket.a%02d", i),
				Why:     "a delete nobody approved",
			})
		}
		return out
	}
	over := safetext.FieldListMax + 5
	msg := (&RefusedError{Verdict: Verdict{Refusals: refusalsOf(over)}}).Error()
	if want := fmt.Sprintf("(%d refusal(s))", over); !strings.Contains(msg, want) {
		t.Errorf("the refusal does not count the whole list (%q):\n%s", want, msg)
	}
	if note := "(" + safetext.FieldListNote(5) + ")"; !strings.Contains(msg, note) {
		t.Errorf("the refusal does not say it left five out (%q):\n%s", note, msg)
	}
	if !strings.Contains(msg, fmt.Sprintf("google_storage_bucket.a%02d", safetext.FieldListMax-1)) {
		t.Errorf("the refusal does not carry the entries inside the cap:\n%s", msg)
	}
	if last := fmt.Sprintf("google_storage_bucket.a%02d", safetext.FieldListMax); strings.Contains(msg, last) {
		t.Errorf("the refusal carries %s, past the cap:\n%s", last, msg)
	}
	if lines := strings.Split(msg, "\n"); len(lines) != safetext.FieldListMax+2 {
		t.Errorf("the refusal is %d lines, want a head, %d refusals and the note:\n%s",
			len(lines), safetext.FieldListMax, msg)
	}
	bounded(t, "the capped refusal", msg, safetext.FieldListMax+2)

	// At the cap exactly and at one, nothing was left out and nothing says it
	// was; the empty verdict keeps its own sentence.
	for _, n := range []int{1, safetext.FieldListMax} {
		at := (&RefusedError{Verdict: Verdict{Refusals: refusalsOf(n)}}).Error()
		if strings.Contains(at, "not listed here") {
			t.Errorf("a refusal of %d, with nothing left out, still counts something:\n%s", n, at)
		}
		if lines := strings.Split(at, "\n"); len(lines) != n+1 {
			t.Errorf("a refusal of %d is %d lines, want a head and %d refusals:\n%s", n, len(lines), n, at)
		}
	}
	// One past the cap is the boundary an off-by-one hides behind: twenty
	// listed, one counted, and the twenty-first address nowhere.
	one := (&RefusedError{Verdict: Verdict{Refusals: refusalsOf(safetext.FieldListMax + 1)}}).Error()
	if note := "(" + safetext.FieldListNote(1) + ")"; !strings.Contains(one, note) {
		t.Errorf("a refusal one past the cap does not say it left one out (%q):\n%s", note, one)
	}
	if lines := strings.Split(one, "\n"); len(lines) != safetext.FieldListMax+2 {
		t.Errorf("a refusal one past the cap is %d lines, want a head, %d refusals and the note:\n%s", len(lines), safetext.FieldListMax, one)
	}
	if past := fmt.Sprintf("google_storage_bucket.a%02d", safetext.FieldListMax); strings.Contains(one, past) {
		t.Errorf("a refusal one past the cap carries %s:\n%s", past, one)
	}
	if empty := (&RefusedError{}).Error(); !strings.Contains(empty, "listed nothing, which is a refusal all the same") {
		t.Errorf("an empty verdict lost its own sentence: %q", empty)
	}
}
