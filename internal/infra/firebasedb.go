package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The mark init writes into the project's database (an RTDB instance can't
// carry labels), so a later run can tell the database is ours.
const (
	markBy      = "fugaro"
	markVersion = 1
	// DefaultMaxReserveMicros is the largest amount one write may reserve
	// until the owner changes it (design §6.4: $5). Without the node the
	// rules deny every lease.
	DefaultMaxReserveMicros budget.Micros = 5_000_000
)

type dbMark struct {
	By      string `json:"managed_by"`
	Project string `json:"project"`
	Version int    `json:"version"`
}

// DB is what init does to the project's database: the mark check, then the
// rules, the mark, the project name, the mode and the limits it seeds.
// Everything is read first (Plan), shown, and written only after the
// operator confirms (Apply); a database already in the wanted state needs no
// confirmation and no write.
type DB struct {
	c       *rtdb.Client
	project string
	// mode is the mode the operator asked for ("" = none: an absent mode is
	// seeded observe and a present one kept).
	mode string

	rules []byte
	acts  []DBAction
}

// DBAction is one pending write.
type DBAction struct {
	Path string // "rules", "fugaro/mark", ...
	Text string // what it does, for the confirmation
	do   func(context.Context) error
}

// NewDB is the database client c of Fugaro project name project; mode is
// "", observe or enforce.
func NewDB(c *rtdb.Client, project, mode string) *DB {
	return &DB{c: c, project: project, mode: mode}
}

