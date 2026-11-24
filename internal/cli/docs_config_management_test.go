package cli

// The configuration-management docs folder, pinned.
//
// docs/config-management/ is a design-only folder: README.md (the index, the
// owner's ruling verbatim and the reading of the number block), spec.md (the
// design: what "managed" means for a configuration, the generic writer's
// extension, the dedicated commands that remain, the registry's owner column
// and the managed gate's rows), reconciliation.md (the audit F1-F14 against the
// tree, the corrections C1-C8 to the brief's own reading, and this work's
// decisions D1160-D1183) and implementation-plan.md (the tasks G0-G13). It adds
// no code and no task in it is approved, so nothing here pins a command, a flag
// or an option.
//
// What it does pin is the half of these documents that rots silently: the
// decision block, the citations the audit rests on, and the two figures the
// audit's own argument is made of -- the registry's row count and the count of
// rows the generic writer's allowlist carries.
//
//  * TestDocsConfigManagementIsWritten -- the four files exist, the index
//    carries the status line and the owner's ruling, and the plan states the
//    order it orders every task by.
//  * TestDocsConfigManagementDecisionsAreInRange -- D1160-D1183 are written,
//    each exactly once and in order, every one is open with a recommendation,
//    and D1184-D1189, the reserved tail, is written as no decision at all.
//  * TestDocsConfigManagementCitationsResolve -- every `path:line` citation
//    resolves to a file of this tree and a line inside it, measured against an
//    independent census so a regexp that stopped matching fails rather than
//    passing vacuously.
//  * TestDocsConfigManagementRelativeCitationsResolve -- the same rule for the
//    `:NNN` shape, which names no file and resolves against the nearest
//    citation before it in its own paragraph.
//  * TestDocsConfigManagementAllowlistCounts -- the audit's central figure: the
//    registry really holds the rows the documents claim, and the allowlist
//    really carries the rows they claim it carries.
//  * TestDocsRigorModesReconciliationCountsTheConfigManagementPin -- the census
//    row this folder moves is moved, and the figure in it is the live count of
//    the folders' pins rather than a number somebody typed.
//  * TestDocsConfigManagementNoSecondWritePath -- the property the plan states
//    for every task it adds: one write path, and the ten commands of its fifth
//    section are the ones it names.
//  * TestDocsConfigManagementKeepsTheSettingsWriteVersion -- the settings-write
//    document's version, pinned against the console that refuses any other, so
//    the design can never plan a bump that breaks the console's own edit.
//  * TestDocsConfigManagementLeavesTheBudgetKeysWithTheGenericWriter -- the four
//    repo-layer budget keys, pinned against the scope table that makes them
//    repository rows, so the design can never hand them to the command that
//    writes the budget database instead.
//  * TestDocsConfigManagementRowsCoverEveryNewCommand -- the spec's fifth
//    section is the row set the managed build cites, so it holds one row per
//    command the plan's own fifth section names and no row for anything else.
//  * TestDocsConfigManagementEveryRowNamesItsRefuser -- every one of those rows
//    says who refuses what the gate does not admit, and the refuser is a
//    decision this design already wrote.
//  * TestDocsConfigManagementInstallationFactsHaveNoWriter -- the owner table
//    D1182 asks for lists every installation fact no writer carries, and each
//    converge flag it names is a flag the two doors really have.
//
// The citation resolver is the cloud senior review's (`csrBareNames`,
// `csrResolveCitation`), through the console-settings-edit folder's
// (`cseCitationRE`'s shapes): three docs tests asking one question of one tree
// should not each carry their own, and a second copy is the one that drifts.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/settings"
)

const (
	cfmReadme     = "docs/config-management/README.md"
	cfmSpec       = "docs/config-management/spec.md"
	cfmReconciled = "docs/config-management/reconciliation.md"
	cfmPlan       = "docs/config-management/implementation-plan.md"
)

// cfmDocs is every file of the folder that cites code, in the index's own
// order. The index summarises and links, so a citation census over it would
// only ever read zero.
var cfmDocs = []string{cfmSpec, cfmReconciled, cfmPlan}

// cfmStatusLine is the folder's status line, in the three documents that carry
// it: the index, the design and the plan. It is a snapshot and not a promise --
// the count is the tasks merged into the tree the three files are written
// against, and the table it points at is `docs/status.md`'s. G13 writes it,
// because a folder whose last task has landed while its own index still reads
// as a design nobody has built is a folder a reader stops trusting; every later
// merger of a task here moves the count in the same sweep as its row.
const cfmStatusLine = "**Status as of 2026-11-10: in progress, 8/14 merged; see [docs/status.md](../status.md) for the current table.**"

// The block this work holds, as the documents write it: D1160-D1183 written,
// D1184-D1189 reserved and unused. Every decision in it is open with a
// recommendation -- the owner has answered none of them, which is why the plan's
// tasks do not start.
const (
	cfmBlockFirst    = 1160
	cfmBlockLast     = 1183
	cfmReservedFirst = 1184
	cfmReservedLast  = 1189
)

