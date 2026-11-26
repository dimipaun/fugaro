package creds

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/routing"
)

func personPrincipal(id string) routing.Principal {
	return routing.Principal{Kind: routing.PrincipalPerson, ID: id}
}

// TestADesignationIsOnePrincipalsPoolForOneProject: the node holds the three
// members C2's table fixes — the principal, the project and the pool's name —
// and no value of any kind: a designation is display data every launcher of the
// project can read, and the pool's members are names.
func TestADesignationIsOnePrincipalsPoolForOneProject(t *testing.T) {
	db := newFakeDB()
	d := routing.Designation{Principal: personPrincipal("1041"), Project: "acme", Pool: "alice-personal"}
	if err := WriteDesignation(ctx, db, d); err != nil {
		t.Fatalf("writing the designation: %v", err)
	}
	got, found, err := ReadDesignation(ctx, db, d.Principal)
	if err != nil || !found {
		t.Fatalf("reading the designation: found=%v err=%v", found, err)
	}
	if got != d {
		t.Fatalf("the designation read back as %+v, want %+v", got, d)
	}

	var node map[string]json.RawMessage
	if err := json.Unmarshal(rawNode(t, db, DesignationPath(d.Principal)), &node); err != nil {
		t.Fatalf("decoding the designation node: %v", err)
	}
	keys := make([]string, 0, len(node))
	for k := range node {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"pool", "principal", "project"}; !slices.Equal(keys, want) {
		t.Fatalf("the designation node carries %v; want exactly %v — one principal's pool for one project", keys, want)
	}
}

// TestDesignationPathIsThePrincipalsOwn: the node is keyed by the principal's
// verified id and escaped the way the rules' claim is, so one principal cannot
// be written at another's path.
func TestDesignationPathIsThePrincipalsOwn(t *testing.T) {
	if got, want := DesignationPath(personPrincipal("1041")), "creds/designations/1041"; got != want {
		t.Fatalf("DesignationPath = %q, want %q", got, want)
	}
	if got, want := DesignationPath(personPrincipal("a/b")), "creds/designations/a%2Fb"; got != want {
		t.Fatalf("DesignationPath = %q, want %q: the segment is escaped, so two principals never share a node", got, want)
	}
	if DesignationPath(personPrincipal("1041")) == DesignationPath(personPrincipal("1042")) {
		t.Fatal("two principals share a designation path")
	}
}

// TestNoDesignationIsNotAnError: a node that is not there, and a node written
// as null, are both "no pool designated" — the answer the lookup turns into a
// skipped source (D3180) and never a failure.
func TestNoDesignationIsNotAnError(t *testing.T) {
	db := newFakeDB()
	if _, found, err := ReadDesignation(ctx, db, personPrincipal("1041")); found || err != nil {
		t.Fatalf("an absent node answered found=%v err=%v, want false and no error", found, err)
	}
	db.nodes[DesignationPath(personPrincipal("1041"))] = json.RawMessage("null")
	if _, found, err := ReadDesignation(ctx, db, personPrincipal("1041")); found || err != nil {
		t.Fatalf("a null node answered found=%v err=%v, want false and no error", found, err)
	}
}

// TestADesignationNamingAnotherPrincipalIsRefused: a node keyed by one
// principal that names another is refused rather than read — reading it would
// serve a pool to whoever the path names, on a record somebody else's route
// wrote — and a write over it is refused too.
func TestADesignationNamingAnotherPrincipalIsRefused(t *testing.T) {
	db := newFakeDB()
	mine := personPrincipal("1041")
	path := DesignationPath(mine)
	db.put(path, routing.Designation{Principal: personPrincipal("1042"), Project: "acme", Pool: "bobs"})

	if _, found, err := ReadDesignation(ctx, db, mine); found || !errors.Is(err, ErrDesignation) {
		t.Fatalf("a record naming another principal answered found=%v err=%v, want it refused", found, err)
	}
	err := WriteDesignation(ctx, db, routing.Designation{Principal: mine, Project: "acme", Pool: "alices"})
	if !errors.Is(err, ErrDesignation) {
		t.Fatalf("writing over another principal's record = %v, want it refused", err)
	}
	var still routing.Designation
	if err := json.Unmarshal(rawNode(t, db, path), &still); err != nil {
		t.Fatal(err)
	}
	if still.Pool != "bobs" {
		t.Fatalf("the refused write left %q, want the record that was there", still.Pool)
	}
}

// TestADesignationIsReadStrictly: a key this release does not model, a record
// that names no pool, and one that names no project are all refusals and never
// "no designation" — a record half understood would serve a fallback source
// where a pool exists.
func TestADesignationIsReadStrictly(t *testing.T) {
	for name, raw := range map[string]string{
		"an unknown key": `{"principal":{"kind":"person","id":"1041"},"project":"acme","pool":"alices","note":"hello"}`,
		"no pool":        `{"principal":{"kind":"person","id":"1041"},"project":"acme","pool":""}`,
		"no project":     `{"principal":{"kind":"person","id":"1041"},"project":"","pool":"alices"}`,
		"no principal":   `{"principal":{"kind":"person","id":""},"project":"acme","pool":"alices"}`,
		"not an object":  `"alices"`,
	} {
		t.Run(name, func(t *testing.T) {
			db := newFakeDB()
			db.nodes[DesignationPath(personPrincipal("1041"))] = json.RawMessage(raw)
			_, found, err := ReadDesignation(ctx, db, personPrincipal("1041"))
			if found || err == nil {
				t.Fatalf("found=%v err=%v, want the record refused", found, err)
			}
			if strings.Contains(err.Error(), "alices") {
				t.Fatalf("the refusal %q carries the record's own text", err)
			}
		})
	}
}

// TestADesignationThisCodeWillNotWrite: a designation with no principal, no
// project or no pool is refused by the writer rather than written as a record
// the reader would refuse.
func TestADesignationThisCodeWillNotWrite(t *testing.T) {
	db := newFakeDB()
	for name, d := range map[string]routing.Designation{
		"no principal": {Project: "acme", Pool: "alices"},
		"no project":   {Principal: personPrincipal("1041"), Pool: "alices"},
		"no pool":      {Principal: personPrincipal("1041"), Project: "acme"},
		"no kind":      {Principal: routing.Principal{ID: "1041"}, Project: "acme", Pool: "alices"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := WriteDesignation(ctx, db, d); !errors.Is(err, ErrDesignation) {
				t.Fatalf("writing %+v = %v, want it refused", d, err)
			}
		})
	}
}
