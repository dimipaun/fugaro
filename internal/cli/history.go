package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// fugaro budget history is the history job's hidden entry point (design
// §6.5, D12): a scheduled Cloud Run job, in its own small image, running as
// the history service account (firebasedatabase.admin and firebaseauth.admin
// on the Firebase project, run viewer on the GCP project). --sweep is shipped
// here; --rollover moves finished days to Firestore (M9d, rollover.go).
//
// The job has no project config file: Terraform gives it the plain
// environment below, and the account's identity is its only credential.

const (
	envProject     = "FUGARO_PROJECT"
	envGCPProject  = "FUGARO_GCP_PROJECT"
	envRegion      = "FUGARO_REGION"
	envFirebaseFP  = "FUGARO_FIREBASE_PROJECT"
	envRTDBURL     = "FUGARO_RTDB_URL"
	envRunsBucket  = "FUGARO_RUNS_BUCKET"
	envFirestoreDB = "FUGARO_FIRESTORE_DB"
	identityRoot   = "https://identitytoolkit.googleapis.com"
	cloudPlatform  = "https://www.googleapis.com/auth/cloud-platform"
	historyTimeout = 8 * time.Minute
	// rolloverTimeout bounds the daily pass, which reads the runs bucket.
	rolloverTimeout = 15 * time.Minute
	// rolloverMaxSkew is how far the job's clock may differ from the
	// database's before the rollover refuses to finalize days.
	rolloverMaxSkew = 10 * time.Minute
)

// historyWiring points the history command at fakes; tests only.
type historyWiring struct {
	noAuth      bool // send no credentials
	runURL      string
	identityURL string
	now         func() time.Time
	firestore   string // Firestore endpoint
	// bucket opens the runs bucket named by FUGARO_RUNS_BUCKET.
	bucket func(ctx context.Context, name string) (*blobx.Bucket, error)
	// skew checks the clock against the database's, as in production.
	skew bool
}

var historyTest *historyWiring

func newBudgetHistoryCmd() *cobra.Command {
	var sweep, rollover, force bool
	var day string
	cmd := &cobra.Command{
		Use:    "history (--sweep | --rollover)",
		Short:  "The history job: sweep the run registry (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case sweep == rollover:
				return userErr("history needs exactly one of --sweep and --rollover")
			case sweep && (force || day != ""):
				return userErr("--day and --force belong to --rollover")
			case rollover:
				ctx, cancel := context.WithTimeout(cmd.Context(), rolloverTimeout)
				defer cancel()
				return runHistoryRollover(ctx, cmd, os.Getenv, day, force)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), historyTimeout)
			defer cancel()
			return runHistorySweep(ctx, cmd, os.Getenv)
		},
	}
	cmd.Flags().BoolVar(&sweep, "sweep", false, "remove the registry entries of runs that ended, record them crashed, delete old run users")
	cmd.Flags().BoolVar(&rollover, "rollover", false, "move finished days to Firestore and prune old day nodes")
	cmd.Flags().StringVar(&day, "day", "", "with --rollover: only this UTC day (YYYY-MM-DD), any age still in the database")
	cmd.Flags().BoolVar(&force, "force", false, "with --rollover --day: rewrite a final document (a backfill)")
	return cmd
}

func runHistorySweep(ctx context.Context, cmd *cobra.Command, getenv func(string) string) error {
	if err := refuseHTTP2Debug(getenv); err != nil {
		return err
	}
	env := map[string]string{}
	for _, k := range []string{envProject, envGCPProject, envRegion, envFirebaseFP, envRTDBURL} {
		if env[k] = getenv(k); env[k] == "" {
			return userErr("the history job needs %s in its environment (Terraform sets it on the job)", k)
		}
	}
	test := historyTest
	if err := rtdb.ValidateURL(env[envRTDBURL], test != nil && test.noAuth); err != nil {
		return userErr("%s: %v", envRTDBURL, err)
	}

	if err := checkDatabaseHost(env[envRTDBURL], env[envFirebaseFP]); err != nil {
		return userErr("%v", err)
	}

	var (
		dbAuth   rtdb.Auth
		runOpts  = gcp.Options{GCPProject: env[envGCPProject], Region: env[envRegion]}
		authHTTP = http.DefaultClient
		idURL    = identityRoot
		now      = time.Now
	)
	if test != nil {
		now = test.now
		runOpts.Endpoints = gcp.Endpoints{Run: test.runURL, NoAuth: test.noAuth}
		if test.identityURL != "" {
			idURL = test.identityURL
		}
	}
	if test != nil && test.noAuth {
		dbAuth = rtdb.Auth{IDToken: func() string { return "" }}
	} else {
		ts, err := google.DefaultTokenSource(ctx, budgetScopes...)
		if err != nil {
			return remote(fmt.Errorf("no credentials for the budget database: %w", err))
		}
		dbAuth = rtdb.Auth{Source: ts}
		ats, err := google.DefaultTokenSource(ctx, cloudPlatform)
		if err != nil {
			return remote(fmt.Errorf("no credentials for the Identity Toolkit: %w", err))
		}
		authHTTP = oauth2.NewClient(ctx, ats)
	}
	db, err := rtdb.New(env[envRTDBURL], dbAuth)
	if err != nil {
		return userErr("%s: %v", envRTDBURL, err)
	}

	// The account may hold admin rights on several databases; write only to
	// this Fugaro project's own (plan R-9).
	var raw json.RawMessage
	found, err := db.Get(ctx, budget.PathProject, &raw)
	if err != nil {
		return remote(err)
	}
	var mark string
	if !found || json.Unmarshal(raw, &mark) != nil || mark != env[envProject] {
		return userErr("the database at %s is not project %s's budget database (its /fugaro/project does not name the project); nothing was swept", envRTDBURL, oneLine(env[envProject]))
	}

	be, err := gcp.New(ctx, runOpts)
	if err != nil {
		return remote(err)
	}
	sw := &budget.Sweeper{
		DB: db, Execs: be, JobOf: gcp.JobName, Now: now,
		Auth: &budget.AuthAdmin{Endpoint: idURL, Project: env[envFirebaseFP], HTTP: authHTTP},
		Warn: func(s string) { fmt.Fprintln(cmd.ErrOrStderr(), "sweep: "+oneLine(s)) },
	}
	rep, err := sw.Sweep(ctx)
	for _, r := range rep.Removed {
		fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", oneLine(r))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "sweep: %d removed, %d kept, %d run users deleted\n", len(rep.Removed), rep.Kept, rep.UsersDeleted)
	return remote(err)
}

// checkDatabaseHost refuses a database URL that is not the Firebase project's
// own: the sweeper deletes users in the project FUGARO_FIREBASE_PROJECT names
// and writes to the database FUGARO_RTDB_URL names, so the two must agree. A
// project's default database is <project>-default-rtdb (Terraform's
// instance_id). Loopback hosts (the fakes) are exempt.
func checkDatabaseHost(rawURL, firebaseProject string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%s: %v", envRTDBURL, err)
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	if label, _, _ := strings.Cut(host, "."); label != firebaseProject+"-default-rtdb" {
		return fmt.Errorf("%s names the database %q, which is not Firebase project %s's (%s); nothing was swept", envRTDBURL, oneLine(label), oneLine(firebaseProject), envFirebaseFP)
	}
	return nil
}
