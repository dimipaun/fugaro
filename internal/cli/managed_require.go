package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/spf13/cobra"

	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// managed_require.go is D1541's fence in one place: what a command that writes
// to a project says to a project that is not managed, and the one command that
// turns one into a managed project.
//
// The spec is "Fugaro only supports managed projects", and under the no-prompt
// policy a command may not fall back to asking: a command that writes refuses a
// project that is not managed and prints the one command that flags it managed
// (D1541, answered 2026-11-07: consent by argument, not by typing at a prompt).
// This file is the single writer of that sentence, so the command a refusal
// names — and the way it is named — cannot drift between the commands that
// refuse this way.
//
// Which commands those are is root.go's classification of the whole tree
// (managedFencedCommands and managedExemptCommands), applied to the tree as the
// two annotations below. The mechanism is the command itself: root's pre-run
// puts the running *cobra.Command in the context, and the two seams a command
// opens its cloud clients through — openCloud and openCloudFor (cloud.go) — ask
// this file whether that command is fenced. A command that opens its clients
// another way asks directly (requireManaged), which is the same check.
//
// The fence is read once per command, at the first cloud client it opens, and
// its answer is a refusal or nothing: no command may treat the read as a
// permission it did not get (D2218's rule, in requireManaged's doc below).
const (
	// managedRequiresAnnotation is on a fenced command, holding the opening
	// clause its refusal prints: what it declines and what it is.
	managedRequiresAnnotation = "fugaro.io/requires-managed-project"
	// managedExemptAnnotation is on a command the fence does not apply to,
	// holding the one-line reason it is exempt. It is written on the tree so
	// that the answer to "which commands are fenced" is a table a reader can
	// read and a test can walk, rather than a search (D2217).
	managedExemptAnnotation = "fugaro.io/managed-exempt"
)

// managedCommandKey is the context key root's pre-run files the running command
// under. A command that opens its cloud clients from a helper has no other way
// to be the one the fence is about: the helper's own frame knows the config it
// was handed and nothing about which command is running.
type managedCommandKey struct{}

// withManagedCommand is root's pre-run: the command the rest of this invocation
// is about, for every seam that has to ask which command it is serving.
func withManagedCommand(ctx context.Context, cmd *cobra.Command) context.Context {
	return context.WithValue(ctx, managedCommandKey{}, cmd)
}

// managedCommandFrom is the command the context was filed with, nil when
// nothing filed one: a helper called from a test or from a background goroutine
// that never went through root's pre-run is served by a context with no
// command, and the fence answers nothing about it.
func managedCommandFrom(ctx context.Context) *cobra.Command {
	c, _ := ctx.Value(managedCommandKey{}).(*cobra.Command)
	return c
}

// managedFenced is whether cmd is one of the commands the fence applies to.
// A nil command is not: nothing in the tree is being run, so there is nothing
// to refuse.
func managedFenced(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Annotations[managedRequiresAnnotation] != ""
}

// managedFlagCommand is the one command that makes an existing project managed.
// It is spelled plain, with nothing after it: the budget flags it requires are
// the owner's own decision, and the project it flags is the installation's own
// (D2213), so a sentence that printed an ID would be naming a project the
// command would not read.
func managedFlagCommand() string {
	return selfCommand() + " managed flag"
}

// managedRequired is the refusal of a command that may not write to a project
// that is not managed: the project, the managed read's own reason (so the
// refusal says which half is missing and not only that something is), what the
// command declines to do, and the one command that flags the project managed.
//
// cmd is the command refusing, and its annotation is the opening clause. A
// command that refuses this way without being declared in managedFencedCommands
// is a sentence with a hole in it rather than a wrong answer, so the refusal
// still holds and says what it is.
func managedRequired(cmd *cobra.Command, gcpProject, reason string) error {
	what := ""
	if cmd != nil {
		what = cmd.Annotations[managedRequiresAnnotation]
	}
	if what == "" {
		what = "nothing was written: this command changes a managed project"
	}
	return managedRequiredFrom(what, gcpProject, reason)
}

