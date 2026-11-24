package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The published policy node an older Fugaro wrote, carried over to the version
// this one reads, IN PLACE: the policy stage of `fugaro update`'s converge, and
// the one door into the two functions below since U4 of
// docs/plugin-user-scope/implementation-plan.md retired the second command. The
// diff a check shows and the write an apply makes are those functions', so the
// two cannot drift.
//
// It exists for the owner's rule of 2026-10-30 — `fugaro update` is the whole
// upgrade, and it works perfectly. Before this, a bump of projectpolicy.Version
// (0.14.0 raised it to 4) left every installation holding an older node with
// one way forward, 'fugaro project policy publish --delete' and a fresh
// publish: a deletion, and one that loses every field the owner had edited into
// the node by hand (allow_repos, clone_urls, routing, profiles, accounts,
// blocks no release models). The migration moves the node instead
// (projectpolicy.Migrate) and adds what the version this Fugaro writes needs
// and the node alone cannot state: the bitbucket_token block and
// bitbucket_api_roots, derived from the project's own local config with the
// publisher's own functions.
//
// The write is a writer's write, so it holds the writer's posture (P8): a node
// carrying keys this Fugaro does not know is refused rather than rewritten, and
// the refusal names the count and never a value. Migrate preserves those keys —
// its Change.Kept is read off the node for exactly that reason — so the refusal
// is this stage's decision and not the migration's: a node whose unknown blocks
// only a newer Fugaro can read is that Fugaro's to migrate.

// policyMigration is one read of the published node and the node it would be
// written as: the plan the operator is shown, and the write itself. Everything
// the write needs is read once, here, because the ETag the write is conditional
// on is the ETag of the node the operator read the diff against — a second read
// between the confirmation and the write would make the confirmed diff and the
// written document two different things.
type policyMigration struct {
	lc      *localcfg.Config
	db      *rtdb.Client
	etag    string // the published node's ETag, as read; the write is conditional on it
	from    int
	moved   []byte // the node as it will be written: Migrate's output, plus what was derived
	changes []projectpolicy.Change
	derived []string // the top-level members derived from the local config, sorted

	// identities is what the creds state store holds for the project's typed
	// Bitbucket tokens, keyed by each token's document key (R12). It is read
	// once, from the same database this plan's node came from, because three
	// readers of one plan need it — the migration's derived members, the drift
	// comparison and the write's own verification — and a second read of a
	// store the plan already had in hand would be a second state.
	identities map[string]string

	// node is the node as it stands: the migrated document when there is a
	// migration, the published one otherwise. It is what the publish and the
	// re-seed read, so both work on the document the operator's diff showed.
	node *projectpolicy.Policy
	// drift is what the local config states and the node does not carry: the
	// publish's reason to exist, and what the re-seed writes back.
	drift []policyDrift
	// generations is set when the published node has moved past the one this
	// machine's config was written from (D854): the publish is refused, and the
	// stage offers to re-seed instead of writing.
	generations *policyGenerationState
	// pub is the node the local config states, when the published one does not
	// carry it. It is built on top of the migrated node when there is one, so
	// the one write of this run carries both.
	pub *policyPublish
	// path is the local config file this machine's policy keys live in: where
	// the published generation is recorded and where a re-seed writes. It is
	// empty when the run holds no such file, and the publish half then writes
	// nothing rather than recording a baseline in a file it was not built from.
	path string
	// record is the generation this run records in this machine's file when it
	// writes no node at all: the file and the node agree, and the generation the
	// file records as the one its policy keys were written from is not the node's
	// own. It is empty whenever the run writes the node — the record then follows
	// that write (recordGeneration) — and when the file is not this installation's
	// to write (path empty) or already records the node's generation.
	record string
	// recorded is what this machine's file recorded as the generation its policy
	// keys were written from when this plan was read: the value `record` is
	// compared against and the one the check line names. It is read here rather
	// than off the config later, because the plan is one read of one state — a
	// re-seed rewrites the file's policy keys under the same config struct, and
	// a plan that read them again afterwards would be comparing two states.
	recorded string

	// importCloneURLs is the published node's clone_urls block, when this
	// machine's file states none of its own and there is a file to write it
	// into (D842, D844). It is nil in the common case: an installation whose
	// file already states the block, and one whose node carries none — the
	// case D843 words, where no default is seeded into a file nobody wrote.
	// A plan that holds it writes the file and not the node, which is why it
	// is read here beside the plan rather than discovered at the write.
	importCloneURLs map[string]string

	// firstPublish is whether the write this plan holds is the project's FIRST
	// published node: nothing is at the path today, so the write creates it
	// rather than replacing one. The distinction is not decoration — every other
	// sentence of this stage is worded for a node that is already there ("this
	// rewrites the published policy node in place", the diff against it, the
	// generation the file was written from) — and the publish that admits no
	// repository is confirmed differently when it is the first (#1674).
	firstPublish bool
}

// policyNodeUnreachable is whether this project has no published policy node to
// read at all. The node lives in the project's Realtime Database, so a config
// that names no budget backend has none — not a failure of this stage but the
// absence of the thing it migrates, which is why it is a skip and not an error
// (a config written before the budget backend, or one whose operator never
// configured it, is a state `fugaro update` must not stop on).
func policyNodeUnreachable(lc *localcfg.Config) bool {
	return lc.Budget == nil || lc.Budget.RTDBURL == ""
}

// policyMigrationNotOurs is the answer policyMigrationFor gives for a database
// the shared ownership predicate refuses (budgetDBUnadopted): there is nothing
// to migrate HERE, and the callers turn it into the skip their own vocabulary
// has — a skipped stage rather than a refusal — rather than into an error,
// because the rest of the run has
// nothing to do with that database, exactly as the rules stage's skip is not a
// refusal.
//
// It is a type and not a plain error so a caller cannot mistake it for a read
// that failed (the two are the only two things this function returns): a failed
// read is the run's error, and this is a state the operator resolves with the
// sentence the refusal carries — Check's own words, or the command the config's
// half names.
type policyMigrationNotOurs struct{ reason string }

func (e *policyMigrationNotOurs) Error() string { return e.reason }

// policyMigrationFor reads the published node and works out what it would be
// written as. published is whether a node is there at all — a project with a
// budget backend and no published policy is a state of its own, and one the
// delete and show paths already word ("runs fall back to the ceiling their
// job's environment carries"), not the same thing as a node already at this
// version. The migration is nil when there is nothing to migrate.
//
// It is a writer, so it holds a writer's gate (budgetDBUnadopted), and it holds
// it HERE, before the node is read: this is the second stage that writes into
// the budget database, and the rules stage being skipped over a database the
// installation never adopted does not stop this one from migrating the node in
// it — the read, the ETag and the write all land in that same database, and
// this stage reaches this function whatever the stage in front of it answered.
//
// That gate is the rules stage's own, the same function and not a second one
// worded alike (the second review's finding: the two drifted, and a database the
// rules stage skipped came out writable for this stage). It is asked of every
// shape — no Firebase project recorded, the installation's own project with a
// URL pointing at the database beside it, a project other than the
// installation's own — so the two stages answer the same question the same way
// by construction, and the ordinary installation (budget.firebase_project equal
// to gcp_project, its own database and its own mark) asks a read it already had
// to make anyway.
func policyMigrationFor(ctx context.Context, lc *localcfg.Config) (m *policyMigration, published bool, err error) {
	return policyMigrationForIn(ctx, lc, policyConfigPath(lc))
}

// policyMigrationForIn is policyMigrationFor with the local config file named by
// the caller that holds it: `fugaro update`'s stage knows the exact file the run
// selected, and the path this package derives from the project alone could name
// another file that holds the same project (the one a --config run did not
// read). The publish records the generation it wrote in that file (D854) and the
// re-seed writes it, so the file has to be the one the plan was built from.
func policyMigrationForIn(ctx context.Context, lc *localcfg.Config, configPath string) (m *policyMigration, published bool, err error) {
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, false, err
	}
	if why := budgetDBUnadopted(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), "this stage migrates no policy there", selfCommand()+" update"); why != "" {
		return nil, false, &policyMigrationNotOurs{reason: why}
	}
	etag, found, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return nil, false, err
	}
	// The identities the node's bitbucket_tokens key carries (R12) are read
	// from this same database, once, before anything is derived from them: a
	// project that names no typed token answers nil without a read at all, so
	// the installation holding the legacy bitbucket-token alone converges with
	// no step (R3).
	identities, err := bitbucketTokenIdentities(ctx, db, lc)
	if err != nil {
		return nil, false, err
	}
	if !found {
		// #1656: a project that has never published a node is not always a
		// project with nothing to do. The local config can state something only
		// the node can carry — an image_catalog or the orchestrator's guardrails,
		// neither of which any job's environment carries — and a stage that
		// reported done here left the operator's catalog, or the guardrails they
		// had just written, in a file no run reads, with
		// `fugaro project policy publish` as the second command `fugaro update`'s
		// one command must not need.
		if !firstPublishCarriesANodeOnlyMember(lc) {
			return nil, false, nil
		}
		return firstPolicyPublish(ctx, db, lc, etag, configPath, identities)
	}
	node, derr := withDerivedMembers(raw, lc, identities)
	moved, changes, err := projectpolicy.Migrate(node)
	switch {
	case err == nil && len(changes) == 0:
		// A node already at this version: nothing is migrated, so nothing is
		// derived for it either, and the derivation's refusal is not the
		// MIGRATION's to make here. It is a config a publish refuses (an
		// allow_data_to entry the policy's own reader rejects), and the
		// publish half below is where it lands — there, it is the refusal the
		// publish it was about to make deserves, and on a node the config does
		// not ask to publish it is not a failure of the policy stage of every
		// fugaro update at all.
		moved, changes = nil, nil
	case derr != nil:
		// The derivation's own error is the refusal whether or not Migrate
		// had one: a member the local config cannot state is a config a
		// publish refuses too, and carrying on writes a node without the
		// block a publish would have put there — the state a proxied run's
		// whole admission is missing from. It also says more than Migrate's
		// note can about the node it did refuse: the note says only that the
		// block belongs to a publish, the derivation names the entry to fix.
		return nil, true, derr
	case err != nil:
		return nil, true, policyMigrationRefusal(err)
	}
	base := raw
	if moved != nil {
		base = moved
	}
	// The version the operator's diff names is the node's own, read off the
	// document rather than off the migration: a run that only publishes has no
	// Change to read it from, and both doors word their outcome with it.
	var head struct {
		Version int `json:"version"`
	}
	_ = json.Unmarshal(raw, &head)
	m = &policyMigration{lc: lc, db: db, etag: etag, moved: moved, changes: changes,
		from: head.Version, path: configPath, recorded: lc.PolicyGeneration,
		derived: derivedMembers(base, raw), identities: identities}
	// The publish half reads the same node, through the same ownership gate and
	// the same ETag: the stage is one writer, and a publish that opened a second
	// read path would be the second writer — with its own, weaker gate — that
	// #1460's review found between two stages worded alike. The decode is the
	// tolerant one (projectpolicy.Decode and not publishedPolicyOf): a node
	// carrying top-level keys this Fugaro does not model is read here and
	// refused at the write, by the one sentence both halves share
	// (refuseMigrationUnknownKeys), rather than by a sentence the migration half
	// would then never reach.
	old, _, derr := projectpolicy.Decode(base)
	if derr != nil {
		return nil, true, policyMigrationRefusal(derr)
	}
	m.node = old
	m.importCloneURLs = cloneURLsToImport(lc, old, configPath)
	m.drift = policyConfigDrift(lc, old, identities)
	if len(m.drift) > 0 {
		if err := publishPlanFor(ctx, db, lc, m, base, old); err != nil {
			return nil, true, err
		}
	}
	// The write is the publisher's write, so it holds the publisher's gate
	// (P3, requirePolicyRules): a policy node is read by the runs a deployed
	// rules generation admits, and one written under rules that predate the
	// policy model is a policy nothing reads. It is checked only when a node is
	// about to be written — a node already at this version, carrying what the
	// config states, is left alone whatever the deployed rules are, and D854's
	// re-seed writes the local file and not the node — and it is checked here
	// rather than at the write because the operator reads the diff before it: a
	// refusal after the confirmation would be a document they confirmed
	// and did not get.
	writes := moved != nil || m.pub != nil
	// The generation record belongs to the agreement between the file and the
	// node, not to this run's write: a run that writes no node — the file
	// already states what the node carries — still refreshes it when the
	// generation the file records is not the node's own. Without that, a record
	// left behind by a migration-only write, by the explicit door or by a
	// hand-fixed re-seed refusal would refuse the operator's next legitimate edit
	// and offer to re-seed the file over it, which is the overwrite D854 exists
	// to prevent, one round later.
	if !writes && m.generations == nil && m.path != "" && m.recorded != etag {
		m.record = etag
	}
	if writes {
		if err := requirePolicyRules(ctx, db, lc); err != nil {
			return nil, true, err
		}
		if err := refuseMigrationUnknownKeys(base); err != nil {
			return nil, true, err
		}
	}
	if !writes && m.generations == nil && m.record == "" && len(m.importCloneURLs) == 0 {
		return nil, true, nil
	}
	return m, true, nil
}

