package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The inputs a first run needs (design §3.1, "Inputs and defaults"): the
// Fugaro project's name, the GCP project's ID and the region. In a terminal
// each is asked once, with a suggestion Enter accepts; without one (or with
// --non-interactive, --yes or --json, which never prompt) the run is refused
// with every flag still missing named in one message. Once the local config
// exists nothing is asked.
//
// The GCP project's suggestion comes only from GOOGLE_CLOUD_PROJECT or the
// quota project of the application default credentials. It never comes from
// gcloud's configured default project (the standing rule: a default project
// is how a command lands in the wrong project), so neither gcloud's
// properties file nor CLOUDSDK_CORE_PROJECT is read here.

const defaultRegion = "us-east5"

// regionRE is the shape of a Google Cloud region (us-east5, europe-west4).
var regionRE = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+$`)

var nameJunkRE = regexp.MustCompile(`[^a-z0-9]+`)

// sanitiseName turns s into a project name (config.ProjectNameRE: 1 to 40 of
// a-z, 0-9 and '-', starting and ending with a letter or digit), or "" when
// nothing usable is left.
func sanitiseName(s string) string {
	s = strings.Trim(nameJunkRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if !config.ProjectNameRE.MatchString(s) {
		return ""
	}
	return s
}

// suggestName is the name suggested for the project: the owner of the
// checkout's origin repository, else the checkout's (or the working
// directory's) own name, sanitised.
func suggestName(ctx context.Context) string {
	_, n, _ := originOwner(ctx)
	if n != "" {
		return n
	}
	return dirName(ctx)
}

// originOwner is the checkout's origin repository's owner, sanitised.
func originOwner(ctx context.Context) (repo, name string, ok bool) {
	if repo, err := originRepo(ctx); err == nil {
		owner, _, _ := strings.Cut(repo, "/")
		if n := sanitiseName(owner); n != "" {
			return repo, n, true
		}
	}
	return "", "", false
}

func dirName(ctx context.Context) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	if out, err := gitCmd(ctx, ".", "rev-parse", "--show-toplevel").Output(); err == nil {
		dir = strings.TrimSpace(string(out))
	}
	return sanitiseName(filepath.Base(dir))
}

// adcPath is where the application default credentials are: the file
// GOOGLE_APPLICATION_CREDENTIALS names, else gcloud's well-known one. Tests
// replace it.
var adcPath = func() string {
	if p := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); p != "" {
		return p
	}
	dir := os.Getenv("CLOUDSDK_CONFIG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config", "gcloud")
	}
	return filepath.Join(dir, "application_default_credentials.json")
}

// adcQuotaProject is the quota project the application default credentials
// name. Only that one field is decoded; nothing else of the file is kept.
func adcQuotaProject() string {
	p := adcPath()
	if p == "" {
		return ""
	}
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	var c struct {
		Quota string `json:"quota_project_id"`
	}
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&c) != nil || !infra.ValidProjectID(c.Quota) {
		return ""
	}
	return c.Quota
}

// suggestGCPProject is the GCP project ID to suggest, or "": never gcloud's
// configured default project.
func suggestGCPProject() string {
	if v := os.Getenv("GOOGLE_CLOUD_PROJECT"); infra.ValidProjectID(v) {
		return v
	}
	return adcQuotaProject()
}

// missingInput is one input a first run lacks.
type missingInput struct{ flag, what string }

func (m missingInput) String() string { return m.flag + " (" + m.what + ")" }

// firstRunMissing is what creating the project config still needs: the name
// (not with --config-only, which takes it from the installation), the GCP
// project and the region, or none when a config is selected.
func (r *initRun) firstRunMissing(ctx context.Context) (name, fromCheckout string, missing []missingInput, creating bool) {
	o := r.o
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return "", "", nil, false // loadInitConfig reports it
	}
	sel, lc, err := selectNamed(o.cloud, co, true, o.name)
	if err != nil || lc != nil {
		return "", "", nil, false
	}
	name = o.name
	if name == "" && sel.From == "checkout" {
		// A checkout's own fugaro.yaml must not name a brand-new, permanent
		// installation: it is somebody's file, perhaps a clone's.
		fromCheckout = sel.Name
	} else if name == "" {
		name = sel.Name
	}
	if name == "" && !o.configOnly {
		what := "the Fugaro project's name"
		if fromCheckout != "" {
			what = "this checkout's fugaro.yaml says " + pluginwire.Printable(fromCheckout) + ", which does not by itself name a new installation (the name is permanent): pass --name " + pluginwire.Printable(fromCheckout) + " to confirm it, or another name"
		}
		missing = append(missing, missingInput{"--name <name>", what})
	}
	if o.cloud.gcpProject == "" {
		missing = append(missing, missingInput{"--gcp-project <id>", "the GCP project ID"})
	}
	if o.cloud.region == "" {
		missing = append(missing, missingInput{"--region <region>", "the GCP region, such as " + defaultRegion})
	}
	return name, fromCheckout, missing, true
}

// canAsk is whether the run may show a prompt for an input: a terminal on
// stdin, and none of --non-interactive, --yes, --json (a script's flags) or
// --print-vars (stdout is the variables alone).
func (r *initRun) canAsk() bool {
	o := r.o
	return !o.nonInteractive && !o.yes && !o.asJSON && !o.printVars && stdinIsTerminal(r.cmd.InOrStdin())
}

// gatherInputs completes --name, --gcp-project and --region for a first run:
// it asks for what is missing in a terminal, and otherwise refuses, naming
// every missing flag (the GitHub App's ID too, when the checkout's
// repository needs it) in one error. With a project config nothing is asked.
func (r *initRun) gatherInputs(ctx context.Context) error {
	name, fromCheckout, missing, creating := r.firstRunMissing(ctx)
	if !creating || len(missing) == 0 {
		return nil
	}
	if !r.canAsk() {
		flags := make([]string, len(missing))
		for i, m := range missing {
			flags[i] = m.String()
		}
		if r.o.githubAppID == "" {
			if t, _ := resolveRepoTarget(ctx, name); t != nil && t.needsAppID(nil, r.o) && sameRepo(r.o.onboardRepo, t.repo) {
				flags = append(flags, appIDFlag)
			}
		}
		if r.o.nonInteractive {
			return userErr("%v", &initflow.MissingInputsError{Flags: flags})
		}
		return userErr("there is no project config yet: pass %s to create one", strings.Join(flags, ", "))
	}
	fmt.Fprintln(r.w, "fugaro init: this is the first run here; each answer has a suggestion, press Enter to accept it.")
	o := r.o
	if name == "" && !o.configOnly {
		def, label := suggestName(ctx), "Fugaro project name (permanent"
		switch _, _, ok := originOwner(ctx); {
		case fromCheckout != "":
			// No default: the file may be a clone's, so the name is typed.
			def, label = "", label+"; this checkout's fugaro.yaml says "+pluginwire.Printable(fromCheckout)+", but a cloned repository's file can say anything: type the name you want"
		case ok:
			label += "; suggested from the origin's owner"
		case def != "":
			label += "; suggested from this directory's name"
		}
		label += ")"
		v, err := r.prompt(label, def, func(s string) error {
			if !config.ProjectNameRE.MatchString(s) {
				return errors.New("a project name is 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
			}
			return nil
		})
		if err != nil {
			return err
		}
		o.name = v
	}
	if o.cloud.gcpProject == "" {
		v, err := r.prompt("GCP project ID", suggestGCPProject(), func(s string) error {
			if !infra.ValidProjectID(s) {
				return errors.New("a project ID is 6 to 30 of a-z, 0-9 and '-', starting with a letter")
			}
			return nil
		})
		if err != nil {
			return err
		}
		o.cloud.gcpProject = v
	}
	if o.cloud.region == "" {
		v, err := r.prompt("Region", defaultRegion, func(s string) error {
			if !regionRE.MatchString(s) {
				return errors.New("a region looks like us-east5 or europe-west4")
			}
			return nil
		})
		if err != nil {
			return err
		}
		o.cloud.region = v
	}
	return nil
}

// prompt asks label with def as the suggestion and returns the validated
// answer. Three unusable answers, or the end of input, refuse the run.
func (r *initRun) prompt(label, def string, valid func(string) error) (string, error) {
	for range 3 {
		if def != "" {
			fmt.Fprintf(r.w, "%s [%s]: ", label, def)
		} else {
			fmt.Fprintf(r.w, "%s: ", label)
		}
		line, err := r.in.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", userErr("reading the answer: %v", err)
		}
		v := cmp.Or(strings.TrimSpace(line), def)
		if errors.Is(err, io.EOF) && strings.TrimSpace(line) == "" {
			return "", userErr("no answer for %q (the input ended)", label)
		}
		if v == "" {
			fmt.Fprintln(r.w, "  a value is needed")
			continue
		}
		if verr := valid(v); verr != nil {
			fmt.Fprintf(r.w, "  %v\n", verr)
			continue
		}
		return v, nil
	}
	return "", userErr("no usable answer for %q after 3 tries", label)
}
