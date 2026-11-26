package credsvc

// The personal-pool route (R26, R9, D3179 as amended; plan task PL9).
//
// A personal pool is one PERSON's own pool in a project: the containers of
// theirs a run they launch may be served from. It is not the project's shared
// pool and not a pool anyone publishes — a member of the project cannot write
// its config or its policy (F61) — so its members and its rule are kept HERE,
// in a store of the service's own, and written through this route and nowhere
// else that matters.
//
// The route is the LAUNCHER class's, for the designation route's reason one
// step further (designation_route.go): a person records what their OWN runs are
// served from, the runs it answers for do not exist yet, and no body of either
// route has a field a principal could be written in. What this route keys on is
// the Google account id (`sub`) the caller's own identity token carries — the
// same identity a per-user container's name is built from (secretname.User), so
// **a token can only ever enter its owner's pool**: the member of a personal
// pool is a container of the caller's own, and a name that would build another
// person's container is refused before the record is written (R9).
//
// What the record holds is names and never a value (F19): the pool's name, its
// rule and the names of the containers in it. No command and no view shows a
// value to anyone, an admin included, and nothing here reads one.
//
// The store is `creds/personal_pools/<subject>/<pool>` — the service's own
// subtree, beside the credential state and under the same denial (the rules
// refuse the whole of creds/ to every run token) — and the record carries the
// principal it was keyed by, so a node written by anything that did not come
// through this route is refused rather than read.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// PersonalPoolPath records the caller's own personal pool of this project: one
// POST whose body is the pool's whole record — its name, its rule and the names
// of the members it holds — answered with the record the service kept.
const PersonalPoolPath = "/v1/pool/personal"

// PersonalPoolGetPath reads the caller's own personal pool back: one POST whose
// body names the pool and which answers the record it holds, or refuses where
// the caller has none of that name. It is a POST rather than a GET because its
// body is what it is a read OF, and a name in a URL is a name in a log.
const PersonalPoolGetPath = "/v1/pool/personal/get"

// pathPersonalPools is the node one person's own pools live under.
const pathPersonalPools = "creds/personal_pools"

// personalPoolAttempts bounds the compare-and-set a write retries: the same
// three attempts every other store of this service uses (creds.DefaultAttempts).
const personalPoolAttempts = 3

// PersonalPoolRequest is the declare route's body: the pool's name, its rule
// and the WHOLE list of the members it holds.
//
// There is deliberately no principal member, for the designation route's
// reason: a personal pool is a person's own, so the principal is the subject
// the service verified and a body that named one is a request of a shape this
// route does not have, which the strict decode refuses.
//
// The list is the whole record every time (the idiom `fugaro accounts pool add`
// already has): a member left out is a member dropped, which is how a member is
// removed — the token itself is the person's own and stays theirs until they
// remove it with `fugaro secrets user rm`.
type PersonalPoolRequest struct {
	Pool    string   `json:"pool"`
	Rule    string   `json:"rule,omitempty"`
	Members []string `json:"members,omitempty"`
}

// PersonalPoolGetRequest is the read route's body: the pool's name alone.
type PersonalPoolGetRequest struct {
	Pool string `json:"pool"`
}

// PersonalPoolRecord is what the service keeps and answers: whose pool it is,
// the project it belongs to, and the rule and the member NAMES it holds. It
// carries no value, for anyone.
type PersonalPoolRecord struct {
	// Principal is whose pool it is: the person's Google subject, which is the
	// subject the route verified and the id the node is keyed by (R7).
	Principal routing.Principal `json:"principal"`
	// Project is the project the pool belongs to. A personal pool's members are
	// that project's per-user containers (D3179 as amended, F55), so a record
	// answers for one project and no other.
	Project string `json:"project"`
	// Pool is the pool's name, the one `fugaro accounts pool ls` lists and a
	// launch may name with `--pool`.
	Pool string `json:"pool"`
	// Rule is how the service picks among the pool's members, resolved and never
	// empty: a personal pool names no `strategy` to fall back on, so a request
	// that names no rule is its own default (Rule.RuleOrDefault).
	Rule routing.Rule `json:"rule"`
	// Members are the containers the pool holds, by NAME and never by value
	// (F19): each is the name half of one `fugaro_u_<subject>_<name>` of the
	// principal's own.
	Members []string `json:"members"`
}

