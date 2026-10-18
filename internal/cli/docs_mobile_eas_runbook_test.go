package cli

// Task P4.4 of docs/mobile/implementation-plan.md -- "The EAS project, the
// signing keys and the store entries -- owner-run" -- is the one task in the
// mobile plan that **no agent performs a step of**, and its verification is
// Check 89 in the live register (docs/gcp-live-checklist.md, written by P4.2a).
// docs/mobile/eas-signing-runbook.md is the runbook the owner follows at their
// own terminal: the Expo/EAS project, the Apple and Google prerequisites, where
// each signing credential lives, the protected environment a future build
// workflow would read, and the rollback (D120, Global Constraint 9).
//
// There is nothing here to run against a fake. No agent runs `eas build` or
// `eas submit`, no signing key or store credential ever enters an agent's
// environment, and no CLI command implements a step of the runbook -- so the
// runbook's truth is a document's truth, and these are the presence tests every
// docs_*_test.go in this package writes. They hold five claims:
//
//   - TestDocsMobileEasRunbookNamesTheTaskAndTheCheck: the runbook is the one
//     the plan's P4.4 asks for, and it points at the register's Check 89
//     instead of restating it (P4.2a writes that text; PR #580 carries it, and
//     the runbook must not contradict it).
//   - TestDocsMobileEasRunbookStatesEveryOwnerOnlyWarning: each thing the
//     owner must not delegate is written down where a reader meets the step,
//     because "no agent performs any step" is the whole of Global Constraint 9.
//   - TestDocsMobileEasRunbookNamesEveryCredentialAndWhereItLives: every
//     credential the path needs is named, and each is placed (EAS's servers,
//     the owner's machine, or nowhere).
//   - TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape:
//     the repository-side settings, described rather than written -- no
//     workflow file is created here (Global Constraint 8).
//   - TestDocsMobileEasRunbookHoldsNoSecretShapedLiteral: the document holds
//     names and never a value, with a positive control so a mistyped pattern
//     cannot pass for a clean page.
//
// Two more tests hold the edges this runbook must not get wrong: the bundle
// identity tension it records rather than resolves (P3.1's config test and
// Check 89's guard want opposite things of mobile/app.json, and both are on the
// tree), and the directory's own index, which is where a reader finds it.

import (
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

const mobileEasRunbook = "docs/mobile/eas-signing-runbook.md"

// TestDocsMobileEasRunbookNamesTheTaskAndTheCheck asserts the runbook is the
// one Task P4.4 asks for and that it hands the running of the check to the
// register rather than paraphrasing it: Check 89's text is P4.2a's (PR #580),
// and a runbook that restated the steps would be a second copy to drift.
func TestDocsMobileEasRunbookNamesTheTaskAndTheCheck(t *testing.T) {
	doc := readDoc(t, mobileEasRunbook)
	for _, want := range []string{
		"Task P4.4",
		"EAS project, the signing keys and the store entries",
		"implementation-plan.md",
		"Check 89",
		"../gcp-live-checklist.md",
		"P4.2a",
		"#580",
		"NOT RUN",
		// The constraints and decisions the task is built from.
		"Global Constraint 9",
		"D111",
		"D120",
		"D125",
		"C2",
		"C6",
		"C8",
		// The register's own names for the things this runbook configures.
		"mobile/app.json",
		"mobile/eas.json",
		"distribution: store",
		"submit.internal.android.track",
		"eas whoami",
		"eas project:info",
		// The app is versioned from the tag the check builds (C6).
		"git describe --tags --exact-match",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not name %q", mobileEasRunbook, want)
		}
	}
}

