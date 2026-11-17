package orchestrator_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// refusalOf is the rule err refuses with, failing the test when err is not a
// refusal of the never-list at all: a rule that answers a plain error, or nil,
// is a rule that does not stand where a call to the cloud API would (never.go,
// "the polarity").
func refusalOf(t *testing.T, err error) orchestrator.Refusal {
	t.Helper()
	if err == nil {
		t.Fatalf("the rule answered nil, so the path it stands on is open")
	}
	if !errors.Is(err, orchestrator.ErrNever) {
		t.Fatalf("errors.Is(err, ErrNever) is false, so a caller cannot tell a rule of section 6 from an outage: %v", err)
	}
	var refusal *orchestrator.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("errors.As(err, *Refusal) is false, so a log line cannot name the rule: %v", err)
	}
	return *refusal
}

// TestNeverRulesAreNumbered holds section 6's list to its own numbering and to
// its keys' being distinct: Number is the design's and the slice's order is
// the design's, so the two may not disagree, and two rules sharing a key would
// make a log line ambiguous about which of the eleven fired.
func TestNeverRulesAreNumbered(t *testing.T) {
	rules := orchestrator.NeverRules()
	if len(rules) != 11 {
		t.Fatalf("section 6 has %d rules here, and the design's list is eleven", len(rules))
	}
	seen := map[string]bool{}
	for i, rule := range rules {
		if rule.Number != i+1 {
			t.Errorf("rule %d carries number %d: the list is the design's order and its numbering is that order", i+1, rule.Number)
		}
		if rule.Key == "" || rule.Never == "" {
			t.Errorf("rule %d has no key or no sentence: a refusal names both", rule.Number)
		}
		if seen[rule.Key] {
			t.Errorf("two rules share the key %q, so a log line cannot say which fired", rule.Key)
		}
		seen[rule.Key] = true
	}
	// The copy is the caller's: editing it must not edit what a refusal is
	// written from.
	rules[0].Key = "edited"
	if orchestrator.NeverRules()[0].Key == "edited" {
		t.Errorf("NeverRules returns the list itself rather than a copy")
	}
}

// TestNeverIAMRefuses is rule 1, the wall a future path that reached for an IAM
// call meets.
func TestNeverIAMRefuses(t *testing.T) {
	refusal := refusalOf(t, orchestrator.NeverIAM("granting itself roles/run.invoker"))
	if refusal.Rule.Number != 1 || refusal.Rule.Key != "no-iam" {
		t.Errorf("the refusal is rule %d (%s), and an IAM change is section 6's rule 1", refusal.Rule.Number, refusal.Rule.Key)
	}
	if !strings.Contains(refusal.Error(), "No IAM.") {
		t.Errorf("the message does not carry the rule's own sentence, so a reader is not sent to section 6: %v", refusal.Error())
	}
}

// TestNeverManagedApplyAdmitsTheLaunchersThreePrefixes is rule 2 in both
// directions: the three prefixes a launcher may write under, and the bucket
// root, the managed marker and fugaro/allow-delete.json, which are outside the
// account's object write condition.
func TestNeverManagedApplyAdmitsTheLaunchersThreePrefixes(t *testing.T) {
	for _, admitted := range []string{
		"runs/fugaro-abc/task.json",
		"tokens/fugaro-abc/sealed",
		"issues/fugaro-abc/1.json",
	} {
		if err := orchestrator.NeverManagedApply(admitted); err != nil {
			t.Errorf("%s is inside one of the launchers' three prefixes, and the rule refuses it: %v", admitted, err)
		}
	}
	for _, refused := range []string{
		"",
		"runs",
		"fugaro/allow-delete.json",
		"cache/fugaro-abc/artifact",
		"locks/x",
		"builds/fugaro-abc/1.json",
		"reports/2026.json",
		"../runs/x",
	} {
		refusal := refusalOf(t, orchestrator.NeverManagedApply(refused))
		if refusal.Rule.Number != 2 {
			t.Errorf("%q is refused by rule %d, and a write outside the buckets three prefixes is section 6's rule 2", refused, refusal.Rule.Number)
		}
	}
}

