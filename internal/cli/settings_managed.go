package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The managed gate as `fugaro settings set` and `fugaro settings unset` read it
// (the managed plan's §2 settings handoff row, D1012, D1018).
//
// Why these two commands call the gate at all. What they write is this
// machine's own file — the local project config, the local project layer file,
// or a repository's fugaro.yaml — and the write path has always refused a
// coding agent's session everything outside the repository (SD9, S4.2 step 4):
// `--layer local` and `--layer project` write the config home, and a typed edit
// is refused even at `--layer repo`. In a managed project the project's own
// gate answers that question per step instead, so a session that writes a
// registry key whose effect the document shows is admitted, and the owner gets
// a record of which key was written, by which rules, and against which bytes.
//
// Which rules are this command's own, and which it deliberately does not
// restate. The registry (spec §4.4) and the layer a key may be set in are the
// command's own checks, asked before the gate is (settingsWriteRow,
// settingsResolveTarget), and they are named among the rules that passed
// because a record has to say what was judged. The reserved keys are the gate's
// own rule, in config publish's own words.
//
// Two rules that could not fire here, and are therefore absent rather than
// restated. (1) The rigor floors and `review_floor`: those rows belong to
// another family (D853), so settingsWriteRow refuses them before any document
// exists. (2) The widening of a cap: `budget.*` and `governance.*` are the
// reserved runs, refused whole.
//
// The executable keys are NOT one of those, and the earlier claim that they
// were was wrong: the project layer's profiles carry them
// (`layer.profiles.*.commands.*`, `layer.profiles.*.image.apt`,
// `layer.profiles.*.image.setup`), and a `--layer project` write is exactly a
// document this gate judges. So spec §4.2's executable-key guard belongs here,
// and settingsProfilesRule is it: every key of the project layer's profiles
// block is refused, whatever the command line says, because a profile is where
// a build script, an apt package or a command lives. The allowlist below
// refuses those keys too — they are not on it — and that is not a reason to
// drop the rule: a session that tries one reads the reason spec §4.2 gives
// rather than "nobody decided about this key", and the record an owner audits
// says which door refused it. The two rules are one refusal with one reason
// each, which is what the plan's §2 row carried before this gate existed.
//
// A rule named among the ones that passed is a claim that it ran and could
// have failed, and an owner auditing a record reads it as one — so a rule that
// can never fire is worse than an absent one, which is the reason M3 gives for
// the same omission on its own path.
//
// What this gate admits is an ALLOWLIST and not a list of things to refuse
// (settingsManagedKeys, the owner decision D1027). The rules below judge what
// the allowlist names; in a coding agent's session every other key is refused
// by settingsAdmittedRefusal, whether it is in the registry today or is added
// to it by a later release. A person at a real terminal is not held to the
// allowlist (the owner's ruling of 2026-11-06): the other rules still judge
// that person's write.
// The direction is the design's own (§4.1 rule 2, and D1021's "a caller that
// reads nothing refuses"): a key nobody decided about is refused rather than
// admitted by an omission, and the two keys on the list are the ones whose
// write cannot weaken a run — the review rounds, and only upward
// (settingsFloorRule).
//
// Which config the questions are asked about is part of the boundary too.
// settingsManagedStep admits nothing unless the selected config is this
// installation's own file (`localcfg.ProjectPath`): a session that aims
// `--config` at a file it wrote would otherwise choose the installation whose
// marks are read, and the gcpProject a mark read is asked about may never come
// from content the session can write (spec §1, spec §7).
//
// Why the class the gate judges under is YesOrTyped and not the edit's own.
// The edit's Typed form is what the confirmation asks a PERSON for, and it
// stays the fence's (settingsEdit.editClass): nothing here touches it. What the
// gate adds is the refusal the session reads instead of the marker's own — and
// the typed edit is refused BY A RULE of the gate (settingsOwnerOnlyRule), so
// the session reads which declaration it tried to move rather than "run this
// yourself". Both halves hold at once: an edit the typed confirmation reserves
// is refused by the gate, and an edit the gate admitted without noticing the
// typed form would still be refused by the fence, because the fence decides
// `Typed` on its own (D1026's rule is asked there and is not restated here).
// No Typed edit can therefore be admitted by any path.
//
// Why a repository-scoped ordinary write reads nothing in a coding agent's
// session. Today's fence does not apply inside a repository (`--layer repo`
// writes a file an agent could edit anyway, unless the edit is the typed one),
// so there is no refusal for the gate to stand in for and no mark is read: the
// ordinary repository edit keeps exactly the behaviour it has, pays for
// nothing, and cannot be turned into a refusal by a transient Storage or
// Resource Manager failure (M6's review of 2026-11-03, item 2).
//
// Why the OWNER's own terminal reads now, where it read nothing before. The
// gate's second job is the owner's ruling of 2026-11-01 — a managed project
// takes no confirmation prompt, which is the whole point of a managed project —
// and the write's own y/N was the question the gate stood in for. That y/N is
// gone (D1540) and the terminal it was asked at is not, so the read is made
// where the write is the owner's own: a real terminal
// (settingsWriteOwnTerminal), which is the condition the prompt had. A run with
// no terminal is refused before the file is touched and has no write for the
// gate to answer for, so it pays for no read either; --ask does not narrow this
// — the flag asks the confirmations of a run that has already been admitted,
// and the gate's verdict is read before it. The aim is the write's own
// condition, asked once, in settingsGateAsks.

