package issues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/issueresolve"
)

// resolveEnv is an environment whose cache lives in dir, so a pass under test
// writes its cursor there and never into the machine's own cache directory.
func resolveEnv(dir string) func(string) string {
	return func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
}

// resolveDeps is one pass's wiring for a test: a bucket, a cursor in a
// directory of this test's own, a person's session and the fixture clock.
func resolveDeps(t *testing.T, b *blobx.Bucket, r Reader) ResolveDeps {
	t.Helper()
	return ResolveDeps{
		Bucket:      b,
		Reader:      r,
		Cursor:      issueresolve.LoadCursor(resolveEnv(t.TempDir())),
		AgentMarker: func() string { return "" },
		Now:         func() time.Time { return sharedAt },
	}
}

// fakeReader is GitHub as a test says it is: the answer it gives for a number,
// and the numbers it was asked about, in the order it was asked.
type fakeReader struct {
	asked  []int
	answer func(ctx context.Context, number int) (issueresolve.State, error)
}

func (f *fakeReader) Fetch(ctx context.Context, number int) (issueresolve.State, error) {
	f.asked = append(f.asked, number)
	return f.answer(ctx, number)
}

// answering is a fakeReader that knows one answer per number and shows every
// other issue as open.
func answering(known map[int]issueresolve.State) *fakeReader {
	return &fakeReader{answer: func(_ context.Context, number int) (issueresolve.State, error) {
		if state, ok := known[number]; ok {
			return state, nil
		}
		return issueresolve.Open{}, nil
	}}
}

// filedAt files one issue in the store and answers its number and the URL it
// was filed at: the shape a pass asks GitHub about (F28).
func filedAt(t *testing.T, ctx context.Context, b *blobx.Bucket, hexPair string) (int, string) {
	t.Helper()
	up, url := filedIssue(t, ctx, b, fingerprint(hexPair))
	return up.Number, url
}

// rewriteURL puts url on a filed record without going through the store's own
// writer: it is how a test seeds a record some launcher rewrote, which is what
// a store every launcher can write has to survive (F24).
func rewriteURL(t *testing.T, ctx context.Context, b *blobx.Bucket, number int, url string) {
	t.Helper()
	rec, v, err := readShared(ctx, b, number)
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", number, err)
	}
	rec.Issue.URL = url
	if err := rec.setRecord(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replaceRaw(ctx, b, recordKey(number), raw, v); err != nil {
		t.Fatalf("rewriting the link of issue %d: %v", number, err)
	}
}

// A filed issue GitHub shows closed as completed with a milestone at or below
// the release that was installed is fixed: the record goes to `resolved` with
// the release that shipped the fix — the milestone, not the version just
// installed (F4) — and the finding comes back for the update to print.
func TestResolveMarksClosedCompletedAtOrBelowInstalled(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	below, belowURL := filedAt(t, ctx, b, "61")
	equal, _ := filedAt(t, ctx, b, "62")
	reader := answering(map[int]issueresolve.State{
		below: issueresolve.ClosedCompleted{Milestone: "0.21.0"},
		equal: issueresolve.ClosedCompleted{Milestone: "0.22.0"},
	})

	res, err := Resolve(ctx, resolveDeps(t, b, reader), "0.22.0")
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if res.Skipped != "" {
		t.Errorf("a pass over a reachable store was skipped: %q", res.Skipped)
	}
	if len(res.Fixed) != 2 {
		t.Fatalf("the pass found %+v, want the two issues closed as completed", res.Fixed)
	}
	// The order the issues were asked about is the cursor's; what comes back
	// is in number order, so the update's line reads the way a person expects.
	if res.Fixed[0].Number != below || res.Fixed[1].Number != equal {
		t.Errorf("the findings came back as %d then %d, want %d then %d",
			res.Fixed[0].Number, res.Fixed[1].Number, below, equal)
	}
	if res.Fixed[0].Release != "0.21.0" || res.Fixed[0].URL != belowURL {
		t.Errorf("issue %d is reported as %q in %q, want 0.21.0 in %q",
			below, res.Fixed[0].Release, res.Fixed[0].URL, belowURL)
	}
	if res.Fixed[0].HitByLine != "hit by ana@example.test" {
		t.Errorf("the reporter that filed issue %d is printed as %q", below, res.Fixed[0].HitByLine)
	}
	for number, release := range map[int]string{below: "0.21.0", equal: "0.22.0"} {
		got := recordAt(t, ctx, b, number)
		if got.Status != StatusResolved || got.ResolvedIn != release {
			t.Errorf("issue %d is %q in %q, want resolved in %q", number, got.Status, got.ResolvedIn, release)
		}
		stampIs(t, "resolved_at", got.ResolvedAt, sharedAt)
	}
	if res.Quiet != 0 {
		t.Errorf("the pass counted %d issues as closed without a fix", res.Quiet)
	}
}

