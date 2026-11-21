package cli

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/settings"
)

// settingsMetaSingleWords is the longest a label may run (spec §4): a label is
// the name a row is drawn under in the accordion, not a sentence, and the
// plan's four backfills are each held to six words.
const settingsMetaSingleWords = 6

// settingsMetaSingleNamed are the family's standalone keys: SM4's twelve beside
// `budget.`'s block. The budget keys are not listed because the plan names them
// as a block: they are read from the registry below, so a `budget.` row the
// allowlist gains is a key these tests measure rather than one they miss.
var settingsMetaSingleNamed = []string{
	"bake_repos",
	"base_branch",
	"max_parallel",
	"update_check",
	"commit_name",
	"commit_email",
	"default_claude_account",
	"claude_account_auto",
	"subscription_spread",
	"triage",
	"verifier_accounts",
	"planning.decoupling",
}

// settingsMetaSingleKeys is the whole family: the names above, and every
// `budget.` registry row the generic write path allows. The two halves are
// checked against the allowlist and the registry as they are collected, so a
// key the tree has moved away from — a row renamed, or a name this file kept
// after `settingsGenericKeys` dropped it — fails here rather than passing as a
// key nothing writes.
func settingsMetaSingleKeys(t *testing.T) []string {
	t.Helper()
	keys := slices.Clone(settingsMetaSingleNamed)
	for _, r := range settings.Rows() {
		if strings.HasPrefix(r.Key, "budget.") && settingsGenericKey(r.Key) {
			keys = append(keys, r.Key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		if !settingsGenericKey(key) {
			t.Errorf("%s is in the family this file covers and the generic write path does not allow it, so no write reaches it: a family's list moves with settingsGenericKeys", key)
		}
		if _, ok := settings.Lookup(key); !ok {
			t.Errorf("%s is in the family this file covers and the registry holds no row for it", key)
		}
	}
	return keys
}

// TestSettingsMetaSingleComplete is the completeness half of SM4: every key of
// the family carries a label a person reads, a description of one sentence, a
// group of the order and a type of the vocabulary, and the type's own shape is
// true of what the entry carries — an enum holds the values it names, a bound
// is spelled the way its type is read.
func TestSettingsMetaSingleComplete(t *testing.T) {
	keys := settingsMetaSingleKeys(t)
	if len(keys) == 0 {
		t.Fatal("the family is empty, and the twelve names and the budget block are its whole")
	}
	for _, key := range keys {
		m, ok := settings.MetaOf(key)
		if !ok {
			t.Errorf("%s carries no metadata, and every writable key shows a label, a description, a group and a type (D2908)", key)
			continue
		}
		if n := len(strings.Fields(m.Label)); n == 0 || n > settingsMetaSingleWords {
			t.Errorf("%s's label is %q, which is %d words: a label is at most %d", key, m.Label, n, settingsMetaSingleWords)
		}
		if !strings.HasSuffix(m.Description, ".") || strings.Contains(m.Description, ". ") {
			t.Errorf("%s's description is %q: a description is one sentence, so it ends in a full stop and holds no sentence break inside", key, m.Description)
		}
		if !slices.Contains(settings.MetaGroups, m.Group) {
			t.Errorf("%s's group is %q, which is not one of the group order %v", key, m.Group, settings.MetaGroups)
		}
		if !slices.Contains(settings.MetaTypes, m.Type) {
			t.Errorf("%s's type is %q, which is not one of %v", key, m.Type, settings.MetaTypes)
		}
		switch m.Type {
		case settings.TypeEnum:
			if len(m.Enum) == 0 {
				t.Errorf("%s is an enum and names no value", key)
			}
		case settings.TypeInt, settings.TypeFloat, settings.TypeDuration:
			for _, b := range []struct{ name, value string }{{"Min", m.Min}, {"Max", m.Max}} {
				if b.value == "" {
					continue
				}
				if _, err := settingsMetaSingleBound(b.value, m.Type); err != nil {
					t.Errorf("%s's %s is %q, which does not read as a %s: %v", key, b.name, b.value, m.Type, err)
				}
			}
		default:
			if len(m.Enum) > 0 || m.Min != "" || m.Max != "" {
				t.Errorf("%s is a %s and carries an enum %v or a bound %q..%q: a value's own shape belongs to the types that carry one", key, m.Type, m.Enum, m.Min, m.Max)
			}
		}
	}
}

// settingsMetaSingleBound reads a bound the way the console does — by the key's
// type, because a bound is spelled in the value's own words (`0`, `0.5`, `5m`)
// rather than as a number.
func settingsMetaSingleBound(value string, ty settings.MetaType) (any, error) {
	switch ty {
	case settings.TypeInt:
		return strconv.Atoi(value)
	case settings.TypeFloat:
		return strconv.ParseFloat(value, 64)
	case settings.TypeDuration:
		return config.ParseDuration(value)
	}
	return nil, nil
}

// settingsMetaSingleBoundCase is one family key the boundary test writes: how a
// value of it becomes a document, and the values the loader refuses on each
// side of what the key states.
type settingsMetaSingleBoundCase struct {
	key string
	// write builds the document one value is written into, with the target
	// settingsReparse reads it as. The target's path is never opened: the
	// reparse is handed the bytes, and the path is what a refusal prints.
	write func(value string) (settingsTarget, []byte)
	// refusedBelow and refusedAbove are one value the loader refuses on that
	// side of Min and of Max. They are stated rather than computed because a
	// step outside a bound is not the same step for every key: max_parallel's 0
	// is a value the loader takes (it reads it as the default and fills it), so
	// the figure below its floor that is refused is -1.
	refusedBelow, refusedAbove string
	// outside is one value beside a closed enum.
	outside string
}

// settingsMetaSingleBounds is every key of the family whose value the loader
// decides: the caps and the mode, and the parallel limit.
var settingsMetaSingleBounds = []settingsMetaSingleBoundCase{
	{
		key: "budget.per_run_usd",
		write: func(value string) (settingsTarget, []byte) {
			return settingsMetaSingleLocalDoc("budget:\n  per_run_usd: " + value + "\n")
		},
		refusedBelow: "-1", refusedAbove: "100001",
	},
	{
		// The daily cap is the one member of the block a project config does not
		// carry at all: its layers are the repository's own file alone.
		key: "budget.per_day_usd",
		write: func(value string) (settingsTarget, []byte) {
			return settingsMetaSingleRepoDoc("budget: { per_day_usd: " + value + " }\n")
		},
		refusedBelow: "-1", refusedAbove: "100001",
	},
	{
		key: "budget.max_run_tokens",
		write: func(value string) (settingsTarget, []byte) {
			return settingsMetaSingleLocalDoc("budget:\n  max_run_tokens: " + value + "\n")
		},
		refusedBelow: "-1",
	},
	{
		key: "budget.unreachable_grace",
		write: func(value string) (settingsTarget, []byte) {
			return settingsMetaSingleLocalDoc("budget:\n  unreachable_grace: " + value + "\n")
		},
		refusedBelow: "4s", refusedAbove: "4m",
	},
	{
		// `enforce` is refused without a per-run cap beside it, which is a rule
		// about the pair and not about this value: the document carries the cap
		// so that the mode is the only thing a value here varies.
		key: "budget.mode",
		write: func(value string) (settingsTarget, []byte) {
			return settingsMetaSingleLocalDoc("budget:\n  per_run_usd: 1\n  mode: " + value + "\n")
		},
		outside: "on",
	},
	{
		key: "max_parallel",
		write: func(value string) (settingsTarget, []byte) {
			return settingsMetaSingleLocalDoc("max_parallel: " + value + "\n")
		},
		refusedBelow: "-1",
	},
}

// TestSettingsMetaSingleBoundariesAreAccepted holds every bound and every enum
// value the family states against the strict decode, which is the only reader
// that decides: each boundary is written into a document of a layer the key may
// be set at and settingsReparse is asked to take it, and the values named as
// outside the stated range are asked to be refused. A bound stated wider or
// narrower than the loader's own is what this fails on — the console validates
// against the metadata before OK, so a value it refuses is a write a person
// cannot make from the panel, and one it lets through is a refusal after OK.
func TestSettingsMetaSingleBoundariesAreAccepted(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range settingsMetaSingleBounds {
		covered[c.key] = true
		m, ok := settings.MetaOf(c.key)
		if !ok {
			t.Errorf("%s is written by the boundary test and carries no metadata", c.key)
			continue
		}
		for _, value := range m.Enum {
			settingsMetaSingleReparse(t, c, value, true)
		}
		if c.outside != "" {
			settingsMetaSingleReparse(t, c, c.outside, false)
		}
		for _, b := range []struct{ name, value, refused string }{
			{name: "Min", value: m.Min, refused: c.refusedBelow},
			{name: "Max", value: m.Max, refused: c.refusedAbove},
		} {
			if b.value == "" {
				continue
			}
			settingsMetaSingleReparse(t, c, b.value, true)
			if b.refused != "" {
				settingsMetaSingleReparse(t, c, b.refused, false)
			}
		}
	}
	// Every value the loader decides must be proven here: an enum or a bound on
	// a key the table does not carry is one no test holds against the strict
	// decode, which is what the acceptance asks this test to be.
	for _, key := range settingsMetaSingleKeys(t) {
		m, ok := settings.MetaOf(key)
		if !ok || (len(m.Enum) == 0 && m.Min == "" && m.Max == "") {
			continue
		}
		if !covered[key] {
			t.Errorf("%s states a %s value no document here writes: add it to settingsMetaSingleBounds", key, m.Type)
		}
	}
}

// settingsMetaSingleReparse writes value for c's key and holds settingsReparse
// to want it or to refuse it.
func settingsMetaSingleReparse(t *testing.T, c settingsMetaSingleBoundCase, value string, want bool) {
	t.Helper()
	target, doc := c.write(value)
	err := settingsReparse(target, doc)
	if want && err != nil {
		t.Errorf("settingsReparse refused %s = %s, and the metadata states it as a value the key takes: %v", c.key, value, err)
	}
	if !want && err == nil {
		t.Errorf("settingsReparse took %s = %s, and the metadata names it as outside the key's own range", c.key, value)
	}
}

// settingsMetaSingleLocalDoc is the local layer's document: the smallest
// project config localcfg.Parse takes, plus the line the caller writes. The
// value's own block is written the way the write path writes it — YAML at the
// document's top level — because that is what settingsReparse is handed.
func settingsMetaSingleLocalDoc(line string) (settingsTarget, []byte) {
	const head = "version: 1\nname: aurora\ngcp_project: aurora-gcp\nregion: us-east5\nruns_bucket: fugaro-runs-aurora-gcp\n"
	return settingsTarget{layer: settingsLayerLocal, kind: settingsDocLocal, path: "aurora.yaml", project: "aurora"},
		[]byte(head + line)
}

// settingsMetaSingleRepoDoc is a repository's own fugaro.yaml, built from the
// fixture the repository-layer write tests use: settingsRepoYAML is the
// smallest file config.Resolve accepts, and the caller's extra is appended to
// it.
func settingsMetaSingleRepoDoc(extra string) (settingsTarget, []byte) {
	return settingsTarget{layer: settingsLayerRepo, kind: settingsDocRepo, path: "fugaro.yaml", project: "aurora"},
		[]byte(settingsRepoYAML(extra))
}