// settingsGateMarker is the coding agent's session this command was run in, ""
// for a person at a terminal. It is read once per command, here, so the two
// places that ask "is the gate this command's business" cannot disagree.
func settingsGateMarker(terminal bool) string { return agentMarker(os.Getenv, terminal) }

// settingsWriteOwnTerminal is the caller's half of whether the gate is read for
// this write: a real terminal. It was `settingsAsk` — "a real terminal and no
// --yes" — while the write's own y/N was the question the gate stood in for;
// that y/N is gone (D1540), so the terminal is the whole of the condition and
// --yes no longer narrows it. It is read from the command the caller is already
// holding (its own stdin), so the two answers cannot disagree.
func settingsWriteOwnTerminal(cmd *cobra.Command) bool {
	return stdinIsTerminal(cmd.InOrStdin())
}

// settingsGateAsks reports whether one edit is one this command would ask a
// confirmation for — the coding agent's refusal SD9 makes, or the y/N a person
// at a terminal is asked. applySettingsEdit reads exactly this condition, and a
// gate consulted anywhere else would be reading marks for a write nobody would
// have been asked about (D1025).
func settingsGateAsks(marker string, asks bool, kind settingsDocKind, typed bool) bool {
	if marker != "" {
		return kind != settingsDocRepo || typed
	}
	return asks
}

// settingsConfigIsTheProjects reports whether one settings write's selected
// config is the project's own file — the path this installation derives from
// its config home, and not a path the command line aimed somewhere else.
//
// Why a path and not the config's content. Everything this gate reads hangs
// off the selected config: its gcp_project is the project the marks are read
// about, and its name is the layer file the write lands in. Both `--config`
// and $FUGARO_CONFIG let the caller name that file, and a coding agent's
// session can write a file inside the repository it is working in — so a
// session-chosen path is a session-chosen project, and the read would be asked
// about an installation the content of the task picked (spec §1: the read's
// verdict rests entirely on the gcpProject it is handed, which every caller
// takes from the installation's own trusted configuration; spec §7: never from
// a file in the repository).
//
// A path that cannot be derived at all — a name that is not a project name, a
// config home that cannot be read — is not the project's own file either, and
// the caller refuses on the same answer rather than on a second one.
//
// The comparison is over the paths made absolute rather than over the two
// strings as they were written, so the file's own spelling with a redundant
// segment (`/x/./aurora.yaml`) is the file it names: a guard that refused the
// right file over how it was typed would refuse an edit a person is entitled to.
// A symlinked or otherwise different spelling is still refused, which is the
// fail-closed direction — this program compares names, and a name it cannot
// prove is the project's own is not the project's own.
//
// What this does NOT decide is which config home is trusted. That path is
// derived from the environment the command runs in (XDG_CONFIG_HOME, else
// HOME), which every selection in this CLI already reads — `$FUGARO_CONFIG` and
// `$FUGARO_PROJECT` are env-derived too — so a session that redirects the whole
// config home selects another installation the same way it can select one with
// `--config`. This guard narrows which FILE inside that home may be the
// installation, which is the half the design's own read can check; the wider
// question (a trusted home for a session) is D1027's recorded residual.
func settingsConfigIsTheProjects(selPath, project string) bool {
	if selPath == "" {
		return false
	}
	own, err := localcfg.ProjectPath(os.Getenv, project)
	if err != nil {
		return false
	}
	picked, err := filepath.Abs(selPath)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(own)
	if err != nil {
		return false
	}
	return picked == target
}