// A milestone above the installed release names a release this installation
// does not have yet: the issue is a later update's to resolve, and this pass
// leaves it exactly as it is (F4).
func TestResolveLeavesMilestoneAboveInstalled(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	number, _ := filedAt(t, ctx, b, "63")
	reader := answering(map[int]issueresolve.State{
		number: issueresolve.ClosedCompleted{Milestone: "0.23.0"},
	})

	res, err := Resolve(ctx, resolveDeps(t, b, reader), "0.22.0")
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if len(res.Fixed) != 0 || res.Quiet != 0 || res.Skipped != "" {
		t.Errorf("an issue fixed after this release gave %+v", res)
	}
	if got := recordAt(t, ctx, b, number); got.Status != StatusSubmitted {
		t.Errorf("issue %d is %q, want it left submitted for a later update", number, got.Status)
	}
}

// An issue GitHub closed as not planned, or as a duplicate, is set aside with
// the reason it closed and counted rather than listed: a closure that fixes
// nothing is nothing an update has to say (F9, D1752).
func TestResolveNotPlannedAndDuplicateAreQuiet(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	notPlanned, _ := filedAt(t, ctx, b, "64")
	duplicate, _ := filedAt(t, ctx, b, "65")
	reader := answering(map[int]issueresolve.State{
		notPlanned: issueresolve.ClosedNotPlanned{},
		duplicate:  issueresolve.ClosedDuplicate{},
	})

	res, err := Resolve(ctx, resolveDeps(t, b, reader), "0.22.0")
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if len(res.Fixed) != 0 {
		t.Errorf("an issue closed without a fix was listed as fixed: %+v", res.Fixed)
	}
	if res.Quiet != 2 {
		t.Errorf("the pass counted %d quiet closures, want 2", res.Quiet)
	}
	for number, reason := range map[int]ClosedAs{notPlanned: ClosedNotPlanned, duplicate: ClosedDuplicate} {
		got := recordAt(t, ctx, b, number)
		if got.Status != StatusDismissed || got.ClosedAs != reason {
			t.Errorf("issue %d is %q as %q, want dismissed as %q", number, got.Status, got.ClosedAs, reason)
		}
		if got.ResolvedIn != "" || got.ResolvedAt != nil {
			t.Errorf("issue %d was recorded as fixed by %q", number, got.ResolvedIn)
		}
	}
}

// An issue GitHub decided nothing about — still open, or closed as completed
// with no release milestone, which the reader reports as no verdict (F15) —
// stays submitted: this pass records only what GitHub said.
func TestResolveNoMilestoneNoVerdictStaysSubmitted(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	open, _ := filedAt(t, ctx, b, "66")
	undecided, _ := filedAt(t, ctx, b, "67")
	reader := answering(map[int]issueresolve.State{
		open:      issueresolve.Open{},
		undecided: issueresolve.NoVerdict{},
	})

	res, err := Resolve(ctx, resolveDeps(t, b, reader), "0.22.0")
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if len(res.Fixed) != 0 || res.Quiet != 0 || res.Skipped != "" {
		t.Errorf("an issue GitHub decided nothing about gave %+v", res)
	}
	for _, number := range []int{open, undecided} {
		if got := recordAt(t, ctx, b, number); got.Status != StatusSubmitted {
			t.Errorf("issue %d is %q, want it left submitted", number, got.Status)
		}
	}
}

// A rate limit is not one issue's: every later ask would be refused the same
// way, so the pass ends rather than spending what is left of its budget on
// answers it already knows (F8). The issue behind the refusal is asked about
// next time — its number is recorded as asked either way.
func TestResolveStopsOnRateLimit(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	first, _ := filedAt(t, ctx, b, "68")
	refused, _ := filedAt(t, ctx, b, "69")
	third, _ := filedAt(t, ctx, b, "6a")
	reader := &fakeReader{answer: func(_ context.Context, number int) (issueresolve.State, error) {
		if number == refused {
			return issueresolve.NoVerdict{}, fmt.Errorf("%w: GitHub answered 403", issueresolve.ErrRateLimited)
		}
		return issueresolve.Open{}, nil
	}}

	res, err := Resolve(ctx, resolveDeps(t, b, reader), "0.22.0")
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if want := []int{first, refused}; !slices.Equal(reader.asked, want) {
		t.Errorf("the pass asked about %v, want %v: a rate limit ends it", reader.asked, want)
	}
	if res.Skipped == "" {
		t.Error("a rate-limited pass said nothing about it")
	}
	if got := recordAt(t, ctx, b, third); got.Status != StatusSubmitted {
		t.Errorf("issue %d is %q, want it left submitted", third, got.Status)
	}
}

