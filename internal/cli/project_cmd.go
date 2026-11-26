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
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/shellword"
	"github.com/dimipaun/fugaro/internal/task"
)

// The `fugaro project` command group: the project's policy, its allow-list
// and what is enrolled (docs/project-centric/implementation-plan.md, phase 1
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
	cmd.AddCommand(newProjectPolicyCmd(), newProjectShowCmd(), newProjectReposCmd(), newProjectSyncCmd(), newProjectCloneURLCmd(),
		newProjectProfileCmd(), newProjectRoutingCmd(), newProjectBinariesCmd())
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
		Use:   "publish [--seed-repos] [--delete] [--remove-routing]",
		Short: "Publish the project's policy from the operator's config (budget admins)",
		Long: "Build the project's policy from this machine's project config — the budget\n" +
			"block, the providers (their secrets by NAME, never a value), the model_prices\n" +
			"overrides and the allow-list — and write it to config/policy/shared with an\n" +
			"ETag-guarded write. The diff follows the write: it is printed once the node\n" +
			"holds the policy, and a write that failed prints no diff — the node is as it\n" +
			"was, and the command says so. From then on the node is the\n" +
			"ceiling for every key it names, and a key it leaves out keeps the value the\n" +
			"job's environment was deployed with.\n\n" +
			"Publishing refuses without the rules marker (config/limits/policyRules): a\n" +
			"policy published before the rules are deployed is one the runs it bounds\n" +
			"cannot read, so run fugaro update with a Fugaro that deploys them first. It\n" +
			"also refuses a node written by a newer Fugaro.\n\n" +
			"allow_repos is seeded from this machine's repos: map and from the shared\n" +
			"config's repository list, and only when the node is absent or --seed-repos is\n" +
			"passed: a later publish never re-adds a name an operator removed. It is never\n" +
			"seeded from the bucket's runs/<slug>/ prefixes, which every run of a profile\n" +
			"can write.\n\n" +
			"--delete removes the policy (the children first, the shared node last, so a\n" +
			"launch mid-delete still sees the policy and keeps its tighter ceiling) and\n" +
			"puts the project back on the ceiling its jobs' environments carry. It goes\n" +
			"through the same rules-marker gate as a publish, refuses while\n" +
			"runs are in flight, and asks you to type the project's name otherwise. The\n" +
			"config/policy/repos children are removed too, shared node or not: a delete\n" +
			"that failed on the shared node leaves them behind, and this is what clears\n" +
			"them.\n\n" +
			"Each repository an account admits also gets a child under\n" +
			"config/policy/repos/$slug, holding the NAMES of its admitted accounts and\n" +
			"nothing else. An account is admitted by the profiles that name it, each\n" +
			"qualified by its profile's provider, and by a repository's own\n" +
			"repos.<owner/name>.claude_accounts, qualified by that entry's provider:\n" +
			"an account this config defines but neither names admits nothing a run\n" +
			"could read — a repository whose runs are not on a profile job (a\n" +
			"per-repository job — what a repository the published policy serves no\n" +
			"profile for, and a `checkout: baked` one, keep) names its\n" +
			"accounts there, because a profile is not the only kind of job its runs\n" +
			"land on. The two are a union, so nothing a profile admits is narrowed by\n" +
			"a repository's entry.\n" +
			"The children are narrowed before the shared node and widened\n" +
			"after it, so a publish that stops between the two writes leaves the installation tighter than the config asked, never looser.\n" +
			"When the node this publish writes carries a member an older Fugaro refuses\n" +
			"whole — a credentials\n" +
			"block, or routing.claude.via: cascade — the diff warns: older launchers (and,\n" +
			"for the cascade, older runner images) stop until they are upgraded.\n\n" +
			"routing.claude is written where the operator writes it: this config. A\n" +
			"publish refuses to drop an active route the config does not account for, and\n" +
			"refuses to turn the credentials cascade off — a node carrying\n" +
			"routing.claude.via: cascade with a config that names no Claude switch is\n" +
			"refused with its own sentence, because the cascade is not a route: it decides\n" +
			"which credential carries a Claude call, never where the call goes, and it is\n" +
			"what hands a run the project's other credentials. A config that writes a\n" +
			"routing block naming no Claude switch in the same state is refused too.\n" +
			"--remove-routing is how the operator says the block goes away on purpose.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runProjectPolicyPublish(cmd, &o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.seedRepos, "seed-repos", false, "seed allow_repos from the operator's repositories again")
	f.BoolVar(&o.delete, "delete", false, "remove the published policy (project-centric mode off again)")
	f.BoolVar(&o.removeRouting, "remove-routing", false, "drop the published policy's routing block on purpose (an active route stops being taken)")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// openProjectBudget is openBudget that keeps the selection: the publisher
// reads the operator's own config file (its providers block), the selection is
// what names that file, and the path comes back with it because the generation
// a publish records goes into that file rather than into the one this package
// would derive from the project's name alone (a `--config` run's file is not the
// project's own, and a baseline recorded in the other file is one that file's
// business, not this run's).
func openProjectBudget(ctx context.Context, o cloudOptions, declined string) (*localcfg.Config, *rtdb.Client, string, error) {
	sel, lc, err := selectProject(ctx, o)
	if err != nil {
		return nil, nil, "", err
	}
	// The shared config has no file, so it carries no providers: a publish
	// from it would write a policy with none.
	if err := refuseSharedWrite(sel); err != nil {
		return nil, nil, "", err
	}
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, nil, "", err
	}
	// Every command that comes through here writes the published policy node,
	// so all of them ask the one ownership question first (declined is the
	// caller's word for what it did not write).
	if err := requireOwnBudgetDB(ctx, lc, db, declined); err != nil {
		return nil, nil, "", err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, nil, "", err
	}
	return lc, db, sel.Path, nil
}

func runProjectPolicyPublish(cmd *cobra.Command, o *projectPolicyPublishOptions) error {
	ctx := cmd.Context()
	// SD8: this command writes the node every run of the project is admitted by
	// — and, beside it, the per-repository admissions — so it is a person's write
	// and not a coding agent's, the same refusal `fugaro config publish`,
	// `fugaro recipes publish` and the credential commands carry. It holds for
	// --delete too: taking the policy away changes what every run is admitted by
	// exactly as much as publishing one does, and it is not an agent's to do on
	// its own.
	//
	// A MANAGED project is the one exception the design makes (M3, D1012): there
	// a coding agent's session reaches the deterministic gate instead, and the
	// rules of project_policy_managed.go decide. That gate refuses a widening of
	// any allow list, a raised cap, this command's three flags whatever the
	// session's arguments are — and, because those rules judge a handful of the
	// node's members and the node carries many, any change at all to a member no
	// rule of them reads (policyUnjudgedRule). So what the exception admits is a
	// publish whose every changed member is one a rule read and judged not to be
	// a widening: a node that narrows, or one that does not move.
	agent := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	lc, db, configPath, err := openProjectBudget(ctx, o.cloud, "nothing was published")
	if err != nil {
		return err
	}
	// The node is what every run of the project is admitted by, so publishing
	// it — and deleting it, which the same command does — is a project-level
	// write: admin at project scope (F19). It is asked before the rules and
	// before every read of the node, so a caller whose roles do not hold it
	// reads the sentence naming the role rather than anything about the node.
	if err := settingsAccessGate(ctx, lc, "fugaro project policy publish"); err != nil {
		return err
	}
	if err := requirePolicyRules(ctx, db, lc); err != nil {
		return err
	}
	// The gate is read here, before the node is, and only in a coding agent's
	// session: a person's own publish reads no mark and pays for no call
	// (project_policy_managed.go says why). A project the read does not prove is
	// managed keeps today's refusal, byte for byte, and reaches nothing else in
	// this function.
	gate, err := policyPublishGate(ctx, lc, db, agent)
	if err != nil {
		return err
	}
	if agent != "" && !gate.managed {
		return publishAgentRefusal(agent)
	}
	out := cmd.OutOrStdout()
	// narrowedSoFar is what the tightening phase has already written when a
	// later write of this publish fails: the failure message has to say it,
	// because the operator's next publish will not (the child already reads as
	// narrowed). It is accumulated across the retries of the loop, because a
	// narrowing that landed stays landed.
	var narrowedSoFar []string
	// emptyAllowListConfirmed is whether this run's one empty-allow-list
	// confirmation has been answered, so a retry of the write loop does not ask
	// the operator the same question again.
	var emptyAllowListConfirmed bool
	for attempt := 1; attempt <= setAttempts; attempt++ {
		etag, exists, raw, err := readPolicyNode(ctx, db, lc)
		if err != nil {
			return publishFailure(err, narrowedSoFar)
		}
		if exists {
			if err := refuseNewerPolicy(raw); err != nil {
				return publishFailure(err, narrowedSoFar)
			}
		}
		if o.delete {
			// The ETag and the version this read is what the deletion is made
			// against: see deleteProjectPolicy.
			//
			// And the gate, before a single child is touched: a removal is never
			// admitted (spec §4.2), so the verdict is built refused over the node
			// as it was read, and a coding agent's session reads why with the
			// record of that refusal behind it. In every other session the zero
			// gate answers here and the deletion proceeds exactly as it did.
			m, gerr := gate.admit(ctx, policyDeleteVerdict(raw))
			if gerr != nil {
				return gerr
			}
			if err := policyPublishProceed(agent, out, m); err != nil {
				return err
			}
			if err := deleteProjectPolicy(cmd, lc, db, out, exists, etag, o); err != nil {
				return err
			}
			// The record of an admitted removal, where a removal that was ever
			// admitted would have one: policyDeleteVerdict refuses by construction,
			// so today this call is answered by the gate that admitted nothing and
			// writes nothing. It is here rather than nowhere because the two halves
			// of the gate's contract are one rule — an admission is recorded AFTER
			// the write (spec §6, D1014) — and a door whose verdict changed would
			// otherwise remove the node every run is admitted by and leave no trail
			// of it, which is the one thing a record exists to prevent.
			return gate.recorded(ctx)
		}
		// The shared-job rule, at the operator's own door and nowhere else in
		// this run's path: a profile that mounts the Bitbucket credential is
		// refused here, before a node is built from this config, so the
		// operator reads it with the file in hand rather than meeting it at the
		// update's deploy, one step away from the entry
		// (refuseAProfileMountingTheBitbucketCredential says why the update's
		// policy stage is not a door of it).
		if err := refuseAProfileMountingTheBitbucketCredential(lc); err != nil {
			return publishFailure(err, narrowedSoFar)
		}
		old, err := publishedPolicyOf(raw, exists)
		if err != nil {
			return publishFailure(err, narrowedSoFar)
		}
		// The identities the node's bitbucket_tokens key carries (R12) are read
		// here, once, and handed to the build both doors share: the state store
		// is where the add-time check wrote them, and the node is the only
		// document the credentials service reads.
		identities, err := bitbucketTokenIdentities(ctx, db, lc)
		if err != nil {
			return publishFailure(err, narrowedSoFar)
		}
		// The one build both doors take (policyNodeForPublish): `fugaro update`'s
		// policy stage writes the node the local config states from the same
		// flags, and two builds of one node from one config is a node that
		// depends on which door the operator came through
		// (TestUpdateStageAndTheExplicitDoorBuildTheSameNode).
		body, pol, sources, err := policyNodeForPublish(ctx, lc, old, raw, exists, o.seedRepos, o.removeRouting, identities)
		if err != nil {
			return publishFailure(err, narrowedSoFar)
		}
		// C1's refusal at this door, before the empty-allow-list confirmation and
		// the gate: a published budget mode is read as the mode every run of the
		// project is metered by, and the word that was one is not one any more —
		// a node that says it is one no reader of this release serves (the
		// runner refuses the run, AB1b, and the decoder refuses the node, AB2),
		// so publishing it is what would stop the project's next run with a
		// sentence about a policy this very command wrote. Judged on the mode the
		// local config states, which is the word this publish is an attempt to
		// write: the build both doors take hands the node the mode that word asks
		// for (publishedMode, AB8a), so the node this door would write never
		// carries it and the refusal is what stands between the operator and a
		// config that says a word this release does not serve.
		if err := refuseARemovedBudgetMode(lc.Budget.Mode); err != nil {
			return publishFailure(err, narrowedSoFar)
		}
		// A policy whose allow-list admits nothing stops every run of the
		// project before it starts, which is doctor's allow-list-empty state —
		// and the diff that shows the empty list prints only after the write, so
		// it would announce the consequence only once it was in force. It is
		// said here, before the first write, and the person at the terminal is
		// what answers it: no flag stands in (this door takes no --json or
		// --non-interactive, and confirmEmptyAllowList refuses them and a
		// coding agent's marker where a command that does carries them),
		// nothing is typed (T7, docs/no-prompt-policy), and a session with no
		// terminal — a script's, or a coding agent's — is refused (#1674).
		// Asked once across the
		// retries below, because it is a decision about the document and not
		// about the attempt.
		if len(pol.AllowRepos) == 0 && !emptyAllowListConfirmed {
			if err := confirmEmptyAllowList(cmd, lc, askConditions(cmd, boolFlag(cmd, "json"), boolFlag(cmd, "non-interactive"))); err != nil {
				return publishFailure(err, narrowedSoFar)
			}
			emptyAllowListConfirmed = true
		}
		// The gate, over the exact bytes this publish would write (D1022) and
		// before the first write of this attempt — before the per-repository
		// children are narrowed, and before any argument of a write is built
		// (D1021). It is asked per attempt, so a retry after a precondition
		// failure re-reads the marks and re-judges the node it would now write
		// (D1024). A refusal is the gate's own sentence and the record of it is
		// written before this returns.
		m, err := gate.admit(ctx, policyPublishVerdict(old, pol, body, o.seedRepos, o.removeRouting))
		if err != nil {
			return err
		}
		if err := policyPublishProceed(agent, out, m); err != nil {
			return err
		}
		// The per-repository admissions (O4) and the shared node are two writes,
		// not one transaction (the children are a multi-path patch, the node is
		// a compare-and-set), so what a half-applied publish leaves behind is a
		// decision — and the decision is that a publish never *grants* anything
		// the shared node does not already carry. The children are narrowed
		// first (the accounts a child admits that the config no longer admits,
		// and the children of repositories no account admits any more) and
		// widened only after the node's own write has landed. A failure between
		// the two therefore leaves the installation tighter than the config
		// asked, never looser: the direction that is a security decision is a
		// revocation that silently did not happen, and it cannot happen here.
		// (The review of this change: writing every child first had that
		// direction too — a child added ahead of the shared node admits an
		// account the node does not cover yet.)
		desired, err := repoAdmissions(pol)
		if err != nil {
			return publishFailure(err, narrowedSoFar)
		}
		// Every child the widening phase will write is read back before the first
		// write of this publish, so a child this Fugaro could not read is a
		// refusal with nothing written rather than a node the next publish has to
		// stop on (admissionBytes).
		desiredBytes, err := admissionPatch(desired)
		if err != nil {
			return publishFailure(err, narrowedSoFar)
		}
		var existing map[string]json.RawMessage
		if _, err := db.Get(ctx, projectpolicy.PathRepos(), &existing); err != nil {
			return publishFailure(dbErr(lc, err, false), narrowedSoFar)
		}
		now, narrowed, err := tightenAdmissions(ctx, db, lc, desired, existing)
		if err != nil {
			return publishFailure(err, narrowedSoFar)
		}
		narrowedSoFar = append(narrowedSoFar, narrowed...)
		err = db.PutIfMatch(ctx, projectpolicy.PathShared(), etag, json.RawMessage(body))
		switch {
		case err == nil:
			// The record of the gate's admission, filed as soon as the node it
			// judged has landed and before the widening below: the node is the
			// write the gate admitted, and a trail filed at the end of the
			// command would be missing exactly where an owner needs it most —
			// this publish does end in failure after this point (the widening
			// can) and the node it wrote is real. A failed record is the
			// command's own failure (spec §6, D1014), and a write that failed
			// records nothing at all: there is no application of an admitted
			// step to file, and the node is as it was.
			if err := gate.recorded(ctx); err != nil {
				return err
			}
			admissions, err := widenAdmissions(ctx, db, lc, desired, desiredBytes, now)
			if err != nil {
				return fmt.Errorf("%w; %s was set, but the admissions at %s are not as the config asks: they were narrowed first and never widened, so the repositories that lost an account stay admitted to the accounts they kept; run the publish again",
					err, projectpolicy.PathShared(), projectpolicy.PathRepos())
			}
			for _, line := range append(append([]string{}, narrowed...), admissions...) {
				fmt.Fprintln(out, line)
			}
			// The declared allowlist's own half, after the node's write for the
			// reason the admissions are widened after it: it admits people, so a
			// publish that failed between the two leaves an installation tighter
			// than the config asked and never looser. It is skipped in a coding
			// agent's session (SD8): the node it writes is what admits a phone to
			// every run of the project, the managed gate judges the policy node
			// and not this one (policyUnjudgedRule reads the members of the node
			// this command publishes), so a session whose every other write is
			// judged would otherwise seed a widening nothing judged. A person
			// seeds it; `fugaro pair` and `fugaro pair revoke` are the writes a
			// project's devices are kept by.
			if agent != "" {
				if len(lc.MobileAllow) > 0 {
					fmt.Fprintf(out, "the declared allowlist seeded nothing: %s is a device node, and a device is a person's write (`fugaro pair`), not a coding agent's\n", rules.PathDevices)
				}
			} else if line, err := seedDeclaredDevices(ctx, db, lc, time.Now().UTC().Format(time.RFC3339)); err != nil {
				return fmt.Errorf("%w; %s was set, but the device node at %s is not as the declared allowlist asks: run this publish again", err, projectpolicy.PathShared(), rules.PathDevices)
			} else if line != "" {
				fmt.Fprintln(out, line)
			}
			// The diff is printed after the write, not before it (the review
			// finding on this change): it is a claim about the node as it now
			// is, and a write that came back refused — the policy changed
			// while this ran, or the credential may not write it — left the
			// node exactly as it was, so a diff printed first would have the
			// operator reading a change that did not happen.
			printPolicyDiff(out, old, pol, sources, lc)
			fmt.Fprintf(out, "set %s\n", projectpolicy.PathShared())
			// The generation this write moved the node to is recorded in the file
			// it was built from, the same record the policy stage keeps (D854): the
			// two doors write one node, so both of them have to leave the file
			// saying which generation it is written from. A door that recorded
			// nothing would leave the operator's next config edit refused against
			// the node this very command just wrote, with a re-seed offered as the
			// way out that would revert the edit.
			if err := recordPublishedGeneration(ctx, out, db, lc, configPath, body, pol); err != nil {
				return err
			}
			return nil
		case errors.Is(err, rtdb.ErrPrecondition):
			fmt.Fprintln(out, "changed by someone else meanwhile; reading it again")
			continue
		default:
			// And because nothing was printed, the failure has to say what the
			// operator is not seeing — which is not "nothing": the children
			// were narrowed before this write, and a narrowing is not undone by
			// the node's write failing.
			return fmt.Errorf("%w; %s is as it was%s", dbErr(lc, err, true), projectpolicy.PathShared(), narrowedNote(narrowedSoFar))
		}
	}
	return remote(fmt.Errorf("the policy node kept changing under the write (%d attempts); the shared node is as it was%s; run it again", setAttempts, narrowedNote(narrowedSoFar)))
}

// refuseARemovedBudgetMode is C1's refusal at the publish door: a local config
// that states the word the budget mode's vocabulary dropped is refused here,
// before a node is written, whatever the rest of the installation looks like.
//
// It is one refusal and not a conditional one because the word is not a mode at
// all any more (D3009): a node that carries it is one no reader of this release
// serves — the runner refuses the run that meets it, with the fix, and the
// node's own decoder refuses the document (AB2) — so a publish that wrote it
// would be a policy every run of the project stops on, written by the command
// the operator ran to give the project one. What the door says is where the word
// was written; what it means and the fix are the vocabulary's own text
// (policy.ModeOffRefusal), so this refusal and the flag's, the file's and the
// node's cannot drift (docs/always-a-budget/contract-mode.md).
//
// It reads the mode the local config states and not the node this publish would
// write, because that node no longer says the word at all: the build both doors
// take hands it the mode the word asks for (publishedMode, AB8a). The operator's
// file is the thing to fix, and the sentence says so.
//
// It replaces the narrower refusal of D2071 (a), which refused the word only
// while the credentials service served a switched repository. That state is
// gone with the word — it was the one publishing off made unrepairable, and
// nothing can be published in it now — and the mode a repository's own record
// was applied under is a fact of that record rather than of this write (F15,
// R9: the guards of the removed mode are deleted and not kept as dead code).
//
// It reads the mode through the vocabulary's own predicate and never compares
// the string here, so neither this line nor any other spells the word as a
// literal.
func refuseARemovedBudgetMode(mode string) error {
	if !policy.Mode(mode).Removed() {
		return nil
	}
	return userErr("the local config says the budget mode is %s: %s", mode, policy.ModeOffRefusal)
}

// requirePolicyRules refuses a publish without the deployed rules marker
// (P3): the marker is what makes a published policy readable by the runs it
// bounds, and an installation whose rules predate the policy nodes has no
// node for a run to read — so publishing one would be a policy that silently
// never applies.
//
// The bar is PolicyRulesVersion, the generation that introduced the policy
// nodes, and not RulesVersion: what the gate is about is whether the deployed
// rules carry the node being written and grant the read a run makes of it, and
// a marker between the two (generation 1, the policy generation, while this
// binary deploys generation 2) does both. Reading the newer generation as a
// bar would newly refuse a publish on an installation whose policy is read and
// enforced today, and would refuse it with a sentence about a policy nothing
// reads (rulesMarkerPrePolicyText) that is simply false there — the mirror of
// Read's own gate, which is the same predicate on purpose (the review finding
// on P2.1).
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
		//
		// The move is plain `fugaro update`, not `--firebase-project`: the
		// rules stage of the converge deploys this binary's generation over
		// rules whose marker is absent, and it does so in the same run as the
		// migration this refusal blocks (init_rules_stage.go, the owner's rule
		// of 2026-10-30). `--firebase-project` is the door that adopts a
		// Firebase project, which an installation that has one does not need.
		return userErr("the deployed rules carry no policy marker at %s, so they predate the policy model: a policy published now is one the runs it bounds cannot read; run 'fugaro update' with fugaro >= %s first, which deploys this Fugaro's rules under the policy",
			projectpolicy.PathRulesMarker(), policySince)
	}
	var gen int
	switch {
	case json.Unmarshal(raw, &gen) != nil:
		return userErr("%v", &projectpolicy.MarkerError{Value: oneLine(string(raw))})
	case projectpolicy.MarkerHandEdited(gen):
		// A negative generation is a hand edit and not an old deployment, and
		// it is the state Read refuses as a MarkerError (projectpolicy's
		// MarkerHandEdited): the sentence below says this installation's rules
		// predate the policy nodes, which a hand-edited -1 does not support —
		// the rules may be perfectly current, and the operator it sends to
		// redeploy them would come back to the same node. The one hand edit's
		// own text, then, from the one predicate (the review finding on this
		// change).
		return userErr("%v", &projectpolicy.MarkerError{Value: oneLine(string(raw))})
	case gen >= projectpolicy.PolicyRulesVersion:
		return nil
	}
	return userErr("%s", rulesMarkerPrePolicyText(gen))
}

