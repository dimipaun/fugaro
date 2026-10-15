package projectpolicy

import (
	"strings"
	"testing"
)

// decodeProfiles is a policy node carrying the profiles block under test, with
// the minimum the decoder otherwise needs.
func decodeProfiles(t *testing.T, profiles string) (*Policy, error) {
	t.Helper()
	raw := `{"version":` + itoa(Version) + `,"budget":{"mode":"enforce"},"allow_repos":["github/acme/app"],"profiles":` + profiles + `}`
	p, found, err := Decode([]byte(raw))
	if err != nil {
		return nil, err
	}
	if !found {
		t.Fatal("Decode found no policy")
	}
	return p, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestProfileForRefusesANamedProfileThatIsNotDefined: the fail-closed half of
// step 1. A policy that does not define the profile a workflow selected must
// refuse the run, never fall back to another profile — the mutation is a
// ProfileFor that returns the default (or a zero Profile) instead of an error.
func TestProfileForRefusesANamedProfileThatIsNotDefined(t *testing.T) {
	p, err := decodeProfiles(t, `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"}}`)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := p.ProfileFor("standard")
	if err != nil {
		t.Fatalf("ProfileFor(standard) = %v", err)
	}
	if pr.Provider != "github" || pr.CPU != 2 {
		t.Errorf("ProfileFor(standard) = %+v, want the github profile", pr)
	}
	// "" is the default, and the policy defines it.
	if _, err := p.ProfileFor(""); err != nil {
		t.Errorf("ProfileFor(\"\") = %v, want the default profile (%s)", err, DefaultProfileName)
	}
	if _, err := p.ProfileFor("big"); err == nil {
		t.Fatal("ProfileFor(big) succeeded for a profile the policy does not define")
	} else if !strings.Contains(err.Error(), "big") || !strings.Contains(err.Error(), DefaultProfileName) {
		t.Errorf("the refusal names neither the missing profile nor the defined ones: %v", err)
	}
	// A policy with no profiles at all defines nothing, not even the default.
	empty := &Policy{}
	if _, err := empty.ProfileFor(""); err == nil {
		t.Fatal("ProfileFor on a policy with no profiles succeeded")
	}
}

// TestProfileAdmissionFallsBackToTheProjectList: design §4.1/step 3 — an empty
// profile allow_repos means the project's list applies, never everything. The
// mutation is a fallback to ["*"] (or to "admit anything when the list is
// empty"), which would admit every repository to a profile whose owner wrote
// nothing.
func TestProfileAdmissionFallsBackToTheProjectList(t *testing.T) {
	p, err := decodeProfiles(t, `{
		"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["github/acme/*"]},
		"quiet":{"provider":"github","cpu":1,"memory":"1Gi","timeout_class":"1h"}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	p.AllowRepos = []string{"github/acme/app"}

	// A profile with its own list uses it, and only it.
	own, err := p.Admission("standard")
	if err != nil {
		t.Fatal(err)
	}
	if len(own) != 1 || own[0] != "github/acme/*" {
		t.Errorf("Admission(standard) = %v, want the profile's own list", own)
	}
	// A profile without one falls back to the project's — and the project's is
	// exactly one repository, so anything wider is a loosening.
	fallback, err := p.Admission("quiet")
	if err != nil {
		t.Fatal(err)
	}
	if len(fallback) != 1 || fallback[0] != "github/acme/app" {
		t.Errorf("Admission(quiet) = %v, want the project's allow_repos", fallback)
	}
	if AdmitRepo(fallback, "github/other/app") {
		t.Error("the fallback admits a repository the project's list does not")
	}
	// An empty project list admits nothing: the fallback is not "everything".
	p.AllowRepos = nil
	none, err := p.Admission("quiet")
	if err != nil {
		t.Fatal(err)
	}
	if AdmitRepo(none, "github/acme/app") {
		t.Error("an empty project allow_repos admitted a repository through the fallback")
	}
	// An undefined profile is a refusal here too, not an empty admission list.
	if _, err := p.Admission("nope"); err == nil {
		t.Error("Admission of an undefined profile succeeded")
	}
}

// TestProfileRefusesAnInvalidResourceForm: step 4 — a policy cannot name a job
// the Cloud Run API will refuse. The forms are internal/backend/gcp's own
// (limits.go), so this test is also the drift guard between the two.
func TestProfileRefusesAnInvalidResourceForm(t *testing.T) {
	base := func(cpu int, memory, timeout string) string {
		fields := `"provider":"github","cpu":` + itoa(cpu) + `,"memory":"` + memory + `","timeout_class":"` + timeout + `"`
		return `{"standard":{` + fields + `}}`
	}
	if _, err := decodeProfiles(t, base(2, "4Gi", "6h")); err != nil {
		t.Fatalf("a valid profile was refused: %v", err)
	}
	for _, tc := range []struct{ what, doc string }{
		{"cpu 3 is not a Cloud Run job choice", base(3, "4Gi", "6h")},
		{"cpu 0", base(0, "4Gi", "6h")},
		{"memory over 32Gi", base(8, "64Gi", "6h")},
		{"memory below 512Mi", base(1, "128Mi", "6h")},
		{"9Gi needs 4 CPUs", base(1, "9Gi", "6h")},
		{"a memory string that is not a quantity", base(2, "lots", "6h")},
		{"a timeout that is not a duration", base(2, "4Gi", "soon")},
		{"a zero timeout", base(2, "4Gi", "0s")},
		{"a timeout past Cloud Run's 168h ceiling", base(2, "4Gi", "200h")},
	} {
		if _, err := decodeProfiles(t, tc.doc); err == nil {
			t.Errorf("%s was accepted", tc.what)
		}
	}
}

// TestDecodeRefusesProfilesWithoutTheDefault: step 1 — a published policy that
// carries a profiles block must define the profile every workflow defaults to.
// The mutation is accepting the block, which would leave those workflows
// selecting a profile nobody defined.
func TestDecodeRefusesProfilesWithoutTheDefault(t *testing.T) {
	if _, err := decodeProfiles(t, `{"big":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"}}`); err == nil {
		t.Fatalf("a profiles block without %s was accepted", DefaultProfileName)
	} else if !strings.Contains(err.Error(), DefaultProfileName) {
		t.Errorf("the refusal does not name %s: %v", DefaultProfileName, err)
	}
	if _, err := decodeProfiles(t, `{}`); err == nil {
		t.Fatal("an empty profiles block was accepted")
	}
	// No profiles block at all is a phase-1 node: it sets no profile, and the
	// key it leaves out keeps what the job carries (the policy's own contract).
	p, _, err := Decode([]byte(`{"version":` + itoa(Version) + `,"budget":{"mode":"enforce"}}`))
	if err != nil {
		t.Fatalf("a policy without profiles was refused: %v", err)
	}
	if len(p.Profiles) != 0 {
		t.Errorf("profiles = %v, want none", p.Profiles)
	}
}

// TestDecodeRefusesAProfileWithoutAProvider: finding 4 — the provider is
// required, because it decides which repositories a job serves and is part of
// the job's identity.
func TestDecodeRefusesAProfileWithoutAProvider(t *testing.T) {
	for _, doc := range []string{
		`{"standard":{"cpu":2,"memory":"4Gi","timeout_class":"6h"}}`,
		`{"standard":{"provider":"gitlab","cpu":2,"memory":"4Gi","timeout_class":"6h"}}`,
		`{"standard":{"provider":"GITHUB","cpu":2,"memory":"4Gi","timeout_class":"6h"}}`,
	} {
		if _, err := decodeProfiles(t, doc); err == nil {
			t.Errorf("%s was accepted without a provider kind", doc)
		}
	}
}

// TestDecodeRefusesAnAllowRepoPatternOfAnotherProvider: step 3 — a profile's
// allow_repos may only name repositories of its own provider, because a
// pattern of another provider could never match a repository the profile
// serves (task 3.2's job is one provider's).
func TestDecodeRefusesAnAllowRepoPatternOfAnotherProvider(t *testing.T) {
	bad := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["bitbucket/acme/app"]}}`
	if _, err := decodeProfiles(t, bad); err == nil {
		t.Fatal("a bitbucket pattern was accepted on a github profile")
	}
	good := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["github/acme/*","*"]}}`
	if _, err := decodeProfiles(t, good); err != nil {
		t.Fatalf("a provider-qualified pattern was refused: %v", err)
	}
	// A malformed pattern is refused too, by the allow-list's own rule.
	malformed := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["acme/*"]}}`
	if _, err := decodeProfiles(t, malformed); err == nil {
		t.Fatal("a bare owner/name pattern was accepted")
	}
}
