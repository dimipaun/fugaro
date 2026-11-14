package bbtokens

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// fakeBundle is the bundle's tokens as a selection sees them: the names the
// document holds and each entry's value. It is this file's own double rather
// than the contract's Stub, which I1 deletes: the selector's tests are not the
// placeholder's and must outlive it.
type fakeBundle struct {
	names  []Name
	values map[Name]string
}

func (b fakeBundle) Names() []Name { return b.names }

func (b fakeBundle) Value(name Name) (string, bool) {
	v, ok := b.values[name]
	return v, ok
}

// fakeResolver answers the project UUID each token is set to, and records which
// token it was asked with: R2's whole point is that the lookup is made with the
// candidate project token itself and never with a wider or a narrower one.
type fakeResolver struct {
	uuids map[Name]string
	err   error
	asked []Name
}

func (r *fakeResolver) ProjectOf(_ context.Context, _ string, tok Name) (string, error) {
	r.asked = append(r.asked, tok)
	if r.err != nil {
		return "", r.err
	}
	return r.uuids[tok], nil
}

func (r *fakeResolver) Evict(string) {}

// fakeStatus is the status store as a selection reads it: one status per token,
// and the zero Status for a token nothing recorded.
type fakeStatus struct {
	marks map[Name]Status
	err   error
}

func (s fakeStatus) Read(_ context.Context, name Name) (Status, error) {
	if s.err != nil {
		return Status{}, s.err
	}
	return s.marks[name], nil
}

func (s fakeStatus) Write(_ context.Context, _ Name, _ Status) error { return nil }

func (s fakeStatus) CountRequest(_ context.Context, _ Name) error { return nil }

// bundleOf is the bundle a node's records are matched against: every token the
// test wants the document to hold, and nothing else.
func bundleOf(names ...Name) fakeBundle {
	values := make(map[Name]string, len(names))
	for _, n := range names {
		values[n] = "value-of-" + string(n)
	}
	return fakeBundle{names: names, values: values}
}

// selectionOf is one test's selection: the node's typed records, a resolver
// that answers what the test set, and a status store.
func selectionOf(t *testing.T, records []Record, resolver *fakeResolver, status fakeStatus) Selector {
	t.Helper()
	if resolver == nil {
		resolver = &fakeResolver{}
	}
	return NewSelector(records, resolver, status)
}

// refusalClass reads the class off a selection's answer the way the contract
// says a caller must — with errors.As, never off the error's text.
func refusalClass(t *testing.T, err error) *Refusal {
	t.Helper()
	var ref *Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("Select answered %v, want a *Refusal", err)
	}
	return ref
}

// TestSelectNarrowestFirst is D2355 read from the selector: of the tokens that
// cover a repository, the narrowest serves — an exact repository before a
// project before a workspace — and the legacy untyped key ranks after every
// typed record however exactly its own list names the repository (O6). A
// selector that answered the widest covering token, or that let the legacy
// key's exact match outrank a typed record, fails here.
func TestSelectNarrowestFirst(t *testing.T) {
	ctx := context.Background()
	const canonical = "bitbucket/acme/app"

	repo := Record{Name: "bitbucket-token--repo", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}}
	project := Record{
		Name: "bitbucket-token--proj", Type: TypeProject,
		Coverage: []string{"bitbucket/acme/*"}, ProjectUUID: "proj-uuid", ProjectKey: "PROJ",
	}
	workspace := Record{Name: "bitbucket-token--ws", Type: TypeWorkspace, Coverage: []string{"bitbucket/acme/*"}}

	for _, tc := range []struct {
		why     string
		records []Record
		held    []Name
		allow   []string
		want    Name
	}{
		{
			why:     "a repository token is narrower than a project and a workspace token",
			records: []Record{workspace, project, repo},
			held:    []Name{workspace.Name, project.Name, repo.Name, LegacyName},
			allow:   []string{"bitbucket/acme/app"},
			want:    repo.Name,
		},
		{
			why:     "a project token is narrower than a workspace token",
			records: []Record{workspace, project},
			held:    []Name{workspace.Name, project.Name},
			want:    project.Name,
		},
		{
			why:     "a workspace token serves when nothing narrower covers",
			records: []Record{workspace},
			held:    []Name{workspace.Name},
			want:    workspace.Name,
		},
		{
			why:     "the legacy key ranks after every typed record, even one that covers a whole workspace",
			records: []Record{workspace},
			held:    []Name{workspace.Name, LegacyName},
			allow:   []string{"bitbucket/acme/app"},
			want:    workspace.Name,
		},
		{
			why:     "the legacy key serves a repository no typed record covers",
			records: nil,
			held:    []Name{LegacyName},
			allow:   []string{"bitbucket/acme/app"},
			want:    LegacyName,
		},
	} {
		t.Run(tc.why, func(t *testing.T) {
			resolver := &fakeResolver{uuids: map[Name]string{project.Name: "proj-uuid"}}
			sel := selectionOf(t, tc.records, resolver, fakeStatus{})
			snap := Snapshot{Policy: policyWithLegacy(tc.allow), Bundle: bundleOf(tc.held...)}

			choice, err := sel.Select(ctx, snap, canonical, false)
			if err != nil {
				t.Fatalf("Select = %v, want %q", err, tc.want)
			}
			if choice.Name != tc.want {
				t.Errorf("Select = %q, want %q", choice.Name, tc.want)
			}
		})
	}
}

