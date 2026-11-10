package intake

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// handlers.go is the four routes' bodies. Every one of them ends in call's
// answer or refuse, which is where the call's single audit line is written;
// none of them touches a value except to hand it to AddVersion, and none of
// them ever reads one back.

// owner is whose container a call is about, and it is derived from the
// caller's verified identity and the name — never from the request.
type owner struct {
	// sub is the account id the container's own name carries. It is empty for
	// a project-shared container, whose name carries none.
	sub string
	// email is the verified address a per-user container's owner annotations
	// carry, and empty for a shared one.
	email string
	// creator is the verified address that is writing, recorded as
	// created_by. For a per-user container it is the owner's own address;
	// for a shared one it is the admin who made it first.
	creator string
	// shared reports whether this is the project's prefix, which only an
	// admin on the derived list may write.
	shared bool
}

// The handler's own sentinels, each of which fail() maps to one refusal. They
// are separate from the wire's error types because a handler error is a fact
// about what happened and the type is the contract's word for it: several of
// these could be one status, and one of them (state_unavailable) is every
// error this file did not plan for.
var (
	// errNotAdmin is a caller that is not on the derived admin list asking for
	// the project-shared prefix (D1935, D1940).
	errNotAdmin = errors.New("the caller is not on the derived admin list")
	// errNoSuchSecret is a rotation or a delete of a container that is not
	// there.
	errNoSuchSecret = errors.New("no such container")
	// errForeignContainer is an existing container whose shape is not the
	// intake's own (R12).
	errForeignContainer = errors.New("the container is not the intake's own")
	// errRequestRefused is the request itself: a body this release cannot
	// read, a name outside R1's grammar, or a scope it does not have.
	errRequestRefused = errors.New("the request is refused")
)

// set is CreatePath's and VersionPath's body. The two differ in one respect
// and it is the point of having both: create may make the container, and a
// version must find one — a rotation into a name typed wrong is refused rather
// than becoming a new credential.
func (s *Server) set(w http.ResponseWriter, r *http.Request, op string) {
	c := &call{s: s, w: w, ev: AuditEvent{Op: op}}
	caller, ok := c.caller(r)
	if !ok {
		return
	}
	var req CreateRequest
	if !c.body(r, &req) {
		return
	}
	c.ev.Scope = string(req.Scope)
	value, err := decodeValue(req.Value)
	if err != nil {
		c.fail(err)
		return
	}
	// The line is armed with the value before anything else can write it: a
	// handler that holds a credential cannot reach an answer without saying
	// which one it held.
	c.value = value
	o, id, err := s.container(r.Context(), caller, req.Scope, req.Name)
	if err != nil {
		c.fail(err)
		return
	}
	c.ev.ID = id
	created, err := s.ensure(r.Context(), id, o, op == OpVersion)
	if err != nil {
		c.fail(err)
		return
	}
	version, err := s.cfg.Secrets.AddVersion(r.Context(), id, value)
	if err != nil {
		c.fail(err)
		return
	}
	c.answer(http.StatusOK, SecretAnswer{ID: id, Version: version, Created: created})
}

