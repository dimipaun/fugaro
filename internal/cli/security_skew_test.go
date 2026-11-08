package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// securitySkewBlock is the block the tests here write: one row of the seven-
// value vocabulary SC7 validates, at the key an older runner's decoder refuses
// whatever it holds.
const securitySkewBlock = "security:\n  patterns:\n    - { name: payments, match: \"internal/payments/**\", category: credentials }\n"

// TestRunRefusesSecurityOnAnOlderBaseImage is securitySince's launch gate for a
// workflow whose job image is a derived one: a repository whose fugaro.yaml
// writes the security: block onto a job image older than the release that reads
// it is refused before the task is written, rather than deployed to die at
// bootstrap on a key nobody can do anything about any more. The judge is the
// workflow's build record, as it is for every other per-workflow image gate
// (checkImageSince), and the boundary is the constant's own: one release below
// is refused, the constant and one above pass.
func TestRunRefusesSecurityOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, securitySince, -1), shiftRelease(t, securitySince, 1)
	for _, c := range []struct {
		rel     string
		refused bool
	}{
		{below, true},
		{securitySince, false},
		{above, false},
	} {
		t.Run(c.rel, func(t *testing.T) {
			f := newCloudFixture(t)
			configSkewCheckout(t, f, configSkewHead+securitySkewBlock+configSkewWorkflow)
			writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+c.rel)
			_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
			if !c.refused {
				if err != nil {
					t.Fatalf("release %s: err = %v, want launched", c.rel, err)
				}
				return
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want the launch refused", err)
			}
			for _, want := range []string{
				"fugaro.yaml's security: block of acme/other",
				"knows the security: block",
				"fugaro " + securitySince + " or later",
				"ghcr.io/dimipaun/fugaro-base:" + c.rel + ", release " + c.rel,
				"fugaro image refresh --repo acme/other --workflow svc",
				"; or remove the security: block from fugaro.yaml",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v lacks %q", err, want)
				}
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Errorf("%d executions, want none", n)
			}
		})
	}
}

// TestRunRefusesAnEmptySecurityBlockOnAnOlderBaseImage: the gate judges the key
// being written and not the rows it holds. `security: {}` decodes to a block
// with no patterns and an explicit `security:` decodes to no block at all, and
// both are lines an older runner's KnownFields decoder refuses exactly like a
// full list — so a gate that read the resolved block would launch a run that
// dies at bootstrap on a line the operator did write.
func TestRunRefusesAnEmptySecurityBlockOnAnOlderBaseImage(t *testing.T) {
	for _, c := range []struct{ name, block string }{
		{"an empty block", "security: {}\n"},
		{"an explicit null", "security:\n"},
		{"an empty list", "security:\n  patterns: []\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newCloudFixture(t)
			configSkewCheckout(t, f, configSkewHead+c.block+configSkewWorkflow)
			writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, securitySince, -1))
			wantRefused(t, f, "fugaro.yaml's security: block of acme/other needs a job image whose runner knows the security: block (fugaro "+securitySince+" or later)",
				"run", "--run-id", "20261008-100000-abcd", "A task")
		})
	}
}

// TestRunRefusesSecurityOnACloneWorkflowsOlderBaseImage: a checkout: clone
// workflow has no build record to judge (it runs the installation's base image
// as it is), so the same gate is judged against base_images.base itself and
// names the fix that image has: fugaro update --base base, not an image refresh
// of a workflow that is never built.
func TestRunRefusesSecurityOnACloneWorkflowsOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, securitySince, -1)
	f.appendConfig(t, "base_images: {base: "+older+"}\n")
	configSkewCheckout(t, f, configSkewHead+securitySkewBlock+
		"workflows:\n  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n")
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{"fugaro.yaml's security: block of acme/other", "Run fugaro update --base base", older} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunDoesNotGateAFileThatWritesNoSecurity: the gate is the key's, so a
// repository that writes none is never held to securitySince — the shape the
// title gate has (a launch with no --title carries no field to refuse). The
// image here is one release below the constant, so a gate that fired on
// anything but the key would refuse this launch.
func TestRunDoesNotGateAFileThatWritesNoSecurity(t *testing.T) {
	f := newCloudFixture(t)
	configSkewCheckout(t, f, configSkewHead+configSkewWorkflow)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, securitySince, -1))
	if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
		t.Fatalf("the file writes no security: block, so the launch must not be gated: %v", err)
	}
}

