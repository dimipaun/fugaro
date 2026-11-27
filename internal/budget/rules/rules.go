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
// must agree with auth.uid (which binds against clients, not against whoever
// mints the custom token: see userClaim); and mc, the
// mailbox consumer's claim (D113b), which names the mailbox that writer
// serves. Nothing else in the tree reads mu or mc, and a signed-in user who
// carries neither is admitted nowhere.
//
// A third identity is the pairing session (mobile D165, plan task P6.6): pu, a
// claim naming the one-shot pairing uid whose request node (the phone's public
// key) its holder may write once and whose delivery node (token and MAC key,
// encrypted to that key) it may read. It is read and never minted here, like
// the other two, and it is the identity that exists before a device does: a
// paired phone's token carries mu and needs the device node below, and a
// pairing session carries pu and reaches the two pairing nodes and nothing
// else.
//
// Writes. Every numeric field of a ledger or counter has its own .write rule
// and no ancestor has one, so a write must name the field (a multi-path
// update with keys like runs/<slug>/<run>/reserved). Writing a whole ledger,
// or deleting a field, is denied: validation rules do not run on a delete.
//
// The project policy (docs/project-centric/spec.md §5). config/policy is
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
	"strconv"
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

	// MaxMacLength is the length of a message's or a heartbeat's MAC, in hex
	// characters: 32 bytes of HMAC-SHA-256 (§3.5, D169, D171, O7). The rules
	// check the length and the lowercase-hex characters (patternHex), never the
	// value: verifying a tag needs the key, which the database never holds.
	// TestMailboxRulesAgreeWithTheSharedSchema holds the number to the schema's
	// MAC_MAX.
	MaxMacLength = 64
	// MaxAgentLength bounds an agent id: the value that travels in a message's
	// `agent` field and that the consumer advertises in its heartbeat, so that
	// "talk to <agent>" can match one against the other (D161, D163). It is the
	// schema's AGENT_MAX.
	MaxAgentLength = 64
	// MaxAgentNameLength bounds an advertised agent's display name. It is the
	// schema's AGENT_NAME_MAX.
	MaxAgentNameLength = 64
	// MaxAgents bounds the advertised agent list of one heartbeat: a list nobody
	// can hold is not one a voice interface can read out. It is the schema's
	// AGENTS_MAX.
	MaxAgents = 32
	// MaxOptions bounds a prompt's choice list, and MaxOptionLength one choice:
	// the app reads the list aloud and the answer names a choice by position
	// ("option two"), so both are bounded (the schema's OPTIONS_MAX and
	// OPTION_MAX).
	MaxOptions      = 8
	MaxOptionLength = 200
	// MaxTargetLength bounds an action target's run id or repository slug. It is
	// the schema's TARGET_MAX; the characters are the schema's RUN_PATTERN and
	// REPO_PATTERN (patternRun, patternRepo).
	MaxTargetLength = 200
	// MinShaLength and MaxShaLength are the two accepted lengths of a merge's
	// head SHA: Git's SHA-1 object name, and a SHA-256 repository (the schema's
	// SHA_MIN and SHA_MAX). The rules accept exactly those two lengths, as the
	// schema does, so neither side accepts a SHA the other refuses.
	MinShaLength = 40
	MaxShaLength = 64
	// MaxPublicKey bounds the public key a pairing request carries (the encoded
	// X25519 public key is ~44 characters; 4096 is generous and still a bound).
	// It is this package's own number: P6.3's schema does not model the pairing
	// nodes, which are the rules' half of D165 — the node a phone writes before
	// it holds anything else.
	MaxPublicKey = 4096
	// MaxBootLength bounds a heartbeat's boot id: the schema's BOOT_MAX.
	MaxBootLength = 64
)

// The character patterns the rules apply, each the shared schema's own regex
// literal, byte for byte (TestMailboxRulesCheckTheSchemasCharacters reads
// shared/mailbox-schema/index.js and fails when either side moves). The rules
// language has `String.matches(regex)`, so the characters of a tag, a head
// SHA, an identity and a target are checked at the database as well as by the
// schema. What stays the schema's alone is the free text (`text`, `sender`, an
// option, an agent's display name), where the schema refuses only control
// characters and lone surrogates, a class this file does not spell in the
// rules' regex dialect.
const (
	patternHex      = `/^[0-9a-f]+$/`
	patternIdentity = `/^[A-Za-z0-9._:-]+$/`
	patternRun      = `/^[A-Za-z0-9_:][A-Za-z0-9._:-]*$/`
	patternRepo     = `/^[A-Za-z0-9_][A-Za-z0-9._-]*\/[A-Za-z0-9_][A-Za-z0-9._-]*$/`
	// patternPublicKey is the pairing request's key: base64 or base64url text.
	// It is this package's own, like MaxPublicKey: P6.3's schema does not model
	// the pairing nodes.
	patternPublicKey = `/^[A-Za-z0-9+\/=_-]+$/`
)

// indexPattern is a rules regex literal matching exactly the list indexes
// 0..n-1 as the database stores them (decimal, no leading zero). A list's
// children are keyed by index, so "every key matches" is "at most n entries":
// the rules language has no count of a node's children (DataSnapshot's
// numChildren is the client SDK's, not the rules'; a document that calls it is
// refused at deploy), so the count is bounded through the keys.
func indexPattern(n int) string {
	alts := make([]string, n)
	for i := range alts {
		alts[i] = strconv.Itoa(i)
	}
	return "/^(" + strings.Join(alts, "|") + ")$/"
}

