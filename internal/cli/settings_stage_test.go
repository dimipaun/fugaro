package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/settings"
)

// settings_stage_test.go is W2 of docs/config-ownership: the governance block's
// per-stage rows, and the two fences in front of them. The block's leaves are
// rows at the project and repository layers (registry.go, registry_stage_test.go
// holds the table to config.Governance), so the owner's own spelling
// `governance.stage_timeout.implement` is a setting a person writes — where
// before this the only concrete spelling was the layer's own
// `layer.governance.stage_timeout.implement` and the short one answered "no
// setting has that name" (F1).
//
// What these tests pin is the fence, not the table. A governance key is a
// ceiling (settingsCeilingKey), so a write that raises one takes the owner's own
// hand: a first write into a project that states no figure for it is a raise
// (Fugaro's own defaults are what a run gets today), a repository's own file
// may only tighten, and a coding agent's session is refused the raise whichever
// of the two files it would edit.

// TestStageTimeoutProjectDefaultReachesTheRun: `settings set
// governance.stage_timeout.implement 1h` with no --layer lands in the project
// layer file — the block is a top-level key of it and the project layer is the
// one layer that may loosen a guardrail (D2604, D2605) — and the file it wrote
// is the ceiling a run starts under: the value the loader reads back is the one
// the runner's own bootstrap puts in the ceiling (internal/runner/policy.go:
// ceiling.Governance = l.Governance.Policy().WithDefaults()).
func TestStageTimeoutProjectDefaultReachesTheRun(t *testing.T) {
	settingsWriteHome(t)
	layerPath := settingsWriteLayerFile(t, "")
	repoPath := settingsWriteCheckout(t, settingsRepoYAML(""))
	noTerminal(t)
	seed := readFileString(t, repoPath)

	if out, _, err := execute(t, "settings", "set", "governance.stage_timeout.implement", "1h"); err != nil {
		t.Fatalf("the owner's own spelling of a stage timeout was refused: %v\n%s", err, out)
	}
	got := readFileString(t, layerPath)
	if !strings.Contains(got, "governance:") || !strings.Contains(got, "implement: 1h") {
		t.Errorf("the write did not land in the project layer at the key's own path:\n%s", got)
	}
	if readFileString(t, repoPath) != seed {
		t.Errorf("the write reached the repository's own file, which a project-layer default must not:\n%s", readFileString(t, repoPath))
	}

	l, ps := config.ParseProjectLayer([]byte(got), config.LayerAnchor{Project: "aurora", GCPProject: "aurora-gcp"})
	if len(ps) > 0 {
		t.Fatalf("the layer file the write produced does not read back: %s\n%s", ps[0].String(), got)
	}
	if l.Governance == nil {
		t.Fatalf("the layer file reads back with no governance block:\n%s", got)
	}
	if !l.Governance.StageTimeout.Implement.Set || l.Governance.StageTimeout.Implement.Duration != time.Hour {
		t.Errorf("governance.stage_timeout.implement reads back as %+v, want 1h:\n%s", l.Governance.StageTimeout.Implement, got)
	}

	// The run's own resolution, in the runner's own order: the layer's block is
	// the ceiling, and Fugaro's defaults fill what it leaves unset. A key the
	// layer states is kept exactly as written (a stage timeout ships no default
	// at all: a stage with none of its own is bounded by the run's workflow, so
	// what the layer writes here is the whole of the bound).
	gov := l.Governance.Policy().WithDefaults()
	if gov.StageTimeout.Implement != time.Hour {
		t.Errorf("the run's ceiling bounds the implement stage at %v, and the layer file states 1h", gov.StageTimeout.Implement)
	}
}

// TestStageTimeoutRepoMayOnlyTighten: a repository's own file may carry the
// same key, and it may only tighten it. The write needs --layer repo (with no
// --layer the key is a ceiling and lands in the project layer, D2605), and in
// the session a repository's file is written from — a coding agent's, which may
// edit the file it could edit anyway — a value looser than the one the file
// holds is refused while a tighter one lands.
//
// The comparison is the fence's own (settingsLoosens, the duration branch): the
// file states 2h, so 3h is the raise and 1h is the tightening. The two are one
// pair of writes apart, which is what makes the fence's answer readable rather
// than the file's refusal to parse.
func TestStageTimeoutRepoMayOnlyTighten(t *testing.T) {
	settingsWriteHome(t)
	layerPath := settingsWriteLayerFile(t, "")
	repoPath := settingsWriteCheckout(t, settingsRepoYAML("governance:\n  stage_timeout:\n    implement: 2h\n"))
	agentSession(t)

	if out, _, err := execute(t, "settings", "set", "governance.stage_timeout.implement", "3h", "--layer", "repo"); err == nil {
		t.Fatalf("a repository's file raised a stage timeout the project layer owns:\n%s", out)
	} else if !strings.Contains(err.Error(), "coding agent's session") {
		t.Errorf("the loosening was refused, and not by the session fence: %v", err)
	}
	if got := readFileString(t, repoPath); !strings.Contains(got, "implement: 2h") {
		t.Errorf("the refused write changed the repository's file:\n%s", got)
	}
	if got := readFileString(t, layerPath); strings.Contains(got, "stage_timeout") {
		t.Errorf("the --layer repo write landed in the project layer:\n%s", got)
	}

	if out, _, err := execute(t, "settings", "set", "governance.stage_timeout.implement", "1h", "--layer", "repo"); err != nil {
		t.Fatalf("a repository tightened the project's stage timeout and was refused: %v\n%s", err, out)
	}
	if got := readFileString(t, repoPath); !strings.Contains(got, "implement: 1h") {
		t.Errorf("the tightening did not land in the repository's file:\n%s", got)
	}

	// And the --layer the key would take on its own. In a coding agent's
	// session the project layer's spelling of a governance key is refused
	// outright — `governance.*` is one of the blocks no session writes, which
	// is the gate's own refusal and not this fence's — so the pair is read at a
	// person's terminal, where a ceiling with a project layer lands in it
	// (D2605) and the checkout's file is left as --layer repo left it.
	noAgentSession(t)
	noTerminal(t)
	if out, _, err := execute(t, "settings", "set", "governance.stage_timeout.implement", "30m"); err != nil {
		t.Fatalf("the same key with no --layer was refused at a person's terminal: %v\n%s", err, out)
	}
	if got := readFileString(t, layerPath); !strings.Contains(got, "implement: 30m") {
		t.Errorf("the write with no --layer did not land in the project layer:\n%s", got)
	}
	if got := readFileString(t, repoPath); !strings.Contains(got, "implement: 1h") {
		t.Errorf("the write with no --layer reached the repository's file, which --layer repo is for:\n%s", got)
	}
}