// list is ListPath's body: the caller's own containers, or — an admin alone —
// the containers a member owns, found by the owner annotation (R10). It
// answers names and dates, and there is no version's value anywhere in it.
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	c := &call{s: s, w: w, ev: AuditEvent{Op: OpList}}
	caller, ok := c.caller(r)
	if !ok {
		return
	}
	var req ListRequest
	if !c.body(r, &req) {
		return
	}
	c.ev.Scope = string(req.Scope)
	switch req.Scope {
	case ScopeUser:
		if req.Owner != "" {
			// A member named by address: the admin's lookup, and the one the
			// offboarding command reads a member's containers through.
			c.ev.Owner = req.Owner
			if err := s.adminOnly(r.Context(), caller); err != nil {
				c.fail(err)
				return
			}
			found, err := s.byOwner(r.Context(), req.Owner)
			if err != nil {
				c.fail(err)
				return
			}
			c.answer(http.StatusOK, ListAnswer{Secrets: listed(found)})
			return
		}
		found, err := s.cfg.Secrets.List(r.Context(), ScopeUser)
		if err != nil {
			c.fail(err)
			return
		}
		// The scope's containers, and only the caller's own: the account id is
		// the token's, and the name a container carries is what says whose it
		// is (R3, R12).
		var own []Secret
		for _, sec := range found {
			if n, err := secretname.Parse(sec.ID); err == nil && !n.Shared() && n.ID == caller.Sub {
				own = append(own, sec)
			}
		}
		c.answer(http.StatusOK, ListAnswer{Secrets: listed(own)})
	case ScopeShared:
		if req.Owner != "" {
			c.fail(fmt.Errorf("%w: a project-shared container has no owner to name: it is the project's, and the derived admin list is who writes it", errRequestRefused))
			return
		}
		if err := s.adminOnly(r.Context(), caller); err != nil {
			c.fail(err)
			return
		}
		found, err := s.cfg.Secrets.List(r.Context(), ScopeShared)
		if err != nil {
			c.fail(err)
			return
		}
		c.answer(http.StatusOK, ListAnswer{Secrets: listed(found)})
	default:
		c.fail(scopeRefused(req.Scope))
	}
}

// remove is DeletePath's body: one named container, or — an admin naming a
// member by address — every container that member owns, which is offboarding
// (DS2.4, R10).
func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	c := &call{s: s, w: w, ev: AuditEvent{Op: OpDelete}}
	caller, ok := c.caller(r)
	if !ok {
		return
	}
	var req DeleteRequest
	if !c.body(r, &req) {
		return
	}
	c.ev.Scope = string(req.Scope)
	switch {
	case req.Name == "" && req.Owner == "":
		c.fail(fmt.Errorf("%w: a delete names exactly one of a credential's name or the owner of a prefix to sweep", errRequestRefused))
	case req.Name != "" && req.Owner != "":
		c.fail(fmt.Errorf("%w: a delete names a credential's name or an owner, never both", errRequestRefused))
	case req.Owner != "":
		if req.Scope != ScopeUser {
			c.fail(fmt.Errorf("%w: only a per-user container has an owner to sweep, and this call names the %s scope", errRequestRefused, pluginwire.Printable(string(req.Scope))))
			return
		}
		c.ev.Owner = req.Owner
		if err := s.adminOnly(r.Context(), caller); err != nil {
			c.fail(err)
			return
		}
		found, err := s.byOwner(r.Context(), req.Owner)
		if err != nil {
			c.fail(err)
			return
		}
		// Every container is put to the shape check before any of them is
		// removed, so a sweep is all or nothing: a member whose prefix holds
		// one container the intake did not make is a state an operator has to
		// look at, and half a sweep is one they would have to notice.
		for _, sec := range found {
			if err := checkShape(sec.ID, sec, ownerOf(sec)); err != nil {
				c.fail(err)
				return
			}
		}
		deleted := make([]string, 0, len(found))
		for _, sec := range found {
			if err := s.cfg.Secrets.Delete(r.Context(), sec.ID); err != nil {
				c.fail(err)
				return
			}
			deleted = append(deleted, sec.ID)
		}
		// A member who owns nothing is a sweep that found nothing to do, and
		// it is answered as done: the act's whole point is that nothing of
		// theirs is left, and the audit line names the member either way.
		c.answer(http.StatusOK, DeleteAnswer{Deleted: deleted})
	default:
		o, id, err := s.container(r.Context(), caller, req.Scope, req.Name)
		if err != nil {
			c.fail(err)
			return
		}
		c.ev.ID = id
		existing, found, err := s.cfg.Secrets.Describe(r.Context(), id)
		if err != nil {
			c.fail(err)
			return
		}
		if !found {
			c.fail(errNoSuchSecret)
			return
		}
		// A delete is a write to an existing container, so the same check
		// applies as to a version (R12): removing what this intake did not
		// make is a destruction it cannot account for.
		if err := checkShape(id, existing, o); err != nil {
			c.fail(err)
			return
		}
		if err := s.cfg.Secrets.Delete(r.Context(), id); err != nil {
			c.fail(err)
			return
		}
		c.answer(http.StatusOK, DeleteAnswer{Deleted: []string{id}})
	}
}

