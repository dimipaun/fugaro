package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/place"
)

// Firestore collections and documents init and the history job share.
const (
	FirestoreMetaCollection = "meta"
	FirestoreMarkDoc        = "installation"
	FirestoreHistory        = "spendDaily"
)

// FirestoreMarkVersion is the Firestore mark's schema version.
const FirestoreMarkVersion = 1

// FirestoreRules are the security rules init deploys: nothing is readable or
// writable by a client SDK. Only IAM principals (REST, which bypasses rules)
// reach the database.
const FirestoreRules = `rules_version = '2';
service cloud.firestore {
  match /databases/{database}/documents {
    match /{document=**} {
      allow read, write: if false;
    }
  }
}
`

// FirebaseRulesURL is the Firebase Rules API's root.
const FirebaseRulesURL = "https://firebaserules.googleapis.com"

// ErrFirestoreUnreadable marks a plan that could not read the project's
// Firestore state (the API is not enabled yet, or the caller may not use it).
var ErrFirestoreUnreadable = errors.New("cannot read the Firestore state")

// FirestoreMark is the mark init writes to meta/installation, the Firestore
// counterpart of the RTDB's /fugaro/mark: it names the Fugaro project, its
// GCP project and Firebase project, so a database of another installation is
// refused. It is a safety label, not a boundary.
type FirestoreMark struct {
	By              string
	Project         string
	GCPProject      string
	FirebaseProject string
	Version         int64
}

func (m FirestoreMark) fields(fugaroVersion string) map[string]any {
	return map[string]any{
		"managed_by": m.By, "project": m.Project, "gcp_project": m.GCPProject,
		"firebase_project": m.FirebaseProject, "version": m.Version, "fugaro_version": fugaroVersion,
	}
}

// mismatch says how the stored document differs from m ("" = same).
func (m FirestoreMark) mismatch(f map[string]any) string {
	str := func(k string) string { s, _ := f[k].(string); return s }
	switch {
	case str("managed_by") != m.By:
		return fmt.Sprintf("managed_by %q", str("managed_by"))
	case str("project") != m.Project:
		return fmt.Sprintf("Fugaro project %q", str("project"))
	case str("gcp_project") != m.GCPProject:
		return fmt.Sprintf("GCP project %q", str("gcp_project"))
	case str("firebase_project") != m.FirebaseProject:
		return fmt.Sprintf("Firebase project %q", str("firebase_project"))
	}
	// A mark of a newer schema (a later Fugaro) is still ours.
	if v, ok := f["version"].(int64); !ok || v < m.Version {
		return fmt.Sprintf("version %v", f["version"])
	}
	return ""
}

// FirestoreStep is what init does to the Firebase project's Firestore: the
// database (created only when none exists), the deny-all rules and the mark.
// Plan only reads; Apply writes, and is called after the operator confirmed.
// Nothing that exists is changed, and a surprise is a refusal.
type FirestoreStep struct {
	FS    *firestore.Client
	Rules *RulesClient
	// FP is the Firebase project; Project and GCPProject name the Fugaro
	// project and its GCP project (the mark); Version is fugaro's version.
	FP, Project, GCPProject, Version string
	// Location is where a database this step creates goes: place.For's answer
	// for the installation's region (D3085). It is what a database that does
	// not exist is created in, and a database that existed at plan time keeps
	// the location it has, wherever that is (R5, D3091). A database that
	// appears between the plan and the write is adopted only when it appeared
	// here, because this is the location the plan named and the run printed
	// (no step confirms a location: D3090 of docs/location-preference).
	Location string
	// Place is the place the installation's region lies in, which is what
	// says whether an adopted database is outside it (D3091).
	Place place.Place
	// Wait is the pause between read-back attempts (default 1 s).
	Wait time.Duration

	planned                          bool
	warnings                         []string
	createDB, deployRules, writeMark bool
	// dbKnown and dbExists are what the plan's own read of the database
	// resource answered, kept apart from planned: the reads AFTER it (the mark,
	// the collections, the rules) can fail on a database that is there, and a
	// caller that decides something from the answer has to tell "there is no
	// database" from "the plan never got the answer" (DatabaseExists,
	// DatabaseKnown; RI2).
	dbKnown, dbExists bool
	// adoptedDB is where a database that appeared between the plan and the
	// write, and was adopted instead of created, lives ("" when Apply created
	// the database itself, or when the plan already found one).
	adoptedDB string
}

