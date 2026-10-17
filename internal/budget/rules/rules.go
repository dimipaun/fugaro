// Package rules generates the Realtime Database security rules of the budget
// backend (design m9-budget-and-dashboard §6.4) from rules.json.tmpl.
//
// The rules are the only thing standing between a compromised run and every
// shared counter, so they are generated, never edited by hand: Generate is
// deterministic, testdata/rules.golden.json pins its output, and the emulator
// suite (emulator_test.go, build tag firebase) proves the rules agree with
// budget.Evaluate, the pure predicate the lease client uses to tell a stale
// write from a refusal.
//
// The template holds the rule expressions in the macro notation of the design,
// expanded once by the generator with the depth of the location they sit at:
//
//	NEW, OLD        the value of this location after / before the write (0 if absent)
//	N(path), O(path) the value at an absolute path after / before the write (0 if absent)
//	D(path)         N(path) - O(path)
//	TODAY, YESTERDAY the UTC epoch day keys, computed from `now`
//	TOKEN           a token of this project that has not passed its deadline
//	RUN             TOKEN of the run that owns $slug/$run
//	...             see the macro table below
//
// A path argument is slash separated: a $name is a wildcard variable, an
// auth.* is a token claim, TODAY and YESTERDAY are the day keys, anything else
// is a literal key.
//
// Claims. fs and fr are the run's slug and run id as database keys (see
// budget.Key), fx its deadline in epoch milliseconds, fp the project id that
// must equal /fugaro/project, rb the launcher's identity (requested_by).
//
// The mailbox (docs/design/m9-budget-and-dashboard §6.4) is granted to two
// other identities, and the rules read both claims rather than minting either:
// mu, the mobile app's user claim, which names the mailbox its holder owns and
// must agree with auth.uid, the identity Firebase itself minted; and mc, the
// mailbox consumer's claim (D113b), which names the mailbox that writer
// serves. Nothing else in the tree reads mu or mc, and a signed-in user who
// carries neither is admitted nowhere.
//
// Writes. Every numeric field of a ledger or counter has its own .write rule
// and no ancestor has one, so a write must name the field (a multi-path
// update with keys like runs/<slug>/<run>/reserved). Writing a whole ledger,
// or deleting a field, is denied: validation rules do not run on a delete.
//
// The project policy (docs/design/project-centric.md §5). config/policy is
// readable *per child*, never on the parent, because an RTDB read grant
// cascades to children: config/policy/shared is readable by any token of the
// project, config/policy/repos/$slug only by the token whose fs claim is that
// slug (as config/caps/repos/$slug is), and config/admin by nobody with a
// client token. No location under config is writable by a run token: admin
// writes are IAM-only (deploy/terraform/gcp/modules/firebase/iam.tf). The
// rules are not what keeps config/admin private from a launcher, which holds
// roles/firebasedatabase.viewer and bypasses them, so nothing
// secret-adjacent may be stored there (design §12a item 7).
package rules

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

//go:embed rules.json.tmpl
var source string

// Limits baked into the rules. Amounts are integer micro-dollars; keeping them
// below 2^53 keeps the rules' double arithmetic exact.
const (
	// MaxAmount bounds every amount and counter (10^13 micro-dollars: $10M).
	MaxAmount int64 = 10_000_000_000_000
	// MaxTokensPerWrite bounds one write's increase of a token count.
	MaxTokensPerWrite int64 = 1_000_000_000
	// MaxCallsPerWrite bounds one write's increase of a call count.
	MaxCallsPerWrite int64 = 10_000
	// MaxKeyLength bounds a model name key.
	MaxKeyLength = 100
	// MaxString bounds every registry string.
	MaxString = 200
	// MaxText bounds one mailbox message's body: D118's size bound per
	// message, the same number the shared schema enforces before a message is
	// written (shared/mailbox-schema's TEXT_MAX), so that the phone and the
	// rules cannot disagree about how much one write may carry. The agreement
	// is not a comment: TestMailboxRulesAgreeWithTheSharedSchema reads
	// shared/mailbox-schema/index.js and fails when either artifact's number
	// moves without the other.
	MaxText = 4096
	// MaxTimestamp bounds every epoch-millisecond instant the rules store: a
	// mailbox message's `at` and the heartbeat's. It is the largest integer a
	// double represents exactly (2^53-1), which is what the shared schema
	// applies (shared/mailbox-schema's AT_MAX) and what every other time in
	// the tree already stays below.
	//
	// It is deliberately not MaxAmount. An amount and an instant are different
	// kinds of number: the first is the project's money and is held below
	// $10M, the second is a clock reading and is held to the exact-double line
	// and nothing else. Holding `at` to MaxAmount would refuse a timestamp the
	// phone's own validator accepts (any time after the year 2286), which is
	// the direction that matters — the database refusing a message the schema
	// passed (the review finding on P2.1).
	MaxTimestamp int64 = 1<<53 - 1
)

