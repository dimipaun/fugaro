package cli

// repo_images_e2e_test.go is X1's file: L11's project — one name at two
// providers — read by every reader of the map at once.
//
// The project it holds together is the one the acceptance criterion names: an
// `acme/app` at GitHub and an `acme/app` at Bitbucket, each mapped to its own
// catalog entry (`go` and `tools`). One key per repository and provider is what
// makes that project expressible at all (D3370): the legacy image_map is keyed
// by the bare name, so both repositories were one key and one entry, and the
// Bitbucket one ran the GitHub one's image. Each subtest below is one reader
// answering for the Bitbucket repository, and every one of them must answer
// `tools`.
//
// The readers each need their own world — a checkout, an installation, a Cloud
// Build, a published node — so they are subtests and not one fixture. What they
// share is the definition: twoProvRepo, the two entry names, and the two spellings
// of the map and the catalog (YAML for the file, the node builder for the
// published policy), all built from those constants so no subtest can drift
// from another.
//
// Mutation, run and restored: with the map read by the bare name instead of the
// provider-qualified one, every subtest fails. Four of them read through
// catalog.ImageRequest.mappedEntry (the launch, `update`'s job images, doctor
// and `repos view`), so one mutation there fails all four: the launch refuses
// nothing it must and finds no entry to speak for, `update` moves no job, doctor
// judges no entry for the workflow, and `repos view` reads both rows as
// SourceRepo. The bake has its own reader (imagebake.Request.mappedEntry, whose
// keys are already provider-qualified), and mutating that one composes no stage
// for either machine.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/imagebake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The project every subtest below is about: one repository name, two providers,
// two entries. twoProvGitHubRepo and twoProvBitbucketRepo are the two keys of the map —
// projectpolicy.Canonical's spelling, which is the only spelling a key has
// (R2) — so an assertion never compared against a key the fixture did not
// build.
const (
	twoProvRepo           = "acme/app"
	twoProvGitHubEntry    = "go"
	twoProvBitbucketEntry = "tools"
)

const (
	twoProvGitHubRepo    = "github/" + twoProvRepo
	twoProvBitbucketRepo = "bitbucket/" + twoProvRepo
)

// twoProvMapYAML is the project's repository-to-image map as this machine's config
// spells it: the two keys, one per provider.
const twoProvMapYAML = "repo_images:\n  " + twoProvGitHubRepo + ": " + twoProvGitHubEntry + "\n  " + twoProvBitbucketRepo + ": " + twoProvBitbucketEntry + "\n"

// twoProvCatalogYAML is the catalog the file names: two entries a Fugaro build
// writes, and no entry marked default. This project's whole statement about its
// images is the map — every repository it has, it names there — which is what
// repo_images is for, and a catalog default would be a second answer no reader
// of this project needs.
//
// That is also what makes the collision visible: a reader that lost the
// provider finds no key, the catalog names no default either, and the
// repository keeps the image this project builds for it (SourceRepo, O4 b) —
// an entry no mapping gives it, while the Bitbucket repository's own is
// `tools`.
const twoProvCatalogYAML = `image_catalog:
  entries:
    - name: ` + twoProvGitHubEntry + `
      tags: [node]
      source: { kind: inline, inline: "FROM scratch\n" }
    - name: ` + twoProvBitbucketEntry + `
      tags: [node]
      source: { kind: inline, inline: "FROM scratch\n" }
`

// twoProvCatalogEntries is twoProvCatalogYAML as the published node carries the
// catalog: the same two entries, in the same order, with no default.
func twoProvCatalogEntries() []any {
	return []any{
		map[string]any{"name": twoProvGitHubEntry, "tags": []string{"node"},
			"source": map[string]any{"kind": "inline", "inline": "FROM scratch\n"}},
		map[string]any{"name": twoProvBitbucketEntry, "tags": []string{"node"},
			"source": map[string]any{"kind": "inline", "inline": "FROM scratch\n"}},
	}
}

// twoProvBitbucketYAML is the repository's own file: the Bitbucket repository's, so
// the provider the map is read under and the provider the repository's jobs are
// slugged with are the same one.
const twoProvBitbucketYAML = "version: 1\nproject: aurora\ngit: { provider: bitbucket }\n" +
	"workflows:\n  web: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// twoProvBitbucketCheckout is a checkout of the Bitbucket acme/app: its origin is
// Bitbucket's, and its own file names the same provider, which is the machine
// the Bitbucket repository is onboarded on. The body is the file it carries —
// doctor's fixture needs one the project layer applies to, and every other
// reader wants the plain one.
func twoProvBitbucketCheckout(t *testing.T, body string) string {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "remote", "add", "origin", "git@bitbucket.org:"+twoProvRepo+".git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": body, "README.md": "code\n"})
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "-q", "-m", "init")
	t.Chdir(dir)
	return dir
}