// Generate returns the rules JSON, indented and deterministic.
func Generate() ([]byte, error) {
	t, err := template.New("rules").Option("missingkey=error").Parse(source)
	if err != nil {
		return nil, fmt.Errorf("rules: template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]any{
		"MaxTokensPerWrite":  MaxTokensPerWrite,
		"MaxCallsPerWrite":   MaxCallsPerWrite,
		"MaxKeyLength":       MaxKeyLength,
		"MaxTextLength":      MaxText,
		"MaxTimestamp":       MaxTimestamp,
		"MaxAgentNameLength": MaxAgentNameLength,
		"MaxOptionLength":    MaxOptionLength,
	}); err != nil {
		return nil, fmt.Errorf("rules: template: %w", err)
	}
	root, err := parse(json.NewDecoder(&buf))
	if err != nil {
		return nil, fmt.Errorf("rules: template is not JSON: %w", err)
	}
	// The workspace family's nodes are part of this document and not a document
	// of their own (workspace.go): a database carries one rules document, and
	// the workspace's own project's database is an installation's, so the record,
	// the admitted-token hashes, the meter and the rollup are denied here, beside
	// the budget family's own nodes.
	family, err := workspaceRules()
	if err != nil {
		return nil, err
	}
	if err := mergeWorkspaceRules(root, family); err != nil {
		return nil, err
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

// PathDevices is the device allowlist of D166 (mobile plan task P6.4): the
// parent of one child per paired device, keyed by that device's Firebase Auth
// uid, so the child `config/admin/devices/<uid>` existing IS "this account is
// paired to a phone that has not been revoked". The rules read it in USER
// (userClaim); the node is written and deleted over IAM by `fugaro pair` and
// `fugaro pair revoke` (plan task P6.6, which reads this constant rather than
// spelling the path again), so no client token holds a grant on it — the
// template carries the two locations under config/admin/devices with
// `.read: false` and `.write: false` to say so in the deployed document.
//
// It is deliberately not a claim. A claim is minted by whoever signs the
// custom token and cannot be taken back before the ID token expires (G14),
// while deleting this node revokes at once: the next read or write of every
// family USER reaches fails its existence test. Deleting an Auth user does not
// revoke on its own — an issued ID token keeps passing until it expires — which
// is the whole reason D166 chose a node.
//
// The entry carries the administered email and the role as children beside the
// pairing instant (D222, D225), named by internal/mobileauth's NodeFieldEmail
// and NodeFieldRole so that the writer and every reader share one spelling.
// The rules read neither of them in v1: a role predicate is D225's later half
// and is not this work's, so a role on this node is recorded authority and not
// yet enforced authority.
//
// Those fields take no rules generation, and the deployed document does not
// move (reconciliation §F29): it declares no `.validate` on this node and no
// `$other` beneath it, USER turns on the node's *existence* alone —
// `root.child('config/admin/devices').child(auth.uid).exists()` — and nothing
// in it names a child of the node. So an entry written before the fields
// existed admits its holder exactly as one carrying them does, and a project
// whose rules were deployed before this work needs no redeploy.
// internal/projectpolicy/version.go's generation-3 note is the precedent:
// optional children of a node that behaves identically without them owe no
// bump, where a bump would make `projectpolicy.Read` answer "not found" and
// drop every published policy on an installation whose owner has not redeployed
// the rules yet.
//
// What would move the document is not the fields but a rule that reads one of
// them, or a `.validate` over the node's shape: that is a generation, with
// internal/projectpolicy/version.go's RulesVersion bumped and the goldens
// regenerated — a decision of its own, taken on purpose by whoever needs it
// rather than discovered by whoever writes the first predicate that touches
// these children.
const PathDevices = "config/admin/devices"

// userClaim is the mobile app's user claim (D112, D113): a custom claim mu
// that names the mailbox its holder owns, that must agree with auth.uid, and
// that the device allowlist of D166 still holds. It is read, never minted:
// nothing here writes a claim, and setting mu on an Auth user is the owner's
// own step when an account is added by hand (D114).
//
// The device conjunct (D166, plan task P6.4) is the one part of this predicate
// an operator can take away in one write: `fugaro pair revoke` deletes the
// child, and every family USER reaches — the caps, the day counters, the kill
// switches, the mailbox and its ack — is refused from the next request on,
// without waiting for an ID token to expire. It is also the half that makes a
// *minted* token insufficient on its own: a launcher can mint uid == mu ==
// another account's mailbox (see the residual below), but it may not write
// config/admin/devices/ (that needs roles/firebasedatabase.admin, which
// iam.tf grants to admins and budget admins only, mobile reconciliation G13).
// What it does NOT close is the MAC's half of the boundary — the rules cannot
// verify an HMAC — and it does not close a principal who holds database admin:
// such a principal can write the device node as well as the token, and is
// exactly the IAM reader that bypasses the rules in any case.
//
// What the agreement binds, exactly. A client cannot choose its uid or its
// claims: both come from the custom token it signed in with. But phones sign
// in with custom tokens only (owner decision T5, D165), and whoever mints a
// custom token chooses both the uid and every claim in it. That is any
// principal that can sign for the project — a launcher (signJwt through
// deploy/terraform/gcp/modules/firebase/iam.tf's token_minter role), or
// anyone holding a key of one of the project's service accounts, since
// Firebase accepts custom tokens signed by any of them. Such a principal can
// mint uid == mu == another account's mailbox and pass USER and MAILBOX: read
// that account's outbox and heartbeat, write its inbox, and forge an outbox
// ack — the ack is the phone's MAILBOX write (one field on a message that is
// already there), so a minted token can mark as spoken a message the phone
// never spoke, which is what makes the consumer stop re-delivering it. So the
// agreement keeps one client out of another's mailbox and binds nothing
// against a minter. Against launcher-level principals the confidentiality of
// the outbox and heartbeat is not protected by these rules (launchers also
// hold roles/firebasedatabase.viewer, an IAM read that bypasses the rules,
// mobile reconciliation G13), and neither is the integrity of the heartbeat
// (CONSUMER's mc is minted the same way) nor of the ack. Two of the three
// things phase 6 adds are now in this file: the device node USER requires
// (D166, plan task P6.4, PathDevices) narrows a minted token to an account a
// device node names, and a `mac` is required on every message of every kind
// and on the heartbeat (D169, D171, O7, P6.3's schema), so an inbox write a
// minter invented is one `fugaro mailbox` will refuse for want of a valid tag
// (P6.7 verifies it; the rules can only require the field). What remains is
// the part no rule can close, because a launcher-level principal holds
// roles/firebasedatabase.admin beside the signer — it can write the device
// node as well as the token — and because a launcher reads every mailbox
// through the IAM read whatever the rules say. The narrower residual is
// pinned by TestMailboxResidualMintedTokenNeedsTheDeviceNode.
//
// No claim the rules could add closes it: any claim they read (an fp, an
// expiry) is one the minter writes too, and Firebase already ties a token to
// one project by its signature and audience.
//
// A signed-in user who carries no mu is admitted nowhere, which is F11's
// property kept: no grant admits a signed-in user on its own.
const userClaim = "(auth != null && auth.token.mu != null && auth.token.mu == auth.uid && root.child('config/admin/devices').child(auth.uid).exists())"

// pairingClaim is the pairing session's claim (D165, plan task P6.6): a
// custom claim pu that names the one-shot pairing uid whose two nodes its
// holder may use. It is read, never minted, like mu and mc, and it is the
// identity that exists *before* a device does.
//
// It is a claim *and* a test on the shape of auth.uid. The claim is what the
// whole mailbox design reads: a client chooses neither its uid nor its claims.
// The uid test, `auth.uid.beginsWith('p~')`, binds the session to the
// `p~<random>` uid P6.6 mints for it, so a pu claim minted onto any other uid
// (a device's `m~` uid, an account's own) opens no pairing node under it. P6.6
// must mint both: the pu claim, and a uid that begins with `p~`.
//
// What a minted pu reaches, stated: a launcher can mint pu == auth.uid == a
// uid of its choosing, so it can write that uid's own request node — a public
// key, into a node the real phone would then find already written, because the
// write is once — and read that uid's delivery node, which is the payload
// encrypted to a key the launcher does not hold. Neither is a way in: D165's
// binding is the operator-typed fingerprint of the key in the request, and a
// launcher that cannot open the ciphertext learns nothing from it. The
// one-shot property the same claim must not break is the write-once rule on
// the request node below.
const pairingClaim = "(auth != null && auth.token.pu != null && auth.token.pu == auth.uid && auth.uid.beginsWith('p~'))"

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
	case "PAIRING":
		// The pairing session (D165): the pu claim, plus $uid naming the same
		// uid. Both halves are needed for MAILBOX's reason — the first says the
		// token carries pu at all, the second that the location is that
		// session's own pairing node and not another's.
		return "(" + pairingClaim + " && $uid == auth.uid)", nil
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
		return fallback("dailyMicros", "repoDailyMicros") + dailyClamp(), nil
	case "KINDREQ":
		// The per-kind *required* half of a message's shape (P6.3's
		// KIND_REQUIRED): an `answer` must name the prompt it answers, an
		// `action` must carry its action record, and a `registration` — the kind
		// P5.2.2 adds — must carry the token and the platform it registers, which
		// the schema requires of that kind too. The other half — the fields a
		// kind may not carry — is on the field's own node (see PARENTKIND),
		// where it cannot be reached by a kind the message does not have.
		k := kindOf("newData")
		return "((" + k + " == 'answer' ? newData.child('reply_to').exists() : true) && (" +
			k + " == 'action' ? newData.child('action').exists() : true) && (" +
			k + " == 'registration' ? (newData.child('token').exists() && newData.child('platform').exists()) : true))", nil
	case "PARENTKIND":
		// A field's own kind test: the kind of the message this field is on,
		// read through parent() because the rules give a validating node no
		// other way to see its parent's data. Used to refuse a field on the
		// wrong kind (`options` outside a prompt, `action` outside an action),
		// which P6.3 refuses as field-not-allowed rather than as an unknown
		// field: two agents disagreeing about whether a message is an action is
		// the ambiguity the (kind, field) table exists to remove.
		return kindOf("newData.parent()"), nil
	case "TARGETOK":
		// D169's verb/target agreement, as the schema resolves it: `merge`
		// resolves to a repository and a pull request, `cancel` to a run,
		// `confirm` to either, and `launch` — the verb O10 adds (mobile plan
		// task J5r) — to a repository alone, because a start names the
		// repository it starts in and nothing else. A target that carries both
		// shapes, or neither, is refused on both sides.
		//
		// Every arm is *exclusive*: the node has exactly the fields its shape
		// names and none of the other shape's. The negative half is not
		// decoration — a `confirm` written as `hasChildren(['run']) ||
		// hasChildren(['repo', 'pr'])` accepts `{run, repo, pr}`, which is a
		// target whose resolution the consumer has to guess at, on the one verb
		// that authorises a destructive action (G11).
		v := "newData.parent().child('verb').val()"
		run := "(newData.hasChildren(['run']) && !newData.child('repo').exists() && !newData.child('pr').exists())"
		pull := "(newData.hasChildren(['repo', 'pr']) && !newData.child('run').exists())"
		repo := "(newData.hasChildren(['repo']) && !newData.child('run').exists() && !newData.child('pr').exists())"
		return "(" + v + " == 'merge' ? " + pull + " : (" +
			v + " == 'cancel' ? " + run + " : (" +
			v + " == 'launch' ? " + repo + " : (" + run + " || " + pull + "))))", nil
	case "SHAOK":
		// A merge's head SHA: present only on a merge (the record has no field
		// for it on any other verb), and one of Git's two object-name lengths.
		return fmt.Sprintf("(newData.parent().child('verb').val() == 'merge' && newData.isString() && (newData.val().length == %d || newData.val().length == %d) && newData.val().matches(%s))", MinShaLength, MaxShaLength, patternHex), nil
	case "MACOK":
		// A tag's shape: a string of exactly MAC_MAX characters, every one of
		// them the schema's lowercase hex (HEX_PATTERN). The characters are
		// checked; the *value* never is, and cannot be: verifying a tag needs
		// the key, which is D171's and never passes through the database, so the
		// rules can require a well-formed tag and nothing more (P6.7 is what
		// verifies one). PARENTMACOK applies the same expression one node up,
		// through the shared helper, and SIGOK/PARENTSIGOK are the same pair for
		// the device signature O6 puts in `sig`'s place.
		return macOK("newData"), nil
	case "SIGOK":
		// A device signature's shape (O6, mobile plan task J5r): a string of
		// exactly MaxSigLength characters of unpadded base64url, checked for
		// shape alone. Which scheme a device must use is not this document's to
		// say — a node with a public key accepts signatures, one without accepts
		// a MAC (J5b) — and both are accepted here because a phone paired the
		// old way keeps its MAC until it re-pairs.
		return sigOK("newData"), nil
	case "TAGOK":
		// Exactly one of the two tags, on a message and on the heartbeat: `mac`
		// (D171) or `sig` (O6), never both and never neither. It replaces the
		// `mac` member of the message's own hasChildren list, so a message with
		// no tag at all is refused here rather than stored untagged — which is
		// the whole of what the tag buys (§6.4: without one, a launcher that can
		// mint a token for the account writes into the mailbox).
		return exactlyOneTag("newData"), nil
	case "MODEOK":
		// A launch action's `mode` (O10, mobile plan task J5r): a bounded
		// identity string and nothing more. The vocabulary is internal/rigor's
		// and the costly modes are the published policy's
		// (mobile.costly_modes), so which modes need a read-back is the gate's
		// decision (J5a) and this document's job is only to see that the field
		// is a well-formed short string — a bound rather than a list, because a
		// sixth rigor mode must not be a rules generation.
		return boundedMatch(MaxModeLength, patternIdentity), nil
	case "BRIEFOK":
		// The shaped task a launch's "go" carries (P5.4.2): the shared schema's
		// optional `brief`, a bounded non-empty string like the message's own
		// `text` and for its reason — the schema states no character class for a
		// task, so this gates the bound and non-emptiness and the characters
		// stay the schema's. It is a launch's field alone: the schema refuses a
		// brief beside any other verb, and a field on a kind that cannot carry
		// one is refused on its own node (PARENTKIND, at the template).
		//
		// The message carries the agent the brief is for, which the schema and
		// internal/mailbox refuse a brief without (D161, and the rule the
		// schema's own comment gives it): a brief from nobody is a turn the
		// consumer has nothing to route by, and the inbox is write-once, so a
		// launch the database stores and the consumer refuses is one nobody can
		// retire — the fence has to be the same one the reader holds.
		return fmt.Sprintf("(newData.parent().child('agent').exists() && newData.parent().child('action').child('verb').val() == 'launch' && newData.isString() && newData.val().length > 0 && newData.val().length <= %d)", MaxBriefLength), nil
	case "AGENTID":
		// An agent id: the schema's identity, at most AGENT_MAX.
		return boundedMatch(MaxAgentLength, patternIdentity), nil
	case "BOOTOK":
		// A heartbeat's boot id: the schema's identity, at most BOOT_MAX.
		return boundedMatch(MaxBootLength, patternIdentity), nil
	case "RUNTARGET":
		return boundedMatch(MaxTargetLength, patternRun), nil
	case "REPOTARGET":
		return boundedMatch(MaxTargetLength, patternRepo), nil
	case "PUBKEY":
		return boundedMatch(MaxPublicKey, patternPublicKey), nil
	case "OPTIONIDX":
		// A prompt's option sits at an index below OPTIONS_MAX.
		return "$i.matches(" + indexPattern(MaxOptions) + ")", nil
	case "AGENTIDX":
		// An advertised agent sits at an index below AGENTS_MAX.
		return "$i.matches(" + indexPattern(MaxAgents) + ")", nil
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
	case "TOKENOK":
		// A registration token's bound, and nothing else about its characters:
		// the providers' own alphabets differ (an FCM token is URL-safe base64
		// carrying a `:`, an APNs token is hex, an Expo token is bracketed), so
		// a character class here would refuse a real token — and a refused token
		// is a phone that never receives another notification. The schema's
		// tokenField makes the same choice for the same reason; what the two
		// artifacts do agree on is the bound (MaxTokenLength, the schema's
		// TOKEN_MAX) and non-emptiness, which is what makes a token an identity
		// of a known shape rather than a body.
		return fmt.Sprintf("(newData.isString() && newData.val().length > 0 && newData.val().length <= %d)", MaxTokenLength), nil
	case "PLATFORMOK":
		// A registration's platform: the two names §3.4's device node carries, a
		// closed vocabulary like the kind enumeration and for the same reason —
		// the consumer reads it to decide which provider's endpoint it addresses,
		// and a platform it has never heard of is not one it can send to.
		return "(newData.isString() && (newData.val() == 'ios' || newData.val() == 'android'))", nil
	case "ZONEOK":
		// A quiet-hours zone: the schema's ZONE_PATTERN at the schema's ZONE_MAX,
		// which is an IANA name and not an offset — a stored offset is a fixed
		// offset, and it drifts onto the wrong hour at every DST transition.
		return boundedMatch(MaxZoneLength, patternZone), nil
	case "MINUTEOK":
		// One end of a quiet-hours window: an integer minute of the local day, 0
		// through MaxMinute (the schema's MINUTE_MAX). A float, a string, a
		// negative minute and a minute past the day are each something else, and
		// each is refused rather than floored, as the schema's minuteField
		// refuses them.
		return fmt.Sprintf("(newData.isNumber() && newData.val() >= 0 && newData.val() <= %d && newData.val() %% 1 == 0)", MaxMinute), nil
	case "PARENTMACOK":
		// The tag of the message a field sits on, read from the field: the shape
		// MACOK checks, one node up. It is a restatement — hasChildren on the
		// message already requires a tag and each tag's own node already refuses
		// a malformed one — and the registration's three fields carry it because
		// they are the fields a consumer trusts with a device's address and the
		// token that reaches it: what gates them is then the tag itself and not a
		// list on another node, so an edit that shortens that list cannot leave
		// them valid on a message whose tag is absent or badly shaped.
		return macOK("newData.parent().child('mac')"), nil
	case "PARENTSIGOK":
		// The device signature of the message a field sits on, read from the
		// field: the shape SIGOK checks, one node up, and PARENTMACOK's sibling.
		// The registration's three fields carry both, because the scheme a
		// device writes under is the device node's (J5b) and this document
		// cannot read one: a re-paired phone registers with a signature and has
		// no MAC to offer.
		return sigOK("newData.parent().child('sig')"), nil
	case "REPORTSJOB":
		// The reports job's own identity (E12, F27): the one reader of the
		// ladder's ledger. It is a custom-token claim and deliberately not the
		// job's service account, because these rules bind client tokens only —
		// a service account reads the database over IAM, whose binding has no
		// path-level scope and never reaches this file
		// (deploy/terraform/gcp/modules/installation/state_read.tf, and the
		// orchestrator node's own note below), so a grant written for the
		// account would be a fence that looks like one and is not. The mint is
		// the job's own half and is not in this release: it takes signJwt on
		// its own account the way a run's token takes it on the launcher's
		// (internal/budget/token), and until it exists no token the rules
		// evaluate carries this claim, so the node is inert rather than open.
		return reportsJobClaim, nil
	}
	return "", fmt.Errorf("unknown macro %s", m)
}

