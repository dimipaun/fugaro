// Package secretname is the grammar of the per-user and project-shared
// Secret Manager names the intake writes and the credentials service reads
// (docs/secrets-brokering/dynamic-delivery, R1 and R7).
//
// Two prefixes, and one name rule:
//
//	fugaro_u_<id>_<name>   a credential of the person whose Google account id is <id>
//	fugaro_p_<name>        a credential the project shares; only an admin writes one
//
// The underscore is the whole of the grammar's safety, and it is why the
// prefix is not the hyphenated fugaro-u-/fugaro-p- the addendum's first draft
// used. Every name the existing scheme builds — gcp.SecretID,
// gcp.ProjectSecretID and gcp.CredsBundleSecretID — is sanitized to [a-z0-9-],
// so none of them can hold an underscore and none can fall inside these two
// prefixes. The hyphenated draft could: the existing scheme is
// fugaro-<readable slug>-<logical>-<hash>, so a repository owned by p, u or
// p-labs builds fugaro-p-labs-app-… (F6), and the intake's IAM condition
// would have admitted a repository's mounted token, which an admin — or the
// agent on an admin's machine — could then overwrite or delete through the
// intake. The id is digits only and a name may not hold an underscore either,
// so the split at the prefix's own separator is unique: no two (id, name)
// pairs build one secret ID (G1).
//
// Its two consumers are the intake (DS2.1), which derives the prefix from the
// verified sub and refuses a name outside this grammar, and the credentials
// service (DS3.3), which resolves a user-supplied credential by its
// per-secret name first and by its bundle entry second (R6).
package secretname

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

const (
	// UserPrefix is the prefix of a credential one person owns. The id is
	// the numeric Google account id from the caller's verified identity
	// token (R1): never an email, and never what the request says about
	// itself (R3).
	UserPrefix = "fugaro-u-"
	// ProjectPrefix is the prefix of a credential the project shares. Only
	// an admin on the derived admin list may write one (D1935, D1940).
	ProjectPrefix = "fugaro-p-"
	// MaxIDLen is Secret Manager's own bound on a secret ID: at most 255
	// characters of [A-Za-z0-9_-]
	// (cloud.google.com/secret-manager/docs/reference/rest/v1/projects.secrets/create,
	// the same limit internal/backend/gcp truncates its names to). A name
	// that would build a longer ID is refused here, where the sentence can
	// name the limit, rather than by the API after the write.
	MaxIDLen = 255
)

// bitbucketReadConsumerSecret is credsvc.BitbucketReadConsumerSecret: the
// bundle key of the Bitbucket read consumer (F21, D361), one of the platform
// credential kinds this grammar may not spell (R7). It is written as a
// literal rather than imported because internal/credsvc consumes this package
// (DS3.3), so importing it back would be a cycle; the two spellings are held
// together by TestReservedPlatformNamesRefused, which reads credsvc's own
// constant and asks this package to refuse it.
const bitbucketReadConsumerSecret = "bitbucket-read-consumer"