// container answers the secret ID a call is about, and whose container it is.
// The prefix comes from the caller's verified account id and the name, and
// from nothing else (R3); the shared prefix is an admin's alone, checked
// before the name is even looked at.
func (s *Server) container(ctx context.Context, caller credsvc.LauncherCaller, scope Scope, name string) (owner, string, error) {
	switch scope {
	case ScopeUser:
		id, err := secretname.User(caller.Sub, name)
		if err != nil {
			return owner{}, "", fmt.Errorf("%w: %w", errRequestRefused, err)
		}
		return owner{sub: caller.Sub, email: canonicalOr(caller.Account), creator: canonicalOr(caller.Account)}, id, nil
	case ScopeShared:
		if err := s.adminOnly(ctx, caller); err != nil {
			return owner{}, "", err
		}
		id, err := secretname.Project(name)
		if err != nil {
			return owner{}, "", fmt.Errorf("%w: %w", errRequestRefused, err)
		}
		return owner{creator: canonicalOr(caller.Account), shared: true}, id, nil
	default:
		return owner{}, "", scopeRefused(scope)
	}
}

// adminOnly refuses a caller that is not on the derived admin list. Every
// route that writes or reads the project's shared prefix, and the sweep that
// removes a member's containers, goes through it — and a list this intake
// cannot read is a refusal, not a leniency.
func (s *Server) adminOnly(ctx context.Context, caller credsvc.LauncherCaller) error {
	admin, err := s.isAdmin(ctx, caller)
	if err != nil {
		return err
	}
	if !admin {
		return errNotAdmin
	}
	return nil
}

// isAdmin reports whether the caller's verified address is on the derived
// admin list (D1935, D1940).
func (s *Server) isAdmin(ctx context.Context, caller credsvc.LauncherCaller) (bool, error) {
	list, err := s.cfg.Admins.Admins(ctx)
	if err != nil {
		return false, err
	}
	canonical, ok := canonicalAddress(caller.Account)
	if !ok {
		// An address the list's own fold refuses names no entry: the list's
		// entries are validated by that same fold (access.Admin.validate), so
		// there is nothing this caller could match.
		return false, nil
	}
	for _, a := range list {
		if a.Email == canonical {
			return true, nil
		}
	}
	return false, nil
}

// ensure puts the container to R12's check, and makes it when it is absent:
// it answers whether this call created it.
//
// mustExist is a version's rule — the container has to be there — and it is
// checked here rather than by the client because the client cannot know. A
// container created between the read and the create is read again and put to
// the same check rather than trusted: the two calls are not atomic, and the
// second read is what closes the window.
func (s *Server) ensure(ctx context.Context, id string, o owner, mustExist bool) (bool, error) {
	existing, found, err := s.cfg.Secrets.Describe(ctx, id)
	if err != nil {
		return false, err
	}
	if !found {
		if mustExist {
			return false, errNoSuchSecret
		}
		if err := s.cfg.Secrets.Create(ctx, id, annotations(o), labels(o)); err != nil {
			if !errors.Is(err, ErrExists) {
				return false, err
			}
			if existing, found, err = s.cfg.Secrets.Describe(ctx, id); err != nil {
				return false, err
			}
			if !found {
				// It was there a moment ago and is not now: a state this
				// intake cannot account for, so it does not write into it.
				return false, errForeignContainer
			}
			return true, checkShape(id, existing, o)
		}
		return true, nil
	}
	return false, checkShape(id, existing, o)
}

// annotations is the annotation set the intake writes for a container (R10).
// A per-user container carries its owner's account id and address beside the
// account that made it; a project-shared one carries only who made it, because
// it has no single owner.
func annotations(o owner) map[string]string {
	if o.shared {
		return map[string]string{AnnotationCreatedBy: o.creator}
	}
	return map[string]string{
		AnnotationOwnerSub:   o.sub,
		AnnotationOwnerEmail: o.email,
		AnnotationCreatedBy:  o.creator,
	}
}

// labels is the label set the intake writes for a container: which scope it
// belongs to, and — for a per-user one — the account id its name carries. They
// are how the next call finds it again, since the intake holds no secrets.get
// and no name filter (see the label constants).
func labels(o owner) map[string]string {
	scope := ScopeUser
	if o.shared {
		scope = ScopeShared
	}
	out := map[string]string{LabelScope: string(scope)}
	if !o.shared {
		out[LabelOwnerSub] = o.sub
	}
	return out
}

