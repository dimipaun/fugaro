package projectpolicy

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
)

// DefaultProfileName is the profile a workflow that names none runs on
// (design project-centric §4.1): config.DefaultJobProfile, named here as a
// reader of a policy reads it.
const DefaultProfileName = config.DefaultJobProfile

// Profile is one project job's shape (design §4.1): the unit shared by every
// repository admitted to it. Cloud Run cannot vary the image, the service
// account, cpu, memory or the task timeout per execution, so a profile names
// them once and Task 3.2 creates one job and one job service account per
// (profile, provider, account).
//
// It embeds config.JobProfile rather than respelling its fields, the way
// Provider embeds config.ModelProvider: the operator's config file declares
// the same document (localcfg.Config.Profiles), and a second declaration
// would drift — a key the file sets and the node does not, or the other way.
// The embedded carrier's json tags are what the node decodes.
type Profile struct{ config.JobProfile }

// ValidProfileName checks a profile name: 1 to 40 of a-z, 0-9 and '-',
// starting and ending with a letter or digit (config.ProjectNameRE). It is
// the same rule the operator's config file holds its own profile names to, so
// a name this refuses can never be defined in a policy and a selection naming
// it would be refused at run start instead of where it was written.
func ValidProfileName(name string) error {
	if name == "" {
		return fmt.Errorf("a profile name must not be empty")
	}
	if !config.ProjectNameRE.MatchString(name) {
		return fmt.Errorf("profile name %q must be 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit", name)
	}
	return nil
}

// ProfileFor is the policy's profile of that name, with "" meaning the
// default. It fails closed: a policy that does not define the profile a
// workflow selected halts the run rather than running it on anything else
// (§5(3), the Task 1.3 path). The error lists the profiles the policy does
// define, so an operator reads one line.
//
// It is the gate Task 3.2's job layout calls when it reads a workflow's
// job_profile (config.Workflow.JobProfileOrDefault). Nothing in this release
// calls it outside tests: no run selects a job profile yet, and the refusal
// this returns is the halt a run takes once the selection exists.
func (p *Policy) ProfileFor(name string) (Profile, error) {
	if name == "" {
		name = DefaultProfileName
	}
	if pr, ok := p.Profiles[name]; ok {
		return pr, nil
	}
	defined := p.ProfileNames()
	if len(defined) == 0 {
		return Profile{}, fmt.Errorf("projectpolicy: the policy defines no job profile, so %q cannot run: the default is %s", name, DefaultProfileName)
	}
	return Profile{}, fmt.Errorf("projectpolicy: the policy does not define the job profile %q; it defines %s (the default is %s)",
		name, strings.Join(defined, ", "), DefaultProfileName)
}

// Admission is the list a run of that profile is held to on top of the
// project's: the profile's own allow_repos when it names any, the project's
// otherwise. An empty result admits nothing — never everything — so a profile
// that lists no repository and a project that lists none are both deny-all
// (design §4.3).
//
// It is the profile's list alone: a caller deciding whether a repository may
// run uses Admits, which holds both gates (§4.3's enrollment and this one).
func (p *Policy) Admission(profile string) ([]string, error) {
	pr, err := p.ProfileFor(profile)
	if err != nil {
		return nil, err
	}
	if len(pr.AllowRepos) > 0 {
		return pr.AllowRepos, nil
	}
	return p.AllowRepos, nil
}

// Admits reports whether a canonical repository (Canonical) may run on that
// profile, and is the check a runner makes before it clones or mints a token
// (design §4.1). Two gates, both of which must open:
//
//   - the project's allow_repos: "there is never a state in which a shared job
//     serves a repository that has not been allowed" (§4.3). A profile's list
//     can only narrow the enrollment, never widen it, however it is spelled;
//   - the profile's own allow_repos, when it names one (§4.1): a secret that
//     must stay away from some repositories lives on a profile they are not
//     allowed on, which is the whole meaning of a profile's list.
//
// An undefined profile is an error, not a refusal: the caller reports it as
// the fail-closed halt it is (ProfileFor), rather than as an unenrolled
// repository.
//
// As with ProfileFor, nothing outside tests calls it in this release: a run
// checks the project's allow_repos (internal/runner/project.go,
// internal/cli/run_allowlist.go) and no run has a selected profile to narrow
// it with. This is the check that second gate will be applied through, not one
// applied today — which is why the profile's list is validated strictly here
// (validProfiles) rather than trusted to a caller that does not exist yet.
func (p *Policy) Admits(profile, canonical string) (bool, error) {
	list, err := p.Admission(profile)
	if err != nil {
		return false, err
	}
	return AdmitRepo(p.AllowRepos, canonical) && AdmitRepo(list, canonical), nil
}

