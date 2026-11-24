package cli

import (
	"context"
	"os"

	"github.com/dimipaun/fugaro/internal/versionstamp"
)

// newStampSource is the one place the CLI builds the required-CLI stamp's
// reader (internal/versionstamp, T1). Every consumer of the stamp goes through
// it — the root fence, `fugaro update`'s door, doctor's cli-below-required and
// the orchestrator daemon — and their tests replace it with a fake Source
// rather than reaching a database, which is why it is a variable and not a
// direct call.
var newStampSource = versionstamp.New

// stampOpen is the reader the stamp is read through at runtime: the same client
// the budget paths build (newBudgetClient), so the node is read as the person,
// over ADC, exactly as every other read of `config/` is. It is the CLI's half
// of the seam above, shared by every consumer so the client, the URL validation
// and the no-auth switch have one spelling.
func stampOpen(ctx context.Context, url string, noAuth bool) (versionstamp.Reader, error) {
	// The root fence opens the cloud from a hook that has no guard of its own,
	// which makes this the first Google call of commands that were purely local
	// before it. It carries the guard every other cloud open carries: without it
	// the token refresh and the GET would be printed, bearer token included, to
	// stderr under GODEBUG=http2debug.
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return nil, err
	}
	db, err := newBudgetClient(ctx, url, noAuth)
	if err != nil {
		return nil, err
	}
	return db, nil
}
