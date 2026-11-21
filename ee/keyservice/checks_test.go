// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The checks' own data and grammars (subscriptions plan S3.2; the design
// docs/key-signing-service/spec.md §4 and §7): the licences §4.4 admits, the
// repository lines it reads, the count the deploy bounds them by, and the two
// halves of an address.
//
// Every case here drives the checks through the portal's open-source ask
// (api_opensource.go), which is the door that reaches them since P9.3 retired
// the website's forms and their routes; the free seat's own rules are the
// portal's entitlements and not this service's checks (D2199), so what is left
// to hold here is what a stranger's lines can make the checks do.

import (
	"net/http"
	"testing"
)

// TestTheLicenceTableIsTheOSIsApprovedList holds §4.4's data to the design's
// rule — the licences the OSI approves are the ones admitted — with the ids a
// code host's metadata actually carries on both sides of it.
//
// It is a test rather than a comment because the rule is a data set with an
// update path, and the failure it guards is a permissive-looking id being added
// because it looks permissive: `0BSD`, `BSD-3-Clause-Clear`, `OpenSSL`,
// `Unlicense` and `X11` are the ones that were here and are not approved. SPDX's
// own `isOsiApproved` field is the comparison the list should be held to, and it
// is not in this tree and not reachable from a hermetic run — so the five are
// pinned by name and the owner's update is what re-checks the rest.
func TestTheLicenceTableIsTheOSIsApprovedList(t *testing.T) {
	for _, id := range []string{"MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "GPL-3.0-only", "LGPL-2.1-or-later", "MPL-2.0", "ISC", "PostgreSQL", "OFL-1.1", "EUPL-1.2"} {
		if !osiApproved[id] {
			t.Errorf("%s is not admitted, want it in the table: it is on the OSI's approved list, and a refusal here is an honest maintainer refused", id)
		}
	}
	for _, id := range []string{
		// Permissive and not approved: what the table exists to keep out.
		"0BSD",
		"BSD-3-Clause-Clear",
		"OpenSSL",
		// A public-domain dedication rather than a grant, and not an approval the
		// OSI gave: SPDX records it as not approved, and §4.4 admits the OSI's list.
		"Unlicense",
		"X11",
		// And what is not an id at all, which a host's metadata can still say.
		"",
		"NOASSERTION",
		"LicenseRef-Proprietary",
		"MIT OR Apache-2.0",
	} {
		if osiApproved[id] {
			t.Errorf("%q is admitted, want a refusal: it is not a licence the OSI approves, and §4.4 refuses a repository the check cannot place", id)
		}
	}
}

// TestARepositoryOffTheDeploysHostIsNeverAskedAbout: §4.4's check reads a
// repository **of the deploy's own code host**, with the deploy's credentials —
// so a line a signed-in stranger pasted must never become an address this
// service dials. What is admitted is one host, one scheme, one `owner/name`
// path, and the code host is handed the two parts and never the line: a line
// naming an internal address, a port, a deeper path or another host at all is
// refused before the first call, and the code host is asked about nothing.
func TestARepositoryOffTheDeploysHostIsNeverAskedAbout(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
	}{
		{name: "the metadata server", url: "https://169.254.169.254/latest/meta-data/"},
		{name: "an internal name", url: "https://code.internal.example/acme/widget"},
		{name: "the merchant's own host", url: "https://elsewhere.example/acme/widget"},
		{name: "a plain-http address", url: "http://code.example/acme/widget"},
		{name: "no scheme at all", url: "code.example/acme/widget"},
		{name: "an scp-style address", url: "git@code.example:acme/widget"},
		{name: "a port on the deploy's host", url: "https://code.example:8443/acme/widget"},
		{name: "credentials in the URL", url: "https://token@code.example/acme/widget"},
		{name: "a query string", url: "https://code.example/acme/widget?per_page=1"},
		{name: "a fragment", url: "https://code.example/acme/widget#readme"},
		{name: "a path deeper than a repository", url: "https://code.example/acme/widget/issues/1"},
		{name: "a traversal", url: "https://code.example/acme/../secret"},
		{name: "an encoded traversal", url: "https://code.example/acme%2f..%2fsecret"},
		{name: "a colon in the owner", url: "https://code.example/acme:widget/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.actsAs(danaUID, danaEmail, signInGoogle)
			rec := f.askOpenSource(openSourceAsk(t, repositoryURL, tc.url))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("the ask answered %d (%s), want 422: one line the deploy's host does not serve refuses the whole request (§4.4)", rec.Code, rec.Body.String())
			}
			if code := refusal(f, rec); code != "unserved_repository" {
				t.Errorf("the refusal carries the code %q, want %q", code, "unserved_repository")
			}
			if got := len(f.host.asked); got != 0 {
				t.Errorf("%s asked the code host about %v, want nothing: what a stranger pasted is not an address the service dials (§4.4, §7.2)", tc.name, f.host.asked)
			}
			if got := f.signer.calls; got != 0 {
				t.Errorf("%s signed %d key(s), want none", tc.name, got)
			}
			if got := len(f.mail.sent); got != 0 {
				t.Errorf("%s mailed %d message(s), want none", tc.name, got)
			}
			if got := f.proofs.asked; got != 0 {
				t.Errorf("%s asked for proof of control %d time(s), want none: the lines are read before anything is asked about", tc.name, got)
			}
			if open := f.open(); len(open) != 0 {
				t.Errorf("%s left %+v in the queue, want nothing", tc.name, open)
			}
		})
	}

	// And the line the check does read is read as two parts: the host is
	// lowercased and a `.git` suffix is the same repository, so what the code
	// host is asked about is the repository the applicant named and not the
	// spelling of it.
	f := newFixture(t)
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if rec := f.askOpenSource(openSourceAsk(t, "https://Code.Example/acme/widget.git")); rec.Code != http.StatusNoContent {
		t.Fatalf("the ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	if got := len(f.host.asked); got != 1 || f.host.asked[0] != "acme/widget" {
		t.Fatalf("the code host was asked %v, want the repository's two parts alone", f.host.asked)
	}
	if got := f.proofs.asked; got != 1 {
		t.Errorf("the proof of control was asked for %d time(s), want once", got)
	}
	open := f.open()
	if len(open) != 1 {
		t.Fatalf("the queue holds %d open request(s), want one", len(open))
	}
	if want := []string{repositoryURL}; len(open[0].Repositories) != 1 || open[0].Repositories[0] != want[0] {
		t.Errorf("the queued request names %v, want the canonical URL %q: what is stored is the URL on the deploy's own host and never the line", open[0].Repositories, repositoryURL)
	}
}

