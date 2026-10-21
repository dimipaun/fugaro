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
	// LeaseGraceMs is how long a lease keeps its seat without a renewal, in
	// epoch milliseconds: internal/lease's DefaultGrace (10 minutes, D2), the
	// same number runstore.ClaimTTL and shared/lease's CLAIM_TTL use. It is
	// baked into the rules because the *database* is what decides whether a
	// takeover may land: a client whose grace disagreed with this one would
	// either take a seat the database still considers held (and be refused) or
	// refuse a takeover the database would have allowed. The two numbers are
	// held together by internal/lease's
	// TestTheRulesCarryTheLeaseAndTheGraceWeApply, which reads the generated
	// document rather than this constant — what has to agree is the deployed
	// rules, not a second copy of the number.
	LeaseGraceMs int64 = 600_000
	// MaxGeneration bounds a lease's fencing token (internal/lease's
	// Record.Generation): the largest integer a double represents exactly
	// (2^53-1), the line MaxTimestamp is held to and for the same reason — the
	// rules compare the new value with the old one plus one, and past that line
	// the database's arithmetic rounds. The token moves by one per takeover, so
	// a bound no lease reaches is not a limitation.
	MaxGeneration int64 = 1<<53 - 1
	// MaxEmailLength bounds the email an administrator writes into the
	// orchestrator access record (D6, central-orchestrator P2). It is RFC
	// 5321's maximum forward-path length — the longest an address can be and
	// still be one — so the orchestrator conjunct refuses a field that is not
	// an address at all without pretending to validate the address itself. The
	// rules cannot decide whether an address is verified or belongs to a
	// project's domain: D6 puts both checks at *administration* time, where
	// the command that reads this field can also read the operator's answer.
	MaxEmailLength = 254
)

