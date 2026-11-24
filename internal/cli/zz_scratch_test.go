package cli

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func TestZZScratchFence(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, fenceVersion)
	fenceProjectConfig(t, fenceURL)
	root := cliRefRoot()
	var errOut bytes.Buffer
	root.SetOut(io.Discard)
	root.SetErr(&errOut)
	cmd, flags, _ := root.Find([]string{"runs", "ls"})
	t.Logf("root.ErrOrStderr()==&buf: %v", root.ErrOrStderr() == io.Writer(&errOut))
	t.Logf("cmd.ErrOrStderr()==&buf: %v", cmd.ErrOrStderr() == io.Writer(&errOut))
	t.Logf("cmd.ErrOrStderr()==io.Discard: %v", cmd.ErrOrStderr() == io.Discard)
	cmd.SetContext(context.Background())
	_ = cmd.ParseFlags(flags)
	err := root.PersistentPreRunE(cmd, flags)
	t.Logf("err=%v errOut=%q queries=%d", err, errOut.String(), len(rig.queries))
}
