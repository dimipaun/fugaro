package credsvc

import (
	"slices"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
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
//
// **The pool is every kind of credential the workspace serves, and it was
// model-only until version 2 of the owner's node** (D2978, D2984, F15). A model
// entry is a name, a tier and its allow-list, exactly as it was; a forge entry —
// a Bitbucket token or the GitHub App's key — carries its kind and the concrete
// repositories the owner's credential is stored for (C3's `covers`, U2), which
// is the coverage a forge route holds a run's repository to before the member's
// own policy is asked as well (R5). The two arms of the lookup below are what
// tells the kinds apart, and neither can answer for the other: a model entry is
// read from the bundle's model index and the two provider keys beside it
// (modelEntryOf) and a forge entry from the accessor of its own kind (AppKey,
// BitbucketToken, BitbucketTokens), so a
// Bitbucket token is never offered as a model credential and a model account is
// never offered as a forge one.
//
// **A provider key is a model entry too** (WC1, D2978, F7): what a call through
// one is spent on is a model, so the two shapes the bundle holds a provider's key
// in — the project's own (KindProvider, config.ProviderKeySecret) and one
// repository's named entry for a provider (KindRepoCredential) — are offered from
// the model arm, and the arm asks each kind's own accessor for them
// (modelEntryOf) rather than the model index, which holds neither. What they
// carry is the reserved tier C, which is what has the selection refuse a Claude
// model through one until Check 50 has passed (creds.checkCandidates, D3000) —
// the pool offers the credential and the selection decides the call, as it does
// for every other entry.

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
// allow-list names (D1298) and, for a forge entry, the kind it is and the
// repositories it reaches. An entry offered to every member (D2977, C3's
// `all_members`) is offered to each project the NODE declares, so what the
// allow-list reads is the workspace's membership at the moment the node was
// read, and the member's own policy is still asked on top of it (R5). It is the workspace's side of the intersection the
// service then narrows by the member's own policy (Workspace.Shared), and it is
// the ONLY reader of the pool: the declaration is one document (the bundle, for
// the values and the tiers) and one node (the owner's shared list, for the
// allow-lists, the kinds and the coverages), and no route, no member and no
// other file gets to build a second list.
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
//     and its runs cannot be served. A FORGE entry is skipped on the same
//     reading and by the same rule, whether the bundle holds no credential of
//     that kind at all or holds one under another name: what a forge entry
//     names is one credential of one kind, and a kind and a name that do not
//     agree are a credential this workspace does not hold.
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
		// Who the entry is offered to, as the member's-project list every reader
		// of the pool intersects with the member's own policy (D1298, R5). An
		// entry offered to every member carries no projects: it is offered to
		// each member the node declares, so the list is read off the node's own
		// member table here — the same table the service resolves a caller
		// against, which is what makes "every member" mean the members the
		// workspace really has, a member that joins later included.
		projects := slices.Clone(s.Projects)
		if s.AllMembers {
			projects = workspaceMemberProjects(node)
		}
		if s.Kind == "" || s.Kind == projectpolicy.SharedKindModel {
			// The model arm: the pool's own kind, and what a version-1 node's
			// entry is (C3's rule 8 — the pool held model credentials and
			// nothing else before version 2, F15). A provider key is offered
			// from here too, and modelEntryOf is the whole of how the two
			// shapes it is held in are reached.
			e, held := modelEntryOf(b, s.Name)
			if !held {
				continue
			}
			out = append(out, creds.Shared{
				Credential: creds.Credential{Name: e.Name, Tier: e.Tier},
				Projects:   projects,
			})
			continue
		}
		e, held := forgeEntryOf(b, s.Kind, s.Name)
		if !held {
			continue
		}
		out = append(out, creds.Shared{
			Credential: creds.Credential{Name: e.Name, Tier: e.Tier},
			Projects:   projects,
			Kind:       s.Kind,
			Covers:     slices.Clone(s.Covers),
		})
	}
	return out
}

// workspaceMemberProjects is the node's member list as the projects a pool
// entry offered to every member is offered to: each member the node declares,
// in project-name order, which is what `all_members` means (D2977, C3's rule 5).
//
// It is read off the node and not off the workspace's record, because the node
// is the document the resolver has already admitted the calling member against:
// a pool expanded from a second copy of the membership could offer a credential
// to a project the node that admitted the caller does not declare. A member the
// node drops stops being offered the entry at the service's next read of the
// node (R13), with no new revision and no rewrite of the entry.
func workspaceMemberProjects(node *projectpolicy.WorkspaceCredentials) []string {
	out := make([]string, 0, len(node.Members))
	for _, m := range node.Members {
		out = append(out, m.Project)
	}
	slices.Sort(out)
	return out
}

