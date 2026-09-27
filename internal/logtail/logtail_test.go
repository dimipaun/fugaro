package logtail

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestKeepsLastLines(t *testing.T) {
	w := New(3, 100)
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(w, "line %d\n", i)
	}
	if got := w.Lines(); !slices.Equal(got, []string{"line 3", "line 4", "line 5"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestIncludesUnfinishedLine(t *testing.T) {
	w := New(2, 100)
	w.Write([]byte("a\nb\npart"))
	w.Write([]byte("ial"))
	if got := w.String(); got != "b\npartial" {
		t.Fatalf("tail = %q", got)
	}
}

func TestLongLineIsCutAndBounded(t *testing.T) {
	w := New(2, 10)
	big := strings.Repeat("x", 1<<20)
	w.Write([]byte(big))
	w.Write([]byte(big + "\nshort\n"))
	if len(w.partial) != 0 || cap(w.partial) > 64 {
		t.Fatalf("partial buffer grew to cap %d", cap(w.partial))
	}
	if got := w.Lines(); !slices.Equal(got, []string{"xxxxxxxxxx …", "short"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestCutNeverSplitsARune(t *testing.T) {
	w := New(1, 4)
	w.Write([]byte("abcé\n")) // é is 2 bytes; only its first fits
	if got := w.String(); got != "abc …" {
		t.Fatalf("tail = %q", got)
	}
}

func TestCarriageReturnKeepsFinalRedraw(t *testing.T) {
	w := New(5, 100)
	w.Write([]byte("progress 10%\rprogress 50%\rprogress 100%\r\ndone\r\n"))
	if got := w.Lines(); !slices.Equal(got, []string{"progress 100%", "done"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestConcurrentWrites(t *testing.T) {
	w := New(1000, 100)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				w.Write([]byte("line\n"))
			}
		}()
	}
	wg.Wait()
	if n := len(w.Lines()); n != 800 {
		t.Fatalf("got %d lines, want 800", n)
	}
}

func TestClip(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"short", "short"},
		{"exactly10!", "exactly10!"},
		{"eleven byte", "eleven byt …"},
		{"aaaaaaaaaé", "aaaaaaaaa …"}, // never splits a rune
	} {
		if got := Clip(tc.in, 10); got != tc.want {
			t.Errorf("Clip(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
