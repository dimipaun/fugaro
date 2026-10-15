package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
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
//     *seen* instead, where the operator reads them and adds a name on
//     purpose (Task 4.1 is the fuller version of that question);
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
	cloud     cloudOptions
	seedRepos bool
	delete    bool
	yes       bool
}

func newProjectPolicyPublishCmd() *cobra.Command {
	var o projectPolicyPublishOptions
	cmd := &cobra.Command{
		Use:   "publish [--seed-repos] [--delete] [--yes]",
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
			"--delete removes the policy (the children first, then the shared node) and\n" +
			"puts the project back on the ceiling its jobs' environments carry. It refuses\n" +
			"without --yes while runs are in flight, and asks you to type the project's\n" +
			"name otherwise.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runProjectPolicyPublish(cmd, &o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.seedRepos, "seed-repos", false, "seed allow_repos from the operator's repositories again")
	f.BoolVar(&o.delete, "delete", false, "remove the published policy (project-centric mode off again)")
	f.BoolVar(&o.yes, "yes", false, "confirm the deletion without typing the project's name")
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
			return deleteProjectPolicy(cmd, lc, db, out, exists, o)
		}
		old, err := publishedPolicyOf(raw, exists)
		if err != nil {
			return err
		}
		allow, sources, err := publishAllowRepos(ctx, lc, old, o.seedRepos)
		if err != nil {
			return err
		}
		pol, err := buildProjectPolicy(lc, allow)
		if err != nil {
			return err
		}
		if err := checkPolicyWeWouldWrite(pol); err != nil {
			return err
		}
		printPolicyDiff(out, old, pol, sources)
		err = db.PutIfMatch(ctx, projectpolicy.PathShared(), etag, pol)
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
	var gen int
	switch {
	case !found:
	case json.Unmarshal(raw, &gen) != nil:
		return userErr("%v", &projectpolicy.MarkerError{Value: oneLine(string(raw))})
	case gen >= projectpolicy.RulesVersion:
		return nil
	}
	return userErr("the deployed rules are older than the marker %s: re-run 'fugaro init --firebase' with fugaro >= %s first; a policy published now is one the runs it bounds cannot read",
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
		return nil, userErr("the published policy is version %d and this Fugaro writes version %d: publishing again rewrites it in this Fugaro's shape", ve.Found, ve.Understood)
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
	sources := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		r := lc.Repos[name]
		if r.Provider == "" {
			return nil, nil, userErr("the local config's repos.%s names no git provider: the allow-list is canonical (provider/owner/name), so add provider: github or provider: bitbucket there", name)
		}
		c, err := projectpolicy.Canonical(r.Provider, name)
		if err != nil {
			return nil, nil, userErr("the local config's repos.%s: %v", name, err)
		}
		sources[c] = "repos: " + name
	}
	shared, err := sharedRepos(ctx, lc)
	if err != nil {
		return nil, nil, err
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
	return slices.Sorted(maps.Keys(sources)), sources, nil
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
	sc, refused, err := readShared(ctx, b, SharedAnchor{Name: lc.Name, GCPProject: lc.GCPProject, Bucket: lc.RunsBucketName()})
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
func buildProjectPolicy(lc *localcfg.Config, allow []string) (*projectpolicy.Policy, error) {
	if lc.Budget == nil || lc.Budget.Mode == "" {
		return nil, userErr("this project's config sets no budget.mode, and a published policy's budget block must name one (off, observe or enforce): the mode decides whether the caps bite, and a block that leaves it out would take it from each job's environment, which nobody reading the policy can see; set budget: { mode: ... } in %s, or leave the policy unpublished", lc.Name)
	}
	return &projectpolicy.Policy{
		Version:    projectpolicy.Version,
		Budget:     projectpolicy.Budget{Mode: lc.Budget.Mode, PerRunUSD: lc.Budget.PerRunUSD, AllowedModels: lc.Budget.AllowedModels, MaxRunTokens: lc.Budget.MaxRunTokens},
		AllowRepos: nonNil(allow),
		Providers:  policyProviders(lc),
		Prices:     policyPrices(lc),
	}, nil
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
// one keeps the list default instead of being priced at zero).
func policyPrices(lc *localcfg.Config) map[string]projectpolicy.Price {
	if len(lc.ModelPrices) == 0 {
		return nil
	}
	out := make(map[string]projectpolicy.Price, len(lc.ModelPrices))
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

// checkPolicyWeWouldWrite reads back the node the publisher is about to
// write. The publisher builds the policy itself, so a node it cannot read is
// a bug in the publisher or an operator value the policy's own rules refuse
// (a price_policy outside its two values, a misspelt model pattern, a
// per_run_usd that rounds to nothing): either way nothing is written, and the
// operator sees which value it was.
func checkPolicyWeWouldWrite(p *projectpolicy.Policy) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return userErr("building the policy: %v", err)
	}
	p, found, err := projectpolicy.Decode(raw)
	switch {
	case err != nil:
		return userErr("the policy this Fugaro would publish is one it cannot read back, so nothing was written (%v): check the values it is built from — the budget block, the providers' models, model_prices and the allow-list — with 'fugaro validate'", err)
	case !found || p == nil:
		return userErr("the policy this Fugaro would publish is one it cannot read back, so nothing was written: check the values it is built from with 'fugaro validate'")
	}
	return nil
}

// printPolicyDiff shows what the write changes. An absent node is printed in
// full; an existing one prints the lines that differ, then the provider
// entries with their two price-catalogue switches, because those are the
// values the operator is publishing about money and data sharing.
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
	for _, name := range slices.Sorted(maps.Keys(new.Providers)) {
		p := new.Providers[name]
		fmt.Fprintf(out, "  provider %s: secret %s, require_zdr %v, price_policy %s\n",
			name, p.Secret, p.RequireZDROn(), p.EffectivePricePolicy())
	}
	for _, r := range new.AllowRepos {
		if src, ok := sources[r]; ok {
			fmt.Fprintf(out, "  allow_repos %s (%s)\n", r, src)
		} else {
			fmt.Fprintf(out, "  allow_repos %s (kept from the published node)\n", r)
		}
	}
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
		"model_prices":          fmt.Sprintf("%d", len(p.Prices)),
	}
}

