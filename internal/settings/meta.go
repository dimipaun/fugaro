package settings

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

// Meta is what a setting is called and how it is edited: the human-readable
// label, the one-sentence description, the accordion group, and the control a
// value calls for (docs/console-improvements/spec.md §3 and §4, D2908).
//
// It is an entry of a second table beside Row's (registry.go) rather than more
// fields on Row, because the two answer different questions: Row is what the
// read and the write paths need to know about a key, and Meta is what a person
// needs to be shown. The console reads it from the emitted document, so a key
// with no Meta is drawn by its raw key under the console's own "Other" group,
// which is why the table may be sparse (D2908).
//
// A Meta is not the document. The member names the console reads (`label`,
// `description`, `group`, `type`, `enum`, `min`, `max`, `format`) belong to the
// emitter (internal/cli/settings_render.go, SM-E of the same plan), so this
// type carries no struct tags and the wire shape is stated in one place.
type Meta struct {
	// Label is the name a person reads, at most six words.
	Label string
	// Description is one sentence saying what the setting does.
	Description string
	// Group is the accordion group, one of MetaGroups.
	Group MetaGroup
	// Type is the control the value calls for, one of MetaTypes.
	Type MetaType
	// Enum is the closed set of values a TypeEnum key accepts, in the order a
	// select draws them, and empty for every other type.
	Enum []string
	// Min and Max bound an int, float or duration value, each written in the
	// value's own spelling (`1`, `0.5`, `5m`) and empty when the key has no
	// bound on that side. They are strings rather than numbers because a
	// duration's bound is a duration and because a value is a string on every
	// surface a write reaches (settings set takes `-- <VALUE>`), so the console
	// parses them by Type rather than by a second spelling. Empty for every
	// other type.
	Min string
	Max string
	// Format is the shape the CLI already validates for a string value, one of
	// MetaFormats, or FormatNone for free text.
	Format MetaFormat
}

// MetaType is the control a setting's value calls for (spec §3): the closed
// vocabulary the console branches on to draw a toggle, a number, a text field
// or a select.
type MetaType string

const (
	TypeString   MetaType = "string"
	TypeBool     MetaType = "bool"
	TypeInt      MetaType = "int"
	TypeFloat    MetaType = "float"
	TypeDuration MetaType = "duration"
	TypeEnum     MetaType = "enum"
)

// MetaTypes is the closed vocabulary of Meta.Type. It is exported so that a
// reader which must hold the CLI to it — the schema's own enumeration, SM-E of
// docs/console-improvements/implementation-plan.md — pins against this list
// rather than against a second copy of it.
var MetaTypes = []MetaType{TypeString, TypeBool, TypeInt, TypeFloat, TypeDuration, TypeEnum}

// MetaFormat is the shape of a value the CLI already validates, and the closed
// vocabulary a text control validates against on the client before OK. A shape
// with no entry here is free text.
type MetaFormat string

const (
	// FormatNone is free text: the empty spelling, which the emitter omits.
	FormatNone MetaFormat = ""
	// FormatDuration is a duration as config.ParseDuration reads one, such as
	// `90m` or `14d`.
	FormatDuration MetaFormat = "duration"
	// FormatRepo is a repository as `owner/name`, the shape the loaders' own
	// decode holds a repository list such as allow_data_to to.
	FormatRepo MetaFormat = "repo"
	// FormatEmail is an e-mail address, the shape a pool's owner and its
	// allowed users are held to (config's poolEmailRE).
	FormatEmail MetaFormat = "email"
	// FormatURL is an https clone URL without credentials, the shape
	// repourl.CloneURL accepts for a clone_urls value.
	FormatURL MetaFormat = "url"
	// FormatMemory is a memory size as `512Mi` or `16Gi` (config's memoryRE).
	FormatMemory MetaFormat = "memory"
	// FormatBranch is a git branch name (config.ValidBranchName).
	FormatBranch MetaFormat = "branch"
)

// MetaFormats is the closed vocabulary of Meta.Format, FormatNone included.
var MetaFormats = []MetaFormat{FormatNone, FormatDuration, FormatRepo, FormatEmail, FormatURL, FormatMemory, FormatBranch}

// MetaGroup is an accordion group of the console's settings panel (§4). The
// names are the headings a person reads, so they are plain language rather than
// the registry's key prefixes.
type MetaGroup string