// firstPublishNodeOnlyMembers names the members only the node can carry that
// the local config states, in the order the node's own text writes them, so a
// line about a first publish names what this file holds and not the member it
// does not.
//
// A member written but stating no value is not one of them:
// ImageCatalogEffective is nil for a config that names no catalog and for one
// that writes an empty catalog, which publishes no member at all (D1156),
// ImageMapEffective is nil for the same two spellings of the map (D1906),
// CloneURLsEffective is nil for them too (a block with no entry publishes no
// member, SD5), and OrchestratorEffective is nil for a block that sets no key
// (SetsNothing). The publish would create a policy that admits nothing and
// carry none of them with it.
//
// The rule is a member only the node can carry, listed here one at a time and
// asked of the publisher's own guard for that member: `lc.X != nil` where the
// publisher's guard is that (the bake switch, the triage switch and the
// review-against-task switch among them: BakeReposEffective, TriageEffective
// and ReviewAgainstTaskEffective hand back a written answer whatever it is, and
// the rows that compare them are reached by `lc.BakeRepos != nil`,
// `lc.Triage != nil` and `lc.ReviewAgainstTask != nil`), and the block's own
// emptiness where the publisher hands the node's member back for a config that
// states none (the crafts family, whose three members are compared by
// publishedCraftCredentials, publishedCraftPools and publishedCrafts, and the
// cascade and the grades N4 merges beside them, whose guards are
// credentialsStated and Empty).
func firstPublishNodeOnlyMembers(lc *localcfg.Config) []string {
	var out []string
	if lc.ImageCatalogEffective(nil) != nil {
		out = append(out, "the image_catalog")
	}
	if lc.ImageMapEffective(nil) != nil {
		out = append(out, "the repo-to-image map")
	}
	if lc.BakeReposEffective(nil) != nil {
		out = append(out, "the bake switch")
	}
	if lc.OrchestratorEffective(nil) != nil {
		out = append(out, "the orchestrator's guardrails")
	}
	// The triage switch is the newest of them (D1854): a repository's own file
	// states the switch for itself (agent.triage, read from the base), and the
	// answer a project's runs read is the node's, so a project whose config
	// writes the switch and which never published a node has left it in a file
	// no run reads.
	if lc.TriageEffective(nil) != nil {
		out = append(out, "the triage switch")
	}
	// The review-against-task switch joins them (RA8b, R7, D2935): a
	// repository's own file states the switch for itself, and the answer a
	// project's runs read is the node's — a published false is the project-wide
	// default a repository's own true turns back on — so a project whose config
	// writes it and which never published a node has left the owner's own
	// ceiling in a file no run reads. The node's own text writes it one row
	// after the triage switch (init_policy_publish.go's publish), so it is
	// listed here beside it.
	if lc.ReviewAgainstTaskEffective(nil) != nil {
		out = append(out, "the review-against-task switch")
	}
	// The member's switch for the personal half of a workspace's credentials
	// joins them (R9 of docs/workspace-credentials/, WC6, D2972): the
	// workspace's service — the only enforcer — reads the member's PUBLISHED
	// policy and never anything on the member's disk, so a member that set the
	// switch on their own machine and whose project never published a node has
	// left their own act in a file no service reads. The node's own text writes
	// it beside the review-against-task switch (init_policy_publish.go's
	// publish), so it is listed here beside it.
	if lc.PersonalWorkspaceTokensEffective(nil) != nil {
		out = append(out, "the personal-workspace-tokens switch")
	}
	// The pools' subscription switch joins them (F22, D1636): it is read off
	// the node and nowhere else — a run asks the published policy whether it
	// may spread a project across subscriptions at all — so a project whose
	// config writes it and which never published a node has left the owner's
	// own act in a file no run reads.
	if lc.SubscriptionSpreadEffective(nil) != nil {
		out = append(out, "the pools' subscription switch")
	}
	// The clone URLs join them (SD5, D842): the URL a run clones from is the
	// member the runner computes from the published node, and no job's
	// environment carries it, so a project whose config writes the block and
	// which has never published a node has left where its runs clone from in a
	// file no run reads — the state a cluster with no browser console could not
	// leave at all before this release (G3).
	if lc.CloneURLsEffective(nil) != nil {
		out = append(out, "the clone URLs")
	}
	// The crafts join them (MC18, D1667): what a craft's calls go out on — the
	// credential or the pool its access names, the model it runs and the pool
	// that spreads it across subscriptions — is read off the published node and
	// nowhere else (docs/model-crafts §1–3), so a project whose config states
	// them and which has never published a node has left the owner's crafts in a
	// file no run reads, with `fugaro project policy publish` as the second
	// command the HARD RULE forbids. Each block is its own entry because the
	// list names what this file holds, and the guard is the publisher's own:
	// publishedCraftCredentials, publishedCraftPools and publishedCrafts hand the
	// node's member back for a config that states no block.
	if len(lc.Credentials) > 0 {
		out = append(out, "the craft credentials")
	}
	if len(lc.Pools) > 0 {
		out = append(out, "the craft pools")
	}
	if lc.Crafts != (config.Crafts{}) {
		out = append(out, "the crafts")
	}
	// The cascade and the model grades are the crafts' own case (N4, D2607): the
	// credentials service reads the cascade off the published node — its
	// fallback, its threshold and D142's two controls — and a rung reads its
	// model and its access there (runner/policy.go), so a project whose config
	// states either and which has never published a node has left the owner's
	// own statement in a file no run reads. The publish's own drift rows close
	// that gap for a project that already holds a node; only this list closes it
	// for one that holds none, and without the entry the stage reports there is
	// nothing to do, which is the second command the HARD RULE forbids. Each is
	// asked of the publisher's guard for it, the crafts family's rule one block
	// up: a cascade block written with every member absent is no statement
	// (credentialsStated, publishedCredentials' own guard), and a grades block
	// stating no grade is none either (Empty, publishedModelGrades').
	if credentialsStated(lc.CredentialCascade) {
		out = append(out, "the credential cascade")
	}
	if lc.ModelGrades != nil && !lc.ModelGrades.Empty() {
		out = append(out, "the model grades")
	}
	return out
}

// firstPublishCarriesANodeOnlyMember is whether the local config states
// something only the node can carry, which is what a first publish is made for:
// an image catalog (D1156), the repo-to-image map (D1906), the bake switch
// (D1901) and the orchestrator's guardrails (D1239) are read out of the
// published policy and nowhere else — no job's environment carries any of them,
// and the shared config refuses the map and the switches outright. The triage
// switch joins them (D1854) for the half a repository cannot write: a
// repository's own file states the switch for itself, and the answer a
// project's runs read is the node's. The review-against-task switch joins them
// (RA8b, R7, D2935) for that same half, one switch over: the file's own answer
// is a repository's, a published false is the project-wide default its true
// turns back on, and a project whose config writes an answer the node does not
// carry has left the owner's ceiling in a file no run reads. The clone URLs
// join them (SD5, D842) for the whole of the member: the runner computes a
// run's clone URL from the published node, so the block the local config writes
// reaches no run until the node carries it. The pools' subscription switch
// joins them last (F22, D1636) for the same reason, one over: whether a run may
// spread a project across two or more subscriptions at all is the node's
// answer, and a written true is what makes a pool of two or more subscriptions
// serve anything. So each of them is a member a project that has never
// published a node has left in a file no run reads.
//
// The members that are not node-only are not this stage's to publish here: a
// budget ceiling, a base branch or a model list reaches every run through the
// job's own environment and the shared config, so a project that has never
// published a node is a project those are already read for.
func firstPublishCarriesANodeOnlyMember(lc *localcfg.Config) bool {
	return len(firstPublishNodeOnlyMembers(lc)) > 0
}

// firstPolicyPublish is the publish half of a project that has never published a
// node: the document the local config states, built by the publisher's own
// function (policyNodeForPublish), so the node this stage writes and the node
// `fugaro project policy publish` writes are the same document from the same
// config.
//
// The write creates the node rather than replacing one, which is what makes it
// the one write of this stage that is not a "rewrite" and the only one with a
// first-publishing question to ask (the empty allow-list, in
// policyMigrationConfirm). Everything else is the stage's ordinary posture: the
// ETag the read found absent is the precondition the create is conditional on
// (rtdb.PutIfMatch), the publisher's rules gate is asked before the write
// (requirePolicyRules, P3), and the generation the write lands is recorded in
// the file it was built from (D854).
//
// The drift is measured against an empty node rather than a nil one: there is no
// published policy for the local config to agree with, so every member the
// config states is a member the node does not carry yet — which is what the
// operator's diff is a diff OF. policyConfigDrift reads a nil node as "nothing
// to differ from" and would print no diff at all.
func firstPolicyPublish(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, etag, configPath string, identities map[string]string) (*policyMigration, bool, error) {
	body, pol, sources, err := policyNodeForPublish(ctx, lc, nil, nil, false, false, false, identities)
	if err != nil {
		return nil, false, err
	}
	m := &policyMigration{lc: lc, db: db, etag: etag, path: configPath,
		recorded: lc.PolicyGeneration, firstPublish: true, identities: identities}
	m.drift = policyConfigDrift(lc, &projectpolicy.Policy{}, identities)
	m.pub = &policyPublish{body: body, pol: pol, drift: m.drift, sources: sources,
		adopted: lc.PolicyGeneration == ""}
	if err := requirePolicyRules(ctx, db, lc); err != nil {
		return nil, false, err
	}
	return m, false, nil
}

// publishPlanFor works out what this run does about a node that does not carry
// what the local config states: publish it, or — when this machine's file was
// written from a generation the node has moved past — refuse the publish and
// offer to re-seed the file instead (D854).
//
// The refusal is the answer to a drift that is not this run's own: the node was
// moved by another machine (or by a hand edit) after this file was written from
// it, so publishing would replace an operator's newer node with an older file.
// It is a question and not a wall — the stage prints the three-way state and
// asks for the re-seed, which writes the file and never the node — and it is
// asked before the publish is built, so nothing of this run's own edit is
// carried into a node it does not write.
func publishPlanFor(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, m *policyMigration, base []byte, old *projectpolicy.Policy) error {
	// A run that holds no local config file can neither record the generation it
	// published nor answer the generation refusal by re-seeding one (D854), and a
	// node written from it would be a node with no baseline anywhere: both are
	// left to a run that holds a file.
	if m.path == "" {
		return nil
	}
	if lc.PolicyGeneration != "" && lc.PolicyGeneration != m.etag {
		m.generations = &policyGenerationState{recorded: lc.PolicyGeneration, published: m.etag}
		return nil
	}
	body, pol, sources, err := policyNodeForPublish(ctx, lc, old, base, true, false, false, m.identities)
	if err != nil {
		return err
	}
	m.pub = &policyPublish{body: body, pol: pol, drift: m.drift, sources: sources,
		adopted: lc.PolicyGeneration == ""}
	return nil
}

// policyMigrationRefusal is Migrate's own refusal with the remedy that fits
// this side of the skew: a *VersionError carries the shared sentence alone
// (Migrate's contract: the caller prints SkewSentence), so a node ahead of this
// Fugaro is told to upgrade the binary and one behind it — a version below the
// chain's first step, 1 or 2, which 0.8.0 and 0.8.5 wrote and no step reaches
// (projectpolicy.VersionSince) — is told the move that works. Anything else, a
// malformed node among them, is returned as it came: it is not a skew and has
// no version to word.
func policyMigrationRefusal(err error) error {
	var ve *projectpolicy.VersionError
	if errors.As(err, &ve) {
		return userErr("%s", projectpolicy.SkewSentence(ve, projectpolicy.ReaderCLI))
	}
	return err
}

