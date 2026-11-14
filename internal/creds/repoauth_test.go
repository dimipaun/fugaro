package creds

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// repoAuthFixture is a record of the shape `fugaro update` writes for a
// repository whose agent.auth is oauth and whose workflows name two accounts.
// The names are the accounts' own: what a record of this shape carries is the
// names of credentials, never their values.
func repoAuthFixture() RepoAuth {
	return RepoAuth{
		Version:  RepoAuthVersion,
		Auth:     authOAuth,
		Accounts: []string{"team-a", config.DefaultClaudeAccount},
	}
}

// TestRepoAuthRecordHoldsNoValue: the record is the closed shape G18 allows
// under creds/ — a version, the auth, and for a subscription repository the
// account names — and the test states it as that shape rather than as an
// absence, so a member that could carry a value is an edit a reviewer sees
// here. A node carrying one more member is refused by the read rather than half
// understood, which is the fail-closed direction: a record this release cannot
// read whole is one it must not decide a repository's reach from.
//
// What makes the shape enough is what the members ARE: the auth is a value of
// config.AgentAuths and an account is a name, so the record can be read, cached
// and logged, and the service resolves the credential itself out of the
// bundle's own entry for that name.
func TestRepoAuthRecordHoldsNoValue(t *testing.T) {
	db := newFakeDB()
	if err := PutRepoAuth(ctx, db, run.Slug, repoAuthFixture()); err != nil {
		t.Fatalf("writing the record: %v", err)
	}

	var node map[string]json.RawMessage
	if err := json.Unmarshal(rawNode(t, db, PathRepoAuth(run.Slug)), &node); err != nil {
		t.Fatalf("decoding the record node: %v", err)
	}
	got := make([]string, 0, len(node))
	for k := range node {
		got = append(got, k)
	}
	slices.Sort(got)
	if want := []string{"accounts", "auth", "version"}; !slices.Equal(got, want) {
		t.Fatalf("the record node carries %v; want exactly %v — the record names the auth and the accounts a repository's runs go out on, and holds no value", got, want)
	}

	// The names it carries are the repository's own, and the read answers them
	// as the writer put them: one account per name, once each.
	read, found, err := ReadRepoAuth(ctx, db, run.Slug)
	if err != nil || !found {
		t.Fatalf("reading the record: found=%v err=%v", found, err)
	}
	if read.Auth != authOAuth {
		t.Fatalf("the record reads back the auth %q; want %q", read.Auth, authOAuth)
	}
	if want := []string{config.DefaultClaudeAccount, "team-a"}; !slices.Equal(read.Accounts, want) {
		t.Fatalf("the record reads back the accounts %v; want %v", read.Accounts, want)
	}

	// The member a value would have to arrive in is refused, and refused by the
	// read: an unreadable record must fail closed, never be read as the half of
	// itself this release models.
	var withValue map[string]any
	if err := json.Unmarshal(rawNode(t, db, PathRepoAuth(run.Slug)), &withValue); err != nil {
		t.Fatalf("decoding the record node: %v", err)
	}
	withValue["value"] = "x"
	db.put(PathRepoAuth(run.Slug), withValue)
	if _, _, err := ReadRepoAuth(ctx, db, run.Slug); err == nil {
		t.Fatal("a record node carrying a member this release does not model was read as a record: an unreadable record must fail closed")
	}
}

// TestRepoAuthReplacesWhatTheNodeHeld: the record says what the repository's
// configuration says NOW, so a write replaces the node whole — an account a
// workflow stopped naming is gone from the answer rather than merged into it. A
// merge would leave the service serving an account nothing in the repository
// asks for, which is the reach this record exists to bound, and it is the shape
// a reader cannot see: the node would look like a record of the current
// configuration.
func TestRepoAuthReplacesWhatTheNodeHeld(t *testing.T) {
	db := newFakeDB()
	if err := PutRepoAuth(ctx, db, run.Slug, repoAuthFixture()); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	if err := PutRepoAuth(ctx, db, run.Slug, RepoAuth{Version: RepoAuthVersion, Auth: authOAuth, Accounts: []string{"team-b"}}); err != nil {
		t.Fatalf("rewriting the record: %v", err)
	}
	read, found, err := ReadRepoAuth(ctx, db, run.Slug)
	if err != nil || !found {
		t.Fatalf("reading the record: found=%v err=%v", found, err)
	}
	if want := []string{"team-b"}; !slices.Equal(read.Accounts, want) {
		t.Fatalf("after a write the record names %v; want exactly %v — a record says what the repository's configuration says now", read.Accounts, want)
	}
}

