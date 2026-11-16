// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The deployment's environment against these readers (portal plan P1.7): the two
// services' variables are written in the Terraform root that declares their
// revisions — terraform/services/main.tf, `local.api_env` and `local.minter_env`
// — and read by this package, and nothing but a case holds the two lists
// together. A name added to one side and not the other is a revision that
// refuses at startup and never becomes ready, which a deploy shows as a rollout
// and a caller meets as a service that does not answer.
//
// **Why the case is in this package.** terraform/infra_test.go reads the same
// tree as text, but behind the `terraform` build tag: a case there would let the
// two lists drift in every build that does not pass the tag, and this pair is not
// about the infrastructure, it is about the two programs' own environment. What
// it reads is a file, never a cloud, and it reads names rather than values: a
// value's spelling is the root's own business and this package's readers are
// what a name has to match.

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestTheServicesSetEveryVariableTheProgramsRead is the whole of the pair in both
// directions: every FUGARO_ name the root sets is one the program reads (the
// readers refuse a variable they do not know, so a name only the root sets is a
// revision that does not start), and every name the program reads is one the root
// sets (a missing one is refused too, and a service that names a field nothing
// fills would be a deployment running on a zero value).
func TestTheServicesSetEveryVariableTheProgramsRead(t *testing.T) {
	source := servicesRootSource(t)

	cases := []struct {
		service string
		entry   string
		reads   []string
	}{
		{service: "the API", entry: "api_env", reads: apiVariables},
		{service: "the minter", entry: "minter_env", reads: minterVariables},
	}
	for _, tc := range cases {
		t.Run(tc.service, func(t *testing.T) {
			held := localEnvNames(t, source, tc.entry)
			for _, name := range tc.reads {
				if !held[name] {
					t.Errorf("terraform/services/main.tf's local.%s does not set %s, and %s reads it: the program refuses to start without it, so the revision this root deploys never becomes ready", tc.entry, name, tc.service)
				}
			}
			for name := range held {
				if !slices.Contains(tc.reads, name) {
					t.Errorf("terraform/services/main.tf's local.%s sets %s, which %s does not read: the loader refuses a FUGARO_ variable outside its own set, so this one is a revision that refuses at startup (ee/keyservice/config.go's readDeployEnv)", tc.entry, name, tc.service)
				}
			}
		})
	}

	// And each set is on its own container: the API's environment on the API and
	// the minter's three on the minter. The two services are told apart by their
	// resource headers, so a set moved from one to the other is this check rather
	// than a pair of lists that still match.
	//
	// The seed job is read here as a third container and it is on the API's side
	// of the boundary on purpose (P0.5): it runs the API's own program one time,
	// to write the operations organisation, and it needs the whole of the API's
	// environment to start at all. What it must never carry is the minter's.
	api, minter, seed := splitServices(t, source)
	for _, one := range []struct {
		service string
		body    string
		set     string
		other   string
	}{
		{service: "the API", body: api, set: "local.api_env", other: "local.minter_env"},
		{service: "the minter", body: minter, set: "local.minter_env", other: "local.api_env"},
		{service: "the seed job", body: seed, set: "local.api_env", other: "local.minter_env"},
	} {
		if !strings.Contains(one.body, "dynamic \"env\"") || !strings.Contains(one.body, "for_each = "+one.set) {
			t.Errorf("%s's container takes no `for_each = %s`: the variables above are set nowhere else, and a container declared without them is the revision that never becomes ready", one.service, one.set)
		}
		if strings.Contains(one.body, one.other) {
			t.Errorf("%s's container reads %s: the two sets are the identity boundary at the deployment (§3.3, Check 174) — the merchant's secret, the mail key and the store's variables belong to the API alone", one.service, one.other)
		}
	}

	// The roles the deployment names a secret for are the readers' own, and the
	// four the program cannot start without are all there. This half is what
	// keeps a container's rename from becoming a deployment that refuses at
	// startup: FUGARO_SECRETS is read strictly too, and a role it does not know
	// is a refusal.
	roles := secretRoleNames(t, source)
	for _, role := range roles {
		if !slices.Contains(knownSecretRoles, role) {
			t.Errorf("terraform/services/main.tf's secret_roles names the role %q, which this package does not read: the roles are %s", role, strings.Join(knownSecretRoles, ", "))
		}
	}
	for _, role := range []string{secretMerchant, secretAdmin, secretPaddleKey, secretResendKey} {
		if !slices.Contains(roles, role) {
			t.Errorf("terraform/services/main.tf's secret_roles names no %s, and the API reads that secret to start: %s", role, strings.Join(knownSecretRoles, ", "))
		}
	}
	// The error DSN is P1.6's own and its off is the role's absence, so the root
	// names it behind the map's own entry rather than always: a container nobody
	// has filled has no version to read, and a deployment that reports nowhere
	// must be one that starts.
	if !strings.Contains(secretRolesEntry(t, source), `contains(keys(var.secrets), "sentry_dsn")`) {
		t.Error("terraform/services/main.tf's secret_roles names the error DSN unconditionally: an empty container has no version to read, so a deployment that chose P1.6's off would refuse at startup instead of starting with error reporting off")
	}
}