// TestSelectNarrowestFirstTiesByTokenName is the other half of the order: two
// tokens of one rank that both cover the repository are decided by their
// document keys, so the same request served twice walks the same candidate
// first and an operator reading an audit line reads a stable answer.
func TestSelectNarrowestFirstTiesByTokenName(t *testing.T) {
	ctx := context.Background()
	later := Record{Name: "bitbucket-token--b", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}}
	earlier := Record{Name: "bitbucket-token--a", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}}

	sel := selectionOf(t, []Record{later, earlier}, nil, fakeStatus{})
	snap := Snapshot{Bundle: bundleOf(later.Name, earlier.Name)}

	choice, err := sel.Select(ctx, snap, "bitbucket/acme/app", false)
	if err != nil {
		t.Fatalf("Select = %v, want the name-ordered first token", err)
	}
	if choice.Name != earlier.Name {
		t.Errorf("Select = %q, want %q", choice.Name, earlier.Name)
	}
}

// TestSelectProjectMatchesByUUID is R2 in the selector: a project token is a
// candidate only when the lookup made with that token itself answers the UUID
// the record names, and an empty UUID never matches — a token that names no
// project would otherwise serve every repository its lookup failed for.
func TestSelectProjectMatchesByUUID(t *testing.T) {
	ctx := context.Background()
	const canonical = "bitbucket/acme/app"

	project := Record{
		Name: "bitbucket-token--proj", Type: TypeProject,
		Coverage: []string{"bitbucket/acme/*"}, ProjectUUID: "proj-uuid", ProjectKey: "PROJ",
	}
	workspace := Record{Name: "bitbucket-token--ws", Type: TypeWorkspace, Coverage: []string{"bitbucket/acme/*"}}
	held := []Name{project.Name, workspace.Name}
	snap := Snapshot{Bundle: bundleOf(held...)}

	t.Run("the matching UUID serves", func(t *testing.T) {
		resolver := &fakeResolver{uuids: map[Name]string{project.Name: "proj-uuid"}}
		sel := selectionOf(t, []Record{workspace, project}, resolver, fakeStatus{})

		choice, err := sel.Select(ctx, snap, canonical, false)
		if err != nil {
			t.Fatalf("Select = %v, want the project token", err)
		}
		if choice.Name != project.Name {
			t.Errorf("Select = %q, want %q", choice.Name, project.Name)
		}
		if len(resolver.asked) != 1 || resolver.asked[0] != project.Name {
			t.Errorf("the lookup was asked with %v, want the candidate project token %q alone", resolver.asked, project.Name)
		}
	})

	for _, tc := range []struct {
		why  string
		uuid string
	}{
		{why: "another project's UUID", uuid: "another-uuid"},
		{why: "no UUID at all, which is Bitbucket's 403 or 404", uuid: ""},
	} {
		t.Run(tc.why, func(t *testing.T) {
			resolver := &fakeResolver{uuids: map[Name]string{project.Name: tc.uuid}}
			sel := selectionOf(t, []Record{workspace, project}, resolver, fakeStatus{})

			choice, err := sel.Select(ctx, snap, canonical, false)
			if err != nil {
				t.Fatalf("Select = %v, want the workspace token", err)
			}
			if choice.Name != workspace.Name {
				t.Errorf("Select = %q, want the wider workspace token %q", choice.Name, workspace.Name)
			}
		})
	}

	t.Run("a record naming no project is not a candidate at all", func(t *testing.T) {
		nameless := Record{Name: "bitbucket-token--proj", Type: TypeProject, Coverage: []string{"bitbucket/acme/*"}}
		resolver := &fakeResolver{uuids: map[Name]string{nameless.Name: "proj-uuid"}}
		sel := selectionOf(t, []Record{nameless, workspace}, resolver, fakeStatus{})

		choice, err := sel.Select(ctx, snap, canonical, false)
		if err != nil {
			t.Fatalf("Select = %v, want the workspace token", err)
		}
		if choice.Name != workspace.Name {
			t.Errorf("Select = %q, want %q: a project token that names no UUID matches nothing", choice.Name, workspace.Name)
		}
		if len(resolver.asked) != 0 {
			t.Errorf("the lookup was asked %v about a record a reader refuses, want no question", resolver.asked)
		}
	})
}

