package gcpfake

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"google.golang.org/api/apikeys/v2"
	firebasedatabase "google.golang.org/api/firebasedatabase/v1beta"
	"google.golang.org/api/googleapi"
	iam "google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

func isNotFound(err error) bool {
	var ge *googleapi.Error
	return errors.As(err, &ge) && ge.Code == http.StatusNotFound
}

func TestAPIKeysFakeServesAKeyAndNotFound(t *testing.T) {
	f := NewAPIKeys(t)
	f.AddKey("fp-1234", "fugaro-web", "Fugaro run sign-in", []string{"identitytoolkit.googleapis.com", "securetoken.googleapis.com"}, false, false)
	f.AddKey("fp-1234", "odd", "Odd", nil, true, true)
	svc, _ := apikeys.NewService(context.Background(), option.WithEndpoint(f.URL), option.WithoutAuthentication())
	k, err := svc.Projects.Locations.Keys.Get("projects/fp-1234/locations/global/keys/fugaro-web").Do()
	if err != nil || k.DisplayName != "Fugaro run sign-in" || len(k.Restrictions.ApiTargets) != 2 || k.DeleteTime != "" {
		t.Fatalf("%v %+v", err, k)
	}
	if _, err := svc.Projects.Locations.Keys.Get("projects/fp-1234/locations/global/keys/other").Do(); !isNotFound(err) {
		t.Fatalf("want 404, got %v", err)
	}
	k, err = svc.Projects.Locations.Keys.Get("projects/fp-1234/locations/global/keys/odd").Do()
	if err != nil || k.DeleteTime == "" || k.Restrictions == nil || k.Restrictions.BrowserKeyRestrictions == nil {
		t.Fatalf("%v %+v", err, k)
	}
}

func TestFirebaseDBFakeInstanceAttributes(t *testing.T) {
	f := NewFirebaseDB(t)
	f.AddInstance("fp-1234", "https://a.example")
	f.AddInstanceFull("fp-1234", "europe-west1", "fp-1234-rtdb", "USER_DATABASE", "DISABLED", "https://b.example")
	svc, _ := firebasedatabase.NewService(context.Background(), option.WithEndpoint(f.URL), option.WithoutAuthentication())
	l, err := svc.Projects.Locations.Instances.List("projects/fp-1234/locations/-").Do()
	if err != nil || len(l.Instances) != 2 {
		t.Fatalf("%v %+v", err, l)
	}
	if i := l.Instances[0]; i.Name != "projects/fp-1234/locations/us-central1/instances/dba" || i.Type != "DEFAULT_DATABASE" || i.State != "ACTIVE" {
		t.Errorf("default instance: %+v", i)
	}
	g, err := svc.Projects.Locations.Instances.Get("projects/fp-1234/locations/europe-west1/instances/fp-1234-rtdb").Do()
	if err != nil || g.Type != "USER_DATABASE" || g.State != "DISABLED" || g.DatabaseUrl != "https://b.example" {
		t.Fatalf("%v %+v", err, g)
	}
	if _, err := svc.Projects.Locations.Instances.Get("projects/fp-1234/locations/europe-west1/instances/nope").Do(); !isNotFound(err) {
		t.Fatalf("want 404, got %v", err)
	}
}

func TestIAMFakeAccountAttributesAndUserKeys(t *testing.T) {
	f := NewIAM(t)
	f.AddServiceAccountFull("fp-1234", "s@fp-1234.iam.gserviceaccount.com", "Signer", "mints tokens", true)
	f.AddServiceAccount("fp-1234", "o@fp-1234.iam.gserviceaccount.com", "Other")
	f.AddUserKey("fp-1234", "s@fp-1234.iam.gserviceaccount.com")
	svc, _ := iam.NewService(context.Background(), option.WithEndpoint(f.URL+"/"), option.WithoutAuthentication())
	a, err := svc.Projects.ServiceAccounts.Get("projects/fp-1234/serviceAccounts/s@fp-1234.iam.gserviceaccount.com").Do()
	if err != nil || a.Description != "mints tokens" || !a.Disabled || a.DisplayName != "Signer" {
		t.Fatalf("%v %+v", err, a)
	}
	o, err := svc.Projects.ServiceAccounts.Get("projects/fp-1234/serviceAccounts/o@fp-1234.iam.gserviceaccount.com").Do()
	if err != nil || o.Disabled || o.Description != "" {
		t.Fatalf("%v %+v", err, o)
	}
	user, err := svc.Projects.ServiceAccounts.Keys.List("projects/fp-1234/serviceAccounts/s@fp-1234.iam.gserviceaccount.com").KeyTypes("USER_MANAGED").Do()
	if err != nil || len(user.Keys) != 1 || user.Keys[0].KeyType != "USER_MANAGED" {
		t.Fatalf("%v %+v", err, user)
	}
	sys, err := svc.Projects.ServiceAccounts.Keys.List("projects/fp-1234/serviceAccounts/s@fp-1234.iam.gserviceaccount.com").KeyTypes("SYSTEM_MANAGED").Do()
	if err != nil || len(sys.Keys) != 0 {
		t.Fatalf("%v %+v", err, sys)
	}
	none, err := svc.Projects.ServiceAccounts.Keys.List("projects/fp-1234/serviceAccounts/o@fp-1234.iam.gserviceaccount.com").KeyTypes("USER_MANAGED").Do()
	if err != nil || len(none.Keys) != 0 {
		t.Fatalf("%v %+v", err, none)
	}
	if _, err := svc.Projects.ServiceAccounts.Keys.List("projects/fp-1234/serviceAccounts/x@fp-1234.iam.gserviceaccount.com").Do(); !isNotFound(err) {
		t.Fatalf("want 404, got %v", err)
	}
}
