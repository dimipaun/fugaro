package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// mobileBool is a written switch for the fixtures below: the members are
// pointers so that a written false is a decision and not the absent key
// (projectpolicy.Mobile).
func mobileBool(v bool) *bool { return &v }

// mobilePolicy is the node a project that has run `fugaro update` carries: the
// block update publishes, mutated by the test that wants another shape.
func mobilePolicy(mutate func(*projectpolicy.Mobile)) *projectpolicy.Policy {
	m := projectpolicy.DefaultMobile()
	if mutate != nil {
		mutate(m)
	}
	return doctorPolicy(func(p *projectpolicy.Policy) { p.Mobile = m })
}

// TestDoctorReportsWhatMobileSyncStores: the one line P6's acceptance asks for
// — what is stored in the identity project when sync is on — and the two states
// beside it. The check is a measurement: nothing is wrong with sync on, it is
// the default, and the operator has nothing to do about it, so it is classed
// with project-account-stored (printed by --all and --json, counted as no
// finding by the one screen) rather than as an item of the default view.
func TestDoctorReportsWhatMobileSyncStores(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*projectpolicy.Mobile)
		want   string
		// wantAll are the facts the line an admin reads has to carry: where the
		// list lives and who can read it, or the line answers a different
		// question than "what is stored". They are the sync-on line's, because
		// the state where nothing is stored has no list to describe.
		wantAll []string
	}{
		{
			name:    "the block update publishes",
			want:    "the identity project stores each signed-in person's list of paired projects",
			wantAll: []string{"opaque project ids and endpoints", "Cloud KMS", "staying on the phone"},
		},
		{
			name:   "sync turned off",
			mutate: func(m *projectpolicy.Mobile) { m.Sync = mobileBool(false) },
			want:   "the identity project stores no paired-project list",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doctorPolicyRig(t, mobilePolicy(tc.mutate), "")
			c, ok := doctorChecksByID(t)[doctorMobileID]
			if !ok {
				t.Fatalf("doctor prints no %s check for a project whose mobile block is in force", doctorMobileID)
			}
			if !c.OK || c.Severity != "info" {
				t.Errorf("%s = ok %v, severity %q, want ok and info: this is a measurement and never a finding", doctorMobileID, c.OK, c.Severity)
			}
			if !strings.Contains(c.Problem, tc.want) {
				t.Errorf("%s's problem does not say what is stored: %q", doctorMobileID, c.Problem)
			}
			for _, want := range tc.wantAll {
				if !strings.Contains(c.Problem, want) {
					t.Errorf("%s does not mention %q: %q", doctorMobileID, want, c.Problem)
				}
			}
			if got := doctorClassOf(c); got != doctorClassMeasure {
				t.Errorf("%s is classed %q, want the measurement class", doctorMobileID, got)
			}
		})
	}
}

// TestDoctorSaysNothingAboutMobileWhereNothingIsOn: a project that published
// before the key existed carries no block, and a project that turned the
// surface off says so with a written false — neither stores anything, and
// neither is a state doctor has a line about. Silence is the answer, and it is
// the same silence the default view gives every installation that has not run
// the update yet.
func TestDoctorSaysNothingAboutMobileWhereNothingIsOn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy *projectpolicy.Policy
	}{
		{name: "no block at all", policy: doctorPolicy(nil)},
		{name: "a written false", policy: mobilePolicy(func(m *projectpolicy.Mobile) { m.Enabled = mobileBool(false) })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doctorPolicyRig(t, tc.policy, "")
			if c, ok := doctorChecksByID(t)[doctorMobileID]; ok {
				t.Errorf("doctor prints %s for a project with mobile pairing off: %+v", doctorMobileID, c)
			}
		})
	}
}
