package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/task"
)

// The `fugaro project` command group: the project's policy, its allow-list
// and what is enrolled (docs/plans/2026-10-14-project-centric.md, phase 1
// task 1.6). It is not internal/cli/project.go, which is project *selection*
// (checkoutProject); the group gets its own command files, of which this is
// the first (tasks 4.1 and 6.1 add project_enroll.go and project_export.go
// beside it).
//
// Every command here writes through the operator's own credentials (the same
// client `fugaro budget set` uses: IAM decides, and the rules grant no client
// write to config/*), never through a run token. Nothing on this path loads a
// secret's value: a provider entry carries the secret's NAME, and the node
// the publisher writes holds names and policy text only.
//
// What is *not* here, deliberately:
//
//   - the allow-list is seeded from the operator's own repositories and the
//     shared config's list, and from nothing else — never from the bucket's
//     runs/<slug>/ prefixes (B3): a slug is not invertible, and under phase
//     3's broadened runs/ grant every run of a profile can write those
//     prefixes, so a seed from there would let an enrolled repository enroll
//     itself. `project show` and `project repos ls` print the repositories
//     *running now* instead — the registry's runs in flight, which is all the
//     registry knows — where the operator reads them and adds a name on
//     purpose (Task 4.1 is the fuller version of that question, from a
//     durable source);
//   - `sync` and the discovery surface stay in phase 4 (Task 4.1). `repos
//     add` is here because between phases 1 and 4 a new repository would
//     otherwise be unenrolled (P4, 12a item 3).

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "The project's policy, its allow-list and the repositories enrolled",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newProjectPolicyCmd(), newProjectShowCmd(), newProjectReposCmd())
	return cmd
}

// newProjectPolicyCmd is `fugaro project policy`: the published node.
func newProjectPolicyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "The project's published policy (config/policy/shared)",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newProjectPolicyPublishCmd(), newProjectPolicyShowCmd())
	return cmd
}

type projectPolicyPublishOptions struct {
	cloud         cloudOptions
	seedRepos     bool
	delete        bool
	yes           bool
	removeRouting bool
}

func newProjectPolicyPublishCmd() *cobra.Command {
	var o projectPolicyPublishOptions
	cmd := &cobra.Command{
		Use:   "publish [--seed-repos] [--delete] [--yes] [--remove-routing]",
		Short: "Publish the project's policy from the operator's config (budget admins)",
		Long: "Build the project's policy from this machine's project config — the budget\n" +
			"block, the providers (their secrets by NAME, never a value), the model_prices\n" +
			"overrides and the allow-list — and write it to config/policy/shared with an\n" +
			"ETag-guarded write, printing what changes first. From then on the node is the\n" +
			"ceiling for every key it names, and a key it leaves out keeps the value the\n" +
			"job's environment was deployed with.\n\n" +
			"Publishing refuses without the rules marker (config/limits/policyRules): a\n" +
			"policy published before the rules are deployed is one the runs it bounds\n" +
			"cannot read, so run fugaro init --firebase with a Fugaro that deploys them\n" +
			"first. It also refuses a node written by a newer Fugaro.\n\n" +
			"allow_repos is seeded from this machine's repos: map and from the shared\n" +
			"config's repository list, and only when the node is absent or --seed-repos is\n" +
			"passed: a later publish never re-adds a name an operator removed. It is never\n" +
			"seeded from the bucket's runs/<slug>/ prefixes, which every run of a profile\n" +
			"can write.\n\n" +
			"--delete removes the policy (the children first, the shared node last, so a\n" +
			"launch mid-delete still sees the policy and keeps its tighter ceiling) and\n" +
			"puts the project back on the ceiling its jobs' environments carry. It goes\n" +
			"through the same rules-marker gate as a publish, refuses without --yes while\n" +
			"runs are in flight, and asks you to type the project's name otherwise. The\n" +
			"config/policy/repos children are removed too, shared node or not: a delete\n" +
			"that failed on the shared node leaves them behind, and this is what clears\n" +
			"them.\n\n" +
			"routing.claude is written where the operator writes it: this config. A publish\n" +
			"refuses to drop an active route the config does not account for, and refuses\n" +
			"a config that writes a routing block naming no Claude switch in the same\n" +
			"state; --remove-routing is how the operator says the block goes away on\n" +
			"purpose.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runProjectPolicyPublish(cmd, &o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.seedRepos, "seed-repos", false, "seed allow_repos from the operator's repositories again")
	f.BoolVar(&o.delete, "delete", false, "remove the published policy (project-centric mode off again)")
	f.BoolVar(&o.yes, "yes", false, "confirm the deletion without typing the project's name")
	f.BoolVar(&o.removeRouting, "remove-routing", false, "drop the published policy's routing block on purpose (an active route stops being taken)")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// openProjectBudget is openBudget that keeps the selection: the publisher
// reads the operator's own config file (its providers block), and the
// selection is what names that file.
func openProjectBudget(ctx context.Context, o cloudOptions) (*localcfg.Config, *rtdb.Client, error) {
	sel, lc, err := selectProject(ctx, o)
	if err != nil {
		return nil, nil, err
	}
	// The shared config has no file, so it carries no providers: a publish
	// from it would write a policy with none.
	if err := refuseSharedWrite(sel); err != nil {
		return nil, nil, err
	}
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, nil, err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, nil, err
	}
	return lc, db, nil
}

func runProjectPolicyPublish(cmd *cobra.Command, o *projectPolicyPublishOptions) error {
	ctx := cmd.Context()
	lc, db, err := openProjectBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	if err := requirePolicyRules(ctx, db, lc); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for attempt := 1; attempt <= setAttempts; attempt++ {
		etag, exists, raw, err := readPolicyNode(ctx, db, lc)
		if err != nil {
			return err
		}
		if exists {
			if err := refuseNewerPolicy(raw); err != nil {
				return err
			}
		}
		if o.delete {
			// The ETag and the version this read is what the deletion is made
			// against: see deleteProjectPolicy.
			return deleteProjectPolicy(cmd, lc, db, out, exists, etag, o)
		}
		old, err := publishedPolicyOf(raw, exists)
		if err != nil {
			return err
		}
		allow, sources, err := publishAllowRepos(ctx, lc, old, o.seedRepos)
		if err != nil {
			return err
		}
		pol, keepRouting, err := buildProjectPolicy(lc, allow, old, o.removeRouting)
		if err != nil {
			return err
		}
		body, err := policyNodeBytes(pol, raw, exists, keepRouting)
		if err != nil {
			return err
		}
		if err := checkPolicyBytes(body); err != nil {
			return err
		}
		printPolicyDiff(out, old, pol, sources)
		err = db.PutIfMatch(ctx, projectpolicy.PathShared(), etag, json.RawMessage(body))
		switch {
		case err == nil:
			fmt.Fprintf(out, "set %s\n", projectpolicy.PathShared())
			return nil
		case errors.Is(err, rtdb.ErrPrecondition):
			fmt.Fprintln(out, "changed by someone else meanwhile; reading it again")
			continue
		default:
			return dbErr(lc, err, true)
		}
	}
	return remote(fmt.Errorf("the policy node kept changing under the write (%d attempts); nothing further was written; run it again", setAttempts))
}

// requirePolicyRules refuses a publish without the deployed rules marker
// (P3): the marker is what makes a published policy readable by the runs it
// bounds, and an installation whose rules predate the policy nodes has no
// node for a run to read — so publishing one would be a policy that silently
// never applies.
func requirePolicyRules(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) error {
	var raw json.RawMessage
	found, err := db.Get(ctx, projectpolicy.PathRulesMarker(), &raw)
	if err != nil {
		return dbErr(lc, err, false)
	}
	if !found {
		// No marker at all is a different state from an old one, and it
		// deserves its own sentence: there is nothing to be older than, and a
		// message naming a node that does not exist sends the operator
		// looking for it.
		return userErr("the deployed rules carry no policy marker at %s, so they predate the policy model: a policy published now is one the runs it bounds cannot read; re-run 'fugaro init --firebase' with fugaro >= %s first",
			projectpolicy.PathRulesMarker(), policySince)
	}
	var gen int
	switch {
	case json.Unmarshal(raw, &gen) != nil:
		return userErr("%v", &projectpolicy.MarkerError{Value: oneLine(string(raw))})
	case gen >= projectpolicy.RulesVersion:
		return nil
	}
	return userErr("%s", rulesSkewText())
}

// rulesSkewText is the one sentence for "the deployed rules are older than
// the marker": `policy publish` refuses with it and doctor's
// policy-rules-skew check prints it (plan task 1.6 step 4's fifth check),
// so the state reads the same whether the operator met it as a refusal or
// as a report. It is a plain sentence and not an error, because doctor has
// no error to wrap it in.
func rulesSkewText() string {
	return fmt.Sprintf("the deployed rules are older than the marker %s: re-run 'fugaro init --firebase' with fugaro >= %s first; a policy published now is one the runs it bounds cannot read",
		projectpolicy.PathRulesMarker(), policySince)
}

// readPolicyNode reads the shared node with its ETag, the precondition the
// write is made under.
func readPolicyNode(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) (etag string, found bool, raw json.RawMessage, err error) {
	etag, found, err = db.GetETag(ctx, projectpolicy.PathShared(), &raw)
	if err != nil {
		return "", false, nil, dbErr(lc, err, false)
	}
	return etag, found, raw, nil
}

// refuseNewerPolicy refuses a node written by a newer Fugaro (P8, the other
// direction): this binary cannot say what a policy of a later shape means, so
// it must not overwrite or delete it. The check reads the version field alone,
// so a node that is unreadable for another reason is still deletable (the
// restore path, Check 49 step 8) and is refused by publishedPolicyOf instead
// when a publish would have to merge with it.
func refuseNewerPolicy(raw []byte) error {
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil
	}
	if head.Version > projectpolicy.Version {
		return userErr("%s", fmt.Sprintf(projectpolicy.CLISkew, head.Version, projectpolicy.Version))
	}
	return nil
}