// reportsJobClaim is the reports job's claim (E12, F27): what may read the
// escalation block of the ladder's ledger and nothing else of the database.
//
// Its shape is TOKEN's project half and nothing more, because the location it
// is granted at names no subject to bind it to: the node is
// `orchestrator/$id/...`, and `$id` is the orchestrator's id and not the
// reader's, so there is no `$mb`-shaped half for the claim to agree with. What
// it says is therefore "a token minted for this installation's reports job",
// which is as strong as every other mint here: whoever mints a custom token
// chooses the claims in it, and a launcher that can mint a run's token can
// already mint anything TOKEN and RUN are built on.
//
// Finding the block is the reader's own job, and the two halves of the path are
// found differently. `$id` is the project's name — `orchestrator/<project>/…` is
// what the tree is (`internal/cli`'s orchestratorID: "the orchestrator id is the
// project's name") and it is the same value this claim compares against, so a
// reader that holds the claim already knows it. `$item` is a position in the
// work items array (`orchestrator.State.WorkItems` is a slice), which shifts as
// items are archived out of it, and the reader may not list it: the node holding
// the array carries no rule of its own and is refused. So it walks the positions
// — the array is bounded by `orchestrator.MaxWorkItems`, and each read is one
// block — and takes what it finds there, which is why the block itself carries
// the item's `repo` and the run its last move was about (E12): the reader
// filters and joins on those and never on the position it found the block at.
const reportsJobClaim = "(auth != null && auth.token.rj != null && auth.token.rj == root.child('fugaro/project').val())"