// TestSelectNamesUncoveredRepository is the refusal the plan pins for T4: a
// repository no token covers is refused naming the repository, and never a
// token — a caller told which token exists learns about an installation it has
// no business reading (R9).
func TestSelectNamesUncoveredRepository(t *testing.T) {
	ctx := context.Background()
	const canonical = "bitbucket/another-workspace/app"

	workspace := Record{Name: "bitbucket-token--ws", Type: TypeWorkspace, Coverage: []string{"bitbucket/acme/*"}}
	sel := selectionOf(t, []Record{workspace}, nil, fakeStatus{})
	snap := Snapshot{Bundle: bundleOf(workspace.Name, LegacyName)}

	_, err := sel.Select(ctx, snap, canonical, false)
	ref := refusalClass(t, err)
	if ref.Class != ClassNotCovered {
		t.Errorf("Select refused with %q, want %q", ref.Class, ClassNotCovered)
	}
	if ref.Repo != canonical {
		t.Errorf("the refusal names %q, want the repository %q", ref.Repo, canonical)
	}
	if !strings.Contains(ref.Error(), canonical) {
		t.Errorf("the refusal reads %q, want it to name %q", ref.Error(), canonical)
	}
	if strings.Contains(ref.Error(), string(workspace.Name)) || strings.Contains(ref.Error(), "bitbucket-token") {
		t.Errorf("the refusal reads %q, and a refusal names no token", ref.Error())
	}
}

// TestSelectNoTokenAtAll is the store's own refusal: a bundle holding no
// Bitbucket token is an installation that stored none rather than a coverage
// gap, and a proxied run holds none of its own — so this is a refusal and never
// a call forwarded without a credential.
func TestSelectNoTokenAtAll(t *testing.T) {
	ctx := context.Background()
	sel := selectionOf(t, nil, nil, fakeStatus{})

	for _, tc := range []struct {
		why  string
		snap Snapshot
	}{
		{why: "no bundle at all", snap: Snapshot{Policy: policyWithLegacy([]string{"bitbucket/acme/app"})}},
		{why: "a bundle holding nothing", snap: Snapshot{Bundle: fakeBundle{}}},
	} {
		t.Run(tc.why, func(t *testing.T) {
			_, err := sel.Select(ctx, tc.snap, "bitbucket/acme/app", false)
			ref := refusalClass(t, err)
			if ref.Class != ClassNoToken {
				t.Errorf("Select refused with %q, want %q", ref.Class, ClassNoToken)
			}
		})
	}
}

