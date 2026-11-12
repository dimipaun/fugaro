package orchestrator_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The rule reads a record, a provider's answers and a findings file, so the
// fixtures below are those three: a run that every part of D1245's gate says
// yes to, and one of them broken per test. A path in a fixture is one the
// classifier places, which is what makes a test about a class a test about the
// classifier and not about the fixture.
const (
	triageHead    = "1a2b3c4d"
	triageRun     = "run-triage"
	triageRoutine = "docs/design/v1.md"
)

// mergeable is the input the rule merges as-is: every field is the positive
// evidence D1245 asks for, so a test that wants another outcome changes one
// thing about it.
func mergeable() orchestrator.TriageInput {
	return orchestrator.TriageInput{
		Record: &runstore.Record{
			RunID:        triageRun,
			Status:       runstore.StatusSucceeded,
			Stage:        "finalize",
			HeadSHA:      triageHead,
			PushedHead:   triageHead,
			ChangedFiles: &runstore.ChangedFiles{Paths: []string{triageRoutine}, Total: 1},
		},
		PR:        &gitprov.PRInfo{Number: 12, Draft: false},
		CI:        gitprov.CIGreen,
		Findings:  orchestrator.Findings{Read: orchestrator.FindingsReadable},
		Elapsed:   3 * time.Hour,
		Threshold: 2 * time.Hour,
	}
}

// findingsOf is the findings input a test wants, with the read state spelled
// out: a list is the readable case, and the two unreadable ones are named.
func findingsOf(list ...orchestrator.Finding) orchestrator.Findings {
	return orchestrator.Findings{Read: orchestrator.FindingsReadable, List: list}
}

// round is one triage round of a record holding one key, as the runner's own
// writer produces it (recordTriage, internal/runner): the rule reads the keys
// and nothing else of it.
func round(n int, keys ...string) runstore.TriageRound {
	entries := make([]runstore.TriageEntry, 0, len(keys))
	for i, k := range keys {
		entries = append(entries, runstore.TriageEntry{
			Index:   i,
			Key:     k,
			Verdict: runstore.VerdictBlock,
			Reason:  "the round placed it",
			Source:  runstore.TriageSourceAgent,
		})
	}
	return runstore.TriageRound{Round: n, Mode: rigor.Pro, Level: policy.LevelL2, Entries: entries}
}

// TestTriageNeverCallsAModel: G12's first clause. The fence is read off the
// package's own imports rather than off a call site, because a call a later
// change adds is exactly what the fence is for.
func TestTriageNeverCallsAModel(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if p == "github.com/dimipaun/fugaro/internal/agent" || strings.HasPrefix(p, "github.com/dimipaun/fugaro/internal/agent/") {
				t.Errorf("%s imports %s: the triage rule is code and makes no model call", name, p)
			}
		}
	}
}

// triageConstants is the string value of every constant triage.go declares
// with the named type, in source order, read out of the source rather than
// listed here: a vocabulary has one spelling in this package, and a constant
// added without its list is what the test below exists to catch.
func triageConstants(t *testing.T, typeName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "triage.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != typeName {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("triage.go: a %s constant's value is %T, not a string literal", typeName, v)
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, s)
			}
		}
	}
	return out
}

// TestTriageVocabulariesAreTheDeclaredConstants pins TriageReasons to the
// TriageReason constants triage.go declares, and Severities to the Severity
// ones, both directions and in order. Each list is the one spelling of its
// vocabulary, and each is what the caller gates on — a caller stores or prints
// a reason only after KnownTriageReason, and reads a finding's severity only
// after KnownSeverity — so a code the rule can answer with and the list lacks
// would be dropped by the very guard the rule's own doc sends the caller to,
// and one the list carries while no constant declares it is a word no writer
// can produce. Mutation (run, restored): drop a name from either list, or add
// a constant and leave it out, and this names the side that has it.
func TestTriageVocabulariesAreTheDeclaredConstants(t *testing.T) {
	declared := triageConstants(t, "TriageReason")
	if len(declared) == 0 {
		t.Fatal("no TriageReason constants found in triage.go: this test proves nothing")
	}
	reasons := make([]string, 0, len(orchestrator.TriageReasons))
	for _, r := range orchestrator.TriageReasons {
		reasons = append(reasons, string(r))
		if !orchestrator.KnownTriageReason(r) {
			t.Errorf("KnownTriageReason(%q) is false for a reason TriageReasons carries", r)
		}
	}
	if !slices.Equal(reasons, declared) {
		t.Errorf("TriageReasons = %v, the TriageReason constants triage.go declares = %v", reasons, declared)
	}
	for _, unknown := range []orchestrator.TriageReason{"", "no_such_reason"} {
		if orchestrator.KnownTriageReason(unknown) {
			t.Errorf("KnownTriageReason(%q) is true: the vocabulary is closed", unknown)
		}
	}

	declaredSeverities := triageConstants(t, "Severity")
	if len(declaredSeverities) == 0 {
		t.Fatal("no Severity constants found in triage.go: this test proves nothing")
	}
	severities := make([]string, 0, len(orchestrator.Severities))
	for _, s := range orchestrator.Severities {
		severities = append(severities, string(s))
		if !orchestrator.KnownSeverity(s) {
			t.Errorf("KnownSeverity(%q) is false for a severity Severities carries", s)
		}
	}
	if !slices.Equal(severities, declaredSeverities) {
		t.Errorf("Severities = %v, the Severity constants triage.go declares = %v", severities, declaredSeverities)
	}
	for _, unknown := range []orchestrator.Severity{"", "blocker"} {
		if orchestrator.KnownSeverity(unknown) {
			t.Errorf("KnownSeverity(%q) is true: a word outside the set is refused, never read as low", unknown)
		}
	}
}

