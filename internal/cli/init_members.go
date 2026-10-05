package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The first run of a new project config grants the launcher and operator roles
// to the person running it. Without that, the installation is applied with no
// launcher and no operator at all: nobody holds the launcher or token-minter
// role, and the first fugaro run fails with "not allowed to sign as the token
// signer" (live Check 27). Only a NEW project config gets the default: an
// existing config, one adopted from an installation, and any run that names
// --launcher or --operator are never changed by it.

// errServiceAccount says the credentials are a service account's, which is
// not a person to grant the roles to by default.
var errServiceAccount = errors.New("the credentials are a service account's")

// userinfoURL is Google's OpenID Connect userinfo endpoint.
const userinfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

// authenticatedUser is the email of the person the Application Default
// Credentials belong to. A package variable so tests never reach the network.
// UNVERIFIED live: that a gcloud ADC refresh token yields a userinfo.email
// token (the scope gcloud's own login grants).
var authenticatedUser = func(ctx context.Context, lc *localcfg.Config) (string, error) {
	if lc.Endpoints.NoAuth {
		return "", errors.New("endpoints: no_auth is set, so there are no credentials to ask")
	}
	if kind, _ := adcInfo(); kind == "service_account" {
		return "", errServiceAccount
	}
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/userinfo.email")
	if err != nil {
		return "", fmt.Errorf("no Google credentials: %w", err)
	}
	hc := &http.Client{Timeout: 20 * time.Second, Transport: &oauth2.Transport{Source: oauth2.ReuseTokenSource(nil, ts)}}
	return userEmail(ctx, hc, userinfoURL)
}

// userEmail reads the verified email of the user the client's token belongs to
// from the userinfo endpoint. A service account's address is refused.
func userEmail(ctx context.Context, hc *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("asking who you are: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("asking who you are: %s", resp.Status)
	}
	var u struct {
		Email    string `json:"email"`
		Verified bool   `json:"email_verified"`
	}
	if json.Unmarshal(body, &u) != nil || u.Email == "" {
		return "", errors.New("the credentials have no email (they were not made with the userinfo.email scope)")
	}
	email := strings.ToLower(strings.TrimSpace(u.Email))
	switch {
	case !u.Verified:
		return "", fmt.Errorf("the email %s is not verified", pluginwire.Printable(email))
	case strings.HasSuffix(email, ".gserviceaccount.com"):
		return "", errServiceAccount
	case strings.ContainsAny(email, " \t\r\n,;:\"'"):
		return "", errors.New("the credentials' email is not an email address")
	}
	return email, nil
}

// defaultMembers makes a new project config's launchers and operators the
// authenticated user, before the first plan, and says so. It does nothing for
// an existing config, when --launcher or --operator was given, or when the
// installation already exists (adopt mode copies its own). Without a user
// (a service account, no credentials) it is a needs-you naming the flags.
func (e *initEngine) defaultMembers(ctx context.Context) error {
	r, o := e.r, e.r.o
	if e.old != nil || o.launchersChanged || o.operatorsChanged || len(e.lc.Terraform.Launchers)+len(e.lc.Terraform.Operators) > 0 {
		return nil
	}
	email, err := authenticatedUser(ctx, e.lc)
	if err != nil {
		why := "init cannot tell who you are (" + oneLineCLI(err.Error()) + ")"
		if errors.Is(err, errServiceAccount) {
			why = "init is running with a service account's credentials, which is not a person to launch runs"
		}
		me := "user:<your-email>"
		return &initflow.NeedsYouError{Left: initflow.Left{Stage: initflow.Installation, Kind: initflow.LeftCommand,
			Text:     why + ": without a launcher and an operator nobody can run anything; name who holds those roles, then rerun",
			Commands: []string{selfCommand() + " init --launcher " + me + " --operator " + me}}}
	}
	member := "user:" + email
	o.launchers, o.operators = []string{member}, []string{member}
	o.launchersChanged, o.operatorsChanged = true, true
	spec, err := installOptions(o, e.lc)
	if err != nil {
		return err
	}
	// installOptions starts from the flags: keep what discovery of this run
	// set on the spec before (nothing yet: the defaults come before the plan).
	e.spec = spec
	e.defaulted = member
	fmt.Fprintf(r.w, "launchers: %s (you); operators: %s (you); change with --launcher/--operator (a new project config grants you the launcher and operator roles so that fugaro run works)\n", member, member)
	return nil
}