// dbErr turns a refused or failed read into the operator's words.
func dbErr(what string, err error) error {
	if errors.Is(err, rtdb.ErrPermission) {
		return userErr("%s: the database refused you (%v); init writes it as a project owner or editor, or a budget admin (firebasedatabase.admin on the Firebase project; role changes take a few minutes to apply)", what, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Check refuses a database that holds data but no Fugaro mark, or a mark of
// another Fugaro project: init writes rules that make every node but a
// run's own admin-only, and the project's counters would otherwise mingle
// with someone else's data. An empty database and a database with our mark
// pass; marked reports which.
func (d *DB) Check(ctx context.Context) (marked bool, err error) {
	keys, err := d.c.RootKeys(ctx)
	if err != nil {
		return false, dbErr("reading the database's root", err)
	}
	var m dbMark
	found, err := d.c.Get(ctx, budget.PathMark, &m)
	if err != nil {
		var ue *json.UnmarshalTypeError
		if errors.As(err, &ue) {
			return false, userErr("the database's %s is not a Fugaro mark; it is not ours, and init writes into databases that are empty or carry our mark", budget.PathMark)
		}
		return false, dbErr("reading "+budget.PathMark, err)
	}
	switch {
	case !found && len(keys) == 0:
		return false, nil
	case !found:
		return false, userErr("the database holds data (%s) and no Fugaro mark (%s): it is not ours, and init writes into databases that are empty or carry our mark", strings.Join(clipKeys(keys), ", "), budget.PathMark)
	case m.By != markBy || m.Version != markVersion || m.Project != d.project:
		return false, userErr("the database carries the Fugaro mark of project %q (managed_by %q, version %d), not project %s: one Firebase project serves one Fugaro project (design D3)", m.Project, m.By, m.Version, d.project)
	}
	return true, nil
}

func clipKeys(keys []string) []string {
	if len(keys) > 5 {
		return append(slices.Clone(keys[:5]), "...")
	}
	return keys
}

// Plan reads the database and returns what init would write; nothing is
// written. It checks the database first (Check).
func (d *DB) Plan(ctx context.Context) ([]DBAction, []string, error) {
	if _, err := d.Check(ctx); err != nil {
		return nil, nil, err
	}
	d.acts = nil
	var warnings []string
	mark := dbMark{By: markBy, Project: d.project, Version: markVersion}

	// The mark, then the project name: a database stays recognisable even if
	// a later write fails.
	if err := d.create(ctx, budget.PathMark, mark, "writes the Fugaro mark ("+budget.PathMark+")"); err != nil {
		return nil, nil, err
	}
	var name string
	etag, found, err := d.c.GetETag(ctx, budget.PathProject, &name)
	switch {
	case err != nil:
		return nil, nil, dbErr("reading "+budget.PathProject, err)
	case found && name != d.project:
		return nil, nil, userErr("the database's %s is %q, not %q: it belongs to another Fugaro project", budget.PathProject, name, d.project)
	case !found:
		d.put(budget.PathProject, etag, d.project, "writes the project's name ("+budget.PathProject+" = "+d.project+"), which every run token's fp claim must equal")
	}

	// The mode: seeded when absent, changed only on request.
	var cur string
	etag, found, err = d.c.GetETag(ctx, budget.PathMode, &cur)
	if err != nil {
		return nil, nil, dbErr("reading "+budget.PathMode, err)
	}
	want := cur
	switch {
	case !found && d.mode == "":
		want = budget.ModeObserve
	case d.mode != "":
		want = d.mode
	}
	switch {
	case !found:
		d.put(budget.PathMode, etag, want, "seeds the mode: "+want)
	case cur != want:
		d.put(budget.PathMode, etag, want, fmt.Sprintf("changes the mode: %s -> %s", cur, want))
	}
	if want == budget.ModeEnforce {
		warnings = append(warnings, d.capsWarning(ctx)...)
	}

	// The limit the rules need to grant any lease at all: absent denies.
	var lim map[string]json.RawMessage
	etag, found, err = d.c.GetETag(ctx, budget.PathLimits, &lim)
	if err != nil {
		return nil, nil, dbErr("reading "+budget.PathLimits, err)
	}
	if _, ok := lim["maxReserveMicros"]; !ok {
		if lim == nil {
			lim = map[string]json.RawMessage{}
		}
		lim["maxReserveMicros"], _ = json.Marshal(DefaultMaxReserveMicros)
		d.put(budget.PathLimits, etag, lim, "seeds the largest lease (max reserve) at $5")
	}

	// The rules: last, so they go live on a database that is marked and
	// configured.
	gen, err := rules.Generate()
	if err != nil {
		return nil, nil, fmt.Errorf("generating the rules: %w", err)
	}
	d.rules = gen
	live, err := d.c.Rules(ctx)
	if err != nil {
		return nil, nil, dbErr("reading the deployed rules", err)
	}
	if !sameJSON(live, gen) {
		d.acts = append(d.acts, DBAction{Path: "rules", Text: fmt.Sprintf("deploys the security rules (%d bytes, generated)", len(gen)), do: func(ctx context.Context) error {
			return d.c.PutRules(ctx, d.rules)
		}})
	}
	return d.acts, warnings, nil
}

// capsWarning says when enforce has no global caps to enforce: the rules
// read an absent cap as a refusal, so every lease would be denied.
func (d *DB) capsWarning(ctx context.Context) []string {
	var g budget.GlobalCaps
	found, err := d.c.Get(ctx, budget.PathCapsGlobal, &g)
	if err == nil && found && g.DailyMicros != nil && g.PerRunMicros != nil {
		return nil
	}
	return []string{"the mode is enforce but the global caps (" + budget.PathCapsGlobal + ") are not both set: the rules read an absent cap as a refusal, so every run halts until you set them: fugaro budget set --global --daily <usd> --per-run <usd>"}
}

// create plans writing v at path when the node is absent.
func (d *DB) create(ctx context.Context, path string, v any, text string) error {
	etag, found, err := d.c.GetETag(ctx, path, nil)
	if err != nil {
		return dbErr("reading "+path, err)
	}
	if !found {
		d.put(path, etag, v, text)
	}
	return nil
}

func (d *DB) put(path, etag string, v any, text string) {
	d.acts = append(d.acts, DBAction{Path: path, Text: text, do: func(ctx context.Context) error {
		return d.c.PutIfMatch(ctx, path, etag, v)
	}})
}

// Apply writes the planned actions in order. A write that finds its node
// changed since Plan (412) stops: nothing is overwritten blindly.
func (d *DB) Apply(ctx context.Context) error {
	for _, a := range d.acts {
		if err := a.do(ctx); err != nil {
			if errors.Is(err, rtdb.ErrPrecondition) {
				return userErr("%s changed while init was running; run it again", a.Path)
			}
			return dbErr("writing "+a.Path, err)
		}
	}
	return nil
}

// sameJSON reports whether a and b are the same JSON document, ignoring
// formatting.
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb)
}
