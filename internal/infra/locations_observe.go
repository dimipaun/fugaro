package infra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	firebasedatabase "google.golang.org/api/firebasedatabase/v1beta"
	"google.golang.org/api/googleapi"
	logging "google.golang.org/api/logging/v2"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/firestore"
)

// The permission each of ObserveLocations's five reads needs. Google's refusal
// says PERMISSION_DENIED and nothing else — it names no permission — so an
// unreadable thing is named here by the call's own requirement, and never by
// a guess at where the thing might be.
const (
	permFirestoreDatabase = "datastore.databases.get"
	permDatabaseInstances = "firebasedatabase.instances.list"
	permLogBuckets        = "logging.buckets.list"
	permSecretContainers  = "secretmanager.secrets.list"
	permRunsBucket        = "storage.buckets.get"
)

// What the Fugaro-labelled secret containers add up to, in ObservedSecrets
// .Replication. The first is every installation made before the mapping is, the
// second is a new one, and the third is what such an installation becomes as
// containers created from now on are replicated in the region (F9, D3092).
const (
	ReplicationAutomatic = "automatic"
	ReplicationRegional  = "regional"
	ReplicationMixed     = "mixed"
)

// ObservedLocation is one located thing as a read of the cloud found it. The
// zero value is the thing not existing, and the two fields are never both set:
// a thing Fugaro may not read has no location here at all, so nothing built
// from an Observed can read as one.
type ObservedLocation struct {
	// Location is where the thing is, in the cloud's own spelling (a GCS
	// bucket's location is upper case there and is reported lower case here, so
	// it reads as the region it is).
	Location string
	// Unreadable is the permission the thing's read needs, when Fugaro may not
	// make it.
	Unreadable string
}

// ObservedSecrets is what the project's Fugaro-labelled secret containers
// add up to.
type ObservedSecrets struct {
	// Replication is ReplicationAutomatic, ReplicationRegional or
	// ReplicationMixed, and empty when the project holds no Fugaro container.
	Replication string
	// Regions are the replica locations of the user-managed containers,
	// deduplicated and sorted.
	Regions []string
	// Unreadable is the permission the listing needs, when Fugaro may not
	// read the containers.
	Unreadable string
}

// Observed is every located thing of one installation, as a read of the cloud
// found it. It is a record and never an input: nothing plans from it (R5),
// and its one use is the locations record `fugaro update` and `fugaro setup`
// write down (D3087, LP14).
type Observed struct {
	// Firestore is the (default) database of the Firebase project, where the
	// spend history lives.
	Firestore ObservedLocation
	// Database is the project's default Realtime Database instance, where the
	// budget lives.
	Database ObservedLocation
	// Secrets is what the Fugaro-labelled containers' replication adds up to.
	Secrets ObservedSecrets
	// LogBucket is the Fugaro log bucket, which holds each run's output.
	LogBucket ObservedLocation
	// RunsBucket is the runs bucket, whose location is the installation's own.
	RunsBucket ObservedLocation
}

// ObserveInputs are what ObserveLocations reads by.
type ObserveInputs struct {
	// Clients are the reads of the Realtime Database, the log buckets, the
	// secret containers and the runs bucket.
	Clients *Clients
	// Firestore reads the Firebase project's (default) database. No Clients
	// field carries a Firestore client — it is the CLI's own REST client
	// (internal/firestore) — so the caller passes the one it built for this
	// Firebase project.
	Firestore *firestore.Client
	// Spec is the installation's spec: Project holds the runs bucket, the
	// Fugaro log bucket and the secret containers, and RunsBucket and
	// Names.Log.Bucket are the two names looked up.
	Spec InstallationSpec
	// FirebaseProject hosts the Firestore database and the default Realtime
	// Database instance: the installation's own GCP project unless it names a
	// separate Firebase project (D7).
	FirebaseProject string
}

// ObserveLocations reads where an installation's located things are: the
// Firestore database's location, the default Realtime Database instance's, the
// Fugaro log bucket's, whether the Fugaro-labelled secret containers are
// automatic, regional or both, and the runs bucket's location.
//
// It makes no write and asks nothing. A thing that does not exist is reported
// as nothing, and a thing Fugaro may not read as unreadable, named by the
// permission the read needs; neither is ever a guess at a location. Anything
// else — an outage, an answer the API should not give — is returned as an
// error, because it is neither of those two facts.
func ObserveLocations(ctx context.Context, in ObserveInputs) (Observed, error) {
	// A read with no client behind it is a caller's mistake and not a fact
	// about the cloud: it is an error, as it is everywhere else that reads
	// through Clients. FirebaseDB is the one a configuration can leave out
	// (endpoints.firebase_database), and it is named.
	if in.Clients == nil || in.Firestore == nil {
		return Observed{}, errors.New("observe locations needs the clients it reads by")
	}
	if in.Clients.FirebaseDB == nil {
		return Observed{}, errors.New("no Realtime Database management client (endpoints.firebase_database is not set)")
	}
	o := &observer{ctx: ctx, in: in}
	for _, read := range []func() error{o.firestoreDatabase, o.databaseInstance, o.secretContainers, o.logBucket, o.runsBucket} {
		if err := read(); err != nil {
			return Observed{}, err
		}
	}
	return o.out, nil
}

// observer is one observation's inputs, its answer so far, and the reads that
// fill it in.
type observer struct {
	ctx context.Context
	in  ObserveInputs
	out Observed
}