func (s *FirestoreStep) mark() FirestoreMark {
	return FirestoreMark{By: markBy, Project: s.Project, GCPProject: s.GCPProject, FirebaseProject: s.FP, Version: FirestoreMarkVersion}
}

// Pending reports whether Plan found something to write.
func (s *FirestoreStep) Pending() bool { return s.createDB || s.deployRules || s.writeMark }

// CreatesDatabase reports that Plan found no database.
func (s *FirestoreStep) CreatesDatabase() bool { return s.createDB }

// AdoptedLocation is where a database that appeared after the plan, and was
// adopted instead of created, lives; "" when Apply created the database itself
// or when the plan already found one. The run says where it is, because it did
// not create it and the location is permanent (D3091).
func (s *FirestoreStep) AdoptedLocation() string { return s.adoptedDB }

// DatabaseExists reports whether the plan's own read of the database resource
// found one.
//
// It is the run index's own gate (RI2): the Firebase root's composite indexes
// can only be created under a database that is there, and this stage applies
// that root BEFORE this step creates the database (D3027), so the run has to
// ask here and hand the answer to the root's tfvars. A run that planned the
// indexes against a project without a database would fail its apply and never
// reach this step — and every run after it would fail the same way.
//
// It is this one read's answer and not the plan's, because the reads after it
// are the plan's own business: a mark or a rules read that fails says nothing
// about the database, and a caller must not read that failure as "there is
// none" — the tfvars built from it would plan away the indexes an installation
// already has. False is the safe direction for an installation whose state
// holds no index: it costs a read and never a failed apply, and the indexes
// arrive with the next run that can read the database. An installation whose
// state already holds them is kept out of that arm by its own state and not by
// this answer (internal/cli's runIndexIndexesManaged).
func (s *FirestoreStep) DatabaseExists() bool { return s.dbExists }

// DatabaseKnown reports whether that read answered at all.
//
// False is not "there is no database": it is "this plan never got the answer"
// — a permission problem, the Firestore API not enabled yet, or any other
// failure of the read itself. The run index's gate has to tell the two apart
// (RI2), because "no database" plans the indexes away.
func (s *FirestoreStep) DatabaseKnown() bool { return s.dbKnown }

