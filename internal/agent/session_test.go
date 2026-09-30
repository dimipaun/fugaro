package agent

import (
	"path/filepath"
	"testing"
)

func TestSessionPathMatchesClaude(t *testing.T) {
	home := "/home/fugaro"
	for _, tc := range []struct{ workDir, want string }{
		{"/work/repo", "-work-repo"},
		{"/a/b.c_d", "-a-b-c-d"},
		{"/x/Y9 z", "-x-Y9-z"},
	} {
		if got, want := SessionDir(home, tc.workDir), filepath.Join(home, ".claude", "projects", tc.want); got != want {
			t.Errorf("SessionDir(%q) = %q, want %q", tc.workDir, got, want)
		}
	}
}

func TestValidSessionID(t *testing.T) {
	for id, want := range map[string]bool{
		"3f2a9c1e-0000-4000-8000-000000000001":    true,
		NewSessionID():                            true,
		"3F2A9C1E-0000-4000-8000-000000000001":    false,
		"3f2a9c1e-0000-4000-8000-00000000001":     false,
		"3f2a9c1e00000-4000-8000-000000000001":    false,
		"../../x":                                 false,
		"":                                        false,
		"3f2a9c1e-0000-4000-8000-000000000001\n":  false,
		"3f2a9c1e-0000-4000-8000-000000000001/..": false,
	} {
		if got := ValidSessionID(id); got != want {
			t.Errorf("ValidSessionID(%q) = %v, want %v", id, got, want)
		}
	}
}
