package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	infraTF "github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The workspace's mode (plan task WS5, R2) is what decides which of the two
// deployments this process is. FUGARO_WORKSPACE_MODE (C2) names the workspace
// the service belongs to, and with it set the process builds the workspace's
// own half — the published node it reads its callers out of, the front door
// built over it, the meter over the workspace's own database and C1's
// constructor's resolver — and serves all of it through the SAME Server an
// installation is served by (credsvc.NewServer). Without it nothing changes:
// the seven names, the seven tables, and no resolver of the process's own,
// which is what the installation's deployment has always been.
//
// Two things make these tests hermetic, and both are the seams the file itself
// documents. The process READS its published node at startup, so a test points
// the database client at internal/gcpfake's Realtime Database
// (credentialsTest.database, seeded with a node C3's own encoder wrote); and the
// resolver's answers are WS2's, so a test records the configuration the process
// handed C1's constructor and delegates to the real constructor
// (credentialsWorkspaceTest), which leaves the resolver served the contract's.

// workspaceServeEnv is a complete, valid workspace-mode environment: the six
// names the workspace root sets on its service — internal/infra/tf's
// WorkspaceServiceEnv less the mode's own name — plus the mode's name. The
// workspace is a Fugaro project of its own, so FUGARO_PROJECT and
// FUGARO_FIREBASE_PROJECT are its own name and FUGARO_RTDB_URL its own
// database: the pair credentialsServiceDatabase holds together, which is the
// same rule an installation's deployment is read by.
func workspaceServeEnv(workspace string) map[string]string {
	return map[string]string{
		envProject:               workspace,
		envGCPProject:            "proj-1234",
		envRegion:                "us-east5",
		envRTDBURL:               "https://" + workspace + "-default-rtdb.firebaseio.com",
		envFirebaseFP:            workspace,
		envCredentialsServiceURL: "https://fugaro-workspace-abcde-uc.a.run.app",
		creds.WorkspaceModeEnv:   workspace,
	}
}

// workspaceServiceAccount is the one member job account these tests publish: a
// plain service account address, which is the shape the node's own Validate
// admits (no credential value is anywhere near it).
const workspaceServiceAccount = "fugaro-job-a1b2c3@proj-1234.iam.gserviceaccount.com"

// workspaceNode is the published credentials node a workspace-mode process
// reads at startup: one member, with the row C3's version 2 writes.
func workspaceNode(name string) *projectpolicy.WorkspaceCredentials {
	return &projectpolicy.WorkspaceCredentials{
		Version: projectpolicy.WorkspaceCredentialsVersion,
		Name:    name,
		Members: []projectpolicy.WorkspaceCredentialMember{{
			Project:         "member-one",
			FirebaseProject: "member-one-fp",
			FP:              "member-one",
			JobAccounts:     []string{workspaceServiceAccount},
			JobAccountRepos: []projectpolicy.WorkspaceJobAccount{{
				Account: workspaceServiceAccount,
				Repos:   []string{"github/acme/webapp"},
			}},
			ExchangeURL: "https://member-one.example.com/exchange",
		}},
	}
}

// credsServeMemberDatabase is the URL credsvc derives from that member's own
// Firebase project, and the only thing that says whose state a call may read:
// the tests below assert the process asked for exactly it rather than building
// a URL of its own.
const credsServeMemberDatabase = "https://member-one-fp-default-rtdb.firebaseio.com"