// twoProvBitbucketConfig points the fixture's own config at the Bitbucket
// repository: the machine's `repos` entry is what makes the launch read the map
// under bitbucket (run.go's repoProvider), and the fixture ships acme/app at
// GitHub.
func twoProvBitbucketConfig(t *testing.T, f *cloudFixture) {
	t.Helper()
	f.rewriteConfig(t, "  acme/app: { provider: github,", "  acme/app: { provider: bitbucket,")
}

// TestRepoImagesEndToEnd is acceptance criterion 1: a project with a GitHub and
// a Bitbucket acme/app, each mapped to its own entry, and every reader of the
// map answering the Bitbucket repository with the Bitbucket key's entry —
// launch, `update`'s job images, the bake, doctor and `repos view`.
func TestRepoImagesEndToEnd(t *testing.T) {
	// The launch reads the map under the provider the repository is onboarded
	// on, and a job deployed with the OTHER entry's image is then a job the
	// project no longer gives this repository: the launch refuses it rather
	// than start a run on an image the project moved away from. A reader on the
	// bare name finds no key at all and speaks for no entry of the catalog's,
	// so it admits as the project's own both jobs — the one it must refuse, and
	// the one no mapping names.
	t.Run("the launch", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			deployed string
			refused  bool
		}{
			{"the Bitbucket key's entry", twoProvBitbucketEntry, false},
			{"the GitHub key's entry", twoProvGitHubEntry, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newCloudFixture(t)
				twoProvBitbucketConfig(t, f)
				f.appendConfig(t, twoProvCatalogYAML)
				f.appendConfig(t, twoProvMapYAML)
				twoProvBitbucketCheckout(t, twoProvBitbucketYAML)
				env := fileEnv(t, f)
				env.be = &mappedJob{codec: backend.CloudRunCodec(), image: catalogImageOf(tc.deployed)}
				spec := &task.Spec{Version: 1, RunID: "20261127-100000-a1b2", Repo: twoProvRepo, Ref: "main", Workflow: "web", Task: "Add a feature"}

				entry, speaks, _, err := deployedCatalogEntry(context.Background(), env, mustSlug(gitprov.KindBitbucket, twoProvRepo), spec)

				if tc.refused {
					if err == nil {
						t.Fatalf("the launch of the Bitbucket %s on the image of %s was admitted (entry %q): repo_images gives %s the entry %s, so that job is one the project moved off",
							twoProvRepo, twoProvGitHubEntry, entry.Name, twoProvBitbucketRepo, twoProvBitbucketEntry)
					}
					if !strings.Contains(err.Error(), "no longer gives it") {
						t.Errorf("the refusal does not name the project's map as the reason: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("the launch of the Bitbucket %s on the image of %s was refused: %v", twoProvRepo, twoProvBitbucketEntry, err)
				}
				if !speaks || entry.Name != twoProvBitbucketEntry {
					t.Errorf("the launch reads the deployed image as entry %q (speaks=%v), want %q: the Bitbucket repository's key is what the map is read under",
						entry.Name, speaks, twoProvBitbucketEntry)
				}
			})
		}
	})

	// `update`'s job-images step resolves each repository's workflows through
	// the same resolver a deploy uses (infra.Repo), so a job whose deployed
	// image is not the one the map names for THIS repository is moved onto it.
	// The fixture's job is deployed on `go` — the GitHub key's entry, which is
	// the image a job of that repository would carry — and the step must move
	// it to the entry the Bitbucket key names.
	t.Run("the update's job images", func(t *testing.T) {
		r := newInitRig(t)
		twoProvRigRepo(t, r, twoProvRepo)
		r.appendConfig(t, twoProvCatalogYAML)
		r.appendConfig(t, twoProvMapYAML)
		e := repoJobImagesEngine(t, r)
		slug, label := checkJobOwner(t, gitprov.KindBitbucket, twoProvRepo)
		r.run.SetJob(gcp.JobName(slug, "web"), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: "web"}, catalogImageOf(twoProvGitHubEntry))
		useRepoFilesProvider(t, twoProvRepo, twoProvBitbucketYAML)

		e.reconcileRepoJobs(t.Context())

		if n := len(r.run.Patches()); n != 1 {
			t.Fatalf("%d patches, want the job moved onto the Bitbucket key's entry\n%s", n, e.r.w.(*syncBuf).String())
		}
		want := twoProvRepo + " web: job image catalog-" + twoProvGitHubEntry + ":latest -> catalog-" + twoProvBitbucketEntry + ":latest"
		if got := e.r.w.(*syncBuf).String(); !strings.Contains(got, want) {
			t.Errorf("the run does not say %q:\n%s", want, got)
		}
	})

	// The bake is the reader that decides which repositories an entry's image
	// serves, and it reads the map under the provider of the checkout it is
	// building from (imagebake.Request.Repo.Key). Each machine builds its own
	// repository's entry — the Bitbucket one builds `tools`, the GitHub one
	// builds `go` — and each composes a stage baking its own checkout and no
	// other's: the two same-named repositories are two keys, so neither image
	// holds the other repository's checkout.
	t.Run("the bake", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			provider string
			yaml     string
			appID    string
			entry    string
		}{
			{"the Bitbucket machine", gitprov.KindBitbucket, bitbucketYAML, "", twoProvBitbucketEntry},
			{"the GitHub machine", gitprov.KindGitHub, cliMinimalYAML, catalogBuildAppID, twoProvGitHubEntry},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, gcs, f := catalogBuildCheckoutFor(t, tc.yaml, twoProvCatalogYAML, tc.provider, tc.appID)
				f.appendConfig(t, twoProvMapYAML)

				_, stderr, err := executeBuild(t, "image", "build", tc.entry, "--base", "b:1")
				if err != nil {
					t.Fatalf("building %s from the %s checkout: %v (%s)", tc.entry, tc.provider, err, stderr)
				}
				dir, err := imagebake.Path(mustSlug(tc.provider, twoProvRepo))
				if err != nil {
					t.Fatal(err)
				}
				got, _ := catalogCopy(t, gcs, tc.entry)
				if !strings.Contains(got, "# fugaro bake: "+dir) {
					t.Errorf("the copy of %s has no stage baking a checkout at %s: the entry serves %s at %s, and a reader on the bare name composes no bake at all:\n%s\n%s",
						tc.entry, dir, twoProvRepo, tc.provider, got, stderr)
				}
			})
		}
	})

	// Doctor resolves the same repository's workflow through the same resolver,
	// under the provider this machine's config records for it (doctor_layer.go),
	// and reports the entry it answered: the Bitbucket key's `tools`, which no
	// build has built, so doctor tells the operator to build it. A reader on the
	// bare name finds no key, the catalog names no default, and doctor judges no
	// entry for this workflow at all — the check it owes the project goes
	// missing rather than naming a wrong entry.
	t.Run("doctor", func(t *testing.T) {
		f := newCloudFixture(t)
		isolateCache(t)
		publishedLayer(t, f, testProjectLayer)
		twoProvBitbucketConfig(t, f)
		f.appendConfig(t, twoProvCatalogYAML)
		f.appendConfig(t, twoProvMapYAML)
		dir := twoProvBitbucketCheckout(t, anchoredText(twoProvBitbucketYAML))
		lc := fileEnv(t, f).lc
		ctx := context.Background()

		cs := doctorLayerChecks(ctx, lc, dir, resolvedLayerFixture(t, ctx, lc, dir))

		c := layerCheck(cs, "image-catalog-web")
		if c == nil {
			t.Fatalf("doctor judged no entry for the Bitbucket %s's web workflow: %+v", twoProvRepo, cs)
		}
		if want := "entry " + twoProvBitbucketEntry; !strings.Contains(c.Problem, want) {
			t.Errorf("doctor's line reads %q, want it to name %q: the Bitbucket repository's key is what its workflow is resolved under (%+v)",
				c.Problem, want, c)
		}
		if other := "entry " + twoProvGitHubEntry; strings.Contains(c.Problem, other) {
			t.Errorf("doctor names %q for the Bitbucket %s: that is the GitHub repository's entry, and no key of the map gives it to this one", other, twoProvRepo)
		}
	})

	// `repos view` lists the two repositories as two rows — this machine's
	// config cannot hold one name at two providers, so the Bitbucket row is one
	// the published node admits (F2) — and each row is resolved under its own
	// key. A reader on the bare name finds no key for either row and answers
	// both with the repository's own image (SourceRepo), which is the image the
	// view reports as one the project does not map.
	t.Run("repos view", func(t *testing.T) {
		f := newProjectFixture(t, "")
		f.db.Set(projectpolicy.PathShared(), map[string]any{
			"version":       projectpolicy.Version,
			"budget":        map[string]any{"mode": "enforce", "per_run_usd": 5},
			"allow_repos":   []string{twoProvGitHubRepo, twoProvBitbucketRepo, "bitbucket/acme/other"},
			"image_catalog": map[string]any{"entries": twoProvCatalogEntries()},
			"repo_images": projectpolicy.RepoImages{
				twoProvGitHubRepo:    twoProvGitHubEntry,
				twoProvBitbucketRepo: twoProvBitbucketEntry,
			},
		})

		v := viewOf(t, f.path, f.db, "")
		if v.Allow.State != "" {
			t.Fatalf("the published node did not read: %s", v.Allow.State)
		}
		for _, tc := range []struct{ canonical, entry string }{
			{twoProvGitHubRepo, twoProvGitHubEntry},
			{twoProvBitbucketRepo, twoProvBitbucketEntry},
		} {
			r := viewRow(t, v, tc.canonical)
			if r.ImageEntry != tc.entry || r.ImageSource != reposViewImageMap {
				t.Errorf("%s runs %q from %q, want %q from %q", tc.canonical, r.ImageEntry, r.ImageSource, tc.entry, reposViewImageMap)
			}
		}
	})
}

