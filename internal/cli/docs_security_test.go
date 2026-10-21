package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// TestSecurityMdStatesTheModelAndItsLimits (design generic-tool §9): the
// trust model is in SECURITY.md itself, names each known gap, and says what
// the code does, not what it hopes.
func TestSecurityMdStatesTheModelAndItsLimits(t *testing.T) {
	data, err := os.ReadFile("../../SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"## Trust model and known limits",
		"you trust the repository and the source of the task",
		"egress is not restricted",
		"GH_TOKEN",
		"/proc",
		"GitHub App's private key",
		"branch protection",
		"credit limit",
		"fugaro verify",
		"followup.trusted",
		"docs/design/v1.md",
		"docs/design/bucket-iam.md",
		"Budget mode defaults to off.",
		"Vertex has no dollar cap, in any mode.",
		"roles/storage.objectAdmin",
		"it can read the token from the runner's `/proc`",
		"With budget off there is no managed file and no routing check at all",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SECURITY.md never says %q", want)
		}
	}
	for _, banned := range []string{"fully isolated", "secure by design", "cannot leak", "guarantees"} {
		if strings.Contains(strings.ToLower(s), banned) {
			t.Errorf("SECURITY.md says %q: state the model and its limits, not a claim", banned)
		}
	}
	// Every environment variable it names as reaching the agent is real.
	for _, v := range regexp.MustCompile("`([A-Z][A-Z0-9_]{2,})`").FindAllStringSubmatch(s, -1) {
		if !knownEnvName(t, v[1]) {
			t.Errorf("SECURITY.md names %s, which the code never sets or passes", v[1])
		}
	}
}