// The audit's two figures (F1): the registry's rows, and the rows the generic
// write path's allowlist carries -- ninety-four by name and the rest through
// the nine prefixes. They are re-counted below rather than trusted, because the
// whole of §1's argument is that the rows no writer carries are the defect.
//
// The named figure moved with G0, which is what the audit's §5 says a figure in
// a document does: the allowlist gained the wildcard blocks D1161 hands this
// path and the two budget keys D1179 does, and the two documents whose argument
// is made of this count carry the number the count produced. It moved again with
// T-IS15 of docs/troubleshoot-issue/, whose two local-only nudge thresholds the
// generic path does not carry: they are the config file's own keys, and a key
// added to the registry and not to the allowlist widens the gap the audit is
// about rather than narrowing it, and with CB1 of docs/catalog-build-context/,
// whose image_catalog.entries.*.source.context row is the image catalog's own.
// This moved it to 390 with the six concrete spellings of
// stages.*.claude_account, one per config.RunStages name (settings_stages_test.go),
// which were the whole of the growth then: planning.decoupling's two rows
// (registry.go:301, and the layer row at :543) are already on main.
//
// This branch moved both figures with the image setup skill's two rows: the
// bake switch, which IS7 asks through the setup wizard and writes through this
// path, is a named key and takes the named count with it; the repo-to-image map
// is one row too, and its owner is the `image map` family -- `fugaro image map
// set` is the command that writes it (D1906) -- so it widens the registry and
// not the allowlist, exactly as a key the registry carries and this list does
// not. The two rows are visible in internal/settings/registry.go, and the
// document's own sentence names the split.
//
// This branch's IS9 moved the registry's figure once more, by one row and not
// the allowlist's: `image_catalog.entries.*.services` is the catalog entry's
// own list of the services its image provides (D1904), and the leaf a person
// sets with the generic path like every other catalog member, so it is one row
// the registry carries and this list does not.
//
// The triage stage moved this figure twice: its two rows (docs/post-review-
// triage, T-TR5) are the derived `stages.triage.claude_account`, which the
// named list carries, and the `agent.triage` switch the `agent.` prefix
// reaches, and the switch's own carrier in the local project config is the
// third -- the top-level `triage` the publisher copies into the policy node
// (D1854), whose owner is the `project` family because `fugaro project policy
// publish` is what carries it. That last row is the one this paragraph adds:
// the registry has one row more and the allowlist has not.
//
// The vision slot (docs/vision-model, V1) moved the registry's figure by two
// and not the allowlist's: `agent.models.vision` in `fugaro.yaml` and its
// project-layer key `defaults.agent.models.vision` are both the `models`
// family's, and a row claiming a family is refused before the allowlist is
// asked — the same shape as the two rows above it.
//
// The model-crafts work (MC5) moved both figures once more, by one row each,
// and the figures below are the tree's: the pools' subscription switch
// `subscription_spread` (F22, D1622, D1636) is a widening direction the owner
// answers at their own terminal, so the generic path is its owner and the
// named list carries it; the pools' own `pools.*` rows are the `accounts`
// family's and were already counted here. The registry's figure is one row
// longer and the allowlist's one key wider, so the gap the audit is about does
// not move.
//
// The image rate (docs/vision-model, V2) moved it by one more, and this one
// did move the gap: `model_prices.*.image_tokens` belongs to the `models`
// family and so is refused by a family command like the rows above, but the
// block it sits in is a wildcard block no prefix reaches either, so it is one
// of the rows with no generic writer — one more of them.
//
// The diagnose slot (docs/diagnose-agent, T-DG8) moved it by two the same way
// and in the same family: `agent.models.diagnose` is the model a local
// diagnosis runs on and `defaults.agent.models.diagnose` is the layer's key
// for it, and both are written by hand until `models use` takes the role, so
// neither is in the allowlist.
//
// The settings design's S6 moved the registry's figure once more, by one row
// and not the allowlist's: the clone URLs a run clones its repository from
// (settings spec §5.6, SD5, D842) left the policy node for a block of the
// owner's local config, and its row is one for the whole map whose owner is the
// `project clone-url` family -- `settings set` can make no cross-key check
// between an entry's canonical name and its URL, which is the node's own rule
// (projectpolicy.ValidCloneURLs) -- so it is a row the registry carries and this
// list does not.
//
// Outer-loop escalation's E2 moved it by eight the same way and in the same
// direction: the escalation ladder's two model grades are four rows for a
// repository's own `model_grades.*` key and four for the project layer's
// `defaults.model_grades.*`, and not one of them is in the allowlist -- the
// block is the project's own model choice (G3), and a repository's entry is
// dropped and recorded by the merge rather than written by it.
//
// The account aliases moved it twice, by one row each and neither of them the
// allowlist's, for the clone URLs' reason: the block is local-only (D2090). AL0
// landed the block's own row, and it was owned by `config local` rather than by
// the `accounts` family the write belongs to (D2096), because `fugaro accounts
// alias set/rm` -- the write that converges the node and the job set on a
// repoint -- was a task later than the block, and a row may not name a family
// command that does not take it (D1178). AL3 landed that command, and the row
// moved with it: the map the file holds and the `claude_account_aliases.*`
// wildcard under it both name the `accounts` family, and the generic path
// carries neither -- so this count has moved by two rows and the allowlist's by
// none.
//
// The repository-level account list is the last mover, by one row and not the
// allowlist's: repos.*.claude_accounts is what admits a repository whose runs
// are not on a profile job (docs/project-claude-accounts.md §3), so it is
// local-only and stripped from the shared copy of the map, and its owner is the
// accounts family, whose `accounts rm` clears a name with the declaration it
// names -- the generic path writes neither half.
//
// AR1 of docs/binary-allow-rule/ is the last mover, by two rows and by none:
// the local project config gains the `binaries` block, whose two members are
// the paths a run may add a binary at and the cap one is held to (D981-D983),
// and both rows belong to the `project` family, because `fugaro project policy
// publish` is the door that carries the block onto the node the run reads it
// from. The generic path reaches neither: a path is a doublestar glob held to
// the node's own rule and the cap to its ceiling, which are checks no scalar
// write makes -- and both are local-only, because a launcher who could write
// the block into the shared file would be admitting its own payload to every
// teammate's runs (D986).
//
// R1 of docs/config-ownership/ is the last mover, by eight rows and by
// thirteen: the registry gains the repository's `ab.rate` opt-out, its
// `workflows.*.needs` key and the policy node's own cascade block (six rows),
// and the thirteen scalars and flags D2603 hands the generic path take the owner
// `settings set` in place of a family that does not write them (the six
// orchestrator ceilings, the two nudge thresholds, `triage`, `verifier_accounts`,
// the two `layer.jobs.*.paused` switches and `planning.decoupling`). The cascade
// rows are the eight that move the registry and not the allowlist: their owner
// is the `accounts` family, which settingsFamilyCommands maps, so the generic
// path refuses them by name.
//
// N1 of docs/config-ownership/ is the last mover, by nine rows and by none: the
// local config gains the carriers of the two node-only blocks — the cascade's
// six leaves under `credential_cascade` (F23: the node's `credentials` member
// is the cascade, while this file's `credentials` is already the list of typed
// credentials) and the `node_removals` record's three lists, which is the shape
// an explicit unset takes (F22, D2607). All nine name the `accounts` family,
// which is where `fugaro accounts` writes the cascade and records a removal
// (D2613), so the generic path carries none of them: the fallback is a word
// whose second answer spends the project's money, and a removal is a name held
// to the rule of the block it names.
//
// SA4.1 of docs/spend-accuracy/ is the last mover, by six rows and by none: a
// model price gains the three cache prices in dollars per million and the three
// keys that name the multiplier beside the old cache_read, cache_write_5m and
// cache_write_1h (D2646, R4). All six name the `models` family, which is where
// `fugaro models price set` writes an entry's prices (D2613), so the generic
// path carries none of them.
//
// R1 of docs/orchestrator-graph-estimates/ is the last mover, by two rows and by
// none: the orchestrator's switch `orchestrator_enabled` (D2701, D2739) and the
// project's issue adapter `issue_sync` (D2733) are top-level keys of the
// published policy node and of nothing else, so `policy` is the whole of their
// Layers — and both name the `project` family, the family whose command is the
// node's door, so the generic path refuses them in its name rather than writing
// them somewhere they do not live.
//
// B1.4 of docs/review-levels-ab-jobmax/ is the last mover, by one row and by
// none: the repository's `workflows.*.ab.reviewer_arm` is a workflow's ask for
// an A/B reviewer arm (O12), a key a person states in `fugaro.yaml` by hand and
// the launch reads, and no name and no prefix in the allowlist reaches it.
//
// W of docs/rigor-command/ moved it once more, by one row and by none: the local
// project config gains `rigor_pending`, the wizard's rigor answer waiting for
// the Layer stage that writes it into the project layer (D900), and its owner is
// `setup` — the door whose run is asked the question and writes the answer down,
// and whose Layer stage clears the key — so the generic path carries none of it.
//
// R14 of docs/periodic-reviews/ is the last mover, by two rows and by none: the
// local project config gains the `reports_commit` block, the one repository a
// report run may commit into and the folder inside it (D2750, D2751), and both
// rows name the `project` family — `fugaro project policy publish` is the door
// that carries the block onto the node, which is where a report run reads it —
// so the generic path carries neither.
//
// W2 of docs/config-ownership is the last mover, by twenty-one rows and by
// twenty-one: the governance block's own spelling — a stage's budget, token cap
// and timeout and a tier's three figures — is a row a person writes at the
// project layer and at the repository's own file (D2604), where the only
// concrete spelling before it was the layer file's own `layer.governance.*`, and
// the `governance.` prefix this list already names reaches all twenty-one. The
// block's four own rows stay as rows of the read and the write refuses their own
// spelling, so the named half does not move and the prefix-reached half carries
// one more for every leaf.
//
// S-KNOB of docs/review-against-task/ moved it once more, by one row and by one:
// the review-against-task switch `agent.review_against_task` is a repository-
// scope key of `fugaro.yaml`, read from the base branch, and the `agent.` prefix
// reaches it — the shape `agent.triage` beside it has — so the allowlist carries
// one key more and the rows without a generic writer do not move.
//
// RA8b of the same work moved both by one again: the switch's local carrier,
// the top-level `review_against_task` the owner's own file states and the
// publish copies into the policy node (R7, D2935), is a row `settings set`
// writes — the file is the machine's own, the shape the `triage` row above it
// has — so the named list carries one key more rather than reaching it by
// prefix.
//
// WS3 of docs/workspace-service/ is the last mover, by six rows and by none:
// the member's pin gains the address its runs call the workspace's credentials
// service at (R3), the member row gains where its database lives and splits its
// job service accounts into the account and the repositories it is a job of (R6,
// D2992), and the owner's shared list is three rows of its own (D1298). All six
// name the `workspace` family, whose writer is `fugaro workspace` and never the
// generic path (D853), so the allowlist carries no key of them and the rows
// without a generic writer move by the six.
//
// W3 of docs/config-ownership is the last mover, by two names and by none: the
// repository's `workflows.*.needs` (R1) and `workflows.*.ab.reviewer_arm` (B1.4)
// are rows whose owner column named the generic writer while no name in the
// allowlist reached them, so a concrete spelling was refused by the path its own
// row pointed at (F13), and naming the two is the whole of what makes a write
// land on them. `mobile_allow` — the block a person declares a phone in (D231,
// D2603) — is the third name and it moves no figure: the registry holds the
// three `mobile_allow.*` leaf rows and no row for the block, so the block is
// admitted by its own name and the row an entry write is about is built from its
// leaves rather than counted here.
//
// LP0 of docs/location-preference/ moved it by four rows and by none:
// the local project config gains the `locations` block, the record of where this
// machine read the installation's Firestore database, its Realtime Database, the
// replication of its secret containers and its run-log bucket to be (D3087), and
// it is one machine's own — never published, never an input — so no name and no
// prefix in the allowlist reaches any of its four rows.
//
// C1 of docs/batch-run-identity/ moved it after that, by three rows and by none:
// the `batch:` block gains its two identity facts — the opt-in fact
// (`batch.opt_in`) and the key fact, which is one fact in two fields
// (`batch.seal_key.version`, `batch.seal_key.public_key`, R13) — each owned by
// `fugaro update` and by no settings command, because they are the
// installation's facts and not a machine's preference. No name in the allowlist
// and no prefix of it reaches the three, so the rows without a generic writer
// move by the three.
//
// KS19 of docs/keys-through-service/ is the last mover after it, by two rows and by
// none: the record `fugaro update` writes on a repository's entry gains R12's
// two facts beside the switch it already carries
// (`repos.*.provider_keys_served`, `repos.*.stage_account_served`), and they
// are the same family as that switch — the `project` one, whose writer is
// `fugaro update` and never the generic path — so the allowlist reaches
// neither of them and the rows without a generic writer move by the two.
//
// LV3.1 of docs/rigor-modes/ is the last mover after them, and the first mover this
// paragraph records whose rows are mostly the generic path's: the project
// layer's per-repository levels (LD4, LF4) are `rigor.repos` in the block's own
// spelling — a row config.Scopes derives, whose handful of other rows all carry
// InRepo and this one does not, so its owner is the layer file's token rather
// than the generic writer's — beside the layer's `defaults.rigor.repos` row and
// the two rows an area rule's members are (LF4). The three layer rows are
// reached by the `layer.defaults.` prefix and the block's own is not carried, so
// four rows and three of them move the allowlist's figure.
//
// RT2 of docs/model-credentials-routing/ is the last mover after it, by six rows
// and by none: the project's routing table is the local config's own block, one
// row per entry (`model_routing.*.match`, `model_routing.*.chain.*.kind`, and
// the three members a kind may or may not use beside the `admit` list), and its
// writer is the converge — `fugaro update`'s migration writes a project's first
// table (MG2) and `fugaro routing set` edits one after that (RT5) — so neither a
// name in the allowlist nor a prefix of it reaches a row of it.
//
// PL1 of docs/model-credentials-routing/ is the last mover after it, by four
// rows and by none: a pool's `rule`, its `principal.kind` and `principal.id`
// (R21, D3181) and its `threshold` (D3178, refused wherever it is written until
// the rule that reads it ships) are rows of the local
// project config (`internal/localcfg/localcfg.go:298`) and of no other layer —
// not the repository's `fugaro.yaml` and not the published policy node, the
// same block RT2 above calls the local config's — and the allowlist's
// `pools.*.members` and `pools.*.strategy` names reach none of them, so the
// four join the rows with no generic writer.
//
// AB1 of docs/repo-override-rulings/ is the last mover after it, by one row and by
// none: the project layer's ab: block gains ab.repos (D3297), the per-repository
// sampling rate inside the block's own ab.rate, whose row is `layer.ab.repos` —
// the `models` family's, since the layer file is what `fugaro config publish`
// carries and not the generic path, whose allowlist reaches no key of that
// family — so the rows without a generic writer move by the one.
const (
	cfmRegistryRows   = 542
	cfmAllowlistNamed = 122
)

