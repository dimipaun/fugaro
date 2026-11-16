package runstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"
)

// TestChangeSetMemberRecord: a member's own record is where its pull request
// and its verdict live, under its own prefix, because that prefix is the only
// one its run reaches (SECURITY.md). It is the one thing a step that reads a
// whole set reads from a member's own run (D3), so what a member writes is
// what a member reads back, fields and all — and the record repeats the set
// and the run its task named rather than leaving the reader to infer them from
// the prefix it was found under.
func TestChangeSetMemberRecord(t *testing.T) {
	ctx := context.Background()
	s := Open(memblob.OpenBucket(nil), "acme-app", "20260926-221530-a1b2")
	if _, err := s.ReadChangeSetMember(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a member that recorded nothing = %v, want ErrNotFound", err)
	}
	at := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)
	m := &ChangeSetMember{
		Version: 1,
		SetID:   "20260926-221530-a1b2",
		Repo:    "acme/app",
		RunID:   "20260926-221530-a1b2",
		PRURL:   "https://github.com/acme/app/pull/7",
		Verdict: "ship",
		At:      at,
	}
	if err := s.WriteChangeSetMember(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadChangeSetMember(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if *got != *m || !got.At.Equal(at) {
		t.Fatalf("read back %+v, wrote %+v", *got, *m)
	}
	if key := s.Prefix() + ChangeSetMemberFile; key != "runs/acme-app/20260926-221530-a1b2/changeset.json" {
		t.Fatalf("the record lives at %s, want the run's own prefix", key)
	}
	// A run whose pull request is not open yet records what it has: the record
	// reaching a set's reader half-filled is a member mid-run, not a corrupt
	// one.
	half := &ChangeSetMember{Version: 1, SetID: m.SetID, Repo: m.Repo, RunID: m.RunID, At: at}
	if err := s.WriteChangeSetMember(ctx, half); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReadChangeSetMember(ctx); err != nil || got.Verdict != "" || got.PRURL != "" {
		t.Fatalf("a member without a pull request = %+v, %v", got, err)
	}
}

// TestChangeSetMemberRefusesAShapeItCannotRead: the record is the launcher's
// and the run's, and the step that reads it reads every member's — so a record
// that cannot say which set and which run it is, or that names its repository
// in a spelling the member list does not hold, is refused where it is read
// rather than matched against the wrong prefix or the wrong member.
func TestChangeSetMemberRefusesAShapeItCannotRead(t *testing.T) {
	ctx := context.Background()
	b := memblob.OpenBucket(nil)
	s := Open(b, "acme-app", "20260926-221530-a1b2")
	good := ChangeSetMember{Version: 1, SetID: "20260926-221530-a1b2", Repo: "acme/app", RunID: "20260926-221530-a1b2"}
	if err := good.Validate(); err != nil {
		t.Fatalf("a well-formed record was refused: %v", err)
	}
	for _, c := range []struct {
		name string
		m    ChangeSetMember
		want string
	}{
		{"no version", ChangeSetMember{SetID: good.SetID, Repo: good.Repo, RunID: good.RunID}, "version"},
		{"a version from the future", ChangeSetMember{Version: 2, SetID: good.SetID, Repo: good.Repo, RunID: good.RunID}, "version"},
		{"no set", ChangeSetMember{Version: 1, Repo: good.Repo, RunID: good.RunID}, "set_id"},
		{"no run", ChangeSetMember{Version: 1, SetID: good.SetID, Repo: good.Repo}, "run_id"},
		{"no repository", ChangeSetMember{Version: 1, SetID: good.SetID, RunID: good.RunID}, "repo"},
		{"a repository that is not canonical", ChangeSetMember{Version: 1, SetID: good.SetID, Repo: "Acme/App.git", RunID: good.RunID}, "canonical"},
		{"a pull request that is not a URL", ChangeSetMember{Version: 1, SetID: good.SetID, Repo: good.Repo, RunID: good.RunID, PRURL: "pull/7"}, "pr_url"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.m.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate = %v, want a complaint about %q", err, c.want)
			}
		})
	}
	// A record stored with a shape its reader refuses is a corrupt object and
	// not a member: the reader says so rather than handing a half-read member
	// to the caller that asked.
	bad := good
	bad.SetID = "not-a-run"
	if err := s.WriteChangeSetMember(ctx, &bad); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadChangeSetMember(ctx); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("reading a misshapen record = %v, want ErrCorrupt", err)
	}
}
