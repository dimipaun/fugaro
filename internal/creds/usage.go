package creds

import (
	"context"
	"fmt"
	"time"
)

// pathUsage is the subtree one member's usage is counted under, beside the
// marks and read by nothing on the call path.
const pathUsage = "creds/usage"

// PathUsage is the node one member's usage is counted in.
func PathUsage(name string) string { return pathUsage + "/" + seg("credential name", name) }

// DefaultUsageWindow is the window one member's usage is counted over: five
// hours, the span a subscription's own session limit is stated over. It is this
// service's own choice and not a provider's number — the figures are display
// data, and the rule that acts on a provider's figure (PL8) reads that figure,
// not this window — so it is one constant here rather than a setting.
const DefaultUsageWindow = 5 * time.Hour

// Usage is what one member has spent in the window the service counts: how many
// calls it served and how many tokens those calls spent, and the fraction of the
// member's limit a provider last reported as used.
//
// It is figures and never a value (F19): a count, a count and a fraction, and
// nothing of any credential, any header or any answer's text has a way in.
//
// # Why it is a node of its own
//
// For the reason Observation is: the marks are read by every release and decoded
// strictly (readNameTree, decodeStrict), so a member added to Mark is a member a
// release behind refuses the whole node over. The figures are also read by a
// reader that needs none of them — the operator's listing, and the console's
// pools screen (VS2) — so they are kept where that reader looks for them.
//
// # The window is the count's, and the figure goes with it
//
// A call counted after the window ended starts a new count rather than adding to
// a figure that describes a window nobody is in any more (Record). The provider's
// utilisation figure is part of what the window holds, so it goes with it: a
// figure reported in one window would otherwise hold a member off for ever, and
// the rule that reads it (PL8) is written to come back when the window resets.
type Usage struct {
	// Calls is how many calls the member served in the window.
	Calls int64 `json:"calls"`
	// Tokens is how many tokens those calls spent, as the answers reported them:
	// a count and not a price.
	Tokens int64 `json:"tokens"`
	// Window is the instant the count began, and it is the only clock this node
	// keeps.
	Window time.Time `json:"window"`
	// Utilisation is the fraction of the member's limit the provider last
	// reported as used in this window, and it is absent where the provider
	// reported none — the ordinary case for a key, and the open question U1
	// asks about a subscription token. It is never guessed (D3178).
	Utilisation *float64 `json:"utilisation,omitempty"`
}

// Record counts one served call against a member's usage, in the window now
// falls in: the same window when it is still open, and a new one — the counts
// and the provider's figure cleared — when it has ended.
//
// It answers the next Usage rather than mutating one, so the store's own
// compare-and-set can call it on whatever it reads (UpdateUsage).
func (u Usage) Record(now time.Time, calls, tokens int64, window time.Duration) Usage {
	if window <= 0 {
		window = DefaultUsageWindow
	}
	if u.Window.IsZero() || !now.Before(u.Window.Add(window)) {
		u = Usage{Window: now}
	}
	u.Calls += calls
	u.Tokens += tokens
	return u
}

// Headroom is the fraction of the member's limit that is left, and nil where the
// provider reported no utilisation figure: the figure is never guessed (D3178,
// U1), and a member nothing reported on shows no room rather than a made-up one.
//
// The answer is held to [0,1] because it is a fraction: a provider reporting
// past the limit leaves no room rather than a negative one, and the figure that
// says how far past it is is the utilisation itself, which a rule that acts on
// it reads (PL8).
func (u Usage) Headroom() *float64 {
	if u.Utilisation == nil {
		return nil
	}
	h := 1 - *u.Utilisation
	if h < 0 {
		h = 0
	}
	if h > 1 {
		h = 1
	}
	return &h
}

// UsageCounter is the write half of UsageStore: the one method the SERVICE
// needs, and the whole of what a selector's store is asked to be able to do.
//
// It is an interface of its own, and not UsageStore, because the two halves are
// held by different callers: a service counts one member at a time, on the
// answer path, and the figures of a whole pool are read by the operator's
// listing, which holds the store itself. A capability that demanded both would
// make the service's store answer a read nothing on that path makes — and the
// wrapper a scope hands a selector forwards the write alone, exactly as it
// forwards the observation's (credsvc's stateReads).
//
// It is not a method on Store, for the reason ObservationStore is not:
// CascadeState's six methods are the whole of what a selection may reach
// (TestASelectionReachesTheCascadeStateAndNotTheAuthRecord), and this is not a
// fact a selection reads, writes or may reach. A deployment whose store cannot
// count counts nothing, and the figures are then absent — which is display data
// missing and never a decision made wrongly.
type UsageCounter interface {
	// UpdateUsage reads one member's usage and writes what f answers,
	// conditionally on the value read; a lost race re-reads the node and calls f
	// again, so f must be a function of its arguments only and may be called more
	// than once. The usage written is returned.
	UpdateUsage(ctx context.Context, name string, f func(prev Usage, found bool) (Usage, error)) (Usage, error)
}

// UsageStore is the figures a member has spent, as the service writes them and a
// launcher's own reader reads them.
type UsageStore interface {
	UsageCounter
	// Usages reads every member's usage in one call: the subtree is one short
	// node per member and the operator's listing needs the figures of a whole
	// pool at once. A member nothing was counted for is absent from the map.
	Usages(ctx context.Context) (map[string]Usage, error)
}

// Usages implements UsageStore.
func (s *RTDBStore) Usages(ctx context.Context) (map[string]Usage, error) {
	return readNameTree[Usage](ctx, s.db(), pathUsage, "the usage", "usage node")
}

// UpdateUsage implements UsageStore.
func (s *RTDBStore) UpdateUsage(ctx context.Context, name string, f func(prev Usage, found bool) (Usage, error)) (Usage, error) {
	u, err := cas(ctx, s.db(), PathUsage(name), s.attempts(), f)
	if err != nil {
		return Usage{}, fmt.Errorf("creds: writing the usage of %q: %w", name, err)
	}
	return u, nil
}

// CountUsage counts one served call against one member, in the window now falls
// in: the transition a service makes on the answer path, in one place, so that
// the window the count is kept over is this package's and not each caller's.
//
// A store that cannot count answers the error its own write made; the caller
// decides what an unrecorded figure costs, and on the answer path it costs
// nothing but a log line — the figures are display data (F19).
func CountUsage(ctx context.Context, store UsageCounter, name string, now time.Time, calls, tokens int64) error {
	if store == nil {
		return nil
	}
	_, err := store.UpdateUsage(ctx, name, func(prev Usage, _ bool) (Usage, error) {
		return prev.Record(now, calls, tokens, DefaultUsageWindow), nil
	})
	return err
}
