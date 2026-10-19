package infra

import (
	"slices"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

const hardeningProject = "proj-1234"

func TestRunsBucketFindings(t *testing.T) {
	const b = "fugaro-runs-proj-1234"
	const a, o = "user:a@example.com", "user:o@example.com"
	launcherCond := &storage.Expr{Title: gcp.LauncherBucketConditionTitle, Expression: gcp.LauncherBucketCondition(b)}
	hardened := []*storage.PolicyBindings{
		{Role: "roles/storage.objectAdmin", Members: []string{o}},
		{Role: "roles/storage.objectViewer", Members: []string{a, "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"}},
		{Role: "roles/storage.objectUser", Members: []string{a}, Condition: launcherCond},
		{Role: "roles/storage.objectUser", Members: []string{"serviceAccount:fugaro-acme-web-1a2b3c4d@proj-1234.iam.gserviceaccount.com"},
			Condition: &storage.Expr{Title: "fugaro-x", Expression: gcp.BucketCondition(b, gcp.JobBucketPrefixes, "acme-web-0123456789abcdef")}},
		// The job's read-only grant on its own tokens/<slug>/ (the budget
		// token's move, budget-token-channel.md §7.2): a read, not a finding.
		{Role: "roles/storage.objectViewer", Members: []string{"serviceAccount:fugaro-acme-web-1a2b3c4d@proj-1234.iam.gserviceaccount.com"},
			Condition: &storage.Expr{Title: "fugaro-tokens-x", Expression: gcp.TokenBucketCondition(b, "acme-web-0123456789abcdef")}},
		{Role: "roles/storage.legacyBucketOwner", Members: []string{"projectOwner:proj-1234", "projectEditor:proj-1234"}},
	}
	sev := func(fs []BucketFinding, s string) (n int) {
		for _, f := range fs {
			if f.Severity == s {
				n++
			}
		}
		return n
	}
	t.Run("hardened", func(t *testing.T) {
		fs := RunsBucketFindings(&storage.Policy{Bindings: hardened}, b, hardeningProject, []string{a, o}, []string{o})
		if sev(fs, "warning") != 0 || sev(fs, "info") != 1 {
			t.Fatalf("findings %+v", fs)
		}
	})
	// An installation not re-initialised since the budget token moved to
	// tokens/: its launchers still carry the runs/-only write, so they cannot
	// write the sealed token. The install guard says so, and its fix is
	// fugaro init, which replaces the grant.
	//
	// The old condition is the SAME grant in its pre-0.9.0 form, and the one
	// fix that must never appear for it is a removal: the member holds the
	// runs/ write it is supposed to hold, and `remove-iam-policy-binding
	// --all` would leave the installation unable to launch anything. Doctor
	// printed exactly that on every not-yet-reinitialised installation —
	// the stale binding matched none of the launcher cases and fell through
	// to the catch-all for a condition that is not the launchers' own — so
	// this test asserts both halves: the init fix is there, and nothing for
	// this member tells an operator to remove the binding.
	t.Run("the launchers' pre-seal runs/-only write", func(t *testing.T) {
		old := &storage.Expr{Title: gcp.LegacyLauncherBucketConditionTitle, Expression: gcp.LegacyLauncherBucketCondition(b)}
		p := &storage.Policy{Bindings: slices.Clone(hardened)}
		p.Bindings[2] = &storage.PolicyBindings{Role: "roles/storage.objectUser", Members: []string{a}, Condition: old}
		fs := RunsBucketFindings(p, b, hardeningProject, []string{a, o}, []string{o})
		lacks, oldForm := false, false
		for _, f := range fs {
			if strings.Contains(f.Fix, "remove-iam-policy-binding") {
				t.Errorf("an operator is told to remove the launcher's own grant: %+v", f)
			}
			if f.Severity == "warning" && strings.Contains(f.Problem, "lacks") && strings.Contains(f.Problem, "tokens/") && strings.Contains(f.Fix, "fugaro init") {
				lacks = true
			}
			if f.Severity == "warning" && strings.Contains(f.Problem, gcp.LegacyLauncherBucketConditionTitle) && strings.Contains(f.Fix, "fugaro init (0.9.0 or later)") {
				oldForm = true
			}
		}
		if !lacks {
			t.Errorf("a launcher without the tokens/ write is not reported: %+v", fs)
		}
		if !oldForm {
			t.Errorf("the pre-0.9.0 launcher condition is not reported as that grant's old form: %+v", fs)
		}
	})
	// The same binding on an installation whose launcher and operator lists
	// doctor could not read (both empty): still the launchers' own grant in
	// its old form, so still no removal command — the catch-all that would
	// print one is for conditions that are not the launchers' own.
	t.Run("the pre-seal launcher condition with unknown lists", func(t *testing.T) {
		p := &storage.Policy{Bindings: []*storage.PolicyBindings{
			{Role: "roles/storage.objectUser", Members: []string{a},
				Condition: &storage.Expr{Title: gcp.LegacyLauncherBucketConditionTitle, Expression: gcp.LegacyLauncherBucketCondition(b)}},
		}}
		fs := RunsBucketFindings(p, b, hardeningProject, nil, nil)
		if sev(fs, "info") != 0 {
			t.Errorf("the launchers' own old grant is reported as an unknown writer: %+v", fs)
		}
		for _, f := range fs {
			if strings.Contains(f.Fix, "remove-iam-policy-binding") {
				t.Errorf("unknown lists turned the launchers' old grant into a removal: %+v", f)
			}
			if f.Fix != "an operator runs fugaro init (0.9.0 or later), which replaces the grant" {
				t.Errorf("fix = %q, want the init that replaces the grant", f.Fix)
			}
		}
	})
	// A condition that is NOT the launchers' own — the same title under a
	// different role, or a different expression — keeps the removal: the
	// allowance is for this one grant's old form, not for its title.
	t.Run("the old title with another role or another expression is still a removal", func(t *testing.T) {
		for _, bd := range []*storage.PolicyBindings{
			{Role: "roles/storage.objectAdmin", Members: []string{"user:x@example.com"},
				Condition: &storage.Expr{Title: gcp.LegacyLauncherBucketConditionTitle, Expression: gcp.LegacyLauncherBucketCondition(b)}},
			{Role: "roles/storage.objectUser", Members: []string{"user:x@example.com"},
				Condition: &storage.Expr{Title: gcp.LegacyLauncherBucketConditionTitle, Expression: gcp.LegacyLauncherBucketCondition(b) + ` || true`}},
		} {
			fs := RunsBucketFindings(&storage.Policy{Bindings: []*storage.PolicyBindings{bd}}, b, hardeningProject, []string{o}, []string{o})
			if len(fs) != 1 || !strings.Contains(fs[0].Fix, "remove-iam-policy-binding") {
				t.Errorf("%s under %+v: the launchers' condition is not this binding's, so it is not covered: %+v", bd.Role, bd.Condition.Expression, fs)
			}
		}
	})
	t.Run("not hardened: a launcher with objectAdmin and no launcher grants", func(t *testing.T) {
		p := &storage.Policy{Bindings: []*storage.PolicyBindings{{Role: "roles/storage.objectAdmin", Members: []string{a, o}}}}
		fs := RunsBucketFindings(p, b, hardeningProject, []string{a, o}, []string{o})
		if sev(fs, "warning") != 2 { // the objectAdmin and the missing grants
			t.Fatalf("findings %+v", fs)
		}
		for _, f := range fs {
			if f.Severity == "warning" && !strings.Contains(f.Fix, "fugaro init") {
				t.Errorf("a launcher's fix is not fugaro init: %+v", f)
			}
		}
	})
	t.Run("a stranger with a conditional write on fugaro/", func(t *testing.T) {
		p := &storage.Policy{Bindings: append(slices.Clone(hardened), &storage.PolicyBindings{Role: "roles/storage.objectUser", Members: []string{"user:x@example.com"},
			Condition: &storage.Expr{Title: "t", Expression: `resource.name.startsWith("projects/_/buckets/` + b + `/objects/fugaro/")`}})}
		fs := RunsBucketFindings(p, b, hardeningProject, []string{a, o}, []string{o})
		if sev(fs, "warning") != 1 {
			t.Fatalf("findings %+v", fs)
		}
		for _, f := range fs {
			if f.Severity == "warning" && !strings.Contains(f.Fix, "remove-iam-policy-binding") {
				t.Errorf("no remove command: %+v", f)
			}
		}
	})
	t.Run("a custom role is flagged as cannot tell", func(t *testing.T) {
		p := &storage.Policy{Bindings: append(slices.Clone(hardened), &storage.PolicyBindings{Role: "projects/proj-1234/roles/mine", Members: []string{"user:y@example.com"}})}
		fs := RunsBucketFindings(p, b, hardeningProject, []string{a, o}, []string{o})
		if sev(fs, "warning") != 1 {
			t.Fatalf("findings %+v", fs)
		}
	})
	t.Run("lists unknown: unconditioned writers are info", func(t *testing.T) {
		fs := RunsBucketFindings(&storage.Policy{Bindings: hardened}, b, hardeningProject, nil, nil)
		if sev(fs, "warning") != 0 {
			t.Fatalf("findings %+v", fs)
		}
	})
	// A launcher's objectUser grant carries the right condition title but a
	// wrong expression (not runs/ scoped, here a permissive "true"): it must
	// not be accepted as the hardened writer grant, and the launcher must
	// still be reported as lacking it (since "writer" was never set true).
	t.Run("a launcher's grant has the right title but the wrong expression", func(t *testing.T) {
		p := &storage.Policy{Bindings: []*storage.PolicyBindings{
			{Role: "roles/storage.objectViewer", Members: []string{a}},
			{Role: "roles/storage.objectUser", Members: []string{a},
				Condition: &storage.Expr{Title: gcp.LauncherBucketConditionTitle, Expression: "true"}},
		}}
		fs := RunsBucketFindings(p, b, hardeningProject, []string{a, o}, []string{o})
		if sev(fs, "warning") != 2 { // the bad condition itself, and "lacks the grants"
			t.Fatalf("findings %+v", fs)
		}
		var sawBadCondition bool
		for _, f := range fs {
			if strings.Contains(f.Problem, "under a condition that is not the launchers' runs/ and tokens/ condition") {
				sawBadCondition = true
			}
		}
		if !sawBadCondition {
			t.Errorf("the wrong-expression grant was not flagged: %+v", fs)
		}
	})
	// A service account shaped exactly like a job/build account, but minted
	// in a different GCP project, must not be silently exempted: nothing
	// this installation's own Terraform could have created can live in
	// another project.
	t.Run("a same-shaped service account from another project is a warning, not exempted", func(t *testing.T) {
		p := &storage.Policy{Bindings: []*storage.PolicyBindings{
			{Role: "roles/storage.objectUser", Members: []string{"serviceAccount:fugaro-acme-web-1a2b3c4d@other-project.iam.gserviceaccount.com"},
				Condition: &storage.Expr{Title: "fugaro-x", Expression: gcp.BucketCondition(b, gcp.JobBucketPrefixes, "acme-web-0123456789abcdef")}},
		}}
		fs := RunsBucketFindings(p, b, hardeningProject, []string{o}, []string{o})
		if sev(fs, "warning") != 1 {
			t.Fatalf("findings %+v", fs)
		}
		if !strings.Contains(fs[0].Problem, "condition that is not the launchers' runs/ and tokens/ condition") {
			t.Errorf("not flagged as an unrecognized condition: %+v", fs)
		}
	})
	// An unrecognized service account holding ANY unconditioned write role is
	// a warning once the launcher/operator lists are known (there is no
	// legitimate reason for it), but still only info when they are not
	// (doctor cannot tell who is who). Every predefined write role, not just
	// objectAdmin/storage.admin: the review finding this pins.
	for _, role := range []string{
		"roles/storage.objectAdmin", "roles/storage.admin",
		"roles/storage.objectUser", "roles/storage.objectCreator",
		"roles/storage.legacyBucketWriter", "roles/storage.legacyBucketOwner",
	} {
		t.Run("an unrecognized service account with unconditioned "+role+" is a warning when lists are known", func(t *testing.T) {
			p := &storage.Policy{Bindings: []*storage.PolicyBindings{
				{Role: role, Members: []string{"serviceAccount:stranger@other-project.iam.gserviceaccount.com"}},
			}}
			known := RunsBucketFindings(p, b, hardeningProject, []string{o}, []string{o})
			if sev(known, "warning") != 1 || sev(known, "info") != 0 {
				t.Fatalf("known-lists findings %+v", known)
			}
			unknown := RunsBucketFindings(p, b, hardeningProject, nil, nil)
			if sev(unknown, "warning") != 0 || sev(unknown, "info") != 1 {
				t.Fatalf("unknown-lists findings %+v", unknown)
			}
		})
	}
}

func TestProjectStorageWriters(t *testing.T) {
	p := &crm.Policy{Bindings: []*crm.Binding{
		{Role: "roles/editor", Members: []string{"user:a@example.com", "user:o@example.com"}},
		{Role: "roles/storage.objectViewer", Members: []string{"user:b@example.com"}},
	}}
	fs := ProjectStorageWriters(p, []string{"user:a@example.com", "user:b@example.com", "user:o@example.com"}, []string{"user:o@example.com"})
	if len(fs) != 1 || fs[0].Member != "user:a@example.com" || fs[0].Role != "roles/editor" || fs[0].Severity != "warning" {
		t.Fatalf("findings %+v", fs)
	}
}

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
		{"reader replaced (update), not created", []tf.ResourceChange{full(a)[0], rc(grantAddr("runs_reader", a), "roles/storage.objectViewer", "update"), full(a)[2]}, nil},
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