func TestDocsConfigManagementIsWritten(t *testing.T) {
	for _, path := range []string{cfmReadme, cfmSpec, cfmReconciled, cfmPlan} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s does not exist: %v", path, err)
		}
	}
	readme := flattenDoc(readDoc(t, cfmReadme))
	for _, want := range []string{
		cfmStatusLine,
		// The owner's ruling, so the index cannot drift into a paraphrase of it.
		"not to touch any of the configuration files by hand, be it repo, project, or local level",
		"we should have management commands in Fugaro just like with settings",
		// The block, and the fact that no decision in it is answered: a reader
		// who takes one thing from the index takes that.
		"**D1160 is therefore free.**",
		"D1160–D1183 are written here, and D1184–D1189 are reserved and unused.",
		"Fourteen tasks **G0–G13**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", cfmReadme, want)
		}
	}
	spec := flattenDoc(readDoc(t, cfmSpec))
	for _, want := range []string{
		cfmStatusLine,
		// Which document wins when the two disagree, because the spec is the
		// one a reader meets first and the reconciliation is the one that is
		// checked against the tree.
		"Where this file and [reconciliation.md](reconciliation.md) disagree, the reconciliation wins",
		"Recommendation: extend `fugaro settings set|unset` before any dedicated command is written** (D1160)",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q", cfmSpec, want)
		}
	}
	// The plan carries the order that orders every task in it. Without it the
	// plan reads as fourteen independent tasks, which is the one thing it is not.
	plan := flattenDoc(readDoc(t, cfmPlan))
	for _, want := range []string{
		cfmStatusLine,
		"**G0 → G1 → G2 → G3 → G4 → G5 → G6 → G7 → G8 → G9 → G10 → G11 → G12 → G13.**",
		// The slot, and why it is that one: the owner asked for this design
		// built behind managed mode.
		"**CLI 0.18.0**",
		"built behind managed mode",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q", cfmPlan, want)
		}
	}
}

// cfmDecisionRE finds a decision entry's opening, and nothing else: the
// reserved numbers are checked against it too, so a written decision in the
// tail fails here rather than passing as prose.
var cfmDecisionRE = regexp.MustCompile(`(?m)^- \*\*D([0-9]{4}) \(`)

