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

// embedProjectLayer puts the project layer that applies to the task's
// repository into spec (decisions L8, L9, L13). In the repository's
// checkout, it is the one findLayer finds for its fugaro.yaml. Outside one
// (fugaro run --repo), it is the installation's own, which the runner
// applies only to a fugaro.yaml at the ref that names it. It prints one
// line naming the layer, and one when the workflow's commands come from a
// profile (decision L7).
func embedProjectLayer(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	var data []byte
	if root := checkoutRoot(ctx, spec.Repo); root != "" {
		data, _ = readFugaroYAML(filepath.Join(root, "fugaro.yaml"))
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
	if cfg, res, ps := config.Resolve(data, l); len(ps) == 0 {
		if name, _, err := cfg.SelectWorkflow(spec.Workflow); err == nil {
			if src := res.SourceOf("workflows." + name + ".commands.test"); strings.HasPrefix(src, "profile ") {
				fmt.Fprintf(warn, "commands: from %s (project layer generation %d)\n", src, fl.Generation)
			}
		}
	}
	return nil
}