// settingsWriteGate reads the two marks for one settings write and answers the
// gate every command in the set calls. It is M6's ONE helper, read and called
// here, and this function is only the ways of not being able to ask, each of
// which leaves the zero gate behind — a gate that refuses nothing and records
// nothing, so the session keeps this command's own refusal, the sentence it
// read before this row landed.
//
// That is why the error readManagedGate may answer is folded into the zero gate
// here rather than returned: a session that is refused anyway must not learn
// which read broke, which endpoint carries it or what the Storage or Resource
// Manager error said (config_publish_managed.go's own reason for the same
// answer). A no_auth installation that names no storage or Resource Manager
// endpoint has no cloud to read the marks from at all, so no call is attempted.
func settingsWriteGate(ctx context.Context, lc *localcfg.Config, marker, subcommand string) managedGate {
	if lc.Endpoints.NoAuth && (lc.Endpoints.Storage == "" || lc.Endpoints.ResourceManager == "") {
		return managedGate{}
	}
	s, err := storage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Storage)...)
	if err != nil {
		return managedGate{}
	}
	c, err := crm.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.ResourceManager)...)
	if err != nil {
		return managedGate{}
	}
	g, err := readManagedGate(ctx, managedGateEnv{lc: lc, s: s, crm: c}, managedGateFacts{
		Subcommand:  subcommand,
		Argv:        os.Args[1:],
		Declined:    "nothing was written",
		AgentMarker: marker,
		// The whole-project inventory (D59) is `fugaro managed apply`'s read
		// and is not consulted here: a command that has none passes false,
		// which is the documented answer for it (managedGateFacts). This
		// command's gate holds no budget database either, so the ownership
		// question D1017 asks is not asked on this path.
		InventoryFallback: false,
	})
	if err != nil || !g.managed {
		return managedGate{}
	}
	return g
}

// settingsManagedStep is the whole of one command's call of the gate: read,
// judge, admit. It answers the value the step's confirmation is decided under
// (the zero value is "not managed", which keeps today's refusal), the gate
// itself — whose recorded call the caller makes after its write, and whose zero
// value records nothing — and the gate's own refusal, in the gate's own words,
// when the gate refused the document.
//
// It is `accounts add`'s accountsAddGate in this command's own terms, and the
// three answers are the same three for the same reasons. What differs is when
// it is asked at all (settingsGateAsks, read once here, over the caller's own
// settingsWriteOwnTerminal), which document it judges, and that it admits
// nothing unless
// `selPath` is the selected project's own config file
// (settingsConfigIsTheProjects).
func settingsManagedStep(ctx context.Context, lc *localcfg.Config, selPath string, target settingsTarget, key string, typed bool, why string, old, newData []byte, subcommand string, asks bool) (initflow.Managed, managedGate, error) {
	marker := settingsGateMarker(processTerminal())
	if lc == nil || !settingsGateAsks(marker, asks, target.kind, typed) {
		return initflow.Managed{}, managedGate{}, nil
	}
	if !settingsConfigIsTheProjects(selPath, lc.Name) {
		// The config the command selected is not this installation's own file,
		// so the project the marks would be read about is one the session
		// chose. Nothing is read and nothing is recorded: the answer is "not
		// managed", and the session keeps this command's own refusal — the
		// sentence it read before this row landed.
		return initflow.Managed{}, managedGate{}, nil
	}
	gate := settingsWriteGate(ctx, lc, marker, subcommand)
	if !gate.managed {
		// No installation to read, a mark that did not answer or a client that
		// cannot be built: the project is not one this program can prove is
		// managed, so the session keeps this command's refusal and nothing is
		// recorded.
		return initflow.Managed{}, managedGate{}, nil
	}
	conv := settingsManagedVerdict(ctx, &gate, marker, target, key, typed, why, old, newData)
	m, err := gate.admit(ctx, initflow.YesOrTyped, conv)
	if err == nil {
		return m, gate, nil
	}
	if errors.Is(err, managedmode.ErrNotConverged) {
		// The gate's own sentence, whole and unwrapped: the session reads which
		// rule refused, and errors.Is still finds the gate's refusal inside it.
		// It is a user error and not a failure of this program — the session
		// asked for an edit the project's gate does not admit — and the record
		// of the refusal was written by admit, before it returned.
		return m, gate, &ExitError{Code: ExitUserError, Err: err}
	}
	// The second read did not answer, immediately before the write: a mark that
	// cannot be read is not a permission, and the session keeps this command's
	// own refusal.
	return initflow.Managed{}, managedGate{}, nil
}

