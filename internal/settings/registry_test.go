package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runner"
)

// yamlPaths is every leaf path a yaml-tagged struct writes, with "*" standing
// for the key of a map or the index of a list. It is the completeness guard's
// source: a field the structs carry and the registry does not is a key the read
// silently omits.
func yamlPaths(t reflect.Type, prefix string) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return []string{prefix}
	}
	if prefix != "" && !yamlFields(t) {
		// A struct with no yaml-tagged field of its own is one value, not a
		// subtree: config.Duration writes "90m" and carries its two fields for
		// the parser, so max_age is a leaf the registry needs a row for. Walking
		// into it instead found no tagged field and returned nothing at all,
		// which is how layer.profiles.*.rebuild.max_age stayed out of the table.
		return []string{prefix}
	}
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		path := tag
		if prefix != "" {
			path = prefix + "." + tag
		}
		// The local config's profiles: block holds job profiles, and the
		// project layer's holds workflow profiles (SD14): one key may not
		// mean two things. Its version: and gcp_project: are the same case —
		// a fugaro.yaml key already has each name — and carry the prefix the
		// registry's local rows of those two use.
		switch path {
		case "profiles":
			path = "job_profiles"
		case "version", "gcp_project":
			path = "local." + path
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Struct:
			out = append(out, yamlPaths(ft, path)...)
		case reflect.Map, reflect.Slice, reflect.Array:
			et := ft.Elem()
			for et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				out = append(out, yamlPaths(et, path+".*")...)
			} else {
				out = append(out, path)
			}
		default:
			out = append(out, path)
		}
	}
	return out
}

// yamlFields reports whether a struct writes any yaml-tagged field of its own.
func yamlFields(t reflect.Type) bool {
	for i := range t.NumField() {
		tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
		if tag != "" && tag != "-" {
			return true
		}
	}
	return false
}

// TestRegistryCarriesEveryRunStagesAccount: a stage account is addressable
// under the spelling the owner writes, one row per stage name the config
// accepts. The rows are derived from config.RunStages (stageAccountRows), so
// this is the pin on what that derivation produced: the wildcard row the local
// config's own map yields, the concrete row beside it, and both local-only and
// the generic path's — a stage's account is decided on the owner's own machine
// and by nobody else (spec §11.1).
func TestRegistryCarriesEveryRunStagesAccount(t *testing.T) {
	if _, ok := Lookup("stages.*.claude_account"); !ok {
		t.Error("stages.*.claude_account: no registry row, and the local config's stages: is a map — the walk TestRegistryCoversEveryConfigField makes of it yields that spelling and nothing else")
	}
	for _, stage := range config.RunStages {
		key := "stages." + stage + ".claude_account"
		r, ok := Lookup(key)
		if !ok {
			t.Errorf("%s: no registry row: the read answers that name and the write lands on it", key)
			continue
		}
		if !slices.Equal(r.Layers, []string{LayerLocal}) {
			t.Errorf("%s: Layers = %v, want the local config alone: an account is decided on the owner's machine and never in a reviewed file or a published layer", key, r.Layers)
		}
		if r.Owner != "settings set" || r.Doc == "" {
			t.Errorf("%s: Owner = %q and Doc = %q, and a row a reader can act on names both", key, r.Owner, r.Doc)
		}
		if r.Default.Unset == "" {
			t.Errorf("%s: Default = %+v, want the sentence an unset stage account reads as", key, r.Default)
		}
		if r.Sensitive {
			t.Error("a stage account names an account rather than holding a token, and the read prints a name (spec §2.3)")
		}
	}
}

// TestRegistryIsInKeyOrder: the table is sorted, so a reader scanning for a
// key finds it where its name says and two reads of one registry print the same
// order. Sorted() is the invariant; this is what holds buildRows to it.
func TestRegistryIsInKeyOrder(t *testing.T) {
	if !Sorted() {
		t.Error("Rows() is not in key order")
	}
}

