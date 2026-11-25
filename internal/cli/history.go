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
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runindex"
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
	// The rollover's Scheduler call gives its execution a 30 minute timeout
	// (overrides.timeout); no new day is started after 25 minutes, and the
	// context ends just before the platform's kill, so a pass ends cleanly
	// and the next one continues.
	rolloverStartLimit = 25 * time.Minute
	rolloverTimeout    = 29 * time.Minute
	// rolloverMaxSkew is how far the job's clock may differ from the
	// database's before the rollover refuses to finalize days.
	rolloverMaxSkew = 10 * time.Minute
	// reindexReserve is what a reindex pass leaves itself out of the
	// execution's deadline to write the position the next pass resumes from
	// and give the lease up (RI5, F39): a walk stopped by the platform's kill
	// with nothing left to write it has no cursor, and the sweep behind it
	// starts the walk over rather than continuing it.
	reindexReserve = 45 * time.Second
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
	// pushURL is the FCM endpoint the sender posts to; pushSend replaces the
	// provider whole. Both are tests only: in production the endpoint is
	// fcmRoot, a constant, because a sender that reads where it sends from its
	// environment is a sender anyone who can set the job's environment steers
	// (§3.6).
	pushURL  string
	pushSend pushProvider
}

var historyTest *historyWiring

func newBudgetHistoryCmd() *cobra.Command {
	var sweep, rollover, reindex, force bool
	var day string
	cmd := &cobra.Command{
		Use:    "history (--sweep | --rollover | --reindex)",
		Short:  "The history job: sweep the run registry (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case exactlyOne(sweep, rollover, reindex) != 1:
				return userErr("history needs exactly one of --sweep, --rollover and --reindex")
			case !rollover && (force || day != ""):
				return userErr("--day and --force belong to --rollover")
			case rollover:
				ctx, cancel := context.WithTimeout(cmd.Context(), rolloverTimeout)
				defer cancel()
				return runHistoryRollover(ctx, cmd, os.Getenv, day, force)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), historyTimeout)
			defer cancel()
			return runHistorySweep(ctx, cmd, os.Getenv, reindex)
		},
	}
	cmd.Flags().BoolVar(&sweep, "sweep", false, "remove the registry entries of runs that ended, record them crashed, delete old run users")
	cmd.Flags().BoolVar(&rollover, "rollover", false, "move finished days to Firestore and prune old day nodes")
	cmd.Flags().BoolVar(&reindex, "reindex", false, "walk every run in the bucket into the run index and recompute the summary (RI5)")
	cmd.Flags().StringVar(&day, "day", "", "with --rollover: only this UTC day (YYYY-MM-DD), any age still in the database")
	cmd.Flags().BoolVar(&force, "force", false, "with --rollover --day: rewrite a final document (a backfill)")
	return cmd
}

// exactlyOne counts how many of the job's modes were asked for. A mode is a
// flag's own truth, and the job refuses anything but one: two modes at once
// would run one of them silently.
func exactlyOne(flags ...bool) int {
	n := 0
	for _, f := range flags {
		if f {
			n++
		}
	}
	return n
}