// PathMCPAuth is the hosted MCP server's own subtree (docs/mcp-server/spec.md
// §6 and §8; D922, answered and restated on 2026-11-04): the token records —
// hashes only, never a value — the client registrations, the redirect URIs,
// the revocation state and the whole tool-call audit. It is a top-level
// sibling of `coordinator/` and deliberately not a child of it, so the
// coordinator's shared view and the token records are not one node.
//
// The template carries it with `.read: false` and `.write: false`, written out
// rather than left to the root's own denial, and deliberately not
// `coordinator/`'s `.read: "USER"`: D195 has a signed-in person read the
// coordinator's state, and D922's answer refuses that same person every read
// of this one. Nothing a client token holds admits it: a rule binds a client
// token and refuses this node to every one of them. It binds no IAM identity,
// and the coordinator's account is not the node's only writer — the
// installation grants roles/firebasedatabase.viewer to every launcher and
// operator (deploy/terraform/gcp/modules/firebase/iam.tf:44) and
// roles/firebasedatabase.admin to the admins and budget admins
// (deploy/terraform/gcp/modules/firebase/iam.tf:53), to the history service
// account (deploy/terraform/gcp/modules/firebase/iam.tf:81) and to the
// coordinator's own writer (deploy/terraform/gcp/modules/firebase/iam.tf:141),
// and every one of those reads and writes every node past these rules — the
// fact the package comment above states for config/admin and
// docs/roles/spec.md:104 for the database as a whole. So the node is written
// as a refusal beside the IAM grants rather than as a grant of its own, and the
// exposure those grants leave is not one a rule here can close: it is an open
// item on the plan's M3 (docs/mcp-server/implementation-plan.md) for the owner
// to decide — another store, narrower IAM, or the exposure accepted and the
// spec corrected. Without the read being false, the client list, the redirect
// URIs, the revocation state and the audit would be readable by every client
// the installation admits, and "store only hashes" would protect the token
// values and nothing else.
//
// It sits below the macro table rather than beside PathDevices because a
// constant added above it would move the lines internal/cli's voice-switch
// test reads out of this file to resolve a pinned citation.
const PathMCPAuth = "mcpauth"