// fakeWorkspaceDatabase starts a Realtime Database fake holding the workspace's
// published node — encoded by C3's own writer, so the document the process
// decodes is the one a `fugaro update` publishes (WS4) — and answers a client
// of it, the way the process builds one at its own database's URL.
//
// The fake comes back beside the client because the workspace's database is
// where more than the published node lives: a test of the personal half
// (credentials_serve_test.go, WC14) writes the workspace's ROSTER into the same
// database, and a fake nobody can reach is a database a test can only read.
func fakeWorkspaceDatabase(t *testing.T, name string) (*gcpfake.RTDB, *rtdb.Client) {
	t.Helper()
	fake := gcpfake.NewRTDB(t)
	raw, err := projectpolicy.WorkspaceCredentialsNode(workspaceNode(name))
	if err != nil {
		t.Fatalf("the node these tests publish is not one a Fugaro writes, so they would prove nothing about the read: %v", err)
	}
	fake.Set(projectpolicy.PathWorkspaceCredentials(name), json.RawMessage(raw))
	client, err := rtdb.New(fake.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	return fake, client
}

// TestServeWorkspaceModeBuildsTheWorkspaceResolver is WS5's acceptance: with
// the mode's name set, the process builds the workspace's half through C1's
// constructor — a node reader over the workspace's own database, the front door
// built from the published member table, and the meter over the workspace's own
// database — hands the Server the resolver that came back, and reads the
// caller list for its verifier out of that node rather than out of an
// environment it does not have.
//
// What the successful build itself proves: the workspace's process has no job
// document (credentialsServiceWorkspaceEnvNames), so a configuration that
// reached NewAuthenticator with an empty account list would refuse — the
// accounts therefore came from the node, which is the only other source.
//
// What each mutation was seen to do (each run and reverted before this landed):
// reading the mode's name as the empty string — the process ignoring it, which
// is this command before WS5 — fails this test at the missing job document and
// every subtest of the refusal test below, which is the red step; answering no
// resolver from the builder fails at the assertion under it; and asking the
// node reader for any path but projectpolicy.PathWorkspaceCredentials(name)
// leaves it empty and the process refusing at startup.
func TestServeWorkspaceModeBuildsTheWorkspaceResolver(t *testing.T) {
	const workspace = "aurora-ws"
	for n, v := range workspaceServeEnv(workspace) {
		t.Setenv(n, v)
	}
	fakeCredentialsService(t)
	_, client := fakeWorkspaceDatabase(t, workspace)
	var opened []string
	credentialsTest.database = func(_ context.Context, url string, _ oauth2.TokenSource) (*rtdb.Client, error) {
		opened = append(opened, url)
		return client, nil
	}

	// C1's constructor is a variable in another package, assigned by that
	// package's own files and by nothing outside it: the recorded configuration
	// is delegated to the real one, so the resolver the process ends up with is
	// the contract's and this only watches what it was handed.
	var built credsvc.WorkspaceScopeConfig
	credentialsWorkspaceTest = func(cfg credsvc.WorkspaceScopeConfig) (credsvc.ScopeResolver, error) {
		built = cfg
		return credsvc.NewWorkspaceScopeResolver(cfg)
	}
	t.Cleanup(func() { credentialsWorkspaceTest = nil })

	cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("credentialsServiceConfig with %s set: %v", creds.WorkspaceModeEnv, err)
	}
	if cfg.Resolver == nil {
		t.Fatal("the workspace's configuration handed the Server no resolver: with the mode's name set the process serves the workspace's own scope, and nil is the installation's — which would read this deployment's own database and this deployment's own tables for a member's call")
	}
	if cfg.JobAccounts != nil {
		t.Errorf("the workspace's configuration carries %d installation job rows: a workspace's deployment has no job document (internal/infra/tf's WorkspaceServiceEnv), and its runs are bound by the node its resolver reads", len(cfg.JobAccounts))
	}
	if built.Workspace == nil || built.Node == nil || built.Meter == nil {
		t.Fatalf("C1's constructor was handed Workspace=%v Node=%v Meter=%v, and the mode builds all three: the front door over the published node, the reader of that node and the workspace's own meter", built.Workspace != nil, built.Node != nil, built.Meter != nil)
	}

	// The node reader reads the workspace's OWN database, and what it answers is
	// the published node — not an empty table, which is the difference between a
	// workspace that published none and one that did.
	published, found, err := built.Node.WorkspaceNode(context.Background())
	if err != nil {
		t.Fatalf("the node reader: %v", err)
	}
	if !found {
		t.Fatalf("the node reader found no node at %s in the workspace's own database", projectpolicy.PathWorkspaceCredentials(workspace))
	}
	if published.Name != workspace || len(published.Members) != 1 || published.Members[0].Project != "member-one" {
		t.Fatalf("the node reader answered %+v, want the published node of %s", published, workspace)
	}

	// The member's database is the one credsvc derived from that member's own
	// Firebase project, which is the only thing that says whose state a call may
	// read: the process opens the URL it is handed and builds no URL of its own.
	if !slices.Contains(opened, credsServeMemberDatabase) {
		t.Errorf("the databases this process opened were %v, want the member's own (%s): a workspace's service reaches a member's state at the URL derived from that member's Firebase project and at no other", opened, credsServeMemberDatabase)
	}
	if !slices.Contains(opened, "https://"+workspace+"-default-rtdb.firebaseio.com") {
		t.Errorf("the databases this process opened were %v, want the workspace's own as well: the node it read its callers out of and the meter it keeps the cap in are both the workspace's", opened)
	}

	// ... and the same Server serves it: the workspace's deployment is the
	// service, not a second one (D2980).
	if _, err := credsvc.NewServer(cfg); err != nil {
		t.Fatalf("NewServer refused the workspace's configuration: %v", err)
	}
}

