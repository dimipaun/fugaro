package infra

import "testing"

// TestForgetAddressesExist checks every address the rollback may delete
// against the embedded installation module: ForgetDeletes, and an instance
// of the log view's grants (one per launcher or operator, keyed by member).
func TestForgetAddressesExist(t *testing.T) {
	addrs := append([]string{}, ForgetDeletes...)
	addrs = append(addrs, forgetViewGrants+`["user:launcher@example.com"]`)
	for _, a := range addrs {
		checkModuleAddress(t, a)
	}
}
