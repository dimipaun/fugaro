// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is the pair launch (plan task A3.3): the planner the released
// CLI's launch path asks before it writes a first run's task (A3.0's
// RegisterLaunchPlanner), and the whole of what decides whether that launch
// gets a shadow challenger beside it. What the launcher does with the answer —
// the room for two runs, the pair's budget pre-check, D573's margin, the task
// codec, the batch label, the second start that may never fail the first — is
// the core's, and this file only answers the pair.

// ReasonDailyCount is why the launcher samples nothing when the day has
// already been sampled for as many pairs as the project allows (D557, D566),
// and when today's count cannot be read at all: a pair is never sampled on a
// figure the launcher does not have. It is one word for one decision, like
// Admit's reasons, because the skip line prints it and `fugaro run --json`
// carries it.
const ReasonDailyCount = "daily-count"

// Plan is the Subscription Edition's launch planner: it answers the pair a
// launch is sampled for, or nil when this launch is not one (D558's
// eligibility, or the rate's own roll), or a pair whose Skip says in one
// closed word why no challenger is launched beside the champion.
//
// The champion always goes on, marked or not: every answer here but a pair is
// the developer's run as it was, and the second arm is a decision this build
// makes on top of it (D554). So the ordering below is the cheap questions
// first and never a failure of the launch — an error is only for a read whose
// absence leaves the decision unfounded, and the core prints it as one line
// beside a champion that still launches.
//
// U5 (D571): both arms are launched at one commit, and it is the champion's
// own. The core starts the challenger from the champion's launched task, and
// the only two fields it takes from the task answered here are the run id and
// the ab block (launchChallenger), so the arm inherits the launcher's
// resolved ref — the commit this machine cut the champion from — along with
// the workflow, the recipe, the mode, the tier and the --path set. Nothing
// here resolves a ref of its own, which is what makes the two arms the same
// run twice rather than two runs that happen to look alike.
func Plan(ctx context.Context, run fcli.PlannedRun) (*fcli.Pair, error) {
	spec, cfg := run.Spec, run.Config
	if spec == nil || cfg == nil {
		return nil, nil
	}
	layer := abBlock(cfg)
	if !Sampled(spec.RunID, abRate(cfg, layer)) {
		// The rate's own roll, or a project that samples nothing: most
		// launches of an installation with a challenger configured are this,
		// and neither has anything to say (D550, D557).
		return nil, nil
	}
	if !Eligible(*spec, recipeMode(spec), launchedMode(cfg, spec), LaunchFlags{ChosenRunID: run.Chosen}) {
		return nil, nil
	}
	// The owner's challenger list, judged for this repository the way the run
	// itself judges its own model (D556, A3.2). eff is the launch's own merge
	// (PlannedRun.Policy), which is what the runner re-reads at bootstrap.
	judged := *cfg
	judged.Agent = launchAgent(*cfg)
	model, ok, why := Admit(judged, *layer, run.Repo, run.Policy, run.Providers, run.Prices, run.ServiceHeldKey)
	if !ok {
		return &fcli.Pair{Skip: why}, nil
	}
	if run.Cloud == nil {
		return nil, errors.New("the launch planner was asked about a run with no cloud environment to count today's pairs in")
	}
	n, truncated, err := challengersToday(ctx, run)
	if err != nil {
		return nil, err
	}
	if truncated || !UnderDailyCount(n, layer.MaxPairs()) {
		return &fcli.Pair{Skip: ReasonDailyCount}, nil
	}
	pairID := "pair-" + spec.RunID
	challenger, err := challengerSpec(spec, pairID, model)
	if err != nil {
		return nil, err
	}
	return &fcli.Pair{
		Champion:   task.AB{PairID: pairID, Arm: task.ABArmChampion},
		Challenger: challenger,
	}, nil
}

// launchAgent is the agent a launch's own coder is judged on: the config's
// agent block with the project layer's defaults.agent under it, which is the
// merge the run itself makes at bootstrap (config.Resolve lays the layer's
// defaults down first and the file over them).
//
// PlannedRun.Config is the launch's own read of that merge (launchLevelsConfig):
// the file the run will check out resolved over the layer when this machine
// holds a checkout of the repository, and otherwise — a launch from outside one,
// which is what a --repo from anywhere is — the layer the task carries, a
// configuration whose agent block is the layer's defaults alone. The two are
// the same answer for the two fields an admission reads, and reading cfg.Agent
// alone is what makes them differ: an empty auth refuses every provider
// challenger as needs-api-key, so a launch from outside a checkout would never
// sample, and an empty coder would hide a challenger that is the champion's own
// model (ReasonChampionModel). Every other field of the block is the champion's
// to carry and no check here reads it.
func launchAgent(cfg config.Config) config.Agent {
	a := cfg.Agent
	if cfg.Layer == nil {
		return a
	}
	d := cfg.Layer.Defaults.Agent
	if a.Auth == "" {
		a.Auth = d.Auth
	}
	if a.Models.Coder == "" {
		a.Models.Coder = d.Models.Coder
	}
	return a
}

