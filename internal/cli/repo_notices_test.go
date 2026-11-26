package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// pinnedAmongOthers is a default branch the release before this one left: the
// two entries the old merge wrote, beside a repository's own marketplace, its
// own plugin and its own keys, so the notice has to read the pin out of a file
// that is mostly somebody else's.
const pinnedAmongOthers = `{
  "permissions": {
    "allow": [
      "Bash(go test:*)"
    ]
  },
  "extraKnownMarketplaces": {
    "acme": {
      "source": {
        "source": "github",
        "repo": "acme/tools"
      }
    },
    "fugaro": {
      "source": {
        "source": "github",
        "repo": "dimipaun/fugaro",
        "ref": "v0.1.0"
      }
    }
  },
  "enabledPlugins": {
    "acme@acme": true,
    "fugaro@fugaro": true
  },
  "model": "sonnet"
}
`

// TestPinNoticeFromDefaultBranch: a repository whose default branch still
// carries the plugin pin an older fugaro wrote gets one line from `update`
// naming the file and the exact edit, and nothing writes the repository's
// working tree (U6a, D3340). The view the step reads is C1's — every enrolled
// repository, at its default branch — and a pin that is not ours earns no line.
func TestPinNoticeFromDefaultBranch(t *testing.T) {
	root := repoCheckout(t, githubOrigin, refreshYAML)
	t.Chdir(root)
	settings := filepath.Join(root, repoNoticePinFile)
	checkoutFile(t, settings, pinnedAmongOthers)
	e, out := checkoutEngine(t, nil)

	e.noticeRepoValues(theUpdateLeavesTheRemoval(t.Context()))

	seen := out.String()
	if !strings.Contains(seen, repoNoticePinFile+": "+repoNoticePinEdit) {
		t.Fatalf("the run printed no notice for the pin in the checkout's %s:\n%s", repoNoticePinFile, seen)
	}
	if !strings.Contains(seen, "acme/app: "+string(initflow.RepoNeedsYou)+": ") {
		t.Errorf("the notice does not name the repository and its state:\n%s", seen)
	}
	if got := convergeRead(t, settings); got != pinnedAmongOthers {
		t.Errorf("%s after the step:\n%s\nwant it left exactly as the release behind wrote it", settings, got)
	}
}

// TestBudgetOffNoticeFromDefaultBranch: the same, for the other dead value —
// budget.mode: off in a default branch's fugaro.yaml. The word is read out of
// the branch's own bytes (the view's YAML has had it spliced out before the
// parse, which is how the run reads through it), and the step's answer is the
// line naming it and no write.
func TestBudgetOffNoticeFromDefaultBranch(t *testing.T) {
	root := repoCheckout(t, githubOrigin, refreshYAML+offRepoYAML)
	t.Chdir(root)
	e, out := checkoutEngine(t, nil)

	e.noticeRepoValues(theUpdateLeavesTheRemoval(t.Context()))

	seen := out.String()
	if !strings.Contains(seen, repoNoticeYAMLFile+": "+repoNoticeBudgetEdit) {
		t.Fatalf("the run printed no notice for the word in the default branch's %s:\n%s", repoNoticeYAMLFile, seen)
	}
	if !strings.Contains(seen, "acme/app: "+string(initflow.RepoNeedsYou)+": ") {
		t.Errorf("the notice does not name the repository and its state:\n%s", seen)
	}
	if got := convergeRead(t, filepath.Join(root, repoNoticeYAMLFile)); got != refreshYAML+offRepoYAML {
		t.Errorf("fugaro.yaml after the step:\n%s\nwant it left as the repository committed it", got)
	}
}

// TestNoticeSurvivesAnEarlierReadOfTheRun: the run reads the repositories once
// and every later call is answered with the views that first read took, so the
// two files the notices are judged from are asked for on the run's own read
// (e.repoExtras) and not at the step's. A stage that read the list first must
// not cost the owner the notice (U6a).
func TestNoticeSurvivesAnEarlierReadOfTheRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		// plant makes the checkout hold the dead value the case is about.
		plant func(t *testing.T, root string)
		want  string
	}{
		{
			name: "the plugin pin",
			yaml: refreshYAML,
			plant: func(t *testing.T, root string) {
				checkoutFile(t, filepath.Join(root, repoNoticePinFile), pinnedAmongOthers)
			},
			want: repoNoticePinFile + ": " + repoNoticePinEdit,
		},
		{
			name:  "budget.mode: off",
			yaml:  refreshYAML + offRepoYAML,
			plant: func(*testing.T, string) {},
			want:  repoNoticeYAMLFile + ": " + repoNoticeBudgetEdit,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := repoCheckout(t, githubOrigin, tc.yaml)
			t.Chdir(root)
			tc.plant(t, root)
			e, out := checkoutEngine(t, nil)
			// The door's own context, which is what every stage of a real
			// update reads under: it is what makes the run read through the
			// dead word instead of refusing the file (markTheUpdateReads).
			ctx := theUpdateLeavesTheRemoval(t.Context())

			e.repos(ctx)

			e.noticeRepoValues(ctx)
			if seen := out.String(); !strings.Contains(seen, tc.want) {
				t.Fatalf("a stage read the list first, and the notice went with it:\n%s", seen)
			}
		})
	}
}

// A checkout is somebody else's, and the extra files this step reads are read
// the way a checkout's own files are (config.ReadRegular): a settings file that
// is a symlink is not followed — it could name any file the reader can read —
// and one that is a FIFO is not opened at all, so the read cannot block. The
// step still answers, with no notice for the file it did not get (U6a).
func TestNoticeReadsNoExtraFileThatIsNotARegularOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		// plant makes .claude/settings.json what the case is about, and
		// answers whether the case can run here.
		plant func(t *testing.T, settings string) bool
	}{
		{name: "a symlink", plant: func(t *testing.T, settings string) bool {
			real := filepath.Join(filepath.Dir(filepath.Dir(settings)), "elsewhere.json")
			checkoutFile(t, real, pinnedAmongOthers)
			if err := os.Symlink(real, settings); err != nil {
				t.Skip(err)
				return false
			}
			return true
		}},
		{name: "a FIFO", plant: func(t *testing.T, settings string) bool {
			if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(settings, 0o644); err != nil {
				t.Skip(err)
				return false
			}
			return true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := repoCheckout(t, githubOrigin, refreshYAML)
			t.Chdir(root)
			settings := filepath.Join(root, repoNoticePinFile)
			if !tc.plant(t, settings) {
				return
			}
			e, out := checkoutEngine(t, nil)
			done := make(chan struct{})
			go func() {
				defer close(done)
				e.noticeRepoValues(theUpdateLeavesTheRemoval(t.Context()))
			}()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatalf("the step did not answer with a %s in the checkout: the read blocked", tc.name)
			}
			if seen := out.String(); strings.Contains(seen, repoNoticePinEdit) {
				t.Errorf("a %s earned the pin's notice: the file was read:\n%s", tc.name, seen)
			}
		})
	}
}
