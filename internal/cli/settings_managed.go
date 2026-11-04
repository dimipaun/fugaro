package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	storage "google.golang.org/api/storage/v1"

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
// `--layer local` and `--layer project` write `~/.config/fugaro`, and a typed
// edit is refused even at `--layer repo`. In a managed project the project's
// own gate answers that question per step instead, so a session that writes a
// registry key whose whole effect the document shows is admitted, and the owner
// gets a record of which key was written, by which rules, and against which
// bytes.
//
// Which rules are this command's own, and which it deliberately does not
// restate. The registry (spec §4.4) and the layer a key may be set in are the
// command's own checks, asked before the gate is (settingsWriteRow,
// settingsResolveTarget), and they are named among the rules that passed
// because a record has to say what was judged. The reserved keys and the
// executable keys are the gate's own rules, in the same words `config publish`
// uses. The rigor floors and `review_floor` are NOT rules here: those rows are
// another family's (D853), so settingsWriteRow refuses them before any document
// exists, and a rule over a document that can never carry them would be a check
// an owner audits as one that merely happened to pass (M3's own reasoning).
//
// Why the class the gate judges under is YesOrTyped and not the edit's own.
// The edit's Typed form is what the confirmation asks a PERSON for, and it
// stays the fence's (`settingsEdit.editClass`): nothing here touches it. What
// the gate adds is the refusal the session reads instead of the marker's own —
// and a widening is refused BY A RULE of the gate (settingsOwnerOnlyRule), so
// the session reads which lever it tried to move rather than "run this
// yourself". Both halves hold at once: an edit the typed confirmation reserves
// is refused by the gate before the fence is reached, and an edit the gate
// admitted without noticing the typed form would still be refused by the fence,
// because the fence decides `Typed` on its own (D1026's rule is asked there and
// is not restated here). No Typed edit can therefore be admitted by any path.
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
// for a person at a terminal. It is read once per command, here, so the three
// places that ask "is the gate this command's business" cannot disagree.
func settingsGateMarker() string { return agentMarker(os.Getenv) }

// settingsGateApplies reports whether one write is one today's coding-agent
// refusal would stop: the fence in applySettingsEdit reads exactly this
// condition, and a gate consulted anywhere else would be reading marks for a
// write that was never refused (D1025).
func settingsGateApplies(marker string, kind settingsDocKind, typed bool) bool {
	return marker != "" && (kind != settingsDocRepo || typed)
}