// knownEnvName reports whether name appears, as a whole word, in one of the
// files that build the agent's or the runner's environment or mint its
// tokens: internal/agent/env.go, internal/runner/*.go,
// internal/gitops/credentials.go, internal/gitprov/github/github.go and
// deploy/terraform. A variable renamed in all of them makes a SECURITY.md
// mention of its old name, and so this test, fail.
func knownEnvName(t *testing.T, name string) bool {
	t.Helper()
	files := []string{
		"../agent/env.go",
		"../gitops/credentials.go",
		"../gitprov/github/github.go",
	}
	runnerGo, err := filepath.Glob("../runner/*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, runnerGo...)
	if err := filepath.WalkDir("../../deploy/terraform", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".tf") {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pat := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if pat.Match(data) {
			return true
		}
	}
	return false
}

// TestSecurityMdNamesTheDeployersNinthGrant: the managed-deployer bullet said
// the account holds "one unconditioned custom role ... and nothing else unless
// the installation sets the module's `managed_deployer_scoped`", which is a
// fence wider than the one in the tree. There is a ninth binding with no
// condition on it -- `roles/storage.objectAdmin` at the BUCKET level on D77's
// managed state bucket, written by `fugaro init`'s D77 step for every project
// the creating run creates, gated on neither `--managed` nor
// `managed_deployer_scoped` (internal/managed/statebucket.go; the design's §3
// F4a) -- and a security document that counts one unconditioned role and stops
// is the one place a reader would take the narrower number from. The bullet is
// read by its own opening, and the role and the bucket are read from the
// constants rather than copied, so the doc and the tree move together.
//
// Mutations (verified, run and restored): the ninth-grant sentence dropped from
// SECURITY.md → FAIL; the old "and nothing else unless" wording put back → FAIL
// (the second half of this test, which is what keeps the wrong claim from
// returning in a reworded form); the design's F4a paragraph dropped → FAIL;
// managed.DeployerStateRole given another value → FAIL, because both documents
// are read against the constant and not against a literal.
func TestSecurityMdNamesTheDeployersNinthGrant(t *testing.T) {
	const opening = "- **The managed deployer exists in every installation"
	var bullet string
	for _, line := range strings.Split(readDoc(t, "SECURITY.md"), "\n") {
		if strings.HasPrefix(line, opening) {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatalf("SECURITY.md has no bullet opening %q: this test's marker has moved", opening)
	}
	// The claim that was wrong, in the shape it was wrong in. A security doc
	// may say the project role is the only unconditioned PROJECT role; it may
	// not say the account holds nothing else.
	if strings.Contains(bullet, "and nothing else unless the installation sets") {
		t.Errorf("SECURITY.md is back to \"one unconditioned role and nothing else\": the account also holds %s on the managed state bucket, with no condition on it", managedmode.DeployerStateRole)
	}
	for _, want := range []string{
		managedmode.DeployerStateRole,
		"gs://" + managedmode.ManagedStateBucketName("<project>"),
		"at the **bucket** level",
		"not gated on `--managed`",
		"managed_deployer_scoped",
	} {
		if !strings.Contains(bullet, want) {
			t.Errorf("SECURITY.md's managed-deployer bullet does not carry %q: the ninth grant is the one binding with no condition on it, and this is where a reader counts them", want)
		}
	}
	// And the design of record is where the bullet's account of it comes from:
	// F4a is the paragraph that says the binding is bucket-level and why it is
	// not conditioned, and it names the same role constant and the same bucket.
	// Read by its own opening, so a mention of "F4a" elsewhere in the design
	// (§7 and §8 count it) is not mistaken for the paragraph.
	const f4aOpening = "**F4a — the ninth grant"
	var f4a []string
	for _, line := range strings.Split(readDoc(t, "docs/design/managed-deployer-fence.md"), "\n") {
		if len(f4a) == 0 {
			if !strings.HasPrefix(line, f4aOpening) {
				continue
			}
		} else if strings.TrimSpace(line) == "" {
			break
		}
		f4a = append(f4a, line)
	}
	if len(f4a) == 0 {
		t.Fatalf("the design has no paragraph opening %q: SECURITY.md's account of the ninth grant is read against it", f4aOpening)
	}
	// Joined but not flattened: flattenDoc collapses "//" to a space, which
	// would take the "//" out of the bucket's own `gs://` and make this check
	// unfindable. The design cites the role by its Go symbol rather than by the
	// role's string (the string is `managed.DeployerStateRole`'s value, and a
	// design that repeated it would be a second copy of it), so the symbol is
	// what is looked for here.
	para := strings.Join(f4a, "\n")
	for _, want := range []string{"`managed.DeployerStateRole`", "gs://" + managedmode.ManagedStateBucketName("<project>")} {
		if !strings.Contains(para, want) {
			t.Errorf("the design's F4a paragraph does not carry %q: it is the paragraph SECURITY.md's ninth-grant sentence is read against", want)
		}
	}
}

// TestSecurityMdNarrowsTheUncomparedRemoteToWhatRemains: the launcher-
// environment bullet has been narrowed twice, and this pins the second
// narrowing. It first said a remote naming another repository *on the same
// host* "is not compared with the task at all until Task 3.3 computes the
// clone URL from the task itself"; that stopped being true when the check
// landed (internal/runner's admissionCheck, before any git credential is
// requested). It then went on to place the host-and-path corners on the same
// side of the line — a remote on a host Fugaro does not recognise, and one
// whose path is not exactly one owner and one name, "not compared with the
// task at all" — and that is stale too: at HEAD both are refused before any
// git credential is requested, by remoteHost's unknown-host refusal
// (TestRunnerRefusesARemoteOnAHostNoProviderOwns) and by repoNamedByRemote's
// canonicalization refusal (TestRunnerRefusesARemoteWhosePathIsNotAnOwnerName),
// and a nested repository path is compared with the task's own repository
// rather than read as "no repository" (task.CanonicalRepo allows it, and
// MatchRepo compares it). The remote that really is uncompared is the one the
// comparison can read nothing from: one naming no host, and one naming a host
// with no repository path.
func TestSecurityMdNarrowsTheUncomparedRemoteToWhatRemains(t *testing.T) {
	data, err := os.ReadFile("../../SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"admissionCheck",
		"remoteHost",
		"repoNamedByRemote",
		"one on a host no provider kind owns",
		"a self-hosted forge is refused",
		"compared with the task's own repository segment by segment",
		"one naming no host",
		"one naming a host but no repository path",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SECURITY.md never says %q", want)
		}
	}
	for _, banned := range []string{
		"remoteOfAnotherRepository",
		"a remote on a host Fugaro does not recognise",
		"not exactly one owner and one name",
		"is not compared with the task at all until Task 3.3 computes the clone URL from the task itself is the rest",
		"*on the same host*, or any remote",
	} {
		if strings.Contains(s, banned) {
			t.Errorf("SECURITY.md still says %q: the admission refuses it before any git credential is requested", banned)
		}
	}
}

// TestSecurityMdCrossReferencesPointTheRightWay: SECURITY.md sends a reader
// from one limit to another by name — `see "Every launcher can write runs/"
// above`, `see "What is not on by default" below` — and a reference that names
// the wrong direction costs the reader the section it points at. One did: the
// "Narrow git credentials" bullet said the project-scope default was "below"
// while that section is at line 21, above line 39 and above every bullet that
// cites it (the review finding on this change). Nothing checked the direction,
// so this does: every `see "X" above|below` has to name a section heading or a
// bold bullet that exists, on the side it says.
//
// The anchor forms are the two the file uses: a heading line whose text is the
// name, and a bullet whose bold run *begins* with it (the bullets here are
// "- **Name.** rest of the sentence" or "- **Name, and more.**"). A name with
// no anchor at all fails too: the reference would send the reader nowhere.
//
// Mutation (verified, run and restored): write "below" back into the "Narrow
// git credentials" bullet and this fails naming that reference.
func TestSecurityMdCrossReferencesPointTheRightWay(t *testing.T) {
	data, err := os.ReadFile("../../SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	headingRE := regexp.MustCompile(`^#{2,4} (.+?)\s*$`)
	bulletRE := regexp.MustCompile(`^- \*\*(.+?)\*\*`)
	refRE := regexp.MustCompile(`see "([^"]+)" (above|below)`)
	type anchor struct {
		line int
		name string
	}
	var anchors []anchor
	for i, line := range lines {
		if m := headingRE.FindStringSubmatch(line); m != nil {
			anchors = append(anchors, anchor{i, m[1]})
		}
		if m := bulletRE.FindStringSubmatch(line); m != nil {
			anchors = append(anchors, anchor{i, strings.TrimSuffix(m[1], ".")})
		}
	}
	refs := 0
	for i, line := range lines {
		for _, m := range refRE.FindAllStringSubmatch(line, -1) {
			name, dir := m[1], m[2]
			at := -1
			for _, a := range anchors {
				if a.name == name || strings.HasPrefix(a.name, name) {
					at = a.line
					break
				}
			}
			if at < 0 {
				t.Errorf("SECURITY.md line %d points at %q, which is not a heading or a bold bullet in the file: a reference to nothing is not a direction problem but it is the same dead end", i+1, name)
				continue
			}
			refs++
			if dir == "above" && at > i {
				t.Errorf("SECURITY.md line %d says %q is above it, and its anchor is line %d: the reference sends the reader the wrong way", i+1, name, at+1)
			}
			if dir == "below" && at < i {
				t.Errorf("SECURITY.md line %d says %q is below it, and its anchor is line %d: the reference sends the reader the wrong way", i+1, name, at+1)
			}
		}
	}
	if refs < 5 {
		t.Fatalf("read %d cross-references out of SECURITY.md: the file has moved, so this check saw nothing", refs)
	}
}
