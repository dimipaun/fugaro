// Ordinary tests — no build tag, no credential, no network — of the parts of
// the owner-run live credentials-rules check (the credentials-service plan's
// check 92) that decide what it may do before it does anything:
// crTargetFrom's refusals (creds_rules_guard_test.go), the case table it runs,
// and the cleanup that has to undo every node the table can write.
//
// They exist because of what the check is. It writes to a real installation's
// database with the owner's credential, so the two questions that matter are
// asked here, where every machine can ask them: *where* may it write (nothing
// but the sandbox's database, nothing but the sandbox project's signer, and
// never from inside a coding agent's session), and *what does it leave
// behind* (every node the table names is deleted). CI compiles the live driver
// with `go vet -tags live` and never runs it; these tests run everywhere.
package e2e

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
)

// crNoNetwork runs f with http.DefaultTransport replaced by one that fails the
// test: a guard that reached the network would be a guard that had already
// decided to use the owner's credential. The package has no parallel tests, so
// the swap is the whole process's for the length of f.
func crNoNetwork(t *testing.T, f func()) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = crTransport(func(*http.Request) (*http.Response, error) {
		t.Error("the guard made a network request: every refusal below must happen before anything is reached")
		return nil, errors.New("no network in a unit test")
	})
	defer func() { http.DefaultTransport = old }()
	f()
}

type crTransport func(*http.Request) (*http.Response, error)

