package tf

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// This file decides whether a failed apply may be run again, and it is the
// whole of that decision: Google's APIs are eventually consistent, so an apply
// that creates a resource and then calls another API about it can lose the race
// — terraform creates google_cloud_run_v2_service.credentials[0] ("Creation
// complete after 11s"), and the two google_cloud_run_v2_service_iam_member
// grants on it fail with `googleapi: Error 404: Resource 'fugaro-credentials'
// of kind 'SERVICE' ... does not exist` because the IAM front end has not seen
// the service yet (a real failure: 2026-11-09, 0.21.1, a managed project).
// A person who re-runs the command gets the rest of the apply, and CLAUDE.md's
// HARD RULE says that re-run is not theirs to make.
//
// What is retried is the CLOSED LIST below and nothing else. It is a list
// rather than a substring over the output because a retry is a decision to run
// terraform again: "not yet visible" is a claim about the world, and a message
// that merely mentions a 404 or a service account says nothing about whether
// the next apply would do anything different. Every entry is one thing: an
// apply, some of whose changes Google's API refused because the resource the
// change names is one the SAME apply created. Everything else — a quota, a
// permission, an apply that failed on a plan — is not on the list, and one
// error off the list is enough to stop.
//
// The list's evidence is in two places and both are required. The message says
// what Google answered and, on the 404, names the resource it could not see
// (`Resource '<name>' of kind '<kind>'`); the plan the apply was on says
// whether that name is a resource THIS apply creates. That second half is what
// makes it a propagation error rather than a request for something that does
// not exist anywhere: a 404 on a service no part of this plan creates is a
// real 404 and stays one.

// PropagationFailure reports whether err is a terraform apply that failed only
// on the closed list of propagation errors, and may therefore be run again:
// every diagnostic terraform printed is one, against applied — the plan the
// apply was on, whose changes say what this apply created. The retry itself is
// the caller's (internal/cli's propagationRetry), because running the same
// saved plan again is never what happens: the caller plans afresh, and judges
// the new plan through the same gate and cover tables it judged the first one.
//
// Nothing here is a substring search over "the output": the diagnostics are
// read as terraform's own, each carries the resource it acts on, and a message
// that cannot be read as one of the list's entries makes the whole thing false.
func PropagationFailure(err error, applied *Plan) bool {
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Command != "apply" {
		return false
	}
	// What fugaro kept of stderr is a tail, and a diagnostic the tail cut in
	// half is exactly the error this function must not mistake for something
	// it can name: a caller reads a truncated tail as "not retryable".
	if ee.Truncated {
		return false
	}
	ds := diagnostics(ee.Stderr)
	if len(ds) == 0 {
		return false
	}
	for _, d := range ds {
		if !propagationError(d, applied) {
			return false
		}
	}
	return true
}

// diagnostic is one of terraform's error diagnostics: the resource the error
// was applying, and the whole of what it said.
type diagnostic struct {
	address string // terraform's own address, "" when the diagnostic named none
	text    string // its lines, joined: the box is not part of the message
}

// errorHeader starts a diagnostic. Terraform prints one per error, each with
// the resource it acts on inside it.
const errorHeader = "Error: "

// diagnostics reads terraform's stderr as its error diagnostics, in order.
// Terraform boxes each one and wraps its text at the terminal's width, so the
// lines are unboxed and joined first: -no-color takes the colour off, not the
// box, and a message that was wrapped is one message.
func diagnostics(stderr string) []diagnostic {
	var out []diagnostic
	var cur *diagnostic
	for _, raw := range strings.Split(stderr, "\n") {
		line := unbox(raw)
		head, ok := strings.CutPrefix(line, errorHeader)
		if ok {
			out = append(out, diagnostic{})
			cur = &out[len(out)-1]
			cur.text = head
			continue
		}
		if cur == nil || strings.Trim(line, "╵╷─-") == "" {
			// Progress output, the box's own rules, and the header of a
			// diagnostic the caller's tail dropped: nothing to attach.
			continue
		}
		if a, ok := withAddress(line); ok {
			cur.address = a
		}
		cur.text += " " + line
	}
	for i := range out {
		out[i].text = strings.TrimSpace(out[i].text)
	}
	return out
}