// settingsManagedVerdict judges the document one settings write would land,
// with this command's rules, and answers the verdict the gate decides the
// session's confirmation under.
//
// The document is the bytes the write would put in the file (D1022's digest is
// taken from them), judged against the bytes the file holds now — and it is the
// SAME read the write is conditioned on: applySettingsEdit re-reads the file and
// refuses to write unless it is still byte for byte what the caller read here,
// so the document judged and the document applied are one artifact rather than
// two that happened to agree at different moments (M6's review, item 3).
func settingsManagedVerdict(ctx context.Context, g *managedGate, marker string, target settingsTarget, key string, typed bool, why string, old, newData []byte) managedmode.Converge {
	var applied, refused []string
	judge := func(rule, refusal string) {
		if refusal == "" {
			applied = append(applied, rule)
			return
		}
		refused = append(refused, refusal)
	}
	judge(settingsRegistryRule, "")
	judge(settingsLayerRule, "")
	if marker != "" {
		// The allowlist limits what a coding agent's session writes on its own
		// (D1027). A person at a real terminal (no marker: agentMarker) is the
		// project's owner, whom the 2026-11-01 ruling says a managed project
		// does not make hand-edit what a command can write; the rules below
		// still apply to that person's write.
		judge(settingsAdmittedRule, settingsAdmittedRefusal(key))
	}
	if target.kind == settingsDocLayer {
		// The two rules below are the project layer's own: `layer.profiles.*`
		// and the two floor keys are rows whose only store is that file. Judged
		// anywhere else they would name a check that never ran, for the reason
		// the stale rule is written into the record only where it ran.
		judge(settingsProfilesRule, settingsProfilesRefusal(key))
		if marker != "" {
			// The floors bind a coding agent's session, which only raises
			// them; a person at a real terminal may lower them (the owner's
			// ruling of 2026-11-06, the same one as the allowlist above).
			judge(settingsFloorRule, settingsFloorRefusal(key, old, newData, target.keyPath))
		}
	}
	judge(settingsReservedRule, settingsReservedRefusal(key))
	judge(settingsOwnerOnlyRule, settingsOwnerOnlyRefusal(typed, why))
	if target.kind == settingsDocLayer {
		// D854's subject is this machine's own layer file and no other
		// document: a repository's fugaro.yaml and the local project config are
		// not copies of the published layer, so a rule about that file would
		// name a check that never ran for them. It is written into the record
		// only where it ran, for the reason config publish's own admission does
		// the same.
		judge(settingsStaleRule, settingsStaleRefusal(ctx, g))
	}
	judge(settingsUnjudgedRule, settingsUnjudgedRefusal(old, newData, target.keyPath))
	if len(refused) > 0 {
		return managedmode.ConvergeRefused(newData, initflow.YesOrTyped, refused)
	}
	return managedmode.ConvergeAdmitted(newData, initflow.YesOrTyped, applied)
}