func (f crTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// crSandboxEnv is an environment that aims the check at the sandbox and opts
// in: the one environment crTargetFrom accepts.
func crSandboxEnv() map[string]string {
	return map[string]string{
		crOKVar:      "1",
		crInstallVar: crSandboxInstall,
		crRTDBVar:    "https://" + crSandboxDBHost,
		crAPIKeyVar:  "an-api-key",
		crSignerVar:  "token-signer@" + crSandboxSignerDomain,
	}
}

func crGetenv(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

// TestCredsRulesTargetRefusesEverythingButTheSandbox is the guard's own test:
// every environment that is not the sandbox's is refused, each with no network
// call, and the one that is the sandbox's is accepted. It is the test that
// stands between the owner's database-scoped credential and an environment
// that aims the check somewhere else.
//
// The names are the ones a mistake would use: near-misses of the installation
// the review named (be1ong for belong, acme, fugaroo), the installation the
// check must never touch (belong, in four spellings), another project's
// database, a look-alike of the sandbox's own address, and a URL that carries
// a credential of its own.
func TestCredsRulesTargetRefusesEverythingButTheSandbox(t *testing.T) {
	refused := []struct {
		name, why string
		change    func(env map[string]string)
	}{
		{name: "another installation", why: "F33 pins the installation to the sandbox's",
			change: func(e map[string]string) { e[crInstallVar] = "be1ong" }},
		{name: "a made-up installation", why: "F33 pins the installation to the sandbox's",
			change: func(e map[string]string) { e[crInstallVar] = "acme" }},
		{name: "a near-miss of the sandbox's name", why: "F33 pins the installation to the sandbox's",
			change: func(e map[string]string) { e[crInstallVar] = "fugaroo" }},
		{name: "the installation the check must never touch", why: "F33: never belong",
			change: func(e map[string]string) { e[crInstallVar] = "belong" }},
		{name: "the same name, upper-cased", why: "F33: never belong",
			change: func(e map[string]string) { e[crInstallVar] = "BELONG" }},
		{name: "the same name, mixed case", why: "F33: never belong",
			change: func(e map[string]string) { e[crInstallVar] = "Fugaro" }},
		{name: "the installation unset", why: "the database must say what the owner said",
			change: func(e map[string]string) { delete(e, crInstallVar) }},

		{name: "another project's database", why: "the host is pinned to the sandbox project's",
			change: func(e map[string]string) { e[crRTDBVar] = "https://belong-default-rtdb.firebaseio.com" }},
		{name: "a host that is not a Firebase address", why: "rtdb.ValidateURL",
			change: func(e map[string]string) { e[crRTDBVar] = "https://evil.example.com" }},
		{name: "a look-alike of the sandbox's address", why: "rtdb.ValidateURL: .firebaseio.co is not .firebaseio.com",
			change: func(e map[string]string) {
				e[crRTDBVar] = "https://" + crSandboxProject + "-default-rtdb.firebaseio.co"
			}},
		{name: "the sandbox's address with a credential in its query", why: "rtdb.ValidateURL refuses a query, and a refusal must not print it",
			change: func(e map[string]string) { e[crRTDBVar] = "https://" + crSandboxDBHost + "?auth=SECRET" }},
		{name: "the sandbox's address with userinfo", why: "rtdb.ValidateURL refuses userinfo, and a refusal must not print it",
			change: func(e map[string]string) { e[crRTDBVar] = "https://user:pass@" + crSandboxDBHost }},
		{name: "the sandbox's address with a fragment", why: "rtdb.ValidateURL refuses a fragment",
			change: func(e map[string]string) { e[crRTDBVar] = "https://" + crSandboxDBHost + "#frag" }},
		{name: "the sandbox's address with a path", why: "rtdb.ValidateURL refuses a path",
			change: func(e map[string]string) { e[crRTDBVar] = "https://" + crSandboxDBHost + "/fugaro" }},
		{name: "the sandbox's address with a port", why: "rtdb.ValidateURL refuses a port",
			change: func(e map[string]string) { e[crRTDBVar] = "https://" + crSandboxDBHost + ":8443" }},
		{name: "a loopback address", why: "rtdb.ValidateURL with loopbackOK false",
			change: func(e map[string]string) { e[crRTDBVar] = "https://localhost:9000" }},
		{name: "a literal address", why: "rtdb.ValidateURL",
			change: func(e map[string]string) { e[crRTDBVar] = "https://127.0.0.1" }},
		{name: "plain http", why: "rtdb.ValidateURL requires https",
			change: func(e map[string]string) { e[crRTDBVar] = "http://" + crSandboxDBHost }},
		{name: "the database unset", why: "there is nothing to reach",
			change: func(e map[string]string) { delete(e, crRTDBVar) }},

		{name: "another project's service account", why: "the signer's domain is pinned to the sandbox project's",
			change: func(e map[string]string) { e[crSignerVar] = "token-signer@other-dev.iam.gserviceaccount.com" }},
		{name: "a look-alike of the signer's domain", why: "the domain is compared whole",
			change: func(e map[string]string) {
				e[crSignerVar] = "token-signer@" + crSandboxSignerDomain + ".evil.example.com"
			}},
		{name: "the signer's domain with no local part", why: "a service account has an account name",
			change: func(e map[string]string) { e[crSignerVar] = "@" + crSandboxSignerDomain }},
		{name: "a person's address", why: "a person's credential is not the token signer",
			change: func(e map[string]string) { e[crSignerVar] = "owner@example.com" }},
		{name: "the signer unset", why: "there is nothing to sign with",
			change: func(e map[string]string) { delete(e, crSignerVar) }},

		{name: "no web API key", why: "the exchange needs one",
			change: func(e map[string]string) { delete(e, crAPIKeyVar) }},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			env := crSandboxEnv()
			c.change(env)
			var got crTarget
			var err error
			crNoNetwork(t, func() { got, err = crTargetFrom(crGetenv(env)) })
			if err == nil {
				t.Fatalf("crTargetFrom accepted %s (%s): it reached the target %+v", c.name, c.why, got)
			}
			if errors.Is(err, crSkip) {
				t.Fatalf("crTargetFrom skipped on %s: an environment aimed at a database and opted in must be refused, never skipped", c.name)
			}
		})
	}
}

