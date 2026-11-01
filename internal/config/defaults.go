package config

import (
	"slices"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// BaseKind is the one Fugaro base image's kind (design base-image.md): what a
// workflow without base: builds on, and its key in the local config's
// base_images.
const BaseKind = "base"

// MigrationDoc is the guide every refusal of a removed base setting names
// (design/base-image.md §10).
const MigrationDoc = "docs/base-image-migration.md"

// Bases are the base kinds: the one Fugaro base (the legacy kinds were
// removed in 0.7.0).
var Bases = []string{BaseKind}

// legacyBaseMessage is the "base: removed in 0.7.0" refusal, shared by
// validate.go's workflows.*.base check and layer.go's profiles.*.base
// check: TestBadBaseMessageConsistency pins that both give byte-for-byte
// the same message, which a shared constant, not two independent literals,
// is what actually guarantees. %s is MigrationDoc. It does not repeat
// "base:" at the front: both callers add() this at a path already ending
// in ".base" (such as "workflows.web.base"), and Problem.String() joins
// path and message with ": ", so a leading "base:" here used to print as
// "workflows.web.base: base: was removed in 0.7.0...", the word doubled.
const legacyBaseMessage = "was removed in 0.7.0: every workflow builds on the one Fugaro base image. Delete this line and declare the workflow's runtimes in mise.toml (%s)"

// The loop counts the budget floor is computed at, deliberately flat: a run's
// rounds come from the mode that resolves for it (internal/rigor's
// DefaultRounds) and from the layers over it, and no single default stands for
// them any more.
//
// A floor computed from a mode's own row would move with the mode whose budget
// fraction it floors, and the two rows below the value here — POC's and
// Hotfix's, one round each, whose fraction is the tightest of all — would lower
// it: the floor exists so a fraction cannot put a run under one stage's lease
// (RigorBudgetFloorUSD), so the number under it must not come from the run it
// protects. These are the counts every mode ran at before the per-mode ruling,
// which is the conservative direction: no installation's floor moves, and none
// can start fitting a cap it did not fit before.
const (
	rigorBudgetFloorFirstRounds  = 1
	rigorBudgetFloorReviewRounds = 2
	// DefaultAgentAuth is agent.auth's default: a fugaro.yaml that names none
	// authenticates with a Vertex key. applyDefaults applies it, and
	// AgentOf is the other reader of the rule.
	DefaultAgentAuth = "vertex"
	// DefaultMaxBudgetUSD is agent.max_budget_usd's default: a repository
	// that names none starts under the $25 ceiling. applyDefaults applies it
	// and the settings registry's row for the key reads this constant rather
	// than restating the number.
	DefaultMaxBudgetUSD = 25
	// DefaultCPU and DefaultMemory are a workflow's resources when it names
	// none. applyDefaults applies them and the settings registry's rows read
	// the same two constants.
	DefaultCPU    = 4
	DefaultMemory = "8Gi"
	// DefaultUpdateCheck is update_check's default: the daily update check
	// and its stderr nag run unless the local config turns them off. The
	// local loader reads it (Config.UpdateCheckEnabled) and the settings
	// registry's row for the key cites it.
	DefaultUpdateCheck = true
	// AuthCascade is agent.auth's cascade value (docs/credentials-service,
	// D133): the run's calls are made with whatever credential the project's
	// credentials cascade settled on, through the gateway, instead of with a
	// credential of the job's own. It is a value of the one list below, so
	// every reader that places an auth places this one the same way.
	AuthCascade = "cascade"
)

// DefaultReports are the report globs a workflow collects when it names none,
// the files a run's artifacts step reads back (artifact-runs.md). It is the
// settings registry's row for workflows.*.commands.reports that reads it, so
// the DEFAULT column and a real run cannot part company.
var DefaultReports = []string{"**/junit*.xml", "**/build/test-results/**/*.xml"}

// RigorBudgetStageFloorUSD is the least model spend one stage can be granted:
// the budget gateway's own lease floor (internal/budget's minLease, $0.25).
// Under it the gateway grants a stage nothing, and a run that cannot be
// granted dies on the cap — the most expensive outcome there is, because a
// run that dies on the cap produces nothing (docs/rigor-modes D16).
// TestRigorBudgetStageFloorIsTheLeaseFloor reads the two apart.
const RigorBudgetStageFloorUSD = 0.25

// RigorBudgetFractions are the per-run budget defaults the modes contribute,
// as fractions of the deployed cap (D16, plan T6.1). A fraction rather than a
// dollar figure is what makes "a mode lowers the ceiling and never raises it"
// structural: the effective cap is a fraction of the ceiling, and a fraction
// of a value is never above it (F15).
//
// Critical is absent on purpose: the whole deployed cap is what a run of the
// strictest mode gets, which is what every run got before this table existed,
// and a default below it would change a Critical run's outcome — the one
// thing this work may not do (the plan's §5 and §7.1).
//
// The order is the owner's (D16): POC < Startup < Pro < Critical, with Hotfix
// tightest of all, because a hotfix is the run with the least room to spend.
var RigorBudgetFractions = map[rigor.Mode]float64{
	rigor.Hotfix:  0.4,
	rigor.POC:     0.55,
	rigor.Startup: 0.7,
	rigor.Pro:     0.85,
}

// RigorBudgetFloorUSD is the least a mode's per-run default may be where the
// deployed cap can carry it: one stage's floor for each model stage the
// smallest run the built-in recipe catalog ships can start, which is the smoke
// check a run has to be able to finish. The arithmetic is recipe.StageBound's,
// the same bound the cost preview quotes, and RigorBudgetCap is where it is
// applied — a floor that is only asserted is a floor a mode's fraction can go
// under (D16, plan T6.1).
//
// A deployed cap below the floor is the one case it cannot answer for, and it
// is answered by saying so rather than by enforcing it: the ceiling is the
// ceiling, so a run there gets the whole cap — a mode raising it would be a
// mode raising the owner's ceiling (F15) — and a deployment that low is under
// one stage's lease whatever mode a run is at
// (TestRigorBudgetFloorCannotLiftATinyCap).
//
// It is a function and not a value because computing it parses the whole
// recipe catalog (smokeStages), and this package is imported by every CLI
// command: a package-level initializer made `fugaro version` parse the catalog
// too. The read is memoized, so the catalog is parsed once, on the first run
// that asks for a cap.
func RigorBudgetFloorUSD() float64 { return rigorBudgetFloor() }

// rigorBudgetFloor memoizes the floor above.
var rigorBudgetFloor = sync.OnceValue(func() float64 {
	return RigorBudgetStageFloorUSD * float64(smokeStages())
})

// smokeStages is the model stages the smallest run the built-in recipe
// catalog ships can start, at the knobs' defaults: the smallest
// recipe.StageBound over the catalog.
func smokeStages() int {
	smallest := 0
	for _, name := range recipe.CatalogNames() {
		text, ok := recipe.CatalogText(name)
		if !ok {
			continue
		}
		r, ps := recipe.Parse(text)
		if len(ps) > 0 {
			continue
		}
		if n := recipe.StageBound(r, rigorBudgetFloorFirstRounds, rigorBudgetFloorReviewRounds); smallest == 0 || n < smallest {
			smallest = n
		}
	}
	return smallest
}

// RigorBudgetFraction is the per-run budget default m contributes as a
// fraction of the deployed cap. A mode the table does not hold — Critical, a
// caller that resolved none, a name outside the vocabulary — contributes none
// and so gets the whole cap: nothing here may tighten a run on the strength
// of a value it could not place.
func RigorBudgetFraction(m rigor.Mode) float64 {
	if f, ok := RigorBudgetFractions[m]; ok {
		return f
	}
	return 1
}

// RigorBudgetCap is the per-run cap a run of mode m gets from the deployed
// cap: the deployed cap scaled by RigorBudgetFraction and floored at
// RigorBudgetFloorUSD. It is a clamp in both directions that matter — the
// ceiling is the ceiling, and a mode lowers it and never raises it (F15, D16),
// while a mode's fraction must not put a run under one stage's lease floor,
// where the gateway grants the stage nothing and the run dies on the cap
// having produced nothing (D16, T6.1). The floor is bounded by the deployed
// cap, so it never lifts a cap: a deployment whose whole cap is under the
// floor gives a run all of it, which is the best that ceiling can do.
//
// It never answers with a cap that rounds to nothing, which reads as no cap at
// all (the trap SpendFromEnv guards against at the other end).
func RigorBudgetCap(m rigor.Mode, deployed pricing.Micros) pricing.Micros {
	if deployed <= 0 {
		return deployed
	}
	f := RigorBudgetFraction(m)
	if f >= 1 {
		return deployed
	}
	scaled, err := pricing.FromUSD(deployed.USD() * f)
	if err != nil || scaled <= 0 {
		return deployed
	}
	if floor, err := pricing.FromUSD(RigorBudgetFloorUSD()); err == nil {
		scaled = max(scaled, floor)
	}
	return min(scaled, deployed)
}

// AgentAuths are the values agent.auth may take: validate's check and every
// reader that has to tell "an auth this Fugaro knows" from an arbitrary string
// read one list, so a value the validator accepts is never one a reader
// refuses as unknown. The values are spelled out here rather than named by
// their constants because this literal is the list a reader reads: the desktop
// console's scrub vocabulary is compared with it by name
// (TestDocsDesktopConsoleScrubVocabularyMatchesTheGoSource, which parses
// exactly this assignment), and TestValidateAcceptsCascade pins that every
// constant above — AuthCascade today — is one of them.
var AgentAuths = []string{"vertex", "api-key", "oauth", "cascade"}

// KnownAgentAuth reports whether auth is one of AgentAuths.
//
// The distinction matters where the value did not come through validation:
// config.AgentOf reads a repository's own bytes with the strict decode and
// validates nothing (it exists so a launcher can judge a launch on the
// fugaro.yaml at the ref the run checks out, without resolving that run's whole
// config), so an arbitrary string — a newline, an escape sequence — can arrive
// from a committed file at a gate that both decides on the value and prints it.
// A gate that cannot place the value says so rather than reading it as one of
// them.
func KnownAgentAuth(auth string) bool { return slices.Contains(AgentAuths, auth) }

// applyDefaults fills in every field fugaro.yaml may omit. The per-base cache
// default depends on the checkout's lockfile, so it is not applied here:
// DefaultCache computes it, and M4's cache restore applies it.
//
// The two round knobs are deliberately left at 0 when nothing set them: their
// default depends on the rigor mode a run resolves, which is every layer's
// decision plus the run's own request and paths — none of it known here — so
// the unset value travels to the one place that does know it (internal/runner's
// planOf and internal/cli's budget preview read rigor.DefaultRounds). A layer
// that does set one still wins, and 0 is "unset" at every layer (validate).
func applyDefaults(c *Config) {
	if c.Git.BaseBranch == "" {
		c.Git.BaseBranch = DefaultBaseBranch
	}
	if c.Git.PR.EarlyDraft == nil {
		t := true
		c.Git.PR.EarlyDraft = &t
	}
	if c.Agent.Auth == "" {
		c.Agent.Auth = DefaultAgentAuth
	}
	if c.Agent.FirstLineReview == "" {
		c.Agent.FirstLineReview = FirstLineAuto
	}
	if c.Agent.MaxBudgetUSD == 0 {
		c.Agent.MaxBudgetUSD = DefaultMaxBudgetUSD
	}
	for name, w := range c.Workflows {
		if len(w.Commands.Reports) == 0 {
			w.Commands.Reports = slices.Clone(DefaultReports)
		}
		if w.Resources.CPU == 0 {
			w.Resources.CPU = DefaultCPU
		}
		if w.Resources.Memory == "" {
			w.Resources.Memory = DefaultMemory
		}
		if w.Checkout == "" {
			w.Checkout = CheckoutBaked
		}
		w.Rebuild = w.Rebuild.Defaults()
		t := &w.Timeouts
		if t.Total.Duration == 0 {
			t.Total.Duration = 90 * time.Minute
		}
		// Unset timeouts scale down with a short total so a small total alone is valid.
		if t.FinalizeReserve.Duration == 0 {
			t.FinalizeReserve.Duration = min(5*time.Minute, t.Total.Duration/5)
		}
		if t.Stage.Duration == 0 {
			t.Stage.Duration = min(40*time.Minute, t.Total.Duration)
		}
		if t.Verify.Duration == 0 {
			t.Verify.Duration = min(30*time.Minute, t.Total.Duration)
		}
		// The render step is one command with its own timeout, so it gets
		// what is left of the run once the finalize reserve is set aside,
		// and never more than verify's own share (design artifact-runs.md
		// §3). The guard keeps a tiny total from defaulting it to zero,
		// which Validate would then refuse as "must be positive".
		if t.Render.Duration == 0 {
			if d := t.Total.Duration - t.FinalizeReserve.Duration; d > 0 && d < t.Verify.Duration {
				t.Render.Duration = d
			} else {
				t.Render.Duration = t.Verify.Duration
			}
		}
		c.Workflows[name] = w
	}
}
