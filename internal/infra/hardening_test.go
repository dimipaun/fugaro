package infra

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

func grantAddr(name, member string) string {
	return `module.installation.google_storage_bucket_iam_member.` + name + `["` + member + `"]`
}

func rc(addr, role string, actions ...string) tf.ResourceChange {
	ch := tf.Change{Actions: actions}
	if slices.Contains(actions, "delete") {
		ch.Before = map[string]any{"role": role}
	}
	if slices.Contains(actions, "create") {
		ch.After = map[string]any{"role": role}
	}
	return tf.ResourceChange{Address: addr, Type: "google_storage_bucket_iam_member", Change: ch}
}

func TestHardeningAllowDelete(t *testing.T) {
	const a, b, o = "user:a@example.com", "user:b@example.com", "user:o@example.com"
	launchers, operators := []string{a, b, o}, []string{o}
	full := func(m string) []tf.ResourceChange {
		return []tf.ResourceChange{
			rc(grantAddr("runs", m), "roles/storage.objectAdmin", "delete"),
			rc(grantAddr("runs_reader", m), "roles/storage.objectViewer", "create"),
			rc(grantAddr("runs_launcher", m), "roles/storage.objectUser", "create"),
		}
	}
	for _, tc := range []struct {
		name    string
		changes []tf.ResourceChange
		want    []string
	}{
		{"two launchers migrate", append(full(a), full(b)...), []string{grantAddr("runs", a), grantAddr("runs", b)}},
		{"an operator's grant is never allowed", full(o), nil},
		{"a member on no list", full("user:gone@example.com"), nil},
		{"no reader created", []tf.ResourceChange{full(a)[0], full(a)[2]}, nil},
		{"no launcher grant created", []tf.ResourceChange{full(a)[0], full(a)[1]}, nil},
		{"a replace, not a delete", []tf.ResourceChange{rc(grantAddr("runs", a), "roles/storage.objectAdmin", "delete", "create"), full(a)[1], full(a)[2]}, nil},
		{"another role", []tf.ResourceChange{rc(grantAddr("runs", a), "roles/storage.objectViewer", "delete"), full(a)[1], full(a)[2]}, nil},
		{"another resource", []tf.ResourceChange{rc(grantAddr("state", a), "roles/storage.objectAdmin", "delete"), full(a)[1], full(a)[2]}, nil},
		{"another module", []tf.ResourceChange{rc(strings.Replace(grantAddr("runs", a), "module.installation", "module.other", 1), "roles/storage.objectAdmin", "delete"), full(a)[1], full(a)[2]}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := HardeningAllowDelete(&tf.Plan{ResourceChanges: tc.changes}, launchers, operators)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHardeningBanner(t *testing.T) {
	if HardeningBanner(nil) != "" {
		t.Fatal("a banner with nothing allowed")
	}
	got := HardeningBanner([]string{grantAddr("runs", "user:a@example.com"), grantAddr("runs", "user:\x1b[31mx@example.com")})
	for _, want := range []string{"bucket access (0.7.0)", "  user:a@example.com\n", `\u001b`} {
		if !strings.Contains(got, want) {
			t.Errorf("banner lacks %q:\n%s", want, got)
		}
	}
}