// ours reports whether a container is one this intake wrote under the scope a
// call is about. The stored filter answers it already, and the name is checked
// again here: the name is the truth about who owns a container (R12), and a
// container the filter returned is not one this intake may answer with if its
// own grammar does not agree.
func ours(sec Secret, scope Scope) bool {
	n, err := secretname.Parse(sec.ID)
	if err != nil {
		return false
	}
	if scope == ScopeShared {
		return n.Shared()
	}
	return !n.Shared()
}

// checkShape refuses a container the intake did not make (R12, F35).
//
// It is the second half of R12's rule and it is the whole reason an existing
// container is read before it is written: the managed deployer holds
// secrets.create unconditionally, so it can pre-create a fugaro_u_* container
// and set it up as one that takes the credential away from the person who
// writes it. So the check refuses anything beyond what this package writes —
// a TTL, an expiry, a Pub/Sub topic, a CMEK key or a rotation schedule — and
// any annotation that is not the intake's own with the value this call would
// have written.
//
// What it cannot do is tell a container somebody else made TO THE INTAKE'S
// EXACT SHAPE from one the intake made; R12 accepts that, which is why no
// annotation is ever evidence of identity: the owner is the <sub> in the
// container's own name, and an annotation only says which member a prefix
// belongs to when a sweep looks it up.
func checkShape(id string, got Secret, o owner) error {
	if !got.Shape.Empty() {
		return fmt.Errorf("%w: %s carries %s, which the intake never writes", errForeignContainer, id, got.Shape)
	}
	for _, k := range slices.Sorted(maps.Keys(got.Annotations)) {
		if !intakeAnnotation(k) {
			return fmt.Errorf("%w: %s carries the annotation %q, which the intake does not write", errForeignContainer, id, k)
		}
	}
	if got.Annotations[AnnotationCreatedBy] == "" {
		return fmt.Errorf("%w: %s carries no %s annotation, so this intake did not make it", errForeignContainer, id, AnnotationCreatedBy)
	}
	if o.shared {
		// A shared container has no owner: an owner annotation on one was
		// written by someone else, and this intake does not write it.
		if got.Annotations[AnnotationOwnerSub] != "" || got.Annotations[AnnotationOwnerEmail] != "" {
			return fmt.Errorf("%w: %s is a project-shared container and carries an owner's annotations", errForeignContainer, id)
		}
		return nil
	}
	if got.Annotations[AnnotationOwnerSub] != o.sub {
		return fmt.Errorf("%w: %s carries the owner account %s and its name is the owner account %s", errForeignContainer, id,
			pluginwire.Printable(got.Annotations[AnnotationOwnerSub]), pluginwire.Printable(o.sub))
	}
	if got.Annotations[AnnotationOwnerEmail] != o.email || got.Annotations[AnnotationCreatedBy] != o.creator {
		return fmt.Errorf("%w: %s carries annotations that are not the ones this call would have written, so another writer made it", errForeignContainer, id)
	}
	return nil
}

// ownerOf is the owner a container's own name and annotations say, for the
// sweep: the account id from the NAME (which is the truth, R12), and the
// address the container records beside it. An annotation the intake did not
// write is refused by checkShape, which is what a sweep calls next.
func ownerOf(sec Secret) owner {
	n, err := secretname.Parse(sec.ID)
	if err != nil {
		return owner{}
	}
	email := sec.Annotations[AnnotationOwnerEmail]
	return owner{sub: n.ID, email: email, creator: email}
}