// TestCredsRulesTargetAcceptsOnlyTheSandbox is the other half: the sandbox's
// own environment is accepted, both spellings of its database's address, and
// the target carries the pinned host rather than the URL it was given.
func TestCredsRulesTargetAcceptsOnlyTheSandbox(t *testing.T) {
	for _, url := range []string{
		"https://" + crSandboxDBHost,
		"https://" + crSandboxProject + "-default-rtdb.europe-west1.firebasedatabase.app",
	} {
		env := crSandboxEnv()
		env[crRTDBVar] = url
		var got crTarget
		var err error
		crNoNetwork(t, func() { got, err = crTargetFrom(crGetenv(env)) })
		if err != nil {
			t.Fatalf("crTargetFrom refused the sandbox's own environment (%s): %v", url, err)
		}
		if got.installation != crSandboxInstall || got.signer != env[crSignerVar] || got.apiKey != env[crAPIKeyVar] || got.rtdbURL != url {
			t.Errorf("crTargetFrom(%s) = %+v, want the sandbox installation %q with the environment's signer, key and URL", url, got, crSandboxInstall)
		}
		if !crSandboxHost(got.host) {
			t.Errorf("crTargetFrom(%s) pinned host %q, which crSandboxHost does not accept", url, got.host)
		}
		if got.requestedBy != crDefaultBy {
			t.Errorf("crTargetFrom(%s) left requestedBy %q, want the default %q", url, got.requestedBy, crDefaultBy)
		}
	}
	// The owner may name the launcher's own identity; it is the only field the
	// environment is allowed to choose.
	env := crSandboxEnv()
	env[crByVar] = "owner-laptop"
	var got crTarget
	crNoNetwork(t, func() {
		var err error
		if got, err = crTargetFrom(crGetenv(env)); err != nil {
			t.Fatalf("crTargetFrom refused an environment that names its launcher: %v", err)
		}
	})
	if got.requestedBy != "owner-laptop" {
		t.Errorf("requestedBy = %q, want the environment's owner-laptop", got.requestedBy)
	}
}

// TestCredsRulesTargetRequiresTheExactOptIn: the opt-in is "1" or nothing. A
// value that is merely truthy (yes, true, on, 2) is not the owner's explicit
// opt-in, and neither is the variable being present but empty.
func TestCredsRulesTargetRequiresTheExactOptIn(t *testing.T) {
	// Surrounding whitespace is trimmed before the comparison — a value read
	// from a file or a shell substitution carries a newline — so " 1" is the
	// opt-in; "01", "1.0" and "11" are not.
	for _, v := range []string{"", "0", "2", "01", "1.0", "11", "yes", "true", "TRUE", "on", "on1"} {
		env := crSandboxEnv()
		env[crOKVar] = v
		var err error
		crNoNetwork(t, func() { _, err = crTargetFrom(crGetenv(env)) })
		if err == nil {
			t.Errorf("crTargetFrom accepted %s=%q: the opt-in is exactly \"1\"", crOKVar, v)
		}
	}
	// An aim at a database with no opt-in at all is a refusal, not a skip: a
	// skip would let a machine that is aimed at the sandbox run nothing and
	// report success.
	env := crSandboxEnv()
	delete(env, crOKVar)
	var err error
	crNoNetwork(t, func() { _, err = crTargetFrom(crGetenv(env)) })
	if err == nil || errors.Is(err, crSkip) {
		t.Fatalf("crTargetFrom with the aim set and %s unset = %v, want a refusal (never a skip)", crOKVar, err)
	}
}

// TestCredsRulesTargetSkipsOnlyWhenNothingAimsIt: an environment that names
// nothing skips, so `go test -tags live ./internal/e2e/` stays usable on a
// machine that is not running the check. A coding agent's session is not such
// an environment: it is refused even with nothing else set, because the
// refusal is about who is running the check, not about where it points (F33).
func TestCredsRulesTargetSkipsOnlyWhenNothingAimsIt(t *testing.T) {
	var err error
	crNoNetwork(t, func() { _, err = crTargetFrom(crGetenv(map[string]string{})) })
	if !errors.Is(err, crSkip) {
		t.Fatalf("crTargetFrom on an empty environment = %v, want the skip", err)
	}
	crNoNetwork(t, func() { _, err = crTargetFrom(nil) })
	if !errors.Is(err, crSkip) {
		t.Fatalf("crTargetFrom(nil) = %v, want the skip", err)
	}

	// Every marker the repository knows, alone and in the sandbox's own
	// environment: a coding agent's session is refused either way.
	for _, marker := range agentenv.Markers {
		for _, aimed := range []bool{false, true} {
			env := map[string]string{marker: "1"}
			if aimed {
				env = crSandboxEnv()
				env[marker] = "1"
			}
			var gotErr error
			crNoNetwork(t, func() { _, gotErr = crTargetFrom(crGetenv(env)) })
			if gotErr == nil || errors.Is(gotErr, crSkip) {
				t.Errorf("crTargetFrom with %s set (aimed=%v) = %v, want a refusal: a check that writes to a real installation's database is the owner's, not the agent's that wrote it (F33)", marker, aimed, gotErr)
				continue
			}
			if !strings.Contains(gotErr.Error(), marker) {
				t.Errorf("crTargetFrom with %s set refused with %q, which does not name the variable to unset", marker, gotErr)
			}
		}
	}
	// An empty value is not a set variable.
	env := crSandboxEnv()
	env["CLAUDECODE"] = ""
	var err2 error
	crNoNetwork(t, func() { _, err2 = crTargetFrom(crGetenv(env)) })
	if err2 != nil {
		t.Errorf("crTargetFrom with CLAUDECODE= (empty) refused: %v", err2)
	}
}

