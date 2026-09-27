package image

import (
	"strings"
	"testing"
)

func TestHTTPSOrigin(t *testing.T) {
	cases := map[string]string{
		"git@bitbucket.org:team/repo.git":                         "https://bitbucket.org/team/repo.git",
		"bitbucket.org:team/repo.git":                             "https://bitbucket.org/team/repo.git",
		"ssh://git@github.com/acme/app.git":                       "https://github.com/acme/app.git",
		"ssh://git@github.com:22/acme/app.git":                    "https://github.com:22/acme/app.git",
		"https://x-access-token:s3cr3t@github.com/acme/app.git":   "https://github.com/acme/app.git",
		"https://github.com/acme/app.git":                         "https://github.com/acme/app.git",
		"http://github.com/acme/app.git":                          "https://github.com/acme/app.git",
		"ssh://git@git.example.invalid:2222/team/repo.git":        "https://git.example.invalid:2222/team/repo.git",
		"https://user:tok@git.example.invalid:8443/team/repo.git": "https://git.example.invalid:8443/team/repo.git",
		"/srv/git/app.git":                                        "/srv/git/app.git",
		"file:///srv/git/app.git":                                 "file:///srv/git/app.git",
	}
	for in, want := range cases {
		if got := HTTPSOrigin(in); got != want {
			t.Errorf("HTTPSOrigin(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOriginProblem(t *testing.T) {
	for _, ok := range []string{"https://github.com/acme/app.git", "https://bitbucket.org/team/repo.git", "/srv/git/app.git", "file:///srv/git/app.git"} {
		if p := originProblem(ok); p != "" {
			t.Errorf("originProblem(%q) = %q, want none", ok, p)
		}
	}
	bad := map[string]string{
		"": "no origin remote",
		"https://x-access-token:s3cr3t@github.com/acme/app.git": "embeds credentials",
		"http://github.com/acme/app.git":                        "must be https, not http",
		"ssh://git@github.com/acme/app.git":                     "must be https, not ssh",
		"git@bitbucket.org:team/repo.git":                       "not an SSH remote",
		"../app.git":                                            "must be an https URL",
	}
	for in, want := range bad {
		got := originProblem(in)
		if !strings.Contains(got, want) || strings.Contains(got, "s3cr3t") {
			t.Errorf("originProblem(%q) = %q, want %q (and never the token)", in, got, want)
		}
	}
}