// The rules, in the words the refusal and the audit record carry. Each is a
// constant because two readers grep for the same text: the session reads the
// rule that refused (D1023), and the owner reads it again, months later, in the
// record of a run they did not watch (D1014). Each names where it is written
// down, so an owner comparing a refusal against the design finds the section it
// came from.
const (
	// settingsRegistryRule and settingsLayerRule are the command's own checks,
	// asked before the gate is and named here so the record says what was
	// judged. They cannot fail at this point — settingsWriteRow and
	// settingsResolveTarget have already refused otherwise, in their own words,
	// before a document existed — and they are constants rather than functions
	// for exactly that reason.
	settingsRegistryRule = "the key is one the settings registry knows and this path writes (spec §4.4)"
	settingsLayerRule    = "the key may be set at the layer this write lands in (the registry's Layers, spec §4.2 step 2)"
	// settingsAdmittedRule is the allowlist itself (D1027): the write's key is
	// one the gate decided about, in settingsManagedKeys. It is asked of every
	// write a coding agent's session makes through this gate, at every layer,
	// because "this key is not one a
	// coding agent's session writes" is the answer for a key nobody decided
	// about however the command line spelled it.
	settingsAdmittedRule = "the key is one the gate's allowlist admits for a coding agent's session (D1027)"
	// settingsProfilesRule is spec §4.2's executable-key guard over the project
	// layer's profiles block. The keys it protects are the ones that run as
	// shell — a build script, an apt package, a command — and they are
	// reachable only through a profile, which is why the rule is written over
	// the block rather than over a list of key names a later release could
	// grow past. The allowlist refuses these keys as well (none of them is on
	// it); this rule is what makes the refusal name the executable key, so the
	// session reads the reason and not only "no rule of this gate admits it".
	settingsProfilesRule = "the write is no key of the project layer's profiles, which carry the keys that run as shell (spec §4.2)"
	// settingsFloorRule is the direction rule over the two keys the allowlist
	// admits: the review rounds a run spends, which a session may raise and
	// never lower or remove.
	settingsFloorRule = "the write does not lower the review rounds a run spends (the allowlist's floor keys, D1027)"
	// settingsReservedRule is the plan's §2 settings row: the keys no coding
	// agent's session writes, whatever the registry says about them — most of
	// them ARE registry keys, and that is the point (spec §4.2).
	settingsReservedRule = "the write is no reserved key (credentials.*, clone_urls, budget.*, agent.oauth_token, governance.*)"
	// settingsOwnerOnlyRule is D1026's class rule as a rule of this gate: the
	// edit the typed confirmation reserves — a ceiling loosened, a floor
	// removed, a follow-up key turned on, a credential handed to a job — is
	// never admitted, whatever a document looks like.
	settingsOwnerOnlyRule = "the write is none that the typed confirmation reserves for the project's own hand (D1026)"
	// settingsStaleRule is D854 over this machine's own layer file. Its
	// refusal is config publish's own sentence rather than one written here
	// (layerStaleRule), so the two doors the owner may reach a stale copy
	// through refuse it in the same words; this is the name the admission's
	// record carries.
	settingsStaleRule = "this machine's layer file was written from the published layer's generation (D854)"
	// settingsUnjudgedRule is §4.1 rule 2's fail-closed half: every member of
	// the document no rule above judges is unchanged in the bytes written.
	settingsUnjudgedRule = "every member of the document that no rule of this gate judges is unchanged (spec §4.1 rule 2)"
)

// settingsManagedKeys is the allowlist: every key a coding agent's session may
// write, in a managed project, under this gate. It is a list of NAMES and not
// a rule about a shape, which is the whole of the fail-closed direction — a
// key a later release adds to the registry, a key a profile or a block grows,
// and a key nobody has decided about are each refused, because they are not
// here (settingsAdmittedRefusal).
//
// What is on it, and why these two. They are the review rounds a run spends:
// `layer.defaults.agent.review_rounds` for the senior review and its fixes,
// `layer.defaults.agent.adversarial_rounds` for the adversarial pass. Each is
// a number whose larger value is more review, every other direction of which
// is refused by settingsFloorRule; neither is a ceiling, a reserved key or a
// key the typed confirmation reserves; and neither carries a key that runs as
// shell. That is the whole of what makes a key admitted here: raising it
// cannot weaken the project, and no other key of the registry can say that —
// which is why this list is short and why it is the owner's to lengthen.
//
// It is an owner decision and not a derivation (D1027, recorded in
// docs/managed-agent-converge/reconciliation.md pending the owner's
// confirmation): the plan's §2 settings row admitted "a key the registry
// knows" and refused the reserved ones, which is a denylist, and a denylist
// admits every key nobody thought of. An allowlist that enumerated its
// refusals instead would be the same list read backwards.
var settingsManagedKeys = []string{
	"layer.defaults.agent.review_rounds",
	"layer.defaults.agent.adversarial_rounds",
}

// settingsManagedAdmits reports whether key is one the allowlist names. The
// comparison is the registry's own wildcard matcher and not string equality,
// because the list holds names as the registry spells them: an entry the owner
// adds with a `*` — the spelling every wildcard row has (D1161) — matches the
// concrete key a command line carries, where an equality test would refuse that
// key forever with a sentence naming a pattern nobody can type.
func settingsManagedAdmits(key string) bool {
	for _, k := range settingsManagedKeys {
		if k == key || settingsPatternMatch(k, key) {
			return true
		}
	}
	return false
}