// publishedPolicyOf is the node as a policy, or nil when none is published.
// A node this Fugaro cannot read is refused rather than replaced: publishing
// over one would drop whatever it is that this binary does not understand.
func publishedPolicyOf(raw []byte, exists bool) (*projectpolicy.Policy, error) {
	if !exists {
		return nil, nil
	}
	p, found, err := projectpolicy.Decode(raw)
	var ve *projectpolicy.VersionError
	switch {
	case errors.As(err, &ve):
		if ve.Found > ve.Understood {
			return nil, userErr("%s", fmt.Sprintf(projectpolicy.CLISkew, ve.Found, ve.Understood))
		}
		// An older version (a hand-written node, or one a release before this
		// shape wrote). "Publish again" would be advice this very refusal
		// makes impossible — the publish is what is being refused — so the
		// remedy is the one that works: remove the node, then publish.
		return nil, userErr("the published policy is version %d and this Fugaro writes version %d: publishing over it would rewrite it in this Fugaro's shape, and this command will not do that unasked; remove it with 'fugaro project policy publish --delete' and publish again, or correct its version by hand", ve.Found, ve.Understood)
	case err != nil:
		return nil, userErr("the published policy at %s cannot be read, so it cannot be replaced: %v; fix it by hand, or remove it with 'fugaro project policy publish --delete'", projectpolicy.PathShared(), err)
	case !found:
		return nil, nil
	case len(p.UnknownKeys) > 0:
		return nil, userErr("%s lists %s, which this Fugaro does not know: publishing would drop them; upgrade Fugaro (fugaro upgrade), or fix the node by hand",
			projectpolicy.PathShared(), strings.Join(p.UnknownKeys, ", "))
	}
	return p, nil
}

// publishAllowRepos is the allow-list a publish writes: the node's own list,
// unless the node is absent or the operator passed --seed-repos (P4). A
// publish never silently re-adds a name an operator removed — removing one is
// how a repository is un-enrolled, and it is a security-relevant action.
func publishAllowRepos(ctx context.Context, lc *localcfg.Config, old *projectpolicy.Policy, seed bool) ([]string, map[string]string, error) {
	if old != nil && !seed {
		return nonNil(old.AllowRepos), nil, nil
	}
	return seedAllowRepos(ctx, lc)
}

// seedAllowRepos is the seed P4 names: the operator's own repos: map and the
// shared config's repository list, each converted with Canonical, and nothing
// else. The bucket's runs/<slug>/ prefixes are deliberately not a source
// (B3): a slug is not invertible without reading each run's task.json, and
// every run of a profile can write those prefixes under phase 3's grant, so a
// seed from them would enroll a repository the operator never named.
//
// The second return is where each entry came from, for the printed list.
func seedAllowRepos(ctx context.Context, lc *localcfg.Config) ([]string, map[string]string, error) {
	sources, err := allowSourcesOf(ctx, lc, true)
	if err != nil {
		return nil, nil, err
	}
	return slices.Sorted(maps.Keys(sources)), sources, nil
}

// allowSourcesOf is the seed with the posture chosen: every repository this
// machine's config names — its own repos: map, then the shared config's list —
// as canonical entries, and where each came from.
//
// strict is the writer's: an entry it cannot spell as a canonical name is
// refused, because a seed that quietly leaves one out is a publish that
// un-enrolls a repository which runs today, and the operator asked for that
// list to be written. The readers (`allowSources`, for `project show` and
// `project repos ls`) skip such an entry instead: they only label where an
// allow-list entry came from, a `repos:` entry need not name a provider at all
// (localcfg: the checkout's fugaro.yaml decides it), and a read-only command
// must not abort on a config the tool accepts — the same reason repoBySlug and
// the shared-config branch below skip these.
func allowSourcesOf(ctx context.Context, lc *localcfg.Config, strict bool) (map[string]string, error) {
	sources := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		r := lc.Repos[name]
		if r.Provider == "" {
			if strict {
				return nil, userErr("the local config's repos.%s names no git provider: the allow-list is canonical (provider/owner/name), so add provider: github or provider: bitbucket there", name)
			}
			continue
		}
		c, err := projectpolicy.Canonical(r.Provider, name)
		if err != nil {
			if strict {
				return nil, userErr("the local config's repos.%s: %v", name, err)
			}
			continue
		}
		sources[c] = "repos: " + name
	}
	shared, err := sharedRepos(ctx, lc)
	if err != nil {
		return nil, err
	}
	for _, name := range slices.Sorted(maps.Keys(shared)) {
		r := shared[name]
		if r.Provider == "" {
			continue // the shared config's own validation leaves these alone
		}
		c, err := projectpolicy.Canonical(r.Provider, name)
		if err != nil {
			continue
		}
		if _, ok := sources[c]; !ok {
			sources[c] = "the shared config"
		}
	}
	return sources, nil
}

// sharedRepos is the repository list of the config published to the runs
// bucket, the second seed source P4 names. A shared config that exists and
// cannot be read is a refusal rather than a smaller seed: seeding is what
// keeps a repository that runs today enrolled, and a seed that silently
// leaves one out would un-enroll it.
func sharedRepos(ctx context.Context, lc *localcfg.Config) (map[string]localcfg.Repo, error) {
	url := lc.BucketURL()
	if url == "" || url == "gs://" || fakeEndpointsOnGS(lc, url) {
		// No runs bucket to read a shared config from (a config with neither
		// runs_bucket nor bucket_url), or a fake installation whose gs://
		// bucket is not the one it stands for: there is no second seed source
		// here, and opening a real bucket would reach the network.
		return nil, nil
	}
	b, err := sharedBucketOpener(ctx, url)
	if err != nil {
		return nil, bucketErr(url, "opening the bucket", err)
	}
	defer func() { _ = b.Close() }()
	// ImageSource is part of the anchor (code review, high): ParseShared
	// refuses a published image_source this machine's local config does not
	// record (sharedcfg.go's "image_source" case), and a config published
	// from a machine that took a fork's base pin carries one. Leaving the
	// field out here refused every non-default installation's shared config
	// — and with it the allow-list seed of `project show`, `project repos ls`
	// and `project policy publish`, the publish included, so there was no
	// CLI path out. Every other reader of this anchor sets it.
	sc, refused, err := readShared(ctx, b, SharedAnchor{Name: lc.Name, GCPProject: lc.GCPProject, Bucket: lc.RunsBucketName(), ImageSource: lc.ImageSource})
	switch {
	case err != nil:
		return nil, err
	case refused != "":
		return nil, userErr("the shared config published to %s cannot be read (%s), so the repositories it lists cannot seed the allow-list; fix it, or enroll the repositories with 'fugaro project repos add'",
			infra.SharedConfigObject, refused)
	case sc == nil:
		return nil, nil
	}
	return sc.Repos, nil
}

// buildProjectPolicy is the node the publisher writes, from the operator's
// config. The version is this binary's (P8: a beta cannot publish a shape
// main cannot read), and the budget block always names a mode — an empty mode
// would take the mode from the job environment, which nobody reading the
// policy can see (H1), and the decoder refuses such a node anyway.
//
// The routing block comes from the operator's config, and a publish is where an
// edit to it takes effect (design §6.2 item 9): the switch is written in the
// local config — validated there against the providers it resolves through,
// config.CheckRouting — and only *delivered* to a run through this node.
//
// It is also the one key this command refuses to *drop*. The switch has a
// second definition site this command cannot read — the project layer carries
// the *claims* a route entry makes (`claude_models`), and the policy layer
// slice that writes routing is not part of this release (D1a) — so a node
// carrying an active route that this config cannot account for is a refusal,
// not a rewrite: a route that goes away is a Claude call that stops being sent
// where the owner put it, and printing the removal afterwards does not undo it.
// An inert block (via: direct) is kept as the node carries it — nothing is
// routed by it, so there is nothing to refuse and no reason to rewrite the
// node. Turning routing off is therefore explicit: write routing: {claude:
// {via: direct}} in the config and publish, or pass --remove-routing to drop
// the block itself.
//
// "This config cannot account for it" is about the *Claude switch*, not about
// the routing key: `routing: {}` and `routing: {claude:}` decode to a block
// with no Claude entry, and neither is a statement that the switch is off —
// the operator wrote a block with nothing in it, not `via: direct`. Reading
// either as "off" would turn an active route off on a publish that only prints
// the removal afterwards, so they are held to the same refusal.
//
// The second return value is whether the node's own `routing` member stands
// (policyNodeBytes carries it over byte for byte): true exactly when the config
// says nothing about Claude and --remove-routing was not passed, which is also
// when pol.Routing is the node's own block rather than this config's.
//
// A repository cannot reach this key either way: its fugaro.yaml has no routing
// (D1a), and `repos add|rm` edits the node as the document it is
// (patchPolicyJSON) so it cannot drop what this publisher writes.
func buildProjectPolicy(lc *localcfg.Config, allow []string, old *projectpolicy.Policy, removeRouting bool) (*projectpolicy.Policy, bool, error) {
	if lc.Budget == nil || lc.Budget.Mode == "" {
		return nil, false, userErr("this project's config sets no budget.mode, and a published policy's budget block must name one (off, observe or enforce): the mode decides whether the caps bite, and a block that leaves it out would take it from each job's environment, which nobody reading the policy can see; set budget: { mode: ... } in %s, or leave the policy unpublished", lc.Name)
	}
	silent := lc.Routing == nil || lc.Routing.Claude == nil
	switch {
	case silent && !removeRouting && nodeRoutesClaude(old):
		why := "your config sets no routing"
		if lc.Routing != nil {
			why = "your config's routing block names no Claude switch"
		}
		return nil, false, userErr("the policy published at %s carries a routing block this publish cannot account for (routing: %s), and %s: publishing from here would turn that route off, and a route that goes away is a Claude call that stops being sent where it was put. Write the same switch under routing: in %s to publish it as yours, write routing: {claude: {via: direct}} to say the switch is off on purpose, or pass --remove-routing to drop the block itself on purpose",
			projectpolicy.PathShared(), routingText(old.Routing), why, lc.Name)
	case !silent && removeRouting:
		return nil, false, userErr("--remove-routing drops the published policy's routing block, and %s writes one (routing: %s): one of the two has to go", lc.Name, routingText(lc.Routing))
	}
	keepRouting := silent && !removeRouting
	pol := &projectpolicy.Policy{
		Version:    projectpolicy.Version,
		Budget:     projectpolicy.Budget{Mode: lc.Budget.Mode, PerRunUSD: lc.Budget.PerRunUSD, AllowedModels: lc.Budget.AllowedModels, MaxRunTokens: lc.Budget.MaxRunTokens},
		AllowRepos: nonNil(allow),
		Providers:  policyProviders(lc),
		Prices:     policyPrices(lc),
	}
	switch {
	case !silent:
		pol.Routing = lc.Routing
	case keepRouting && old != nil:
		// Inert (or absent): kept as it is, never invented and never written
		// from a value this config does not hold. The member itself is carried
		// over byte for byte on top of this (policyNodeBytes, keepRouting).
		pol.Routing = old.Routing
	}
	return pol, keepRouting, nil
}

