// Package place is the product's table from a region to where a new
// installation's data goes: the two databases, the secret containers and the
// run log bucket (docs/location-preference/reconciliation.md, "The mapping").
//
// It is pure. No cloud call, no project config and no I/O decides an answer,
// so every reader of the table answers the same thing offline, and the lists
// behind it are dated against Google's own pages (ListsReadOn, and
// docs/location-preference/locations.md, which TestTheListsAreDated holds to
// them, list for list).
//
// Nothing that exists is ever given this answer. A database, a secret
// container or a log bucket an installation already has keeps what it has,
// read from the cloud (R5); For is what a new installation gets, and it is
// also what a missing one gets on an older installation (D3089).
package place

import (
	"fmt"
	"slices"
	"strings"
)

// Place is where an installation's region lies: one of the two places Fugaro
// promises, or None for a region in neither (D3081).
type Place string

const (
	// US is the United States: every `us-*` region.
	US Place = "us"
	// EU is the European Union, which is not every `europe-*` region: London
	// (europe-west2) and Zürich (europe-west6) are not in it, and so are in
	// neither place.
	EU Place = "eu"
	// None is a region in neither place. Nothing here refuses one: it works,
	// and every fallback the mapping gives for it is stated before anything
	// is created (D3081). What it does not carry is a promise.
	None Place = "none"
)

// String is the place as a line a person reads, which is what a fallback's
// reason is built from.
func (p Place) String() string {
	switch p {
	case US:
		return "US"
	case EU:
		return "EU"
	default:
		return "neither US nor EU"
	}
}

// outsideEU are the `europe-*` regions that are not in the European Union.
// They are named rather than derived from anything: the union is a political
// fact and no region name carries it, while the two that most obviously look
// European are the two a promise would be wrong about.
var outsideEU = map[string]bool{
	"europe-west2": true, // London, the United Kingdom
	"europe-west6": true, // Zürich, Switzerland
}

// Of is the place region derives (D3080: the region stays the one stored
// setting, and the place is derived from it). It answers by the region's
// prefix rather than from Regions, so a region Google adds tomorrow is in its
// place with no change here; Regions is the dated list of what is known
// today, and the tests walk it.
func Of(region string) Place {
	switch {
	case strings.HasPrefix(region, "us-"):
		return US
	case strings.HasPrefix(region, "europe-") && !outsideEU[region]:
		return EU
	default:
		return None
	}
}

// usDefault and euDefault are the two places' regions when only the place is
// chosen: us-east5, today's default region, and europe-west1, which is also
// the Realtime Database's one EU location, so an EU installation with no
// region typed gets everything in one region (D3086).
const (
	usDefault = "us-east5"
	euDefault = "europe-west1"
)

// DefaultRegion is the region a place gives when only the place is chosen
// (D3086). None has no default: a region in neither place is typed in full,
// which is why the answer is empty.
func DefaultRegion(p Place) string {
	switch p {
	case US:
		return usDefault
	case EU:
		return euDefault
	default:
		return ""
	}
}

// Location is where one of the mapping's rows goes, and the reason when it is
// not the installation's region itself. A reason is a short parenthetical on
// the row's own line — where the thing goes is the line's other column, so the
// reason says why and not where ("no log bucket in us-east5") — and it is empty
// exactly when Where is the region.
type Location struct {
	Where  string
	Reason string
}

// Mapping is what a new installation in one region gets.
type Mapping struct {
	// Region is the installation's own region, the one everything else is
	// derived from.
	Region string
	// Place is Of(Region).
	Place Place

	// Firestore is the run history database's location (D3085). It is the
	// region itself where Firestore offers one, and the place's default
	// region where it does not.
	Firestore Location
	// RealtimeDatabase is the budget's database, the only thing of Fugaro's
	// that Google offers in three locations for the whole world.
	RealtimeDatabase Location
	// Secrets is where every secret container created from now on is
	// replicated (D3082, D3092). It is always the region, user-managed: a
	// container that exists keeps its own replication (R5), and what is new
	// is in the region, on an installation made before this work too.
	Secrets Location
	// LogBucket is the run log bucket's location (D3082): the region itself
	// where Cloud Logging offers a bucket there, else the place's
	// multi-region, else global.
	LogBucket Location
}