func fsErr(what string, err error) error {
	if errors.Is(err, firestore.ErrPermission) {
		return fmt.Errorf("%w: %s: %v (the Firestore API is enabled by the Firebase root, and fugaro needs a project owner, editor or budget admin; role changes take a few minutes to apply)", ErrFirestoreUnreadable, what, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Plan reads the Firestore state, refuses what must not be touched and
// returns the lines the confirmation lists, and warnings. It makes no write.
func (s *FirestoreStep) Plan(ctx context.Context) (lines, warnings []string, err error) {
	s.createDB, s.deployRules, s.writeMark, s.planned = false, false, false, false
	s.dbKnown, s.dbExists = false, false
	s.warnings = nil
	db, err := s.FS.GetDatabase(ctx)
	switch {
	case errors.Is(err, firestore.ErrNotFound):
		s.dbKnown, s.createDB, s.writeMark = true, true, true
		lines = append(lines, fmt.Sprintf("Cloud Firestore database: CREATE the (default) database in %s (native mode, delete protection on). The location is PERMANENT: a Firestore database can never be moved, and fugaro never recreates one", s.Location))
	case err != nil:
		return nil, nil, fsErr("reading the Firestore database of "+s.FP, err)
	default:
		s.dbKnown, s.dbExists = true, true
		if err := s.checkExisting(db); err != nil {
			return nil, nil, err
		}
		if db.DeleteProtection != "DELETE_PROTECTION_ENABLED" {
			warnings = append(warnings, fmt.Sprintf("the Firestore database of %s has delete protection %s: fugaro leaves it as it is", s.FP, strings.ToLower(db.DeleteProtection)))
		}
		lines = append(lines, fmt.Sprintf("Cloud Firestore database: exists in %s%s, adopted as it is", db.LocationID, outsidePlaceWords(db.LocationID, s.Place)))
		marked, err := s.adoptable(ctx)
		if err != nil {
			return nil, nil, err
		}
		if marked {
			lines = append(lines, "Firestore mark (meta/installation): present, left as it is")
		} else {
			lines = append(lines, "Firestore database holds no collection and no mark: it is empty, adopted")
			s.writeMark = true
		}
	}
	if s.writeMark {
		lines = append(lines, fmt.Sprintf("Firestore mark: write %s/%s (project %s, GCP project %s, Firebase project %s)", FirestoreMetaCollection, FirestoreMarkDoc, s.Project, s.GCPProject, s.FP))
	}
	switch st, err := s.Rules.inspect(ctx, FirestoreRules); {
	case err != nil:
		return nil, nil, err
	case st == rulesMissing:
		s.deployRules = true
		lines = append(lines, "Firestore security rules: deploy deny-all rules (allow read, write: if false), so only IAM principals reach the database")
	default:
		lines = append(lines, "Firestore security rules: deny-all already deployed, left as it is")
	}
	s.planned = true
	return lines, append(warnings, s.warnings...), nil
}

// checkExisting refuses the one database fugaro cannot use at all: one that is
// not in native mode. Where it is decides nothing here — an installation made
// before this work, or a database someone else created, keeps the location it
// has (R5, D3091) — and the type is the one thing that is not a location and
// cannot be worked around: a Firestore client cannot reach a database in
// Datastore mode, and Google cannot convert one.
func (s *FirestoreStep) checkExisting(db *firestore.Database) error {
	if db.Type != "FIRESTORE_NATIVE" {
		return userErr("the Firebase project %s already has a Firestore database in location %q in %s mode, and fugaro adopts only a native-mode database: a database in Datastore mode cannot be converted and cannot serve Firestore clients, so fugaro neither adopts, moves nor recreates this one. Use a Firebase project whose database is in native mode, or delete this one yourself if it is empty and unwanted, then run it again", s.FP, db.LocationID, db.Type)
	}
	return nil
}

// outsidePlaceWords is what the adoption line adds when the database is
// somewhere other than the installation's place: where it is is then the one
// thing about it a person has to know, because nothing moves it (D3091). p is
// never None where the words are added — a region in neither place promises
// nothing for a database to be outside of (D3081) — and a location in no place
// at all, or in the same one, adds nothing.
func outsidePlaceWords(loc string, p place.Place) string {
	if p == place.None {
		return ""
	}
	if q := locationPlace(loc); q == place.None || q == p {
		return ""
	}
	if p == place.EU {
		return " (outside the EU)"
	}
	return " (outside the US)"
}

// locationPlace is the place a Firestore location lies in. place.Of answers
// for a region and for nothing else, and a database adopted where it was found
// can be in one of Firestore's multi-regions: nam5 is in the US and eur3 in
// the EU. It answers by the prefix, as place.Of does, so a multi-region Google
// adds tomorrow is in its place with no change here.
func locationPlace(loc string) place.Place {
	switch {
	case strings.HasPrefix(loc, "nam"):
		return place.US
	case strings.HasPrefix(loc, "eur"):
		return place.EU
	}
	return place.Of(loc)
}

// readMark reads meta/installation: marked is false when there is none; a
// mark of another installation is refused.
func (s *FirestoreStep) readMark(ctx context.Context) (marked bool, err error) {
	d, err := s.FS.Get(ctx, FirestoreMetaCollection, FirestoreMarkDoc)
	switch {
	case errors.Is(err, firestore.ErrNotFound):
		return false, nil
	case err != nil:
		return false, fsErr("reading "+FirestoreMetaCollection+"/"+FirestoreMarkDoc, err)
	}
	if why := s.mark().mismatch(d.Fields); why != "" {
		return false, userErr("the Firestore database of %s carries a Fugaro mark that is not ours: it names %s, not project %s in GCP project %s with Firebase project %s (one Firebase project serves one installation, design D3). Fugaro changes no mark", s.FP, why, s.Project, s.GCPProject, s.FP)
	}
	if v, _ := d.Fields["version"].(int64); v > s.mark().Version {
		s.warnings = append(s.warnings, fmt.Sprintf("the Firestore mark of %s has version %d, newer than this fugaro knows (%d): left as it is", s.FP, v, s.mark().Version))
	}
	return true, nil
}

// adoptable decides whether an existing database may be adopted: one with
// our mark is; one with a foreign mark is refused; an unmarked one only when
// it is empty (no root collection at all), because it is somebody else's
// otherwise.
func (s *FirestoreStep) adoptable(ctx context.Context) (marked bool, err error) {
	if marked, err = s.readMark(ctx); err != nil || marked {
		return marked, err
	}
	ids, err := s.FS.ListCollectionIDs(ctx)
	if err != nil {
		return false, fsErr("listing the collections of the Firestore database of "+s.FP, err)
	}
	if len(ids) > 0 {
		if len(ids) > 5 {
			ids = append(ids[:5:5], "...")
		}
		return false, userErr("the Firestore database of %s holds data (collections: %s) and no Fugaro mark (%s/%s): it is not ours, and fugaro adopts a database only when it is empty or carries our mark; nothing there was changed", s.FP, strings.Join(ids, ", "), FirestoreMetaCollection, FirestoreMarkDoc)
	}
	return false, nil
}

// Apply performs what Plan found, in order: the database, the rules, the mark,
// each verified by a read-back. Call it only after the confirmation.
func (s *FirestoreStep) Apply(ctx context.Context) error {
	if !s.planned {
		return errors.New("internal error: the Firestore step was applied without a plan")
	}
	if s.createDB {
		db, err := s.FS.CreateDatabase(ctx, s.Location, true)
		created := err == nil
		if errors.Is(err, firestore.ErrPrecondition) {
			// Created by someone since the plan: adopt only if it is ours to
			// adopt, and only where the plan put it and the run printed it.
			// Ours-or-empty is asked first, because a database holding
			// somebody else's data is refused for that and not for where it is
			// — and the refusal's own way out would be wrong advice about a
			// database the next plan would refuse too.
			if db, err = s.FS.GetDatabase(ctx); err == nil {
				if err = s.checkExisting(db); err == nil {
					if _, err = s.adoptable(ctx); err == nil {
						if db.LocationID != s.Location {
							// A location is permanent and s.Location is the one
							// the plan named and the run printed, so a database
							// that appeared elsewhere is a state nothing
							// announced: refuse rather than quietly keeping the
							// installation's history where nobody said it would
							// live.
							err = userErr("the Firebase project %s has had a Firestore database created in %s since this run read it, and the plan this run printed said it would create one in %s: a location can never be changed, so fugaro adopts a database only where the plan said it would be, and creates none. Nothing was written; run it again, and the plan will read the database where it is and adopt it there", s.FP, db.LocationID, s.Location)
						} else {
							// Ours, and where the plan named: the run says
							// where it is, because it did not create it.
							s.adoptedDB = db.LocationID
						}
					}
				}
			}
		}
		if err != nil {
			return fsErr("creating the Firestore database of "+s.FP, err)
		}
		if created && (db.LocationID != s.Location || db.Type != "FIRESTORE_NATIVE") {
			// We created it, and the service creates where it was asked: a
			// location or a mode that is not the one asked for is a surprise.
			// A database that existed at plan time is another matter — the plan
			// named where it is and printed it, and the run adopts it there,
			// whatever that is (R5, D3091) — and the race above refuses one that
			// appeared elsewhere after the plan, because the plan named
			// somewhere else.
			return fmt.Errorf("the Firestore database of %s reads back in location %q, type %s, not %s: stopping", s.FP, db.LocationID, db.Type, s.Location)
		}
		s.createDB = false
	}
	if s.writeMark {
		if err := s.putMark(ctx); err != nil {
			return err
		}
		s.writeMark = false
	}
	if s.deployRules {
		if err := s.Rules.deploy(ctx, FirestoreRules, s.wait()); err != nil {
			return err
		}
		s.deployRules = false
	}
	return nil
}

func (s *FirestoreStep) wait() time.Duration {
	if s.Wait > 0 {
		return s.Wait
	}
	return time.Second
}

// putMark writes the mark only if there is none, then reads it back.
func (s *FirestoreStep) putMark(ctx context.Context) error {
	_, err := s.FS.Patch(ctx, FirestoreMetaCollection, FirestoreMarkDoc, s.mark().fields(s.Version), firestore.MustNotExist())
	if err != nil && !errors.Is(err, firestore.ErrPrecondition) {
		return fsErr("writing the Firestore mark", err)
	}
	// A precondition failure means it exists now: it must be ours.
	var last error
	for i := 0; i < readBackTries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.wait()):
			}
		}
		marked, err := s.readMark(ctx)
		var ue *UserError
		switch {
		case errors.As(err, &ue):
			return err
		case err != nil:
			last = err
		case marked:
			return nil
		default:
			last = errors.New("the mark is not there after it was written")
		}
	}
	return fmt.Errorf("the Firestore mark did not read back: %w", last)
}