// nodeRoutesClaude reports whether the node being replaced carries a routing
// block that sends Claude calls somewhere: a via other than direct (D1b). An
// absent block, and via: direct, route nothing and are safe to carry over or
// leave behind.
func nodeRoutesClaude(p *projectpolicy.Policy) bool {
	return p != nil && p.Routing != nil && p.Routing.Claude != nil &&
		p.Routing.Claude.ViaValue() != projectpolicy.RouteDirect
}

// policyProviders is the policy's providers block, built from the operator's
// config.
//
// Each entry is the operator's own carrier, copied whole. That is the one
// shape that cannot drop a switch: require_zdr and price_policy are declared
// on config.ModelProvider and nowhere else, so a copy by name is exhaustive
// by construction, and the embedded carrier's UnmarshalYAML — which is what a
// decode of a projectpolicy.Provider would run — cannot fill a second,
// respelled declaration and leave the first at zero.
func policyProviders(lc *localcfg.Config) map[string]projectpolicy.Provider {
	if len(lc.Providers) == 0 {
		return nil
	}
	out := make(map[string]projectpolicy.Provider, len(lc.Providers))
	for name, mp := range lc.Providers {
		// One carrier, copied whole: require_zdr and price_policy live on
		// config.ModelProvider (the catalogue's own carrier), and the policy's
		// Provider embeds it rather than respelling either switch. A copy by
		// name could drop one, and — because the carrier owns UnmarshalYAML —
		// decoding the policy's Provider out of YAML or JSON would fill the
		// carrier and leave a respelled copy at zero. Neither can happen with
		// exactly one declaration of each key.
		out[name] = projectpolicy.Provider{ModelProvider: mp}
	}
	return out
}

// policyPrices is the operator's model_prices overrides, in the policy's own
// spelling (the catalogue's: the optional rates stay pointers, so an omitted
// one keeps the list default instead of being priced at zero). The keys are
// the model IDs as the operator wrote them; projectpolicy.Prices escapes them
// when the node is written, because an RTDB key cannot hold the "/" of
// deepseek/deepseek-v4-flash or the "." of deepseek/deepseek-v4.1-flash.
func policyPrices(lc *localcfg.Config) projectpolicy.Prices {
	if len(lc.ModelPrices) == 0 {
		return nil
	}
	out := make(projectpolicy.Prices, len(lc.ModelPrices))
	for id, p := range lc.ModelPrices {
		price := projectpolicy.Price{
			InputPerM: p.InputPerM, OutputPerM: p.OutputPerM,
			CacheWrite5m: p.CacheWrite5m, CacheWrite1h: p.CacheWrite1h,
			CacheRead: p.CacheRead, WebSearchPer1k: p.WebSearchPer1k,
		}
		if t := p.LongContext; t != nil {
			price.LongContext = &projectpolicy.PriceTier{AboveInputTokens: t.AboveInputTokens, InputPerM: t.InputPerM, OutputPerM: t.OutputPerM}
		}
		out[id] = price
	}
	return out
}

// policyNodeBytes is the document a publish writes: the policy built from the
// operator's config, with the `routing` member of the node being replaced
// carried over byte for byte.
//
// The member and not the decoded value, because routing is shared with
// releases that know more of it than this one does (phase 1b writes it, and a
// later release writes more): a key inside `routing` that this Fugaro does not
// model survives the decode — json.Unmarshal ignores it, and Decode cannot
// list it the way it lists an unknown top-level key — so re-encoding the
// decoded value would drop it. This is the same quiet loosening `repos add|rm`
// refuses to make by patching the document (patchPolicyJSON), and
// printPolicyDiff tells the operator the block is kept.
//
// What "carried over" means depends on what the operator's config says:
//
//   - the config writes no Claude switch (keepRouting): the node's member
//     stands whole, exactly as it is. Nothing routes differently, so there is
//     nothing to rewrite — and a key a newer release wrote anywhere inside
//     `routing` survives (TestProjectPolicyPublishKeepsTheRoutingKeys...).
//   - the config writes one: the switch is this publish's to write, so its
//     members are the config's — `claude.via` and `claude.models` are replaced
//     by what the operator wrote (and a `models` the config does not write is
//     *removed*, not set to null) — while every member this Fugaro does not
//     model, both beside `claude` (a `routing.gemini` block, say) and inside
//     it, is left as the node had it. Replacing the member wholesale instead
//     would drop `routing.gemini` with no refusal and no diff line (the review
//     of PR #414), and the node is the only place that block exists.
//
// A node with no routing member is written without one, which is direct (D1b).
func policyNodeBytes(pol *projectpolicy.Policy, oldRaw []byte, exists, keepRouting bool) ([]byte, error) {
	node, err := json.Marshal(pol)
	if err != nil {
		return nil, userErr("building the policy: %v", err)
	}
	if !exists {
		return node, nil
	}
	routing, ok, err := jsonMember(oldRaw, "routing")
	if err != nil {
		// The node decoded as a policy a moment ago (publishedPolicyOf), so a
		// member of it cannot be unreadable; refuse rather than write a node
		// that silently lost the block.
		return nil, userErr("%s cannot be read back as a document, so its routing block cannot be kept: %v; fix it by hand, or remove it with 'fugaro project policy publish --delete'", projectpolicy.PathShared(), err)
	}
	if !ok {
		return node, nil
	}
	// The member names, before anything is written into the block. Decode refuses
	// these nodes already (validRoutingMembers calls the same check), so this is
	// the write path's own gate rather than the only one: a publish that reached
	// here with a `Claude` beside a `claude`, or a `VIA` beside the `via` it is
	// about to patch, would write a block whose route encoding/json decides by the
	// order the keys happen to be in — and the patch matches keys exactly, so it
	// would leave both.
	if err := projectpolicy.CheckRoutingMemberNames(routing); err != nil {
		return nil, userErr("%s carries a routing block this Fugaro cannot read the way it is written: %v; fix it by hand, or remove it with 'fugaro project policy publish --delete'", projectpolicy.PathShared(), err)
	}
	if keepRouting || pol.Routing == nil || pol.Routing.Claude == nil {
		// The config says nothing about Claude (or --remove-routing: then
		// pol.Routing is nil and the member is dropped). Kept whole, or gone on
		// purpose.
		if keepRouting {
			patched, err := patchPolicyJSON(node, "routing", routing)
			if err != nil {
				return nil, userErr("building the policy: %v", err)
			}
			return patched, nil
		}
		return node, nil
	}
	claude, err := mergeRoutingClaude(routing, pol.Routing.Claude)
	if err != nil {
		return nil, userErr("building the policy: %v", err)
	}
	routing, err = patchPolicyJSON(routing, "claude", json.RawMessage(claude))
	if err != nil {
		return nil, userErr("building the policy: %v", err)
	}
	patched, err := patchPolicyJSON(node, "routing", json.RawMessage(routing))
	if err != nil {
		return nil, userErr("building the policy: %v", err)
	}
	return patched, nil
}

// mergeRoutingClaude is the node's `routing.claude` member with the switch the
// operator's config writes patched into it: via and models come from the
// config, a member this Fugaro does not model is left as the node had it.
//
// `models` is removed when the config writes none (an omitted key, not a null
// one: the policy's own decode reads a null list and an absent one the same,
// but a node is a document people read). A node with no claude member at all
// gets the config's switch as this Fugaro marshals it.
func mergeRoutingClaude(routing json.RawMessage, c *projectpolicy.ClaudeRoute) ([]byte, error) {
	claude, ok, err := jsonMember(routing, "claude")
	if err != nil {
		return nil, err
	}
	if !ok {
		return json.Marshal(c)
	}
	if len(c.Models) > 0 {
		claude, err = patchPolicyJSON(claude, "models", c.Models)
	} else {
		claude, err = dropPolicyJSONMember(claude, "models")
	}
	if err != nil {
		return nil, err
	}
	return patchPolicyJSON(claude, "via", c.ViaValue())
}