// TestDocsConfigManagementDecisionsAreInRange: this work's block is written,
// every decision in it is open with a recommendation, and the block does not
// run past the numbers this work reserved.
func TestDocsConfigManagementDecisionsAreInRange(t *testing.T) {
	raw := readDoc(t, cfmReconciled)
	matches := cfmDecisionRE.FindAllStringSubmatchIndex(raw, -1)
	want := cfmBlockLast - cfmBlockFirst + 1
	if len(matches) != want {
		t.Fatalf("%s writes %d decisions and this work's block is D%d-D%d, %d of them: a decision number outside the block is a number taken without the owner giving it",
			cfmReconciled, len(matches), cfmBlockFirst, cfmBlockLast, want)
	}
	seen := map[int]bool{}
	for i, m := range matches {
		n, err := strconv.Atoi(raw[m[2]:m[3]])
		if err != nil {
			t.Fatalf("%s writes a decision number that is not a number: %v", cfmReconciled, err)
		}
		if n < cfmBlockFirst || n > cfmBlockLast {
			t.Errorf("%s writes D%d, outside this work's block D%d-D%d", cfmReconciled, n, cfmBlockFirst, cfmBlockLast)
		}
		if seen[n] {
			t.Errorf("%s writes D%d twice: a decision id is unique, and a second entry is either a duplicate or a renumbering", cfmReconciled, n)
		}
		seen[n] = true
		end := len(raw)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		block := flattenDoc(raw[m[0]:end])
		if !strings.Contains(block, "OPEN, recommended") {
			t.Errorf("%s' D%d is not written as open: every decision in this work's block is open with a recommendation, and an entry that reads as answered is a decision the design took without the owner", cfmReconciled, n)
			continue
		}
		if !strings.Contains(block, "Recommendation:") {
			t.Errorf("%s' D%d is open and carries no recommendation: an open decision without one is a question, not a decision, and the owner has nothing to answer", cfmReconciled, n)
		}
		if !strings.Contains(block, "Cost of the alternative:") {
			t.Errorf("%s' D%d carries no cost of the alternative: the brief asks every open decision for the price of the other reading, and a decision without one is a preference", cfmReconciled, n)
		}
	}
	for n := cfmBlockFirst; n <= cfmBlockLast; n++ {
		if !seen[n] {
			t.Errorf("%s does not write D%d: this work's block is D%d-D%d and every number in it is a decision a reader is owed", cfmReconciled, n, cfmBlockFirst, cfmBlockLast)
		}
	}
	// The reserved tail is written as no decision at all. Prose may name the
	// range; a bullet for one of its numbers would be a decision this work took
	// without the owner giving it.
	for n := cfmReservedFirst; n <= cfmReservedLast; n++ {
		if shape := "- **D" + strconv.Itoa(n) + " ("; strings.Contains(raw, shape) {
			t.Errorf("%s writes %q: D%d-D%d are reserved and unused, and a written decision in that range is a number this work took without the owner giving it", cfmReconciled, shape, cfmReservedFirst, cfmReservedLast)
		}
	}
}

// cfmCitationRE finds a backtick-quoted repository path with a line number. The
// extension list is the console-settings-edit folder's (`go`, `md`, `json`,
// `yaml`, `yml`, `tf`, `tmpl`, and the console's `ts`, `tsx`, `css`), because
// this folder's findings are about Go, about a JSON Schema, about Terraform's
// neighbours and about documents: a pattern written for `.go` alone would leave
// `schemas/settings-write.schema.json:20` unchecked while still passing its own
// census.
var cfmCitationRE = regexp.MustCompile("`([A-Za-z0-9_][A-Za-z0-9_./-]*\\.(?:go|md|json|ts|tsx|css|yaml|yml|tf|tmpl))(:[0-9]+)`")

// cfmCensusRE is the independent count of how many path-looking tokens carry a
// line number anywhere in them, deliberately looser than the citation regexp
// above: it does not care where the path starts. It is the floor the citation
// check is measured against, so a regexp that has stopped matching the
// documents' shapes fails the test instead of passing it vacuously.
var cfmCensusRE = regexp.MustCompile("`[^`\\n]*\\.(?:go|md|json|ts|tsx|css|yaml|yml|tf|tmpl)[^`\\n]*:[0-9]+`")

// TestDocsConfigManagementCitationsResolve: every file and line these documents
// cite exists. The audit is only worth as much as the code behind it, and a
// citation whose line moved turns a checked finding into an unchecked one with
// no other symptom.
func TestDocsConfigManagementCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range cfmDocs {
		doc := readDoc(t, path)
		// The census first, and independently: how many path-looking tokens with
		// a line the document holds at all. The check below has to see at least
		// that many, so a regexp that stopped matching a shape the prose uses
		// fails here rather than quietly checking less.
		census := len(cfmCensusRE.FindAllString(doc, -1))
		matches := cfmCitationRE.FindAllStringSubmatch(doc, -1)
		if len(matches) < census {
			t.Fatalf("%s holds %d path-looking tokens carrying a line and the citation check matched %d of them: the document cites a shape the citation regexp no longer knows, so those citations are unchecked",
				path, census, len(matches))
		}
		for _, m := range matches {
			file, ok := csrResolveCitation(m[1], bare)
			if !ok {
				t.Errorf("%s cites %q, which is not a file in this tree: a reconciliation whose citations have moved is a reconciliation nobody can check", path, m[1])
				continue
			}
			line, err := strconv.Atoi(strings.TrimPrefix(m[2], ":"))
			if err != nil {
				t.Errorf("%s cites %q%s, whose line is not a number: %v", path, m[1], m[2], err)
				continue
			}
			body, err := os.ReadFile("../../" + filepath.FromSlash(file))
			if err != nil {
				t.Errorf("%s cites %q, which could not be read, so the line is unchecked: %v", path, m[1], err)
				continue
			}
			// The trailing newline every file ends with is not a line of it.
			lines := len(strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"))
			if line < 1 || line > lines {
				t.Errorf("%s cites %q%s, but %s holds %d lines: line %d is past the end of the file, so the citation names a line that is not there",
					path, m[1], m[2], file, lines, line)
			}
		}
		t.Logf("%s: %d citations carrying a line, against a census of %d path-looking tokens with one", path, len(matches), census)
	}
}

// TestDocsConfigManagementRelativeCitationsResolve: every `:NNN` reference
// resolves against the nearest citation before it in its own paragraph, to a
// line that is in that file. The findings and the decisions lean on this shape
// -- a paragraph cites `internal/settings/registry.go:210` and then `:213` --
// and a reference whose anchor moved sends a reader to a line of the wrong file
// while the paragraph still reads as checked.
func TestDocsConfigManagementRelativeCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range cfmDocs {
		doc := readDoc(t, path)
		refs := 0
		for _, block := range strings.Split(doc, "\n\n") {
			for _, loc := range csrRelativeRE.FindAllStringIndex(block, -1) {
				anchor := ""
				for _, m := range cfmCitationRE.FindAllStringSubmatchIndex(block[:loc[0]], -1) {
					anchor = block[m[2]:m[3]] + block[m[4]:m[5]]
				}
				if anchor == "" {
					t.Errorf("%s holds %s with no citation before it in its own paragraph: a line reference resolves against the paragraph's nearest file, and there is none, so the reference names nothing",
						path, block[loc[0]:loc[1]])
					continue
				}
				cut := strings.LastIndex(anchor, ":")
				file, ok := csrResolveCitation(anchor[:cut], bare)
				if !ok {
					t.Errorf("%s holds %s anchored to %q, which is not a file in this tree", path, block[loc[0]:loc[1]], anchor[:cut])
					continue
				}
				refs++
				line, err := strconv.Atoi(strings.Trim(block[loc[0]:loc[1]], "`:"))
				if err != nil {
					t.Errorf("%s holds %s, whose line is not a number: %v", path, block[loc[0]:loc[1]], err)
					continue
				}
				body, err := os.ReadFile("../../" + filepath.FromSlash(file))
				if err != nil {
					t.Errorf("%s holds %s anchored to %q, which could not be read: %v", path, block[loc[0]:loc[1]], file, err)
					continue
				}
				lines := len(strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"))
				if line < 1 || line > lines {
					t.Errorf("%s holds %s anchored to %q, but %s holds %d lines: the reference names a line that is not there",
						path, block[loc[0]:loc[1]], anchor, file, lines)
				}
			}
		}
		t.Logf("%s: %d relative references resolved against their paragraph's anchor", path, refs)
	}
}