func runHistorySweep(ctx context.Context, cmd *cobra.Command, getenv func(string) string, reindex bool) error {
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
	// The name is half the answer, and the weaker half: a project name is a
	// string two installations can both carry, and what tells them apart is the
	// GCP project the mark records beside it. This job deletes registry nodes
	// and Firebase users, so the ownership question is the one every writer into
	// the budget database asks, asked of the same predicate
	// (budgetDBUnadopted) rather than of a second check that agrees with it
	// until one of the two is edited (the review's F5). The environment is read
	// as the config it is the same facts as (historyJobConfig); the pair itself
	// was refused above, before a client was built (checkDatabaseHost).
	if why := budgetDBUnadopted(ctx, historyJobConfig(env), infra.NewDB(db, env[envProject], env[envGCPProject], ""), "nothing was swept", selfCommand()+" update"); why != "" {
		return userErr("%v", oneLine(why))
	}

	// --reindex is a mode of its own (RI5): the walk over every run in the
	// bucket, with none of the registry sweep's work. It asks the same
	// ownership question above, because it writes the same installation's
	// index, and it is the whole job when it is asked for -- an index that
	// cannot be walked is the reason the execution was started, so its failure
	// is the job's exit code rather than a warning.
	if reindex {
		return remote(indexSweep(ctx, cmd, db, env, getenv, now, test, true))
	}

	// This client is the Cloud Run arm of the sweep's liveness rule, and the
	// rule's load-bearing half is the run's own heartbeat: budget.Sweeper
	// keeps a registry entry whose heartbeat is inside its staleness window
	// whatever the platform says, and this listing only ever ADDS a run the
	// platform still reports (isLive). On an installation whose runs are on
	// Batch the listing answers nothing -- a Batch job's execution is not a
	// Cloud Run execution and never appears here (design compute-backend.md
	// §2.3) -- so the sweep falls back to exactly the rule that already
	// carries every live run, the heartbeat the runner writes on either
	// backend. What that leaves is the window that staleness window bounds,
	// stated in docs/compute-backend/spec.md §9 rather than closed here: the
	// job's environment carries no backend name to key a refusal on
	// (deploy/terraform/gcp/modules/installation/history.tf), and its account
	// holds no Batch read to ask the platform with, so a refusal would have to
	// be invented out of a fact this process does not have.
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
	fmt.Fprintf(cmd.OutOrStdout(), "sweep: %d removed, %d kept, %d users deleted, %d pairing nodes deleted\n", len(rep.Removed), rep.Kept, rep.UsersDeleted, rep.PairingNodesDeleted)

	// The sender (mobile plan Task P5.2.6, design §3.6 (B)): the sweep derives
	// the push events itself and sends them, under this job's own identity —
	// the only account that can send, which is why nothing on a run's path
	// writes a pending-push row.
	//
	// It runs AFTER the registry sweep, so the caps it reads are the live
	// runs': an entry the sweep just removed was a run that ended, and a run
	// that ended is not an event. Every failure it has is a warning rather than
	// the job's exit code, because the mailbox is authoritative and the push is
	// the doorbell (§6): alerts failing must never stop the sweep, and the
	// identity that would let one through arrives with P5.2.7 — the relay's
	// caller, not an FCM grant.
	push := &pushSender{
		db: db, project: env[envFirebaseFP], now: now,
		send: fcmSender{project: env[envFirebaseFP], url: fcmRoot, http: authHTTP}.send,
		warn: func(s string) { fmt.Fprintln(cmd.ErrOrStderr(), "push: "+oneLine(s)) },
	}
	if test != nil {
		if test.pushURL != "" {
			push.send = fcmSender{project: env[envFirebaseFP], url: test.pushURL, http: authHTTP}.send
		}
		if test.pushSend != nil {
			push.send = test.pushSend
		}
	}
	prep, perr := push.Sweep(ctx)
	fmt.Fprintf(cmd.OutOrStdout(), "push: %d sent, %d suppressed, %d queued, %d retried, %d tokens reaped, %d nodes reaped, %d ledger rows pruned\n",
		prep.Sent, prep.Suppressed, prep.Queued, prep.Retried, prep.Unregistered, prep.Orphans, prep.Pruned)
	if perr != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "push: "+oneLine(perr.Error()))
	}

	// The fold (RI4, D3201): the runs that changed since the index's stamp,
	// read out of the runs bucket and written into the run index. It is the
	// index's only incremental writer, and it runs after the registry sweep so
	// that a run the sweep has just removed -- a crashed one, whose outcome
	// node the sweep writes -- is folded rather than missed.
	//
	// Every failure it has is a warning rather than the job's exit code, as the
	// push's are: the sweep's own work is the registry, and a fold that cannot
	// run must not stop the sweep that removes crashed runs and deletes their
	// users (D12). What a failed fold leaves is the stamp where it was, so the
	// next sweep reads the same window again.
	if ferr := indexSweep(ctx, cmd, db, env, getenv, now, test, false); ferr != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "index: "+oneLine(ferr.Error()))
	}
	return remote(err)
}