// TestAnApplicationOverTheRepositoryCapIsRefused: the count is the deploy's,
// because each repository an ask names is a call into the deploy's code-host
// credentials made on the strength of one request — and the body's own size cap
// is not a bound on that (api_opensource.go). An ask past the cap is refused
// before the first call.
func TestAnApplicationOverTheRepositoryCapIsRefused(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) { f.cfg.MaxRepositories = 2 })
	f.actsAs(danaUID, danaEmail, signInGoogle)

	// At the cap: read, and the address verified.
	if rec := f.askOpenSource(openSourceAsk(t, repositoryURL, repositoryTwoURL)); rec.Code != http.StatusNoContent {
		t.Fatalf("an ask at the cap answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	if got := len(f.host.asked); got != 2 {
		t.Fatalf("an ask naming two repositories asked the code host %d time(s), want its two", got)
	}
	if got := len(f.queued()); got != 1 {
		t.Errorf("an ask at the cap queued %d request(s), want one", got)
	}
	asked := len(f.host.asked)

	// One past it: nothing is read and nothing is queued.
	rec := f.askOpenSource(openSourceAsk(t, repositoryURL, repositoryTwoURL, repositoryURL+"-three"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an ask past the cap answered %d (%s), want 400", rec.Code, rec.Body.String())
	}
	if code := refusal(f, rec); code != "unreadable_body" {
		t.Errorf("the refusal carries the code %q, want %q", code, "unreadable_body")
	}
	if got := len(f.host.asked) - asked; got != 0 {
		t.Errorf("an ask past the cap asked the code host %d time(s), want none: the count is checked before the first call", got)
	}
	if got := len(f.queued()); got != 1 {
		t.Errorf("an ask past the cap left %d request(s) in the queue, want the one at the cap alone", got)
	}
}

// TestAddressReadsEachHalfByItsOwnGrammar is the same rule one layer down, where
// the two halves can be read apart: the local part is RFC 5322's dot-atom, which
// is what refuses a header injection's punctuation, and the domain is a name of
// labels, which is what the eligibility checks compare. Both are pinned here,
// because through the routes every refusal is one answer (§7.1) and a whole
// table can go green for the wrong reason.
func TestAddressReadsEachHalfByItsOwnGrammar(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "a plain address", raw: "dev@acme.example", want: "dev@acme.example"},
		{name: "a domain of any case", raw: "dev@ACME.Example", want: "dev@acme.example"},
		{name: "atext and dots in the local part", raw: "first.last+tag_1@sub.acme.example", want: "first.last+tag_1@sub.acme.example"},

		// What the mail header cannot carry: a control character, whatever the
		// local part's grammar says about the rest of it.
		{name: "a header line", raw: "dev@acme.example\r\nBcc: victim@elsewhere.example"},
		{name: "a space", raw: "dev@acme.example (a comment)"},

		// The specials, none of which is atext or a label.
		{name: "a comma", raw: "a@b.example,victim@c.example"},
		{name: "a semicolon", raw: "a@b.example;victim@c.example"},
		{name: "a comment in the domain", raw: "maintainer@gmail.example(x)"},
		{name: "a quoted local part", raw: "\"dev\"@acme.example"},
		{name: "an angle bracket", raw: "dev@acme.example>"},
		{name: "a backslash", raw: "dev\\v@acme.example"},
		{name: "an address literal", raw: "dev@[192.0.2.1]"},
		{name: "a second at sign", raw: "dev@acme.example@other.example"},
		{name: "a character outside ASCII", raw: "dév@acme.example"},

		// Dots, which are a separator and not a character of either half.
		{name: "a leading dot", raw: ".dev@acme.example"},
		{name: "a doubled dot", raw: "de..v@acme.example"},
		{name: "a trailing dot", raw: "dev.@acme.example"},
		{name: "a leading dot in the domain", raw: "dev@.acme.example"},
		{name: "an empty domain label", raw: "dev@acme..example"},

		// Labels of a name, and the shape of a name at all.
		{name: "a label opening with a hyphen", raw: "dev@-acme.example"},
		{name: "a label closing with a hyphen", raw: "dev@acme.example-"},
		{name: "one label", raw: "dev@acme"},
		{name: "no at sign", raw: "dev.acme.example"},
	} {
		got, ok := address(tc.raw)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: address(%q) read %q, want a refusal", tc.name, tc.raw, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("%s: address(%q) = %q, %v, want %q", tc.name, tc.raw, got, ok, tc.want)
		}
	}
}
