package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/firestore"
)

// FirestoreLocation is where init creates the Firebase project's Firestore
// database (design D4). A database's location can NEVER be changed, so init
// creates one only when none exists, never falls back to another location by
// itself, and refuses a database that is anywhere else.
const FirestoreLocation = "us-east5"

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
	case fmt.Sprint(f["version"]) != fmt.Sprint(m.Version):
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
	// Wait is the pause between read-back attempts (default 1 s).
	Wait time.Duration

	planned                          bool
	createDB, deployRules, writeMark bool
}

func (s *FirestoreStep) mark() FirestoreMark {
	return FirestoreMark{By: markBy, Project: s.Project, GCPProject: s.GCPProject, FirebaseProject: s.FP, Version: FirestoreMarkVersion}
}

// Pending reports whether Plan found something to write.
func (s *FirestoreStep) Pending() bool { return s.createDB || s.deployRules || s.writeMark }

// CreatesDatabase reports that Plan found no database.
func (s *FirestoreStep) CreatesDatabase() bool { return s.createDB }

func fsErr(what string, err error) error {
	if errors.Is(err, firestore.ErrPermission) {
		return fmt.Errorf("%w: %s: %v (the Firestore API is enabled by the Firebase root, and init needs a project owner, editor or budget admin; role changes take a few minutes to apply)", ErrFirestoreUnreadable, what, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Plan reads the Firestore state, refuses what must not be touched and
// returns the lines the confirmation lists, and warnings. It makes no write.
func (s *FirestoreStep) Plan(ctx context.Context) (lines, warnings []string, err error) {
	s.createDB, s.deployRules, s.writeMark, s.planned = false, false, false, false
	db, err := s.FS.GetDatabase(ctx)
	switch {
	case errors.Is(err, firestore.ErrNotFound):
		s.createDB, s.writeMark = true, true
		lines = append(lines, fmt.Sprintf("Cloud Firestore database: CREATE the (default) database in %s (native mode, delete protection on). The location is PERMANENT: a Firestore database can never be moved, and init never recreates one", FirestoreLocation))
	case err != nil:
		return nil, nil, fsErr("reading the Firestore database of "+s.FP, err)
	default:
		if err := s.checkExisting(db); err != nil {
			return nil, nil, err
		}
		if db.DeleteProtection != "DELETE_PROTECTION_ENABLED" {
			warnings = append(warnings, fmt.Sprintf("the Firestore database of %s has delete protection %s: init leaves it as it is", s.FP, strings.ToLower(db.DeleteProtection)))
		}
		lines = append(lines, fmt.Sprintf("Cloud Firestore database: exists in %s, adopted as it is", db.LocationID))
		marked, err := s.readMark(ctx)
		if err != nil {
			return nil, nil, err
		}
		if marked {
			lines = append(lines, "Firestore mark (meta/installation): present, left as it is")
		} else {
			if err := s.refuseForeignData(ctx); err != nil {
				return nil, nil, err
			}
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
	return lines, warnings, nil
}

func (s *FirestoreStep) checkExisting(db *firestore.Database) error {
	if db.LocationID != FirestoreLocation || db.Type != "FIRESTORE_NATIVE" {
		return userErr("the Firebase project %s already has a Firestore database in location %q (type %s), and init creates one in %s only: a location is permanent, so init neither adopts, moves nor recreates this one. Use a Firebase project without a Firestore database, or delete this one yourself if it is empty and unwanted, then rerun", s.FP, db.LocationID, db.Type, FirestoreLocation)
	}
	return nil
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
		return false, userErr("the Firestore database of %s carries a Fugaro mark that is not ours: it names %s, not project %s in GCP project %s with Firebase project %s (one Firebase project serves one installation, design D3). init changes no mark", s.FP, why, s.Project, s.GCPProject, s.FP)
	}
	return true, nil
}

// refuseForeignData refuses an existing, unmarked database that already
// holds spend history: it is somebody else's.
func (s *FirestoreStep) refuseForeignData(ctx context.Context) error {
	docs, err := s.FS.Query(ctx, FirestoreHistory, "date", "0000-00-00", "9999-99-99")
	if err != nil && !errors.Is(err, firestore.ErrNotFound) {
		return fsErr("reading the "+FirestoreHistory+" collection", err)
	}
	if len(docs) > 0 {
		return userErr("the Firestore database of %s holds %d %s documents and no Fugaro mark (%s/%s): it is not ours, and init writes into databases that are empty or carry our mark", s.FP, len(docs), FirestoreHistory, FirestoreMetaCollection, FirestoreMarkDoc)
	}
	return nil
}

// Apply performs what Plan found, in order: the database, the rules, the mark,
// each verified by a read-back. Call it only after the confirmation.
func (s *FirestoreStep) Apply(ctx context.Context) error {
	if !s.planned {
		return errors.New("internal error: the Firestore step was applied without a plan")
	}
	if s.createDB {
		db, err := s.FS.CreateDatabase(ctx, FirestoreLocation, true)
		if errors.Is(err, firestore.ErrPrecondition) {
			// Created by someone since the plan: adopt only if it is ours to adopt.
			if db, err = s.FS.GetDatabase(ctx); err == nil {
				err = s.checkExisting(db)
			}
		}
		if err != nil {
			return fsErr("creating the Firestore database of "+s.FP, err)
		}
		if db.LocationID != FirestoreLocation || db.Type != "FIRESTORE_NATIVE" {
			return fmt.Errorf("the Firestore database of %s reads back in location %q, type %s, not %s: stopping", s.FP, db.LocationID, db.Type, FirestoreLocation)
		}
		s.createDB = false
	}
	if s.deployRules {
		if err := s.Rules.deploy(ctx, FirestoreRules, s.wait()); err != nil {
			return err
		}
		s.deployRules = false
	}
	if s.writeMark {
		if err := s.putMark(ctx); err != nil {
			return err
		}
		s.writeMark = false
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
		return "", false, fmt.Errorf("%w: %v (the Firebase Rules API is enabled by the Firebase root, and init needs a project owner, editor or budget admin)", ErrFirestoreUnreadable, err)
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

var (
	lineComment  = regexp.MustCompile(`//[^\n]*`)
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	spaces       = regexp.MustCompile(`\s+`)
)

// normalizeRules makes two rule sources that differ in comments and
// whitespace equal.
func normalizeRules(s string) string {
	s = blockComment.ReplaceAllString(s, " ")
	s = lineComment.ReplaceAllString(s, " ")
	return strings.TrimSpace(spaces.ReplaceAllString(s, " "))
}

// inspect reads the Firestore release. No release is rulesMissing; one whose
// rules equal want (apart from comments and whitespace) is rulesSame; any
// other is refused: init never loosens, replaces or tightens rules that are
// there.
func (c *RulesClient) inspect(ctx context.Context, want string) (rulesState, error) {
	src, exists, err := c.release(ctx)
	switch {
	case err != nil:
		return 0, err
	case !exists:
		return rulesMissing, nil
	case normalizeRules(src) != normalizeRules(want):
		return 0, userErr("the Firebase project %s already has Firestore security rules that are not deny-all, and init never replaces rules that are there (they could be looser or tighter than you want). Read them (Firebase console, Firestore, Rules), and either deploy deny-all yourself (allow read, write: if false; for every document) or delete the release, then rerun init", c.Project)
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