// servicesRootSource reads the Terraform root that declares the two revisions,
// as text: this case is about the names in it, and terraform/ is where the file
// lives rather than where the reader does.
func servicesRootSource(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile("terraform/services/main.tf")
	if err != nil {
		t.Fatalf("reading the services root: %v", err)
	}
	return string(source)
}

// localEnvNames answers the FUGARO_ names one `locals` entry sets: the entry's
// own line, then every line up to the closing brace at the same indentation.
// The values are expressions (a variable, a join, an id) and this case reads only
// the names, which is what makes it about the set and not about a spelling of one
// value in it.
func localEnvNames(t *testing.T, source, entry string) map[string]bool {
	t.Helper()
	lines := strings.Split(source, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == entry+" = {" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("terraform/services/main.tf holds no `%s = {` entry, so this case is not reading the environment it means to hold", entry)
	}
	held := map[string]bool{}
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "}" {
			return held
		}
		// The `=` is padded to the longest name in the entry, so the name is
		// trimmed on both sides and never read as the text before the padding.
		if name, _, ok := strings.Cut(trimmed, "="); ok {
			if name = strings.TrimSpace(name); strings.HasPrefix(name, deployVarPrefix) {
				held[name] = true
			}
		}
	}
	t.Fatalf("terraform/services/main.tf's `%s` entry is never closed", entry)
	return nil
}

// secretRolesEntry answers the text of the `secret_roles` entry, which is a
// `merge(` rather than a map literal and so closes with a line of its own.
func secretRolesEntry(t *testing.T, source string) string {
	t.Helper()
	lines := strings.Split(source, "\n")
	start, end := -1, -1
	for i, line := range lines {
		if start < 0 && strings.HasPrefix(strings.TrimSpace(line), "secret_roles = ") {
			start = i
			continue
		}
		if start >= 0 && strings.TrimSpace(line) == ")" {
			end = i
			break
		}
	}
	if start < 0 || end < 0 {
		t.Fatal("terraform/services/main.tf holds no `secret_roles` entry closed by a `)`, so this case is not reading the roles it means to hold")
	}
	return strings.Join(lines[start:end+1], "\n")
}

// roleAssignments matches one `role = var.secrets["<name>"]` of that entry, which
// is the only place a role and a container meet.
var roleAssignments = regexp.MustCompile(`(?m)^\s*([a-z][a-z0-9_]*)\s*=\s*var\.secrets\[`)

// secretRoleNames answers the roles the root names a container for, in the order
// they are written.
func secretRoleNames(t *testing.T, source string) []string {
	t.Helper()
	var roles []string
	for _, match := range roleAssignments.FindAllStringSubmatch(secretRolesEntry(t, source), -1) {
		roles = append(roles, match[1])
	}
	if len(roles) == 0 {
		t.Fatal("terraform/services/main.tf's secret_roles names no container at all, and the API cannot start without the merchant's secret and the mail key")
	}
	return roles
}

// splitServices answers the three Cloud Run containers' declarations of main.tf —
// the API, the minter and the seed job — and each is one resource's own body, so
// that what is read above is the container that resource declares and a set moved
// from one to another is this check rather than a pair of lists that still match.
//
// The body ends at the resource's own closing brace and not at the next resource's
// header: the comments between two resources discuss these very sets by name, and a
// body read with the next one's comment in it would make this case about prose.
func splitServices(t *testing.T, source string) (api, minter, seed string) {
	t.Helper()
	api = resourceBody(t, source, `resource "google_cloud_run_v2_service" "api"`)
	minter = resourceBody(t, source, `resource "google_cloud_run_v2_service" "minter"`)
	seed = resourceBody(t, source, `resource "google_cloud_run_v2_job" "seed"`)
	return api, minter, seed
}

// resourceBody answers one top-level resource's declaration, from its header to
// the `}` that closes it at the start of a line. Every resource in this root is
// written that way, so that brace is the end of it and nothing else is.
func resourceBody(t *testing.T, source, header string) string {
	t.Helper()
	start := strings.Index(source, header)
	if start < 0 {
		t.Fatalf("terraform/services/main.tf holds no %s, so this case is not reading the container it means to hold", header)
	}
	rest := source[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("%s is never closed by a `}` at the start of a line", header)
	}
	return rest[:end]
}