// dropPolicyJSONMember is raw with the one member key removed, and every other
// byte of the object as it was. It is patchPolicyJSON's sibling: a member the
// publish writes from the config cannot be *set* to "the config writes none",
// so the member is removed instead.
func dropPolicyJSONMember(raw []byte, key string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("the member is not a JSON object")
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, ok := kt.(string)
		if !ok {
			return nil, errors.New("the object has a member without a name")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if name == key {
			continue
		}
		k, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(v)
	}
	if _, err := dec.Token(); err != nil { // the object's closing brace
		return nil, err
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// jsonMember is the raw bytes of one member of a JSON object, and whether the
// object has it. It is patchPolicyJSON's reader: the members are streamed, so
// what comes back is the member's own bytes, not a re-encoding of its value.
func jsonMember(raw []byte, key string) (json.RawMessage, bool, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, false, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, false, errors.New("the node is not a JSON object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, false, err
		}
		name, ok := kt.(string)
		if !ok {
			return nil, false, errors.New("the node has a member without a name")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, false, err
		}
		if name == key {
			return v, true, nil
		}
	}
	return nil, false, nil
}

// checkPolicyBytes refuses bytes this Fugaro would write but cannot read back
// as its own policy: the last thing between a mistake in what the node is
// built from and a node the runs it bounds cannot use. Both writers go through
// it — the publisher, on the policy it built, and `repos add|rm`, on the node
// it patched — so neither can write something the other would refuse.
//
// The publisher builds the policy itself, so a node it cannot read is a bug in
// the publisher or an operator value the policy's own rules refuse (a
// price_policy outside its two values, a misspelt model pattern, a per_run_usd
// that rounds to nothing): either way nothing is written, and the operator
// sees which value it was.
func checkPolicyBytes(raw []byte) error {
	p, found, err := projectpolicy.Decode(raw)
	switch {
	case err != nil:
		return userErr("the policy this Fugaro would write is one it cannot read back, so nothing was written (%v): check the values it is built from — the budget block, the providers' models, model_prices and the allow-list — with 'fugaro validate'", err)
	case !found || p == nil:
		return userErr("the policy this Fugaro would write is one it cannot read back, so nothing was written: check the values it is built from with 'fugaro validate'")
	}
	return nil
}

// patchPolicyJSON is raw with the one top-level member key replaced by val, and
// every other byte of the node left as it was.
//
// `project repos add|rm` changes one key of a document this Fugaro shares with
// releases that know keys it does not: decoding the node into the typed Policy,
// changing allow_repos and encoding that writes back only the keys this Fugaro
// knows, so a key a newer release added to a block this Fugaro decodes but does
// not write — routing's — is dropped, and a dropped tightening key is a quiet
// loosening, in the one command whose help says only allow_repos changes. The
// top-level ones are refused before this is called (Decode can list those), and
// an unknown key inside a providers entry is refused by Decode too
// (projectpolicy.decodeProviders: ignoring one there means a misspelt
// allow_data_to is published as "every repository"); a key inside `routing` is
// the one Decode cannot list, which is exactly why the node is edited as the
// document it is rather than as a value of this Fugaro's type.
//
// The members are streamed rather than decoded into a map: a map would sort the
// keys and re-encode every nested value, where this keeps each value's own
// bytes and the order they were read in.
func patchPolicyJSON(raw []byte, key string, val any) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("the node is not a JSON object")
	}
	enc, err := json.Marshal(val)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	set := false
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, ok := kt.(string)
		if !ok {
			return nil, errors.New("the node has a member without a name")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if name == key {
			v, set = enc, true
		}
		k, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(v)
	}
	if _, err := dec.Token(); err != nil { // the object's closing brace
		return nil, err
	}
	if !set {
		k, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(enc)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// printPolicyDiff shows what the write changes. An absent node is printed in
// full; an existing one prints the lines that differ — the budget and the
// allow-list, every field of every provider entry, and every price override —
// so the operator reads the values about money and data sharing that this
// publish replaces rather than a count of them.
func printPolicyDiff(out io.Writer, old, new *projectpolicy.Policy, sources map[string]string) {
	if old == nil {
		fmt.Fprintf(out, "%s: not published yet; writing version %d\n", projectpolicy.PathShared(), new.Version)
	} else {
		fmt.Fprintf(out, "%s: published (version %d); writing version %d\n", projectpolicy.PathShared(), old.Version, new.Version)
	}
	now := policyLines(new)
	var before map[string]string
	if old != nil {
		before = policyLines(old)
	}
	diffLines(out, before, now)
	// The provider entries and the price overrides are named field by field:
	// they are the values the operator is publishing about money and data
	// sharing, and a line that only said "providers: openrouter" or
	// "model_prices: 3" would hide a changed base_url, a widened models list,
	// a data-sharing list that grew, a route fee or a rate (the review of
	// PR #390). The secret is its NAME — the node carries no value, and
	// TestPolicyCarriesNoSecretValue is the guard that it never does.
	diffLines(out, providerLines(old), providerLines(new))
	diffLines(out, priceLines(old), priceLines(new))
	// The routing switch moves with this publish: it comes from the operator's
	// config, so a route that appeared or changed is a line an operator reading
	// money-and-data-sharing changes must see — a route that quietly starts
	// sending Claude prompts to a provider (or stops) is exactly what this diff
	// is for. A route that *goes away* against the operator's silence cannot
	// reach here: buildProjectPolicy refuses a publish whose config says nothing
	// about Claude over a node that carries an active one. The one removal that
	// can reach here is the deliberate one, --remove-routing (or a config that
	// writes via: direct), and then the "-" line is the whole point of the diff.
	diffLines(out, routingLines(old), routingLines(new))
	for _, r := range new.AllowRepos {
		if src, ok := sources[r]; ok {
			fmt.Fprintf(out, "  allow_repos %s (%s)\n", r, src)
		} else {
			fmt.Fprintf(out, "  allow_repos %s (kept from the published node)\n", r)
		}
	}
}

// diffLines prints what a write changes, one line per key: the keys the write
// adds ("+"), the ones it changes ("~", with both values) and the ones it drops
// ("-"). The keys are already spelled for a reader ("provider x.base_url",
// "model_prices y"), so one function serves every part of the diff.
func diffLines(out io.Writer, before, now map[string]string) {
	for _, k := range slices.Sorted(maps.Keys(now)) {
		was, had := before[k]
		switch {
		case !had:
			fmt.Fprintf(out, "  + %s: %s\n", k, now[k])
		case was != now[k]:
			fmt.Fprintf(out, "  ~ %s: %s → %s\n", k, was, now[k])
		}
	}
	for _, k := range slices.Sorted(maps.Keys(before)) {
		if _, still := now[k]; !still {
			fmt.Fprintf(out, "  - %s: %s\n", k, before[k])
		}
	}
}

// providerLines is one line per value of every provider entry, for the diff. A
// nil policy (no node published) has no entries.
func providerLines(p *projectpolicy.Policy) map[string]string {
	if p == nil {
		return nil
	}
	out := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(p.Providers)) {
		pv := p.Providers[name]
		pre := "provider " + name + "."
		out[pre+"kind"] = pv.Kind
		out[pre+"base_url"] = pv.BaseURL
		out[pre+"auth"] = pv.Auth
		out[pre+"secret"] = pv.Secret
		out[pre+"models"] = fmt.Sprintf("%v", nonNil(pv.Models))
		out[pre+"allow_data_to"] = allowDataToText(pv)
		out[pre+"route_fee_pct"] = fmt.Sprintf("%g", pv.RouteFeePct)
		out[pre+"require_zdr"] = fmt.Sprintf("%v", pv.RequireZDROn())
		out[pre+"price_policy"] = pv.EffectivePricePolicy()
	}
	return out
}

// allowDataToText is a provider's allow_data_to for a line an operator reads:
// an absent list is the deliberate "every repository" the node means by it, and
// an empty line there would read as "nothing".
func allowDataToText(p projectpolicy.Provider) string {
	if len(p.AllowDataTo) == 0 {
		return "every repository (the key is absent)"
	}
	return fmt.Sprintf("%v", p.AllowDataTo)
}

// routingLines is the routing switch as diff lines: the via and the explicit
// list of Claude IDs, so a publish that adds, changes or removes a route says
// which one and for which models. A node with no block has no lines, which is
// what makes "the route is gone" print as a removal rather than as silence.
func routingLines(p *projectpolicy.Policy) map[string]string {
	if p == nil || p.Routing == nil || p.Routing.Claude == nil {
		return nil
	}
	c := p.Routing.Claude
	return map[string]string{
		"routing.claude.via":    c.ViaValue(),
		"routing.claude.models": fmt.Sprintf("%v", nonNil(c.Models)),
	}
}

// priceLines is one line per model under model_prices, for the diff: a count
// would hide a rate that changed, and which model it changed for.
func priceLines(p *projectpolicy.Policy) map[string]string {
	if p == nil {
		return nil
	}
	out := map[string]string{}
	for _, id := range slices.Sorted(maps.Keys(p.Prices)) {
		out["model_prices "+id] = priceText(p.Prices[id])
	}
	return out
}

