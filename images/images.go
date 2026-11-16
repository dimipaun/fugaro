// Package images holds the container image sources (design §7): the one
// Fugaro base (images/base/), the history image (images/history/) and the
// credentials service's image (images/credentials/), scripts the base images
// share (images/common/), and the derived-image template and Cloud Build
// config (images/derived/). It embeds the template so the fugaro binary can
// render it.
package images

import _ "embed"

// DerivedTemplate is images/derived/Dockerfile.tmpl, a text/template
// rendered by internal/image.Render.
//
//go:embed derived/Dockerfile.tmpl
var DerivedTemplate string

// The git config patterns that define a credential in the baked checkout.
// images/common/finalize-checkout.sh uses each one verbatim (a test pins
// that), and fugaro image selftest applies the same ones, so the build-time
// strip and the smoke test agree on what counts. The key patterns are
// extended regular expressions for `git config --get-regexp`, which matches
// them against lowercased key names; GitCredentialURL also compiles as Go
// regexp syntax.
const (
	// GitCredentialHelperKey matches any credential.* key.
	GitCredentialHelperKey = `^credential\.`
	// GitCredentialExtraHeaderKey matches http.extraheader, scoped to a URL
	// or not.
	GitCredentialExtraHeaderKey = `^http\.(.*\.)?extraheader$`
	// GitCredentialInsteadOfKey matches url.<base>.insteadof keys, which are
	// a credential only when the line also matches GitCredentialURL.
	GitCredentialInsteadOfKey = `^url\..*\.insteadof$`
	// GitCredentialPushInsteadOfKey is GitCredentialInsteadOfKey for
	// url.<base>.pushinsteadof.
	GitCredentialPushInsteadOfKey = `^url\..*\.pushinsteadof$`
	// GitCredentialURL matches a URL with userinfo anywhere in a
	// `git config --get-regexp` output line (key or value). It is
	// deliberately broad: ssh://git@host is refused too, because run-time
	// credential injection can't use it either.
	GitCredentialURL = `://[^/[:space:]]*@`
	// GitCredentialRemoteURLKey matches every remote's url and pushurl,
	// which are a credential when the line also matches GitCredentialURL.
	// All remotes count, not only origin's fetch URL: a token in a pushurl
	// would also send the runner's pushes to a stale embedded credential.
	GitCredentialRemoteURLKey = `^remote\..*\.(url|pushurl)$`
	// GitConfigCredentialText matches a git config file's own text that
	// carries a credential: a credential section, a helper, an
	// http.extraheader, or a URL with userinfo. The constants above are
	// `git config --get-regexp` patterns over a *key*, read lowercased, and
	// cannot be applied to the file as it is written; this is the same thing
	// a reader with no git to ask greps for. images/scan.sh's leak check and
	// images/trivy-secret.yaml's fugaro-git-config-credential rule both carry
	// it verbatim (a test pins that), so the two never disagree about what a
	// credential in a config is.
	GitConfigCredentialText = `\[credential|^[[:space:]]*helper[[:space:]]*=|extraheader|` + GitCredentialURL
)

// CloudBuild is images/derived/cloudbuild.yaml, which fugaro image build
// submits (with per-workflow secrets added) and M5's nightly trigger runs.
//
//go:embed derived/cloudbuild.yaml
var CloudBuild []byte

// BaseTools is images/base/tools.tsv, the base image's presence checks,
// which fugaro image selftest runs (internal/image.ParseTools).
//
//go:embed base/tools.tsv
var BaseTools string

// ScanScript is images/scan.sh, the leak scan, and TrivySecretConfig is
// images/trivy-secret.yaml, the rules and allowances it passes Trivy. The
// catalog build runs the two of them (internal/backend/gcp's catalog-scan
// step, B3), where the script cannot be read from a checkout: the CLI embeds
// them, and the step writes both out and runs the script, so the build scans
// its image with the same script CI does rather than with a second copy of
// the checks.
//
//go:embed scan.sh
var ScanScript []byte

//go:embed trivy-secret.yaml
var TrivySecretConfig []byte
