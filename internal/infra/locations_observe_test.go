package infra

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// The installation these tests observe, and its Firebase project: two ids, as
// an installation that names a separate Firebase project has (D7). The log
// bucket, the secret containers and the runs bucket are read in the GCP
// project, and the two Firebase things in the Firebase project.
const (
	observeProject = "proj-1234"
	observeFP      = "obs-1234"
	observeBucket  = "aurora-runs"
	observeLogs    = "aurora-logs"
)

// observeCloud is every fake ObserveLocations reads: the discovery cloud's,
// plus the Firebase project's Realtime Database management API and its
// Firestore database, and the clients pointed at them.
type observeCloud struct {
	*cloud
	fbdb *gcpfake.FirebaseDB
	fs   *gcpfake.Firestore
	fsc  *firestore.Client
}

func newObserveCloud(t *testing.T) *observeCloud {
	t.Helper()
	f := &observeCloud{cloud: newCloud(t), fbdb: gcpfake.NewFirebaseDB(t), fs: gcpfake.NewFirestore(t)}
	e := f.endpoints()
	e.FirebaseDatabase = f.fbdb.URL + "/"
	c, err := NewClients(context.Background(), f.options(nil), e)
	if err != nil {
		t.Fatal(err)
	}
	f.c = c
	fsc, err := firestore.New(f.fs.URL, observeFP, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	f.fsc = fsc
	return f
}

func (f *observeCloud) inputs() ObserveInputs {
	return ObserveInputs{
		Clients:   f.c,
		Firestore: f.fsc,
		Spec: InstallationSpec{Project: observeProject, FugaroProject: "aurora", Region: "us-east5",
			RunsBucket: observeBucket, Names: InstallationNames{Log: LogNames{Bucket: observeLogs}}},
		FirebaseProject: observeFP,
	}
}

// beforeThisWork seeds the fakes as an installation made before this work is: a
// Firestore database in us-east5, the default Realtime Database instance in
// us-central1, automatic secret containers and the log bucket in `global`. Its
// runs bucket is in its region, the one thing the mapping does not change.
func (f *observeCloud) beforeThisWork(t *testing.T) {
	t.Helper()
	f.gcs.AddBucket(observeBucket, testProjectNumber, managed)
	f.gcs.SetBucketLocation(observeBucket, "US-EAST5")
	f.fbdb.AddInstanceFull(observeFP, "us-central1", observeFP+"-default-rtdb", "DEFAULT_DATABASE", "ACTIVE",
		"https://"+observeFP+"-default-rtdb.firebaseio.com")
	f.logs.AddBucket(observeProject, "global", observeLogs, "ACTIVE", "Fugaro run logs")
	f.sm.Seed("fugaro-github-app", managed, nil)
}

// regionalContainer creates a container with user-managed replication in
// region, as one created from now on is, carrying labels: the fake keeps the
// policy of the create that made it and answers it back on the listing. A nil
// labels map makes one that is not Fugaro's at all, which is what a project's
// own containers are.
func (f *observeCloud) regionalContainer(t *testing.T, id, region string, labels map[string]string) {
	t.Helper()
	svc, err := secretmanager.NewService(context.Background(), option.WithoutAuthentication(), option.WithEndpoint(f.sm.URL+"/"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Projects.Secrets.Create("projects/"+observeProject, &secretmanager.Secret{
		Labels: labels,
		Replication: &secretmanager.Replication{UserManaged: &secretmanager.UserManaged{
			Replicas: []*secretmanager.Replica{{Location: region}}}}},
	).SecretId(id).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("creating container %s in %s: %v", id, region, err)
	}
}

func TestWhatAnInstallationFromBeforeThisWorkHolds(t *testing.T) {
	f := newObserveCloud(t)
	f.beforeThisWork(t)
	got, err := ObserveLocations(context.Background(), f.inputs())
	if err != nil {
		t.Fatal(err)
	}
	// Every answer is what the cloud holds and none is the mapping's: the
	// installation is in us-east5, and its secrets are automatic and its log
	// bucket global.
	want := Observed{
		Firestore:  ObservedLocation{Location: "us-east5"},
		Database:   ObservedLocation{Location: "us-central1"},
		Secrets:    ObservedSecrets{Replication: ReplicationAutomatic},
		LogBucket:  ObservedLocation{Location: "global"},
		RunsBucket: ObservedLocation{Location: "us-east5"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("observed %#v, want %#v", got, want)
	}
}

func TestAMixOfSecretsIsReportedAsMixed(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		create func(t *testing.T, f *observeCloud)
		want   ObservedSecrets
	}{
		{"every container automatic", func(t *testing.T, f *observeCloud) {
			f.sm.Seed("fugaro-github-app", managed, nil)
			f.sm.Seed("fugaro-bitbucket-token", managed, nil)
		}, ObservedSecrets{Replication: ReplicationAutomatic}},
		{"every container regional", func(t *testing.T, f *observeCloud) {
			f.regionalContainer(t, "fugaro-github-app", "europe-west1", managed)
			f.regionalContainer(t, "fugaro-bitbucket-token", "us-east5", managed)
		}, ObservedSecrets{Replication: ReplicationRegional, Regions: []string{"europe-west1", "us-east5"}}},
		{"a new container beside an old one", func(t *testing.T, f *observeCloud) {
			f.sm.Seed("fugaro-github-app", managed, nil)
			f.regionalContainer(t, "fugaro-bitbucket-token", "europe-west1", managed)
		}, ObservedSecrets{Replication: ReplicationMixed, Regions: []string{"europe-west1"}}},
		// The listing filters on `fugaro=managed`, which is what keeps the
		// answer to the installation's own containers: a project's own secret,
		// replicated where its owner chose, is neither Fugaro's nor a reason to
		// call the installation's containers regional.
		{"a container that is not ours", func(t *testing.T, f *observeCloud) {
			f.sm.Seed("fugaro-github-app", managed, nil)
			f.regionalContainer(t, "the-owners-own", "europe-west1", nil)
		}, ObservedSecrets{Replication: ReplicationAutomatic}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newObserveCloud(t)
			tc.create(t, f)
			got, err := ObserveLocations(ctx, f.inputs())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Secrets, tc.want) {
				t.Errorf("secrets %#v, want %#v", got.Secrets, tc.want)
			}
		})
	}
}