// TestStageTimeoutRaiseRefusedInAnAgentSession: the fence W2 teaches the
// built-in figures to. The project states no timeout for the implement stage,
// so what a run gets today is Fugaro's own — no bound of its own, which is a
// state and not a number (settingsGovernanceBuiltIn,
// settingsCeilingUnset) — and a first write over it is a raise nobody can prove
// tightens. In a coding agent's session that is the typed widening D1026 keeps
// from every gate, so the refusal is the managed gate's own (ErrNotConverged)
// and names the typed class rather than the class of keys an agent's session
// never writes: remove the fence and the same write is refused for the other
// reason, which is what makes this test the fence's.
//
// The rig is W1's: a managed project — both marks — so the blanket refusal an
// unmanaged installation gives every project-layer write is not what answers,
// and the refusal can only be about this value.
func TestStageTimeoutRaiseRefusedInAnAgentSession(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	path := settingsManagedLayerFile(t, minimalAnchored)
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "settings", "set", "governance.stage_timeout.implement", "1h")
	if err == nil {
		t.Fatalf("a stage timeout was raised from unset in a coding agent's session:\n%s", out)
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the raise was not refused by the gate, so nothing says which control refused it: %v", err)
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed class, so the write was not read as a raise over Fugaro's own figure: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the file changed under a refused raise")
	}
	recs := r.audits(t)
	if len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the refusal left %+v, want exactly one refused record", recs)
	}
	if !strings.Contains(strings.Join(recs[0].Rules, " "), "typed confirmation") {
		t.Errorf("the refused record's rules (%v) do not name the typed class", recs[0].Rules)
	}
}

// TestNoBlockRowIsListedWritableThatCannotBeWritten: the read offers an edit
// exactly where the write takes one (D1102), so a row the write refuses prints
// `writable: false` and no owner. A block is the row that pair was wrong for:
// `governance.stage_timeout` is a mapping of three stages and has no value of
// its own, and writing a scalar at its spelling lands a string where the
// loader reads a block — a refusal about the reader rather than about the key
// (D2604). The row stays in the read, because a person reading `fugaro settings`
// sees the block and the settings it holds, and the schema's keyDefaults keeps
// its member (a field never changes meaning).
func TestNoBlockRowIsListedWritableThatCannotBeWritten(t *testing.T) {
	settingsWriteHome(t)
	rows := readSettingsWritability(t)
	generic := settingsOwnerCommands[settingsOwnerDefaultToken]

	blocks := 0
	for _, row := range settings.Rows() {
		if !settings.IsBlock(row.Key) {
			continue
		}
		blocks++
		if _, err := settingsWriteRow(row.Key); err == nil {
			t.Errorf("%s: the generic path writes the block's own spelling", row.Key)
		}
		got := writableRowOf(t, rows, row.Key, "project")
		if got.Writable == nil {
			t.Errorf("%s: no writable member, and the read writes one on every row", row.Key)
			continue
		}
		if *got.Writable {
			t.Errorf("%s: writable = true, and the generic path refuses it", row.Key)
		}
		if got.Owner != nil && *got.Owner == generic {
			t.Errorf("%s: writable = false and owner = %q, and the console draws that as `written by %s, not by the generic path` (D1178)", row.Key, *got.Owner, generic)
		}
	}
	if blocks == 0 {
		t.Fatal("the registry holds no block row, and the four governance blocks are four")
	}

	// The other half, and the reason the blocks stay: what a person writes is a
	// leaf, and a leaf of the stage blocks is a row the generic path takes at
	// both layers it has.
	for _, key := range []string{
		"governance.stage_timeout.implement",
		"governance.stage_budget.review",
		"governance.stage_tokens.fix",
		"governance.tiers.small.expected_usd",
	} {
		got := writableRowOf(t, rows, key, "project")
		if got.Writable == nil || !*got.Writable {
			t.Errorf("%s: writable = %v, and settings set writes the leaf (F1)", key, got.Writable)
		}
		if got.Owner == nil || *got.Owner != generic {
			t.Errorf("%s: owner = %v, want %q: the block has no command family of its own (D2604)", key, got.Owner, generic)
		}
	}
}