// TestRepoAuthAbsentAndUnreadable: a repository no update has recorded is not an
// error — the caller decides what an absent record means — and a node that is
// present but unreadable, or written by a release this one does not know, is
// one: a record half understood could say a repository goes out on the cascade
// when its own configuration says otherwise.
func TestRepoAuthAbsentAndUnreadable(t *testing.T) {
	db := newFakeDB()
	if _, found, err := ReadRepoAuth(ctx, db, run.Slug); found || err != nil {
		t.Fatalf("an absent record answered found=%v err=%v; want found=false and no error", found, err)
	}
	if _, _, err := ReadRepoAuth(ctx, db, ""); !errors.Is(err, ErrNoRepoAuth) {
		t.Fatalf("reading a record for no repository = %v; want ErrNoRepoAuth", err)
	}

	db.put(PathRepoAuth(run.Slug), map[string]any{"version": RepoAuthVersion, "auth": authOAuth, "accounts": []string{"team-a"}, "extra": true})
	if _, _, err := ReadRepoAuth(ctx, db, run.Slug); err == nil {
		t.Fatal("a record carrying a member this release does not model was read: it must be refused rather than half understood")
	}

	db.put(PathRepoAuth(run.Slug), map[string]any{"version": RepoAuthVersion + 1, "auth": authOAuth})
	if _, _, err := ReadRepoAuth(ctx, db, run.Slug); !errors.Is(err, ErrRepoAuthVersion) {
		t.Fatalf("reading a record of another version = %v; want ErrRepoAuthVersion", err)
	}
}

// TestRepoAuthWriterRefusesARecordNothingCanBeDecidedFrom: the writer is where a
// record that could not be decided from has to stop, because the service reads
// what is on the node and has no second source. An auth outside the list, an
// account under an auth that names none, an account outside the shape a Claude
// account's name has, and one account named twice are each refused — and a node
// of another version is refused rather than clobbered.
func TestRepoAuthWriterRefusesARecordNothingCanBeDecidedFrom(t *testing.T) {
	cases := []struct {
		name string
		rec  RepoAuth
	}{
		{"no auth", RepoAuth{Version: RepoAuthVersion}},
		{"an auth this release does not know", RepoAuth{Version: RepoAuthVersion, Auth: "bedrock"}},
		{"accounts under the cascade", RepoAuth{Version: RepoAuthVersion, Auth: config.AuthCascade, Accounts: []string{"team-a"}}},
		{"accounts under an api key", RepoAuth{Version: RepoAuthVersion, Auth: "api-key", Accounts: []string{"team-a"}}},
		{"an account outside the name rule", RepoAuth{Version: RepoAuthVersion, Auth: authOAuth, Accounts: []string{"Team A"}}},
		{"an account longer than the cap", RepoAuth{Version: RepoAuthVersion, Auth: authOAuth, Accounts: []string{strings.Repeat("a", config.MaxClaudeAccountNameLen+1)}}},
		{"one account named twice", RepoAuth{Version: RepoAuthVersion, Auth: authOAuth, Accounts: []string{"team-a", "team-a"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newFakeDB()
			if err := PutRepoAuth(ctx, db, run.Slug, tc.rec); !errors.Is(err, ErrRepoAuthMalformed) {
				t.Fatalf("writing %+v = %v; want ErrRepoAuthMalformed", tc.rec, err)
			}
			if _, found, _ := ReadRepoAuth(ctx, db, run.Slug); found {
				t.Fatal("a refused write left a record on the node")
			}
		})
	}

	db := newFakeDB()
	db.put(PathRepoAuth(run.Slug), map[string]any{"version": RepoAuthVersion + 1, "auth": authOAuth})
	if err := PutRepoAuth(ctx, db, run.Slug, repoAuthFixture()); !errors.Is(err, ErrRepoAuthVersion) {
		t.Fatalf("writing over a record of another version = %v; want ErrRepoAuthVersion", err)
	}
}

// TestRepoAuthWriterSpellsTheDefaultAccount: the axis a repository states an
// account on writes the unsuffixed default as the empty string, and the
// bundle's tier-A credential for that account is named
// config.DefaultClaudeAccount. A record that kept the axis's spelling would
// name an account the service compares against nothing, so the writer puts the
// one spelling the bundle carries, and the node is in name order however the
// caller listed the accounts.
func TestRepoAuthWriterSpellsTheDefaultAccount(t *testing.T) {
	db := newFakeDB()
	if err := PutRepoAuth(ctx, db, run.Slug, RepoAuth{Version: RepoAuthVersion, Auth: authOAuth, Accounts: []string{"team-b", "", "team-a"}}); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	read, found, err := ReadRepoAuth(ctx, db, run.Slug)
	if err != nil || !found {
		t.Fatalf("reading the record: found=%v err=%v", found, err)
	}
	if want := []string{config.DefaultClaudeAccount, "team-a", "team-b"}; !slices.Equal(read.Accounts, want) {
		t.Fatalf("the record reads back the accounts %v; want %v", read.Accounts, want)
	}
	if !read.NamesAccount("") || !read.NamesAccount(config.DefaultClaudeAccount) {
		t.Fatalf("the record %v does not answer for the unsuffixed default account, which it was written with", read.Accounts)
	}
	if read.NamesAccount("team-c") {
		t.Fatalf("the record %v answers for an account it does not name", read.Accounts)
	}
}