// TestServeWithoutTheModeIsUnchanged is the other half of WS5's acceptance:
// without the mode's name the process is exactly today's. The mode's branch is
// never reached, the configuration carries no resolver (nil is the
// installation's own, which NewServer builds over the same configuration), and
// the job table is still the installation's own document — which is what the
// Server in front of every installation reads a caller's repository from.
func TestServeWithoutTheModeIsUnchanged(t *testing.T) {
	for n, v := range credentialsServeEnv {
		t.Setenv(n, v)
	}
	// The mode's name is deliberately left unset, and it is set to the empty
	// string by no test here: an absent name is the state every installation's
	// container is in (credentials.tf sets none).
	fakeCredentialsService(t)
	credentialsWorkspaceTest = func(credsvc.WorkspaceScopeConfig) (credsvc.ScopeResolver, error) {
		t.Error("the process built the workspace's resolver for a deployment that names no workspace: nothing here is a workspace's, and a resolver of its own would be an installation served by something other than the installation it was configured with")
		return nil, nil
	}
	t.Cleanup(func() { credentialsWorkspaceTest = nil })

	cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("credentialsServiceConfig with no %s: %v", creds.WorkspaceModeEnv, err)
	}
	if cfg.Resolver != nil {
		t.Error("an installation's configuration handed the Server a resolver of its own: nil is the installation's scope, built by NewServer over this same configuration (scope_installation.go)")
	}
	if len(cfg.JobAccounts) == 0 {
		t.Fatal("an installation's configuration carries no job table: the table is what the installation's scope binds a caller's verified account to a repository with, and an empty one is a service that can bind nothing")
	}
	if _, err := credsvc.NewServer(cfg); err != nil {
		t.Fatalf("NewServer refused an installation's configuration: %v", err)
	}
}