// unbox takes terraform's box off one line: the "│ " its diagnostics are
// prefixed with, and the box's own rules, which carry nothing.
func unbox(line string) string {
	line = strings.TrimRight(line, "\r")
	trimmed := strings.TrimPrefix(strings.TrimLeft(line, " \t"), "│")
	if trimmed != line {
		return strings.TrimLeft(trimmed, " \t")
	}
	return line
}

// withAddress is the resource a diagnostic was applying: terraform writes
// "with <address>," above the source line for every error that has one.
func withAddress(line string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "with ")
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), ","))
	if rest == "" || strings.ContainsAny(rest, " \t") || !addressRE.MatchString(rest) {
		return "", false
	}
	return rest, true
}

// addressRE is an address's last "type.name" pair, with the index an instance
// carries ("...service_iam_member.credentials_invoker[\"user:a@b\"]").
var addressRE = regexp.MustCompile(`(?:^|\.)([a-z][a-z0-9_]*)\.([a-z][a-z0-9_]*)(?:\[.*\])?$`)

// iamGrantSuffixes are the resource types an IAM grant is written as: the
// resources Google answers a 404 or a "Service account ... does not exist" for
// while it has not caught up. The list is the one the types are named by, not a
// guess at what a plan says.
var iamGrantSuffixes = []string{"_iam_member", "_iam_binding", "_iam_policy"}

// notFound404 is Google's 404 for a resource it has not seen: the name and kind
// it could not find.
var notFound404 = regexp.MustCompile(`googleapi: Error 404: Resource '([^']+)' of kind '[^']+'`)

// missingAccount400 is Google's 400 for a binding that names a service account
// it has not seen.
var missingAccount400 = regexp.MustCompile(`googleapi: Error 400: Service account (\S+@\S+) does not exist`)

// propagationError reports whether one diagnostic is on the list: an IAM
// grant, and an error about a resource the plan created.
func propagationError(d diagnostic, applied *Plan) bool {
	typ := resourceType(d.address)
	if !isIAMGrant(typ) {
		return false
	}
	if m := notFound404.FindStringSubmatch(d.text); m != nil {
		// The wording the failure was reported with; a 404 of this shape whose
		// message does not say the resource does not exist is not this class.
		return strings.Contains(d.text, "does not exist") && createdNamed(applied, m[1])
	}
	if m := missingAccount400.FindStringSubmatch(d.text); m != nil {
		return createdNamed(applied, m[1])
	}
	return false
}

// isIAMGrant is whether typ is one of the types an IAM grant is written as.
func isIAMGrant(typ string) bool {
	for _, s := range iamGrantSuffixes {
		if strings.HasSuffix(typ, s) {
			return true
		}
	}
	return false
}

// resourceType is the resource type of a terraform address: the last
// "type.name" pair, whatever module path and instance index it hangs under.
func resourceType(addr string) string {
	m := addressRE.FindStringSubmatch(addr)
	if m == nil {
		return ""
	}
	return m[1]
}

// identityAttributes are the plan's own fields Google's error can name a
// resource by. It is a closed list for the same reason the errors are: these
// are the attributes an IAM grant or a created resource carries that point at
// the thing the call was about — the name it was made under, the account, the
// member.
var identityAttributes = []string{"name", "service", "account_id", "email", "member"}

// createdNamed reports whether the plan applied creates a resource the error
// named. A grant is never the target of a grant, so the IAM grant resources
// this plan changes — including the one that failed — do not count as the
// created target: without that, every failing grant would name itself.
func createdNamed(p *Plan, name string) bool {
	if p == nil || name == "" {
		return false
	}
	for _, rc := range p.ResourceChanges {
		if !slices.Contains(rc.Change.Actions, "create") || isIAMGrant(rc.Type) {
			continue
		}
		for _, attr := range identityAttributes {
			v, ok := rc.Change.After[attr].(string)
			if !ok || v == "" {
				continue
			}
			if sameIdentity(attr, v, name) {
				return true
			}
		}
	}
	return false
}

// sameIdentity is whether a created resource's attribute v is the name the
// error used: the same string, or the same account — Google's 400 names a
// service account by its full address while the resource records its id and
// its member form.
func sameIdentity(attr, v, name string) bool {
	if v == name || strings.TrimPrefix(v, "serviceAccount:") == name {
		return true
	}
	if attr != "account_id" {
		return false
	}
	local, _, ok := strings.Cut(name, "@")
	return ok && local != "" && local == v
}
