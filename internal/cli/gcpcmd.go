package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// jobSpec is everything the M4 bootstrap passes to gcloud for one
// repository's workflow. It is derived here, from the naming contract in
// internal/backend/gcp, so the script never re-implements it.
type jobSpec struct {
	Project             string            `json:"project"`
	Region              string            `json:"region"`
	Job                 string            `json:"job"`
	ServiceAccountID    string            `json:"service_account_id"`
	ServiceAccount      string            `json:"service_account"`
	Image               string            `json:"image"`
	CPU                 int               `json:"cpu"`
	Memory              string            `json:"memory"`
	TaskTimeoutS        int               `json:"task_timeout_s"`
	Env                 map[string]string `json:"env"`
	Secrets             map[string]string `json:"secrets"` // env var → secret ID
	GitSecret           string            `json:"git_secret"`
	BuildServiceAccount string            `json:"build_service_account,omitempty"`

	secretNames     map[string]string // logical name → secret ID
	buildSecretIDs  []string          // the git secret and the workflow secrets
	bucketCondition string
}

// jobSpecFields are the values of --field.
var jobSpecFields = []string{"job", "sa-id", "sa", "image", "cpu", "memory", "task-timeout", "git-secret",
	"env", "secrets", "secret-ids", "secret-names", "build-secret-ids", "bucket-condition"}

func newGCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "gcp",
		Short:  "Development helpers for the GCP backend (M4 bootstrap only)",
		Hidden: true,
	}
	cmd.AddCommand(newGCPJobSpecCmd())
	return cmd
}

type jobSpecOptions struct {
	workflow, repo, field string
	asJSON                bool
}

// newGCPJobSpecCmd is `fugaro gcp job-spec`: the names and flags
// deploy/bootstrap/gcp-m4.sh passes to gcloud for the checkout's workflow.
// It reads only the checkout and the local config.
func newGCPJobSpecCmd() *cobra.Command {
	var o jobSpecOptions
	cmd := &cobra.Command{
		Use:   "job-spec",
		Short: "Print the Cloud Run job spec of the checkout's workflow (used by deploy/bootstrap/gcp-m4.sh)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.field != "" && o.asJSON {
				return &ExitError{Code: ExitUserError, Err: errors.New("--field and --json are mutually exclusive")}
			}
			if o.field != "" && !slices.Contains(jobSpecFields, o.field) {
				return &ExitError{Code: ExitUserError, Err: fmt.Errorf("unknown --field %q; want one of %s", o.field, strings.Join(jobSpecFields, ", "))}
			}
			js, err := buildJobSpec(cmd.Context(), o)
			if err != nil {
				return err
			}
			return printJobSpec(cmd.OutOrStdout(), js, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.workflow, "workflow", "", "workflow; optional when fugaro.yaml defines one")
	f.StringVar(&o.repo, "repo", "", "repository as owner/name (default: from the checkout's origin; must match it)")
	f.StringVar(&o.field, "field", "", "print one value: "+strings.Join(jobSpecFields, ", "))
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	return cmd
}