// TestDocsConfigManagementAllowlistCounts: the figure the audit's whole argument
// is made of is re-counted here rather than trusted. The registry holds 384 rows
// and the generic write path's allowlist carries eighty-eight of them by name
// plus the rows its nine prefixes reach; the documents state the registry's
// count and the allowlist's, and a row added to either -- which moves the gap
// between the two -- fails here rather than leaving §1's "219 rows have no
// generic writer" reading as checked when it is not.
//
// The count moved with G0, which gained the allowlist the wildcard blocks
// D1161 hands it and the two budget keys D1179 does, with the workspace rows
// main added, with O4, whose six orchestrator guardrail rows the generic path
// does not write -- they are the owner's policy, and a family that would write
// them does not exist yet -- and with R13, whose two jobs: rows it refuses for
// the same reason: turning a schedule on is the operator's own edit of the
// layer file. It moved once more with T-IS15, whose two issues.nudge_* rows are
// the local config file's own, and with SC7, whose security.patterns row is the
// repository file's: the audit's subject is the keys no writer carries, and a
// key added to the registry without one widens that gap rather than narrowing
// it. SC8's four rows are the layer's half of that block -- three row fields
// and always_level_3 -- and no writer carries them either: the layer file is
// `fugaro config publish`'s, not `fugaro settings set`'s.
// CB1 of docs/catalog-build-context/ moved it once more: its
// image_catalog.entries.*.source.context row is the image catalog's own, and no
// writer carries it. This branch's planning.decoupling is the last mover: the
// planning pass's own preference is two rows the merged tree adds --
// internal/settings/registry.go:301, and its layer row for defaults.planning at
// :542.
//
// E3 of docs/outer-loop-escalation/ is the last mover, by twelve rows and by
// twelve: the local ladder's `escalation.*` block is the machine's own — a
// threshold is authority, so no shared layer holds one — and the generic path is
// its door, which is what puts the owner's own hand and the coding agent's fence
// in front of it. Four of the twelve are the per-mode override rows the named
// list carries, and the eight scalars beside them (the four a mode reads, the
// cost ceiling, the critical-path factor, the judge's grade and the readiness
// switch) are reached by the block's own prefix.
//
// T2 of docs/bitbucket-token-types/ is the last mover, by six rows and by none:
// the local config's own `bitbucket_tokens.*` block is the machine's file, one
// row per member of a typed Bitbucket token's record, and no name or prefix in
// the allowlist reaches it.
//
// WS19 of docs/workspace-service/ is the last mover, by one row and by none:
// the owner's shared list gains `workspace_shared.*.all_members`, written by
// `fugaro workspace share --all-members` and by no name or prefix in the
// allowlist (the whole block is the `workspace` family's, D853).
//
// It counts through the same predicate the write path uses, so the test and the
// command cannot disagree about what "the allowlist carries" means: the three
// refusals settingsWriteRowHinted makes before the allowlist is asked are
// applied here in the same order.
func TestDocsConfigManagementAllowlistCounts(t *testing.T) {
	rows := settings.Rows()
	if len(rows) != cfmRegistryRows {
		t.Errorf("the registry holds %d rows and every document of this folder states %d: the figure the audit is argued from has moved", len(rows), cfmRegistryRows)
	}
	named, prefixed := 0, 0
	for _, r := range rows {
		if r.Sensitive {
			continue
		}
		if _, family := settingsFamilyCommands[r.Owner]; family {
			continue
		}
		if settingsGenericKeys[r.Key] {
			named++
			continue
		}
		if settingsGenericKey(r.Key) {
			prefixed++
		}
	}
	if named != cfmAllowlistNamed {
		t.Errorf("the allowlist names %d keys and the audit says %d: a key added to it is a key the audit's own subject changed", named, cfmAllowlistNamed)
	}
	carried := named + prefixed
	// Each document states the figure in its own sentence, so each is asked for
	// its own wording: the audit's finding states the allowlist's count, and the
	// index states the gap the ruling is about.
	for path, wants := range map[string][]string{
		cfmReconciled: {
			strconv.Itoa(len(rows)) + " rows",
			strconv.Itoa(carried) + " of them",
		},
		cfmReadme: {
			strconv.Itoa(len(rows)) + " rows",
			strconv.Itoa(carried) + " of the registry's",
			strconv.Itoa(len(rows)-carried) + " rows have no generic writer",
		},
	} {
		doc := flattenDoc(readDoc(t, path))
		for _, want := range wants {
			if !strings.Contains(doc, want) {
				t.Errorf("%s lacks %q: the audit's figure is re-counted here and the document has to carry the number the count produced (%d named + %d by prefix = %d, leaving %d with no writer)",
					path, want, named, prefixed, carried, len(rows)-carried)
			}
		}
	}
}

// TestDocsRigorModesReconciliationCountsTheConfigManagementPin: the census row
// that counts these folders' pins is a running total of other folders' guards,
// and its own text says a folder writing one moves the row in its own commit.
// This folder writes one, so the row is checked twice: that it carries the
// number the tree holds, and that its amendment sentences name this folder's pin
// -- a row moved without the sentence beside it is a figure a reader cannot
// trace to the commit that moved it.
func TestDocsRigorModesReconciliationCountsTheConfigManagementPin(t *testing.T) {
	const path = "docs/rigor-modes/reconciliation.md"
	doc := readDoc(t, path)
	row := mdRow(t, path, doc, "| Docs folders that ship their own `internal/cli/docs_*_test.go` pin |")
	pins, err := filepath.Glob("../../internal/cli/docs_*_test.go")
	if err != nil {
		t.Fatalf("the folders' pins could not be listed, so the census row cannot be checked: %v", err)
	}
	// The count the row states, read out of the row itself rather than assumed:
	// the columns are `| construct | count | command |`.
	cols := strings.Split(row, "|")
	if len(cols) < 4 {
		t.Fatalf("%s' census row is not a three-column row: %q", path, row)
	}
	stated := strings.TrimSpace(cols[2])
	if stated != strconv.Itoa(len(pins)) {
		t.Errorf("%s' census row states %s rows and the tree holds %d `internal/cli/docs_*_test.go` files: the row is a count a reader re-runs, so a folder that added a pin moves it", path, stated, len(pins))
	}
	if !strings.Contains(doc, "the configuration-management folder's\npin, moved the table's row from 92 to 93.") {
		t.Errorf("%s does not carry the amendment sentence naming this folder's pin: the row's own text says a folder that writes one moves the row in its own commit, and an amendment the row does not record is the merge that did not", path)
	}
}

// TestDocsConfigManagementNoSecondWritePath: the property the plan states for
// every task it adds -- one write path, the diff, the confirmation, the fence,
// the backup and the compare-and-swap, in `applySettingsEdit` -- and the ten
// commands whose tasks carry it. The property is a claim about code that does
// not exist yet, so this is a pin on the plan's own words and not a source
// check: what it asserts is that the plan states the property, names the helper,
// and names the same ten tasks in its fifth section as it writes headings for.
func TestDocsConfigManagementNoSecondWritePath(t *testing.T) {
	plan := readDoc(t, cfmPlan)
	for _, want := range []string{
		"*Every* write here goes through `applySettingsEdit`",
		"`internal/cli/settings_write.go:86`",
		"none of the ten commands in §5 carries a second one",
	} {
		if !strings.Contains(flattenDoc(plan), want) {
			t.Errorf("%s lacks %q: the one-write-path property is the reason no task here re-implements the diff, the fence or the compare-and-swap", cfmPlan, want)
		}
	}
	// §5 names the tasks that need the managed build's gate and its audit
	// writer, and the two that add to `settings set` without one. Each of the ten
	// is a task of this plan, and each has a heading -- a task a section names and
	// the plan never writes is a dependency on nothing.
	start := strings.Index(plan, "## 5. The tasks that need")
	end := strings.Index(plan, "## G0. ")
	if start < 0 || end < start {
		t.Fatalf("%s has no fifth section, or no first task heading after it: the section that says which tasks wait on the managed build is the one a reader checks the order against", cfmPlan)
	}
	section := plan[start:end]
	for _, task := range cfmGateTasks {
		if !regexp.MustCompile(`\b` + task + `\b`).MatchString(section) {
			t.Errorf("%s' §5 does not name %s: the section lists the tasks that call the managed build's gate or its audit writer and the tasks that need neither, so a task it leaves out is one whose dependency a reader cannot see", cfmPlan, task)
		}
		if !strings.Contains(plan, "\n## "+task+". ") {
			t.Errorf("%s names %s in §5 and writes no task heading for it", cfmPlan, task)
		}
	}
}