// The two roles the orchestrator access record may carry (D6, central
// orchestrator P2). Nothing else in the tree names a role yet, so these two are
// the whole vocabulary, and the rules spell them out in the predicate: the
// record's role is what the *service* reads to decide whether a connection may
// override a consequential action on a job (the spec's "the job's owner, or an
// explicit override by an authorised person"), and a role the record cannot
// hold is a permission nobody granted.
const (
	// RoleOrchestratorMember may connect, submit and shepherd — the spec's
	// "everyone allowed in" of the shared session.
	RoleOrchestratorMember = "member"
	// RoleOrchestratorAdmin may do everything a member may, and is the
	// "authorised person" of the spec's action locks: the identity a
	// consequential action on a job (a merge, a cancel, a raised cap) may be
	// overridden by when the job's owner is not the one asking.
	RoleOrchestratorAdmin = "admin"
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
		"LeaseGraceMs":       LeaseGraceMs,
		"MaxGeneration":      MaxGeneration,
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

// orchestratorAccess is ORCH: the orchestrator access record (D6, D12,
// central-orchestrator plan P2), which is the pairing record above *extended*
// — deliberately the same node and not a second email list.
//
// The shape, conjunct by conjunct:
//
//   - userClaim — the pairing record D166 already requires, unchanged: an
//     mu claim that agrees with auth.uid, and a device node that exists.
//   - the administered email (D6): present, a string, non-empty, at most
//     MaxEmailLength. It is *administered*, never self-asserted: the node
//     carries `.write: false` for every client, so only the operator's own
//     credentials (IAM, which bypasses the rules) can write it — the same
//     principal that mints the tokens.
//   - the role: one of exactly the two values in the const block above. A
//     record with an email and no role is not an identity the orchestrator can
//     place, and the safe reading of a field a reader cannot place is "no".
//
// What a uid with **no device node** gets is unchanged and total: USER's own
// conjunct fails, so it reaches nothing in this document at all — not the
// mailbox, not the dashboard, not the orchestrator. What is new here is the
// narrower refusal: a *paired* device whose node carries no email or no role
// keeps every family it had (they are all on userClaim, asserted by
// TestTheAdministeredEmailGatesTheOrchestratorAndNothingElse) and reaches no
// orchestrator lease. Administration and pairing are therefore two different
// revocations with two different blast radii, which is what makes the record
// usable: an operator can take the orchestrator away from an identity without
// taking the phone's dashboard away, and take the phone away entirely by
// deleting the node.
//
// What the rules deliberately do not decide. D6 puts the domain rule and the
// verified-email rule at *administration*, and this predicate carries only
// their outcome (an email someone wrote). Nor does it decide *which*
// orchestrator an identity may use: that is the project policy's per-
// orchestrator allow-list, which the service reads on every connection and
// every submission, and which no rule here can consult per `$slug` without
// making a published policy a second authorisation document inside the rules.
// Stated plainly, because it is a limit and not an oversight: the rules admit
// any administered identity to any orchestrator's lease, and the per-slug
// narrowing is the service's — which is not deployed (D19), so today the rules
// are the whole of what guards a lease, and they guard it by identity alone.
// `d` is the same node userClaim's own line names, spelled again rather than
// shared through a constant: the line above is a citation anchor in
// docs/central-orchestrator/reconciliation.md (internal/cli's
// TestDocsCentralOrchestratorCitationsAreAnchoredByContent reads the phrase out
// of this file), so it stays one literal line, and the rules that guard the
// access record are about *that* path.
func orchestratorAccess() string {
	d := "root.child('config/admin/devices').child(auth.uid)"
	email := d + ".child('email')"
	role := d + ".child('role')"
	return "(" + userClaim +
		" && " + email + ".isString() && " + email + ".val().length > 0 && " + email + ".val().length <= " + strconv.Itoa(MaxEmailLength) +
		" && (" + role + ".val() == '" + RoleOrchestratorMember + "' || " + role + ".val() == '" + RoleOrchestratorAdmin + "'))"
}

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
		return fallback("dailyMicros", "repoDailyMicros"), nil
	case "KINDREQ":
		// The per-kind *required* half of a message's shape (P6.3's
		// KIND_REQUIRED): an `answer` must name the prompt it answers, and an
		// `action` must carry its action record. The other half — the fields a
		// kind may not carry — is on the field's own node (see PARENTKIND),
		// where it cannot be reached by a kind the message does not have.
		k := kindOf("newData")
		return "((" + k + " == 'answer' ? newData.child('reply_to').exists() : true) && (" + k + " == 'action' ? newData.child('action').exists() : true))", nil
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
		// `confirm` to either. A target that carries both shapes, or neither,
		// is refused on both sides.
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
		return "(" + v + " == 'merge' ? " + pull + " : (" +
			v + " == 'cancel' ? " + run + " : (" + run + " || " + pull + ")))", nil
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
		// verifies one).
		return fmt.Sprintf("(newData.isString() && newData.val().length == %d && newData.val().matches(%s))", MaxMacLength, patternHex), nil
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
	case "ORCH":
		// The orchestrator access record (D6, D12): the pairing record plus
		// the administered email and the role. It takes no path — it is the
		// identity, not a location — and it is deliberately *not* userClaim
		// with an extra term bolted on at the call site: one macro keeps every
		// orchestrator grant on the same predicate, so a later surface cannot
		// be granted the weaker one by accident.
		return orchestratorAccess(), nil
	case "LEASEWRITE":
		return leaseWrite(), nil
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

// boundedMatch is a non-empty string of at most max characters, every one of
// them in the pattern's set.
func boundedMatch(max int, pattern string) string {
	return fmt.Sprintf("(newData.isString() && newData.val().length > 0 && newData.val().length <= %d && newData.val().matches(%s))", max, pattern)
}

// leaseWrite is the lease's whole `.write`: the four transitions that may
// change `orchestrators/$slug/$job/lease`, and nothing else (central
// orchestrator P3, decisions D1, D2, D3).
//
// It is on the lease node's `.write` and not on a child's `.validate` because
// of how the engine evaluates a write. The three statements below were
// measured on the emulator with this document (they are what
// TestLeaseDeletingARequiredFieldIsRefused and its neighbours pin), and the
// first two are easy to get backwards:
//
//   - A `.write` is consulted at the location written *and at every ancestor*,
//     with `data` and `newData` read at the ancestor. So this one rule sees
//     every write to any part of the lease as the whole record it makes, and
//     the four transitions below are the only ones it accepts.
//   - A `.validate` is consulted at the location written, at its ancestors and
//     at the children of the location written — but not at a node that is none
//     of those. The lease node's own `.validate` is therefore *not* inert: it
//     is read for a write to `lease/holder_uid` (the lease node is an ancestor
//     of the written location), which is what makes its hasChildren clause the
//     thing that refuses a write deleting a required field — the `.write`
//     predicate alone allows the holder's `renewed_at`-only write, that being
//     the heartbeat. The transition used to live on the `generation` child,
//     and that is exactly why it was bypassable: a write to `lease/holder_uid`
//     never reaches a sibling, so the seat was stolen field by field. Deleting
//     the hasChildren clause, or moving it onto a child, reopens that class of
//     write rather than tidying anything.
//   - A child `.write: false` would not help: a grant cascades down and cannot
//     be withdrawn by a deeper node (measured: 200).
//
// The four transitions, each reading `data` (the record now), `newData` (the
// record after this write) and `auth.uid` (the writer):
//
//  1. **Create** on an absent node, at generation 1, by the writer itself.
//  2. **Renew**: the seat is the writer's already and stays its own, at the
//     same generation. This is the heartbeat, and it is the generation that
//     makes it a heartbeat rather than a takeover: the fencing token does not
//     move for a holder, so a stale process that lost the seat to a takeover
//     writes a generation that is no longer the recorded one and is refused.
//  3. **Takeover** of a seat that is nobody's (a released lease keeps its
//     node, with an empty holder_uid) or stale — its holder has not renewed
//     for the grace — moving the generation on by one. The staleness test is
//     `now - renewed_at >= LeaseGraceMs`, the database's own `now` and never a
//     clock the writer brought with it; internal/lease's Record.Fresh applies
//     the same comparison at the same boundary (`now < renewed_at + grace`),
//     so the two never disagree about the instant a seat becomes takeable.
//  4. **Release**: the holder gives the seat up, moving the generation on and
//     emptying the holder. Only the recorded holder may, for renew's reason.
//
// Every transition fixes the generation or moves it by exactly one, and every
// one of them requires the writer to be the holder the record ends with — so a
// write that changes no generation (a child write, a multi-path update that
// rewrites `holder` alone) satisfies none of them unless the writer already
// holds the seat. The stamp is required to be the database's own by the
// renewed_at field's `.validate` (`NEW == now`), which is what a write can only
// satisfy with rtdb.ServerTimestamp.
func leaseWrite() string {
	uid, gen := "newData.child('holder_uid').val()", "newData.child('generation').val()"
	oldUid, oldGen := "data.child('holder_uid').val()", "data.child('generation').val()"
	create := "(!data.exists() && " + uid + " == auth.uid && " + gen + " == 1)"
	renew := "(data.exists() && " + oldUid + " == auth.uid && " + uid + " == auth.uid && " + gen + " == " + oldGen + ")"
	take := fmt.Sprintf("(data.exists() && %s == auth.uid && %s == %s + 1 && (%s == '' || now - data.child('renewed_at').val() >= %d))",
		uid, gen, oldGen, oldUid, LeaseGraceMs)
	release := "(data.exists() && " + oldUid + " == auth.uid && " + uid + " == '' && " + gen + " == " + oldGen + " + 1)"
	return "(" + create + " || " + renew + " || " + take + " || " + release + ")"
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
