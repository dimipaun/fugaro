package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gocloud.dev/blob"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runmsg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// tell_test.go is RM1: `fugaro tell` writes one message under the run's own
// prefix and refuses, before writing anything, every run that could not read
// it (docs/run-messages F9, F12, F14, F17, F21, F24, F25).
//
// The world is the launch-budget fixture: a file:// runs bucket, the fakes and
// the roles database, so a case that refuses can also read the bucket back and
// prove the refusal left nothing behind. The run's record is written by the
// test rather than by a runner, because what tell reads is exactly that block.

const tellRunID = "20260927-100000-abcd"

// tellRecord is the record of a run whose runner reads messages: status
// running, stage implement, and the block RM2 writes at bootstrap. Its fields
// are the ones tell reads, and the rest are what a running record carries.
func tellRecord(id string, m *runmsg.Record) *runstore.Record {
	return &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusRunning, Stage: "implement", Messages: m}
}

// tellRig is the world a sending case runs in: the launch-budget fixture, one
// launched run, and that run's record as a case asks for.
func tellRig(t *testing.T, m *runmsg.Record) (*launchBudgetFixture, string) {
	t.Helper()
	f := newLaunchBudget(t, "off")
	id := tellRunID
	seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
	writeRecord(t, f.cloudFixture, id, tellRecord(id, m))
	return f, id
}

// tellAccepting is the block a run whose recipe has a coder stage and whose
// runner honours no flag yet writes: what every sending case before RM4's
// flags needs.
func tellAccepting() *runmsg.Record {
	return &runmsg.Record{Accepting: true, Format: runmsg.Format}
}

// tellRef is how a case names the run: the full form, which is what tell takes
// without a lookup.
func tellRef(id string) string { return appSlug + "/" + id }

