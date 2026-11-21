package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // the IANA zone of a quiet-hours window is honoured on a host with no zoneinfo

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/mailbox"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The sender (mobile plan Task P5.2.6, design docs/mobile/design/
// push-notifications.md §3.6 (B), §3.7, §3.8 and §6): the history sweep
// DERIVES the push events itself and sends them.
//
// The shape is the design's answer to Q1 — one cloud sender, the history job,
// with no producer write anywhere: the run-side producers (the runner at
// finalize, the gateway at a threshold) are exactly the accounts §3.6 (A)
// refuses to widen with the FCM send permission, so nothing on a run's path
// writes a pending-push row and nothing on it can send. Everything the sender
// sends, it derives from state its own account already reads, on the schedule
// it already keeps (the 15-minute sweep, budget.SweepEvery).
//
// Its own state is one node, `config/admin/push/$uid/ledger` (§3.4, §3.7): the
// dedupe key of an event it has sent, the cap window's timestamps and the
// pending intent it will retry. The rows carry no content — a category, a
// state-change key and a timestamp (§3.1's rules hold for the ledger as much as
// for the payload), and the node is one per paired account, so it is deleted
// with the pairing by `fugaro pair revoke` (P5.2.5) and needs no second store,
// no second grant and no Firestore role.
//
// What the sender is NOT allowed to do, and each is a rule of the design
// rather than a preference: it never writes `config/admin/devices/**` (§3.4
// rule 1 — the device node's existence is the allowlist, so a write there would
// re-admit a revoked device); it never writes a token, a platform or a pref,
// and never a marker (reaping deletes); it never creates a push node (§3.4 —
// the registration principal's write is the node's first); it takes nothing
// from its environment or its arguments, because whoever can override the
// history job's environment can steer anything a sender reads from it (§3.6);
// and it sends only while BOTH nodes exist.

const (
	// The category set is D160's, and the names are the registration message's
	// own `prefs` children (internal/mailbox's PrefsFields): the message, the
	// node and the payload spell one vocabulary, so a switch the app writes and
	// a category the sender sends cannot drift into two.
	pushKillSwitch      = "kill_switch"
	pushCapReached      = "cap_reached"
	pushConsumerOffline = "consumer_offline"
	pushCapNear         = "cap_near"
	pushPrompt          = "prompt"
)

// pushCategories is the set in the order a sweep considers it, and the set
// `pushEnabled` and the payload read. "Finished" and "failed" are not in it:
// §1 records that D160 left them out because the phone cannot see them, and a
// category the app cannot resolve against its own authenticated mailbox is a
// lock-screen lie rather than a notification.
var pushCategories = []string{pushKillSwitch, pushCapReached, pushConsumerOffline, pushCapNear, pushPrompt}

const (
	// pushHourlyCap is §3.7's storm cap, N per paired account per hour. It is a
	// floor on nuisance and not a loss of information: everything a suppressed
	// push would have carried is in the mailbox regardless, because the push is
	// a doorbell (§3.2).
	pushHourlyCap = 6
	// pushLedgerWindow is the dedupe and cap window (Q10 (ii)): the age past
	// which a ledger row is pruned on the sweep, so the node holds state about
	// the last window only. One hour is the cap's own period, which is why the
	// two share one row and one lifetime (§3.7).
	//
	// It is the whole of what the SENDER keeps. The rest of Q10's retention is
	// the pairing's lifetime — the node goes with `fugaro pair revoke` (P5.2.5),
	// so nothing outlives the pairing — and the provider's own copy of a
	// payload, which the disclosure states (P5.2.10) rather than this sender
	// implying it away.
	pushLedgerWindow = time.Hour
	// pushRetryWindow is §6's retry window: a pending intent is retried by the
	// sweeps inside it — two of them at the job's 15-minute cadence — and
	// dropped when it ages out. It is shorter than the dedupe window because a
	// buzz that is half an hour late is worth sending and one that is an hour
	// late is not.
	pushRetryWindow = 30 * time.Minute
	// pushOfflineAfter is when the account's consumer is called offline: the
	// app's own presence rule, twelve intervals of a minute (shared/presence's
	// DEFAULT_OFFLINE_INTERVALS x DEFAULT_INTERVAL_MS, D117). The sender and
	// the phone must agree on the number, because the phone tells the user the
	// same thing from the same heartbeat.
	pushOfflineAfter = 12 * time.Minute
	// pushCapNearPercent is how full a run's own cap has to be for cap-near.
	// The design names the category and not the threshold (D160 answers
	// "available, off by default"); this task's number is this constant, and
	// its pull request states it. It is measured against the run's per-run cap,
	// the one cap a run's own spend is directly comparable to.
	pushCapNearPercent = 80
	// pushMaxRows bounds one account's ledger as the sender reads it: a node
	// holding more rows than the cap's window can hold is one a writer other
	// than this sender has been at, and the sender refuses it rather than
	// working through it.
	pushMaxRows = 200
)

