package lift

import "testing"

// TestRecordPath pins the one name update writes a migration record at and
// the launcher reads it from (D3305, D3308): a move of it on either side
// without the other would leave a repository migrated and unread.
func TestRecordPath(t *testing.T) {
	if got, want := RecordPath("acme-app-0123456789abcdef"), "fugaro/lift/l8/acme-app-0123456789abcdef.json"; got != want {
		t.Errorf("RecordPath() = %q, want %q", got, want)
	}
}