// TestTriageMergesOnlyWithGreenCIAndANonCriticalFindingSet: the merge is the
// one outcome that lands work without a person, so it takes every part of the
// evidence and a red CI or a critical finding takes it back (D1245, G12).
func TestTriageMergesOnlyWithGreenCIAndANonCriticalFindingSet(t *testing.T) {
	got := orchestrator.Triage(mergeable())
	if got.Kind != orchestrator.MergeAsIs {
		t.Fatalf("the whole gate passed and the rule answered %q (%v): %+v", got.Kind, got.Reasons, got)
	}
	if !got.Due() {
		t.Errorf("a run past the threshold is due: %+v", got)
	}
	if !got.WithoutAPerson {
		t.Errorf("a merge lands work without a person: %+v", got)
	}

	in := mergeable()
	in.CI = gitprov.CIRed
	if got := orchestrator.Triage(in); got.Kind == orchestrator.MergeAsIs {
		t.Errorf("a red CI merged the run: %+v", got)
	}

	in = mergeable()
	in.Findings = findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityCritical, Path: triageRoutine, Key: "k1"})
	if got := orchestrator.Triage(in); got.Kind != orchestrator.Escalate {
		t.Errorf("a critical finding did not escalate: %+v", got)
	}
}

// TestTriageFailsClosedWithoutAFindingsFile: BF1's file is not on main
// (reconciliation U10), so until it lands every triage takes the outcome that
// acts on nothing (D1247).
func TestTriageFailsClosedWithoutAFindingsFile(t *testing.T) {
	in := mergeable()
	in.Findings = orchestrator.Findings{Read: orchestrator.FindingsAbsent}

	got := orchestrator.Triage(in)
	if got.Kind != orchestrator.RecommendOnly {
		t.Fatalf("an absent findings file answered %q, and a merge on it would land work nobody read: %+v", got.Kind, got)
	}
	if !slices.Contains(got.Reasons, orchestrator.ReasonFindingsAbsent) {
		t.Errorf("the reason does not name the absent file: %v", got.Reasons)
	}
	if got.Unreadable != orchestrator.InputFindings {
		t.Errorf("the print names %q as the input it could not read", got.Unreadable)
	}
	if got.WithoutAPerson {
		t.Errorf("recommend-only merges, cancels and restarts nothing: %+v", got)
	}
}

// TestTriageFailsClosedOnAnUnknownSeverity: a word the rule does not know is
// not a word it can place, and the fail-closed reading is the one that acts on
// nothing (D1247).
func TestTriageFailsClosedOnAnUnknownSeverity(t *testing.T) {
	in := mergeable()
	in.Findings = findingsOf(orchestrator.Finding{Severity: "urgent", Path: triageRoutine, Key: "k1"})

	got := orchestrator.Triage(in)
	if got.Kind != orchestrator.RecommendOnly {
		t.Fatalf("an unknown severity answered %q: %+v", got.Kind, got)
	}
	if !slices.Contains(got.Reasons, orchestrator.ReasonUnknownSeverity) {
		t.Errorf("the reason does not name the severity: %v", got.Reasons)
	}
	if got.Unreadable != orchestrator.InputFindings {
		t.Errorf("the print names %q as the input it could not read", got.Unreadable)
	}
}

// TestTriageUsesThePathClassifierForSecurity: a high finding is a person's
// only in a security path, and "security path" is the path classifier's answer
// over the finding's own file — never a word in the finding (D1245, O1).
func TestTriageUsesThePathClassifierForSecurity(t *testing.T) {
	in := mergeable()
	in.Findings = findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityHigh, Path: triageRoutine, Key: "k1"})
	if got := orchestrator.Triage(in); got.Kind != orchestrator.MergeAsIs {
		t.Errorf("a high finding in a routine path answered %q, and the merge is the outcome the plan gives it: %+v", got.Kind, got)
	}

	in = mergeable()
	in.Findings = findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityHigh, Path: "internal/creds/store.go", Key: "k1"})
	got := orchestrator.Triage(in)
	if got.Kind != orchestrator.Escalate {
		t.Fatalf("a high finding in a security path answered %q: %+v", got.Kind, got)
	}
	if !slices.Contains(got.Reasons, orchestrator.ReasonSecurityHighFinding) {
		t.Errorf("the reason does not name the security path: %v", got.Reasons)
	}

	in = mergeable()
	in.Findings = findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityMedium, Path: "internal/creds/store.go", Key: "k1"})
	if got := orchestrator.Triage(in); got.Kind == orchestrator.Escalate {
		t.Errorf("a medium finding in a security path escalated: only high is a person's there: %+v", got)
	}
}