// tellBucket opens the fixture's runs bucket.
func tellBucket(t *testing.T, f *cloudFixture) *blob.Bucket {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// tellPrefixKeys is every object under the run's message prefix: what a case
// reads to prove a refusal wrote nothing, or that a send wrote exactly one.
func tellPrefixKeys(t *testing.T, f *cloudFixture, id string) []string {
	t.Helper()
	return tellKeys(t, tellBucket(t, f), runmsg.Prefix(appSlug, id))
}

// tellKeys is every object key under prefix.
func tellKeys(t *testing.T, b *blob.Bucket, prefix string) []string {
	t.Helper()
	it := b.List(&blob.ListOptions{Prefix: prefix})
	var keys []string
	for {
		obj, err := it.Next(context.Background())
		if err != nil {
			break
		}
		keys = append(keys, obj.Key)
	}
	return keys
}

// tellSent reads back the message numbered seq through the store, which is the
// reader a runner uses: a message is what that reader makes of the object.
func tellSent(t *testing.T, f *cloudFixture, id string, seq int) *runmsg.Message {
	t.Helper()
	m, err := runmsg.Next(context.Background(), blobx.Wrap(tellBucket(t, f)), appSlug, id, seq)
	if err != nil {
		t.Fatalf("reading message %d back: %v", seq, err)
	}
	if m == nil {
		t.Fatalf("message %d is not in the run's prefix: %v", seq, tellPrefixKeys(t, f, id))
	}
	return m
}

func TestTellWrites(t *testing.T) {
	f, id := tellRig(t, tellAccepting())
	out, errOut, err := execute(t, "tell", tellRef(id), "check the flaky test before the fix")
	if err != nil {
		t.Fatalf("tell: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "sent message 1 to "+task.ShortRun(tellRef(id))) {
		t.Errorf("tell printed %q, want the message's number and the run it went to", out)
	}
	// The object is at the path RM0 defines, under tokens/ and never under
	// runs/ (D2941): the prefix is the whole of a message's authentication.
	if keys := tellPrefixKeys(t, f.cloudFixture, id); len(keys) != 1 || keys[0] != runmsg.Path(appSlug, id, 1) {
		t.Fatalf("the run's message prefix holds %v, want exactly %s", keys, runmsg.Path(appSlug, id, 1))
	}
	for _, k := range tellKeys(t, tellBucket(t, f.cloudFixture), "runs/"+appSlug+"/"+id+"/") {
		if strings.HasSuffix(k, runmsg.Suffix) {
			t.Errorf("a message object is under the run's own writable prefix: %s", k)
		}
	}
	m := tellSent(t, f.cloudFixture, id, 1)
	if m.Text != "check the flaky test before the fix" || m.Seq != 1 {
		t.Errorf("message = %+v, want the text that was typed at number 1", m)
	}
	if m.Version != runmsg.Format {
		t.Errorf("message version = %d, want the format the run advertised (%d)", m.Version, runmsg.Format)
	}
	if m.SentBy != "someone@example.com" {
		t.Errorf("message sent_by = %q, want the config's own user", m.SentBy)
	}
	if len(m.Flags) != 0 {
		t.Errorf("message flags = %v, want none for a message sent with no flag", m.Flags)
	}
	if m.SentAt.IsZero() {
		t.Error("the message carries no send time")
	}
}

// TestTellWritesUnderTheRunsSlug: the message goes to the slug the run is
// stored under, not to one this machine's config names. A run of a repository
// the checkout does not carry is still its own run (the same rule the access
// gate follows).
func TestTellWritesUnderTheRunsSlug(t *testing.T) {
	f, id := tellRig(t, tellAccepting())
	if _, _, err := execute(t, "tell", tellRef(id), "one more round on the parser"); err != nil {
		t.Fatal(err)
	}
	if keys := tellKeys(t, tellBucket(t, f.cloudFixture), "tokens/"); len(keys) != 1 || !strings.HasPrefix(keys[0], runmsg.Prefix(appSlug, id)) {
		t.Fatalf("tokens/ holds %v, want the message under %s", keys, runmsg.Prefix(appSlug, id))
	}
}

func TestTellChecksLaunchAccess(t *testing.T) {
	f, id := tellRig(t, tellAccepting())
	accessRunNode(t, f, userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA))
	accessRunAs(t, accessViewer)

	_, _, err := execute(t, "tell", tellRef(id), "slow down")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("the viewer's message: err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{
		"nothing was sent",
		"launching " + accessRepoA,
		"needs the role operator",
		"holds the role viewer",
		accessViewer,
		"user grant " + accessViewer + " --role operator --scope " + accessRepoA,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if keys := tellPrefixKeys(t, f.cloudFixture, id); len(keys) != 0 {
		t.Errorf("a refused message was written anyway: %v", keys)
	}
}

// An operator at the run's repository may send one: the check refuses a
// credential that does not reach it, and tells nothing else to stop.
func TestTellOperatorMaySend(t *testing.T) {
	f, id := tellRig(t, tellAccepting())
	accessRunNode(t, f, userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA))
	accessRunAs(t, accessOperator)
	if _, _, err := execute(t, "tell", tellRef(id), "carry on"); err != nil {
		t.Fatalf("the operator's message: %v", err)
	}
	if keys := tellPrefixKeys(t, f.cloudFixture, id); len(keys) != 1 {
		t.Errorf("the prefix holds %v, want one message", keys)
	}
}

func TestTellRefusesOldRunner(t *testing.T) {
	// A record from before the block existed: a run whose runner predates the
	// channel, which no message can reach (F9).
	f, id := tellRig(t, nil)
	_, _, err := execute(t, "tell", tellRef(id), "stop")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"does not read messages", "before Fugaro " + messagesSince, "Nothing was sent", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if keys := tellPrefixKeys(t, f.cloudFixture, id); len(keys) != 0 {
		t.Errorf("a message was written to a run whose runner reads none: %v", keys)
	}
}

func TestTellRefusesFinished(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  *runstore.Record
	}{
		{"a succeeded run", &runstore.Record{Version: 1, RunID: tellRunID, Repo: "acme/app", Status: runstore.StatusSucceeded,
			Stage: "finalize", Messages: tellAccepting()}},
		{"a run in writeback", &runstore.Record{Version: 1, RunID: tellRunID, Repo: "acme/app", Status: runstore.StatusRunning,
			Stage: "writeback", Messages: tellAccepting()}},
		{"a cancelled run", &runstore.Record{Version: 1, RunID: tellRunID, Repo: "acme/app", Status: runstore.StatusCancelled,
			Stage: "implement", Messages: tellAccepting()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchBudget(t, "off")
			id := tellRunID
			seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
			writeRecord(t, f.cloudFixture, id, tc.rec)
			_, _, err := execute(t, "tell", tellRef(id), "one last thing")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
			}
			if !strings.Contains(err.Error(), "has finished") {
				t.Errorf("the refusal does not say the run has finished: %v", err)
			}
			if keys := tellPrefixKeys(t, f.cloudFixture, id); len(keys) != 0 {
				t.Errorf("a message was written past the last coder stage: %v", keys)
			}
		})
	}
}