const (
	// GroupRuns is what a run does and what it may spend on it: `agent.`,
	// `stages.`, `escalation.`, `orchestrator.` and `max_parallel`.
	GroupRuns MetaGroup = "Runs"
	// GroupBudget is the budget database's own caps: `budget.`.
	GroupBudget MetaGroup = "Budget"
	// GroupRepositories is what a repository is and how it is reached: `git.`,
	// `repos.`, `base_branch` and `clone_urls.`.
	GroupRepositories MetaGroup = "Repositories"
	// GroupWorkflows is one repository's build and test: `workflows.*`.
	GroupWorkflows MetaGroup = "Workflows"
	// GroupProfiles is the project layer's own file: `layer.*`.
	GroupProfiles MetaGroup = "Profiles"
	// GroupCredentials is what a model call goes out on: `credentials.`,
	// `default_claude_account`, `claude_account_auto`, `subscription_spread`,
	// `verifier_accounts` and `personal_workspace_tokens`.
	GroupCredentials MetaGroup = "Credentials"
	// GroupReviews is how much review a run gives and what it costs:
	// `governance.`, `triage` and `issues.`.
	GroupReviews MetaGroup = "Reviews"
	// GroupFollowups is what a run does with the comments it is handed:
	// `followup.`.
	GroupFollowups MetaGroup = "Follow-ups"
	// GroupWatching is what Watch alerts on: `watch.`.
	GroupWatching MetaGroup = "Watching"
	// GroupMachine is this machine's own copy: `update_check`, `commit_name`,
	// `commit_email`, `bake_repos` and `planning.decoupling`.
	GroupMachine MetaGroup = "This machine"
)

// MetaGroups is the group order: the accordion's sections, in the order the
// console draws them, and the whole of the vocabulary a Meta.Group may name. It
// is the one list the console's grouping and the entries' groups agree on, so a
// group added here is a section the panel grows and a group named nowhere here
// is refused where its family is loaded.
var MetaGroups = []MetaGroup{
	GroupRuns, GroupBudget, GroupRepositories, GroupWorkflows, GroupProfiles,
	GroupCredentials, GroupReviews, GroupFollowups, GroupWatching, GroupMachine,
}

// metaByKey is the table. Registration is what a family's init and a test do
// while a lookup may run in the same binary, so it is guarded rather than built
// once before any reader exists as registry.go's rows are.
var (
	metaMu    sync.RWMutex
	metaByKey = map[string]Meta{}
)

// Register adds key's metadata to the table. It answers an error for a key that
// already has an entry, and for an entry that names no group of MetaGroups, no
// type of MetaTypes or a control that cannot carry what it carries.
//
// Registration happens in the family's package init, where the error is a
// programming error rather than a user's mistake: the family panics on it, as
// the writers table does for RegisterWriter, so that a table which would draw a
// row wrong fails every command of the binary loudly instead of at a screen.
func Register(key string, m Meta) error {
	if key == "" {
		return errors.New("settings: metadata is registered under a registry key, and this one is empty")
	}
	if err := checkMeta(m); err != nil {
		return fmt.Errorf("settings: the metadata of %s: %w", key, err)
	}
	m.Enum = slices.Clone(m.Enum)
	metaMu.Lock()
	defer metaMu.Unlock()
	if _, dup := metaByKey[key]; dup {
		return fmt.Errorf("settings: %s already has metadata, and one key has one entry: two families naming one key is a diff to settle, not a registration to win", key)
	}
	metaByKey[key] = m
	return nil
}

// MetaOf returns key's metadata and whether the table holds one. The name is
// not Lookup: this package's Lookup is the row table's (registry.go), and the
// shape here is that one's and config.ScopeOf's — the value and whether it was
// found.
func MetaOf(key string) (Meta, bool) {
	metaMu.RLock()
	defer metaMu.RUnlock()
	m, ok := metaByKey[key]
	if !ok {
		return Meta{}, false
	}
	m.Enum = slices.Clone(m.Enum)
	return m, true
}

// unregisterMeta drops a key's entry. Nothing in the binary calls it: it is
// what a test's t.Cleanup runs, so that a key a test registered is not seen by
// another test of this package or by a later run.
func unregisterMeta(key string) {
	metaMu.Lock()
	defer metaMu.Unlock()
	delete(metaByKey, key)
}

// checkMeta is why Register refuses this entry, nil when it takes it.
func checkMeta(m Meta) error {
	switch {
	case m.Label == "":
		return errors.New("the label is empty")
	case m.Description == "":
		return errors.New("the description is empty")
	case !slices.Contains(MetaGroups, m.Group):
		return fmt.Errorf("the group %q is not one of MetaGroups", m.Group)
	case !slices.Contains(MetaTypes, m.Type):
		return fmt.Errorf("the type %q is not one of MetaTypes", m.Type)
	case !slices.Contains(MetaFormats, m.Format):
		return fmt.Errorf("the format %q is not one of MetaFormats", m.Format)
	case m.Type == TypeEnum && len(m.Enum) == 0:
		return errors.New("an enum carries no value")
	case m.Type != TypeEnum && len(m.Enum) > 0:
		return fmt.Errorf("the type %q carries an enum", m.Type)
	case (m.Min != "" || m.Max != "") && m.Type != TypeInt && m.Type != TypeFloat && m.Type != TypeDuration:
		return fmt.Errorf("the type %q carries a bound", m.Type)
	}
	return nil
}
