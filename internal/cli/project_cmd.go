package cli

import (
	"github.com/spf13/cobra"
)

// The `fugaro project` command group: the project's policy, its allow-list
// and what is enrolled (docs/plans/2026-10-14-project-centric.md, phase 1
// task 1.6). It is not internal/cli/project.go, which is project *selection*
// (checkoutProject); the group gets its own command files, of which this is
// the first (tasks 4.1 and 6.1 add project_enroll.go and project_export.go
// beside it).
//
// Every command here writes through the operator's own credentials (the same
// client `fugaro budget set` uses: IAM decides, and the rules grant no
// client write to config/*), never through a run token. Nothing on this path
// loads a secret's value: a provider entry carries the secret's NAME.

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "The project's policy, its allow-list and the repositories enrolled",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newProjectPolicyCmd(), newProjectShowCmd(), newProjectReposCmd())
	return cmd
}

// newProjectPolicyCmd is `fugaro project policy`: the published node.
func newProjectPolicyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "The project's published policy (config/policy/shared)",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newProjectPolicyPublishCmd(), newProjectPolicyShowCmd())
	return cmd
}

type projectPolicyPublishOptions struct {
	cloud     cloudOptions
	seedRepos bool
	delete    bool
	yes       bool
}

func newProjectPolicyPublishCmd() *cobra.Command {
	var o projectPolicyPublishOptions
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish the project's policy from the operator's config (budget admins)",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runProjectPolicyPublish(cmd, &o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.seedRepos, "seed-repos", false, "seed allow_repos from the operator's repositories again")
	f.BoolVar(&o.delete, "delete", false, "remove the published policy (project-centric mode off again)")
	f.BoolVar(&o.yes, "yes", false, "confirm the deletion without typing the project's name")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runProjectPolicyPublish(cmd *cobra.Command, o *projectPolicyPublishOptions) error {
	_ = cmd
	_ = o
	return nil
}

type projectShowOptions struct {
	cloud cloudOptions
	json  bool
}

func newProjectPolicyShowCmd() *cobra.Command {
	var o projectShowOptions
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the published policy",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runProjectShow(cmd, &o, true) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func newProjectShowCmd() *cobra.Command {
	var o projectShowOptions
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the effective project: caps, policy, allow-list, repositories seen",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runProjectShow(cmd, &o, false) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runProjectShow(cmd *cobra.Command, o *projectShowOptions, policyOnly bool) error {
	_, _, _ = cmd, o, policyOnly
	return nil
}

// newProjectReposCmd is `fugaro project repos`: the interim enrollment path
// between phases 1 and 4 (P4, 12a item 3). `sync` and the discovery surface
// stay in phase 4 (task 4.1).
func newProjectReposCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repos",
		Short: "The project's allow-list: which repositories may run",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newProjectReposLsCmd(), newProjectReposAddCmd(), newProjectReposRmCmd())
	return cmd
}

type projectReposOptions struct {
	cloud cloudOptions
	yes   bool
}

func newProjectReposLsCmd() *cobra.Command {
	var o projectReposOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the allow-list and the repositories seen",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runProjectReposLs(cmd, &o) },
	}
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func newProjectReposAddCmd() *cobra.Command {
	var o projectReposOptions
	cmd := &cobra.Command{
		Use:   "add <provider/owner/name | owner/name>",
		Short: "Enroll a repository: add it to the policy's allow-list",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runProjectReposEdit(cmd, &o, args[0], false) },
	}
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func newProjectReposRmCmd() *cobra.Command {
	var o projectReposOptions
	cmd := &cobra.Command{
		Use:   "rm <provider/owner/name | owner/name>",
		Short: "Un-enroll a repository: remove it from the allow-list",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runProjectReposEdit(cmd, &o, args[0], true) },
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the removal without typing the project's name")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runProjectReposLs(cmd *cobra.Command, o *projectReposOptions) error {
	_, _ = cmd, o
	return nil
}

func runProjectReposEdit(cmd *cobra.Command, o *projectReposOptions, repo string, remove bool) error {
	_, _, _, _ = cmd, o, repo, remove
	return nil
}