// modelEntryOf answers the bundle's entry for a model arm's name, which is the
// name the node carries and the bundle's own spelling of the credential.
//
// **The provider key is asked FIRST, and it is the one name two credentials of
// one document can answer.** An account may legally be named after it
// (ClaudeAccountNameRE admits "provider-key", and the account's document key is
// claude-oauth-token-provider-key), so the model index holds that account under
// the very name the provider key's own document key resolves to — CredentialKey
// answers both, and the KindProvider arm of the duplicate rule is what lets
// the document hold the two without either hiding the other (ProviderKey,
// byName). A node entry naming that name is the provider key, which is what
// `fugaro workspace share provider-key` writes and what its own help says the
// name means; the other order would offer the member the owner's Claude account
// under the name the owner read as the provider key — one name serving two
// credentials, the hazard the model family's duplicate rule exists to keep out
// of this document. A bundle holding no provider key is unchanged by the order:
// ProviderKey answers false and the index decides, as it always did.
//
// The model index (Bundle.Lookup) is every credential a coding caller's
// candidate list holds — the accounts, the project's own Anthropic key and the
// service-served key credentials — and it is what every entry of a version-1
// node, and every entry that names a Claude credential, is read through. Both
// provider shapes WC1 makes shareable are deliberately NOT in that index, so
// each is asked for by the accessor that owns its kind, and by its KIND rather
// than by the name the bundle would answer to a lookup (AK6b's reason, and R2's):
//
//   - the project's provider key (KindProvider), asked above and by kind,
//     because an account may legally be named "provider-key" — and the index is
//     in fact holding one when this arm matters most;
//   - one repository's named entry for a provider (KindRepoCredential), which is
//     keyed by its DOCUMENT KEY because two repositories' entries for one
//     provider answer the same credential's name, and whose key is therefore what
//     the node carries (R5, D3008).
//
// The family's other half — a named entry whose credential half is the
// repository's OWN Anthropic key — is skipped, and skipped here as well as at the
// write (`fugaro workspace share` refuses that name): its tier is a paid key's
// and not the reserved third-party one, and it is one repository's by
// construction, so another member's runs are not the runs it is held for. The
// two halves are told apart by the TIER and by nothing else, which is the one
// fact of the kind that is not the entry's own text (KindRepoCredential, and
// CredentialKey's own arm).
func modelEntryOf(b *Bundle, name string) (Entry, bool) {
	if e, ok := b.ProviderKey(); ok && e.Name == name {
		return e, true
	}
	if e, ok := b.Lookup(name); ok {
		return e, true
	}
	if i, ok := b.repoCredentials[name]; ok {
		if e := b.entries[i]; e.Tier == creds.TierC {
			return e, true
		}
	}
	return Entry{}, false
}

// forgeEntryOf answers the bundle's credential of a forge entry's kind and
// name: the App key for a GitHub entry, and for a Bitbucket one the upstream
// token or a member of the named family (D2350) — each selected by the
// accessor that already knows the credential's kind (AppKey, BitbucketToken,
// BitbucketTokens), so this file adds no second reading of what a bundle key
// is and no second spelling of which kinds exist.
//
// The kind is asked first and the name second, and both have to agree: a node
// entry naming a Bitbucket kind and the App key's name is a credential this
// bundle does not hold under that kind, which is a skip and never the other
// forge's credential — C3's rule 4 read between two forges rather than between
// a forge and a model ("a model call is never served a Bitbucket token, and
// vice versa"). A kind with no arm here is a forge the bundle has no accessor
// for, so its entry is skipped with the others the bundle does not hold.
func forgeEntryOf(b *Bundle, kind, name string) (Entry, bool) {
	switch kind {
	case gitprov.KindGitHub:
		if e, ok := b.AppKey(); ok && e.Name == name {
			return e, true
		}
	case gitprov.KindBitbucket:
		if e, ok := b.BitbucketToken(); ok && e.Name == name {
			return e, true
		}
		for _, e := range b.BitbucketTokens() {
			if e.Name == name {
				return e, true
			}
		}
	}
	return Entry{}, false
}
