package github

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/task"
)

// What the GitHub App must grant (design §6.1) is what Fugaro's tokens ask
// for: a run's (tokenPermissions) and an image build's (buildTokenPermissions).
// An installation that lacks one fails a run or a build only when it first
// asks, long after init; CheckInstallation reads what the installation grants
// so init and doctor can say so first.

// permissionLevels orders a permission's levels.
var permissionLevels = map[string]int{"": 0, "read": 1, "write": 2, "admin": 3}

// RequiredPermissions is the least the installation must grant: the union of
// what a run's token and an image build's token ask for, the higher level where
// both name a permission. A copy the caller may keep.
func RequiredPermissions() map[string]string {
	need := map[string]string{}
	for _, perms := range []map[string]string{tokenPermissions, buildTokenPermissions} {
		for name, level := range perms {
			if permissionLevels[level] > permissionLevels[need[name]] {
				need[name] = level
			}
		}
	}
	return need
}

// forbiddenPermissions are permissions Fugaro must never be granted at the
// level named or above: a run could rewrite the workflows of its own
// repository, which run with the repository's secrets.
var forbiddenPermissions = map[string]string{"workflows": "write"}

// displayNames are the names GitHub's App settings page shows.
var displayNames = map[string]string{
	"contents": "Contents", "pull_requests": "Pull requests", "issues": "Issues", "metadata": "Metadata",
	"workflows": "Workflows", "administration": "Administration", "actions": "Actions", "checks": "Checks",
	"statuses": "Commit statuses", "deployments": "Deployments", "packages": "Packages", "pages": "Pages",
	"secrets": "Secrets", "environments": "Environments", "members": "Members",
}

func displayName(api string) string {
	if n, ok := displayNames[api]; ok {
		return n
	}
	s := strings.ReplaceAll(api, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func displayLevel(level string) string {
	switch level {
	case "read":
		return "Read"
	case "write":
		return "Write"
	case "admin":
		return "Admin"
	}
	return level
}

// PermissionGap is a permission the installation lacks or grants too low.
type PermissionGap struct {
	Name string // the API's name (issues, pull_requests)
	Want string // read or write
	Have string // what the installation grants; "" for none
}

// Display is the gap as the App settings page names it: "Issues: Read".
func (g PermissionGap) Display() string { return displayName(g.Name) + ": " + displayLevel(g.Want) }

// Fix is the exact change: add it (or raise it), then accept the new
// permission on the installation (GitHub holds the new permission back until
// the installation's owner approves it).
func (g PermissionGap) Fix() string {
	accept := ", then accept the new permission on the installation"
	if g.Have == "" {
		return "add " + g.Display() + accept
	}
	return "change " + displayName(g.Name) + " to " + map[string]string{"write": "Read and write", "admin": "Admin"}[g.Want] + accept
}

// InstallationReport is what the installation of the App on the repository
// grants, against what Fugaro asks.
type InstallationReport struct {
	InstallationID int64
	// Missing are the permissions the installation lacks or grants too low,
	// sorted by name.
	Missing []PermissionGap
	// Forbidden are permissions it holds that Fugaro must never have
	// ("Workflows: Write").
	Forbidden []string
	// Extra are other permissions it holds beyond what Fugaro asks: unused (a
	// run's token never asks for them), kept to least privilege by removing
	// them. Display names with their level, sorted.
	Extra []string
}

// NotInstalledError is the App not being installed on the repository (the
// installation lookup answered 404).
type NotInstalledError struct{ AppID, Repo string }

func (e *NotInstalledError) Error() string { return e.Problem() + ": " + e.Fix() }

// Problem is what is wrong, Fix the one-line remedy.
func (e *NotInstalledError) Problem() string {
	return fmt.Sprintf("the GitHub App %s is not installed on %s", e.AppID, e.Repo)
}
func (e *NotInstalledError) Fix() string {
	return "install it on this repository only (Settings, GitHub Apps, Install App)"
}

// BadCredentialsError is GitHub refusing the App's own JWT: the private key
// is not the App's, or the clock is far off.
type BadCredentialsError struct{ AppID string }

func (e *BadCredentialsError) Error() string {
	return fmt.Sprintf("GitHub refused the credentials of App %s: the private key stored for it is not that App's key (or this machine's clock is wrong)", e.AppID)
}

// CheckInstallation reads, as the App (its JWT), the installation on o's
// repository and what it grants: one call, GET /repos/{owner}/{repo}/installation.
// It returns *NotInstalledError for 404, *BadCredentialsError for 401, and any
// other failure as an error that says nothing about the App. The private key
// stays in memory: it signs the JWT, and no error carries either.
func CheckInstallation(ctx context.Context, o Options) (InstallationReport, error) {
	if o.Owner == "" || o.Repo == "" {
		return InstallationReport{}, errors.New("github: owner and repository are required")
	}
	// Before any path is built: dot segments, encoded slashes and the like
	// are refused, never escaped into another API path.
	if _, err := task.CanonicalRepo(o.Owner + "/" + o.Repo); err != nil {
		return InstallationReport{}, fmt.Errorf("github: %q is not an owner and a repository name", o.Owner+"/"+o.Repo)
	}
	if o.AppID == "" || o.PrivateKey == nil {
		return InstallationReport{}, errors.New("github: an App ID and private key are required")
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	app := &httpjson.Client{BaseURL: strings.TrimSuffix(o.BaseURL, "/"), HTTP: o.HTTP, Auth: appAuth(o.AppID, o.PrivateKey, o.Now), Header: http.Header{
		"Accept":               {"application/vnd.github+json"},
		"X-Github-Api-Version": {"2022-11-28"},
		"User-Agent":           {"fugaro"},
	}}
	var inst struct {
		ID          int64             `json:"id"`
		Permissions map[string]string `json:"permissions"`
	}
	path := "/repos/" + url.PathEscape(o.Owner) + "/" + url.PathEscape(o.Repo) + "/installation"
	if err := app.Do(ctx, "GET", path, nil, &inst); err != nil {
		var se *httpjson.StatusError
		if errors.As(err, &se) {
			switch se.Status {
			case http.StatusNotFound:
				return InstallationReport{}, &NotInstalledError{AppID: o.AppID, Repo: o.Owner + "/" + o.Repo}
			case http.StatusUnauthorized:
				return InstallationReport{}, &BadCredentialsError{AppID: o.AppID}
			}
		}
		return InstallationReport{}, fmt.Errorf("reading the installation of App %s on %s/%s: %w", o.AppID, o.Owner, o.Repo, err)
	}
	if inst.ID == 0 {
		return InstallationReport{}, fmt.Errorf("github returned no installation id for %s/%s", o.Owner, o.Repo)
	}
	rep := InstallationReport{InstallationID: inst.ID}
	need := RequiredPermissions()
	for _, name := range slices.Sorted(maps.Keys(need)) {
		if permissionLevels[inst.Permissions[name]] < permissionLevels[need[name]] {
			rep.Missing = append(rep.Missing, PermissionGap{Name: name, Want: need[name], Have: inst.Permissions[name]})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(inst.Permissions)) {
		level := inst.Permissions[name]
		switch forbidden, isForbidden := forbiddenPermissions[name]; {
		case isForbidden && permissionLevels[level] >= permissionLevels[forbidden]:
			rep.Forbidden = append(rep.Forbidden, displayName(name)+": "+displayLevel(level))
		case need[name] == "" && level != "":
			rep.Extra = append(rep.Extra, displayName(name)+": "+displayLevel(level))
		}
	}
	return rep, nil
}