// TestTriageEscalatesOnACriticalFinding: a critical finding stops the run
// wherever it names, and it is the first outcome tried (D1247, D1244).
func TestTriageEscalatesOnACriticalFinding(t *testing.T) {
	in := mergeable()
	in.Findings = findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityCritical, Path: triageRoutine, Key: "k1"})

	got := orchestrator.Triage(in)
	if got.Kind != orchestrator.Escalate {
		t.Fatalf("a critical finding in a routine path answered %q: %+v", got.Kind, got)
	}
	if !slices.Contains(got.Reasons, orchestrator.ReasonCriticalFinding) {
		t.Errorf("the reason does not name the finding: %v", got.Reasons)
	}
	if got.WithoutAPerson {
		t.Errorf("an escalation cancels and asks a person: %+v", got)
	}
}

// TestTriageRestartsOnNoCommits: a long run that pushed nothing has nothing to
// land, and D1246's answer is smaller work rather than the same spec again.
// The fixture clears both halves of the signal at once — no pushed head and no
// changed path — so each half is pinned on its own by
// TestTriageRestartsOnEitherHalfOfNoCommits.
func TestTriageRestartsOnNoCommits(t *testing.T) {
	in := mergeable()
	in.Record.PushedHead = ""
	in.Record.ChangedFiles = &runstore.ChangedFiles{Paths: []string{}, Total: 0}

	got := orchestrator.Triage(in)
	if got.Kind != orchestrator.Restart {
		t.Fatalf("a run with no commits answered %q: %+v", got.Kind, got)
	}
	if !slices.Contains(got.Reasons, orchestrator.ReasonNoCommits) {
		t.Errorf("the reason does not name the commits: %v", got.Reasons)
	}
	if !got.WithoutAPerson {
		t.Errorf("a restart cancels the run without asking: %+v", got)
	}
}

// TestTriageRestartsOnNoProgress: the stage, the head and the pushed head are
// the ones the tick before saw, on a run still in flight (D1246).
func TestTriageRestartsOnNoProgress(t *testing.T) {
	in := mergeable()
	in.Record.Status = runstore.StatusRunning
	in.Progress = &orchestrator.Progress{Stage: in.Record.Stage, Head: in.Record.HeadSHA, PushedHead: in.Record.PushedHead}

	got := orchestrator.Triage(in)
	if got.Kind != orchestrator.Restart {
		t.Fatalf("a run in flight that has not moved answered %q: %+v", got.Kind, got)
	}
	if !slices.Contains(got.Reasons, orchestrator.ReasonNoProgress) {
		t.Errorf("the reason does not name the progress: %v", got.Reasons)
	}

	in = mergeable()
	in.Record.Status = runstore.StatusRunning
	in.Progress = &orchestrator.Progress{Stage: "implement", Head: in.Record.HeadSHA, PushedHead: in.Record.PushedHead}
	if got := orchestrator.Triage(in); got.Kind == orchestrator.Restart {
		t.Errorf("a run in flight that moved restarted: %+v", got)
	}
}

// TestTriageRestartsOnALoopingFinding: a finding the run's own rounds placed
// twice is the loop D1246 splits, and the key is the identity (D1246, D2036).
func TestTriageRestartsOnALoopingFinding(t *testing.T) {
	in := mergeable()
	in.PR.Draft = true
	in.Record.Triage = runstore.NewTriage([]runstore.TriageRound{
		round(1, "loop-key"),
		round(2, "loop-key"),
	})
	in.Findings = findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityMedium, Path: triageRoutine, Key: "loop-key"})

	got := orchestrator.Triage(in)
	if got.Kind != orchestrator.Restart {
		t.Fatalf("a finding placed in two rounds answered %q: %+v", got.Kind, got)
	}
	if !slices.Contains(got.Reasons, orchestrator.ReasonLoopingFinding) {
		t.Errorf("the reason does not name the loop: %v", got.Reasons)
	}

	in = mergeable()
	in.PR.Draft = true
	in.Record.Triage = runstore.NewTriage([]runstore.TriageRound{round(1, "loop-key")})
	in.Findings = findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityMedium, Path: triageRoutine, Key: "loop-key"})
	if got := orchestrator.Triage(in); got.Kind == orchestrator.Restart {
		t.Errorf("a finding placed in one round restarted: %+v", got)
	}
}

// TestTriageIgnoresARunUnderTheThreshold: the threshold is the caller's, and a
// run that has not reached it is not read at all — the answer is the distinct
// "not due" and never one of the four (D1244, D2032).
func TestTriageIgnoresARunUnderTheThreshold(t *testing.T) {
	in := mergeable()
	in.Elapsed = time.Hour

	got := orchestrator.Triage(in)
	if got.Due() {
		t.Fatalf("a run under the threshold was triaged: %+v", got)
	}
	for _, kind := range []orchestrator.TriageKind{
		orchestrator.MergeAsIs, orchestrator.Restart, orchestrator.Escalate, orchestrator.RecommendOnly,
	} {
		if got.Kind == kind {
			t.Errorf("the not-due answer is %q, which is one of the four outcomes", kind)
		}
	}
	if len(got.Reasons) != 0 || got.WithoutAPerson {
		t.Errorf("a run nobody read carries no reasons and acts on nothing: %+v", got)
	}

	in.Elapsed = in.Threshold
	if got := orchestrator.Triage(in); !got.Due() {
		t.Errorf("a run at the threshold is due: %+v", got)
	}

	in = mergeable()
	in.Threshold = 0
	if got := orchestrator.Triage(in); got.Due() {
		t.Errorf("a caller that supplied no threshold triaged the run: %+v", got)
	}
}