// priceText is one model's rates: every rate the entry sets, and "the list
// default" for each optional one it leaves out (which is what the run prices
// it at — internal/pricing's Default* multipliers — so a line that omitted it
// would not be what the node does).
func priceText(p projectpolicy.Price) string {
	parts := []string{"input_per_m " + rateText(p.InputPerM), "output_per_m " + rateText(p.OutputPerM),
		"cache_write_5m " + rateText(p.CacheWrite5m), "cache_write_1h " + rateText(p.CacheWrite1h),
		"cache_read " + rateText(p.CacheRead), "web_search_per_1k " + rateText(p.WebSearchPer1k)}
	if t := p.LongContext; t != nil {
		parts = append(parts, fmt.Sprintf("long_context above_input_tokens %d: input_per_m %s, output_per_m %s",
			t.AboveInputTokens, rateText(t.InputPerM), rateText(t.OutputPerM)))
	}
	return strings.Join(parts, ", ")
}

// rateText is one per-million rate. It is the number the operator wrote, not a
// dollar figure: a rate is dollars per million tokens, and rendering $0.28/M
// as money would print it as zero.
func rateText(v *float64) string {
	if v == nil {
		return "the list default"
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// policyLines is one line per value the publisher sets, for the diff.
func policyLines(p *projectpolicy.Policy) map[string]string {
	models := "the job environment's list"
	if len(p.Budget.AllowedModels) > 0 {
		models = strings.Join(p.Budget.AllowedModels, ", ")
	}
	return map[string]string{
		"version":               fmt.Sprintf("%d", p.Version),
		"budget.mode":           p.Budget.Mode,
		"budget.per_run_usd":    usdVal(p.Budget.PerRunUSD),
		"budget.allowed_models": models,
		"budget.max_run_tokens": fmt.Sprintf("%d", p.Budget.MaxRunTokens),
		"allow_repos":           fmt.Sprintf("%v", nonNil(p.AllowRepos)),
		"providers":             strings.Join(slices.Sorted(maps.Keys(p.Providers)), ", "),
	}
}

// deleteProjectPolicy removes the published policy (B5): the child nodes of
// config/policy/repos first, the shared node last. The children go first
// because the shared node's existence *is* project-centric mode, so deleting
// it first and failing on a child would leave a $slug node behind that nothing
// reads, while deleting the shared node last means a failure leaves a policy
// that still governs. Runs in flight are what makes this ask: a repository
// that is mid-run keeps the policy until it finishes.
//
// The children are removed whether or not the shared node is there: a delete
// that failed on the shared node leaves them behind, and a later --delete that
// answered "nothing to delete" on the strength of the absent shared node would
// leave them forever (the review of PR #390). A governance node left under
// config/policy/repos is policy text a later release may read.
//
// etag is the one readPolicyNode returned for that first read — the node
// refuseNewerPolicy saw, the one the in-flight check and the confirmation were
// made about — and the shared node is deleted against it, not against a fresh
// read. Reading it again here would delete whatever a publish landing in that
// window wrote: an unconfirmed deletion of a node nobody read, and, if it
// carries a version this binary refuses to overwrite (P8), of a node it must
// not delete at all. A failed precondition is therefore a refusal, not a retry:
// a deletion is not repeated behind an operator's back.
func deleteProjectPolicy(cmd *cobra.Command, lc *localcfg.Config, db *rtdb.Client, out io.Writer, exists bool, etag string, o *projectPolicyPublishOptions) error {
	ctx := cmd.Context()
	children, err := policyRepoChildren(ctx, db, lc)
	if err != nil {
		return err
	}
	if !exists {
		// No policy is in force, so nothing has to be confirmed: what is left
		// is the children an earlier half-finished delete left behind, and only
		// that. With none, there is truly nothing to delete.
		if len(children) == 0 {
			fmt.Fprintf(out, "%s is not published and %s holds no children: nothing to delete\n", projectpolicy.PathShared(), projectpolicy.PathRepos())
			return nil
		}
		fmt.Fprintf(out, "%s is not published, but %s holds %d child node(s) an earlier delete left behind\n",
			projectpolicy.PathShared(), projectpolicy.PathRepos(), len(children))
	} else {
		inflight, err := runsInFlight(ctx, db, lc)
		if err != nil {
			return err
		}
		if inflight > 0 && !o.yes {
			return userErr("%d run(s) are in flight: a run that is mid-flight keeps the policy until it finishes, so --delete is refused without --yes; wait for them (fugaro runs ls), or pass --yes once you have read what it does", inflight)
		}
		if err := confirmRemoval(cmd, lc, o.yes, "this removes the published policy: every run falls back to the ceiling its job's environment carries, and the project's allow-list stops being enforced"); err != nil {
			return err
		}
	}
	// Each removal is printed as it happens: a deletion that stops at the last
	// node must not leave the operator guessing which ones went.
	for _, key := range children {
		path := projectpolicy.PathRepos() + "/" + key
		childEtag, found, err := db.GetETag(ctx, path, nil)
		if err != nil {
			// A read, so the refusal names the read role: see dbErr's flag.
			return dbErr(lc, err, false)
		}
		if !found {
			continue
		}
		if err := db.PutIfMatch(ctx, path, childEtag, nil); err != nil {
			return dbErr(lc, err, true)
		}
		fmt.Fprintf(out, "removed %s\n", path)
	}
	if exists {
		if err := db.PutIfMatch(ctx, projectpolicy.PathShared(), etag, nil); err != nil {
			if errors.Is(err, rtdb.ErrPrecondition) {
				return userErr("the policy node at %s changed while this deletion was being confirmed (another publish landed meanwhile), so it was not removed: the child nodes under %s are deleted first and are gone; read what the node now holds with 'fugaro project policy show', then run 'fugaro project policy publish --delete' again",
					projectpolicy.PathShared(), projectpolicy.PathRepos())
			}
			return dbErr(lc, err, true)
		}
		fmt.Fprintf(out, "removed %s\n", projectpolicy.PathShared())
	}
	if exists {
		fmt.Fprintln(out, "no policy is published: runs fall back to the ceiling their job's environment carries")
	}
	return nil
}

// policyRepoChildren is the keys under config/policy/repos, as stored (the
// keys are escaped slugs; a slug is [a-z0-9-], so this is only about not
// unescaping and re-escaping a hand-written key).
func policyRepoChildren(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) ([]string, error) {
	var m map[string]json.RawMessage
	if _, err := db.Get(ctx, projectpolicy.PathRepos(), &m); err != nil {
		return nil, dbErr(lc, err, false)
	}
	return slices.Sorted(maps.Keys(m)), nil
}

// runsInFlight counts the runs the registry still holds: a run's entry is
// written when it starts and removed when it ends, which is the same source
// `fugaro budget show` lists as running.
func runsInFlight(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) (int, error) {
	var agents map[string]map[string]budget.AgentEntry
	if _, err := db.Get(ctx, "agents", &agents); err != nil {
		return 0, dbErr(lc, err, false)
	}
	n := 0
	for _, runs := range agents {
		n += len(runs)
	}
	return n, nil
}

// confirmRemovalStrict asks for the project's name before an un-enrollment,
// and takes --yes out of the operator's hands: removing an allowed repository
// stops its runs (a run of a repository the allow-list does not admit is
// refused before it mints a git token), and a run that is in flight keeps the
// policy until it finishes. A flag typed in advance must not be what stops a
// run that is already running, so this asks for the name itself.
func confirmRemovalStrict(cmd *cobra.Command, lc *localcfg.Config, what string) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "⚠ CONFIRM (project %s): %s\n", lc.Name, what)
	if !stdinIsTerminal(cmd.InOrStdin()) {
		return userErr("%s: --yes does not confirm this one while a run is in flight; wait for it (fugaro runs ls), or type the project's name at a terminal", initflow.NeedsTerminal("the typed confirmation of this step"))
	}
	fmt.Fprintf(out, "Type %s to confirm: ", lc.Name)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return userErr("reading the confirmation: %v", err)
	}
	if strings.TrimSpace(line) != lc.Name {
		return userErr("not confirmed (the project's name was not typed); nothing was changed")
	}
	return nil
}

// confirmRemoval is confirmRemovalStrict where --yes may stand in for the
// typed name: the caller has checked that nothing is in flight.
func confirmRemoval(cmd *cobra.Command, lc *localcfg.Config, yes bool, what string) error {
	return confirmTyped(cmd, bufio.NewReader(cmd.InOrStdin()), lc, yes, what)
}

// ------------------------------------------------------- show, policy show

type projectShowOptions struct {
	cloud cloudOptions
	json  bool
}

// projectShowDoc is `project show`'s JSON shape, for watch-style consumers.
type projectShowDoc struct {
	Project    string      `json:"project"`
	GCPProject string      `json:"gcp_project"`
	Mode       string      `json:"mode"`
	Caps       showCaps    `json:"caps"`
	Policy     *showPolicy `json:"policy"`
	AllowRepos []string    `json:"allow_repos"`
	// Enrolled maps each allow-list entry to where it came from: the
	// operator's repos:, the shared config's list, or "" for one that is in
	// neither (added on purpose, or a pattern).
	Enrolled map[string]string `json:"enrolled"`
	// Running are the repositories the run registry holds, which is the runs
	// that are running now: an entry is written when a run starts and removed
	// when it ends, so an empty registry is "nothing is running", never "no
	// repository has ever run here".
	Running []string `json:"running_now"`
	// RunningNotEnrolled are the ones that are running and that the published
	// allow-list does not admit: their next run is refused. EnrolledNotRunning
	// is the other direction — allowed, and nothing of it is running now (or
	// has ever run here: the registry cannot tell the two apart, and neither
	// does this say).
	RunningNotEnrolled []string `json:"running_not_enrolled"`
	EnrolledNotRunning []string `json:"enrolled_not_running"`
}