// policyMigrationConfirm is the tail both doors run, to the line and in the
// order: the in-flight refusal, the diff, and the write behind the
// confirmation the delete path takes. It is one function because the decision
// is one decision — the node every run of the project is admitted by,
// rewritten with what the operator has just read — and all three of its parts
// are that decision: a sentence worded twice is a sentence that can drift, and
// an order kept twice is one that can differ, which is what it was before this
// (the step printed the diff and then refused, showing a document the operator
// could not act on without a second run of the same command).
//
// What each door does around it is its own: the agent-session refusal and the
// absent node's detail, and how a failure is worded where the door reports one.
//
// admitted is the managed project's gate's answer for this node (M1, D1016):
// true means the document the write puts at the shared path was judged and
// admitted, so the confirmations that exist to make the operator read it are
// the gate's own and are not asked again. It is a parameter and not a field of
// the migration for the reason initflow.Managed gives for its own: a caller
// that could assert an admission could assert it for a node nobody judged, and
// a caller under no gate passes false and gets exactly the code that was here
// before.
// The answer about the runs in flight is not among the confirmations admitted
// stands in for: it is asked before any of them and whatever admitted the
// write, because it is about the runs that read the node and not about who
// approved this one.
//
// managed is whether the project's marks say it is managed, and it is the
// reading the rest of init makes for that question (initGate.underway, the same
// answer the gate's own stages take in initflow.GateStandsIn). It changes
// the confirmations of the owner's own session: a person at a real terminal is
// not asked to type the project's name to publish the node, so the typed
// confirmation below is replaced by a note of what is written — for a node that
// is there and for a project's first one alike, the empty-allow-list first
// publish above being the only write that keeps a question of its own. Nothing
// is typed in either case (T7, docs/no-prompt-policy): the session the note
// does not reach takes confirmStageWrite's confirmation instead, where the
// person at the terminal answers by being there and --ask is the flag that puts
// the question back. The upgrade door has no gate and passes false, so it keeps
// that confirmation exactly as it was.
//
// inflight is what this door does about the runs in flight over the node
// (inflightAnswer): the update door passes inflightQuiet, because the owner's
// ruling of 2026-11-09 is that `fugaro update` is safe while runs are in flight
// and never tells anybody to wait, the doors that reach the stage without being
// the update door (`fugaro setup`, and the composite doors running a named
// subset of the stages: `fugaro accounts add`, `accounts rm`, `accounts alias`)
// pass inflightManaged
// and keep the answer of 2026-11-06. It is a parameter and not a field of the
// migration for
// the reason the paragraph above gives for admitted: a door is a fact of the
// command, not of the node.
//
// c is the run's own conditions, the reading initRun.conditions hands every
// other confirmation in this package (the marker and the terminal among them):
// it is passed rather than read here so that the session the confirmations
// below are scoped to is one reading and cannot disagree with itself. `fugaro
// upgrade`, which has no initRun, passes the same two facts its own agent
// refusal was made from.
//
// ownerHand is whether the command this run is converging for already IS the
// owner's hand at their own terminal (initOptions.ownerHand): `fugaro accounts
// alias set` is refused in a coding agent's session before it reads anything, so
// the account choice its publish carries was made by the owner typing that
// command, and the typed name below would be a second confirmation of the same
// decision — which D2097 does not have (F10, D1540). It waives that confirmation
// for the account-choice widening and for nothing else
// (policyMigration.accountChoiceOnly): a ceiling raised in the same publish is
// the separate decision D846 reserves for the project's own hand. It is a
// parameter for the reason admitted is: a door is a fact of the command, not of
// the node.
func policyMigrationConfirm(ctx context.Context, cmd *cobra.Command, m *policyMigration, yes bool, w io.Writer, admitted, managed, ownerHand bool, c initflow.Conditions, inflight inflightAnswer) error {
	// The in-flight answer is about the NODE this run rewrites, and it is asked
	// before the branch below: D854's re-seed carries a pending migration with it
	// (confirmReseed writes the re-seeded file and then the migrated node), and a
	// re-seed that answered its own confirmation and re-seeded the operator's
	// file before asking about the run in flight would move that file under a
	// write this refusal exists to stop — and would ask the operator to confirm
	// the re-seed of a run that a plain --yes cannot then finish.
	if err := m.refuseRunsInFlight(ctx, yes, inflight, managed, c, w); err != nil {
		return err
	}
	// A publish that carries a grant is refused in a coding agent's session
	// whatever the gate said (D2617): the grant members are judged on the gate's
	// side — loosening() is what the gate refuses a managed project's --yes for
	// — and the session's own refusal is asked here beside it, before the diff,
	// so a grant a coding agent queued in the local config is published by the
	// owner's own next `fugaro update` and never by the session that wrote it.
	if err := m.refuseGrantsInAnAgentSession(c); err != nil {
		return err
	}
	// --ask's own read, the one confirmNow asks its question on: it puts back a
	// confirmation only where the gate replaced one, and this door's read of the
	// marks is that fact here (initRun.asksBack is the same read made at the
	// run). A project the marks did not answer "managed" for keeps the
	// confirmation it always had, in its terminal arm as well, instead of one
	// the flag puts back (restoreAsk).
	ask := c.Ask && managed
	if m.generations != nil {
		return m.confirmReseed(ctx, cmd, restoreAsk(c, ask), yes, w, admitted)
	}
	if !m.writesNode() {
		if len(m.importCloneURLs) > 0 {
			// The import is a write of its own, and the only one this run
			// makes: the block has been readable to this operator inside the
			// published node and nowhere else, so it is confirmed the way the
			// node's own write is rather than riding on the one-line record
			// that would otherwise be this run's whole work (spec §6.4).
			if err := confirmStageWrite(cmd, m.lc, restoreAsk(c, ask), yes, policyCloneURLsWhat); err != nil {
				return err
			}
			if err := m.recordGeneration(ctx, cmd); err != nil {
				return err
			}
			return m.importPolicyNodeCloneURLs(w)
		}
		// The one write of this run is the generation record in this machine's
		// file: there is no node to rewrite and so nothing for the operator to
		// confirm by name — the file is backed up and replaced the same way, and
		// the two lines the caller prints say what was recorded and where the
		// backup is.
		return m.recordGeneration(ctx, cmd)
	}
	m.print(w)
	what := m.what() + ": every run of the project reads it"
	if !m.firstPublish {
		// A node that is there carries the fields the operator edited into it by
		// hand; the first one has no such fields to carry over, and saying it did
		// would describe a document the project does not have.
		what += ", and the fields you edited into it by hand are carried over rather than published again"
	}
	if m.firstPublish && admitsNoRepository(m.pub) {
		// The first node of a project whose local config lists no repository
		// admits nothing, and from the moment it is published every run of the
		// project stops before it starts (doctor's allow-list-empty). The diff
		// above shows the empty list as one line among the ones a first publish
		// adds, which reads as harmless, so the consequence is said before the
		// write and the person at the terminal is what answers it (nothing is
		// typed, T7) — the publisher's own confirmation, reused rather than
		// worded a second time, and the same one the explicit door asks
		// (#1674). It replaces the confirmations below rather than joining
		// them: it is the stricter of the two (only --ask puts the question
		// back, and a session with no terminal, a coding agent's, or
		// --non-interactive or --json is refused), and a document this node's
		// confirmation already covers needs no second question.
		if err := confirmEmptyAllowList(cmd, m.lc, c); err != nil {
			return err
		}
	} else if managed && initflow.PersonAtTheTerminal(c) && !admitted {
		// The owner's ruling of 2026-11-06: no typed prompt for a person at a
		// real terminal on a managed project, except data loss and secret
		// values. Rewriting the node from the person's own local config is
		// neither, so the person is told what is written and it is written.
		//
		// A project's FIRST node is this case too, and deliberately: the
		// document is entirely the person's own local config, nothing is
		// replaced and nothing is deleted, and the write is the one the project
		// needs to have a policy at all — the diff above the note is the first
		// publish's own (printFirstPublishDiff), naming every member the node is
		// created with, and the note says the node is the project's first. The
		// first publish that keeps the publisher's own confirmation rather than
		// this note is the empty allow-list one handled above, which is the
		// write that would stop every run of the project
		// (confirmEmptyAllowList, #1674).
		//
		// The condition is the gate's own reading of who is there
		// (initflow.PersonAtTheTerminal, GateStandsIn's condition half): a
		// real terminal, no marker, and none of --ask, --non-interactive and
		// --json. The terminal is the load-bearing half, because a pipe and a
		// coding agent's tool call carry no marker and a managed project is
		// reached by scripts, cron and ssh without a tty as well as by a
		// person; --ask is the owner asking for the confirmations back, which
		// is never a licence to skip one.
		//
		// A coding agent's session carries a marker and keeps every
		// confirmation below; an unmanaged project reads managed false and
		// keeps them too. The gate's own admission (admitted) is untouched:
		// where the gate judged and admitted the document, the confirmations
		// below were already the gate's own and were not asked again.
		fmt.Fprintf(w, "note: %s\n", what)
	} else if !admitted {
		// D846 reaches this door too: a publish that raises a ceiling, removes one
		// or widens the model list is the widening the settings commands reserve for
		// the project's own hand, and the two are one decision — what every run of
		// the project is admitted by. So the typed name is asked for it and --yes
		// does not stand in, which is what the sentence says to the operator whose
		// script (or agent session) has just passed --yes and been refused.
		if loosening := m.loosening(); len(loosening) > 0 {
			why := m.looseningWhy(loosening)
			// The sentence is said once. A session with no terminal is refused with
			// it inside the error (confirmTypedStrict), and a door that reported
			// that error next to the sentence it already printed said the same
			// thing twice; the person at a terminal reads it here and only here,
			// because the prompt below names the step and never the widening.
			if stdinIsTerminal(cmd.InOrStdin()) {
				fmt.Fprintf(w, "%s\n", why)
			}
			// Two widenings this run may carry without the typed name, and both
			// are the sentence above being the whole of what is asked:
			//
			// The account the owner just chose with the command they typed is
			// one of them — for that door the sentence IS the report, and the
			// confirmation would be a second one for a decision already made at
			// this same terminal (D2097, F10) — and a grant is the other, at
			// every door: `fugaro update` asks nothing for a grant (D1542,
			// D1552, F24), because a grant is what the owner's own update
			// publishes and a typed name in front of it would be the prompt the
			// HARD RULE's own migration cannot carry. Everything else — a
			// ceiling raised, a member removed, an account choice this door did
			// not make, a first node — keeps it.
			if !(m.grantOnly(loosening) || (ownerHand && m.accountChoiceOnly(loosening))) {
				if err := confirmTypedStrict(cmd, m.lc, why, what); err != nil {
					return err
				}
			}
		} else if err := confirmStageWrite(cmd, m.lc, restoreAsk(c, ask), yes, what); err != nil {
			return err
		}
	}
	if err := m.write(ctx); err != nil {
		return err
	}
	if err := m.recordGeneration(ctx, cmd); err != nil {
		return err
	}
	// The import rides on the confirmation the node's write has just taken:
	// this run is publishing the node the local config states, and the block's
	// home is now the file, so it is written there in the run the operator
	// confirmed rather than left for the next one.
	return m.importPolicyNodeCloneURLs(w)
}

// inflightAnswer is what a door does about the runs a node write finds in
// flight over it (refuseRunsInFlight).
type inflightAnswer int

const (
	// inflightAsk refuses the write without --yes and tells the operator to
	// wait for the runs: the answer of the door that is NOT the whole upgrade,
	// where the person is in the middle of something else and a node of theirs
	// is being published, and the refusal costs them nothing the HARD RULE
	// promises them because the one command the upgrade is stays `fugaro
	// update`. No door passes it since U4 retired the second one — the answer
	// is kept, and kept first, because it is the enum's zero value: a door that
	// forgot to pass one refuses the write rather than making it, and of the
	// two that is the safe side.
	inflightAsk inflightAnswer = iota
	// inflightQuiet writes and says nothing about the runs. `fugaro update` is
	// the whole upgrade, and the owner's ruling of 2026-11-09 is that it is
	// safe while runs are in flight, always: stopping every running job to
	// update is unacceptable for one developer and impossible for a team of ten
	// or twenty. The audit that establishes it is
	// docs/design/update-in-flight.md.
	//
	// The count is not the stage's to print either. The door prints it once,
	// itself, before the first stage (update_in_flight.go's
	// announceRunsInFlight) — a door line and a stage line about the same runs
	// would be the two readings of one fact this package keeps collapsing into
	// one, and the door's is the one that can say what the update as a whole
	// does, which is what the person deciding whether to update now needs.
	//
	// It is the owner's own session's alone: a coding agent's session keeps the
	// refusal (refuseRunsInFlight), because every ruling widens the person's
	// session and never a marker's. And it is `fugaro update`'s alone, which is
	// why it is not what every door into the stage passes (inflightManaged):
	// `fugaro setup` and the composite doors reach this stage too, and the
	// ruling is about the one command the whole upgrade is.
	inflightQuiet
	// inflightManaged is the answer the doors that are not `fugaro update` had
	// before 2026-11-09, kept for them: the owner's ruling of 2026-11-06 is
	// that a managed project's policy write is never refused for the runs in
	// flight — with up to twenty cloud runs running the refusal fired nearly
	// every time and left the owner unable to apply a policy at all — so the
	// count is printed as a note and the write proceeds. Every other session,
	// a project that is not managed and a coding agent's session included, is
	// refused without --yes.
	//
	// It is `fugaro setup`'s and the composite doors' — `fugaro accounts add`,
	// `accounts rm` and `accounts alias` converge the same stages through
	// stagesNamed (spec §2.5 rule 2) and none of them is the update door, so
	// none gets the update door's answer, and the update door's own line
	// (announceRunsInFlight) is not one of theirs. What they keep is what they
	// had, which is this.
	inflightManaged
)

// refuseRunsInFlight is the guard a door holds before a node write: a run that
// is mid-flight read the policy this write replaces, so the door that is not
// the whole upgrade refused it without --yes, and --yes was the way past it
// once the operator had read what the write does (the same reading `publish
// --delete` has). A run
// that writes no node — D854's re-seed alone, or the generation record alone —
// asks nothing: no run in flight reads this machine's file.
//
// The answer is the door's and not this function's (inflightAnswer): the update
// door passes inflightQuiet, which reads no count at all and returns before the
// write, so `fugaro update` neither refuses nor tells anybody to wait. What it
// does instead — the note it prints, and the one step of it that can still
// bind a run in flight — is update_in_flight.go's. inflightManaged is the other
// ruling (2026-11-06), kept for the doors that are not the update door: a
// managed project's own session is told the count and writes.
//
// A coding agent's session is not the session any of this is about (c): every
// ruling widens the person's own session and never a marker's, so a marker gets
// the refusal whatever its door passes. It is the same scoping the managed
// case's own sentence had (2026-11-06), kept after that sentence was, and the
// gate is what a marker's update is judged by in any case — a session the gate
// refuses never reaches this call.
func (m *policyMigration) refuseRunsInFlight(ctx context.Context, yes bool, answer inflightAnswer, managed bool, c initflow.Conditions, w io.Writer) error {
	if !m.writesNode() {
		return nil
	}
	// inflightQuiet is the one answer that reads nothing: the update door has
	// already read the count for its own line (announceRunsInFlight), and a
	// count that cannot be read is silence there rather than a refusal of the
	// one command the upgrade is.
	if answer == inflightQuiet && c.Agent == "" {
		return nil
	}
	inflight, err := runsInFlight(ctx, m.db, m.lc)
	if err != nil {
		return err
	}
	if inflight == 0 {
		return nil
	}
	if answer == inflightManaged && managed && c.Agent == "" {
		fmt.Fprintf(w, "%d run(s) are in flight and a run whose model calls go through the credentials service reads the new policy on its next call\n", inflight)
		return nil
	}
	if yes {
		return nil
	}
	return userErr("%d run(s) are in flight: a run that is mid-flight read the policy this migration would rewrite, so it is refused while they run; wait for them (fugaro runs ls), then repeat it", inflight)
}

// writesNode is whether this run's confirmation ends in a write to the published
// node: the migration, the publish, or both in one write.
func (m *policyMigration) writesNode() bool { return m.moved != nil || m.pub != nil }

// raisesVersion is whether the write this run holds moves the published node's
// version: the walk across versions does, and the step that runs at a version
// rather than across two — AB8a's rewrite of a budget mode the vocabulary
// dropped — does not. The two credentials rules around the write are what ask
// it (credentialsMoveHeld, credentialsResolveBeforeRaise): they exist because
// the node's version and the process reading it move together, and a node
// rewritten at the version it already has asks no service to be replaced for
// it, which is the whole of what D3025 buys (no bump, no image rebuilt).
func (m *policyMigration) raisesVersion() bool {
	return m.moved != nil && m.from != projectpolicy.Version
}

// sameVersion is whether this run's write is the one step that runs at a
// version rather than across two (projectpolicy.Migrate's own answer for a node
// at this version whose budget mode says the word the vocabulary dropped). It
// is read off the node's version and not off the changes, because the version
// is what the write is: a node already at this version can only change through
// that step, so the number and the write are one fact.
func (m *policyMigration) sameVersion() bool {
	return m.moved != nil && m.from == projectpolicy.Version
}

// admitsNoRepository is whether the document this publish would write admits no
// repository at all, which is the node that stops every run of the project
// before it starts. It is asked of the built publish and not of the local
// config: the allow-list a publish writes also carries what the shared config's
// repositories add and the entries a node already had, and a confirmation keyed
// on the file alone would refuse a write whose allow-list is not empty.
func admitsNoRepository(p *policyPublish) bool { return p != nil && len(p.pol.AllowRepos) == 0 }

// what names this run's write as the confirmation and the outcome read it: the
// migration and the publish are one write and are worded as one, and either
// alone is worded as itself.
func (m *policyMigration) what() string {
	switch {
	case m.firstPublish:
		return "this writes the project's first published policy node, from your local config"
	case m.moved != nil && m.pub != nil:
		return "this rewrites the published policy node in place, carrying the migration and what your local config states"
	case m.pub != nil:
		return policyPublishWhat
	default:
		return policyMigrationWhat
	}
}

