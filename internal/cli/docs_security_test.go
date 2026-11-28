package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
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
		"docs/bucket-iam/spec.md",
		// The gap the release now ships, where the document used to say the
		// budget mode defaults to off (always-a-budget, F17): the real key
		// stays out of the agent's environment in every mode.
		"With no `budget:` block the mode is `observe`, so the caps record rather than stop.",
		// The other half of that bullet, which a tightening pass dropped and a
		// review asked back: the code refuses the word outright
		// (policy.Mode.Removed, ModeOffRefusal), so the document has to say
		// there is none to write.
		"there is no `off` to write instead",
		"Vertex has no dollar cap, in any mode.",
		"roles/storage.objectAdmin",
		"it can read the token from the runner's `/proc`",
		"The one run without a gateway — an `oauth` run the credentials service does not serve — has no routing check at all",
		// The shared-image key's limit, which the design's C6 text repeats: the
		// integrity rules keep two repositories from sharing an image built
		// from two different artifacts, and they are not a boundary between
		// repositories of one installation — the files that run during the
		// install state no version and are no fact of the key.
		"### Shared images: what the key protects, and what it does not",
		"**It is not a boundary between the repositories that share an image.**",
		"so they contribute no fact of the key",
		"the same class as the `image.setup` of the repository that built the key",
		// The empty integrities the key reads from configuration rather than from
		// a line in the file: pnpm's bare key, whose registry `.npmrc` configures,
		// and a requirements pin with no `--hash`, whose index pip.conf or
		// PIP_INDEX_URL configures. Both sentences are pinned, so the document
		// cannot go back to saying that only pnpm's is one.
		"**Two shapes key an empty integrity from configuration the file does not state.**",
		"**The other empty integrities are read from a line in the file**",
		"`requirements*.txt` pin that carries no `--hash` is keyed the same way",
		"pip's index is configuration too — `pip.conf`, `PIP_INDEX_URL`",
		// And the other half of it: the lines it does read are read as URLs,
		// where the host is the one the authority names and not the text before
		// the first slash — a source stating the default registry as userinfo
		// is another host's artifact.
		"is read as a URL and not as text",
		// The other half of the image claim, and the one a repository's jobs
		// actually run on: what source a catalog image carries and who can
		// read it (C6 as re-pointed by D651; the sentences themselves are
		// pinned in docs_registry_readers_test.go).
		"### The source an image carries, and who can read it",
		// The shared image's own half of that claim, which the design's §12
		// states and its prerequisites ask SECURITY.md to carry: it holds the
		// dependency layer and no repository's source, so a repository's
		// registry readers never read another's through it.
		"**A shared dependency-layer image bakes no repository's source at all.**",
		// And what C8b does and does not ship, so the paragraph above it cannot
		// go back to claiming a refresh that builds a shared image out of
		// nothing.
		"no catalog entry is keyed by a resolved dependency set yet",
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
	// Every environment variable it names as reaching the agent is real. The
	// exception is a variable of another program's that the document names
	// because the key rests on its configuration: `PIP_INDEX_URL` is pip's, no
	// file in this tree sets or passes it, and there is nothing to check it
	// against.
	for _, v := range regexp.MustCompile("`([A-Z][A-Z0-9_]{2,})`").FindAllStringSubmatch(s, -1) {
		if v[1] == "PIP_INDEX_URL" {
			continue
		}
		// B5.4: a name the credentials service reads and no module renders yet
		// is a name this document has to be able to state — the class's own
		// table is exactly the limit B5.4 exists to write down — and the
		// exemption expires by itself rather than by memory: the day the
		// deployment renders the name it leaves
		// CredentialsServiceEnvAwaitingTheModule (internal/infra/tf's own
		// tripwire), and this loop then fails over the sentence until the name
		// is one knownEnvName can find.
		if slices.Contains(tf.CredentialsServiceEnvAwaitingTheModule(), v[1]) {
			continue
		}
		if !knownEnvName(t, v[1]) {
			t.Errorf("SECURITY.md names %s, which the code never sets or passes", v[1])
		}
	}
}