// readPersonalPoolRequest reads the declare route's body out of the call the
// handler already read (readCall), where the caller's own token was registered
// with the service's redaction for the length of the call. The decode is strict,
// as every body of this service is.
func readPersonalPoolRequest(body []byte) (PersonalPoolRequest, error) {
	var req PersonalPoolRequest
	if err := decodeStrict(body, &req); err != nil {
		return PersonalPoolRequest{}, errors.New("the body is not one JSON object of this route's fields: a personal pool names its pool, its rule and the members it holds, and the principal is the one the service verified")
	}
	if msg := config.ValidCredentialRef(req.Pool); msg != "" {
		return PersonalPoolRequest{}, errors.New("the call names no pool: " + msg)
	}
	if !routing.Rule(req.Rule).Valid() {
		return PersonalPoolRequest{}, errors.New("the call names no rule this release serves pools by: " + strings.Join(ruleWords(), ", "))
	}
	seen := make(map[string]bool, len(req.Members))
	for _, m := range req.Members {
		if seen[m] {
			return PersonalPoolRequest{}, errors.New("a member is listed twice: a pool walks each member once")
		}
		seen[m] = true
	}
	return req, nil
}

func readPersonalPoolGetRequest(body []byte) (PersonalPoolGetRequest, error) {
	var req PersonalPoolGetRequest
	if err := decodeStrict(body, &req); err != nil {
		return PersonalPoolGetRequest{}, errors.New("the body is not one JSON object of this route's fields: this route names the pool it reads")
	}
	if msg := config.ValidCredentialRef(req.Pool); msg != "" {
		return PersonalPoolGetRequest{}, errors.New("the call names no pool: " + msg)
	}
	return req, nil
}

// ruleWords is the rule vocabulary as a sentence, from routing's own list.
func ruleWords() []string {
	out := make([]string, 0, len(routing.Rules))
	for _, r := range routing.Rules {
		out = append(out, string(r))
	}
	return out
}

// personalPoolRefusals, one sentence each, and none carries a value: the pool is
// named by the answer's own field and never repeated into a refusal, which is
// what keeps a caller's own text out of an operator's log (the package's
// no-echo rule).
const (
	personalPoolNoRunIdentity = "this route takes no run identity: a personal pool is its owner's own act, and a caller presenting one is a call of a shape it does not have"
	personalPoolNotMigrated   = "this project publishes no pools yet: `fugaro update` migrates them, and a pool written before it would resolve nothing"
	personalPoolNotAPerson    = "a personal pool is one person's own: a service account's runs are served from a project pool an admin fills (D3181)"
	personalPoolMemberForeign = "a member of a personal pool is a container of your own: the name given is not one this caller's own containers could carry (R9)"
	personalPoolStoreUnread   = "your personal pool could not be read"
	personalPoolStoreUnwritt  = "your personal pool could not be written"
	personalPoolNoSuchPool    = "you have no personal pool of that name in this project"
)

// PersonalPoolNoSuchPoolRefusal is the refusal of a read of a pool the caller has
// not declared, as a sentence the CLI compares against: "not declared yet" and
// "the store could not be read" are two states one command apart, and the client
// has to tell them apart without reading the service's prose by eye.
const PersonalPoolNoSuchPoolRefusal = personalPoolNoSuchPool