// A run that has no record at all is refused as itself, not as an old runner:
// the two are different states and only one of them is the run's job image.
func TestTellRefusesNoRecordYet(t *testing.T) {
	f := newLaunchBudget(t, "off")
	id := tellRunID
	seedRun(t, f.cloudFixture, id, "", "someone@example.com", true)
	_, _, err := execute(t, "tell", tellRef(id), "hello")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "no run record yet") {
		t.Errorf("the refusal does not say the run has no record: %v", err)
	}
	if keys := tellPrefixKeys(t, f.cloudFixture, id); len(keys) != 0 {
		t.Errorf("a message was written for a run with no record: %v", keys)
	}
}

func TestTellNotAccepting(t *testing.T) {
	// F24: a review-only job writes accepting: false, because a message would
	// have no coder stage to reach.
	f, id := tellRig(t, &runmsg.Record{Accepting: false, Format: runmsg.Format})
	_, _, err := execute(t, "tell", tellRef(id), "look at the auth module too")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"reads no messages", "no coder stage", "F24"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if keys := tellPrefixKeys(t, f.cloudFixture, id); len(keys) != 0 {
		t.Errorf("a message was written to a run that reads none: %v", keys)
	}
}

func TestTellFlagsExclusive(t *testing.T) {
	f, id := tellRig(t, &runmsg.Record{Accepting: true, Format: runmsg.Format, Flags: []string{runmsg.FlagNow, runmsg.FlagWrapUp}})
	_, _, err := execute(t, "tell", tellRef(id), "stop", "--now", "--wrap-up")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"now", "wrap-up"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if keys := tellPrefixKeys(t, f.cloudFixture, id); len(keys) != 0 {
		t.Errorf("a message was written for a pair of flags: %v", keys)
	}
	f2, id2 := tellRig(t, &runmsg.Record{Accepting: true, Format: runmsg.Format, Flags: []string{runmsg.FlagWrapUp}})
	if _, _, err := execute(t, "tell", tellRef(id2), "stop", "--wrap-up"); err != nil {
		t.Fatalf("one flag alone: %v", err)
	}
	if keys := tellPrefixKeys(t, f2.cloudFixture, id2); len(keys) != 1 {
		t.Errorf("the prefix holds %v, want one message", keys)
	}
}

