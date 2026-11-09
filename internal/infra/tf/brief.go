package tf

import (
	"fmt"
	"slices"
	"strings"
)

// Brief describes a plan for the person at the terminal: the counts, and one
// plain-words line for each change they have to look at before it is applied.
//
// It exists beside Summary because the two have different readers (the
// owner's feedback of 2026-11-09). Summary is the run's own record — every
// change by its Terraform address, with the attribute paths an update moves —
// and it is what goes to the log (and to the terminal under --verbose), where
// an address is exactly what a reader wants. The terminal's own view is what
// a person reads while a run goes past, and an address is not a thing they
// can act on: `module.installation.google_cloud_run_v2_service.credentials`
// and `template.template.containers[0].env[2].value` are Fugaro's own
// plumbing, named in the module's words and not in Fugaro's. So the screen
// gets the counts, and — for the changes nobody may let through unread, the
// same ones Summary marks ⚠ — one line each saying what changes, in words
// that name the thing: "the catalog check job's configuration changes".
//
// What counts as "must be reviewed first" is Summary's own answer and not a
// second one: the sensitive in-place updates (the sensitive table), and every
// removal (delete, replace, and an action the guard does not know). A create
// is not one: it is the plan doing its job, and its count is on the line
// above.
func Brief(p *Plan) string {
	if p == nil {
		return "No plan.\n"
	}
	var counts counts
	var review []string
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		importing := rc.Change.Importing != nil
		replace := slices.Contains(acts, "delete") && slices.Contains(acts, "create")
		if importing {
			counts.imports++
		}
		switch {
		case replace:
			counts.replaces++
			review = append(review, "⚠ "+describe(rc, "replaced"))
			continue
		case slices.Equal(acts, []string{"delete"}):
			counts.deletes++
			review = append(review, "⚠ "+describe(rc, "removed"))
			continue
		case slices.Equal(acts, []string{"create"}):
			counts.creates++
			continue
		case slices.Equal(acts, []string{"forget"}):
			counts.forgets++
			continue
		case slices.Equal(acts, []string{"update"}):
			counts.updates++
		case importing && slices.Equal(acts, []string{"no-op"}):
			continue
		case slices.Equal(acts, []string{"no-op"}), slices.Equal(acts, []string{"read"}):
			continue
		default:
			review = append(review, fmt.Sprintf("⚠ %s is %s", thing(rc), strings.Join(acts, " and ")))
			continue
		}
		paths := changedPaths(rc.Type, rc.Change)
		if isSensitive(rc.Type, paths) {
			review = append(review, "⚠ "+describe(rc, pathWords(rc.Type, paths)))
		}
	}

	var b strings.Builder
	b.WriteString(counts.line() + "\n")
	if len(review) > 0 {
		b.WriteString("\n⚠ Review these first:\n")
		for _, l := range review {
			b.WriteString("  " + l + "\n")
		}
	}
	return b.String()
}

// counts is a plan's totals, kept apart from Summary's own so the two cannot
// drift into disagreeing about what they counted.
type counts struct {
	imports, creates, updates, forgets, replaces, deletes int
}