// TestDocsMobileEasRunbookStatesEveryOwnerOnlyWarning asserts each owner-only
// warning is present. The list is the brief's own ("no agent performs any step,
// no signing key or store credential ever enters an agent's environment"), and
// each line is one an agent reading this page could otherwise read as an
// invitation: to run a build, to hold a token, to paste a value, or to write
// the workflow the build would use.
func TestDocsMobileEasRunbookStatesEveryOwnerOnlyWarning(t *testing.T) {
	doc := readDoc(t, mobileEasRunbook)
	for _, want := range []struct{ what, line string }{
		{"that no agent performs a step", "No agent performs any step of this runbook"},
		{"that no agent builds or submits", "No agent runs `eas build` or `eas submit`"},
		{"that no agent touches a key", "no agent touches a signing key or a store credential"},
		{"that no credential enters an agent's environment", "no signing key or store credential ever enters an agent's environment"},
		{"that it is the owner's terminal", "at your own terminal"},
		{"that it has never been run", "Nothing here has been run."},
		{"that it is instructions rather than a record", "This page is the instructions, not a record."},
		{"never to paste a key", "Never paste a signing key"},
		{"that a name is not a value", "the names only, never a value"},
		{"that a credential is not in this repository", "never in this repository"},
		{"that a store credential is not in CI", "never in CI"},
		{"what a store credential in CI would mean", "A store credential in CI is the finding Check 89's step 5 exists to produce."},
		{"that no agent copies a workflow", "No agent copies a workflow file into"},
	} {
		if !strings.Contains(doc, want.line) {
			t.Errorf("%s does not say (%s) %q", mobileEasRunbook, want.what, want.line)
		}
	}
}

// TestDocsMobileEasRunbookNamesEveryCredentialAndWhereItLives asserts the
// runbook's credential table is complete: each thing the path needs is named,
// and each is placed. A runbook that named a credential without saying where it
// lives is one an owner completes by putting it somewhere -- which is how a key
// reaches CI.
func TestDocsMobileEasRunbookNamesEveryCredentialAndWhereItLives(t *testing.T) {
	doc := readDoc(t, mobileEasRunbook)
	for _, want := range []string{
		// The two answers EAS offers, and the difference between them.
		"EAS-managed",
		"owner-held",
		// iOS.
		"distribution certificate",
		"provisioning profile",
		"App Store Connect API key",
		// Android: the upload key is the owner's; the app signing key is Google's.
		"upload key",
		"Play app signing",
		// The file names Check 89's step 5 looks for.
		"google-play-service-account.json",
		"serviceAccountKeyPath",
		"eas credentials",
		// The one name CI may hold, and the names it may not.
		"EXPO_TOKEN",
		"ASC_API_KEY_ID",
		"PLAY_SERVICE_ACCOUNT_JSON",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not name %q", mobileEasRunbook, want)
		}
	}
}

// TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape
// asserts the repository-side half is described: a protected environment whose
// only required reviewer is the owner (the `website-live` and `console-release`
// shape), the trigger the build would run on, least privilege, pinned actions --
// and that the runbook writes no workflow file, only the shape a later staged
// file under docs/pending-workflows/ would have (Global Constraint 8).
func TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape(t *testing.T) {
	doc := readDoc(t, mobileEasRunbook)
	for _, want := range []string{
		"mobile-release",
		"required reviewer",
		"environment:",
		"workflow_dispatch",
		"pull_request_target",
		"workflow_run",
		"full 40-character commit SHA",
		"permissions:",
		"contents: read",
		"docs/pending-workflows/",
		"describes a workflow and writes none",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not name %q", mobileEasRunbook, want)
		}
	}
	// The trigger shape is a stop, not a menu: the runbook must say which
	// events are refused rather than leaving the reader to infer it.
	for _, banned := range []string{"`push` of any branch", "`on: pull_request`"} {
		if strings.Contains(doc, banned) {
			t.Errorf("%s says %q: the build must not run on a pull request", mobileEasRunbook, banned)
		}
	}
}

// TestDocsMobileEasRunbookDoesNotRestateCheck89 asserts the runbook defers to
// the register's text, keeps its own guard as the check's (the block the owner
// re-runs before every step) and repeats none of its stop conditions as its
// own. It also asserts the two values Check 89's guard turns on are named the
// way the check names them, so the two files cannot drift into disagreement
// about which profile is the store one.
func TestDocsMobileEasRunbookDoesNotRestateCheck89(t *testing.T) {
	doc := readDoc(t, mobileEasRunbook)
	for _, want := range []string{
		"This runbook does not restate Check 89.",
		"The register is its authority.",
		"A profile whose submit section names a public track is a stop",
		"the `internal` profile",
		"never `production`",
		// What Check 89 asks the owner to paste back, by name.
		"gitCommitHash",
		"credentials source",
		"both submission ids",
		"the secret **names**",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not say %q", mobileEasRunbook, want)
		}
	}
}

