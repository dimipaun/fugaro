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
	for _, k := range []string{envProject, envFirebaseFP, envRTDBURL, envRunsBucket, envFirestoreDB} {
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
		fmt.Fprintln(cmd.ErrOrStderr(), "WARNING: no Firestore database: spend history is not being archived; run fugaro init --firebase")
	}
	switch {
	case err == nil:
		return nil
	case budget.OnlyRefusals(err):
		return userErr("%s", oneLine(err.Error()))
	}
	return remote(fmt.Errorf("%s", oneLine(err.Error())))
}
