// The guard and the case table of the owner-run live credentials-rules check
// (the credentials-service plan's check 92): everything the check decides
// before it touches the network, in a file with no build tag, so that an
// ordinary `go test ./internal/e2e/...` covers it. The live driver — the
// clients, the seeding, the identities and the cleanup — is
// live_creds_rules_test.go, behind the `live` tag; CI compiles that one with
// `go vet -tags live` and never runs it.
//
// It is split this way because of what the check is: it writes to a real
// installation's database, so the part that decides *where* it may write has
// to be covered by tests that run everywhere, on every machine, with no
// credential and no network. crTargetFrom is that decision — it is a pure
// function of the environment (it takes getenv, not os.Getenv) — and it is
// pinned to one installation.
package e2e

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	// crOKVar is the owner's explicit opt-in. It is what makes this helper
	// refuse to run: the check writes to a real installation's database, and
	// nothing but the owner at a terminal sets this.
	crOKVar = "FUGARO_LIVE_CREDS_RULES_OK"
	// crInstallVar names the installation the owner believes this database is
	// (the value of /fugaro/project). It is not a choice: the value must be
	// the sandbox installation below, the database must say the same thing,
	// and anything else is refused. An environment can only aim the check at
	// that one target.
	crInstallVar = "FUGARO_LIVE_INSTALLATION"
	crRTDBVar    = "FUGARO_LIVE_RTDB_URL"
	crAPIKeyVar  = "FUGARO_LIVE_FIREBASE_API_KEY"
	crSignerVar  = "FUGARO_LIVE_TOKEN_SIGNER"
	crByVar      = "FUGARO_LIVE_REQUESTED_BY"

	crDefaultBy = "live-creds-rules-test"
	// crRepoBase is the readable prefix of the scratch repository, as check
	// 21's live21- is: `fugaro budget show --all` must show no live92-*
	// repository when the check is over.
	crRepoBase = "live92-"
	crSub      = "/rules"

	// crClockSkew is how far this machine's clock may sit from the database's
	// before the check refuses to run. The two *days* may differ while the
	// clocks agree — that is the day boundary, and the check works in the
	// database's day — but a clock minutes off is a clock to fix: the rules
	// judge a run token's fx by the database's clock and token.Exchange judges
	// the custom token's exp by this machine's, so every case below would fail
	// for a reason that is not the rules.
	crClockSkew = 2 * time.Minute

	// crNoEcho stands in for a database URL the check refuses. The value is
	// never printed: a URL can carry a credential in its userinfo or its
	// query (?auth=…), so a refusal that quotes what it refused would print
	// one, and a FACT line would too.
	crNoEcho = "the value is not printed: a database URL can carry a credential in its userinfo or its query"
)

// The sandbox, hard-coded. The credentials-service plan's ruling F33 is
// binding: "every live step is owner-run, on installation `fugaro` and a
// sandbox repository only, never `belong`" (reconciliation.md §4 F33, D146:
// "live checks owner-run on installation `fugaro`, sandbox repository"), and
// `agentMarker` refuses live applies inside a coding agent. The installation,
// the Firebase project it belongs to, that project's database host and its
// service-account domain are constants, not environment variables: an
// environment can aim the check at this target or be refused, and there is no
// third answer.
//
// The host and the signer's domain are the sandbox project's, from
// docs/gcp-live-checklist.md ("installation `fugaro`, project `fugaro-dev`,
// repository `edgeappinc/fugarosandbox`"). A sandbox whose Firebase project
// is not this one is refused rather than served: the constant is a one-line
// fix, and refusing is the failure mode that writes nothing.
const (
	crSandboxInstall      = "fugaro"
	crSandboxProject      = "fugaro-dev"
	crSandboxDBHost       = crSandboxProject + "-default-rtdb.firebaseio.com"
	crSandboxSignerDomain = crSandboxProject + ".iam.gserviceaccount.com"
)

// crSandboxRegionHost is the regional form of the same database's address
// (https://<db>.<region>.firebasedatabase.app): the project component is
// pinned to the sandbox's, and the region is the installation's to choose.
var crSandboxRegionHost = regexp.MustCompile(`^` + regexp.QuoteMeta(crSandboxProject+"-default-rtdb") + `\.[a-z0-9-]+\.firebasedatabase\.app$`)

