package runner

import (
	"context"

	"github.com/dimipaun/fugaro/internal/gitops"
)

// FailOnShowRepoFile makes a read of a base file through git show call fail
// and returns the restore func.
func FailOnShowRepoFile(fail func()) func() {
	old := showRepoFile
	showRepoFile = func(context.Context, *gitops.Repo, string, string) ([]byte, error) {
		fail()
		return nil, nil
	}
	return func() { showRepoFile = old }
}