// TestCredsRulesRefusalNeverPrintsTheURL: a database URL can carry a
// credential in its userinfo or its query, so no refusal may quote one.
// rtdb.ValidateURL's own message echoes what it was given, which is why the
// guard does not propagate it.
func TestCredsRulesRefusalNeverPrintsTheURL(t *testing.T) {
	const secret = "SUPERSECRETVALUE"
	for _, url := range []string{
		"https://" + crSandboxDBHost + "?auth=" + secret,
		"https://owner:" + secret + "@" + crSandboxDBHost,
		"https://owner:" + secret + "@evil.example.com/?auth=" + secret,
	} {
		env := crSandboxEnv()
		env[crRTDBVar] = url
		var err error
		crNoNetwork(t, func() { _, err = crTargetFrom(crGetenv(env)) })
		if err == nil {
			t.Fatalf("crTargetFrom accepted %q", url)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the refusal for a URL carrying a credential prints it: %q", err)
		}
		if strings.Contains(err.Error(), url) {
			t.Errorf("the refusal for a URL prints the URL it refused: %q", err)
		}
	}
}

// TestCredsRulesSandboxPins: the pins themselves, so that a pin that is
// loosened to a substring or to a case-insensitive compare is caught here.
func TestCredsRulesSandboxPins(t *testing.T) {
	for _, host := range []string{
		crSandboxDBHost,
		crSandboxProject + "-default-rtdb.europe-west1.firebasedatabase.app",
		crSandboxProject + "-default-rtdb.asia-southeast1.firebasedatabase.app",
	} {
		if !crSandboxHost(host) {
			t.Errorf("crSandboxHost(%q) = false, want the sandbox's own address", host)
		}
	}
	for _, host := range []string{
		"",
		"belong-default-rtdb.firebaseio.com",
		crSandboxProject + "-default-rtdb.firebaseio.co",
		crSandboxProject + "-default-rtdb.firebaseio.com.evil.example.com",
		"evil-" + crSandboxDBHost,
		crSandboxProject + "-default-rtdb2.firebaseio.com",
		crSandboxProject + "-other-rtdb.firebaseio.com",
		crSandboxProject + "-default-rtdb.firebasedatabase.app",
		"localhost",
		"127.0.0.1",
		"fugaroo-default-rtdb.firebaseio.com",
	} {
		if crSandboxHost(host) {
			t.Errorf("crSandboxHost(%q) = true, want a refusal: only the sandbox project's database is this check's", host)
		}
	}
	if !crSandboxHost(strings.ToUpper(crSandboxDBHost)) {
		t.Errorf("crSandboxHost is case-sensitive: a host is case-insensitive, and the guard must not be talked out of the pin by a capital letter")
	}
	for _, email := range []string{
		"token-signer@" + crSandboxSignerDomain,
		"a-b_c@" + crSandboxSignerDomain,
	} {
		if !crSandboxSigner(email) {
			t.Errorf("crSandboxSigner(%q) = false, want the sandbox project's service account", email)
		}
	}
	for _, email := range []string{
		"",
		"@",
		"token-signer",
		"token-signer@",
		"@" + crSandboxSignerDomain,
		"token-signer@other-dev.iam.gserviceaccount.com",
		"token-signer@" + crSandboxSignerDomain + ".evil.example.com",
		"token-signer@fugaro-dev.iam.gserviceaccount.com.evil.example.com",
		"owner@example.com",
	} {
		if crSandboxSigner(email) {
			t.Errorf("crSandboxSigner(%q) = true, want a refusal: only the sandbox project's accounts may sign tokens this database honours", email)
		}
	}
}