// crSandboxHost reports whether host is the sandbox database's and no other.
// A host that is not (a look-alike, another project's, a loopback name, a
// literal address, a hand-written name) is refused before the owner's
// database-scoped credential can be sent anywhere.
func crSandboxHost(host string) bool {
	h := strings.ToLower(host)
	return h == crSandboxDBHost || crSandboxRegionHost.MatchString(h)
}

// crSandboxSigner reports whether email is a service account of the sandbox
// project. The domain is pinned, not the account: the owner's ADC needs
// signJwt on whichever account the sandbox's `token_signer` names, and that
// account is the sandbox project's — an account of any other project signs
// tokens this database must not honour.
func crSandboxSigner(email string) bool {
	local, domain, ok := strings.Cut(email, "@")
	return ok && local != "" && domain == crSandboxSignerDomain
}

// crSkip is what crTargetFrom returns when nothing in the environment aims
// this check anywhere: the test skips. An environment that does aim it at a
// database and leaves the opt-in out is a refusal instead, never a skip.
var crSkip = errors.New("the live credentials-rules check is not aimed at a database")

// crTarget is the owner's aim: which installation, reached how. host, not
// rtdbURL, is what a FACT line may name: the host is pinned, and the URL is
// whatever the environment held.
type crTarget struct {
	rtdbURL      string // for rtdb.New; never printed
	host         string // its host, pinned to the sandbox's
	apiKey       string
	signer       string
	installation string
	requestedBy  string
}

// crTargetFrom reads and checks the owner's environment. getenv is a
// parameter, not os.Getenv, so the guard is a pure function of the
// environment: it makes no request, and an ordinary test can drive every
// refusal below without a credential or a network.
//
// It refuses, before anything is reached: a coding agent's session (F33); an
// aim at a database without the opt-in; a missing variable; any installation
// but the sandbox's; any database host but the sandbox's; any signer that is
// not a service account of the sandbox project; and any database URL that is
// not a plain Firebase address — rtdb.ValidateURL is the repository's own
// check for that, and it is what keeps the owner's credential from being sent
// to a host that merely looks like a database.
func crTargetFrom(getenv func(string) string) (crTarget, error) {
	if getenv == nil {
		// An environment that is not there at all is the empty environment:
		// the guard stays a total function of what it is given.
		getenv = func(string) string { return "" }
	}
	t := crTarget{
		rtdbURL:      strings.TrimSpace(getenv(crRTDBVar)),
		apiKey:       strings.TrimSpace(getenv(crAPIKeyVar)),
		signer:       strings.TrimSpace(getenv(crSignerVar)),
		installation: strings.TrimSpace(getenv(crInstallVar)),
		requestedBy:  strings.TrimSpace(getenv(crByVar)),
	}
	optIn := strings.TrimSpace(getenv(crOKVar))
	if t.rtdbURL == "" && t.apiKey == "" && t.signer == "" && t.installation == "" && optIn == "" && !agentenv.Present(getenv) {
		return crTarget{}, crSkip
	}
	if m := agentenv.Marker(getenv); m != "" {
		return crTarget{}, fmt.Errorf("%s is set: this is a coding agent's session, and a check that writes to a real installation's database is the owner's to run at their own terminal (F33); run it outside the agent's session", m)
	}
	if optIn != "1" {
		return crTarget{}, fmt.Errorf("%s is not 1: this check writes to a real installation's database and is yours to run (the credentials-service plan's check 92); set %s=1 to opt in", crOKVar, crOKVar)
	}
	var missing []string
	for _, v := range []struct{ name, value string }{
		{crInstallVar, t.installation}, {crRTDBVar, t.rtdbURL}, {crAPIKeyVar, t.apiKey}, {crSignerVar, t.signer},
	} {
		if v.value == "" {
			missing = append(missing, v.name)
		}
	}
	if len(missing) > 0 {
		return crTarget{}, fmt.Errorf("%s=1 but %s is unset (the same variables check 21's helper needs; see this file's comment)", crOKVar, strings.Join(missing, ", "))
	}
	if t.installation != crSandboxInstall {
		return crTarget{}, fmt.Errorf("%s=%q: this check runs on the sandbox installation %q only (F33: every live step is owner-run, on installation %q and a sandbox repository only, never belong); it refuses every other installation, the dogfood's included", crInstallVar, t.installation, crSandboxInstall, crSandboxInstall)
	}
	if err := rtdb.ValidateURL(t.rtdbURL, false); err != nil {
		return crTarget{}, fmt.Errorf("%s is not a plain Firebase Realtime Database address (https://<db>.firebaseio.com or https://<db>.<region>.firebasedatabase.app, with no credentials, port, path, query or fragment); %s", crRTDBVar, crNoEcho)
	}
	u, err := url.Parse(t.rtdbURL)
	if err != nil {
		return crTarget{}, fmt.Errorf("%s is not a URL (%s)", crRTDBVar, crNoEcho)
	}
	t.host = strings.ToLower(u.Hostname())
	if !crSandboxHost(t.host) {
		return crTarget{}, fmt.Errorf("%s is not the sandbox database's address: this check runs on installation %q, whose database is %s (F33); it refuses every other database, the dogfood's and belong's included, before the owner's credential is sent anywhere", crRTDBVar, crSandboxInstall, crSandboxDBHost)
	}
	if !crSandboxSigner(t.signer) {
		return crTarget{}, fmt.Errorf("%s=%q: the token signer must be a service account of the sandbox project (%s), which is the only project whose tokens this database's rules may honour", crSignerVar, t.signer, crSandboxSignerDomain)
	}
	if t.requestedBy == "" {
		t.requestedBy = crDefaultBy
	}
	return t, nil
}