// settingsAdmittedRefusal is the key this write names when it is not one the
// allowlist admits, or "".
func settingsAdmittedRefusal(key string) string {
	if settingsManagedAdmits(key) {
		return ""
	}
	return ruleSaid(settingsAdmittedRule, fmt.Sprintf("%s is not a key a coding agent's session writes: this gate admits %s and nothing else, so a key the registry gains later is refused until the project's owner confirms it; the project's owner writes it at their own terminal", safetext.Field(key), strings.Join(settingsManagedKeys, " and ")))
}

// settingsProfilesRefusal is the executable-key refusal over the project
// layer's profiles block: every key under it, or "".
//
// The block is refused whole rather than key by key. A profile is one unit —
// `default_profile` names one and a repository selects one — and the keys that
// run as shell are inside it beside the profile's scope, its cache paths and
// its timeouts, so a rule that admitted the profile's harmless members would
// be a rule that admitted the block's own name and then had to be right about
// every member a later release adds to it. `HasExecutable` (SD4) makes the
// same call from the other side.
func settingsProfilesRefusal(key string) string {
	if !strings.HasPrefix(key, "layer.profiles.") {
		return ""
	}
	return ruleSaid(settingsProfilesRule, fmt.Sprintf("the project layer's %s is a profile key, and a profile is where a build script, an apt package and a command live beside the scope of every repository that uses it: no coding agent's session writes one, whatever the command line says, and the project's owner writes it at their own terminal", safetext.Field(key)))
}

// settingsFloorRefusal is the direction rule over the allowlist's floor keys:
// the write lowers the rounds a run spends, or takes them away, or "".
//
// The comparison is against what a run gets today, read out of `old` — the
// same bytes the write is conditioned on (applySettingsEdit refuses to write
// unless the file is still byte for byte what the caller read), so the value
// judged and the value replaced are one artifact. The mode supplies the base
// only where the layer sets no number of its own: the layer's
// `defaults.rigor.default` when it names one, else rigor.Default, which is the
// number a run of this project starts from (rigor.DefaultRounds).
//
// A write that leaves no number — `settings unset`, whose document carries the
// key nowhere — is refused with the lowering. A removal hands the number back
// to the mode's own row, and which way that moves is not something this gate
// resolves: admitting the removal would need it to, because the number the
// document then leaves is one nobody named. So the removal is refused and the
// session that wants a different number names one, which this rule admits
// whenever the number it names is at or above what a run spends today. Same
// direction as every other read of this gate — a value nobody can hold at or
// above the floor is not a value this rule may admit.
func settingsFloorRefusal(key string, old, next []byte, keyPath []string) string {
	if !settingsManagedAdmits(key) {
		return ""
	}
	raw := settingsValueIn(next, keyPath)
	value, ok := settingsRounds(raw)
	if !ok {
		return ruleSaid(settingsFloorRule, fmt.Sprintf("%s would be left as no number this gate can hold at or above the rounds a run spends today: the two keys it admits are floors, a coding agent's session raises them and never lowers or removes them, and the project's owner makes that change at their own terminal", safetext.Field(key)))
	}
	base, baseText := settingsFloorBase(old, keyPath)
	if value < base {
		return ruleSaid(settingsFloorRule, fmt.Sprintf("setting %s to %s would lower it from %s: the two keys this gate admits are floors, so a coding agent's session raises them and never lowers them, and the project's owner makes that change at their own terminal", safetext.Field(key), safetext.Field(raw), baseText))
	}
	return ""
}

// settingsRounds is a round count as a number, false for anything this rule
// cannot read as one: a value the loader would refuse anyway, and the empty
// string an unset or a removed key leaves.
func settingsRounds(raw string) (float64, bool) {
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(raw, 64)
	return n, err == nil
}

// settingsFloorBase is the number of rounds a run can spend today for the
// senior-review key at keyPath: the layer's own value where it sets one, else
// the strictest mode's senior row. See settingsFloorBaseRow for the rule and
// why the fallback is the highest row.
func settingsFloorBase(old []byte, keyPath []string) (float64, string) {
	return settingsFloorBaseRow(old, keyPath, rigor.DefaultRounds(rigor.Critical).Senior)
}