// TestCredsRulesClockSkew: the check refuses a clock minutes off and accepts a
// day boundary, which is the distinction crClockSkew documents.
func TestCredsRulesClockSkew(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if crClockTooFar(now, now) {
		t.Error("crClockTooFar on one instant = true")
	}
	if crClockTooFar(now.Add(crClockSkew), now) || crClockTooFar(now.Add(-crClockSkew), now) {
		t.Error("crClockTooFar at exactly the skew = true, want the bound to admit it")
	}
	if !crClockTooFar(now.Add(crClockSkew+time.Second), now) || !crClockTooFar(now.Add(-crClockSkew-time.Second), now) {
		t.Error("crClockTooFar past the skew = false, want a refusal")
	}
	// A day boundary is not a clock problem: 23:59 and 00:01 are two minutes
	// apart, and the check works in the database's day.
	midnight := time.Date(2026, 10, 21, 0, 1, 0, 0, time.UTC)
	if crClockTooFar(midnight, midnight.Add(-2*time.Minute)) {
		t.Error("crClockTooFar across midnight = true, want the day boundary to be the check's business, not a clock to fix")
	}
}

// TestCredsRulesCaseTableIsComplete is the table's test: every family the
// emulator suite walks is in it, the /creds cases are all there — the writes
// *and* the deletes — and the accepted cases are the positive controls without
// which a refused write would also be the answer a database that refuses
// everything gives.
func TestCredsRulesCaseTableIsComplete(t *testing.T) {
	n := crNewNames(time.Now(), budget.Day(time.Now()))
	cases := crCases(n, 1_000_000, crDefaultBy)
	if err := crCheckCases(cases, budget.Key(n.slug), budget.Key(n.otherSlug)); err != nil {
		t.Fatalf("the case table breaks its own safety invariants: %v", err)
	}

	// The /creds cases, all eight: the six writes and the two deletes. A
	// removed case would leave a grant unproven.
	var credsWrites, credsDeletes int
	creds := crCredsPaths(n)
	want := map[string]string{
		creds.mark1:            "a mark under /creds",
		creds.mark1 + "/state": "a field of a mark under /creds",
		creds.mark3:            "a new mark under /creds",
		creds.lease:            "a lease under /creds",
		creds.current:          "this run's sticky credential",
		creds.roundrobin:       "the round-robin counter",
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if !strings.HasPrefix(c.path, "creds/") {
			continue
		}
		if c.allow {
			t.Errorf("case %q accepts a write under /creds: no run token holds a grant there (O1)", c.name)
		}
		if c.value == nil {
			credsDeletes++
		} else {
			credsWrites++
		}
		seen[c.path] = true
	}
	if credsWrites != 6 || credsDeletes != 2 {
		t.Errorf("the table has %d /creds writes and %d /creds deletes, want 6 and 2: every write and every delete under /creds must be attempted", credsWrites, credsDeletes)
	}
	for path, name := range want {
		if !seen[path] {
			t.Errorf("the table has no case for /%s (%s): a node the service writes there would be untested", path, name)
		}
	}

	// The positive controls: the writes a run's own identity must be able to
	// make. Without them a database that refused everything would pass.
	entry := budget.PathAgent(n.slug, n.run)
	allowed := map[string]bool{}
	for _, c := range cases {
		if c.allow {
			allowed[c.path] = true
		}
	}
	for _, path := range []string{
		entry,
		entry + "/credential",
		entry + "/waiting_until",
	} {
		if !allowed[path] {
			t.Errorf("the table has no accepted case for /%s: the rules must accept a run's own registry entry and the two children the cascade writes", path)
		}
	}
	if len(allowed) < 7 {
		t.Errorf("the table has %d accepted cases, want the registry entry, its two children and the by-tier leaves", len(allowed))
	}

	// The claims the entry's children are tied to, and the bound the one
	// positive counter write respects.
	var rbSeen, boundSeen bool
	for _, c := range cases {
		if c.allow {
			if m, ok := c.value.(map[string]any); ok && m["requestedBy"] == crDefaultBy {
				rbSeen = true
			}
		} else if strings.HasSuffix(c.path, "/b/spent") && c.seed != nil {
			if v, ok := c.value.(int64); ok && v > 1_000_000 {
				boundSeen = true
			}
		}
	}
	if !rbSeen {
		t.Error("no accepted case writes the entry's requestedBy: RBOK ties every child of the entry to it, and a case that omits it would prove nothing about the tie")
	}
	if !boundSeen {
		t.Error("the table has no case writing past maxReserveMicros: the bound the leaves carry would be untested")
	}
}