// For is the mapping for region: where a new installation's Firestore
// database, Realtime Database, secret containers and run log bucket go, each
// with the reason when it is not the region itself.
//
// It answers for any region, the ones Regions lists and the ones Google adds
// after that: a region in neither place gets the same table, with each
// fallback stated, which is what setup prints before anything is created
// (D3081, R7).
func For(region string) Mapping {
	p := Of(region)
	return Mapping{
		Region:           region,
		Place:            p,
		Firestore:        firestoreFor(region, p),
		RealtimeDatabase: databaseFor(region),
		Secrets:          Location{Where: region},
		LogBucket:        logBucketFor(region, p),
	}
}

// firestoreFor is D3085: the installation's own region where Firestore offers
// a database there, else the place's default region. A region in neither
// place has no place to fall back to, and us-east5 — Fugaro's own default —
// is stated as what it gets.
func firestoreFor(region string, p Place) Location {
	if slices.Contains(FirestoreLocations, region) {
		return Location{Where: region}
	}
	if d := DefaultRegion(p); d != "" {
		return Location{
			Where:  d,
			Reason: fmt.Sprintf("no Firestore in %s; %s is the %s default region", region, d, p),
		}
	}
	return Location{
		Where:  usDefault,
		Reason: fmt.Sprintf("no Firestore in %s, and the region is in neither place", region),
	}
}

// databaseFor is the Realtime Database's row. Google offers it in three
// locations and no other, so the region itself is only ever the answer for
// one of the three; every other region gets the nearest of them, stated.
// Which one is nearest is the product's guess (O4, the owner's to correct);
// for a region of US or EU it never leaves the place, which is what
// TestNoFallbackLeavesItsPlace pins.
func databaseFor(region string) Location {
	where := nearestDatabase(region)
	if where == region {
		return Location{Where: where}
	}
	return Location{
		Where:  where,
		Reason: "nearest Realtime DB region",
	}
}

// nearestDatabase is the nearest of the Realtime Database's three locations
// for every region Fugaro answers for, by continent: the Americas to
// us-central1, Europe, the Middle East and Africa to europe-west1, and
// everything else to asia-southeast1. London's and Zürich's own regions are
// in neither place and still land in europe-west1, which is the nearest of
// the three to both.
func nearestDatabase(region string) string {
	switch {
	case strings.HasPrefix(region, "us-"),
		strings.HasPrefix(region, "northamerica-"),
		strings.HasPrefix(region, "southamerica-"):
		return "us-central1"
	case strings.HasPrefix(region, "europe-"),
		strings.HasPrefix(region, "me-"),
		strings.HasPrefix(region, "africa-"):
		return "europe-west1"
	default:
		return "asia-southeast1"
	}
}

// logBucketFor is the run log bucket's row: the region itself where Cloud
// Logging offers a bucket there, else the place's multi-region (us, eu),
// else global. A region in neither place has no multi-region to fall back to
// and global is what is left, stated.
func logBucketFor(region string, p Place) Location {
	if slices.Contains(LoggingLocations, region) {
		return Location{Where: region}
	}
	switch p {
	case US, EU:
		// The multi-region a US or EU region falls back to is the place's own
		// name: us and eu are exactly Cloud Logging's two multi-regions, and
		// Place's values are those two strings (Place.String is the words a
		// person reads, which is not what a location is).
		return Location{
			Where:  string(p),
			Reason: fmt.Sprintf("no log bucket in %s", region),
		}
	default:
		return Location{
			Where:  "global",
			Reason: fmt.Sprintf("no log bucket in %s, and the region is in neither place", region),
		}
	}
}