// crClockTooFar reports whether this machine's clock is too far from the
// database's to run at all. The two *days* may differ while the clocks agree —
// that is the day boundary, and the check works in the database's day — but a
// clock minutes off is a clock to fix, for the reason crClockSkew gives.
func crClockTooFar(serverNow, localNow time.Time) bool {
	skew := serverNow.Sub(localNow)
	return skew > crClockSkew || skew < -crClockSkew
}

// crNames are the scratch names one run of the check works under. Nothing
// here names a real repository, a real run or a real credential: the slug
// carries a hash of a repository that does not exist, the run ids say what
// they are, and the credential names are the check's own.
type crNames struct {
	h                              string // the run's one hex stamp, shared by every scratch name
	repo, slug, run                string
	otherRepo, otherSlug, otherRun string
	n1, n2, n3                     string // scratch credential names
	day, oldDay                    int64  // the database's day, and one DAYOK refuses
	resumeAt                       int64  // the instant the registry entry's waiting_until holds
}

// crNewNames builds one run of the check's scratch names. day is the
// *database's* day — budget.Day of the owner client's ServerNow(), as
// budget.Session reads it — not budget.Day(now): DAYOK compares $day with the
// server's TODAY and YESTERDAY, so a machine whose clock is on the other side
// of midnight would build the accepted counters for the wrong day and the
// refused ones for a day DAYOK admits.
//
// The run ids are not a real run's shape (20260101-120000-abcd): they read as
// what they are. The check deletes the Firebase Auth users its identities
// create, and the uid a run's identity gets is r~<slug>~<run>, so a run id
// that could be mistaken for a real run's would leave a real-looking user
// behind if a cleanup ever failed.
func crNewNames(now time.Time, day int64) crNames {
	h := crHex(3)
	n := crNames{
		h:         h,
		repo:      crRepoBase + h + crSub,
		otherRepo: crRepoBase + h + "-other" + crSub,
		day:       day,
		oldDay:    day - 2, // DAYOK admits the server's today and yesterday only
		resumeAt:  now.Add(15 * time.Minute).UnixMilli(),
	}
	var err error
	if n.slug, err = task.Slug("fake", n.repo); err != nil {
		panic(err)
	}
	if n.otherSlug, err = task.Slug("fake", n.otherRepo); err != nil {
		panic(err)
	}
	n.run, n.otherRun = crRepoBase+h+"-run", crRepoBase+h+"-run2"
	n.n1, n.n2, n.n3 = crRepoBase+h+"-a1", crRepoBase+h+"-a2", crRepoBase+h+"-a3"
	return n
}

func crHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// crCredsMark is a mark under /creds in the shape the service writes (a state,
// two instants and a backoff count). /creds is denied to every client, so the
// shape is not validated by anything; it is what the denial is proven against.
func crCredsMark(now time.Time) map[string]any {
	return map[string]any{"state": "exhausted", "until": now.Add(time.Hour).UnixMilli(), "since": now.UnixMilli(), "backoff": 2}
}

// crCreds is every location under /creds the check names: the nodes the
// service writes there (C1.9), at the depth it writes them. They are built
// once, with budget.Key like every path in this tree, so the table, the seed,
// the denied reads and the cleanup cannot disagree about a path.
type crCreds struct {
	mark1, mark2, mark3, lease, roundrobin string
	currentParent, current                 string // the sticky credential, and the node it lives under
}

