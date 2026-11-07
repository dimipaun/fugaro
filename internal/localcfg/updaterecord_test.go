package localcfg

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

// updateRecordHome is a machine's config home, as the writer and the reader see
// it: XDG_CONFIG_HOME under a temporary directory, which is the only home these
// tests touch.
func updateRecordHome(t *testing.T) func(string) string {
	t.Helper()
	home := t.TempDir()
	return func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return home
		}
		return ""
	}
}

// TestUpdateRecordRoundTrips: what the console's project screen reads has to
// survive the write, member for member — including the two members a run may
// not have an answer for, which is the whole reason runs_in_flight is written
// as an absence rather than a zero (D1483).
//
// The document is state, so the other half of the test is the state file's own
// rules: another project's entry survives a write (each project's record is
// that project's last update), the file is 0600 in a 0700 directory, a corrupt
// one is replaced rather than left refusing every later write, and one a newer
// fugaro wrote is refused and left alone.
func TestUpdateRecordRoundTrips(t *testing.T) {
	getenv := updateRecordHome(t)
	path, err := UpdateRecordPath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(getenv("XDG_CONFIG_HOME"), "fugaro", CheckoutsDir, UpdateRecordFile); path != want {
		t.Errorf("the record's path is %s, want %s", path, want)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing was written and %s exists", path)
	}
	// A machine that has recorded nothing is an empty document and no error:
	// every reader starts here.
	empty, err := LoadUpdateRecord(getenv)
	if err != nil {
		t.Fatalf("LoadUpdateRecord with no file: %v", err)
	}
	if empty.Version != UpdateRecordVersion || len(empty.Projects) != 0 {
		t.Errorf("the empty record is %+v, want version %d and no projects", empty, UpdateRecordVersion)
	}

	at := time.Date(2026, 11, 7, 12, 5, 53, 0, time.UTC)
	inflight := 3
	aurora := UpdateProject{At: at, RunsInFlight: &inflight, Retire: []UpdateRetirement{
		{Address: "module.repo.module.workflow[\"app_bmb\"].google_cloud_run_v2_job.this[0]",
			Kind: "workflow job", Cause: "the workflow's default variant is app", Kept: "the job's address",
			Job: "fugaro-acme-sandbox-app-bmb", Region: "us-east5", Protected: true},
		{Address: "module.repo.google_secret_manager_secret.this[\"claude-oauth-token-bmb\"]",
			Kind: "secret container", Cause: "no workflow mounts it", Kept: "the container is not Terraform's to delete",
			Logical: "claude-oauth-token-bmb", SecretID: "fugaro-acme-sandbox-claude-oauth-token-bmb", Protected: true},
	}}
	if err := RecordUpdate(getenv, "aurora", aurora); err != nil {
		t.Fatal(err)
	}
	got, err := LoadUpdateRecord(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != UpdateRecordVersion {
		t.Errorf("version = %d, want %d", got.Version, UpdateRecordVersion)
	}
	back := got.Projects["aurora"]
	if !back.At.Equal(at) || back.RunsInFlight == nil || *back.RunsInFlight != inflight {
		t.Errorf("aurora's entry = %+v, want at %s and %d runs in flight", back, at, inflight)
	}
	if !slices.Equal(back.Retire, aurora.Retire) {
		t.Errorf("aurora's rows = %+v, want %+v", back.Retire, aurora.Retire)
	}
	if mode, err := fileMode(path); err != nil || mode != 0o600 {
		t.Errorf("the record's mode is %v (err %v), want 0600", mode, err)
	}
	if mode, err := fileMode(filepath.Dir(path)); err != nil || mode != 0o700 {
		t.Errorf("the state directory's mode is %v (err %v), want 0700", mode, err)
	}

	// A number the run could not read is absent, never zero.
	silent := UpdateProject{At: at.Add(time.Hour), Retire: []UpdateRetirement{}}
	if err := RecordUpdate(getenv, "proxima", silent); err != nil {
		t.Fatal(err)
	}
	got, err = LoadUpdateRecord(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if p := got.Projects["proxima"]; p.RunsInFlight != nil || len(p.Retire) != 0 {
		t.Errorf("proxima's entry = %+v, want no runs_in_flight and an empty set", p)
	}
	if !slices.Equal(got.Projects["aurora"].Retire, aurora.Retire) {
		t.Errorf("proxima's write dropped aurora's record: %+v", got.Projects["aurora"])
	}
	var raw map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	proxima := raw["projects"].(map[string]any)["proxima"].(map[string]any)
	if _, ok := proxima["runs_in_flight"]; ok {
		t.Errorf("an unread count was written as %v, want the member absent", proxima["runs_in_flight"])
	}
	if set, ok := proxima["retire"].([]any); !ok || len(set) != 0 {
		t.Errorf("an empty set was written as %v, want []", proxima["retire"])
	}

	t.Run("a corrupt file is replaced", func(t *testing.T) {
		if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadUpdateRecord(getenv); err == nil {
			t.Fatal("a corrupt record was read")
		}
		if err := RecordUpdate(getenv, "aurora", silent); err != nil {
			t.Fatal(err)
		}
		got, err := LoadUpdateRecord(getenv)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.Projects["aurora"]; !ok || got.Projects["aurora"].RunsInFlight != nil {
			t.Errorf("the rebuilt record is %+v, want aurora's entry", got.Projects)
		}
		if _, ok := got.Projects["proxima"]; ok {
			t.Errorf("the corrupt file's other entries were invented: %+v", got.Projects)
		}
	})

	t.Run("a newer version is refused and left alone", func(t *testing.T) {
		newer := `{"version": ` + strconv.Itoa(UpdateRecordVersion+1) + `, "projects": {"aurora": {"at": "2027-01-01T00:00:00Z", "retire": []}}}`
		if err := os.WriteFile(path, []byte(newer), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadUpdateRecord(getenv); err == nil {
			t.Fatal("a newer version was read as this one's")
		}
		if err := RecordUpdate(getenv, "aurora", aurora); !errors.Is(err, errUpdateRecordVersion) {
			t.Fatalf("writing over a newer record = %v, want the version refusal", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != newer {
			t.Errorf("the newer record was written over:\n%s", data)
		}
	})

	t.Run("a project name is required", func(t *testing.T) {
		if err := RecordUpdate(getenv, "../etc", aurora); err == nil {
			t.Error("a project name that is not one was recorded")
		}
	})

	t.Run("a state directory that cannot be written is an error", func(t *testing.T) {
		getenv := updateRecordHome(t)
		if err := os.MkdirAll(filepath.Join(getenv("XDG_CONFIG_HOME"), "fugaro"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(getenv("XDG_CONFIG_HOME"), "fugaro", CheckoutsDir), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := RecordUpdate(getenv, "aurora", aurora); err == nil {
			t.Error("a record was written into a state directory that is a file")
		}
	})
}

// fileMode is path's permission bits.
func fileMode(path string) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}