// payload is the document this stage writes: the publish's, when the local
// config states something the node does not carry — built on the migrated node
// when a migration is pending too, so the one write carries both — and the
// migrated node otherwise.
func (m *policyMigration) payload() []byte {
	if m.pub != nil {
		return m.pub.body
	}
	return m.moved
}

// doneDetail is the line both doors report the write with.
func (m *policyMigration) doneDetail() string {
	switch {
	case m.firstPublish:
		return "published the project's first policy node, carrying what the local config states"
	case m.sameVersion() && m.pub != nil:
		return "rewrote the published policy node in place, with what the local config states"
	case m.moved != nil && m.pub != nil:
		return fmt.Sprintf("migrated the published policy from version %d to %d in place, with what the local config states", m.from, projectpolicy.Version)
	case m.pub != nil:
		return "published the node the local config states"
	case m.generations != nil:
		return fmt.Sprintf("re-seeded %s from the published policy node", m.path)
	case m.sameVersion():
		return fmt.Sprintf("rewrote the published policy node in place: %s", m.changes[0].Changed)
	case m.writesNode():
		return fmt.Sprintf("migrated the published policy from version %d to %d in place", m.from, projectpolicy.Version)
	case len(m.importCloneURLs) > 0:
		return fmt.Sprintf("imported the published policy node's clone_urls block into %s", m.path)
	default:
		return fmt.Sprintf("recorded the published policy's generation in %s", m.path)
	}
}

// recordGeneration is the tail of every write this stage makes: the generation
// this machine's file is written from, written into the file. A run that wrote
// the node records the generation it wrote — read back and checked against the
// document, never assumed (recordPublishedGeneration) — and a run that wrote no
// node records the generation the node it already agrees with is at (m.record).
// The record belongs to the agreement between the file and the node rather than
// to the write: a record left behind at an older generation is what refuses the
// operator's own next edit, and hands them a re-seed that reverts it.
//
// It is a no-op for a run with nothing of its own to record, which is the run
// whose file already records the generation the node is at.
func (m *policyMigration) recordGeneration(ctx context.Context, cmd *cobra.Command) error {
	if m.writesNode() {
		// The node the run built is handed on so the record of what it removed
		// can be cleared in the same write of the file (published, nil for a run
		// that migrated a node and published none).
		var published *projectpolicy.Policy
		if m.pub != nil {
			published = m.pub.pol
		}
		return recordPublishedGeneration(ctx, cmd.OutOrStdout(), m.db, m.lc, m.path, m.payload(), published)
	}
	if m.record == "" {
		return nil
	}
	return policyGenerationRecord(cmd.OutOrStdout(), m.path, m.lc, m.record)
}

// confirmReseed is D854's remedy: the operator reads the three-way state and, on
// confirming it, gets this machine's local config re-seeded from the published
// node — backed up first — instead of the node being overwritten with what the
// older file says. A migration pending beside it is carried out afterwards,
// because the re-seed has just made the file the node's own, so writing the
// migrated node no longer replaces anything a newer publish decided.
//
// admitted is policyMigrationConfirm's (M1, D1016): a re-seed that carries a
// pending migration with it does write the node (writesNode), and that node is
// the document the gate judged — the move of this machine's file is the same
// kind of write as the local config an admitted installation apply writes
// afterwards. A bare re-seed never reaches here admitted: nothing is published,
// so there is no document for a verdict to be about, and the gate keeps the
// session's own refusal for it.
//
// The confirmation is confirmStageWrite's, the same one the node's own write
// takes: the file the re-seed replaces is backed up beside it, so nothing is
// lost and nothing secret is written, and a person at their own terminal
// answers it by having read the three-way state above (nothing is typed, T7).
func (m *policyMigration) confirmReseed(ctx context.Context, cmd *cobra.Command, c initflow.Conditions, yes bool, w io.Writer, admitted bool) error {
	m.printGenerationState(w)
	if !admitted {
		if err := confirmStageWrite(cmd, m.lc, c, yes, policyReseedWhat); err != nil {
			return err
		}
	}
	written, err := applyPolicyReseed(cmd, m.path, m.lc, m.node, m.drift)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "re-seeded %s from the published policy node: %s\n", m.path, strings.Join(written, ", "))
	if !m.writesNode() {
		// Nothing was written to the node, so the generation recorded is the one
		// the file's policy keys now come from: the node as this run read it, and
		// nothing of this run's own may move it. There is no node write for
		// Verify to read back either, which is the whole of what it asks
		// (writesNode): a re-seed that carries a migration with it does write the
		// node and is verified like any other write.
		if err := policyGenerationRecord(w, m.path, m.lc, m.generations.published); err != nil {
			return err
		}
		return m.importPolicyNodeCloneURLs(w)
	}
	if err := m.write(ctx); err != nil {
		return err
	}
	// The migration is the write that moved the generation: the one recorded is
	// the node this run has just written, read back, and not the one the file was
	// re-seeded from — recording the generation the node was at before the write
	// would leave the file refusing the operator's own next edit against the node
	// this very run wrote.
	if err := m.recordGeneration(ctx, cmd); err != nil {
		return err
	}
	return m.importPolicyNodeCloneURLs(w)
}

// printGenerationState is D854's three-way state: what this machine's config was
// written from, what the published node is now, and the members the two differ
// on — the ones a re-seed writes back.
func (m *policyMigration) printGenerationState(w io.Writer) {
	fmt.Fprintf(w, "%s has moved past the generation this machine's config was written from, so publishing from here would replace it with an older file: %s\n",
		projectpolicy.PathShared(), m.generations.text())
	printReseedDrift(w, m.drift)
	fmt.Fprintf(w, "  re-seeding writes the published node's values into %s instead (the file is backed up first); publishing stays yours to do afterwards\n", m.path)
}

// derivedMembers is the top-level members the node gained on the way from raw
// to moved: what the publisher's own derivations added. It is read off the two
// documents rather than tracked as the derivation runs, because Migrate itself
// adds members too (the version) and the diff has to say which is which.
func derivedMembers(moved, raw []byte) []string {
	var out []string
	for _, key := range []string{bitbucketTokenKey, projectpolicy.BitbucketTokensKey, bitbucketAPIRootsKey, verifierAccountsKey} {
		before, was, err := jsonMember(raw, key)
		if err == nil && was && !projectpolicy.IsNull(before) {
			continue
		}
		now, is, err := jsonMember(moved, key)
		if err == nil && is && !projectpolicy.IsNull(now) {
			out = append(out, key)
		}
	}
	return out
}

const (
	bitbucketTokenKey    = "bitbucket_token"
	bitbucketAPIRootsKey = "bitbucket_api_roots"
	verifierAccountsKey  = "verifier_accounts"
	mobileKey            = "mobile"
	policyMigrationWhat  = "this rewrites the published policy node in place"
	policyPublishWhat    = "this rewrites the published policy node in place, from your local config"
	policyReseedWhat     = "this re-seeds your local config from the published policy node"
	policyCloneURLsWhat  = "this imports the published policy node's clone_urls block into your local config, which becomes the block's source from then on"
	policyNoDBReason     = "the project's config names no Firebase budget backend, so there is no published policy node to read"
)

// withDerivedMembers patches into the node the members a publish would have
// derived from the project's local config and the node cannot state — the
// bitbucket_token block, the typed bitbucket_tokens key, the
// bitbucket_api_roots allow-list and the verifier_accounts list — using the
// publisher's own functions (DeriveBitbucketToken, DeriveBitbucketTokens, and
// the config's bitbucket_api_roots and verifier_accounts, which the publisher
// copies). The returned node is the input byte for byte when there was nothing
// to add.
//
// It runs whether or not the migration would have refused, and before it: a
// version-3 node whose profile mounts the project's Bitbucket credential is
// refused by Migrate (the block that credential is bounded by is derived at
// publish, and a step whose only input is the node cannot state it), but a node
// with no profile mounting the token migrates cleanly without the block — and
// the migrated node is then read by every run of the project, where a block a
// publish would have written is missing and the project's Bitbucket runs fail
// closed. Both are the same omission and get the same answer.
//
// A member the node already carries is never rewritten: the block is the
// node's, and the owner may have narrowed its allow-list by hand. The error is
// the derivation's own (the config's allow_data_to cannot become a policy
// entry), and it is the caller's to place: only a write that would carry the
// block — a pending migration — is stopped by it, because a node that is not
// migrated derives nothing and so needs nothing.
//
// identities is the half of the typed tokens the file does not state (R12), and
// it is read by the caller from the creds state store rather than here, because
// the same read answers the drift comparison and the write's verification too.
func withDerivedMembers(raw []byte, lc *localcfg.Config, identities map[string]string) ([]byte, error) {
	carries := func(key string) bool {
		before, was, err := jsonMember(raw, key)
		return err == nil && was && !projectpolicy.IsNull(before)
	}
	node := raw
	if !carries(bitbucketTokenKey) {
		token, err := projectpolicy.DeriveBitbucketToken(policyProfiles(lc), lc.ProjectSecrets)
		if err != nil {
			return raw, userErr("%v", err)
		}
		if token != nil {
			if node, err = patchPolicyJSON(node, bitbucketTokenKey, token); err != nil {
				return raw, userErr("deriving the policy's %s block from %s: %v", bitbucketTokenKey, lc.Name, err)
			}
		}
	}
	// The typed tokens are the other half of the same file and travel the same
	// way (R5): the records are in the config and the identities are in the
	// creds state store, and a node the migration rewrote without the key is a
	// node whose runs cannot tell one token's coverage from another's.
	if !carries(projectpolicy.BitbucketTokensKey) {
		tokens, err := projectpolicy.DeriveBitbucketTokens(lc.BitbucketTokens, identities)
		if err != nil {
			return raw, userErr("%v", err)
		}
		if tokens != nil {
			if node, err = patchPolicyJSON(node, projectpolicy.BitbucketTokensKey, tokens); err != nil {
				return raw, userErr("deriving the policy's %s key from %s: %v", projectpolicy.BitbucketTokensKey, lc.Name, err)
			}
		}
	}
	if !carries(bitbucketAPIRootsKey) && len(lc.BitbucketAPIRoots) > 0 {
		var err error
		if node, err = patchPolicyJSON(node, bitbucketAPIRootsKey, lc.BitbucketAPIRoots); err != nil {
			return raw, userErr("carrying %s's %s allow-list into the policy: %v", lc.Name, bitbucketAPIRootsKey, err)
		}
	}
	// The verifier list travels the same way and is copied for the same reason:
	// it reaches the service through this node, so a node the migration rewrote
	// without it would be a route the owner named and that the credentials
	// stage can no longer read (C3.9g3, D1075). A config silent about the list
	// states none — which is the node's own answer, and the node is what is
	// being migrated — so only a written list is carried.
	if !carries(verifierAccountsKey) && lc.VerifierAccounts != nil && len(*lc.VerifierAccounts) > 0 {
		var err error
		if node, err = patchPolicyJSON(node, verifierAccountsKey, *lc.VerifierAccounts); err != nil {
			return raw, userErr("carrying %s's %s verifier list into the policy: %v", lc.Name, verifierAccountsKey, err)
		}
	}
	// The mobile block is carried for the reason the policy stage compares it:
	// a node this migration writes without one reads as mobile disabled and is
	// then a difference the very next run publishes, so a migrated node states
	// the defaults a publish would (MobileForPublish).
	if !carries(mobileKey) {
		var err error
		if node, err = patchPolicyJSON(node, mobileKey, projectpolicy.DefaultMobile()); err != nil {
			return raw, userErr("carrying the policy's %s block into %s: %v", mobileKey, lc.Name, err)
		}
	}
	// The app's crash-report switch travels beside the block for the same
	// reason (D731, D738): a node this migration writes without the key is a
	// difference the very next run publishes, and until that run the switch
	// reads off (MobileCrashReportsOn), so a migrated node states the default a
	// publish would (MobileCrashReportsForPublish).
	if !carries(projectpolicy.MobileCrashReportsKey) {
		var err error
		if node, err = patchPolicyJSON(node, projectpolicy.MobileCrashReportsKey, projectpolicy.MobileCrashReportsForPublish(nil)); err != nil {
			return raw, userErr("carrying the policy's %s key into %s: %v", projectpolicy.MobileCrashReportsKey, lc.Name, err)
		}
	}
	return node, nil
}

// refuseMigrationUnknownKeys is P8 at this stage's write: a node carrying
// top-level keys this Fugaro does not model is refused, and the node is left
// exactly as it was.
//
// The count is the whole of what the refusal says about the keys — not their
// values, which a node may hold anything under — and the remedy is the one that
// works: a newer Fugaro knows what they mean, so it, and then `fugaro update`,
// is the move. It is deliberately not 'fix the node by hand': the operator of a
// node written by a release above this binary has nothing to fix.
func refuseMigrationUnknownKeys(moved []byte) error {
	p, found, err := projectpolicy.Decode(moved)
	if err != nil || !found || len(p.UnknownKeys) == 0 {
		return nil
	}
	return userErr("%s carries %d key(s) this Fugaro does not know (%s), and migrating it would write a document whose meaning this binary cannot read: nothing was written; upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then run %s again",
		projectpolicy.PathShared(), len(p.UnknownKeys), unknownKeysText(p.UnknownKeys), selfCommand()+" update")
}

// write puts the migrated node back with the ETag it was read at. A concurrent
// writer — another publish, a hand edit through the console — makes the
// precondition fail, and that is a refusal, not a retry: the operator confirmed
// a diff of the node this write replaces, and a write that suppressed a change
// nobody read would be exactly the unasked rewrite `--delete` exists to avoid.
func (m *policyMigration) write(ctx context.Context) error {
	if err := m.db.PutIfMatch(ctx, projectpolicy.PathShared(), m.etag, json.RawMessage(m.payload())); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return userErr("the policy node at %s changed while this %s was being confirmed (another publish landed meanwhile), so nothing was written: read what the node now holds with 'fugaro project policy show', then run %s again",
				projectpolicy.PathShared(), m.writeName(), selfCommand()+" update")
		}
		return dbErr(m.lc, err, true)
	}
	return nil
}