// seeded are the nodes the owner writes, and so the ones a denial is proven
// against.
func (c crCreds) seeded() []string {
	return []string{c.mark1, c.mark2, c.lease, c.current, c.roundrobin}
}

// all are every node under /creds the check names, for the reads.
func (c crCreds) all() []string {
	return append(c.seeded(), c.mark3)
}

// cleanup are the nodes the check deletes. It is not all() verbatim: the
// sticky credential itself is named by currentParent instead, because one
// multi-path update may not name a node and its own descendant, and deleting
// the parent deletes the child.
func (c crCreds) cleanup() []string {
	return []string{c.mark1, c.mark2, c.mark3, c.lease, c.currentParent, c.roundrobin}
}

func crCredsPaths(n crNames) crCreds {
	slug, run := budget.Key(n.slug), budget.Key(n.run)
	return crCreds{
		mark1:         "creds/marks/" + budget.Key(n.n1),
		mark2:         "creds/marks/" + budget.Key(n.n2),
		mark3:         "creds/marks/" + budget.Key(n.n3),
		lease:         "creds/leases/" + run,
		currentParent: "creds/current/" + slug,
		current:       "creds/current/" + slug + "/" + run,
		roundrobin:    "creds/roundrobin/" + budget.Key(n.n1),
	}
}

// crCredsReads are every path under /creds the check reads as an identity that
// must be refused: the nodes themselves, and the two parents a reader of the
// plan would name.
func crCredsReads(n crNames) []string {
	return append(crCredsPaths(n).all(), "creds", "creds/marks")
}

// crWritten is every node under the check's scratch names, in one list: the
// cleanup deletes exactly these, and nothing else, whatever happened before it.
// No entry here is an ancestor of another, so they go out in one update, and
// deleting a node that was never written is a no-op.
//
// It is every path the table names that carries a scratch name — both scratch
// repositories, both scratch runs, both days — and not only the ones an
// accepted case writes. A refused case is an *attempt*: the rules are what
// refuse it, and the point of this list is that a live document which wrongly
// accepted one would still leave nothing behind. The paths outside the scratch
// names (the two generation markers) are crUndo's, because the check may not
// delete the deploy's nodes.
func crWritten(n crNames) []string {
	key := budget.Key
	return append([]string{
		"agents/" + key(n.slug),
		"agents/" + key(n.otherSlug),
		budget.PathRun(n.slug, n.run),
		budget.PathRun(n.slug, n.otherRun),
		budget.PathRun(n.otherSlug, n.run),
		budget.PathRun(n.otherSlug, n.otherRun),
		budget.PathSpendRepo(n.day, n.slug),
		budget.PathSpendRepo(n.day, n.otherSlug),
		budget.PathSpendRepo(n.oldDay, n.slug),
		budget.PathSpendRun(n.day, n.slug, n.run),
		budget.PathSpendRun(n.day, n.otherSlug, n.otherRun),
		budget.PathSpendRun(n.oldDay, n.slug, n.run),
		crDevicePath(crPhoneUID(n)),
	}, crCredsPaths(n).cleanup()...)
}

// crCase is one write the run's own identity attempts.
type crCase struct {
	name  string
	path  string
	seed  any // what the owner writes there first (nil: nothing)
	value any // what the run writes (nil: a delete)
	allow bool
	why   string // the rule that decides it
}

