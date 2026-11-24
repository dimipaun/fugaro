package settings

import "strings"

// This file is the metadata (meta.go, D2908) of a repository's workflows.*
// keys — SM3 of docs/console-improvements/implementation-plan.md, the half of
// its family that a repository's own fugaro.yaml carries. The project layer's
// keys are meta_layer.go.
//
// Every key here belongs to GroupWorkflows, because MetaGroups gives that
// group "one repository's build and test: workflows.*". The keys are the
// registry's own spellings, `*` and all: the read prints the row's key, so a
// metadata entry is registered under the pattern (`workflows.*.commands.test`)
// and never under a concrete workflow's name, which no table can enumerate
// (D1161).
//
// The bounds and the enumerations are the ones internal/config's Validate and
// validateImage, validateCheckout, validateRebuild and validateInputs enforce
// for a repository's file, and internal/cli's
// TestSettingsMetaLayerBoundariesAreAccepted sets each of them through
// settingsReparse — the strict decode `settings set` re-reads the bytes it
// wrote with.

func init() { registerWorkflowMeta() }

// registerWorkflowMeta fills the table with this family's entries. It panics
// on a refusal as registerLayerMeta does, and for the same reason: a table
// that would draw a row wrong is a diff to fix, not a screen to ship.
func registerWorkflowMeta() {
	each := func(key string, m Meta) {
		if err := Register(key, m); err != nil {
			panic("internal/settings: " + err.Error())
		}
	}

	each("workflows.*.profile", Meta{
		Label:       "Profile",
		Description: "The project layer's profile this workflow starts from, which the workflow's own keys override field by field.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.job_profile", Meta{
		Label:       "Job profile",
		Description: "The published policy's job profile this workflow runs on, which sets the Cloud Run job's own shape.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.base", Meta{
		Label:       "Base image",
		Description: "The workflow's base image, which 0.7.0 removed: Fugaro builds on its own base alone.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	// checkout: shared is a value the reader refuses until the release that
	// builds shared dependency-layer images flips its gate
	// (config.SharedCheckoutShipped), so the enum is the two kinds a file may
	// name today.
	each("workflows.*.checkout", Meta{
		Label:       "Checkout kind",
		Description: "Whether a job of this workflow runs a baked per-repository image or the base image with a clone.",
		Group:       GroupWorkflows,
		Type:        TypeEnum,
		Enum:        []string{"baked", "clone"},
	})
	each("workflows.*.image.node", Meta{
		Label:       "Node version",
		Description: "The Node version of the workflow's image, which 0.7.0 removed in favour of mise tools.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.image.jdk", Meta{
		Label:       "Java version",
		Description: "The Java version of the workflow's image, which 0.7.0 removed in favour of mise tools.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.image.apt", Meta{
		Label:       "System packages",
		Description: "The system packages the workflow's image installs as root.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.image.setup", Meta{
		Label:       "Build steps",
		Description: "The extra build steps the workflow's image runs after its warm-up.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.image.skip_build_scripts", Meta{
		Label:       "Skip build scripts",
		Description: "Whether the workflow's Node dependency install skips package lifecycle and build scripts.",
		Group:       GroupWorkflows,
		Type:        TypeBool,
	})
	each("workflows.*.image.tools", Meta{
		Label:       "Mise tools",
		Description: "The mise tools and versions the workflow's image writes into its global config.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.image.key_files", Meta{
		Label:       "Key files",
		Description: "The files the dependency install reads, whose change rebuilds the workflow's image.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.dockerfile", Meta{
		Label:       "Dockerfile",
		Description: "The repository-relative Dockerfile this workflow's image is built from instead of the image block.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.commands.build", Meta{
		Label:       "Build command",
		Description: "The command the run executes to build this workflow.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.commands.test", Meta{
		Label:       "Test command",
		Description: "The command the run executes to test this workflow.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.commands.lint", Meta{
		Label:       "Lint command",
		Description: "The gate command the check step runs after its fix command to judge the repair.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.commands.fix", Meta{
		Label:       "Fix command",
		Description: "The command the check step runs before lint to repair what the gate finds.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.commands.render", Meta{
		Label:       "Render command",
		Description: "The command an artifact run of this workflow runs once to render its outputs.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.commands.probe", Meta{
		Label:       "Probe command",
		Description: "The command an artifact run of this workflow runs to check what it rendered.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.commands.reports", Meta{
		Label:       "Report globs",
		Description: "The paths an artifact run of this workflow collects as its report.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.commands.rerun_failed", Meta{
		Label:       "Rerun failed tests",
		Description: "The command that reruns the tests a failed run left behind, built from this workflow's own command and per-test arguments.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.cache", Meta{
		Label:       "Dependency caches",
		Description: "The dependency caches this workflow's jobs restore and write back.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.secrets", Meta{
		Label:       "Workflow secrets",
		Description: "The Secret Manager entries this workflow's jobs mount, each as one environment variable.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.needs", Meta{
		Label:       "Image needs",
		Description: "The catalog tags this workflow's job needs, which the image selection picks an entry for.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.ab.reviewer_arm", Meta{
		Label:       "Reviewer arm level",
		Description: "The review level a second run replays over this workflow's champion, or nothing for no A/B arm.",
		Group:       GroupWorkflows,
		Type:        TypeEnum,
		Enum:        []string{"l1", "l2", "l3"},
	})
	// 0 is a value the reader takes: it is what a workflow that names no CPU
	// carries, and the run fills Fugaro's own default into it (config's
	// applyDefaults), so the floor a console offers is 0 and not 1 — a 1 the
	// entry refused would refuse a value the write accepts.
	each("workflows.*.resources.cpu", Meta{
		Label:       "CPU",
		Description: "The number of CPUs this workflow's job runs with, where 0 takes Fugaro's own default.",
		Group:       GroupWorkflows,
		Type:        TypeInt,
		Min:         "0",
	})
	each("workflows.*.resources.memory", Meta{
		Label:       "Memory",
		Description: "The memory this workflow's job runs with, such as 8Gi.",
		Group:       GroupWorkflows,
		Type:        TypeString,
		Format:      FormatMemory,
	})
	for to, name := range map[string]string{
		"total":            "Timeout",
		"stage":            "Stage timeout",
		"verify":           "Verify timeout",
		"render":           "Render timeout",
		"finalize_reserve": "Finalize reserve",
	} {
		each("workflows.*.timeouts."+to, Meta{
			Label:       name,
			Description: "The " + strings.ToLower(name) + " of this workflow's job, a positive duration such as 90m.",
			Group:       GroupWorkflows,
			Type:        TypeDuration,
			Format:      FormatDuration,
		})
	}
	each("workflows.*.rebuild.check", Meta{
		Label:       "Rebuild check",
		Description: "Whether this workflow's image is checked daily or only rebuilt by hand.",
		Group:       GroupWorkflows,
		Type:        TypeEnum,
		Enum:        []string{"daily", "off"},
	})
	// max_age is 0 or a duration between 1h and 90d, which no one bound pair
	// says: a floor of 1h would refuse the 0 that turns the age trigger off, so
	// the two ends are named in the description and the console's control is a
	// duration field.
	each("workflows.*.rebuild.max_age", Meta{
		Label:       "Rebuild max age",
		Description: "The age at which this workflow's image is rebuilt, 0 for no age limit, otherwise 1h to 90d.",
		Group:       GroupWorkflows,
		Type:        TypeDuration,
		Format:      FormatDuration,
	})
	each("workflows.*.rebuild.lockfiles", Meta{
		Label:       "Rebuild on lockfiles",
		Description: "Whether a lockfile change on the base branch rebuilds this workflow's image.",
		Group:       GroupWorkflows,
		Type:        TypeBool,
	})
	each("workflows.*.rebuild.base", Meta{
		Label:       "Rebuild on base image",
		Description: "Whether a move of the base image's digest rebuilds this workflow's image.",
		Group:       GroupWorkflows,
		Type:        TypeBool,
	})
	each("workflows.*.rebuild.paths", Meta{
		Label:       "Rebuild paths",
		Description: "The globs whose change on the base branch rebuilds this workflow's image.",
		Group:       GroupWorkflows,
		Type:        TypeString,
	})
	each("workflows.*.inputs.max_files", Meta{
		Label:       "Input file cap",
		Description: "The most files a launcher may attach to a run of this workflow.",
		Group:       GroupWorkflows,
		Type:        TypeInt,
		Min:         "0",
		Max:         "64",
	})
	each("workflows.*.inputs.max_bytes", Meta{
		Label:       "Input file size",
		Description: "The most bytes one attached file may hold.",
		Group:       GroupWorkflows,
		Type:        TypeInt,
		Min:         "0",
		Max:         "1073741824",
	})
	each("workflows.*.inputs.max_total_bytes", Meta{
		Label:       "Input total size",
		Description: "The most bytes the files attached to one run may hold together.",
		Group:       GroupWorkflows,
		Type:        TypeInt,
		Min:         "0",
		Max:         "4294967296",
	})
}