// TestDocsConfigManagementKeepsTheSettingsWriteVersion: the settings-write
// document is read by a settings view that pins its version -- its
// `SETTINGS_VIEW_DOC_VERSION` is 1 -- so the design may not bump it, and the
// two documents have to say so and name the file that pins it. A design that
// planned a version 2 would break every settings edit that view makes, with no
// other symptom. The file is pinned by its own name rather than by its path:
// this is Go in the CLI's module, and a reference under the console's directory
// is the boundary another test guards (Task 0.3's allow-list).
func TestDocsConfigManagementKeepsTheSettingsWriteVersion(t *testing.T) {
	for _, path := range []string{cfmSpec, cfmReconciled} {
		doc := flattenDoc(readDoc(t, path))
		if !strings.Contains(doc, "settingsview.ts:42") {
			t.Errorf("%s plans the settings-write document's members without naming the settings view's own version pin (`settingsview.ts:42`): the pin is why the version cannot move", path)
		}
		for _, stale := range []string{"gains a version 2", "moves to version 2", "reads a version-2 document"} {
			if strings.Contains(doc, stale) {
				t.Errorf("%s still plans a version 2 of the settings-write document (%q): the settings view refuses any version but 1, so a bump breaks the edit it spawns `settings set` behind", path, stale)
			}
		}
	}
}

// TestDocsConfigManagementLeavesTheBudgetKeysWithTheGenericWriter: the four
// budget keys are a repository's own rows, and `fugaro budget set` writes the
// budget database's caps rather than the repository's fugaro.yaml. The rows are
// re-read from the scope table and the claim from the documents, so a design
// that hands the four to `fugaro budget set` -- which would refuse the two keys
// the generic path writes today -- fails here instead of at a person's settings
// edit.
func TestDocsConfigManagementLeavesTheBudgetKeysWithTheGenericWriter(t *testing.T) {
	scopes := flattenDoc(readDoc(t, "internal/config/scope.go"))
	for _, key := range []string{"budget.mode", "budget.per_run_usd", "budget.allowed_models", "budget.per_day_usd"} {
		if !strings.Contains(scopes, `{Key: "`+key+`", In: InRepo`) {
			t.Errorf("internal/config/scope.go no longer holds %s as an InRepo row, so the design's reading of the four budget keys has moved", key)
		}
	}
	for _, path := range []string{cfmSpec, cfmReconciled} {
		doc := flattenDoc(readDoc(t, path))
		if !strings.Contains(doc, "stay the generic writer's") {
			t.Errorf("%s does not say the four budget keys stay the generic writer's: naming `fugaro budget set` for them refuses the two the allowlist carries today", path)
		}
		if strings.Contains(doc, "are owned by `fugaro budget set`") {
			t.Errorf("%s hands the budget caps to `fugaro budget set`, which writes the budget database and no registry row", path)
		}
	}
}

// cfmGateTasks is the ten tasks of the plan that add a command or an operation
// -- "the ten commands in §5", as that plan's §6 calls them. Its fifth section
// groups all fourteen by the managed build's gate and audit calls and is the
// reason the ten can be read off it: G2 adds a document's members, G5 rides a
// stage of `update`, G12 renames an owner string and G13 writes these rows, and
// the other ten each extend a command this design manages. The spec's §5 table
// owes every one of them a row and owes a row to nothing else, so this list is
// the left-hand side of that equality and
// `TestDocsConfigManagementNoSecondWritePath` reads the same list.
var cfmGateTasks = []string{"G0", "G1", "G3", "G4", "G6", "G7", "G8", "G9", "G10", "G11"}

// cfmGateRow is one row the spec's fifth section owes: the task of the plan
// that adds the command, and the row's first cell exactly as the table writes
// it -- a backticked command or operation and the section of this spec that
// designs it, and nothing else.
//
// The cell is pinned whole rather than as a token because the three rows the
// generic writer owes (§2.1, §2.2 and the layer file of §3.6) all name
// `settings set|unset` and are told apart only by what they do: a table with
// one row for the three would read as a command with one rule, and one of the
// three is the layer write that publishes.
type cfmGateRow struct {
	task string
	cell string
}

// cfmGateRows is the row set the spec's fifth section has to hold, in the
// plan's own order.
var cfmGateRows = []cfmGateRow{
	{"G0", "`settings set|unset` with a **concrete** key (§2.1)"},
	{"G1", "`settings set|unset` with `--add-entry` / `--remove-entry` (§2.2)"},
	{"G3", "`fugaro workflow add|rm` (§3.7)"},
	{"G4", "`settings set --layer project` (§3.6)"},
	{"G6", "`fugaro rigor set` (§3.1)"},
	{"G7", "`fugaro rigor floor` (§3.1)"},
	{"G8", "`fugaro secrets rm`, the secret policy flags (§3.2)"},
	{"G9", "`fugaro accounts rm` (§3.3)"},
	{"G10", "`fugaro project repos set` (§3.4)"},
	{"G11", "`fugaro image add --image` (§3.5)"},
}

// cfmGateFamilies ties a row's first cell to the family command the tree
// already names for it: `settingsFamilyCommands` is what refuses a write and
// points its key at that family (`internal/cli/settings_set.go:537`), so a row
// spelled some other way would be a table whose command no refusal answers
// with. A row extends its family's own spelling -- `fugaro rigor set` is the
// spelling itself, `fugaro secrets rm` adds a subcommand to `fugaro secrets` --
// and the rows this map leaves out are the three the plan reaches without a
// registry family behind them: the generic writer, the workflow group and the
// image catalog's `add`.
var cfmGateFamilies = map[string]string{
	"`fugaro rigor set` (§3.1)":                           "fugaro rigor set",
	"`fugaro rigor floor` (§3.1)":                         "fugaro rigor floor",
	"`fugaro secrets rm`, the secret policy flags (§3.2)": "fugaro secrets",
	"`fugaro accounts rm` (§3.3)":                         "fugaro accounts",
	"`fugaro project repos set` (§3.4)":                   "fugaro project",
}

// cfmGateNewGroups are the rows whose command belongs to no family the registry
// names: the generic writer the whole design extends, the group `fugaro
// workflow` G3 creates, and the image catalog's `add`, which G11 gives a flag
// rather than a command. A row in this list that a family did claim would be a
// command the write path already refuses keys to, which is the one thing these
// three are not.
var cfmGateNewGroups = []string{
	"`settings set|unset` with a **concrete** key (§2.1)",
	"`settings set|unset` with `--add-entry` / `--remove-entry` (§2.2)",
	"`settings set --layer project` (§3.6)",
	"`fugaro workflow add|rm` (§3.7)",
	"`fugaro image add --image` (§3.5)",
}

// cfmDecisionIDRE finds a decision id, in either shape this corpus writes one
// (`D1026`, `D1015(e)`).
var cfmDecisionIDRE = regexp.MustCompile(`\bD[0-9]{3,4}\b`)

// cfmSection is the body of a document's numbered section, up to the next `##`
// heading. A section read by its heading rather than by an offset is one a
// later paragraph can grow without moving what this test reads.
func cfmSection(t *testing.T, path, doc, heading string) string {
	t.Helper()
	start := strings.Index(doc, "\n"+heading)
	if start < 0 {
		t.Fatalf("%s has no %q section: this case reads the table inside it", path, heading)
	}
	body := doc[start+1:]
	if end := strings.Index(body, "\n## "); end >= 0 {
		body = body[:end]
	}
	return body
}

// cfmMarkdownCells splits one markdown table row into its cells, with `\|` read
// as an escaped pipe: the table writes `settings set|unset` as
// `settings set\|unset`, and a split on the bare byte would cut that cell in
// two and shift every column after it. The escape is what a reader sees
// rendered, so the cell a caller compares against carries a plain `|`.
func cfmMarkdownCells(line string) []string {
	line = strings.TrimPrefix(strings.TrimSpace(line), "|")
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cur.WriteByte('|')
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(line[i])
		}
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	if n := len(cells); n > 0 && cells[n-1] == "" {
		cells = cells[:n-1]
	}
	return cells
}

