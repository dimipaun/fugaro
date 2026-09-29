package infra

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// ErrNoOutputs means the installation's state has no outputs: nothing has
// been applied to it yet.
var ErrNoOutputs = errors.New("the installation's state has no outputs; run fugaro init to apply it first")

// DecodeOutputs reads the installation root's `terraform output -json`
// values. A null output (the legacy registry when none is adopted, the log
// view without log isolation) leaves its field empty. No outputs at all
// means the state holds no installation, which is an error.
func DecodeOutputs(raw map[string]json.RawMessage) (InstallationOutputs, error) {
	var o InstallationOutputs
	if len(raw) == 0 {
		return o, ErrNoOutputs
	}
	typ := reflect.TypeFor[InstallationOutputs]()
	v := reflect.ValueOf(&o).Elem()
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		val, ok := raw[name]
		if !ok || bytes.Equal(bytes.TrimSpace(val), []byte("null")) {
			continue
		}
		if err := json.Unmarshal(val, v.Field(i).Addr().Interface()); err != nil {
			return InstallationOutputs{}, fmt.Errorf("the installation's output %s: %w", name, err)
		}
	}
	return o, nil
}

// PlanCounts are what a plan does, by kind of change. An import that also
// updates counts as both.
type PlanCounts struct {
	Imports  int `json:"imports"`
	Creates  int `json:"creates"`
	Updates  int `json:"updates"`
	Deletes  int `json:"deletes"`
	Replaces int `json:"replaces"`
}

// CountPlan counts p's changes.
func CountPlan(p *tf.Plan) PlanCounts {
	var c PlanCounts
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		if rc.Change.Importing != nil {
			c.Imports++
		}
		switch {
		case slices.Contains(acts, "delete") && slices.Contains(acts, "create"):
			c.Replaces++
		case slices.Equal(acts, []string{"delete"}):
			c.Deletes++
		case slices.Equal(acts, []string{"create"}):
			c.Creates++
		case slices.Equal(acts, []string{"update"}):
			c.Updates++
		}
	}
	return c
}

// String is "N imports, M creates, K updates", with the deletes and
// replaces when there are any.
func (c PlanCounts) String() string {
	s := fmt.Sprintf("%d imports, %d creates, %d updates", c.Imports, c.Creates, c.Updates)
	if c.Deletes > 0 || c.Replaces > 0 {
		s += fmt.Sprintf(", %d deletes, %d replaces", c.Deletes, c.Replaces)
	}
	return s
}

// AdoptsRunsBucket reports whether im imports the runs bucket: it exists,
// is in the project and carries our mark.
func (im Imports) AdoptsRunsBucket() bool {
	to := importTable[importRunsBucket].to
	return slices.ContainsFunc(im.List, func(i Import) bool { return i.To == to })
}

// flagHints are the resources of the installation only flags declare, by
// address prefix, and the flags that keep them. A run without those flags
// plans their deletion, which the guard refuses.
var flagHints = []struct {
	prefixes []string
	hint     string
}{
	{[]string{"module.installation.google_billing_budget."},
		"the budget is declared only by --budget, --budget-currency and --billing-account, which nothing records: pass them again with the values it was created with"},
	{[]string{"module.installation.google_monitoring_notification_channel.", "module.installation.google_monitoring_alert_policy."},
		"the alert is declared by --alert-email (or terraform.alert_email in the local config): pass it again"},
	{[]string{"module.installation.google_project_iam_member.", "module.installation.google_storage_bucket_iam_member.",
		"module.installation.google_artifact_registry_repository_iam_member.", "module.installation.google_service_account_iam_member."},
		"launchers' and operators' grants are declared by --launcher and --operator (or terraform.launchers and terraform.operators in the local config): pass every member again"},
	{[]string{"module.installation.google_project_service."},
		"two APIs are declared by what this run was given: the Vertex AI API by a repository the local config records as using it (repos.<repo>.vertex, which fugaro init --repo writes), so run from a local config that records every Vertex repository; the Billing Budgets API by --budget, --budget-currency and --billing-account, so pass them again. A deleted API is never disabled, but its management would be dropped"},
}

// DeleteHints says, for the deletes in p that allowDelete doesn't name,
// which flags would keep the resources a run left out.
func DeleteHints(p *tf.Plan, allowDelete []string) []string {
	seen := make([]bool, len(flagHints))
	for _, rc := range p.ResourceChanges {
		if !slices.Contains(rc.Change.Actions, "delete") || slices.Contains(allowDelete, rc.Address) {
			continue
		}
		for i, h := range flagHints {
			if slices.ContainsFunc(h.prefixes, func(pre string) bool { return strings.HasPrefix(rc.Address, pre) }) {
				seen[i] = true
			}
		}
	}
	var out []string
	for i, h := range flagHints {
		if seen[i] {
			out = append(out, h.hint)
		}
	}
	return out
}