// A machine that cannot reach GitHub cannot reach it for the next issue
// either: the first transport error ends the pass, so an offline update costs
// one timeout rather than twenty (F8).
func TestResolveStopsOnFirstTransportError(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	first, _ := filedAt(t, ctx, b, "6b")
	unreachable, _ := filedAt(t, ctx, b, "6c")
	third, _ := filedAt(t, ctx, b, "6d")
	reader := &fakeReader{answer: func(_ context.Context, number int) (issueresolve.State, error) {
		if number == unreachable {
			return issueresolve.NoVerdict{}, fmt.Errorf("%w: no route to the host", issueresolve.ErrTransport)
		}
		return issueresolve.Open{}, nil
	}}

	res, err := Resolve(ctx, resolveDeps(t, b, reader), "0.22.0")
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if want := []int{first, unreachable}; !slices.Equal(reader.asked, want) {
		t.Errorf("the pass asked about %v, want %v: the first transport error ends it", reader.asked, want)
	}
	if res.Skipped == "" {
		t.Error("a pass that could not reach GitHub said nothing about it")
	}
	if got := recordAt(t, ctx, b, third); got.Status != StatusSubmitted {
		t.Errorf("issue %d is %q, want it left submitted", third, got.Status)
	}
}

// An issue that stays open for months must not keep a newer one from ever
// being reached (F8): the pass asks the issues it has left alone longest
// first, so the twenty-fifth open issue is passed over for the new closed one
// on the very next pass.
func TestResolveNewIssuesAreNotStarvedByOldOpenOnes(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	for i := 0; i < passIssues+5; i++ {
		filedAt(t, ctx, b, fmt.Sprintf("%02x", i))
	}
	fresh, _ := filedAt(t, ctx, b, "ff")

	first := answering(map[int]issueresolve.State{})
	deps := resolveDeps(t, b, first)
	if _, err := Resolve(ctx, deps, "0.22.0"); err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if len(first.asked) != passIssues {
		t.Errorf("the first pass asked about %d issues, want %d", len(first.asked), passIssues)
	}
	if slices.Contains(first.asked, fresh) {
		t.Fatalf("the first pass reached the new issue %d: %v", fresh, first.asked)
	}

	second := answering(map[int]issueresolve.State{fresh: issueresolve.ClosedCompleted{Milestone: "0.22.0"}})
	deps.Reader = second
	res, err := Resolve(ctx, deps, "0.22.0")
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if len(res.Fixed) != 1 || res.Fixed[0].Number != fresh {
		t.Errorf("the second pass found %+v, want issue %d", res.Fixed, fresh)
	}
	if got := recordAt(t, ctx, b, fresh); got.Status != StatusResolved {
		t.Errorf("issue %d is %q, want it resolved by the second pass", fresh, got.Status)
	}
}

// A panic anywhere in the pass is one line in the result and not an update
// that stops half way: the reader decodes answers from GitHub and the store is
// writable by every launcher, and neither may take the upgrade down (F7).
func TestResolvePanicIsSkipped(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	filedAt(t, ctx, b, "6e")
	reader := &fakeReader{answer: func(context.Context, int) (issueresolve.State, error) {
		panic("the reader broke")
	}}

	res, err := Resolve(ctx, resolveDeps(t, b, reader), "0.22.0")
	if err != nil {
		t.Fatalf("a panic in the reader came back as an error: %v", err)
	}
	if res.Skipped == "" {
		t.Error("a panic in the reader was swallowed without a word")
	}
}