// TestServeAssignsTheOldRecordBridgeToAnInstallationOnly is MG3's release-1
// seam read in both directions, because the guard that separates the two is a
// security control and nothing else in the tree observes the assignment: the
// chain walk that consults it (CS1) is not merged, so a mutant that drops the
// guard — `if workspace == ""` becoming an unconditional assignment — leaves
// every serve-path test green while a workspace deployment would answer a
// member's older run out of the WORKSPACE's own database, which is the reach
// the assignment's own comment calls serving a call off a reach nobody recorded
// for it.
//
// Both halves are read off the bridge's OWN answer rather than off the type of
// what was assigned — an installation's deployment must answer the record its
// own database holds (found, one pool), and a workspace's must answer no record
// at all — and the workspace's fake database is seeded with a record at the
// very slug the installation's holds, so a dropped guard is not merely
// unobserved here: it answers that record and fails the second assertion.
//
// What the bridge reads is creds' own record, written by creds' own writer, so
// this pins the release-1 promise at the one place the deployment makes it.
func TestServeAssignsTheOldRecordBridgeToAnInstallationOnly(t *testing.T) {
	slug := mustSlug("github", "acme/webapp")
	ctx := context.Background()
	// One repository's record, as `fugaro update` writes it: an oauth
	// repository, which is the record whose answer does not depend on the
	// published policy the other modes are bounded by — so a difference between
	// the two deployments below can only be the assignment itself.
	writeRecord := func(t *testing.T, db *rtdb.Client) {
		t.Helper()
		rec := creds.RepoAuth{Version: creds.RepoAuthVersion, Auth: config.AuthOAuth, Accounts: []string{config.DefaultClaudeAccount}}
		if err := creds.PutRepoAuth(ctx, db, slug, rec); err != nil {
			t.Fatalf("writing %s: %v", creds.PathRepoAuth(slug), err)
		}
	}
	// The seam that builds the process's own database client, which is what the
	// bridge is handed: the fake is the deployment's database here, so the
	// bridge's answer is a read of the database this deployment serves with.
	withDatabase := func(t *testing.T, db *rtdb.Client) {
		t.Helper()
		fakeCredentialsService(t)
		credentialsTest.database = func(context.Context, string, oauth2.TokenSource) (*rtdb.Client, error) { return db, nil }
	}
	// The seam survives the test: it is one process-wide variable and the walk
	// that reads it is CS1's.
	saved := credsvc.LegacyChain
	t.Cleanup(func() { credsvc.LegacyChain = saved })

	t.Run("an installation's deployment assigns it", func(t *testing.T) {
		for n, v := range credentialsServeEnv {
			t.Setenv(n, v)
		}
		fake := gcpfake.NewRTDB(t)
		db, err := rtdb.New(fake.URL, rtdb.Auth{IDToken: func() string { return "" }})
		if err != nil {
			t.Fatal(err)
		}
		writeRecord(t, db)
		withDatabase(t, db)

		if _, err := credentialsServiceConfig(ctx, os.Getenv, io.Discard); err != nil {
			t.Fatalf("credentialsServiceConfig for an installation: %v", err)
		}
		sources, found := credsvc.LegacyChain.OldSources(slug)
		if !found {
			t.Fatalf("an installation's deployment left the seam answering no old record for %s: a run of that repository whose image predates the routing table carries no table, so the record is the only thing that can answer it, and the stub refuses it — the one thing the migration's first release promises not to do", creds.PathRepoAuth(slug))
		}
		if len(sources) != 1 || sources[0].Pool != config.DefaultClaudeAccount {
			t.Errorf("the bridge answered %v, want the record's own account (%s) as one pool: the record is read from THIS deployment's database, which is the one `fugaro update` wrote it into", sources, config.DefaultClaudeAccount)
		}
	})

	t.Run("a workspace's deployment keeps the stub", func(t *testing.T) {
		const workspace = "aurora-ws"
		for n, v := range workspaceServeEnv(workspace) {
			t.Setenv(n, v)
		}
		db := fakeWorkspaceDatabase(t, workspace)
		// The record the mutant would answer: the workspace's own database,
		// holding the same slug, is exactly the reach a member's call must not
		// be served off (another installation's record, where the workspace
		// holds one).
		writeRecord(t, db)
		withDatabase(t, db)
		credentialsWorkspaceTest = func(cfg credsvc.WorkspaceScopeConfig) (credsvc.ScopeResolver, error) {
			return credsvc.NewWorkspaceScopeResolver(cfg)
		}
		t.Cleanup(func() { credentialsWorkspaceTest = nil })

		if _, err := credentialsServiceConfig(ctx, os.Getenv, io.Discard); err != nil {
			t.Fatalf("credentialsServiceConfig for a workspace: %v", err)
		}
		if sources, found := credsvc.LegacyChain.OldSources(slug); found {
			t.Errorf("a workspace's deployment assigned the old-record bridge, and it answered %v out of the workspace's OWN database: a member's record lives in the MEMBER's database, read per call through the scope's own reader, so a process-wide bridge over this one answers a member's older run off a reach nobody recorded for it — an absent record where this database holds no such slug, and another installation's record where it does", sources)
		}
	})
}

