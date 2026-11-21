package localcfg

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Locations is where this installation's located things are, as one machine
// read them from the cloud (LP0 of docs/location-preference/, D3087): the
// project config's `locations` block. Its document is
// docs/location-preference/record.md.
//
// It is a record and never an input. What a thing that exists is told to be is
// read from the cloud, for that thing, every time it is planned (R5), so this
// block decides nothing: a wrong, stale or hand-edited copy of it changes no
// plan, no import and no apply. Its readers are the ones that say where the
// data already is — `fugaro doctor`'s row (LP12) — and the converge that writes
// it back from what it found (LP14, infra.ObserveLocations).
//
// Every member is optional and an absent one means the same thing wherever it
// is absent: not known yet. A member is absent on an installation no converge
// has read yet, and on an installation whose thing does not exist at all —
// there is no database to be anywhere — and the two are one statement on
// purpose, because neither is a location a reader may act on.
//
// It is NOT published in the shared config (Shared drops it), and that is the
// reason it is one machine's own rather than the installation's like `region`
// (D3087 as amended after review, F17): the published copy decodes strictly, so
// a `locations:` key in the runs bucket's file would stop every teammate whose
// Fugaro is older than the release that wrote it, and a record Terraform read
// would plan a new replication for every secret container that exists, which
// carries prevent_destroy.
type Locations struct {
	// Firestore is the location of the project's Firestore database, in
	// Google's own spelling: a region (us-east5) or one of its multi-regions
	// (nam5, eur3). It is empty for a project with no database.
	Firestore string `yaml:"firestore,omitempty"`
	// RTDB is the region of the project's default Realtime Database — the
	// budget's backend — which is a region and never a multi-region: a
	// database created before this work is in us-central1, and one the
	// installation created since is in the installation's region.
	RTDB string `yaml:"rtdb,omitempty"`
	// Secrets is what the installation's own Secret Manager containers do:
	// automatic replication, the regions they are replicated in, or both at
	// once (SecretsReplication).
	Secrets SecretsReplication `yaml:"secrets,omitempty"`
	// LogBucket is the location of the installation's run log bucket: the
	// region Cloud Logging offers it in, the place's multi-region (us, eu)
	// where its region does not, or global on an installation made before
	// this work. It is empty for an installation set up with
	// --no-log-isolation, which has no bucket of its own.
	LogBucket string `yaml:"log_bucket,omitempty"`
}

// IsZero reports whether the block holds nothing: a document that wrote
// `locations: {}` is the document that wrote no record at all, and `omitempty`
// reads this to leave the key out when the file is written back. Without it a
// file carrying the empty block — one an operator hand-edited, or a writer that
// wrote the block before a member existed — would be rewritten by the next
// converge with the block still in it, which is a rewrite of a file the record
// has nothing to say about (the HARD RULE at the block's own scale).
func (l *Locations) IsZero() bool {
	return l == nil || (l.Firestore == "" && l.RTDB == "" && l.LogBucket == "" && l.Secrets.IsZero())
}

// The two replications a container's policy has a name for. A third answer is
// a list of regions, which is not a constant because it carries them.
const (
	// ReplicationAutomatic is Google's own default: the container is
	// replicated by Google, which today means the United States.
	ReplicationAutomatic = "automatic"
	// ReplicationMixed is both at once, which is what every installation made
	// before this work becomes: the containers that existed keep their
	// automatic replication, and every container created since is replicated
	// in the installation's region (R5, D3092).
	ReplicationMixed = "mixed"
)

// SecretsReplication is the replication of the installation's Secret Manager
// containers, written one of three ways: `automatic`, `mixed`, or the regions
// the containers are replicated in (D3087, R5).
//
// It is three spellings of one value, so it is one type with the file's own
// marshalling rather than three members of a block: a written
// `secrets: {automatic: true, regions: [...]}` would be a fourth statement
// nothing reads, and the two facts it spells are one policy. The read that
// prints the settings row renders it through String.
//
// The two fields carry no yaml tag of their own, which is what makes the value
// one key: the settings registry walks the config's yaml tags
// (TestRegistryHasARowForEveryRealConfigField), so a member here would be a
// row for a spelling no file has.
type SecretsReplication struct {
	// Kind is ReplicationAutomatic, ReplicationMixed, or empty for a policy
	// that names regions instead — and for an absent key, which is why
	// IsZero asks about both fields.
	Kind string `yaml:"-"`
	// Regions are the regions a user-managed policy replicates in, as the
	// cloud reported them, sorted and each once by the writer that records
	// them. A legacy container replicated in several regions has several;
	// the ones this work creates have one.
	Regions []string `yaml:"-"`
}