// TestTriageFailsClosedWithoutACIAnswer: CIUnknown is an answer nobody gave —
// a failed read, or a check that has not finished — and it is never a merge
// (D1244, D1245, G12).
func TestTriageFailsClosedWithoutACIAnswer(t *testing.T) {
	in := mergeable()
	in.CI = gitprov.CIUnknown

	got := orchestrator.Triage(in)
	if got.Kind != orchestrator.RecommendOnly {
		t.Fatalf("an unanswered CI read answered %q: %+v", got.Kind, got)
	}
	if !slices.Contains(got.Reasons, orchestrator.ReasonCIUnknown) {
		t.Errorf("the reason does not name the CI read: %v", got.Reasons)
	}
	if got.Unreadable != orchestrator.InputCI {
		t.Errorf("the print names %q as the input it could not read", got.Unreadable)
	}
}

// TestTriageNeverMergesWhileCIIsPending: a check that has not finished is what
// the provider's own reader calls CIUnknown, so the pending case and the
// failed read are one answer here (gitprov.ChecksState, F25).
func TestTriageNeverMergesWhileCIIsPending(t *testing.T) {
	pending := gitprov.ChecksState([]gitprov.CheckRun{{Status: "in_progress"}})
	if pending != gitprov.CIUnknown {
		t.Fatalf("a pending check reads as %v, and the rule's no-answer input is CIUnknown", pending)
	}

	in := mergeable()
	in.CI = pending
	if got := orchestrator.Triage(in); got.Kind == orchestrator.MergeAsIs {
		t.Errorf("a run whose checks have not finished was merged: %+v", got)
	}
}

// TestTriageRecommendsOnlyWhenAnInputCannotBeRead: each unreadable input on
// its own takes the outcome that acts on nothing, and the print names it
// (D1247).
func TestTriageRecommendsOnlyWhenAnInputCannotBeRead(t *testing.T) {
	cases := []struct {
		name       string
		break_     func(*orchestrator.TriageInput)
		reason     orchestrator.TriageReason
		unreadable orchestrator.UnreadableInput
	}{
		{
			name: "the findings are absent",
			break_: func(in *orchestrator.TriageInput) {
				in.Findings = orchestrator.Findings{Read: orchestrator.FindingsAbsent}
			},
			reason:     orchestrator.ReasonFindingsAbsent,
			unreadable: orchestrator.InputFindings,
		},
		{
			name: "the findings are not the runner's own",
			break_: func(in *orchestrator.TriageInput) {
				in.Findings = orchestrator.Findings{Read: orchestrator.FindingsUnattributable}
			},
			reason:     orchestrator.ReasonFindingsUnattributable,
			unreadable: orchestrator.InputFindings,
		},
		{
			name:       "the findings carry no read state at all",
			break_:     func(in *orchestrator.TriageInput) { in.Findings = orchestrator.Findings{} },
			reason:     orchestrator.ReasonFindingsUnreadable,
			unreadable: orchestrator.InputFindings,
		},
		{
			name: "a severity nobody knows",
			break_: func(in *orchestrator.TriageInput) {
				in.Findings = findingsOf(orchestrator.Finding{Severity: "blocker", Key: "k1"})
			},
			reason:     orchestrator.ReasonUnknownSeverity,
			unreadable: orchestrator.InputFindings,
		},
		{
			name:       "the record was not given",
			break_:     func(in *orchestrator.TriageInput) { in.Record = nil },
			reason:     orchestrator.ReasonRecordUnreadable,
			unreadable: orchestrator.InputRecord,
		},
		{
			name:       "the record's status is not one this release knows",
			break_:     func(in *orchestrator.TriageInput) { in.Record.Status = "halfway" },
			reason:     orchestrator.ReasonRecordUnreadable,
			unreadable: orchestrator.InputRecord,
		},
		{
			name:       "the record holds no changed-path set",
			break_:     func(in *orchestrator.TriageInput) { in.Record.ChangedFiles = nil },
			reason:     orchestrator.ReasonPathsMissing,
			unreadable: orchestrator.InputPaths,
		},
		{
			name:       "the changed-path set is short of what the record says it was",
			break_:     func(in *orchestrator.TriageInput) { in.Record.ChangedFiles.Truncated = true },
			reason:     orchestrator.ReasonPathsTruncated,
			unreadable: orchestrator.InputPaths,
		},
		{
			name:       "the pull request's CI could not be read",
			break_:     func(in *orchestrator.TriageInput) { in.CI = gitprov.CIUnknown },
			reason:     orchestrator.ReasonCIUnknown,
			unreadable: orchestrator.InputCI,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := mergeable()
			tc.break_(&in)

			got := orchestrator.Triage(in)
			if got.Kind != orchestrator.RecommendOnly {
				t.Fatalf("an input nobody could read answered %q: %+v", got.Kind, got)
			}
			if !slices.Contains(got.Reasons, tc.reason) {
				t.Errorf("the reason is %v, and %q is the input's", got.Reasons, tc.reason)
			}
			if got.Unreadable != tc.unreadable {
				t.Errorf("the print names %q as the input it could not read, and %q is the one that failed", got.Unreadable, tc.unreadable)
			}
			if got.WithoutAPerson {
				t.Errorf("recommend-only acts on nothing: %+v", got)
			}
		})
	}
}

