// Package secretname_test is external so the test can hold the grammar
// against the packages that build the names it may not collide with
// (internal/backend/gcp, internal/task) and against the platform credential
// names it must refuse (internal/config, internal/credsvc): internal/credsvc
// consumes this package (DS3.3), so an in-package test could not import it.
package secretname_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/secretname"
	"github.com/dimipaun/fugaro/internal/task"
)

func mustUser(t *testing.T, id, name string) string {
	t.Helper()
	s, err := secretname.User(id, name)
	if err != nil {
		t.Fatalf("User(%q, %q): %v", id, name, err)
	}
	return s
}

func mustProject(t *testing.T, name string) string {
	t.Helper()
	s, err := secretname.Project(name)
	if err != nil {
		t.Fatalf("Project(%q): %v", name, err)
	}
	return s
}

// G1: no two (id, name) pairs build one secret ID. The pairs below are the
// ones a grammar whose halves may hold the separator would merge — (12, 3)
// against (1, 23), a name that is all digits against an id — and the
// project-shared names are in the same table, because a project name may not
// take a per-user ID's either.
func TestNoTwoPairsOneSecretID(t *testing.T) {
	ids := []string{"1", "12", "123", "123456789012345678901"}
	names := []string{"0", "1", "23", "3", "3-x", "x-3", "x", "claude-oauth-token-acme", "workflow-token"}
	seen := map[string]string{}
	for _, id := range ids {
		for _, name := range names {
			secretID := mustUser(t, id, name)
			where := "User(" + id + ", " + name + ")"
			if prev, ok := seen[secretID]; ok {
				t.Errorf("%s and %s both build %s", prev, where, secretID)
				continue
			}
			seen[secretID] = where
		}
	}
	for _, name := range names {
		secretID := mustProject(t, name)
		where := "Project(" + name + ")"
		if prev, ok := seen[secretID]; ok {
			t.Errorf("%s and %s both build %s", prev, where, secretID)
			continue
		}
		seen[secretID] = where
	}
}

// R1: no name the existing scheme builds may fall inside the two prefixes the
// intake's IAM condition admits. The existing scheme is
// fugaro-<readable slug>-<logical>-<hash> over [a-z0-9-], so a repository
// owned by p, u or p-labs builds fugaro-p-labs-app-… (F6): the first draft's
// fugaro-p-/fugaro-u- admitted it, and an admin could then have overwritten a
// repository's mounted token through the intake. p-labs/app is first because
// it is the first case that draft admits, which is the plan's red step.
func TestNoOverlapWithExistingSecretIDs(t *testing.T) {
	for _, repo := range []string{"p-labs/app", "p/app", "u/app"} {
		slug, err := task.Slug("github", repo)
		if err != nil {
			t.Fatalf("Slug(github, %s): %v", repo, err)
		}
		for _, logical := range []string{"workflow-token", "github-app-key", "bitbucket-token", "claude-oauth-token"} {
			notInsideTheGrammar(t, "gcp.SecretID("+repo+", "+logical+")", gcp.SecretID(slug, logical))
		}
		// The two bounds are one number: gcp's builders truncate to Secret
		// Manager's limit and this package refuses anything longer, so a
		// change on either side would leave a name one builds and the other
		// cannot speak.
		if got := len(gcp.SecretID(slug, strings.Repeat("x", 2*secretname.MaxIDLen))); got != secretname.MaxIDLen {
			t.Errorf("gcp.SecretID builds a %d-character name, and secretname.MaxIDLen is %d", got, secretname.MaxIDLen)
		}
	}
	for _, project := range []string{"p-labs", "p", "u"} {
		notInsideTheGrammar(t, "gcp.ProjectSecretID("+project+")", gcp.ProjectSecretID(project, "workflow-token"))
		notInsideTheGrammar(t, "gcp.CredsBundleSecretID("+project+")", gcp.CredsBundleSecretID(project))
	}
}

func notInsideTheGrammar(t *testing.T, what, secretID string) {
	t.Helper()
	if strings.HasPrefix(secretID, secretname.UserPrefix) || strings.HasPrefix(secretID, secretname.ProjectPrefix) {
		t.Errorf("%s is %s, inside the intake's prefix", what, secretID)
	}
	if got, err := secretname.Parse(secretID); err == nil {
		t.Errorf("%s is %s, which the grammar reads as %+v", what, secretID, got)
	}
}