// writeName is what the ETag refusal calls the write that lost the race: the
// migration's own name when it is a migration, and the publish's otherwise.
func (m *policyMigration) writeName() string {
	if m.moved != nil {
		return "migration"
	}
	return "publish"
}

// print is the one confirmation diff: what each step changes, what stays as it
// was, what had to come from the local config, and — when the node mounts the
// project's Bitbucket credential and no block could be derived — the republish
// that is still the operator's move (Migrate's own note, passed through rather
// than worded a second time here).
func (m *policyMigration) print(w io.Writer) {
	if m.moved != nil {
		if m.sameVersion() {
			// The step runs at the version rather than across two (AB8a): the
			// node's number does not move, so a line naming two versions would
			// be a diff of a change that is not there.
			fmt.Fprintf(w, "%s: rewritten in place at version %d; nothing is removed\n",
				projectpolicy.PathShared(), m.from)
		} else {
			fmt.Fprintf(w, "%s: published (version %d) and rewritten in place as version %d; nothing is removed\n",
				projectpolicy.PathShared(), m.from, projectpolicy.Version)
		}
		for _, ch := range m.changes {
			head := fmt.Sprintf("%d to %d: %s", ch.From, ch.To, ch.Changed)
			if ch.From == ch.To {
				// The step that runs at a version says what it changed without
				// the two numbers that would read as a version move (AB8a).
				head = ch.Changed
			}
			fmt.Fprintf(w, "  %s\n", head)
			kept := make([]string, 0, len(ch.Kept))
			for _, name := range ch.Kept {
				if !slices.Contains(m.derived, name) {
					kept = append(kept, name)
				}
			}
			if len(kept) > 0 {
				fmt.Fprintf(w, "    kept as it is: %s\n", strings.Join(kept, ", "))
			}
			if ch.Note != "" && !slices.Contains(m.derived, bitbucketTokenKey) {
				fmt.Fprintf(w, "    %s\n", ch.Note)
			}
		}
		if len(m.derived) > 0 {
			fmt.Fprintf(w, "  derived from the project's local config, as a publish derives them: %s\n", strings.Join(m.derived, ", "))
		}
	}
	if m.pub != nil {
		if m.firstPublish {
			printFirstPublishDiff(w, m.pub.drift, m.pub.sources)
		} else {
			printPolicyPublishDiff(w, m.pub.drift, m.pub.sources)
		}
		// A pool this file states that the node cannot carry is neither written
		// nor dropped in silence (D2608): the line names it and says what it
		// needs, asked of the publisher's own function so the line and the
		// omission cannot part company — the call the explicit door's own diff
		// makes (printPolicyDiff). It is printed here rather than carried as a
		// drift row because such a pool can never reach the node: a row that
		// reported it would fire on every run of `fugaro update` and never
		// converge, which is the second command the HARD RULE forbids.
		if _, lines := publishedCraftPools(m.lc, m.node, m.pub.pol.CraftCredentials); len(lines) > 0 {
			for _, line := range lines {
				fmt.Fprintln(w, line)
			}
		}
		if m.pub.adopted {
			// D854's other half: an installation that records no generation has
			// no baseline to compare against, so this run adopts the node it is
			// publishing from instead of asking a question with no information in
			// it — and it says so, because "adopted" is what the operator has to
			// know to read the record that appears in their file afterwards.
			fmt.Fprintf(w, "  this machine's config records no generation for its policy keys, so this run adopts the one it publishes as the generation the file is written from\n")
		}
	}
	if m.record != "" {
		fmt.Fprintf(w, "%s carries what the local config states, so nothing is published; this run records its generation (%s) in %s, which is what the next edit of the config is read against\n",
			projectpolicy.PathShared(), m.record, m.path)
	}
}

// policyMigrateStage is the stage itself. It is SelfConfirming, like the
// engines: it shows its own diff and takes its own confirmation, and refuses a
// coding agent's session and a run with no terminal and no --yes.
type policyMigrateStage struct {
	e         *initEngine
	planned   bool
	m         *policyMigration
	published bool
	err       error

	// sw is the Bitbucket proxy switch this run turns on, read with the plan
	// and written after the node (update_proxy_switch.go). It is nil for every
	// run that turns nothing on, which is the ordinary case.
	sw *bitbucketProxySwitch
	// swSaid is whether the switch's own line has been printed. It is the one
	// field of this stage forget() does NOT clear: the line is about this
	// session's bundle rather than about a read a later pass supersedes, and a
	// person told once that they hold no token for the workspace does not need
	// it again from the next pass of the same update.
	swSaid bool
}

// policyNoneDetail is what both doors say for a project that has a budget
// backend and no node in it. It is the state the delete and show paths already
// word, and it is not the same thing as a node already at this version: an
// operator told "the published policy is version 4" would go looking for a
// policy that is not there.
func policyNoneDetail() string {
	return fmt.Sprintf("no policy is published at %s: runs fall back to the ceiling their job's environment carries", projectpolicy.PathShared())
}

func newPolicyMigrateStage(e *initEngine) *policyMigrateStage { return &policyMigrateStage{e: e} }

func (policyMigrateStage) Name() string    { return initflow.Policy }
func (policyMigrateStage) SelfConfirming() {}

func (policyMigrateStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Policy, Kind: initflow.LeftCommand, Text: selfCommand() + " update"}
}

// plan reads the node once per run: the stage's check, its plan and its apply
// all ask the same question, and the answer is what the confirmation is made
// against.
func (s *policyMigrateStage) plan(ctx context.Context) (*policyMigration, bool, error) {
	if !s.planned {
		s.planned = true
		// The Bitbucket proxy switch is decided before the node is read, and the
		// node is planned from the config the switch implies: the file and the
		// node are one decision (update_proxy_switch.go), and a node planned from
		// the config as it is would carry no bitbucket_token block while the file
		// this run writes states one — the disagreement resolveBitbucketProxied
		// stops a run over. A switch that does not turn leaves the config the run
		// holds exactly where it was.
		sw, err := s.proxySwitch(ctx)
		if err != nil {
			s.m, s.published, s.err = nil, false, err
			return nil, false, err
		}
		lc := s.e.lc
		if sw != nil {
			lc = sw.lc
		}
		m, published, err := policyMigrationForIn(ctx, lc, s.e.path)
		if sw != nil && err == nil && m != nil && m.generations != nil {
			// The node has moved past the generation this machine's config was
			// written from, so this run is D854's re-seed: the file is written
			// from the node and the generation is recorded, rather than the node
			// from the file. The switch has no part in that — it is a member the
			// re-seed's splice table has no case for, so a plan built from the
			// switch's config lands on the fallback that refuses the whole
			// update with "put the published values of bitbucket_token there by
			// hand", and the operator meets an update that fails and goes on
			// failing until they edit the file by hand (S6's fix, 2026-11-18).
			//
			// So the switch is held back for this run and the plan is taken
			// again from the config as it is: the generation is recorded, every
			// other stage of the update runs, and the NEXT update — whose
			// generation now matches — is the one that turns the switch on.
			// Dropping it leaves the file and the node in agreement, which is
			// what the switch may never break: the entry this run does not write
			// is one the node does not carry, and an absent entry is never a
			// disagreement for resolveBitbucketProxied.
			s.proxySwitchSays("this update leaves the Bitbucket proxy switch off: the published policy node has moved past the generation this machine's local config was written from, and this run re-seeds the config from it — the next update turns the switch on")
			sw = nil
			lc = s.e.lc
			m, published, err = policyMigrationForIn(ctx, lc, s.e.path)
		}
		s.sw = sw
		s.m, s.published, s.err = m, published, err
	}
	return s.m, s.published, s.err
}

// forget drops the read the check, the plan and the apply share for the whole
// run, so the next of them reads the node and the rules it is gated on again.
// It exists because a run changes under this stage's feet: the rules stage
// (init_rules_stage.go) runs before it and deploys the generation this stage's
// write is gated on (requirePolicyRules), while the read-only preview pass —
// every stage's check, before the first apply — is where this stage's refusal
// is made. Without this the refusal cached there outlives the deploy that
// answered it, and the run refuses its own work with a sentence about rules
// that are no longer deployed.
func (s *policyMigrateStage) forget() {
	// The switch goes with the plan: the installation stage's own write is what
	// puts the credentials_service block in the config this run holds, and a
	// decision taken before it would be one the config the run now holds does
	// not support. swSaid is kept — the bundle gate's line is about the session,
	// not about the read (proxySwitchSays).
	s.planned, s.m, s.published, s.err, s.sw = false, nil, false, nil, nil
}

// Check reports what the published node is. A refusal here is the run's, not a
// step left for the user: a malformed node, or one written by a newer Fugaro,
// is not something the operator of this binary can finish by hand, and the
// message says which of the two it is.
//
// The three states it skips instead of refusing — a project whose config names
// no database to read the node in, a database the ownership pair does not
// prove is ours, and a node this run holds where it is while the credentials
// service is not replaced — are one word in the plan view and two facts to a
// caller: every one of them leaves the node exactly as it was, so a step run
// for this stage stays owed (Undone), and only a run that published something
// answers it.
func (s *policyMigrateStage) Check(ctx context.Context) (initflow.Status, error) {
	if policyNodeUnreachable(s.e.lc) {
		return initflow.Status{State: initflow.Skipped, Detail: policyNoDBReason, Undone: true}, nil
	}
	m, published, err := s.plan(ctx)
	var notOurs *policyMigrationNotOurs
	if errors.As(err, &notOurs) {
		return initflow.Status{State: initflow.Skipped, Detail: notOurs.reason, Undone: true}, nil
	}
	if err != nil {
		return initflow.Status{}, err
	}
	// The node's version and the credentials service's process move together:
	// a run that replaces no service may not raise the node either, or the
	// service deployed on the project refuses every cascade and
	// Bitbucket-proxied call until a later update succeeds
	// (init_stages.go's credentialsMoveHeld has the whole argument).
	var held string
	if m != nil && m.raisesVersion() {
		held = s.e.credentialsMoveHeld()
	}
	switch {
	case !published && m == nil:
		return initflow.Status{State: initflow.Done, Detail: policyNoneDetail()}, nil
	case !published:
		// The project has no node and the local config states a member only the
		// node carries, so the check is not the no-policy state: it is the first
		// publish this run would make, and it says so, naming the member the
		// operator's own file states, rather than reporting the stage done.
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("no policy is published at %s, and the local config states %s, which only the node carries: this run publishes the project's first node, after your confirmation", projectpolicy.PathShared(), strings.Join(firstPublishNodeOnlyMembers(s.e.lc), " and "))}, nil
	case m == nil:
		return initflow.Status{State: initflow.Done, Detail: fmt.Sprintf("the published policy is version %d", projectpolicy.Version)}, nil
	case held != "":
		return initflow.Status{State: initflow.Skipped, Detail: fmt.Sprintf("the published policy is version %d and this run holds it there: %s, and the service that reads the published node on every cascade and Bitbucket-proxied call refuses a node its own binary does not understand — %s, run from the owner's own session, migrates the node and replaces the service in one run", m.from, held, selfCommand()+" update"), Undone: true}, nil
	case m.generations != nil:
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the published policy at %s has moved past the generation this machine's config was written from (%s)", projectpolicy.PathShared(), m.generations.text())}, nil
	case m.record != "":
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the published policy node carries what the local config states, and the config records %s as the generation its policy keys were written from: this run records %s in %s",
			generationText(m.recorded), m.record, m.path)}, nil
	case m.moved == nil && len(m.importCloneURLs) > 0 && len(m.drift) == 0 && m.record == "":
		// The only thing this run has to do is move the block: the node is at
		// this version, the file states nothing the node does not carry, and
		// the block the node holds is not in the file. A check line that said
		// "the local config states what the published policy does not carry"
		// would name an empty list, which reads as a bug rather than as the
		// migration the next update is.
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the published policy node carries a clone_urls block this machine's config file does not: this run imports it into %s", m.path)}, nil
	case m.moved == nil:
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the local config states what the published policy does not carry: %s", memberListText(m.drift))}, nil
	}
	if m.sameVersion() {
		// The node's number does not move, so the line for a version the node
		// is behind would say the version twice (AB8a). What this run does is
		// the step, and the step's own sentence is the detail.
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the published policy is version %d and this run rewrites it in place: %s", m.from, m.changes[0].Changed)}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the published policy is version %d and this Fugaro writes version %d", m.from, projectpolicy.Version)}, nil
}

// memberListText names the members a plan would write, for a check line.
func memberListText(drift []policyDrift) string {
	names := make([]string, 0, len(drift))
	for _, d := range drift {
		names = append(names, d.Member)
	}
	return strings.Join(names, ", ")
}

func (s *policyMigrateStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	if policyNodeUnreachable(s.e.lc) {
		return initflow.Plan{NothingToDo: true, Detail: policyNoDBReason}, nil
	}
	m, published, err := s.plan(ctx)
	var notOurs *policyMigrationNotOurs
	if errors.As(err, &notOurs) {
		return initflow.Plan{NothingToDo: true, Detail: notOurs.reason}, nil
	}
	if err != nil {
		return initflow.Plan{}, err
	}
	switch {
	case !published && m == nil:
		return initflow.Plan{NothingToDo: true, Detail: policyNoneDetail()}, nil
	case m == nil:
		return initflow.Plan{NothingToDo: true, Detail: fmt.Sprintf("the published policy is version %d", projectpolicy.Version)}, nil
	}
	if env.Interactive || env.Yes {
		m.print(s.e.r.cmd.OutOrStdout())
	}
	return initflow.Plan{Detail: m.planDetail()}, nil
}

// planDetail is what the stage says it is about to do, in the wording that fits
// this run's plan: the migration, the publish, or D854's re-seed of the local
// file.
func (m *policyMigration) planDetail() string {
	switch {
	case m.firstPublish:
		return fmt.Sprintf("publish the project's first policy node, carrying %s, after your confirmation", strings.Join(firstPublishNodeOnlyMembers(m.lc), " and "))
	case m.generations != nil:
		return fmt.Sprintf("re-seed %s from the published policy node, after your confirmation", m.path)
	case m.record != "":
		return fmt.Sprintf("record the published policy node's generation (%s) in %s", m.record, m.path)
	case m.moved == nil && len(m.importCloneURLs) > 0 && m.pub == nil:
		return fmt.Sprintf("import the published policy node's clone_urls block into %s, after your confirmation", m.path)
	case m.moved == nil:
		return "publish the node the local config states, after your confirmation"
	case m.sameVersion() && m.pub == nil:
		return "rewrite the published policy node in place, after your confirmation"
	case m.sameVersion():
		return "rewrite the published policy node in place and publish what the local config states, in one write, after your confirmation"
	case m.pub == nil:
		return fmt.Sprintf("migrate the published policy from version %d to %d in place, after your confirmation", m.from, projectpolicy.Version)
	default:
		return fmt.Sprintf("migrate the published policy from version %d to %d in place and publish what the local config states, in one write, after your confirmation", m.from, projectpolicy.Version)
	}
}