// TestDocsMobileEasRunbookRecordsTheBundleIdentityTension asserts the one
// mismatch an owner will meet first is recorded rather than papered over.
// mobile/src/app-config.test.js (P3.1, on main) asserts mobile/app.json carries
// no bundle identifier and no Android package; the guard block of Check 89
// (P4.2a, PR #580) reads both fields out of that same file. Both are on the
// tree, neither is wrong on its own, and this runbook does not get to decide
// which is amended -- so it must say so, name both files, and say the owner
// rules.
func TestDocsMobileEasRunbookRecordsTheBundleIdentityTension(t *testing.T) {
	doc := readDoc(t, mobileEasRunbook)
	for _, want := range []string{
		"Recorded tensions, not resolved here",
		"app-config.test.js",
		"bundleIdentifier",
		"android.package",
		"the owner rules",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not record %q", mobileEasRunbook, want)
		}
	}
	if !strings.Contains(doc, "mobile/app.json") {
		t.Errorf("%s records the identity tension without naming the file both sides read", mobileEasRunbook)
	}
}

// runbookDocHosts is the allow-list of hosts a runbook may cite: the accounts'
// own documentation, the Expo project's, GitHub's, and this repository. A URL
// that is not one of these is a link the owner is asked to trust for a credential
// step, and the fix is the vendor's own documentation page.
var runbookDocHosts = map[string]bool{
	"docs.expo.dev":             true,
	"expo.dev":                  true,
	"developer.apple.com":       true,
	"appstoreconnect.apple.com": true,
	"play.google.com":           true,
	"support.google.com":        true,
	"docs.github.com":           true,
	"github.com":                true,
}

// TestDocsMobileEasRunbookCitesOnlyTheAccountsOwnDocumentation asserts every
// URL in the runbook is https and on a host the owner's accounts own. The
// runbook deliberately does not state an exact console label as certain -- the
// vendors rename them -- so each step names the page to check, and this test
// holds those pages to the accounts' own documentation rather than to a
// look-alike.
func TestDocsMobileEasRunbookCitesOnlyTheAccountsOwnDocumentation(t *testing.T) {
	doc := readDoc(t, mobileEasRunbook)
	links := regexp.MustCompile(`https?://[^\s)\]<>"']+`).FindAllString(doc, -1)
	if len(links) < 8 {
		t.Fatalf("%s cites %d URLs: the vendor steps are supposed to point at the vendors' documentation", mobileEasRunbook, len(links))
	}
	for _, link := range links {
		u, err := url.Parse(strings.TrimRight(link, ".,;:"))
		if err != nil {
			t.Errorf("%s cites %q, which is not a URL: %v", mobileEasRunbook, link, err)
			continue
		}
		if u.Scheme != "https" {
			t.Errorf("%s cites %q over %s: every one of these pages is https", mobileEasRunbook, link, u.Scheme)
		}
		if !runbookDocHosts[u.Host] {
			t.Errorf("%s cites %q: %q is not one of the accounts' own documentation hosts", mobileEasRunbook, link, u.Host)
		}
	}
}