// crCases is every write the run's own identity attempts, and what the
// deployed rules must answer. The table is data so that crCheckCases can
// assert its safety invariants before the first write, and it mirrors
// internal/budget/rules' emulator cases (creds_emulator_test.go, build tag
// firebase) family for family: that suite is the CI-run proof of the matrix
// against the generated document, and this is the same matrix against the
// document the installation actually deploys.
//
// maxRes is config/limits/maxReserveMicros, read as the run before the table
// is built; the one case that needs it is the write past the bound.
// requestedBy is the rb claim every identity this check mints carries, and so
// the value of the entry's own requestedBy field: RBOK ties each child of the
// entry to it.
//
// The allowed cases come first, path by path, and crCheckCases enforces that:
// an accepted write changes what the next case on the same path sees.
func crCases(n crNames, maxRes int64, requestedBy string) []crCase {
	entry := budget.PathAgent(n.slug, n.run)
	repoDay := budget.PathSpendRepo(n.day, n.slug) + "/byTier"
	runDay := budget.PathSpendRun(n.day, n.slug, n.run) + "/byTier"
	runLedger := budget.PathRun(n.slug, n.run) + "/byTier"
	oldRepoDay := budget.PathSpendRepo(n.oldDay, n.slug) + "/byTier"
	oldRunDay := budget.PathSpendRun(n.oldDay, n.slug, n.run) + "/byTier"
	otherRepoDay := budget.PathSpendRepo(n.day, n.otherSlug) + "/byTier"
	otherRunLedger := budget.PathRun(n.slug, n.otherRun) + "/byTier"
	otherRepoLedger := budget.PathRun(n.otherSlug, n.run) + "/byTier"
	cp := crCredsPaths(n)

	// amount is what the check's two positive counter writes carry (this
	// repository's tier-B and tier-C day leaves): they are what proves the
	// day leaves accept a real amount, and both live under the scratch slug.
	// With no maxReserveMicros bound deployed the leaves accept only a zero,
	// and the zero is what is written.
	amount := int64(1)
	if maxRes < 1 {
		amount = 0
	}
	return []crCase{
		// The registry entry and the two new children of C1.3 rule 2.
		{name: "create the run's registry entry", path: entry, allow: true,
			value: map[string]any{"repo": n.repo, "auth": "cascade", "startedAt": time.Now().UnixMilli(), "requestedBy": requestedBy},
			why:   "agents/$slug/$run: .write RUN, .validate hasChildren(['repo', 'requestedBy'])"},
		{name: "the name of the credential the run is using", path: entry + "/credential", allow: true, value: n.n1,
			why: "credential: STR && RBOK"},
		{name: "the instant the run expects to resume at", path: entry + "/waiting_until", allow: true, value: n.resumeAt,
			why: "waiting_until: TIMESTAMP && RBOK"},
		// The by-tier split of C1.3 rule 3. The run's own two leaves are
		// written with a zero on purpose: a positive amount needs a
		// reservation, and a reservation is one multi-path write into
		// spend/$day/global/counted and spend/$day/repos/$slug/counted
		// (COUNTERS_FOLLOW) — a rules check must not move the project's
		// counters. Zero is what those two leaves accept with no reservation.
		{name: "the run's own tier-B lifetime counter", path: runLedger + "/b/spent", allow: true, value: int64(0),
			why: "byTier/$tier/spent: $tier is 'b' or 'c', AMOUNT, within the run's reservation (none: zero)"},
		{name: "the run's own tier-B share of the day", path: runDay + "/b/spent", allow: true, value: int64(0),
			why: "the same leaf on the run's day share"},
		{name: "this repository's tier-B day counter", path: repoDay + "/b/spent", allow: true, value: amount,
			why: "spend/$day/repos/$slug/byTier/$tier/spent: TOKEN && fs == $slug && DAYOK, AMOUNT, increase <= maxReserveMicros"},
		{name: "this repository's tier-C day counter", path: repoDay + "/c/spent", allow: true, value: amount,
			why: "the same leaf: the tier guard admits 'c' as it admits 'b'"},

		// The registry entry's children, refused: the credential name is a
		// bounded string tied to the launcher's rb, the waiting time is a
		// timestamp, and the entry is a closed allowlist.
		{name: "a credential name that is a number", path: entry + "/credential", value: 7, why: "credential: STR"},
		{name: "a credential name that is a boolean", path: entry + "/credential", value: true, why: "credential: STR"},
		{name: "a credential name that is an object", path: entry + "/credential", value: map[string]any{"name": n.n1}, why: "credential: STR"},
		{name: "a credential name past the bound", path: entry + "/credential", value: strings.Repeat("x", rules.MaxString+1), why: "credential: STR (at most MaxString characters)"},
		{name: "a field the registry entry does not name", path: entry + "/credential_value", value: n.n1, why: "agents/$slug/$run/$other: .validate false"},
		{name: "a waiting time that is negative", path: entry + "/waiting_until", value: -1, why: "waiting_until: TIMESTAMP (>= 0)"},
		{name: "a waiting time that is fractional", path: entry + "/waiting_until", value: 1.5, why: "waiting_until: TIMESTAMP (whole)"},
		{name: "a waiting time that is a string", path: entry + "/waiting_until", value: "1700000000000", why: "waiting_until: TIMESTAMP (a number)"},
		{name: "a waiting time past the bound", path: entry + "/waiting_until", value: int64(rules.MaxTimestamp) + 1, why: "waiting_until: TIMESTAMP (<= MaxTimestamp)"},

		// The by-tier split, refused: only the run's own leaves, only the
		// tiers the split has, and never past what the run reserved.
		{name: "a tier the split does not have", path: runLedger + "/a/spent", value: int64(0), why: "byTier/$tier: $tier == 'b' || $tier == 'c'"},
		{name: "an upper-case tier", path: runLedger + "/A/spent", value: int64(0), why: "byTier/$tier: $tier == 'b' || $tier == 'c'"},
		{name: "more than the run reserved", path: runLedger + "/b/spent", value: int64(1000), why: "the run's leaf: NEW + released <= reserved"},
		{name: "a tier the run's day share does not have", path: runDay + "/a/spent", value: int64(0), why: "byTier/$tier: $tier == 'b' || $tier == 'c'"},
		{name: "more than the run reserved, on its day share", path: runDay + "/b/spent", value: int64(1000), why: "the day share's leaf: NEW + released <= reserved"},
		{name: "another repository's lifetime counter", path: otherRepoLedger + "/b/spent", value: int64(0), why: "RUN: fs == $slug"},
		{name: "another run's lifetime counter", path: otherRunLedger + "/b/spent", value: int64(0), why: "RUN: fr == $run"},
		{name: "another repository's day counter", path: otherRepoDay + "/b/spent", value: int64(1), why: "spend/$day/repos/$slug: fs == $slug"},
		{name: "another day's repository counter", path: oldRepoDay + "/b/spent", value: int64(1), why: "DAYOK: $day is today or yesterday"},
		{name: "another day's share of the run", path: oldRunDay + "/b/spent", value: int64(1), why: "DAYOK: $day is today or yesterday"},
		{name: "a tier the repository's day counter does not have", path: repoDay + "/a/spent", value: int64(1), why: "byTier/$tier: $tier == 'b' || $tier == 'c'"},
		{name: "a negative counter", path: repoDay + "/b/spent", value: int64(-1), why: "AMOUNT: >= 0"},
		{name: "a fractional counter", path: repoDay + "/b/spent", value: 1.5, why: "AMOUNT: whole micro-dollars"},
		{name: "a counter that is a string", path: repoDay + "/b/spent", value: "1000", why: "AMOUNT: a number"},
		{name: "a counter that falls", path: repoDay + "/b/spent", seed: int64(5), value: int64(1), why: "NEW >= OLD: a counter is increase-only"},
		{name: "a write past maxReserveMicros", path: repoDay + "/b/spent", seed: int64(5), value: int64(5) + maxRes + 1,
			why: "NEW - OLD <= maxReserveMicros"},

		// /creds, refused: the state the service alone writes as its own
		// account (D136, O1). No run token holds a grant anywhere under it.
		{name: "a mark under /creds", path: cp.mark1, value: crCredsMark(time.Now()), why: "creds: .read false, .write false"},
		{name: "a field of a mark under /creds", path: cp.mark1 + "/state", value: "exhausted", why: "creds: .read false, .write false"},
		{name: "a new mark under /creds", path: cp.mark3, value: crCredsMark(time.Now()), why: "creds: .read false, .write false"},
		{name: "a lease under /creds", path: cp.lease, value: map[string]any{"credential": n.n2}, why: "creds: .read false, .write false"},
		{name: "this run's sticky credential", path: cp.current, value: n.n2, why: "creds: .read false, .write false"},
		{name: "the round-robin counter", path: cp.roundrobin, value: 99, why: "creds: .read false, .write false"},
		{name: "deleting a mark under /creds", path: cp.mark1, value: nil, why: "creds: .write false (a delete is a write)"},
		{name: "deleting the round-robin counter", path: cp.roundrobin, value: nil, why: "creds: .write false (a delete is a write)"},

		// The two generation markers, refused: nothing under config is a
		// client's to write, and a run that could write either could make a
		// run pass a gate the installation has not met.
		{name: "the credentials rules marker", path: rules.PathCredsRulesMarker(), value: rules.CredsRulesVersion, why: "config: no .write for any client; the deploy writes it over IAM"},
		{name: "the policy rules marker", path: projectpolicy.PathRulesMarker(), value: projectpolicy.RulesVersion, why: "config: no .write for any client; the deploy writes it over IAM"},
	}
}

