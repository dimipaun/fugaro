package cli

import (
	"context"
	"fmt"
	"slices"

	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// This file is the ONE answer to "is this person a project owner" (F36, D3006,
// KS2 of docs/keys-through-service).
//
// Two questions are asked of anyone who writes a provider key, and they are
// asked in this order, before anything is read and before any version is added:
//
//  1. is this a person at their own terminal? `fugaro creds set` already
//     refuses a coding agent's session, a run's own process and the GODEBUG
//     http2debug leak (credsOwnerOnly, in the same order and for the same
//     reason), and a provider key's other two writers now write the same
//     document, so the same three refusals stand in front of them
//     (providerKeyOwnerOnly).
//  2. is that person a PROJECT owner? A provider key is the whole project's:
//     one key, spent by every repository the owner bound it to, so D3006 makes
//     the write the owner's and an operator's run of it is refused with the
//     person who can make it named (bundleOwnerProbe).
//
// The probe is testIamPermissions on the bundle's own container and not a
// policy read: what a caller may learn about its OWN reach is one call, where
// reading the policy is a permission of its own that an owner-only command must
// not need (the launcher check makes the same choice, doctor_launcher.go). The
// permission it asks for is `secretmanager.secrets.setIamPolicy`, which is the
// one a project's EDITOR does not have: Owner, Editor and every operator role
// Fugaro grants can add a version of the container — that is what
// `secretmanager.versions.add` means, and it is what makes the container
// writable at all — so `versions.add` cannot tell a project owner from an
// operator, while setIamPolicy can. A custom role that carries setIamPolicy on
// this one container is a project owner's own grant and is answered as one.
//
// It answers a nil error and nothing else: the probe is a gate and not a
// report, and no caller prints its result.

// bundleOwnerPermission is the permission the probe asks for on the bundle's
// container. See the file comment for why it is this one.
const bundleOwnerPermission = "secretmanager.secrets.setIamPolicy"

// providerKeyOwnerOnly is credsOwnerOnly's three refusals, for the commands
// that write a provider key without being `fugaro creds`. The three checks are
// credsOwnerOnly's own, called rather than restated and in its order, so a
// fourth way of refusing there cannot be one the other writers lack. Only the
// sentence a person reads is this file's, and only the reason it gives: the
// document these commands write is the credentials service's one bundle, which
// holds every model credential the service issues.
//
// did is what the refusal says was not done: "written" or "read".
func providerKeyOwnerOnly(getenv func(string) string, did string) error {
	if m := agentMarker(getenv, processTerminal()); m != "" {
		return userErr("nothing was %s: a provider key goes into the credentials service's bundle secret, which holds every model credential the service issues, and that write is made by a person at their own terminal: %s", did, initflow.AgentRefusal(m))
	}
	if m := managedFlagRunMarker(getenv); m != "" {
		return userErr("nothing was %s: a provider key goes into the credentials service's bundle secret, which holds every model credential the service issues, and this process is a Fugaro run's (%s is set) rather than a person at their own terminal", did, m)
	}
	return refuseHTTP2Debug(getenv)
}

// bundleOwnerProbe is question 2: the caller's own reach on the bundle's
// container, asked as one testIamPermissions call. It is the ONE probe of
// "is this person a project owner" — KS12 and KS15 use it too — so the answer
// cannot differ between the writers.
//
// A container that does not exist is answered as the missing container it is
// (apiErr's own sentence) rather than as a refusal of the person: the writers
// check the container before they probe, and a 404 read as "you are not an
// owner" would send an owner to look at their IAM over a container nobody has
// created yet.
func bundleOwnerProbe(ctx context.Context, store *bundleStore, lc *localcfg.Config, did string) error {
	r, err := store.svc.Projects.Secrets.TestIamPermissions(store.parent(), &secretmanager.TestIamPermissionsRequest{
		Permissions: []string{bundleOwnerPermission},
	}).Context(ctx).Do()
	if err != nil {
		return remoteErr(store.apiErr("read", err), nil)
	}
	if r != nil && slices.Contains(r.Permissions, bundleOwnerPermission) {
		return nil
	}
	return bundleOwnerRefusal(lc, did)
}

// bundleOwnerRefusal is the refusal of a caller who is not a project owner: it
// names what it means, who can make the write, and the one thing that would
// change the answer. It asks nothing (no-prompt spec rule 1) — D3006's fix for
// an operator is a person's own step, never a question this command waits on.
func bundleOwnerRefusal(lc *localcfg.Config, did string) error {
	return userErr("nothing was %s: a provider key is entered in the credentials service's bundle secret, one entry per repository the key is bound to, and that document is the whole project's — so only a PROJECT OWNER writes one (D3006). Your credentials may write versions of the container but not set its IAM policy, which is what tells a project owner from an operator here. Ask a project owner of %s to run this command with the key, or to grant you a role that carries %s on that container",
		did, fmt.Sprintf("project %s", lc.GCPProject), bundleOwnerPermission)
}