func buildJobSpec(ctx context.Context, o jobSpecOptions) (*jobSpec, error) {
	root, cfg, name, err := loadCheckout(ctx, o.workflow)
	if err != nil {
		return nil, err
	}
	repo, err := checkoutRepo(ctx, root, o.repo)
	if err != nil {
		return nil, err
	}
	lc, err := loadLocalConfig()
	if err != nil {
		return nil, err
	}
	if lc.Registry == "" {
		return nil, &ExitError{Code: ExitUserError, Err: errors.New("the local config has no registry")}
	}
	bucket, err := bucketName(lc)
	if err != nil {
		return nil, &ExitError{Code: ExitUserError, Err: err}
	}
	w := cfg.Workflows[name]
	if issues := gcp.CheckResources(w.Resources); len(issues) > 0 {
		msgs := make([]string, len(issues))
		for i, is := range issues {
			msgs[i] = "resources." + is.Field + ": " + is.Message
		}
		return nil, &ExitError{Code: ExitUserError, Err: fmt.Errorf("workflow %s cannot run on Cloud Run: %s", name, strings.Join(msgs, "; "))}
	}

	slug := task.Slug(repo)
	saID := gcp.ServiceAccountID(slug, name)
	js := &jobSpec{
		Project:          lc.Project,
		Region:           lc.Region,
		Job:              gcp.JobName(slug, name),
		ServiceAccountID: saID,
		ServiceAccount:   saID + "@" + lc.Project + ".iam.gserviceaccount.com",
		Image:            gcp.ImageName(lc.Registry, slug, name),
		CPU:              w.Resources.CPU,
		Memory:           w.Resources.Memory,
		TaskTimeoutS:     int((w.Timeouts.Total.Duration + 2*time.Minute) / time.Second),
		Env: map[string]string{
			"FUGARO_BUCKET":  "gs://" + bucket,
			"FUGARO_BACKEND": "cloud-run",
			"FUGARO_PROJECT": lc.Project,
			"FUGARO_REGION":  lc.Region,
		},
		Secrets:             map[string]string{},
		BuildServiceAccount: lc.Build.ServiceAccount,
		secretNames:         map[string]string{},
	}
	mount := func(logical, env string) {
		id := gcp.SecretID(slug, logical)
		js.Secrets[env] = id
		js.secretNames[logical] = id
	}

	switch cfg.Git.Provider {
	case gitprov.KindBitbucket:
		mount("bitbucket-token", config.ReservedSecrets["bitbucket-token"])
		js.GitSecret = gcp.SecretID(slug, "bitbucket-token")
	default:
		// A GitHub job also needs the App ID and installation, which come
		// from Terraform's variables in M5.
		return nil, &ExitError{Code: ExitUserError, Err: fmt.Errorf("git.provider %s is not supported by the M4 bootstrap; only bitbucket is (GitHub arrives with M5's Terraform)", cfg.Git.Provider)}
	}
	switch cfg.Agent.Auth {
	case "oauth":
		mount("claude-oauth-token", config.ReservedSecrets["claude-oauth-token"])
	case "api-key":
		mount("anthropic-api-key", config.ReservedSecrets["anthropic-api-key"])
	case "vertex":
		js.Env["CLOUD_ML_REGION"] = lc.Region
		js.Env["ANTHROPIC_VERTEX_PROJECT_ID"] = lc.Project
	}
	js.buildSecretIDs = []string{js.GitSecret}
	for _, s := range w.Secrets {
		mount(s.Name, s.Env)
		js.buildSecretIDs = append(js.buildSecretIDs, gcp.SecretID(slug, s.Name))
	}
	slices.Sort(js.buildSecretIDs)
	js.buildSecretIDs = slices.Compact(js.buildSecretIDs)

	// Trailing slashes, so acme-app can never reach acme-app-x (design §6.1).
	var conds []string
	for _, prefix := range []string{"runs", "cache", "locks"} {
		conds = append(conds, fmt.Sprintf(`resource.name.startsWith("projects/_/buckets/%s/objects/%s/%s/")`, bucket, prefix, slug))
	}
	js.bucketCondition = strings.Join(conds, " || ")
	return js, nil
}