// ListsReadOn is the date the three lists below were read from Google's
// pages. It is one date for all three because that is when the table was
// read as a whole; each page carries its own "last updated" date beside it in
// docs/location-preference/locations.md, which a test holds to these lists.
const ListsReadOn = "2026-11-20"

// Regions are the regions Fugaro accepts as an installation's region: Cloud
// Run's locations, from https://cloud.google.com/run/docs/locations (checked
// 2026-11-18, 43 of them). A region outside it is not refused — Of and For
// answer for it, and SchedulerRegion is what has nothing to say (F3).
var Regions = []string{
	"asia-east1", "asia-east2", "asia-northeast1", "asia-northeast2", "asia-northeast3",
	"asia-south1", "asia-south2", "asia-southeast1", "asia-southeast2", "asia-southeast3",
	"australia-southeast1", "australia-southeast2",
	"europe-central2", "europe-north1", "europe-north2", "europe-southwest1",
	"europe-west1", "europe-west2", "europe-west3", "europe-west4", "europe-west6",
	"europe-west8", "europe-west9", "europe-west10", "europe-west12",
	"me-central1", "me-central2", "me-west1",
	"northamerica-northeast1", "northamerica-northeast2", "northamerica-south1",
	"southamerica-east1", "southamerica-west1",
	"us-central1", "us-east1", "us-east4", "us-east5", "us-south1",
	"us-west1", "us-west2", "us-west3", "us-west4", "us-west8",
}

// FirestoreLocations are the locations a native Firestore database can be
// created in, regions only, from
// https://firebase.google.com/docs/firestore/locations (checked 2026-11-18).
// Firestore's multi-regions (nam5, nam7, eur3) are deliberately absent: the
// mapping never chooses one (D3085 — a higher price for a database that is
// not on a run's path), so a region missing from this list falls to a region
// and never to a multi-region.
var FirestoreLocations = []string{
	"asia-east1", "asia-east2", "asia-northeast1", "asia-northeast2", "asia-northeast3",
	"asia-south1", "asia-south2", "asia-southeast1", "asia-southeast2",
	"australia-southeast1", "australia-southeast2",
	"europe-central2", "europe-north1", "europe-north2", "europe-southwest1",
	"europe-west1", "europe-west2", "europe-west3", "europe-west4", "europe-west6",
	"europe-west8", "europe-west9", "europe-west10", "europe-west12",
	"me-central1", "me-central2", "me-west1",
	"northamerica-northeast1", "northamerica-northeast2", "northamerica-south1",
	"southamerica-east1", "southamerica-west1",
	"us-central1", "us-east1", "us-east4", "us-east5", "us-south1",
	"us-west1", "us-west2", "us-west3", "us-west4",
}

// LoggingLocations are the regions Cloud Logging offers a log bucket in,
// from https://cloud.google.com/logging/docs/region-support (checked
// 2026-11-20). The buckets Cloud Logging offers beside them — global and the
// multi-regions us and eu — are the mapping's fallbacks and are not listed
// here: a region is what an installation's own region is compared against.
var LoggingLocations = []string{
	"asia-east1", "asia-east2", "asia-northeast1", "asia-northeast2", "asia-northeast3",
	"asia-south1", "asia-south2", "asia-southeast1", "asia-southeast2",
	"australia-southeast1", "australia-southeast2",
	"europe-central2", "europe-north1",
	"europe-west1", "europe-west2", "europe-west3", "europe-west4", "europe-west6",
	"europe-west8", "europe-west9", "europe-west12",
	"me-central1", "me-west1",
	"northamerica-northeast1", "northamerica-northeast2",
	"southamerica-east1",
	"us-central1", "us-east1", "us-east4", "us-south1",
	"us-west1", "us-west2", "us-west3", "us-west4",
}
