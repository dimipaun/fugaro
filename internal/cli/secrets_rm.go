package cli

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is `fugaro secrets rm NAME`: the withdrawal D1171 gives a project
// secret. It deletes the Secret Manager container with every version in it and
// the local config's project_secrets.NAME entry that made the value reachable
// project-wide — the pair nothing could remove before it, which left a
// withdrawn value's policy standing and a policy's value unreachable.
//
// The config half is the settings commands' one write path (applySettingsEdit),
// so the diff, the confirmation, the backup and the compare-and-swap are the
// same ones every other settings write pays. The Secret Manager half is not
// something that path knows about, so the coding agent's session refusal is made
// here, before either half runs: a session that reached the deletion and was
// then refused the config write would have destroyed the value and left the
// entry that promises it (D1181, spec §5 — this family is refused in a session,
// gate or not).

// secretsRmOptions are the flags `fugaro secrets rm` takes. It takes no
// --repo: the entry this command removes is the project_secrets one, which only
// a project-wide copy has, so the copy it deletes is the project's own. A
// repository's own copy is the repository's, and `secrets ls --repo` names it.
type secretsRmOptions struct {
	cloud  cloudOptions
	yes    bool
	dryRun bool
	asJSON bool
}

// newSecretsRmCmd is `fugaro secrets rm NAME`.
func newSecretsRmCmd() *cobra.Command {
	var o secretsRmOptions
	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Withdraw a project secret: delete its Secret Manager container and its config entry",
		Long: `Delete the project-wide secret NAME and the local config entry that makes it
reachable.

NAME is stored once for the whole Fugaro project (project_secrets.NAME), and
this is the command that takes it back: it deletes the Secret Manager
container, with every version in it, and removes project_secrets.NAME from
your local config in the same run, behind one confirmation and one diff.

Both halves go, and neither is left behind: an entry whose container is gone
promises a value no job can mount, and a container with no entry is a value
the project still holds after its owner asked for it to be withdrawn.

Deleting the container revokes nothing retroactively. A repository whose own
copy has an enabled version keeps using it, and a job already deployed keeps
the mount it was applied with until its next update — the command says which
repositories of this project hold their own copy.

This command never prints a value: the entry it removes holds an allow-list
and a switch, and a value is not a setting and is never in a diff.

Run it at your own terminal: a removal of a secret is a value no plan shows,
so a coding agent's session is refused, --yes included.`,
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return userErr("secrets rm needs the secret's NAME")
			case len(args) > 1:
				return userErr("secrets rm takes one NAME")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return secretsRm(cmd, o, args[0])
		},
	}
	addCloudFlags(cmd, &o.cloud)
	f := cmd.Flags()
	f.BoolVar(&o.yes, "yes", false, "confirm the removal without a prompt")
	f.BoolVar(&o.dryRun, "dry-run", false, "print what the removal would do and write nothing")
	f.BoolVar(&o.asJSON, "json", false, "print the dry run's document as JSON (with --dry-run)")
	return cmd
}

// secretsRmCommand is the command a refusal in a coding agent's session prints
// for the person to run at their own terminal.
func secretsRmCommand(name string) string {
	return selfCommand() + " secrets rm " + quoteWord(name)
}

