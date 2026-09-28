package config

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// TestProvidersAreTheGitprovKinds keeps config.Providers in step with the
// provider kinds gitprov implements.
func TestProvidersAreTheGitprovKinds(t *testing.T) {
	want := []string{gitprov.KindGitHub, gitprov.KindBitbucket}
	if got := slices.Sorted(slices.Values(Providers)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Fatalf("config.Providers = %v, gitprov kinds = %v", Providers, want)
	}
}