func TestTellFlagsRefusedUnlisted(t *testing.T) {
	// Before RM4's runner lands, a run that reads messages advertises no flag:
	// sending one would reach the agent as ordinary text, which is what the
	// refusal is for (F25).
	f, id := tellRig(t, tellAccepting())
	_, _, err := execute(t, "tell", tellRef(id), "stop now", "--now")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"--now", "does not honour it", "none", "ordinary text"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if keys := tellPrefixKeys(t, f.cloudFixture, id); len(keys) != 0 {
		t.Errorf("a refused flag was written anyway: %v", keys)
	}
	// And a flag the run does list is carried, in the run's own spelling.
	f2, id2 := tellRig(t, &runmsg.Record{Accepting: true, Format: runmsg.Format, Flags: []string{runmsg.FlagWrapUp}})
	if _, _, err := execute(t, "tell", tellRef(id2), "commit what you have", "--wrap-up"); err != nil {
		t.Fatalf("the run's own flag: %v", err)
	}
	m := tellSent(t, f2.cloudFixture, id2, 1)
	if len(m.Flags) != 1 || m.Flags[0] != runmsg.FlagWrapUp {
		t.Errorf("message flags = %v, want [%s]", m.Flags, runmsg.FlagWrapUp)
	}
}

// The run advertises the format it reads, and the message carries that number:
// a runner older than the sender refuses a shape it does not know (F25).
func TestTellWritesTheRunsFormat(t *testing.T) {
	f, id := tellRig(t, &runmsg.Record{Accepting: true, Format: 1})
	if _, _, err := execute(t, "tell", tellRef(id), "carry on"); err != nil {
		t.Fatal(err)
	}
	if m := tellSent(t, f.cloudFixture, id, 1); m.Version != 1 {
		t.Errorf("message version = %d, want the run's own 1", m.Version)
	}
	// A run advertising a format this build does not write is refused rather
	// than sent something it would not read.
	f2, id2 := tellRig(t, &runmsg.Record{Accepting: true, Format: runmsg.Format + 1})
	_, _, err := execute(t, "tell", tellRef(id2), "carry on")
	if err == nil || !strings.Contains(err.Error(), "reads message format") {
		t.Fatalf("a newer runner's format: err = %v, want a refusal", err)
	}
	if keys := tellPrefixKeys(t, f2.cloudFixture, id2); len(keys) != 0 {
		t.Errorf("a message was written in a format this build does not write: %v", keys)
	}
}

func TestTellNoTextFailsAtOnce(t *testing.T) {
	// The refusal is made at the argument check: this world holds no run at
	// all, so a lookup would answer "no run" — and the text is what a person
	// is told about (F14).
	newCloudFixture(t)
	for _, tc := range []struct{ name, text string }{
		{"empty", ""},
		{"over the cap", strings.Repeat("x", runmsg.MaxText+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := execute(t, "tell", tellRunID, tc.text)
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
			}
			if !strings.Contains(err.Error(), "the message") {
				t.Errorf("the refusal is not about the text: %v", err)
			}
			for _, not := range []string{"no run", "no active run", "could not read"} {
				if strings.Contains(err.Error(), not) {
					t.Errorf("the text was checked after the run was looked up: %v", err)
				}
			}
		})
	}
	if _, _, err := execute(t, "tell", tellRunID); err == nil {
		t.Error("tell with one argument was not refused")
	}
}

func TestTellOverCapText(t *testing.T) {
	f, id := tellRig(t, tellAccepting())
	_, _, err := execute(t, "tell", tellRef(id), strings.Repeat("x", runmsg.MaxText+1))
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"4096", "F12"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if keys := tellPrefixKeys(t, f.cloudFixture, id); len(keys) != 0 {
		t.Errorf("an over-cap message was written: %v", keys)
	}
	// A text at the cap is written: the cap is the bound, not the first
	// refused size.
	if _, _, err := execute(t, "tell", tellRef(id), strings.Repeat("x", runmsg.MaxText)); err != nil {
		t.Fatalf("a text at the cap: %v", err)
	}
	if m := tellSent(t, f.cloudFixture, id, 1); len(m.Text) != runmsg.MaxText {
		t.Errorf("the text is %d bytes, want %d", len(m.Text), runmsg.MaxText)
	}
}

