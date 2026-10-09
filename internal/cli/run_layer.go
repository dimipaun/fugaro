package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

// layerAnnounceKeys are config.ExecutableKeys' run-time keys: what the
// launched job itself executes, in its own container. image.apt and
// image.setup are the catalog's other executable keys, but they are
// image-time, not run-time: per docs/design/layered-config.md §11 (decision
// L7, option A, the row this release ships), the line fugaro run prints is
// `commands: from profile <p> (project layer gen <n>)` — commands only.
// image.apt/image.setup run later, in Cloud Build, the next time the
// image is rebuilt (fugaro image build/refresh or the daily check job),
// never as part of this launch, so a launch never announces them.
var layerAnnounceKeys = []string{"commands.build", "commands.test", "commands.rerun_failed"}

// embedProjectLayer puts the project layer that applies to the task's
// repository into spec (decisions L8, L9, L13). In the repository's
// checkout, it is the one findLayer finds for its fugaro.yaml. Outside one
// (fugaro run --repo, and every follow-up launch, which never has a
// checkout of its own either: decision L15, it already re-reads the base
// branch's file, so it resolves against today's project), it is the
// installation's own, which the runner applies only to a fugaro.yaml at the
// ref that names it. Once a project layer is published, every such launch
// therefore carries project_layer and is gated by layeredSince, even for a
// repository whose own fugaro.yaml has no gcp_project: (or could not be
// read here): the installation's own project/gcp_project anchors it
// regardless (design §10's rollout order assumes this). It prints one line
// naming the layer, and one per profile a workflow's commands came from
// (decision L7).
func embedProjectLayer(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	var data []byte
	if root := checkoutRoot(ctx, spec.Repo); root != "" {
		var rerr error
		if data, rerr = readFugaroYAML(filepath.Join(root, "fugaro.yaml")); rerr != nil {
			fmt.Fprintf(warn, "note: could not read %s (%s); resolving the project layer against the installation's own project instead\n",
				filepath.Join(root, "fugaro.yaml"), oneLineCLI(rerr.Error()))
			data = nil
		}
	}
	if data == nil {
		data = []byte(fmt.Sprintf("version: 1\nproject: %s\ngcp_project: %s\n", env.lc.Name, env.lc.GCPProject))
	}
	fl, err := findLayer(ctx, os.Getenv, data, env.lc, layerOptions{}, time.Now())
	if err != nil {
		return err
	}
	l := fl.Layer
	if l == nil {
		return nil // why none applies is config show's and doctor's to say, not every launch's
	}
	if fl.Note != "" {
		fmt.Fprintln(warn, "note: "+fl.Note) // a cached copy stood in
	}
	spec.ProjectLayer = &task.ProjectLayer{SHA256: l.SHA256, Generation: fl.Generation, YAML: string(l.Raw)}
	fmt.Fprintf(warn, "project layer: %s generation %d (sha256 %s)\n", l.Project, fl.Generation, l.SHA256[:12])
	announceProfileCommands(warn, data, l, spec.Workflow, fl.Generation)
	return nil
}

// announceProfileCommands prints the L7 launch line for every profile that
// one of the launched workflow's run-time keys (layerAnnounceKeys) came
// from: one line per profile, naming which of its keys apply, so a launcher
// sees exactly which shell commands run from the bucket. When the
// checkout's fugaro.yaml can't be resolved locally against the layer (ps
// non-empty: data and l disagree on something Resolve checks, or the
// workflow can't be selected), the task still carries the layer
// (embedProjectLayer already set spec.ProjectLayer before calling this), so
// this prints a note instead of silently announcing nothing: the runner
// resolves it for real at launch regardless of what could be told here.
func announceProfileCommands(warn io.Writer, data []byte, l *config.ProjectLayer, workflow string, generation int64) {
	note := func(why string) {
		fmt.Fprintf(warn, "note: could not tell locally which profile workflow %s would use (%s); project layer %s generation %d still applies, and the runner resolves it at launch\n",
			workflow, why, l.Project, generation)
	}
	cfg, res, ps := config.Resolve(data, l)
	if len(ps) != 0 {
		note(oneLineCLI(layerProblemsText(ps)))
		return
	}
	name, _, err := cfg.SelectWorkflow(workflow)
	if err != nil {
		note(oneLineCLI(err.Error()))
		return
	}
	var order []string
	keys := map[string][]string{}
	for _, key := range layerAnnounceKeys {
		src := res.SourceOf("workflows." + name + "." + key)
		if !strings.HasPrefix(src, "profile ") {
			continue
		}
		if _, ok := keys[src]; !ok {
			order = append(order, src)
		}
		keys[src] = append(keys[src], strings.TrimPrefix(key, "commands."))
	}
	for _, src := range order {
		fmt.Fprintf(warn, "commands: from %s (%s; project layer generation %d)\n", src, strings.Join(keys[src], ", "), generation)
	}
}