// firestoreDatabase reads the Firebase project's (default) database.
func (o *observer) firestoreDatabase() error {
	db, err := o.in.Firestore.GetDatabase(o.ctx)
	switch {
	case errors.Is(err, firestore.ErrNotFound):
		// No database: the mapping answers for one that does not exist (LP3),
		// and so does nothing here.
	case errors.Is(err, firestore.ErrPermission):
		o.out.Firestore.Unreadable = permFirestoreDatabase
	case err != nil:
		return fmt.Errorf("reading the Firestore database of %s: %w", o.in.FirebaseProject, err)
	default:
		o.out.Firestore.Location = db.LocationID
	}
	return nil
}

// databaseInstance reads the Firebase project's default Realtime Database
// instance. The instance's location is in the name it is listed under — a
// location cannot be changed after creation, and the ID carries none — so the
// listing is what answers it, across every location.
func (o *observer) databaseInstance() error {
	parent := "projects/" + o.in.FirebaseProject + "/locations/-"
	err := o.in.Clients.FirebaseDB.Projects.Locations.Instances.List(parent).Context(o.ctx).
		Pages(o.ctx, func(p *firebasedatabase.ListDatabaseInstancesResponse) error {
			for _, i := range p.Instances {
				if i.Type != firebaseDBType {
					continue
				}
				o.out.Database.Location, _ = instanceLocationID(i.Name)
			}
			return nil
		})
	switch {
	case notFound(err):
		// The project itself is gone, so it has no instance.
	case forbidden(err):
		o.out.Database.Unreadable = permDatabaseInstances
	case err != nil:
		return fmt.Errorf("listing the Realtime Database instances of %s: %w", o.in.FirebaseProject, err)
	}
	return nil
}

// secretContainers lists the project's Fugaro-labelled containers and adds up
// their replication. The listing answers each container's replication, so no
// container is read on its own.
func (o *observer) secretContainers() error {
	var automatic, regional bool
	var regions []string
	err := o.in.Clients.Secrets.Projects.Secrets.List("projects/"+o.in.Spec.Project).
		Filter(labelFilter(map[string]string{gcp.LabelManaged: gcp.ManagedValue})).
		Pages(o.ctx, func(p *secretmanager.ListSecretsResponse) error {
			for _, s := range p.Secrets {
				// Automatic is the zero value a container made before the
				// mapping carries, and the API answers one of the two policies
				// for every container and never neither.
				if s.Replication == nil || s.Replication.UserManaged == nil {
					automatic = true
					continue
				}
				regional = true
				for _, r := range s.Replication.UserManaged.Replicas {
					if r.Location != "" {
						regions = append(regions, r.Location)
					}
				}
			}
			return nil
		})
	switch {
	case forbidden(err):
		o.out.Secrets.Unreadable = permSecretContainers
		return nil
	case err != nil:
		return fmt.Errorf("listing the secret containers of %s: %w", o.in.Spec.Project, err)
	}
	slices.Sort(regions)
	o.out.Secrets.Regions = slices.Compact(regions)
	switch {
	case automatic && regional:
		o.out.Secrets.Replication = ReplicationMixed
	case regional:
		o.out.Secrets.Replication = ReplicationRegional
	case automatic:
		o.out.Secrets.Replication = ReplicationAutomatic
	}
	return nil
}

// logBucket reads the installation's own log bucket. Its ID carries no
// location — the failover the mapping gives it is the place's multi-region, or
// `global` — so the buckets are listed across every location and the one
// carrying the installation's bucket ID is the answer.
func (o *observer) logBucket() error {
	id := o.in.Spec.Names.Log.Bucket
	parent := "projects/" + o.in.Spec.Project + "/locations/-"
	err := o.in.Clients.Logging.Projects.Locations.Buckets.List(parent).Context(o.ctx).
		Pages(o.ctx, func(p *logging.ListBucketsResponse) error {
			for _, b := range p.Buckets {
				if loc, bucket := logBucketLocationID(b.Name); bucket == id {
					o.out.LogBucket.Location = loc
				}
			}
			return nil
		})
	switch {
	case notFound(err):
		// The project itself is gone, so it has no bucket.
	case forbidden(err):
		o.out.LogBucket.Unreadable = permLogBuckets
	case err != nil:
		return fmt.Errorf("listing the log buckets of %s: %w", o.in.Spec.Project, err)
	}
	return nil
}

// runsBucket reads the runs bucket's own location. It is the installation's
// region: the installation root creates it in `upper(var.region)`, which is
// why the answer is folded to lower case, the spelling a region is named in
// everywhere else.
func (o *observer) runsBucket() error {
	b, err := o.in.Clients.Storage.Buckets.Get(o.in.Spec.RunsBucket).Context(o.ctx).Do()
	switch {
	case notFound(err):
		// No runs bucket: nothing is reported for what does not exist.
	case forbidden(err):
		o.out.RunsBucket.Unreadable = permRunsBucket
	case err != nil:
		return fmt.Errorf("reading bucket gs://%s: %w", o.in.Spec.RunsBucket, err)
	default:
		o.out.RunsBucket.Location = strings.ToLower(b.Location)
	}
	return nil
}

// forbidden reports whether err is the API's 403: the answer a thing Fugaro
// may not read gets, which it names rather than guesses at.
func forbidden(err error) bool {
	var ae *googleapi.Error
	return errors.As(err, &ae) && ae.Code == http.StatusForbidden
}

// logBucketLocationID splits projects/<p>/locations/<l>/buckets/<id>; both are
// empty when name has another shape.
func logBucketLocationID(name string) (loc, id string) {
	p := strings.Split(name, "/")
	if len(p) != 6 || p[0] != "projects" || p[2] != "locations" || p[4] != "buckets" {
		return "", ""
	}
	return p[3], p[5]
}