// knownEnvName reports whether name appears, as a whole word, in one of the
// files that build the agent's, the runner's or a build step's environment or
// mint its tokens: internal/agent/env.go, internal/runner/*.go,
// internal/gitops/credentials.go, internal/gitprov/github/github.go,
// internal/backend/gcp/build.go (the Cloud Build request's own env, which is
// where the credential step's mounted variables are declared),
// internal/cli/gitcred.go (the step that reads them) and deploy/terraform. A
// variable renamed in all of them makes a SECURITY.md mention of its old name,
// and so this test, fail.
func knownEnvName(t *testing.T, name string) bool {
	t.Helper()
	files := []string{
		"../agent/env.go",
		"../gitops/credentials.go",
		"../gitprov/github/github.go",
		"../backend/gcp/build.go",
		"gitcred.go",
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

// TestSecurityMdBoundsTheSlipTheftWindow: "only" is what bounds *when* a
// launcher can steal an unclaimed slip -- before the run's own start-up claim,
// and no later (D1941) -- and the In-short bullet and the paragraph stating
// the rule are the two places a reader takes it from. The laconic tightening
// of SECURITY.md (PR #3907) dropped the word from the bullet and a review of
// the same change put it back, but nothing pinned it: a later tightening could
// drop it again and leave the bullet reading as if a launcher could steal a
// slip at any time. Both statements are pinned, so the bound survives the next
// rewrite of either.
func TestSecurityMdBoundsTheSlipTheftWindow(t *testing.T) {
	s := flattenDoc(readDoc(t, "SECURITY.md"))
	for _, want := range []string{
		"a launcher can steal an unclaimed slip only before the run claims it",
		"A launcher can steal an unclaimed slip, and only that",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SECURITY.md no longer says %q: `only` bounds the slip theft window to before the run's start-up claim (D1941), and a sentence without it reads as a theft with no window at all", want)
		}
	}
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
	for _, line := range strings.Split(readDoc(t, "docs/managed-deployer-fence/spec.md"), "\n") {
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
		// And Q6's shape scoped, the way secret-readers.md E7 scopes it: the
		// bundle's grant is the installation root's one UNCONDITIONED
		// accessor, while the account also holds one per container a switched
		// repository's job mounts (B3.3, D2074) and the dynamic class's
		// prefix-conditioned project accessor (D1931, E25). Pinned, because
		// the flat count this paragraph carried ("its only grant on Secret
		// Manager") is false on this tree and reads as a contradiction beside
		// S3's own sentence below (the review finding on B3.5).
		"exactly one unconditioned `secretmanager.secretAccessor` grant in the installation root",
		"one `secretmanager.secretAccessor` per container the job's own environment mounts",
		"conditioned on that grammar's two name prefixes",
		"a read of the bundle is every credential the service holds at once",
		// Deployed on every installation, and the switch a run reads -- the
		// posture the owner's ruling of 2026-11-05 replaced "off by default,
		// opt-in, and the flag that opts in" with (DON1, DON8).
		"**Deployed for every installation, and reached only where the switch is on (the owner's ruling of 2026-11-05; DON1 and DON8).**",
		"**`fugaro update` deploys it on every installation**",
		"the deploy alone changes no run",
		// The switch's terms, per auth. The section said a repository whose
		// auth is not the cascade "is never switched on", which B2.2 and B2.3
		// made false (infra.CredentialsServiceServes admits `api-key` and
		// `oauth` under a gateway mode, runner.ServiceSwitchedAuth names them,
		// and internal/cli's credentialsServiceLaunchEnv hands them the
		// address), and the auth-blind drop it named beside that was false for
		// the same reason — the review of this branch caught both still
		// standing ten lines above the paragraph that says the opposite.
		"**The switch admits an `api-key` or `oauth` repository too (B2.2, B2.3, D2071)**:",
		"a budget mode of `observe` or `enforce`, because the gateway that reaches the service exists only there",
		"`anthropic-api-key` for `api-key` and every account's `claude-oauth-token[-<account>]` for `oauth`",
		"a `vertex` repository is the one `agent.auth` it never admits",
		// And what the switch does for the two auths it now admits: the mount
		// went with B2.7's flip (D2072), which is the fact the bullet below
		// states in full, alongside the provider keys the drop leaves alone
		// (R6, D1634).
		"the mount drop landed in 0.26.0",
		"its job now declares neither the Anthropic key nor the Claude OAuth token, its account holding no `secretAccessor` on either",
		// The job holds nothing — and the one job that is not reached, named
		// rather than left to be assumed, because a shared project job keeps
		// its accessors and its token mount. The claim is the cascade job's as
		// of B2.4 and the two Claude mounts' as of B2.7: a switched `api-key`
		// or `oauth` repository's job keeps only its provider keys, so a
		// sentence that says "a per-repository job" flat is false for it
		// (B2.3, B2.7, D2072; R6, D1634).
		"a per-repository `cascade` job holds no model credential at all",
		"as of 0.26.0 an `api-key` or `oauth` one gives up the two Claude mounts and keeps only its provider keys",
		"its job mounts neither the Anthropic key nor the Claude OAuth token, its account holding `secretAccessor` on neither",
		// B5.4. The shared class is a caller the service serves now (B5.1's
		// pairing, B5.2's address), so the section states what a shared coding
		// run reaches and what its job keeps, where it used to state the
		// refusal. Three separate facts, pinned separately, because a reader
		// outside the same binary consumes them: the launch hands the address,
		// the service admits the caller for the profile's repositories, and the
		// job keeps every mount (G17 — the drop is B5.7's flip).
		"**A shared project job is given the address too, on the same switch, and the class is served for its profile's repositories (B5.1, B5.2, D2078)**",
		"the profile, and not an IAM grant, is the boundary between the repositories one job serves",
		"**A shared coding run is served by the service for its profile's repositories, and no shared job loses a mount here**",
		"the job keeps the GitHub App key, the profile's provider keys and its Claude account's token",
		// The class has no deployment: the table its callers are read from is
		// rendered by no module, so every deployed service starts empty and
		// refuses the class whole. Stated rather than left to be discovered,
		// because it is the difference between "served" and "served here".
		"its table is `FUGARO_SHARED_JOB_ACCOUNTS`",
		"refuses the class whole",
		"fugaro creds set",
		// S3 (B3.5). The service brokers a workflow's own `secrets:` too, and
		// the claim that matters is the one F4 makes: the reach does NOT
		// change — the code under test still reads a plain variable in the
		// job's environment, so the service is where the runner fetches the
		// value, not a place the value stops. Pinned separately from the
		// model-credential sentences above, because a reader who takes the
		// two together would believe S3 narrows what a run can reach, and it
		// does not.
		"**The service reads the workflow's own `secrets:` too",
		"the reach does not change",
		"the same value in the same variable the job's environment carries",
		"`creds/workflowsecrets/<slug>`",
		"variable names and container IDs, never a value",
		// The runner's fetch, and what it is bound to: the verified job
		// account decides the repository, never a header the run writes (G19).
		// Version 2 names the logical secret and not a container (IS11's
		// reading of IS1 and IS6): the service builds the container from the
		// name, so the workflow stops being a fact the call carries.
		"the service's own route, which binds the repository to the **verified** job account",
		// A mounted value wins, so the flip is a no-op until B3.8 lands it.
		"**A mounted value always wins:**",
		// And the run-start dependency S3 adds, with D501's cold start and
		// D1934's heartbeat — the two facts a reader of the plan's T3 owes.
		"halts the run at bootstrap, before any agent call, rather than carrying on without the value (D143)",
		"pays a **cold start** on a run's own start",
		"keeps the instance serving it warm while it runs",
		"no redundancy beyond Cloud Run's own",
		// The exception D2075 stated, corrected: D3104 supersedes it (D3107,
		// the owner's ruling of 2026-11-20, docs/batch-run-identity's
		// reconciliation), so a Batch job mounts nothing at all and its
		// workflow secrets are fetched from the service like any other
		// repository's, while a launch the service does not serve is refused
		// before the job exists. Pinned, because the sentence it replaced
		// promised a Batch installation a mount it no longer has.
		"**A repository on `backend: cloud-batch` is served like any other (D3104, D3107):**",
		"a Batch job mounts nothing at all",
		"a launch of a repository the service does not serve is refused before the job is created",
		// The flip itself (IS6, D3324, D3328, D3333), which IS11 states where
		// a person meets it. Each fact is pinned separately, because each is
		// one a reader acts on: the mount that is gone, the container the
		// value now lives in, what admits a fetch, the marker that keeps the
		// two `fugaro_p_` classes apart, and the class the fetch refuses.
		"**No job mounts one any more: the value is the project's own container (IS6, D3324), and a name `fugaro update` has not copied yet keeps its mount until the update that copies it.**",
		"`fugaro_p_<name>`",
		"serves it only to a repository the project's own record admits",
		"`fugaro secrets allow` and `deny` write it, and `fugaro update` seeds it from the operator's `workflow_secrets` block, and never from a repository's own `fugaro.yaml`",
		"A container that does not carry the intake's workflow-secret marker is refused rather than read",
		"conditioned on the `fugaro_u_` and `fugaro_p_` prefixes",
		"a shared project job's run, a review job, a launcher and a person are each refused",
		"a shared job's run carries none at all",
		"Version 1 of the call",
		// S4 (B4.6). A build's credential step fetches from the service, and
		// the claim that matters is D2076's: what moves is where the build
		// reads the value, not what a Dockerfile can reach — the service
		// hands the build its repository's own token, and the value lands in
		// the volume the build step reads, exactly where BuildKit's mounted
		// secret was. Pinned separately from S3's sentences, because a reader
		// who takes "the build fetches from the service" as a narrowing
		// concludes that a Dockerfile can no longer read the token, which
		// D2076 (a) does not do.
		"**A build's credential step fetches from the service when its environment names it, and the reach does not change (S4, D2076 (a), D2077).**",
		"asks the service's build route for the git credential and for every workflow build secret the build mounts",
		// The wiring the review found missing: the tree carries the fetch in
		// the step and the request builder, and no request it submits is
		// given an address, so a repository's build and check job read their
		// mounts today. Pinned, because a reader who takes "the build fetches"
		// as the tree's behaviour would conclude the mount is already unused.
		"**No repository's build or check job is handed that address on this tree**",
		"a repository's build and its daily image check therefore still read their mounts",
		// The check job runs as the same account (E3), so it fetches the same
		// way: pinned, because a reader who takes "builds" to mean the image
		// build alone would think the daily check never fetches.
		"the daily image-check job, which runs as that same account",
		"asks the same route for its clone credential when it is handed the address",
		"the value lands in the volume the build step reads",
		"a Dockerfile reaches it exactly as it did through BuildKit's mounted secret",
		// The copy the fetch does not remove: the build account's accessor
		// and the request's own `availableSecrets` entries are what B4.9's
		// flip takes.
		"**And the build account has not given its copy up yet.**",
		"the build account keeps its `secretAccessor`",
		"`availableSecrets`",
		"until the flip takes them away (B4.9)",
		// The two classes do not read alike: the git credential's fetched
		// value is the one the clone uses, while a workflow build secret's
		// mount is what reaches the Dockerfile until the flip drops the
		// `availableSecrets` entry the build step prefers over the fetched
		// file. Pinned because the blanket "the fetched value wins" the first
		// draft carried was wrong for the second class.
		"**The git credential's fetched value wins over the mounted one**",
		"**a workflow build secret's mounted value wins until B4.9**",
		// The invariant, spelled as the code enforces it.
		"an OAuth token is only ever sent to Anthropic's own endpoint",
		// Unreachable halts rather than falls back (D143) — and the claim is
		// scoped to the job the opt-in reaches: a shared project job keeps its
		// mounts, so "the job has nothing to fall back to" is only true of a
		// per-repository job, while a shared job's run that the service cannot
		// serve stops at the service rather than being answered from the App
		// key its container still holds.
		"never falls back",
		"the mount is the switch's, not the outage's",
		"halts at the service's door rather than being answered from the App key its container still holds",
		// WS12 (D2983): an unreachable service is waited for before it halts,
		// which is the one shape of an outage a wait can end — a service that
		// is up and answers unreadably, or refuses, still halts the run at
		// once. Pinned with the bound and the naming, because the paragraph
		// said "halts" flat before this task, while a run now spends minutes
		// of its own budget on the wait and reports which service it was.
		"**Unreachable waits, then halts, and never falls back.**",
		"is waited for and retried for a bounded time",
		"a reason that names **which** service could not be reached",
		"its reason is already known, so it halts the run at once",
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
		// The database role's count, scoped (the review finding on B3.5): the
		// paragraph said it was the account's ONLY project-level grant and
		// that the installation project holds nothing else of the account's,
		// which the two project-level Secret Manager bindings of E25 falsify
		// in the installation project — the prefix-conditioned accessor and
		// the lister role — in either layout. Pinned as the scoped sentences,
		// because the flat count is what a reader checks against
		// modules/installation/credentials.tf and finds wrong.
		"the account's **only grant on the database**",
		"two of them are project-level in the installation project",
		"the installation project holds the two Secret Manager bindings and nothing of the database's",
		// The revocation, stated exactly: write-once outcome, and fx as the
		// only bound until it exists.
		"the write-once outcome",
		"the only bound on a replayed identity",
		// The accepted limits the owner ruled on 2026-10-19, and the one S5
		// adds to them (B5.4, the residue the plan names): one account serves a
		// whole profile, so a launcher can aim a run at another of that
		// profile's repositories and the service will serve it. Recorded
		// beside the other two rather than closed, because closing it is an
		// identity per repository, which is what a shared job is not.
		"The launcher's reach",
		"The replay bounds",
		"**A shared job's reach is its profile's, and that is the residue this slice accepts (D475, G21):**",
		"can still obtain a run identity naming another repository the profile admits",
		// The move itself (D3121, LM14): the launcher launches and the service
		// mints, so the section states what a launcher may still do — launch,
		// and not mint — and each residual the move leaves. Pinned one by one,
		// because a reader of the trust model acts on each: the claim race, the
		// denial of one launch, the bearer token, who writes past the rules, the
		// class the scope check does not judge, and the issuer as a single point.
		"**A launcher launches, and the service mints — this is the release that stops a launcher minting a run's identity (D3121, LM14).**",
		"may launch any repository its launch scope admits and may not mint",
		"**On Cloud Run a launcher who reads another run's ticket from its execution and runs code as the same job account before that run starts can claim first**",
		"**And a roster caller who learns a run id before its launcher has asked can ask first and take the record**",
		"**The caller's token is a bearer (O5):**",
		"**The issuance record, and who writes past the rules (R3).**",
		"a database admin, the history account, the coordinator's writer and the public mobile service's account",
		"**A roster service account is the one class the scope check does not judge (D3137):**",
		"**The issuer is a single point of every launch (D3121, R1).**",
		"**And `rb` is still the launcher's own word (R11):**",
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
		// The pre-B2.3 rule, in the two spellings this section carried before
		// the review of this branch: a non-cascade repository is switched on
		// under the mode and the bundle entry named above, and its job keeps
		// its model mount until the flip — so a reader who meets either
		// sentence concludes that a credential this release brokers never
		// leaves its job, which is the exposure the service exists to shrink.
		"A repository whose `agent.auth` is not `cascade` is never switched on",
		"a repository job in another `agent.auth` mode has no model credential at all",
		"the drop is auth-blind",
		// The pre-B5.1 refusal, in every spelling this document carried before
		// B5.4: the service serves a shared coding caller for its profile's
		// repositories now, so each of these sentences tells a `cascade` user
		// that a path which exists is closed to them — and tells a reader of
		// the trust model that a class of caller is excluded when the gate is
		// the published profile instead.
		"the service refuses a caller whose account belongs to one",
		"A shared project job is not given the address and is not served",
		"a `cascade` workflow cannot be served",
		"A shared project job is not wired either",
		"is deployed with no address at all",
		"refused at the gateway rather than answered from the job's own token",
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
		// that wires the service is 0.10.1. The bullet it used to carry
		// said the opt-in gave a job no address and that no installation
		// should opt in — both false once internal/cli/run.go sets
		// FUGARO_CREDENTIALS_SERVICE_URL on a repository job's execution.
		// B2.4 rewrote the half that replaced it again: as of this release the
		// switch admits `api-key` and `oauth` repositories too (B2.2, B2.3,
		// D2071), so "the half it does not carry is the other auth modes'" and
		// the auth-blind drop it named are both false. The MOUNT half has landed
		// with the flip (B2.7, D2072), so the bullet this list carries now says
		// what a switched one gives up and what it keeps, and the two pins below
		// are those sentences.
		"the service is wired as of 0.10.1",
		// The auths the switch admits, named rather than "every `agent.auth`":
		// `vertex` — the default — is the one it never admits, and the launcher
		// puts no address on such a repository's job at all (the switch,
		// infra.CredentialsServiceServes; internal/cli's credentialsServiceLaunchEnv),
		// so "every `agent.auth` reaches it" told a `vertex` repository's owner
		// that a credential the service never sees was brokered.
		"every `agent.auth` the switch admits — `cascade`, `api-key` and `oauth` — reaches it",
		"`vertex` is the one it does not admit at all",
		"its calls go to Vertex AI on the job's own service account",
		"the mount drop reached the other two in 0.26.0 (B2.7)",
		"FUGARO_CREDENTIALS_SERVICE_URL",
		"a switched `api-key` or `oauth` repository's job declares neither the Anthropic key nor the Claude OAuth token, and its account holds `secretAccessor` on neither",
		"every provider key the job mounts stays its own (R6, D1634)",
		// B5.4 rewrote this limit, deliberately: B5.2 hands a switched
		// profile's shared job the address on the same switch a repository's
		// own job is judged with, B5.1 pairs its account, and B5.4's own
		// follow-up landed the table — FUGARO_SHARED_JOB_ACCOUNTS is rendered
		// by the installation module, so a service that installation carries
		// after a re-apply admits the class. What is left is the installation
		// that has not been re-applied: its service keeps the empty shared set
		// and refuses the class whole. Stated rather than dropped: an owner who
		// reads "wired" and finds a shared run refused at the door has to be
		// able to find out why here.
		"**A shared project job is wired too, and it is the last job the switch reaches**",
		"the class's own table (`FUGARO_SHARED_JOB_ACCOUNTS`) is rendered by the installation module",
		"a service not re-applied since that release still carries an empty shared set and refuses the class whole",
		"a repository whose root has not been re-applied with the switch on is byte for byte what it was",
		// S3's limit (B3.5) as IS6 leaves it: the flip has landed, so the
		// read has left the job for a name `fugaro update` has copied — and
		// only for those. A name the update skipped keeps its mount, and a
		// root not re-applied with the switch on keeps every one. Stated here
		// rather than only beside the fetch, because a reader who takes "the
		// runner fetches them" as the whole story would conclude a switched
		// job no longer holds one.
		"the read leaves the job only for a name `fugaro update` has copied",
		// S4's own limit (B4.6): the build's fetch is wired and reached by no
		// repository's build or check job, so the read is still the mount's;
		// and the copy the flip ends is still there — the build account keeps
		// its accessor on the git secret and on the workflow build secrets,
		// and the request keeps the `availableSecrets` entries.
		"a repository's build and its daily image check still read their mounts on this tree",
		"nothing hands a repository's build request the URL",
		"the build's own copy of the git credential and of the workflow build secrets is still there",
		// And the App key's own limit stays: the mount and the grant are
		// still there, the runner just no longer reads the key.
		"the release after the owner's live Check 96 proves the deployed service mints",
		"no longer reads the key and mints at the service instead",
		// KS4's own limit (D3003, R11): the service checks the route for
		// every caller, and the cap is still the gateway's, so a call an
		// agent builds itself and posts to the provider route directly is
		// served under the policy's rules and never priced. Stated here
		// rather than only where the route is described, because a reader
		// who takes "the service holds the credentials" as the whole story
		// would conclude the run's cap bounds every call that spends one.
		"a hand-built call reaches the service without the gateway, and escapes the run's cost cap",
		"whoever built the request",
		"is not counted against the run's own cap",
		"Metering these calls in the service was considered and declined",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the `### The credentials service` section does not state the known limit %q: a limit that is not stated is read as a guarantee", want)
		}
	}
}

