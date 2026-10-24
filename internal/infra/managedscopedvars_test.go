package infra

import (
	"encoding/json"
	"strings"
	"testing"
)

// managed_deployer_scoped is the key that creates the managed deployer's seven
// scoped roles and their seven conditioned bindings, and
// CarriedManagedDeployerScoped is how every path that re-serializes the
// installation root's tfvars keeps it. It is the same shape of carry as
// CarriedCredentialsService: a read-modify-write of ONE key, taken from the
// documents the workdir holds, so a document rebuilt from a spec — which never
// carries the key — does not plan the fence's destruction.

// The key is carried forward as it was found, present or absent, and a value
// the caller decided for itself stands: InstallationVars writes the key only
// when the spec's field is set, and what sets it is the project's own creation
// fact (installRoot and --forget), which is the authority on whether the
// installation has a fence.
//
// Mutation (verified, run and restored): drop the "the caller's own decision
// stands" early return — a document whose own value is false is overwritten
// with the carried true, which is the direction that invents a fence the run's
// own read says is not there.
func TestCarriedManagedDeployerScopedCarriesExactlyTheOneKey(t *testing.T) {
	const scoped = `{"project": "proj-1234", "region": "us-east5", "managed_deployer_scoped": true}`
	const fresh = `{"project": "proj-1234", "region": "us-east5"}`

	got, err := CarriedManagedDeployerScoped([]byte(fresh), []byte(scoped))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("the result is not a tfvars document: %v\n%s", err, got)
	}
	if doc["managed_deployer_scoped"] != true {
		t.Errorf("managed_deployer_scoped = %v, want the key carried forward as true\n%s", doc["managed_deployer_scoped"], got)
	}
	var want map[string]any
	if err := json.Unmarshal([]byte(fresh), &want); err != nil {
		t.Fatal(err)
	}
	delete(doc, "managed_deployer_scoped")
	if !mapsEqual(want, doc) {
		t.Errorf("the carried document changed beyond the one key:\nwant %v\ngot  %v", want, doc)
	}

	// A document that carries its own value is left byte for byte: this is the
	// plan-making paths' spec, whose value came from the creation fact.
	own := []byte(`{"project": "proj-1234", "managed_deployer_scoped": true}`)
	if got2, err := CarriedManagedDeployerScoped(own, []byte(fresh)); err != nil || string(got2) != string(own) {
		t.Errorf("a document with its own key = %s, %v, want it unchanged", got2, err)
	}
	// A run whose read answered "not managed" is that direction and not a key
	// to overwrite: false is a decision too.
	off := []byte(`{"project": "proj-1234", "managed_deployer_scoped": false}`)
	if got2, err := CarriedManagedDeployerScoped(off, []byte(scoped)); err != nil || string(got2) != string(off) {
		t.Errorf("a document whose own key is false = %s, %v, want it unchanged: the run's own read decides", got2, err)
	}

	// No key to carry — an installation that never used managed projects, and a
	// workdir with no tfvars at all (nil: a first run) — invents nothing.
	for name, prev := range map[string][]byte{"no key in the previous document": []byte(fresh), "no previous document": nil} {
		got2, err := CarriedManagedDeployerScoped([]byte(fresh), prev)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got2) != fresh {
			t.Errorf("%s: the document changed: %s", name, got2)
		}
	}
}

// A previous document this program cannot read is an error, not "nothing to
// carry": the direction that fails closed is the one that refuses to write a
// document which drops the fence.
//
// Mutation (verified, run and restored): return vars on a decode failure: this
// test fails.
func TestCarriedManagedDeployerScopedRefusesUnreadablePreviousVars(t *testing.T) {
	fresh := []byte(`{"project": "proj-1234"}`)
	for _, prev := range [][]byte{[]byte("not json"), []byte(""), []byte("[1,2]")} {
		if _, err := CarriedManagedDeployerScoped(fresh, prev); err == nil {
			t.Errorf("CarriedManagedDeployerScoped(fresh, %q) = nil error, want a refusal", prev)
		}
	}
}

// ManagedDeployerScopedRecorded is the read of the same key, and it is the
// installation's own record of the fence rather than the project's: what
// `fugaro update --forget` refuses an installation by when the project's marks
// no longer admit to it (internal/cli's installationFenceRecorded). True is the
// key present and true, which is the shape the plan writes (present) and the
// carry keeps; a key absent, a value that is not a bool, and a workdir with no
// document at all (nil: a first run) are all "the fence is not recorded", and
// the write direction is what would put the key there.
//
// A document this program cannot read is an error and not a "no": a tfvars it
// cannot parse is not evidence that the installation has no fence, and reading
// it as one would let the rollback past the refusal. The sentence names the
// file, because that is what the operator has to look at.
//
// Mutation (verified, run and restored): return false, nil on a decode failure
// (the git-history shape of a read that answers "not recorded" whenever it
// cannot tell): the last case below fails.
func TestManagedDeployerScopedRecordedReadsTheKeyAndRefusesAnUnreadableDocument(t *testing.T) {
	for name, tc := range map[string]struct {
		vars []byte
		want bool
	}{
		"the key true":            {[]byte(`{"project": "proj-1234", "managed_deployer_scoped": true}`), true},
		"the key false":           {[]byte(`{"project": "proj-1234", "managed_deployer_scoped": false}`), false},
		"no key":                  {[]byte(`{"project": "proj-1234", "region": "us-east5"}`), false},
		"the key is not a bool":   {[]byte(`{"managed_deployer_scoped": "true"}`), false},
		"the key is null":         {[]byte(`{"managed_deployer_scoped": null}`), false},
		"no document at all":      {nil, false},
		"a document with nothing": {[]byte(`{}`), false},
	} {
		got, err := ManagedDeployerScopedRecorded(tc.vars)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != tc.want {
			t.Errorf("%s: ManagedDeployerScopedRecorded(%s) = %v, want %v", name, tc.vars, got, tc.want)
		}
	}
	for _, vars := range [][]byte{[]byte("not json"), []byte(""), []byte("[1,2]"), []byte(`{"managed_deployer_scoped": true`)} {
		got, err := ManagedDeployerScopedRecorded(vars)
		if err == nil {
			t.Errorf("ManagedDeployerScopedRecorded(%q) = %v, nil error, want a refusal: a document this program cannot read is not one without a fence", vars, got)
			continue
		}
		if !strings.Contains(err.Error(), VarsFile) {
			t.Errorf("ManagedDeployerScopedRecorded(%q) refused with %q, want the file named (%s)", vars, err, VarsFile)
		}
	}
}
