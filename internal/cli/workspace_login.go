package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// This file is `fugaro workspace login`: the member's own half of the workspace
// sign-on, read offline from the pin this machine's config carries (spec.md §3,
// D1293).
//
// **What a person does.** They sign in to the workspace's own Firebase once and
// hold a short-lived workspace token; the workspace's signer endpoint turns that
// into the member-bound assertion this project's exchange accepts at its own
// /oauth/authorize. Both of those are the workspace's machine and this member's
// authorization server, and neither is this command: it is the step before
// them, and it answers the one question a person has at that point — which
// workspace is this project pinned to, and so which Firebase do I sign in at.
//
// **The half that verifies an assertion is not here, and cannot be.** It is
// licensed (ee/coordinator/oauth) and a core file may not import ee/ — the core
// builds and tests with that directory deleted (.claude/rules/ee.md, and
// internal/eeboundary is the guard). So this command performs no exchange, mints
// nothing and verifies nothing; what it does is read the pin and refuse when
// there is none, which is the member's side of `workspace rm` stated where a
// person meets it: with the membership key and the pin gone there is no key, no
// project and no issuer left, and no sign-in this project can complete.
//
// The pin's assertion key is never printed: the command says it is set, exactly
// as `workspace ls` and `workspace show` do, so no reader of this output can log
// the member's key by accident.

// workspaceLoginDocVersion is the version of the document `fugaro workspace
// login --json` prints. It is a version of its own: the login document and the
// listing are two contracts, and a member added to one says nothing about the
// other.
const workspaceLoginDocVersion = 1

// workspaceLoginDoc is the whole document.
type workspaceLoginDoc struct {
	Version int `json:"version"`
	// Project is the project on this machine that names the workspace: the
	// member, whose own config the pin is written in.
	Project string `json:"project"`
	// Workspace is the workspace's name.
	Workspace string `json:"workspace"`
	// Pin is what this member trusts, in the values it verifies against (spec
	// §2). The assertion key is a fact about it and never a value in it.
	Pin workspaceLoginPin `json:"pin"`
	// Next is the one step left: the sign-in at the workspace's own Firebase,
	// whose end is the member-bound assertion this project's exchange accepts.
	// It is in the document and not only on the terminal because a consumer that
	// reads JSON would otherwise be told what the machine is pinned to and not
	// what to do about it.
	Next string `json:"next"`
}

// workspaceLoginPin is one member's pin as this document prints it: the three
// values a person can act on, and the fact that a fourth is set.
type workspaceLoginPin struct {
	// FirebaseProject is the WORKSPACE's Firebase project id: the one to sign in
	// to, and the value an assertion's subject must name.
	FirebaseProject string `json:"firebase_project"`
	// Issuer is the issuer the member's exchange requires, which is exactly
	// https://securetoken.google.com/<firebase_project>.
	Issuer string `json:"issuer"`
	// ServiceAccount is the workspace service account this project's own
	// `fugaro update` binds its read-only database grant to.
	ServiceAccount string `json:"service_account"`
	// AssertionKeySet reports that the pin carries the key this member verifies
	// an assertion against. The key itself is not a field here.
	AssertionKeySet bool `json:"assertion_key_set"`
}

type workspaceLoginOptions struct {
	json bool
}

func newWorkspaceLoginCmd() *cobra.Command {
	var o workspaceLoginOptions
	cmd := &cobra.Command{
		Use:   "login <name>",
		Short: "Print the sign-in this project's pin asks for",
		Long: `Print the sign-in this project's pin asks for, offline.

A project that is a member of a workspace signs in at the WORKSPACE's own
Firebase: that sign-in is what the workspace's signer endpoint turns into the
member-bound assertion this project's exchange accepts. This command is the step
before it — which workspace is this project pinned to, and which Firebase do I
sign in at — and it reads nothing but this machine's own config.

It refuses when no project config on this machine names the workspace: 'fugaro
workspace rm' takes the membership key and the pin together, and a project with
no pin has no key, no project and no issuer to verify an assertion against, so
there is no sign-in to describe. Nothing here opens a bucket, calls an API or
holds a credential, and the pin's assertion key is reported as set rather than
printed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runWorkspaceLogin(cmd, args[0], &o) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "print machine-readable output")
	return cmd
}

func runWorkspaceLogin(cmd *cobra.Command, name string, o *workspaceLoginOptions) error {
	m, err := workspaceReadMachine()
	if err != nil {
		return err
	}
	out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()
	for _, w := range m.warnings {
		fmt.Fprintf(errw, "warning: %s\n", w)
	}
	mem, err := workspaceLoginMembership(m, name)
	if err != nil {
		return err
	}
	next := fmt.Sprintf("sign in to the workspace's own Firebase (project %s) and present the workspace's signer endpoint's assertion at this project's own /oauth/authorize: the assertion is bound to this member and to one use", mem.pin.FirebaseProject)
	doc := workspaceLoginDoc{
		Version:   workspaceLoginDocVersion,
		Project:   mem.project,
		Workspace: name,
		Pin: workspaceLoginPin{
			FirebaseProject: mem.pin.FirebaseProject,
			Issuer:          mem.pin.Issuer,
			ServiceAccount:  mem.pin.ServiceAccount,
			AssertionKeySet: mem.pin.AssertionKey != "",
		},
		Next: next,
	}
	if o.json {
		return writeJSON(out, doc)
	}
	fmt.Fprintf(out, "WORKSPACE %s\n", oneLine(name))
	fmt.Fprintf(out, "member: %s\n", oneLine(mem.project))
	fmt.Fprintln(out)
	fmt.Fprintln(out, "SIGN-IN (this project's pin, which is this machine's own record of what it trusts)")
	fmt.Fprintf(out, "  firebase project: %s\n", oneLine(mem.pin.FirebaseProject))
	fmt.Fprintf(out, "  issuer: %s\n", oneLine(mem.pin.Issuer))
	fmt.Fprintf(out, "  service account: %s\n", oneLine(mem.pin.ServiceAccount))
	fmt.Fprintln(out, "  assertion key: set (printed nowhere)")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "next: %s\n", next)
	return nil
}

// workspaceLoginMembership is the one project config on this machine that names
// the workspace, or a refusal: a member of no workspace has no sign-in, and a
// machine whose files name the workspace from several projects has no one pin
// this command could describe — the sign-in belongs to one project, and picking
// one of them silently would describe a membership the person did not ask about.
func workspaceLoginMembership(m *workspaceMachine, name string) (workspaceMembership, error) {
	row, ok := m.known(name)
	if !ok || len(row.memberships) == 0 {
		return workspaceMembership{}, userErr("no project config on this machine names workspace %s, so there is no pin to sign in with: the membership key and the pin are written together and fugaro workspace rm drops both, and a project that names no workspace has no key, no project and no issuer left to verify an assertion against (fugaro workspace add %s writes them back)", name, name)
	}
	if len(row.memberships) > 1 {
		projects := make([]string, 0, len(row.memberships))
		for _, mem := range row.memberships {
			projects = append(projects, mem.project)
		}
		return workspaceMembership{}, userErr("%d project configs on this machine name workspace %s (%s), and a sign-in is one project's own: run this from the machine that holds the one you mean", len(row.memberships), name, oneLine(listOrNone(projects)))
	}
	return row.memberships[0], nil
}