// line is the counts as the one line the screen gets. A zero is left out
// rather than printed as "0 to import": the person reads the line to know
// what is about to happen, and "0 to update" is not something that happens.
// A plan with nothing in it says so.
func (c counts) line() string {
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{
		{c.imports, "to import"}, {c.creates, "to create"}, {c.updates, "to update"},
		{c.forgets, "to forget"}, {c.replaces, "to replace"}, {c.deletes, "to delete"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	if len(parts) == 0 {
		return "Plan: no changes."
	}
	return "Plan: " + strings.Join(parts, ", ") + "."
}

// describe is one change in Fugaro's words: what the thing is, and what
// happens to it. what is either a possessive clause for an update ("'s image
// changes") or a bare verb for a removal ("removed"), which are the two
// shapes a person reads a plan line in.
func describe(rc ResourceChange, what string) string {
	if strings.HasPrefix(what, "'s ") {
		return thing(rc) + what
	}
	return thing(rc) + " is " + what
}

// thing is what a change is about, in Fugaro's words: the noun its resource
// type stands for, qualified by the name the module gave it — "the catalog
// check job", "the runs bucket", "the credentials service" — and, for a grant
// that carries the member it is for in its address (an indexed iam_member),
// the member as well: "the admin grant to user:old@example.com".
//
// The type has no noun it does not know: those are the types our own modules
// declare (allowedTypes), and a type outside the table falls back to its own
// last name segments in words, which is worse reading but never an address.
func thing(rc ResourceChange) string {
	local, index := splitAddress(rc.Address)
	if isGrant(rc.Type) {
		name := strings.ReplaceAll(local, "_", " ")
		if index != "" {
			return "the " + name + " grant to " + index
		}
		return "the " + name + " grant"
	}
	if noun, ok := nouns[rc.Type]; ok {
		if local == "" {
			return "the " + noun
		}
		return "the " + strings.ReplaceAll(local, "_", " ") + " " + noun
	}
	return "the " + wordsOf(rc.Type)
}

// isGrant reports whether a resource type is a role binding, whose module
// name is the role's short name and whose address may carry the member.
func isGrant(typ string) bool {
	return strings.HasSuffix(typ, "_iam_member") || strings.HasSuffix(typ, "_iam_binding")
}

// splitAddress returns a resource's own name out of its Terraform address,
// and the index it carries when it has one: the address's last segment is the
// name, the index inside it (`admin["user:a@b"]`) is the member of a grant.
//
// The last segment is the last dot outside the index, not the last dot: an
// indexed address holds a whole address of its own — a member is
// `user:old@example.com` — and its dots are not the module's.
func splitAddress(address string) (name, index string) {
	depth, cut := 0, -1
	for i := 0; i < len(address); i++ {
		switch address[i] {
		case '[':
			depth++
		case ']':
			depth--
		case '.':
			if depth == 0 {
				cut = i
			}
		}
	}
	if cut >= 0 {
		address = address[cut+1:]
	}
	if i := strings.IndexByte(address, '['); i >= 0 {
		index = strings.Trim(address[i:], "[]\"")
		address = address[:i]
	}
	return address, index
}

// wordsOf is a resource type as words, for a type the noun table does not
// carry: its own name, without the provider's prefix. It is a fallback and
// not a name — TestEveryAllowedTypeHasANoun is what keeps it unreachable for
// the types our modules declare.
func wordsOf(typ string) string {
	return strings.ReplaceAll(strings.TrimPrefix(typ, "google_"), "_", " ")
}

// nouns is the noun each resource type prints as, so that a plan line names
// the thing in Fugaro's terms instead of a resource type. Every type in
// allowedTypes is here, and TestEveryAllowedTypeHasANoun fails when a module
// adds one that is not — the same rule allowedTypes itself is held to, for
// the same reason: what the person reads on the screen is a decision.
var nouns = map[string]string{
	"google_apikeys_key":                             "API key",
	"google_artifact_registry_repository":            "image registry",
	"google_artifact_registry_repository_iam_member": "image registry grant",
	"google_billing_budget":                          "budget alert",
	"google_cloud_run_v2_job":                        "job",
	"google_cloud_run_v2_job_iam_member":             "job invoker grant",
	"google_cloud_run_v2_service":                    "service",
	"google_cloud_run_v2_service_iam_member":         "service invoker grant",
	"google_cloud_scheduler_job":                     "schedule",
	"google_compute_firewall":                        "firewall rule",
	"google_compute_router":                          "router",
	"google_compute_router_nat":                      "NAT",
	"google_firebase_database_instance":              "Realtime Database",
	"google_firebase_project":                        "Firebase project",
	"google_logging_log_view":                        "log view",
	"google_logging_log_view_iam_member":             "log view grant",
	"google_logging_project_bucket_config":           "log bucket",
	"google_logging_project_exclusion":               "log exclusion",
	"google_logging_project_sink":                    "log sink",
	"google_monitoring_alert_policy":                 "alert policy",
	"google_monitoring_notification_channel":         "notification channel",
	"google_project_iam_custom_role":                 "custom role",
	"google_project_iam_member":                      "project role grant",
	"google_project_service":                         "API",
	"google_secret_manager_secret":                   "secret",
	"google_secret_manager_secret_iam_member":        "secret grant",
	"google_service_account":                         "service account",
	"google_service_account_iam_member":              "service account grant",
	"google_storage_bucket":                          "bucket",
	"google_storage_bucket_iam_member":               "bucket grant",
	"google_storage_bucket_object":                   "bucket object",
}

// pathWords says what an in-place update moves, in the words a person reads
// it in: the sensitive locations (the table the plan is judged by) named as
// the thing they are, and nothing else — the attribute path itself stays in
// the log. A path the table does not carry is Fugaro's own plumbing, which is
// still worth reviewing; it is named by the resource alone.
func pathWords(typ string, paths []string) string {
	locs := sensitive[typ]
	slices.Sort(locs)
	for _, loc := range locs {
		for _, p := range paths {
			if underPath(p, loc) {
				if w, ok := pathNeeds[loc]; ok {
					return w
				}
			}
		}
	}
	return "'s setup changes"
}

// underPath reports whether a changed path is the location or lies under it:
// the same reading isSensitive makes, so the two never disagree about which
// change a line is about.
func underPath(p, loc string) bool {
	segs := pathSegments(p)
	l := strings.Split(loc, ".")
	n := min(len(segs), len(l))
	return slices.Equal(segs[:n], l[:n])
}

// pathNeeds is each sensitive location as the words its change prints as. The
// headings are Fugaro's: what a person is being asked to look at, and not
// where in Terraform's own shape it sits.
var pathNeeds = map[string]string{
	"template.template.service_account":  "'s identity changes",
	"template.template.containers.image": "'s image changes",
	"template.template.containers.env":   "'s configuration changes",
	"template.template.max_retries":      "'s retry count changes",
	"lifecycle_rule":                     "'s lifecycle rule changes",
	"cleanup_policies":                   "'s cleanup policy changes",
	"cleanup_policy_dry_run":             "'s cleanup policy changes",
	"paused":                             "'s schedule changes",
	"retention_days":                     "'s retention changes",
}
