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
		// The shared-image key's limit, which the design's C6 text repeats: the
		// integrity rules keep two repositories from sharing an image built
		// from two different artifacts, and they are not a boundary between
		// repositories of one installation — the files that run during the
		// install state no version and are no fact of the key.
		"### Shared images: what the key protects, and what it does not",
		"**It is not a boundary between the repositories that share an image.**",
		"so they contribute no fact of the key",
		"the same class as the `image.setup` of the repository that built the key",
		// The one empty integrity the key reads from a convention rather than
		// from a line in the file: pnpm's bare key, whose registry `.npmrc`
		// configures and the key never reads.
		"**One ecosystem's empty integrity rests on a convention the file does not state.**",
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
// managed state bucket, written by `fugaro update`'s D77 step for every project
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

// TestSecurityMdStatesTheCredentialsService is Task C1.22's headline: the
// credentials service changes what SECURITY.md may claim, and this is what
// holds the document to the claims that are true on this tree. It pins the
// sentences the task names — D148's scope sentence with the identities it does
// *not* cover, Q6's one secret and one grant, the invariant, the revocation
// stated exactly, the accepted limits the owner ruled on, and the ADVISORY
// cap — and it bans the three overclaims a rewrite drifts into: that the
// service is the only identity with Secret Manager access, that it audits
// every issuance, and that the bundle cannot leak.
//
// Mutation-proofed once, on 2026-10-22, as the plan's Red step requires.
// Replacing the section's sentence "It is the only Fugaro runtime identity
// with access to model credentials (D148)." with "It is the only Fugaro
// identity with access to model credentials (D148)." — the wider claim the
// design's D148 draft used — makes this test fail on the "the only Fugaro
// runtime identity with access to model credentials" pin and on nothing else.
// The byte-identity guard for the website page fails with it, as it does for
// any edit to SECURITY.md until the page is regenerated; that is the page
// guard working, not a second finding. The mutation also settled *how* the
// claims are matched: the first version of this test searched the whole file,
// and the mutation passed, because the "In short" bullet carries the same
// phrase and satisfied the pin on its own. The claims are therefore read out
// of the section (docSection) and not out of the document, so a claim that
// lives only in a bullet pointing here does not make this test pass.
// Restored immediately; the guards are red before the doc edit and green
// after it, which is the point of writing them first.
//
// The section read was not narrow enough on its own, and the code review of
// this change found it: docSection cut at the next "## " whatever the
// heading's own level was, so a "### " heading ran on through every following
// "### " up to "## Scope" — this section together with all of "### Known
// limits". The egress pin proved it: SECURITY.md's Known-limits bullet
// "**Network egress is not restricted.**" satisfied "Network egress is not
// restricted" on its own, so deleting this section's whole "**Egress is
// unchanged (D144).**" paragraph left this test green (mutation planted, run
// and restored — it now fails on that pin and only on it). docSection cuts at
// the next heading of the same or a higher level, skipping fenced code blocks,
// and the same mutation is red: the claims below are read out of this
// subsection alone.
func TestSecurityMdStatesTheCredentialsService(t *testing.T) {
	s := readDoc(t, "SECURITY.md")
	// The section is a section: the "In short" bullet links to it and the
	// cross-reference guard resolves against its heading, so the heading is
	// matched on the file as written.
	if !strings.Contains(s, "\n### The credentials service\n") {
		t.Error("SECURITY.md has no `### The credentials service` section: the service changes the trust model, and the section is where it is stated")
	}
	// The section alone, flattened: every claim below has to be *in* it.
	section := flattenDoc(docSection(t, "SECURITY.md", s, "### The credentials service"))
	for _, want := range []string{
		// D148's sentence, at the scope D148 gives it: a runtime identity,
		// not every identity.
		"the only Fugaro runtime identity with access to model credentials",
		"not the only identity with access to Secret Manager",
		"the build service account",
		// Owners read every secret; an Editor does not read one of its own.
		// roles/editor carries secretmanager.secrets.getIamPolicy and delete
		// but not secretmanager.versions.access, so an Editor's own read is a
		// container grant like any other — and the wider claim would hide
		// exactly those grants from the reader (the owner's ruling, 2026-10-29;
		// docs/secrets-brokering/reconciliation.md F3). It also carries
		// iam.serviceAccounts.actAs and run.services.update, though, so an
		// Editor can read a secret indirectly by deploying code that runs as an
		// account holding one: the sentence has to name that mechanism itself,
		// so "directly, only through its own grant" is not read as "cannot read
		// one at all", and so it points at nothing undefined in this document
		// (the independent review's findings, 2026-10-29).
		"Project Owners bypass any secret-level policy",
		"an Editor reads a secret directly, only through its own `secretAccessor` grant; indirectly, by carrying `iam.serviceAccounts.actAs` and `run.services.update`, which let it deploy code that runs as an account holding one",
		// Q6's shape and its cost.
		"One secret, one grant, one leak",
		"a read of the bundle is every credential the service holds at once",
		// Off by default, opt-in, and the flag that opts in.
		"fugaro update --provision-credentials",
		"with it off",
		// The job holds nothing — and the one job that is not reached, named
		// rather than left to be assumed, because a shared project job keeps
		// its accessors and its token mount.
		"a per-repository job holds no model credential at all",
		"a shared project job is the one job it does not reach",
		"fugaro creds set",
		// The invariant, spelled as the code enforces it.
		"an OAuth token is only ever sent to Anthropic's own endpoint",
		// Unreachable halts rather than falls back (D143) — and the claim is
		// scoped to the job the opt-in reaches: a shared project job keeps its
		// mounts, so "the job has nothing to fall back to" is only true of a
		// per-repository job, and the shared job is refused rather than served
		// from its own token.
		"never falls back",
		"the mount is the opt-in's, not the outage's",
		"refused at the gateway rather than answered from the job's own token",
		// The stop flag.
		"fugaro budget kill --all",
		// The database role's reach, said plainly rather than left to be
		// inferred from the operations named beside it (the 0.10.1 reviews):
		// Realtime Database IAM has no path-level scope, so the two permissions
		// the role is made of are the whole installation database's — the
		// service could in principle write the stop switch and the policy it is
		// checked against, and the same identity already holds every model
		// credential. Pinned as sentences, because "no path-level scope" alone
		// states the fence and not what it does not cover.
		"can read and write the whole installation database",
		"`config/kill/*`, including the switch that is supposed to stop it",
		"`config/policy/*`, including the allow-list that holds a call to a model",
		"turn off its own stop switch or widen the policy it is checked against",
		"the same identity already holds every model credential a call can be given",
		"The fence is the service's own behaviour",
		// The revocation, stated exactly: write-once outcome, and fx as the
		// only bound until it exists.
		"the write-once outcome",
		"the only bound on a replayed identity",
		// The accepted limits the owner ruled on 2026-10-19.
		"The launcher's reach",
		"The replay bounds",
		// The cap is advisory.
		"ADVISORY",
		// Egress stays a known limit (D144).
		"Network egress is not restricted",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the `### The credentials service` section never says %q: the credentials service is in the trust model, and the section is where it is stated", want)
		}
	}
	// The three overclaims. Each is the shape a rewrite drifts into, so each
	// is banned as the whole sentence, not as a word: "the service is not the
	// only identity with access to Secret Manager" is the true sentence and
	// does not contain the banned one. These are read out of the whole
	// document: an overclaim in a bullet that points at the section is just as
	// wrong as one in the section.
	for _, banned := range []string{
		"the service is the only identity with access to Secret Manager",
		"the service audits every issuance",
		"the bundle cannot leak",
	} {
		if strings.Contains(flattenDoc(s), banned) {
			t.Errorf("SECURITY.md says %q: that claim is wider than the code on this tree", banned)
		}
	}
}