// crCheckCases asserts the table's own safety invariants, before anything is
// written: they are what makes the table safe to run at all, and a table that
// breaks one must fail here rather than on a live installation.
//
//  1. a seed names a scratch slug, and lies under neither /creds nor
//     config/: a seed is written by the owner client, which bypasses the rules
//     — no rule stands behind it, so this check is all that does;
//  2. an accepted write names the scratch slug: nothing real is written;
//  3. nothing under /creds, and nothing under config/, is ever accepted;
//  4. no accepted write names the project-wide day counter;
//  5. a delete is never accepted;
//  6. the first case on a path does not seed it, and an accepted case is the
//     first on its path — an accepted write changes what the next case on the
//     same path sees, and a seeded one would change what the accepted case
//     sees.
//
// The refused cases' own paths are deliberately not held to 2: a case that
// names another repository's counter or a generation marker is an *attempt* by
// the run's identity, which the rules refuse (and which crUndo puts back if a
// live document accepts it). Their seeds are held to 1, because a seed is not
// an attempt: the owner writes it.
func crCheckCases(cases []crCase, scratch ...string) error {
	seen := map[string]bool{}
	for i, c := range cases {
		switch {
		case c.name == "":
			return fmt.Errorf("case %d (/ %s) has no name", i, c.path)
		case c.path == "":
			return fmt.Errorf("case %q has no path", c.name)
		case c.why == "":
			return fmt.Errorf("case %q (/ %s) names no rule", c.name, c.path)
		}
		first := !seen[c.path]
		seen[c.path] = true
		if first && c.seed != nil {
			return fmt.Errorf("case %q seeds /%s before anything has read it as it is: the first case on a path must not seed", c.name, c.path)
		}
		if c.seed != nil {
			switch {
			case !crNamesAny(c.path, scratch):
				return fmt.Errorf("case %q seeds /%s, which carries no scratch name: the owner client bypasses the rules, so a seed outside the check's own nodes would be written for real", c.name, c.path)
			case strings.HasPrefix(c.path, "creds/"), strings.HasPrefix(c.path, "config/"):
				return fmt.Errorf("case %q seeds /%s: nothing under /creds or config/ is the check's to write, even as the owner", c.name, c.path)
			}
		}
		if strings.HasPrefix(c.path, "creds/") && c.allow {
			return fmt.Errorf("case %q accepts a write under /creds: no run token holds a grant there (O1)", c.name)
		}
		if strings.HasPrefix(c.path, "config/") && c.allow {
			return fmt.Errorf("case %q accepts a write under config/: nothing under it is a client's to write", c.name)
		}
		if c.value == nil && c.allow {
			return fmt.Errorf("case %q accepts a delete: a delete is not validated, so nothing may accept one", c.name)
		}
		if !c.allow {
			continue
		}
		if c.seed != nil {
			return fmt.Errorf("case %q is accepted and seeds its own path", c.name)
		}
		if !first {
			return fmt.Errorf("case %q is accepted but another case on /%s ran before it", c.name, c.path)
		}
		if !crNamesAny(c.path, scratch) {
			return fmt.Errorf("case %q is accepted and names no scratch repository: /%s would write something real", c.name, c.path)
		}
		if strings.Contains(c.path, "/global/") {
			return fmt.Errorf("case %q is accepted and names the project-wide day counter: /%s", c.name, c.path)
		}
	}
	return nil
}

