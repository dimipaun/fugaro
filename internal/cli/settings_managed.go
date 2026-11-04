package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
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
// Three rules that could not fire here, and are therefore absent rather than
// restated. (1) The executable keys (`commands.*`, `image.apt`,
// `image.setup`): every one of them is a `repo` or `profile` layer key, and a
// `--layer repo` write that is not the typed one is exactly the write today's
// refusal admits (see settingsGateApplies), so no document this gate judges
// can carry one. (2) The rigor floors and `review_floor`: those rows belong to
// another family (D853), so settingsWriteRow refuses them before any document
// exists. (3) The widening of a cap: `budget.*` and `governance.*` are the
// reserved runs, refused whole. A rule named among the ones that passed is a
// claim that it ran and could have failed, and an owner auditing a record
// reads it as one — so a rule that can never fire is worse than an absent one,
// which is the reason M3 gives for the same omission on its own path.
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
// Why a repository-scoped ordinary write reads nothing at all. Today's fence
// does not apply inside a repository (`--layer repo` writes a file an agent
// could edit anyway, unless the edit is the typed one), so there is no refusal
// for the gate to stand in for and no mark is read: the ordinary repository
// edit keeps exactly the behaviour it has, pays for nothing, and cannot be
// turned into a refusal by a transient Storage or Resource Manager failure
// (M6's review of 2026-11-03, item 2). The aim is the fence's own condition,
// asked once, in settingsGateApplies.

// settingsGateMarker is the coding agent's session this command was run in, ""
// for a person at a terminal. It is read once per command, here, so the two
// places that ask "is the gate this command's business" cannot disagree.
func settingsGateMarker() string { return agentMarker(os.Getenv) }

// settingsGateApplies reports whether one edit is one today's coding-agent
// refusal would stop: applySettingsEdit reads exactly this condition, and a
// gate consulted anywhere else would be reading marks for a write that was
// never refused (D1025).
func settingsGateApplies(marker string, kind settingsDocKind, typed bool) bool {
	return marker != "" && (kind != settingsDocRepo || typed)
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
// it is asked at all (settingsGateApplies, read once here) and which document
// it judges.
func settingsManagedStep(ctx context.Context, lc *localcfg.Config, target settingsTarget, key string, typed bool, why string, old, newData []byte, subcommand string) (initflow.Managed, managedGate, error) {
	marker := settingsGateMarker()
	if lc == nil || !settingsGateApplies(marker, target.kind, typed) {
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
	conv := settingsManagedVerdict(ctx, &gate, target, key, typed, why, old, newData)
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
func settingsManagedVerdict(ctx context.Context, g *managedGate, target settingsTarget, key string, typed bool, why string, old, newData []byte) managedmode.Converge {
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