// TestTriageNeverCancelsOnAnUnreadableFindingsFile: the run's record alone
// would be a restart, and the fail-closed outcome is tried before the one that
// cancels — an input nobody read is not evidence that the run is bad (D1247).
func TestTriageNeverCancelsOnAnUnreadableFindingsFile(t *testing.T) {
	in := mergeable()
	in.Record.Status = runstore.StatusRunning
	in.Progress = &orchestrator.Progress{Stage: in.Record.Stage, Head: in.Record.HeadSHA, PushedHead: in.Record.PushedHead}
	in.Findings = orchestrator.Findings{Read: orchestrator.FindingsAbsent}

	if got := orchestrator.Triage(in); got.Kind != orchestrator.RecommendOnly {
		t.Fatalf("a restartable run with no findings file answered %q: %+v", got.Kind, got)
	}
}

// TestTriageBlocksTheMergeOnASensitiveChangedPath: the classifier is asked
// about every path the record carries, so one path outside the routine
// allowlist is no merge however green the CI (D1245, F25). The set is the
// run's own account and is bounded rather than corroborated, which is the
// merge gate's own doc and not a property this test proves.
func TestTriageBlocksTheMergeOnASensitiveChangedPath(t *testing.T) {
	for _, p := range []string{".github/workflows/ci.yml", "internal/creds/store.go", "CLAUDE.md", "internal/runner/runner.go"} {
		in := mergeable()
		in.Record.ChangedFiles = &runstore.ChangedFiles{Paths: []string{triageRoutine, p}, Total: 2}

		got := orchestrator.Triage(in)
		if got.Kind == orchestrator.MergeAsIs {
			t.Errorf("a run whose diff reaches %s was merged: %+v", p, got)
		}
		if got.WithoutAPerson {
			t.Errorf("nothing about %s is taken without a person: %+v", p, got)
		}
	}
}

// TestTriageNeverMergesATruncatedPathSet: the class is taken over the whole
// diff, and a set the record cut short is not the whole diff. The record is an
// object the run's own service account writes, so it is read through
// ChangedFiles.Bounded and a set short of its own Total counts as truncated
// even when the record does not say so (D1245, G12).
func TestTriageNeverMergesATruncatedPathSet(t *testing.T) {
	paths := make([]string, 0, runstore.MaxChangedFiles)
	for i := 0; i < runstore.MaxChangedFiles; i++ {
		paths = append(paths, triageRoutine)
	}

	in := mergeable()
	in.Record.ChangedFiles = &runstore.ChangedFiles{Paths: paths, Total: len(paths), Truncated: true}
	got := orchestrator.Triage(in)
	if got.Kind != orchestrator.RecommendOnly {
		t.Fatalf("a truncated path set answered %q: %+v", got.Kind, got)
	}
	if !slices.Contains(got.Reasons, orchestrator.ReasonPathsTruncated) {
		t.Errorf("the reason does not name the truncation: %v", got.Reasons)
	}

	in = mergeable()
	in.Record.ChangedFiles = &runstore.ChangedFiles{Paths: paths, Total: 5 * len(paths)}
	if got := orchestrator.Triage(in); got.Kind == orchestrator.MergeAsIs {
		t.Errorf("a set the record cut short of its own total was merged: %+v", got)
	}

	in = mergeable()
	in.Record.ChangedFiles = nil
	if got := orchestrator.Triage(in); got.Kind == orchestrator.MergeAsIs {
		t.Errorf("a record with no path set was merged: %+v", got)
	}
}

// TestTriageRefusesAnUnknownSeverityWithoutCancelling: the run's own record
// would be a restart, and the severity the rule cannot read keeps it from
// cancelling as much as it keeps it from merging (D1247).
func TestTriageRefusesAnUnknownSeverityWithoutCancelling(t *testing.T) {
	in := mergeable()
	in.Record.PushedHead = ""
	in.Record.ChangedFiles = &runstore.ChangedFiles{Paths: []string{}, Total: 0}
	in.Findings = findingsOf(orchestrator.Finding{Severity: "urgent", Path: triageRoutine, Key: "k1"})

	got := orchestrator.Triage(in)
	if got.Kind != orchestrator.RecommendOnly {
		t.Fatalf("a restartable run with an unknown severity answered %q: %+v", got.Kind, got)
	}
	if got.WithoutAPerson {
		t.Errorf("recommend-only cancels nothing: %+v", got)
	}
}

// TestTriageNeverMergesAnInFlightRun: the pull request a running run has open
// is a draft, and a run still in flight past the threshold is never merged
// however green the CI (D1245).
func TestTriageNeverMergesAnInFlightRun(t *testing.T) {
	in := mergeable()
	in.Record.Status = runstore.StatusRunning

	if got := orchestrator.Triage(in); got.Kind == orchestrator.MergeAsIs {
		t.Errorf("a run still in flight was merged: %+v", got)
	}

	in.PR.Draft = true
	if got := orchestrator.Triage(in); got.Kind == orchestrator.MergeAsIs {
		t.Errorf("a draft pull request was merged: %+v", got)
	}
}

// TestTriageNeverMergesADraftPullRequest: a draft is what a failed run, a
// cancelled one and one still in flight all leave behind (D1245).
func TestTriageNeverMergesADraftPullRequest(t *testing.T) {
	in := mergeable()
	in.PR.Draft = true

	got := orchestrator.Triage(in)
	if got.Kind == orchestrator.MergeAsIs {
		t.Fatalf("a run whose pull request is a draft was merged: %+v", got)
	}
	if got.WithoutAPerson {
		t.Errorf("a draft is nothing this rule acts on without a person: %+v", got)
	}
}