// TestCredsRulesIdentityFamilies: the five identities besides the run's own,
// which is what the review's minor asked for — the emulator walks them and the
// live check must too. Each one is an identity the rules read (a run of
// another repository, another run of this repository, a phone, a consumer, a
// pairing session), and each carries what makes its refusal mean something:
// the claim its family is read by, the device node the phone needs, and a
// positive control where the family has a read grant.
func TestCredsRulesIdentityFamilies(t *testing.T) {
	n := crNewNames(time.Now(), budget.Day(time.Now()))
	ids := crIdentities(n)
	if len(ids) != 5 {
		t.Fatalf("crIdentities has %d identities, want the five families the emulator walks", len(ids))
	}
	byName := map[string]crIdentity{}
	for _, id := range ids {
		if id.name == "" {
			t.Error("an identity has no name")
		}
		if id.uid == "" && (id.slug == "" || id.run == "") {
			t.Errorf("%s: neither a run token (slug and run) nor a claim token (uid)", id.name)
		}
		if id.uid != "" && (id.slug != "" || id.run != "") {
			t.Errorf("%s: both a run token and a claim token", id.name)
		}
		if id.uid != "" && len(id.claims) == 0 {
			t.Errorf("%s: a claim token with no claims: token.Exchange refuses one (sameIdentity requires a non-empty claims object), so the check could never mint it", id.name)
		}
		if _, dup := byName[id.name]; dup {
			t.Errorf("two identities named %q", id.name)
		}
		byName[id.name] = id
	}
	// The two run-token families, and the claims the rules read them by.
	for _, name := range []string{"another repository's run", "another run of this repository"} {
		id, ok := byName[name]
		if !ok {
			t.Errorf("crIdentities has no %s: the emulator's denial is the node's, not this run's, and a token of the same shape is what proves it", name)
			continue
		}
		if got := id.expected(); got["fs"] != budget.Key(id.slug) || got["fr"] != budget.Key(id.run) {
			t.Errorf("%s expects %v, want the fs/fr pair every rule in this tree reads", name, got)
		}
		if id.reach != "" || id.device {
			t.Errorf("%s claims a reach or a device node, which only the three claim families have", name)
		}
	}
	if byName["another repository's run"].slug == n.slug {
		t.Error("the other repository's run names this repository's slug: its denial would be this run's own slug's answer")
	}
	if byName["another run of this repository"].slug != n.slug {
		t.Error("another run of this repository does not name this repository's slug")
	}
	if byName["another run of this repository"].run == n.run {
		t.Error("another run of this repository names this run's own run id")
	}
	// The three claim families the rules read: mu (a phone), mc (a consumer),
	// pu (a pairing session), each with its own uid.
	for claim, name := range map[string]string{"mu": "the phone", "mc": "the consumer", "pu": "the pairing session"} {
		id, ok := byName[name]
		if !ok {
			t.Errorf("crIdentities has no %s: the claim %s is one of the three the rules read outside a run's own families, and /creds must not be one of the families it reaches", name, claim)
			continue
		}
		if got := id.claims[claim]; got != id.uid || id.uid == "" {
			t.Errorf("%s carries %s=%v with uid %q, want the uid the rules compare it with (auth.token.%s == auth.uid)", name, claim, got, id.uid, claim)
		}
		if id.expected()[claim] != id.uid {
			t.Errorf("%s expects %v, want the claim itself", name, id.expected())
		}
	}
	if uid := byName["the phone"].uid; !byName["the phone"].device || uid == "" {
		t.Errorf("the phone (uid %q, device=%v) has no device node: D166's USER conjunct reads config/admin/devices/<uid>, and without it the phone's refusal would be the answer of a token the rules ignore everywhere", uid, byName["the phone"].device)
	}
	if byName["the consumer"].reach != "" {
		t.Errorf("the consumer claims the reach %q: its only grant is a mailbox write this check does not make, so it has no positive control to make — the check says so rather than inventing one", byName["the consumer"].reach)
	}
	if byName["the pairing session"].reach == "" {
		t.Error("the pairing session has no positive control: it reaches config/pairing/<uid>/delivery, and a refusal from a token that reaches nothing proves nothing")
	}
	// Every claim identity's uid is a scratch name, and the pairing session's
	// is the shape its rule requires (auth.uid.beginsWith('p~')).
	for _, name := range []string{"the phone", "the consumer", "the pairing session"} {
		if uid := byName[name].uid; !strings.HasPrefix(uid, crRepoBase) && !strings.HasPrefix(uid, "p~"+crRepoBase) {
			t.Errorf("%s has uid %q, which does not carry the scratch prefix %q: an identity left behind would look like a real one", name, uid, crRepoBase)
		}
	}
	if uid := byName["the pairing session"].uid; !strings.HasPrefix(uid, "p~") {
		t.Errorf("the pairing session's uid is %q, want the p~ prefix PAIRING reads (auth.uid.beginsWith('p~'))", uid)
	}
}