// runbookSecretShapes are the shapes a credential takes when it is written down
// by accident, and the runbook must hold none of them. The list is the one
// mobile/src/accounts/settings.js refuses values with, in the shapes the
// repository writes them: a PEM block, a provider key prefix, a JWT, a long
// alphanumeric blob, and a value assigned to a name that says what it is.
var runbookSecretShapes = []struct {
	what string
	re   *regexp.Regexp
}{
	{"a PEM block", regexp.MustCompile(`-----BEGIN [A-Z ]*(PRIVATE KEY|CERTIFICATE|RSA)`)},
	{"a model-provider key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}`)},
	{"a GitHub token", regexp.MustCompile(`\b(ghp_|gho_|ghs_|ghu_|github_pat_)[A-Za-z0-9_]{10,}`)},
	{"an AWS access key id", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"a Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"a JSON Web Token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.`)},
	{"a value assigned to a credential's name", regexp.MustCompile(`(?i)\b(secret|token|password|apikey|api_key|private_key|service_account)\s*[:=]\s*["']?[A-Za-z0-9/+_.-]{16,}`)},
}

// runbookBlobish finds an unbroken run of 40 or more alphanumerics that holds an
// upper-case letter, a lower-case letter and a digit -- a key, a token or a
// commit SHA written into the page. The three-letter requirement is what keeps
// this from firing on a long word or a path: English prose is separated by
// spaces and URLs are separated by dots and slashes, so neither produces such a
// run, and a base64 blob of key length essentially always does.
var runbookBlobish = regexp.MustCompile(`\b[A-Za-z0-9]{40,}\b`)

func runbookSecretShaped(page string) []string {
	var found []string
	for _, shape := range runbookSecretShapes {
		for _, hit := range shape.re.FindAllString(page, -1) {
			found = append(found, shape.what+": "+hit)
		}
	}
	for _, hit := range runbookBlobish.FindAllString(page, -1) {
		if strings.ContainsAny(hit, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") &&
			strings.ContainsAny(hit, "abcdefghijklmnopqrstuvwxyz") &&
			strings.ContainsAny(hit, "0123456789") {
			found = append(found, "a long alphanumeric blob: "+hit)
		}
	}
	return found
}

// TestMobileEasRunbookSecretScanFindsEveryShape is the positive control, and it
// comes first for the reason every fence in this repository has one: a scan
// whose patterns match nothing reports a clean page whatever the page says.
// Every example is assembled at run time from harmless pieces, so this test
// file holds no credential-shaped literal either (mobile/src/accounts'
// convention).
func TestMobileEasRunbookSecretScanFindsEveryShape(t *testing.T) {
	planted := []struct {
		what  string
		value string
	}{
		{"a PEM block", "-" + "----BEGIN " + "PRIVATE KEY-----"},
		{"a model-provider key", "sk-" + "abcdef123456"},
		{"a GitHub token", "ghp_" + "abcdefghijklmnopqrst"},
		{"an AWS access key id", "AKIA" + "IOSFODNN7EXAMPLE"},
		{"a Google API key", "AI" + "za" + strings.Repeat("y", 35)},
		{"a JSON Web Token", "eyJ" + "hbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiIxIn0" + "." + "signature"},
		{"a long alphanumeric blob", "Aa1" + strings.Repeat("b2C", 14)},
		{"a value assigned to a credential's name", "password: " + "hunter2hunter2hunter"},
	}
	for _, tc := range planted {
		if got := runbookSecretShaped(tc.value); len(got) == 0 {
			t.Errorf("the scan does not find %s in a planted %q: a green run over the runbook would mean nothing", tc.what, tc.value)
		}
	}
	// And the control's other half: the words the runbook is made of are not
	// false positives, so a failure names a real defect.
	clean := []string{
		"The App Store Connect API key is the owner's, and it is never in CI.",
		"`google-play-service-account.json` is named by `serviceAccountKeyPath`, and its contents are never read.",
		"See https://docs.expo.dev/app-signing/managed-credentials/ for where EAS keeps the certificate.",
		"Pin every action to a full 40-character commit SHA.",
	}
	for _, page := range clean {
		if got := runbookSecretShaped(page); len(got) != 0 {
			t.Errorf("the scan calls clean prose a credential: %q -> %q", page, got)
		}
	}
}

// TestDocsMobileEasRunbookHoldsNoSecretShapedLiteral is the runbook's own half
// of the control above: names, paths and identifiers, and not one value. Check
// 89's step 5 makes the same claim of the repository by hand; this is the
// machine-checkable half of it for the page that tells the owner where the keys
// go.
func TestDocsMobileEasRunbookHoldsNoSecretShapedLiteral(t *testing.T) {
	doc := readDoc(t, mobileEasRunbook)
	if found := runbookSecretShaped(doc); len(found) != 0 {
		t.Errorf("%s holds %d secret-shaped literal(s): %v", mobileEasRunbook, len(found), found)
	}
	// The names it is supposed to hold, so the scan cannot be satisfied by a
	// runbook that names no credential at all.
	for _, want := range []string{"EXPO_TOKEN", "google-play-service-account.json"} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not name %q: the scan above is only meaningful over a page that names them", mobileEasRunbook, want)
		}
	}
}

// TestDocsMobileReadmeListsTheRunbook asserts the directory's own index carries
// the runbook: docs/mobile/README.md is the table a reader meets first, and a
// page nobody is pointed at is a page the next owner rediscovers from the plan.
func TestDocsMobileReadmeListsTheRunbook(t *testing.T) {
	readme := readDoc(t, "docs/mobile/README.md")
	for _, want := range []string{
		"eas-signing-runbook.md",
		"P4.4",
		"Check 89",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("docs/mobile/README.md does not name %q", want)
		}
	}
	if _, err := os.Stat("../../docs/mobile/eas-signing-runbook.md"); err != nil {
		t.Errorf("the README points at a runbook that is not there: %v", err)
	}
}
