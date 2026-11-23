package infra

import (
	"context"
	"strings"
	"testing"
)

// Adopting an existing project (the owner's ruling of 2026-11-22): the two
// primitives the read that decides and the write that records it are made of.
// Both are about a project this program did not create, so both are made to
// say only what they can prove — a bucket of the project's own number, and a
// label written once.

func TestBucketInProject(t *testing.T) {
	const name = "fugaro-runs-proj-1234"
	for _, tc := range []struct {
		what  string
		seed  func(f *cloud)
		want  bool
		fails bool
	}{
		{what: "the project's own bucket", seed: func(f *cloud) { f.gcs.AddBucket(name, testProjectNumber, managed) }, want: true},
		{what: "another project's bucket under the same name", seed: func(f *cloud) { f.gcs.AddBucket(name, 999, managed) }},
		{what: "no such bucket", seed: func(f *cloud) {}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			f := newCloud(t)
			tc.seed(f)
			got, err := BucketInProject(context.Background(), f.c, name, testProjectNumber)
			if err != nil || got != tc.want {
				t.Fatalf("BucketInProject = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	// A bucket the caller may not read is an error, never "not there": the
	// caller refuses on the second and adopts on the first, and the two
	// answers must not be one (init's footprint).
	f := newCloud(t)
	f.gcs.AddBucket(name, testProjectNumber, managed)
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BucketInProject(dead, f.c, name, testProjectNumber); err == nil {
		t.Error("a bucket whose read failed was reported absent")
	}
	// Nothing to ask, and no client to ask with: both are "not there" rather
	// than a call to Google.
	if got, err := BucketInProject(context.Background(), f.c, "", testProjectNumber); got || err != nil {
		t.Errorf("no bucket name: %v, %v", got, err)
	}
	if got, err := BucketInProject(context.Background(), f.c, name, 0); got || err != nil {
		t.Errorf("no project number: %v, %v", got, err)
	}
	if got, err := BucketInProject(context.Background(), nil, name, testProjectNumber); got || err != nil {
		t.Errorf("no clients: %v, %v", got, err)
	}
}

func TestWriteAdoptedLabel(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.crm.SetLabels("proj-1234", map[string]string{"team": "platform"})
	f.crm.SetDisplayName("proj-1234", "Aurora")
	if err := WriteAdoptedLabel(ctx, f.c, "proj-1234"); err != nil {
		t.Fatal(err)
	}
	got := f.crm.Labels("proj-1234")
	if got[CreatedLabelKey] != AdoptedLabelValue || got["team"] != "platform" {
		t.Errorf("labels = %v, want %s=%s and the project's own label kept", got, CreatedLabelKey, AdoptedLabelValue)
	}
	if name := f.crm.DisplayName("proj-1234"); name != "Aurora" {
		t.Errorf("display name = %q: projects.update replaces the project's editable state, so the write keeps the name it read", name)
	}

	// The label states the project's origin and is written once: a project
	// that carries it — created by an earlier run, or adopted by one — is not
	// labelled again, and its value is left as it is.
	f = newCloud(t)
	f.crm.SetLabels("proj-1234", map[string]string{CreatedLabelKey: CreatedLabelValue})
	err := WriteAdoptedLabel(ctx, f.c, "proj-1234")
	if err == nil || !strings.Contains(err.Error(), "already carries "+CreatedLabelKey+"="+CreatedLabelValue) {
		t.Errorf("err = %v, want a refusal naming the label it carries", err)
	}
	if got := f.crm.Labels("proj-1234")[CreatedLabelKey]; got != CreatedLabelValue {
		t.Errorf("%s = %q after the refused write, want %q", CreatedLabelKey, got, CreatedLabelValue)
	}

	// Nothing to write, and no client to write with.
	if err := WriteAdoptedLabel(ctx, f.c, ""); err == nil {
		t.Error("an empty project name was accepted")
	}
	if err := WriteAdoptedLabel(ctx, nil, "proj-1234"); err == nil {
		t.Error("no clients was accepted")
	}
	if err := WriteAdoptedLabel(ctx, &Clients{}, "proj-1234"); err == nil {
		t.Error("no Resource Manager client was accepted")
	}
}