// indexSweep is the sweep's last step: the fold of what changed into the run
// index (RI4, D3201), or -- for --reindex and for a sweep that finds an
// unfinished walk -- the reindex pass over the runs bucket (RI5, D3215).
//
// The job's environment is Terraform's and carries the runs bucket and the
// Firestore database name for the rollover's sake; a sweep without them is not
// an error but a warning, because the sweep itself does not need them (its own
// work is the registry) and the deployment that carries them is the same job.
//
// The writer lease's holder is the execution's name, so a lease a person reads
// names a job run rather than a process. An invocation outside Cloud Run (a
// test, a person running the job's command by hand) has no execution name and
// takes the lease under the job's own name.
//
// Either pass is bounded by the execution's own deadline rather than by a
// timeout of its own: a walk that reaches the bound stops at the run it is on
// and leaves its position behind, so the next sweep continues it, with no
// second execution started and no override anywhere (F9, F39, RI5).
func indexSweep(ctx context.Context, cmd *cobra.Command, db *rtdb.Client, env map[string]string, getenv func(string) string, now func() time.Time, test *historyWiring, reindex bool) error {
	bucketName, fsDB := getenv(envRunsBucket), getenv(envFirestoreDB)
	if bucketName == "" || fsDB == "" {
		return fmt.Errorf("not folded: the job's environment carries no %s and %s", envRunsBucket, envFirestoreDB)
	}
	if fsDB != "(default)" {
		return fmt.Errorf("not folded: %s must be (default): Fugaro uses the project's default database", envFirestoreDB)
	}
	if strings.ContainsAny(bucketName, "/: ") {
		return fmt.Errorf("not folded: %s must be a bucket name", envRunsBucket)
	}
	noAuth := test != nil && test.noAuth
	fsURL := ""
	openBkt := func(ctx context.Context, name string) (*blobx.Bucket, error) { return blobx.Open(ctx, "gs://"+name) }
	if test != nil {
		fsURL = test.firestore
		if test.bucket != nil {
			openBkt = test.bucket
		}
	}
	var fsSrc oauth2.TokenSource
	if noAuth {
		if fsURL == "" {
			return fmt.Errorf("not folded: no-auth wiring without a Firestore endpoint")
		}
		fsSrc = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"})
	} else {
		ts, err := google.DefaultTokenSource(ctx, cloudPlatform)
		if err != nil {
			return fmt.Errorf("no credentials for Firestore: %w", err)
		}
		fsSrc = ts
	}
	fs, err := firestore.New(fsURL, env[envFirebaseFP], fsSrc)
	if err != nil {
		return err
	}
	bucket, err := openBkt(ctx, bucketName)
	if err != nil {
		return err
	}
	defer bucket.Close()

	holder := getenv("CLOUD_RUN_EXECUTION")
	if holder == "" {
		holder = "fugaro-history"
	}
	f := &runindex.Fold{
		DB: db, FS: fs, Bucket: bucket.Bucket, Holder: holder, Now: now,
		Warn: func(s string) { fmt.Fprintln(cmd.ErrOrStderr(), "index: "+oneLine(s)) },
	}
	if deadline, ok := ctx.Deadline(); ok {
		f.StartLimit = deadline.Add(-reindexReserve)
	}
	if reindex {
		rep, err := f.Walk(ctx)
		if err != nil {
			return err
		}
		return printWalk(cmd, rep)
	}
	rep, err := f.Sweep(ctx)
	if err != nil {
		return err
	}
	if rep.Walk != nil {
		return printWalk(cmd, *rep.Walk)
	}
	if rep.Held {
		fmt.Fprintf(cmd.OutOrStdout(), "index: not folded: %s holds the writer lease\n", oneLine(rep.Holder))
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "index: %d folded, %d unreadable, through %s\n",
		rep.Folded, rep.Unreadable, rep.Through.UTC().Format(time.RFC3339))
	return nil
}

// printWalk says what a reindex pass did, in one line. A pass that reached a
// writer -- its own lease taken by another execution -- says who, because an
// operator who started a reindex and read nothing about it has no way to tell
// a walk that found nothing from one that never ran.
func printWalk(cmd *cobra.Command, rep runindex.WalkReport) error {
	if rep.Held {
		fmt.Fprintf(cmd.OutOrStdout(), "index: not walked: %s holds the writer lease\n", oneLine(rep.Holder))
		return nil
	}
	out := fmt.Sprintf("index: %d walked, %d unreadable, through %s",
		rep.Walked, rep.Unreadable, rep.Through.UTC().Format(time.RFC3339))
	if rep.Restarted {
		out += " (the cursor could not be read: the walk began again)"
	}
	if rep.Done {
		out += " (complete)"
	} else {
		out += " (unfinished: the next sweep continues from there)"
	}
	fmt.Fprintln(cmd.OutOrStdout(), out)
	return nil
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