func TestTellTwentyFirst(t *testing.T) {
	f, id := tellRig(t, tellAccepting())
	ctx := context.Background()
	b := blobx.Wrap(tellBucket(t, f.cloudFixture))
	for i := 0; i < runmsg.MaxMessages; i++ {
		if _, err := runmsg.Put(ctx, b, appSlug, id, runmsg.Message{Version: runmsg.Format, Text: "one"}); err != nil {
			t.Fatalf("seeding message %d: %v", i+1, err)
		}
	}
	_, _, err := execute(t, "tell", tellRef(id), "the twenty-first")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"holds the 20 messages it reads", "21st"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if keys := tellPrefixKeys(t, f.cloudFixture, id); len(keys) != runmsg.MaxMessages {
		t.Errorf("the prefix holds %d objects after the refusal, want %d", len(keys), runmsg.MaxMessages)
	}
}

// tellStorageRig is the world the F21 check needs: the cloud fixture with a
// storage endpoint holding the runs bucket and the policy a case gives it.
// No bucket policy is a bucket the fake refuses to answer for, which is the
// state a launcher's own credential is in.
func tellStorageRig(t *testing.T, policy ...gcpfake.Binding) *cloudFixture {
	t.Helper()
	g := gcpfake.NewGCS(t)
	g.AddBucket(tellRunsBucket, 123456789012, map[string]string{"fugaro": "managed"})
	g.SetBucketPolicy(tellRunsBucket, policy)
	f := newCloudFixture(t, "storage: "+g.URL+"/storage/v1/")
	id := tellRunID
	seedRun(t, f, id, "", "someone@example.com", true)
	writeRecord(t, f, id, tellRecord(id, tellAccepting()))
	return f
}

const tellRunsBucket = "unused-bucket"

// tellJobGrant is a repository's job account holding the runs/, cache/ and
// locks/ write the hardened Terraform writes, with no tokens/ read: a
// repository root that has not been re-applied since the budget token moved
// under tokens/ (F21).
func tellJobGrant() gcpfake.Binding {
	return gcpfake.Binding{Role: "roles/storage.objectUser",
		Members:   []string{"serviceAccount:fugaro-" + appSlug + "@proj-1234.iam.gserviceaccount.com"},
		Condition: &gcpfake.IAMCondition{Title: gcp.BucketConditionTitle(appSlug), Expression: gcp.BucketCondition(tellRunsBucket, gcp.JobBucketPrefixes, appSlug)}}
}

// tellTokenGrantBinding is the read the same job account holds once its
// repository's root has been re-applied.
func tellTokenGrantBinding() gcpfake.Binding {
	return gcpfake.Binding{Role: "roles/storage.objectViewer",
		Members:   []string{"serviceAccount:fugaro-" + appSlug + "@proj-1234.iam.gserviceaccount.com"},
		Condition: &gcpfake.IAMCondition{Title: gcp.TokenBucketConditionTitle(appSlug), Expression: gcp.TokenBucketCondition(tellRunsBucket, appSlug)}}
}

func TestTellRefusesNoTokenGrant(t *testing.T) {
	f := tellStorageRig(t, tellJobGrant())
	_, _, err := execute(t, "tell", tellRef(tellRunID), "carry on")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	for _, want := range []string{"no read on tokens/", "F21", "fugaro update --repo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if keys := tellPrefixKeys(t, f, tellRunID); len(keys) != 0 {
		t.Errorf("a message nobody could read was written: %v", keys)
	}
}