var (
	// idRE is the owner half: digits only, so it can never hold the
	// separator and the split stays unique.
	idRE = regexp.MustCompile(`^[0-9]+$`)
	// nameRE is the name half: lower-case letters, digits and dashes, the
	// first character a letter or a digit. It is config.SecretNameRE's
	// charset — the one a credential name is spelled in everywhere else, in
	// `fugaro secrets`, a workflow's `secrets:` and a provider's — without
	// that rule's 63-character cap, because the bound that matters for a
	// secret ID is the whole ID's (MaxIDLen). A name outside this charset is
	// one no other path could write, so it would be a secret nothing could
	// name again.
	nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// reservedPlatformName reports whether name is one of the platform's own
// credential kinds, which this grammar may not spell (R7): the service
// resolves a user-supplied credential by its per-secret name first and by its
// bundle entry second (R6), so a per-secret name equal to a platform entry's
// would let a member shadow the GitHub App key, the git token, the Bitbucket
// read consumer or a review-signing key with a value of their own.
//
// The model credentials are deliberately NOT reserved (F29): they are the
// names R6 rotates *through* the intake (config.AnthropicSecret,
// config.ClaudeOAuthToken and its named accounts, a provider's own key), so
// reserving them would make the rotation R6 exists for impossible.
func reservedPlatformName(name string) bool {
	if name == config.GitHubAppKeySecret || name == config.BitbucketSecret || name == bitbucketReadConsumerSecret {
		return true
	}
	_, _, signing := config.ReviewSigningKeyAccount(name)
	return signing
}

// User is the secret ID of the credential name one person owns, and Project
// is the secret ID of a project-shared one. Both refuse a name the grammar
// does not admit; neither reads or carries a value.
func User(id, name string) (string, error) {
	if err := checkID(id); err != nil {
		return "", err
	}
	if err := checkName(name); err != nil {
		return "", err
	}
	return checkLen(UserPrefix + id + "-" + name)
}

// Project is User's project-shared half: the prefix only an admin may write.
func Project(name string) (string, error) {
	if err := checkName(name); err != nil {
		return "", err
	}
	return checkLen(ProjectPrefix + name)
}

func checkID(id string) error {
	if !idRE.MatchString(id) {
		return fmt.Errorf("the owner of a per-user secret is the numeric Google account id, not %q", id)
	}
	return nil
}

func checkName(name string) error {
	switch {
	case name == "":
		return errors.New("a credential name is empty")
	case reservedPlatformName(name):
		return fmt.Errorf("%s is a platform credential kind, and no user or project secret may be stored under it (R7)", name)
	case !nameRE.MatchString(name):
		return fmt.Errorf("%s is not a credential name: lower-case letters, digits and dashes, starting with a letter or a digit", name)
	}
	return nil
}

func checkLen(secretID string) (string, error) {
	if len(secretID) > MaxIDLen {
		return "", tooLong(len(secretID))
	}
	return secretID, nil
}

func tooLong(n int) error {
	return fmt.Errorf("the secret ID would be %d characters and Secret Manager allows at most %d", n, MaxIDLen)
}

// Name is a secret ID read back through the grammar: the credential name it
// holds and, for a per-user one, the account that owns it.
type Name struct {
	// ID is the numeric Google account id that owns the secret, and is empty
	// for a project-shared one.
	ID string
	// Name is the credential name.
	Name string
}

// Shared reports whether the parsed name is a project-shared one.
func (n Name) Shared() bool { return n.ID == "" }

// Parse reads a secret ID back to the pair it was built from: every ID User
// and Project build parses, and nothing else does — not a name outside the
// grammar, not a reserved platform name, and not a Secret Manager name of any
// other shape. It is how the intake (DS2.1) tells which prefix a name it is
// looking at belongs to, and how the service (DS3.3) tells a per-secret name
// from everything else.
func Parse(secretID string) (Name, error) {
	if len(secretID) > MaxIDLen {
		return Name{}, tooLong(len(secretID))
	}
	if name, ok := strings.CutPrefix(secretID, ProjectPrefix); ok {
		if err := checkName(name); err != nil {
			return Name{}, err
		}
		return Name{Name: name}, nil
	}
	rest, ok := strings.CutPrefix(secretID, UserPrefix)
	if !ok {
		return Name{}, fmt.Errorf("%s is neither a per-user nor a project-shared secret ID", secretID)
	}
	id, name, ok := strings.Cut(rest, "-")
	if !ok {
		return Name{}, fmt.Errorf("%s is not a per-user secret ID: they are %s<id>-<name>", secretID, UserPrefix)
	}
	if err := checkID(id); err != nil {
		return Name{}, err
	}
	if err := checkName(name); err != nil {
		return Name{}, err
	}
	return Name{ID: id, Name: name}, nil
}
