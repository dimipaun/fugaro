package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The GitHub App pre-check (live Check 27): a run or an image build fails
// only when it first asks GitHub for a token, long after init, if the App is
// not installed on the repository or the installation lacks a permission
// Fugaro's tokens ask for (a Cloud Build billed, then refused). Before the
// first billable build, and in fugaro doctor, the App's own JWT asks GitHub
// what the installation grants (github.CheckInstallation) and compares it
// with what the tokens request, derived from the code that requests them.
//
// The CLI holds no provider credential (docs/git-providers.md), so the check
// needs the App's private key only in two ways, both in memory, both used only
// to sign the JWT, never printed, logged, put in an argument or an error:
//   - in init, the key the user typed at THIS run's secrets stage (appKeyMem),
//     with no Secret Manager read;
//   - behind the explicit flag --check-github-app (init and doctor), which
//     reads it from Secret Manager with the person's own credentials
//     (gcp.Secrets.Access) after a one-line notice, and is refused under a
//     coding agent's marker, so the value never reaches an agent's process.
// Without either, the check is "not made", never "fine".

// githubAPIURL is the GitHub REST root the pre-check asks; tests replace it.
var githubAPIURL = github.DefaultBaseURL

// githubAPIClient is the HTTP client of the pre-check (nil: the default one,
// with a timeout). Tests replace it.
var githubAPIClient *http.Client

// readAppKey reads the PEM of the GitHub App's private key from the secret.
// Only behind --check-github-app. Tests replace it.
var readAppKey = func(ctx context.Context, lc *localcfg.Config, secretID string) ([]byte, error) {
	sm, err := gcp.NewSecrets(ctx, gcpOptions(lc))
	if err != nil {
		return nil, err
	}
	return sm.Access(ctx, secretID)
}

// checkGitHubAppFlag is the flag that allows the key's Secret Manager read.
const checkGitHubAppFlag = "--check-github-app"

// refuseAppKeyReadInAgent is the refusal of the flag under a coding agent's
// environment, before anything is read.
func refuseAppKeyReadInAgent(getenv func(string) string) error {
	if agentEnv(getenv) {
		return userErr("%s reads the GitHub App's private key with your own credentials, so it is refused in a coding agent's session (the value must never reach an agent's process): run it in your own terminal window", checkGitHubAppFlag)
	}
	return nil
}

// appKeySource says where the check may get the App's key.
type appKeySource struct {
	// Mem is the key the user typed in this run, if still held; the check
	// copies it and does not clear it (its holder does).
	Mem []byte
	// Read allows reading it from Secret Manager (--check-github-app);
	// SecretID names the secret; Notice receives the one-line notice first.
	Read     bool
	SecretID string
	Notice   io.Writer
}

// appVerdict is how the check ended.
type appVerdict int

const (
	appFine    appVerdict = iota // installed, with every permission Fugaro asks for
	appFailed                    // installed wrongly: Problem and Fix say how
	appUnknown                   // not checked: Problem says why; nothing is claimed about the App
)

// appCheck is the pre-check's result.
type appCheck struct {
	Verdict appVerdict
	// Problem is one line: what is wrong, or why the check was not made. Fix is
	// the one-line remedy (empty when there is none to give).
	Problem, Fix string
	// Warnings are about an App that works but holds more than Fugaro should
	// have (Workflows: Write above all).
	Warnings []string
}

// checkGitHubApp asks GitHub whether the App appID is installed on repo
// (owner/name) with the permissions Fugaro's tokens ask for, with the key from
// src.
func checkGitHubApp(ctx context.Context, lc *localcfg.Config, repo, appID string, src appKeySource) appCheck {
	owner, name, ok := gitprov.SplitRepo(repo)
	if !ok || appID == "" {
		return appCheck{Verdict: appUnknown, Problem: "the GitHub App's installation was not checked: no App ID or repository to check it for"}
	}
	var pem []byte
	switch {
	case len(src.Mem) > 0:
		pem = append([]byte(nil), src.Mem...)
	case src.Read:
		if src.Notice != nil {
			fmt.Fprintln(src.Notice, "reads the App's private key with your own credentials, holds it in memory only")
		}
		var err error
		pem, err = readAppKey(ctx, lc, src.SecretID)
		switch {
		case errors.Is(err, gcp.ErrNoVersion):
			return appCheck{Verdict: appUnknown, Problem: "the GitHub App's installation on " + repo + " was not checked: its private key is not stored yet (the github-app-key secret has no version)"}
		case errors.Is(err, gcp.ErrNoAccess):
			return appCheck{Verdict: appUnknown, Problem: "the GitHub App's installation on " + repo + " was not checked: you may not read secret values (a project owner can), and the check signs with the App's key"}
		case err != nil:
			return appCheck{Verdict: appUnknown, Problem: "the GitHub App's installation on " + repo + " was not checked: the App's key could not be read: " + oneLineCLI(err.Error())}
		}
	default:
		return appCheck{Verdict: appUnknown, Problem: "the GitHub App's installation on " + repo + " was not checked: rerun with " + checkGitHubAppFlag + " (reads the key with your own credentials) or run " + selfCommand() + " doctor " + checkGitHubAppFlag + " in your own terminal"}
	}
	key, err := github.ParsePrivateKey(pem)
	clear(pem)
	if err != nil {
		// The message of a parse error never holds key material.
		return appCheck{Verdict: appFailed, Problem: "the private key stored for the GitHub App is not a PEM RSA private key (" + oneLineCLI(err.Error()) + ")",
			Fix: "store the App's .pem again: " + selfCommand() + " secrets set github-app-key --repo " + quoteWord(repo) + " < PATH-TO-THE-PEM-FILE"}
	}
	rep, err := github.CheckInstallation(ctx, github.Options{Owner: owner, Repo: name, AppID: appID, PrivateKey: key, BaseURL: githubAPIURL, HTTP: githubAPIClient})
	var ni *github.NotInstalledError
	var bad *github.BadCredentialsError
	switch {
	case errors.As(err, &ni):
		return appCheck{Verdict: appFailed, Problem: ni.Problem(), Fix: ni.Fix()}
	case errors.As(err, &bad):
		return appCheck{Verdict: appFailed, Problem: bad.Error(), Fix: "store the right App's .pem: " + selfCommand() + " secrets set github-app-key --repo " + quoteWord(repo) + " < PATH-TO-THE-PEM-FILE"}
	case err != nil:
		return appCheck{Verdict: appUnknown, Problem: "the GitHub App's installation on " + repo + " was not checked (GitHub could not be asked: " + oneLineCLI(err.Error()) + "); this does not say the App is fine"}
	}
	res := appCheck{Verdict: appFine}
	for _, f := range rep.Forbidden {
		res.Warnings = append(res.Warnings, fmt.Sprintf("the GitHub App %s holds %s on %s: Fugaro must never have it (a run could rewrite the repository's workflows, which run with its secrets); remove it in the App's permissions", appID, f, repo))
	}
	if len(rep.Extra) > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("the GitHub App %s also holds %s on %s, which Fugaro's tokens never ask for; remove them for least privilege", appID, strings.Join(rep.Extra, ", "), repo))
	}
	if len(rep.Missing) > 0 {
		var have, fixes []string
		for _, g := range rep.Missing {
			have = append(have, g.Display())
			fixes = append(fixes, g.Fix())
		}
		res.Verdict = appFailed
		res.Problem = fmt.Sprintf("the installation of the GitHub App %s on %s lacks %s", appID, repo, strings.Join(have, ", "))
		res.Fix = strings.Join(fixes, "; ")
	}
	return res
}
