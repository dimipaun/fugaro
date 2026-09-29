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

// DecodeOutputs reads the installation root's `terraform output -json`
// values. A null output (the legacy registry when none is adopted, the log
// view without log isolation) leaves its field empty. No outputs at all
// means the state holds no installation, which is an error.
func DecodeOutputs(raw map[string]json.RawMessage) (InstallationOutputs, error) {
	var o InstallationOutputs
	if len(raw) == 0 {
		return o, errors.New("the installation's state has no outputs; run fugaro init to apply it first")
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