// PathBuildLeases is the build lease's own family (T8 of
// docs/no-prompt-policy/implementation-plan.md; D1543, F19, F22): one node per
// submission, buildleases/<slug>/<id>, holding the reservation a build takes
// before it is submitted, what it really spent and what was given back.
// It is deliberately not under runs/: a lease keyed by a run id would be read
// as a run by everything that reads runs/ (runstore, watch, the console,
// internal/budget's own Outstanding), and F22 asks for a key space of its own
// so a lease's reader and a run's never meet.
//
// The template carries it with `.read: false` and `.write: false`, written out
// rather than left to the root's denial, for the reason PathMCPAuth gives above.
// Its writers are IAM identities and never client tokens. The CLI's lease
// writer (internal/cli/image_cost.go) runs on the person's credentials
// (internal/cli/cloud.go's newBudgetClient, ADC with the budget scopes), and the
// project's catalog-check job would hold its lease on the job's service account;
// neither is a token these rules can bound. The one client token the document
// knows how to bind — a run's, fs/fr — is not a build lease's writer: nothing in
// the container submits a build. So the family may be named and denied and
// nothing more, and naming it is what keeps the next grant to it from being an
// edit under a path nobody reading this file could see.
//
// Denied whole, it is what internal/projectpolicy's Version documents as "a
// generation that only adds a deny, or a node no client may reach": the family
// is not opened to any client, so it does not move RulesVersion and `fugaro
// update` carries no migration for it — the rules stage already redeploys a
// document `SameJSON` reports as different (internal/infra/firebasedb.go's
// Plan). Opening it to a client later is a grant, and that is the change the
// generation bump is for.
//
// Like PathMCPAuth, it sits below the macro table rather than beside
// PathDevices: a constant added above those citations moves the lines the tests
// that resolve them read.
const PathBuildLeases = "buildleases"

// PathOrchestrator is the stateless orchestrator's own subtree
// (docs/stateless-orchestrator/implementation-plan.md's O2; D1220, D1241
// answered on 2026-11-04): one state document per orchestrator id, plus its
// lease and its audit, under `orchestrator/<id>/`.
//
// The template carries it with `.read: "USER"` and `.write: false`, and the
// write is the half that matters: the person's own credential writes the node
// (D1241's answer), and IAM bypasses `.write` and `.validate` entirely
// (reconciliation.md, "What the rules do and do not bind"), so the rules bind
// no writer here — what they do is refuse every identity they *do* evaluate. A
// run-scoped job token, a custom token the launcher's `signJwt` minter made
// (`iam.tf`'s `minter` grant) and the phone's own `mu` claim are all refused,
// and the refusal is a flat `false` rather than a denylist of run-scoped jobs
// with a shape check behind it, because a shape check is O0's client-side
// validator and never the rules' business: a value that validates is still a
// write this subtree refuses.
//
// The read is `USER`, the paired phone's `mu` claim (D166) — the same reader
// the coordinator's state has — so the person can read the node over a client
// token; the CLI's own read is the person's OAuth Bearer, which no rule here
// binds either.
//
// It sits below the macro table for PathMCPAuth's reason: a constant added
// above it would move the lines internal/cli's voice-switch test reads out of
// this file to resolve a pinned citation.
const PathOrchestrator = "orchestrator"

