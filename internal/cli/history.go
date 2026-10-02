package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// fugaro budget history is the history job's hidden entry point (design
// §6.5, D12): a scheduled Cloud Run job, in its own small image, running as
// the history service account (firebasedatabase.admin and firebaseauth.admin
// on the Firebase project, run viewer on the GCP project). --sweep is shipped
// here; --rollover (RTDB to Firestore) arrives with M9d.
//
// The job has no project config file: Terraform gives it the plain
// environment below, and the account's identity is its only credential.

const (
	envProject     = "FUGARO_PROJECT"
	envGCPProject  = "FUGARO_GCP_PROJECT"
	envRegion      = "FUGARO_REGION"
	envFirebaseFP  = "FUGARO_FIREBASE_PROJECT"
	envRTDBURL     = "FUGARO_RTDB_URL"
	identityRoot   = "https://identitytoolkit.googleapis.com"
	cloudPlatform  = "https://www.googleapis.com/auth/cloud-platform"
	historyTimeout = 8 * time.Minute
)

// historyWiring points the history command at fakes; tests only.
type historyWiring struct {
	noAuth      bool // send no credentials
	runURL      string
	identityURL string
	now         func() time.Time
}

var historyTest *historyWiring

func newBudgetHistoryCmd() *cobra.Command {
	var sweep, rollover bool
	cmd := &cobra.Command{
		Use:    "history (--sweep | --rollover)",
		Short:  "The history job: sweep the run registry (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case sweep == rollover:
				return userErr("history needs exactly one of --sweep and --rollover")
			case rollover:
				return userErr("history --rollover (the daily move of the RTDB counters to Firestore) arrives with M9d; only --sweep exists in M9b")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), historyTimeout)
			defer cancel()
			return runHistorySweep(ctx, cmd, os.Getenv)
		},
	}
	cmd.Flags().BoolVar(&sweep, "sweep", false, "remove the registry entries of runs that ended, record them crashed, delete old run users")
	cmd.Flags().BoolVar(&rollover, "rollover", false, "move finished days to Firestore (arrives with M9d)")
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