// TestTriagePrefersEscalateOverEveryOtherOutcome: escalate is first because it
// acts on a finding the triage did read, so a run that would also merge, or
// whose other inputs nobody could read, escalates all the same (D1244).
func TestTriagePrefersEscalateOverEveryOtherOutcome(t *testing.T) {
	in := mergeable()
	in.Findings = findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityCritical, Path: triageRoutine, Key: "k1"})
	if got := orchestrator.Triage(in); got.Kind != orchestrator.Escalate {
		t.Errorf("a critical finding on a mergeable run answered %q: %+v", got.Kind, got)
	}

	in.CI = gitprov.CIUnknown
	if got := orchestrator.Triage(in); got.Kind != orchestrator.Escalate {
		t.Errorf("a critical finding on a run whose CI nobody read answered %q: %+v", got.Kind, got)
	}

	in.Record.ChangedFiles.Truncated = true
	if got := orchestrator.Triage(in); got.Kind != orchestrator.Escalate {
		t.Errorf("a critical finding on a run whose path set is cut answered %q: %+v", got.Kind, got)
	}

	in.Record = nil
	if got := orchestrator.Triage(in); got.Kind != orchestrator.Escalate {
		t.Errorf("a critical finding on a run whose record nobody read answered %q: %+v", got.Kind, got)
	}

	in = mergeable()
	in.Record.PushedHead = ""
	in.Record.ChangedFiles = nil
	in.Findings = findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityCritical, Path: triageRoutine, Key: "k1"})
	if got := orchestrator.Triage(in); got.Kind != orchestrator.Escalate {
		t.Errorf("a critical finding on a run that would restart answered %q: %+v", got.Kind, got)
	}
}

// TestTriageMergesAFinishedStalledRunRatherThanRestartingIt: the overlap the
// fixed order decides. This pins the order and not the restart's in-flight
// guard: the merge is tried before restart and answers first, so the mutation
// that drops the guard leaves this fixture green.
// TestTriageReadsAStallOnlyWhileTheRunIsInFlight is the guard's own test
// (D1244, D1245).
func TestTriageMergesAFinishedStalledRunRatherThanRestartingIt(t *testing.T) {
	in := mergeable()
	in.Progress = &orchestrator.Progress{Stage: in.Record.Stage, Head: in.Record.HeadSHA, PushedHead: in.Record.PushedHead}

	got := orchestrator.Triage(in)
	if got.Kind != orchestrator.MergeAsIs {
		t.Fatalf("a finished run whose record stopped moving answered %q: %+v", got.Kind, got)
	}
}

// TestTriageNeverEscalatesOnFindingsItCouldNotRead: escalate acts on a finding
// the rule did read, so a read it could not make is recommend-only's even when
// the list it was handed carries a critical finding. That list is the case
// that makes the refusal load-bearing — a readable one with the same critical
// finding escalates — and the merge gate is asked after it, so an escalate
// that fired here would also be a cancel (D1247).
func TestTriageNeverEscalatesOnFindingsItCouldNotRead(t *testing.T) {
	critical := orchestrator.Finding{Severity: orchestrator.SeverityCritical, Path: ".github/workflows/ci.yml", Key: "k-critical"}

	for _, tc := range []struct {
		name   string
		read   orchestrator.FindingsRead
		list   []orchestrator.Finding
		reason orchestrator.TriageReason
	}{
		{
			name:   "a critical finding beside one the rule cannot score",
			read:   orchestrator.FindingsReadable,
			list:   []orchestrator.Finding{critical, {Severity: "urgent", Path: triageRoutine, Key: "k-unknown"}},
			reason: orchestrator.ReasonUnknownSeverity,
		},
		{
			name:   "a list handed beside a read state that says there is no file",
			read:   orchestrator.FindingsAbsent,
			list:   []orchestrator.Finding{critical},
			reason: orchestrator.ReasonFindingsAbsent,
		},
		{
			name:   "a read state this release does not know",
			read:   "half-read",
			list:   []orchestrator.Finding{critical},
			reason: orchestrator.ReasonFindingsUnreadable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := mergeable()
			in.Findings = orchestrator.Findings{Read: tc.read, List: tc.list}

			got := orchestrator.Triage(in)
			if got.Kind != orchestrator.RecommendOnly {
				t.Fatalf("findings the rule could not read answered %q: %+v", got.Kind, got)
			}
			if !slices.Contains(got.Reasons, tc.reason) {
				t.Errorf("the reasons do not name the read: %v, want %s", got.Reasons, tc.reason)
			}
			if got.Unreadable != orchestrator.InputFindings {
				t.Errorf("the outcome names %q as the unreadable input, want %q", got.Unreadable, orchestrator.InputFindings)
			}
		})
	}
}