// TestNeverLauncherPrefixesAreTheConditions holds the three prefixes this
// package reads out to the one condition that defines them: the launchers'
// objectUser grant on the runs bucket (gcp.LauncherBucketCondition, bound by
// the installation module). Two spellings of one fence drift, which is what
// this test is for.
func TestNeverLauncherPrefixesAreTheConditions(t *testing.T) {
	condition := gcp.LauncherBucketCondition("fugaro-runs-example")
	for _, prefix := range []string{"runs", "tokens", "issues"} {
		if !strings.Contains(condition, "objects/"+prefix+"/") {
			t.Errorf("the launchers' condition does not carry the %s/ prefix this package admits a write under:\n%s", prefix, condition)
		}
	}
	// And the list is closed the other way: a prefix the condition does not
	// carry may not be admitted here.
	for _, elsewhere := range []string{"cache", "locks", "builds", "reports", "fugaro"} {
		if strings.Contains(condition, "objects/"+elsewhere+"/") {
			t.Errorf("the launchers' condition carries %s/, which this package does not admit: the two lists must be the same three", elsewhere)
		}
	}
}

// TestNeverCommandAdmitsTheReadsAndRefusesTheDestructive is rule 3: a closed
// list, and the destructive commands section 6 names are not on it.
func TestNeverCommandAdmitsTheReadsAndRefusesTheDestructive(t *testing.T) {
	for _, admitted := range []string{"ls", "watch", "state", "run"} {
		if err := orchestrator.NeverCommand(admitted); err != nil {
			t.Errorf("`fugaro %s` is a command the resident orchestrator runs, and the rule refuses it: %v", admitted, err)
		}
	}
	for _, refused := range []string{
		"allow-delete", "budget", "secrets", "init", "restore",
		// The D128 amendment's three verbs: the spoken confirmation's, never a
		// name this list admits.
		"cancel", "approve", "merge",
		"",
	} {
		refusal := refusalOf(t, orchestrator.NeverCommand(refused))
		if refusal.Rule.Number != 3 {
			t.Errorf("`fugaro %s` is refused by rule %d, and a command outside the closed list is section 6's rule 3", refused, refusal.Rule.Number)
		}
	}
}

// TestNeverModelCredentialRefuses is rule 4: the resident service holds no
// model, git or App credential, and nothing it is handed makes one acceptable.
func TestNeverModelCredentialRefuses(t *testing.T) {
	refusal := refusalOf(t, orchestrator.NeverModelCredential("mounting the model key into the revision"))
	if refusal.Rule.Number != 4 || refusal.Rule.Key != "no-model-credential" {
		t.Errorf("the refusal is rule %d (%s), and a credential of its own is section 6's rule 4", refusal.Rule.Number, refusal.Rule.Key)
	}
}

// TestNeverSecretValueRefuses is rule 5, and the pairing key's copy is not its
// exception: that copy is P5.1e's, which lands the mount and section 4.7's
// step 2b with it.
func TestNeverSecretValueRefuses(t *testing.T) {
	for _, name := range []string{"pairing-key", "model-key", "github-app"} {
		refusal := refusalOf(t, orchestrator.NeverSecretValue(name))
		if refusal.Rule.Number != 5 {
			t.Errorf("reading %s is refused by rule %d, and a secret value is section 6's rule 5", name, refusal.Rule.Number)
		}
	}
}

// TestNeverBelongRefusesTheFamily is rule 6 (F31): the owner's own
// installation, in every spelling the tree's other guards know it by, and
// nothing else.
//
// The spellings are the two sides the rule is judged on. The word inside a
// longer name is the direction that fails closed and is what catches the
// project owner's installation is actually read from, `fugaro-belong`
// (check-lc4's own guard is a substring test "on purpose"); `edge-devel-dimi`
// carries the word nowhere at all, and `belong` was a name before it became a
// project id, so both are named here the way
// internal/backend/batch's outOfBoundsFloor names them.
func TestNeverBelongRefusesTheFamily(t *testing.T) {
	for _, refused := range []string{
		"belong", "belong-sandbox", "belong-2", "Belong",
		"fugaro-belong", "my-fugaro-belong", "sandbox-belong",
		"edge-devel-dimi", "EDGE-DEVEL-DIMI", " edge-devel-dimi ",
	} {
		refusal := refusalOf(t, orchestrator.NeverBelong(refused))
		if refusal.Rule.Number != 6 {
			t.Errorf("%q is refused by rule %d, and the owner's own installation is section 6's rule 6", refused, refusal.Rule.Number)
		}
	}
	// The two ids are matched exactly and not by prefix, which is
	// outOfBoundsFloor's own reason for doing the same: a project whose id
	// merely begins with one of them is another project, and refusing it would
	// refuse a project nobody named. The empty name is admitted here too — a
	// name nobody set is the caller's own check (orchestratorServiceConfig),
	// not a spelling of this rule.
	for _, admitted := range []string{"fugaro-sandbox", "aurora", "edge-devel-dimi-staging", ""} {
		if err := orchestrator.NeverBelong(admitted); err != nil {
			t.Errorf("%q is not the owner's own installation, and the rule refuses it: %v", admitted, err)
		}
	}
}

