package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// runHistoryRollover is `budget history --rollover`: the daily move of finished
// days from the Realtime Database to Firestore, and the prune (see
// budget/rollover.go). Exit codes: 0 done (also when there is no Firestore
// database yet: history is optional until init creates it, and nothing is
// touched), 1 a refusal (wrong database, a day that cannot be pruned, ...),
// 2 a backend failure.
func runHistoryRollover(ctx context.Context, cmd *cobra.Command, getenv func(string) string, dayArg string, force bool) error {
	if err := refuseHTTP2Debug(getenv); err != nil {
		return err
	}
	var opt budget.RolloverOptions
	opt.Force = force
	if dayArg != "" {
		t, err := time.Parse("2006-01-02", dayArg)
		if err != nil {
			return userErr("--day %q is not a date (YYYY-MM-DD)", oneLine(dayArg))
		}
		d := budget.Day(t)
		opt.Day = &d
	}
	if force && opt.Day == nil {
		return userErr("--force rewrites final documents and needs --day")
	}
	env := map[string]string{}
	for _, k := range []string{envProject, envGCPProject, envFirebaseFP, envRTDBURL, envRunsBucket, envFirestoreDB} {
		if env[k] = getenv(k); env[k] == "" {
			return userErr("the history job needs %s in its environment (Terraform sets it on the job)", k)
		}
	}
	if env[envFirestoreDB] != "(default)" {
		return userErr("%s must be (default): Fugaro uses the project's default database", envFirestoreDB)
	}
	if strings.ContainsAny(env[envRunsBucket], "/: ") {
		return userErr("%s must be a bucket name", envRunsBucket)
	}
	test := historyTest
	noAuth := test != nil && test.noAuth
	if err := rtdb.ValidateURL(env[envRTDBURL], noAuth); err != nil {
		return userErr("%s: %v", envRTDBURL, err)
	}
	if err := checkDatabaseHost(env[envRTDBURL], env[envFirebaseFP]); err != nil {
		return userErr("%v", strings.Replace(err.Error(), "nothing was swept", "nothing was moved", 1))
	}

	var (
		dbAuth  rtdb.Auth
		fsSrc   oauth2.TokenSource
		fsURL   string
		now     = time.Now
		skew    = rolloverMaxSkew
		openBkt = func(ctx context.Context, name string) (*blobx.Bucket, error) { return blobx.Open(ctx, "gs://"+name) }
	)
	if test != nil {
		now, fsURL, skew = test.now, test.firestore, 0
		if test.skew {
			skew = rolloverMaxSkew
		}
		if test.bucket != nil {
			openBkt = test.bucket
		}
	}
	if noAuth && fsURL == "" {
		return userErr("no-auth wiring without a Firestore endpoint: refusing to reach the real Firestore without credentials")
	}
	if noAuth {
		dbAuth = rtdb.Auth{IDToken: func() string { return "" }}
		fsSrc = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"})
	} else {
		ts, err := google.DefaultTokenSource(ctx, budgetScopes...)
		if err != nil {
			return remote(fmt.Errorf("no credentials for the budget database: %w", err))
		}
		dbAuth = rtdb.Auth{Source: ts}
		if fsSrc, err = google.DefaultTokenSource(ctx, cloudPlatform); err != nil {
			return remote(fmt.Errorf("no credentials for Firestore: %w", err))
		}
	}
	db, err := rtdb.New(env[envRTDBURL], dbAuth)
	if err != nil {
		return userErr("%s: %v", envRTDBURL, err)
	}
	// The one thing this job does to the database is destructive — it moves
	// finished days out and then DELETES their nodes — so which database it may
	// do that to is asked of the database and not of the names beside it. The
	// job's environment is Terraform's, not a config file's, but the two names
	// still decide it: an installation created before 2026-10-23 may have had a
	// Firebase project of its own, other than its GCP project (the job carries
	// both, history.tf), and a database that is not this installation's is not
	// one to prune.
	//
	// It is the SAME question, and now the same function, that the stages of
	// `fugaro update` ask (budgetDBUnadopted): the environment is read as the
	// config that predicate takes (rolloverConfig) and the answer is asked of
	// every shape, not only of a job whose two names differ. One predicate is
	// what keeps a database the rules stage skips from coming out writable for
	// this job, and this job is the last place a drift between the two would
	// ever be noticed: it DELETES what it moves out. The gate above
	// (checkDatabaseHost) still refuses the mismatched pair before a client is
	// built; what is left for the predicate is the database's own mark
	// (infra.DB.Check), which an EMPTY database passes whatever project its URL
	// belongs to — that is why it is asked and not assumed from the pair.
	//
	// It is a refusal and not a skip, unlike those stages: this job is the whole
	// of what it does, and there is nowhere to carry on to.
	if why := budgetDBUnadopted(ctx, historyJobConfig(env), infra.NewDB(db, env[envProject], env[envGCPProject], ""), "nothing was moved", selfCommand()+" update"); why != "" {
		return userErr("%v", oneLine(why))
	}
	fs, err := firestore.New(fsURL, env[envFirebaseFP], fsSrc)
	if err != nil {
		return userErr("%v", err)
	}
	bucket, err := openBkt(ctx, env[envRunsBucket])
	if err != nil {
		return remote(err)
	}
	defer bucket.Close()

	limit := time.Now().Add(rolloverStartLimit)
	warn := func(s string) { fmt.Fprintln(cmd.ErrOrStderr(), "rollover: "+oneLine(s)) }
	r := &budget.Roller{
		DB: db, FS: fs, Project: env[envProject], Now: now, MaxSkew: skew, Warn: warn,
		StartLimit: limit,
		Facts:      budget.BucketFacts{Bucket: bucket.Bucket, Warn: warn},
	}
	rep, err := r.Rollover(ctx, opt)
	out := cmd.OutOrStdout()
	for _, d := range rep.Days {
		fmt.Fprintln(out, oneLine(d.Line()))
	}
	fmt.Fprintln(out, oneLine(rep.Summary()))
	if rep.NoFirestore {
		// --firebase-project takes only the installation's own GCP project
		// (checkFirebaseProject), so naming the Firebase project this job reads
		// when the two differ — an installation that had a Firebase project of
		// its own before 2026-10-23, the one shape whose marked database got
		// past the gate above — prints a command that is refused before it
		// reads anything. The way to a Firestore database this job can write is
		// the move `fugaro update`'s skip names.
		if env[envFirebaseFP] == env[envGCPProject] {
			fmt.Fprintln(cmd.ErrOrStderr(), "WARNING: no Firestore database: spend history is not being archived; run fugaro update --firebase-project "+oneLine(env[envGCPProject]))
		} else {
			fmt.Fprintln(cmd.ErrOrStderr(), fmt.Sprintf("WARNING: no Firestore database: spend history is not being archived, and --firebase-project takes only the installation's own GCP project (%s), not the Firebase project this job reads (%s): drop the config's budget.firebase_project (and the budget.rtdb_url beside it) and run fugaro update --firebase-project %s once, which builds this job's databases in that project", oneLine(env[envGCPProject]), oneLine(env[envFirebaseFP]), oneLine(env[envGCPProject])))
		}
	}
	switch {
	case err == nil:
		return nil
	case budget.OnlyRefusals(err):
		return userErr("%s", oneLine(err.Error()))
	}
	return remote(fmt.Errorf("%s", oneLine(err.Error())))
}

// historyJobConfig is the history job's environment read as the config the
// ownership predicate takes (budgetDBUnadopted). The job has no config file —
// Terraform sets its environment, history.tf — but the names it carries are the
// same two facts, and handing them to the one predicate is what makes the job's
// answer and the stages' answer the same answer rather than two that agree
// today. Both halves of the job read it: --rollover before it moves and deletes
// days, --sweep before it deletes users.
func historyJobConfig(env map[string]string) *localcfg.Config {
	return &localcfg.Config{
		Name:       env[envProject],
		GCPProject: env[envGCPProject],
		Budget: &localcfg.Budget{
			FirebaseProject: env[envFirebaseFP],
			RTDBURL:         env[envRTDBURL],
		},
	}
}