func crNamesAny(path string, scratch []string) bool {
	for _, s := range scratch {
		if s != "" && strings.Contains(path, s) {
			return true
		}
	}
	return false
}

// crIdentity is one identity the check mints besides the run's own, and what
// the deployed rules must answer it under /creds. The families are the
// emulator's (internal/budget/rules/creds_emulator_test.go,
// TestCredsTokenOfAnotherRepositoryDenied): the denial is the node's, not the
// run's — another repository's run, another run of this repository, and the
// three identities the rules read (a phone's mu, a consumer's mc, a pairing
// session's pu) are refused exactly as this run is. The last three matter for
// a second reason: they are the only claims admitted anywhere outside a run's
// own families, and /creds must not be one of the families they reach.
type crIdentity struct {
	name string
	// slug and run make a run token: the launcher's own shape, minted through
	// token.MintAt as every run token is. Its uid is r~<slug>~<run>.
	slug, run string
	// uid and claims make a claim token: the uid the custom token names and
	// the claims the exchange copies into the ID token. token.MintAt cannot
	// express these — its uid is always a run's — and the three identities the
	// rules read are not run tokens.
	uid    string
	claims map[string]any
	// device is whether the owner writes config/admin/devices/<uid> first. A
	// phone's token reaches nothing without it (D166's USER conjunct), and a
	// denial from a token the rules do not honour anywhere proves nothing
	// about /creds. The node is the check's to delete again (crWritten).
	device bool
	// reach is a path this identity must be able to read: its positive
	// control, without which "it was refused /creds" is also the answer a
	// token the rules ignore entirely would give. "" where the identity's only
	// grant is a write this check does not make (the consumer's is a mailbox).
	//
	// It is a path in the deployed document, and a test walks
	// rules.Generate()'s document to it and asserts the read its family is
	// read by: a reach the rules do not grant would make the check fail every
	// time it ran, for a reason that is not the rules.
	reach string
}

