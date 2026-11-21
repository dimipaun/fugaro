package infra

// The Cloud Audit Logs cross-check (WU12, D2811, R13): the SetIamPolicy calls
// Admin Activity records for a project, read back by the drift comparison when
// it asks whether a change a member's IAM carries is one Fugaro's own trails
// hold.
//
// Admin Activity is the one audit stream a project gets whether or not anybody
// configured audit logging, it is not writable by the project's own users
// (`roles/logging.admin` can read it and cannot append to it), and Google keeps
// it for 400 days. That is what makes it the cross-check the trails are not: a
// member's own admins hold the write on the member's database, so both of
// Fugaro's trails are as tamperable as the rosters they record (threat-model.md
// §3), and the entries read here are Google's own.
//
// The service's client is the caller's (NewClients); this file connects to
// nothing of its own, impersonates nobody, and reads one project's own log.
//
// UNVERIFIED against the real service, as the folder and SA surfaces are: the
// filter is the shape Google's own documentation gives for a project's Admin
// Activity entries, and what the fakes encode is that documentation rather than
// what was observed. What is not verified is the exact spelling a real
// `serviceData.policyDelta.bindingDeltas` entry carries for every principal
// type, and whether an entry can be returned for a SetIamPolicy that Google
// logged with no delta at all.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	logging "google.golang.org/api/logging/v2"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/workspace"
)

const (
	// auditLogPageSize is what one entries.list page asks for. The service's
	// maximum is 1000 entries, and a member's policy changes over thirty days
	// are far below it; the read follows the page tokens either way.
	auditLogPageSize = 1000
	// adminActivityLogID is the log Admin Activity's entries are written to.
	// The filter spells it percent-encoded, which is how the log's name is
	// written everywhere it is compared rather than read by a person.
	adminActivityLogID = "cloudaudit.googleapis.com%2Factivity"
	// setIamPolicyMethod is the audited method name a policy change carries.
	// Admin Activity records one entry per called method, so the filter names
	// it and nothing else.
	setIamPolicyMethod = "SetIamPolicy"
	// bindingActionAdd is the only delta action read: a change's REMOVED
	// binding is an absence and not a binding, and what drift asks is what the
	// member's policy was left carrying (internal/workspace/drift.go).
	bindingActionAdd = "ADD"
)

// AuditLogs is Cloud Logging's Admin Activity surface, read for the policy
// changes made to a project.
type AuditLogs struct{ logs *logging.Service }

// NewAuditLogs wraps the Cloud Logging client the caller already built
// (NewClients); it connects to nothing of its own.
func NewAuditLogs(logs *logging.Service) *AuditLogs { return &AuditLogs{logs: logs} }

// AuditLogs is the read internal/workspace's drift cross-check is written
// against (its SetIamPolicyLog).
var _ workspace.SetIamPolicyLog = (*AuditLogs)(nil)

// SetIamPolicyChanges reads project's Admin Activity entries for SetIamPolicy
// calls logged at or after since, oldest first, and answers one change per
// binding the call ADDED.
//
// A project that has never had a policy change answers an empty list and not an
// error: that is the ordinary state of a project whose bindings came with its
// creation. A read that fails -- denied, unreachable, an entry that cannot be
// parsed -- is an error, and the caller must treat the window as unmeasured
// rather than as clean, which is what `DriftReport.Unmeasured` is for.
//
// The caller is the caller of the change, as Google recorded it
// (protoPayload.authenticationInfo.principalEmail): the member's own `fugaro
// update` appears here under the member owner's own address, and it is what the
// cross-check matches a trail event against.
func (a *AuditLogs) SetIamPolicyChanges(ctx context.Context, project string, since time.Time) ([]workspace.SetIamPolicyChange, error) {
	if !config.ProjectNameRE.MatchString(project) {
		return nil, userErr("%q is not a project name, so it has no Admin Activity log to read", project)
	}
	// The log name is quoted with %q so that the filter cannot be broken by a
	// name and the encoded slash inside it stays encoded: the log's own name
	// carries %2F, and an unquoted filter is where an escape would be read as
	// one.
	filter := fmt.Sprintf("logName=%q AND protoPayload.methodName=%q", "projects/"+project+"/logs/"+adminActivityLogID, setIamPolicyMethod)
	if !since.IsZero() {
		// Without a bound Cloud Logging scans the whole retention of the log,
		// which is 400 days of Admin Activity.
		filter += " AND timestamp>=" + strconv.Quote(since.UTC().Format(time.RFC3339Nano))
	}
	req := &logging.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + project},
		Filter:        filter,
		OrderBy:       "timestamp asc",
		PageSize:      auditLogPageSize,
	}
	var out []workspace.SetIamPolicyChange
	for {
		resp, err := a.logs.Entries.List(req).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("reading the Admin Activity log of %s: %w", project, err)
		}
		for _, e := range resp.Entries {
			changes, err := policyChanges(project, e)
			if err != nil {
				return nil, fmt.Errorf("reading the Admin Activity log of %s: %w", project, err)
			}
			out = append(out, changes...)
		}
		// A page can be empty and still carry a token.
		if resp.NextPageToken == "" {
			return out, nil
		}
		req.PageToken = resp.NextPageToken
	}
}

// auditPayload is the part of an entry's protoPayload this reads, which is the
// part a policy change carries: the method that was called, who called it, and
// the deltas the call made to the policy.
type auditPayload struct {
	MethodName         string `json:"methodName"`
	AuthenticationInfo struct {
		PrincipalEmail string `json:"principalEmail"`
	} `json:"authenticationInfo"`
	ServiceData struct {
		PolicyDelta struct {
			BindingDeltas []struct {
				Action string `json:"action"`
				Role   string `json:"role"`
				Member string `json:"member"`
			} `json:"bindingDeltas"`
		} `json:"policyDelta"`
	} `json:"serviceData"`
}

// policyChanges maps one Admin Activity entry to the changes it records, and
// answers none for an entry that records none.
//
// An entry whose methodName is not SetIamPolicy is skipped rather than refused:
// the filter names the method, but the field it names is inside the payload, so
// an entry that mentions the string anywhere else is one the service may return
// and not one this reader has a rule for. An entry with no payload at all is
// the same case: the read is of a stream Google writes, and a reader that
// refused a shape it has no rule for would fail drift on an entry that carries
// nothing to compare.
func policyChanges(project string, e *logging.LogEntry) ([]workspace.SetIamPolicyChange, error) {
	if len(e.ProtoPayload) == 0 {
		return nil, nil
	}
	var p auditPayload
	if err := json.Unmarshal(e.ProtoPayload, &p); err != nil {
		return nil, fmt.Errorf("entry %s: protoPayload: %w", e.InsertId, err)
	}
	if p.MethodName != setIamPolicyMethod {
		return nil, nil
	}
	at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("entry %s: timestamp %q: %w", e.InsertId, e.Timestamp, err)
	}
	var out []workspace.SetIamPolicyChange
	for _, d := range p.ServiceData.PolicyDelta.BindingDeltas {
		if d.Action != bindingActionAdd {
			continue
		}
		out = append(out, workspace.SetIamPolicyChange{
			Project:   project,
			Principal: p.AuthenticationInfo.PrincipalEmail,
			Member:    d.Member,
			Role:      d.Role,
			Entry:     e.InsertId,
			At:        at,
		})
	}
	return out, nil
}