// showPolicy is the published policy as this document renders it: the node's
// own bytes spell model_prices' member names the way RTDB requires (escaped,
// projectpolicy.Prices), and a consumer of this document is reading model IDs.
// The embedded Policy's own model_prices is shadowed by the field below, which
// is at a shallower depth and so is the one encoding/json writes.
type showPolicy struct {
	*projectpolicy.Policy
	Prices map[string]projectpolicy.Price `json:"model_prices,omitempty"`
}

// showPolicyOf is the document's policy, or nil when none is published.
func showPolicyOf(p *projectpolicy.Policy) *showPolicy {
	if p == nil {
		return nil
	}
	return &showPolicy{Policy: p, Prices: p.Prices}
}

// showCaps is the ceiling the installation's own nodes carry — the side of the
// picture a published policy sits on top of and may only tighten (§4). It is
// read from /config, so it is what a run would use with no policy published.
type showCaps struct {
	Global   showGlobalCaps          `json:"global"`
	Defaults showCapsPair            `json:"defaults"`
	Repos    map[string]showCapsPair `json:"repos,omitempty"`
}

// showCapsPair is a per-run and a daily cap, in the shape the caps nodes hold
// them (nil is "the node does not set one").
type showCapsPair struct {
	Repo         string   `json:"repo,omitempty"`
	PerRunCapUSD *float64 `json:"per_run_cap_usd"`
	DailyCapUSD  *float64 `json:"daily_cap_usd"`
}

type showGlobalCaps struct {
	PerRunCapUSD *float64 `json:"per_run_cap_usd"`
	DailyCapUSD  *float64 `json:"daily_cap_usd"`
}