// TestServeRefusesAHalfSetWorkspaceEnvironment is the fail-closed half of the
// acceptance: a workspace-mode deployment whose environment is incomplete does
// not serve. Every one of the six names is required, and the refusal names the
// one that is missing — never a value — before anything reaches Google, so a
// deployment that is wrong costs nothing but the line an operator reads.
//
// The list itself is pinned here too, and it is the third side of the same
// fact: the six names plus the mode's own name are EXACTLY
// internal/infra/tf's WorkspaceServiceEnv, which is what the workspace root's
// cover holds its container to (WS6). A name this command reads and that list
// does not carry is a process that refuses to start on the container the root
// creates; a name the list carries and this command does not read is a fact an
// operator would believe was in effect.
func TestServeRefusesAHalfSetWorkspaceEnvironment(t *testing.T) {
	want := append(slices.Clone(credentialsServiceWorkspaceEnvNames), creds.WorkspaceModeEnv)
	have := slices.Clone(infraTF.WorkspaceServiceEnv)
	slices.Sort(want)
	slices.Sort(have)
	if !slices.Equal(have, want) {
		t.Fatalf("the workspace root's environment is %v and this command reads %v: the root's cover admits exactly its own list on that container, so a name on one side and not the other is a service that cannot start (or a fact nothing sets)", have, want)
	}

	for _, k := range credentialsServiceWorkspaceEnvNames {
		t.Run(k, func(t *testing.T) {
			// Every name is set, then the one under test is emptied: an empty
			// variable is not a value the root can set (Terraform writes each of
			// these from an input the workspace has), so the refusal must be
			// about that name and not about another.
			for n, v := range workspaceServeEnv("aurora-ws") {
				t.Setenv(n, v)
			}
			t.Setenv(k, "")
			// No seam (credentialsTest is nil): every refusal below is made
			// before the configuration reaches Google, which is the property
			// this pins — a deployment that is wrong costs nothing but the
			// refusal.
			_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error (exit 1)", err)
			}
			if !strings.Contains(err.Error(), k) {
				t.Fatalf("err = %v, want a refusal naming %s", err, k)
			}
			if !strings.Contains(err.Error(), creds.WorkspaceModeEnv) {
				t.Errorf("err = %v, want the refusal to name %s as well: it is what says this is the workspace's container, and so which root sets the name that is missing", err, creds.WorkspaceModeEnv)
			}
		})
	}

	t.Run("the mode alone", func(t *testing.T) {
		// The mode's name is the process's first read, so a container that sets
		// it and nothing else is refused for the first of the six rather than
		// for the installation's job document — which a workspace's deployment
		// never has.
		t.Setenv(creds.WorkspaceModeEnv, "aurora-ws")
		_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
		if err == nil || !strings.Contains(err.Error(), credentialsServiceWorkspaceEnvNames[0]) {
			t.Fatalf("err = %v, want a refusal naming %s (the first name a workspace's process needs and this deployment did not set)", err, credentialsServiceWorkspaceEnvNames[0])
		}
		if strings.Contains(err.Error(), envJobAccounts) {
			t.Fatalf("err = %v, want no refusal about %s: a workspace's deployment has no job document, and requiring one would refuse the container the workspace root creates", err, envJobAccounts)
		}
	})

	t.Run("the mode names another project", func(t *testing.T) {
		// The mode's value is the workspace's own Fugaro project name — the
		// name its node, its record and its bundle are read by — so a pair that
		// disagrees is a deployment no root produces, and one this process
		// refuses rather than serving with one project's bundle under another
		// project's name.
		for n, v := range workspaceServeEnv("aurora-ws") {
			t.Setenv(n, v)
		}
		t.Setenv(envProject, "other-project")
		_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("err = %v, want a user error (exit 1)", err)
		}
		for _, n := range []string{creds.WorkspaceModeEnv, envProject} {
			if !strings.Contains(err.Error(), n) {
				t.Errorf("err = %v, want a refusal naming %s", err, n)
			}
		}
		if strings.Contains(err.Error(), "other-project") {
			t.Errorf("err = %v, which carries the value of %s: a refusal names the variable and never what it held", err, envProject)
		}
	})

	t.Run("the workspace has published no node", func(t *testing.T) {
		// A workspace-mode deployment whose workspace published nothing is a
		// refusal at startup: a service that started and refused every call
		// would look healthy while every run that reached it halted (D143), and
		// the sentence an owner reads has to name the step that publishes the
		// node.
		const workspace = "aurora-ws"
		for n, v := range workspaceServeEnv(workspace) {
			t.Setenv(n, v)
		}
		fakeCredentialsService(t)
		empty := gcpfake.NewRTDB(t)
		client, err := rtdb.New(empty.URL, rtdb.Auth{IDToken: func() string { return "" }})
		if err != nil {
			t.Fatal(err)
		}
		credentialsTest.database = func(_ context.Context, _ string, _ oauth2.TokenSource) (*rtdb.Client, error) {
			return client, nil
		}
		_, err = credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("err = %v, want a user error: a workspace that published no credentials node has no member this service could bind a run to", err)
		}
		if !strings.Contains(err.Error(), creds.WorkspaceModeEnv) || !strings.Contains(err.Error(), "fugaro update") {
			t.Errorf("err = %v, want the refusal to name %s and the owner's `fugaro update`, which is the command that publishes the node", err, creds.WorkspaceModeEnv)
		}
	})
}