// deleteProjectPolicy removes the published policy (B5). The children go
// first and the shared node last: the shared node's existence *is*
// project-centric mode, so deleting it first and failing on a child would
// leave a $slug node behind that nothing reads, while deleting the shared
// node last means a failure leaves a policy that still governs. Runs in
// flight are what makes this ask: a repository that is mid-run keeps the
// policy until it finishes.
func deleteProjectPolicy(cmd *cobra.Command, lc *localcfg.Config, db *rtdb.Client, out io.Writer, exists bool, o *projectPolicyPublishOptions) error {
	ctx := cmd.Context()
	if !exists {
		fmt.Fprintf(out, "%s is not published: nothing to delete\n", projectpolicy.PathShared())
		return nil
	}
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
	removed := []string{}
	children, err := policyRepoChildren(ctx, db, lc)
	if err != nil {
		return err
	}
	for _, key := range children {
		path := projectpolicy.PathRepos() + "/" + key
		etag, found, err := db.GetETag(ctx, path, nil)
		if err != nil {
			return dbErr(lc, err, true)
		}
		if !found {
			continue
		}
		if err := db.PutIfMatch(ctx, path, etag, nil); err != nil {
			return dbErr(lc, err, true)
		}
		removed = append(removed, path)
	}
	etag, found, err := db.GetETag(ctx, projectpolicy.PathShared(), nil)
	if err != nil {
		return dbErr(lc, err, true)
	}
	if found {
		if err := db.PutIfMatch(ctx, projectpolicy.PathShared(), etag, nil); err != nil {
			return dbErr(lc, err, true)
		}
		removed = append(removed, projectpolicy.PathShared())
	}
	for _, p := range removed {
		fmt.Fprintf(out, "removed %s\n", p)
	}
	fmt.Fprintln(out, "no policy is published: runs fall back to the ceiling their job's environment carries")
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
	Project    string                `json:"project"`
	GCPProject string                `json:"gcp_project"`
	Mode       string                `json:"mode"`
	Caps       showCaps              `json:"caps"`
	Policy     *projectpolicy.Policy `json:"policy"`
	AllowRepos []string              `json:"allow_repos"`
	// Enrolled maps each allow-list entry to where it came from: the
	// operator's repos:, the shared config's list, or "" for one that is in
	// neither (added on purpose, or a pattern).
	Enrolled map[string]string `json:"enrolled"`
	// Seen are the repositories that have run here, from the run registry.
	Seen []string `json:"seen"`
	// SeenNotEnrolled are the ones that have run here and that the published
	// allow-list does not admit: their next run is refused. EnrolledNeverSeen
	// is the other direction — allowed, and nothing here has run yet.
	SeenNotEnrolled   []string `json:"seen_not_enrolled"`
	EnrolledNeverSeen []string `json:"enrolled_never_seen"`
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
		Short: "Show the effective project: caps, policy, allow-list, repositories seen",
		Long: "Print the effective project: the caps the installation's nodes carry (global,\n" +
			"defaults and per repository), the published policy, the allow-list with where\n" +
			"each entry came from, and the repositories that have run here. The last pair is\n" +
			"what an operator needs: repositories that are running and are not on the\n" +
			"allow-list (their next run is refused until 'fugaro project repos add'), and\n" +
			"allow-list entries nothing here has run yet.",
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
	if found && len(p.UnknownKeys) > 0 {
		return userErr("%s lists %s, which this Fugaro does not know: a key nobody reads is a policy that never takes effect; fix the policy, or upgrade Fugaro (fugaro upgrade)",
			projectpolicy.PathShared(), strings.Join(p.UnknownKeys, ", "))
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
				doc["policy"] = p
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
	seen, err := seenRepos(ctx, db, lc)
	if err != nil {
		return err
	}
	doc := projectShowDoc{Project: lc.Name, GCPProject: lc.GCPProject, Mode: cfg.Mode, Caps: caps,
		Enrolled: sources, Seen: seen, SeenNotEnrolled: []string{}, EnrolledNeverSeen: []string{}}
	if found {
		doc.Policy = p
		doc.AllowRepos = nonNil(p.AllowRepos)
	}
	for _, r := range seen {
		if !found {
			continue
		}
		if !projectpolicy.AdmitRepo(p.AllowRepos, r) {
			doc.SeenNotEnrolled = append(doc.SeenNotEnrolled, r)
		}
	}
	for _, r := range doc.AllowRepos {
		if !slices.Contains(seen, r) {
			doc.EnrolledNeverSeen = append(doc.EnrolledNeverSeen, r)
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
	fmt.Fprintln(out, "repositories seen (from the run registry):")
	if len(seen) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	for _, r := range seen {
		mark := ""
		if slices.Contains(doc.SeenNotEnrolled, r) {
			mark = "  (not admitted: its next run is refused until 'fugaro project repos add " + r + "')"
		}
		fmt.Fprintf(out, "  %s%s\n", r, mark)
	}
	for _, r := range doc.EnrolledNeverSeen {
		fmt.Fprintf(out, "  %s is on the allow-list and has never run here\n", r)
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
}

// seenRepos is the repositories that have run here, from the run registry's
// slugs. A slug is not invertible (task.Slug is a hash of the provider and
// the canonical name), so each is matched against the repositories this
// machine's config names and reported as a slug when none matches. The
// bucket's runs/<slug>/ prefixes are the other durable source, and the fuller
// answer is Task 4.1's.
func seenRepos(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) ([]string, error) {
	var agents map[string]map[string]json.RawMessage
	if _, err := db.Get(ctx, "agents", &agents); err != nil {
		return nil, dbErr(lc, err, false)
	}
	known := repoBySlug(lc)
	seen := map[string]bool{}
	for k := range agents {
		if c, ok := known[k]; ok {
			seen[c] = true
			continue
		}
		seen[k] = true
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

// allowSources says where each entry of the published allow-list came from,
// for `project show`. It is the same seed the publisher would compute, so an
// entry that is not in it was added on purpose (`project repos add`) or is a
// pattern.
func allowSources(ctx context.Context, lc *localcfg.Config) (map[string]string, error) {
	_, sources, err := seedAllowRepos(ctx, lc)
	if err != nil {
		return nil, err
	}
	return sources, nil
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
		Short: "List the allow-list and the repositories seen",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runProjectReposLs(cmd, &o) },
	}
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func newProjectReposAddCmd() *cobra.Command {
	var o projectReposOptions
	cmd := &cobra.Command{
		Use:   "add <provider/owner/name | owner/name>",
		Short: "Enroll a repository: add it to the policy's allow-list",
		Long: "Add a canonical repository (github/acme/app, bitbucket/acme/*) to the\n" +
			"published policy's allow_repos, through the same ETag-guarded write the\n" +
			"publisher uses, and print the change. A bare owner/name takes its provider\n" +
			"from this machine's project config. Only allow_repos changes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectReposEdit(cmd, &o, args[0], false) },
	}
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func newProjectReposRmCmd() *cobra.Command {
	var o projectReposOptions
	cmd := &cobra.Command{
		Use:   "rm <provider/owner/name | owner/name>",
		Short: "Un-enroll a repository: remove it from the allow-list",
		Long: "Remove an entry from the published policy's allow_repos. Removing an allowed\n" +
			"repository stops its runs: a run of a repository the list does not admit is\n" +
			"refused before it mints a git token. So it asks you to type the project's\n" +
			"name, and while a run is in flight --yes alone is not enough.",
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
	sources, err := allowSources(ctx, lc)
	if err != nil {
		return err
	}
	seen, err := seenRepos(ctx, db, lc)
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
	fmt.Fprintln(out, "repositories seen (runs in the registry):")
	if len(seen) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	for _, r := range seen {
		mark := ""
		if !projectpolicy.AdmitRepo(p.AllowRepos, r) {
			mark = "  (not admitted: its next run is refused until 'fugaro project repos add " + r + "')"
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
	lc, db, err := openBudget(ctx, o.cloud)
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
	before := nonNil(p.AllowRepos)
	have := slices.Contains(p.AllowRepos, canonical)
	if remove && !have {
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
		what := "this removes " + canonical + " from the allow-list: its runs are refused from now on, and a run in flight keeps the policy until it finishes"
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
		p.AllowRepos = slices.DeleteFunc(p.AllowRepos, func(r string) bool { return r == canonical })
	} else {
		p.AllowRepos = append(p.AllowRepos, canonical)
		slices.Sort(p.AllowRepos)
	}
	p.AllowRepos = nonNil(p.AllowRepos)
	if err := checkPolicyWeWouldWrite(p); err != nil {
		return err
	}
	fmt.Fprintf(out, "allow_repos: %v → %v\n", before, p.AllowRepos)
	if err := db.PutIfMatch(ctx, projectpolicy.PathShared(), etag, p); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return userErr("the policy changed while this ran; nothing was written: run it again")
		}
		return dbErr(lc, err, true)
	}
	fmt.Fprintf(out, "set %s\n", projectpolicy.PathShared())
	return nil
}

// canonicalRepoArg is the operator's argument as a canonical allow-list
// entry. A bare owner/name takes its provider from this machine's config
// (repos: is keyed by owner/name); anything else must already be canonical,
// which is what the allow-list matches.
func canonicalRepoArg(lc *localcfg.Config, arg string) (string, error) {
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
	canonical := strings.ToLower(parts[0]) + "/" + strings.ToLower(parts[1]) + "/" + strings.ToLower(parts[2])
	if err := projectpolicy.ValidPattern(canonical); err != nil {
		return "", userErr("%v", err)
	}
	return canonical, nil
}