func secretsRm(cmd *cobra.Command, o secretsRmOptions, name string) error {
	if _, reserved := config.ReservedSecrets[name]; !reserved && !config.SecretNameRE.MatchString(name) {
		// The name is not quoted: a value typed in its place must not be echoed.
		return userErr("the secret NAME must be one of %s, or a workflow secret's name matching %s",
			strings.Join(slices.Sorted(maps.Keys(config.ReservedSecrets)), ", "), config.SecretNameRE)
	}
	if err := settingsDryRunFlags(settingsWriteOptions{dryRun: o.dryRun, jsonOut: o.asJSON}); err != nil {
		return err
	}
	// The whole family is refused in a coding agent's session (spec §5), and
	// here it has to come before anything else: the Secret Manager deletion is
	// not the settings write path's, so the fence that path carries would
	// arrive after the value was already gone.
	command := secretsRmCommand(name)
	if marker := agentMarker(os.Getenv); marker != "" {
		return userErr("refusing to remove %s in a coding agent's session (%s is set): removing it deletes its Secret Manager container and every version in it, which no plan shows and no dry run brings back; run this yourself at your own terminal: %s; nothing was removed",
			pluginwire.Printable(name), marker, command)
	}

	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	id := gcp.ProjectSecretID(lc.Name, name)
	sm, err := gcp.NewSecrets(ctx, env.gcp)
	if err != nil {
		return remote(err)
	}
	// The container is looked up rather than deleted blind: a delete of a
	// derived ID that carries no Fugaro label would take a secret this program
	// never made, and an ID that is not there is a state to report rather than
	// an error to hand back.
	list, err := sm.List(ctx, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelSecret: name})
	if err != nil {
		return remote(err)
	}
	held := slices.ContainsFunc(list, func(info gcp.SecretInfo) bool { return info.ID == id })

	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	// DropEmptyParents, so the entry's removal is the whole of it: a
	// project_secrets block this command empties is a block the file no longer
	// needs, and the entry was the last thing it held.
	next, err := yamledit.RemoveKeyWithOptions(old, []string{"project_secrets", name}, yamledit.RemoveOptions{DropEmptyParents: true})
	if err != nil {
		return userErr("project_secrets.%s: %v", name, err)
	}
	changed := yamledit.Changed(old, next)
	if changed {
		// A file this Fugaro cannot read back is a file the next command of the
		// owner's would refuse, and the refusal belongs here, with nothing
		// deleted.
		if _, err := localcfg.Parse(next); err != nil {
			return userErr("the local config this would write does not read back: %v", err)
		}
	}
	if !held && !changed {
		return userErr("nothing to remove: no Fugaro-managed secret %s exists in %s and this local config has no project_secrets.%s entry",
			id, pluginwire.Printable(lc.Name), name)
	}

	what := secretsRmWhat(id, name, held, changed)
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    what,
		Kind:    settingsDocLocal,
		Command: command,
		Yes:     o.yes,
		DryRun:  secretsRmDryRun(o, name, old),
	}); err != nil {
		return err
	}
	if o.dryRun {
		// The settings document describes the config half alone, so the half it
		// cannot carry is said here rather than left for the person to find in
		// the command's own help.
		if held {
			fmt.Fprintf(cmd.ErrOrStderr(), "dry run: %s would be deleted, with every version in it\n", id)
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "dry run: no Fugaro-managed secret %s exists; nothing would be deleted there\n", id)
		}
		return nil
	}
	if !changed {
		// applySettingsEdit returns before its confirmation when there is
		// nothing to write, and the deletion below is the change this command
		// is about: it is confirmed here, once, for the same reason and in the
		// same words.
		if err := confirmSettingsWrite(cmd, lc, sel.Path, o.yes, what); err != nil {
			return err
		}
	}
	if held {
		if err := sm.Delete(ctx, id); err != nil {
			return remote(err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", id)
	}
	// A repository copy with an enabled version wins over the project copy, so
	// the project copy's removal does not reach those repositories: said, in
	// the words `secrets set --project-wide` already uses for the same fact.
	if repos, err := repoCopiesWithVersions(ctx, env, name); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"note: whether a repository's own copy of %s exists could not be read (%s); a repository copy with an enabled version is not affected by this removal\n",
			pluginwire.Printable(name), oneLine(err.Error()))
	} else if len(repos) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"note: %s still has its own copy with an enabled version in %s, and a repository copy wins over the project copy: those repositories keep the value their own copy holds (`%s secrets ls --repo <repository>` names it)\n",
			pluginwire.Printable(name), pluginwire.Printable(strings.Join(repos, ", ")), selfCommand())
	}
	return nil
}

// secretsRmWhat is the sentence the confirmation shows. It names both halves
// this command removes, because the diff below it shows only one of them.
func secretsRmWhat(id, name string, held, changed bool) string {
	switch {
	case held && changed:
		return fmt.Sprintf("delete the Secret Manager container %s with every version in it, and remove project_secrets.%s from this local config", id, name)
	case held:
		return fmt.Sprintf("delete the Secret Manager container %s with every version in it (this local config has no project_secrets.%s entry)", id, name)
	default:
		return fmt.Sprintf("remove project_secrets.%s from this local config (no Secret Manager container %s exists)", name, id)
	}
}

// secretsRmDryRun is the classification a --dry-run answers with. The document
// is the settings write path's own — key, layer, path, changed, old and new are
// the config half it describes — and operation is this command's own
// "remove-secret": what the run takes away is the whole project_secrets.NAME
// entry *and* the Secret Manager container behind it, which is more than the
// entry pair's one member of a block and is not what "remove-entry" means. The
// container is named on stderr as well, because a document whose members are
// the settings write contract carries no member for it.
func secretsRmDryRun(o secretsRmOptions, name string, old []byte) *settingsDryRun {
	if !o.dryRun {
		return nil
	}
	return &settingsDryRun{
		key:       "project_secrets." + name,
		layer:     settingsLayerLocal,
		oldValue:  secretsEntryFields(old, name),
		newValue:  "",
		operation: "remove-secret",
		jsonOut:   o.asJSON,
	}
}

// secretsEntryFields renders project_secrets.NAME's own members the one way the
// dry run's old and new carry an entry: one field=value pair each, sorted by
// field and comma-separated. A value has no member here and never has — the
// entry holds an allow-list and a switch, and this is the whole of it.
func secretsEntryFields(data []byte, name string) string {
	lc, err := localcfg.Parse(data)
	if err != nil {
		return ""
	}
	entry, ok := lc.ProjectSecrets[name]
	if !ok {
		return ""
	}
	var pairs []string
	if len(entry.AllowDataTo) > 0 {
		pairs = append(pairs, "allow_data_to="+strings.Join(entry.AllowDataTo, ", "))
	}
	if entry.Proxy {
		pairs = append(pairs, "proxy=true")
	}
	return strings.Join(pairs, ", ")
}