// settingsFloorBaseRow is the number of rounds a run can spend today for the
// key at keyPath: the layer's own value where it sets one, else the STRICTEST
// mode's row for the loop that key counts. A layer value pins the count, but
// with none set every run takes its rounds from the mode it resolves, and that
// mode is not this layer's to know: `--rigor critical`, an area match, a
// reached rigor (D22) and a repository's own `rigor.default` all resolve higher
// than the layer's mode, and the checkouts are not part of this write. So the
// base is the highest row, and a session that writes a lower number into a
// layer that sets none is refused. The text beside it is what the refusal
// names.
//
// The row is the caller's, because a mode's row counts two loops and they are
// not the same number: the senior review runs rigor.Rounds.Senior times where
// the first-line review runs .Inner (critical is 3 and 5). The settings door
// reads the two keys its allowlist admits, both senior loops, and passes the
// senior row; the publish door judges defaults.agent.first_line_rounds beside
// them and passes that loop's own row, which a base taken from the wrong one
// would judge against a number no run spends. One function decides the base for
// both doors on purpose: "the layer's own value where it sets a positive
// number, else the row" is one rule, and two copies of it would be two answers
// to what a run spends today.
func settingsFloorBaseRow(old []byte, keyPath []string, row int) (float64, string) {
	if raw := settingsValueIn(old, keyPath); raw != "" {
		// Zero is no number here: the layer's own reader treats a 0 round count
		// as unset and lets the mode's row decide (config's validator fires only
		// for a non-zero value that is out of range), so a base of 0 would admit
		// the write that takes a run from the mode's row down to one round.
		if n, ok := settingsRounds(raw); ok && n > 0 {
			return n, raw
		}
	}
	return float64(row), fmt.Sprintf("%d (the strictest mode's row)", row)
}

// settingsReservedRefusal is the reserved run a written key hits, or "".
//
// The runs are config publish's own (layerReservedRuns, asked through the same
// matcher), because the two commands write the same vocabulary of keys and a
// second table would be a second thing to keep in step with the plan.
//
// The comparison is over the key as the command line spelled it and not over
// the registry's pattern: `settings set governance.review_ratio` is refused for
// the `governance.*` run, and the project layer's `layer.governance.review_ratio`
// is refused for the same run one word deeper — the run is matched anywhere in
// the path, exactly as it is for a published document.
func settingsReservedRefusal(key string) string {
	run := layerReservedRun(strings.Split(key, "."))
	if len(run) == 0 {
		return ""
	}
	return ruleSaid(settingsReservedRule, fmt.Sprintf("%s is a key no coding agent's session writes, whatever the registry says about it: the project's owner writes it at their own terminal", safetext.Field(strings.Join(run, "."))))
}

// settingsOwnerOnlyRefusal is the widening this edit is, in the command's own
// words for why the typed form was asked for, or "".
//
// It is settingsTypedWrite's answer read back rather than a second comparison:
// one predicate decides what the typed confirmation is for, so a direction the
// prompt asks the owner's own name for is a direction this gate refuses — and
// one it does not ask for cannot be refused here by accident. The gate can only
// narrow: an edit that reaches this rule is one D846 already reserves, so
// refusing it takes nothing from a person and gives the session the rule it
// moved rather than the marker's own refusal.
func settingsOwnerOnlyRefusal(typed bool, why string) string {
	if !typed {
		return ""
	}
	return ruleSaid(settingsOwnerOnlyRule, why+" — a step of that class is never admitted by a plan gate, so nothing is written here")
}

// settingsStaleRefusal is D854 over this machine's layer file, or "".
//
// It is doctor's own state and doctor's own sentence (layerStaleRule, read from
// config_publish_managed.go so neither door invents its own), asked the way
// config publish asks it: the published object read out of the installation's
// own bucket, then the local file's recorded generation against the object's,
// with the two causes a difference has told apart by the record. Only the stale
// cause refuses — a local edit on top of the published generation is exactly
// what a settings write IS, and a file with no recorded generation is one this
// machine has never published.
//
// A published object that cannot be read or decoded is a refusal rather than a
// predecessor of nothing: measured against nothing every local file counts as
// unchanged, so the rule an owner audits would have run without ever being able
// to fail. Same answer, same reason, as config publish's own read.
func settingsStaleRefusal(ctx context.Context, g *managedGate) string {
	lc := g.env.lc
	b, err := blobx.Open(ctx, lc.BucketURL())
	if err != nil {
		return "the published project layer could not be read (" + oneLineCLI(err.Error()) + "), so whether this machine's layer file was written from it cannot be judged, and the project's owner makes this change at their own terminal"
	}
	defer b.Close()
	data, gen, err := b.ReadMaxStrict(ctx, config.LayerKey, config.LayerMaxBytes)
	var prev *config.ProjectLayer
	switch {
	case err == nil:
		l, ps := config.ParseProjectLayer(data, config.LayerAnchor{Project: lc.Name, GCPProject: lc.GCPProject})
		if len(ps) > 0 {
			return fmt.Sprintf("the published project layer is not a layer this program can read (%d problem(s)), so whether this machine's layer file was written from it cannot be judged, and the project's owner republishes it at their own terminal", len(ps))
		}
		prev = l
	case errors.Is(err, blobx.ErrNotExist):
		// Nothing is published yet: this machine's file has no predecessor to
		// have been written from, which is the ordinary first publish.
		return ""
	default:
		return "the published project layer could not be read (" + oneLineCLI(err.Error()) + "), so whether this machine's layer file was written from it cannot be judged, and the project's owner makes this change at their own terminal"
	}
	return layerStaleRule(lc, prev, gen)
}

