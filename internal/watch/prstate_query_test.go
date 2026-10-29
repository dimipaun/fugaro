package watch

import (
	"context"
	"testing"
	"time"
)

// TestMergeNowAnswersTheStateWords: the synchronous lookup answers what gh
// said — merged, open, and "no answer" for a failure — and none of them is an
// error to the caller. Mutation caught: reporting an unreadable answer as "not
// merged, known".
func TestMergeNowAnswersTheStateWords(t *testing.T) {
	for _, c := range []struct {
		script      string
		merged, ok  bool
		description string
	}{
		{"echo MERGED\n", true, true, "merged"},
		{"echo OPEN\n", false, true, "open"},
		{"echo CLOSED\n", false, true, "closed"},
		{"exit 1\n", false, false, "a lookup that failed"},
	} {
		calls := fakeGH(t, c.script)
		p := NewPRState(PRStateOptions{})
		merged, known := p.MergeNow(context.Background(), "https://github.com/acme/app/pull/1")
		if merged != c.merged || known != c.ok {
			t.Errorf("%s: MergeNow = %v, %v, want %v, %v", c.description, merged, known, c.merged, c.ok)
		}
		if got := ghCalls(t, calls); len(got) != 1 {
			t.Errorf("%s: gh calls = %q, want one", c.description, got)
		}
	}
}

// TestMergeNowNeverSpawnsOnAURLOurPatternRefuses: the URL is matched against
// this package's own anchored pattern before anything runs, so a Bitbucket URL,
// a URL with an argument appended and a traversal-shaped one cost a regexp and
// never a process. Mutation caught: handing the caller's string to gh, or
// skipping the parse because the answer is only used for a display field.
func TestMergeNowNeverSpawnsOnAURLOurPatternRefuses(t *testing.T) {
	calls := fakeGH(t, "echo MERGED\n")
	p := NewPRState(PRStateOptions{})
	for _, url := range []string{
		"",
		"https://bitbucket.org/acme/app/pull/1",
		"https://github.com/acme/app/pull/1; rm -rf /",
		"https://github.com/acme/../app/pull/1",
		"https://github.com/acme/app/pull/0",
		"https://evil.example/github.com/acme/app/pull/1",
	} {
		if merged, known := p.MergeNow(context.Background(), url); merged || known {
			t.Errorf("MergeNow(%q) = %v, %v, want no answer", url, merged, known)
		}
	}
	if got := ghCalls(t, calls); got != nil {
		t.Errorf("gh ran for a refused URL: %q", got)
	}
}

// TestPRRepoReadsWhatMergeNowWouldAsk: PRRepo names the repository of exactly
// the URLs MergeNow would ask about, and of no other.
func TestPRRepoReadsWhatMergeNowWouldAsk(t *testing.T) {
	if repo, ok := PRRepo("https://github.com/acme/app/pull/7"); !ok || repo != "acme/app" {
		t.Errorf("PRRepo = %q, %v, want acme/app", repo, ok)
	}
	for _, url := range []string{
		"", "https://bitbucket.org/acme/app/pull/1", "https://github.com/acme/../app/pull/1",
		"https://evil.example/github.com/acme/app/pull/1",
	} {
		if repo, ok := PRRepo(url); ok {
			t.Errorf("PRRepo(%q) = %q, want no repository", url, repo)
		}
	}
}

// TestMergeNowReadsTheCacheAndBoundsItself: an answer already in hand is that
// answer rather than a second question, a disabled resolver and a nil one ask
// nothing, and a gh that never answers ends the call at the resolver's own
// timeout instead of hanging the read.
func TestMergeNowReadsTheCacheAndBoundsItself(t *testing.T) {
	calls := fakeGH(t, "sleep 30\n")
	p := NewPRState(PRStateOptions{Timeout: 50 * time.Millisecond})
	start := time.Now()
	if merged, known := p.MergeNow(context.Background(), "https://github.com/acme/app/pull/1"); merged || known {
		t.Errorf("a gh that never answers = %v, %v, want no answer", merged, known)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("MergeNow took %s, want the resolver's own timeout", took)
	}
	if got := ghCalls(t, calls); len(got) != 1 {
		t.Fatalf("gh calls = %q, want one", got)
	}

	// The cache: a merged answer in hand is merged, and asks gh nothing.
	cached := NewPRState(PRStateOptions{})
	cached.mu.Lock()
	cached.seen["https://github.com/acme/app/pull/2"] = prAnswer{state: prMerged, at: cached.now()}
	cached.mu.Unlock()
	if merged, known := cached.MergeNow(context.Background(), "https://github.com/acme/app/pull/2"); !merged || !known {
		t.Errorf("a cached merge = %v, %v, want merged", merged, known)
	}
	if got := ghCalls(t, calls); len(got) != 1 {
		t.Errorf("a cached answer spawned gh: %q", got)
	}

	off := NewPRState(PRStateOptions{Disabled: true})
	if merged, known := off.MergeNow(context.Background(), "https://github.com/acme/app/pull/1"); merged || known {
		t.Errorf("a disabled resolver = %v, %v", merged, known)
	}
	var none *PRState
	if merged, known := none.MergeNow(context.Background(), "https://github.com/acme/app/pull/1"); merged || known {
		t.Errorf("a nil resolver = %v, %v", merged, known)
	}
}
