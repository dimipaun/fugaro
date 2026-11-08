//go:build unix

package srctree

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestCacheDirRefusesAnotherUsersDirectory pins the ownership half of the
// cache check without a second account: a directory the process does not own
// decides what the agent reads, and it can be replaced after the check, which
// is the one thing the 0700 mode cannot defend against.
func TestCacheDirRefusesAnotherUsersDirectory(t *testing.T) {
	r := newRemote(t, "v1.0.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	if err := os.MkdirAll(cacheRootOf(t, o), cacheDirMode); err != nil {
		t.Fatal(err)
	}
	was := euid
	euid = func() int { return was() + 1 }
	t.Cleanup(func() { euid = was })
	_, _, err := Ensure(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "not this user's directory") {
		t.Fatalf("err = %v", err)
	}
}