func newProjectPolicyShowCmd() *cobra.Command {
	var o projectShowOptions
	cmd := &cobra.Command{
		Use:   "show [--json]",
		Short: "Show the published policy",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runProjectShow(cmd, &o, true) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func newProjectShowCmd() *cobra.Command {
	var o projectShowOptions
	cmd := &cobra.Command{
		Use:   "show [--json]",
		Short: "Show the effective project: caps, policy, allow-list, repositories running now",
		Long: "Print the effective project: the caps the installation's nodes carry (global,\n" +
			"defaults and per repository), the published policy, the allow-list with where\n" +
			"each entry came from, and the repositories the run registry holds. The registry\n" +
			"holds the runs in flight, so that list is what is running now — an empty one is\n" +
			"nothing running, not nothing that has ever run. The last pair is what an\n" +
			"operator needs: repositories that are running and are not on the allow-list\n" +
			"(their next run is refused until 'fugaro project repos add'), and allow-list\n" +
			"entries nothing is running now.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runProjectShow(cmd, &o, false) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runProjectShow(cmd *cobra.Command, o *projectShowOptions, policyOnly bool) error {
	ctx := cmd.Context()
	lc, db, err := openBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	p, found, err := projectpolicy.Read(ctx, db)
	if err != nil {
		return policyReadErr(err)
	}
	if err := refuseUnknownPolicyKeys(p); err != nil {
		return err
	}
	var cfg dbConfig
	if _, err := db.Get(ctx, "config", &cfg); err != nil {
		return dbErr(lc, err, false)
	}
	caps := capsFrom(cfg, lc)
	if policyOnly {
		if o.json {
			doc := map[string]any{"project": lc.Name, "policy": nil}
			if found {
				doc["policy"] = showPolicyOf(p)
			}
			return writeJSON(out, doc)
		}
		if !found {
			fmt.Fprintf(out, "project %s: no policy is published (%s); runs use the ceiling their job's environment carries\n", lc.Name, projectpolicy.PathShared())
			return nil
		}
		printPolicy(out, p)
		return nil
	}

	sources, err := allowSources(ctx, lc)
	if err != nil {
		return err
	}
	running, err := runningRepos(ctx, db, lc)
	if err != nil {
		return err
	}
	doc := projectShowDoc{Project: lc.Name, GCPProject: lc.GCPProject, Mode: cfg.Mode, Caps: caps,
		Enrolled: map[string]string{}, Running: running, RunningNotEnrolled: []string{}, EnrolledNotRunning: []string{}}
	if found {
		doc.Policy = showPolicyOf(p)
		doc.AllowRepos = nonNil(p.AllowRepos)
	}
	// Enrolled is one key per allow-list entry, and only those: where the
	// publisher would have seeded that entry from, or "" for one it would not
	// have (an entry added on purpose with 'project repos add', or a pattern).
	// The seed is wider than the allow-list — it is every repository this
	// machine's config names — so a name the operator removed with
	// 'project repos rm' is still in it, and reporting it as enrolled would
	// say a repository is admitted when the published policy does not admit
	// it.
	for _, r := range doc.AllowRepos {
		doc.Enrolled[r] = sources[r]
	}
	for _, r := range running {
		if !found {
			continue
		}
		if !projectpolicy.AdmitRepo(p.AllowRepos, r) {
			doc.RunningNotEnrolled = append(doc.RunningNotEnrolled, r)
		}
	}
	for _, r := range doc.AllowRepos {
		if !slices.Contains(running, r) {
			doc.EnrolledNotRunning = append(doc.EnrolledNotRunning, r)
		}
	}
	if o.json {
		return writeJSON(out, doc)
	}

	fmt.Fprintf(out, "project %s (GCP %s): mode %s\n", lc.Name, lc.GCPProject, orNone(cfg.Mode))
	printCaps(out, caps)
	if !found {
		fmt.Fprintf(out, "no policy is published (%s): runs use the ceiling above, as their job's environment carries it\n", projectpolicy.PathShared())
	} else {
		printPolicy(out, p)
	}
	fmt.Fprintln(out, "allow_repos:")
	switch {
	case !found:
		fmt.Fprintln(out, "  (no policy is published, so no allow-list is enforced)")
	case len(doc.AllowRepos) == 0:
		fmt.Fprintln(out, "  (empty: a published policy with an empty allow-list admits no repository at all)")
	}
	for _, r := range doc.AllowRepos {
		fmt.Fprintf(out, "  %s%s\n", r, sourceSuffix(doc.Enrolled[r]))
	}
	fmt.Fprintln(out, "repositories running now (from the run registry, which holds the runs in flight):")
	if len(running) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	for _, r := range running {
		mark := ""
		if slices.Contains(doc.RunningNotEnrolled, r) {
			mark = notAdmittedNote(r)
		}
		fmt.Fprintf(out, "  %s%s\n", r, mark)
	}
	for _, r := range doc.EnrolledNotRunning {
		fmt.Fprintf(out, "  %s is on the allow-list and has no run in flight here\n", r)
	}
	return nil
}

// printCaps prints the ceiling the installation's nodes carry: what a run uses
// with no policy published, and the ceiling a published policy may only
// tighten.
func printCaps(out io.Writer, c showCaps) {
	fmt.Fprintln(out, "caps (the installation's ceiling; a published policy tightens it):")
	fmt.Fprintf(out, "  global: per-run %s, daily %s\n", usdText(c.Global.PerRunCapUSD), usdText(c.Global.DailyCapUSD))
	fmt.Fprintf(out, "  defaults: repo per-run %s, repo daily %s\n", usdText(c.Defaults.PerRunCapUSD), usdText(c.Defaults.DailyCapUSD))
	if len(c.Repos) == 0 {
		fmt.Fprintln(out, "  (no per-repository caps)")
	}
	for _, k := range slices.Sorted(maps.Keys(c.Repos)) {
		r := c.Repos[k]
		name := r.Repo
		if name == "" {
			name = k
		}
		fmt.Fprintf(out, "  %s (slug %s): per-run %s, daily %s\n", name, k, usdText(r.PerRunCapUSD), usdText(r.DailyCapUSD))
	}
}

// capsFrom is showCaps from /config, naming a per-repository cap by the
// repository the operator's config says a slug is where it can.
func capsFrom(cfg dbConfig, lc *localcfg.Config) showCaps {
	out := showCaps{}
	if g := cfg.Caps.Global; g != nil {
		out.Global.PerRunCapUSD = usdPtr(g.PerRunMicros)
		out.Global.DailyCapUSD = usdPtr(g.DailyMicros)
	}
	if d := cfg.Caps.Defaults; d != nil {
		out.Defaults.PerRunCapUSD = usdPtr(d.RepoPerRunMicros)
		out.Defaults.DailyCapUSD = usdPtr(d.RepoDailyMicros)
	}
	bySlug := repoBySlug(lc)
	for _, k := range slices.Sorted(maps.Keys(cfg.Caps.Repos)) {
		c := cfg.Caps.Repos[k]
		pair := showCapsPair{Repo: c.Repo, PerRunCapUSD: usdPtr(c.PerRunMicros), DailyCapUSD: usdPtr(c.DailyMicros)}
		if pair.Repo == "" {
			pair.Repo = bySlug[k]
		}
		if out.Repos == nil {
			out.Repos = map[string]showCapsPair{}
		}
		out.Repos[k] = pair
	}
	return out
}

// repoBySlug maps a registry/caps key (the escaped slug) back to a canonical
// repository name, for the repositories this machine's config names. A slug
// is not invertible (task.Slug is a hash), so a slug the config does not
// account for stays a slug.
func repoBySlug(lc *localcfg.Config) map[string]string {
	out := map[string]string{}
	for name, r := range lc.Repos {
		if r.Provider == "" {
			continue
		}
		s, err := task.Slug(r.Provider, name)
		if err != nil {
			continue
		}
		c, err := projectpolicy.Canonical(r.Provider, name)
		if err != nil {
			continue
		}
		out[budget.Key(s)] = c
	}
	return out
}

func printPolicy(out io.Writer, p *projectpolicy.Policy) {
	fmt.Fprintf(out, "policy %s: version %d\n", projectpolicy.PathShared(), p.Version)
	for _, line := range slices.Sorted(maps.Keys(policyLines(p))) {
		fmt.Fprintf(out, "  %s: %s\n", line, policyLines(p)[line])
	}
	for _, name := range slices.Sorted(maps.Keys(p.Providers)) {
		pv := p.Providers[name]
		fmt.Fprintf(out, "  provider %s: %s %s, auth %s, secret %s, require_zdr %v, price_policy %s\n",
			name, pv.Kind, pv.BaseURL, pv.Auth, pv.Secret, pv.RequireZDROn(), pv.EffectivePricePolicy())
	}
	// One line per model, under the ID the operator priced it by: the node
	// stores them under an escaped key (an RTDB key cannot hold the "/" of
	// deepseek/deepseek-v4-flash), and a reader of this command is reading
	// model IDs.
	for _, id := range slices.Sorted(maps.Keys(p.Prices)) {
		fmt.Fprintf(out, "  model_prices %s: %s\n", id, priceText(p.Prices[id]))
	}
	if p.Routing != nil {
		fmt.Fprintf(out, "  routing: %s\n", routingText(p.Routing))
	}
}

// routingText is the policy's routing block as one line, for a command that
// reads the node — `policy show`, `project show`, and the refusal a publish
// makes when its config does not account for the block the node carries. A node
// with a switch must not look like a node without one.
func routingText(r *projectpolicy.Routing) string {
	if r == nil || r.Claude == nil {
		return "direct (no block)"
	}
	return fmt.Sprintf("claude via %s, models %v", r.Claude.Via, nonNil(r.Claude.Models))
}

// runningRepos is the repositories the run registry holds: the runs that are
// running now. Its entries are written when a run starts and removed when it
// ends (the same source `fugaro budget show` lists as running and
// runsInFlight counts), so an empty registry means nothing is running — it is
// not evidence that a repository has never run here, and no caller may say it
// is. The bucket's runs/<slug>/ prefixes are the durable source (a run's
// objects outlive it) and syncing from them is Task 4.1's.
//
// A registry key is a slug, not a repository name: task.Slug is a hash of the
// provider and the canonical name, so it is not invertible. Each is matched
// against the repositories this machine's config names and reported as a slug
// when none matches.
func runningRepos(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) ([]string, error) {
	var agents map[string]map[string]json.RawMessage
	if _, err := db.Get(ctx, "agents", &agents); err != nil {
		return nil, dbErr(lc, err, false)
	}
	known := repoBySlug(lc)
	running := map[string]bool{}
	for k := range agents {
		if c, ok := known[k]; ok {
			running[c] = true
			continue
		}
		running[k] = true
	}
	return slices.Sorted(maps.Keys(running)), nil
}

// allowSources says where each entry of the published allow-list came from,
// for `project show`. It is the same seed the publisher would compute, so an
// entry that is not in it was added on purpose (`project repos add`) or is a
// pattern.
func allowSources(ctx context.Context, lc *localcfg.Config) (map[string]string, error) {
	return allowSourcesOf(ctx, lc, false)
}

// refuseUnknownPolicyKeys is what every CLI reader of the published node does
// with a key this Fugaro does not know: it refuses the node rather than acting
// on a half-read policy, because a key nobody reads is a policy that never
// takes effect (projectpolicy's Decode tolerates them for the *runner*, which
// enforces every value it understands and warns about the rest, P8). One
// function for the readers, so `policy show`, `project show` and `repos ls`
// cannot disagree on the posture; the writers word their own refusal, because
// theirs is about dropping the keys (publishedPolicyOf).
func refuseUnknownPolicyKeys(p *projectpolicy.Policy) error {
	if p == nil || len(p.UnknownKeys) == 0 {
		return nil
	}
	return userErr("%s lists %s, which this Fugaro does not know: a key nobody reads is a policy that never takes effect; fix the policy, or upgrade Fugaro (fugaro upgrade)",
		projectpolicy.PathShared(), strings.Join(p.UnknownKeys, ", "))
}

// notAdmittedNote marks a repository that has run here and that the published
// allow-list does not admit: its next run is refused until it is enrolled. The
// remedy is 'fugaro project repos add', whose argument is a canonical name —
// and a name this command would refuse is not a remedy. seenRepos reports what
// the registry knows, which for a repository this machine's config does not
// name is the slug alone, and a slug is a hash of the provider and the
// canonical name (task.Slug): it cannot be turned back into one, so the
// operator has to name the repository instead of being handed an argument that
// fails.
func notAdmittedNote(r string) string {
	if projectpolicy.ValidPattern(r) != nil {
		return "  (not admitted: its next run is refused until the allow-list admits it; this is a slug, a hash of the repository's name, so name it: 'fugaro project repos add provider/owner/name')"
	}
	return "  (not admitted: its next run is refused until 'fugaro project repos add " + r + "')"
}

func sourceSuffix(src string) string {
	if src == "" {
		return ""
	}
	return "  (" + src + ")"
}

func orNone(s string) string {
	if s == "" {
		return "not set"
	}
	return s
}

// ---------------------------------------------------------------- repos

// newProjectReposCmd is `fugaro project repos`: the interim enrollment path
// between phases 1 and 4 (P4, 12a item 3). `sync` and the discovery surface
// stay in phase 4 (task 4.1).
func newProjectReposCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repos",
		Short: "The project's allow-list: which repositories may run",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newProjectReposLsCmd(), newProjectReposAddCmd(), newProjectReposRmCmd())
	return cmd
}

type projectReposOptions struct {
	cloud cloudOptions
	yes   bool
}

func newProjectReposLsCmd() *cobra.Command {
	var o projectReposOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the allow-list and the repositories running now",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runProjectReposLs(cmd, &o) },
	}
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func newProjectReposAddCmd() *cobra.Command {
	var o projectReposOptions
	cmd := &cobra.Command{
		Use:   "add <provider/owner/name | owner/name | *>",
		Short: "Enroll a repository: add it to the policy's allow-list",
		Long: "Add a canonical repository (github/acme/app, bitbucket/acme/*) to the\n" +
			"published policy's allow_repos, through the same ETag-guarded write the\n" +
			"publisher uses, and print the change. A bare owner/name takes its provider\n" +
			"from this machine's project config, and * is the list's own entry for every\n" +
			"repository: it is written as it is. Only allow_repos changes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectReposEdit(cmd, &o, args[0], false) },
	}
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func newProjectReposRmCmd() *cobra.Command {
	var o projectReposOptions
	cmd := &cobra.Command{
		Use:   "rm <provider/owner/name | owner/name | *>",
		Short: "Un-enroll a repository: remove it from the allow-list",
		Long: "Remove an entry from the published policy's allow_repos. Removing an allowed\n" +
			"repository stops its runs — a run of a repository the list does not admit is\n" +
			"refused before it mints a git token — unless another entry on the list still\n" +
			"admits it, which is the case a pattern makes (github/acme/app stays admitted\n" +
			"while github/acme/* is there); the confirmation says which of the two it is.\n" +
			"Removing * refuses every repository the rest of the list does not admit.\n" +
			"So it asks you to type the project's name, and while a run is in flight --yes\n" +
			"alone is not enough.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectReposEdit(cmd, &o, args[0], true) },
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the removal without typing the project's name (not while a run is in flight)")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runProjectReposLs(cmd *cobra.Command, o *projectReposOptions) error {
	ctx := cmd.Context()
	lc, db, err := openBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	p, found, err := projectpolicy.Read(ctx, db)
	if err != nil {
		return policyReadErr(err)
	}
	if !found {
		return userErr("no policy is published for project %s (%s): enroll repositories by publishing one ('fugaro project policy publish'), which seeds the allow-list from this machine's config", lc.Name, projectpolicy.PathShared())
	}
	if err := refuseUnknownPolicyKeys(p); err != nil {
		return err
	}
	sources, err := allowSources(ctx, lc)
	if err != nil {
		return err
	}
	running, err := runningRepos(ctx, db, lc)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "allow_repos (%s):\n", projectpolicy.PathShared())
	if len(p.AllowRepos) == 0 {
		fmt.Fprintln(out, "  (empty: a published policy with an empty allow-list admits no repository at all)")
	}
	for _, r := range p.AllowRepos {
		fmt.Fprintf(out, "  %s%s\n", r, sourceSuffix(sources[r]))
	}
	fmt.Fprintln(out, "repositories running now (from the run registry):")
	if len(running) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	for _, r := range running {
		mark := ""
		if !projectpolicy.AdmitRepo(p.AllowRepos, r) {
			mark = notAdmittedNote(r)
		}
		fmt.Fprintf(out, "  %s%s\n", r, mark)
	}
	return nil
}

// runProjectReposEdit adds or removes one allow_repos entry. It edits the
// node's allow_repos and nothing else: the rest of the published policy is
// written back as it was read, under the ETag the read returned, so a
// concurrent publisher is refused rather than overwritten.
func runProjectReposEdit(cmd *cobra.Command, o *projectReposOptions, repo string, remove bool) error {
	ctx := cmd.Context()
	// openProjectBudget, not openBudget: this writes the published policy node,
	// so it is a writer like `policy publish` and takes the same two refusals —
	// the shared config is not an operator's own installation (and its repos:
	// map is not the one a bare owner/name resolves against), and the database
	// must be this project's.
	lc, db, err := openProjectBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	canonical, err := canonicalRepoArg(lc, repo)
	if err != nil {
		return err
	}
	etag, exists, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		return err
	}
	if !exists {
		return userErr("no policy is published for project %s (%s): 'fugaro project repos add' edits the published allow-list, so publish one first ('fugaro project policy publish')", lc.Name, projectpolicy.PathShared())
	}
	if err := refuseNewerPolicy(raw); err != nil {
		return err
	}
	p, err := publishedPolicyOf(raw, true)
	if err != nil {
		return err
	}
	// before is the list as the node holds it, under its own allocation: allow
	// is edited in place (slices.DeleteFunc shifts and clears the backing
	// array; append and Sort write it), and nonNil hands back the argument
	// itself, so the two would otherwise be one list and the diff line would
	// print the post-edit list on both sides — on a command whose point is
	// showing the operator what the edit removed.
	before := append([]string{}, nonNil(p.AllowRepos)...)
	allow := nonNil(p.AllowRepos)
	have := slices.Contains(p.AllowRepos, canonical)
	if remove && !have {
		// A name that is not an entry can still be admitted by a pattern on the
		// list, and then the removal removes nothing and refuses nothing: saying
		// only "nothing to remove" would read as if the repository had stopped
		// running.
		for _, pattern := range p.AllowRepos {
			if projectpolicy.MatchRepo(pattern, canonical) {
				return userErr("%s is not an entry of the allow-list of %s; the pattern %s admits it, so its runs keep running — remove that pattern to refuse them",
					canonical, projectpolicy.PathShared(), pattern)
			}
		}
		return userErr("%s is not on the allow-list of %s: nothing to remove", canonical, projectpolicy.PathShared())
	}
	if !remove && have {
		fmt.Fprintf(out, "%s is already on the allow-list; nothing changed\n", canonical)
		return nil
	}
	if remove {
		inflight, err := runsInFlight(ctx, db, lc)
		if err != nil {
			return err
		}
		what := removalWarning(allow, canonical)
		switch {
		case inflight > 0:
			// --yes was typed before the run started, so it is not the
			// confirmation here: the project's name is.
			if err := confirmRemovalStrict(cmd, lc, fmt.Sprintf("%s; %d run(s) are in flight", what, inflight)); err != nil {
				return err
			}
		default:
			if err := confirmRemoval(cmd, lc, o.yes, what); err != nil {
				return err
			}
		}
		allow = slices.DeleteFunc(allow, func(r string) bool { return r == canonical })
	} else {
		allow = append(allow, canonical)
		slices.Sort(allow)
	}
	allow = nonNil(allow)
	// The node is patched, not rewritten: only allow_repos changes, and a key
	// this Fugaro does not know inside `routing` — a newer release's, where
	// Decode cannot list it — stays exactly as it was (patchPolicyJSON).
	// Providers entries are handled above: an unknown key inside one is a
	// refusal, not something to preserve.
	patched, err := patchPolicyJSON(raw, "allow_repos", allow)
	if err != nil {
		return userErr("editing %s: %v; nothing was written", projectpolicy.PathShared(), err)
	}
	if err := checkPolicyBytes(patched); err != nil {
		return err
	}
	fmt.Fprintf(out, "allow_repos: %v → %v\n", before, allow)
	if err := db.PutIfMatch(ctx, projectpolicy.PathShared(), etag, json.RawMessage(patched)); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return userErr("the policy changed while this ran; nothing was written: run it again")
		}
		return dbErr(lc, err, true)
	}
	fmt.Fprintf(out, "set %s\n", projectpolicy.PathShared())
	return nil
}