// R7: the platform credential kinds may not be spelled as a user or project
// secret. The service resolves a user-supplied credential by its per-secret
// name first and its bundle entry second (R6), so one of these names would
// let a member shadow the GitHub App key, the git token, the Bitbucket read
// consumer or a review-signing key with a value of their own.
func TestReservedPlatformNamesRefused(t *testing.T) {
	for _, name := range []string{
		config.GitHubAppKeySecret,
		config.BitbucketSecret,
		credsvc.BitbucketReadConsumerSecret,
		config.ReviewSigningKeySecret(config.DefaultClaudeAccount),
		config.ReviewSigningKeySecret("acme"),
		config.ReviewSigningKeyPreviousSecret("acme"),
	} {
		if got, err := secretname.Project(name); err == nil {
			t.Errorf("Project(%q) = %q, want a refusal: %s is a platform credential kind", name, got, name)
		}
		if got, err := secretname.User("1234567890", name); err == nil {
			t.Errorf("User(..., %q) = %q, want a refusal: %s is a platform credential kind", name, got, name)
		}
		// The refusal is the grammar's and not the writer's: a container of
		// that name can already exist (the deployer can pre-create one,
		// F35), and the read side refuses it too rather than resolving it.
		if got, err := secretname.Parse(secretname.ProjectPrefix + name); err == nil {
			t.Errorf("Parse(%q) = %+v, want a refusal", secretname.ProjectPrefix+name, got)
		}
	}
}

// F29: the model token names are NOT reserved — they are exactly what R6
// rotates through the intake, so reserving them would make that rotation
// impossible. A name merely shaped like one of them is accepted too.
func TestModelTokenNamesAccepted(t *testing.T) {
	for _, name := range []string{
		config.AnthropicSecret,
		config.ClaudeOAuthToken,
		config.ClaudeOAuthTokenPrefix + "acme",
		config.CodexOAuthToken,
		config.CodexOAuthTokenPrefix + "acme",
		"openrouter-api-key",
	} {
		if _, err := secretname.Project(name); err != nil {
			t.Errorf("Project(%q) was refused (%v): R6 rotates a model token through the intake (F29)", name, err)
		}
		if _, err := secretname.User("1234567890", name); err != nil {
			t.Errorf("User(..., %q) was refused (%v): R6 rotates a model token through the intake (F29)", name, err)
		}
	}
}

// R1: the underscore is the separator — it is the last character of both
// prefixes — and neither half may hold one. That is the whole of "so the
// split is unique": a half that could hold the separator could move the
// boundary, and one secret ID would then answer for two credentials.
func TestSeparatorNotInName(t *testing.T) {
	for _, c := range []struct{ id, name string }{
		{"12", "my_key"},
		{"12", "key_"},
		{"12", "a_b-c"},
		{"1_2", "key"},
		{"12_", "key"},
		{"_12", "key"},
	} {
		if got, err := secretname.User(c.id, c.name); err == nil {
			t.Errorf("User(%q, %q) = %q, want a refusal: neither half may hold the separator", c.id, c.name, got)
		}
	}
	for _, name := range []string{"my_key", "key_", "_key", "a_b"} {
		if got, err := secretname.Project(name); err == nil {
			t.Errorf("Project(%q) = %q, want a refusal: a name may not hold the separator", name, got)
		}
	}
	// And a name that is already a secret does not read back either: the
	// boundary the separator draws is the one Parse splits at.
	if got, err := secretname.Parse(secretname.UserPrefix + "12_my_key"); err == nil {
		t.Errorf("Parse(%q) = %+v, want a refusal", secretname.UserPrefix+"12_my_key", got)
	}
}