// checkoutRepo returns the checkout's owner/name from its origin, and
// refuses a --repo that names another repository: the names would be for
// one repository and the secrets and resources for another.
func checkoutRepo(ctx context.Context, root, flag string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "remote", "get-url", "origin")
	cmd.WaitDelay = 5 * time.Second
	out, _ := cmd.Output()
	origin := ""
	if u, err := url.Parse(image.HTTPSOrigin(strings.TrimSpace(string(out)))); err == nil && u.Scheme == "https" {
		origin = strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
		if _, _, ok := gitprov.SplitRepo(origin); !ok {
			origin = ""
		}
	}
	switch {
	case flag == "" && origin == "":
		return "", &ExitError{Code: ExitUserError, Err: errors.New("cannot tell the repository from the checkout's origin; pass --repo owner/name")}
	case flag == "":
		return origin, nil
	}
	if _, _, ok := gitprov.SplitRepo(flag); !ok {
		return "", &ExitError{Code: ExitUserError, Err: fmt.Errorf("--repo %q must look like owner/name", flag)}
	}
	if origin != "" && !strings.EqualFold(flag, origin) {
		return "", &ExitError{Code: ExitUserError, Err: fmt.Errorf("--repo %s does not match the checkout's origin %s", flag, origin)}
	}
	return flag, nil
}

func loadLocalConfig() (*localcfg.Config, error) {
	path, err := localcfg.Path(os.Getenv)
	if err != nil {
		return nil, &ExitError{Code: ExitUserError, Err: err}
	}
	lc, err := localcfg.Load(path)
	if err != nil {
		return nil, &ExitError{Code: ExitUserError, Err: err}
	}
	return lc, nil
}

// bucketName is the runs bucket's name, which IAM conditions need.
func bucketName(lc *localcfg.Config) (string, error) {
	if lc.RunsBucket != "" {
		return lc.RunsBucket, nil
	}
	if u, err := url.Parse(lc.Bucket); err == nil && u.Scheme == "gs" && u.Host != "" {
		return u.Host, nil
	}
	return "", errors.New("the local config's bucket_url is not a gs:// bucket")
}

func printJobSpec(w io.Writer, js *jobSpec, o jobSpecOptions) error {
	if o.asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(js)
	}
	if o.field == "" {
		fmt.Fprintf(w, "job %s in %s/%s\n  image %s:latest\n  service account %s\n  %d CPU, %s, task timeout %ds\n",
			js.Job, js.Project, js.Region, js.Image, js.ServiceAccount, js.CPU, js.Memory, js.TaskTimeoutS)
		for _, k := range slices.Sorted(maps.Keys(js.Env)) {
			fmt.Fprintf(w, "  env %s=%s\n", k, js.Env[k])
		}
		for _, k := range slices.Sorted(maps.Keys(js.Secrets)) {
			fmt.Fprintf(w, "  secret %s <- %s\n", k, js.Secrets[k])
		}
		return nil
	}
	_, err := fmt.Fprintln(w, jobSpecField(js, o.field))
	return err
}

// jobSpecField renders one --field value in the form gcloud takes it.
func jobSpecField(js *jobSpec, field string) string {
	pairs := func(m map[string]string, format string) []string {
		var out []string
		for _, k := range slices.Sorted(maps.Keys(m)) {
			out = append(out, fmt.Sprintf(format, k, m[k]))
		}
		return out
	}
	switch field {
	case "job":
		return js.Job
	case "sa-id":
		return js.ServiceAccountID
	case "sa":
		return js.ServiceAccount
	case "image":
		return js.Image
	case "cpu":
		return strconv.Itoa(js.CPU)
	case "memory":
		return js.Memory
	case "task-timeout":
		return strconv.Itoa(js.TaskTimeoutS)
	case "git-secret":
		return js.GitSecret
	case "env":
		return strings.Join(pairs(js.Env, "%s=%s"), ",")
	case "secrets":
		return strings.Join(pairs(js.Secrets, "%s=%s:latest"), ",")
	case "secret-ids":
		ids := slices.Sorted(maps.Values(js.Secrets))
		return strings.Join(slices.Compact(ids), "\n")
	case "secret-names":
		return strings.Join(pairs(js.secretNames, "%s=%s"), "\n")
	case "build-secret-ids":
		return strings.Join(js.buildSecretIDs, "\n")
	case "bucket-condition":
		return js.bucketCondition
	}
	return ""
}