// The same repository once its root has been re-applied: the read is there and
// the message is sent. And a shared project job's whole-tokens/ read is the
// read for the same reason.
func TestTellSendsWithTheTokenGrant(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bind   gcpfake.Binding
		policy []gcpfake.Binding
	}{
		{"the repository's own grant", tellTokenGrantBinding(), nil},
		{"a shared project job's grant", gcpfake.Binding{Role: "roles/storage.objectViewer",
			Members:   []string{"serviceAccount:fugaro-shared@proj-1234.iam.gserviceaccount.com"},
			Condition: &gcpfake.IAMCondition{Title: gcp.ProjectTokenBucketConditionTitle("shared"), Expression: gcp.ProjectTokenBucketCondition(tellRunsBucket)}}, nil},
		{"a pre-hardening unconditional read", gcpfake.Binding{Role: "roles/storage.objectViewer",
			Members: []string{"user:o@example.com"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := append([]gcpfake.Binding{tellJobGrant(), tc.bind}, tc.policy...)
			tellStorageRig(t, policy...)
			out, errOut, err := execute(t, "tell", tellRef(tellRunID), "carry on")
			if err != nil {
				t.Fatalf("tell with the grant in place: %v (%s)", err, errOut)
			}
			if !strings.Contains(out, "sent message 1") {
				t.Errorf("tell printed %q, want the message's number", out)
			}
		})
	}
}

func TestTellPolicyUnreadableSends(t *testing.T) {
	g := gcpfake.NewGCS(t)
	g.AddBucket(tellRunsBucket, 123456789012, map[string]string{"fugaro": "managed"})
	g.RefuseBucketPolicy(tellRunsBucket) // a launcher holds no storage.buckets.getIamPolicy
	f := newCloudFixture(t, "storage: "+g.URL+"/storage/v1/")
	id := tellRunID
	seedRun(t, f, id, "", "someone@example.com", true)
	writeRecord(t, f, id, tellRecord(id, tellAccepting()))

	out, errOut, err := execute(t, "tell", tellRef(id), "carry on")
	if err != nil {
		t.Fatalf("tell with an unreadable policy: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "sent message 1") {
		t.Errorf("tell printed %q, want the message to have been sent", out)
	}
	for _, want := range []string{"was not checked", tellRunsBucket} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the note does not say %q: %q", want, errOut)
		}
	}
	if m := tellSent(t, f, id, 1); m.Text != "carry on" {
		t.Errorf("message = %+v, want the text", m)
	}
}

func TestTellJSON(t *testing.T) {
	f, id := tellRig(t, &runmsg.Record{Accepting: true, Format: runmsg.Format, Flags: []string{runmsg.FlagNow}})
	out, errOut, err := execute(t, "tell", tellRef(id), "slow down", "--now", "--json")
	if err != nil {
		t.Fatalf("tell --json: %v (%s)", err, errOut)
	}
	var got tellResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not a document: %v\n%s", err, out)
	}
	if got.Project != "aurora" || got.Run != tellRef(id) || got.Seq != 1 {
		t.Errorf("document = %+v, want the project, the run and the number", got)
	}
	if len(got.Flags) != 1 || got.Flags[0] != runmsg.FlagNow {
		t.Errorf("document flags = %v, want [%s]", got.Flags, runmsg.FlagNow)
	}
	if m := tellSent(t, f.cloudFixture, id, 1); m.Text != "slow down" {
		t.Errorf("message = %+v, want the text", m)
	}
}

// A run named by an ending is not steered: as cancel does, tell answers with
// the full run IDs the ending names and writes nothing (D1710).
func TestTellEndingSendsNothing(t *testing.T) {
	f, id := tellRig(t, tellAccepting())
	// The live-run feed cancel reads too: the run's entry under /agents, which
	// activeRuns confirms against the bucket before it believes it.
	f.db.Set("agents/"+appSlug+"/"+id, map[string]any{"repo": "acme/app", "workflow": "web", "requestedBy": "someone@example.com"})
	_, _, err := execute(t, "tell", task.ShortRun(id), "slow down")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a refusal", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "full run ID") {
		t.Errorf("the refusal does not name the way out: %v", err)
	}
	if keys := tellPrefixKeys(t, f.cloudFixture, id); len(keys) != 0 {
		t.Errorf("an ending steered a run: %v", keys)
	}
}
