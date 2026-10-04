// Package preflight holds the read-only readiness checks fugaro init and
// fugaro doctor share (design doc m11-setup-and-skills.md §3.1 stage 0):
// the environment, Terraform, Docker, and, given the Google API clients
// the caller already holds, Cloud Billing and the project's IAM policy.
// Every check only reads; none of them enable, create or change anything
// the caller didn't already have, and none of them talk to Google except
// through a client the caller passes in, so a fake is all a test needs.
package preflight

import (
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Check is one readiness check's result: ok, or a one-line Problem and a
// one-line Fix (a command or a short instruction) a person can act on
// without the fugaro source open.
type Check struct {
	ID      string
	OK      bool
	Problem string
	Fix     string
}

// projectEnvVars name a project to the Google tools, including the one
// gcloud itself reads for its "default project" (CLOUDSDK_CORE_PROJECT).
// One naming another project than the installation's is how a command
// lands in the wrong project (the standing rule: never rely on it
// silently), so it is refused, not merely overridden.
var projectEnvVars = []string{"GOOGLE_PROJECT", "GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT"}

// impersonateEnv, when set, makes every Google client impersonate another
// identity; fugaro init and doctor must act as the caller's own
// credentials only.
const impersonateEnv = "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT"

// Environment checks the variables that would point Terraform or the
// Google tools at a project other than project, the impersonation
// variable, and the GODEBUG setting that would print credentials to
// stderr (cli.refuseHTTP2Debug's own check, mirrored here so a plan view
// or doctor can report it without running a command first).
func Environment(getenv func(string) string, project string) []Check {
	out := make([]Check, 0, 3)
	envProject := Check{ID: "env-project", OK: true}
	for _, k := range projectEnvVars {
		if v := getenv(k); v != "" && v != project {
			envProject = Check{
				ID:      "env-project",
				Problem: k + " is set to " + v + ", not the installation's project " + project,
				Fix:     "unset " + k + " (or set it to " + project + ") and rerun",
			}
			break
		}
	}
	out = append(out, envProject)
	if getenv(impersonateEnv) != "" {
		out = append(out, Check{
			ID:      "env-impersonation",
			Problem: impersonateEnv + " is set",
			Fix:     "fugaro runs as your own credentials only; unset it and rerun",
		})
	} else {
		out = append(out, Check{ID: "env-impersonation", OK: true})
	}
	if strings.Contains(getenv("GODEBUG"), "http2debug") {
		out = append(out, Check{
			ID:      "env-http2debug",
			Problem: "GODEBUG sets http2debug, which makes Go print HTTP requests, credentials and secrets included, to stderr",
			Fix:     "unset it (or drop http2debug) and rerun",
		})
	} else {
		out = append(out, Check{ID: "env-http2debug", OK: true})
	}
	return out
}

// firstLine is s up to its first newline: a googleapi.Error's Error()
// prints a multi-line "Details:" dump, which a one-line Problem must not
// carry.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// terraformMinMinor bounds the supported Terraform versions together with
// the major-version check in supportedTerraform, as internal/infra/tf.New
// enforces before any plan or apply (>= 1.7.0, < 2.0.0: import for_each,
// removed blocks, mock providers).
const terraformMinMinor = 7

// Terraform finds terraform on PATH with lookPath and, if found, runs only
// `terraform version -json` (the one subcommand every check here may run)
// to check it reports a supported version. bin is "" when it isn't found.
func Terraform(lookPath func(string) (string, error)) (bin string, c Check) {
	bin, err := lookPath("terraform")
	if err != nil {
		return "", Check{ID: "terraform", Problem: "terraform is not on PATH", Fix: "install terraform >= 1.7.0 and < 2.0.0 and put it on PATH"}
	}
	out, err := exec.Command(bin, "version", "-json").Output()
	if err != nil {
		return bin, Check{ID: "terraform", Problem: "terraform version -json failed: " + firstLine(err.Error()), Fix: "reinstall terraform >= 1.7.0 and < 2.0.0"}
	}
	v, err := parseTerraformVersion(out)
	if err != nil {
		return bin, Check{ID: "terraform", Problem: firstLine(err.Error()), Fix: "reinstall terraform >= 1.7.0 and < 2.0.0"}
	}
	if !supportedTerraform(v) {
		return bin, Check{ID: "terraform", Problem: "terraform " + v + " is not supported", Fix: "install terraform >= 1." + strconv.Itoa(terraformMinMinor) + ".0 and < 2.0.0"}
	}
	return bin, Check{ID: "terraform", OK: true}
}

// terraformVersionRE matches a release version, as internal/infra/tf does.
var terraformVersionRE = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.-]+)?$`)

// parseTerraformVersion reads terraform_version out of `terraform version
// -json`'s output.
func parseTerraformVersion(out []byte) (string, error) {
	var v struct {
		Version string `json:"terraform_version"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", errors.New("terraform version -json: " + err.Error())
	}
	return v.Version, nil
}

// supportedTerraform reports whether v is within bounds (>= 1.7.0, <
// 2.0.0); an unparseable version is not supported.
func supportedTerraform(v string) bool {
	m := terraformVersionRE.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	pre := m[4] != ""
	if major != 1 || minor < terraformMinMinor || (minor == terraformMinMinor && patch == 0 && pre) {
		return false
	}
	return true
}

// Docker checks docker is on PATH, only when needed (a local base image
// build): the cloud path never calls it, so a doctor run that isn't about
// to build one shouldn't fail on a laptop without Docker.
func Docker(lookPath func(string) (string, error), needed bool) Check {
	if !needed {
		return Check{ID: "docker", OK: true}
	}
	if _, err := lookPath("docker"); err != nil {
		return Check{ID: "docker", Problem: "docker is not on PATH", Fix: "install Docker and put it on PATH, or skip the local build"}
	}
	return Check{ID: "docker", OK: true}
}