// poolPersonal is the declare route's handler: verify the launcher, read the
// body, hold the members to the caller's own containers, and write the record.
func (s *Server) poolPersonal(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	caller, ok := s.personalPoolCaller(ctx, w, r)
	if !ok {
		return
	}
	cl, err := readCall(r, []string{caller.token})
	if err != nil {
		s.refusePersonal(ctx, w, caller.account, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	req, err := readPersonalPoolRequest(cl.body)
	if err != nil {
		s.refusePersonal(ctx, w, caller.account, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	rec, ok := s.personalPoolRecord(ctx, w, caller, req)
	if !ok {
		return
	}
	if !s.personalPoolReady(ctx, w, caller.account) {
		return
	}
	db, ok := s.cfg.DB.(creds.DB)
	if !ok {
		s.refusePersonal(ctx, w, caller.account, StatusUnavailable, ErrorStateUnavailable, personalPoolStoreUnwritt)
		return
	}
	if err := writePersonalPool(ctx, db, rec); err != nil {
		// The refusal names the field and never the record: the write's own
		// error carries a node path and the decode's own words, and neither
		// belongs in an answer to a caller.
		s.log.Error("credsvc: a personal pool could not be written", "error", err)
		s.refusePersonal(ctx, w, caller.account, StatusUnavailable, ErrorStateUnavailable, personalPoolStoreUnwritt)
		return
	}
	s.auditPersonalPool(ctx, caller.account, StatusOK)
	writePersonalPoolAnswer(w, rec)
}

// poolPersonalGet is the read route's handler: verify the launcher, read the
// body, and answer the caller's own record or refuse.
func (s *Server) poolPersonalGet(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	caller, ok := s.personalPoolCaller(ctx, w, r)
	if !ok {
		return
	}
	cl, err := readCall(r, []string{caller.token})
	if err != nil {
		s.refusePersonal(ctx, w, caller.account, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	req, err := readPersonalPoolGetRequest(cl.body)
	if err != nil {
		s.refusePersonal(ctx, w, caller.account, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if !s.personalPoolReady(ctx, w, caller.account) {
		return
	}
	db, ok := s.cfg.DB.(creds.DB)
	if !ok {
		s.refusePersonal(ctx, w, caller.account, StatusUnavailable, ErrorStateUnavailable, personalPoolStoreUnread)
		return
	}
	rec, found, err := readPersonalPool(ctx, db, caller.principal, req.Pool)
	if err != nil {
		s.log.Error("credsvc: a personal pool could not be read", "error", err)
		s.refusePersonal(ctx, w, caller.account, StatusUnavailable, ErrorStateUnavailable, personalPoolStoreUnread)
		return
	}
	if !found {
		// A pool the caller does not have is a request refused and not an empty
		// answer: the two are one command's apart in the CLI, and a reader that
		// took "none" for "could not look" would add a member to nothing.
		s.refusePersonal(ctx, w, caller.account, StatusRequestRefused, ErrorRequestRefused, requestRefused+personalPoolNoSuchPool)
		return
	}
	s.auditPersonalPool(ctx, caller.account, StatusOK)
	writePersonalPoolAnswer(w, rec)
}

// personalPoolCaller is the caller both routes are answered for: the launcher
// class's own verification, the run-identity refusal the designation route
// answers, and the one fact this route adds — a personal pool is a PERSON's, so
// a service account's call is refused in words that name where its pool comes
// from instead.
type personalPoolCaller struct {
	token     string
	account   string
	principal routing.Principal
}

func (s *Server) personalPoolCaller(ctx context.Context, w http.ResponseWriter, r *http.Request) (personalPoolCaller, bool) {
	tok, err := bearerFromHTTP(r)
	if err != nil {
		// No line: the call is refused before its caller was verified, which is
		// the one refusal on this route with nothing to name.
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return personalPoolCaller{}, false
	}
	caller, err := s.launcherCaller(ctx, tok)
	if err != nil {
		s.refuseLauncher(w, err)
		return personalPoolCaller{}, false
	}
	if r.Header.Get(HeaderRunIdentity) != "" || r.Header.Get(HeaderRunJob) != "" {
		s.refusePersonal(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+personalPoolNoRunIdentity)
		return personalPoolCaller{}, false
	}
	if caller.Principal.Kind != routing.PrincipalPerson {
		s.refusePersonal(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+personalPoolNotAPerson)
		return personalPoolCaller{}, false
	}
	return personalPoolCaller{token: tok, account: caller.Account, principal: caller.Principal}, true
}

// personalPoolReady is the state both routes need before either reads or writes
// a record: the stop switches (read HERE, because these routes are answered
// ahead of the shared parse that reads them for every other route — a stopped
// service decides nothing) and the project's migration (a pool written before
// `model_routing` is published would resolve nothing).
func (s *Server) personalPoolReady(ctx context.Context, w http.ResponseWriter, account string) bool {
	on, which, err := s.cfg.Kills.Stop(ctx, "")
	if err != nil {
		s.refusePersonal(ctx, w, account, StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return false
	}
	if on {
		s.refusePersonal(ctx, w, account, StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
		return false
	}
	shared, found, err := sharedPolicy(ctx, s)
	if err != nil {
		s.refusePersonal(ctx, w, account, StatusUnavailable, ErrorStateUnavailable, "the project's policy could not be read")
		return false
	}
	if !found || !migrated(shared) {
		s.refusePersonal(ctx, w, account, StatusRequestRefused, ErrorRequestRefused, requestRefused+personalPoolNotMigrated)
		return false
	}
	return true
}

// personalPoolRecord is the record a declare writes: the principal the service
// verified, this deployment's own project, the pool and the rule (resolved), and
// the members held to the caller's own containers.
//
// The member check is the whole of "nobody else can add a token to it" on this
// side (R9): the container a member names is built from the caller's OWN subject
// (secretname.User), so a name that would build somebody else's container — or
// that no container could carry at all — is refused before anything is written.
func (s *Server) personalPoolRecord(ctx context.Context, w http.ResponseWriter, caller personalPoolCaller, req PersonalPoolRequest) (PersonalPoolRecord, bool) {
	project, ok := installationProject(s)
	if !ok {
		s.refusePersonal(ctx, w, caller.account, StatusUnavailable, ErrorStateUnavailable, "the installation's own project could not be read")
		return PersonalPoolRecord{}, false
	}
	members := make([]string, 0, len(req.Members))
	for _, m := range req.Members {
		if _, err := secretname.User(caller.principal.ID, m); err != nil {
			s.refusePersonal(ctx, w, caller.account, StatusRequestRefused, ErrorRequestRefused, requestRefused+personalPoolMemberForeign)
			return PersonalPoolRecord{}, false
		}
		members = append(members, m)
	}
	return PersonalPoolRecord{
		Principal: caller.principal,
		Project:   project,
		Pool:      req.Pool,
		Rule:      routing.Rule(req.Rule).RuleOrDefault(""),
		Members:   members,
	}, true
}

// auditPersonalPool writes the route's one line: the caller the service verified
// and the purpose, and no run, no job and no credential — the act is a
// launcher's own statement about the containers of its own, and it touches no
// bundle entry (credentiallessPurpose). It is the designation's own purpose: the
// same class of caller, the same shape of act, and a record of names either way.
func (s *Server) auditPersonalPool(ctx context.Context, caller, status string) {
	purpose, credential := routeAct(PersonalPoolPath)
	if err := s.audit.Write(ctx, AuditEvent{Caller: caller, Credential: credential, Purpose: purpose, Status: status}); err != nil {
		// The service's own log line is where an operator reads a line it could
		// not record, for the reason refuseGrant gives: the answer to the caller
		// is settled either way.
		s.log.Error("credsvc: a personal pool route's line could not be audited", "error", err)
	}
}

// refusePersonal writes one refusal of this route, with the one audit line every
// refusal after verification owes (the package's rule). No sentence it is handed
// carries a credential or a caller's own text.
func (s *Server) refusePersonal(ctx context.Context, w http.ResponseWriter, caller string, status int, typ, message string) {
	s.auditPersonalPool(ctx, caller, typ)
	s.refuse(w, status, typ, message)
}

// writePersonalPoolAnswer writes the record the call wrote or read: names only,
// no-store, and with its members a list whatever the caller built in memory — a
// pool with no member is a pool its owner has not filled, and a reader should
// never have to tell that from a writer that forgot to say.
func writePersonalPoolAnswer(w http.ResponseWriter, rec PersonalPoolRecord) {
	if rec.Members == nil {
		rec.Members = []string{}
	}
	body, err := json.Marshal(rec)
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		WriteWireError(w, StatusUnavailable, WireError{Error: ErrorStateUnavailable, Message: personalPoolStoreUnwritt})
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// personalPoolPath is the node one pool of one principal lives at. The two
// segments are escaped with budget.Key, the same escape every node of this
// service's stores uses, so a principal id and a pool name are one spelling
// there and in a rule that compares them.
func personalPoolPath(principal routing.Principal, pool string) string {
	return pathPersonalPools + "/" + personalSeg("principal id", principal.ID) + "/" + personalSeg("pool", pool)
}

// personalSeg escapes one segment and panics on an empty one, exactly as
// creds' own seg does and for the same reason: an empty segment collapses the
// path onto a different node, and neither a verified principal's id nor a
// validated pool name is empty by the time one reaches here.
func personalSeg(what, v string) string {
	if v == "" {
		panic("credsvc: empty " + what + " in a personal pool's path")
	}
	return budget.Key(v)
}

// errPersonalPool is the refusal of a record this code will not read or write:
// a record whose principal is not the one its node is keyed by, or one that
// names no project or no pool. Every refusal wraps it, so a caller has one
// sentinel and no refusal carries a value.
var errPersonalPool = errors.New("credsvc: the personal pool record is not one this service may read or write")

// readPersonalPool reads one principal's pool of one name. found is false for a
// pool its owner has not declared — a node that is not there at all — and that
// is not an error.
//
// A node that is present but unreadable IS an error, and never "no pool": a
// record half understood is one the read route would refuse over with the wrong
// sentence, and the two answers have to stay tellable apart. It is refused for a
// key this release does not model (every key is strict) and for a record whose
// principal is not the one the node is keyed by.
func readPersonalPool(ctx context.Context, db creds.DB, principal routing.Principal, pool string) (PersonalPoolRecord, bool, error) {
	path := personalPoolPath(principal, pool)
	var raw json.RawMessage
	_, found, err := db.GetETag(ctx, path, &raw)
	if err != nil {
		return PersonalPoolRecord{}, false, fmt.Errorf("credsvc: reading %s: %w", path, err)
	}
	if !found || len(raw) == 0 || projectpolicy.IsNull(raw) {
		return PersonalPoolRecord{}, false, nil
	}
	var rec PersonalPoolRecord
	if err := decodeStrict(raw, &rec); err != nil {
		return PersonalPoolRecord{}, false, fmt.Errorf("credsvc: reading %s: %w", path, err)
	}
	if err := checkPersonalPool(rec, principal, pool); err != nil {
		return PersonalPoolRecord{}, false, fmt.Errorf("credsvc: reading %s: %w", path, err)
	}
	return rec, true, nil
}

// writePersonalPool records one principal's pool, replacing whatever the node
// held: the member list a caller sends is the whole list, so a pool its owner
// moved a member off has to lose it.
//
// The write is conditional on the value read, so two writers of the same pool
// leave one of the two answers rather than a torn one. A node that already holds
// a record naming another principal is refused instead of clobbered: one
// principal's node is never another's to write, whatever path a caller worked
// out.
func writePersonalPool(ctx context.Context, db creds.DB, rec PersonalPoolRecord) error {
	if err := checkPersonalPool(rec, rec.Principal, rec.Pool); err != nil {
		return err
	}
	if rec.Members == nil {
		rec.Members = []string{}
	}
	path := personalPoolPath(rec.Principal, rec.Pool)
	for i := 0; i < personalPoolAttempts; i++ {
		var raw json.RawMessage
		etag, found, err := db.GetETag(ctx, path, &raw)
		if err != nil {
			return fmt.Errorf("credsvc: writing %s: %w", path, err)
		}
		if found && len(raw) > 0 && !projectpolicy.IsNull(raw) {
			var cur PersonalPoolRecord
			if err := decodeStrict(raw, &cur); err != nil {
				return fmt.Errorf("credsvc: writing %s: %w", path, err)
			}
			if cur.Principal != rec.Principal {
				return fmt.Errorf("%w: it would replace a record that names another principal", errPersonalPool)
			}
		}
		err = db.PutIfMatch(ctx, path, etag, rec)
		if errors.Is(err, rtdb.ErrPrecondition) {
			continue
		}
		if err != nil {
			return fmt.Errorf("credsvc: writing %s: %w", path, err)
		}
		return nil
	}
	return fmt.Errorf("credsvc: writing %s: another writer kept changing the node", path)
}

// checkPersonalPool is the whole of what makes a record readable at a node: it
// names the principal the node is keyed by, the pool the node names, a project,
// and a rule this release serves pools by.
//
// A record naming another principal is refused rather than read: the path is
// built from the principal a caller verified, so a record there that named
// somebody else was written by something that did not go through the route — and
// reading it would be a pool serving another person's containers, which is
// exactly what this route exists to refuse (R26, R9).
func checkPersonalPool(rec PersonalPoolRecord, principal routing.Principal, pool string) error {
	switch {
	case !rec.Principal.Valid() || rec.Principal.Kind != routing.PrincipalPerson:
		return fmt.Errorf("%w: it names no person", errPersonalPool)
	case rec.Principal != principal:
		return fmt.Errorf("%w: it names another principal than the node it is stored under", errPersonalPool)
	case rec.Pool == "" || rec.Pool != pool:
		return fmt.Errorf("%w: it names another pool than the node it is stored under", errPersonalPool)
	case rec.Project == "":
		return fmt.Errorf("%w: it names no project", errPersonalPool)
	case !rec.Rule.Valid() || rec.Rule == "":
		return fmt.Errorf("%w: it names no rule this release serves pools by", errPersonalPool)
	}
	return nil
}
