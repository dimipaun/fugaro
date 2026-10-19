package gcpfake

import (
	"context"
	"maps"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/option"
)

// TestProjectUpdateReplacesTheProjectsEditableFields pins the fake's
// projects.update to the real v1 API's semantics, because a writer that gets
// them wrong damages a real project and no test would notice: the request IS
// the project's editable state — the display name (the v1 resource's `name`
// field) and the labels — so a body that leaves the display name out takes it
// off the project (the generated client's `name,omitempty` is what omits it),
// and a body that leaves a label out drops that label. gcloud and Terraform
// both read the project, change it and send the whole object back, which is
// why they do not clear anything.
func TestProjectUpdateReplacesTheProjectsEditableFields(t *testing.T) {
	ctx := context.Background()
	f := NewCRM(t)
	f.AddProject("p", 1)
	f.SetDisplayName("p", "Belong production")
	f.SetLabels("p", map[string]string{"environment": "prod", "team": "infra"})
	svc, err := crm.NewService(ctx, option.WithEndpoint(f.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}

	// A body holding only labels — what a caller sends when it builds the
	// project object rather than sending the one it read.
	if _, err := svc.Projects.Update("p", &crm.Project{Labels: map[string]string{"environment": "prod"}}).Context(ctx).Do(); err != nil {
		t.Fatal(err)
	}
	if got := f.DisplayName("p"); got != "" {
		t.Errorf("a labels-only update left the display name %q, want the replacement the API makes (nothing at all)", got)
	}
	p, err := svc.Projects.Get("p").Context(ctx).Do()
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "" {
		t.Errorf("the project reads back with display name %q, want none", p.Name)
	}
	if got, want := f.Labels("p"), map[string]string{"environment": "prod"}; !maps.Equal(got, want) {
		t.Errorf("the project carries %v, want %v: the update replaces the whole label map", got, want)
	}

	// A body carrying the fields, which is what gcloud and Terraform send.
	f.SetDisplayName("p", "Belong production")
	if _, err := svc.Projects.Update("p", &crm.Project{Name: "Belong production", Labels: map[string]string{"environment": "dev"}}).Context(ctx).Do(); err != nil {
		t.Fatal(err)
	}
	if got := f.DisplayName("p"); got != "Belong production" {
		t.Errorf("the display name is %q, want it kept", got)
	}
	if got, want := f.Labels("p"), map[string]string{"environment": "dev"}; !maps.Equal(got, want) {
		t.Errorf("the project carries %v, want %v", got, want)
	}
}

// TestProjectDisplayNameDefaultsToItsID: a project a test added with
// AddProject answers its own ID as its display name until a test gives it one
// of its own, which is what the fake answered before it knew about display
// names at all.
func TestProjectDisplayNameDefaultsToItsID(t *testing.T) {
	f := NewCRM(t)
	f.AddProject("p", 1)
	if got := f.DisplayName("p"); got != "p" {
		t.Errorf("DisplayName = %q, want the project's ID", got)
	}
	f.SetDisplayName("p", "Belong production")
	if got := f.DisplayName("p"); got != "Belong production" {
		t.Errorf("DisplayName = %q, want the name the test set", got)
	}
}
