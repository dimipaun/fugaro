package cli

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/place"
)

// doctor_locations.go is doctor's own row for where the installation's data
// is: the region the installation is in, the place that region lies in, and
// where each thing this machine's locations record knows about really is (LP12
// of docs/location-preference/, D3087, D3091).
//
// It reads the local config and nothing else: no cloud, no credential. The
// record it prints is written from what the cloud holds by `fugaro setup` and
// `fugaro update` (LP14, docs/location-preference/record.md), and this row is
// its only reader — the reason the record is kept at all. The record is never
// an input to anything (R5), so where it and the cloud disagree the cloud wins
// and the next converge corrects the file: a stale, wrong or hand-edited block
// costs a reader nothing but that correction, which is why nothing here
// refuses one.
//
// A thing outside the installation's place is information and never a failure
// (D3091). A database an installation adopted where it was found keeps its
// location — nothing moves it — so doctor's job is to say where it is, not to
// complain about what cannot change. The row is therefore ok whatever it
// finds, at severity "info", which also files it as a measurement: --all and
// --json carry it, no view counts it as a finding, and it fails doctor neither
// with nor without --strict.
//
// The record is this machine's own and is never published (F17), so on the
// shared-config fallback it is always absent and the row says so.

// doctorLocationsCheckID is doctor's row for where the installation's data is.
const doctorLocationsCheckID = "locations"

// doctorLocationsCheck is that row: nil for a run that selected no config at
// all, and one check for every installation. A record this machine has not
// written yet — which is every installation until LP14 writes one — leaves the
// row as the region and its place alone: nothing here knows where the data is,
// and the rest of the row is where each *recorded* thing is.
func doctorLocationsCheck(lc *localcfg.Config) *doctorCheck {
	if lc == nil {
		return nil
	}
	p := place.Of(lc.Region)
	problem := fmt.Sprintf("the installation is in %s, %s", lc.Region, placeClause(p))
	if things := recordedThings(lc, p); len(things) == 0 {
		problem += "; this machine has no locations record yet, so where its data is is not known here"
	} else {
		problem += "; the locations record says " + strings.Join(things, ", ")
	}
	return &doctorCheck{ID: doctorLocationsCheckID, OK: true, Severity: "info", Problem: problem}
}

// recordedThings is where each thing this machine's record knows about is, in
// the order the record's own document lists them. A member the record leaves
// out is a thing not known here — no converge has read it, or it does not
// exist at all, and the two are one statement on purpose (localcfg.Locations)
// — so it is not a thing to print.
func recordedThings(lc *localcfg.Config, p place.Place) []string {
	l := lc.Locations
	if l == nil {
		return nil
	}
	var out []string
	if l.Firestore != "" {
		out = append(out, "its Firestore database is in "+locatedWords(l.Firestore, p))
	}
	if l.RTDB != "" {
		out = append(out, "its Realtime Database is in "+locatedWords(l.RTDB, p))
	}
	if !l.Secrets.IsZero() {
		out = append(out, "its secret containers are "+secretsWords(l.Secrets, p))
	}
	if l.LogBucket != "" {
		out = append(out, "its run log bucket is in "+locatedWords(l.LogBucket, p))
	}
	return out
}

// locatedWords is one thing's location as the row prints it, with the place it
// lies in named when that is not the installation's: the one thing the row
// adds to the record's own words (D3091). A location in no place — global, or
// a name this Fugaro does not know — is printed as it is, with nothing claimed
// about it.
func locatedWords(loc string, p place.Place) string {
	if q := locationPlace(loc); p != place.None && q != place.None && q != p {
		return loc + outsideWords(p)
	}
	return loc
}

// outsideWords says a thing is outside the installation's place. p is never
// None where it is called: a region in neither place promises nothing for a
// thing to be outside of (D3081), and a row that marked every thing there
// would be saying nothing.
func outsideWords(p place.Place) string {
	if p == place.EU {
		return " (outside the EU)"
	}
	return " (outside the US)"
}

// locationPlace is the place a location lies in. place.Of answers for a region
// and for nothing else, and the record can hold names that are not regions and
// are still in a place: a Firestore database adopted as nam5 is in the US and
// one as eur3 is in the EU, and the run log bucket's us and eu are the two
// multi-regions the mapping falls back to. It answers by the prefix, as
// place.Of does, so a multi-region Google adds tomorrow is in its place with
// no change here. global is in no place, and neither is anything else this
// Fugaro does not know.
func locationPlace(loc string) place.Place {
	switch {
	case loc == "us" || strings.HasPrefix(loc, "nam"):
		return place.US
	case loc == "eu" || strings.HasPrefix(loc, "eur"):
		return place.EU
	}
	return place.Of(loc)
}

// placeClause is the installation's place as a clause in the row's sentence.
// place.Place's own String answers a table cell ("US", "neither US nor EU").
func placeClause(p place.Place) string {
	switch p {
	case place.US:
		return "in the US"
	case place.EU:
		return "in the EU"
	default:
		return "in neither the US nor the EU"
	}
}

// automaticGloss is what Google's automatic replication means for a reader of
// the row, in the words the record's document uses: it names no region, and
// today it lands in the United States.
const automaticGloss = "Google's automatic replication, which today means the United States"

// secretsWords is where the installation's own Secret Manager containers are,
// in the record's three spellings (localcfg.SecretsReplication): the regions
// the containers are replicated in, Google's automatic replication, or both at
// once, which is what an installation made before this work becomes as new
// containers are created in the region (D3092).
func secretsWords(r localcfg.SecretsReplication, p place.Place) string {
	if len(r.Regions) > 0 {
		where := make([]string, 0, len(r.Regions))
		for _, region := range r.Regions {
			where = append(where, locatedWords(region, p))
		}
		return "replicated in " + strings.Join(where, ", ")
	}
	if r.Kind == localcfg.ReplicationMixed {
		return "replicated both automatically and in the installation's region (" + localcfg.ReplicationMixed + ": the containers that existed before keep " + automaticGloss + ")"
	}
	return "replicated with " + automaticGloss
}