// managedRequiredFrom is managedRequired for a caller that states the opening
// clause itself: the converge's own stop (T3) refuses an installation that is
// not managed before any cloud client is opened, and the two commands that
// reach it — `fugaro update`, and `fugaro setup` on an installation that
// already exists — carry their reason on managedExemptCommands (T7a) rather
// than an opening clause, because the fence beside a cloud client is not what
// refuses them. Everything after the clause — the project, the read's own
// reason and the one command that flags an existing project managed — is this
// file's, so the refusal cannot come to say two different things.
func managedRequiredFrom(clause, gcpProject, reason string) error {
	return userErr("%s: %s is not managed (%s), and no flag of this command turns an installation that is not managed into one: the command that flags an existing project managed is `%s`, run by its owner at their own terminal (D1541)",
		clause, safetext.Field(gcpProject), safetext.Field(reason), managedFlagCommand())
}

// errNoCloudForManagedRead is the fence's answer on an installation that names
// no cloud to read the marks from at all: a test configuration (endpoints.no_auth
// with no Storage or Resource Manager endpoint), where there is no GCP project
// for a creation fact to be about. It is not a failed read — nothing was
// attempted — so it is not a warning either: configPublishGate and the doctor
// readers take the same configuration the same way.
var errNoCloudForManagedRead = errors.New("no storage or resource manager endpoint to read the marks from")

// requireManaged is the fence for a command that opens its cloud clients
// another way than openCloud and openCloudFor (T7b, T7c): it asks whether cmd
// is fenced, makes the managed read, and refuses through managedRequired when
// the read answers that the project is not managed.
//
// lc is the installation's own configuration and not a name, because the read
// needs three things off it — the project name, the GCP project and the
// endpoints the marks are read through — and a caller that passed a name would
// have to build the other two itself. It MUST be the installation's own trusted
// configuration: internal/infra/managed's warning about gcpProject is this
// argument's, and a caller that let a repository or a run choose it has handed
// the answer away.
//
// A read that fails WARNS and proceeds (D2218): the path an unmanaged project
// took before this plan, with its confirmations and its agent-session refusals,
// and never admission through the managed gate. A read that answers "not
// managed" stops. The warning goes to cmd's stderr and names the read that
// failed, because the owner's own terminal is where a fenced command's refusal
// is read — a coding agent's session is not a surface this line was written
// for, and it is not one that can act on it.
func requireManaged(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config) error {
	if !managedFenced(cmd) {
		return nil
	}
	ok, reason, err := readManagedFor(ctx, lc)
	switch {
	case err == nil && ok:
		return nil
	case err == nil:
		return managedRequired(cmd, lc.GCPProject, reason)
	case errors.Is(err, errNoCloudForManagedRead):
		return nil
	default:
		warnManagedUnreadable(cmd, lc, err)
		return nil
	}
}

// readManagedFor is the fence's read: the creation fact and the label of lc's
// GCP project, through the two clients the read needs and no others.
//
// The two clients are built here rather than through newInitClients because the
// read is two APIs' and the installation's whole client set is eight: a fence
// that demanded an IAM, an Artifact Registry and a Cloud Scheduler endpoint
// before it could ask whether a project is managed would fail on installations
// that are otherwise fine, and a test configuration would have to carry six
// fakes it has no use for. doctorAPIOpts is the same builder the other two
// narrow managed reads use (config_publish_managed.go, image_map_managed.go).
func readManagedFor(ctx context.Context, lc *localcfg.Config) (bool, string, error) {
	if lc.Endpoints.NoAuth && (lc.Endpoints.Storage == "" || lc.Endpoints.ResourceManager == "") {
		return false, "", errNoCloudForManagedRead
	}
	s, err := storage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Storage)...)
	if err != nil {
		return false, "", err
	}
	c, err := crm.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.ResourceManager)...)
	if err != nil {
		return false, "", err
	}
	return managed.IsManaged(ctx, s, c, lc.Name, lc.GCPProject)
}

// warnManagedUnreadable is D2218's warning: the marks could not be read, so
// this program cannot prove the project is managed, and the command goes on
// along the path an unmanaged project takes. What it may not do is treat the
// failure as an answer in the other direction: a read that failed cannot open
// the managed gate, so making the read fail buys nothing that the unmanaged
// path did not already allow.
func warnManagedUnreadable(cmd *cobra.Command, lc *localcfg.Config, err error) {
	w := io.Writer(os.Stderr)
	if cmd != nil {
		w = cmd.ErrOrStderr()
	}
	fmt.Fprintf(w, "fugaro: warning: could not read whether %s is managed (%s); going on as a project that is not managed\n",
		safetext.Field(lc.GCPProject), safetext.FieldErr(err))
}