func TestRegistryCoversEveryConfigField(t *testing.T) {
	have := map[string]bool{}
	for _, r := range Rows() {
		have[r.Key] = true
	}
	var missing []string
	add := func(paths []string) {
		for _, p := range paths {
			if !have[p] {
				missing = append(missing, p)
			}
		}
	}
	for _, s := range config.Scopes {
		if !have[s.Key] {
			missing = append(missing, s.Key)
		}
	}
	add(yamlPaths(reflect.TypeOf(config.ProjectLayer{}), "layer"))
	add(yamlPaths(reflect.TypeOf(localcfg.Config{}), ""))
	if len(missing) > 0 {
		t.Fatalf("no registry row for %d key(s):\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

func TestRegistryFugaroYAMLKeysMatchConfigScopes(t *testing.T) {
	rows := map[string]Row{}
	for _, r := range Rows() {
		rows[r.Key] = r
	}
	for _, s := range config.Scopes {
		r, ok := rows[s.Key]
		if !ok {
			t.Errorf("%s: config.Scopes has the key and the registry does not", s.Key)
			continue
		}
		// A ceiling key is set in the local config and the published node
		// as well as in fugaro.yaml (spec §2.2), so the row's layers are a
		// superset of the scope's and never a different set.
		for _, l := range scopeLayers(s.In) {
			if !slices.Contains(r.Layers, l) {
				t.Errorf("%s: layers = %v, config.Scopes says %v", s.Key, r.Layers, scopeLayers(s.In))
				break
			}
		}
	}
	// The reverse direction: only a fugaro.yaml key may be set in the layers
	// config.Scopes owns, so a row claiming one of them is a row whose key
	// belongs in that table. The table's own answer for a path inside a listed
	// key is the listed key (config.ScopeOf): `governance.stage_timeout.
	// implement` is a key of the block `governance.stage_timeout` is, which the
	// table lists InProject|InRepo, and that is what makes W2's concrete rows
	// members of the table rather than rows beside it. A key the table answers
	// nothing for is the failure here, and so is a row claiming a layer the key
	// it is inside is not set in — the block a repository may set is the whole
	// of what its leaves may claim.
	for _, r := range Rows() {
		if strings.HasPrefix(r.Key, "layer.") {
			continue
		}
		scope, listed := config.ScopeOf(r.Key)
		for _, l := range r.Layers {
			if l != LayerProject && l != LayerProfile && l != LayerRepo && l != LayerFlag {
				continue
			}
			if listed && slices.Contains(scopeLayers(scope.In), l) {
				continue
			}
			t.Errorf("%s: claims the %s layer, and config.Scopes lists neither the key nor a block it is a key of (layer %v)", r.Key, l, scopeLayers(scope.In))
			break
		}
	}
}

func TestRegistryKeysAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Rows() {
		if seen[r.Key] {
			t.Errorf("%s: two rows carry the key", r.Key)
		}
		seen[r.Key] = true
	}
}

// TestRegistryOwnerNamesACommand pins every row's Owner to the command family
// that writes it, out of the vocabulary the spec's §4.4 lists. A row that names
// nothing, or names a family that is not a command, leaves a reader unable to
// answer "who writes this".
func TestRegistryOwnerNamesACommand(t *testing.T) {
	families := []string{
		"settings set", "models", "rigor", "rigor floor", "rigor security",
		"accounts", "secrets",
		"config", "budget", "project", "setup", "update", "workspace",
		// The four keys a checkout's `fugaro.yaml` is opened with, which
		// `fugaro config init` writes and no edit lands on (D1178): the token is
		// this table's, and the read's own map is where it becomes the command's
		// name (settingsOwnerCommands).
		"config init",
		// The image map's own family (D1906): the key is the project's image
		// policy per repository and `fugaro image map set` is what writes it, so
		// the row names that command rather than `fugaro project`, which writes
		// the project's other policy keys and not this one.
		"image map",
		// The clone URLs' family, the image map's twin: an entry is keyed by a
		// canonical repository name and its value is held to the node's own
		// rule, so `fugaro project clone-url` is where the two are checked
		// against each other (settings spec §5.6, D842).
		"project clone-url",
		// The binary admission's family (K2 of docs/config-ownership, D2613):
		// both members are the owner's grant over the bytes a run may add, and
		// each is held to a rule no scalar write can apply — a pattern to the
		// node's own glob rule and the cap to a ceiling a project may only
		// lower — so `fugaro project binaries` writes them, and the row names
		// that command rather than `fugaro project`, whose other keys this
		// block is not.
		"project binaries",
		// A block of settings (W2 of docs/config-ownership, D2604): the key
		// whose own spelling holds other rows and no value of its own, so the
		// generic path refuses it (settingsRefuseRow) and its row names the
		// command that writes the settings under it rather than the command
		// itself — the default token would say `settings set` writes a key it
		// will not write (D1178, F13).
		"settings block",
	}
	for _, r := range Rows() {
		ok := false
		for _, f := range families {
			if r.Owner == f {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s: Owner = %q, which is not one of %v", r.Key, r.Owner, families)
		}
		if r.Doc == "" {
			t.Errorf("%s: no document states the key", r.Key)
		}
	}
}

// credentialShaped are the key suffixes whose value is a credential, and so is
// never printed. excused are the keys that carry one of those words and are
// still printed, each with why: a name is not a secret (spec §2.3).
var (
	credentialShaped = []string{"api_key", "token", "secret", "password"}
	excused          = map[string]string{
		"agent.oauth_token":               "a credential identity, not a credential",
		"budget.token_signer":             "a service account name, not key material",
		"workflows.*.secrets":             "a list of Secret Manager names",
		"layer.profiles.*.secrets":        "a list of Secret Manager names",
		"job_profiles.*.secrets":          "a list of Secret Manager names",
		"batch.secrets":                   "a map of Secret Manager names",
		"providers.*.secret":              "a Secret Manager name",
		"project_secrets.*.allow_data_to": "a list of destinations, not a credential",
		"project_secrets.*.proxy":         "a Secret Manager name",
		// The cascade's cap on how many runs may hold one shared credential at
		// a time (projectpolicy.Credentials.ConcurrencyPerToken): the word is
		// the credential's, the value is a count of runs, and the read prints
		// the figure the node holds.
		"credentials.concurrency_per_token": "a count of runs, not a credential",
		// The cascade's cap is a count of runs, not key material: "token" here
		// is the shared OAuth token the runs queue behind, and the value is how
		// many of them may hold it at once (D142). Printing it is the point of
		// the row — a person reading why their launches queue wants the figure,
		// and hiding it would leave the one key of the cascade that is a number
		// reading as a secret.
		"credential_cascade.concurrency_per_token": "how many runs may hold one shared credential, not a credential",
	}
)

func TestRegistryMarksEverySensitiveKey(t *testing.T) {
	sensitive := map[string]bool{}
	for _, r := range Rows() {
		if r.Sensitive {
			sensitive[r.Key] = true
		}
	}
	for _, r := range Rows() {
		shaped := false
		for _, suf := range credentialShaped {
			if strings.HasSuffix(r.Key, suf) {
				shaped = true
			}
		}
		if _, ok := excused[r.Key]; ok {
			if r.Sensitive {
				t.Errorf("%s: marked sensitive, and it is a name rather than a secret", r.Key)
			}
			continue
		}
		if shaped != r.Sensitive {
			t.Errorf("%s: sensitive = %v, want %v (credential-shaped: %v)", r.Key, r.Sensitive, shaped, shaped)
		}
	}
	if len(sensitive) == 0 {
		t.Fatal("no row is sensitive: the flag is not exercised")
	}
}

// TestSettingsRegistryHasIssuesKeys pins the two nudge thresholds of the
// terminal Issues section (T-IS15 of docs/troubleshoot-issue/, F14): the
// section's wording firms up at them, they are the local config's own — the
// section is printed on whichever machine runs the digest and the committed
// report carries none — and the DEFAULT column is where their defaults are
// documented, read back out of localcfg by
// TestRegistryDefaultsMatchApplyDefaults.
func TestSettingsRegistryHasIssuesKeys(t *testing.T) {
	for _, tc := range []struct{ key, def, constant string }{
		{"issues.nudge_after_days", strconv.Itoa(localcfg.DefaultIssuesNudgeAfterDays), "localcfg.DefaultIssuesNudgeAfterDays"},
		{"issues.nudge_recurrence", strconv.Itoa(localcfg.DefaultIssuesNudgeRecurrence), "localcfg.DefaultIssuesNudgeRecurrence"},
	} {
		r, ok := Lookup(tc.key)
		if !ok {
			t.Errorf("%s: no registry row", tc.key)
			continue
		}
		if !slices.Equal(r.Layers, []string{LayerLocal}) {
			t.Errorf("%s: Layers = %v, want the local config alone: the nudge is the printing machine's own and is never published", tc.key, r.Layers)
		}
		if r.Default.Value != tc.def || r.Default.Const != tc.constant {
			t.Errorf("%s: Default = {%q %q}, want {%q %q}", tc.key, r.Default.Value, r.Default.Const, tc.def, tc.constant)
		}
		if r.Owner == "" || r.Doc == "" {
			t.Errorf("%s: Owner = %q and Doc = %q, and a row a reader can act on names both", tc.key, r.Owner, r.Doc)
		}
	}
}

// TestEscalationRegistryKeysAreLocalOnly: the ladder's thresholds decide when a
// stalled item comes off its normal recipe, what it runs on then and at what
// cost it is merged under the gate, so the local config is their only store
// (E3, D2032, G3) — a row advertising the project layer or the shared file
// would name a store the key cannot have, and the read would offer an edit the
// write then refuses. Their owner is the generic path, which is what puts the
// owner's own hand and the coding agent's fence in front of them at all.
func TestEscalationRegistryKeysAreLocalOnly(t *testing.T) {
	for _, key := range []string{
		"escalation.r2_after_hours",
		"escalation.r2_after_attempts",
		"escalation.r3_after_hours",
		"escalation.r2_coder",
		"escalation.critical_path_factor",
		"escalation.cost_ceiling",
		"escalation.merge_judge",
		"escalation.ready_drafts",
		"escalation.modes.*.r2_after_hours",
		"escalation.modes.*.r2_after_attempts",
		"escalation.modes.*.r3_after_hours",
		"escalation.modes.*.r2_coder",
	} {
		r, ok := Lookup(key)
		if !ok {
			t.Errorf("%s: no registry row, so the read cannot name a key the file holds", key)
			continue
		}
		if !slices.Equal(r.Layers, []string{LayerLocal}) {
			t.Errorf("%s: Layers = %v, want the local config alone — a threshold is authority, and no shared layer may hold it", key, r.Layers)
		}
		if r.Owner != "settings set" {
			t.Errorf("%s: Owner = %q, want the generic path, the door that carries the typed form and the coding agent's fence", key, r.Owner)
		}
		if r.Doc != docEscalation {
			t.Errorf("%s: Doc = %q, want %q", key, r.Doc, docEscalation)
		}
	}
	if r, ok := Lookup("escalation.ready_drafts"); !ok || r.TypedWhy == "" {
		t.Error("escalation.ready_drafts carries no TypedWhy, so turning the daemon's readiness authority on is written unasked (D2046)")
	}
}

// TestCraftDefaultsNameTheCraftTheyFollow: a craft's DEFAULT column is what
// `fugaro settings ls` prints for a key the owner has not set, so the craft it
// names is the craft the read tells him his write will follow. config.CraftParent
// is the rule — the first-line review and the merge judge follow the coder, the
// security review follows the senior review — and the judge is the craft where a
// wrong sentence costs: an owner who reads that the judge follows the senior
// review, and sets agent.crafts.senior-review.model for it, leaves the judge on
// the coder's model, on the stage whose verdict gates a merge (E6, D2047).
//
// The rows are the two halves wherever a half is the owner's to write: a craft's
// model in the repository's own file, the project layer and the local config, and
// its access in the two places an access may be named. The project layer's access
// row is not one of them: no layer above the local config names a credential, so
// its DEFAULT column says what unset means there whatever the craft follows.
func TestCraftDefaultsNameTheCraftTheyFollow(t *testing.T) {
	// The craft a craft follows, as the column spells it. The key is hyphenated
	// and the sentence is not.
	followed := map[string]string{config.CraftCoder: "the coder", config.CraftSeniorReview: "the senior review"}
	keys := []string{
		"agent.crafts.%s.model",
		"layer.defaults.agent.crafts.%s.model",
		"crafts.%s.model",
		"agent.crafts.%s.access",
		"crafts.%s.access",
	}
	for _, craft := range config.CraftNames {
		parent := config.CraftParent(craft)
		if parent == "" {
			// The coder and the senior review follow nobody: the spec makes both
			// required, and their rows say so rather than naming a craft.
			continue
		}
		for _, k := range keys {
			key := fmt.Sprintf(k, craft)
			r, ok := Lookup(key)
			if !ok {
				t.Errorf("%s has no row: a craft's half is a key an owner looks up", key)
				continue
			}
			half := "model"
			if strings.HasSuffix(key, ".access") {
				half = "access"
			}
			want := "unset = " + followed[parent] + "'s " + half
			if r.Default.Unset != want {
				t.Errorf("%s: the DEFAULT column reads %q, want %q: config.CraftParent(%q) is %q", key, r.Default.Unset, want, craft, parent)
			}
		}
	}
}

// TestRegistryCarriesTheOrchestratorSwitchAndIssueSync pins R1 of
// docs/orchestrator-graph-estimates/'s two rows (D2701, D2733, D2739): the
// daemon's switch and the project's issue adapter.
//
// Both are top-level keys of the published policy node and of no other layer —
// the node is what every machine of the project reads, which is the whole point
// of the switch — so `policy` is the whole of their Layers. The switch merges by
// Tighten, and that is the row where "false wins at any layer" is held: false is
// its stricter value, so a layer that writes it off decides whatever another
// layer writes, and no layer turns orchestration back on over one. The adapter
// replaces, because which repositories are synced and whether an item opens an
// issue is one answer rather than the stricter of two.
//
// Both name the `project` family, whose `fugaro project policy publish` is the
// door that carries a node-only key onto the node, and neither a name nor a
// prefix in the generic allowlist reaches them — which the writer ratchet's
// testdata/writer_gaps.golden records until a command writes them.
func TestRegistryCarriesTheOrchestratorSwitchAndIssueSync(t *testing.T) {
	for _, tc := range []struct {
		key   string
		merge Merge
	}{
		{"orchestrator_enabled", Tighten},
		{"issue_sync", Replace},
	} {
		r, ok := Lookup(tc.key)
		if !ok {
			t.Errorf("%s has no row: a key of the published node that no row names is one the read and the write cannot see", tc.key)
			continue
		}
		if !slices.Equal(r.Layers, []string{LayerPolicy}) {
			t.Errorf("%s: Layers = %v, want the policy node alone: both are top-level keys of the node every machine of the project reads", tc.key, r.Layers)
		}
		if r.Merge != tc.merge {
			t.Errorf("%s: Merge = %q, want %q: the switch's stricter value is false, which is what makes a written false win at any layer, and the adapter is one answer rather than the stricter of two", tc.key, r.Merge, tc.merge)
		}
		if r.Default.Unset == "" || r.Default.Value != "" || r.Default.Const != "" {
			t.Errorf("%s: Default = %+v, want an unset sentence: neither key has a value a config field can be read back from", tc.key, r.Default)
		}
		if r.Owner != "project" || r.Doc == "" {
			t.Errorf("%s: Owner = %q and Doc = %q, want the `project` family and the plan that states the key: the publish door is what carries a node-only key onto the node", tc.key, r.Owner, r.Doc)
		}
	}
}

// TestSettingsSchemaNamesTheReportsCommitRows: the designated reports
// repository's two rows (periodic-reviews R15, D2751) are the surface
// `fugaro settings` publishes, and schemas/settings.schema.json is where a
// consumer of the settings document reads what a key means before it is
// written — its $defs.keyDefaults carries one entry per registry row with the
// default the registry gives it, which schemas' TestSettingsSchema holds to the
// whole table. The two rows are the `binaries.*` rows' shape: the owner's local
// project config alone, reached by no generic write, so the schema states them
// beside `binaries.allow` and `binaries.max_bytes` and each entry carries the
// sentence the registry gives it. A key dropped from either side — or one whose
// layer list widens past the local project config — fails here.
func TestSettingsSchemaNamesTheReportsCommitRows(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs struct {
			KeyDefaults struct {
				Properties map[string]struct {
					Default string `json:"default"`
				} `json:"properties"`
			} `json:"keyDefaults"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	props := doc.Defs.KeyDefaults.Properties
	if len(props) == 0 {
		t.Fatal("settings.schema.json has no $defs.keyDefaults.properties")
	}
	// The block whose shape the two rows follow, so the comparison is one a
	// reader can make in the document itself.
	if _, ok := props["binaries.allow"]; !ok {
		t.Fatal("settings.schema.json names no binaries.allow, and the reports_commit rows are that block's shape")
	}
	for _, key := range []string{"reports_commit.repo", "reports_commit.dir"} {
		entry, ok := props[key]
		if !ok {
			t.Errorf("settings.schema.json names no %s: the designation is a registry key, and the schema is where a consumer reads what its absence means", key)
			continue
		}
		r, ok := Lookup(key)
		if !ok {
			t.Errorf("%s: the schema entry has no registry row to answer for it", key)
			continue
		}
		want := r.Default.Value
		if want == "" {
			want = r.Default.Unset
		}
		if entry.Default != want {
			t.Errorf("%s: the schema's default is %q, and the registry says %q", key, entry.Default, want)
		}
		if !slices.Equal(r.Layers, []string{LayerLocal}) {
			t.Errorf("%s: Layers = %v, want the owner's local project config alone: a launcher-writable layer that carried the designation would aim every report of the project", key, r.Layers)
		}
	}
}

// TestTheRegistryAndTheRunAgreeOnUnset (AB11, R3, F2): the DEFAULT column and
// the schema have said "unset = observe" since before any of this work, and the
// run read an unset mode as the old word until AB1a. This is what holds the
// three to one answer rather than to each other: the mode a job that names
// nothing gets is read out of the run's own reader (runner.SpendFromEnv), and
// the registry row and the schema's default keyword must name that same word.
// The row's default is a decision rather than a value, so
// TestRegistryDefaultsMatchApplyDefaults leaves it alone by design; this is the
// pin it has instead.
func TestTheRegistryAndTheRunAgreeOnUnset(t *testing.T) {
	// The run's own answer for a job that carries no budget at all — no mode,
	// no cap, no ceiling — which is the shape of a job written before this
	// release (AB5 writes the mode from now on).
	s, err := runner.SpendFromEnv(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("a job that names no budget at all is refused: %v", err)
	}
	if !s.On() {
		t.Errorf("a job that names no mode is not accounted for: Spend = %+v", s)
	}
	// The string the run defaults to is not the word that was a mode: that
	// word was what an unset mode resolved to at the run until AB1a, while
	// the registry and the schema said observe (F2), and this is the line that
	// fails on the reading coming back.
	if policy.Mode(s.Mode).Removed() {
		t.Errorf("a job that names no mode defaults to the word that was a mode (%q), and the registry and the schema have said observe for it all along (F2, D3009)", s.Mode)
	}
	runMode := policy.Mode(s.Mode).Effective()
	if runMode != policy.ModeObserve {
		t.Fatalf("the run reads an unset mode as %q, want %q (D3009, R3)", runMode, policy.ModeObserve)
	}

	// The registry row, which is the sentence a person reads and the value the
	// DEFAULT column prints.
	r, ok := Lookup("budget.mode")
	if !ok {
		t.Fatal("budget.mode: no registry row")
	}
	if want := unset(runMode); r.Default != want {
		t.Errorf("budget.mode: the registry says %+v, and the run's answer for an unset mode is %q", r.Default, runMode)
	}

	// And the schema's own copy of the sentence, which is where a consumer of
	// the settings document reads what an absent key means: schemas'
	// TestSettingsSchema holds the whole table to the registry, and this holds
	// this key to the run.
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs struct {
			KeyDefaults struct {
				Properties map[string]struct {
					Default string `json:"default"`
				} `json:"properties"`
			} `json:"keyDefaults"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	entry, ok := doc.Defs.KeyDefaults.Properties["budget.mode"]
	if !ok {
		t.Fatal("settings.schema.json names no budget.mode")
	}
	if want := unset(runMode).Unset; entry.Default != want {
		t.Errorf("settings.schema.json budget.mode: the default is %q, and the run's answer for an unset mode is %q", entry.Default, runMode)
	}
}