// rulesSkewText is the one sentence for "the deployed rules are older than
// the marker": the marker records this generation, and the *document* deployed
// at .settings/rules is not the one this generation generates. `fugaro update`
// redeploys over it — the rules stage of the converge (init_rules_stage.go)
// deploys this binary's generation for the state, and infra.DB.Plan compares the
// same way — and doctor's policy-rules-skew check prints the sentence (plan task
// 1.6 step 4's fifth check), so the state reads the same whether the operator met
// it as a report or in the deploy's own words. It is a plain sentence and not an
// error, because doctor has no error to wrap it in.
func rulesSkewText() string {
	return fmt.Sprintf("the deployed rules are older than the marker %s: re-run 'fugaro update' with fugaro >= %s first; a policy published now is one the runs it bounds cannot read",
		projectpolicy.PathRulesMarker(), policySince)
}

// The marker-below states have two sentences, one each, because they are two
// states: below the *policy* generation, where the nodes a policy lives in do
// not exist and nothing reads or writes one, and below *this binary's*
// generation but at or above the policy generation, where the nodes exist, the
// published policy is read and enforced, and what is missing is the document
// this release deploys — the mailbox (mobile plan P2.1, design m9 §6.4).
// Reading the second with the first's sentence says "no run reads a published
// policy" about rules that carry the nodes, and sends the operator to redeploy
// after a policy that in fact governs; it also stopped being the same predicate
// as Read's gate on the day RulesVersion moved past PolicyRulesVersion, which
// is the review finding this split answers.

// rulesMarkerPrePolicyText is the sentence for a marker below the generation
// that carries the policy nodes: `fugaro project policy publish` refuses with
// it and doctor's policy-rules-skew prints it, so the state reads the same
// whether the operator met it as a refusal or as a report.
//
// It cannot be rulesSkewText, which is the *other* state's sentence: that one
// is about a *document* that disagrees with a marker ("the deployed rules are
// older than the marker"), while here the marker is itself the value that is
// wrong — the deployed rules may be anything at all and doctor never reads
// them for this state, so the refusal told the operator about a comparison
// nothing made (the review of PR #402 found the two texts disagreeing; this is
// one text per state, shared by both readers).
func rulesMarkerPrePolicyText(gen int) string {
	return fmt.Sprintf("the policy marker %s records generation %d, below the generation %d this Fugaro reads a project policy under: this installation's rules predate the policy nodes, so no run reads a published policy and publishing one is refused; re-run 'fugaro update' with fugaro >= %s first, which deploys the rules a policy is read under and records generation %d in the marker",
		projectpolicy.PathRulesMarker(), gen, projectpolicy.PolicyRulesVersion, policySince, projectpolicy.RulesVersion)
}

// rulesDeviceGeneration is the rules generation that added the mailbox's
// message fields, the device allowlist and the pairing nodes (mobile plan
// P6.4, D166).
const rulesDeviceGeneration = 3

// rulesCoordinatorGeneration is the rules generation that added the
// coordinator's read grant (coordinator plan P3, D194).
const rulesCoordinatorGeneration = 4

// rulesVoiceGeneration is the rules generation that added the voice policy node
// (mobile plan task P5.3.1).
const rulesVoiceGeneration = 5

// rulesRegistrationGeneration is the rules generation that added the fields a
// push registration carries on a mailbox message (mobile plan task P5.2.2).
const rulesRegistrationGeneration = 6

// rulesSpendLeavesGeneration is the rules generation that added the four
// day-counter leaves a run's stage report writes per model (spend-accuracy task
// SA5.0, D2648 as amended by D2650).
const rulesSpendLeavesGeneration = 7

// rulesOutcomeGeneration is the rules generation that added the paired device's
// read of a run's outcome (mobile identity-pairing task J1, F30, D2797, O3).
const rulesOutcomeGeneration = 8

// rulesRouteGeneration is the rules generation that added the credential route a
// run reads at bootstrap (workspace-service task WS22, R3 and R4).
const rulesRouteGeneration = 9

// rulesLaunchGeneration is the rules generation that added the phone's stop
// request and the launch envelope a paired device reads (mobile plan task
// P5.1g). It is the generation *below* this binary's, which is why the branch
// it gates in rulesMarkerOlderText is the one that announces generation 11's
// two shapes (mobile identity-pairing task J5r): a marker at 10 already has the
// stop kind and the envelope, and what the deploy adds over it is the tag a
// signing device carries and the typed launch it sends.
const rulesLaunchGeneration = 10

// rulesMarkerOlderText is the sentence for the state between the two
// generations: at or above PolicyRulesVersion and below RulesVersion. Nothing
// refuses here — Read reads the policy the marker's rules carry and
// requirePolicyRules publishes beside it — so this text has one printer,
// doctor's policy-rules-skew, and it is worded as what is *absent* rather than
// what is broken: these rules do carry the policy nodes, and what they predate
// is this release's own document, whose mailbox the mobile app has no grant for
// until the owner deploys it (mobile plan P2.3).
//
// Each branch names the one thing the next generation adds, so the sentence
// never sends the operator after a grant the deployed rules already carry: a
// marker at rulesDeviceGeneration already has the mailbox and the device
// binding, one at rulesCoordinatorGeneration already has the coordinator's read
// grant and only the voice policy node is new to it (mobile plan task P5.3.1),
// one at rulesVoiceGeneration already has that node too and only the
// registration kind's fields are new to it (mobile plan task P5.2.2), and one
// at rulesRegistrationGeneration already has those fields and only the four
// day-counter leaves a run's stage report writes per model are new to it
// (spend-accuracy task SA5.0, D2648 as amended by D2650), and one at
// rulesSpendLeavesGeneration already has those leaves and only the paired
// device's read of a run's outcome is new to it (mobile identity-pairing task
// J1, F30, D2797, O3), one at rulesOutcomeGeneration already has that read and
// only the credential route a run reads is new to it (workspace-service task
// WS22, R3 and R4), one at rulesRouteGeneration already has that route and only
// the phone's stop request and the launch envelope are new to it (mobile plan
// task P5.1g), and one at rulesLaunchGeneration already has those two and only
// the tag a signing device carries and the typed launch it sends are new to it
// (mobile identity-pairing task J5r).
func rulesMarkerOlderText(gen int) string {
	if gen >= rulesLaunchGeneration {
		return fmt.Sprintf("the policy marker %s records generation %d, below the generation %d this Fugaro deploys: these rules carry the policy nodes, the mailbox, the device binding, the coordinator's read grant, the voice policy node, the registration kind's fields, the four day-counter leaves a run's stage report writes per model, the paired device's read of a run's outcome, the credential route a run reads, the phone's stop request and the launch envelope, so runs, a published policy, the mobile app and the coordinator are unaffected — what they predate is this release's own document, which adds the two shapes a phone of this release writes (mobile identity-pairing task J5r, O6 and O10): a message and the heartbeat carry `sig` — the base64url Ed25519 signature a device that holds a key signs with — in place of the `mac` tag, exactly one of the two, and an inbox action may be the typed launch, whose `mode` names the rigor mode to start in and which needs no `confirmation` where every other verb carries one; under these rules a device that signs has every message and every heartbeat refused at the database with no error, and a launch is refused at the verb enumeration, so the app can neither speak nor start anything until the deploy; re-run 'fugaro update' in your own terminal from a Fugaro that deploys generation %d or newer, which writes that document and records its generation in the marker",
			projectpolicy.PathRulesMarker(), gen, projectpolicy.RulesVersion, projectpolicy.RulesVersion)
	}
	if gen >= rulesRouteGeneration {
		return fmt.Sprintf("the policy marker %s records generation %d, below the generation %d this Fugaro deploys: these rules carry the policy nodes, the mailbox, the device binding, the coordinator's read grant, the voice policy node, the registration kind's fields, the four day-counter leaves a run's stage report writes per model, the paired device's read of a run's outcome and the credential route a run reads, so runs, a published policy, the mobile app and the coordinator are unaffected — what they predate is this release's own document, which adds two things the phone needs (mobile plan task P5.1g): the `stop` kind, which is how the app asks a human to stop what they are looking at (the owner's Q10 answer (c): a request the human acts on at the console or the laptop, never a phone-writable kill switch) and which the older document refuses with no error, and the launch envelope at %s/{recipe,devices/$uid} (the installation's named safe recipe and the paired device's own repository allow-list, launch rate and daily cost ceiling, read by that device alone and written by no client) — without them the app can neither ask for a stop nor read the values it refuses an utterance against, so it refuses every launch until the deploy; re-run 'fugaro update' in your own terminal from a Fugaro that deploys generation %d or newer, which writes that document and records its generation in the marker",
			projectpolicy.PathRulesMarker(), gen, projectpolicy.RulesVersion, rules.PathLaunchEnvelope, projectpolicy.RulesVersion)
	}
	if gen >= rulesOutcomeGeneration {
		return fmt.Sprintf("the policy marker %s records generation %d, below the generation %d this Fugaro deploys: these rules carry the policy nodes, the mailbox, the device binding, the coordinator's read grant, the voice policy node, the registration kind's fields, the four day-counter leaves a run's stage report writes per model and the paired device's read of a run's outcome, so runs, a published policy, the mobile app and the coordinator are unaffected — what they predate is this release's own document, which adds the credential route a run reads at bootstrap (config/policy/route/$slug, one node per repository, granted to that repository's own run token and to nothing else, workspace-service WS22, R3 and R4): a run under these rules reads no route and is served by the project's own credential, so a member that pinned a workspace keeps calling the project until the deploy; re-run 'fugaro update' in your own terminal from a Fugaro that deploys generation %d or newer, which writes that document and records its generation in the marker",
			projectpolicy.PathRulesMarker(), gen, projectpolicy.RulesVersion, projectpolicy.RulesVersion)
	}
	if gen >= rulesSpendLeavesGeneration {
		return fmt.Sprintf("the policy marker %s records generation %d, below the generation %d this Fugaro deploys: these rules carry the policy nodes, the mailbox, the device binding, the coordinator's read grant, the voice policy node, the registration kind's fields and the four day-counter leaves a run's stage report writes per model, so runs, a published policy, the mobile app and the coordinator are unaffected — what they predate is this release's own document, which adds the paired device's read of a run's outcome (outcomes/$day/$slug/$run, one node granted to the app's user claim and to nothing else, so the phone can name a run that is still going): a phone under these rules sees a running run's spend and not the run, so the app's jobs list shows the spend alone until the deploy; re-run 'fugaro update' in your own terminal from a Fugaro that deploys generation %d or newer, which writes that document and records its generation in the marker",
			projectpolicy.PathRulesMarker(), gen, projectpolicy.RulesVersion, projectpolicy.RulesVersion)
	}
	if gen >= rulesRegistrationGeneration {
		return fmt.Sprintf("the policy marker %s records generation %d, below the generation %d this Fugaro deploys: these rules carry the policy nodes, the mailbox, the device binding, the coordinator's read grant, the voice policy node and the registration kind's fields, so runs, a published policy, the mobile app and the coordinator are unaffected — what they predate is this release's own document, which adds the four leaves a run's stage report writes per model (%s/{notional,rep,tlo,thi}: the subscription part of the model's dollars, the cost the provider reported for the calls that carried one, and the table's charge over those same calls at the two ends of the tier that served them, spend-accuracy D2648 and D2650): a run under these rules that writes them is refused the whole stage report, because Realtime Database applies a multi-path update atomically, so it writes the leaves it knows and none of these until the deploy; re-run 'fugaro update' in your own terminal from a Fugaro that deploys generation %d or newer, which writes that document and records its generation in the marker",
			projectpolicy.PathRulesMarker(), gen, projectpolicy.RulesVersion, rules.PathByModel, projectpolicy.RulesVersion)
	}
	if gen >= rulesVoiceGeneration {
		return fmt.Sprintf("the policy marker %s records generation %d, below the generation %d this Fugaro deploys: these rules carry the policy nodes, the mailbox, the device binding, the coordinator's read grant and the voice policy node, so runs, a published policy, the mobile app and the coordinator are unaffected — what they predate is this release's own document, which adds the fields a push registration carries (the `token`, `platform` and `prefs` fields of a mailbox message of the new `registration` kind, mobile plan task P5.2.2): the app writes one into its own inbox, and until this is deployed the database refuses that message with no error, so the phone is registered with nothing; re-run 'fugaro update' in your own terminal from a Fugaro that deploys generation %d or newer, which writes that document and records its generation in the marker",
			projectpolicy.PathRulesMarker(), gen, projectpolicy.RulesVersion, projectpolicy.RulesVersion)
	}
	if gen >= rulesCoordinatorGeneration {
		return fmt.Sprintf("the policy marker %s records generation %d, below the generation %d this Fugaro deploys: these rules carry the policy nodes, the mailbox, the device binding and the coordinator's read grant, so runs, a published policy, the mobile app and the coordinator are unaffected — what they predate is this release's own document, which adds the voice policy node %s/{shared,repos/$slug} (mobile plan task P5.3.1): a run token reads its own project's node there, and no client writes either node; re-run 'fugaro update' in your own terminal from a Fugaro that deploys generation %d or newer, which writes that document and records its generation in the marker",
			projectpolicy.PathRulesMarker(), gen, projectpolicy.RulesVersion, rules.PathVoice, projectpolicy.RulesVersion)
	}
	if gen >= rulesDeviceGeneration {
		return fmt.Sprintf("the policy marker %s records generation %d, below the generation %d this Fugaro deploys: these rules carry the policy nodes, the mailbox and the device binding, so runs, a published policy and the mobile app are unaffected — what they predate is this release's own document, which adds the read grant on the coordinator's state (coordinator plan P3): a signed-in reader is refused every read of %s/ until it is deployed; re-run 'fugaro update' in your own terminal from a Fugaro that deploys generation %d or newer, which writes that document and records its generation in the marker",
			projectpolicy.PathRulesMarker(), gen, projectpolicy.RulesVersion, coordpath.PathRoot, projectpolicy.RulesVersion)
	}
	return fmt.Sprintf("the policy marker %s records generation %d, below the generation %d this Fugaro deploys: these rules do carry the policy nodes of generation %d, so a published policy is read and enforced and publishing another is not refused — what they predate is this release's own document, which adds the mailbox subtree and the user-claim read grants the mobile app reads (mobile plan P2.1, design m9 §6.4), and binds that user claim to a device node (mobile plan P6.4, D166): a phone account with no %s/<uid> node reads and writes nothing at all, so before you deploy, every account that is meant to reach the app needs one — nothing mints a device node yet ('fugaro pair' is plan task P6.6 and is not built), so today that means adding it by hand; re-run 'fugaro update' in your own terminal from a Fugaro that deploys generation %d or newer, which writes that document and records its generation in the marker",
		projectpolicy.PathRulesMarker(), gen, projectpolicy.RulesVersion, projectpolicy.PolicyRulesVersion, rules.PathDevices, projectpolicy.RulesVersion)
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

// bitbucketTokenIdentities is the half of the node's bitbucket_tokens key the
// local config does not state (R12, R5): the account UUID the add-time check
// proved for each typed token, keyed by the token's DOCUMENT KEY — the key
// `creds set` writes the token's status under (T6), so the store's own map is
// handed to the derivation without re-keying it.
//
// It is read only when the local config names a typed token, and that is R3's
// half of the rule rather than an optimization: an installation holding the
// legacy bitbucket-token alone publishes no such key at all, so its update must
// reach no state store and converge with no step — the read is a cloud read of
// the project's own database, and a project with no record has nothing for it
// to answer.
//
// A name the store does not hold, and one it holds with an empty identity,
// contribute nothing: an absent identity is what a repository token has and all
// it has (R14), never a value to be guessed. A read that fails is an error
// rather than an empty set, because an empty set is a legal answer here and a
// failed read that wore it would publish a node claiming the project's tokens
// have no account.
func bitbucketTokenIdentities(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) (map[string]string, error) {
	if len(lc.BitbucketTokens) == 0 {
		return nil, nil
	}
	statuses, err := (&creds.RTDBStore{DB: db}).TokenStatuses(ctx)
	if err != nil {
		return nil, dbErr(lc, err, false)
	}
	out := make(map[string]string, len(statuses))
	for name, st := range statuses {
		if st.Identity != "" {
			out[name] = st.Identity
		}
	}
	return out, nil
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
		return userErr("%s", projectpolicy.SkewSentence(&projectpolicy.VersionError{Found: head.Version, Understood: projectpolicy.Version}, projectpolicy.ReaderCLI))
	}
	return nil
}

