//go:build terraform

package tf

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/plan_*.json from the real terraform")

// The configurations the real terraform plans. Each plan starts from the
// state that base leaves, and uses only the built-in terraform_data resource,
// so nothing is downloaded and no credentials are needed.
const (
	base = `
resource "terraform_data" "a" { input = "one" }
resource "terraform_data" "r" { triggers_replace = "1" }
resource "terraform_data" "c" {
  triggers_replace = "1"
  lifecycle { create_before_destroy = true }
}
resource "terraform_data" "d" { input = "gone" }
`
	updated = `
resource "terraform_data" "a" { input = "two" }
resource "terraform_data" "r" { triggers_replace = "1" }
resource "terraform_data" "c" {
  triggers_replace = "1"
  lifecycle { create_before_destroy = true }
}
resource "terraform_data" "d" { input = "gone" }
`
	replaced = `
resource "terraform_data" "a" { input = "one" }
resource "terraform_data" "r" { triggers_replace = "2" }
resource "terraform_data" "c" {
  triggers_replace = "2"
  lifecycle { create_before_destroy = true }
}
resource "terraform_data" "d" { input = "gone" }
`
	deleted = `
resource "terraform_data" "a" { input = "one" }
resource "terraform_data" "r" { triggers_replace = "1" }
resource "terraform_data" "c" {
  triggers_replace = "1"
  lifecycle { create_before_destroy = true }
}
`
	forgotten = deleted + `
removed {
  from = terraform_data.d
  lifecycle { destroy = false }
}
`
	imported = base + `
import {
  to = terraform_data.i
  id = "adopted-id"
}
resource "terraform_data" "i" {}
`
)

func TestRealTerraformPlanJSONShape(t *testing.T) {
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Fatalf("this test needs terraform on PATH: %v", err)
	}
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A fresh HOME, so nothing of the developer's terraform setup is read.
	env, err := Env([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "home")}, dir, filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("terraform.tfvars.json", "{}\n")
	write("main.tf", base)

	tf, err := New(bin, dir, env)
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	tf.Out = &log
	defer func() {
		if t.Failed() {
			t.Log(log.String())
		}
	}()
	if err := tf.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	planFile := filepath.Join(dir, "tfplan")
	if changed, err := tf.Plan(ctx, planFile); err != nil || !changed {
		t.Fatalf("base plan: changed=%v err=%v", changed, err)
	}
	if err := tf.Apply(ctx, planFile); err != nil {
		t.Fatal(err)
	}
	if changed, err := tf.Plan(ctx, planFile); err != nil || changed {
		t.Fatalf("re-plan after apply: changed=%v err=%v, want no changes", changed, err)
	}

	for _, tc := range []struct {
		fixture, config string
		// check the real plan: which addresses carry which actions.
		want map[string][]string
	}{
		{"plan_update.json", updated, map[string][]string{"terraform_data.a": {"update"}}},
		{"plan_replace.json", replaced, map[string][]string{"terraform_data.r": {"delete", "create"}, "terraform_data.c": {"create", "delete"}}},
		{"plan_delete.json", deleted, map[string][]string{"terraform_data.d": {"delete"}}},
		{"plan_forget.json", forgotten, map[string][]string{"terraform_data.d": {"forget"}}},
		{"plan_import.json", imported, map[string][]string{"terraform_data.i": {"no-op"}}},
	} {
		write("main.tf", tc.config)
		if changed, err := tf.Plan(ctx, planFile); err != nil || !changed {
			t.Fatalf("%s: changed=%v err=%v", tc.fixture, changed, err)
		}
		raw, err := tf.showJSON(ctx, planFile)
		if err != nil {
			t.Fatal(err)
		}
		real, err := parsePlan(raw)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string][]string{}
		for _, rc := range real.ResourceChanges {
			if !slices.Equal(rc.Change.Actions, []string{"no-op"}) || rc.Change.Importing != nil {
				got[rc.Address] = rc.Change.Actions
			}
		}
		if !mapsEqual(got, tc.want) {
			t.Errorf("%s: real actions %v, want %v", tc.fixture, got, tc.want)
		}
		if tc.fixture == "plan_import.json" {
			if rc := find(real, "terraform_data.i"); rc == nil || rc.Change.Importing == nil || rc.Change.Importing.ID != "adopted-id" {
				t.Errorf("the import isn't marked as importing adopted-id: %+v", rc)
			}
		}

		path := filepath.Join("testdata", tc.fixture)
		if *update {
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, raw, "", "  "); err != nil {
				t.Fatal(err)
			}
			pretty.WriteByte('\n')
			if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		// The fixture must still parse to the same changes as the real output.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fixture, err := parsePlan(data)
		if err != nil {
			t.Fatal(err)
		}
		if a, b := shape(fixture), shape(real); !slices.Equal(a, b) {
			t.Errorf("%s drifted from the real format; rerun with -update:\nfixture %q\nreal    %q", tc.fixture, a, b)
		}
	}
}

func find(p *Plan, addr string) *ResourceChange {
	for i := range p.ResourceChanges {
		if p.ResourceChanges[i].Address == addr {
			return &p.ResourceChanges[i]
		}
	}
	return nil
}

// shape is what the guard and the summary read of a plan, minus the values
// that change on every run (ids).
func shape(p *Plan) []string {
	var out []string
	for _, rc := range p.ResourceChanges {
		s := rc.Address + " " + rc.Type + " " + string(mustJSON(rc.Change.Actions))
		if rc.Change.Importing != nil {
			s += " importing " + rc.Change.Importing.ID
		}
		out = append(out, s)
	}
	return out
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func mapsEqual(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}
	return true
}
