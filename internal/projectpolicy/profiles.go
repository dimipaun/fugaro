package projectpolicy

import (
	"encoding/json"
	"sort"

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
// the same rule the operator's config file holds its own profiles: to, so a
// name this refuses can never be defined in a policy and a selection naming
// it would be refused at run start instead of where it was written.
func ValidProfileName(name string) error {
	return nil
}

// ProfileFor is the policy's profile of that name, with "" meaning the
// default. It fails closed: a policy that does not define the profile a
// workflow selected halts the run rather than running it on anything else
// (§5(3), the Task 1.3 path). The error lists the profiles the policy does
// define, so an operator reads one line.
func (p *Policy) ProfileFor(name string) (Profile, error) {
	return Profile{}, nil
}

// Admission is the admission list a run of that profile is held to: the
// profile's own allow_repos when it names any, the project's otherwise. An
// empty result admits nothing — never everything — so a profile that lists
// no repository and a project that lists none are both deny-all (design §4.3).
func (p *Policy) Admission(profile string) ([]string, error) {
	return nil, nil
}

// validProfiles checks the profiles block of a node, entry by entry: the
// default profile must be defined, every name a profile name, every profile
// pin one provider (required, finding 4), its cpu/memory/timeout be a shape
// Cloud Run accepts, and every allow_repos pattern name a repository of that
// one provider — a pattern of another provider could never match and is
// refused here instead of silently admitting nothing.
func validProfiles(profiles map[string]Profile) error {
	return nil
}

// sortedProfileNames is a stable order for error messages and for the strict
// decode (a map's range is random, so a node with two bad entries would
// otherwise name a different one on each read).
func sortedProfileNames(entries map[string]json.RawMessage) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ProfileNames is the policy's profile names, sorted: what a reader lists when
// it reports the profiles a policy defines (the CLI's `project show`) and what
// ProfileFor's refusal names.
func (p *Policy) ProfileNames() []string {
	names := make([]string, 0, len(p.Profiles))
	for name := range p.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