// IsZero reports whether the replication is the absent one, which is what
// `omitempty` reads to leave the key out of a document. An absent key and a
// written `secrets:` with nothing under it are both "not known yet".
func (r SecretsReplication) IsZero() bool { return r.Kind == "" && len(r.Regions) == 0 }

// String renders the replication the way the settings read prints it: the
// regions when it names them, else the word. It is also what keeps a struct of
// two unexported-to-yaml fields from printing as an empty JSON object.
func (r SecretsReplication) String() string {
	if len(r.Regions) > 0 {
		return strings.Join(r.Regions, ", ")
	}
	return r.Kind
}

// MarshalYAML writes the one spelling this policy has: the regions when it
// names them, else the word, and nothing at all for an absent one (which
// omitempty has already dropped).
func (r SecretsReplication) MarshalYAML() (any, error) {
	if len(r.Regions) > 0 {
		return r.Regions, nil
	}
	if r.Kind == "" {
		return nil, nil
	}
	return r.Kind, nil
}

// UnmarshalYAML reads the three spellings. It is a custom reader and not three
// tagged fields because the value is a scalar or a list in the same key, and
// it therefore does the strictness a tagged struct would get from Parse's own
// decoder: a scalar that is neither word is refused rather than kept as a
// region list of one, and a list is held to the region shape here, where the
// spelling is in hand.
func (r *SecretsReplication) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Tag == "!!null" || value.Value == "" {
			// A written `secrets:` with nothing under it is the absent policy,
			// the same statement the absent key makes: a writer that clears the
			// member leaves a key behind, and refusing it would make the
			// clearing a file its own author has to repair.
			r.Kind, r.Regions = "", nil
			return nil
		}
		if value.Value != ReplicationAutomatic && value.Value != ReplicationMixed {
			return fmt.Errorf("locations.secrets %q is not a replication: write %s, %s, or the regions the containers are replicated in (for example [europe-west1])",
				value.Value, ReplicationAutomatic, ReplicationMixed)
		}
		r.Kind, r.Regions = value.Value, nil
		return nil
	case yaml.SequenceNode:
		var regions []string
		if err := value.Decode(&regions); err != nil {
			return errors.New("locations.secrets must be " + ReplicationAutomatic + ", " + ReplicationMixed + " or a list of regions such as [europe-west1]")
		}
		if len(regions) == 0 {
			return errors.New("locations.secrets is a list with no region in it: write " + ReplicationAutomatic + ", " + ReplicationMixed + ", or the regions the containers are replicated in, or remove the key to say the replication is not known yet")
		}
		r.Kind, r.Regions = "", regions
		return nil
	}
	return errors.New("locations.secrets must be " + ReplicationAutomatic + ", " + ReplicationMixed + " or a list of regions such as [europe-west1]")
}

// locationRE is the shape of a location Google names: a region such as
// europe-west1, one of the multi-regions a Firestore database and a log bucket
// may live in (nam5, eur3, us, eu), or global. It is deliberately not regionRE:
// those three are locations and are not regions, and a Firestore database an
// installation adopted outside its place is recorded where it really is.
var locationRE = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

// validateLocations holds the record to its own rules at the load. A location
// that is not a location is a record a reader would take for a read of the
// cloud — doctor prints it as where the data is — so the shape is checked
// where it is written rather than at the reader.
//
// The one rule not here is the scalar-or-list spelling of the replication,
// which only the reader that sees the node can apply (SecretsReplication's own
// UnmarshalYAML), and the region shape of a list entry is asked here as well as
// there for the writer's sake: a block built in Go and marshalled never passed
// through the reader.
func validateLocations(c *Config, bad func(string, ...any)) {
	l := c.Locations
	if l == nil {
		return
	}
	if l.Firestore != "" && !locationRE.MatchString(l.Firestore) {
		bad("locations.firestore %q is not a location: write the region or multi-region Google put the database in, such as us-east5 or nam5", l.Firestore)
	}
	if l.RTDB != "" && !regionRE.MatchString(l.RTDB) {
		bad("locations.rtdb %q is not a region such as us-central1: a Realtime Database lives in a region", l.RTDB)
	}
	if l.LogBucket != "" && !locationRE.MatchString(l.LogBucket) {
		bad("locations.log_bucket %q is not a location: write the region, multi-region or global the run log bucket is in", l.LogBucket)
	}
	for _, region := range l.Secrets.Regions {
		if !regionRE.MatchString(region) {
			bad("locations.secrets: %q is not a region such as europe-west1", region)
		}
	}
	if why := duplicated(l.Secrets.Regions); why != "" {
		bad("locations.secrets lists %s twice: the regions a replication names are a set, and a repeated one is a typo rather than a second fact", why)
	}
}