// A record whose link is not this project's issue is not one to ask GitHub
// about (F28): the store is writable by every launcher, and the number the
// request is built from has to be one this project filed. A record with no
// link at all is the same — and a record whose link is another project's is
// left out of the store's own listing, so the pass never even sees it.
func TestResolveIgnoresForeignURLShape(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	good, goodURL := filedAt(t, ctx, b, "6f")
	unlinked, _ := filedAt(t, ctx, b, "70")
	foreign, _ := filedAt(t, ctx, b, "71")
	rewriteURL(t, ctx, b, unlinked, "")
	rewriteURL(t, ctx, b, foreign, "https://github.com/someone-else/project/issues/9")
	reader := answering(map[int]issueresolve.State{good: issueresolve.ClosedCompleted{Milestone: "0.22.0"}})

	res, err := Resolve(ctx, resolveDeps(t, b, reader), "0.22.0")
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if want := []int{good}; !slices.Equal(reader.asked, want) {
		t.Errorf("the pass asked about %v, want only %v", reader.asked, want)
	}
	if len(res.Fixed) != 1 || res.Fixed[0].Number != good || res.Fixed[0].URL != goodURL {
		t.Errorf("the pass found %+v, want issue %d at %s", res.Fixed, good, goodURL)
	}
	if got := recordAt(t, ctx, b, unlinked); got.Status != StatusSubmitted {
		t.Errorf("the unlinked issue is %q, want it left submitted", got.Status)
	}
	rows, err := ListShared(ctx, b)
	if err != nil {
		t.Fatalf("listing the shared store: %v", err)
	}
	for _, row := range rows {
		if row.Number == foreign {
			t.Errorf("the store listed issue %d, whose link is another project's", foreign)
		}
	}
}

// A coding agent's session writes nothing: the store's transitions are writes
// to shared state and a session's writes are the person's own (F16d). The
// findings still come back, for whoever reads the session's output.
func TestResolveAgentSessionWritesNothingButReturnsFindings(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	fixedNumber, _ := filedAt(t, ctx, b, "72")
	quietNumber, _ := filedAt(t, ctx, b, "73")
	reader := answering(map[int]issueresolve.State{
		fixedNumber: issueresolve.ClosedCompleted{Milestone: "0.22.0"},
		quietNumber: issueresolve.ClosedNotPlanned{},
	})
	deps := resolveDeps(t, b, reader)
	deps.AgentMarker = func() string { return agentenv.AgentMarkers[0] }

	res, err := Resolve(ctx, deps, "0.22.0")
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if len(res.Fixed) != 1 || res.Fixed[0].Number != fixedNumber {
		t.Errorf("an agent's session found %+v, want issue %d", res.Fixed, fixedNumber)
	}
	if res.Quiet != 1 {
		t.Errorf("an agent's session counted %d quiet closures, want 1", res.Quiet)
	}
	for _, number := range []int{fixedNumber, quietNumber} {
		if got := recordAt(t, ctx, b, number); got.Status != StatusSubmitted {
			t.Errorf("a coding agent's session wrote %q on issue %d", got.Status, number)
		}
	}
}

// An error that is not GitHub being unreachable is one issue's: it is noted
// and the issues behind it are still asked about, so one answer nobody could
// use does not cost the rest of the pass.
func TestResolveReaderErrorIsSkipped(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	broken, _ := filedAt(t, ctx, b, "74")
	fixedNumber, _ := filedAt(t, ctx, b, "75")
	reader := &fakeReader{answer: func(_ context.Context, number int) (issueresolve.State, error) {
		if number == broken {
			return issueresolve.NoVerdict{}, errors.New("the reader was handed something it could not use")
		}
		return issueresolve.ClosedCompleted{Milestone: "0.22.0"}, nil
	}}

	res, err := Resolve(ctx, resolveDeps(t, b, reader), "0.22.0")
	if err != nil {
		t.Fatalf("an error the reader returned came back as the pass's: %v", err)
	}
	if want := []int{broken, fixedNumber}; !slices.Equal(reader.asked, want) {
		t.Errorf("the pass asked about %v, want %v", reader.asked, want)
	}
	if res.Skipped == "" {
		t.Error("an error the reader returned was swallowed without a word")
	}
	if len(res.Fixed) != 1 || res.Fixed[0].Number != fixedNumber {
		t.Errorf("the pass found %+v, want issue %d", res.Fixed, fixedNumber)
	}
}