// Every refusal echoes the caller's own bytes escaped, never raw: a name is
// caller-supplied, the intake's refusal path and the CLI both print the error,
// and a terminal escape, a bidi override or a credential pasted where a name
// belonged must not arrive as itself.
func TestRefusalEscapesTheCallersBytes(t *testing.T) {
	const hostile = "key\x1b[2K\u202egnp.exe"
	for _, c := range []struct {
		what string
		call func() error
	}{
		{"Project", func() error { _, err := secretname.Project(hostile); return err }},
		{"User name", func() error { _, err := secretname.User("12", hostile); return err }},
		{"User id", func() error { _, err := secretname.User(hostile, "key"); return err }},
		{"Parse", func() error { _, err := secretname.Parse(hostile); return err }},
		{"Parse without the separator", func() error {
			_, err := secretname.Parse(secretname.UserPrefix + hostile)
			return err
		}},
	} {
		err := c.call()
		if err == nil {
			t.Errorf("%s accepted %q, want a refusal", c.what, hostile)
			continue
		}
		if strings.ContainsAny(err.Error(), "\x1b\u202e") {
			t.Errorf("%s: the refusal echoes the caller's bytes raw: %q", c.what, err.Error())
		}
		if !strings.Contains(err.Error(), `\u001b`) || !strings.Contains(err.Error(), `\u202e`) {
			t.Errorf("%s: the refusal does not show the bytes escaped: %q", c.what, err.Error())
		}
	}
}

// R1: a name that would build a secret ID over Secret Manager's own limit is
// refused, and the sentence says what the limit is.
func TestLengthLimit(t *testing.T) {
	// The longest per-user ID: the prefix, the shortest id, the separator and
	// as much name as fits.
	longest := strings.Repeat("a", secretname.MaxIDLen-len(secretname.UserPrefix)-1-len("1"))
	secretID, err := secretname.User("1", longest)
	if err != nil {
		t.Fatalf("a %d-character secret ID was refused: %v", secretname.MaxIDLen, err)
	}
	if len(secretID) != secretname.MaxIDLen {
		t.Fatalf("User built a %d-character ID, want %d", len(secretID), secretname.MaxIDLen)
	}
	_, err = secretname.User("1", longest+"a")
	if err == nil {
		t.Fatalf("a %d-character secret ID was accepted", secretname.MaxIDLen+1)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(secretname.MaxIDLen)) {
		t.Errorf("the refusal of an over-long ID does not name the limit %d: %v", secretname.MaxIDLen, err)
	}
	if got, err := secretname.Project(strings.Repeat("a", secretname.MaxIDLen)); err == nil {
		t.Errorf("Project of a %d-character name = %q, want a refusal", secretname.MaxIDLen, got)
	}
}

// R1: every ID User and Project build parses back to the pair it was built
// from, and nothing else parses — so a caller holding a Secret Manager name
// (the intake's list and delete, DS2.1; the service's per-secret read, DS3.3)
// can ask the grammar whether it is one of its own.
func TestParseRoundTrip(t *testing.T) {
	for _, c := range []struct{ id, name string }{
		{"1", "a"},
		{"12", "3-x"},
		{"123456789012345678901", "claude-oauth-token-acme"},
	} {
		got, err := secretname.Parse(mustUser(t, c.id, c.name))
		if err != nil {
			t.Fatalf("Parse(User(%q, %q)): %v", c.id, c.name, err)
		}
		if want := (secretname.Name{ID: c.id, Name: c.name}); got != want {
			t.Errorf("Parse(User(%q, %q)) = %+v, want %+v", c.id, c.name, got, want)
		}
		if got.Shared() {
			t.Errorf("Parse(User(%q, %q)) reports a project-shared name", c.id, c.name)
		}
	}
	for _, name := range []string{"a", "3-x", "claude-oauth-token-acme"} {
		got, err := secretname.Parse(mustProject(t, name))
		if err != nil {
			t.Fatalf("Parse(Project(%q)): %v", name, err)
		}
		if want := (secretname.Name{Name: name}); got != want {
			t.Errorf("Parse(Project(%q)) = %+v, want %+v", name, got, want)
		}
		if !got.Shared() {
			t.Errorf("Parse(Project(%q)) = %+v, want a project-shared name", name, got)
		}
	}
	for _, secretID := range []string{
		"",
		"fugaro",
		"fugaro_x_12_x",
		"fugaro_u_12",
		"fugaro_u__x",
		"fugaro_u_12_",
		"fugaro_u_ab_x",
		"fugaro_u_12_-x",
		"fugaro_p_",
		"fugaro_p_My-Key",
		"fugaro-project-acme-creds-bundle-0123456789abcdef",
	} {
		if got, err := secretname.Parse(secretID); err == nil {
			t.Errorf("Parse(%q) = %+v, want a refusal", secretID, got)
		}
	}
}