// TestSecurityMdNamesTheCredentialsServicesKnownLimits: the parts of the
// service that are *not* wired on this tree, said plainly rather than left for
// a reader to assume. Each is a claim the section would otherwise make by
// being silent, and each names the thing a later release turns on. Read out of
// the section, like the headline test's claims, so a limit stated somewhere
// else in the file does not count as this section stating it.
func TestSecurityMdNamesTheCredentialsServicesKnownLimits(t *testing.T) {
	section := flattenDoc(docSection(t, "SECURITY.md", readDoc(t, "SECURITY.md"), "### The credentials service"))
	for _, want := range []string{
		// The audit limit this list used to carry is gone, and deliberately not
		// replaced here: C1.11 landed the answer path's auditor (one line per act —
		// a model call served, a mint issued or refused, and every refusal answered
		// once the caller's identity is verified; internal/credsvc/cascade.go's
		// refuseCall and auditCall, github.go's auditMint), so "a forwarded call
		// writes no audit line yet" was a limit that had closed and a sentence that
		// was false. What the audit log records is stated where it belongs, in the
		// service's own section above, and Check 96's test holds it to the code.
		// ServiceHeldSecretNames is empty, so the App key does not move.
		"the GitHub App key does not move off the job in this release",
		"config.ServiceHeldSecretNames",
		// The launch-side allow-list check is the service's, not the
		// launcher's.
		"the launcher makes no account allow-list check of its own",
		// The dormancy limit is GONE and replaced, deliberately: the release
		// that wires the service is this one. The bullet it used to carry
		// said the opt-in gave a job no address and that no installation
		// should opt in — both false once internal/cli/run.go sets
		// FUGARO_CREDENTIALS_SERVICE_URL on a repository job's execution.
		// What is stated instead is the half that is still not wired (every
		// other agent.auth mode, and the shared project jobs), so the
		// section cannot be read as "the service now serves everything".
		"the service is wired as of 0.10.1",
		"the half it does not carry is the other auth modes'",
		"FUGARO_CREDENTIALS_SERVICE_URL",
		"the mount drop is auth-blind",
		"no model credential of their own and nothing yet to replace it",
		"agent.auth: cascade",
		"A shared project job is not wired either",
		"an installation that has not opted in is byte for byte what it was",
		// And the App key's own limit stays: the mount and the grant are
		// still there, the runner just no longer reads the key.
		"the release after the owner's live Check 96 proves the deployed service mints",
		"no longer reads the key and mints at the service instead",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the `### The credentials service` section does not state the known limit %q: a limit that is not stated is read as a guarantee", want)
		}
	}
}