// byOwner answers the per-user containers whose owner annotation is the
// address: the admin's lookup and the offboarding sweep (R10).
//
// It is a scan of every container the intake wrote under the per-user scope,
// which is the shape of the grant the intake holds: secrets.list cannot be
// conditioned on a name prefix (F30) and the address is not a label a list
// could filter on, so the intake lists the scope and matches the annotation
// here. A container whose
// name is not one this grammar builds is not one this intake wrote and is not
// returned — the NAME is the truth about who owns a container, and a name that
// does not parse names nobody.
func (s *Server) byOwner(ctx context.Context, address string) ([]Secret, error) {
	canonical, ok := canonicalAddress(address)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not an address this intake can find a member's containers by", errRequestRefused, pluginwire.Printable(address))
	}
	all, err := s.cfg.Secrets.List(ctx, ScopeUser)
	if err != nil {
		return nil, err
	}
	var out []Secret
	for _, sec := range all {
		if !ours(sec, ScopeUser) {
			continue
		}
		if !sameAddress(sec.Annotations[AnnotationOwnerEmail], canonical) {
			continue
		}
		out = append(out, sec)
	}
	return out, nil
}

// listed renders containers as a list's answer: the name a caller asks for it
// by, its scope and the date it was made. The scope is the name's own fact and
// not a parameter, so a row cannot be answered with a scope its container is
// not in. A container whose name this grammar does not build is skipped rather
// than answered with a name this intake never writes — and there is nothing
// else on the row: no version's count, and no version's value.
func listed(found []Secret) []ListedSecret {
	out := make([]ListedSecret, 0, len(found))
	for _, sec := range found {
		n, err := secretname.Parse(sec.ID)
		if err != nil {
			continue
		}
		scope := ScopeUser
		if n.Shared() {
			scope = ScopeShared
		}
		out = append(out, ListedSecret{ID: sec.ID, Name: n.Name, Scope: scope, Created: sec.Created})
	}
	slices.SortFunc(out, func(a, b ListedSecret) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// decodeValue reads the request's value: base64, and inside Secret Manager's
// own payload bound. The refusal never repeats the value — not even its
// length's worth of it — and an empty one is refused because a credential with
// no bytes is not a credential.
func decodeValue(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, fmt.Errorf("%w: the call carries no value, and a credential with no bytes is not one", errRequestRefused)
	}
	value, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: the call's value is not base64, which is how a credential travels on this wire", errRequestRefused)
	}
	if len(value) == 0 {
		return nil, fmt.Errorf("%w: the call's value is empty, and a credential with no bytes is not one", errRequestRefused)
	}
	if len(value) > maxValueBytes {
		return nil, fmt.Errorf("%w: the value is %d bytes and Secret Manager allows at most %d", errRequestRefused, len(value), maxValueBytes)
	}
	return value, nil
}

// scopeRefused is the refusal of a scope this release does not have.
func scopeRefused(scope Scope) error {
	return fmt.Errorf("%w: %s is not a scope: a credential is per-user (%s) or project-shared (%s)",
		errRequestRefused, pluginwire.Printable(string(scope)), ScopeUser, ScopeShared)
}

// canonicalAddress folds an address the way the derived list's entries were
// folded (access.Member: the local part byte for byte, the domain in lower
// case), so a caller's claim and a list of the roster cannot spell one person
// two ways. An address the fold refuses is not one the fold could have
// validated into a list entry, which is fail closed and not an error here.
func canonicalAddress(address string) (string, bool) {
	member, err := access.Member(address)
	if err != nil {
		return "", false
	}
	return strings.TrimPrefix(member, "user:"), true
}

// canonicalOr is the canonical spelling of an address, or the address itself
// when the fold refuses it: an annotation records what was verified, and a
// verification that produced something access.Member will not fold is a
// deployment's own spelling rather than a value this file may edit.
func canonicalOr(address string) string {
	if canonical, ok := canonicalAddress(address); ok {
		return canonical
	}
	return address
}

// sameAddress reports whether two addresses name the same member, by the one
// fold above. A value the fold refuses matches only itself, so a string that
// is not an address cannot match every entry of a list.
func sameAddress(a, b string) bool {
	if a == b {
		return true
	}
	ca, oka := canonicalAddress(a)
	cb, okb := canonicalAddress(b)
	return oka && okb && ca == cb
}

// intakeAnnotation reports whether key is one of the three this package
// writes. A container carrying anything else under the fugaro_ prefix is one
// another writer touched.
func intakeAnnotation(key string) bool {
	return key == AnnotationOwnerSub || key == AnnotationOwnerEmail || key == AnnotationCreatedBy
}