// TestNeverUnverifiedRefuses is rule 7: no MAC, no action, whatever arrived.
func TestNeverUnverifiedRefuses(t *testing.T) {
	for _, kind := range []string{"a message", "an answer", "an action record", "free text aimed at a destructive prompt"} {
		refusal := refusalOf(t, orchestrator.NeverUnverified(kind))
		if refusal.Rule.Number != 7 {
			t.Errorf("%s is refused by rule %d, and an unverified message is section 6's rule 7", kind, refusal.Rule.Number)
		}
	}
}

// TestNeverOwnPromptRefuses is rule 8: a promise rather than a mechanism, and
// a refusal all the same.
func TestNeverOwnPromptRefuses(t *testing.T) {
	for _, what := range []string{"answering the question this process asked", "treating a message's word as the confirmation"} {
		refusal := refusalOf(t, orchestrator.NeverOwnPrompt(what))
		if refusal.Rule.Number != 8 {
			t.Errorf("%s is refused by rule %d, and its own prompt is section 6's rule 8", what, refusal.Rule.Number)
		}
	}
}

// TestNeverSelfWideningRefuses is rule 9: no self-deploy, no second revision,
// no Terraform, no workflow file, no rules change.
func TestNeverSelfWideningRefuses(t *testing.T) {
	for _, what := range []string{"deploying a revision from a phone request", "writing a workflow file", "changing the database rules"} {
		refusal := refusalOf(t, orchestrator.NeverSelfWidening(what))
		if refusal.Rule.Number != 9 {
			t.Errorf("%s is refused by rule %d, and widening itself is section 6's rule 9", what, refusal.Rule.Number)
		}
	}
}

// TestNeverForeignInstallationRefusesAnotherInstallation is rule 10, including
// the direction it fails closed in: an installation nobody named is not "this
// one by default".
func TestNeverForeignInstallationRefusesAnotherInstallation(t *testing.T) {
	if err := orchestrator.NeverForeignInstallation("fugaro-sandbox", "fugaro-sandbox"); err != nil {
		t.Errorf("a name compared against itself is this installation's, and the rule refuses it: %v", err)
	}
	for _, c := range []struct{ project, want string }{
		{"fugaro-other", "fugaro-sandbox"},
		{"fugaro-sandbox", ""},
		{"", "fugaro-sandbox"},
	} {
		refusal := refusalOf(t, orchestrator.NeverForeignInstallation(c.project, c.want))
		if refusal.Rule.Number != 10 {
			t.Errorf("%q against %q is refused by rule %d, and another installation's nodes are section 6's rule 10", c.project, c.want, refusal.Rule.Number)
		}
	}
}

// TestNeverLauncherMembershipRefusesTheList is rule 11: the account is granted
// section 4.3's rows one binding at a time, and being on the launchers' list
// is the one thing that would add the three project roles beside them.
func TestNeverLauncherMembershipRefusesTheList(t *testing.T) {
	launchers := []string{"user:someone@example.com", "serviceAccount:orch@example.iam.gserviceaccount.com"}
	refusal := refusalOf(t, orchestrator.NeverLauncherMembership(launchers[1], launchers))
	if refusal.Rule.Number != 11 {
		t.Errorf("the orchestrator on var.launchers is refused by rule %d, and that list is section 6's rule 11", refusal.Rule.Number)
	}
	for _, member := range []string{"user:someone-else@example.com", "serviceAccount:orch@other.iam.gserviceaccount.com"} {
		if err := orchestrator.NeverLauncherMembership(member, launchers); err != nil {
			t.Errorf("%s is not on the launchers' list, and the rule refuses it: %v", member, err)
		}
	}
	if err := orchestrator.NeverLauncherMembership("user:x@example.com", nil); err != nil {
		t.Errorf("an installation with no launchers admits nobody to the list, and the rule refuses a member anyway: %v", err)
	}
}