// Apply is the write. The refusals are the ones the delete path has, in the
// same order and the same words where they are the same refusal: a coding
// agent's session, and no terminal (--yes is what stands in for the
// confirmation of the write where it reaches, confirmStageWrite, which asks
// nothing of a person and types nothing: T7, docs/no-prompt-policy). The answer
// about the runs in flight is the door's rather than the stage's
// (inflightAnswer, refuseRunsInFlight): `fugaro update` implies no refusal and
// no request to wait at all — the owner's ruling of 2026-11-09, the whole
// upgrade being this command — the doors that reach this stage without being
// the update door (`fugaro setup`, and the composite doors naming a subset of
// the stages) keep the answer of
// 2026-11-06 they had before it. The refusal --yes is the way past is
// `fugaro project policy publish --delete`'s, the same reading
// (internal/cli/project_cmd.go).
func (s *policyMigrateStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	// A managed project's gate is asked FIRST, and only this door has one: in a
	// coding agent's session on a managed project the step that rewrites the
	// published node is judged by the gate before it writes, and an admitted
	// one proceeds (M1, D1016). The session's own refusal is kept for every
	// other case, which is why it moves below the plan rather than going away:
	// the plan is read-only, so a session that is going to refuse refuses after
	// the same reads it has always made in the loop's preview pass, and the
	// sentence it reads is the one it always read.
	gate := s.e.r.gateUnderway()
	if !gate {
		if marker := agentMarker(os.Getenv, s.e.r.stdinTerminal()); marker != "" {
			return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
		}
	}
	if policyNodeUnreachable(s.e.lc) {
		return initflow.Outcome{Detail: policyNoDBReason}, nil
	}
	m, _, err := s.plan(ctx)
	var notOurs *policyMigrationNotOurs
	if errors.As(err, &notOurs) {
		return initflow.Outcome{Detail: notOurs.reason}, nil
	}
	if err != nil {
		return initflow.Outcome{}, err
	}
	if m == nil {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	// The write's own precondition, before the gate is asked and before the
	// confirmation: a node whose version rises is written by a run that will
	// replace the service reading it, and the image that replacement pins is
	// resolved here, before the write, so a registry read or a signature this
	// run cannot make fails the run with the node exactly where it was
	// (credentialsResolveBeforeRaise; a run that replaces nothing at all never
	// reaches this line, Check having held the stage back).
	if m.raisesVersion() {
		if err := s.e.credentialsResolveBeforeRaise(ctx); err != nil {
			return initflow.Outcome{}, err
		}
	}
	admitted, err := s.gateStep(ctx, m)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if !admitted {
		if marker := agentMarker(os.Getenv, s.e.r.stdinTerminal()); marker != "" {
			return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
		}
	}
	// The diff, the confirmation and every line of the write go through the
	// run's own writer, and the questions this stage asks are the shared ones
	// (confirmEmptyAllowList, confirmTypedStrict, confirmStageWrite), which hold
	// the command rather than the run: the whole call is put to the person with
	// the line in flight down (initRun.asking), so neither the diff nor the
	// CONFIRM block under it is drawn over (G1).
	cmd := s.e.r.cmd
	if err := s.e.r.asking(func() error {
		return policyMigrationConfirm(ctx, cmd, m, env.Yes, s.e.r.w, admitted, s.e.r.gateUnderway(), s.e.r.o.ownerHand(), s.e.r.conditions(), s.inflightAnswer())
	}); err != nil {
		return initflow.Outcome{}, err
	}
	if admitted {
		// The record follows the write (D1014) and not the admission: what an
		// owner reads is that this node was published, and a step that failed
		// inside the write has published nothing to record.
		if err := s.e.r.gateApplied(ctx); err != nil {
			return initflow.Outcome{}, err
		}
	}
	// The flip's file half, after the node's own write (update_proxy_switch.go
	// has the order's whole argument: an absent entry is never a disagreement,
	// so the file may land after the node and never before it). It is the same
	// run's apply and no second confirmation: what the operator confirmed is the
	// node, whose bitbucket_token block is the derived entry this write puts in
	// their file, and `fugaro secrets set --proxy` writes the same member with
	// the same edit and the same backup.
	if s.sw != nil {
		if err := s.sw.write(s.e, s.e.r.w); err != nil {
			return initflow.Outcome{}, err
		}
	}
	return initflow.Outcome{Changed: true, Detail: m.doneDetail()}, nil
}

// inflightAnswer is what the door this run was opened by does about the runs in
// flight over the node the stage writes.
//
// `fugaro update`'s answer is the owner's ruling of 2026-11-09 (inflightQuiet):
// the whole upgrade is that command, it is safe while runs are in flight, and
// it never asks anybody to wait. That ruling is that door's, and the doors that
// reach this stage without being it — `fugaro setup`, and the composite doors
// (`fugaro accounts add`, `accounts rm`, `accounts alias`), whose stage list is
// built from initStages through stagesNamed too — keep
// the answer they had before it (inflightManaged), which is the ruling of
// 2026-11-06 those doors' own users were given: a managed project's own session
// writes and is told the count, every other session is refused without --yes.
// A ruling widens the door it was made for, never every door into one stage —
// the same scoping the coding-agent marker takes (refuseRunsInFlight).
//
// It is read off the door and not taken as a parameter of the stage for the
// reason initflow.Managed gives for its own fields: a stage that could be
// handed any answer could be handed one no door gave it, and the door is the
// fact the answer is about.
func (s *policyMigrateStage) inflightAnswer() inflightAnswer {
	if s.e.r.o.doorName() == updateDoor.name {
		return inflightQuiet
	}
	return inflightManaged
}

// Verify reads the node back: a write that claims a version it did not land is
// a policy this Fugaro would keep rewriting on every run.
//
// The version is not the whole of the answer. A node already at this version
// whose one step changes nothing comes back byte for byte and is never judged
// by the reader gate — and that gate judged the document the migration was
// about to write, not the bytes the database now holds. Decode is that gate,
// called here on what was read back: a node at this version that the current
// reader refuses is a write that landed a policy no run of the project can
// admit itself by.
func (s *policyMigrateStage) Verify(ctx context.Context) error {
	if s.m == nil || !s.m.writesNode() {
		// The run wrote no node: D854's re-seed wrote this machine's file and left
		// the node exactly as it was, and a record-only run wrote the file alone.
		// There is nothing of this run's to read back, and the node a re-seed read
		// is another machine's to change. The question is asked of the write the
		// plan holds, never of a flag one branch of one confirmation sets: a
		// re-seed that carries a migration with it (writesNode) is a node write
		// like any other, and a Verify that skipped it would leave a migration that
		// did not land to the next run — the loop that keeps rewriting.
		return nil
	}
	_, found, raw, err := readPolicyNode(ctx, s.m.db, s.m.lc)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("the published policy is gone")
	}
	_, changes, err := projectpolicy.Migrate(raw)
	if err != nil {
		return err
	}
	if len(changes) > 0 {
		if ch := changes[0]; ch.From == ch.To {
			// The step that runs at a version rather than across two: the number
			// a version-move refusal names would be the one the node already
			// had, which reads as a node that converged when nothing landed.
			return fmt.Errorf("the published policy was written but the migration's step at version %d is still owed for it, so the run would rewrite the node again", ch.From)
		}
		return fmt.Errorf("the published policy is still version %d", changes[0].From)
	}
	p, _, err := projectpolicy.Decode(raw)
	if err != nil {
		return fmt.Errorf("the published policy was written but cannot be read back: %w", err)
	}
	if s.m.pub != nil {
		// A publish that landed is a node the config states what it says: the
		// write is the whole of the fix, and a node that still differs is a
		// publish this Fugaro would make again on every run.
		if drift := policyConfigDrift(s.m.lc, p, s.m.identities); len(drift) > 0 {
			return fmt.Errorf("the published policy was written but still does not carry what the local config states (%s)", memberListText(drift))
		}
	}
	return nil
}

// defaultProfileTimeoutClass is the task timeout class a written default
// profile states. A profile must state one — projectpolicy.validProfiles holds
// every profile to Cloud Run's own reading, and a profile that left the class
// out would make the publish refuse the config this step just wrote — and six
// hours is the class the docs, the fixtures and every profile an operator has
// today carry.
const defaultProfileTimeoutClass = "6h"

// defaultProfile is one default profile `fugaro update` writes: the provider it
// serves, the name it is written under, and the canonical repositories of that
// provider this project enrols.
type defaultProfile struct {
	Provider string
	Name     string
	Repos    []string
}

// jobProfile is the profile as it is written: the provider, the resources every
// profile must state, and the repositories the provider's runs are held to
// (R1). It carries no image field, because config.JobProfile has none (R1) —
// the job's image is the installation's — and no claude_accounts, which R1
// leaves "to the project's default account": the job a profile listing no
// account deploys is the profile's own default one.
func (d defaultProfile) jobProfile() config.JobProfile {
	return config.JobProfile{
		Provider:     d.Provider,
		CPU:          config.DefaultCPU,
		Memory:       config.DefaultMemory,
		TimeoutClass: defaultProfileTimeoutClass,
		AllowRepos:   d.Repos,
	}
}

// usedProviders is the providers this project's repositories use, in
// config.Providers order, with each provider's repositories as canonical names
// (R1: the default profile is the provider's, and allow_repos is derived from
// the repositories the project enrols, which is how a run of one of them is
// admitted).
//
// A repos: entry that records no provider is left out rather than guessed: the
// checkout's fugaro.yaml decides which provider that repository is on
// (localcfg accepts such an entry, allowSourcesOf skips it for the same
// reason), so this config does not say which default would serve it. A
// provider outside config.Providers is left out too: no policy may define a
// profile for it (projectpolicy.validProfiles), so writing one would make the
// publish refuse the config the update had just written.
func usedProviders(lc *localcfg.Config) ([]string, map[string][]string) {
	repos := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		r := lc.Repos[name]
		if r.Provider == "" {
			continue
		}
		c, err := projectpolicy.Canonical(r.Provider, name)
		if err != nil {
			continue
		}
		repos[r.Provider] = append(repos[r.Provider], c)
	}
	var order []string
	for _, p := range config.Providers {
		if _, ok := repos[p]; ok {
			order = append(order, p)
		}
	}
	return order, repos
}

// servedProviders is the providers the config's profiles block serves, in
// config.Providers order: every provider some profile in the block pins. It is
// the second half of the providers a default profile is written for (below) and
// the set the publisher asks its own rule over, so that the block `fugaro
// update` writes and the block `fugaro project policy publish` accepts cannot
// disagree about which defaults a block must define.
func servedProviders(lc *localcfg.Config) []string {
	served := map[string]bool{}
	for _, pr := range lc.Profiles {
		if pr.Provider != "" {
			served[pr.Provider] = true
		}
	}
	var order []string
	for _, p := range config.Providers {
		if served[p] {
			order = append(order, p)
		}
	}
	return order
}

// defaultProfileProviders is the providers this step writes a default profile
// for, in config.Providers order: the ones the project's repositories use (R1)
// and the ones the block already serves.
//
// The second half is what keeps the update converging. The publisher requires a
// default for every provider the block serves, so a config that pins, say, a
// Bitbucket profile with no Bitbucket repository enrolled would be refused —
// and refused by the policy stage of this very `fugaro update`, which is the
// one thing an update must never do (CLAUDE.md). Its default is written with
// no allow_repos at all: a provider with no repository of its own in this
// project falls back to the project's own list, which is the only list that
// could admit anything.
func defaultProfileProviders(used, served []string) []string {
	write := map[string]bool{}
	for _, p := range used {
		write[p] = true
	}
	for _, p := range served {
		write[p] = true
	}
	var order []string
	for _, p := range config.Providers {
		if write[p] {
			order = append(order, p)
		}
	}
	return order
}

// firstDefaultProvider is the provider whose default profile carries the name
// DefaultProfileName: the first of providers, unless the config already writes
// a profile under that name, which is the provider that profile serves.
//
// R8 keeps `standard` where it is — "standard stays the first provider's
// profile" — so a config whose standard profile is Bitbucket's gets its GitHub
// default written beside it as standard-github and never on top of it, and an
// operator's own standard profile is never the name a fresh default lands on.
// A standard profile that names no provider states nothing this can read: the
// publisher's own refusal for that profile is the one to read, and the name it
// holds is left alone (writtenDefaultProfiles only ever adds names the block
// does not define).
func firstDefaultProvider(lc *localcfg.Config, providers []string) string {
	if pr, ok := lc.Profiles[projectpolicy.DefaultProfileName]; ok && pr.Provider != "" {
		return pr.Provider
	}
	if len(providers) == 0 {
		return ""
	}
	return providers[0]
}

// writtenDefaultProfiles is the default profile of every provider given that
// the block does not define yet, in that order (R8, D2310): DefaultProfileName
// for the project's first provider and DefaultProfileName-<provider> for
// another. A name the config already defines is never returned, whatever it
// holds (R1): an existing profile is the operator's.
func writtenDefaultProfiles(lc *localcfg.Config, providers []string, repos map[string][]string) []defaultProfile {
	first := firstDefaultProvider(lc, providers)
	var out []defaultProfile
	for _, p := range providers {
		name := projectpolicy.DefaultProfileNameFor(p, p == first)
		if _, ok := lc.Profiles[name]; ok {
			continue
		}
		out = append(out, defaultProfile{Provider: p, Name: name, Repos: repos[p]})
	}
	return out
}

// defaultProfileNames spells the profiles of a write as a person reads them in
// the file: "profiles.standard, profiles.standard-bitbucket".
func defaultProfileNames(defaults []defaultProfile) string {
	return strings.Join(profileNames(defaults), ", ")
}