// TestTriageReadsAStallOnlyWhileTheRunIsInFlight: the no-progress signal is
// the one a stopped run cannot have, so the same stalled record is a restart
// while the run is going and never one once it has finished — the gate's
// refusal is what a stopped run's answer is. The CI is red because the merge
// is tried before restart: with the whole of D1245's evidence in place the
// merge answers first and restart is never asked, which is what left the guard
// unpinned (D1244, D1246).
func TestTriageReadsAStallOnlyWhileTheRunIsInFlight(t *testing.T) {
	in := mergeable()
	in.CI = gitprov.CIRed
	in.Progress = &orchestrator.Progress{Stage: in.Record.Stage, Head: in.Record.HeadSHA, PushedHead: in.Record.PushedHead}

	in.Record.Status = runstore.StatusRunning
	got := orchestrator.Triage(in)
	if got.Kind != orchestrator.Restart || !slices.Contains(got.Reasons, orchestrator.ReasonNoProgress) {
		t.Fatalf("a stalled run in flight answered %q (%v), want restart %s", got.Kind, got.Reasons, orchestrator.ReasonNoProgress)
	}
	if !got.WithoutAPerson {
		t.Errorf("a restart cancels the run without asking: %+v", got)
	}

	in.Record.Status = runstore.StatusSucceeded
	got = orchestrator.Triage(in)
	if got.Kind == orchestrator.Restart {
		t.Errorf("a stalled run that is over was restarted: %+v", got)
	}
	if !slices.Contains(got.Reasons, orchestrator.ReasonMergeRefused) {
		t.Errorf("a run the gate refuses answers %v, want %s", got.Reasons, orchestrator.ReasonMergeRefused)
	}
}

// TestTriageRestartsOnEitherHalfOfNoCommits: the no-commits signal has two
// halves, and the fixture that clears both at once pins neither, so each is
// broken here on its own: a record that pushed nothing, and one whose changed
// path set is empty. Either is a run whose work is not in the repository, so
// the gate that lands work refuses it and restart is what answers — the pair
// is why the merged-as-is gate is asked after this one's reasons (D1245,
// D1246).
func TestTriageRestartsOnEitherHalfOfNoCommits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(in *orchestrator.TriageInput)
	}{
		{
			name:   "no pushed head",
			break_: func(in *orchestrator.TriageInput) { in.Record.PushedHead = "" },
		},
		{
			name: "no changed path",
			break_: func(in *orchestrator.TriageInput) {
				in.Record.ChangedFiles = &runstore.ChangedFiles{Paths: []string{}, Total: 0}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := mergeable()
			tc.break_(&in)

			got := orchestrator.Triage(in)
			if got.Kind != orchestrator.Restart {
				t.Fatalf("a run that committed nothing answered %q: %+v", got.Kind, got)
			}
			if !slices.Contains(got.Reasons, orchestrator.ReasonNoCommits) {
				t.Errorf("the reason does not name the commits: %v", got.Reasons)
			}
		})
	}
}

// TestTriageAuditIDIsRunColonTriage: the id is derived from the run and the
// transition, so a repeated tick appends one line and not one per tick (D1247,
// O3).
func TestTriageAuditIDIsRunColonTriage(t *testing.T) {
	if got, want := orchestrator.TriageAuditID(triageRun), triageRun+":"+orchestrator.TriageTransition; got != want {
		t.Errorf("TriageAuditID(%q) = %q, and the log's id is %q", triageRun, got, want)
	}
	if got, want := orchestrator.TriageAuditID(triageRun), triageRun+":triage"; got != want {
		t.Errorf("TriageAuditID(%q) = %q, and D1247 fixes the id at %q", triageRun, got, want)
	}
}

// TestTriageMergeGateHoldsOnItsOwn: the merge gate is asked directly, because
// the rule tries the fail-closed outcome before it and a gate that leaned on
// that order would merge on an input nobody read the moment the order changed
// (D1245, G12).
func TestTriageMergeGateHoldsOnItsOwn(t *testing.T) {
	if _, ok := orchestrator.MergeAsIsGate(mergeable()); !ok {
		t.Fatal("the gate refused the whole of D1245's evidence")
	}

	cases := []struct {
		name   string
		break_ func(*orchestrator.TriageInput)
	}{
		{name: "an unread CI answer", break_: func(in *orchestrator.TriageInput) { in.CI = gitprov.CIUnknown }},
		{name: "a red CI", break_: func(in *orchestrator.TriageInput) { in.CI = gitprov.CIRed }},
		{name: "no pull request", break_: func(in *orchestrator.TriageInput) { in.PR = nil }},
		{name: "a draft pull request", break_: func(in *orchestrator.TriageInput) { in.PR.Draft = true }},
		{name: "a run still in flight", break_: func(in *orchestrator.TriageInput) { in.Record.Status = runstore.StatusRunning }},
		{name: "a record nobody read", break_: func(in *orchestrator.TriageInput) { in.Record = nil }},
		{name: "a status this release does not know", break_: func(in *orchestrator.TriageInput) { in.Record.Status = "halfway" }},
		{name: "no pushed head", break_: func(in *orchestrator.TriageInput) { in.Record.PushedHead = "" }},
		{name: "no changed path at all", break_: func(in *orchestrator.TriageInput) { in.Record.ChangedFiles = &runstore.ChangedFiles{} }},
		{name: "no changed-path set", break_: func(in *orchestrator.TriageInput) { in.Record.ChangedFiles = nil }},
		{name: "a truncated changed-path set", break_: func(in *orchestrator.TriageInput) { in.Record.ChangedFiles.Truncated = true }},
		{name: "a path outside the routine allowlist", break_: func(in *orchestrator.TriageInput) {
			in.Record.ChangedFiles = &runstore.ChangedFiles{Paths: []string{".github/workflows/ci.yml"}, Total: 1}
		}},
		{name: "a findings file nobody could read", break_: func(in *orchestrator.TriageInput) {
			in.Findings = orchestrator.Findings{Read: orchestrator.FindingsAbsent}
		}},
		{name: "an unattributable findings file", break_: func(in *orchestrator.TriageInput) {
			in.Findings = orchestrator.Findings{Read: orchestrator.FindingsUnattributable}
		}},
		{name: "a severity nobody knows", break_: func(in *orchestrator.TriageInput) {
			in.Findings = findingsOf(orchestrator.Finding{Severity: "urgent", Path: triageRoutine, Key: "k1"})
		}},
		{name: "a critical finding", break_: func(in *orchestrator.TriageInput) {
			in.Findings = findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityCritical, Path: triageRoutine, Key: "k1"})
		}},
		{name: "a high finding in a security path", break_: func(in *orchestrator.TriageInput) {
			in.Findings = findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityHigh, Path: "internal/creds/store.go", Key: "k1"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := mergeable()
			tc.break_(&in)
			if out, ok := orchestrator.MergeAsIsGate(in); ok {
				t.Fatalf("the gate merged on %s: %+v", tc.name, out)
			}
		})
	}
}

