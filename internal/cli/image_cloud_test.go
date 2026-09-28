package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// The Cloud Build clone authenticates with the Bitbucket token, so an
// origin on any host but bitbucket.org is refused before anything is
// submitted: the token would go to that host.
func TestImageBuildCloudPinsTheProviderHost(t *testing.T) {
	for _, origin := range []string{
		"https://bitbucket.example.com/acme/app.git",
		"https://github.com/acme/app.git",
		"https://bitbucket.org:8443/acme/app.git",
	} {
		t.Run(origin, func(t *testing.T) {
			fb, _ := cloudBuildCheckout(t, false)
			testutil.Git(t, ".", "remote", "set-url", "origin", origin)
			_, _, err := execute(t, "image", "build", "--base", "b:1")
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "bitbucket") {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
			if len(fb.Requests()) != 0 {
				t.Error("a build cloning from another host reached Cloud Build")
			}
		})
	}
}