// TestSelectSkipsThrottledAndBroken is R12: a token the store marks is skipped
// and the narrowest USABLE token serves, which may be a wider one — the stated
// price of the swap — and a repository no usable token covers is refused with
// the class that says why.
func TestSelectSkipsThrottledAndBroken(t *testing.T) {
	ctx := context.Background()
	const canonical = "bitbucket/acme/app"

	repo := Record{Name: "bitbucket-token--repo", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}}
	workspace := Record{Name: "bitbucket-token--ws", Type: TypeWorkspace, Coverage: []string{"bitbucket/acme/*"}}
	both := []Record{repo, workspace}
	snap := Snapshot{Bundle: bundleOf(repo.Name, workspace.Name)}

	for _, tc := range []struct {
		why   string
		marks map[Name]Status
		want  Name
	}{
		{
			why:   "a throttled repository token leaves the wider workspace token to serve",
			marks: map[Name]Status{repo.Name: {State: StateExhausted, Until: time.Now().Add(time.Minute)}},
			want:  workspace.Name,
		},
		{
			why:   "a backed-off repository token is skipped too",
			marks: map[Name]Status{repo.Name: {State: StateBackedOff, Until: time.Now().Add(time.Minute)}},
			want:  workspace.Name,
		},
		{
			why:   "a broken repository token leaves the wider workspace token to serve",
			marks: map[Name]Status{repo.Name: {State: StateBroken}},
			want:  workspace.Name,
		},
		{
			why:   "a throttle whose Retry-After has passed holds nothing",
			marks: map[Name]Status{repo.Name: {State: StateExhausted, Until: time.Now().Add(-time.Minute)}},
			want:  repo.Name,
		},
		// A broken mark carries no Until — the contract says so, and T6's store
		// writes none — so a node that carries one anyway must not read as a
		// hold that lapsed: an operator clears a broken token, nothing else does.
		{
			why:   "a broken token is not a hold that lapses, whatever Until the node carries",
			marks: map[Name]Status{repo.Name: {State: StateBroken, Until: time.Now().Add(-time.Minute)}},
			want:  workspace.Name,
		},
		// Until's zero value is "no end" and not "lapsed long ago", which is the
		// reading `!now.Before(Until)` on its own would give it.
		{
			why:   "an exhaustion that named no end holds on",
			marks: map[Name]Status{repo.Name: {State: StateExhausted}},
			want:  workspace.Name,
		},
	} {
		t.Run(tc.why, func(t *testing.T) {
			sel := selectionOf(t, both, nil, fakeStatus{marks: tc.marks})
			choice, err := sel.Select(ctx, snap, canonical, false)
			if err != nil {
				t.Fatalf("Select = %v, want %q", err, tc.want)
			}
			if choice.Name != tc.want {
				t.Errorf("Select = %q, want %q", choice.Name, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		why   string
		marks map[Name]Status
		want  RefusalClass
	}{
		{
			why:   "every covering token throttled",
			marks: map[Name]Status{repo.Name: {State: StateExhausted, Until: time.Now().Add(time.Minute)}, workspace.Name: {State: StateExhausted, Until: time.Now().Add(time.Minute)}},
			want:  ClassThrottled,
		},
		{
			why:   "every covering token broken",
			marks: map[Name]Status{repo.Name: {State: StateBroken}, workspace.Name: {State: StateBroken}},
			want:  ClassBroken,
		},
		{
			why:   "a broken token and a throttled one: the throttle is the answer, because it lapses and the next call reports the broken one",
			marks: map[Name]Status{repo.Name: {State: StateBroken}, workspace.Name: {State: StateExhausted, Until: time.Now().Add(time.Minute)}},
			want:  ClassThrottled,
		},
	} {
		t.Run(tc.why, func(t *testing.T) {
			sel := selectionOf(t, both, nil, fakeStatus{marks: tc.marks})
			_, err := sel.Select(ctx, snap, canonical, false)
			ref := refusalClass(t, err)
			if ref.Class != tc.want {
				t.Errorf("Select refused with %q, want %q", ref.Class, tc.want)
			}
			if ref.Repo != canonical {
				t.Errorf("the refusal names %q, want the repository %q", ref.Repo, canonical)
			}
		})
	}

	// The class a lone broken token earns, and not ClassNotCovered: the token
	// covers the repository and is in the way, which is what an operator has to
	// read off the refusal.
	t.Run("a lone broken token whose Until has passed is still the reason", func(t *testing.T) {
		marks := map[Name]Status{repo.Name: {State: StateBroken, Until: time.Now().Add(-time.Minute)}}
		sel := selectionOf(t, []Record{repo}, nil, fakeStatus{marks: marks})

		_, err := sel.Select(ctx, Snapshot{Bundle: bundleOf(repo.Name)}, canonical, false)
		ref := refusalClass(t, err)
		if ref.Class != ClassBroken {
			t.Errorf("Select refused with %q, want %q", ref.Class, ClassBroken)
		}
	})
}

// TestSelectSkipsAKeyWithNoRecordAndARecordWithNoKey is R14's fail-closed half:
// a token has to be in BOTH documents to serve anything, so a bundle key with
// no node record and a node record with no bundle key are skipped rather than
// selected — a half-published token is not a candidate, whichever side of it
// landed first.
func TestSelectSkipsAKeyWithNoRecordAndARecordWithNoKey(t *testing.T) {
	ctx := context.Background()
	const canonical = "bitbucket/acme/app"

	ghost := Record{Name: "bitbucket-token--ghost", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}}
	orphan := Record{Name: "bitbucket-token--orphan", Type: TypeWorkspace, Coverage: []string{"bitbucket/acme/*"}}
	legacyNamed := Record{Name: LegacyName, Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}}

	t.Run("a bundle key with no node record is not a candidate", func(t *testing.T) {
		sel := selectionOf(t, nil, nil, fakeStatus{})
		snap := Snapshot{Bundle: bundleOf(ghost.Name, "bitbucket-token--published")}

		_, err := sel.Select(ctx, snap, canonical, false)
		ref := refusalClass(t, err)
		if ref.Class != ClassNotCovered {
			t.Errorf("Select refused with %q, want %q: the bundle holds a token no record names, and it serves nothing", ref.Class, ClassNotCovered)
		}
	})

	t.Run("a node record with no bundle key is not a candidate", func(t *testing.T) {
		sel := selectionOf(t, []Record{orphan}, nil, fakeStatus{})
		snap := Snapshot{Bundle: bundleOf("bitbucket-token--published")}

		_, err := sel.Select(ctx, snap, canonical, false)
		ref := refusalClass(t, err)
		if ref.Class != ClassNotCovered {
			t.Errorf("Select refused with %q, want %q: the node names a token the bundle does not hold", ref.Class, ClassNotCovered)
		}
	})

	t.Run("a typed record named the legacy key serves nothing", func(t *testing.T) {
		sel := selectionOf(t, []Record{legacyNamed}, nil, fakeStatus{})
		snap := Snapshot{Bundle: bundleOf(LegacyName), Policy: policyWithLegacy(nil)}

		_, err := sel.Select(ctx, snap, canonical, false)
		ref := refusalClass(t, err)
		if ref.Class != ClassNotCovered {
			t.Errorf("Select refused with %q, want %q: the legacy key is the untyped record alone (R14)", ref.Class, ClassNotCovered)
		}
	})

	t.Run("the half-published token does not stop a whole one", func(t *testing.T) {
		whole := Record{Name: "bitbucket-token--whole", Type: TypeRepo, Coverage: []string{"bitbucket/acme/app"}}
		sel := selectionOf(t, []Record{whole, orphan}, nil, fakeStatus{})
		snap := Snapshot{Bundle: bundleOf(whole.Name)}

		choice, err := sel.Select(ctx, snap, canonical, false)
		if err != nil {
			t.Fatalf("Select = %v, want the token both documents hold", err)
		}
		if choice.Name != whole.Name {
			t.Errorf("Select = %q, want %q", choice.Name, whole.Name)
		}
	})
}

// TestSelectReadsTheLegacyRecordFromTheSnapshot is R3: the legacy key takes part
// in selection as an IMPLICIT record, untyped, whose coverage is the node's
// bitbucket_token.allow_repos list as written — so the selector reads that list
// off the snapshot rather than being handed it, and a node with no block leaves
// the legacy key reaching nothing.
func TestSelectReadsTheLegacyRecordFromTheSnapshot(t *testing.T) {
	ctx := context.Background()
	sel := selectionOf(t, nil, nil, fakeStatus{})

	for _, tc := range []struct {
		why    string
		policy *projectpolicy.Policy
		want   bool
	}{
		{
			why:    "the block's list is the legacy record's coverage",
			policy: policyWithLegacy([]string{"bitbucket/acme/app"}),
			want:   true,
		},
		{
			why:    "a node with no block holds nothing",
			policy: &projectpolicy.Policy{},
		},
		{
			why: "no node at all",
		},
	} {
		t.Run(tc.why, func(t *testing.T) {
			snap := Snapshot{Policy: tc.policy, Bundle: bundleOf(LegacyName)}
			choice, err := sel.Select(ctx, snap, "bitbucket/acme/app", false)
			if !tc.want {
				ref := refusalClass(t, err)
				if ref.Class != ClassNotCovered {
					t.Errorf("Select refused with %q, want %q", ref.Class, ClassNotCovered)
				}
				return
			}
			if err != nil {
				t.Fatalf("Select = %v, want the legacy token", err)
			}
			if choice.Name != LegacyName {
				t.Errorf("Select = %q, want %q", choice.Name, LegacyName)
			}
		})
	}
}

// TestSelectWithoutAResolverOrStatusKeepsTheStoresOutOfIt is the seam the
// contract leaves open: a selection whose resolver cannot answer, or whose
// status cannot be read, fails closed — the token it could not confirm is not a
// candidate — and never forwards the call on a guess.
func TestSelectWithoutAResolverOrStatusKeepsTheStoresOutOfIt(t *testing.T) {
	ctx := context.Background()
	const canonical = "bitbucket/acme/app"

	project := Record{
		Name: "bitbucket-token--proj", Type: TypeProject,
		Coverage: []string{"bitbucket/acme/*"}, ProjectUUID: "proj-uuid",
	}
	workspace := Record{Name: "bitbucket-token--ws", Type: TypeWorkspace, Coverage: []string{"bitbucket/acme/*"}}
	snap := Snapshot{Bundle: bundleOf(project.Name, workspace.Name)}

	t.Run("a lookup that fails leaves the project token out", func(t *testing.T) {
		resolver := &fakeResolver{err: errors.New("the lookup is down")}
		sel := selectionOf(t, []Record{project, workspace}, resolver, fakeStatus{})

		choice, err := sel.Select(ctx, snap, canonical, false)
		if err != nil {
			t.Fatalf("Select = %v, want the workspace token", err)
		}
		if choice.Name != workspace.Name {
			t.Errorf("Select = %q, want %q", choice.Name, workspace.Name)
		}
	})

	t.Run("a status that cannot be read is not an available one", func(t *testing.T) {
		sel := selectionOf(t, []Record{workspace}, nil, fakeStatus{err: errors.New("the store is down")})

		_, err := sel.Select(ctx, snap, canonical, false)
		ref := refusalClass(t, err)
		if ref.Class != ClassNotCovered {
			t.Errorf("Select refused with %q, want %q", ref.Class, ClassNotCovered)
		}
	})
}

// policyWithLegacy is the node a test's selection reads: the bitbucket_token
// block's allow_repos list, and nothing else, since the typed records are the
// selector's own input.
func policyWithLegacy(allow []string) *projectpolicy.Policy {
	if allow == nil {
		return &projectpolicy.Policy{}
	}
	return &projectpolicy.Policy{BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: allow}}
}