// profileNames is those names as a list, for the one line a withheld write
// prints: nameList is what caps it.
func profileNames(defaults []defaultProfile) []string {
	names := make([]string, len(defaults))
	for i, d := range defaults {
		names[i] = "profiles." + d.Name
	}
	return names
}

// installSpec is the installation spec a run plans from, with the one
// fallback the profiles `fugaro update` writes for itself need.
//
// installOptions refuses a local config that declares a github job profile and
// records no single GitHub App ID (projectJobGitHubAppID), and the default
// profile this step wrote is a github one for every github project (R1): a
// project whose last GitHub repository moves to Bitbucket, or that adds a
// repository on a second App, records no single ID any more, so the door has
// refused every update of that installation whole — with a message that asks
// for an App ID the project no longer has, on a profile the update wrote and the
// operator never touched. That is a failure of a HARD RULE (CLAUDE.md: `fugaro
// update` is the whole upgrade, and it works perfectly).
//
// The job that profile deployed is the installation's own record, and it is
// kept from it (infra.CarriedProjectJobs, prepare): what this composes is the
// spec of the config with no JOB for that profile, and the carry puts the
// deployed job back into the document the plan is made from, so the plan
// proposes no change to it and the run converges. The profile itself stays
// composed — in the spec's policy as in the config — because the published
// policy is where a workflow that names no profile reads it: a spec that
// dropped the profile whole would leave those workflows refused at run start
// (projectpolicy's rule that DefaultProfileName be defined), and the refusal
// would move from the update to every run of the project.
//
// The fallback is exactly this narrow: a default-named profile, of a provider a
// shared job serves, whose job the installation's own tfvars hold (and this
// run's spec does not). Every other refusal stays installOptions', whole: a
// config that declares a github profile and has never deployed its job has
// nothing to carry and is asked for the App ID, as it is today, and a profile
// the operator renamed or deleted is the config's fact with the remedy
// RefuseProjectJobDeletes prints for it.
// sayWithheld is how a run says a write it did not make: the one line the
// screen and the result's warnings read, and the reason whole, which the run's
// log keeps (initRun.withheld).
type sayWithheld func(line, why string)

func installSpec(o *initOptions, lc *localcfg.Config, say sayWithheld) (infra.InstallationSpec, error) {
	spec, err := installOptions(o, lc)
	if err == nil {
		return spec, nil
	}
	held := heldDefaultProfiles(o, lc)
	// The profile the refusal is about, and only it: the spec is composed once
	// with each held profile carried in turn, and the first composition that
	// succeeds names it. A profile the config can still compose is never
	// carried — its job follows the config, as an edit of it means it to — and
	// a refusal two of them together explain is not a refusal this carries:
	// installOptions' own message names that reason, and it stands.
	for _, name := range held {
		kept, kerr := installOptionsCarrying(o, lc, []string{name})
		if kerr != nil {
			continue
		}
		kept.CarriedProjectJobs = []string{name}
		say(withheldLine("kept the shared job of ", prefixedProfileNames([]string{name}),
			fmt.Sprintf(" as deployed: this config cannot compose it; a later %s writes one once fixed", selfCommand()+" update")),
			fmt.Sprintf("kept the shared job of %s as the installation holds it: this local config cannot compose it now (%s)",
				defaultProfileNameList([]string{name}), oneLineCLI(err.Error())))
		return kept, nil
	}
	return infra.InstallationSpec{}, err
}

// heldDefaultProfiles is the default profiles whose shared job the
// installation already holds, which is what installSpec carries: the profiles
// of the config the run cannot compose a job for, named by the installation's
// own tfvars.
//
// It answers for every door and not for the update alone (the owner's ruling of
// 2026-11-16 on S5): the hold is what keeps a default profile this config can no
// longer compose from taking the installation's jobs with it, and every door
// that composes that spec plans those same jobs — `fugaro accounts add` and
// `fugaro accounts rm` converge the Installation stage in-process, and a door
// that refused where another converges is an installation the operator cannot
// work on with the command they have. No door writes a default profile another
// holds back.
//
// What keeps this narrow is what it reads rather than the door it is under: a
// run with no local config has no profile to ask about, and a workdir that holds
// no tfvars — a machine whose installation this run has not planned yet, which
// is every first run, `fugaro setup` included — holds no job either. The config
// is the only thing that says what those profiles are, and installOptions'
// refusal is the answer for a profile whose job the installation never held.
//
// A --plan-only run is one of those doors and not an exception to them: it is
// the preview of the same converge, so it must not refuse an installation that
// the run it previews converges. What createDefaultProfiles writes is not part
// of this answer at all: it does not run under --plan-only, so a profile only
// that write would add is not in the config for this to name, and a profile the
// config states is one an earlier `fugaro update` wrote or the operator's own.
func heldDefaultProfiles(o *initOptions, lc *localcfg.Config) []string {
	if lc == nil {
		return nil
	}
	dir, err := infra.InstallationWorkdir(os.Getenv, lc.GCPProject)
	if err != nil {
		return nil
	}
	prev, err := infra.ReadWorkdirVars(dir, "installation")
	if err != nil {
		return nil
	}
	deployed, err := infra.ProjectJobProfilesOf(prev)
	if err != nil {
		return nil
	}
	var names []string
	for _, name := range slices.Sorted(maps.Keys(lc.Profiles)) {
		pr := lc.Profiles[name]
		if !infra.SharedJobProvider(pr.Provider) || !deployed[name] {
			continue
		}
		// The default names alone, as this step writes them (D2310, R8): the
		// name is the profile's, and a name the update would never write is the
		// operator's own profile, kept as the config states it.
		if name != projectpolicy.DefaultProfileNameFor(pr.Provider, true) && name != projectpolicy.DefaultProfileNameFor(pr.Provider, false) {
			continue
		}
		names = append(names, name)
	}
	return names
}

// defaultProfileNameList spells a held set as defaultProfileNames spells a
// written one, from the names the installation answered for.
func defaultProfileNameList(names []string) string {
	return strings.Join(prefixedProfileNames(names), ", ")
}

// prefixedProfileNames is those names as the list a withheld write's line reads,
// which is the list nameList cuts to the characters the line has.
func prefixedProfileNames(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "profiles." + n
	}
	return out
}

// createDefaultProfiles writes the default profile of every provider this
// project's repositories use and whose default profile the config does not
// define yet, so that the installation stage — the one that turns profiles into
// project jobs — deploys its job in this same `fugaro update` (S5, R1, D2310).
//
// It is the update door's alone: a profile describes a project that exists, and
// `fugaro setup` is a first run whose plan the operator reads before anything
// is created.
//
// The step is here, before the loop, and not a stage of its own: what it writes
// is an input to the spec the installation stage plans, so the apply must carry
// the profiles this run wrote (the same reason syncAccess is where it is), and
// it runs after makeOnboardingRepoEntry so that the repository this run onboards
// counts among the providers in use.
//
// The project's default catalog entry is written in the same step and after the
// profiles above, because the entry is written only when every repository this
// config lists is admitted by one of them (writeDefaultCatalogEntry).
func (e *initEngine) createDefaultProfiles(ctx context.Context) {
	r, o := e.r, e.r.o
	if e.lc == nil || o.doorName() != updateDoor.name || o.planOnly {
		return
	}
	providers, repos := usedProviders(e.lc)
	created := writtenDefaultProfiles(e.lc, defaultProfileProviders(providers, servedProviders(e.lc)), repos)
	hadBlock := e.lc.Profiles != nil
	if len(created) > 0 {
		if !hadBlock {
			e.lc.Profiles = map[string]config.JobProfile{}
		}
		for _, d := range created {
			e.lc.Profiles[d.Name] = d.jobProfile()
		}
	}
	// The entry joins the config before the spec below is composed, so the
	// installation stage deploys the shared jobs with the image this run just
	// gave the project (projectJobImage reads the default entry), and so that
	// the spec this run holds is the one its own config yields.
	e.writeDefaultCatalogEntry(ctx)
	if len(created) == 0 {
		return
	}
	// The written config is held to the deployer's own reading before it is
	// kept, and withdrawn whole when the deployer would refuse it: `fugaro
	// update` is the whole upgrade and must converge (CLAUDE.md), and a project
	// whose GitHub repositories record two different App IDs has no shared
	// GitHub job at all (infra.projectJobGitHubAppID refuses it), so a default
	// profile written there would stop the run whole. Nothing is written in
	// that case — the file keeps what the operator had, those repositories keep
	// the per-repository jobs they run today, and the line says why, so the
	// next update writes the profile once the reason is fixed.
	spec, err := installSpec(o, e.lc, r.withheld)
	if err != nil {
		for _, d := range created {
			delete(e.lc.Profiles, d.Name)
		}
		if !hadBlock {
			e.lc.Profiles = nil
		}
		r.withheld(
			withheldLine("no default profile written: this config cannot compose ",
				profileNames(created),
				fmt.Sprintf("; a later %s writes it once fixed", selfCommand()+" update")),
			fmt.Sprintf("no default profile was written (%s): %s",
				defaultProfileNames(created), oneLineCLI(err.Error())))
		return
	}
	// The spec every stage below builds is the one this config now yields, so
	// the installation stage plans and deploys the jobs of the profiles just
	// written (the rebuild init_members.go's defaultMembers does for the
	// members it discovered).
	e.spec = spec
	for _, d := range created {
		fmt.Fprintf(r.w, "%swrote the default profile %s (%s) to the local config: it is the job a %s workflow that names no profile runs on, and it admits %s\n",
			o.programPrefix(), d.Name, d.Provider, d.Provider, strings.Join(d.Repos, ", "))
	}
}

// defaultCatalogEntryName is the name of the entry `fugaro update` writes into
// a project's image catalog: the installation's pinned base image, as a
// prebuilt entry marked default (D2305, S10). The name is the base image kind's
// because that is what the entry holds, and `fugaro image ls` reads the two
// side by side.
const defaultCatalogEntryName = "base"

// writeDefaultCatalogEntry writes the project's default catalog entry into the
// local config, so that the shared project jobs of every profile have an image
// the project's own catalog names (internal/infra's projectJobImage resolves
// exactly this entry for them, F21) and so that the project has a default an
// operator can curate a baked image into.
//
// The entry is a reference to the base image this config pins, and nothing
// builds it (D2305): the write says which image the project's jobs run today
// and gives the catalog a place to grow, and it needs no build, no bucket and
// no cloud call of its own.
//
// It writes nothing, and says why, in every state where the entry would make
// this run refuse something it converges today. The entry is the image of every
// job whose repository names no image, so a project that keeps a per-repository
// job for any reason would meet the deployer's refusal for a bare `checkout:
// baked` workflow (internal/infra's unbuiltOnBaked, D1191) — and a repository
// keeps one whenever no profile admits it, whenever this run holds no checkout
// of it (RepoSpec.Shared is a mark no root reads yet, M7), or, for the checkout
// this run does hold, whenever its own workflows are such a workflow: the three
// states defaultCatalogEntryRefusal reads, and the reason a repository admitted
// to a profile is still read here. The other states are the project's own: a
// catalog this machine's config does not hold is not one to write into, and an
// entry the config already has under this name is the operator's.
//
// Every line it prints is one line — what happened, and whether anything is
// left for the operator — and the reason whole is in the run's log
// (withholdDefaultCatalogEntry, initRun.withheld), and it is silent in the one
// state where there is nothing to say: a project that already marks a default
// has the entry this write is for, whether an earlier update or the operator
// put it there.
func (e *initEngine) writeDefaultCatalogEntry(ctx context.Context) {
	r := e.r
	// The published node, once per run and through the run's own memo
	// (policyReadOnce), because it is where the project's catalog is when this
	// machine's config names none (the keep rule, D1140): an entry written over
	// a published catalog would replace it rather than be added to it, which is
	// the note `fugaro image add` prints for the same write.
	//
	// An older node is tolerated rather than reported, as it is everywhere else
	// this run reads one (init.go's effectiveVerifiers): it is the node this
	// same run migrates in place, so it is a state `fugaro update` has to
	// converge through and not a fact about the project's catalog.
	p, _, err := r.policy.read(ctx, e.lc)
	if err != nil && !olderPolicyNode(err) {
		e.withholdDefaultCatalogEntry(
			fmt.Sprintf("no default catalog entry written: the project's policy could not be read; a later %s retries", selfCommand()+" update"),
			fmt.Sprintf("the project's published policy could not be read (%s)", oneLineCLI(policyReadErr(err).Error())))
		return
	}
	var published *config.Catalog
	var publishedMap map[string]string
	if p != nil {
		published, publishedMap = p.ImageCatalog, p.ImageMap
	}
	cat := e.lc.ImageCatalogEffective(published)
	if cat != nil {
		if _, ok := catalog.Default(*cat); ok {
			// The project has a default entry, so there is nothing to write and
			// nothing to say: this write is for the project that has none.
			return
		}
	}
	if e.lc.ImageCatalog == nil && published != nil && len(published.Entries) > 0 {
		e.withholdDefaultCatalogEntry(
			"no default catalog entry written: the project publishes a catalog this config does not hold; nothing to do",
			"this machine's project config names no image catalog, and the project has one published, so an entry written here would replace that catalog rather than be added to it")
		return
	}
	base := e.lc.BaseImage(config.BaseKind)
	if base == "" {
		e.withholdDefaultCatalogEntry(
			"no default catalog entry written: this config records no base image to write the entry on; nothing to do",
			"this config records no base image for the base kind, so there is no reference to write the entry on")
		return
	}
	entry := config.Entry{
		Name:    defaultCatalogEntryName,
		Default: true,
		Source:  config.Source{Kind: config.SourceImage, Image: base},
	}
	if slices.ContainsFunc(catalogEntries(cat), func(x config.Entry) bool { return x.Name == entry.Name }) {
		e.withholdDefaultCatalogEntry(
			fmt.Sprintf("no default catalog entry written: the catalog already has an entry named %s; mark the entry you want as the default", entry.Name),
			fmt.Sprintf("this project's catalog already has an entry named %s, and an entry of the operator's is never written over: mark the entry you want as the default in the project config, or rename that one, and a later %s writes this entry",
				entry.Name, selfCommand()+" update"))
		return
	}
	if line, why := e.defaultCatalogEntryRefusal(cat, e.lc.ImageMapEffective(publishedMap), entry, p); why != "" {
		e.withholdDefaultCatalogEntry(line, why)
		return
	}
	next := config.Catalog{Entries: append(catalogEntries(cat), entry)}
	if problems := config.ValidateCatalog("image_catalog", next); len(problems) > 0 {
		e.withholdDefaultCatalogEntry(
			"no default catalog entry written: this project's catalog cannot carry the entry; nothing to do",
			fmt.Sprintf("the entry is not one this project's catalog can carry (%s: %s)", problems[0].Path, problems[0].Message))
		return
	}
	e.lc.ImageCatalog = &next
	fmt.Fprintf(r.w, "%swrote the default catalog entry %s (%s) to the local config: it is the image every shared job of this project runs, and a %s that gives it a baked image moves every job of the project onto it\n",
		r.o.programPrefix(), entry.Name, base, selfCommand()+" image add")
}