// Generate returns the rules JSON, indented and deterministic.
func Generate() ([]byte, error) {
	t, err := template.New("rules").Option("missingkey=error").Parse(source)
	if err != nil {
		return nil, fmt.Errorf("rules: template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]any{
		"MaxTokensPerWrite": MaxTokensPerWrite,
		"MaxCallsPerWrite":  MaxCallsPerWrite,
		"MaxKeyLength":      MaxKeyLength,
		"MaxTextLength":     MaxText,
	}); err != nil {
		return nil, fmt.Errorf("rules: template: %w", err)
	}
	root, err := parse(json.NewDecoder(&buf))
	if err != nil {
		return nil, fmt.Errorf("rules: template is not JSON: %w", err)
	}
	var out bytes.Buffer
	if err := root.render(&out, 0, nil); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	if !json.Valid(out.Bytes()) {
		return nil, fmt.Errorf("rules: generated output is not JSON")
	}
	return out.Bytes(), nil
}

// node is an ordered JSON value (the rule tree keeps the template's order).
type node struct {
	keys []string
	vals []*node
	lit  any // string or bool for a leaf
	obj  bool
}

func parse(d *json.Decoder) (*node, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch v := tok.(type) {
	case json.Delim:
		if v != '{' {
			return nil, fmt.Errorf("unexpected %q", v)
		}
		n := &node{obj: true}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("unexpected key %v", k)
			}
			c, err := parse(d)
			if err != nil {
				return nil, err
			}
			n.keys = append(n.keys, key)
			n.vals = append(n.vals, c)
		}
		if _, err := d.Token(); err != nil { // the closing }
			return nil, err
		}
		return n, nil
	case string, bool:
		return &node{lit: v}, nil
	}
	return nil, fmt.Errorf("unexpected value %v", tok)
}

// render writes n; path holds the location keys from the "rules" root down
// (".read" style keys are attributes, not locations).
func (n *node) render(w *bytes.Buffer, indent int, path []string) error {
	if !n.obj {
		switch v := n.lit.(type) {
		case bool:
			fmt.Fprintf(w, "%v", v)
		case string:
			e, err := expand(v, len(path))
			if err != nil {
				return fmt.Errorf("rules: /%s: %w", strings.Join(path, "/"), err)
			}
			var b bytes.Buffer
			enc := json.NewEncoder(&b)
			enc.SetEscapeHTML(false)
			if err := enc.Encode(e); err != nil {
				return err
			}
			w.Write(bytes.TrimRight(b.Bytes(), "\n"))
		}
		return nil
	}
	pad := strings.Repeat("  ", indent+1)
	w.WriteString("{\n")
	for i, k := range n.keys {
		if i > 0 {
			w.WriteString(",\n")
		}
		w.WriteString(pad)
		kb, _ := json.Marshal(k)
		w.Write(kb)
		w.WriteString(": ")
		child := path
		switch {
		case k == "rules" && len(path) == 0:
		case strings.HasPrefix(k, "."):
		default:
			child = append(append([]string(nil), path...), k)
		}
		if err := n.vals[i].render(w, indent+1, child); err != nil {
			return err
		}
	}
	w.WriteString("\n" + strings.Repeat("  ", indent) + "}")
	return nil
}

// macroRe matches a macro invocation or a bare macro word.
var macroRe = regexp.MustCompile(`\b(?:(O|N|D)\(([^()]*)\)|[A-Z][A-Z_]+\b)`)