// PathVoice is the voice policy subtree (docs/mobile/design/
// voice-evaluation-switch.md §4.3; mobile plan Task P5.3.1, approved by the
// owner on 2026-11-14, D123 and Q0 answered on 2026-10-19): the provider
// allow-list an operator publishes per project, and the one node the switch's
// server-side hop reads to answer whether a project serves voice remotely. It
// carries the three fields the design names and no date — `providers`,
// `direction` and `force_local` — because the register of what the owner read
// about each provider lives with the hop and not in the policy node.
//
// The template carries `config/policy/voice/shared` and
// `config/policy/voice/repos/$slug` field for field as `config/policy/shared`
// and `config/policy/repos/$slug`: `TOKEN` on the shared node, `TOKEN &&
// auth.token.fs == $slug` on a repository's, and `.write: false` on both. The
// parent — this node — carries no grant at all, for the reason the package
// comment above states for `config/policy`: an RTDB read grant sits where it
// is written and cascades down, so a grant here would make **every project's**
// voice policy readable by **every** run token of the installation. `USER` is
// in neither grant, which is the design's point and not an omission: the phone
// holds no read of `config/policy/**`, so the switch's "blocked by project
// policy" state arrives as the hop's project-status answer rather than as a
// read, and a signed-in user reads nothing anywhere under this subtree.
//
// It is what the document's generation 5 adds (internal/projectpolicy's
// RulesVersion): a client holding the older marker would not know the grant is
// there, and `fugaro update`'s rules stage is the migration that deploys it —
// the same stage every generation is met through, and one that is safe while
// runs are in flight.
//
// It sits below the macro table for PathMCPAuth's reason: a constant added
// above it would move the lines internal/cli's voice-switch test reads out of
// this file to resolve a pinned citation.
const PathVoice = "config/policy/voice"

// PathLaunchEnvelope is the launch envelope of Q9's answer (mobile plan task
// P5.1g, the design's section 4.5 E1 and section 4.8): the values the owner
// publishes from the operator's local config — **the named safe recipe an
// unconfirmed launch may run** at `recipe`, and, under `devices`, **one entry
// per paired device** carrying that device's repository allow-list (narrowed
// from the project's own `allow_repos`), its launch rate and its daily cost
// ceiling.
//
// **One node for the installation, and one entry per device under it**, which
// is P5.1-Q11's own ruling: the alternative the owner refused is a node *per
// device* ("a second place the same policy lives"), not a node at all — the
// values belong to the policy subtree, so the operator publishes them where
// they publish every other policy and no device gets a node of its own.
//
// **It is a sibling of `shared` and not a member of it**, and that is not a
// style choice: `config/policy/shared` (projectpolicy.PathShared) *is* the
// published policy document, which decodes strictly — an unknown member is an
// error — so a launch block inside it would be a policy *version* bump and a
// migration for a value the policy's own reader never reads. A child of the
// policy subtree carries the grant with no change to that document, which is
// what the voice policy node (PathVoice, generation 5) does one sibling over.
//
// **The entry is keyed by the device's Firebase Auth uid**, exactly as
// `config/admin/devices/$uid` (PathDevices) is, and the two are read together
// on purpose: `USER` already holds the device node's *existence* as a
// conjunct, so a revoked pairing reads no envelope entry — deleting one node
// takes the allow-list away with the identity, and the allow-list cannot be
// read by an account that is not paired at all.
//
// **The grant is the device's own entry and the recipe, and nothing else.** A
// device reads `devices/<its own uid>` — the rules compare `$uid` with
// `auth.uid`, so one device cannot read another's allow-list — and the
// installation's one `recipe`. It reads no sibling of the envelope and no
// other part of the policy, which is the state `USER` is in for
// `config/policy/**` generally (PathVoice's own note): a read granted on
// `devices` itself would cascade to every paired device's entry, and one on
// `config/policy/launch` to the recipe and every entry at once.
//
// **What the rules do NOT do here is enforce the envelope.** They hand the
// phone its own values and nothing more; the launch a phone asks for is a
// message, and a message's free text is not a shape the rules can resolve a
// repository out of. The decision is the consumer's and the app's, which is
// where Q9's answer puts it ("real work in the consumer"), and the app refuses
// an utterance outside its allow-list before it signs anything — so an
// unreadable envelope is an app that refuses every launch rather than one that
// launches unbounded.
//
// It is what the document's generation 10 adds (internal/projectpolicy's
// RulesVersion), and it bumps for generation 4's, 5's and 8's reason: it is a
// *read grant*, so a device under the older document is refused a read rather
// than shown a degraded one. Generation 10 carries P5.1g's other half in the
// same document — the stop request's `kind`, a write the phone holds and the
// older document refuses — which bumps for generation 6's reason.
//
// It sits below the macro table for PathMCPAuth's reason: a constant added
// above it would move the lines internal/cli's voice-switch test reads out of
// this file to resolve a pinned citation.
const PathLaunchEnvelope = "config/policy/launch"

// boundedMatch is a non-empty string of at most max characters, every one of
// them in the pattern's set.
func boundedMatch(max int, pattern string) string {
	return fmt.Sprintf("(newData.isString() && newData.val().length > 0 && newData.val().length <= %d && newData.val().matches(%s))", max, pattern)
}