// cfmSectionTable returns every data row of the table a section opens with, the
// header row and the separator dropped. The header is passed in rather than
// assumed, so a section that grows a second table fails here instead of being
// read as one table of both.
func cfmSectionTable(t *testing.T, path, section, header string) [][]string {
	t.Helper()
	var out [][]string
	for _, raw := range strings.Split(section, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := cfmMarkdownCells(line)
		if len(cells) < 2 || cells[0] == header || strings.HasPrefix(cells[0], "---") {
			continue
		}
		out = append(out, cells)
	}
	if len(out) == 0 {
		t.Fatalf("%s' %s table has no data row: the table this case reads is the one that answers %q", path, header, header)
	}
	return out
}

// cfmGateTable is the spec's fifth section as rows of cells.
func cfmGateTable(t *testing.T) [][]string {
	t.Helper()
	return cfmSectionTable(t, cfmSpec, cfmSection(t, cfmSpec, readDoc(t, cfmSpec), "## 5. The managed gate's rows"), "New command or operation")
}

// cfmCellCommand is the command or operation a gate row's first cell names: its
// first backticked span. A cell that opens with no backticked span names no
// command, and the row is checked as one below.
func cfmCellCommand(cell string) string {
	open := strings.Index(cell, "`")
	if open < 0 {
		return ""
	}
	rest := cell[open+1:]
	close := strings.Index(rest, "`")
	if close < 0 {
		return ""
	}
	return rest[:close]
}

// cfmExtendsCommand reports whether a family's command is the same as a row's
// command or a whole-word prefix of it: `fugaro secrets` extends to `fugaro
// secrets rm`, and `fugaro image map` is not a prefix of `fugaro image add`.
func cfmExtendsCommand(family, command string) bool {
	return command == family || strings.HasPrefix(command, family+" ")
}

// TestDocsConfigManagementRowsCoverEveryNewCommand: the spec's fifth section is
// the row set the managed build cites (D1180), so it holds exactly one row per
// command or operation this plan adds and a row for nothing else. What the plan
// adds is the ten tasks of its own fifth section, and each row has to name the
// command that task's interface writes: a task the table leaves out is a
// command an agent's session reaches with no rule of the gate's over it, which
// is the hole the rows exist to close.
func TestDocsConfigManagementRowsCoverEveryNewCommand(t *testing.T) {
	rows := cfmGateTable(t)
	seen := map[string]bool{}
	for _, cells := range rows {
		if seen[cells[0]] {
			t.Errorf("%s' §5 table writes the row %q twice: one row per command or operation, and a second row for one is two answers to what the gate admits", cfmSpec, cells[0])
		}
		seen[cells[0]] = true
	}
	if len(rows) != len(cfmGateRows) {
		t.Errorf("%s' §5 table holds %d rows and the plan writes %d tasks that add a command or an operation: the table is that list's row set, one row each", cfmSpec, len(rows), len(cfmGateRows))
	}
	answered := map[string]bool{}
	for _, want := range cfmGateRows {
		if !seen[want.cell] {
			t.Errorf("%s' §5 table has no row for %s, the task that adds %s: a command the plan adds and the table does not carry is one no gate row names", cfmSpec, want.task, want.cell)
		}
		if answered[want.task] {
			t.Errorf("%s' §5 table owes %s two rows", cfmSpec, want.task)
		}
		answered[want.task] = true
	}
	// The rows answer for the plan's own list, both ways: a row for a task the
	// plan does not write is a command nobody adds.
	plan := readDoc(t, cfmPlan)
	for _, want := range cfmGateRows {
		if !strings.Contains(plan, "\n## "+want.task+". ") {
			t.Errorf("%s' §5 table carries a row for %s and the plan writes no such task heading", cfmSpec, want.task)
		}
	}
	for _, task := range cfmGateTasks {
		if !answered[task] {
			t.Errorf("the plan writes %s as a task that adds a command and the spec's fifth section owes it no row: the two lists are the same set, and one a reader cannot pair is a command whose gate rule nobody wrote", task)
		}
	}
	// A row that extends one of the registry's families is spelled from that
	// family, and the rows that extend none are the three no family claims.
	for _, cells := range rows {
		command := cfmCellCommand(cells[0])
		if command == "" {
			t.Errorf("%s' §5 table row %q opens with no backticked command or operation: the first column is the command the row is about", cfmSpec, cells[0])
			continue
		}
		if family, owned := cfmGateFamilies[cells[0]]; owned {
			if !cfmExtendsCommand(family, command) {
				t.Errorf("%s' §5 table spells the row %q as %q, and the family command settingsFamilyCommands answers with is %q: the table and the refusal that points a key at its family would then disagree", cfmSpec, cells[0], command, family)
			}
			continue
		}
		for _, family := range settingsFamilyCommands {
			if cfmExtendsCommand(family, command) {
				t.Errorf("%s' §5 table calls %q a group this design reaches without a family behind it, and settingsFamilyCommands makes %q a family's own command: a family's keys are refused by the generic path", cfmSpec, cells[0], family)
				break
			}
		}
	}
	for _, cell := range cfmGateNewGroups {
		if !seen[cell] {
			t.Errorf("%s' §5 table holds no row for the group %q: this case names the rows no family claims, and one the table dropped is a command the check above stops covering", cfmSpec, cell)
		}
	}
}

// TestDocsConfigManagementEveryRowNamesItsRefuser: every row of the spec's
// fifth section is filled in and says who refuses what its gate does not admit
// (D1180), and a refuser here is a decision this design already wrote -- the
// class rule of D1026, the design of the command the row is about, or the
// refusal that command's own section states. A row whose third column names no
// decision is a rule the table invented, and this table is what the managed
// build's gate is read from.
func TestDocsConfigManagementEveryRowNamesItsRefuser(t *testing.T) {
	for _, cells := range cfmGateTable(t) {
		if len(cells) != 3 {
			t.Errorf("%s' §5 table row %q splits into %d cells and the table has three columns: a row that shifted a column is one whose admission and refusal a reader reads as each other", cfmSpec, cells, len(cells))
			continue
		}
		for i, name := range []string{"command", "admission", "refuser"} {
			if strings.TrimSpace(cells[i]) == "" || cells[i] == "—" {
				t.Errorf("%s' §5 table row %q carries no %s: every cell of the row is an answer D1180 asks for", cfmSpec, cells[0], name)
			}
		}
		if !cfmDecisionIDRE.MatchString(cells[2]) {
			t.Errorf("%s' §5 table row %q names no decision in its refuser column (%q): every refusal this table states is one a decision of this design already wrote, and a row that states one without it is a rule the table invented", cfmSpec, cells[0], cells[2])
		}
	}
}