// expand replaces every macro of one expression, once, for a location at the
// given depth (the number of keys below the root). Expansions are built from
// Go functions, never re-scanned, so a macro cannot expand into another's
// text by accident.
func expand(expr string, depth int) (string, error) {
	var firstErr error
	out := macroRe.ReplaceAllStringFunc(expr, func(m string) string {
		var s string
		var err error
		if i := strings.IndexByte(m, '('); i > 0 {
			s, err = pathMacro(m[:i], m[i+1:len(m)-1], depth)
		} else {
			s, err = wordMacro(m, depth)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return s
	})
	return out, firstErr
}

const (
	todayExpr     = `('' + ((now - now % 86400000) / 86400000))`
	yesterdayExpr = `('' + ((now - now % 86400000) / 86400000 - 1))`
)

// segment turns one path segment into the expression handed to child().
func segment(s string) (string, error) {
	switch {
	case s == "TODAY":
		return todayExpr, nil
	case s == "YESTERDAY":
		return yesterdayExpr, nil
	case strings.HasPrefix(s, "$"), strings.HasPrefix(s, "auth.token."):
		return s, nil
	case s == "" || strings.ContainsAny(s, "'()"):
		return "", fmt.Errorf("bad path segment %q", s)
	}
	return "'" + s + "'", nil
}

func chain(base, p string) (string, error) {
	var b strings.Builder
	b.WriteString(base)
	for _, s := range strings.Split(p, "/") {
		e, err := segment(s)
		if err != nil {
			return "", err
		}
		b.WriteString(".child(" + e + ")")
	}
	return b.String(), nil
}

// val is the value of a snapshot expression, 0 when the node is absent.
func val(snap string) string { return "(" + snap + ".exists() ? " + snap + ".val() : 0)" }

func pathMacro(name, p string, depth int) (string, error) {
	oldSnap, err := chain("root", p)
	if err != nil {
		return "", err
	}
	newSnap, err := chain("newData"+strings.Repeat(".parent()", depth), p)
	if err != nil {
		return "", err
	}
	switch name {
	case "O":
		return val(oldSnap), nil
	case "N":
		return val(newSnap), nil
	case "D":
		return "(" + val(newSnap) + " - " + val(oldSnap) + ")", nil
	}
	return "", fmt.Errorf("unknown macro %s(", name)
}

// runDelta is how far the day share of the run (slug, run) moved in this
// write: its reserved less its released.
func runDelta(slug, run string, depth int) (string, error) {
	share := "spend/$day/runs/" + slug + "/" + run
	r, err := pathMacro("D", share+"/reserved", depth)
	if err != nil {
		return "", err
	}
	l, err := pathMacro("D", share+"/released", depth)
	if err != nil {
		return "", err
	}
	return "(" + r + " - " + l + ")", nil
}

// userClaim is the mobile app's user claim (D112, D113): a custom claim mu
// that names the mailbox its holder owns, and that must agree with auth.uid —
// the identity Firebase mints itself, which no client can choose. The
// agreement is what makes the claim unusable to reach another account's
// mailbox: a token carrying a claim for someone else's uid satisfies this
// nowhere. It is read, never minted: nothing here writes a claim, and setting
// mu on an Auth user is the owner's own step when an account is added by hand
// (D114).
//
// A signed-in user who carries no mu is admitted nowhere, which is F11's
// property kept: no grant admits a signed-in user on its own.
const userClaim = "(auth != null && auth.token.mu != null && auth.token.mu == auth.uid)"

// wordMacro expands a macro word that takes no path.
func wordMacro(m string, depth int) (string, error) {
	switch m {
	case "NEW":
		return "newData.val()", nil
	case "OLD":
		return "(data.exists() ? data.val() : 0)", nil
	case "TODAY":
		return todayExpr, nil
	case "YESTERDAY":
		return yesterdayExpr, nil
	case "TOKEN":
		return "(auth != null && auth.token.fp != null && auth.token.fp == root.child('fugaro/project').val() && auth.token.fx > now)", nil
	case "USER":
		// The mobile app's reader claim (D112): the families its dashboard
		// reads are granted on this and nothing else. It takes no path — no
		// $mb is in scope at the config and spend nodes it is used at — and
		// it names no mailbox, so it cannot be used to reach one.
		return userClaim, nil
	case "MAILBOX":
		// The owner of the mailbox $mb: the user claim, plus $mb naming the
		// same account. Both halves are needed: the first says the token
		// carries mu at all and that it agrees with auth.uid, the second that
		// the location being written is that account's own mailbox, not
		// another's.
		return "(" + userClaim + " && $mb == auth.uid)", nil
	case "CONSUMER":
		// The mailbox's consumer (D113b): a token whose mc claim names the
		// mailbox it serves. It is what may write the outbox and the
		// heartbeat, and nothing else — in particular it may read no mailbox,
		// its own or another's.
		return "(auth != null && auth.token.mc != null && auth.token.mc == $mb)", nil
	case "RUN":
		return "(auth != null && auth.token.fp != null && auth.token.fp == root.child('fugaro/project').val() && auth.token.fx > now && auth.token.fs == $slug && auth.token.fr == $run)", nil
	case "DAYOK":
		return "($day == " + todayExpr + " || $day == " + yesterdayExpr + ")", nil
	case "AMOUNT":
		return fmt.Sprintf("(newData.isNumber() && newData.val() >= 0 && newData.val() <= %d && newData.val() %% 1 == 0)", MaxAmount), nil
	case "TIMESTAMP":
		// An epoch-millisecond instant, bounded at the schema's own AT_MAX
		// (MaxTimestamp), so a message the phone's validator accepts is not
		// refused by the database. Not AMOUNT: see MaxTimestamp.
		return fmt.Sprintf("(newData.isNumber() && newData.val() >= 0 && newData.val() <= %d && newData.val() %% 1 == 0)", MaxTimestamp), nil
	case "STR":
		return fmt.Sprintf("(newData.isString() && newData.val().length <= %d)", MaxString), nil
	case "RBOK":
		return "(auth.token.rb != null && newData.parent().child('requestedBy').val() == auth.token.rb)", nil
	case "MAXRES":
		return "root.child('config/limits/maxReserveMicros').val()", nil
	case "ENFORCE":
		return "(root.child('config/mode').val() == 'enforce')", nil
	case "KILLS_OFF":
		return "(root.child('config/kill/global/on').val() != true && root.child('config/kill/repos').child($slug).child('on').val() != true)", nil
	case "REPODAILY":
		return fallback("dailyMicros", "repoDailyMicros"), nil
	case "PERRUNOK":
		return perRunOK(depth)
	case "GLOBALDAILY":
		return "root.child('config/caps/global/dailyMicros').val()", nil
	case "RUNDELTA":
		// The caller's own share (counters are written by any run of the
		// token's identity, never by name).
		return runDelta("auth.token.fs", "auth.token.fr", depth)
	case "COUNTERS_FOLLOW":
		// This share's delta equals the delta of both shared counters.
		d, err := runDelta("$slug", "$run", depth)
		if err != nil {
			return "", err
		}
		rc, err := pathMacro("D", "spend/$day/repos/$slug/counted", depth)
		if err != nil {
			return "", err
		}
		gc, err := pathMacro("D", "spend/$day/global/counted", depth)
		if err != nil {
			return "", err
		}
		return "(" + rc + " == " + d + " && " + gc + " == " + d + ")", nil
	}
	return "", fmt.Errorf("unknown macro %s", m)
}

// fallback is the repository's own cap, else the default, else null (which
// makes every comparison false: the rules deny).
func fallback(own, def string) string {
	snap := "root.child('config/caps/repos').child($slug).child('" + own + "')"
	return "(" + snap + ".exists() ? " + snap + ".val() : root.child('config/caps/defaults/" + def + "').val())"
}

// tern is the rules language's conditional: (cond ? a : b). The language has
// no coalesce, so a fallback is written as one of these.
func tern(cond, a, b string) string { return "(" + cond + " ? " + a + " : " + b + ")" }

// perRunOK is the lease's per-run cap comparison, the rules' half of
// Caps.PerRun (design D6): the effective cap is the smaller of the
// repository's own cap -- its node, else the default -- and the project's
// global one, and a side that is not set does not bound the run. With no
// per-run cap set anywhere the expression is false and the rules deny the
// lease (fail closed: a missing node reads null and every comparison against
// it is false).
//
// It is one boolean, not two comparisons, because a comparison against an
// unset side must not deny on its own: the global cap is the fallback of a
// repository that has none, and the default is the fallback of a global one.
func perRunOK(depth int) (string, error) {
	released, err := pathMacro("N", "runs/$slug/$run/released", depth)
	if err != nil {
		return "", err
	}
	// The run's lifetime outstanding after this write: the new reserved less
	// what it released. Written out rather than as the NEW macro, so that
	// expansion stays a single pass (a macro's expansion is never re-scanned).
	delta := "(newData.val() - " + released + ")"
	own := "root.child('config/caps/repos').child($slug).child('perRunMicros')"
	def := "root.child('config/caps/defaults/repoPerRunMicros')"
	global := "root.child('config/caps/global/perRunMicros')"
	// within is one side's bound: the run's outstanding, against the cap
	// that node holds.
	within := func(snap string) string { return "(" + delta + " <= " + snap + ".val())" }
	hasOwn, hasDef, hasGlobal := own+".exists()", def+".exists()", global+".exists()"
	// The repository's side is its own cap, else the default; the global
	// side, where it is set, must hold too. A side that is not set does not
	// bound the run: the global one is the fallback of a repository whose
	// own side is unset, and with nothing set anywhere the expression is
	// false (deny).
	repoSide := tern(hasOwn, within(own), within(def))
	return tern(hasOwn+" || "+hasDef,
		repoSide+" && "+tern(hasGlobal, within(global), "true"),
		tern(hasGlobal, within(global), "false")), nil
}