// kindOf is the kind of the message at the snapshot, as the rules read it: the
// `kind` field when the message carries one, else 'message'. A message written
// without a `kind` IS the ordinary message (shared/mailbox-schema's
// DEFAULT_KIND), so every per-kind clause has to read an absent field as
// 'message' rather than as "no kind at all" — which is also why the reading is
// a ternary on exists() and not a comparison against a null that no string
// equals.
func kindOf(snap string) string {
	c := snap + ".child('kind')"
	return "(" + c + ".exists() ? " + c + ".val() : 'message')"
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

// The three cap nodes the lease's per-run comparison reads: the repository's
// own node, the default repository cap, and the project's global cap.
const (
	perRunOwnSnap     = "root.child('config/caps/repos').child($slug).child('perRunMicros')"
	perRunDefaultSnap = "root.child('config/caps/defaults/repoPerRunMicros')"
	perRunGlobalSnap  = "root.child('config/caps/global/perRunMicros')"
)

// perRunCeiling is the repository's per-run node's ceiling (spec §5.6, B18):
// the project's global per-run cap where it is set, else the default
// repository cap. It is *not* `fallback`, which reads the node first -- the
// node is what is being clamped here. Where neither is set the ceiling is the
// node itself, which leaves the node standing: a repository with a cap of its
// own in a project with none is bounded by its own cap alone (the owner's
// ruling of 2026-10-15, D6), and a comparison that read a missing default as
// null would deny every lease there instead.
func perRunCeiling() string {
	return tern(perRunGlobalSnap+".exists()", perRunGlobalSnap+".val()",
		tern(perRunDefaultSnap+".exists()", perRunDefaultSnap+".val()", perRunOwnSnap+".val()"))
}

// perRunClamp is the conjunct the lease's per-run comparison adds where the
// repository's own node is the side that binds: the run's outstanding against
// that node's ceiling, so a node above its ceiling counts as the ceiling (spec
// §5.6, B18). It is a second comparison and not a minimum, because the rules
// language has no minimum.
//
// Only that arm needs it. Where the repository has no node the side that binds
// is the default, whose ceiling is the global cap -- which the comparison
// beside it already applies -- or the default itself: either way the bound is
// the ceiling. The repository's own arm is the one that can read a node above
// its ceiling, which is exactly the write an admin lands over IAM, past the
// refusals `fugaro budget set --repo` makes.
//
// `budget.Caps.RepoPerRun` clamps the same way (plan task RC1): one behaviour
// with a reading on each side, and the rules' side is the one that holds
// against a writer that is not the Go client.
func perRunClamp(delta string) string {
	return " && (" + delta + " <= " + perRunCeiling() + ")"
}

// perRunDelta is the run's lifetime outstanding after this write: the new
// reserved less what it released. Written out rather than as the NEW macro, so
// that expansion stays a single pass (a macro's expansion is never re-scanned).
// Split from perRunOK so that rules_test.go can name the clause this branch
// adds to the lease's validator without spelling the delta a second time.
func perRunDelta(depth int) (string, error) {
	released, err := pathMacro("N", "runs/$slug/$run/released", depth)
	if err != nil {
		return "", err
	}
	return "(newData.val() - " + released + ")", nil
}

// perRunOK is the lease's per-run cap comparison, the rules' half of
// Caps.PerRun (design D6): the effective cap is the smaller of the
// repository's own cap -- its node, else the default -- and the project's
// global one, a repository node above its ceiling counts as the ceiling (spec
// §5.6, B18), and a side that is not set does not bound the run. With no
// per-run cap set anywhere the expression is false and the rules deny the
// lease (fail closed: a missing node reads null and every comparison against
// it is false).
//
// It is one boolean, not two comparisons, because a comparison against an
// unset side must not deny on its own: the global cap is the fallback of a
// repository that has none, and the default is the fallback of a global one.
func perRunOK(depth int) (string, error) {
	delta, err := perRunDelta(depth)
	if err != nil {
		return "", err
	}
	// within is one side's bound: the run's outstanding, against the cap
	// that node holds.
	within := func(snap string) string { return "(" + delta + " <= " + snap + ".val())" }
	hasOwn, hasDef, hasGlobal := perRunOwnSnap+".exists()", perRunDefaultSnap+".exists()", perRunGlobalSnap+".exists()"
	// The repository's side is its own cap clamped to its ceiling, else the
	// default; the global side, where it is set, must hold too. A side that is
	// not set does not bound the run: the global one is the fallback of a
	// repository whose own side is unset, and with nothing set anywhere the
	// expression is false (deny).
	repoSide := tern(hasOwn, within(perRunOwnSnap)+perRunClamp(delta), within(perRunDefaultSnap))
	return tern(hasOwn+" || "+hasDef,
		repoSide+" && "+tern(hasGlobal, within(perRunGlobalSnap), "true"),
		tern(hasGlobal, within(perRunGlobalSnap), "false")), nil
}

// The three cap nodes the day counter's comparison reads: the same three the
// per-run clamp reads, one cap over. The first two are the nodes REPODAILY's
// fallback reads, spelled again because the ceiling has to ask each of them
// whether it exists, which the fallback expression beside it does not answer --
// and the emulator rows are what hold the two spellings to the same nodes: a
// ceiling built from a path nothing writes is the node itself, which is a clamp
// that clamps nothing.
const (
	dailyOwnSnap     = "root.child('config/caps/repos').child($slug).child('dailyMicros')"
	dailyDefaultSnap = "root.child('config/caps/defaults/repoDailyMicros')"
	dailyGlobalSnap  = "root.child('config/caps/global/dailyMicros')"
)

// dailyCeiling is the repository's daily node's ceiling (spec §5.6, B18): the
// project's global daily cap where it is set, else the default repository daily
// cap, and the node itself where neither is set, which leaves the node
// standing (the owner's ruling of 2026-10-15, D6, as perRunCeiling gives it).
// Three arms and not `fallback`, for perRunCeiling's reason: the node is what
// is being clamped, so it cannot be the first thing read.
func dailyCeiling() string {
	return tern(dailyGlobalSnap+".exists()", dailyGlobalSnap+".val()",
		tern(dailyDefaultSnap+".exists()", dailyDefaultSnap+".val()", dailyOwnSnap+".val()"))
}

// dailyClamp is the conjunct the day counter's validator adds: the day's new
// repository total against that ceiling, so a repository node above its ceiling
// counts as the ceiling (spec §5.6, B18 -- the ceiling is "for each of daily and
// per-run", and this counter is part of the lease check: the lease's multi-path
// write raises it as it takes a reservation, budget/lease.go). A conjunct and
// never an alternative,
// which is what keeps the ceiling from *loosening* a node below it: a
// repository whose own daily cap is $30 under a $60 default must not reach $60,
// and `NEW <= cap || NEW <= ceiling` would hand it the $60.
//
// `newData.val()` is written out rather than as the NEW macro for the reason
// perRunDelta gives: an expansion is never re-scanned.
//
// It reads flat -- `OLD || !ENFORCE || NEW <= cap && NEW <= ceiling` -- on
// purpose: && binds tighter than ||, so the ceiling is a conjunct of the cap
// comparison and not of the disjunction it sits in, which would measure a
// release against the ceiling too.
//
// Where it can decide is narrow, said here so that nobody reads the clause as
// text that could never run: every write that grows this counter moves the two
// counters beside it by the same delta (COUNTERS_FOLLOW, on the run's own
// share), and under enforce the global counter's check holds the global day
// total under the global daily cap -- this node's ceiling wherever a global cap
// is set, and where none is set it reads null and refuses every growing write.
// So the clause decides only on a state the rules do not produce: a
// repository's counter left above its ceiling while the global one is under it,
// which needs a write that bypassed the rules -- an admin's, the case the
// ceiling is named for (spec §5.6: "an admin's direct database write bypasses
// rules, so the clamp at read is what holds"). It fails closed.
func dailyClamp() string {
	return " && (newData.val() <= " + dailyCeiling() + ")"
}

// MaxTokenLength bounds a registration's push token (mobile plan task P5.2.2,
// design §3.5 (b)): the schema's TOKEN_MAX, above every token that really
// exists (an FCM token at roughly 150-200 characters, an APNs token at 64 hex
// characters, an Expo token at about 41) with room for the provider to grow and
// far below MaxText. The distance is the point: a token is an identity string
// and never a body, so "bounded" means something here.
//
// MaxZoneLength bounds a quiet-hours zone name (design §3.8): the schema's
// ZONE_MAX, the longest IANA name plus room, and the same bound the other short
// identity strings carry.
//
// MaxMinute is the largest minute of the local day a quiet-hours window may
// name: the schema's MINUTE_MAX, so a window's ends are minutes since local
// midnight 0 through 1439, one spelling of every time of day and no clock
// format for the two artifacts to disagree about.
//
// They sit below the macro table, as PathMCPAuth and the family constants above
// them do: a constant added above those citations moves the lines the tests
// that resolve them read.
const (
	MaxTokenLength = 256
	MaxZoneLength  = 64
	MaxMinute      = 1439
)

// patternZone is the schema's ZONE_PATTERN, byte for byte
// (TestMailboxRulesCheckTheSchemasCharacters reads shared/mailbox-schema and
// fails when either side moves): a zone name is dot-separated IANA labels, so
// `Europe/Berlin` is one and a path, an offset or a blank label is not.
const patternZone = `/^[A-Za-z][A-Za-z0-9._+-]*(\/[A-Za-z0-9._+-]*[A-Za-z0-9_+-][A-Za-z0-9._+-]*)*$/`

// MaxSigLength is the length of a device signature in the mailbox's `sig`
// field: the unpadded base64url encoding of a 64-byte Ed25519 signature, which
// is 86 characters (mobile plan task J5r, O6, which amends D171). The rules
// check the length and the characters (patternSignature) and never the value,
// for MaxMacLength's reason: verifying a signature needs the device's public
// key, which the device node holds and this document does not read — J5b's gate
// verifies one.
//
// MaxModeLength bounds a launch action's `mode`, the rigor mode a phone asks a
// run to start in (O10, D162 as amended by D2786). It is a bound and not the
// vocabulary: the modes are internal/rigor's, and a mode the consumer has never
// heard of is refused there, at the gate J5a owns, so a sixth rigor mode is not
// a rules generation — which is the whole reason this is a bound.
//
// MaxBriefLength bounds a launch's `brief`, the shaped task a "go" carries
// (P5.4.2): the schema's BRIEF_MAX, four times MaxText because a brief is the
// document a run is started from rather than a spoken turn, and the same 16 KiB
// internal/task allows a spec a run works from.
//
// They sit below the macro table with the protection above them, for that
// comment's reason: a constant added above those citations moves the lines the
// tests that resolve them read.
const (
	MaxSigLength   = 86
	MaxModeLength  = 64
	MaxBriefLength = 16384
)

// patternSignature is the character set of a device signature: unpadded
// base64url, which is how a pairing key already travels in this tree
// (internal/budget/token's base64.RawURLEncoding). It is this package's own,
// like patternPublicKey and patternZone: the shared schema's own is
// BASE64URL_PATTERN, which mobile plan task J5b added and its test reads.
const patternSignature = `/^[A-Za-z0-9_-]+$/`

// macOK and sigOK are one tag's shape at one snapshot: a string of exactly n
// characters, every one of them in the pattern's set. MACOK and PARENTMACOK
// take the first (the D171 MAC: MaxMacLength lowercase-hex characters) and
// SIGOK and PARENTSIGOK the second (the O6 device signature: MaxSigLength
// characters of unpadded base64url).
func macOK(at string) string { return fixedMatch(at, MaxMacLength, patternHex) }

func sigOK(at string) string { return fixedMatch(at, MaxSigLength, patternSignature) }

// fixedMatch is boundedMatch's fixed-length sibling: a string of exactly n
// characters, every one of them in the pattern's set. Requiring the length
// exactly is what makes a tag an identity of one shape rather than a body, and
// it is checked beside the characters because a signature of the wrong length
// is one no verifier will accept.
func fixedMatch(at string, n int, pattern string) string {
	return fmt.Sprintf("(%s.isString() && %s.val().length == %d && %s.val().matches(%s))", at, at, n, at, pattern)
}

// exactlyOneTag is TAGOK's expression at one snapshot: the node carries exactly
// one of the two tags — the D171 MAC or the O6 device signature — and never
// both and never neither. It has to be written out because the rules language
// has no "one of these keys": `hasChildren` names the keys it requires, and the
// message's own validator no longer names `mac` for the reason J5r exists —
// a phone paired the new way has no MAC to write. Each tag's own node still
// refuses a malformed one, whichever of the two is there.
func exactlyOneTag(at string) string {
	mac := at + ".child('mac')"
	sig := at + ".child('sig')"
	return "((" + mac + ".exists() && !" + sig + ".exists()) || (" + sig + ".exists() && !" + mac + ".exists()))"
}

// PathByModel is the parent of one model's counters under a repository's day
// counter: `spend/<day>/repos/<slug>/byModel/<model>`, whose leaves are the
// dollars and the tokens of that model on that day (internal/budget's
// PathByModel renders the same path for a concrete day, slug and model). The
// four leaves generation 7 adds hang here, and doctor's sentence for a marker
// below that generation names them, so the path is written once.
//
// It sits at the end of the file for PathMCPAuth's reason: a constant added
// above those citations moves the lines the tests that resolve them read.
const PathByModel = "spend/$day/repos/$slug/byModel/$model"
