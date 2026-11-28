package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	storage "google.golang.org/api/storage/v1"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runmsg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// messagesSince is the release whose runner writes the record's messages block
// at bootstrap, and so the release a run has to have started on to read what
// this command writes (F9). The record carries no runner version — its own
// `version` is the record format's — so the block's presence is the only thing
// that tells a runner that reads messages from one that predates the channel,
// which is what makes this release worth naming in the refusal.
const messagesSince = "0.27.0"

type tellOptions struct {
	cloud       cloudOptions
	now, wrapUp bool
	asJSON      bool
}

// tellResult is what tell reports: the message's number under the run, which is
// the whole of what a sender learns here (F14). It says nothing about delivery:
// that is the run's record, and the run writes it.
type tellResult struct {
	Project string   `json:"project"` // the Fugaro project
	Run     string   `json:"run"`     // "<repo-slug>/<run-id>"
	Seq     int      `json:"seq"`     // the number the message took
	Flags   []string `json:"flags,omitempty"`
}

func newTellCmd() *cobra.Command {
	var o tellOptions
	cmd := &cobra.Command{
		Use:   `tell RUN "text"`,
		Short: "Send a message to a run that is already going",
		Long: "Tell RUN (a full run ID, or <repo-slug>/<run-id>) something.\n\n" +
			"The message is written into the run's own prefix of the runs bucket\n" +
			"(tokens/<repo-slug>/<run-id>/messages/NNNN.msg.json, where no job account\n" +
			"may write) and read by its runner at the next coder stage: a first run's\n" +
			"implement, a follow-up's implement, or a fix. A review round never sees one.\n" +
			"tell prints the number the message took and returns at once; it never waits\n" +
			"for delivery, and what became of the message is in the run's record.\n\n" +
			"Given an ending (the last four or more characters of a run ID), tell sends\n" +
			"nothing: as cancel does, it looks the ending up among the active runs and\n" +
			"refuses with the full run IDs it names, so an ending never steers a run\n" +
			"nobody named in full.\n\n" +
			"A run that is past finalize, whose runner does not read messages, or whose\n" +
			"recipe has no coder stage is refused, with the reason; so is a text over\n" +
			"4 KiB and a 21st message. --now asks for the message at the agent's next\n" +
			"quiet point, inside the coder stage that is running; --wrap-up asks the run\n" +
			"to finish: commit what it has and stop. Either flag is refused for a run\n" +
			"whose runner does not honour it.",
		Args: func(_ *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(2)(nil, args); err != nil {
				return err
			}
			// The text is checked here rather than in the flow, so a message
			// nobody can send fails before the local config is read, let alone
			// the cloud: there is no run it could be sent to (F14, "with no
			// text argument it fails at once").
			return checkTellText(args[0], args[1])
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := openCloud(cmd.Context(), o.cloud)
			if err != nil {
				return err
			}
			defer env.Close()
			return tellRun(cmd.Context(), env, &o, args[0], args[1], cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.now, "now", false, "ask for the message at the agent's next quiet point, inside the coder stage that is running")
	f.BoolVar(&o.wrapUp, "wrap-up", false, "ask the run to commit what it has and stop")
	f.BoolVar(&o.asJSON, "json", false, "print the result as JSON")
	cmd.MarkFlagsMutuallyExclusive("now", "wrap-up") // one message asks for one of the two
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// checkTellText refuses a message that carries nothing or more than a run
// reads (F12). Both are refused here and not left to the store, because this is
// where a person can still be told which of the two it is: the store's own
// refusal says how many bytes it saw, not where the cap came from.
func checkTellText(ref, text string) error {
	switch {
	case text == "":
		return userErr(`the message has no text: tell takes the words as its second argument, so quote them — fugaro tell %q "check the flaky test before the fix"`, ref)
	case len(text) > runmsg.MaxText:
		return userErr("the message is %d bytes and a run reads at most %d (finding F12): send the part that matters, or put the rest in a follow-up run", len(text), runmsg.MaxText)
	}
	return nil
}

// tellRun is tell's flow against env: everything that can refuse the message is
// checked before it is written, so a refusal leaves the run's prefix as it was
// (there is no half-sent message), and the write itself is the last thing here.
// The text it is given has been through checkTellText.
func tellRun(ctx context.Context, env *cloudEnv, o *tellOptions, ref, text string, out, errOut io.Writer) error {
	if err := checkTellText(ref, text); err != nil {
		return err
	}
	// resolveRun's active-only reading, exactly as cancel takes it (D1710): an
	// ending never yields a run here either, so a message is never steered into
	// a run by a name that could mean another one, and nothing asks a person to
	// pick (F14, the no-prompt policy).
	slug, id, err := resolveRun(ctx, env, ref, resolveRunOptions{out: out, errOut: errOut, asJSON: o.asJSON, activeOnly: true})
	if err != nil {
		return err
	}
	runRef := slug + "/" + id
	// The role check (F17) is the one `cancel` makes for ActionCancel, taken
	// here for ActionLaunch: steering a going run is what a launch does, so it
	// takes the role a launch takes, and no new action is added. It runs before
	// anything is read or written, so a refusal keeps no trace of the message.
	// It is the CLI's check and advisory (F22): IAM lets any launcher write
	// under tokens/ by hand, and what the check buys is the ordinary path
	// stopping with a sentence rather than a write that reaches nobody.
	s := runstore.Open(env.bucket.Bucket, slug, id)
	if err := requireRunAccess(ctx, env, accesslib.ActionLaunch, s, "nothing was sent"); err != nil {
		return err
	}
	rec, err := absent(s.ReadRecord(ctx))
	switch {
	case err != nil && corruptObject(err):
		// A record no reader can hold (too large, unparseable) proves nothing
		// about what this run reads, and guessing "it reads messages" would
		// write an object nobody looks at. The refusal says what is wrong
		// rather than calling it an old runner.
		return userErr("%s's record cannot be read (%s), so whether its runner reads messages cannot be told; nothing was sent. Check fugaro diagnose %s", task.ShortRun(runRef), oneLineCLI(err.Error()), task.ShortRun(runRef))
	case err != nil:
		return remote(err)
	case rec == nil:
		return userErr("%s has no run record yet: it is bootstrapping, or it ended without writing one, and neither reads a message. Nothing was sent; check fugaro runs show %s and try again if it is only just starting", task.ShortRun(runRef), task.ShortRun(runRef))
	case hasFinalized(rec):
		return userErr("%s has finished (%s), so there is no coder stage left to read a message: it would sit under the run unread and then be reaped. Nothing was sent", task.ShortRun(runRef), tellRunState(rec))
	case rec.Messages == nil:
		// F9: the block is written at bootstrap, so its absence is a run whose
		// job image predates the channel. Not an error in the record, and not
		// something this command can work around.
		return userErr("%s's runner does not read messages; it started before Fugaro %s, whose job images carry the message channel. Nothing was sent: an operator runs fugaro update, and a run launched after that reads one", task.ShortRun(runRef), messagesSince)
	case !rec.Messages.Accepting:
		// F24: a recipe with no coder stage (a review-only, artifact,
		// merge-judge, evaluate or rebase-only run) has nowhere to put a
		// message, and its runner says so rather than leaving one unread.
		return userErr("%s reads no messages: its recipe has no coder stage to carry one (finding F24). Nothing was sent; a review-only, artifact, merge-judge, evaluate or rebase-only run has nothing to tell", task.ShortRun(runRef))
	}
	m := rec.Messages
	// F25: the run advertises the format it reads and the flags it honours, and
	// a sender writes only those. A flag the runner does not list would reach
	// the agent as ordinary text, which is worse than a refusal: the person
	// believes they asked for something.
	format := m.Format
	if format < 1 || format > runmsg.Format {
		return userErr("%s reads message format %d, and this build writes format %d: nothing was sent. Run fugaro update; a CLI older than the run's runner is refused here rather than writing a message the runner would not read", task.ShortRun(runRef), format, runmsg.Format)
	}
	flags, err := tellFlags(runRef, o, m)
	if err != nil {
		return err
	}
	// F21, and last before the write: the bucket policy, read from the
	// launcher's side, held to the read the run's own job account makes with.
	// A repository whose root was never re-applied has no tokens/ read grant
	// for the job account that runs it, and GCS answers that refusal as it
	// answers a missing object — the run would read nothing and say nothing.
	// When the policy cannot be read (a launcher holds no
	// storage.buckets.getIamPolicy, which is the usual case), the message is
	// sent and one line says the grant was not checked: this never refuses for
	// want of access to the policy.
	if err := tellTokenGrant(ctx, env, s, slug, runRef, errOut); err != nil {
		return err
	}
	// The sender's name is display only and self-declared (D2940): the bucket's
	// IAM, not this field, is what makes a message unforgeable. A machine that
	// cannot name its own user still gets to send one, so the failure is not
	// carried into the message or the command.
	me, _ := env.lc.Me(ctx)
	seq, err := runmsg.Put(ctx, env.bucket, slug, id, runmsg.Message{
		Version: format,
		SentBy:  me,
		Flags:   flags,
		Text:    text,
	})
	if err != nil {
		return tellPutErr(runRef, err)
	}
	return printTell(out, o, tellResult{Project: env.lc.Name, Run: runRef, Seq: seq, Flags: flags})
}

// tellRunState is how a finished run reads in a refusal: its status, and the
// stage it was in when it wrote the record, which is what says whether it
// finalized or stopped somewhere else.
func tellRunState(rec *runstore.Record) string {
	if rec.Stage == "" {
		return string(rec.Status)
	}
	return string(rec.Status) + ", stage " + rec.Stage
}

// tellFlags is the flags the message carries, or the refusal for one the run's
// runner does not honour (F25). Both flags together are refused by the command
// itself (MarkFlagsMutuallyExclusive), because one message asks for one of the
// two, and that refusal needs no run to make.
func tellFlags(runRef string, o *tellOptions, m *runmsg.Record) ([]string, error) {
	var flags []string
	for _, f := range []struct {
		on   bool
		name string
	}{
		{o.now, runmsg.FlagNow},
		{o.wrapUp, runmsg.FlagWrapUp},
	} {
		if !f.on {
			continue
		}
		if !slices.Contains(m.Flags, f.name) {
			return nil, userErr("--%s is refused for %s: its runner does not honour it (the record lists %s), so the message would reach the agent as ordinary text. Nothing was sent; send it without the flag and the run reads it at its next coder stage", strings.ReplaceAll(f.name, "_", "-"), task.ShortRun(runRef), tellFlagsList(m.Flags))
		}
		flags = append(flags, f.name)
	}
	return flags, nil
}

// tellFlagsList names the flags a run's record lists, for the refusal above:
// "none" rather than an empty space, so the sentence reads as one either way.
func tellFlagsList(flags []string) string {
	if len(flags) == 0 {
		return "none"
	}
	return strings.Join(flags, ", ")
}

// tellTokenGrant refuses a message the run's own job account could not read
// (F21), and says so on errOut when it cannot tell.
//
// Which account that is comes from the run itself and not from the policy's
// shape: the job its launch recorded (runstore.Launch.Job), placed by the
// published policy exactly as a launch places it (projectJobOfRecord). A run on
// a shared project job reads its messages through that job account's
// whole-tokens/ grant (gcp.ProjectTokenBucketCondition, the widening plan task
// 3.2 deploys); a run on its own repository's job reads them through the
// slug-scoped gcp.TokenBucketCondition its root writes (modules/workflow/sa.tf,
// job_tokens). Pairing the run with its own job is the whole content of the
// check: a read another account holds is a read of a run this one is not, and
// the runner would find no message while the record went on saying it accepts
// them. It is the same per-account pairing infra.JobTokenGrantFindings makes,
// asked about the one account this run has.
//
// When the policy cannot be read at all — a launcher holds no
// storage.buckets.getIamPolicy, which is the usual case — the message is sent
// and one line says the read was not checked: this never refuses for want of
// access. The same goes for the two answers the run's own job cannot be read
// from (a launch no reader can hold, a published policy that cannot be read):
// which read to look for is unknown, and a refusal would be a guess.
//
// A configuration with no storage endpoint (a test's fake, or a project whose
// endpoints name none) has no policy to read at all and is passed over in
// silence, exactly as doctor's own check is.
func tellTokenGrant(ctx context.Context, env *cloudEnv, s *runstore.Store, slug, runRef string, errOut io.Writer) error {
	lc := env.lc
	if lc.Endpoints.NoAuth && lc.Endpoints.Storage == "" {
		return nil
	}
	bucket := lc.RunsBucketName()
	if bucket == "" {
		return nil
	}
	unchecked := func(err error) {
		// The shape of the path, not this run's: a person reads the run by its
		// ending (D1705), and the operator who checks the grant needs the
		// prefix the message was written under, which the store's own builder
		// spells once (RM0).
		fmt.Fprintf(errOut, "note: whether this run's own job account may read %s's messages was not checked: %s. Messages live under %s in gs://%s; a job account without that read finds no message there and the run never sees it\n",
			task.ShortRun(runRef), oneLineCLI(err.Error()), runmsg.Prefix("<repo-slug>", "<run-id>"), bucket)
	}
	want, err := tellTokenReadFor(ctx, env, s, slug, bucket)
	if err != nil {
		unchecked(err)
		return nil
	}
	svc, err := storage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Storage)...)
	if err != nil {
		unchecked(err)
		return nil
	}
	p, err := svc.Buckets.GetIamPolicy(bucket).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	if err != nil {
		unchecked(err)
		return nil
	}
	if tellReadGranted(p, want) {
		return nil
	}
	return userErr("nothing was sent: the bucket policy of gs://%s shows %s, where %s's messages are written, so the run's own job account would read the message as missing and the run would never see it (finding F21). %s",
		bucket, want.missing, task.ShortRun(runRef), want.fix)
}

// tellTokenRead is the binding this run's messages are read through: the
// condition the binding has to carry, and — for a shared project job, whose
// condition names no repository and so no account — the title and member that
// say whose binding it is. A per-repository read needs neither: its expression
// names the slug, and every job account of that repository holds it under a
// title of its own (the workflow variants differ in the title's saID and in
// nothing else), so the expression is the account scope here.
//
// missing and fix are the refusal's own two sentences about it, so that a
// person reads which read is missing and which command writes it.
type tellTokenRead struct {
	expression string
	title      string // "" for a per-repository read, whose expression already scopes it
	member     string // the member whose binding this is, likewise ""
	missing    string
	fix        string
}

// tellTokenReadFor is that read, worked out from the job the run's launch
// recorded. A launch nothing records — a run that never launched, or a
// launch.json no reader can hold — leaves the job unknown, and with it the read:
// that is an error here, and the caller sends with the note rather than
// guessing.
//
// A review-only variant's account is not derived here (credsvc owns that name),
// and does not need to be: a run whose recipe has no coder stage carries no
// accepting block, and tell refuses it before this (F24).
func tellTokenReadFor(ctx context.Context, env *cloudEnv, s *runstore.Store, slug, bucket string) (tellTokenRead, error) {
	l, _, err := recordedLaunch(ctx, s)
	if err != nil {
		return tellTokenRead{}, fmt.Errorf("the launch this run records could not be read: %w", err)
	}
	job := ""
	if l != nil {
		job = l.Job
	}
	pj, shared, err := env.projectJobOfRecord(ctx, job)
	if err != nil {
		return tellTokenRead{}, fmt.Errorf("the job this run launched on could not be placed: %w", err)
	}
	if shared {
		saID := gcp.ProjectServiceAccountID(pj.Profile, pj.Provider, pj.Account)
		member := "serviceAccount:" + saID + "@" + env.lc.GCPProject + ".iam.gserviceaccount.com"
		return tellTokenRead{
			expression: gcp.ProjectTokenBucketCondition(bucket),
			title:      gcp.ProjectTokenBucketConditionTitle(saID),
			member:     member,
			missing:    "no read of tokens/ for " + member + ", the account of the shared project job " + pj.Name + " this run launched on",
			fix:        "The installation's project jobs have not been re-applied since the budget token moved under tokens/: an operator runs fugaro update",
		}, nil
	}
	return tellTokenRead{
		expression: gcp.TokenBucketCondition(bucket, slug),
		missing:    "no read of tokens/" + slug + "/ for this repository's job accounts",
		fix:        "The repository's root has not been re-applied since the budget token moved under tokens/: an operator runs fugaro update --repo in its checkout",
	}, nil
}

// tellReadGranted reports whether p is a policy under which this run's messages
// can be read: a binding that carries an object read, carries a condition, and
// whose condition is want's — and, where want names an account, whose title or
// member is that account's too.
//
// The condition is judged and not the member, because the expression is what
// names the scope: the per-repository read names the slug, so no other
// repository's account can hold it, while the bindings a *caller* holds — every
// operator is bound roles/storage.objectAdmin on the whole bucket and every
// launcher and viewer roles/storage.objectViewer, unconditionally
// (modules/installation/iam.tf) — carry no condition at all and are therefore
// not this run's read. Judging the caller's bindings is what would make the
// refusal in tellTokenGrant unreachable on every real installation: a person who
// may tell a run anything is, in every installation, also a person who may read
// the whole bucket. Judging the whole-tokens/ expression alone is what would
// make it unreachable for a repository whose job account reads nothing once
// *any* shared project job has been deployed, since that expression says which
// prefix and not which account — hence want's title and member.
//
// A read granted some third way — the roles the project level carries, a
// hand-written condition this build does not write — is not read as granting
// it, and the refusal's own fix is the one that settles the question for good,
// because a root this build did write the grant in is the only state under
// which the run reads its messages.
func tellReadGranted(p *storage.Policy, want tellTokenRead) bool {
	for _, bd := range p.Bindings {
		if len(bd.Members) == 0 || !tellReadRoles[bd.Role] || bd.Condition == nil {
			continue
		}
		if bd.Condition.Expression != want.expression {
			continue
		}
		if want.member == "" || bd.Condition.Title == want.title || slices.Contains(bd.Members, want.member) {
			return true
		}
	}
	return false
}

// tellReadRoles are the predefined roles whose permissions include
// storage.objects.get, so a binding of one of them is a read of the objects the
// condition allows.
var tellReadRoles = map[string]bool{
	"roles/storage.objectViewer":       true,
	"roles/storage.objectAdmin":        true,
	"roles/storage.objectUser":         true,
	"roles/storage.legacyObjectReader": true,
	"roles/storage.legacyBucketReader": true,
	"roles/storage.legacyBucketOwner":  true,
	"roles/storage.admin":              true,
	"roles/owner":                      true,
	"roles/editor":                     true,
}

// tellPutErr is runmsg.Put's error as the sentence a person needs. The store's
// own refusals are the caps and the listing's state; anything else is the
// bucket's answer and stays remote.
func tellPutErr(runRef string, err error) error {
	switch {
	case errors.Is(err, runmsg.ErrEmpty):
		return userErr("the message has no text: nothing was sent to %s", task.ShortRun(runRef))
	case errors.Is(err, runmsg.ErrTooLong):
		return userErr("nothing was sent to %s: %v (finding F12)", task.ShortRun(runRef), err)
	case errors.Is(err, runmsg.ErrTooMany):
		return userErr("nothing was sent to %s: it already holds the %d messages it reads, and the 21st is refused rather than written for nobody to read (finding F12). Send it to a follow-up run with fugaro run --pr instead", task.ShortRun(runRef), runmsg.MaxMessages)
	case errors.Is(err, runmsg.ErrGap):
		return userErr("nothing was sent to %s: %v. A number below the highest was deleted, and writing past the hole would leave the runner with a number it cannot tell from a message nobody wrote yet (finding F10)", task.ShortRun(runRef), err)
	case errors.Is(err, runmsg.ErrShape):
		return remote(err)
	default:
		return remote(err)
	}
}

// printTell prints r as JSON (--json) or as the one line a person reads.
func printTell(w io.Writer, o *tellOptions, r tellResult) error {
	if o.asJSON {
		data, err := json.Marshal(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s\n", data)
		return err
	}
	// The human line names the run the way every surface shows it; the JSON
	// above keeps the whole "<slug>/<run-id>" (D1705, F8).
	what := "; its runner reads it into the next coder stage"
	switch {
	case slices.Contains(r.Flags, runmsg.FlagNow):
		what = "; its runner hands it to the agent at its next quiet point, inside the stage that is running"
	case slices.Contains(r.Flags, runmsg.FlagWrapUp):
		what = "; its runner hands it to the agent and then ends the coder stage"
	}
	_, err := fmt.Fprintf(w, "sent message %d to %s%s. It is not waited for: what became of it is in the run's own record, and fugaro diagnose %s reads it back\n",
		r.Seq, task.ShortRun(oneLine(r.Run)), what, task.ShortRun(oneLine(r.Run)))
	return err
}