const readBackTries = 4

// RulesClient is the Firebase Rules API (REST, the person's credentials).
type RulesClient struct {
	// Endpoint is the API root (FirebaseRulesURL when empty).
	Endpoint string
	Project  string
	HTTP     *http.Client
}

type rulesError struct {
	method, path string
	code         int
	body         string
}

func (e *rulesError) Error() string {
	return fmt.Sprintf("Firebase Rules %s %s: HTTP %d: %s", e.method, e.path, e.code, e.body)
}

func (c *RulesClient) do(ctx context.Context, method, path string, body any, out any) error {
	if c.Project == "" || strings.ContainsAny(c.Project, "/?#% ") {
		return fmt.Errorf("%q is not a Firebase project id", c.Project)
	}
	root := c.Endpoint
	if root == "" {
		root = FirebaseRulesURL
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(root, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("x-goog-user-project", c.Project)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Firebase Rules %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("Firebase Rules %s %s: %w", method, path, err)
	}
	if resp.StatusCode/100 != 2 {
		msg := string(b)
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return &rulesError{method, path, resp.StatusCode, msg}
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("Firebase Rules %s %s: %w", method, path, err)
		}
	}
	return nil
}

func (c *RulesClient) releasePath() string {
	return "/v1/projects/" + url.PathEscape(c.Project) + "/releases/cloud.firestore"
}