// TestCredsRulesCleanupCoversEveryNodeTheCheckTouches is the cleanup's test,
// and the answer to the review's "only 2 of the /creds nodes are deleted": the
// check's cleanup list must cover every node the seed writes, every node the
// table names under /creds (each write and each delete), and every read the
// denials make — so that nothing the check writes can survive it.
//
// Coverage is by ancestry: the cleanup may name a parent instead of a child
// (one multi-path update may not name a node and its own descendant, and
// deleting the parent deletes the child), but never the other way round.
func TestCredsRulesCleanupCoversEveryNodeTheCheckTouches(t *testing.T) {
	n := crNewNames(time.Now(), budget.Day(time.Now()))
	written := crWritten(n)

	// No entry may be an ancestor of another, or the cleanup's single update
	// would name a node and its own descendant.
	for i, a := range written {
		for j, b := range written {
			if i != j && crIsUnder(b, a) {
				t.Errorf("crWritten names /%s and /%s, which is under it: one update may not name a node and its own descendant", a, b)
			}
		}
		if a == "" {
			t.Error("crWritten names an empty path")
		}
	}
	if len(written) == 0 {
		t.Fatal("crWritten names nothing: the check would leave everything it writes behind")
	}
	if len(written) < 6 {
		t.Errorf("crWritten names %d nodes: every node under /creds the check touches, both scratch repositories' nodes and the phone's device node must be in it", len(written))
	}

	// Every path the check can leave something at: what the owner seeds, what
	// the table attempts (accepted *or* refused: the rules are what refuse an
	// attempt, and a live document that wrongly accepted one must still leave
	// nothing behind), and the nodes the identity walk and the other launcher
	// write.
	//
	// The two parents a reader of the plan would name under /creds ("creds"
	// and "creds/marks") are not here: they are only ever *read*, a read leaves
	// nothing behind, and crCheckCases refuses any accepted case under /creds,
	// so nothing ever writes them.
	var touched []string
	touched = append(touched, crCredsPaths(n).seeded()...)
	for _, c := range crCases(n, 1_000_000, crDefaultBy) {
		touched = append(touched, c.path)
	}
	touched = append(touched,
		crDevicePath(crPhoneUID(n)),
		budget.PathAgent(n.slug, n.run),
		budget.PathAgent(n.slug, n.otherRun), // the other launcher's own entry
	)
	for _, id := range crIdentities(n) {
		if id.device {
			touched = append(touched, crDevicePath(id.uid))
		}
	}
	for _, p := range touched {
		// The two generation markers are the deploy's, and the check only ever
		// attempts them: crUndo is what puts them back if a live document
		// accepted the attempt, and the check may not delete them.
		if strings.HasPrefix(p, "config/") && !strings.HasPrefix(p, rules.PathDevices+"/") {
			continue
		}
		if !crCovered(p, written) {
			t.Errorf("the check names /%s and the cleanup does not cover it: a live document that accepted it would leave it behind", p)
		}
	}

	// The two nodes the review saw deleted are the ones the cleanup names
	// under /creds, and there are more than two: the sticky credential's
	// parent is named instead of the child.
	cp := crCredsPaths(n)
	for _, p := range []string{cp.mark1, cp.mark2, cp.mark3, cp.lease, cp.roundrobin} {
		if !crCovered(p, written) {
			t.Errorf("the cleanup does not cover the seeded /%s", p)
		}
	}
	if !crCovered(cp.current, written) {
		t.Errorf("the cleanup does not cover the sticky credential /%s", cp.current)
	}
}

