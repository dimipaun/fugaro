package credsvc

import (
	"slices"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The workspace's bundle, and the pool read out of it (W6, D1297).
//
// **One secret, one grant.** A workspace's model credentials live in ONE Secret
// Manager container and are read with ONE `secretmanager.secretAccessor` grant:
// this is Q6's shape, which the installation's own service already has, applied
// to the second deployment. It is worth restating rather than inheriting,
// because the tempting shape for a *pool* is one secret per shared credential —
// an owner adds one, a member is granted the one it needs — and that shape is
// worse in both directions at once: it multiplies the grants the workspace's
// service holds (one per credential, each of them a full read of a credential
// that pays the workspace's money) and it makes "which credentials does the
// workspace hold" a Secret Manager listing rather than a document an operator
// reads. One container also makes the *deployment* independent of the pool: a
// credential the owner adds tomorrow is served by the running service, because
// the service reads the container's newest version and holds no per-credential
// configuration at all.
//
// The one grant is bound once, by the workspace's own root (W3), and it is the
// deployment's only project-level secret grant: the members' job service
// accounts reach the value through this service's proxy and never directly
// (rule 2 — no run, no CLI and no person is handed a credential's value).

// WorkspaceBundleSecretID is the Secret Manager ID of the workspace's bundle
// container: the workspace's one secret, and the container the workspace's
// service holds its one grant on.
//
// It is BundleSecretID under the workspace's own name, and that is not a
// coincidence to be tidied away later: a workspace's own installation IS a
// Fugaro project whose name is the workspace's (§2 of spec.md — the workspace's
// project is the one its members pin), so the container an installation named
// `workspace-alpha` writes and the container a workspace named
// `workspace-alpha` serves from are one container, which is exactly what an
// owner needs them to be. It is a function of its own so the workspace half of
// the rule has a name a reader can find, and so a later change to the derived
// ID has one place to make it in.
func WorkspaceBundleSecretID(workspace string) string { return BundleSecretID(workspace) }

// WorkspaceShared is the workspace's offered pool: the credentials the
// workspace holds AND declares shared, each with the member projects its
// allow-list names (D1298). It is the workspace's side of the intersection the
// service then narrows by the member's own policy (Workspace.Shared), and it is
// the ONLY reader of the pool: the declaration is one document (the bundle, for
// the values and the tiers) and one node (the owner's shared list, for the
// allow-lists), and no route, no member and no other file gets to build a
// second list.
//
// **Both directions of the difference are a refusal, and they are told apart by
// the shape rather than by a message**, which is what makes this a function
// with two skips and no error:
//
//   - A credential the owner's node declares shared and the bundle does not
//     hold is **not offered**. There is no value to attach, so serving it would
//     be a cascade candidate whose call cannot be made. It is a half-written
//     declaration — `fugaro creds set` writes the value and the sharing
//     together — and the operator sees it as a credential the workspace lists
//     and its runs cannot be served.
//   - A credential the bundle holds and the node does not declare is offered to
//     **nobody**: no projects, so no member. This is the direction that matters
//     for safety, because it is the state of every credential the owner put in
//     the workspace's bundle before anyone was on its allow-list, and reading
//     an undeclared credential as "shared with everyone" would be D1298's
//     refused option (B) — a pool every member draws on — arrived at by
//     omission rather than by choice.
//
// The order is the node's, so what an operator reads in the workspace's
// declaration is the order the cascade is offered the pool in, and each
// credential appears once (creds.SharedFor holds the same rule on the other
// side of the intersection). The entries carry a name and a tier and never a
// value: Entry's value is reachable only through the accessor of the kind that
// attaches it to a call.
func WorkspaceShared(b *Bundle, node *projectpolicy.WorkspaceCredentials) []creds.Shared {
	if b == nil || node == nil {
		return nil
	}
	out := make([]creds.Shared, 0, len(node.Shared))
	for _, s := range node.Shared {
		e, held := b.Lookup(s.Name)
		if !held {
			continue
		}
		out = append(out, creds.Shared{
			Credential: creds.Credential{Name: e.Name, Tier: e.Tier},
			Projects:   slices.Clone(s.Projects),
		})
	}
	return out
}