// TestRunJudgesTheRefsSecurityFileNotTheWorkingTree: the judge is the file the
// run checks out, not the launcher's tree (cascadeRefConfig's own reason, which
// the auth read has always followed). The checkout below commits one document
// and leaves another in the working tree, once each way round.
func TestRunJudgesTheRefsSecurityFileNotTheWorkingTree(t *testing.T) {
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, securitySince, -1)
	withBlock := configSkewHead + securitySkewBlock + configSkewWorkflow
	t.Run("the ref writes it", func(t *testing.T) {
		f := newCloudFixture(t)
		dir := configSkewCheckout(t, f, withBlock)
		testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": configSkewHead + configSkewWorkflow})
		writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", older)
		_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro.yaml's security: block of acme/other") {
			t.Fatalf("err = %v, want the launch refused on the ref's file", err)
		}
	})
	t.Run("the working tree writes it", func(t *testing.T) {
		f := newCloudFixture(t)
		dir := configSkewCheckout(t, f, configSkewHead+configSkewWorkflow)
		if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(withBlock), 0o644); err != nil {
			t.Fatal(err)
		}
		writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", older)
		if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
			t.Fatalf("the ref writes no security: block, so the launch must not be gated: %v", err)
		}
	})
}

// TestUpdateRepoRefusesSecurityOnOlderBaseImage is securitySince's gate at
// fugaro update --repo, the same shape as rigorSince's
// (TestUpdateRepoRefusesRigorOnOlderBaseImage): the key is decoded strictly by
// every release, so a runner older than the release that reads it dies at
// bootstrap on it, and update --repo refuses to deploy a job that would. The
// boundary is the constant's own: one release below is refused, the constant
// and one above pass, a base that is not a release only warns, and no base at
// all passes.
//
// The repository's half is the key being written (RepoSecuritySet), not the
// rows it holds: security: {} is the key absent to this release and an explicit
// null decodes to no block at all, while both are the same line to an older
// runner's decoder — a rows test deploys a job that dies at bootstrap on a line
// the operator did write.
func TestUpdateRepoRefusesSecurityOnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, securitySince, -1), shiftRelease(t, securitySince, 1)
	older := "ghcr.io/dimipaun/fugaro-base:" + below
	lc := func(ref string) *localcfg.Config {
		if ref == "" {
			return &localcfg.Config{}
		}
		return &localcfg.Config{BaseImages: map[string]string{"base": ref}}
	}
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	r := &initRun{o: &initOptions{}, cmd: cmd, w: &out}

	written := []*config.Config{
		{Security: &config.Security{}, RepoSecuritySet: true},
		{RepoSecuritySet: true},
	}
	for _, cfg := range written {
		err := r.gateSecurity(r.o, "acme/app", lc(older), cfg)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("%+v: err = %v, want refused", cfg.Security, err)
		}
		for _, want := range []string{
			"fugaro " + securitySince + " or later",
			older + ", release " + below,
			"fugaro update --base base",
			"fugaro.yaml's security: block of acme/app",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%+v: %v lacks %q", cfg.Security, err, want)
			}
		}
		if errOut.Len() != 0 {
			t.Errorf("stderr = %q, want none (refused, not warned)", errOut.String())
		}
	}

	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + securitySince, "ghcr.io/dimipaun/fugaro-base:" + above, ""} {
		var warn bytes.Buffer
		if err := refuseSecuritySkew("acme/app", true, ref, &warn); err != nil || warn.Len() != 0 {
			t.Errorf("%q: err = %v, warn = %q, want neither", ref, err, warn.String())
		}
	}
	var warn bytes.Buffer
	if err := refuseSecuritySkew("acme/app", true, devAuthorBase, &warn); err != nil {
		t.Errorf("dev base: err = %v, want a warning only", err)
	}
	if !strings.Contains(warn.String(), "not a release base image") || !strings.Contains(warn.String(), "is not checked") {
		t.Errorf("dev base: warn = %q", warn.String())
	}

	// A configuration that writes no security: block asks nothing of the image:
	// the block alone, without the key, is every repository that never wrote
	// one.
	for _, cfg := range []*config.Config{nil, {}, {Security: &config.Security{}}} {
		if err := r.gateSecurity(r.o, "acme/app", lc(older), cfg); err != nil {
			t.Errorf("%+v: err = %v, want none", cfg, err)
		}
	}

	// --forget and --plan-only deploy nothing, so neither is refused;
	// --print-vars is judged like a deploy and writes nothing to stdout.
	for _, o := range []*initOptions{{planOnly: true}, {forget: true}} {
		if err := (&initRun{o: o, cmd: cmd, w: &out}).gateSecurity(o, "acme/app", lc(older), written[0]); err != nil {
			t.Errorf("%+v: err = %v, want none", o, err)
		}
	}
	out.Reset()
	pv := &initOptions{printVars: true}
	if err := (&initRun{o: pv, cmd: cmd, w: io.Discard}).gateSecurity(pv, "acme/app", lc(older), written[0]); err == nil {
		t.Fatal("--print-vars: want refused")
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars wrote to stdout: %q", out.String())
	}
}