func TestNothingIsReportedForWhatDoesNotExist(t *testing.T) {
	f := newObserveCloud(t)
	// A project holding none of the five: the Firestore fake's own database is
	// removed, and nothing else was ever made.
	f.fs.RemoveDatabase()
	got, err := ObserveLocations(context.Background(), f.inputs())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, Observed{}) {
		t.Errorf("observed %#v, want nothing at all", got)
	}
}

func TestAnUnreadableThingIsNamedNotGuessed(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		thing      string
		refuse     func(f *observeCloud)
		permission string
		where      func(Observed) (unreadable, location string)
	}{
		{"the Firestore database",
			func(f *observeCloud) {
				f.fs.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied")
			},
			"datastore.databases.get",
			func(o Observed) (string, string) { return o.Firestore.Unreadable, o.Firestore.Location }},
		{"the Realtime Database instance",
			func(f *observeCloud) {
				f.fbdb.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied")
			},
			"firebasedatabase.instances.list",
			func(o Observed) (string, string) { return o.Database.Unreadable, o.Database.Location }},
		{"the secret containers",
			func(f *observeCloud) {
				f.sm.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied")
			},
			"secretmanager.secrets.list",
			func(o Observed) (string, string) { return o.Secrets.Unreadable, o.Secrets.Replication }},
		{"the log bucket",
			func(f *observeCloud) {
				f.logs.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied")
			},
			"logging.buckets.list",
			func(o Observed) (string, string) { return o.LogBucket.Unreadable, o.LogBucket.Location }},
		{"the runs bucket",
			func(f *observeCloud) {
				f.gcs.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied")
			},
			"storage.buckets.get",
			func(o Observed) (string, string) { return o.RunsBucket.Unreadable, o.RunsBucket.Location }},
	} {
		t.Run(tc.thing, func(t *testing.T) {
			f := newObserveCloud(t)
			// Everything exists, so an answer that guessed would have
			// something to guess with.
			f.beforeThisWork(t)
			tc.refuse(f)
			got, err := ObserveLocations(ctx, f.inputs())
			if err != nil {
				t.Fatal(err)
			}
			unreadable, location := tc.where(got)
			if unreadable != tc.permission {
				t.Errorf("%s is called unreadable %q, want the permission %q", tc.thing, unreadable, tc.permission)
			}
			if location != "" {
				t.Errorf("%s is unreadable and a location was reported anyway (%q): that is a guess", tc.thing, location)
			}
			if n := unreadables(got); n != 1 {
				t.Errorf("%d things are reported unreadable, want the one: %#v", n, got)
			}
		})
	}
}

// unreadables counts the things an answer names a permission for.
func unreadables(o Observed) int {
	n := 0
	for _, l := range []ObservedLocation{o.Firestore, o.Database, o.LogBucket, o.RunsBucket} {
		if l.Unreadable != "" {
			n++
		}
	}
	if o.Secrets.Unreadable != "" {
		n++
	}
	return n
}