// repinDefaultCatalogEntry moves the project's default catalog entry onto the
// base image this run pinned, in the config the images stage is about to write.
//
// The entry write above runs before the loop, where the base image this config
// records is still the release behind — the pin moves in the images stage, which
// is the only step that resolves it — so an entry written there and never moved
// would name the runner image of the release behind, for every shared job of the
// project, and the early return above would keep it there: the entry is the
// image the jobs run, and this is the step that knows where the base moved to.
//
// It moves only what this tool put in place, by the rule the same stage applies
// to base_images (isManaged, and never downgraded): an entry an operator gave a
// baked image, or one whose image is not a base pin of this kind, is theirs and
// is left alone.
func (e *initEngine) repinDefaultCatalogEntry(next *localcfg.Config, host, owner, kind, pin, version string) {
	if kind != config.BaseKind || next.ImageCatalog == nil || len(next.ImageCatalog.Entries) == 0 {
		return
	}
	entries := slices.Clone(next.ImageCatalog.Entries)
	moved := false
	for i, entry := range entries {
		if !entry.Default || entry.Source.Kind != config.SourceImage {
			continue
		}
		cur := entry.Source.Image
		if !isManaged(cur, host, kind, owner) || newer(curVersion(cur, host, kind, owner), version) || cur == pin {
			continue
		}
		entries[i].Source.Image = pin
		moved = true
		fmt.Fprintf(e.r.w, "  the default catalog entry %s = %s (the base image this run pinned)\n", entry.Name, pin)
	}
	if !moved {
		return
	}
	next.ImageCatalog = &config.Catalog{Entries: entries}
}

// withholdDefaultCatalogEntry is how this write says it did not write: one
// line — what happened and what, if anything, is left for the operator, which
// is why the line ends in "nothing to do" where the project needs no repair —
// and the reason whole, which the run's log keeps (initRun.withheld). It names
// no command but `fugaro update`, because every reason is a fact about the
// project's own configuration that the operator edits.
//
// The line carries the repositories where they are what the operator acts on
// and they fit within its bound (withheldLine), and the log carries them all
// either way: the owner's rule of 2026-11-24 is that this warning is one line
// of about withheldLineMax characters, and a project can list twenty
// repositories.
func (e *initEngine) withholdDefaultCatalogEntry(line, why string) {
	e.r.withheld(line, why)
}

// withheldLineMax is how long the line a withheld write builds may be: the
// owner's rule of 2026-11-24, that such a warning is one line of about this
// many characters rather than the 600 it was. It is the line itself, without
// the reporter's own `Warning: ` label in front of it (initRun.warn prints
// whatever the caller builds).
const withheldLineMax = 140

// withheldLine is one line of a withheld write: the fixed head and the fixed
// tail with the names the write is about between them, cut to as many names as
// leave the whole line within withheldLineMax characters
// (nameList). The count is what holds the bound for a project that lists twenty
// repositories or names two long profiles; what the line cuts is in the run's
// log with the rest of the reason (initRun.withheld).
func withheldLine(head string, names []string, tail string) string {
	return head + nameList(names, withheldLineMax-len(head)-len(tail)) + tail
}

// nameList names as many of the names a withheld write is about as fit budget
// characters, with the rest counted, "and 3 more". The count is part of the
// line, so a list that only fits without it is cut one name further: what the
// reader is left with has to say that it is not the whole list, or the line
// reads as a project with one repository.
//
// One name is the floor, and it is named whole even when it alone runs over the
// budget: a line a little long reads better than one whose remedy names no
// repository, and half a repository name is not one the operator can act on.
func nameList(names []string, budget int) string {
	if len(names) == 0 {
		return ""
	}
	shown := len(names)
	list := strings.Join(names, ", ")
	for shown > 1 && len(list) > budget {
		shown--
		list = strings.Join(names[:shown], ", ") + fmt.Sprintf(", and %d more", len(names)-shown)
	}
	return list
}

// catalogEntries is the entries of a catalog a config may not have at all, as a
// list a caller appends to: the copy is what keeps the config's own slice out
// of the write below.
func catalogEntries(cat *config.Catalog) []config.Entry {
	if cat == nil {
		return nil
	}
	return slices.Clone(cat.Entries)
}

// defaultCatalogEntryRefusal is what holds the entry back — the one line the
// screen reads, and the reason whole, which the run's log keeps — and both of
// them empty when nothing holds it back: the three states in which the entry
// would be the image of a job this run has to deploy and the deployer would
// refuse that job.
//
// The first is a repository the project does not admit. Such a repository runs
// on the per-repository job its own root deploys, and that job's image is the
// catalog's default — so a bare `checkout: baked` workflow of it, which carries
// no image of its own to keep instead, meets unbuiltOnBaked. The write waits
// for the whole list: the entry is the project's, and half a project on it is a
// project whose next update refuses.
//
// The second is every repository this run holds no checkout of, which is the
// same refusal one step earlier and the reason the two are separate: a
// repository admitted to a profile still deploys the per-repository job of its
// own root (RepoSpec.Shared is a mark no root reads yet, M7), and the entry is
// that job's image too — so a bare `checkout: baked` workflow of a repository
// this machine does not hold is refused by the deployer at its own repository
// stage, in a run this one cannot warn. Nothing about that file is readable
// from here (a checkout's workflows live in its own fugaro.yaml, and this
// machine holds one checkout), so the entry waits until the project lists no
// repository but this checkout's — the state in which the per-repository jobs
// the entry could be the image of are the ones this run reads.
//
// The third is the checkout's own workflows, which are the ones this run
// really builds (the repository stage and the images stage run for this
// checkout alone): the entry is asked of the one resolver here, so that a
// workflow that pins an entry, needs tags, or writes an image of its own — the
// three ways a job keeps an image that is not the default — is not read as one
// that would take it. What is left is the workflow the entry would be handed to
// and the deployer would refuse, and the workflow whose own resolution the
// entry would break: a project with no catalog has one the moment this entry is
// written, and a workflow's image_name, its needs and the project's image_map
// are read only against a catalog, so a job that runs the base image today can
// be refused the moment the entry lands.
func (e *initEngine) defaultCatalogEntryRefusal(cat *config.Catalog, imageMap map[string]string, entry config.Entry, node *projectpolicy.Policy) (line, why string) {
	if repos := unadmittedRepos(e.lc, node); len(repos) > 0 {
		return withheldLine("no default catalog entry written: this project does not admit ", repos, "; nothing to do"),
			fmt.Sprintf("this project does not admit %s — a repository is admitted when the project's published allow_repos names it and a profile of its provider admits it — and a repository it does not admit runs on the per-repository job its own root deploys, so the entry would be the image of that job: a checkout: baked workflow of it that writes no image of its own is refused there (unbuiltOnBaked, D1191)",
				strings.Join(repos, ", "))
	}
	cfg, slug := e.checkoutWorkflows()
	if repos := unvouchedRepos(e.lc, e.heldCanonical()); len(repos) > 0 {
		return withheldLine("no default catalog entry written: this machine holds no checkout of ", repos, "; nothing to do"),
			fmt.Sprintf("this project's config lists %s: a repository this run holds no checkout of keeps the per-repository job its own root deploys, whose image is this project's catalog default — where a checkout: baked workflow that writes no image of its own is refused (unbuiltOnBaked, D1191), and this run cannot read those workflows from here",
				strings.Join(repos, ", "))
	}
	if cfg == nil {
		return "", ""
	}
	asked := config.Catalog{Entries: append(catalogEntries(cat), entry)}
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		req := catalog.ImageRequest{ImageMap: imageMap, Canonical: slug, Workflow: cfg.Workflows[name], Backend: catalog.Backend(e.lc.Backend())}
		// The workflow's image is asked of the resolver twice, with the catalog
		// this project has (nil for a project with none) and with the one the
		// entry would give it, because the entry is what makes a catalog exist at
		// all and the resolver reads a workflow's image_name, its needs and the
		// project's image_map only against one: a pin naming an entry this project
		// does not have is inert today and a refusal the moment the entry is
		// written, so the write is held back by it rather than turning a job that
		// runs the base image into one the deployer refuses.
		req.Catalog = cat
		if _, err := catalog.JobImage(req); err != nil {
			// A workflow whose image is already unresolvable is not what holds the
			// write back: the entry did not break it, and a project refused for it
			// would never get a default entry at all.
			continue
		}
		req.Catalog = &asked
		sel, err := catalog.JobImage(req)
		if err != nil {
			return fmt.Sprintf("no default catalog entry written: workflow %s of %s would stop resolving; fix the image it names", name, slug),
				fmt.Sprintf("workflow %s of %s would stop resolving once this project has a catalog: %v — a workflow's image_name, its needs and the project's image_map are read only against one, and the entry is what makes one exist, so a job that runs the base image today would be refused the moment the entry is written",
					name, slug, err)
		}
		if sel.Source != catalog.SourceCatalog || sel.Entry.Name != entry.Name {
			continue
		}
		return fmt.Sprintf("no default catalog entry written: workflow %s of %s writes no image of its own; give it a dockerfile", name, slug),
			fmt.Sprintf("workflow %s of %s writes no image of its own, so the entry would be the image of a job that cannot run it and the deployer would refuse it (unbuiltOnBaked, D1191)",
				name, slug)
	}
	return "", ""
}

// checkoutWorkflows is the checkout's own fugaro.yaml and the repository it is
// about, nil when this run holds no checkout (a converge from a machine that
// does not hold the repository, which has no workflow to read). The repository
// is the checkout's own name (owner/name), which is the form the image
// selection is asked about; heldCanonical is the form the project's lists
// spell.
func (e *initEngine) checkoutWorkflows() (*config.Config, string) {
	if e.repo == nil || e.repo.tg == nil {
		return nil, ""
	}
	return e.repo.tg.cfg, e.repo.tg.repo
}

// heldCanonical is the checkout's repository as this project's allow-lists
// spell it ("github/acme/app"), "" when this run holds no checkout or nothing
// here names the repository's provider: the name unvouchedRepos compares the
// project's repositories against.
//
// The provider is the config's own for the checkout's entry, canonicalized
// exactly as usedProviders canonicalizes the list that comparison walks, so
// that the two spellings cannot disagree about one repository.
func (e *initEngine) heldCanonical() string {
	if e.repo == nil || e.repo.tg == nil {
		return ""
	}
	key := repoConfigKey(e.lc, e.repo.tg.repo)
	r, ok := e.lc.Repos[key]
	if !ok || r.Provider == "" {
		return ""
	}
	c, err := projectpolicy.Canonical(r.Provider, key)
	if err != nil {
		return ""
	}
	return c
}

// unadmittedRepos is the repositories this project's config lists that no
// profile of it admits, in the order the config names them: the repositories
// whose runs are per-repository ones.
//
// A repository is admitted when the project admits it on a profile of its
// provider — projectpolicy.Admits' answer, which is the gate a run of a shared
// job is held to (internal/runner's admissionCheck, and internal/credsvc's
// pairShared on the serving side), read here on the config this run is about to
// publish rather than on the node: the profile's own allow_repos when it names
// one, and the project's own enrolment otherwise.
func unadmittedRepos(lc *localcfg.Config, node *projectpolicy.Policy) []string {
	_, used := usedProviders(lc)
	var out []string
	for _, name := range config.Providers {
		for _, canonical := range used[name] {
			if !admittedByAProfile(lc, node, name, canonical) {
				out = append(out, canonical)
			}
		}
	}
	return out
}

// unvouchedRepos is the repositories this project's config lists that are not
// the checkout this run holds — held is that repository as the project's lists
// spell it — in the order the config names them: the ones whose own root this
// run does not read, and which therefore keep the per-repository job that root
// deploys — a repository the entry would be the image of just as much as one
// the project does not admit, and whose workflows nothing here can read.
//
// A held of "" is a run with no checkout, or one whose repository this config
// does not name: it vouches for no repository, so every one the config lists is
// unvouched.
func unvouchedRepos(lc *localcfg.Config, held string) []string {
	_, used := usedProviders(lc)
	var out []string
	for _, name := range config.Providers {
		for _, canonical := range used[name] {
			if canonical != held {
				out = append(out, canonical)
			}
		}
	}
	return out
}

// admittedByAProfile reports whether the project admits the repository on a
// profile of this provider, which is what projectpolicy.Admits answers: the
// project's own enrolment and the profile's list, both of them.
//
// The enrolment is the node's allow_repos, and reading it is not a formality: a
// publish keeps the list the node holds and never re-seeds it from this config
// (publishAllowRepos), so a repository this config lists and the node does not
// admit is one whose runs every shared job refuses — the state `fugaro doctor`
// reports as allow-list-uncovered — and it keeps the per-repository job its own
// root deploys whatever the profiles say.
//
// A profile that names no allow_repos admits what the enrolment admits, so the
// node's list is the one that decides for it. A node that is not there at all
// has no list to read, and this run's own publish is what seeds one from the
// repositories this config lists (publishAllowRepos seeds a node that is
// absent): those are the repositories the caller walked, so the enrolment half
// admits them.
func admittedByAProfile(lc *localcfg.Config, node *projectpolicy.Policy, provider, canonical string) bool {
	if node != nil && !projectpolicy.AdmitRepo(node.AllowRepos, canonical) {
		return false
	}
	for _, name := range slices.Sorted(maps.Keys(lc.Profiles)) {
		pr := lc.Profiles[name]
		if pr.Provider != provider {
			continue
		}
		if len(pr.AllowRepos) == 0 || projectpolicy.AdmitRepo(pr.AllowRepos, canonical) {
			return true
		}
	}
	return false
}
