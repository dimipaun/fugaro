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
	// workflow reports whether this call is storing a secret a workflow
	// declares — a project's own application secrets (F18, G2). It is the
	// request's own member and never anything about the caller, it is only
	// ever true on the shared prefix, and it is the whole of what asks for
	// the marker on a container this call makes and of what is compared
	// against a container it finds: a workflow's value is stored in a marked
	// container and a project-shared credential in an unmarked one, in both
	// directions.
	workflow bool
	// pool is the project pool this call's container is a member of, and empty
	// for every container that is not one (D3194, R9). It is written as the
	// pool's label and compared against it, and it is the whole of what tells a
	// pool's member and a project-shared credential apart.
	pool string
	// mark reports that this container is the pool's own — the one named for
	// the pool itself, which says the pool takes contributions and which only
	// an admin writes. It is the pool's marker, and like every other
	// project-shared container it carries no owner's annotations.
	mark bool
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
	c.nameScope(req.Scope)
	// The marker is the shared prefix's: a workflow's own secret is stored
	// once for the project, and a container under a person's own prefix has
	// no workflow to be shared with (F18). Refused before the value is
	// decoded, so a call that cannot be answered never holds one.
	if req.WorkflowSecret && req.Scope != ScopeShared {
		c.fail(fmt.Errorf("%w: the workflow-secret marker is the project-shared prefix's: a workflow's own secret is stored once for the project (%s), and a per-user container has no workflow to be shared with", errRequestRefused, secretname.ProjectPrefix))
		return
	}
	// A pool is the project's, so a contribution is the shared prefix's, and a
	// contribution is a person's own token rather than a workflow's. Both are
	// refused before the value is decoded, as the marker's scope is.
	if req.Pool != "" && req.Scope != ScopeShared {
		c.fail(fmt.Errorf("%w: a pool is the project's, so a contribution to one is stored under the project-shared prefix (%s): a per-user container is your own and belongs to no pool (D3194)", errRequestRefused, secretname.ProjectPrefix))
		return
	}
	if req.Pool != "" && req.WorkflowSecret {
		c.fail(fmt.Errorf("%w: a contribution to a pool is a person's own token and never a secret a workflow declares, so it carries no %s marker (D3194)", errRequestRefused, LabelWorkflowSecret))
		return
	}
	value, err := decodeValue(req.Value)
	if err != nil {
		c.fail(err)
		return
	}
	// The line is armed with the value before anything else can write it: a
	// handler that holds a credential cannot reach an answer without saying
	// which one it held.
	c.value = value
	o, id, err := s.container(r.Context(), caller, req.Scope, req.Name, req.Pool)
	if err != nil {
		c.fail(err)
		return
	}
	o.workflow = req.WorkflowSecret
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
	c.nameScope(req.Scope)
	switch req.Scope {
	case ScopeUser:
		if req.Owner != "" {
			// A member named by address: the admin's lookup, and the one the
			// offboarding command reads a member's containers through.
			c.nameOwner(req.Owner)
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
	c.nameScope(req.Scope)
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
		c.nameOwner(req.Owner)
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
		o, id, err := s.deleteTarget(r.Context(), caller, req)
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
// before the name is even looked at — with the one case D3194 adds, a person's
// own contribution to a pool an admin has marked as taking them.
func (s *Server) container(ctx context.Context, caller credsvc.LauncherCaller, scope Scope, name, pool string) (owner, string, error) {
	switch scope {
	case ScopeUser:
		id, err := secretname.User(caller.Sub, name)
		if err != nil {
			return owner{}, "", fmt.Errorf("%w: %w", errRequestRefused, err)
		}
		return owner{sub: caller.Sub, email: canonicalOr(caller.Account), creator: canonicalOr(caller.Account)}, id, nil
	case ScopeShared:
		if pool != "" {
			return s.contribution(ctx, caller, name, pool)
		}
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

// deleteTarget answers whose container a delete by name is about. An ordinary
// name is its own prefix's, resolved exactly as a write is; a delete naming a
// pool is a member of it, and that is the one case a member reaches the
// project's prefix (D3194, R9).
func (s *Server) deleteTarget(ctx context.Context, caller credsvc.LauncherCaller, req DeleteRequest) (owner, string, error) {
	if req.Pool == "" {
		return s.container(ctx, caller, req.Scope, req.Name, "")
	}
	if req.Scope != ScopeShared {
		return owner{}, "", fmt.Errorf("%w: a pool is the project's, so its members are stored under the project-shared prefix (%s): a per-user container is your own and belongs to no pool (D3194)", errRequestRefused, secretname.ProjectPrefix)
	}
	id, err := secretname.Project(req.Name)
	if err != nil {
		return owner{}, "", fmt.Errorf("%w: %w", errRequestRefused, err)
	}
	got, found, err := s.cfg.Secrets.Describe(ctx, id)
	if err != nil {
		return owner{}, "", err
	}
	if !found {
		return owner{}, "", errNoSuchSecret
	}
	// The container's own label says which pool it is a member of, and one
	// naming another pool is refused rather than answered: a withdrawal names
	// the pool it withdraws from, and the label is what the container is.
	if member := got.Labels[LabelPoolContributions]; member != req.Pool {
		if member == "" {
			return owner{}, "", fmt.Errorf("%w: %s is not a member of the pool %s: a member carries %s=%s, and this container does not (D3194)", errForeignContainer, id, pluginwire.Printable(req.Pool), LabelPoolContributions, pluginwire.Printable(req.Pool))
		}
		return owner{}, "", fmt.Errorf("%w: %s is a member of the pool %s and this call withdraws it from %s (D3194)", errForeignContainer, id, pluginwire.Printable(member), pluginwire.Printable(req.Pool))
	}
	o := memberOf(id, got)
	// The pool's marker is the admin's and a member's contribution is the
	// contributor's own: a caller who is neither withdraws nothing, and an
	// admin removes any of them (R9).
	if o.sub != caller.Sub {
		if err := s.adminOnly(ctx, caller); err != nil {
			return owner{}, "", err
		}
	}
	return o, id, nil
}

// contribution answers whose container a write to a pool is about, and it is
// the one case a member writes the project's prefix (D3194, R9).
//
// The pool's OWN member — the container named for the pool itself — is the
// pool's marker: an admin writes it, and its being there is what says the pool
// takes contributions at all. Every other member is one person's own token,
// admitted only where the marker is there and written with the contributor's
// own owner annotations, which is what makes it theirs to write again and
// theirs to withdraw.
func (s *Server) contribution(ctx context.Context, caller credsvc.LauncherCaller, name, pool string) (owner, string, error) {
	id, err := secretname.Project(name)
	if err != nil {
		return owner{}, "", fmt.Errorf("%w: %w", errRequestRefused, err)
	}
	if _, err := secretname.Project(pool); err != nil {
		return owner{}, "", fmt.Errorf("%w: %s is not a pool a contribution can be made to: a pool is a name, and this is not one", errRequestRefused, pluginwire.Printable(pool))
	}
	if name == pool {
		// The pool's own member is its marker, and only an admin writes it:
		// the marker is the statement that the pool takes contributions, and
		// what it holds is the admin's own token.
		if err := s.adminOnly(ctx, caller); err != nil {
			return owner{}, "", err
		}
		return owner{creator: canonicalOr(caller.Account), shared: true, pool: pool, mark: true}, id, nil
	}
	marked, err := s.markedPool(ctx, pool)
	if err != nil {
		return owner{}, "", err
	}
	if !marked {
		return owner{}, "", fmt.Errorf("%w: the pool %s is not one that takes contributions: an admin marks it by contributing to it, and until then only an admin writes it (D3194)", errRequestRefused, pluginwire.Printable(pool))
	}
	address := canonicalOr(caller.Account)
	return owner{sub: caller.Sub, email: address, creator: address, shared: true, pool: pool}, id, nil
}

// markedPool reports whether a pool carries its marker: the container named
// for the pool, carrying LabelPoolContributions with the pool's own name. A
// pool with no marker takes no contributions, and a read that cannot answer is
// a refusal rather than a no (D143).
func (s *Server) markedPool(ctx context.Context, pool string) (bool, error) {
	id, err := secretname.Project(pool)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errRequestRefused, err)
	}
	got, found, err := s.cfg.Secrets.Describe(ctx, id)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	return got.Labels[LabelPoolContributions] == pool, nil
}

// memberOf is the owner a pool's member carries, read back off the container:
// the contributor's own annotations, and the label that says which pool it is
// a member of. It is the read side of contribution, and it is how a delete
// knows whose member it is holding without asking the request (R12: the name
// is the truth, and for a member of a pool the name cannot carry the
// contributor, so the annotations the intake alone writes stand in its place).
func memberOf(id string, got Secret) owner {
	n, err := secretname.Parse(id)
	if err != nil {
		return owner{}
	}
	return owner{
		sub:      got.Annotations[AnnotationOwnerSub],
		email:    got.Annotations[AnnotationOwnerEmail],
		creator:  got.Annotations[AnnotationCreatedBy],
		shared:   true,
		pool:     got.Labels[LabelPoolContributions],
		mark:     n.ID == got.Labels[LabelPoolContributions],
		workflow: false,
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
			// Found by the second read: the container was there all along and
			// this call did not make it — which is what Created reports, and
			// the same answer the found-path below gives for the same state.
			return false, checkedWrite(id, existing, o)
		}
		return true, nil
	}
	return false, checkedWrite(id, existing, o)
}

// checkedWrite is everything a write into a container that exists must pass: R12's
// shape check, and the marker's own class check (F18, G2). They are one call
// so that no write path can reach one of them without the other, and they stay
// two functions because they refuse different things and are read apart.
func checkedWrite(id string, got Secret, o owner) error {
	if err := checkShape(id, got, o); err != nil {
		return err
	}
	return checkMarker(id, got, o)
}

// AcceptsWorkflowWrite reports whether a project-shared container that already
// exists is one this intake would write a workflow's own secret into: the shape
// check and the marker check, asked exactly as the write path asks them
// (checkedWrite). It is exported for one caller — `fugaro update`'s migration of
// a project copy into the fugaro_p_ grammar (internal/cli) — so that a container
// that migration creates is one the next `fugaro secrets set` accepts, and one
// another writer made is skipped rather than written into (G2). Two definitions
// of that fence could come to disagree about a container the copy made, which is
// the one thing this export prevents.
func AcceptsWorkflowWrite(id string, got Secret) error {
	return checkedWrite(id, got, owner{shared: true, workflow: true})
}

// checkMarker refuses a write whose class is not the container's own: a
// workflow's value is stored in a container carrying the marker and a
// project-shared credential in one that does not, both directions (F18, G2) —
// and, for the other label, a pool's member holds a contribution and a
// project-shared credential is never stored in one (D3194, R9).
//
// The marker is set at create alone — neither this service nor the credentials
// service holds secrets.update — so what a container was made for is the whole
// of what it can be written with afterwards. Refusing the other direction is
// the point: without it a project-shared credential of a name a workflow also
// declares would be a value the workflow's jobs mount and the credentials
// service would hand over (G2), and a workflow's value stored into the
// project's shared container would be every repository's.
//
// It is about a write into a container that exists and never about a delete:
// removing the container is exactly what these two refusals tell the person to
// do with it, so `fugaro secrets shared rm <name>` reaches a marked container
// and an unmarked one the same way. A container the marker cannot be read from
// at all — a pre-created one with no labels (F35), which is the shape check's
// refusal before this — is refused by checkShape, which is why this one reads
// the labels alone.
func checkMarker(id string, got Secret, o owner) error {
	if !o.shared {
		return nil
	}
	name := secretname.Name{Name: id}
	if n, err := secretname.Parse(id); err == nil {
		name = n
	}
	// A pool's member and the project's own credential are two classes of the
	// shared prefix, and neither holds the other's value (D3194). The label
	// says which a container is, it is written at create alone, and it is
	// compared both ways for the marker's reason (G2).
	if member := got.Labels[LabelPoolContributions]; member != o.pool {
		switch {
		case member == "":
			return fmt.Errorf("%w: %s is not a member of the pool %s, and a contribution is stored only in one that carries %s=%s (D3194): contribute under the member's own name, or remove this container with `fugaro secrets shared rm %s` if the value in it was meant for the pool",
				errForeignContainer, id, pluginwire.Printable(o.pool), LabelPoolContributions, pluginwire.Printable(o.pool), pluginwire.Printable(name.Name))
		case o.pool == "":
			return fmt.Errorf("%w: %s is a member of the pool %s and carries %s=%s, so a project-shared credential is not stored in it (D3194): remove this container with `fugaro accounts pool rm-member %s --member %s` if the token in it is to go, or store the credential under a name no pool's member holds",
				errForeignContainer, id, pluginwire.Printable(member), LabelPoolContributions, pluginwire.Printable(member), pluginwire.Printable(member), pluginwire.Printable(name.Name))
		default:
			return fmt.Errorf("%w: %s is a member of the pool %s and this call contributes to %s (D3194): a member belongs to one pool",
				errForeignContainer, id, pluginwire.Printable(member), pluginwire.Printable(o.pool))
		}
	}
	marked := got.Labels[LabelWorkflowSecret] == WorkflowSecretMarker
	if marked == o.workflow {
		return nil
	}
	if o.workflow {
		return fmt.Errorf("%w: %s holds a project-shared credential and carries no %s=%s marker, so a workflow's own secret is not stored in it (G2): store the workflow's value under a name no project-shared credential holds, or remove this container with `fugaro secrets shared rm %s` if the value in it was meant for the workflow",
			errForeignContainer, id, LabelWorkflowSecret, WorkflowSecretMarker, pluginwire.Printable(name.Name))
	}
	return fmt.Errorf("%w: %s holds a workflow's own secret (marked %s=%s), and a project-shared credential is not stored in it (G2): rename the workflow's secret, or remove this container with `fugaro secrets shared rm %s` and store the credential again",
		errForeignContainer, id, LabelWorkflowSecret, WorkflowSecretMarker, pluginwire.Printable(name.Name))
}

// annotations is the annotation set the intake writes for a container (R10).
// A per-user container carries its owner's account id and address beside the
// account that made it; a pool's member carries the contributor's own, which
// is what makes it theirs (D3194, R9); a project-shared one — and a pool's own
// member, which is the marker and holds the admin's token rather than one
// person's — carries only who made it, because it has no single owner.
func annotations(o owner) map[string]string {
	if o.shared && (o.pool == "" || o.mark) {
		return map[string]string{AnnotationCreatedBy: o.creator}
	}
	return map[string]string{
		AnnotationOwnerSub:   o.sub,
		AnnotationOwnerEmail: o.email,
		AnnotationCreatedBy:  o.creator,
	}
}

// labels is the label set the intake writes for a container: which scope it
// belongs to, — for a per-user one — the account id its name carries, — for a
// pool's member — the pool it belongs to, and — for a workflow's own secret —
// the marker. They are how the next call finds it again, since the intake
// holds no secrets.get and no name filter (see the label constants), and the
// markers are among them because a container is only ever created once: the
// class a container was made for is decided by the call that made it and read
// from the container for every call after.
func labels(o owner) map[string]string {
	scope := ScopeUser
	if o.shared {
		scope = ScopeShared
	}
	out := map[string]string{LabelScope: string(scope)}
	if !o.shared {
		out[LabelOwnerSub] = o.sub
	}
	if o.pool != "" {
		out[LabelPoolContributions] = o.pool
	}
	if o.workflow {
		out[LabelWorkflowSecret] = WorkflowSecretMarker
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
		return fmt.Errorf("%w: %s carries %s, which the intake never writes%s", errForeignContainer, id, got.Shape, notOurs)
	}
	for _, k := range slices.Sorted(maps.Keys(got.Annotations)) {
		if !intakeAnnotation(k) {
			return fmt.Errorf("%w: %s carries the annotation %q, which the intake does not write%s", errForeignContainer, id, k, notOurs)
		}
	}
	if got.Annotations[AnnotationCreatedBy] == "" {
		return fmt.Errorf("%w: %s carries no %s annotation, so this intake did not make it%s", errForeignContainer, id, AnnotationCreatedBy, notOurs)
	}
	if o.shared {
		// A project-shared container has no owner: an owner annotation on one
		// was written by someone else, and this intake does not write it. A
		// pool's member is the one exception (D3194, R9) — it is one person's
		// token in a pool, so it carries the contributor's own annotations,
		// and the pool's marker, named for the pool itself, carries none.
		if o.pool == "" {
			if got.Annotations[AnnotationOwnerSub] != "" || got.Annotations[AnnotationOwnerEmail] != "" {
				return fmt.Errorf("%w: %s is a project-shared container and carries an owner's annotations%s", errForeignContainer, id, notOurs)
			}
			return nil
		}
		if o.mark {
			if got.Annotations[AnnotationOwnerSub] != "" || got.Annotations[AnnotationOwnerEmail] != "" {
				return fmt.Errorf("%w: %s is the pool %s's own member and carries an owner's annotations%s", errForeignContainer, id, pluginwire.Printable(o.pool), notOurs)
			}
			return nil
		}
		if got.Annotations[AnnotationOwnerSub] != o.sub {
			return fmt.Errorf("%w: %s carries the contributor account %s and this call is %s%s", errForeignContainer, id,
				pluginwire.Printable(got.Annotations[AnnotationOwnerSub]), pluginwire.Printable(o.sub), notOurs)
		}
		if got.Annotations[AnnotationOwnerEmail] != o.email || got.Annotations[AnnotationCreatedBy] != o.creator {
			return fmt.Errorf("%w: %s carries annotations that are not the ones this call would have written, so another writer made it%s", errForeignContainer, id, notOurs)
		}
		return nil
	}
	if got.Annotations[AnnotationOwnerSub] != o.sub {
		return fmt.Errorf("%w: %s carries the owner account %s and its name is the owner account %s%s", errForeignContainer, id,
			pluginwire.Printable(got.Annotations[AnnotationOwnerSub]), pluginwire.Printable(o.sub), notOurs)
	}
	if got.Annotations[AnnotationOwnerEmail] != o.email || got.Annotations[AnnotationCreatedBy] != o.creator {
		return fmt.Errorf("%w: %s carries annotations that are not the ones this call would have written, so another writer made it%s", errForeignContainer, id, notOurs)
	}
	return nil
}

// notOurs is the fix every checkShape refusal carries. The grant holds no
// secrets.update and no secrets.patch, so a container that is not this
// intake's own can never be brought to its shape: the shape check describes
// what is wrong and this says what can be done about it, which is the whole
// difference between a refusal a person can act on and one they can only
// report. Both remove verbs are named because both prefixes reach this check:
// a member's own container, and — through a sweep or a shared store — the
// project's.
const notOurs = "; the intake holds no write that changes a container, so a value is stored only in one this intake made: remove this container (`fugaro secrets shared rm <name>` for the project's prefix, `fugaro secrets user rm <name>` for your own) and store the value again"

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