// validProfiles checks the profiles block of a node, entry by entry: the
// default profile must be defined, every name a profile name, every profile
// pin one provider (required, finding 4), its cpu/memory/timeout be a shape
// Cloud Run accepts, and every allow_repos pattern name a repository of that
// one provider — a pattern of another provider could never match and is
// refused here instead of silently admitting nothing.
func validProfiles(profiles map[string]Profile) error {
	if len(profiles) == 0 {
		// A block that carries nothing defines nothing — not even the profile
		// every workflow defaults to. Leaving the key out is how a policy says
		// it sets no profile at all.
		return fmt.Errorf("projectpolicy: profiles: a profiles block must define %s; leave the block out to set no profile", DefaultProfileName)
	}
	for _, name := range profileMapNames(profiles) {
		pr := profiles[name]
		at := fmt.Sprintf("projectpolicy: profiles.%s", name)
		if err := ValidProfileName(name); err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
		// The provider is required (finding 4): it is part of the job's
		// identity (one job per profile, provider and account, task 3.2), it
		// decides which repositories the profile may serve, and it is the
		// credential the job holds — a profile without one describes no job.
		if pr.Provider == "" {
			return fmt.Errorf("%s.provider: a profile must name the provider it serves; there is no default (a Bitbucket profile never shares a project-wide credential)", at)
		}
		// The kinds Fugaro has a git path for (config.Providers) — the same
		// list fugaro.yaml's git.provider and the local config's
		// repos.<repo>.provider are held to. A kind outside it names no job
		// this code could ever deploy.
		if !slices.Contains(config.Providers, pr.Provider) {
			return fmt.Errorf("%s.provider: %q must be one of %s", at, pr.Provider, strings.Join(config.Providers, ", "))
		}
		if issues := gcp.CheckResources(config.Resources{CPU: pr.CPU, Memory: pr.Memory}); len(issues) > 0 {
			msgs := make([]string, len(issues))
			for i, issue := range issues {
				msgs[i] = issue.Field + ": " + issue.Message
			}
			return fmt.Errorf("%s: %s", at, strings.Join(msgs, "; "))
		}
		if err := validTimeoutClass(at, pr.TimeoutClass); err != nil {
			return err
		}
		// A secret entry is a NAME. It is held to config's own rule for one
		// (CheckSecretName: a lower-case name, and never something that looks
		// like a pasted key), because this document is readable by every run
		// token of the job and printed by `policy show` — a value written
		// here would be a value published. Neither refusal below echoes the
		// entry: the thing being refused may be the value itself.
		seen := make(map[string]bool, len(pr.Secrets))
		for i, secret := range pr.Secrets {
			if msg := config.CheckSecretName(secret); msg != "" {
				return fmt.Errorf("%s.secrets[%d]: %s", at, i, msg)
			}
			if seen[secret] {
				return fmt.Errorf("%s.secrets[%d]: a secret is listed twice; list each secret once", at, i)
			}
			seen[secret] = true
		}
		for i, pattern := range pr.AllowRepos {
			if err := ValidPattern(pattern); err != nil {
				return fmt.Errorf("%s.allow_repos[%d]: %w", at, i, err)
			}
			// A pattern of another provider can never match a repository this
			// profile's job is given (task 3.2 mints one job per provider), so
			// it is refused rather than read as "admits nothing": the owner
			// wrote a list they believe admits something.
			if head := patternProvider(pattern); head != "" && head != pr.Provider {
				return fmt.Errorf("%s.allow_repos[%d]: %q names the %s provider, and this profile serves %s; a profile admits only its own provider's repositories",
					at, i, pattern, head, pr.Provider)
			}
		}
	}
	if _, ok := profiles[DefaultProfileName]; !ok {
		return fmt.Errorf("projectpolicy: profiles: %s must be defined: it is the profile every workflow that names none runs on", DefaultProfileName)
	}
	return nil
}

// validTimeoutClass checks a profile's task timeout. Cloud Run's own ceiling
// is the authority (gcp.MaxTaskTimeout, 168h) exactly as its cpu and memory
// limits are: the value becomes the job's task timeout, so a form the API
// would refuse must be refused where the owner can read it. The spelling is
// config.ParseDuration's — the one timeouts.total is read with in fugaro.yaml,
// day counts and all — so "14d" is a timeout here because it is a timeout
// there, rather than a form this package refused and that one accepted.
func validTimeoutClass(at, class string) error {
	if class == "" {
		return fmt.Errorf("%s.timeout_class: a profile must name its task timeout, for example \"6h\"", at)
	}
	d, err := config.ParseDuration(class)
	if err != nil {
		return fmt.Errorf("%s.timeout_class: %q is not a duration, for example \"90m\", \"6h\" or \"7d\"", at, class)
	}
	if d <= 0 {
		return fmt.Errorf("%s.timeout_class: %q is not a positive duration", at, class)
	}
	if d > gcp.MaxTaskTimeout {
		return fmt.Errorf("%s.timeout_class: %q is longer than Cloud Run's longest task timeout, %s", at, class, gcp.MaxTaskTimeout)
	}
	return nil
}

// patternProvider is the provider a repository pattern names, or "" when the
// pattern names none ("*", which admits every provider's repositories).
func patternProvider(pattern string) string {
	head, _, _ := strings.Cut(pattern, "/")
	if head == "*" {
		return ""
	}
	return head
}

// profileMapNames is a stable order for error messages (a map's range is
// random, so a node with two bad entries would otherwise name a different one
// on each read).
func profileMapNames(m map[string]Profile) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ProfileNames is the policy's profile names, sorted: what a reader lists when
// it reports the profiles a policy defines (the CLI's `project show`) and what
// ProfileFor's refusal names.
func (p *Policy) ProfileNames() []string { return profileMapNames(p.Profiles) }

// sortedProfileNames is a stable order for the strict decode of the profiles
// block (its entries are raw, so a bad entry is reported by name).
func sortedProfileNames(entries map[string]json.RawMessage) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
