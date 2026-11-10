// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The eligibility checks (§4): the free-mail list, the code host's answer about
// a repository, and proof of control. Each one is a seam with an interface in
// front of it, because each one's data source is a decision the owner has not
// taken — D453's free-mail list is configuration, and D452's proof of control
// has three candidate answers with different costs to an honest individual
// maintainer, so this package states the question and refuses to guess it
// (§4.4, §4.6).

import (
	"context"
	"errors"
	"strings"
)

// Plan is what one of the service's paths mints: the claims a key carries and
// the term it covers.
//
// The numbers are **D449** and the owner has not ruled them — which tier a free
// key is, what features it turns on, how many seats and for how long — so the
// service mints from the plan it is handed. A deploy that names none of these
// does not start.
type Plan struct {
	Name     string
	Tier     string
	Features []string
	Seats    int
	TermDays int
}

// Repository is what the code host answers about one repository: whether it is
// public, and the licence its own metadata names (§4.4).
type Repository struct {
	Public  bool
	Licence string
}

// CodeHost reads one repository. The URL is what the applicant typed and the
// answer is the host's, never this service's guess.
type CodeHost interface {
	Repository(ctx context.Context, url string) (Repository, error)
}

// ErrRepository is a URL the code host has no answer for: not a repository, not
// one the service's credentials can see, or a host it does not serve. All three
// are a refusal and none of them is an error the applicant should be shown.
var ErrRepository = errors.New("keyservice: the code host has no repository at that URL")

// OpenSource is one application to the open-source programme: the address that
// asked and the repositories it named.
type OpenSource struct {
	Email        string
	Repositories []string
}

// Proof is who the applicant proved control of — the organisation a key of the
// open-source programme binds to (§4.4).
type Proof struct {
	Organisation string
}

// Proofs answers whether the applicant controls what it named. **D452 is open**:
// the candidates are a token committed to a repository the check can read, an
// application installation that shows control of the organisation, or a signed
// commit, and which one it is is the owner's to rule because each has a
// different cost to the maintainer T15 protects.
//
// A service built with no Proofs refuses every open-source application and mints
// nothing rather than treating visibility as identity: F47 is exactly that a
// public repository proves nothing about who is asking.
type Proofs interface {
	Proved(ctx context.Context, application OpenSource) (Proof, error)
}

// osiApproved is the licences §4.4 admits, as SPDX ids: the Open Source
// Initiative's own approved list, which is the design's words. The table is the
// check's data and its update path is the owner's; an id that is not in it —
// including `LicenseRef-…`, `NOASSERTION`, an empty answer and an expression
// this check does not evaluate — is a refusal, because the cost of a wrong
// refusal to an honest maintainer is a mail to the owner and the cost of a
// wrong admission is a licence nobody granted (§4.6).
var osiApproved = map[string]bool{
	"0BSD":               true,
	"AGPL-3.0-only":      true,
	"AGPL-3.0-or-later":  true,
	"Apache-2.0":         true,
	"Artistic-2.0":       true,
	"BSD-2-Clause":       true,
	"BSD-3-Clause":       true,
	"BSD-3-Clause-Clear": true,
	"BSL-1.0":            true,
	"CDDL-1.0":           true,
	"EPL-2.0":            true,
	"EUPL-1.2":           true,
	"GPL-2.0-only":       true,
	"GPL-2.0-or-later":   true,
	"GPL-3.0-only":       true,
	"GPL-3.0-or-later":   true,
	"ISC":                true,
	"LGPL-2.1-only":      true,
	"LGPL-2.1-or-later":  true,
	"LGPL-3.0-only":      true,
	"LGPL-3.0-or-later":  true,
	"MIT":                true,
	"MPL-2.0":            true,
	"MS-PL":              true,
	"NCSA":               true,
	"OFL-1.1":            true,
	"OpenSSL":            true,
	"PostgreSQL":         true,
	"Python-2.0":         true,
	"Unlicense":          true,
	"X11":                true,
	"Zlib":               true,
}