// crCovered reports whether path is named by the cleanup, or is under a node
// it names.
func crCovered(path string, written []string) bool {
	for _, w := range written {
		if path == w || crIsUnder(path, w) {
			return true
		}
	}
	return false
}

// crIsUnder reports whether path is a descendant of parent.
func crIsUnder(path, parent string) bool {
	return strings.HasPrefix(path, parent+"/")
}

// TestCredsRulesSecretsAreScrubbed: the FACT recorder's own guard. A secret
// that reached a FACT line would be printed into the checklist the owner
// pastes, and the check's last act is to look for exactly that.
func TestCredsRulesSecretsAreScrubbed(t *testing.T) {
	secrets := []string{"the-id-token", "the-api-key"}
	lines := []string{
		"the exchange produced the identity r~a~b",
		"the token the-id-token was refreshed",
		"the key the-api-key was registered",
	}
	got := crSecretsIn(lines, secrets)
	if len(got) != 2 {
		t.Fatalf("crSecretsIn found %d leaking lines, want 2: %v", len(got), got)
	}
	for _, line := range got {
		if strings.Contains(line, "the-id-token") || strings.Contains(line, "the-api-key") {
			t.Errorf("crSecretsIn's report quotes the secret it reports: %q", line)
		}
		if !strings.Contains(line, "[redacted]") {
			t.Errorf("crSecretsIn's report is not scrubbed: %q", line)
		}
	}
	if got := crSecretsIn(lines[:1], secrets); len(got) != 0 {
		t.Errorf("crSecretsIn found a leak in a clean line: %v", got)
	}
	// An empty secret is not a secret: every line contains "".
	if got := crSecretsIn(lines, []string{""}); len(got) != 0 {
		t.Errorf("crSecretsIn treated the empty string as a secret: %v", got)
	}
	if s := crScrub("x the-id-token y", secrets); s != "x [redacted] y" {
		t.Errorf("crScrub = %q", s)
	}
}

// TestCredsRulesIDTokenClaims: the check reads an ID token's claims to prove
// the identity it holds is the one it minted. It must never return the token
// itself, and it must not panic on a token that is not a JWT.
func TestCredsRulesIDTokenClaims(t *testing.T) {
	// header.payload.signature, base64url, no padding: what Firebase issues.
	tok := "eyJhbGciOiJub25lIn0.eyJ1c2VyX2lkIjoiciJ9.sig"
	got, err := crIDTokenClaims(tok)
	if err != nil {
		t.Fatalf("crIDTokenClaims on a well-formed token: %v", err)
	}
	if got["user_id"] != "r" {
		t.Errorf("crIDTokenClaims = %v, want user_id r", got)
	}
	// An empty payload is a well-formed, empty claim set: the caller asserts
	// the claims it needs, so an empty one fails there rather than here.
	if got, err := crIDTokenClaims("e30.e30.e30"); err != nil || len(got) != 0 {
		t.Errorf("crIDTokenClaims on an empty payload = (%v, %v), want an empty claim set", got, err)
	}
	for _, bad := range []string{"", "a", "a.b", "a.b.c.d", "!!.!!.!!", "e30.!!.e30"} {
		if _, err := crIDTokenClaims(bad); err == nil {
			t.Errorf("crIDTokenClaims(%q) = nil error, want a refusal", bad)
		}
	}
}

// TestCredsRulesSameJSON: the database stores numbers one way and reorders
// keys, so an accepted write is compared as JSON, not as Go values.
func TestCredsRulesSameJSON(t *testing.T) {
	yes := []struct{ got, want any }{
		{float64(0), int64(0)},
		{float64(1), 1},
		{map[string]any{"b": float64(2), "a": float64(1)}, map[string]any{"a": 1, "b": 2}},
		{nil, nil},
	}
	for _, c := range yes {
		if !crSameJSON(c.got, c.want) {
			t.Errorf("crSameJSON(%v, %v) = false, want true", c.got, c.want)
		}
	}
	no := []struct{ got, want any }{
		{float64(0), nil},
		{nil, float64(0)},
		{float64(1), int64(2)},
		{map[string]any{"a": float64(1)}, map[string]any{"a": float64(2)}},
	}
	for _, c := range no {
		if crSameJSON(c.got, c.want) {
			t.Errorf("crSameJSON(%v, %v) = true, want false", c.got, c.want)
		}
	}
}