// pushEvent is one thing a sweep derived: the category, and the state-change key
// that makes it ONE event rather than one per sweep (§3.7 — the key is the
// transition, not the event). A budget event's key names the run it is about, so
// "one per category per run per hour" is the key's own doing and not a second
// rule the sender has to keep.
type pushEvent struct {
	category string
	key      string
}

// pushRow is one row of the sender's ledger, as `config/admin/push/$uid/ledger`
// holds it. Its key in that node is budget.Key(category + ":" + key), because a
// state-change key carries `/` (a run is `<slug>/<run>`) and the database
// refuses that in a node name; the row keeps the unescaped key so a reader sees
// the event it is about.
type pushRow struct {
	Category string `json:"category"`
	Key      string `json:"key"`
	At       int64  `json:"at"`
	// State is "sent" or "pending": a pending row is an intent §6 retries.
	State    string `json:"state"`
	Attempts int    `json:"attempts,omitempty"`
}

const (
	pushStateSent    = "sent"
	pushStatePending = "pending"
)

// pushNode is one paired account's push node as the sender reads it (§3.4's
// shape: the token, the platform, `updated_at`, the prefs and the sender's own
// ledger). It is decoded leniently on purpose: the fields the sender does not
// know are fields it does not read, and a node whose shape grew must not stop
// delivery. No client writes this node — the rules deny config/admin to every
// client token — so there is no hostile writer for a strict decode to defend
// against, and the registration principal's message is already strict-decoded
// where it is read (P5.2.4).
type pushNode struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
	Prefs    struct {
		KillSwitch      *bool `json:"kill_switch"`
		CapReached      *bool `json:"cap_reached"`
		ConsumerOffline *bool `json:"consumer_offline"`
		CapNear         *bool `json:"cap_near"`
		Prompt          *bool `json:"prompt"`
		QuietHours      *struct {
			Zone  string `json:"zone"`
			Start int64  `json:"start"`
			End   int64  `json:"end"`
		} `json:"quiet_hours"`
	} `json:"prefs"`
	Ledger map[string]pushRow `json:"ledger"`
}

// pushRequest is what the sender hands the provider: the token, the platform
// and the category, and nothing else (§3.1). No title, no body, no run, no
// repository, no amount — the lock screen is read by whoever is standing there,
// and the provider and the OS see the payload in the clear.
type pushRequest struct {
	Token    string
	Platform string
	Category string
}

// pushOutcome is what the provider answered about one send.
type pushOutcome int

const (
	// pushSent: the provider took it.
	pushSent pushOutcome = iota
	// pushUnregistered: the provider reports this token dead (FCM's
	// UNREGISTERED, or a 404 for the token). The sender deletes the entry and
	// does not retry (§6).
	pushUnregistered
	// pushFailed: anything else — the provider is down, the network is, or the
	// permission is not granted (P5.2.7's grant, which is a later task). The
	// intent is queued and retried inside the retry window.
	pushFailed
)

// pushProvider sends one notification. The shipped one is pushFCM; a test
// injects its own.
type pushProvider func(ctx context.Context, req pushRequest) (pushOutcome, error)

// pushSender is one sweep's sender. Everything it needs is ambient: the
// database client it was given, the Firebase project the send addresses, and
// the identity the process already runs as (§3.6 — no key, no secret, nothing
// from the environment).
type pushSender struct {
	db      *rtdb.Client
	project string
	now     func() time.Time
	send    pushProvider
	warn    func(string)
}

// pushReport says what a sweep did, in the words the history job prints.
type pushReport struct {
	Sent         int // notifications the provider took
	Suppressed   int // derived events the account's own settings or the cap held back
	Queued       int // intents written pending after a send that failed
	Retried      int // pending intents this sweep sent again
	Unregistered int // tokens the provider reported dead and the sender deleted
	Orphans      int // push nodes deleted because their device node is gone
	Pruned       int // ledger rows past the window
}

// pushSweep derives and sends one sweep's events. It is called by
// `fugaro budget history --sweep`, after the registry sweep, under the history
// job's own identity.
//
// **A push failing is never worse than no push** (§6's rule): every failure
// here is a warning and a queued intent, never an error that would end the
// history job — the mailbox is authoritative and the push is best-effort, so
// alerts stopping must not stop the sweep. The error returned is what the job
// prints as a warning.
func (p *pushSender) Sweep(ctx context.Context) (pushReport, error) {
	var rep pushReport
	uids, err := p.db.Keys(ctx, pairPushRoot)
	if err != nil {
		return rep, fmt.Errorf("reading %s: %w", pairPushRoot, err)
	}
	if len(uids) == 0 {
		return rep, nil
	}
	// The installation's own events — the kill switches and the runs at or near
	// their caps — are derived once and considered for every paired account:
	// they are facts about the installation, and a paired account is one of its
	// operators. The per-account events (a prompt, a consumer gone quiet) are
	// derived inside the loop.
	shared, errs := p.installationEvents(ctx)
	sort.Strings(uids)
	for _, uid := range uids {
		errs = append(errs, p.account(ctx, uid, shared, &rep))
	}
	return rep, errors.Join(errs...)
}