// openSourceControl runs §4.4's two gates in one place: every repository the
// applicant named is public under an OSI licence, and D452's proof of control
// was given. It answers the organisation a key binds to, or false for a refusal.
func (s *Service) openSourceControl(ctx context.Context, application OpenSource) (string, bool) {
	for _, url := range application.Repositories {
		repo, err := s.host.Repository(ctx, url)
		if err != nil {
			return "", false
		}
		if !repo.Public {
			return "", false
		}
		if !osiApproved[strings.TrimSpace(repo.Licence)] {
			return "", false
		}
	}
	if s.proofs == nil {
		return "", false
	}
	proof, err := s.proofs.Proved(ctx, application)
	if err != nil {
		return "", false
	}
	if strings.TrimSpace(proof.Organisation) == "" {
		return "", false
	}
	return proof.Organisation, true
}

// splitLines reads a form field the page lays out one entry per line, and drops
// the empty ones: a trailing newline is not a repository.
func splitLines(raw string) []string {
	out := []string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// freeMail answers whether an address's domain is a free email provider (§4.1).
// The list is D453 — its source, how it is pinned and who updates it — so it is
// configuration and the comparison is exact: a subdomain of a provider is a
// different domain and is not on the list.
func (s *Service) freeMail(domain string) bool {
	for _, held := range s.cfg.FreeMailDomains {
		if held == domain {
			return true
		}
	}
	return false
}

// holdsFree answers whether a domain already holds a free key (§4.2).
//
// The count is over issued keys and not over applications: a domain that has
// applied and not yet followed the link holds nothing, and a second applicant at
// the same domain can still apply. That is what the record is for and §4.2's
// proxy is only as strong as it — a second domain, a subdomain and anyone who
// can read mail at an address they do not administer are outside it (§4.6).
func (s *Service) holdsFree(ctx context.Context, domain string) (bool, error) {
	held, err := s.store.Issuances(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range held {
		if r.Plan == s.freeSeat.Name && strings.EqualFold(r.Organisation, domain) {
			return true, nil
		}
	}
	return false, nil
}

// paidFor answers whether a domain the service's own event store knows holds a
// paid subscription (§4.3, §10.2(a)). A cancelled subscription is not a
// customer: the check refuses a buyer asking for a free seat, and a domain whose
// subscription has ended may apply like any other.
//
// The store is the only place this question is answered, which is the limit the
// design states with it: a customer who bought under another domain is not
// caught (§4.3, §4.6).
func (s *Service) paidFor(ctx context.Context, domain string) (bool, error) {
	held, err := s.store.Subscriptions(ctx)
	if err != nil {
		return false, err
	}
	for _, sub := range held {
		if !sub.Cancelled && strings.EqualFold(sub.Organisation, domain) {
			return true, nil
		}
	}
	return false, nil
}

// recorded answers whether a source has already been minted for, and the record
// it made (§5.2). It is the whole of the service's idempotency: an event
// delivered twice, a link followed twice and a trigger that fires twice all read
// it.
func (s *Service) recorded(ctx context.Context, source string) (Record, bool, error) {
	held, err := s.store.Issuances(ctx)
	if err != nil {
		return Record{}, false, err
	}
	for _, r := range held {
		if r.Source == source {
			return r, true, nil
		}
	}
	return Record{}, false, nil
}

// planFor answers the plan one verification or application names. The name in a
// record is what the record carries and the plan is the deploy's, so the two are
// matched here rather than trusted: a store written by an earlier deploy names a
// plan this one may not have, and that is a refusal and not a mint.
func (s *Service) planFor(name string) (Plan, bool) {
	if s.freeSeat.Name == name {
		return s.freeSeat, true
	}
	if s.openSource.Name == name {
		return s.openSource, true
	}
	return Plan{}, false
}

// detail is the applicant's own text as it is stored: bounded, because a
// store entry is not a place to paste a novel, and trimmed of nothing — what a
// person wrote is what the owner reads (§6, escaping is the page's).
func detail(raw string) string {
	if len(raw) > maxDetailBytes {
		return raw[:maxDetailBytes]
	}
	return raw
}