// abBlock is the ab: block a launch is sampled on: the project layer's, which
// is the only one that may name a challenger or a daily count (D556), and
// otherwise the repository's own block, whose rate alone a repository may
// write — the opt-out (A1.2). It is never nil by the time a rate has been read
// from one of the two: a rate above 0 comes from a block.
func abBlock(cfg *config.Config) *config.AB {
	if cfg.Layer != nil && cfg.Layer.AB != nil {
		return cfg.Layer.AB
	}
	return cfg.AB
}

// abRate is the rate the launch samples at: the project layer's, lowered by
// the repository's own opt-out when it writes one (D556, D557). The merge is
// internal/policy's, so the tightening rule lives where every other layer
// boundary does, and it is built here rather than read off PlannedRun.Policy
// because no other builder fills a policy layer's A/B rate (policy.Layer.ABRate
// is written by this launcher and by nothing else).
func abRate(cfg *config.Config, layer *config.AB) float64 {
	var ceiling, repo policy.Layer
	if layer != nil {
		ceiling.ABRate = &layer.Rate
	}
	if cfg.AB != nil {
		repo.ABRate = &cfg.AB.Rate
	}
	eff := policy.Merge(ceiling, repo)
	if eff.ABRate == nil {
		return 0
	}
	return *eff.ABRate
}

// recipeMode is the mode of the recipe the launch resolved, as A3.1's
// Eligible reads it: the text the launcher embedded in the task. A repository
// recipe travels as its name alone (the runner reads the file at the ref), so
// its mode cannot be read here and comes out as the empty one that fails
// closed — a launch whose recipe this build cannot read is not one it spends a
// second run on.
func recipeMode(spec *task.Spec) string {
	if spec.Recipe == nil || spec.Recipe.YAML == "" {
		return ""
	}
	r, ps := recipe.Parse([]byte(spec.Recipe.YAML))
	if len(ps) > 0 {
		return ""
	}
	return r.Mode
}

// launchedMode is the rigor mode the launch resolved, with the same
// expression the launcher's own policy merge reads it by (chainLimitPolicyOf):
// the project layer's rigor over the repository's, with the launch's --path
// set applied.
func launchedMode(cfg *config.Config, spec *task.Spec) rigor.Mode {
	return rigor.Resolve(cfg.RigorConfig(), rigor.Request{Mode: rigor.Mode(spec.Rigor), Paths: spec.Paths}).Mode
}

// challengersToday counts the challenger arms this repository's day already
// holds, under the project's own prefix of the runs bucket, and reports
// whether the read was truncated by its bound: a window that held more runs
// than the read returns is not a count this build can show is under the cap,
// and sampling nothing is the direction that spends nothing.
//
// The count is a read and not a reservation, which is what makes the cap soft
// (UnderDailyCount): two launches at the same moment both read the same number
// and both sample, and the day can overshoot by however many are in flight.
// The per-run cap and the daily cap in the budget database are what bound the
// money either way (D566).
func challengersToday(ctx context.Context, run fcli.PlannedRun) (int, bool, error) {
	now := time.Now().UTC()
	records, _, truncated, err := run.Cloud.ReadRunRecords(ctx,
		[]string{run.Slug}, now.Truncate(24*time.Hour), time.Time{}, fcli.UserStatsMaxRuns)
	if err != nil {
		return 0, false, fmt.Errorf("today's A/B pairs could not be counted: %w", err)
	}
	n := 0
	for _, rec := range records {
		if r := rec.Record(); r != nil && r.AB != nil && r.AB.Arm == task.ABArmChallenger {
			n++
		}
	}
	return n, truncated, nil
}

// challengerSpec is the challenger's task: the champion's own, with the arm's
// two differences (D555). The launcher starts the arm from the champion's
// launched task and reads this copy for its run id and its ab block alone, so
// what it is here is the arm as a task — the same fields throughout, and the
// three that describe a challenger filled in: it never publishes (D554), it
// runs the admitted model on the coder role, and that model is the owner's
// (D556).
func challengerSpec(spec *task.Spec, pairID, model string) (*task.Spec, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("the challenger's task could not be copied: %w", err)
	}
	arm := &task.Spec{}
	if err := json.Unmarshal(data, arm); err != nil {
		return nil, fmt.Errorf("the challenger's task could not be copied: %w", err)
	}
	id, err := freshRunID(spec.RunID)
	if err != nil {
		return nil, err
	}
	arm.RunID = id
	arm.AB = &task.AB{
		PairID: pairID, Arm: task.ABArmChallenger, Publish: task.ABPublishNone,
		Challenger: model, ChallengerSource: task.ABChallengerOwner,
	}
	return arm, nil
}

// freshRunID mints the challenger's run id, which is a run's own: the launcher
// refuses a pair whose challenger carries the champion's id (the two arms
// would be one run), and an id is a timestamp and four random digits, so two
// mints in the same second can collide.
func freshRunID(champion string) (string, error) {
	for range 4 {
		id, err := task.NewRunID(time.Now(), rand.Reader)
		if err != nil {
			return "", fmt.Errorf("the challenger's run id: %w", err)
		}
		if id != champion {
			return id, nil
		}
	}
	return "", errors.New("the challenger's run id collided with the champion's own on every draw")
}