// removalWarning is what `repos rm` asks the operator to confirm. Removing an
// entry refuses the repository's runs — a run of a repository the list does not
// admit is stopped before it mints a git token — but only if nothing else on
// the list still admits it: `github/acme/*` admits `github/acme/app` whatever
// happens to the concrete entry, and a confirmation that said "its runs are
// refused from now on" would be telling the operator a removal stops runs it
// does not stop. Admission is the list's own matcher (projectpolicy.AdmitRepo,
// which the runner and the launcher use), so the sentence is what the next run
// will actually do.
func removalWarning(allow []string, canonical string) string {
	if still, by := admittedBy(allow, canonical); still {
		return "this removes " + canonical + " from the allow-list, but " + by + " still admits it: its runs are not refused, and a run in flight keeps the policy until it finishes"
	}
	return "this removes " + canonical + " from the allow-list: its runs are refused from now on, and a run in flight keeps the policy until it finishes"
}

// admittedBy is the entry of allow that still admits arg once arg's own entry
// is gone — the list the removal leaves, read with the same matcher the runner
// and the launcher use (projectpolicy.MatchRepo), so the sentence above is what
// the next run will actually do.
//
// One matcher answers both questions a removal raises. A concrete repository is
// admitted by any remaining entry that matches it: the wildcard, or a pattern
// such as github/acme/*. A *pattern* being removed is moot only if some
// remaining entry admits every repository it admitted — and MatchRepo answers
// that too, because it matches its second argument as a *repository*: an entry
// that matches the string github/acme/* is one whose own segments match
// "github", "acme" and "*", which is exactly what admitting github/acme/<any>
// takes. github/*/* matches it, github/ac*/* matches it (github/ac* is every
// owner whose name starts with "ac"), and github/*/app does not — its middle
// segment would have to match the literal string "*". The reading is sound in
// the direction that matters: a pattern that matches a string matches every
// expansion of it, since a "*" stands for any run of characters within one
// segment, so no entry can be reported as still admitting a repository the
// removal really does refuse.
//
// That soundness is a property of the matcher, not of this function, and it
// takes the "*" the second argument may carry being matched as the character
// it stands for: github/acme/*app removed while github/acme/* remains is not a
// revocation, and the matcher reads it that way only because a pattern's
// wildcard is tried before literal equality (projectpolicy.matchSeg).
//
// An entry is a pattern when it carries a wildcard, and that is not a question
// for this function: MatchRepo reads a concrete repository and a pattern alike,
// because ValidPattern accepts a concrete name too (a pattern with no "*"
// matches exactly itself) — so github/acme/app removed while github/acme/* is on
// the list reads "its runs are not refused", which is what the removal does.
func admittedBy(allow []string, arg string) (bool, string) {
	rest := make([]string, 0, len(allow))
	for _, p := range allow {
		if p != arg {
			rest = append(rest, p)
		}
	}
	for _, p := range rest {
		if projectpolicy.MatchRepo(p, arg) {
			return true, p
		}
	}
	return false, ""
}

// canonicalRepoArg is the operator's argument as a canonical allow-list
// entry. A bare owner/name takes its provider from this machine's config
// (repos: is keyed by owner/name); anything else must already be canonical,
// which is what the allow-list matches. Upper-case letters are folded — ASCII
// only, and a concrete name goes through projectpolicy.Canonical — so the
// entry written is one the runner's own canonical form could have produced.
func canonicalRepoArg(lc *localcfg.Config, arg string) (string, error) {
	if arg == "*" {
		// The allow-list's own entry for every repository — the design's
		// explicit choice (docs/design/project-centric.md §4.3), which
		// MatchRepo reads as admitting anything. It is not a repository, so
		// neither the config lookup below (there is no owner/name to resolve a
		// provider from) nor projectpolicy.Canonical applies to it: it is
		// taken as it is — projectpolicy.ValidPattern has a branch of its own
		// for it, so there is nothing left to check here.
		// Without this branch it fell through the split to "write it as
		// provider/owner/name", so the widest list could not be written and a
		// list holding it could not be narrowed without a full republish.
		return arg, nil
	}
	parts := strings.Split(arg, "/")
	if len(parts) == 2 {
		r, ok := lc.Repos[arg]
		if !ok || r.Provider == "" {
			return "", userErr("%q is not a repository this machine's config lists (its provider would come from there), so name it canonically: provider/owner/name, as in \"github/acme/app\" or \"bitbucket/acme/*\"", arg)
		}
		parts = []string{r.Provider, parts[0], parts[1]}
	}
	if len(parts) != 3 {
		return "", userErr("%q is not a repository: write it as provider/owner/name (github/acme/app), or as owner/name for a repository this machine's config lists", arg)
	}
	provider, repo := asciiLower(parts[0]), asciiLower(parts[1])+"/"+asciiLower(parts[2])
	canonical := provider + "/" + repo
	if !strings.Contains(canonical, "*") {
		// A concrete repository goes through projectpolicy.Canonical, the one
		// implementation of the form the runner compares an allow-list entry
		// against. A pattern is not a repository (Canonical would refuse its
		// "*"), so it is left to ValidPattern below.
		c, err := projectpolicy.Canonical(provider, repo)
		if err != nil {
			return "", userErr("%q is not a repository: %v", arg, err)
		}
		canonical = c
	}
	if err := projectpolicy.ValidPattern(canonical); err != nil {
		return "", userErr("%v", err)
	}
	return canonical, nil
}

// asciiLower lower-cases A–Z only, which is task's own rule for a repository
// (task.asciiLower, unexported there, reached through
// projectpolicy.Canonical). strings.ToLower case-folds Unicode instead: it
// turns a Kelvin sign K (U+212A) into an ASCII "k", so an argument typed with
// one would come out of here looking canonical while the runner's Canonical —
// ASCII-only, and the form the allow-list is matched in — refuses to produce
// that name at all. With this fold the look-alike survives into
// ValidPattern/Canonical, which refuse it.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
