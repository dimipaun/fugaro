package cli

import (
	"regexp"
	"slices"
	"testing"

	infraTF "github.com/dimipaun/fugaro/internal/infra/tf"
)

// The public mobile service's argv is the one fact of the installation module
// that a plan cannot check: the module starts a container with
// `credentials serve --mobile` (mobile plan task P5; D2782 as amended by F31,
// O8), and whether this binary RUNS that command is a fact of internal/cli's
// own tree — cobra answers a flag it does not define with `unknown flag:
// --mobile`, so the revision never becomes ready and the apply that created it
// fails, on an installation whose runs are doing nothing wrong. The mode
// belongs to another plan task (I1), so this release must not start it: the
// module's switch (var.enable_mobile_service, and its root's own) is OFF, and
// this test is what keeps the two facts in step in BOTH directions.
//
// Three sides, one fact. The argv is read from internal/infra/tf, which is what
// the cover holds every plan of this service to (mobileServiceArgs), and from
// the module's own container block — two languages, one string, which nothing
// but a comparison can hold together (the shape
// TestCredentialsServiceEnvIsTheDeploymentsOwn uses for the seven environment
// names). The switch's default is read from the module's variable, because a
// default is what an installation that writes no such key gets, and that is
// every installation: `fugaro update` writes no key for it (the module's
// comment on the variable, and internal/infra's test over what the binary
// writes). The command tree is the REAL one — NewRootCmd, not a command built
// for a test — because what the container runs is the built binary.
//
// What the test says when it fails, and to whom. A release that lands the mode
// (I1) reads it as "the switch is still off and you have the flag: turn it on
// in both roots"; a release that turns the switch on without the flag reads
// "the binary does not run this argv yet". Either way the sentence is the fix,
// and neither release can be shipped past it.
//
// Mutation (verified, run and restored): removing the args line from
// mobile_service.tf fails the first comparison; `default = true` on the
// module's variable fails the second; adding `--mobile` to
// newCredentialsServeCmd makes the tree run the argv with the switch still off
// and fails the same one.
func TestMobileServiceIsOffUntilThisBinaryRunsIt(t *testing.T) {
	const module = "deploy/terraform/gcp/modules/installation/mobile_service.tf"
	src := readRepoFile(t, module)
	args := infraTF.MobileServiceArgs()

	// The module's own container block, and not a sentence of its header: the
	// argv the plan carries is what the platform starts.
	literal := regexp.MustCompile(`args\s*=\s*\[([^\]]*)\]`).FindStringSubmatch(src)
	if literal == nil {
		t.Fatalf("%s writes no args for the mobile service's container: without them the image's default command serves the credentials routes at a public URL, which is the outcome F31 exists to prevent — and this test cannot read the argv the plan carries", module)
	}
	var inModule []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(literal[1], -1) {
		inModule = append(inModule, m[1])
	}
	if !slices.Equal(inModule, args) {
		t.Errorf("%s starts the container with %v, internal/infra/tf's MobileServiceArgs is %v: the cover refuses any other argv on this service (mobileServiceChange), so a drift here is a plan nobody would apply", module, inModule, args)
	}

	// The command tree the image's own binary has, and the one question this
	// test asked it: does `credentials serve` know the mode?
	if len(args) < 3 || args[1] != "serve" {
		t.Fatalf("the mobile service's argv is %v, which is not `credentials serve <flag>`: this test reads the subcommand and the flag off it", args)
	}
	serve := findSub(t, findSub(t, NewRootCmd(), args[0]), args[1])
	runnable := serve.ParseFlags(args[2:]) == nil

	// ... and the switch that decides whether anything starts it. The module's
	// default, and the root's, because a tfvars key is what an operator's own
	// file carries and the module's variable is what it is passed into.
	on := variableBoolDefault(t, "deploy/terraform/gcp/modules/installation/variables.tf", "enable_mobile_service")
	if rootOn := variableBoolDefault(t, "deploy/terraform/gcp/roots/installation/variables.tf", "enable_mobile_service"); rootOn != on {
		t.Errorf("the installation root's enable_mobile_service defaults to %v and the module's to %v: the root passes its own value through (roots/installation/main.tf), so the two defaults are one switch and a second spelling is an installation whose tfvars say one thing and whose module does another", rootOn, on)
	}
	if runnable == on {
		return
	}
	if on {
		t.Errorf("the module starts the mobile service with %v, and this binary's `%s %s` does not run it (ParseFlags: unknown flag): every installation that turned the switch on would get a revision that never becomes ready, and `fugaro update` would fail on the service it had just created — land the mode (internal/cli/credentials_serve.go, I1) before the switch, or leave it off", args, args[0], args[1])
		return
	}
	t.Errorf("this binary's `%s %s` runs %v, and the module's enable_mobile_service still defaults to false: the mode has landed, so turn the switch on in both roots — and hand the runtime account to the Firebase root as mobile_account in the same change (modules/firebase/iam.tf's mobile_token_minter), because the signJwt is written where the signer is created", args[0], args[1], args[2:])
}

// variableBoolDefault is a root's or a module's default for a bool variable,
// read off the file: a default is the value every installation that writes no
// such key gets, which is the whole of what this test needs to know about a
// switch the CLI has no writer for yet. It fails when the variable is not
// there, because a test that read "false" out of a missing block would pass on
// a tree where the switch had never been written.
func variableBoolDefault(t *testing.T, path, name string) bool {
	t.Helper()
	src := readRepoFile(t, path)
	block := regexp.MustCompile(`(?s)variable "` + name + `" \{(.*?)\n\}`).FindStringSubmatch(src)
	if block == nil {
		t.Fatalf("%s declares no variable %q: this test reads that variable's default, and a tree without it is one where the mobile service has no switch at all", path, name)
	}
	m := regexp.MustCompile(`(?m)^\s*default\s*=\s*(true|false)\s*$`).FindStringSubmatch(block[1])
	if m == nil {
		t.Fatalf("%s's variable %q has no bool default: an installation that writes no key gets the module's own default, and a variable without one is required", path, name)
	}
	return m[1] == "true"
}