// publishedPolicyOf is the node as a policy, or nil when none is published.
// A node this Fugaro cannot read is refused rather than replaced: publishing
// over one would drop whatever it is that this binary does not understand.
//
// The strict read is the one that answers "cannot read" here, for the reason
// the sentence above gives: what this release does not model inside the routing
// table is a member a publish would drop, since the block is written from the
// typed members and not patched — so a node a later release wrote is one this
// binary refuses to publish over, and the move is the upgrade.
func publishedPolicyOf(raw []byte, exists bool) (*projectpolicy.Policy, error) {
	if !exists {
		return nil, nil
	}
	p, found, err := projectpolicy.DecodeStrict(raw)
	var ve *projectpolicy.VersionError
	switch {
	case errors.As(err, &ve):
		if ve.Found > ve.Understood {
			return nil, userErr("%s", projectpolicy.SkewSentence(ve, projectpolicy.ReaderCLI))
		}
		// An older version (a hand-written node, or one a release before this
		// shape wrote). "Publish again" would be advice this very refusal
		// makes impossible — the publish is what is being refused — so the
		// remedy is the one that works for the version at hand, and the
		// migration chain decides which that is (Migratable): a version a step
		// reaches is rewritten in place by `fugaro update`, which keeps the
		// members the node holds, and only a version below the chain's first
		// step has to be removed before a publish — which then rebuilds it
		// from this machine's config rather than from the node. Doctor's
		// policy-older-than-binary names the same move for the same state.
		if projectpolicy.Migratable(ve.Found) {
			return nil, userErr("the published policy is version %d and this Fugaro writes version %d: publishing over it would rewrite it in this Fugaro's shape, and this command will not do that unasked; run 'fugaro update', whose policy stage rewrites the node in place at version %d, keeping the members it carries", ve.Found, ve.Understood, ve.Understood)
		}
		return nil, userErr("the published policy is version %d and this Fugaro writes version %d: publishing over it would rewrite it in this Fugaro's shape, and this command will not do that unasked; remove it with 'fugaro project policy publish --delete' and publish again, or correct its version by hand", ve.Found, ve.Understood)
	case err != nil:
		return nil, userErr("the published policy at %s cannot be read, so it cannot be replaced: %v; fix it by hand, or remove it with 'fugaro project policy publish --delete'", projectpolicy.PathShared(), err)
	case !found:
		return nil, nil
	case len(p.UnknownKeys) > 0:
		// The count is named and the values never are: what a key nobody
		// models holds is the node's, and the publisher prints it in no
		// message (the keys themselves are printed escaped, which only
		// says which names were not understood). The remedy is the one
		// that works, and for the owner's rule of 2026-10-30 it is two
		// commands: a newer Fugaro knows the keys, and `fugaro update` is
		// what carries the node over without a deletion.
		return nil, userErr("%s lists %d key(s) this Fugaro does not know (%s): publishing would drop them; upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then run 'fugaro update' to migrate the node in place, or fix the node by hand",
			projectpolicy.PathShared(), len(p.UnknownKeys), unknownKeysText(p.UnknownKeys))
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

// seedDeclaredDevices writes the DECLARED allowlist (lc.MobileAllow, D231) into
// the device node every phone's token is admitted by, and it is
// publishAllowRepos' rule applied to people: it seeds a project whose node is
// not there and writes nothing else, ever.
//
// The node's absence is the whole condition, and it has to be, because one
// revocation is one deleted child (D229: `fugaro pair revoke`) and a deletion
// leaves nothing behind. An entry a node does not hold cannot be told apart
// from an entry an operator removed, so seeding per entry would re-add every
// revoked person at the next publish, from a declaration nobody edited — the
// exact silent re-grant publishAllowRepos' own rule exists to prevent. What
// follows from it is the honest half: a person declared after the first seed is
// admitted by pairing, not by a publish, which is what the line this returns
// when the node is there says.
//
// The write is ETag-guarded (`db.PutIfMatch` on the ETag the read that found
// the node absent returned), so a node another writer created in between is
// never overwritten: the race is lost loudly, and the node's writer wins.
//
// It returns the line the publish prints, and "" when the file declares
// nothing (a project with no declared allowlist writes no node at all, and says
// nothing about one).
func seedDeclaredDevices(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, at string) (string, error) {
	if len(lc.MobileAllow) == 0 {
		return "", nil
	}
	var raw json.RawMessage
	etag, found, err := db.GetETag(ctx, rules.PathDevices, &raw)
	if err != nil {
		return "", dbErr(lc, err, false)
	}
	if found {
		return fmt.Sprintf("kept the device node at %s: the declared allowlist seeds a project whose node is not there, and it never re-adds a device an operator removed — `fugaro pair` admits a person, `fugaro pair revoke` removes one", rules.PathDevices), nil
	}
	nodes := map[string]pairDeviceNode{}
	for _, e := range lc.MobileAllow {
		if e.UID == "" {
			continue
		}
		nodes[e.UID] = pairDeviceNode{Email: e.Address, Role: string(e.Role), At: at}
	}
	if len(nodes) == 0 {
		return fmt.Sprintf("the declared allowlist at mobile_allow: names no uid, so %s was not written: a device node is keyed by the uid a pairing minted", rules.PathDevices), nil
	}
	body, err := json.Marshal(nodes)
	if err != nil {
		return "", fmt.Errorf("building the device node at %s: %w", rules.PathDevices, err)
	}
	if err := db.PutIfMatch(ctx, rules.PathDevices, etag, json.RawMessage(body)); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return "", userErr("%s was written by someone else while this publish ran, so the declared allowlist seeded nothing: the node stands as its writer left it, and a publish seeds a project whose node is not there (a device is added and removed by `fugaro pair`)", rules.PathDevices)
		}
		return "", dbErr(lc, err, true)
	}
	return fmt.Sprintf("seeded %d device(s) at %s from the declared allowlist", len(nodes), rules.PathDevices), nil
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
// list to be written. It holds for both sources of the seed — the operator's
// own repos: map and the shared config's list, which ParseShared accepts with
// a provider-less entry too. The readers (`allowSources`, for `project show`
// and `project repos ls`) skip such an entry instead: they only label where an
// allow-list entry came from, a `repos:` entry need not name a provider at all
// (localcfg: the checkout's fugaro.yaml decides it), and a read-only command
// must not abort on a config the tool accepts — the same reason repoBySlug
// skips these.
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
			// The same posture as the local loop above, for the same reason:
			// the seed is what the writer writes, and an entry it cannot spell
			// as a canonical name is a repository that drops out of the
			// allow-list — here one this machine's own installation publishes,
			// so it is not even in the local config the operator is editing.
			// localcfg's validation leaves a provider-less repos entry alone
			// (the checkout's fugaro.yaml decides it), so this is reachable
			// through a document ParseShared accepts.
			if strict {
				return nil, userErr("the shared config's repos.%s names no git provider: the allow-list is canonical (provider/owner/name), so add provider: github or provider: bitbucket there (the document published to the project's runs bucket)", name)
			}
			continue // a reader only labels where an entry came from
		}
		c, err := projectpolicy.Canonical(r.Provider, name)
		if err != nil {
			if strict {
				return nil, userErr("the shared config's repos.%s: %v", name, err)
			}
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

// refuseAProfileMountingTheBitbucketCredential is the operator's door on the
// shared-job rule, and it does not copy the rule's sentence: the sentence is
// projectpolicy.CheckSharedJobProfile's, the function the deployer asks too, so
// the two doors refuse the entry in one wording rather than in two that can
// drift.
//
// The rule is asked here, at the command a person writes a policy with, and not
// inside buildProjectPolicy, which `fugaro update`'s policy stage builds its
// node through as well (policyNodeForPublish): an installation whose local
// config already mounts the credential is one this repository published without
// complaint before the owner's ruling of 2026-11-14, and the HARD RULE is that
// `fugaro update` converges it — a refusal at the shared build would stop the
// update whole and leave the operator editing the file by hand, which is the one
// step an update must never need. What the stage writes is the config's own
// node, and internal/infra's ProjectJobs is where a job for such a profile is
// refused.
func refuseAProfileMountingTheBitbucketCredential(lc *localcfg.Config) error {
	for _, name := range slices.Sorted(maps.Keys(lc.Profiles)) {
		if err := projectpolicy.CheckSharedJobProfile("profiles."+name, projectpolicy.Profile{JobProfile: lc.Profiles[name]}); err != nil {
			return userErr("%s would not publish as a policy: %v", lc.Name, err)
		}
	}
	return nil
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
//
// The clone_urls block is the local config's (SD5, D842): `fugaro project
// clone-url` writes it and this publish carries it, so the node no longer holds
// a value the file does not. A config that states no block keeps the node's
// entries (localcfg.Config.CloneURLsEffective), which is what leaves a machine
// whose config predates the key, and an owner who declines the import, with the
// node exactly as it was (§6.4) — the block is where a repository's runs clone
// from (the runner computes a project job's clone URL from it, plan task 3.3
// step 3, design §4.1(1)), and a publish that dropped an entry would silently
// move them to the provider's usual URL. printPolicyDiff says which side each
// entry came from.
func buildProjectPolicy(lc *localcfg.Config, allow []string, old *projectpolicy.Policy, removeRouting bool, identities map[string]string) (*projectpolicy.Policy, bool, error) {
	if lc.Budget == nil || lc.Budget.Mode == "" {
		return nil, false, userErr("this project's config sets no budget.mode, and a published policy's budget block must name one (observe or enforce): the mode decides whether the caps bite, and a block that leaves it out would take it from each job's environment, which nobody reading the policy can see; set budget: { mode: ... } in %s, or leave the policy unpublished", lc.Name)
	}
	// A policy that carries profiles must define the one every workflow that
	// names none runs on (projectpolicy.validProfiles applies the same rule to
	// the node, and checkPolicyBytes would refuse it there). It is
	// refused here, before the node is built, so that the operator is told
	// which file to fix: the alternative is a repository selecting a profile
	// nobody defined, which is refused at run start, one run at a time.
	if len(lc.Profiles) > 0 {
		if _, ok := lc.Profiles[projectpolicy.DefaultProfileName]; !ok {
			return nil, false, userErr("this project's config sets profiles: and does not define %q, the profile every workflow that names none runs on: a repository selecting it would have every run refused; define it in %s, or remove the profiles: block (a policy with no profiles block sets no profile at all)",
				projectpolicy.DefaultProfileName, lc.Name)
		}
		// R8, D2310: one default per provider the block serves, not `standard`
		// alone. A workflow that names no job profile resolves to the default
		// of its repository's provider (the runner's ProjectJobDefaultProfile:
		// `standard` when that profile serves the provider and
		// `standard-<provider>` when the policy defines one), so a block that
		// serves a provider without defining its default is a block whose
		// repositories on that provider resolve to no profile at all: they
		// keep the per-repository jobs the block was published to replace, and
		// nothing says why. It is refused here, where the file it came from can
		// be named, and the fix is the one command: `fugaro update` writes the
		// default profile of every provider a project uses.
		served := servedProviders(lc)
		first := firstDefaultProvider(lc, served)
		for _, provider := range served {
			name := projectpolicy.DefaultProfileNameFor(provider, provider == first)
			if _, ok := lc.Profiles[name]; ok {
				continue
			}
			return nil, false, userErr("this project's config sets profiles: and serves %s with them but defines no %q, the default profile of a %s repository whose workflow names none: that workflow would resolve to no profile and keep its per-repository job instead of the shared job this block is published for; run %s (it writes the default profile of every provider a project uses), or define %s in %s",
				provider, name, provider, selfCommand()+" update", name, lc.Name)
		}
		for _, name := range slices.Sorted(maps.Keys(lc.Profiles)) {
			// A profile's allow_repos is either written or not, and the node
			// could not tell the two apart: the carrier's json omitempty drops
			// an empty list on the way into the policy, so projectpolicy's own
			// refusal for a written empty list (checkWrittenProfileFields)
			// never sees this one — the profile would silently fall back to
			// the project's wider allow_repos, which is the one direction a
			// profile's list must never move (docs/project-layer.md §5).
			// It is refused here, where the file still says which it was: a
			// written empty list (`allow_repos: []`) and a written blank one
			// (`allow_repos:` with nothing under it) both reach this function
			// as an empty slice, and config.JobProfile's allowReposWritten is
			// what tells them from the absent key. localcfg refuses both when
			// the file is read (validate), so through the CLI this check is
			// unreachable: it is defence in depth, the second door on the same
			// rule for a config that reaches the publisher by another path.
			// TestBuildProjectPolicyRefusesAnEmptyProfileAllowRepos tests it
			// with a config built in code.
			if pr := lc.Profiles[name]; pr.AllowReposWritten() && len(pr.AllowRepos) == 0 {
				return nil, false, userErr("this project's config sets profiles.%s.allow_repos to a written list with nothing in it, which is ambiguous: remove the key to fall back to the project's allow_repos, or write the repositories this profile admits (the project's own allow_repos is still required, so a profile's list can only narrow it)", name)
			}
			// A profile that mounts the Bitbucket credential is not this
			// function's to refuse: the update's policy stage builds its node
			// through it, and the stage must converge an installation whose
			// local config already mounts the credential — one this repository
			// published without complaint before the owner's ruling of
			// 2026-11-14 — where a refusal would stop `fugaro update` whole and
			// leave the operator editing the file by hand (CLAUDE.md's hard
			// rule). The operator's own door asks the rule before it gets here
			// (runProjectPolicyPublish, projectpolicy.CheckSharedJobProfile),
			// and the deployer asks the same function of the node this build
			// writes, which is what refuses the job (internal/infra's
			// ProjectJobs): the entry is refused in one sentence at the two
			// doors that own the decision, and neither is the stage.
		}
	}
	silent := lc.Routing == nil || lc.Routing.Claude == nil
	switch {
	// The cascade first, and with its own sentence: it is a switch a publish
	// must not leave on or turn off on its own either, and it is not a route,
	// so the route refusal's reason is false of it. See nodeTurnsTheCascadeOn.
	case silent && !removeRouting && nodeTurnsTheCascadeOn(old):
		why := "your config sets no routing"
		if lc.Routing != nil {
			why = "your config's routing block names no Claude switch"
		}
		return nil, false, userErr("the policy published at %s turns the credentials cascade on (routing: %s), and %s: publishing from here would turn it off, and the cascade is the switch that lets a run's Claude calls fall through to the project's other credentials — with credentials.fallback: auto it also lets them spend the paid tiers. Write routing: {claude: {via: cascade}} in %s to keep it, write routing: {claude: {via: direct}} to turn it off on purpose, or pass --remove-routing to drop the block itself on purpose",
			projectpolicy.PathShared(), routingText(old.Routing), why, lc.Name)
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
	// The accounts block is DERIVED, never written by hand (D3): it is the
	// operator's claude_accounts.<name>.allow_data_to, qualified with the
	// provider of the profile(s) that name the account, for the accounts some
	// profile names. The refusal below is the operator's file being wrong
	// about an account — one nothing defines, or one whose allow_data_to is
	// empty — and it is made here, before the node is built, so the sentence
	// names the file rather than the node. The profiles are then judged
	// against it by the node's own rules (checkPolicyBytes decodes the
	// document this function returns), which is where the subset rule fires:
	// a profile admitting a repository one of its accounts does not cover is
	// refused before anything is written.
	profiles := policyProfiles(lc)
	accounts, err := projectpolicy.DeriveAccounts(profiles, lc.ClaudeAccounts, lc.ProjectSecrets, lc.PolicyRepoAccounts())
	if err != nil {
		if gaps := repoAccountGaps(lc); len(gaps) > 0 {
			return nil, false, userErr("%v. The one command that fixes it: %s", err, gaps[0].fix())
		}
		return nil, false, userErr("%v", err)
	}
	// The Bitbucket block is derived the same way and for the same reason: it
	// is the operator's project_secrets.bitbucket-token.allow_data_to, and no
	// command writes it by hand (D473, D493). It is derived whether or not a
	// profile mounts the token — a proxied run's job holds no credential at
	// all and this block is its whole admission — so the derivation reads the
	// profiles for nothing, and the two states that mean "this token reaches no
	// repository" publish no block at all (DeriveBitbucketToken). The profiles
	// are then judged against it by the node's own rule, in the same
	// checkPolicyBytes call that judges them against the accounts block.
	//
	// bitbucket_api_roots is the operator's own allow-list (D474) and the node
	// is where the run reads it. It is copied rather than derived, and it is
	// held to the node's rule (projectpolicy.ValidBitbucketAPIRoots, applied by
	// that same decode) rather than to a copy of it here: one rule, at the one
	// point a root can be written.
	token, err := projectpolicy.DeriveBitbucketToken(profiles, lc.ProjectSecrets)
	if err != nil {
		return nil, false, userErr("%v", err)
	}
	// The typed tokens are derived beside that block and from the other half of
	// the same file (R5): the legacy untyped record's coverage IS the block
	// above, while each record of the config's bitbucket_tokens block is a
	// token of its own, and the key is the one place the credentials service
	// learns what each of them reaches. identities is the half the file does
	// not state (R12): the account UUID `creds set` proved for a token is in
	// the creds state store, and the caller reads it there, so a hand-edited
	// file cannot add one.
	//
	// A project that names no record derives no key at all, which is what
	// leaves the node of an installation holding the legacy bitbucket-token
	// alone byte for byte what it was (R3).
	tokens, err := projectpolicy.DeriveBitbucketTokens(lc.BitbucketTokens, identities)
	if err != nil {
		return nil, false, userErr("%v", err)
	}
	// The review-level floor is copied rather than derived, and it is held to
	// the same rule the loader applies (localcfg's ReviewFloorPolicy, which is
	// policy.ParseFloor and also what validate calls), so a floor the
	// operator's file spells wrong cannot reach the node half-read: the floor
	// is a minimum, and a level lost on the way to the node is a run reviewing
	// less than the owner asked for. A project that sets none publishes no
	// member at all, so its node is byte for byte what it was before the key
	// existed.
	floor, err := lc.ReviewFloorPolicy()
	if err != nil {
		return nil, false, userErr("%v", err)
	}
	// A config that says nothing about the floor does not take the node's
	// away: the floor is the one member of this node that binds the operator's
	// own future runs, and the publish that would drop it is the one he did
	// not mean — a second machine whose config predates the key, or a key
	// deleted while tidying a file. He says the floor goes on purpose by
	// writing the key with no mode under it, and the two spellings reach here
	// differently: the key absent is a nil map (keep what the node has), the
	// key written empty a non-nil one (`review_floor: {}` is no floor, so the
	// member goes). A config that writes a floor replaces the member with what
	// it writes, the way routing does — that edit is the operator's own, and
	// the diff prints it either way.
	keepReviewFloor := lc.ReviewFloor == nil && old != nil && len(old.ReviewFloor) > 0
	if keepReviewFloor {
		floor = old.ReviewFloor
	}
	// The verifier list is kept the same way, by the one rule localcfg states
	// for both its writers (C3.9g3, D1075): the file's list when it writes one,
	// the node's otherwise. The cost of the other reading is higher here than
	// for the floor — the list is what the credentials service admits to its
	// verify route, and the publish that dropped it would leave a service that
	// refuses every verifier the owner named, from a machine whose config
	// merely predates the key.
	var publishedVerifiers []string
	if old != nil {
		publishedVerifiers = old.VerifierAccounts
	}
	verifierAccounts := lc.VerifierAccountsEffective(publishedVerifiers)
	// The image catalog is kept by the same rule (D1140, D1156), and the cost
	// of the other reading is the floor's and the verifier list's: a machine
	// whose config predates the key — a teammate's, a CI box's, an operator's
	// second laptop — would publish a node without the catalog and leave every
	// repository that names an entry with nothing to run. A config that writes
	// a catalog replaces what the node holds, and one that writes an empty
	// catalog takes it away, which is how a published catalog is removed on
	// purpose; a node with no catalog carries no member at all, so a project
	// that names none is byte for byte what it was before the key existed.
	var publishedCatalog *config.Catalog
	if old != nil {
		publishedCatalog = old.ImageCatalog
	}
	imageCatalog := lc.ImageCatalogEffective(publishedCatalog)
	// The services each entry's image provides are derived from that catalog
	// (D1904) and not kept member by member, because there is no second place
	// they are written: an entry carries them in the catalog the operator edits,
	// and the node carries them as a key of its own (F20's answer, in
	// projectpolicy). A config that writes a catalog therefore states the whole
	// block — an entry whose services were removed has them removed on the node,
	// which is an edit the drift comparison sees. A config that writes no
	// catalog keeps the node's block, which is the catalog's own keep rule and
	// its reason: a machine whose config predates the key cannot restate the
	// services, and deriving from the node's catalog would drop every one of
	// them — the node's entries cannot carry the member at all.
	var imageServices projectpolicy.ImageServices
	switch {
	case lc.ImageCatalog != nil:
		imageServices = projectpolicy.ImageServicesFromCatalog(imageCatalog)
	case old != nil:
		imageServices = old.ImageServices
	}
	// The repo-to-image map and the bake switch are kept by that same rule
	// (D1906, D1901), and the cost of the other reading is the catalog's: a
	// machine whose config predates the keys — a teammate's, a CI box's — would
	// publish a node that maps nothing and bakes nothing, so every repository
	// the owner mapped would fall back to the catalog's default and every job
	// of the project would clone again. A config that writes the map replaces
	// what the node holds, and one that writes an empty map takes it away, which
	// is how a published one is removed on purpose. The bake switch is a
	// pointer on both sides because absent and true are one statement: a config
	// that writes neither keeps the node's, and a node and a config that both
	// say nothing publish no member at all, so such a node is byte for byte
	// what it was before the key existed.
	var publishedImageMap map[string]string
	var publishedBake *bool
	if old != nil {
		publishedImageMap = old.ImageMap
		publishedBake = old.BakeRepos
	}
	imageMap := lc.ImageMapEffective(publishedImageMap)
	bakeRepos := lc.BakeReposEffective(publishedBake)
	// The triage switch is kept by that same rule (D1854), and the cost of the
	// other reading is the floor's, one direction over: the stage is what judges
	// the findings a review round's table would have blocked, so a publish that
	// dropped the member would turn the judge back on for every run of the
	// project — from a machine whose config merely predates the key. A config
	// that writes the switch replaces what the node holds, and one that writes
	// neither keeps the node's; the switch is a pointer on both sides because
	// absent and true are one statement, so a node and a config that both say
	// nothing publish no member at all.
	var publishedTriage *bool
	if old != nil {
		publishedTriage = old.Triage
	}
	triage := lc.TriageEffective(publishedTriage)
	// The review-against-task switch is kept by the triage switch's rule one
	// key over (R7 of docs/review-against-task/), with the same cost on the
	// wrong reading: the switch is what turns off the check every review stage
	// makes of a run's own work, so a publish from a config that says nothing
	// about it — every machine but the owner's — must not be the write that
	// takes the check off a node that carried it. A config that states the
	// switch replaces what the node holds — the direction D2935 gives the
	// repository's own key is the merge's between layers, not this one, because
	// the file read here is the owner's own — and a config that states neither
	// keeps the node's, so a node and a config that both say nothing publish no
	// member at all.
	var publishedReviewAgainstTask *bool
	if old != nil {
		publishedReviewAgainstTask = old.ReviewAgainstTask
	}
	reviewAgainstTask := lc.ReviewAgainstTaskEffective(publishedReviewAgainstTask)
	// The member's switch for the personal half of a workspace's credentials is
	// kept by the triage switch's rule one key over (R9 of
	// docs/workspace-credentials/, WC6, D2972), with the switch's own cost on
	// the wrong reading: the node is what the workspace's service reads — the
	// only enforcer, and never anything on the member's disk — so a publish from
	// a machine whose config says nothing about it (a teammate's, whose config
	// predates the key) must not be the write that turns the personal half off
	// for the member that turned it on. A config that states the switch replaces
	// what the node holds, and a config that states neither keeps the node's; the
	// switch is a pointer on both sides because absent is off, so a node and a
	// config that both say nothing publish no member at all.
	var publishedPersonalTokens *bool
	if old != nil {
		publishedPersonalTokens = old.PersonalWorkspaceTokens
	}
	personalTokens := lc.PersonalWorkspaceTokensEffective(publishedPersonalTokens)
	// The pools' subscription switch is kept by the triage switch's rule (F22,
	// D1636), and the cost of the other reading is that switch's: the member is
	// what makes a pool of two or more subscriptions serve anything at all
	// (D1622), so a publish that dropped it — from a machine whose config says
	// nothing, which is every machine but the owner's — would take the owner's
	// pools away from every run of the project, a run in flight while `fugaro
	// update` republishes this node included. A written false is his own "no"
	// and replaces what the node holds; a config and a node that both say
	// nothing publish no member at all.
	var publishedSpread *bool
	if old != nil {
		publishedSpread = old.SubscriptionSpread
	}
	spread := lc.SubscriptionSpreadEffective(publishedSpread)
	// The orchestrator block is kept by the same rule (D1239, D1234), with the
	// catalog's two spellings collapsed into one: a config that states a block
	// replaces what the node holds — the daemon's guardrails are the owner's own
	// figures, and a second machine whose config predates the key must not
	// publish a node without them — and a config that states none, or states a
	// block that sets no key, keeps the node's (OrchestratorEffective). There is
	// no spelling that empties a published block: a guardrail is a ceiling, so
	// the only direction it moves is down, and a member dropped here would be
	// every cap back at Fugaro's defaults on a machine nobody meant to change
	// anything on.
	var publishedOrchestrator *policy.Orchestrator
	if old != nil {
		publishedOrchestrator = old.Orchestrator
	}
	orchestrator := lc.OrchestratorEffective(publishedOrchestrator)
	// The account-choice members are copied rather than derived — there is
	// nothing to derive them from, they are the owner's own choice — and they
	// are held to the node's own rules by the decode checkPolicyBytes runs on
	// the document this function returns: the stage segment's vocabulary and the
	// account name's shape. What a stage may *serve* is not judged here and
	// cannot be: the node carries no agent.auth, so config.CheckStageAccountMix
	// fires at the launch and in `fugaro validate`, where the run's auth is
	// known. An empty entry names nothing and is dropped, and a config that
	// names no stage account publishes no member at all, so the node of such a
	// project is byte for byte what it was before the key existed.
	//
	// A key may hold an alias rather than an account's own name (D2091), and
	// what is copied is the account it resolves to: the node is read by every
	// job image — and by the launcher of every teammate — none of which knows
	// this machine's aliases, so an alias written there would name no account of
	// it at all (F4, D2090). The resolution is one step and never a walk
	// (localcfg.Config.ResolveAccount, which is config.ResolveAccountAlias), and
	// a name that is no alias is copied unchanged, so a project that writes none
	// publishes what it always did.
	//
	// A stage the release behind cannot read is held back
	// (config.AccountStageHeldBack): the node's reader refuses the whole node
	// over one name outside its own run, and the reader is a job image that
	// `fugaro update` rebuilds behind its own confirmation — so publishing a
	// stage this release added would break every run of the project on an image
	// that is not rebuilt yet, an in-flight run included, which the update must
	// never do (docs/design/update-in-flight.md). The operator's choice stays in
	// the local config, and it reaches the node from the release after the one
	// that added the stage: adding the name to
	// config.AccountStagesAReleasedImageKnows is what lifts the hold-back, and
	// that list moves when a release is cut and the work moves on, the way the
	// release-skew constants beside it do.
	var stageAccounts map[string]string
	for stage, choice := range lc.ClaudeAccountStages {
		if choice.ClaudeAccount == "" || config.AccountStageHeldBack(stage) {
			continue
		}
		if stageAccounts == nil {
			stageAccounts = map[string]string{}
		}
		stageAccounts[stage], _ = lc.ResolveAccount(choice.ClaudeAccount)
	}
	defaultAccount, _ := lc.ResolveAccount(lc.DefaultClaudeAccount)
	// The budget mode is the one the file asks for (publishedMode, AB8a): a file
	// that spells the word the mode's vocabulary dropped is asking for the mode
	// it means, and copying the word into the node would undo the migration step
	// that has just taken it out of the published node — the update's own publish
	// path reaches this builder with no refusal of its own, and the node it wrote
	// would be one no reader of this release serves. The publish door's own
	// refusal of the word is above it, and the file itself loses it in AB8b.
	pol := &projectpolicy.Policy{
		DefaultClaudeAccount:    defaultAccount,
		ClaudeAccountStages:     stageAccounts,
		Version:                 projectpolicy.Version,
		Budget:                  projectpolicy.Budget{Mode: publishedMode(lc.Budget.Mode), PerRunUSD: lc.Budget.PerRunUSD, AllowedModels: lc.Budget.AllowedModels, MaxRunTokens: lc.Budget.MaxRunTokens},
		AllowRepos:              nonNil(allow),
		BaseBranch:              lc.BaseBranch,
		Providers:               policyProviders(lc),
		Prices:                  policyPrices(lc),
		Profiles:                profiles,
		Accounts:                accounts,
		BitbucketToken:          token,
		BitbucketTokens:         tokens,
		BitbucketAPIRoots:       lc.BitbucketAPIRoots,
		ReviewFloor:             floor,
		VerifierAccounts:        verifierAccounts,
		ImageCatalog:            imageCatalog,
		ImageServices:           imageServices,
		ImageMap:                imageMap,
		BakeRepos:               bakeRepos,
		Triage:                  triage,
		ReviewAgainstTask:       reviewAgainstTask,
		PersonalWorkspaceTokens: personalTokens,
		Orchestrator:            orchestrator,
		SubscriptionSpread:      spread,
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
	// clone_urls moved out of the node and into the local config (SD5, D842):
	// `fugaro project clone-url` is the block's writer, and this file is its
	// source, so the node is published from what the file states. A file that
	// states nothing still keeps the node's entries (CloneURLsEffective), which
	// is what leaves a machine whose config predates the key, and an owner who
	// declines the import, with the node exactly as it was (§6.4) — the block
	// is where a repository's runs clone from, and a publish that dropped an
	// entry would move them back to the provider's usual URL silently. The
	// member is carried over byte for byte only while the file states nothing
	// (policyNodeBytes, keepCloneURLs): once it states the block, the file is
	// what the operator's own edit wrote.
	var oldCloneURLs map[string]string
	if old != nil {
		oldCloneURLs = old.CloneURLs
	}
	pol.CloneURLs = lc.CloneURLsEffective(oldCloneURLs)
	// The binaries block is the owner's grant of the paths a run may add a
	// binary at and of the cap one is held to, and the local config is its
	// carrier from this release on (AR1): `fugaro.yaml` still has no such key
	// (D986 — the checkout's own agent writes that file, and a key there would
	// let the coder admit the payload the rule refuses), and the shared config
	// refuses it for the launcher's reason, so this file is where the operator
	// states it. A file that says nothing keeps the node's block
	// (BinariesEffective), and a written empty block clears it (D987) — the two
	// spellings a publisher has, and the difference between them is what a
	// publish must not decide for itself.
	//
	// The member is carried over byte for byte only while the file says nothing
	// (policyNodeBytes, keepBinaries), the clone_urls rule: the block's own
	// spellings are the reason it is carried rather than re-encoded, and once
	// the file states it, the file is what the operator's own edit wrote. The
	// typed copy is what the diff reads (printPolicyDiff's kept line) and what
	// the node is built from.
	var publishedBinaries *projectpolicy.Binaries
	var publishedCascade *projectpolicy.Credentials
	var publishedGrades *config.ModelGrades
	var publishedPauseNode *projectpolicy.Pause
	if old != nil {
		publishedBinaries = old.Binaries
		publishedCascade = old.Credentials
		publishedGrades = old.ModelGrades
		publishedPauseNode = old.Pause
	}
	pol.Binaries = lc.BinariesEffective(publishedBinaries)
	// The credentials block — the cascade's fallback, its threshold, its
	// backoff and D142's two controls (D152, D153) — is the operator's own
	// statement, and from this release the local config carries it
	// (CredentialCascade, D2607, F5, F6), so a publish MERGES the two: the
	// members the file states are this machine's, the members it leaves out are
	// the node's, and a file that says nothing about the block keeps it whole.
	// What the merge must never do is drop a member nobody wrote about: the
	// block is what decides whether a run may spend the project's money at all,
	// so a publish that wrote a node without its fallback would put a project
	// back on oauth-only for `fallback: auto` with an enforce cap, silently,
	// from a machine whose config merely predates the key.
	//
	// The merge is written here and not inside the `old != nil` guard below
	// because the block's source is now the file as much as the node: a first
	// publish writes what the file states (publishedCredentials).
	pol.Credentials = publishedCredentials(lc, publishedCascade)
	// The merged block is held to the block's own rules, because the merge can
	// hand back one neither side held while each side was validated alone: the
	// file at load (localcfg.validate) and the node at decode
	// (publishedPolicyOf), and the backoff pair's rule is about the EFFECTIVE
	// pair, an absent member being its default (ValidCredentials, D153). A file
	// that writes only `backoff.cap: 1h` is valid beside a node holding
	// `backoff.initial: 2h`, and the merge of the two is a cap below the initial
	// — a schedule that never doubles. Left to the node's decode, the operator
	// would meet it on the next `fugaro update`, in a sentence that names
	// neither the key they wrote nor the member that came from the node; asked
	// here, both halves are still in hand and the refusal names the file's key.
	// A config that states no block is not asked: what it hands back is the
	// node's own block, which the decode already read.
	if credentialsStated(lc.CredentialCascade) {
		if err := projectpolicy.ValidCredentials(pol.Credentials, "credential_cascade"); err != nil {
			return nil, false, userErr("the credential_cascade block this project's local config states, merged with the members the published node holds, is not one the cascade can use: %v. A member the file leaves out is the node's (D2607), and the rules are asked of the block as the cascade would use it — an absent member being its default — so a member that is valid on its own can be refused beside one the node holds: write the member in the file too, or remove the one you wrote", err)
		}
	}
	// The model grades are merged for the cascade's reason (E2, D2039, D2607):
	// they are the project's own choice of the model each rung runs, the local
	// config carries them from this release (ModelGrades), and the node's block
	// is the only one a rung reads — so a publish that wrote a node without the
	// grades the file does not state would take the project's choice of model
	// away from the ladder, silently, on a machine whose config merely predates
	// the key. Before this member was modelled the same node was *refused*
	// rather than rewritten (publishedPolicyOf's unknown-key refusal,
	// "publishing would drop them"), so a drop here would be a loosening this
	// release introduced.
	pol.ModelGrades = publishedModelGrades(lc, publishedGrades)
	// The pause block is merged for the cascade's reason (R11 of
	// docs/model-credentials-routing, D3184, D3187, D3190): its source is the
	// file as much as the node, and a publish that dropped a member nobody wrote
	// about would put the project back on the plan's own figures — a run held
	// open in the job for a reset the owner meant to pause on.
	//
	// The merge is written here and not inside the `old != nil` guard below
	// because a first publish writes what the file states (publishedPause).
	pol.Pause = publishedPause(lc, publishedPauseNode)
	// The merged block is held to the block's own rule, for the reason the
	// cascade's merge is (ValidCredentials): the merge can hand back a block
	// neither side held while each side was validated alone — the file at load
	// (localcfg.validate) and the node at decode (publishedPolicyOf) — and the
	// rule is about the EFFECTIVE triple, an absent member being its default
	// (ValidPause). A file that writes only `wait_in_job: 1h` is valid beside a
	// node holding `min_pause: 30m`, and the merge of the two is a floor at or
	// over the minimum — a service that would wait in the job for every reset
	// the block was written to pause on. Left to the node's decode, the operator
	// would meet it on the next `fugaro update`, in a sentence that names
	// neither the key they wrote nor the member that came from the node; asked
	// here, both halves are still in hand and the refusal names the file's key.
	// A config that states no block is not asked: what it hands back is the
	// node's own block, which the decode already read.
	if pauseStated(lc.Pause) {
		if err := projectpolicy.ValidPause(pol.Pause, "pause"); err != nil {
			return nil, false, userErr("the pause block this project's local config states, merged with the members the published node holds, is not one the pause can use: %v. A member the file leaves out is the node's (D2607), and the rule is asked of the block as the credentials service would use it — an absent member being its default — so a member that is valid on its own can be refused beside one the node holds: write min_pause beside the floor you raised, or remove the one you wrote", err)
		}
	}
	// The designated repository for committed reports is the owner's own word
	// about where every report of the project lands (D2750, D2751;
	// periodic-reviews R14), and the local config is its carrier, the binaries
	// block's shape: `fugaro.yaml` has no such key (D986's reason, sharper here
	// — the checkout's own agent writes that file, and a key there would let a
	// prompt-injected agent designate where its own reports are committed), the
	// project layer refuses it by name (the launcher's file, and this member
	// decides where every report of the project lands) and the shared config
	// refuses it for the launcher's reason again, so this file is where the
	// operator states it. A file that says nothing keeps the node's designation
	// (ReportsCommitEffective), and a written empty block takes it away (D987).
	//
	// The typed copy is the whole of it, the credentials block's reading rather
	// than the binaries one: the block is strict inside (decodeReportsCommit), so
	// a member this Fugaro does not model, or a block its rules refuse, is
	// refused by publishedPolicyOf before a publish gets here, and what this
	// assignment re-encodes is therefore the whole of the operator's block — no
	// byte-for-byte carry is needed for a spelling to survive. It is what the
	// diff reads (printPolicyDiff's kept line) and what the node is built from.
	var publishedReportsCommit *projectpolicy.ReportsCommit
	if old != nil {
		publishedReportsCommit = old.ReportsCommit
	}
	pol.ReportsCommit = lc.ReportsCommitEffective(publishedReportsCommit)
	if old != nil {
		// The daemon's switch and the project's issue adapter are kept for the
		// model grades' reason (R1 of docs/orchestrator-graph-estimates, D2701,
		// D2733, D2739): no key of this machine's config states either — the
		// switch is the project's own answer to whether the daemon launches, and
		// the adapter is which repositories' issues the backlog is fed from —
		// and the node is the only place both exist. A publish that built a node
		// without them would turn the daemon back on for a project whose owner
		// turned it off, and stop ingesting the issues of a project whose
		// adapter names repositories, silently, from a machine whose config
		// merely predates the keys.
		//
		// The typed copy is the whole of both, the credentials block's reading:
		// the adapter is strict inside (decodeIssueSync, its nested block
		// included), so a member this Fugaro does not model is refused by
		// publishedPolicyOf before a publish gets here, and the switch is one
		// bool. The two are what the diff reads (printPolicyDiff's kept lines)
		// and what the node is built from.
		pol.OrchestratorEnabled = old.OrchestratorEnabled
		pol.IssueSync = old.IssueSync
	}
	// The mobile block is the project's own answers to the pairing surface (P6
	// of docs/mobile-identity-pairing, D2796, O9), and no key of this machine's
	// config states them: the node is the only place they live, so a publish
	// keeps the block it finds — a project whose owner turned pairing off, or
	// chose to store nothing in the identity project, keeps that decision
	// through a publish from any machine. A node that carries no block is one
	// published before the key existed, and O9's answer is that the update
	// writes the default on when it publishes: MobileForPublish is that rule,
	// and it is the whole of what makes the pairing route answer after `fugaro
	// update` and answer `mobile_disabled` before it (F40). It is written
	// outside the `old != nil` guard above for the craft members' reason: the
	// node's own block is one of its two sources, and the other is the default
	// a node that carries none is written with, so a first publish and an
	// installation that has not published since the key existed both get it.
	var publishedMobile *projectpolicy.Mobile
	if old != nil {
		publishedMobile = old.Mobile
	}
	pol.Mobile = projectpolicy.MobileForPublish(publishedMobile)
	// The app's crash-report switch is a top-level key BESIDE the block and not
	// a member of it (policy.go's Policy.MobileCrashReports), and a publish keeps
	// and fills it the way it keeps the block: a node that carries the key holds
	// the project's own answer — the owner's hand-written false included (O13) —
	// and a node published before the key existed is written the default on.
	// Without that fill the reader's fail-safe (an absent key answers off) would
	// leave every installation at the release behind sending nothing for ever
	// (D731, D734, D738).
	var publishedCrashReports *bool
	if old != nil {
		publishedCrashReports = old.MobileCrashReports
	}
	pol.MobileCrashReports = projectpolicy.MobileCrashReportsForPublish(publishedCrashReports)
	// The crafts' credentials, their pools and the crafts that choose among
	// them are the operator's own blocks from this release on (docs/model-crafts
	// §1–3, MC18, D1667): the local config is their carrier, and this publish
	// writes what it states. It is written outside the `old != nil` guard above
	// because it is the one member of the group whose source is the file rather
	// than the node — a first publish writes the file's blocks, and the node's
	// own stand for every name the file does not state (craftMembersForPublish).
	creds, pools, crafts, err := craftMembersForPublish(lc, old)
	if err != nil {
		return nil, false, err
	}
	pol.CraftCredentials, pol.CraftPools, pol.Crafts = creds, pools, crafts
	return pol, keepRouting, nil
}

// craftMembersForPublish is the craft_credentials, craft_pools and crafts block
// the node is written with: the definitions the operator's config states, and
// the node's own for every name it does not state.
//
// The merge direction is the clone_urls rule and the reason for it is the same
// one, one block over: a file that states a definition is the operator's own
// edit and wins, and a file that states none leaves the node's exactly as it
// was — a machine whose config predates the keys, and an owner who declined to
// write them, keep a project whose runs resolve their crafts as they did. What
// is NOT in the merge is a removal: a definition the node holds and the file
// does not mention stays, because the node is what a run resolves a craft
// against, and a pool that disappeared because one machine's file happened to
// be silent would take a grant away with no refusal and no line. A removal is
// the explicit unset's (D2607, F22): a name in this file's node_removals is the
// one statement that takes a definition out of the node (removedNames), and it
// is written by the command that removed it rather than inferred from a
// silence.
//
// The one thing that can make the node unreadable is refused here rather than
// left to the writer's own check: a credential and a pool share one namespace
// in a published block (config.ValidatePools), so a credential the file defines
// under a name the node already publishes a pool of would be a node this
// Fugaro cannot decode — and a refusal that names the two and the fix is what
// the operator can act on, where checkPolicyBytes' own sentence says only that
// the document cannot be read back.
func craftMembersForPublish(lc *localcfg.Config, old *projectpolicy.Policy) ([]config.Credential, []config.Pool, *config.Crafts, error) {
	creds := publishedCraftCredentials(lc, old)
	pools, _ := publishedCraftPools(lc, old, creds)
	if clash := craftNamespaceClash(creds, pools); clash != "" {
		return nil, nil, nil, userErr("%s; a craft's access names a credential or a pool and does not say which, so the node this publish would write is one no reader can decode. Rename one of the two in this machine's config, or in the published node by hand, and publish again", clash)
	}
	return creds, pools, publishedCrafts(lc, old), nil
}

// publishedCraftCredentials is the craft_credentials block the node is written
// with: the definitions the config states, in the config's order, and the
// node's own for every name it does not state. A definition the config states
// replaces the node's entry of that name — a credential is a name and a type,
// so a file that changes the type is an edit like any other.
func publishedCraftCredentials(lc *localcfg.Config, old *projectpolicy.Policy) []config.Credential {
	node := craftCredentialsOf(old)
	removed := nameSet(removedNames(lc).CraftCredentials)
	if len(lc.Credentials) == 0 && len(removed) == 0 {
		return node
	}
	out := slices.Clone(lc.Credentials)
	stated := make(map[string]bool, len(out))
	for _, c := range out {
		stated[c.Name] = true
	}
	for _, c := range node {
		if !stated[c.Name] {
			out = append(out, c)
		}
	}
	out = slices.DeleteFunc(out, func(c config.Credential) bool { return removed[c.Name] })
	if len(out) == 0 {
		// A block this publish emptied is no block: the database keeps no member
		// for an empty list, so returning the empty slice would leave a publish
		// reporting a difference against the node it just wrote on every run
		// after it (the HARD RULE's own loss).
		return nil
	}
	return out
}

// publishedCraftPools is the craft_pools block the node is written with, and
// the diff lines the operator reads about a pool of the config the node cannot
// carry.
//
// A pool the config states wins the node's copy of its name member by member —
// the members, the strategy and the allowed users are the config's where it
// states them, and the owner comes from the node's copy where the config states
// none (docs/config-ownership, A1's "a written value wins field by field"). The
// node's legacy-open marker is the one member that is never the config's: it is
// what the D1942 migration writes for a pool that predates owners, it serves
// the pool to everyone, and a marking written in a file would be a false claim
// about the node's own history (D2608). A pool the config marks and the node
// does not carries no marker, so it is published only if its own owner says who
// answers for it.
//
// The one pool that is published without an owner is the pool the config marks
// as the cascade's: the marker never reaches the node (R15), and what the node
// holds instead is the ordinary legacy_open pool a cascaded project's runs are
// served — the node's readers know no cascades, and this is the shape they do
// know (F36). Its own rule refuses a marker with no cascade behind it and an
// owner written beside one (config.ValidateCascadePools), so the exception
// cannot be reached by a pool nobody's migration wrote.
//
// A pool neither the config nor the node can publish — an ownerless one the
// node has no copy of, which is what `fugaro accounts pool add` writes today —
// is left out and NAMED in one line: the node would be one this Fugaro refuses
// to read back (config.ValidatePublishedPools refuses an ownerless published
// pool, and both doors write through checkPolicyBytes), and neither door fails
// on it (D2608). The check is the block's own and is asked of the pool in the
// block it is being written into, so a pool of the config whose name the node
// defines a credential of is left out here rather than refused: a craft's
// access resolves to that credential, and the line says which name it was.
func publishedCraftPools(lc *localcfg.Config, old *projectpolicy.Policy, creds []config.Credential) ([]config.Pool, []string) {
	node := craftPoolsOf(old)
	removed := nameSet(removedNames(lc).Pools)
	if len(lc.Pools) == 0 && len(removed) == 0 {
		return node, nil
	}
	var out []config.Pool
	var lines []string
	stated := make(map[string]bool, len(lc.Pools))
	for _, p := range lc.Pools {
		stated[p.Name] = true
		if removed[p.Name] {
			// The name is this file's own removal (D2607, F22): the pool is
			// gone from the node, and a definition the file still states for it
			// is the file contradicting its own record — the removal wins,
			// because it is the statement about the node and the definition is
			// what the command that wrote the record dropped already.
			continue
		}
		q, held := craftPoolNamed(old, p.Name)
		pub := p
		// A pool's rule, principal and threshold are the config's three new
		// members (PL1, R21) and the node's craft_pools may not carry them: that
		// block is decoded strictly by every job image of the installation, so a
		// release behind this one refuses the whole node for a key it does not
		// know. The pools in their new shape travel under the policy's
		// `model_routing` key instead (RT4), and what the node holds here is the
		// pool as the readers of craft_pools have always read it.
		pub.Rule, pub.Principal, pub.Threshold = "", nil, nil
		// The marker is never the config's (D2608), so it starts cleared and is
		// taken from the node alone, and only while nothing else answers for the
		// pool: a pool that names an owner now is not one that predates owners.
		pub.LegacyOpen = false
		// The cascade's marker is the installation's and never the node's: the
		// node's readers are job images that know no such key, and the access
		// record the service reads is written from the file (R15, CM36). The
		// pool it marks is published as the ordinary legacy_open pool the
		// cascade is — served to everyone, owned by nobody — which is the one
		// exception to the rule above and to the owner the validator asks for:
		// the marker's own rule refuses a marker with no cascade behind it and
		// an owner written beside one (config.ValidateCascadePools), so a
		// marked pool here is the migration's, and a publish that asked it for
		// an owner would leave a cascaded project's runs served nothing (F36).
		// The node's owner of that name goes with it: a pool served to everyone
		// serves no owner, and carrying one forward would leave the node with a
		// pool this publish then refuses to write at all.
		pub.Cascade = false
		pub.LegacyOpen = p.Cascade
		if held && !p.Cascade {
			if pub.Owner == "" {
				pub.Owner = q.Owner
			}
			if len(pub.AllowedUsers) == 0 {
				pub.AllowedUsers = q.AllowedUsers
			}
			pub.LegacyOpen = q.LegacyOpen && pub.Owner == "" && len(pub.AllowedUsers) == 0
		}
		if ps := config.ValidatePublishedPools([]config.Pool{pub}, creds); len(ps) > 0 {
			why := ps[0].Message
			if p.LegacyOpen && !pub.LegacyOpen {
				// The pool was written with the migration's marker and this
				// publish does not carry it, so the validator's own sentence
				// ("mark a pool that predates owners as legacy_open") would be
				// advice the operator has already taken: say what happened to
				// the key instead.
				why = "legacy_open is the marker the migration writes for a pool that predates owners, and a pool in this file is never published with it: name the pool's owner"
			}
			lines = append(lines, fmt.Sprintf("  craft_pools %s not published: %s", pub.Name, why))
			continue
		}
		out = append(out, pub)
	}
	for _, q := range node {
		if !stated[q.Name] && !removed[q.Name] {
			out = append(out, q)
		}
	}
	return out, lines
}

// craftPoolNamed is the node's pool of this name, and whether it holds one.
func craftPoolNamed(old *projectpolicy.Policy, name string) (config.Pool, bool) {
	for _, q := range craftPoolsOf(old) {
		if q.Name == name {
			return q, true
		}
	}
	return config.Pool{}, false
}

// craftNamespaceClash is the name a credential of the published block and a
// pool of it both carry, "" when the two blocks are as they must be. It is the
// rule config.ValidatePools applies to one document, asked here of the document
// this publish would write — whose two halves come from two sources, which is
// why it can arise at all when neither the file nor the node breaks it alone.
//
// The one collision that document admits is the one that rule admits: the pool
// of one named after its own credential (D3176), which is the pool `fugaro
// accounts add` writes for a subscription token and the migration writes for
// every lone account, so a node that holds one publishes like any other pool.
func craftNamespaceClash(creds []config.Credential, pools []config.Pool) string {
	if len(creds) == 0 || len(pools) == 0 {
		return ""
	}
	named := make(map[string]bool, len(creds))
	for _, c := range creds {
		named[c.Name] = true
	}
	for _, p := range pools {
		if named[p.Name] && !config.PoolMayTakeItsOnlyMembersName(p, creds) {
			return fmt.Sprintf("this machine's config defines the credential %s and the published node holds a pool of that name", p.Name)
		}
	}
	return ""
}

// publishedCrafts is the crafts block the node is written with: the config's
// crafts, each half filled from the node's craft of the same name where the
// config sets none, and the node's whole block where the config states no craft
// at all.
//
// The merge is per half and not per craft, which is the pools' rule and the
// spec's own shape: a craft sets its model and its access independently
// (config.Craft), so a file that writes one of the two has said nothing about
// the other, and the answer the node carries stands. A block the config writes
// that sets no craft is no block (the node's own rule, decodeCraftMembers), so
// it keeps the node's exactly as a file that writes nothing does.
func publishedCrafts(lc *localcfg.Config, old *projectpolicy.Policy) *config.Crafts {
	node := policyCraftsOf(old)
	removed := nameSet(removedNames(lc).Crafts)
	if lc.Crafts == (config.Crafts{}) && len(removed) == 0 {
		return node
	}
	out := config.Crafts{}
	for _, name := range config.CraftNames {
		if removed[name] {
			// The craft is this file's own removal (D2607, F22), the pool
			// removal's rule one block over: the node stops stating it, and
			// whatever the file says about it beside the record is dropped with
			// it.
			continue
		}
		c := lc.Crafts.Of(name)
		if node != nil {
			q := node.Of(name)
			if c.Model == "" {
				c.Model = q.Model
			}
			if c.Access == "" {
				c.Access = q.Access
			}
		}
		out = withCraft(out, name, c)
	}
	if out == (config.Crafts{}) {
		// Every craft of the block is gone, so the block is: a node that
		// carried an empty crafts member would be one this Fugaro reads back as
		// no member at all (decodeCraftMembers' rule), and the publish would go
		// on reporting the difference it cannot close.
		return nil
	}
	return &out
}

// withCraft is the block with one craft written into it. The crafts are a
// struct of five fields rather than a map (config.Crafts says why), so the name
// is the only way in — and every name of config.CraftNames is one case here,
// which is what a craft the operator writes being published at all depends on
// (the list and this switch are one enumeration, and the merge-judge craft is
// as much a member of the block as the four the runner's stages name).
func withCraft(c config.Crafts, name string, craft config.Craft) config.Crafts {
	switch name {
	case config.CraftCoder:
		c.Coder = craft
	case config.CraftReview:
		c.Review = craft
	case config.CraftSeniorReview:
		c.SeniorReview = craft
	case config.CraftSecurityReview:
		c.SecurityReview = craft
	case config.CraftMergeJudge:
		c.MergeJudge = craft
	}
	return c
}

// publishedCredentials is the credentials block the node is written with: the
// cascade this machine's config states, merged into the node's own member by
// member (D2607, F5, F6).
//
// The merge is the craft members' rule and the binaries block's, one block
// over: a config that states nothing keeps the node's block whole — the
// carrier's pointer is what says which of the two this file is
// (CredentialCascade), and a block written with every member absent is no
// statement at all (credentialsStated), the crafts block's own rule — and a
// config that states the block wins member by member, because a file that
// writes `wait_threshold` has said nothing about the fallback and a publish
// that read the silence as "the default" would put a project back on
// oauth-only for a block nobody meant to change.
//
// The block's own type is carried rather than a value per key, and the two
// pointer members are why: a written `false` for
// stop_after_exhaustion_per_window and a written 0 for concurrency_per_token
// are values (D142) and not absent members, and only the pointer tells the two
// apart. The block is strict inside (projectpolicy.ValidCredentials, asked of
// the carrier at load and of the node's own document by publishedPolicyOf), so
// a member this Fugaro does not model never reaches here.
func publishedCredentials(lc *localcfg.Config, node *projectpolicy.Credentials) *projectpolicy.Credentials {
	if !credentialsStated(lc.CredentialCascade) {
		return node
	}
	out := *lc.CredentialCascade
	if node != nil {
		if out.Fallback == "" {
			out.Fallback = node.Fallback
		}
		if out.WaitThreshold == "" {
			out.WaitThreshold = node.WaitThreshold
		}
		switch {
		case out.Backoff == nil:
			out.Backoff = node.Backoff
		case node.Backoff != nil:
			// The file states the block, so its members are the file's and the
			// node's fill only what it leaves out. The copy is the file's own,
			// which is what keeps this merge from writing into the node it is
			// merging over.
			b := *out.Backoff
			if b.Initial == "" {
				b.Initial = node.Backoff.Initial
			}
			if b.Cap == "" {
				b.Cap = node.Backoff.Cap
			}
			out.Backoff = &b
		}
		if out.StopAfterExhaustionPerWindow == nil {
			out.StopAfterExhaustionPerWindow = node.StopAfterExhaustionPerWindow
		}
		if out.ConcurrencyPerToken == nil {
			out.ConcurrencyPerToken = node.ConcurrencyPerToken
		}
	}
	return &out
}

// publishedPause is the pause block the node is written with (R11 of
// docs/model-credentials-routing, D3184, D3187, D3190): the members this
// machine's config states, merged into the node's own member by member.
//
// The merge is publishedCredentials' exactly: a config that states nothing
// keeps the node's block whole — a block written with every member absent is no
// statement at all (pauseStated) — and a config that states a member wins that
// member and takes the node's for the ones it leaves out. What the merge must
// never do is drop a member nobody wrote about: the block decides what every
// run of the project does when its credentials are exhausted, and a publish
// that wrote a node without the minimum would put a project back on the plan's
// 30 minutes from a machine whose config merely predates the key — for the runs
// in flight while `fugaro update` republishes the node, a pause placed on a
// reset a minute away.
//
// A member is a duration and nothing else, so a written one is a written one:
// there is no zero value that means "the operator said so" here, and the
// pointer is the whole of the silence (Pause).
func publishedPause(lc *localcfg.Config, node *projectpolicy.Pause) *projectpolicy.Pause {
	if !pauseStated(lc.Pause) {
		return node
	}
	out := *lc.Pause
	if node != nil {
		if out.WaitInJob == "" {
			out.WaitInJob = node.WaitInJob
		}
		if out.MinPause == "" {
			out.MinPause = node.MinPause
		}
		if out.MaxPause == "" {
			out.MaxPause = node.MaxPause
		}
	}
	return &out
}

// pauseStated is whether a pause block states a member at all: a block written
// with every member absent is no statement, the crafts block's rule ("a block
// that sets no craft is no block"). It is what keeps a publish from writing an
// empty member: the database keeps none for an empty object, and a node that
// carried one would be reported as a difference against the node it has just
// written on every run after it (the HARD RULE's own loss).
func pauseStated(p *projectpolicy.Pause) bool {
	return p != nil && *p != (projectpolicy.Pause{})
}

// credentialsStated is whether a cascade block states a member at all, one
// level down included: a block written with every member absent — and a
// `backoff:` written with both of its own absent — is no statement, the crafts
// block's rule ("a block that sets no craft is no block"). It is what keeps a
// publish from writing an empty member: the database keeps none for an empty
// object, so a publish that wrote one would report a difference against the
// node it just wrote on every run after it (the HARD RULE's own loss).
func credentialsStated(c *projectpolicy.Credentials) bool {
	if c == nil {
		return false
	}
	if c.Backoff != nil && (c.Backoff.Initial != "" || c.Backoff.Cap != "") {
		return true
	}
	return c.Fallback != "" || c.WaitThreshold != "" ||
		c.StopAfterExhaustionPerWindow != nil || c.ConcurrencyPerToken != nil
}

// publishedModelGrades is the model_grades block the node is written with: the
// grades this machine's config states, merged into the node's own grade by
// grade and half by half (D2607, E2).
//
// The merge is publishedCrafts' exactly: a config that states no grade keeps
// the node's block, a grade the config writes wins the halves it writes and
// takes the node's for the half it leaves out — a grade's model and its access
// are the independent pair a craft carries (config.ModelGrades) — and a grade
// the config does not write is the node's. A block the config writes that sets
// no grade is no block (config.ModelGrades.Empty), the crafts block's rule
// again, so an installation whose config predates the key publishes the node's
// grades exactly as they were.
func publishedModelGrades(lc *localcfg.Config, node *config.ModelGrades) *config.ModelGrades {
	if lc.ModelGrades == nil || lc.ModelGrades.Empty() {
		return node
	}
	out := config.ModelGrades{}
	for _, name := range config.GradeNames {
		c := lc.ModelGrades.Of(name)
		if node != nil {
			q := node.Of(name)
			if c.Model == "" {
				c.Model = q.Model
			}
			if c.Access == "" {
				c.Access = q.Access
			}
		}
		if c == (config.Craft{}) {
			continue
		}
		out = withGrade(out, name, c)
	}
	if out.Empty() {
		return node
	}
	return &out
}

// withGrade is the grades block with one grade written into it, withCraft's
// shape and its reason: the block is a struct rather than a map, so the name is
// the only way in.
func withGrade(g config.ModelGrades, name string, grade config.Craft) config.ModelGrades {
	switch name {
	case config.GradeStrong:
		g.Strong = &grade
	case config.GradeStrongest:
		g.Strongest = &grade
	}
	return g
}

// removedNames is this machine's explicit-unset record as a value, so a config
// that holds none — the state of every installation until a removal command
// writes one — reads as a record that names nothing.
func removedNames(lc *localcfg.Config) localcfg.NodeRemovals {
	if lc == nil || lc.NodeRemovals == nil {
		return localcfg.NodeRemovals{}
	}
	return *lc.NodeRemovals
}

// nameSet is a list of names as a set, which is how a removal record is read:
// the question asked of it is always "is this name one of them".
func nameSet(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

// craftCredentialsOf, craftPoolsOf and craftsOf are the node's own three
// members, with a node that is not there — and one that holds none — read as no
// member at all.
func craftCredentialsOf(p *projectpolicy.Policy) []config.Credential {
	if p == nil {
		return nil
	}
	return p.CraftCredentials
}

func craftPoolsOf(p *projectpolicy.Policy) []config.Pool {
	if p == nil {
		return nil
	}
	return p.CraftPools
}

func policyCraftsOf(p *projectpolicy.Policy) *config.Crafts {
	if p == nil {
		return nil
	}
	return p.Crafts
}

// nodeRoutesClaude reports whether the node being replaced carries a routing
// block that sends Claude calls somewhere: a via that is a route — openrouter,
// or auto, which is openrouter when the run holds no Anthropic key (D1b). An
// absent block, via: direct and via: cascade are not routes: the first two route
// nothing, and the cascade decides which credential carries a call and never
// where the call goes (nodeTurnsTheCascadeOn, below).
func nodeRoutesClaude(p *projectpolicy.Policy) bool {
	if p == nil {
		return false
	}
	return routingRoutesClaude(p.Routing)
}

// routingRoutesClaude is the switch that rule is about, stated for a routing
// block rather than for the node that carries one: `fugaro project routing set`
// writes the same block into the operator's config, and it asks the same two
// questions of what it read (projectRoutingRefuseSilence) that this publish asks
// of the node — one rule, asked twice, rather than a second copy of it in the
// command that writes the block.
func routingRoutesClaude(r *projectpolicy.Routing) bool {
	if r == nil || r.Claude == nil {
		return false
	}
	switch r.Claude.ViaValue() {
	case projectpolicy.RouteOpenRouter, projectpolicy.RouteAuto:
		return true
	default:
		return false
	}
}

// nodeTurnsTheCascadeOn reports whether the node being replaced carries the
// switch that turns the credentials cascade on: routing.claude.via: cascade
// (O2, O3; the spelling O3 settled). It is asked separately from
// nodeRoutesClaude because the two answers need different sentences, not
// because either is less of a switch: both refuse a publish that is silent
// about routing, and for opposite-sounding reasons.
//
// A route that goes away is a Claude call that stops being sent where it was
// put, which is what nodeRoutesClaude's refusal says. The cascade going away
// sends every call where it always went and changes only which credential pays
// for it — that is exactly why it must be refused too, and not carried over in
// silence: the cascade is what hands a run the project's other credentials
// (the donated tier-A subscription tokens, and with credentials.fallback: auto
// the paid tiers). Deleting routing.claude from the config reads as "cascade
// off", and until commit 653decaf this publish refused on that reading; that
// commit narrowed the predicate to the routes, on the correct ground that the
// cascade is not a route but the wrong conclusion that nothing therefore needed
// refusing, so a publish kept the switch and only the version warning mentioned
// it. The independent security review's major finding on that commit.
//
// What ends it is the same as for a route: writing the switch the operator
// means (via: direct turns the cascade off and is an inert block, so it is kept
// and costs nothing), or --remove-routing to drop the block itself on purpose.
func nodeTurnsTheCascadeOn(p *projectpolicy.Policy) bool {
	return p != nil && routingTurnsTheCascadeOn(p.Routing)
}

// routingTurnsTheCascadeOn is that switch for a routing block rather than for
// the node that carries one, so `fugaro project routing unset` refuses the same
// shape this publish refuses (projectRoutingRefuseSilence): the two ask one
// question, and neither can drift from the other about what the cascade is. The
// question itself is config's (RoutingServesTheCascade), because the pool
// carrying the cascade's marker is admitted by the same answer
// (config.ValidateCascadePools, CM32): a project whose cascade serves is one
// thing to this CLI, and a third reading of it here would be a way for the
// marker's rule and the publish's to disagree.
func routingTurnsTheCascadeOn(r *projectpolicy.Routing) bool {
	return config.RoutingServesTheCascade(r)
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

// policyProfiles is the policy's profiles block, built from the operator's
// config.
//
// Each entry is the operator's own carrier, copied whole, for the reason
// policyProviders copies a provider whole: config.JobProfile is the one
// declaration of the document (projectpolicy.Profile embeds it), so a copy by
// name could only lose a key a later release adds.
//
// The block is published exactly as the file spells it, and the rules that
// make it usable are the policy's own (projectpolicy.validProfiles, run by
// checkPolicyBytes before anything is written): the default profile
// defined, every provider named and known, cpu/memory a Cloud Run shape, the
// timeout within Cloud Run's ceiling, and every allow_repos pattern of the
// profile's own provider. Refusing them here too would be a second copy of
// those rules to drift from — and the operator reads the refusal with the
// profile's name in it either way.
//
// A config that sets no profiles: publishes no block, and so drops the one
// the node carried — the same rule the providers, the prices and the budget
// follow (the file is what a publish is built from), and the reason
// printPolicyDiff prints a line per profile: the operator reads the loss
// before it is written, rather than discovering it when a repository's
// workflow is refused at run start.
func policyProfiles(lc *localcfg.Config) map[string]projectpolicy.Profile {
	if len(lc.Profiles) == 0 {
		return nil
	}
	out := make(map[string]projectpolicy.Profile, len(lc.Profiles))
	for name, jp := range lc.Profiles {
		out[name] = projectpolicy.Profile{JobProfile: jp}
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
			ImageTokens: p.ImageTokens,
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
// A node with no routing member, and one whose member is written as null, are
// both written without one — that is direct (D1b), and the policy's own decode
// reads the two the same way.
func policyNodeBytes(pol *projectpolicy.Policy, oldRaw []byte, exists, keepRouting, keepCloneURLs, keepBinaries bool) ([]byte, error) {
	node, err := json.Marshal(pol)
	if err != nil {
		return nil, userErr("building the policy: %v", err)
	}
	if !exists {
		return node, nil
	}
	// The clone_urls member is carried over byte for byte while this machine's
	// config states no block of its own: the member is then the node's, and a
	// key inside it that this binary does not model (a clone_urls field of a
	// newer release) would be dropped by a re-encode of the decoded value. Once
	// the config states the block the member is that block — every entry the
	// operator's own file names — and a key the node holds beside them belongs
	// to the source the file just replaced, so the encoded value is what is
	// written.
	if member, ok, err := jsonMember(oldRaw, "clone_urls"); err != nil {
		// The node decoded as a policy a moment ago (publishedPolicyOf), so a
		// member of it cannot be unreadable; refuse rather than write a node
		// that silently lost the block.
		return nil, userErr("%s cannot be read back as a document, so its clone_urls block cannot be kept: %v; fix it by hand, or remove it with 'fugaro project policy publish --delete'", projectpolicy.PathShared(), err)
	} else if ok && keepCloneURLs {
		if node, err = patchPolicyJSON(node, "clone_urls", member); err != nil {
			return nil, userErr("building the policy: %v", err)
		}
	}
	// The binaries member is carried over byte for byte while this machine's
	// config states no block of its own (keepBinaries), for the clone_urls
	// reason: the member is then the node's, and the carry is what keeps this
	// function from choosing a spelling for a block the operator hand-wrote.
	// Once the file states the block — the empty one included, which is how an
	// operator clears it (D987) — the file is what the operator's own edit
	// wrote, and the encoded value is what is written.
	if member, ok, err := jsonMember(oldRaw, "binaries"); err != nil {
		return nil, userErr("%s cannot be read back as a document, so its binaries block cannot be kept: %v; fix it by hand, or remove it with 'fugaro project policy publish --delete'", projectpolicy.PathShared(), err)
	} else if ok && keepBinaries && !projectpolicy.IsNull(member) {
		if node, err = patchPolicyJSON(node, "binaries", member); err != nil {
			return nil, userErr("building the policy: %v", err)
		}
	}
	// The credentials member is *not* patched here, unlike clone_urls and
	// routing, and that is a deliberate difference rather than an oversight.
	// Those two are shared with releases that know members this one does not:
	// Decode tolerates an unmodelled key inside them, so a re-encode of the
	// decoded value would drop it and the node's own bytes are what has to
	// survive. This block is strict inside (the package doc's "strict in the
	// other direction"), so an unmodelled key in it is refused by
	// publishedPolicyOf before this function is reached, and every block that
	// gets here is one this Fugaro models *whole* — the typed assignment in
	// buildProjectPolicy re-encodes it losslessly. Carrying it as a document
	// member on top would be code no test can tell the difference in.
	routing, ok, err := jsonMember(oldRaw, "routing")
	if err != nil {
		// The node decoded as a policy a moment ago (publishedPolicyOf), so a
		// member of it cannot be unreadable; refuse rather than write a node
		// that silently lost the block.
		return nil, userErr("%s cannot be read back as a document, so its routing block cannot be kept: %v; fix it by hand, or remove it with 'fugaro project policy publish --delete'", projectpolicy.PathShared(), err)
	}
	if !ok || projectpolicy.IsNull(routing) {
		// No member and a null member are one thing here, as they are to Decode
		// (validRoutingMembers reads a null block as the absent one): the node
		// carries no switch, so there is nothing to keep and nothing to patch,
		// and the node the publisher built is written as it is. Refusing a null
		// member instead — the check below reads it as a non-object block — made
		// a published `"routing": null` unpublishable until it was edited by
		// hand (review finding on this change).
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
	if !ok || projectpolicy.IsNull(claude) {
		// No claude member, or one written as null: the config's switch is the
		// whole of it (the null member is an absent one to Decode too).
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
//
// The read is the strict one (projectpolicy.DecodeStrict), because this is the
// writer: a member inside the routing table that this release does not model,
// and a table RT3's checks refuse, are exactly the mistakes this gate is here
// to keep off the node. A run in flight reads that same node through Decode,
// which tolerates both — a reader that refused them would halt every job image
// on the project over a node a later release published.
func checkPolicyBytes(raw []byte) error {
	p, found, err := projectpolicy.DecodeStrict(raw)
	switch {
	case err != nil:
		// The credentials member, when the refusal names one, is the block this
		// machine's own file is the carrier of (credential_cascade, D2607) as the
		// publisher merged it with the members the node holds — or, for a config
		// that states no block, the published node's own. That matters to the
		// operator because 'fugaro validate' — which the sentence sends them to
		// for everything else — reads a repository's fugaro.yaml and the project
		// config, not this key: a block the file states is fixed in the file,
		// while a block only the node holds has no local key to fix, and its ways
		// out are the node itself or a fresh policy. (Security review m-1.)
		return userErr("the policy this Fugaro would write is one it cannot read back, so nothing was written (%v): check the values it is built from — the budget block, the providers' models, model_prices, the profiles and the allow-list — with 'fugaro validate'. When the refusal names a credentials member, the block is this machine's own: the credential_cascade key its local config states, merged with the members the published node holds — or, when the file states no block, the node's own, which no local key reaches. Write the member in your own file ('fugaro validate' reads a repository's fugaro.yaml, not this key); when the file states no block there is no key to write, and the ways out are removing that member from the node by hand, or publishing with --delete to drop the policy and write a fresh one", err)
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

// ------------------------------------------------- per-repository admissions

// A publish writes each repository's admitted Claude account names into its own
// node under config/policy/repos (O4), and removes the children of the
// repositories no account admits any more. Two functions do it, in this order:
// tightenAdmissions before the shared node's own write, widenAdmissions after
// it.
//
// The admissions are the accounts block inverted: accounts.<name>.allow_repos
// says which repositories an account's token may be used by, and the service
// that holds those tokens reads the question the other way round — for this
// repository, which accounts may it use (the plan's C1.11). The names come from
// the same derived block the shared node carries (projectpolicy.DeriveAccounts,
// from the operator's config), so the two are one answer written twice, and the
// node holds names only: an account's token is a secret version the service
// reads as its own service account, never something published here.
//
// The key is the repository's slug — task.Slug(provider, owner/name), the
// string the run's own token carries as its `fs` claim and the key
// config/policy/repos/$slug is written under — so the repository whose run may
// read a child is exactly the repository the child is about.
//
// A repository some account covers has a child; one no account covers has none.
// The absence is the whole statement ("this repository is admitted to no
// account"), and it is what a node with an empty list would have to mean anyway
// — DecodeRepo refuses that spelling rather than reading it.

// admissionBytes is the document a publish writes for one repository child, and
// the reader's own verdict on it before it is written — checkPolicyBytes, for the
// children.
//
// The shared node is decoded before it is written and a child was not, so a
// child this Fugaro could not read back went to the database and was discovered
// by the *next* publish, which then refuses the whole node and stops every later
// publish of the project until an operator fixes it by hand: DecodeRepo is
// strict, and tightenAdmissions refuses to write over a child it cannot read.
// No config reaches that today — the names are config.ValidClaudeAccountRef's
// own (DeriveAccounts checks each one), the account names are map keys, and
// repoAdmissions sorts and never repeats one — so this is the second door on a
// rule the first door already holds, and it is here because the two ways in are
// a rule the writer and the reader could come to disagree about and a future
// caller of these phases. (Security review m-6.)
//
// The bytes it returns are what the caller patches, so the document that is
// written and the document that was read back cannot be two spellings of the
// same admission.
func admissionBytes(slug string, accounts []string) (json.RawMessage, error) {
	raw, err := json.Marshal(projectpolicy.RepoPolicy{Credentials: &projectpolicy.RepoCredentials{Accounts: accounts}})
	if err != nil {
		return nil, remote(err)
	}
	switch decoded, found, err := projectpolicy.DecodeRepo(raw); {
	case err != nil:
		return nil, userErr("the admission this publish would write for %s is not a node this Fugaro can read back (%v), so nothing was written: a child node holds the NAMES of the accounts its repository may use, and each must be a name the project policy's accounts block carries — check this machine's config with 'fugaro validate'", slug, err)
	case !found || decoded == nil:
		return nil, userErr("the admission this publish would write for %s is not a node this Fugaro can read back, so nothing was written: check this machine's config with 'fugaro validate'", slug)
	}
	return raw, nil
}

// admissionPatch is admissionBytes over a whole phase's children, keyed by slug.
// The phase's write is one multi-path patch, so every child it would carry is
// checked before any of them is written: a refusal has to come before the patch,
// not out of the middle of it.
func admissionPatch(desired map[string][]string) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(desired))
	for _, slug := range slices.Sorted(maps.Keys(desired)) {
		raw, err := admissionBytes(slug, desired[slug])
		if err != nil {
			return nil, err
		}
		out[slug] = raw
	}
	return out, nil
}

// tightenAdmissions is the first of the admissions' two writes: it takes away
// what the config no longer admits, and it writes *before* the shared node.
// `now` is what each existing child holds once it has run, so the widening
// phase knows what it still has to write; `lines` says what it narrowed, and
// the caller repeats them in the failure message when a later write of the same
// publish fails (a narrowing is not undone by that failure, and the operator is
// not told about it anywhere else).
//
// The direction is the security decision. A child is what the service reads to
// decide whether a repository's run may be handed somebody's subscription token
// (C1.11), so a child left admitting an account the shared node does not cover
// is a revocation that silently did not happen; a child *not* yet admitting an
// account the shared node does cover costs a repository tier A until the next
// publish, which is the fail-closed direction. The children are one atomic
// patch, so the set of narrowings is never half-applied.
//
// An existing child this Fugaro cannot read is refused, not written over: the
// set of accounts it admits is exactly what this function has to narrow, and a
// node whose meaning is unknown is a node whose narrowing cannot be computed.
// (Removing such a child is refused by the same rule, below.)
func tightenAdmissions(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, desired map[string][]string, existing map[string]json.RawMessage) (now map[string][]string, lines []string, err error) {
	now = make(map[string][]string, len(existing))
	updates := make(map[string]any, len(existing))
	for _, key := range slices.Sorted(maps.Keys(existing)) {
		decoded, found, err := projectpolicy.DecodeRepo(existing[key])
		if err != nil {
			return nil, nil, userErr("%s/%s is a node this Fugaro cannot read (%v), so this publish cannot say what it admits: it will not be narrowed and it will not be removed. A node this reader does not understand is a node whose admissions it cannot compute, and a publish that wrote over it would be a publish that changed an admission it could not read; fix it by hand, or remove it with 'fugaro project policy publish --delete'",
				projectpolicy.PathRepos(), key, err)
		}
		if !found {
			continue // a null child is no child
		}
		admitted := decoded.AdmittedAccounts()
		// What the child may keep: the accounts it admits today that the config
		// still admits for this repository. A repository the config no longer
		// admits at all keeps none of them.
		var keep []string
		for _, name := range admitted {
			if slices.Contains(desired[key], name) {
				keep = append(keep, name)
			}
		}
		if len(keep) == len(admitted) {
			// Nothing to take away: this child is left for the widening phase
			// (which writes it only if it is missing something), so the child
			// is not written twice with the same value.
			now[key] = admitted
			continue
		}
		if len(keep) == 0 {
			// Nothing the child admits survives, so it goes — a written empty
			// list is a document DecodeRepo refuses, and leaving the child as it
			// is would leave an admission the shared node no longer covers.
			// Two different facts wear that same shape, and the line has to say
			// which one it is (the review finding on this change): the
			// repository is admitted by no account at all any more, or it has
			// changed hands — the accounts that used to serve it do not any
			// more, and the widening phase writes the ones the config admits
			// below, once the shared node carries them. Reporting the swap as a
			// removal put "no account admits this repository" over the very
			// next line's "admits <slug>: spare", and the first line was untrue.
			updates[key] = nil
			now[key] = nil
			if len(desired[key]) == 0 {
				lines = append(lines, fmt.Sprintf("  admits %s: removed (no account admits this repository)", key))
			} else {
				lines = append(lines, fmt.Sprintf("  admits %s: %s no longer admits this repository (the child is written again below)", key, strings.Join(admitted, ", ")))
			}
			continue
		}
		raw, err := admissionBytes(key, keep)
		if err != nil {
			return nil, nil, err
		}
		updates[key] = raw
		now[key] = keep
		lines = append(lines, fmt.Sprintf("  admits %s: narrowed to %s (%s no longer admits this repository)", key, strings.Join(keep, ", "), strings.Join(accountsWithout(admitted, keep), ", ")))
	}
	if len(updates) == 0 {
		return now, nil, nil
	}
	if err := db.Patch(ctx, projectpolicy.PathRepos(), updates); err != nil {
		return nil, nil, dbErr(lc, err, true)
	}
	return now, lines, nil
}

// widenAdmissions is the second write: the children brought up to what the
// config admits, and it runs only once the shared node carries that config.
// Every repository the config admits is reported (that line is the statement of
// who may use a token, and an operator reading a publish has to see it), but a
// child that already holds the desired names is not written again — so a
// publish of an unchanged config writes no child at all, and cannot fail on
// one.
func widenAdmissions(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, desired map[string][]string, desiredBytes map[string]json.RawMessage, now map[string][]string) ([]string, error) {
	updates := make(map[string]any, len(desired))
	var lines []string
	for _, slug := range slices.Sorted(maps.Keys(desired)) {
		lines = append(lines, fmt.Sprintf("  admits %s: %s", slug, strings.Join(desired[slug], ", ")))
		if cur := now[slug]; len(cur) == len(desired[slug]) && slices.Equal(cur, desired[slug]) {
			continue
		}
		updates[slug] = desiredBytes[slug]
	}
	if len(updates) == 0 {
		return lines, nil
	}
	if err := db.Patch(ctx, projectpolicy.PathRepos(), updates); err != nil {
		return nil, dbErr(lc, err, true)
	}
	return lines, nil
}

// accountsWithout is the elements of have that keep does not hold, in have's
// order: the accounts a narrowing took away, for the line that says so.
func accountsWithout(have, keep []string) []string {
	var out []string
	for _, name := range have {
		if !slices.Contains(keep, name) {
			out = append(out, name)
		}
	}
	return out
}

// narrowedNote is the tail of a failure message that follows the tightening
// phase: nothing at all when it narrowed nothing, and otherwise what it did,
// because that write is not undone by whatever failed afterwards and no other
// line of this command reports it.
func narrowedNote(narrowed []string) string {
	if len(narrowed) == 0 {
		return ""
	}
	return fmt.Sprintf("; the admissions at %s were narrowed first, and a narrowing is not undone by this failure:\n%s",
		projectpolicy.PathRepos(), strings.Join(narrowed, "\n"))
}

// publishFailure is the failure return of runProjectPolicyPublish's loop: the
// error, and narrowedNote of what the tightening phase has already written.
//
// Every failure of an attempt goes through it, not only the two writes' — the
// loop is what makes that necessary. A retry re-enters with narrowedSoFar
// already carrying the attempt that narrowed and drops the lines that attempt
// returned, so if the failure that ends the command does not name them, nothing
// does: the next publish reads the child as already narrowed and says nothing,
// and the operator is left believing a revocation they asked for did not happen
// when it did (or, on the next publish, that it happened when it did not). The
// note is empty on a first attempt that narrowed nothing, so those messages
// read as they always did.
func publishFailure(err error, narrowedSoFar []string) error {
	return fmt.Errorf("%w%s", err, narrowedNote(narrowedSoFar))
}

// repoAdmissions is the accounts block inverted: the slug of each repository an
// account admits, to the sorted names of the accounts admitting it.
//
// The entries are canonical names (projectpolicy.Canonical's spelling,
// validated by the node's
// own rules before this runs), and task.Slug is what turns one into the key the
// repository's own run token carries. A canonical name that task.Slug refuses
// is a name the node's rules already refused, so this is the belt to their
// braces: publishing an admissions child for a slug nothing can look up is
// worse than refusing the publish.
func repoAdmissions(pol *projectpolicy.Policy) (map[string][]string, error) {
	if pol == nil || len(pol.Accounts) == 0 {
		return nil, nil
	}
	out := make(map[string][]string, len(pol.Accounts))
	for _, name := range pol.AccountNames() {
		for _, canonical := range pol.Accounts[name].AllowRepos {
			provider, repo, ok := strings.Cut(canonical, "/")
			if !ok || provider == "" || repo == "" {
				return nil, userErr("the accounts block holds %q for account %s, which is not a canonical repository name (provider/owner/name): this publish cannot say which repository's node that admission belongs to", canonical, name)
			}
			slug, err := task.Slug(provider, repo)
			if err != nil {
				return nil, userErr("the accounts block holds %q for account %s: %v", canonical, name, err)
			}
			out[slug] = append(out[slug], name)
		}
	}
	for slug := range out {
		slices.Sort(out[slug])
	}
	return out, nil
}

// printPolicyDiff shows what the write changes. An absent node is printed in
// full; an existing one prints the lines that differ — the budget and the
// allow-list, every field of every provider entry, and every price override —
// so the operator reads the values about money and data sharing that this
// publish replaces rather than a count of them.
//
// lc is the operator's own config, and the members whose source it is read off
// it rather than passed beside it (the clone_urls, binaries and reports_commit
// blocks are the config's where it states them): the diff says which side an
// entry came from, and the craft members' lines are the same question asked of
// a third family of blocks.
func printPolicyDiff(out io.Writer, old, new *projectpolicy.Policy, sources map[string]string, lc *localcfg.Config) {
	keepCloneURLs, keepBinaries := lc.CloneURLs == nil, lc.Binaries == nil
	keepReportsCommit := lc.ReportsCommit == nil
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
	// The catalog is one line per entry, and a publish whose whole purpose was
	// to carry one that printed no catalog line read as if the catalog was not
	// published at all (#1657): the entries reach a run through this node, so
	// the diff names them the way it names the providers and the prices.
	diffLines(out, policyCatalogLines(old), policyCatalogLines(new))
	// Profiles are the jobs this project deploys and the repositories each is
	// admitted to, so a publish that changes one must say which: a line that
	// only said "profiles: standard" would hide a widened allow_repos list or
	// an upsized job, and one that said nothing at all would hide a profile
	// that stopped existing — which refuses the runs of every workflow that
	// selected it.
	diffLines(out, profileLines(old), profileLines(new))
	// The accounts block is derived from the operator's config, and it is
	// what decides which repositories may use which account's token (D3), so
	// a change to it is a line an operator reading a publish must see: a list
	// that grew is one more repository whose runs hold that token, and one
	// that shrank is a run that will be refused.
	diffLines(out, accountLines(old), accountLines(new))
	// The Bitbucket block and the API-root allow-list are derived from this
	// machine's local config, so they are replaced on every publish — and a
	// replacement is what the operator has to read, because both are decisions
	// about where a credential may go: the block is the token's whole reach,
	// and a root off this list is a run that sends the token elsewhere or is
	// refused. A node another operator's config wrote carries values this
	// machine cannot re-derive, and those lines are the difference between a
	// publish that says what it removed and one that quietly narrowed the
	// installation (the review of #1329).
	diffLines(out, bitbucketLines(old), bitbucketLines(new))
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
	// The clone URLs a project job clones from (plan task 3.3 step 3; the block
	// moved into the local config with SD5, D842): worth a line because an
	// entry decides where a repository's runs clone from, and it says which
	// side it came from — `kept` when this machine's config states no block, so
	// the entry is the node's own and this publish only leaves it standing, and
	// nothing when the block is the config's, because then the line is what
	// this publish wrote. The URL is credential-free by the node's own
	// validation (repourl.CloneURL), so printing it is not printing a secret.
	for _, canonical := range slices.Sorted(maps.Keys(new.CloneURLs)) {
		if keepCloneURLs {
			fmt.Fprintf(out, "  clone_urls %s kept: %s\n", canonical, new.CloneURLs[canonical])
			continue
		}
		fmt.Fprintf(out, "  clone_urls %s: %s\n", canonical, new.CloneURLs[canonical])
	}
	// An entry the node carried that this config does not is a removal the
	// operator reading the diff has to see, for the reason its own line exists:
	// the repository whose runs cloned from a URL of its own goes back to the
	// provider's own clone URL. Only a config that states a block removes
	// anything — one that states none keeps every entry, which is the line
	// above.
	if !keepCloneURLs && old != nil {
		for _, canonical := range slices.Sorted(maps.Keys(old.CloneURLs)) {
			if _, held := new.CloneURLs[canonical]; held {
				continue
			}
			fmt.Fprintf(out, "  clone_urls %s removed (was %s)\n", canonical, old.CloneURLs[canonical])
		}
	}
	// The credentials block is operator-written policy about spending (D152,
	// D153, D142), and the local config is its carrier from this release on
	// (D2607): a file that states it writes the block it states and keeps the
	// node's members for the rest, and a file that says nothing keeps the
	// node's whole — which is a fact the operator reading this diff has to be
	// told, the way the clone URLs' line tells them, because a block that
	// vanished here would silently put a project back on oauth-only (or back on
	// the defaults it never chose). The two readings are printed apart: `kept`
	// is the node's own block standing as it was, and a written one is named as
	// the statement it is. The members printed are the ones the block *writes*;
	// a member it leaves out is its default, and the line does not invent it.
	if new.Credentials != nil {
		if credentialsStated(lc.CredentialCascade) {
			fmt.Fprintf(out, "  credentials: %s\n", credentialsText(new.Credentials))
		} else {
			fmt.Fprintf(out, "  credentials kept: %s\n", credentialsText(new.Credentials))
		}
	}
	// The binaries block is the owner's grant of the paths a run may add a
	// binary at and of the cap it is held to, and the local config is where the
	// operator states it from this release on (AR1). While that file says
	// nothing the node's block stands (buildProjectPolicy), and a fact the
	// operator reading this diff has to be told is worth a line, the clone
	// URLs' `kept` line: the block is unchanged, so no +/- line of the diff
	// above mentions it, and an operator who hand-wrote the block would read
	// that silence as "it is gone". A file that states the block writes it, and
	// then the line would be the operator's own edit read back.
	if keepBinaries && new.Binaries != nil {
		fmt.Fprintf(out, "  binaries kept: %s\n", binariesText(new.Binaries))
	}
	// The designated repository is kept while this machine's config states none
	// (ReportsCommitEffective, D987), and the operator reading this diff has to
	// be told, the binaries block's `kept` line: the member is unchanged, so no
	// +/- line above mentions it, and a person who designated a repository would
	// read that silence as "the project commits its reports nowhere now".
	if keepReportsCommit && new.ReportsCommit != nil {
		fmt.Fprintf(out, "  reports_commit kept: %s\n", reportsCommitText(new))
	}
	// The model grades are the project's own choice of model for the ladder's
	// rungs, and the local config is their carrier from this release on
	// (ModelGrades, D2607), so the line reads the credentials block's two ways:
	// a file that states a grade writes it and keeps the node's for the rest,
	// and a file that says nothing keeps the node's block — a fact the operator
	// reading this diff has to be told, the way the clone URLs' line tells
	// them, since the block is then unchanged and no +/- line above mentions
	// it, and an operator who hand-wrote a grade would read that silence as "it
	// is gone".
	if new.ModelGrades != nil {
		if lc.ModelGrades != nil && !lc.ModelGrades.Empty() {
			fmt.Fprintf(out, "  model_grades: %s\n", modelGradesText(new.ModelGrades))
		} else {
			fmt.Fprintf(out, "  model_grades kept: %s\n", modelGradesText(new.ModelGrades))
		}
	}
	// The daemon's switch and the project's issue adapter are kept too (R1 of
	// docs/orchestrator-graph-estimates, D2701, D2733), and the operator reading
	// this diff has to be told: no key of this machine's config states either,
	// so the member is unchanged and no +/- line above mentions it — and a
	// person who turned the daemon off, or named the repositories whose issues
	// feed the backlog, would read that silence as "it is gone". It is the model
	// grades' line and the clone URLs' `kept` one, for the same reason.
	if new.OrchestratorEnabled != nil {
		fmt.Fprintf(out, "  orchestrator_enabled kept: %v\n", *new.OrchestratorEnabled)
	}
	if new.IssueSync != nil {
		fmt.Fprintf(out, "  issue_sync kept: %s\n", issueSyncText(new.IssueSync))
	}
	// The mobile block is kept too (P6 of docs/mobile-identity-pairing,
	// D2796), and the line is the two above it: no key of this machine's config
	// states it, so a block the node carries is unchanged and no +/- line above
	// mentions it — and a person who turned pairing on, or off, or chose to
	// store nothing in the identity project, would read that silence as "it is
	// gone". A node that carries none is written the default block (O9), and
	// that is the diff's own `mobile` row above: `kept` would be false of a
	// block this publish has just written.
	if old != nil && old.Mobile != nil {
		fmt.Fprintf(out, "  mobile kept: %s\n", mobileText(new.Mobile))
	}
	// The app's crash-report switch is kept too, and the line is the mobile
	// block's one above: no key of this machine's config states it, so a key the
	// node carries is unchanged and no +/- line above mentions it — and a person
	// who turned the reports off by hand (O13) would read that silence as "they
	// are on again". A node that carries no key is written the default on, and
	// that is the diff's own row above: `kept` would be false of a switch this
	// publish has just written.
	if old != nil && old.MobileCrashReports != nil {
		fmt.Fprintf(out, "  %s kept: %v\n", projectpolicy.MobileCrashReportsKey, *new.MobileCrashReports)
	}
	// The crafts, their credentials and their pools are the operator's own
	// blocks (docs/model-crafts §1–3, MC18, D1667), so a publish that writes
	// them names them: a credential by its name and its type, a pool by its
	// members, its strategy and the people it is served to, and a craft by the
	// model and the access it sets. The line is the credentials block's kept
	// line's reason twice over — the three are a spending and an egress grant
	// (F12, D1628), and a line that named only the member would hide the pool
	// that gained a member or the craft that moved onto another credential.
	credsBefore, credsNow := craftCredentialLines(old), craftCredentialLines(new)
	poolsBefore, poolsNow := craftPoolLines(old), craftPoolLines(new)
	craftsBefore, craftsNow := craftLines(old), craftLines(new)
	diffLines(out, credsBefore, credsNow)
	diffLines(out, poolsBefore, poolsNow)
	diffLines(out, craftsBefore, craftsNow)
	// A block this machine's file says nothing about is the node's own and this
	// publish only leaves it standing, which is a fact the operator reading the
	// diff has to be told (the clone URLs' line, the binaries block's and the
	// model grades'): the block is unchanged, so no +/- line above mentions it,
	// and an operator who never wrote the keys would read that silence as "they
	// are gone". A file that states one writes it, and then the +/- lines above
	// are that file's own edit read back.
	if old != nil {
		if len(lc.Credentials) == 0 && len(credsNow) > 0 {
			fmt.Fprintf(out, "  craft_credentials kept: %s\n", craftKeptText("craft_credentials", credsNow))
		}
		if len(lc.Pools) == 0 && len(poolsNow) > 0 {
			fmt.Fprintf(out, "  craft_pools kept: %s\n", craftKeptText("craft_pools", poolsNow))
		}
		if lc.Crafts == (config.Crafts{}) && len(craftsNow) > 0 {
			fmt.Fprintf(out, "  crafts kept: %s\n", craftKeptText("crafts", craftsNow))
		}
	}
	// And a pool the file states that the node cannot carry is neither written
	// nor dropped in silence: the line names it and says what it needs (D2608),
	// asked of the publisher's own function so the line and the omission cannot
	// part company.
	if _, lines := publishedCraftPools(lc, old, new.CraftCredentials); len(lines) > 0 {
		for _, line := range lines {
			fmt.Fprintln(out, line)
		}
	}
	// The floor is the one member a publish keeps when the config is silent
	// about it (buildProjectPolicy), and a kept member that prints nothing is
	// the diff an operator who deleted the key reads as "it is gone": the
	// value is unchanged, so no +/- line of the diff above mentions it. It is
	// printed whenever the node carried a floor and still carries the same
	// one, which is both the silent case and the case of a config that writes
	// what the node already had — in either, the floor the runs read is the
	// floor that was there before this publish, which is the fact to state.
	if old != nil && len(new.ReviewFloor) > 0 && before["review_floor"] == now["review_floor"] {
		fmt.Fprintf(out, "  review_floor kept: %s\n", reviewFloorText(new))
	}
	// And the members that stop older readers (the plan's C1.2 skew
	// section, which hands this warning to the review of this task; the review
	// asked for it): publishing one is a decision with a cost that is paid
	// by every run of the project until the CLI and the runner image are
	// upgraded, and a diff that named the values without naming that cost would
	// be a diff an operator can read and still not know what they are
	// publishing.
	for _, line := range skewWarning(new) {
		fmt.Fprintln(out, line)
	}
	for _, r := range new.AllowRepos {
		if src, ok := sources[r]; ok {
			fmt.Fprintf(out, "  allow_repos %s (%s)\n", r, src)
		} else {
			fmt.Fprintf(out, "  allow_repos %s (kept from the published node)\n", r)
		}
	}
}

// credentialsText is the credentials block as one line, member by member and
// only the members the node writes: the block's absent members are its defaults
// (projectpolicy.Credentials' accessors), and this line is about what the node
// says, not about what a reader would resolve. A written empty block prints as
// the block it is.
func credentialsText(c *projectpolicy.Credentials) string {
	if c == nil {
		return "none"
	}
	var parts []string
	if c.Fallback != "" {
		parts = append(parts, "fallback "+c.Fallback)
	}
	if c.WaitThreshold != "" {
		parts = append(parts, "wait_threshold "+c.WaitThreshold)
	}
	if c.Backoff != nil {
		if c.Backoff.Initial != "" {
			parts = append(parts, "backoff.initial "+c.Backoff.Initial)
		}
		if c.Backoff.Cap != "" {
			parts = append(parts, "backoff.cap "+c.Backoff.Cap)
		}
	}
	if c.StopAfterExhaustionPerWindow != nil {
		parts = append(parts, fmt.Sprintf("stop_after_exhaustion_per_window %v", *c.StopAfterExhaustionPerWindow))
	}
	if c.ConcurrencyPerToken != nil {
		parts = append(parts, fmt.Sprintf("concurrency_per_token %d", *c.ConcurrencyPerToken))
	}
	if len(parts) == 0 {
		return "written with every member absent: the defaults"
	}
	return strings.Join(parts, ", ")
}

// modelGradesText is the model_grades block as one line, grade by grade and
// only the grades the block writes: a grade left out is not a grade at all
// (config.ModelGrades), and the rung that needs one refuses and asks rather
// than falling back to anything this line could name, so there is no default
// spelling to print for the absent one. Each grade renders as
// internal/policy's gradeText renders it — the model, and the credential or
// pool its calls go out on when it names one — so an operator reads one
// wording for a grade wherever one is printed.
//
// A block with no grade in it reached here from a value a caller built rather
// than from a node: decodeModelGrades answers an absent member for a block that
// writes neither grade, so a node never carries one.
func modelGradesText(g *projectpolicy.ModelGrades) string {
	if g == nil {
		return "none"
	}
	var parts []string
	for _, name := range config.GradeNames {
		c := g.At(name)
		if c == nil {
			continue
		}
		line := name + " " + c.Model
		if c.Access != "" {
			line += " on " + c.Access
		}
		parts = append(parts, line)
	}
	if len(parts) == 0 {
		return "written with no grade in it"
	}
	return strings.Join(parts, ", ")
}

// issueSyncText is the issue adapter as one line: the repositories whose open
// issues are ingested into the backlog, and whether an item of the project with
// no spec and no external id opens an issue of its own (D2719, D2733). Only what
// the block writes is printed, the credentials block's line: an absent `github`
// is the absent adapter and not an adapter with no repository, and a mirror that
// is not written is off.
func issueSyncText(b *projectpolicy.IssueSync) string {
	if b == nil || b.GitHub == nil {
		return "none"
	}
	repos := "no repository"
	if len(b.GitHub.Repos) > 0 {
		repos = strings.Join(b.GitHub.Repos, ", ")
	}
	if b.GitHub.Mirror {
		return repos + ", mirror on: an item with no spec and no external id opens an issue"
	}
	return repos
}

// mobileText is the mobile block as one line, the two above it: the two
// switches and the members a project that narrowed them writes. Only what the
// block writes is printed, and each member is printed as the reader answers it
// (projectpolicy's accessors), so the line says what the pairing route will do
// and not what a node happens to spell — an absent `enabled` inside a written
// block is on, and an absent `accepted_providers` is the default four.
//
// The app's crash-report switch is not a member of this block and is not printed
// here: it is a top-level key beside it, with a line of its own
// (projectpolicy.MobileCrashReportsKey in policyLines), because a member inside
// this block would be refused by every reader of the release behind.
func mobileText(b *projectpolicy.Mobile) string {
	if b == nil {
		return "none"
	}
	enabled, sync := "off", "off"
	if b.Enabled == nil || *b.Enabled {
		enabled = "on"
	}
	if b.Sync == nil || *b.Sync {
		sync = "on"
	}
	parts := []string{"pairing " + enabled, "sync " + sync}
	if len(b.AcceptedProviders) > 0 {
		parts = append(parts, "providers "+strings.Join(b.AcceptedProviders, ", "))
	}
	if b.MaxAuthAge != "" {
		parts = append(parts, "auth age "+b.MaxAuthAge)
	}
	if b.StrictNewDevice != nil && *b.StrictNewDevice {
		parts = append(parts, "a new device needs a QR code")
	}
	if len(b.CostlyModes) > 0 {
		modes := make([]string, 0, len(b.CostlyModes))
		for _, m := range b.CostlyModes {
			modes = append(modes, string(m))
		}
		parts = append(parts, "costly modes "+strings.Join(modes, ", "))
	}
	if b.BiometricOnCostly != nil && *b.BiometricOnCostly {
		parts = append(parts, "biometric on a costly start")
	}
	return strings.Join(parts, ", ")
}

// binariesText is the binaries block as one line, member by member and only the
// members the block writes: an absent max_bytes is the shipped bound and not a
// cap of zero (projectpolicy.BinariesMaxBytes), and this line is about what the
// block says. A written empty block prints as the block it is — the spelling
// that takes an admission away on purpose — which is a value a *document* holds
// and not one the database keeps for an empty list (Policy's Binaries field
// comment), so it is reachable from a hand-written node or from a value a test
// hands this function, never from a node a publish wrote.
func binariesText(b *projectpolicy.Binaries) string {
	if b == nil {
		return "none"
	}
	var parts []string
	if len(b.Allow) == 0 {
		parts = append(parts, "allow written empty: nothing admitted")
	} else {
		parts = append(parts, "allow "+strings.Join(b.Allow, ", "))
	}
	if b.MaxBytes != 0 {
		parts = append(parts, fmt.Sprintf("max_bytes %d", b.MaxBytes))
	}
	return strings.Join(parts, ", ")
}

// craftCredentialLines is one line per credential the node carries, for the
// diff: a credential is a name and a type, so the entry's whole change is the
// type beside it.
func craftCredentialLines(p *projectpolicy.Policy) map[string]string {
	if p == nil {
		return nil
	}
	out := make(map[string]string, len(p.CraftCredentials))
	for _, c := range p.CraftCredentials {
		out["craft_credentials "+c.Name] = c.Type
	}
	return out
}

// craftPoolLines is one line per pool, and the line carries every member that
// decides who may spend and where code goes: the members in the order the pool
// tries them, the strategy it resolves to, the owner and the users it is served
// to (D1936) — and the migration's marker where the node carries it, because a
// pool served to everyone is not one an operator may read as a pool of one
// person's tokens.
func craftPoolLines(p *projectpolicy.Policy) map[string]string {
	if p == nil {
		return nil
	}
	out := make(map[string]string, len(p.CraftPools))
	for _, pool := range p.CraftPools {
		parts := []string{"members " + strings.Join(pool.Members, ", "), "strategy " + pool.StrategyOrDefault()}
		if pool.Owner != "" {
			parts = append(parts, "owner "+pool.Owner)
		}
		if len(pool.AllowedUsers) > 0 {
			parts = append(parts, "allowed_users "+strings.Join(pool.AllowedUsers, ", "))
		}
		if pool.LegacyOpen {
			parts = append(parts, "legacy_open (served to everyone: the migration's marker)")
		}
		out["craft_pools "+pool.Name] = strings.Join(parts, ", ")
	}
	return out
}

// craftLines is one line per craft the block sets: the model it runs and the
// credential or pool its calls go out on, each named only where the craft sets
// it — the line is what the node says, and a craft that sets neither half is no
// craft the node writes (config.Crafts drops it), so it has no line.
func craftLines(p *projectpolicy.Policy) map[string]string {
	if p == nil || p.Crafts == nil {
		return nil
	}
	out := map[string]string{}
	for _, name := range config.CraftNames {
		c := p.Crafts.Of(name)
		var parts []string
		if c.Model != "" {
			parts = append(parts, "model "+c.Model)
		}
		if c.Access != "" {
			parts = append(parts, "access "+c.Access)
		}
		if len(parts) > 0 {
			out["crafts "+name] = strings.Join(parts, ", ")
		}
	}
	return out
}

// craftAccessLines is the access half of craftLines: one line per craft that
// names the credential or pool its calls go out on, keyed for the member that
// decides whose credential a craft spends rather than for the craft's model,
// which decides what a stage runs and not what it spends.
func craftAccessLines(p *projectpolicy.Policy) map[string]string {
	if p == nil || p.Crafts == nil {
		return nil
	}
	out := map[string]string{}
	for _, name := range config.CraftNames {
		if access := p.Crafts.Of(name).Access; access != "" {
			out["crafts "+name+".access"] = access
		}
	}
	return out
}

// craftKeptText is one of those members' lines as a single line, for the `kept`
// line the diff prints: the name each line is keyed by, without the member's
// prefix, and the value it carries.
func craftKeptText(prefix string, lines map[string]string) string {
	parts := make([]string, 0, len(lines))
	for _, k := range slices.Sorted(maps.Keys(lines)) {
		parts = append(parts, strings.TrimPrefix(k, prefix+" ")+": "+lines[k])
	}
	return strings.Join(parts, "; ")
}

// skewWarning is the lines that tell the operator what publishing these
// members costs every *other* Fugaro in the installation (the plan's C1.2 skew
// section: "the review of this task decides whether `fugaro project policy
// publish` warns before publishing either"; the review asked for the line, and
// the placement it settled on is the one below — the warning goes out with the
// diff, once the write has landed). Each is a member an older reader cannot
// apply, so this is not a version-skew nicety — it is the difference between a
// project that publishes and a project whose every launch, every publish and
// every run stops until the CLI and the runner image are upgraded (or, for the
// floor, one whose runs go on reviewing less than the owner required, which is
// the worse of the two and is why that line says so):
//
//   - a `credentials` member: an older CLI does not know the key, lists it and
//     refuses every launch of the project, cascade or not, and refuses to
//     publish over the node.
//   - `routing.claude.via: cascade`: a new value inside the routing block an
//     older reader knows, refused whole by the older shape check — an older CLI
//     refuses launch and publish, and an older runner refuses the policy it
//     reads, which halts the run with that routing error rather than a skew
//     sentence.
//   - `review_floor`: a member an older reader does not know at all, and the
//     two directions differ. An older CLI lists it in the keys it does not
//     know and refuses every launch and every publish of the project, the way
//     it does a `credentials` block. An older *runner image* lists it, warns
//     once that the key has no effect, and runs the levels its rigor mode and
//     the repository ask for — so the floor is published and does not bind,
//     which is the direction that matters (the plan's L1.2, a launcher's gate
//     on the *resolved* levels, is what refuses that image; it is not in this
//     change, so until it ships the operator upgrades the image himself).
//
// The lines are printed as part of the diff, after the shared node's write
// (printPolicyDiff is called once that write has landed), so they are claims
// about the node as it now is: "before this publish" would be advice about a
// moment that has already passed. What is still ahead is the next launch —
// refused by an older reader, or, for the floor, run by an image that does not
// apply it — so each line ends there.
//
// The line names the requirement and not a version number: this binary has no
// version to name (the release that carries the cascade decides that), and a
// number written here would be a second place to keep in step with it.
func skewWarning(p *projectpolicy.Policy) []string {
	if p == nil {
		return nil
	}
	var out []string
	if p.Credentials != nil {
		out = append(out, "  warning: this node now carries a credentials member, which a Fugaro older than the one publishing it does not know: an older CLI refuses every launch and every publish of this project until it is upgraded — upgrade the CLI (and, for a project whose runs read the node, anything else that reads it) before the next launch")
	}
	if p.Routing != nil && p.Routing.Claude != nil && p.Routing.Claude.ViaValue() == projectpolicy.RouteCascade {
		out = append(out, "  warning: this node now carries routing.claude.via: cascade, which a Fugaro older than the one publishing it refuses whole: an older CLI refuses every launch and every publish of this project, and an older runner halts every run it launches with that routing error — upgrade the CLI and the runner image before the next launch")
	}
	if len(p.ReviewFloor) > 0 {
		out = append(out, "  warning: this node now carries a review_floor member, which a Fugaro older than the one publishing it does not know: an older CLI refuses every launch and every publish of this project, and an older runner image warns that the key has no effect and runs the levels anyway, so the floor does not bind there — upgrade the CLI, and the runner image before the next run that has to be held to the floor")
	}
	return out
}

// accountLines is one line per account of the accounts block, for the diff
// and for `policy show`: the repositories that account's token may be used by
// (D3). A nil policy (no node published) has no accounts.
func accountLines(p *projectpolicy.Policy) map[string]string {
	if p == nil {
		return nil
	}
	out := make(map[string]string, len(p.Accounts))
	for _, name := range p.AccountNames() {
		out["account "+name] = accountText(p.Accounts[name])
	}
	return out
}

// accountText is one account as one line: its repositories, which is the whole
// of what an accounts entry carries.
func accountText(a projectpolicy.Account) string {
	return fmt.Sprintf("allow_repos %s", strings.Join(a.AllowRepos, ", "))
}

// bitbucketLines is the Bitbucket block and the API-root allow-list as lines,
// for the diff and for `policy show` — one function for both, so the command an
// operator audits the node with and the diff a publish prints cannot disagree
// about what the node carries. A policy that carries neither — which is every
// project without a project_secrets.bitbucket-token entry — has no lines, so a
// publish that has nothing to say about them says nothing: a "none" row would
// announce, on every publish, two blocks that are not there.
func bitbucketLines(p *projectpolicy.Policy) map[string]string {
	if p == nil {
		return nil
	}
	out := map[string]string{}
	if bt := p.BitbucketToken; bt != nil {
		out["bitbucket_token.allow_repos"] = strings.Join(bt.AllowRepos, ", ")
		out["bitbucket_token.proxy"] = proxyText(bt.Proxy)
	}
	if len(p.BitbucketAPIRoots) > 0 {
		out["bitbucket_api_roots"] = strings.Join(p.BitbucketAPIRoots, ", ")
	}
	return out
}

// proxyText is the operator's per-installation switch (D493) as one line. Both
// readings are printed, and neither is left out: "the block is here" and "the
// token is forwarded" are two different statements, and a switch that is off
// is what a node with no proxy says without writing the member at all.
func proxyText(proxy bool) string {
	if proxy {
		return "on (the credentials service forwards this token)"
	}
	return "off (the jobs that mount this token hold it)"
}

// profileLines is one line per value of every profile, and one per job it
// maps to, for the diff and for `policy show`. A nil policy (no node
// published) has no profiles.
func profileLines(p *projectpolicy.Policy) map[string]string {
	if p == nil {
		return nil
	}
	out := make(map[string]string, 2*len(p.Profiles))
	for _, name := range p.ProfileNames() {
		pr := p.Profiles[name]
		out["profile "+name] = profileText(pr)
		out["profile "+name+" jobs"] = profileJobsText(name, pr)
	}
	return out
}

// profileText is a profile as one line: everything the job is deployed as, and
// the admission list a run of it is held to. The secrets are their NAMES —
// the node carries no value (TestPolicyCarriesNoSecretValue is the guard) —
// and an absent list is printed as the fallback it is, never as an empty one.
func profileText(pr projectpolicy.Profile) string {
	secrets := "none"
	if len(pr.Secrets) > 0 {
		secrets = strings.Join(pr.Secrets, ", ")
	}
	allow := "the project's allow_repos"
	switch {
	case len(pr.AllowRepos) > 0:
		allow = strings.Join(pr.AllowRepos, ", ")
	case len(pr.Secrets) > 0:
		// Any enrolled repository on the profile's provider can select it
		// with job_profile:, and every run of it mounts these secrets.
		allow += " (warning: every enrolled repository can select this profile and read its secrets; give it its own allow_repos)"
	}
	line := fmt.Sprintf("provider %s, cpu %d, memory %s, timeout %s, secrets %s, allow_repos %s",
		pr.Provider, pr.CPU, pr.Memory, pr.TimeoutClass, secrets, allow)
	// The accounts the profile serves, when it serves any: one job is
	// deployed per (profile, provider, account), so a reader that cannot see
	// this list cannot tell a profile that serves an account from one that
	// serves none — and the accounts are what decides which job a run lands
	// on. A profile that lists none reads exactly as it did before this
	// release (the field is absent from the node).
	if len(pr.ClaudeAccounts) > 0 {
		line += fmt.Sprintf(", claude_accounts %s", strings.Join(pr.ClaudeAccounts, ", "))
	}
	return line
}

// profileJobsText is the jobs a profile maps to. Design §4.1 deploys one job
// and one job service account per (profile, provider, account): the provider
// is the profile's, and the account is the Claude credential the run uses, so
// a profile is as many jobs as the project has accounts. The Cloud Run job
// name itself is Task 3.2's (gcp's own naming), and this prints the key it is
// built from rather than a spelling this command would have to guess.
func profileJobsText(name string, pr projectpolicy.Profile) string {
	return fmt.Sprintf("one job per (profile, provider, account): (%s, %s, <account>)", name, pr.Provider)
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

// policyCatalogLines is one line per catalog entry, for the diff and for `policy
// show` — one function for both, for the reason the provider and profile lines
// are: a node that carries a catalog and a diff that does not print it read as
// a publish that carried none, and a `policy show` that cannot print it is a
// block nobody can audit (#1657). A node with no catalog has no lines.
func policyCatalogLines(p *projectpolicy.Policy) map[string]string {
	if p == nil || p.ImageCatalog == nil {
		return nil
	}
	out := make(map[string]string, len(p.ImageCatalog.Entries))
	for _, e := range p.ImageCatalog.Entries {
		out["image_catalog "+e.Name] = catalogEntryText(e)
	}
	return out
}

// catalogEntryText is one catalog entry as one line: what it provides, whether
// it is the entry a job that names nothing runs, and where its image comes from
// (catalogSourceLine's own spelling, so a listing and this line cannot disagree
// about a source).
func catalogEntryText(e config.Entry) string {
	tags := "none"
	if len(e.Tags) > 0 {
		tags = strings.Join(e.Tags, ", ")
	}
	return fmt.Sprintf("tags %s, default %v, source %s", tags, e.Default, catalogSourceLine(e.Source))
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
//
// The image ceiling is named when the entry sets it, and only then: left out,
// it is not priced at any default — the run keeps the ceiling the model's row
// already carries — so there is no list value to print, and a publish that
// added or removed one would otherwise print a line that read as no change.
func priceText(p projectpolicy.Price) string {
	parts := []string{"input_per_m " + rateText(p.InputPerM), "output_per_m " + rateText(p.OutputPerM),
		"cache_write_5m " + rateText(p.CacheWrite5m), "cache_write_1h " + rateText(p.CacheWrite1h),
		"cache_read " + rateText(p.CacheRead), "web_search_per_1k " + rateText(p.WebSearchPer1k)}
	if p.ImageTokens != nil {
		parts = append(parts, fmt.Sprintf("image_tokens %d", *p.ImageTokens))
	}
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
	out := map[string]string{
		"version":               fmt.Sprintf("%d", p.Version),
		"budget.mode":           p.Budget.Mode,
		"budget.per_run_usd":    usdVal(p.Budget.PerRunUSD),
		"budget.allowed_models": models,
		"budget.max_run_tokens": fmt.Sprintf("%d", p.Budget.MaxRunTokens),
		"allow_repos":           fmt.Sprintf("%v", nonNil(p.AllowRepos)),
		"providers":             strings.Join(slices.Sorted(maps.Keys(p.Providers)), ", "),
		"profiles":              profilesText(p),
		"accounts":              accountsText(p),
		"review_floor":          reviewFloorText(p),
		"reports_commit":        reportsCommitText(p),
	}
	// The mobile block is the one member whose source is neither this machine's
	// config nor the node it replaces: a publish writes the node's own block
	// when there is one and O9's defaults when there is not (MobileForPublish),
	// so the row is the block as written and the diff prints it as the line a
	// publish adds to a node published before the key existed. A row is left
	// out where there is no block at all — a node read before the publish and
	// nothing else — because a line of "none" would read as a block this
	// publish had taken away, which no publish does.
	if p.Mobile != nil {
		out["mobile"] = mobileText(p.Mobile)
	}
	// The app's crash-report switch is a row of its own, beside the block and
	// never inside it (D731, D738): a publish writes the node's value where it
	// carries one and the default on where it does not
	// (projectpolicy.MobileCrashReportsForPublish), so the row is the switch as
	// written — the diff prints it as the key a publish adds to a node published
	// before it existed. A row is left out where the policy carries none, for
	// the block's own reason one line up: "none" would read as a key this
	// publish had taken away, which no publish does.
	if p.MobileCrashReports != nil {
		out[projectpolicy.MobileCrashReportsKey] = fmt.Sprintf("%v", *p.MobileCrashReports)
	}
	return out
}

// reportsCommitText is the designated repository as one line — the repository
// and the folder inside it (D2750, D2751; periodic-reviews R14). It is a row of
// the diff and of `policy show` for the review floor's reason: the member is
// what decides where every report of the project is committed, so a
// designation that appeared, moved or went away is exactly what an operator
// reading the publish has to see, and a node that carries one must not read
// like a node that designates nothing.
//
// The two values are read through the policy's own accessors, so this line and
// the commit run cannot disagree about the spelling of a repository or about
// which folder an absent `dir` means. An absent member prints as the
// designation it is, "none", and not as a folder nobody named.
func reportsCommitText(p *projectpolicy.Policy) string {
	if p == nil || p.ReportsCommit == nil {
		return "none (no report may be committed anywhere)"
	}
	return fmt.Sprintf("%s, folder %s", p.ReportsCommitRepo(), p.ReportsCommitDir())
}

// reviewFloorText is the owner's review-level floor as one line, mode by mode
// (D722, G1). It is a row of the diff and of `policy show` for the reason the
// Bitbucket block is: it is a value this publish decides, and a floor that
// appeared, changed or went away is exactly what an operator reading the diff
// has to see — the member bounds how much review a launcher-written layer may
// turn off, so a diff that did not name it would leave him unable to tell
// whether the floor he published is the floor the runs now read.
func reviewFloorText(p *projectpolicy.Policy) string {
	if len(p.ReviewFloor) == 0 {
		return "none (a run reviews what its rigor mode and the repository ask for)"
	}
	modes := make([]string, 0, len(p.ReviewFloor))
	for _, mode := range slices.Sorted(maps.Keys(p.ReviewFloor)) {
		levels := make([]string, 0, len(p.ReviewFloor[mode]))
		for _, l := range p.ReviewFloor[mode] {
			levels = append(levels, string(l))
		}
		modes = append(modes, fmt.Sprintf("%s: %s", mode, strings.Join(levels, ", ")))
	}
	return strings.Join(modes, "; ")
}

// profilesText is the policy's profile names as one line, for the diff's
// summary row (the per-profile lines below it carry the values).
func profilesText(p *projectpolicy.Policy) string {
	if len(p.Profiles) == 0 {
		return "none (a workflow that selects one is refused at run start)"
	}
	return strings.Join(p.ProfileNames(), ", ")
}

// accountsText is the policy's account names as one line, for the diff's
// summary row (the per-account lines carry the repositories). The empty row
// names BOTH sources of the block, because either one alone may fill it: the
// profiles that name an account, and the repositories whose own
// repos.<owner/name>.claude_accounts does (DeriveAccounts). A row that named
// the profiles only would tell an operator whose per-repository job is the
// whole point of the entry that there is nothing to look for.
func accountsText(p *projectpolicy.Policy) string {
	if len(p.Accounts) == 0 {
		return "none (no profile and no repository entry serves a Claude account)"
	}
	return strings.Join(p.AccountNames(), ", ")
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
			return userErr("%d run(s) are in flight: a run that is mid-flight keeps the policy until it finishes, so --delete is refused while they run; wait for them (fugaro runs ls), then repeat it", inflight)
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

// inFlightRegistry is the registry's own document: one entry per running run,
// keyed by repository then by run ID. It is one read rather than a count and a
// second read beside it, because the two questions asked of it — how many runs
// are running, and which of them the update's note is about — are asked by the
// same reader in the same breath, and the read is the expensive one on an
// installation with many runs.
func inFlightRegistry(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) (map[string]map[string]budget.AgentEntry, error) {
	var agents map[string]map[string]budget.AgentEntry
	if _, err := db.Get(ctx, "agents", &agents); err != nil {
		return nil, dbErr(lc, err, false)
	}
	return agents, nil
}

// runsCount is how many runs one registry document holds.
func runsCount(agents map[string]map[string]budget.AgentEntry) int {
	n := 0
	for _, runs := range agents {
		n += len(runs)
	}
	return n
}

// runsInFlight counts the runs the registry still holds: a run's entry is
// written when it starts and removed when it ends, which is the same source
// `fugaro budget show` lists as running.
func runsInFlight(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) (int, error) {
	agents, err := inFlightRegistry(ctx, db, lc)
	if err != nil {
		return 0, err
	}
	return runsCount(agents), nil
}

// confirmRemovalStrict asks for the project's name before an un-enrollment,
// and takes --yes out of the operator's hands: removing an allowed repository
// stops its runs (a run of a repository the allow-list does not admit is
// refused before it mints a git token), and a run that is in flight keeps the
// policy until it finishes. A flag typed in advance must not be what stops a
// run that is already running, so this asks for the name itself.
func confirmRemovalStrict(cmd *cobra.Command, lc *localcfg.Config, what string) error {
	return confirmTypedStrict(cmd, lc, "typing the project's name is the only thing that confirms this one while a run is in flight; wait for it (fugaro runs ls), then confirm it at your own terminal", what)
}

// confirmRemoval is confirmRemovalStrict where the run's own answer may stand
// in for the typed name: the caller has checked that nothing is in flight. The
// flag that once carried that answer is gone from the door (T11), so what
// reaches here is the field the run itself holds.
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

// showPolicy is the published policy as this document renders it: the node
// spells two blocks' member names the way RTDB requires them escaped
// (projectpolicy.Prices for model IDs with a "." or a "/", projectpolicy.ImageMap
// for repository names, which always carry a "/"), and a consumer of this
// document is reading model IDs and repository names. So both blocks are
// shadowed here by the same maps in the reader's own spelling — the shadows sit
// at a shallower depth and are therefore the ones encoding/json writes.
type showPolicy struct {
	*projectpolicy.Policy
	Prices   map[string]projectpolicy.Price `json:"model_prices,omitempty"`
	ImageMap map[string]string              `json:"image_map,omitempty"`
}

// showPolicyOf is the document's policy, or nil when none is published.
func showPolicyOf(p *projectpolicy.Policy) *showPolicy {
	if p == nil {
		return nil
	}
	return &showPolicy{Policy: p, Prices: p.Prices, ImageMap: p.ImageMap}
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
	// Every name on these two lists is a key of the run registry, which a run
	// wrote: a repository's slug, and — for a key the database does not take as
	// it stands — budget.Key's percent-escapes of it, which leave a bidi
	// override, a C1 control, a zero-width character and the length of the name
	// alone. Both forms of this command print them (the table and the --json
	// document), so both carry the bounded printable form (printableName), and
	// both carry the same value: a table and a document that mirror each other
	// must not disagree. The raw names stay in `running`, which is what
	// admission is asked of and what the pair of lists is computed on.
	runningShown := boundedNames(running)
	notAdmitted := map[string]bool{}
	doc := projectShowDoc{Project: lc.Name, GCPProject: lc.GCPProject, Mode: cfg.Mode, Caps: caps,
		Enrolled: map[string]string{}, Running: runningShown, RunningNotEnrolled: []string{}, EnrolledNotRunning: []string{}}
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
			notAdmitted[r] = true
			doc.RunningNotEnrolled = append(doc.RunningNotEnrolled, printableName(r))
		}
	}
	for _, r := range doc.AllowRepos {
		if entryHasARun(r, running) {
			continue
		}
		doc.EnrolledNotRunning = append(doc.EnrolledNotRunning, r)
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
	for i, r := range running {
		mark := ""
		if notAdmitted[r] {
			mark = notAdmittedNote(r)
		}
		fmt.Fprintf(out, "  %s%s\n", runningShown[i], mark)
	}
	// EnrolledNotRunning is the allow-list's own entries, each one checked to
	// be a pattern the allow-list takes (projectpolicy.ValidPattern, on decode
	// and again on publish): a value there is this program's alphabet, never a
	// run's.
	for _, r := range doc.EnrolledNotRunning {
		fmt.Fprintf(out, "  %s is on the allow-list and has no run in flight here\n", r)
	}
	return nil
}

// entryHasARun reports whether any run in flight is of a repository this one
// allow-list entry admits.
//
// The run registry holds repository names, and an entry is not one: an entry
// that is a pattern (github/acme/*) or the wildcard "*" equals no name at all,
// so a comparison by equality reported every pattern as "on the allow-list and
// has no run in flight here" while the repositories it admits were running.
// The question is admission — MatchRepo, which is what the runner's admission
// check and the launcher's advisory check ask (projectpolicy.AdmitRepo is the
// same matcher over a whole list, and this is one entry of a list).
func entryHasARun(entry string, running []string) bool {
	for _, r := range running {
		if projectpolicy.MatchRepo(entry, r) {
			return true
		}
	}
	return false
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
	// One line per profile, then the jobs it maps to: a reader of this command
	// is reading what the project deploys and who may run on it (§4.1).
	for _, name := range p.ProfileNames() {
		pr := p.Profiles[name]
		fmt.Fprintf(out, "  profile %s: %s\n", name, profileText(pr))
		fmt.Fprintf(out, "  profile %s jobs: %s\n", name, profileJobsText(name, pr))
	}
	// One line per account, then its repositories: what decides which
	// repositories a run of an account's job may hold that account's token
	// (D10, the owner's ruling that this command prints the block).
	for _, name := range p.AccountNames() {
		fmt.Fprintf(out, "  account %s: %s\n", name, accountText(p.Accounts[name]))
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
	// One line per catalog entry, under the entry's name: the catalog is the
	// named images the project's jobs are built from (D1140), and a node that
	// carries one this command does not print is a set of definitions nobody
	// can audit (#1657).
	catalog := policyCatalogLines(p)
	for _, name := range slices.Sorted(maps.Keys(catalog)) {
		fmt.Fprintf(out, "  %s: %s\n", name, catalog[name])
	}
	if p.Routing != nil {
		fmt.Fprintf(out, "  routing: %s\n", routingText(p.Routing))
	}
	// One line for the credentials block, when the node carries one, for the
	// reason the clone URLs get one and one more: this Fugaro reads the block
	// and no command of this release writes any of it (D153 — it is
	// operator-written policy, per project), so this command is the only place
	// an operator sees whether the cascade's fallback, its threshold or D142's
	// two controls are set — on the settings that decide whether the project
	// spends money and whose subscription quota it spends. Printing a block the
	// node does not carry would read as a policy that has one, so an absent
	// member prints as no line.
	if p.Credentials != nil {
		fmt.Fprintf(out, "  credentials: %s\n", credentialsText(p.Credentials))
	}
	// The Bitbucket block and the API-root allow-list, on the credentials rule:
	// a policy that carries neither prints neither. They are the same lines the
	// publish diff prints (bitbucketLines), because they answer the question
	// this command exists to answer for the project's one long-lived forge
	// credential — which repositories it may reach (D473) and whether the
	// credentials service holds it instead of the job (D493) — and a block the
	// node carries that this command does not print is a setting nobody can
	// audit.
	lines := bitbucketLines(p)
	for _, key := range slices.Sorted(maps.Keys(lines)) {
		fmt.Fprintf(out, "  %s: %s\n", key, lines[key])
	}
	// One line per clone URL, under the canonical repository name the runner
	// looks up (plan task 3.3 step 3). Like routing: this Fugaro reads the
	// block and writes none, so an operator has to be able to see what the
	// node carries — a node with an entry must not read like one without, and
	// the entry is where that repository's runs clone from. The URL is
	// credential-free by the node's own validation (repourl.CloneURL), so
	// printing it is not printing a secret.
	for _, canonical := range slices.Sorted(maps.Keys(p.CloneURLs)) {
		fmt.Fprintf(out, "  clone_urls: %s → %s\n", canonical, p.CloneURLs[canonical])
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

// boundedNames is a list of run-written names as this program prints them:
// each one through printableName, in the same order, so a table line and the
// --json document that mirror each other carry the same value. The raw list
// stays the one matching and admission are asked of (AdmitRepo, MatchRepo,
// entryHasARun): a name is compared as the run wrote it, and only what is
// printed is bounded.
func boundedNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, printableName(n))
	}
	return out
}

// printableName is one run-written name as every listing of this package
// prints it: one inert line, bounded, still distinct from every other name,
// and with its whitespace normalised the way safetext.Field normalises it.
//
// Field is the first half of that and not the whole of it. It drops what Strip
// drops — bidi controls, zero-width and other format characters, invalid UTF-8
// — so a name carrying U+2066 and U+2069 between its letters and a plain name
// of the same letters without them print as one and the same line. An operator
// reads that line as the entry of whichever of the two they have in mind, and
// the two are not the same entry: one is admitted by the allow-list and the
// other is refused. A name Field would change is therefore escaped (each of
// those characters as \uXXXX) instead of dropped, so a name reads as what it
// is — the same choice the CLI makes for a policy node's unknown keys
// (unknownKeysText), whose provenance this shares.
//
// The escape set is Field's *drop* set and not pluginwire's escape set, which
// is a review finding. PrintableN escapes the controls and the format
// characters (Cf), and Field drops those plus a handful of invisible ones that
// are not Cf — the variation selectors (U+FE00–U+FE0F and U+E0100–U+E01EF),
// U+034F, the Hangul fillers U+115F and U+1160, U+17B4, U+17B5, U+3164 and
// U+FFA0 — so escaping by pluginwire's rule alone emitted those literally and
// invisibly, and "x<U+FE00>y" printed as "xy": the collision above, for a
// character the guard test's Cf fixture never carried. escapedNameRune asks
// safetext's own predicate (Strip) rather than copying the list, so the two
// cannot come apart.
//
// Two things Field does that PrintableN does not, and that are done here
// rather than inherited:
//
// The bound is on what is printed, and PrintableN's is on what it reads. It
// counts runes of *input* against its argument and writes six characters for
// each escaped one, so PrintableN(s, safetext.FieldMax) is no bound on the
// line: a name of safetext.FieldMax zero-width joiners — which both durable
// sources accept, a bucket object key having no length limit and
// budget.FromFields checking only that a slug is valid UTF-8 and not empty —
// comes out over three thousand runes wide, past even the listings' own line
// bound (seenLineMaxRunes) and enough to push this program's words off the
// screen, which is the whole point of bounding the name. So each rune is
// escaped on its own here and the loop stops the moment the next one would
// cross FieldMax, with Field's own ellipsis — printableNameMax is
// safetext.FieldMax, the same bound Field has, not a multiple of it.
//
// Whitespace is normalised first, by Field's rule: every unicode space — tab,
// newline, NEL and the line and paragraph separators among them — becomes one
// space, runs of them collapse, and the ends are trimmed. PrintableN is
// deliberately not Field here (it keeps a literal tab), and a tab in a listing
// line shifts everything after it out of the column the operator is reading,
// which is the one thing no other run-written value in this program does. The
// reading that costs is a name that differs from another only in whitespace:
// both print alike, and neither is a repository identifier (the alphabet has
// no space in it) nor reaches a command. What is *not* normalised is the
// characters the escape set covers, which are escaped — two names that differ
// in one of those still read as two.
//
// What is escaped is inert either way: Printable escapes every control and
// format character, so the result is still one line no terminal acts on. It
// reaches no command: the advisory this package builds is enrollableArg's,
// which returns only a value over the repository alphabet.
//
// The one reading it does not separate is a name that literally spells an
// escape ("x⁦y" typed as nine ASCII characters), which Field leaves
// alone and so prints exactly as the escaped form of a name carrying the
// character. Real repository identifiers cannot carry a backslash at all, and
// a name that spells one is not a repository identifier either, so neither
// reaches a command, and telling them apart would cost the ordinary name its
// plain form.
func printableName(s string) string {
	if f := safetext.Field(s); f == s {
		return f
	}
	var b strings.Builder
	used := 0
	for _, r := range normalisedSpaces(s) {
		e := escapedNameRune(r)
		n := utf8.RuneCountInString(e)
		if used+n > safetext.FieldMax-1 {
			b.WriteRune('…')
			break
		}
		b.WriteString(e)
		used += n
	}
	return b.String()
}

// escapedNameRune is one rune of a run-written name as printableName prints it:
// the rune as it stands, or its \uXXXX escape when printing it literally would
// let two different names read as one. Which characters those are, and why the
// set is Field's drop set rather than pluginwire's escape set, is
// printableName's doc.
//
// safetext.Strip is the drop half of Field, so Strip(string(r)) != string(r)
// is "Field would have dropped this rune" — asked rather than copied, so a
// character added to that set later is escaped here without anyone remembering
// to. It is asked per rune because Field's whitespace rule cannot be: Fields
// and its Join keep a value's interior spaces but drop a lone one, so Field(" ")
// is "" and asking Field would escape the ordinary names that hold a space.
func escapedNameRune(r rune) string {
	if e := pluginwire.PrintableN(string(r), 1); e != string(r) {
		// pluginwire's rule already escapes it — the controls, the format
		// characters — and its spelling of the escape is the one to keep.
		return e
	}
	if safetext.Strip(string(r)) == string(r) {
		return string(r)
	}
	return fmt.Sprintf("\\u%04x", r)
}

// printableNameMax is the most runes printableName returns. Its bound is on
// what is printed, so it is safetext.FieldMax itself: the ellipsis stands in
// for the rune it replaced.
const printableNameMax = safetext.FieldMax

// normalisedSpaces is s with Field's whitespace rule applied and nothing else.
// Field does the same and then drops the characters the escape set covers (the
// format characters among them) and the invalid UTF-8 as well; those are kept
// here, because printableName escapes them rather than dropping them, so that
// two names that differ in one still read as two.
func normalisedSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			// Leading whitespace is dropped, interior runs are collapsed to
			// one space, trailing whitespace never flushes: strings.Fields
			// and its Join, which is what Field does with the same rule.
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// allowSources says where each entry of the published allow-list came from,
// for `project show`. It is the same seed the publisher would compute, so an
// entry that is not in it was added on purpose (`project repos add`) or is a
// pattern.
func allowSources(ctx context.Context, lc *localcfg.Config) (map[string]string, error) {
	return allowSourcesOf(ctx, lc, false)
}

// unknownKeysText renders a node's unknown top-level keys for a message, for
// either node a refusal names them of: the policy node and the workspace record
// (projectpolicy.Policy.UnknownKeys and Workspace.UnknownKeys are what the
// tolerant top-level decode could not place). They are raw JSON member names of
// a hand-writable object, and a key carrying a bidi override, a zero-width
// joiner or a line/paragraph separator is a legal JSON name that
// unicode.IsControl does not catch (checkTopLevelNames refuses duplicates and
// case-variants of the keys this Fugaro knows, never their shape). Printed raw,
// such a name can rewrite the line it is printed in — the terminal's reading of
// everything after it is the file's to choose — so every name goes through
// pluginwire.Printable, the same escaped form config.ValidateClaudeAccounts and
// providerConventionRefusal print a name of the same provenance with.
func unknownKeysText(keys []string) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = pluginwire.Printable(k)
	}
	return strings.Join(out, ", ")
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
	return userErr("%s lists %s, which this Fugaro does not know: a key nobody reads is a policy that never takes effect; fix the policy, or upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then run 'fugaro update'",
		projectpolicy.PathShared(), unknownKeysText(p.UnknownKeys))
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
	return "  (" + notAdmittedSentence(r) + ")"
}

// notAdmittedSentence is the sentence notAdmittedNote wraps, unwrapped for the
// listings that print it inside a parenthetical of their own (`project repos
// ls`'s seen list names the sources first). It is one sentence for one state,
// so the two listings cannot word the same refusal differently.
func notAdmittedSentence(r string) string {
	// The remedy is a command an operator is invited to run, so it is built
	// only from a repository identifier — never from a pattern, and never from
	// a name a run wrote that is not one. `ValidPattern` is the wrong test for
	// that: it is the allow-list's own test, and it accepts "*" (the entry that
	// admits every repository) and every wildcard pattern besides, so a run
	// that writes the bucket prefix `runs/*/` or a spendDaily slug of
	// `github/*/*` made this program print
	// 'fugaro project repos add *' — the widest enrollment there is, advised by
	// the program itself out of data the run chose. A name that is not an
	// identifier gets no command at all, whatever its shape.
	if arg, ok := enrollableArg(r); ok {
		return "not admitted: its next run is refused until 'fugaro project repos add " + arg + "'"
	}
	reason := notARepoName
	if looksLikeASlug(r) {
		reason = slugIsAHash
	}
	return "not admitted: its next run is refused until the allow-list admits it — " + reason + ", so " + slugNameIt
}

// enrollableArg is name as the one argument `fugaro project repos add` takes
// for it, when name is a repository identifier and nothing else: a
// provider/owner/name path (a repository may nest, so any depth above two) over
// the repository alphabet [A-Za-z0-9._-], whose first segment is a provider
// kind this Fugaro knows (config.Providers — the set projectpolicy.ValidPattern
// requires, and the one `repos add` validates the argument with), with no
// empty, "." or ".." segment and no segment opening with "-" (a shell
// word that opens with a dash reads as an option). A "*" — the wildcard of a
// pattern, and the one character that makes the name admit more than itself —
// is not in the alphabet, and neither is a space, so a pattern and a name a run
// padded are both refused.
//
// The provider is the KIND and not its shape: task.ValidProvider reads
// ^[a-z][a-z0-9-]*$, which admits any lower-case word, while `repos add`
// validates the argument it is given with projectpolicy.ValidPattern, whose
// first segment must be a member of config.Providers. A slug field of
// `prod/acme/app` — a name one hand-written spendDaily document can carry — is
// provider-shaped, so a shape-only gate printed
// 'fugaro project repos add prod/acme/app', and that command exits with
// `"prod" is not a git provider kind (github, bitbucket)`: an instruction the
// program hands out that the program itself refuses. What is printed is meant
// to work, so the set is the one the command applies. A provider kind spelled
// in another case is refused by both tests anyway (config.Providers is
// lower-case, and so is task.ValidProvider).
//
// The value comes back quoted as one shell word
// (shellword.Quote: unchanged when it is already safe, single-quoted
// otherwise), because what is printed is meant to be pasted into a shell.
//
// The bare owner/name form `repos add` also takes is deliberately not offered:
// it resolves its provider through this machine's config (canonicalRepoArg),
// and these listings reach this sentence exactly for the names the config does
// not know. The canonical form is the one that stands on its own.
//
// The name must also be one `repos add` accepts after its own canonicalising:
// a last segment of exactly ".git" is not, because task.CanonicalRepo strips a
// trailing ".git" (after lower-casing) and is then left with an empty one, so
// 'github/acme/.git' is printed by the shape rules above and refused by the
// command. "github/acme/app.git" IS accepted (the command stores
// "github/acme/app"), so the rule is about the last segment and not the
// suffix.
//
// The name is bounded too, at projectpolicy.MaxPatternLen bytes (the alphabet
// is ASCII, so bytes are characters) — the bound `repos add` applies to the
// argument through ValidPattern, and not a bound of this package's own. It is
// the tightest of the two that could be used here: safetext.FieldMax is 512,
// so bounding at that let a hand-written 256-byte slug field be handed
// 'fugaro project repos add github/acme/<300 bytes>', which exits with "the
// pattern is N characters; at most 255 are allowed" — the same defect as the
// provider-kind one above, and the property the test is named for. Every other
// name these listings print is held to that bound as well, and without any
// bound a run that wrote a long enough identifier-shaped slug made the advisory
// line, and the command in it, as long as it chose. A longer name gets no
// command, the same answer as any other name that is not a repository
// identifier.
func enrollableArg(name string) (string, bool) {
	if len(name) > projectpolicy.MaxPatternLen {
		return "", false
	}
	segs := strings.Split(name, "/")
	if len(segs) < 3 || !slices.Contains(config.Providers, segs[0]) {
		return "", false
	}
	for _, seg := range segs {
		if !repoIdentSegment(seg) {
			return "", false
		}
	}
	// A name whose LAST segment is exactly ".git" is refused by `repos add`,
	// and this gate has to refuse it too or it prints a command the program
	// itself answers with `"github/acme/.git" is not a repository: the
	// repository must look like owner/name`. task.CanonicalRepo strips a
	// trailing ".git" — after ASCII-lowering, so ".GIT" goes the same way —
	// and then requires every segment to be non-empty: "github/acme/.git"
	// becomes "github/acme/" and fails. Only the last segment, and only the
	// whole segment: "github/acme/app.git" is a name the command accepts (it
	// stores "github/acme/app"), and a ".git" segment in the middle is left
	// alone.
	if strings.EqualFold(segs[len(segs)-1], ".git") {
		return "", false
	}
	return shellword.Quote(name), true
}

// repoIdentSegment reports whether seg is one segment of a repository
// identifier: non-empty, over [A-Za-z0-9._-], not "." or ".." (which name
// directories, not repositories) and not opening with "-".
func repoIdentSegment(seg string) bool {
	if seg == "" || seg == "." || seg == ".." || seg[0] == '-' {
		return false
	}
	for i := 0; i < len(seg); i++ {
		switch c := seg[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// looksLikeASlug reports whether s is over the slug's own alphabet — lower-case
// letters, digits and "-", no separator. That is the shape task.Slug mints and
// runstore lists (its slugRE), and the shape of a name that cannot be turned
// back into a repository: what the listing has to say about one is that it is a
// slug, not a command.
func looksLikeASlug(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}

// slugIsAHash and slugNameIt are the two halves of what a name that is a slug
// means for a reader: it is not invertible, and the remedy is to name the
// repository. A listing that prints a slug (the run registry's keys, the
// bucket's runs/<slug>/ prefixes) hands the operator neither a provider nor a
// name, so it says both, in the same words, wherever it prints one.
const (
	slugIsAHash = "this is a slug, a hash of the repository's name, so its provider is not recorded"
	slugNameIt  = "name it: 'fugaro project repos add provider/owner/name'"
	// notARepoName is the other reason a name gets no command: it is not a
	// repository identifier at all — a pattern a run wrote (github/*/*), the
	// allow-list's own "*", or a name with something else in it a repository
	// cannot hold. Saying "this is a slug" of one of those would be false
	// (nobody hashed it), and saying nothing would leave a reader wondering
	// why the neighbouring entries are given a remedy and this one is not.
	notARepoName = "this is not a repository name (a repository is provider/owner/name, with no wildcard)"
)

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
