package gcp

import (
	"regexp"
	"strings"
	"testing"
)

var (
	jobNameRE = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)
	saIDRE    = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
)

func TestNames(t *testing.T) {
	if got := JobName("acme-app", "web"); got != "fugaro-acme-app-web" {
		t.Errorf("JobName = %q", got)
	}
	if got := ServiceAccountID("acme-app", "web"); got != "fugaro-acme-app-web" {
		t.Errorf("ServiceAccountID = %q", got)
	}
	if got := SecretID("acme-app", "bitbucket-token"); got != "fugaro-acme-app-bitbucket-token" {
		t.Errorf("SecretID = %q", got)
	}
	if got := ImageName("us-east5-docker.pkg.dev/p/fugaro", "acme-my.app", "web"); got != "us-east5-docker.pkg.dev/p/fugaro/acme-my-app-web" {
		t.Errorf("ImageName = %q", got)
	}
	// Storage slugs may hold '.' and '_'; Cloud Run names may not.
	if got := JobName("acme-my.app_x", "web"); got != "fugaro-acme-my-app-x-web" {
		t.Errorf("JobName sanitizes to %q", got)
	}
}

func TestNamesFitLimitsWithAStableHash(t *testing.T) {
	long := "someorganization-" + strings.Repeat("verylongrepositoryname", 3)
	a, b := JobName(long, "web"), JobName(long, "api")
	if len(a) > 63 || !jobNameRE.MatchString(a) || a == b || a != JobName(long, "web") {
		t.Fatalf("JobName(long) = %q / %q", a, b)
	}
	sa := ServiceAccountID("acmecorp-fugarosandbox", "web") // 35 characters untruncated
	if len(sa) != 30 || !saIDRE.MatchString(sa) || !strings.HasPrefix(sa, "fugaro-acmecorp-fugaro") {
		t.Fatalf("ServiceAccountID = %q (%d)", sa, len(sa))
	}
	if sa == ServiceAccountID("acmecorp-fugarosandbox", "api") {
		t.Fatal("hash suffix does not separate workflows")
	}
}
