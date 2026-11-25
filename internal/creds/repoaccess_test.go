package creds

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// repoAccessFixture is a record of the shape C1 (d) writes: one craft whose
// project wrote an access, one craft the project wrote nothing for (so the
// migration names one credential per model family, F43), one grade, and the
// subscription credentials `--token` may name. Every member is a name — the
// craft it is for, the credential's or pool's name, and whether that pool is
// the cascade's.
func repoAccessFixture() RepoAccess {
	return RepoAccess{
		Version: RepoAccessVersion,
		Crafts: map[string][]AccessRef{
			config.CraftCoder: {{Name: "alice"}},
			config.CraftSeniorReview: {
				{Name: "alice"},
				{Name: config.CredentialAnthropicKey},
			},
			config.CraftMergeJudge: {{Name: config.PoolCascade, Pool: true, Cascade: true}},
		},
		Grades: map[string][]AccessRef{
			config.GradeStrong: {{Name: "alice"}},
		},
		Tokens: []string{config.DefaultClaudeAccount, "alice"},
	}
}

// TestTheRecordNamesCredentialsAndNothingElse is the contract's pin over (d).
//
// It states the record as a shape — the members it carries and no others — so a
// member that could carry a credential's value is an edit a reviewer sees here,
// exactly as the auth record's own test does: the record is under creds/, it is
// read on every call the service serves, and what it may carry is names.
//
// It also pins the two things the plan names: the record's own path and its own
// version, and R4's statement that a run's reach is the union over its crafts —
// which is a fact about the record and not about one craft of it.
func TestTheRecordNamesCredentialsAndNothingElse(t *testing.T) {
	if got, want := PathRepoAccess("owner/name"), "creds/repoaccess/owner%2Fname"; got != want {
		t.Fatalf("PathRepoAccess = %q, want %q", got, want)
	}
	// The old record keeps its own path: the two live side by side through
	// release 1, and one of them must never be read as the other (R4, R15).
	if PathRepoAccess(run.Slug) == PathRepoAuth(run.Slug) {
		t.Fatal("the access record and the auth record share a path")
	}

	db := newFakeDB()
	if err := PutRepoAccess(ctx, db, run.Slug, repoAccessFixture()); err != nil {
		t.Fatalf("writing the record: %v", err)
	}

	var node map[string]json.RawMessage
	if err := json.Unmarshal(rawNode(t, db, PathRepoAccess(run.Slug)), &node); err != nil {
		t.Fatalf("decoding the record node: %v", err)
	}
	got := make([]string, 0, len(node))
	for k := range node {
		got = append(got, k)
	}
	slices.Sort(got)
	if want := []string{"crafts", "grades", "tokens", "version"}; !slices.Equal(got, want) {
		t.Fatalf("the record node carries %v; want exactly %v — the record names what a repository's runs may be served by, and holds no value", got, want)
	}

	var crafts map[string][]map[string]json.RawMessage
	if err := json.Unmarshal(node["crafts"], &crafts); err != nil {
		t.Fatalf("decoding the crafts: %v", err)
	}
	for craft, refs := range crafts {
		for i, ref := range refs {
			members := make([]string, 0, len(ref))
			for k := range ref {
				members = append(members, k)
			}
			slices.Sort(members)
			switch len(members) {
			case 1:
				if members[0] != "name" {
					t.Fatalf("%s[%d] carries %v; want a name", craft, i, members)
				}
			case 2:
				if !slices.Equal(members, []string{"name", "pool"}) {
					t.Fatalf("%s[%d] carries %v; want a name and whether it is a pool", craft, i, members)
				}
			case 3:
				if !slices.Equal(members, []string{"cascade", "name", "pool"}) {
					t.Fatalf("%s[%d] carries %v; want a name, a pool and the cascade's marker", craft, i, members)
				}
			default:
				t.Fatalf("%s[%d] carries %v; want a name, and for a pool whether it carries the cascade's marker", craft, i, members)
			}
		}
	}

	read, found, err := ReadRepoAccess(ctx, db, run.Slug)
	if err != nil || !found {
		t.Fatalf("reading the record: found=%v err=%v", found, err)
	}
	if read.Version != RepoAccessVersion {
		t.Errorf("the record read back at version %d, want %d", read.Version, RepoAccessVersion)
	}

	// One credential per model family for a craft the project wrote nothing
	// for (F43), and the craft's own access where it wrote one.
	if refs, ok := read.AccessForCraft(config.CraftCoder); !ok || len(refs) != 1 || refs[0].Name != "alice" {
		t.Errorf("the coder's refs = %+v (ok=%v), want alice", refs, ok)
	}
	if refs, ok := read.AccessForCraft(config.CraftSecurityReview); ok {
		t.Errorf("the security review's refs = %+v, want none: the project wrote nothing for it", refs)
	}
	if refs, ok := read.AccessForGrade(config.GradeStrong); !ok || len(refs) != 1 || refs[0].Name != "alice" {
		t.Errorf("strong's refs = %+v (ok=%v), want alice", refs, ok)
	}
	if !read.NamesToken("alice") || !read.NamesToken(config.DefaultClaudeAccount) {
		t.Error("the record does not name a subscription credential --token may name")
	}
	if read.NamesToken("carol") {
		t.Error("the record names a credential it was not written with")
	}

	// R4: a call declares its craft, and the service serves it what the record
	// names for that craft — so a run's whole reach is the union over its
	// crafts, which is what Reach answers.
	reach := read.Reach()
	for _, want := range []string{"alice", config.CredentialAnthropicKey, config.PoolCascade} {
		if !slices.Contains(reach, want) {
			t.Errorf("Reach = %v, want it to hold %q", reach, want)
		}
	}
	if slices.Contains(reach, "carol") {
		t.Errorf("Reach = %v, want it to hold nothing the record does not name", reach)
	}

	// The record's refusals, each of which is a shape no reader could decide
	// from: a version this release does not know, a node carrying a member it
	// does not model, and a name that is no name.
	if err := PutRepoAccess(ctx, db, "", repoAccessFixture()); !errors.Is(err, ErrNoRepoAccess) {
		t.Errorf("writing the record for no repository: err=%v, want ErrNoRepoAccess", err)
	}
	bad := repoAccessFixture()
	bad.Crafts = map[string][]AccessRef{"not-a-craft": {{Name: "alice"}}}
	if err := PutRepoAccess(ctx, db, run.Slug, bad); !errors.Is(err, ErrRepoAccessMalformed) {
		t.Errorf("writing a record with a craft this vocabulary does not know: err=%v, want ErrRepoAccessMalformed", err)
	}
	bad = repoAccessFixture()
	bad.Grades = map[string][]AccessRef{"not-a-grade": {{Name: "alice"}}}
	if err := PutRepoAccess(ctx, db, run.Slug, bad); !errors.Is(err, ErrRepoAccessMalformed) {
		t.Errorf("writing a record with a grade this vocabulary does not know: err=%v, want ErrRepoAccessMalformed", err)
	}
	bad = repoAccessFixture()
	bad.Crafts = map[string][]AccessRef{config.CraftCoder: {{Name: "alice", Cascade: true}}}
	if err := PutRepoAccess(ctx, db, run.Slug, bad); !errors.Is(err, ErrRepoAccessMalformed) {
		t.Errorf("writing a credential that carries the cascade's marker: err=%v, want ErrRepoAccessMalformed", err)
	}
	bad = repoAccessFixture()
	bad.Crafts = map[string][]AccessRef{config.CraftCoder: {}}
	if err := PutRepoAccess(ctx, db, run.Slug, bad); !errors.Is(err, ErrRepoAccessMalformed) {
		t.Errorf("writing a craft that names nothing: err=%v, want ErrRepoAccessMalformed", err)
	}
	bad = repoAccessFixture()
	bad.Tokens = []string{"not a name at all"}
	if err := PutRepoAccess(ctx, db, run.Slug, bad); !errors.Is(err, ErrRepoAccessMalformed) {
		t.Errorf("writing a token entry that is no name: err=%v, want ErrRepoAccessMalformed", err)
	}

	if _, _, err := ReadRepoAccess(ctx, db, ""); !errors.Is(err, ErrNoRepoAccess) {
		t.Errorf("reading the record for no repository: err=%v, want ErrNoRepoAccess", err)
	}
	db.put(PathRepoAccess(run.Slug), map[string]any{"version": RepoAccessVersion + 1, "crafts": map[string]any{}})
	if _, _, err := ReadRepoAccess(ctx, db, run.Slug); !errors.Is(err, ErrRepoAccessVersion) {
		t.Errorf("reading a record of another version: err=%v, want ErrRepoAccessVersion", err)
	}
	db.put(PathRepoAccess(run.Slug), map[string]any{"version": RepoAccessVersion, "crafts": map[string]any{}, "value": "sk-not-a-real-value"})
	if _, _, err := ReadRepoAccess(ctx, db, run.Slug); err == nil {
		t.Error("reading a record carrying a member this release does not model was accepted")
	}

	// A repository no update has recorded is not an error: the caller decides
	// what an absent record means, and for the service that is a refusal of
	// every call that carries the new headers (CM6).
	db = newFakeDB()
	if _, found, err := ReadRepoAccess(ctx, db, run.Slug); found || err != nil {
		t.Errorf("reading a record that was never written: found=%v err=%v, want not found and no error", found, err)
	}
	if _, found, err := (&RTDBStore{DB: db}).RepoAccess(ctx, run.Slug); found || err != nil {
		t.Errorf("the store's read of a record that was never written: found=%v err=%v", found, err)
	}
}