// TestSecurityMdStatesTheBitbucketGitProxy is task B1.10's headline: with the
// Bitbucket proxy shipped (B1.5, B1.6, B1.7), SECURITY.md has to say what a
// proxied run may do and what it no longer holds, in the document's own
// "state the limit" voice. The claims pinned here are the ones the plan's
// task B1.10 names: the agent can act as the run at the proxy (R3, O1), the
// reach it is narrowed to (one repository, the run's branch and pull
// requests, the run's life), D462's reach ending for a proxied repository with
// the flip landed, the build account's copy until
// S4, D501's cold starts and no redundancy, D504's revocation and what a
// cancel does not end, and O6's cross-repository fetch refusal.
//
// The section is read on its own (docSection), like the credentials service's
// claims: a bullet in "In short" that points here must not satisfy a pin on
// its own.
//
// Mutation (verified, run and restored): the R3 sentence removed from the
// section -- "The agent can act as the run at the proxy (R3, O1)." -- fails
// this on that pin and on the "one repository" sentence that follows it.
//
// The pins T14 of docs/bitbucket-token-types added are the three paragraphs
// this section gained (any mix of the three token types and the narrowest
// covering one; a failing token marked, and the node's facts readable and
// trusted at the writer's level; a rolled-back service refusing the whole
// bundle), the widening of the first paragraph's "the one Bitbucket token the
// bundle carries" to the plural, and the two banned spellings of that singular
// -- the red step this task was written around: the plural pin and the banned
// singular fail together until SECURITY.md and its pins move. The ban is the
// whole phrase and never the word "one", because the section says "that one
// token" and "a wider one" about what a request is served with, which is a
// different claim and still true.
//
// Third mutation (verified, run and restored), from the review of this round:
// the R5 skew sentence's "the row naming that fallback in
// docs/design/update-in-flight.md is the publisher's, T12, and is not written
// on this tree either" put back as the citation the first cut of this round
// gave it -- "(`docs/design/update-in-flight.md` names it)" -- fails this on
// that pin and on the banned phrase alone, which is the finding's second half:
// the page cited a row that is not there, and the fallback is the publisher's
// (T12) to write with the key.
//
// Fourth mutation (verified, run and restored), from the review that blocked
// the pull request once T12 had landed: the pins that describe the publisher
// put back as the not-yet-written wording this section carried before it --
// "**no non-test code on this release writes it into the published node**",
// "a typed token stored today serves nothing" and "**that line is owed with
// T12**" -- fail this on those three pins and on the three banned spellings of
// the same claim, and on nothing else. The landed wording is the stage that
// writes the key, the token that serves nothing until the update after the
// store, and the citation by line of the row and of the release notes' line.
//
// Second mutation (verified, run and restored), from the review of B1.10: the
// D504 paragraph put back as it was written first -- cancel "cancels the
// execution" once the run has launched, and "a cancelled run still finishes
// what it was launched to do" -- fails this on every cancel pin and on both
// banned spellings of that claim, and on nothing else (internal/cli/cancel.go
// writes the marker; a run stopped through Cloud Run after --grace or with
// --now never reaches finalize).
func TestSecurityMdStatesTheBitbucketGitProxy(t *testing.T) {
	s := readDoc(t, "SECURITY.md")
	const heading = "### The Bitbucket git proxy"
	if !strings.Contains(s, "\n"+heading+"\n") {
		t.Fatalf("SECURITY.md has no `%s` section: a proxied Bitbucket run changes what the agent can reach and what the job holds, and the section is where that is stated", heading)
	}
	section := flattenDoc(docSection(t, "SECURITY.md", s, heading))
	for _, want := range []string{
		// R3 and O1: the agent acts as the run, and the run is what the proxy
		// narrows. No agent sandbox is planned, said rather than assumed.
		"The agent can act as the run at the proxy",
		"one repository",
		"the run's own branch",
		"the run's own pull requests",
		"the run's life",
		"There is no agent sandbox, and none is planned",
		// The switch a repository is proxied by, and the one source of that
		// fact (G15): the published node's block with `proxy: true`. B2.4 adds
		// the second half of that fact's reach (F29): before the switch
		// admitted `api-key` and `oauth` repositories, `proxy: true` was inert
		// for every repository whose `agent.auth` was not `cascade`, because
		// the launcher writes the address on a job's execution only where the
		// switch holds — and a `vertex` repository is still not admitted at all.
		"bitbucket_token",
		"proxy: true",
		"`proxy: true` was inert for every repository whose `agent.auth` was not `cascade`",
		"a `vertex` repository is still not admitted by the switch at all and so cannot be proxied",
		// The forwarder's shape (G5): loopback, the job's ID token, no
		// credential of its own.
		"loopback forwarder",
		"no credential of its own",
		// D462's reach ends for a proxied repository: the run's own traffic
		// carries no token, and the flip has landed -- the list names the token
		// -- so the mount and the accessor are gone too. What is left is the
		// build account's copy, and every repository the node does not proxy.
		"config.ProxiedGitSecretNames",
		"names `bitbucket-token`",
		"reads no token at all",
		"holds no `secretAccessor` on the token",
		// The build account's copy until S4 — and B4.6's half of the same
		// fact: the read is the service's once the step is handed the
		// address, this tree hands one to no repository's build or check job,
		// the service hands the build this repository's own token when it is
		// asked, and what the flip ends is the accessor and the mounted copy
		// rather than the reach (D2076 (a)).
		"build account still holds the token",
		"until S4",
		"hands one to no repository's build or check job",
		"the service hands the build this repository's own token",
		"what the flip ends is the account's accessor and the mounted copy, not the reach",
		// The three token types (T14 of docs/bitbucket-token-types, D2350,
		// D2355): the bundle holds any mix, the narrowest covering one serves
		// each request, a reader of this page has to know that coverage is not
		// permission, and one run can carry several bot identities because the
		// token is chosen per request (D472, D2354). The plural is the whole
		// point of this round and the sentence that used to stand here -- the
		// bundle carrying "the one Bitbucket token" -- is banned below.
		"holds the Bitbucket tokens the bundle carries",
		"**The bundle may hold any mix of the three token types, and the narrowest covering one serves each request (D2350, D2355).**",
		"`fugaro creds set bitbucket-token--<name>`",
		"Every request is then served by the **narrowest token whose coverage admits the repository it names**",
		"carry **several bot identities**, because each token is a different author at Bitbucket (D472)",
		"so **coverage never widens what a run may reach** (D2354)",
		"A repository no usable token covers is refused **naming the repository** (`token_not_covered`), never served by a wider token that does not admit it.",
		// A failing token is marked, and the mark is a limit of this release:
		// the state store is the operator's to clear, no command here clears
		// it, and the mark is a node of its own so no reader takes a token for
		// a model account (R4, R12).
		"**A token that fails is marked, not trusted, and the node's facts are readable by every run.**",
		"a 403 or a 404 marks nothing and evicts the cached project match",
		"the **legacy** untyped `bitbucket-token` entry is never marked, because an installation holding only it keeps today's behaviour with no step (R3)",
		"the mark lives under the token's own subtree of the credential state store (`creds/tokens/<name>`)",
		"**No Fugaro command clears a Bitbucket token's mark in this release**",
		// What the published node says, who may read it, and whom it is
		// trusted to (F21, R5, R12), including the skew the new top-level key
		// brings to a CLI of the release behind.
		"**Each token's facts are readable by every run token of the job when they are published, and that is deliberate (F21, R5).**",
		// The publisher is T12, and T12 is in this release: the policy stage
		// of `fugaro update` writes the key (`buildProjectPolicy`,
		// `internal/cli/init_policy_publish.go`), from the local config's
		// records and the identities the state store holds (R5, R12), and
		// `fugaro project policy publish` is the same write. The three pins
		// below were the review's own finding, read the other way once T12
		// landed: the section used to say the publisher was not on the tree,
		// so the pins say which stage writes the key, that the node carries no
		// member until it runs, and where the row naming the older runner's
		// fallback is.
		"the policy stage of `fugaro update`",
		"`projectpolicy.DeriveBitbucketTokens` derives the key from the local config's typed records and from the identities the credential state store holds for them",
		"(`buildProjectPolicy`, `internal/cli/init_policy_publish.go`; `fugaro project policy publish` makes the same write)",
		"a typed token stored before the next `fugaro update` serves nothing",
		"`docs/design/update-in-flight.md:62`",
		"**upgrade the CLI before the `fugaro update` that publishes a typed Bitbucket token's coverage**",
		// The step's own release, and the one whose notes state the rule
		// without being able to publish the key: the review found the section
		// naming v0.25.0's `### For operators` section, which does not carry
		// the step, and v0.25.0 is the release whose `fugaro update` cannot
		// publish the key at all.
		"That step is written by the release that carries the publisher, under its `For operators` heading (`docs/releases/v0.26.0.md`)",
		"recorded under that release's `Known limits` heading (`docs/releases/v0.25.0.md`), because v0.25.0's own `fugaro update` cannot publish the key",
		"never a value and never a secret version",
		"**The identity set is trusted at the level of the node's writer (F21, R12):**",
		// The set can be partial, and R14 is why: a repository token answers no
		// GET /user, so a set naming fewer accounts than the bundle holds
		// tokens is the honest state and never a missing entry.
		"it can be **partial**, because a repository token answers no `GET /user` at all, so a set that names fewer accounts than the bundle holds tokens is the honest state and not a missing entry (R14)",
		"a hand-edited config states no identity, because the identity is never read from config",
		"**And a node carrying the key is a skew (R5):**",
		"a **CLI of the previous release refuses an unknown top-level key**",
		"a runner of the previous release tolerates the key and falls back to its single `GET /user` identity for a run already in flight",
		// The rollback the key family brings (T1, T11): the loader refuses the
		// whole document, so the service moves before the write.
		"**An older service refuses the whole bundle, so the service moves first.**",
		"the bundle loader refuses a document with a key it does not know **whole** — every credential in it, the model credentials included",
		"`docs/credentials-service/runbook.md` §11",
		// D501: cold starts on the critical path, and no redundancy.
		"scales to zero",
		"cold start",
		"no redundancy",
		// D504: what stops the proxy, and what a cancel does not end. A cancel
		// writes the marker and the runner finalizes a **draft** through the
		// proxy; only a run still in a stage after --grace (or one cancelled
		// with --now) has its execution stopped, and such a run never reaches
		// finalize -- internal/cli/cancel.go's own description.
		"fugaro cancel",
		"kill switch",
		"writes the run's cancel marker",
		"finalizes a **draft** pull request",
		"still in a stage after `--grace`",
		"`--now`",
		"never reaches finalize and opens no pull request",
		"a cancelled run does not finish its task",
		"the forwarder is closed only after finalize",
		"until the run's write-once outcome exists",
		"at most the ID token's hour",
		// O6: only this run's repository is fetched.
		"fetches only its own repository",
		"submodule",
		// R2: names only, and what is actually recorded -- one line per act
		// and nothing for a call refused before its identity verified, which
		// is what internal/credsvc does (server.go's refuseIdentity answers
		// through refuse, not refuseCall; forwardBitbucket writes its line
		// once, after bitbucket.org answered).
		"names only",
		"one line per **act**",
		"once the caller's identity is verified",
		"not recorded",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the `%s` section never says %q: a proxied run's reach and the credential it no longer reads are what this section is for", heading, want)
		}
	}
	// The overclaims a rewrite drifts into: the proxy is not a sandbox, the
	// token is not out of every job, the reach is not "no reach", and the
	// drop is not a repository's whole cloud. Read out of the whole document,
	// because a bullet that points here is as wrong as the section.
	//
	// Every one of them was written into this section once and is still false
	// after the flip: the flip takes the mount off a **proxied** repository's
	// job and nothing else, so a repository the node does not proxy keeps its
	// mount and the token's own reach, the build account keeps its copy until
	// S4, and what the proxy narrows is still the job's **own git traffic**.
	for _, banned := range []string{
		"the agent cannot reach Bitbucket at all",
		"the Bitbucket token is out of the job",
		"a proxied run is sandboxed",
		"no real token in reach",
		"instead of holding a token",
		"that reach ends **for that repository**",
		// The audit claim that was false on this tree: a call refused before
		// its identity verified writes no line at all (server.go's
		// refuseIdentity answers through refuse, not refuseCall), and a git
		// command is several proxied requests.
		"every refusal is audited with **names only**, one line per proxied request",
		"one line per proxied request",
		// The cancel wording that was false on this tree: cancel does not
		// itself cancel the execution once the run has launched, and a stopped
		// run reaches no finalize, so it opens no pull request at all
		// (internal/cli/cancel.go:76-84, 235-321).
		"once the run has launched, cancels the execution",
		"a cancelled run still finishes what it was launched to do",
		// The singular that was true before the three token types and is now
		// false twice over: the bundle carries a set of Bitbucket tokens, and
		// which one serves a request is decided per request (T14, D2350).
		"the one Bitbucket token the bundle carries",
		"the Bitbucket token the bundle carries",
		// The citation the first cut of this round gave for the R5 skew, which
		// the review found false: the page's row names the fallback, and it is
		// cited by its line now (`docs/design/update-in-flight.md:62`), so a
		// pointer that says the page does not carry it can never come back.
		"(`docs/design/update-in-flight.md` names it)",
		// What the second review found false once T12 landed: the publisher is
		// on this tree and `fugaro update` runs it, so every spelling of "it is
		// not here" is a claim about the tree that is no longer true -- and the
		// one that said the release-notes line was owed.
		"and the publisher is not on this tree",
		"no non-test code on this release writes it into the published node",
		"a typed token stored today serves nothing",
		"is the publisher's, T12, and is not written on this tree either",
		"that line is owed with T12",
		// What the third review found false: the operator step is not written
		// in v0.25.0's `### For operators` section (that file's sentence sits
		// under `### Known limits`), and v0.25.0 is the one release whose
		// `fugaro update` cannot publish the key, so a citation that names it
		// as the release the step belongs to can never come back.
		"what it asks of an operator is the release notes' `### For operators` line",
		"the release notes' `### For operators` line: **upgrade the CLI before",
	} {
		if strings.Contains(flattenDoc(s), banned) {
			t.Errorf("SECURITY.md says %q: the proxy narrows the run, and the flip that takes the mount has not landed on this tree", banned)
		}
	}
}