// twoProvRigRepo renames the converge rig's own repository: the rig ships
// acme/sandbox at Bitbucket, and this test's project is one name at two
// providers, so the machine's entry has to carry the name the map's keys do.
func twoProvRigRepo(t *testing.T, r *initRig, name string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data := readFileString(t, path)
	const old = "  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }\n"
	if !strings.Contains(data, old) {
		t.Fatalf("the rig's repos entry has changed shape:\n%s", data)
	}
	next := "  " + name + ": { provider: bitbucket, base_branch: main, workflows: [web] }\n"
	if err := os.WriteFile(path, []byte(strings.Replace(data, old, next, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRepoImagesConvergesFromPreviousRelease is acceptance criteria 2 and 7 at
// the door the HARD RULE is about: an installation of the release behind —
// whose node and whose local config hold the map in the old form only — reaches
// repo_images with one `fugaro update` and nothing else.
//
// The name is enrolled at both providers (the config records the GitHub
// repository, the node's allow-list admits the Bitbucket one), which is what
// makes the migration write TWO keys for the one bare key the old map holds
// (migrateImageMap's union, D3373): the entry each repository ran under the old
// map is the entry it keeps. A name nothing enrolls is dropped with its line
// rather than guessed at (D3375), and the second `update` is the idempotence
// half: the node the first left is one this run has nothing left to publish.
func TestRepoImagesConvergesFromPreviousRelease(t *testing.T) {
	node := v7Node()
	node["allow_repos"] = []string{twoProvGitHubRepo, twoProvBitbucketRepo}
	r := newConvergeRig(t, node)
	// The map of the release behind: one bare key per repository, and a name
	// no repository of this project has.
	r.appendConfig(t, "image_map:\n  "+twoProvRepo+": "+twoProvGitHubEntry+"\n  acme/gone: "+twoProvBitbucketEntry+"\n")
	registerInstallation(t, r.initRig)

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if want := "image map: acme/gone dropped: no enrolled repository has that name"; !strings.Contains(out, want) {
		t.Errorf("the run does not say %q: a name nothing enrolls has no key to be migrated to, and the line is what says so\n%s", want, out)
	}
	p := convergeNode(t, r)
	want := projectpolicy.RepoImages{twoProvGitHubRepo: twoProvGitHubEntry, twoProvBitbucketRepo: twoProvGitHubEntry}
	if !reflect.DeepEqual(p.RepoImages, want) {
		t.Errorf("after one fugaro update, the published node's repo_images = %v, want %v: the one bare key is the key of every provider the name is enrolled at, and each keeps the entry it ran",
			p.RepoImages, want)
	}
	if p.ImageMap != nil {
		t.Errorf("after one fugaro update, the published node still carries image_map = %v: repo_images is what every reader serves (R2)", p.ImageMap)
	}
	// The local file drops the old member in the same run and states the new
	// one, which is the half a run of this release reads (R14).
	written := readFileString(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initProjectName+".yaml"))
	if strings.Contains(written, "image_map") || !strings.Contains(written, "repo_images:") {
		t.Errorf("after one fugaro update, the local config still states the old member or names no repo_images:\n%s", written)
	}

	// A second `fugaro update` over what the first left: the map is repo_images
	// on both sides now, so the migration has nothing to rewrite and the run
	// publishes not one byte of the node.
	before := nodeConvergeJSON(t, r)
	testutil.Git(t, r.root, "add", "-A")
	testutil.Git(t, r.root, "commit", "-q", "-m", "what the first fugaro update wrote")
	fetched(t, r.root)
	out2, errOut, err := executeStdin(t, "aurora\n", "update")
	if err != nil {
		t.Fatalf("the second fugaro update stopped at %s (exit %d):\n%s\n%s", stoppedStage(out2), ExitCode(err), out2, errOut)
	}
	if after := nodeConvergeJSON(t, r); after != before {
		t.Errorf("the second fugaro update rewrote the published node:\n%s\nwant the node the first run left:\n%s", after, before)
	}
}

// nodeConvergeJSON is the published node as the database holds it, for a test
// comparing what one update left with what another wrote.
func nodeConvergeJSON(t *testing.T, r *convergeRig) string {
	t.Helper()
	b, err := json.Marshal(r.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
