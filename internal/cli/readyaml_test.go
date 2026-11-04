package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A checkout can be hostile: its fugaro.yaml may be a link to a file outside
// it (a credential, a device, a FIFO that blocks the read) or enormous. One
// reader serves every command that reads one, and refuses all of those.
func TestReadFugaroYAMLRefusesWhatIsNotARegularFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "fugaro.yaml")
	if err := os.WriteFile(good, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := readFugaroYAML(good); err != nil || string(b) != "version: 1\n" {
		t.Fatalf("regular file: %q, %v", b, err)
	}

	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("TOPSECRETVALUE"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo.yaml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.yaml")
	if err := os.WriteFile(big, []byte(strings.Repeat("a", maxFugaroYAML+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	atCap := filepath.Join(dir, "cap.yaml")
	if err := os.WriteFile(atCap, []byte(strings.Repeat("a", maxFugaroYAML)), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"symlink": link, "fifo": fifo, "device": "/dev/null", "directory": dir, "too big": big} {
		t.Run(name, func(t *testing.T) {
			b, err := readFugaroYAML(path)
			if err == nil || len(b) != 0 {
				t.Fatalf("read %d bytes, err %v", len(b), err)
			}
			if strings.Contains(err.Error(), "TOPSECRETVALUE") || strings.Contains(err.Error(), "\n") {
				t.Errorf("the error carries a value or is not one line: %q", err)
			}
		})
	}
	if _, err := readFugaroYAML(atCap); err != nil {
		t.Errorf("a file at the cap: %v", err)
	}
	// A missing file stays a missing file for callers that treat it so.
	if _, err := readFugaroYAML(filepath.Join(dir, "none.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
}

// A checkout with no fugaro.yaml is the first thing anybody meets, and a
// command that reads one must say what to do about it, never the raw
// os.ErrNotExist ("lstat <path>: no such file or directory"), which reads as
// a crash. loadCheckoutResolved is setup --repo's read; configLayerAnchor is
// config layer's.
func TestMissingFugaroYAMLGetsTheGuidedSentence(t *testing.T) {
	const sentence = "this checkout has no fugaro.yaml yet: write it with /fugaro:setup in Claude Code (or start from fugaro config example), merge it, then run fugaro setup --repo again"
	dir := repoCheckout(t, "https://github.com/acme/app.git", "")
	t.Chdir(dir)

	check := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("a checkout with no fugaro.yaml was read as if it had one")
		}
		if !strings.Contains(err.Error(), sentence) {
			t.Errorf("the error is not the guided sentence:\n%v", err)
		}
		if s := err.Error(); strings.Contains(s, "lstat") || strings.Contains(s, "no such file") {
			t.Errorf("the error carries syscall text:\n%s", s)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("exit code %d, want %d", ExitCode(err), ExitUserError)
		}
	}
	t.Run("setup --repo's checkout read", func(t *testing.T) {
		_, _, err := loadCheckoutResolved(t.Context(), "", nil, layerOptions{})
		check(t, err)
	})
	t.Run("config layer's anchor", func(t *testing.T) {
		_, _, err := configLayerAnchor(t.Context())
		check(t, err)
	})
	t.Run("the shared read", func(t *testing.T) {
		_, err := requireFugaroYAML(filepath.Join(dir, "fugaro.yaml"))
		check(t, err)
	})
}

// The same sentence through the door it was reported from: setup --repo, run
// on a checkout whose fugaro.yaml is not there yet.
func TestSetupRepoMissingFugaroYAMLPrintsTheSentence(t *testing.T) {
	const sentence = "this checkout has no fugaro.yaml yet: write it with /fugaro:setup in Claude Code (or start from fugaro config example), merge it, then run fugaro setup --repo again"
	r := newInitRig(t)
	r.stateBucket()
	dir := repoCheckout(t, "https://github.com/acme/app.git", "")
	t.Chdir(t.TempDir())
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--yes", "--github-app-id", "42", dir)
	if err == nil {
		t.Fatalf("setup --repo accepted a checkout with no fugaro.yaml:\n%s%s", out, errOut)
	}
	all := out + errOut + err.Error()
	if !strings.Contains(all, sentence) {
		t.Errorf("setup --repo printed no guided sentence:\n%s", all)
	}
	if strings.Contains(all, "lstat") || strings.Contains(all, "no such file") {
		t.Errorf("setup --repo printed syscall text:\n%s", all)
	}
}

// Every command that reads a checkout's fugaro.yaml goes through it: a
// symlinked one is never followed, and nothing it points at is read.
func TestCommandsNeverFollowAFugaroYAMLSymlink(t *testing.T) {
	testutilCheckout := func(t *testing.T) string {
		t.Helper()
		dir := repoCheckout(t, "https://github.com/acme/app.git", "")
		secret := filepath.Join(t.TempDir(), "outside.yaml")
		if err := os.WriteFile(secret, []byte("version: 1\nproject: aurora\nsecretish: TOPSECRETVALUE\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(secret, filepath.Join(dir, "fugaro.yaml")); err != nil {
			t.Fatal(err)
		}
		t.Chdir(dir)
		return dir
	}
	t.Run("validate", func(t *testing.T) {
		testutilCheckout(t)
		out, errOut, err := executeStdin(t, "", "validate")
		if err == nil || strings.Contains(out+errOut+err.Error(), "TOPSECRETVALUE") {
			t.Fatalf("err %v\n%s%s", err, out, errOut)
		}
	})
	t.Run("checkout project", func(t *testing.T) {
		testutilCheckout(t)
		if co, err := checkoutProject(t.Context(), ""); err == nil || co != nil || strings.Contains(err.Error(), "TOPSECRETVALUE") {
			t.Fatalf("%v, %v", co, err)
		}
	})
	t.Run("doctor", func(t *testing.T) {
		dir := testutilCheckout(t)
		if c, fy, _ := fugaroYAMLCheck(context.Background(), dir, nil); c == nil || c.OK || fy == nil || fy.Valid {
			t.Fatalf("check %+v, %+v: a linked fugaro.yaml must be reported, not read", c, fy)
		}
	})
	t.Run("checkout config", func(t *testing.T) {
		testutilCheckout(t)
		if _, cfg, err := loadCheckoutConfigAt(t.Context(), "."); err == nil || cfg != nil || strings.Contains(err.Error(), "TOPSECRETVALUE") {
			t.Fatalf("%v, %v", cfg, err)
		}
	})
}

// validate and doctor must not print a value of the file in a type error: the
// message names the key and the type it wanted, never what was there.
func TestValidateErrorsDoNotEchoValues(t *testing.T) {
	dir := repoCheckout(t, "https://github.com/acme/app.git", "")
	body := "version: 1\nproject: aurora\nagent: TOPSECRETVALUE\nworkflows: TOPSECRETVALUE2\ngit: { provider: TOPSECRETVALUE3 }\n"
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	out, errOut, err := executeStdin(t, "", "validate")
	if err == nil {
		t.Fatal("an invalid file validated")
	}
	if all := out + errOut + err.Error(); strings.Contains(all, "TOPSECRETVALUE") {
		t.Errorf("validate echoed a value of the file:\n%s", all)
	}
	out, errOut, _ = executeStdin(t, "", "validate", "--json")
	if strings.Contains(out+errOut, "TOPSECRETVALUE") {
		t.Errorf("validate --json echoed a value:\n%s%s", out, errOut)
	}
	if c, fy, _ := fugaroYAMLCheck(context.Background(), dir, nil); c == nil || strings.Contains(c.Problem+c.Fix, "TOPSECRETVALUE") || fy == nil {
		t.Fatalf("%+v", c)
	} else {
		for _, p := range fy.Problems {
			if strings.Contains(p.String(), "TOPSECRETVALUE") {
				t.Errorf("doctor problem echoes a value: %s", p)
			}
		}
	}
}