// settingsWriteGate reads the two marks for one settings write and answers the
// gate every command in the set calls. It is M6's ONE helper, read and called
// here, and this function is only the three ways of not being able to ask:
// a person's session (no marker), an installation with no cloud to read the
// marks from, and a client that cannot be built.
//
// Each of the three leaves the zero gate behind, which refuses nothing and
// records nothing: the session keeps this command's own refusal — the sentence
// it read before this row landed — and a mark that cannot be read is never a
// permission. That is also why the error `readManagedGate` may answer is folded
// into the zero gate here rather than returned: a session that is refused
// anyway must not learn which read broke or which endpoint carries it.
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
		Subcommand: subcommand,
		Argv:       os.Args[1:],
		// What this command would have written, for the ownership question
		// D1017 asks with the database a command is about to write. This one
		// writes a file and no database, so its gate holds no client for one
		// (managedGateEnv.db stays nil) and the question is not asked here;
		// the sentence travels all the same, so a later release that gives
		// this path a database does not have to invent one.
		Declined:    "nothing was written",
		AgentMarker: marker,
		// The whole-project inventory (D59) is `fugaro managed apply`'s read
		// and is not consulted here: a command that has none passes false,
		// which is the documented answer for it.
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
// three answers are the same three for the same reasons. What differs is only
// when it is asked at all (settingsGateApplies, read once here) and which
// document it judges.
func settingsManagedStep(ctx context.Context, lc *localcfg.Config, target settingsTarget, key string, typed bool, why string, old, newData []byte, subcommand string) (initflow.Managed, managedGate, error) {
	marker := settingsGateMarker()
	if lc == nil || !settingsGateApplies(marker, target.kind, typed) {
		return initflow.Managed{}, managedGate{}, nil
	}
	gate := settingsWriteGate(ctx, lc, marker, subcommand)
	if !gate.managed {
		// No installation to read, a mark that did not answer, a client that
		// cannot be built, or the ownership question's own refusal: the project
		// is not one this program can prove is managed, so the session keeps
		// this command's refusal — the sentence it read before this row landed
		// — and nothing is recorded.
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
// two that happened to agree at different moments (the plan's M6 review, item 3).
func settingsManagedVerdict(ctx context.Context, g *managedGate, target settingsTarget, key string, typed bool, typedWhy string, old, newData []byte) managedmode.Converge {
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
	judge(settingsExecutableRule, settingsExecutableRefusal(target.keyPath))
	judge(settingsOwnerOnlyRule, settingsOwnerOnlyRefusal(typed, typedWhy))
	if target.kind == settingsDocLayer {
		// D854's subject is this machine's own layer file and no other
		// document: a repository's fugaro.yaml and the local project config
		// are not copies of the published layer, so a rule about that file
		// would name a check that never ran for them. It is written into the
		// record only when it ran, for the reason config publish's own
		// admission does the same.
		judge(settingsStaleRule, settingsStaleRefusal(ctx, g, target))
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
	// settingsResolveTarget have already refused otherwise — and they are
	// constants rather than functions for exactly that reason.
	settingsRegistryRule = "the key is one the settings registry knows and this path writes (spec §4.4)"
	settingsLayerRule    = "the key may be set at the layer this write lands in (the registry's Layers, spec §4.2 step 2)"
	// settingsReservedRule is the plan's §2 settings row: the keys no coding
	// agent's session writes, whatever the registry says about them — most of
	// them ARE registry keys, and that is the point (spec §4.2).
	settingsReservedRule = "the write is no reserved key (credentials.*, clone_urls, budget.*, agent.oauth_token, governance.*)"
	// settingsExecutableRule is §4.2 step 4's executable half, once more: what
	// runs as shell in the job is the owner's to change in their own terminal.
	settingsExecutableRule = "the write changes nothing that runs as shell (commands.*, image.apt, image.setup)"
	// settingsOwnerOnlyRule is D1026's class rule as a rule of this gate: the
	// edit the typed confirmation reserves — a ceiling loosened, a floor
	// removed, a follow-up key turned on, a credential handed to a job — is
	// never admitted, whatever a document looks like.
	settingsOwnerOnlyRule = "the write is none that the typed confirmation reserves for the project's own hand (D1026)"
	// settingsStaleRule is D854 over this machine's own layer file.
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
// the `governance.*` run whichever layer it would land in, and
// `layer.governance.review_ratio` is refused for the same run one word deeper —
// the run is matched anywhere in the path, exactly as it is for a published
// document.
func settingsReservedRefusal(key string) string {
	run := layerReservedRun(strings.Split(key, "."))
	if len(run) == 0 {
		return ""
	}
	return ruleSaid(settingsReservedRule, fmt.Sprintf("%s is a key no coding agent's session writes, whatever the registry says about it: the project's owner writes it at their own terminal", safetext.Field(strings.Join(run, "."))))
}

// settingsExecutableRefusal is the executable key this write would change, or
// "".
//
// The vocabulary is config.ExecutableKeys's and not a second list: those are
// the keys whose value a job runs as shell, and the tail of each of them —
// everything under the workflow's or the profile's own name — is what tells a
// repository's `workflows.web.commands.test` and the project layer's
// `layer.profiles.svc.commands.test` from an ordinary key. A list written out
// here would go stale the day a new executable key is added, which is the day
// it would matter.
func settingsExecutableRefusal(path []string) string {
	for _, key := range config.ExecutableKeys {
		segs := strings.Split(key, ".")
		// "workflows.<name>.<tail>" and "profiles.<name>.<tail>": the first two
		// segments are the block's own name and the entry the write names, and
		// both commands' paths carry those under their own spellings.
		if len(segs) < 3 {
			continue
		}
		tail := segs[2:]
		if !settingsPathEndsWith(path, tail) {
			continue
		}
		return ruleSaid(settingsExecutableRule, fmt.Sprintf("%s is one of the keys a job runs as shell, and changing it is what the project's owner does in their own terminal — the document is judged by its effect and not by the flag the session passed", safetext.Field(strings.Join(tail, "."))))
	}
	return ""
}

// settingsPathEndsWith reports whether path ends with the pattern segments: the
// same length from the tail, equal where the pattern names a value, and any
// non-empty segment where it writes `*`.
func settingsPathEndsWith(path, pattern []string) bool {
	if len(path) < len(pattern) {
		return false
	}
	tail := path[len(path)-len(pattern):]
	for i := range pattern {
		if pattern[i] == "*" {
			if tail[i] == "" {
				return false
			}
			continue
		}
		if pattern[i] != tail[i] {
			return false
		}
	}
	return true
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
// It is doctor's own state and doctor's own sentence (localLayerState,
// unpublishedText), asked the way config publish asks it: the local file's
// recorded generation against the published object's, with the two causes a
// difference has told apart by the record. Only the stale cause refuses — a
// local edit on top of the published generation is exactly what a settings
// write IS, and a file with no recorded generation is one this machine has
// never published.
//
// A published object that cannot be read or decoded is a refusal rather than a
// predecessor of nothing: measured against nothing, every local file is
// "unchanged", and the rule an owner audits would have run without ever being
// able to fail. Same answer, same reason, as config publish's own read.
func settingsStaleRefusal(ctx context.Context, g *managedGate, target settingsTarget) string {
	pub, gen, why := settingsPublishedLayer(ctx, g.env.s, g.env.lc)
	if why != "" {
		return ruleSaid(settingsStaleRule, why)
	}
	st := localLayerState(g.env.lc, g.env.lc.Name, pub, gen)
	if !st.Unpublished || st.Cause != layerLocalStale {
		return ""
	}
	return ruleSaid(settingsStaleRule, st.unpublishedText())
}

// settingsPublishedLayer reads the published project layer this machine's own
// layer file is a copy of, with the gate's own storage client: the bytes, the
// object's generation, and the one-line reason it could not be judged.
//
// An object that is not there is not a failure: a project that has published no
// layer has no predecessor for the local file to have been written from, which
// is the ordinary first publish and not a stale copy. Everything else — a grant
// the session does not hold, an API that did not answer, an object this build
// cannot decode — is a reason, and a reason refuses.
func settingsPublishedLayer(ctx context.Context, s *storage.Service, lc *localcfg.Config) (*config.ProjectLayer, int64, string) {
	if s == nil || lc == nil {
		return nil, 0, "the published project layer cannot be read from this session at all, so whether this machine's file was written from it cannot be judged"
	}
	bucket := lc.RunsBucketName()
	resp, err := s.Objects.Get(bucket, config.LayerKey).Context(ctx).Download()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
			return nil, 0, ""
		}
		return nil, 0, fmt.Sprintf("the published project layer at gs://%s/%s could not be read (%s), so whether this machine's layer file was written from it cannot be judged: the project's owner writes this key at their own terminal", safetext.Field(bucket), config.LayerKey, oneLineCLI(err.Error()))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, config.LayerMaxBytes+1))
	if err != nil {
		return nil, 0, fmt.Sprintf("the published project layer at gs://%s/%s could not be read (%s), so whether this machine's layer file was written from it cannot be judged", safetext.Field(bucket), config.LayerKey, oneLineCLI(err.Error()))
	}
	gen, _ := strconv.ParseInt(resp.Header.Get("X-Goog-Generation"), 10, 64)
	l, ps := config.ParseProjectLayer(data, config.LayerAnchor{Project: lc.Name, GCPProject: lc.GCPProject})
	if len(ps) > 0 {
		return nil, 0, fmt.Sprintf("the published project layer at gs://%s/%s is not a layer this program can read (%d problem(s)), so whether this machine's layer file was written from it cannot be judged: the project's owner republishes it at their own terminal", safetext.Field(bucket), config.LayerKey, len(ps))
	}
	return l, gen, ""
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
// the members masked out of both sides — so a byte moved anywhere else is a
// refusal, named, rather than a change that travelled in beside the one that
// was admitted.
//
// The comparison is over the two documents and never over the two files: the
// bytes are compared by their meaning as YAML, so a re-indent or a re-ordered
// key that `yamledit` produced is not a change, which is the same reason
// config publish compares member maps and not documents.
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
	return ruleSaid(settingsUnjudgedRule, fmt.Sprintf("this write would change a member of %s other than %s, and no rule of this gate reads it: a coding agent's session writes what these rules judged, so the project's owner makes that change at their own terminal",
		safetext.Field("the document"), safetext.Field(strings.Join(keyPath, "."))))
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
func settingsMaskPath(doc any, path []string) {
	cur, ok := doc.(map[string]any)
	if !ok {
		return
	}
	for i, seg := range path {
		if i == len(path)-1 {
			delete(cur, seg)
			return
		}
		next, ok := cur[seg].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
}