// TestDocsConfigManagementInstallationFactsHaveNoWriter: the owner table D1182
// asks for lists the installation's facts -- the registry rows `setup` and
// `update` own -- and every one of them is a fact no writer carries: refused by
// the generic path, owned by no family, and (where the table names one) written
// by a converge flag the two doors really have. The set is re-counted from the
// registry rather than trusted, so a row added to either side fails here rather
// than leaving the table's claim about the installation unchecked.
func TestDocsConfigManagementInstallationFactsHaveNoWriter(t *testing.T) {
	facts := cfmInstallationFacts(t)
	// The facts the table owes, through the write path's own refusals
	// (settingsRefuseRow), in the order it asks them: the sensitive rows and
	// the rows a family owns are out, and so is every row the allowlist
	// carries. Its `*` refusal is the one this predicate does not apply,
	// because a registry row's own spelling carries the `*` and the fact the
	// table lists is the row: a wildcard row the allowlist does not name has
	// no writer for any of its entries, which is what a row here says.
	want := map[string]bool{}
	for _, r := range settings.Rows() {
		if r.Owner != "setup" && r.Owner != "update" {
			continue
		}
		if r.Sensitive {
			continue
		}
		if _, family := settingsFamilyCommands[r.Owner]; family {
			continue
		}
		if settingsGenericKey(r.Key) {
			continue
		}
		want[r.Key] = true
	}
	for key, flag := range facts {
		if !want[key] {
			reason := "it is not a registry row at all"
			if row, ok := settings.Lookup(key); ok {
				switch {
				case row.Sensitive:
					reason = "its row is sensitive, and no fact in this table has a value a diff may print"
				case row.Owner != "setup" && row.Owner != "update":
					reason = "its row's owner is " + strconv.Quote(row.Owner) + ", which is not setup's or update's"
				case settingsGenericKey(key):
					reason = "the generic write path already carries it, so it is a key with a writer and not an installation fact"
				default:
					reason = "a family already owns it"
				}
			}
			t.Errorf("%s' §6 table lists %s as an installation fact with no writer, and the tree disagrees: %s", cfmSpec, key, reason)
			continue
		}
		delete(want, key)
		if flag == "" {
			continue
		}
		if newSetupCmd().Flags().Lookup(flag) == nil || newUpdateCmd().Flags().Lookup(flag) == nil {
			t.Errorf("%s' §6 table says --%s writes %s, and that flag is not on both converge doors: a table that points a reader at a flag neither door has sends them to a command line that does not exist", cfmSpec, flag, key)
		}
	}
	for key := range want {
		t.Errorf("%s' §6 table carries no row for the installation fact %s: the table is the owner table D1182 asks for, and a fact it leaves out is one a person reads as editable", cfmSpec, key)
	}
	if !strings.Contains(flattenDoc(readDoc(t, cfmSpec)), "A fact with no flag today waits for a flag rather than for an editor") {
		t.Errorf("%s does not carry the note D1182 puts beside this table: a `—` cell without it reads as a fact nothing writes rather than one that waits for the flag that feeds the converge", cfmSpec)
	}
}

// cfmInstallationFacts reads the spec's §6 table: the registry key each row
// names, and the converge flag the row gives it -- the flag's own name without
// its dashes, or "" for the rows the table leaves to the converge.
func cfmInstallationFacts(t *testing.T) map[string]string {
	t.Helper()
	spec := readDoc(t, cfmSpec)
	section := cfmSection(t, cfmSpec, spec, "## 6. What stays a setup/update-owned installation fact")
	out := map[string]string{}
	for _, cells := range cfmSectionTable(t, cfmSpec, section, "Installation fact") {
		if len(cells) != 2 {
			t.Fatalf("%s' §6 table row %q splits into %d cells and the table has two columns", cfmSpec, cells, len(cells))
		}
		key := strings.Trim(cells[0], "`")
		if key == "" || strings.ContainsAny(key, " ()") {
			t.Fatalf("%s' §6 table row %q does not open with one backticked registry key", cfmSpec, cells[0])
		}
		flag := strings.TrimSpace(cells[1])
		switch {
		case flag == "—":
			flag = ""
		case strings.HasPrefix(flag, "`--") && strings.HasSuffix(flag, "`"):
			flag = strings.Trim(flag, "`")[2:]
		default:
			t.Fatalf("%s' §6 table row for %s names neither a `--flag` nor an em dash: %q", cfmSpec, key, cells[1])
		}
		if _, dup := out[key]; dup {
			t.Fatalf("%s' §6 table lists %s twice: a second row for one fact is a second answer to what writes it", cfmSpec, key)
		}
		out[key] = flag
	}
	return out
}

// cfmGateKey is one setting key a §5 row's cells name, in two spellings: the
// one the cell writes, checked against the cell so the table and this fixture
// move together, and a concrete spelling the write path can resolve. `entry`
// says the write is an `--add-entry` / `--remove-entry` of a block rather than
// a scalar, because that is the mechanism whose row the write path builds for
// itself (settingsEntryRow) rather than finding in the registry.
type cfmGateKey struct {
	spelling string
	probe    string
	entry    bool
}

// cfmGateAdmittedKeys is the keys each row's admission column carries, and
// cfmGateRefusedKeys the ones its refuser column does: a row that admits what
// the write path refuses, or refuses what it writes, is a gate table wider or
// narrower than the commands it is read from (D1180). Both are keyed by the
// row's first cell, so a renamed row fails here rather than losing its check.
//
// The G4 row is why this exists. Its first draft admitted `ab.*` and
// `claude_models` as generic layer writes; the tree refuses both -- they are
// the `fugaro models` family's rows and the family refusal is asked before the
// allowlist -- so the table claimed cover the write path does not give.
var cfmGateAdmittedKeys = map[string][]cfmGateKey{
	"`settings set|unset` with a **concrete** key (§2.1)": {
		{spelling: "budget.mode", probe: "budget.mode"},
		{spelling: "budget.per_day_usd", probe: "budget.per_day_usd"},
	},
	"`settings set --layer project` (§3.6)": {
		{spelling: "layer.profiles.*", probe: "layer.profiles.web.commands.rerun_failed", entry: true},
		{spelling: "layer.defaults.*", probe: "layer.defaults.agent.max_budget_usd"},
		{spelling: "layer.ab", probe: "layer.ab", entry: true},
	},
}

var cfmGateRefusedKeys = map[string][]cfmGateKey{
	"`settings set --layer project` (§3.6)": {
		{spelling: "layer.claude_models", probe: "layer.claude_models"},
		{spelling: "layer.ab.*", probe: "layer.ab.rate"},
		{spelling: "layer.defaults.agent", probe: "layer.defaults.agent.model"},
	},
}

// cfmGateResolve is the write path's own answer for one key: the entry
// mechanism's row for a block write, and the registry's for a scalar -- the
// same two resolutions `settings set` itself makes, refusals and all.
func cfmGateResolve(key cfmGateKey) error {
	if key.entry {
		_, err := settingsEntryRow(key.probe)
		return err
	}
	_, err := settingsWriteRow(key.probe)
	return err
}

// TestDocsConfigManagementAdmittedKeysPassTheWritePath: every key a §5 row
// admits is a key the write path really writes, and every key a row refuses is
// one it really refuses. The table is what the managed build's gate cites
// (D1180), so a row that admits more than `settings set` does claims cover the
// agent's session does not have -- and a row that refuses less than the tree
// does is the same hole read the other way. Each fixture key also has to appear
// in the cell it belongs to, so a row cannot be reworded out from under it.
func TestDocsConfigManagementAdmittedKeysPassTheWritePath(t *testing.T) {
	cells := map[string][]string{}
	for _, row := range cfmGateTable(t) {
		if len(row) == 3 {
			cells[row[0]] = row
		}
	}
	for _, tc := range []struct {
		name  string
		fixt  map[string][]cfmGateKey
		col   int
		admit bool
	}{
		{"admits", cfmGateAdmittedKeys, 1, true},
		{"refuses", cfmGateRefusedKeys, 2, false},
	} {
		for cell, keys := range tc.fixt {
			row, ok := cells[cell]
			if !ok {
				t.Errorf("%s' §5 table has no row %q, and this case pins the keys that row %s: a row renamed is a check that stopped running", cfmSpec, cell, tc.name)
				continue
			}
			for _, key := range keys {
				if !strings.Contains(row[tc.col], "`"+key.spelling+"`") {
					t.Errorf("%s' §5 table row %q %s no %q: the fixture and the table have to name the same key, and this one is pinned against a sentence that does not carry it", cfmSpec, cell, tc.name, key.spelling)
					continue
				}
				err := cfmGateResolve(key)
				if tc.admit && err != nil {
					t.Errorf("%s' §5 table row %q admits %s and the write path refuses it: %v -- the gate would admit a command whose write is refused before the gate is ever asked", cfmSpec, cell, key.probe, err)
				}
				if !tc.admit && err == nil {
					t.Errorf("%s' §5 table row %q refuses %s and the write path writes it: a row that refuses a key its own command writes is a rule the tree does not have", cfmSpec, cell, key.probe)
				}
			}
		}
	}
}