// account is one paired account's turn: reap an orphan, prune the ledger, retry
// what is pending, then derive and consider this sweep's events.
func (p *pushSender) account(ctx context.Context, uid string, shared []pushEvent, rep *pushReport) error {
	var raw json.RawMessage
	paired, err := p.db.Get(ctx, rules.PathDevices+"/"+uid, &raw)
	if err != nil {
		return fmt.Errorf("reading %s: %w", rules.PathDevices+"/"+uid, err)
	}
	node, found, err := p.readNode(ctx, uid)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if !paired {
		// §3.4's reaping: a push node whose device node is gone is deleted, not
		// marked. It is the net for a revoke written by a principal that removed
		// only the device node, and for a re-registration the consumer had in
		// flight when the revoke landed — and it is why a leftover node is
		// harmless rather than why it exists.
		if err := p.delete(ctx, uid); err != nil {
			return fmt.Errorf("deleting the orphaned push node %s: %w", pairPushPath(uid), err)
		}
		rep.Orphans++
		return nil
	}
	now := p.now()
	kept, pruned := pushPrune(node.Ledger, now)
	if len(pruned) > 0 {
		updates := map[string]any{}
		for _, key := range pruned {
			updates["ledger/"+key] = nil
		}
		if err := p.db.Patch(ctx, pairPushPath(uid), updates); err != nil {
			return fmt.Errorf("pruning %s/ledger: %w", pairPushPath(uid), err)
		}
		rep.Pruned += len(pruned)
	}
	node.Ledger = kept
	if node.Token == "" {
		// §6's first row: no token, no send — and no ledger entry, because
		// there is no event to dedupe and nothing to cap. The node's prefs and
		// ledger are left exactly as they are; the app re-registers (§3.5 (b)).
		return nil
	}
	// A pending intent is retried before new events are derived, so a retry is
	// not crowded out by the events of the same sweep — and a retry that reaps
	// the token ends the account's turn exactly as a fresh send does: the
	// registration is gone, so the events this sweep derived must not be offered
	// to a provider that has just said the token is dead.
	stop, err := p.retry(ctx, uid, &node, rep)
	if err != nil || stop {
		return err
	}
	events, err := p.accountEvents(ctx, uid)
	if err != nil {
		return err
	}
	events = append(events, shared...)
	sort.Slice(events, func(i, j int) bool {
		if events[i].category != events[j].category {
			return events[i].category < events[j].category
		}
		return events[i].key < events[j].key
	})
	for _, e := range events {
		stop, err := p.consider(ctx, uid, &node, e, rep)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
	return nil
}

// readNode reads one account's push node.
func (p *pushSender) readNode(ctx context.Context, uid string) (pushNode, bool, error) {
	var node pushNode
	found, err := p.db.Get(ctx, pairPushPath(uid), &node)
	if err != nil {
		return node, false, fmt.Errorf("reading %s: %w", pairPushPath(uid), err)
	}
	if !found {
		return node, false, nil
	}
	if len(node.Ledger) > pushMaxRows {
		return node, true, fmt.Errorf("%s/ledger holds %d rows, more than the window can hold (%d): the node is not this sender's and nothing was sent to %s", pairPushPath(uid), len(node.Ledger), pushMaxRows, uid)
	}
	return node, true, nil
}

// consider is one event's turn: the account's own switch, then quiet hours,
// then the dedupe, then the cap — in that order, so that what the account asked
// for is decided before the provider is called at all, and a suppressed event
// writes nothing (it is not an event the sender has sent, and §3.7's cap is
// counted from what it did send).
//
// It answers whether this account's turn is over: a token the provider reports
// unregistered ends it, because the token is gone.
func (p *pushSender) consider(ctx context.Context, uid string, node *pushNode, e pushEvent, rep *pushReport) (bool, error) {
	if !pushEnabled(node, e.category) {
		rep.Suppressed++
		return false, nil
	}
	quiet, why := pushQuiet(node, p.now())
	if why != "" {
		p.warnf(why)
	}
	if quiet {
		rep.Suppressed++
		return false, nil
	}
	now := p.now().UnixMilli()
	rowKey := pushRowKey(e)
	if row, ok := node.Ledger[rowKey]; ok && now-row.At < pushLedgerWindow.Milliseconds() {
		// §3.7's dedupe: the key is the transition, so a cap still reached on
		// the next sweep is not a second notification. A row that is pending is
		// the retry's, which has already had its turn this sweep.
		rep.Suppressed++
		return false, nil
	}
	if pushSentInWindow(node.Ledger, now) >= pushHourlyCap {
		rep.Suppressed++
		return false, nil
	}
	return p.deliver(ctx, uid, node, e, nil, rep)
}

// retry sends again what a previous sweep left pending, inside the retry
// window. An intent past it is dropped by pushPrune, never escalated (§6).
//
// It answers what deliver answers — whether the account's turn is over because
// the provider reported the token unregistered — so a retry that reaps the
// registration stops the sweep's own events from following it to a token the
// provider has just called dead.
func (p *pushSender) retry(ctx context.Context, uid string, node *pushNode, rep *pushReport) (bool, error) {
	now := p.now().UnixMilli()
	keys := make([]string, 0, len(node.Ledger))
	for key := range node.Ledger {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		row := node.Ledger[key]
		if row.State != pushStatePending || now-row.At >= pushRetryWindow.Milliseconds() {
			continue
		}
		if !pushEnabled(node, row.Category) {
			// The account turned the category off between the intent and this
			// sweep: the switch is honoured at the send, so the intent is
			// dropped rather than sent. Deleting the row is the sender's own
			// write to its own state and never a marker.
			if err := p.deleteRow(ctx, uid, key); err != nil {
				return false, err
			}
			delete(node.Ledger, key)
			rep.Suppressed++
			continue
		}
		e := pushEvent{category: row.Category, key: row.Key}
		rep.Retried++
		stop, err := p.deliver(ctx, uid, node, e, &row, rep)
		if err != nil || stop {
			return stop, err
		}
	}
	return false, nil
}

// deliver is the one place a send happens. The row is written BEFORE the
// provider is called — the ledger records what the sender has sent, and a
// record written after the send would let a crash between the two send twice —
// and it is left pending, with its attempt counted, when the send fails.
//
// row is the row being retried, or nil for a new event.
func (p *pushSender) deliver(ctx context.Context, uid string, node *pushNode, e pushEvent, row *pushRow, rep *pushReport) (bool, error) {
	now := p.now().UnixMilli()
	next := pushRow{Category: e.category, Key: e.key, At: now, State: pushStatePending}
	if row != nil {
		// A retry keeps the intent's OWN instant: both the retry filter and the
		// prune measure age from At, so a row restamped on every attempt would
		// never reach the end of the window and would be retried for ever.
		next.At = row.At
		next.Attempts = row.Attempts
	}
	key := pushRowKey(e)
	if err := p.writeRow(ctx, uid, key, next); err != nil {
		return false, err
	}
	// The row is held in memory too, so the rest of THIS sweep's decisions —
	// the cap above all — count what it has just decided to send. A sender that
	// only counted the rows it read at the start of the sweep would send one
	// notification per event and never reach its cap.
	node.Ledger[key] = next
	outcome, err := p.send(ctx, pushRequest{Token: node.Token, Platform: node.Platform, Category: e.category})
	if err != nil {
		p.warnf(fmt.Sprintf("the %s notification for %s was not sent and is queued: %v", e.category, uid, oneLine(err.Error())))
		rep.Queued++
		return false, nil
	}
	switch outcome {
	case pushSent:
		next.State = pushStateSent
		node.Ledger[key] = next
		if err := p.writeRow(ctx, uid, key, next); err != nil {
			return false, err
		}
		rep.Sent++
		return false, nil
	case pushUnregistered:
		// §6 and §3.4's "reaping deletes; it never marks": the sender drops the
		// token and deletes the entry — never a marker, never a retry, and never
		// a write under devices/**. The row goes with it: there is nothing left
		// for it to dedupe, and the account's next registration starts a fresh
		// ledger.
		delete(node.Ledger, key)
		if err := p.deleteRow(ctx, uid, key); err != nil {
			return false, err
		}
		if err := p.deleteToken(ctx, uid, node); err != nil {
			return false, err
		}
		rep.Unregistered++
		p.warnf(fmt.Sprintf("the provider reports the registration token of %s unregistered: the entry was deleted and the phone registers again on its next open", uid))
		return true, nil
	default:
		next.Attempts++
		node.Ledger[key] = next
		if err := p.writeRow(ctx, uid, key, next); err != nil {
			return false, err
		}
		rep.Queued++
		p.warnf(fmt.Sprintf("the %s notification for %s was not sent and is queued for the next sweep", e.category, uid))
		return false, nil
	}
}

// installationEvents derives the events that are facts about the installation:
// a kill switch that is on, and a run at or near its own cap.
func (p *pushSender) installationEvents(ctx context.Context) ([]pushEvent, []error) {
	var events []pushEvent
	var errs []error
	kills, err := p.killEvents(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	events = append(events, kills...)
	caps, err := p.capEvents(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	return append(events, caps...), errs
}

// killEvents reads the two switches a run honours and derives one event per
// switch that is ON. The key carries the instant the switch was written when it
// has one (`at`), so turning a switch off and on again is a second event while
// a switch left on is one. The read is budget.ReadGlobalKill/ReadKills rather
// than a decode of this file's own, because their fail-closed rule is the one
// that matters here: a switch nobody can read counts as ON.
func (p *pushSender) killEvents(ctx context.Context) ([]pushEvent, error) {
	var events []pushEvent
	kills, err := budget.ReadGlobalKill(ctx, p.db)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", budget.PathKillGlobal, err)
	}
	if kills.Global != nil && kills.Global.On {
		events = append(events, pushEvent{category: pushKillSwitch, key: pushKillKey("global", kills.Global)})
	}
	slugs, err := p.db.Keys(ctx, "config/kill/repos")
	if err != nil {
		return nil, fmt.Errorf("reading config/kill/repos: %w", err)
	}
	for _, slug := range slugs {
		raw, err := budget.Unkey(slug)
		if err != nil {
			p.warnf(fmt.Sprintf("skipping the kill switch at config/kill/repos/%s: %v", slug, err))
			continue
		}
		both, err := budget.ReadKills(ctx, p.db, raw)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", budget.PathKillRepo(raw), err)
		}
		if both.Repo != nil && both.Repo.On {
			events = append(events, pushEvent{category: pushKillSwitch, key: pushKillKey(raw, both.Repo)})
		}
	}
	return events, nil
}

// pushKillKey is one switch's state-change key: the scope it is about and the
// instant it was written, when the node carries one.
func pushKillKey(scope string, k *budget.Kill) string {
	if k.At > 0 {
		return fmt.Sprintf("kill:%s:%d", scope, k.At)
	}
	return "kill:" + scope
}

// capEvents derives the two cap categories, and they are read from different
// places on purpose.
func (p *pushSender) capEvents(ctx context.Context) ([]pushEvent, error) {
	reached, err := p.capReachedEvents(ctx)
	if err != nil {
		return nil, err
	}
	near, err := p.capNearEvents(ctx)
	if err != nil {
		return nil, err
	}
	return append(reached, near...), nil
}

// capReachedEvents is "a cap is binding on a run": every run whose share of the
// day has reached the cap that binds it, read from the two families the phone
// reads this category from — `config/caps/**` and `spend/$day/**` (§1) — and
// never from the run registry.
//
// The registry is not a source for it, and this is the reason rather than a
// preference: a halted run deletes its own entry as it ends, and the same job's
// registry phase reaps a hard-killed run's entry before this one runs, so a
// derivation from the entry's `halted` sees a halted run only in the window
// between the halt and its end — against a 15-minute sweep. §3.7's "a cap that
// is still reached on the next sweep sends one cap reached" describes a
// condition that OUTLIVES the run's entry, and the spend node is where that
// condition lives. It is also the only place the phone can see it, which is what
// makes the buzz resolvable when the tap lands (§3.2): an event the app cannot
// resolve against what it may read is a lock-screen lie.
//
// The day is the sweep's own UTC day (D5), the one a person reading the app is
// in, and the cap is budget.Caps.PerRun() — the smallest of the repository's own
// per-run cap or the default, and the project's global one — so the comparison
// is the budget's own and not a second reading of the same nodes. The day is
// also the window: a run over its cap in an earlier day's share is not an event
// of this one, because the day's share is the figure the phone resolves against.
// A run with no per-run cap set anywhere is not an event either: nothing binds
// it, and the app has no cap to resolve the category against.
func (p *pushSender) capReachedEvents(ctx context.Context) ([]pushEvent, error) {
	day := budget.Day(p.now())
	var shares map[string]map[string]budget.Counters
	if _, err := p.db.Get(ctx, "spend/"+budget.DayKey(day)+"/runs", &shares); err != nil {
		return nil, fmt.Errorf("reading spend/%s/runs: %w", budget.DayKey(day), err)
	}
	slugs := make([]string, 0, len(shares))
	for key := range shares {
		slugs = append(slugs, key)
	}
	sort.Strings(slugs)
	var events []pushEvent
	for _, slugKey := range slugs {
		slug, err := budget.Unkey(slugKey)
		if err != nil || slug == "" {
			continue
		}
		caps, err := p.pushCaps(ctx, slug)
		if err != nil {
			return nil, err
		}
		perRun, hasCap := caps.PerRun()
		if !hasCap {
			continue
		}
		runs := make([]string, 0, len(shares[slugKey]))
		for key := range shares[slugKey] {
			runs = append(runs, key)
		}
		sort.Strings(runs)
		for _, runKey := range runs {
			run, err := budget.Unkey(runKey)
			if err != nil || run == "" {
				continue
			}
			if shares[slugKey][runKey].Spent < perRun {
				continue
			}
			// The run's name is the key: "one per category per run per hour"
			// (§3.7) is the key's own doing, and a run that is still over its cap
			// on the next sweep is one event, not one per sweep.
			events = append(events, pushEvent{category: pushCapReached, key: "cap:" + slug + "/" + run})
		}
	}
	return events, nil
}

// capNearEvents is "a run is approaching the ceiling that will stop it", and the
// registry is the right source for it: it is about a run still burning toward
// its own cap — a live run's trajectory, which the entry it heartbeats is the
// record of — and the quantity compared is the run's own lifetime spend
// (AgentEntry.Spent), the same one Evaluate's per-run side compares. A run whose
// entry is gone is not near a cap; it has ended.
func (p *pushSender) capNearEvents(ctx context.Context) ([]pushEvent, error) {
	var tree map[string]map[string]json.RawMessage
	if _, err := p.db.Get(ctx, "agents", &tree); err != nil {
		return nil, fmt.Errorf("reading the registry: %w", err)
	}
	var defaults *budget.DefaultCaps
	if found, err := p.db.Get(ctx, budget.PathCapsDefaults, &defaults); err != nil {
		return nil, fmt.Errorf("reading %s: %w", budget.PathCapsDefaults, err)
	} else if !found {
		defaults = nil
	}
	slugs := make([]string, 0, len(tree))
	for key := range tree {
		slugs = append(slugs, key)
	}
	sort.Strings(slugs)
	var events []pushEvent
	for _, slugKey := range slugs {
		slug, err := budget.Unkey(slugKey)
		if err != nil || slug == "" {
			continue
		}
		var repo *budget.RepoCaps
		if found, err := p.db.Get(ctx, budget.PathCapsRepo(slug), &repo); err != nil {
			return nil, fmt.Errorf("reading %s: %w", budget.PathCapsRepo(slug), err)
		} else if !found {
			repo = nil
		}
		perRun, hasCap := pushPerRunCap(repo, defaults)
		runs := make([]string, 0, len(tree[slugKey]))
		for key := range tree[slugKey] {
			runs = append(runs, key)
		}
		sort.Strings(runs)
		for _, runKey := range runs {
			run, err := budget.Unkey(runKey)
			if err != nil || run == "" {
				continue
			}
			var e budget.AgentEntry
			if json.Unmarshal(tree[slugKey][runKey], &e) != nil {
				continue
			}
			if hasCap && perRun > 0 && e.Spent >= perRun*budget.Micros(pushCapNearPercent)/100 {
				events = append(events, pushEvent{category: pushCapNear, key: "cap-near:" + slug + "/" + run})
			}
		}
	}
	return events, nil
}

// pushCaps reads the three nodes a run's cap can come from — the project's, the
// defaults and the repository's own — as the budget's own Caps value, so the
// comparison made on it is budget.Caps's and not this file's. The mode is not
// read: the phone may read config/caps/** and cannot read config/mode, so a
// category it resolves from the caps alone must not turn on a node it cannot
// see.
func (p *pushSender) pushCaps(ctx context.Context, slug string) (budget.Caps, error) {
	var caps budget.Caps
	if found, err := p.db.Get(ctx, budget.PathCapsGlobal, &caps.Global); err != nil {
		return caps, fmt.Errorf("reading %s: %w", budget.PathCapsGlobal, err)
	} else if !found {
		caps.Global = nil
	}
	if found, err := p.db.Get(ctx, budget.PathCapsDefaults, &caps.Defaults); err != nil {
		return caps, fmt.Errorf("reading %s: %w", budget.PathCapsDefaults, err)
	} else if !found {
		caps.Defaults = nil
	}
	if found, err := p.db.Get(ctx, budget.PathCapsRepo(slug), &caps.Repo); err != nil {
		return caps, fmt.Errorf("reading %s: %w", budget.PathCapsRepo(slug), err)
	} else if !found {
		caps.Repo = nil
	}
	return caps, nil
}

// pushPerRunCap is a repository's per-run cap: its own node, else the default.
// It is cap-near's comparison (capNearEvents); the cap that BINDS a run —
// budget.Caps.PerRun(), the smallest of the three sides — is read by pushCaps,
// because cap_reached is about the cap that really stops the run.
func pushPerRunCap(repo *budget.RepoCaps, defaults *budget.DefaultCaps) (budget.Micros, bool) {
	if repo != nil && repo.PerRunMicros != nil {
		return *repo.PerRunMicros, true
	}
	if defaults != nil && defaults.RepoPerRunMicros != nil {
		return *defaults.RepoPerRunMicros, true
	}
	return 0, false
}

// accountEvents derives the events that are facts about one paired account: a
// prompt the mailbox carries for it, and its consumer gone quiet.
func (p *pushSender) accountEvents(ctx context.Context, uid string) ([]pushEvent, error) {
	var events []pushEvent
	var outbox map[string]json.RawMessage
	if _, err := p.db.Get(ctx, "mailbox/"+uid+"/outbox", &outbox); err != nil {
		return nil, fmt.Errorf("reading mailbox/%s/outbox: %w", uid, err)
	}
	ids := make([]string, 0, len(outbox))
	for id := range outbox {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		// The consumer's own strict decoder, not a lenient read of the one
		// field this needs: a node the schema refuses is not a stored prompt,
		// and a buzz for it would be a notification for something the app
		// cannot resolve against its mailbox (§3.2).
		record, err := mailbox.Decode(outbox[id])
		if err != nil {
			continue
		}
		if kind, _ := record["kind"].(string); kind != mailboxKindPrompt {
			continue
		}
		// The message's own id is the state-change key: one question, one buzz,
		// however often the outbox is re-read (§3.7 — the consumer re-delivers
		// until the ack).
		events = append(events, pushEvent{category: pushPrompt, key: "prompt:" + id})
	}
	var beat struct {
		At   int64  `json:"at"`
		Boot string `json:"boot"`
	}
	found, err := p.db.Get(ctx, "mailbox/"+uid+"/heartbeat", &beat)
	if err != nil {
		return nil, fmt.Errorf("reading mailbox/%s/heartbeat: %w", uid, err)
	}
	if found && beat.At > 0 && p.now().UnixMilli()-beat.At >= pushOfflineAfter.Milliseconds() {
		// The consumer's boot id is the state-change key: one buzz per consumer
		// that goes quiet, not one per sweep it stays quiet for.
		key := "consumer-offline"
		if beat.Boot != "" {
			key += ":" + beat.Boot
		}
		events = append(events, pushEvent{category: pushConsumerOffline, key: key})
	}
	return events, nil
}

// pushEnabled answers whether the account's own switch lets this category
// through (§3.8: the switch is honoured server-side, because a client-side
// filter cannot stop a push from reaching the OS and the provider). An absent
// prefs node is D160's default set: on for the categories it leaves
// interrupting, off for cap-near, the one it keeps off by default. The
// registration message always carries all five (internal/mailbox's
// PrefsRequired), so the absent case is a node written by hand.
func pushEnabled(node *pushNode, category string) bool {
	var v *bool
	switch category {
	case pushKillSwitch:
		v = node.Prefs.KillSwitch
	case pushCapReached:
		v = node.Prefs.CapReached
	case pushConsumerOffline:
		v = node.Prefs.ConsumerOffline
	case pushCapNear:
		v = node.Prefs.CapNear
	case pushPrompt:
		v = node.Prefs.Prompt
	default:
		return false
	}
	if v == nil {
		return category != pushCapNear
	}
	return *v
}

// pushQuiet answers whether the account's quiet-hours window is on now, and a
// warning sentence when the window cannot be honoured at all.
//
// The window is a zone NAME and not an offset (§3.8, Q5): the name is resolved
// at the instant of the check, so the window means the same local hours across
// a DST change — a stored offset would suppress the wrong hour twice a year,
// invisibly. The zone database is embedded (the blank time/tzdata import), so
// the rule holds on a host that carries no zoneinfo.
//
// A window whose zone this sender cannot resolve suppresses nothing and says
// so: quiet hours is a nuisance control and not a security one, and silently
// dropping every notification for an account whose zone name is unknown is a
// worse failure than a buzz at the wrong hour — the mailbox carries everything
// either way.
func pushQuiet(node *pushNode, now time.Time) (bool, string) {
	window := node.Prefs.QuietHours
	if window == nil || window.Zone == "" {
		return false, ""
	}
	// The bounds the registration message enforces (mailbox.ZoneMax,
	// mailbox.MinuteMax): a window outside them is one no app wrote, and a
	// minute the sender cannot place in a local day suppresses nothing.
	if len(window.Zone) > mailbox.ZoneMax || window.Start < 0 || window.Start > mailbox.MinuteMax ||
		window.End < 0 || window.End > mailbox.MinuteMax {
		return false, fmt.Sprintf("the quiet-hours window of this account is not one a registration writes (zone %q, %d–%d): nothing was suppressed", oneLine(window.Zone), window.Start, window.End)
	}
	if window.Start == window.End {
		return false, ""
	}
	loc, err := time.LoadLocation(window.Zone)
	if err != nil {
		return false, fmt.Sprintf("the quiet-hours window of this account names the zone %q, which this sender cannot resolve (%v): nothing was suppressed", window.Zone, err)
	}
	local := now.In(loc)
	minutes := int64(local.Hour())*60 + int64(local.Minute())
	if window.Start < window.End {
		return minutes >= window.Start && minutes < window.End, ""
	}
	// A window that wraps midnight (22:00–07:00) is the ordinary case.
	return minutes >= window.Start || minutes < window.End, ""
}

// pushPrune is §6's/Q10 (ii)'s pruning: the rows past the dedupe window, and
// the pending intents past the retry window, are deleted — so the node holds
// state about the last window only. It answers the rows to keep and the node
// keys to delete.
func pushPrune(ledger map[string]pushRow, now time.Time) (map[string]pushRow, []string) {
	kept := make(map[string]pushRow, len(ledger))
	var drop []string
	for key, row := range ledger {
		age := now.UnixMilli() - row.At
		past := age >= pushLedgerWindow.Milliseconds()
		if row.State == pushStatePending && age >= pushRetryWindow.Milliseconds() {
			past = true
		}
		if past {
			drop = append(drop, key)
			continue
		}
		kept[key] = row
	}
	sort.Strings(drop)
	return kept, drop
}

// pushSentInWindow counts the notifications the account's ledger records inside
// the cap's hour. A pending row counts: it is an intent the sender has already
// decided to send, and the cap is about buzzes the account will see.
func pushSentInWindow(ledger map[string]pushRow, now int64) int {
	n := 0
	for _, row := range ledger {
		if now-row.At < pushLedgerWindow.Milliseconds() {
			n++
		}
	}
	return n
}

// pushRowKey is one row's name in the ledger node. It is escaped because a
// state-change key carries a `/` (a run is `<slug>/<run>`) and the database
// refuses that in a node name.
func pushRowKey(e pushEvent) string { return budget.Key(e.category + ":" + e.key) }

// writeRow writes one ledger row: a PATCH of the single child, so a sweep never
// rewrites the rows it did not touch.
func (p *pushSender) writeRow(ctx context.Context, uid, key string, row pushRow) error {
	if err := p.db.Patch(ctx, pairPushPath(uid), map[string]any{"ledger/" + key: row}); err != nil {
		return fmt.Errorf("writing %s/ledger/%s: %w", pairPushPath(uid), key, err)
	}
	return nil
}

// deleteRow takes one row out of the ledger.
func (p *pushSender) deleteRow(ctx context.Context, uid, key string) error {
	if err := p.db.Patch(ctx, pairPushPath(uid), map[string]any{"ledger/" + key: nil}); err != nil {
		return fmt.Errorf("deleting %s/ledger/%s: %w", pairPushPath(uid), key, err)
	}
	return nil
}

// deleteToken is §6's stale-token reaping: the registration's three fields —
// the token, the platform and `updated_at` — are deleted, and the whole node
// goes with them when nothing else is left in it (the node's next write is the
// registration principal's create, §3.5 (b)). It writes no marker, and it never
// touches `config/admin/devices/**`.
//
// node is the node as read, with the row this sweep deleted already taken out
// of its ledger, so an emptied node is recognised as one.
func (p *pushSender) deleteToken(ctx context.Context, uid string, node *pushNode) error {
	if node.contentless() {
		return p.delete(ctx, uid)
	}
	updates := map[string]any{"token": nil, "platform": nil, "updated_at": nil}
	if err := p.db.Patch(ctx, pairPushPath(uid), updates); err != nil {
		return fmt.Errorf("deleting the stale token of %s: %w", pairPushPath(uid), err)
	}
	return nil
}

// contentless answers whether the node holds nothing but the registration
// fields the sender is about to delete: the prefs the app wrote and the ledger
// the sender wrote are the node's only other content.
func (n pushNode) contentless() bool {
	p := n.Prefs
	if p.KillSwitch != nil || p.CapReached != nil || p.ConsumerOffline != nil ||
		p.CapNear != nil || p.Prompt != nil || p.QuietHours != nil {
		return false
	}
	return len(n.Ledger) == 0
}

// delete removes a whole push node. The database's delete is a PATCH of the
// child to null, which is how pair.go's own revoke writes its deletions.
func (p *pushSender) delete(ctx context.Context, uid string) error {
	if err := p.db.Patch(ctx, pairPushRoot, map[string]any{uid: nil}); err != nil {
		return fmt.Errorf("deleting %s: %w", pairPushPath(uid), err)
	}
	return nil
}

func (p *pushSender) warnf(s string) {
	if p.warn != nil {
		p.warn(s)
	}
}

// fcmRoot is where the shipped provider sends. It is a constant and not an
// environment variable on purpose (§3.6): the sender reads nothing from its
// environment or its arguments, because whoever can set the history job's
// environment could otherwise point every notification somewhere else.
const fcmRoot = "https://fcm.googleapis.com"

// fcmSender is the shipped provider: FCM's HTTP v1 API, one message per send,
// under the ambient identity the history job already runs as (§3.6 — the
// permission is firebasecloudmessaging.messages.create, granted by P5.2.7, and
// there is no key anywhere). The payload is the category and nothing else
// (§3.1): a data-only message with no title and no body, so what the lock
// screen shows is the app's own closed set of phrases and never a string the
// sender wrote.
type fcmSender struct {
	project string
	url     string
	http    *http.Client
}

func (f fcmSender) send(ctx context.Context, req pushRequest) (pushOutcome, error) {
	body, err := json.Marshal(map[string]any{"message": map[string]any{
		"token":   req.Token,
		"data":    map[string]string{"category": req.Category},
		"android": map[string]any{"priority": "high"},
	}})
	if err != nil {
		return pushFailed, err
	}
	url := strings.TrimSuffix(f.url, "/") + "/v1/projects/" + f.project + "/messages:send"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return pushFailed, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(httpReq)
	if err != nil {
		return pushFailed, err
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	code := fcmErrorCode(answer)
	switch {
	case resp.StatusCode == http.StatusOK:
		return pushSent, nil
	case code == "UNREGISTERED":
		// Reaping DELETES, so it takes the provider's own word and nothing
		// weaker: FCM v1 answers a dead token with 404 and `UNREGISTERED` in the
		// error details, and a bare 404 (a wrong project path, a proxy, a
		// gateway) is a failure the sender queues and warns about instead of a
		// registration deleted on a guess. SENDER_ID_MISMATCH is not read as
		// unregistered either: a token minted for another Firebase project is
		// Q10's shape (which project mints the token), and the token may be one
		// the app is about to make right.
		return pushUnregistered, nil
	default:
		return pushFailed, fmt.Errorf("FCM answered %d (%s): %s", resp.StatusCode, code, oneLine(string(answer)))
	}
}

// fcmErrorCode reads the errorCode out of FCM's error body, or the status when
// the body carries no code — which is what the sender's warning then names.
func fcmErrorCode(body []byte) string {
	var e struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	for _, d := range e.Error.Details {
		if d.ErrorCode != "" {
			return d.ErrorCode
		}
	}
	return e.Error.Status
}