// crPhoneUID, crConsumerUID and crPairingUID are the three claim identities'
// uids: scratch names, obviously not a real mailbox's, a real consumer's or a
// real pairing session's.
func crPhoneUID(n crNames) string    { return crRepoBase + n.h + "-phone" }
func crConsumerUID(n crNames) string { return crRepoBase + n.h + "-consumer" }
func crPairingUID(n crNames) string  { return "p~" + crRepoBase + n.h + "-pairing" }

// crDevicePath is the device allowlist node D166's USER conjunct reads.
func crDevicePath(uid string) string { return rules.PathDevices + "/" + budget.Key(uid) }

// crIdentities are the identities the check mints, in the order it walks them.
func crIdentities(n crNames) []crIdentity {
	phone, consumer, pairing := crPhoneUID(n), crConsumerUID(n), crPairingUID(n)
	return []crIdentity{
		{name: "another repository's run", slug: n.otherSlug, run: n.run},
		{name: "another run of this repository", slug: n.slug, run: n.otherRun},
		{name: "the phone", uid: phone, claims: map[string]any{"mu": phone}, device: true, reach: "config/caps/global"},
		{name: "the consumer", uid: consumer, claims: map[string]any{"mc": consumer}},
		// The pairing subtree is at the database's root (pairing/$uid/...), not
		// under config/: the read its family is granted is the delivery node
		// there, and it is spelled with the root path the document has.
		{name: "the pairing session", uid: pairing, claims: map[string]any{"pu": pairing}, reach: "pairing/" + budget.Key(pairing) + "/delivery"},
	}
}

// expected are the claims the identity's ID token must carry, beyond the uid:
// for a run token the fs/fr pair every rule in this tree reads, for a claim
// token the claim itself. The check asserts them, because an identity the
// exchange did not really produce would be refused /creds for a reason that is
// not the rules.
func (id crIdentity) expected() map[string]any {
	if id.uid != "" {
		return id.claims
	}
	return map[string]any{"fs": budget.Key(id.slug), "fr": budget.Key(id.run)}
}

// crIDTokenClaims decodes an ID token's payload — the claims only, never the
// token: what the check needs to know is that the identity it minted is the
// one it holds. It is a pure function of the string, so an ordinary test
// covers it.
func crIDTokenClaims(idToken string) (map[string]any, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWT")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("the payload is not base64url")
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, errors.New("the payload is not JSON")
	}
	return m, nil
}

// crSameJSON compares a value read back from the database with the value that
// was written, as JSON: the database may reorder keys and stores every number
// the same way.
func crSameJSON(got, want any) bool {
	b, err := json.Marshal(want)
	if err != nil {
		return false
	}
	var w any
	if json.Unmarshal(b, &w) != nil {
		return false
	}
	return reflect.DeepEqual(got, w)
}

// crJSON is a value as JSON text, for a FACT line or a failure message.
func crJSON(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// crCaseNames is the case names, comma separated, for one FACT line: which
// writes were refused, without one line each.
func crCaseNames(cases []crCase) string {
	out := make([]string, 0, len(cases))
	for _, c := range cases {
		out = append(out, c.name)
	}
	return strings.Join(out, "; ")
}

// crSecretsIn returns one sentence per FACT line that carries a secret. The
// line is scrubbed before it is quoted, so the report cannot leak what it
// reports.
func crSecretsIn(lines, secrets []string) []string {
	var out []string
	for _, line := range lines {
		for _, s := range secrets {
			if s != "" && strings.Contains(line, s) {
				out = append(out, "a FACT line carries a secret ("+fmt.Sprint(len(s))+" characters): "+crScrub(line, secrets))
			}
		}
	}
	return out
}

// crScrub replaces every registered secret in s.
func crScrub(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "[redacted]")
		}
	}
	return s
}