// Two machines resolve one issue at once and that is not a failure: the
// transition is idempotent, so the pass that lost the race still reports the
// issue as fixed and moves no date the first machine wrote (F18).
func TestResolveAlreadyResolvedByAnotherMachineIsNotAnError(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	number, url := filedAt(t, ctx, b, "76")
	other := sharedAt.Add(time.Minute)
	reader := &fakeReader{answer: func(c context.Context, n int) (issueresolve.State, error) {
		// The other machine gets there between this pass's listing and its
		// own write.
		if err := SetResolved(c, b, n, "0.22.0", 0, other); err != nil {
			t.Fatalf("the other machine's write: %v", err)
		}
		return issueresolve.ClosedCompleted{Milestone: "0.22.0"}, nil
	}}

	res, err := Resolve(ctx, resolveDeps(t, b, reader), "0.22.0")
	if err != nil {
		t.Fatalf("losing the race came back as an error: %v", err)
	}
	if res.Skipped != "" {
		t.Errorf("losing the race was reported as a skip: %q", res.Skipped)
	}
	if len(res.Fixed) != 1 || res.Fixed[0].Number != number || res.Fixed[0].URL != url {
		t.Errorf("the pass found %+v, want issue %d at %s", res.Fixed, number, url)
	}
	got := recordAt(t, ctx, b, number)
	if got.Status != StatusResolved || got.ResolvedIn != "0.22.0" {
		t.Errorf("issue %d is %q in %q, want resolved in 0.22.0", number, got.Status, got.ResolvedIn)
	}
	stampIs(t, "resolved_at", got.ResolvedAt, other)
}

// Who hit an issue is the reporter that filed it plus everyone an occurrence
// names (F10). The names are strings the whole team's launchers write, so they
// go through safetext, each one once, and the line names the first five and
// counts the rest.
func TestResolveHitByIsSafeAndCapped(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	number, _ := filedAt(t, ctx, b, "77")
	// The record that filed it is ana@example.test (the fixture's reporter),
	// and the name of the first hit carries a sequence a terminal would obey.
	for i, h := range []struct{ run, reporter string }{
		{"update-20261107183200-48220", "evil\x1b[2Jname"},
		{"update-20261107183201-48221", "bo@example.test"},
		{"update-20261107183202-48222", "cy@example.test"},
		{"update-20261107183203-48223", "dee@example.test"},
		{"update-20261107183204-48224", "eli@example.test"},
		{"update-20261107183205-48225", "fay@example.test"},
		{"update-20261107183206-48226", "ana@example.test"},
	} {
		upload(t, ctx, b, hit(h.run, fingerprint("77"), h.reporter, "0.22.0", sharedAt.Add(time.Duration(i+1)*time.Minute)))
	}
	reader := answering(map[int]issueresolve.State{number: issueresolve.ClosedCompleted{Milestone: "0.22.0"}})

	res, err := Resolve(ctx, resolveDeps(t, b, reader), "0.22.0")
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if len(res.Fixed) != 1 {
		t.Fatalf("the pass found %+v, want issue %d", res.Fixed, number)
	}
	want := []string{"ana@example.test", "evilname", "bo@example.test", "cy@example.test", "dee@example.test"}
	if got := res.Fixed[0].HitBy; !slices.Equal(got, want) {
		t.Errorf("the people who hit issue %d are %v, want %v", number, got, want)
	}
	if got, want := res.Fixed[0].HitByLine, "hit by ana@example.test, evilname, bo@example.test, cy@example.test, dee@example.test and 2 more"; got != want {
		t.Errorf("the hit-by line is %q, want %q", got, want)
	}
	if strings.ContainsAny(res.Fixed[0].HitByLine, "\x1b\x07") {
		t.Error("an escape sequence reached the line a terminal prints")
	}
}

// A pass is bounded in all (F7): it asks GitHub with a deadline of its own,
// and a budget that runs out ends it rather than holding an update up.
func TestResolveTimeBound(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	filedAt(t, ctx, b, "78")
	var left time.Duration
	reader := &fakeReader{answer: func(c context.Context, _ int) (issueresolve.State, error) {
		d, ok := c.Deadline()
		if !ok {
			t.Error("the pass asked GitHub with no bound at all")
			return issueresolve.Open{}, nil
		}
		left = time.Until(d)
		return issueresolve.Open{}, nil
	}}
	if _, err := Resolve(ctx, resolveDeps(t, b, reader), "0.22.0"); err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if left <= 0 || left > passBudget {
		t.Errorf("the pass bounded itself by %s, want at most %s", left, passBudget)
	}

	expired, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	blocked := &fakeReader{answer: func(c context.Context, _ int) (issueresolve.State, error) {
		<-c.Done()
		return issueresolve.NoVerdict{}, fmt.Errorf("%w: %v", issueresolve.ErrTransport, c.Err())
	}}
	start := time.Now()
	res, err := Resolve(expired, resolveDeps(t, b, blocked), "0.22.0")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("a pass whose budget ran out returned an error: %v", err)
	}
	if res.Skipped == "" {
		t.Error("a pass whose budget ran out said nothing about it")
	}
	if elapsed > 5*time.Second {
		t.Errorf("the pass took %s to give up on an ask nobody was waiting for", elapsed)
	}
}