// TestTriageTriesTheOutcomesInTheFixedOrder: D1244's order is the rule, and it
// is what decides a run two of the readings cover. Each case names the
// outcomes whose own reading the input satisfies; the rule must answer the
// earliest of them, and the four must all appear.
func TestTriageTriesTheOutcomesInTheFixedOrder(t *testing.T) {
	order := []orchestrator.TriageKind{
		orchestrator.Escalate, orchestrator.RecommendOnly, orchestrator.MergeAsIs, orchestrator.Restart,
	}
	if got := orchestrator.TriageOrder(); !slices.Equal(got, order) {
		t.Fatalf("the rule tries the outcomes in %v, and D1244 fixes the order at %v", got, order)
	}

	critical := findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityCritical, Path: triageRoutine, Key: "k1"})
	looping := findingsOf(orchestrator.Finding{Severity: orchestrator.SeverityMedium, Path: triageRoutine, Key: "loop-key"})
	absent := orchestrator.Findings{Read: orchestrator.FindingsAbsent}

	restartable := func() orchestrator.TriageInput {
		in := mergeable()
		in.Record.PushedHead = ""
		in.Record.ChangedFiles = &runstore.ChangedFiles{Paths: []string{}, Total: 0}
		return in
	}

	cases := []struct {
		name    string
		in      func() orchestrator.TriageInput
		matches []orchestrator.TriageKind
	}{
		{
			name: "a critical finding on a run that would also merge",
			in: func() orchestrator.TriageInput {
				in := mergeable()
				in.Findings = critical
				return in
			},
			matches: []orchestrator.TriageKind{orchestrator.Escalate, orchestrator.MergeAsIs},
		},
		{
			name: "a critical finding on a run whose CI nobody read",
			in: func() orchestrator.TriageInput {
				in := mergeable()
				in.Findings = critical
				in.CI = gitprov.CIUnknown
				return in
			},
			matches: []orchestrator.TriageKind{orchestrator.Escalate, orchestrator.RecommendOnly},
		},
		{
			name: "an absent findings file on a run that would also merge",
			in: func() orchestrator.TriageInput {
				in := mergeable()
				in.Findings = absent
				return in
			},
			matches: []orchestrator.TriageKind{orchestrator.RecommendOnly, orchestrator.MergeAsIs},
		},
		{
			name: "an absent findings file on a run that would also restart",
			in: func() orchestrator.TriageInput {
				in := restartable()
				in.Findings = absent
				return in
			},
			matches: []orchestrator.TriageKind{orchestrator.RecommendOnly, orchestrator.Restart},
		},
		{
			name: "a looping finding on a run that would also merge",
			in: func() orchestrator.TriageInput {
				in := mergeable()
				in.Record.Triage = runstore.NewTriage([]runstore.TriageRound{round(1, "loop-key"), round(2, "loop-key")})
				in.Findings = looping
				return in
			},
			matches: []orchestrator.TriageKind{orchestrator.MergeAsIs, orchestrator.Restart},
		},
		{
			name:    "a run with every input readable that would merge",
			in:      mergeable,
			matches: []orchestrator.TriageKind{orchestrator.MergeAsIs},
		},
		{
			name:    "a run with every input readable that pushed nothing",
			in:      restartable,
			matches: []orchestrator.TriageKind{orchestrator.Restart},
		},
	}

	seen := map[orchestrator.TriageKind]bool{}
	overlapping := 0
	for _, tc := range cases {
		for _, k := range tc.matches {
			seen[k] = true
		}
		if len(tc.matches) > 1 {
			overlapping++
		}
		want := tc.matches[0]
		for _, k := range order {
			if slices.Contains(tc.matches, k) {
				want = k
				break
			}
		}
		if got := orchestrator.Triage(tc.in()); got.Kind != want {
			t.Errorf("%s answered %q, and the first of %v it matches is %q", tc.name, got.Kind, tc.matches, want)
		}
	}
	for _, k := range order {
		if !seen[k] {
			t.Errorf("no case reads %q, so the table pins no order over it", k)
		}
	}
	if overlapping < 2 {
		t.Errorf("the table holds %d inputs that match more than one outcome, and the order is only tested by those", overlapping)
	}
}