// validateSecurity runs fugaro validate on a repository file whose top level
// carries securityLines, in a directory that is no checkout and has no project
// config: the file's own key is all this judges. It is validateRigor's shape.
func validateSecurity(t *testing.T, securityLines string) (validateOutput, error) {
	t.Helper()
	isolateProjects(t, t.TempDir())
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent: { auth: api-key }\n" +
		securityLines + "workflows:\n  app: { commands: { build: go build ./..., test: go test ./... } }\n"})
	out, _, err := execute(t, "validate", "--json", filepath.Join(dir, "fugaro.yaml"))
	var doc validateOutput
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("output %q: %v", out, jerr)
	}
	return doc, err
}

// TestValidateWarnsThatSecurityNeedsEveryJobImage: the file is valid — the skew
// is the installation's, not the file's — so validate warns rather than
// refusing, the shape rigorImageWarnings has, and names both ways to move an
// image (the installation's base, and a workflow's own). Every spelling of the
// key counts, since it is the key an older runner refuses, and a file that
// writes none is warned about nothing.
func TestValidateWarnsThatSecurityNeedsEveryJobImage(t *testing.T) {
	for _, securityLines := range []string{
		securitySkewBlock,
		"security: {}\n",
		"security:\n",
	} {
		doc, err := validateSecurity(t, securityLines)
		if err != nil {
			t.Fatalf("%q: err = %v, want a valid file", securityLines, err)
		}
		if !has(doc.Warnings, "security", "needs fugaro "+securitySince) {
			t.Errorf("%q: warnings = %+v, want the security warning", securityLines, doc.Warnings)
		}
		for _, want := range []string{"fugaro update --base base", "fugaro image refresh"} {
			if !has(doc.Warnings, "security", want) {
				t.Errorf("%q: the warning does not name %q: %+v", securityLines, want, doc.Warnings)
			}
		}
		if has(doc.Problems, "security", "") {
			t.Errorf("%q: the key was refused, and the file is valid: %+v", securityLines, doc.Problems)
		}
	}
	if doc, err := validateSecurity(t, ""); err != nil || has(doc.Warnings, "security", "") {
		t.Errorf("a file that writes no security: block was warned about: %+v, %v", doc.Warnings, err)
	}
}

// TestSecuritySinceIsTheReleaseTheChecklistNames holds securitySince to
// docs/release.md's Before-you-tag bullet — the reader a release verifies the
// constant against the tag actually cut, the way every other *Since constant
// here has one.
func TestSecuritySinceIsTheReleaseTheChecklistNames(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `securitySince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no securitySince bullet")
	}
	if !strings.Contains(bullet, "currently `\""+securitySince+"\"`") {
		t.Errorf("docs/release.md's securitySince bullet does not carry the constant's release (%q)", securitySince)
	}
	for _, want := range []string{"gateSecurity", "checkSecurityImage", "checkCloneSecurityImage", "securityImageWarnings", "0.22.0"} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's securitySince bullet does not name %q", want)
		}
	}
}

// TestSecuritySinceIsTheReleaseSecurityShipsIn holds the constant to the
// release the security-classification slice is slotted for, which is the
// release jobsSince names: the checklist bullet is verified against the tag
// actually cut, and this is the value it is verified from.
func TestSecuritySinceIsTheReleaseSecurityShipsIn(t *testing.T) {
	if securitySince != "0.22.0" {
		t.Errorf("securitySince = %q: the security: block ships in CLI 0.22.0", securitySince)
	}
}