// settingsUnjudgedRefusal is the rule that closes this gate over every member
// of the document no rule above reads (spec §4.1 rule 2, M6's review of
// 2026-11-03 item 5).
//
// Why the question is asked of the two whole documents and not of a list of
// members. The rules above judge ONE key, and a gate that judged only what
// somebody remembered to write down is a gate a session steps around with an
// editor that splices one byte somewhere else: the entry pair (G1) rewrites a
// whole block, `yamledit` splices text rather than re-emitting the document,
// and a document a later release teaches a new key is one nobody wrote a rule
// for. What is compared is the two documents minus the key under judgement —
// the member masked out of both sides — so a byte moved anywhere else is a
// refusal, named, rather than a change that travelled in beside the one that
// was admitted.
//
// The comparison is over the two documents and never over the two files: the
// bytes are compared by their meaning as YAML, so a re-indent or a re-ordered
// key that `yamledit` produced is not a change, which is the same reason config
// publish compares member maps and not documents.
//
// A pair this walk cannot read is a refusal and not a pass. That is the
// fail-closed direction every read of this gate falls in: a document nobody can
// compare is a document whose other members nobody judged.
func settingsUnjudgedRefusal(old, next []byte, keyPath []string) string {
	was, ok := settingsMaskedDocument(old, keyPath)
	if !ok {
		return ruleSaid(settingsUnjudgedRule, "the document this write starts from cannot be read as one this gate compares, so no member of it was judged")
	}
	now, ok := settingsMaskedDocument(next, keyPath)
	if !ok {
		return ruleSaid(settingsUnjudgedRule, "the document this write would leave cannot be read as one this gate compares, so no member of it was judged")
	}
	if reflect.DeepEqual(was, now) {
		return ""
	}
	return ruleSaid(settingsUnjudgedRule, fmt.Sprintf("this write would change a member of the document other than %s, and no rule of this gate reads it: a coding agent's session writes what these rules judged, so the project's owner makes that change at their own terminal", safetext.Field(strings.Join(keyPath, "."))))
}

// settingsMaskedDocument is one document as the value this gate compares, with
// the key under judgement taken out of it. A document that is not one YAML
// mapping is not compared, which the caller refuses.
func settingsMaskedDocument(data []byte, keyPath []string) (any, bool) {
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	if _, ok := doc.(map[string]any); !ok {
		return nil, false
	}
	settingsMaskPath(doc, keyPath)
	return doc, true
}

// settingsMaskPath removes the key at path from the parsed document, and does
// nothing when the document does not carry it: a write that ADDS a key leaves
// the old side without it, which is exactly the comparison the rule wants.
//
// It takes the empty mappings the walk left behind with it, and that is what
// makes the rule about the key and not about the path to it: `settings set
// layer.defaults.agent.review_rounds` on a layer that has no `defaults:` block
// adds `defaults:` and `agent:` to reach the key, and a mapping this edit
// created to hold one judged key is not a second member the document carries —
// an empty one means exactly what an absent one means. A mapping that was
// already there and is not empty is untouched, so a member that really changed
// still differs between the two sides.
func settingsMaskPath(doc any, path []string) {
	cur, ok := doc.(map[string]any)
	if !ok || len(path) == 0 {
		return
	}
	seg := path[0]
	if len(path) == 1 {
		delete(cur, seg)
		return
	}
	next, ok := cur[seg].(map[string]any)
	if !ok {
		return
	}
	settingsMaskPath(next, path[1:])
	if len(next) == 0 {
		delete(cur, seg)
	}
}