type rulesState int

const (
	rulesMissing rulesState = iota // no release: nothing to loosen
	rulesSame                      // the release's rules equal ours
)

// release is the ruleset's source the release points at; exists is false when
// there is no release (404).
func (c *RulesClient) release(ctx context.Context) (source string, exists bool, err error) {
	var rel struct {
		RulesetName string `json:"rulesetName"`
	}
	err = c.do(ctx, http.MethodGet, c.releasePath(), nil, &rel)
	var re *rulesError
	switch {
	case errors.As(err, &re) && re.code == http.StatusNotFound:
		return "", false, nil
	case errors.As(err, &re) && (re.code == http.StatusForbidden || re.code == http.StatusUnauthorized):
		return "", false, fmt.Errorf("%w: %v (the Firebase Rules API is enabled by the Firebase root, and fugaro needs a project owner, editor or budget admin)", ErrFirestoreUnreadable, err)
	case err != nil:
		return "", false, err
	}
	// rulesetName is projects/<p>/rulesets/<id>; read it by that name only
	// when it is of this project.
	prefix := "projects/" + c.Project + "/rulesets/"
	if !strings.HasPrefix(rel.RulesetName, prefix) || strings.ContainsAny(rel.RulesetName[len(prefix):], "/?#") || len(rel.RulesetName) == len(prefix) {
		return "", false, fmt.Errorf("the Firestore release names the ruleset %q, which is not one of project %s", rel.RulesetName, c.Project)
	}
	var rs struct {
		Source struct {
			Files []struct {
				Name    string `json:"name"`
				Content string `json:"content"`
			} `json:"files"`
		} `json:"source"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/"+rel.RulesetName, nil, &rs); err != nil {
		return "", false, err
	}
	if len(rs.Source.Files) != 1 {
		// Several files are not ours: report them as a source that matches nothing.
		return fmt.Sprintf("%d files", len(rs.Source.Files)), true, nil
	}
	return rs.Source.Files[0].Content, true, nil
}

// normalizeRules makes two rule sources that differ in comments and
// whitespace equal. String literals are kept exactly (a quote starts one, a
// backslash escapes inside it), and comments inside them are not comments.
func normalizeRules(s string) string {
	var b strings.Builder
	space := false
	flush := func() {
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			space = true
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return s // unterminated: matches nothing sensible
			}
			i += end + 3
			space = true
		case c == '\'' || c == '"':
			flush()
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				j = len(s) - 1
			}
			b.WriteString(s[i : j+1])
			i = j
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			space = true
		default:
			flush()
			b.WriteByte(c)
		}
	}
	return b.String()
}

// inspect reads the Firestore release. No release is rulesMissing; one whose
// rules equal want (apart from comments and whitespace) is rulesSame; any
// other is refused: 'fugaro update --firebase-project' never loosens, replaces
// or tightens rules that are there.
func (c *RulesClient) inspect(ctx context.Context, want string) (rulesState, error) {
	src, exists, err := c.release(ctx)
	switch {
	case err != nil:
		return 0, err
	case !exists:
		return rulesMissing, nil
	case normalizeRules(src) != normalizeRules(want):
		return 0, userErr("the Firebase project %s already has Firestore security rules that are not deny-all, and 'fugaro update --firebase-project %s' never replaces rules that are there (they could be looser or tighter than you want). Read them (Firebase console, Firestore, Rules), and either deploy deny-all yourself (allow read, write: if false; for every document) or delete the release, then run 'fugaro update --firebase-project %s' again", c.Project, c.Project, c.Project)
	}
	return rulesSame, nil
}

// deploy creates the ruleset and the release (the project has none, as
// inspect found) and reads the release back until it points at rules equal to
// want.
func (c *RulesClient) deploy(ctx context.Context, want string, wait time.Duration) error {
	if st, err := c.inspect(ctx, want); err != nil {
		return err
	} else if st == rulesSame {
		return nil
	}
	var rs struct {
		Name string `json:"name"`
	}
	body := map[string]any{"source": map[string]any{"files": []any{map[string]any{"name": "firestore.rules", "content": want}}}}
	if err := c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(c.Project)+"/rulesets", body, &rs); err != nil {
		return fmt.Errorf("creating the Firestore ruleset: %w", err)
	}
	rel := map[string]any{"name": "projects/" + c.Project + "/releases/cloud.firestore", "rulesetName": rs.Name}
	err := c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(c.Project)+"/releases", rel, nil)
	var re *rulesError
	if err != nil && !(errors.As(err, &re) && re.code == http.StatusConflict) {
		return fmt.Errorf("releasing the Firestore ruleset: %w", err)
	}
	// A conflict means a release appeared meanwhile: it must be deny-all too,
	// which the read-back below checks (and refuses otherwise).
	var last error
	for i := 0; i < readBackTries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		st, err := c.inspect(ctx, want)
		var ue *UserError
		switch {
		case errors.As(err, &ue):
			return err
		case err != nil:
			last = err
		case st == rulesSame:
			return nil
		default:
			last = errors.New("the release is not there after it was created")
		}
	}
	return fmt.Errorf("the Firestore rules did not read back as deployed: %w", last)
}
