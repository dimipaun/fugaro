package localcfg

import (
	"strings"
	"testing"
)

// profileConfig is the sample config with one profiles entry, whose fields are
// given by the caller so a test writes only the allow_repos spelling in
// question. The block is the operator's copy of the policy's job profiles
// (config.JobProfile, published by `fugaro project policy publish`).
func profileConfig(fields string) string {
	return sample + "profiles:\n  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h" + fields + " }\n"
}

// TestParseRefusesAProfileAllowReposWrittenEmpty: the key absent means the
// project's allow_repos applies (design §4.1), and a list written with nothing
// in it means the operator wrote a list and left it empty — which the carrier's
// omitempty would carry to the node as the absent key, widening the profile to
// every repository the project enrolls. The mutation is dropping the
// allowReposWritten reader from validate: a blank allow_repos then parses as
// the absent key, publish succeeds, and the profile admits the project's whole
// list.
func TestParseRefusesAProfileAllowReposWrittenEmpty(t *testing.T) {
	for _, fields := range []string{", allow_repos: []", ", allow_repos:", ", allow_repos: ~"} {
		_, err := Parse([]byte(profileConfig(fields)))
		if err == nil {
			t.Errorf("a profile with %q parsed", fields)
			continue
		}
		if !strings.Contains(err.Error(), "profiles.standard.allow_repos") {
			t.Errorf("the refusal for %q does not name the key it refused: %v", fields, err)
		}
	}
	// The key absent is the way a profile says the project's list applies, and
	// it records no written list: the two spellings above are refused, not the
	// absence of the key.
	c, err := Parse([]byte(profileConfig("")))
	if err != nil {
		t.Fatalf("a profile without allow_repos was refused: %v", err)
	}
	if pr := c.Profiles["standard"]; pr.AllowRepos != nil || pr.AllowReposWritten() {
		t.Errorf("a profile without allow_repos = %+v, want no list and no written key", pr)
	}
	// A written list is carried whole, and says that it was written.
	c, err = Parse([]byte(profileConfig(`, allow_repos: ["github/acme/app"]`)))
	if err != nil {
		t.Fatalf("a profile with an allow_repos list was refused: %v", err)
	}
	if pr := c.Profiles["standard"]; len(pr.AllowRepos) != 1 || !pr.AllowReposWritten() {
		t.Errorf("a profile with allow_repos = %+v, want the one repository and the written key", pr)
	}
}

// TestParseRefusesAnUnknownOrMisspeltProfileKey: the profile entry is decoded
// through config.JobProfile's UnmarshalYAML, which sees the mapping node (it
// has to, to tell a written blank list from an absent key) and therefore has to
// repeat the known-fields check the decoder would otherwise apply. The mutation
// is dropping jobProfileKeys from UnmarshalYAML: a misspelt timeout_class then
// parses, publishes a job with no timeout and runs it with Cloud Run's own
// default. The case-variant spelling is refused by yaml.v3 itself, whose known
// fields are compared exactly — the JSON side of the same rule is
// projectpolicy's (it decodes with encoding/json, which matches
// case-insensitively).
func TestParseRefusesAnUnknownOrMisspeltProfileKey(t *testing.T) {
	for _, fields := range []string{", timeout: 6h", ", Allow_Repos: [\"github/acme/app\"]", ", allow_repo: [\"github/acme/app\"]"} {
		if _, err := Parse([]byte(profileConfig(fields))); err == nil {
			t.Errorf("a profile with %q parsed", fields)
		}
	}
	// The documented spelling of every member parses, so the check is not a
	// blanket refusal of the block.
	if _, err := Parse([]byte(profileConfig(`, secrets: [deploy-key], allow_repos: ["github/acme/*"]`))); err != nil {
		t.Fatalf("a fully spelled profile was refused: %v", err)
	}
}
