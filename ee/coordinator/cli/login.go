// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// This file is the CLI's front-door client: `fugaro coordinator login` runs the
// authorization-code flow M5 serves, with PKCE on a loopback redirect, and puts
// what it receives where the roadmap commands can use it again — the operating
// system's keychain and never a file (D2122).
//
// **The credential this holds is a refresh token, so the file question is the
// whole design.** A refresh token in a dotfile is a refresh token in every
// backup, every checkout and every `cat` a person runs while debugging, and it
// is the one value of this flow that lives longer than the process: the access
// token it is exchanged for is minted per call and never stored. So the value
// goes through internal/mailbox/keychain — the same store `fugaro pair` puts a
// pairing key in, whose interface exists precisely so that no test here
// touches a real keychain — and nothing on this path opens a file for writing.
//
// **The front door's URL is read from the installation's outputs and never
// derived** (outputs.tf's `mcp_server`): Cloud Run's URL carries the project
// number, which nothing computes offline, and the URL is the issuer every
// token this door mints is bound to. An installation whose front door is off
// answers null there, and that null is the refusal — one sentence, naming what
// turns a door on — rather than a URL this build guessed and a connection that
// then failed.
//
// **The URL is read once, at login, and the credential carries it.** The
// roadmap commands then hold everything they need in the keychain: they check
// the issuer the installation's outputs name against the issuer the credential
// was minted for (a door that moved is a credential that must not be sent to
// it) without reading Terraform again on every write.
//
// **No token, code or verifier is ever printed, logged or named in an error.**
// The three are the flow's own secrets: what a failure says is which step
// failed and what the front door answered about it, and the front door's own
// refusals name fields rather than values (M5's rule, SECURITY.md).

// errFrontDoorOff is the installation whose outputs publish no front door:
// D934's switch is off, so no URL, client or credential exists to reach. The
// sentence is the one both commands refuse with, written here rather than at
// each call site so that login and the roadmap group cannot come to describe
// the same state differently.
var errFrontDoorOff = errors.New("this installation's front door is off: its outputs publish no mcp_server URL, so it serves nothing to sign in to or call")

// errNoFrontDoor is that state as a refusal: exit 1, which is what a person
// who has not enrolled the installation is being told. It wraps the sentinel
// so that a caller which has to tell this refusal from a failure still can.
func errNoFrontDoor() error { return fcli.UserErr("%w", errFrontDoorOff) }

// coordinatorKeychain is the operating system's keychain, and the seam the
// tests replace with keychain.Fake. It is the same seam `fugaro pair` declares
// for the same reason: no test in this repository reads or writes the
// developer's own keychain (M5), and the store the real one returns is the
// platform's program.
var coordinatorKeychain = keychain.System

// coordinatorFrontDoorClient is the HTTP client the flow runs on, nil in
// production — http.DefaultClient — and set by a test whose fake door is a TLS
// server. It is the seam coordinatorServiceClient is for the credentials
// service, in the same package.
var coordinatorFrontDoorClient http.RoundTripper

// coordinatorOpenBrowser hands the authorization URL to the person's browser.
// It is a seam because the flow cannot be tested against a real one: the tests
// replace it with the browser the flow needs — a caller that fetches the
// authorize page and answers the loopback redirect — and every one of them
// would otherwise open a window on the machine running the tests.
var coordinatorOpenBrowser = openCoordinatorURL

// coordinatorFrontDoorURL answers the front-door URL this installation's
// outputs publish. It is the group's, not login's: the roadmap writes compare
// it with the issuer the credential was minted for.
var coordinatorFrontDoorURL = readCoordinatorFrontDoorURL

// coordinatorInstallationOutputs is the installation root's outputs, as
// `terraform output -json` answers them. It is a seam of its own so that the
// reading of `mcp_server` — null, an object, or a shape this build does not
// know — is tested without Terraform, and the Terraform run behind it is the
// one thing here a test drives through the fake binary (internal/infra/tf's
// faketerraform, as internal/cli's own output reads are).
var coordinatorInstallationOutputs = readInstallationOutputs

// coordinatorLoginWait bounds the loopback wait. A person reads the URL, signs
// in, approves the flow on the paired device and continues on the page, so the
// wait is minutes and not seconds; past it the listener closes and the login is
// refused rather than left holding a port nobody will answer. It is a var so
// that the test of that refusal does not spend five minutes proving it.
var coordinatorLoginWait = 5 * time.Minute

// coordinatorCredentialVersion is the version of the stored credential's
// document. A later shape is this number and not a field that means something
// new, so a credential written by another release is refused by name rather
// than decoded into something this build believes.
const coordinatorCredentialVersion = 1

// coordinatorCredentialSuffix is the tail of the keychain item's name, after
// the project's: `<project>-coordinator-roadmap`. ParseItem's ceiling is 64
// characters and a project name may be 40, so this suffix is 20 of the 24 that
// are left — a longer one could not be built for the longest project name
// localcfg accepts, and a login that cannot name its item is a login that
// cannot store a token.
//
// The name says which credential it is: the front door's, for the roadmap
// scope this release's login asks for. A second scope's credential is a second
// name, and a later task that asks for one adds it here.
const coordinatorCredentialSuffix = "-coordinator-roadmap"

// frontDoorCredential is what a login stores and a write reads: the issuer the
// token was minted for, the client registration the flow created, the refresh
// token itself, and the scopes the person consented to.
//
// **It carries no client secret.** The door registers one and this flow never
// presents it: the metadata advertises `token_endpoint_auth_method: none`, both
// grants a native client uses are public-client grants, and a second long-lived
// value on a device is the thing PKCE exists to avoid (M5's own note on the
// token endpoint). Storing one would be storing a credential nothing uses.
//
// **It is stored as base64url and not as JSON text**, and the reason is the
// keychain's own: `security add-generic-password` takes the value on a command
// line, and internal/mailbox/keychain refuses a value outside the base64url
// alphabet rather than escaping it, so a JSON document written raw would be a
// login that works on Linux and fails on macOS. The encoding is the store's
// shape and never a secret's: the value is still the keychain's, and nothing
// here is a file.
type frontDoorCredential struct {
	Version      int      `json:"version"`
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"client_id"`
	RefreshToken string   `json:"refresh_token"`
	Scopes       []string `json:"scopes"`
}

// coordinatorCredentialItem is where this project's credential lives. The
// project's name is in the item and ParseItem checks that it is, so a config
// naming an item cannot reach another project's token.
func coordinatorCredentialItem(lc *localcfg.Config) (keychain.Item, error) {
	item, err := keychain.ParseItem(lc.Name+coordinatorCredentialSuffix, lc.Name)
	if err != nil {
		return keychain.Item{}, fcli.UserErr("the keychain item for this project: %v", err)
	}
	return item, nil
}

// coordinatorReadCredential reads the stored credential, and answers the
// refusal a person who has not signed in needs: one sentence, naming the
// command that signs them in.
func coordinatorReadCredential(ctx context.Context, store keychain.Store, item keychain.Item) (frontDoorCredential, error) {
	key, err := store.Key(ctx, item)
	switch {
	case errors.Is(err, keychain.ErrNoKey):
		return frontDoorCredential{}, errCoordinatorNotSignedIn(item.Name())
	case err != nil:
		return frontDoorCredential{}, fcli.RemoteErr(err)
	}
	material, err := base64.RawURLEncoding.DecodeString(string(key.Bytes()))
	if err != nil {
		return frontDoorCredential{}, errCoordinatorNotSignedIn(item.Name())
	}
	var cred frontDoorCredential
	if err := json.Unmarshal(material, &cred); err != nil {
		return frontDoorCredential{}, errCoordinatorNotSignedIn(item.Name())
	}
	if cred.Version != coordinatorCredentialVersion || cred.ClientID == "" || cred.RefreshToken == "" {
		return frontDoorCredential{}, errCoordinatorNotSignedIn(item.Name())
	}
	return cred, nil
}

// errCoordinatorNotSignedIn is the one sentence every roadmap command refuses
// with when no credential is held, and the sentence a credential this build
// cannot read is refused with too: the two are the same state for the person —
// there is nothing here to write with — and the fix is the same one command.
// It names the item so that a person with several projects can tell which one
// is missing it.
func errCoordinatorNotSignedIn(item string) error {
	return fcli.UserErr("no front-door credential for this project is in the keychain (item %q): run `fugaro coordinator login --scope roadmap:write` first", item)
}

// coordinatorStoreCredential writes the credential, and never anywhere else.
func coordinatorStoreCredential(ctx context.Context, store keychain.Store, item keychain.Item, cred frontDoorCredential) error {
	cred.Version = coordinatorCredentialVersion
	material, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	if err := store.Put(ctx, item, keychain.NewKey([]byte(base64.RawURLEncoding.EncodeToString(material)))); err != nil {
		return fcli.RemoteErr(err)
	}
	return nil
}

// coordinatorLoginDoc is login's --json document: what the person is now signed
// in as, and nothing of the token. The issuer is printed because it is the one
// fact a person needs to tell which installation's door their keychain now
// holds a credential for.
type coordinatorLoginDoc struct {
	Version int      `json:"version"`
	Project string   `json:"project"`
	Issuer  string   `json:"issuer"`
	Scopes  []string `json:"scopes"`
}

// coordinatorLogoutDoc is logout's.
type coordinatorLogoutDoc struct {
	Version int    `json:"version"`
	Project string `json:"project"`
	Removed bool   `json:"removed"`
}

// coordinatorLoginOptions are login's flags.
type coordinatorLoginOptions struct {
	cloud  fcli.CloudFlags
	scopes []string
	asJSON bool
}

func newCoordinatorLoginCmd() *cobra.Command {
	var o coordinatorLoginOptions
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to the installation's front door and keep the token in your keychain",
		Long: "Run the authorization-code flow this installation's front door serves\n" +
			"(docs/coordinator/implementation-plan.md, P9f; the flow is the MCP server\n" +
			"plan's M5): a loopback redirect with PKCE, so nothing the browser carries\n" +
			"back can be replayed from a history, a log or a shoulder.\n\n" +
			"The front door's URL is read from the installation's own outputs, which is\n" +
			"the only place it is published: Cloud Run's URL carries the project number\n" +
			"and nothing derives it. An installation whose front door is off publishes\n" +
			"none, and that is this command's refusal.\n\n" +
			"The refresh token it receives is stored in the operating system's keychain,\n" +
			"under an item named after the project, and never in a file. `fugaro\n" +
			"coordinator logout` deletes it. What the command prints is the issuer and\n" +
			"the scopes: never the token, the code or the verifier.\n\n" +
			"The scope defaults to roadmap:write, which is what the roadmap group's\n" +
			"commands hold. It is an operator's scope on this installation's own list:\n" +
			"a person who is not an operator is refused by the door, at the approval\n" +
			"step, where the list is read.\n" +
			"Exit codes: 1 for a refusal (no front door, no scope this build mints,\n" +
			"the person's own refusal, or an installation that would not store the\n" +
			"token), 2 for a failure of the front door or of the keychain.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runCoordinatorLogin(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringArrayVar(&o.scopes, "scope", []string{mcpauth.ScopeRoadmapWrite}, "the scope to ask for (repeatable; the front door mints "+strings.Join(mcpauth.ScopeVocabulary, ", ")+")")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

func newCoordinatorLogoutCmd() *cobra.Command {
	var o coordinatorLoginOptions
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Delete the front door credential this project's keychain holds",
		Long: "Remove the keychain item `fugaro coordinator login` wrote, which is the\n" +
			"whole of what this machine holds for the front door: no file, no cache and\n" +
			"nothing else is written, so deleting the item is the end of it.\n\n" +
			"An item that is not there is not an error: the state a person asks for is\n" +
			"the state they get. The token family itself is not revoked by this command\n" +
			"— the door revokes a family where it is revoked, and a device that lost\n" +
			"its credential is not a theft signal (M5b).\n" +
			"Exit codes: 1 for a refusal, 2 for a keychain that could not be reached.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runCoordinatorLogout(cmd, &o) },
	}
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

func runCoordinatorLogout(cmd *cobra.Command, o *coordinatorLoginOptions) error {
	ctx := cmd.Context()
	env, err := fcli.SelectProject(ctx, &o.cloud)
	if err != nil {
		return err
	}
	lc := env.Config()
	item, err := coordinatorCredentialItem(lc)
	if err != nil {
		return err
	}
	if err := coordinatorKeychain().Delete(ctx, item); err != nil {
		return fcli.RemoteErr(err)
	}
	if o.asJSON {
		return writeCoordinatorJSON(cmd.OutOrStdout(), coordinatorLogoutDoc{Version: coordinatorCredentialVersion, Project: lc.Name, Removed: true})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Removed the front-door credential for %s from the keychain.\n", lc.Name)
	return nil
}

func runCoordinatorLogin(cmd *cobra.Command, o *coordinatorLoginOptions) error {
	ctx := cmd.Context()
	scopes, err := coordinatorLoginScopes(o.scopes)
	if err != nil {
		return err
	}
	env, err := fcli.SelectProject(ctx, &o.cloud)
	if err != nil {
		return err
	}
	lc := env.Config()
	item, err := coordinatorCredentialItem(lc)
	if err != nil {
		return err
	}
	issuer, err := coordinatorFrontDoorURL(ctx, lc)
	if err != nil {
		return err
	}
	client, err := coordinatorFrontDoorHTTP()
	if err != nil {
		return err
	}
	// The listener comes first, and it has to: the redirect URI the client
	// registers carries the port the person's browser will come back to, and a
	// registration that named a port nothing is listening on would be a flow
	// that ends in a browser's error page. It binds 127.0.0.1 literally and
	// never `localhost`, because the name is resolved by whatever the machine's
	// resolver says and the address is what the door's own rule is written in.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fcli.UserErr("no loopback port could be opened for the sign-in: %v", err)
	}
	defer ln.Close()
	redirect := "http://" + ln.Addr().String() + coordinatorCallbackPath

	verifier, challenge, err := coordinatorPKCE()
	if err != nil {
		return fcli.RemoteErr(err)
	}
	state, err := coordinatorRandomState()
	if err != nil {
		return fcli.RemoteErr(err)
	}
	registration, err := coordinatorRegister(ctx, client, issuer, redirect, lc.Name)
	if err != nil {
		return err
	}
	authorize, err := coordinatorAuthorizeURL(issuer, registration.ClientID, redirect, state, challenge, scopes)
	if err != nil {
		return err
	}
	answer, err := coordinatorAwaitCallback(ctx, ln, state)
	if err != nil {
		return err
	}
	// The browser is opened after the listener is up and after the flow is
	// registered, so that a person who follows the URL immediately is not
	// racing the process that is about to wait for them.
	fmt.Fprintf(cmd.ErrOrStderr(), "Open this URL to sign in:\n%s\n\n", authorize)
	// A browser that could not be opened is not a failure: the URL above is
	// what the person needs, and this command has just printed it.
	_ = coordinatorOpenBrowser(authorize)
	if err := answer.wait(); err != nil {
		return err
	}
	tokens, err := coordinatorExchangeCode(ctx, client, issuer, registration.ClientID, redirect, verifier, answer.code)
	if err != nil {
		return err
	}
	cred := frontDoorCredential{Issuer: issuer, ClientID: registration.ClientID, RefreshToken: tokens.RefreshToken, Scopes: scopes}
	if err := coordinatorStoreCredential(ctx, coordinatorKeychain(), item, cred); err != nil {
		return err
	}
	if o.asJSON {
		return writeCoordinatorJSON(cmd.OutOrStdout(), coordinatorLoginDoc{Version: coordinatorCredentialVersion, Project: lc.Name, Issuer: issuer, Scopes: scopes})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Signed in to %s's front door for %s: the refresh token is in your operating system's keychain.\n", lc.Name, strings.Join(scopes, " "))
	return nil
}

// coordinatorLoginScopes checks the scopes a person asked for against the
// vocabulary this build knows, before a browser is opened: the door would
// refuse an unknown one at the authorize endpoint, and a round trip through a
// person's browser to be told that is a page nobody needed to see. The
// vocabulary is the front door's own constant, so the two cannot drift.
func coordinatorLoginScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return nil, fcli.UserErr("at least one --scope is needed: a login with no scope would hold a token that reaches no tool")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		switch {
		case s == "":
			return nil, fcli.UserErr("--scope is empty: name a scope of %s", strings.Join(mcpauth.ScopeVocabulary, ", "))
		case !mcpauth.KnownScope(s):
			return nil, fcli.UserErr("%q is not a scope this front door mints: it mints %s", s, strings.Join(mcpauth.ScopeVocabulary, ", "))
		case seen[s]:
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, nil
}

// coordinatorFrontDoorHTTP is the client every call of the flow is made with.
func coordinatorFrontDoorHTTP() (*http.Client, error) {
	transport := coordinatorFrontDoorClient
	if transport == nil {
		transport = http.DefaultTransport
	}
	// The redirect is followed by the person's browser and never by this
	// client: the flow's only redirect is the one back to the loopback
	// listener, and a client that followed it would be this process fetching
	// its own callback. Refused rather than followed, so a door that answered
	// a redirect to somewhere else cannot move the exchange with it.
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("the front door answered a redirect, and this client follows none")
		},
	}, nil
}

// coordinatorRegister creates this client's registration at the door's dynamic
// registration endpoint (RFC 7591, M5), for exactly the redirect URI the
// listener above is bound to.
//
// The registration is per login and not kept: it carries nothing but the
// redirect URI and a name, the door bounds how many one source may make and
// sweeps what it wrote, and a client id stored beside a refresh token would be
// a second thing to keep in step with the keychain item for no gain — the
// credential that matters is the token, and the id it names travels with it.
func coordinatorRegister(ctx context.Context, client *http.Client, issuer, redirect, project string) (coordinatorRegistration, error) {
	body, err := json.Marshal(map[string]any{
		"redirect_uris": []string{redirect},
		"client_name":   "fugaro CLI (" + project + ")",
	})
	if err != nil {
		return coordinatorRegistration{}, err
	}
	resp, err := coordinatorPost(ctx, client, issuer+mcpauth.RegisterPath, "application/json", body)
	if err != nil {
		return coordinatorRegistration{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return coordinatorRegistration{}, fcli.RemoteErr(fmt.Errorf("the front door's registration answer could not be read: %w", err))
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return coordinatorRegistration{}, fcli.RemoteErr(fmt.Errorf("the front door refused to register this client (%s)", resp.Status))
	}
	var reg coordinatorRegistration
	if err := json.Unmarshal(data, &reg); err != nil || reg.ClientID == "" {
		return coordinatorRegistration{}, fcli.RemoteErr(errors.New("the front door's registration answer named no client id"))
	}
	return reg, nil
}

// coordinatorRegistration is as much of RFC 7591's answer as this flow reads.
// The secret the door also returns is deliberately not modelled: nothing here
// presents one, and a field this code never reads is a field a later reader
// would believe was used.
type coordinatorRegistration struct {
	ClientID string `json:"client_id"`
}

// coordinatorAuthorizeURL builds the URL the person opens: the code flow, the
// client and its redirect, the state, and the PKCE challenge with the method
// the door requires. S256 is named explicitly because the door refuses an
// absent method rather than reading RFC 7636's `plain` default.
func coordinatorAuthorizeURL(issuer, clientID, redirect, state, challenge string, scopes []string) (string, error) {
	u, err := url.Parse(issuer + mcpauth.AuthorizePath)
	if err != nil {
		return "", fcli.UserErr("the front door's URL is not one this command can build an authorization request on: %v", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirect)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("scope", strings.Join(scopes, " "))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// coordinatorCallbackPath is the path the loopback listener answers. The door
// matches the redirect URI exactly, so the path is a constant of this file and
// the URI is built from it once.
const coordinatorCallbackPath = "/callback"

// coordinatorCallback is the one answer the loopback listener waits for: the
// code the door sent, or the refusal it sent instead.
type coordinatorCallback struct {
	done chan struct{}
	code string
	err  error
	// wrote is whether the browser's request has been answered yet, so that
	// wait can tell "no answer within the bound" from "answered, and this
	// process has not looked yet".
	wrote chan struct{}
}

// coordinatorAwaitCallback serves the listener until the door's answer arrives
// or ctx ends, and returns it unread: the caller opens the browser first, and
// reading here would be this process waiting on a browser it has not started.
//
// The listener answers exactly one request. Every other request — a browser's
// favicon, a second tab, a port scan — is answered with the same page and
// ignored, because a state this process did not mint is not the flow's answer
// and refusing it loudly would be a refusal a stranger could read.
func coordinatorAwaitCallback(ctx context.Context, ln net.Listener, state string) (*coordinatorCallback, error) {
	cb := &coordinatorCallback{done: make(chan struct{}), wrote: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc(coordinatorCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		select {
		case <-cb.done:
			// The flow is answered; this is a second request to the same
			// listener, and it changes nothing.
		default:
			switch {
			case q.Get("state") != state:
				cb.finish("", errors.New("a request reached the loopback listener carrying a state this login did not send, so it is not this flow's answer and was ignored"))
			case q.Get("error") != "":
				cb.finish("", fmt.Errorf("the front door refused the authorization: %s", coordinatorOAuthError(q)))
			case q.Get("code") == "":
				cb.finish("", errors.New("the front door answered the loopback redirect with neither a code nor an error"))
			default:
				cb.finish(q.Get("code"), nil)
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, coordinatorCallbackPage)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	go func() {
		select {
		case <-ctx.Done():
			cb.finish("", ctx.Err())
		case <-cb.done:
		}
		_ = srv.Close()
	}()
	return cb, nil
}

// finish records the answer once and wakes whoever is waiting on it.
func (cb *coordinatorCallback) finish(code string, err error) {
	select {
	case <-cb.done:
		return
	default:
	}
	cb.code, cb.err = code, err
	close(cb.done)
}

// wait blocks until the answer arrives or the bound passes. The bound is
// checked here rather than in the server so that the refusal a person reads
// names the step they were in.
func (cb *coordinatorCallback) wait() error {
	select {
	case <-cb.done:
		return cb.err
	case <-time.After(coordinatorLoginWait):
		return fcli.UserErr("no answer reached the loopback listener within %s: the sign-in was not completed, and nothing was stored", coordinatorLoginWait)
	}
}

// coordinatorCallbackPage is what the person's browser shows once the flow is
// answered. It says nothing about the token, the code or the project: it is a
// page rendered by a process that will exit in a moment, in a browser that may
// be somebody else's.
const coordinatorCallbackPage = "<!doctype html><meta charset=\"utf-8\"><title>fugaro</title>" +
	"<p>Signed in. You can close this window and return to the terminal.</p>\n"

// coordinatorExchangeCode spends the code at the token endpoint (RFC 6749
// §4.1.3) with the verifier that proves this process is the one that started
// the flow.
func coordinatorExchangeCode(ctx context.Context, client *http.Client, issuer, clientID, redirect, verifier, code string) (coordinatorFrontDoorTokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirect},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	return coordinatorTokenRequest(ctx, client, issuer, form)
}

// coordinatorRefreshToken rotates the stored refresh token (D930) and answers
// the access token beside the credential that replaces the one presented.
//
// The rotation is why a write stores what it read: the token presented is
// spent by the exchange, and a command that kept the old one would find its
// family revoked the next time it ran — the door reads a replayed refresh
// token as the theft signal D930 says it is.
func coordinatorRefreshToken(ctx context.Context, client *http.Client, issuer, clientID, refresh string) (coordinatorFrontDoorTokens, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {clientID},
	}
	return coordinatorTokenRequest(ctx, client, issuer, form)
}

// coordinatorFrontDoorTokens is the token endpoint's answer, as much of it as this
// client reads: the access token it sends to the door and the refresh token it
// keeps. `expires_in` and the scope are not read — the access token is used
// within the call it was minted for and never stored, and the scopes a
// credential carries are the ones the person consented to at login.
type coordinatorFrontDoorTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func coordinatorTokenRequest(ctx context.Context, client *http.Client, issuer string, form url.Values) (coordinatorFrontDoorTokens, error) {
	resp, err := coordinatorPost(ctx, client, issuer+mcpauth.TokenPath, "application/x-www-form-urlencoded", []byte(form.Encode()))
	if err != nil {
		return coordinatorFrontDoorTokens{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return coordinatorFrontDoorTokens{}, fcli.RemoteErr(fmt.Errorf("the front door's token answer could not be read: %w", err))
	}
	if resp.StatusCode != http.StatusOK {
		// The door's own refusal sentence is read out of the body and named
		// here; the body carries no token — an OAuth error names the field and
		// never the value — and what this process sent is never echoed.
		var e struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return coordinatorFrontDoorTokens{}, fcli.UserErr("the front door refused the token request (%s): %s", e.Error, e.ErrorDescription)
		}
		return coordinatorFrontDoorTokens{}, fcli.RemoteErr(fmt.Errorf("the front door refused the token request (%s)", resp.Status))
	}
	var tokens coordinatorFrontDoorTokens
	if err := json.Unmarshal(data, &tokens); err != nil || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return coordinatorFrontDoorTokens{}, fcli.RemoteErr(errors.New("the front door's token answer carried no usable access and refresh token pair"))
	}
	return tokens, nil
}

// coordinatorPost is one POST of this flow, with the two headers every request
// here carries: the content type it sends and the JSON accept it reads.
func coordinatorPost(ctx context.Context, client *http.Client, endpoint, contentType string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fcli.UserErr("the front door's URL is not one this command can call: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fcli.RemoteErr(fmt.Errorf("the front door could not be reached: %w", err))
	}
	return resp, nil
}

// coordinatorOAuthError is the door's own error answer, as a sentence: the
// error code and what it said about it, both of which name a field of the
// request and never a value of the flow.
func coordinatorOAuthError(q url.Values) string {
	code := q.Get("error")
	if d := q.Get("error_description"); d != "" {
		return code + ": " + d
	}
	return code
}

// coordinatorPKCE draws one verifier and its S256 challenge (RFC 7636): 32
// random bytes, whose unpadded base64url spelling is the 43 characters the
// door's own bound admits, and the challenge is that spelling's SHA-256 in the
// same alphabet.
func coordinatorPKCE() (verifier, challenge string, err error) {
	verifier, err = coordinatorRandomState()
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// coordinatorRandomState is 32 random bytes in unpadded base64url: the state
// the flow is bound to and the PKCE verifier, which are the same shape.
func coordinatorRandomState() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("no randomness for the sign-in's own value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// readCoordinatorFrontDoorURL answers the URL this installation's front door is
// served at, out of its own outputs, and errFrontDoorOff when it publishes
// none.
//
// It is the read `fugaro user` makes of the same outputs: the installation
// root's workdir, an init against the state bucket the local config's spec
// resolves, and `terraform output`. Nothing is planned and nothing is applied.
func readCoordinatorFrontDoorURL(ctx context.Context, lc *localcfg.Config) (string, error) {
	raw, err := coordinatorInstallationOutputs(ctx, lc)
	if err != nil {
		return "", err
	}
	value, ok := raw["mcp_server"]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return "", errNoFrontDoor()
	}
	var door struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(value, &door); err != nil {
		return "", fcli.RemoteErr(fmt.Errorf("the installation's mcp_server output is not the shape this build reads: %w", err))
	}
	if door.URL == "" {
		return "", fcli.RemoteErr(errors.New("the installation's mcp_server output names no URL, so there is no front door to call"))
	}
	if err := coordinatorFrontDoorAddress(door.URL); err != nil {
		return "", err
	}
	return strings.TrimSuffix(door.URL, "/"), nil
}

// coordinatorFrontDoorAddress refuses a front-door URL this process will not
// send a credential to: https, or a loopback address, which is what a door
// served on this machine is.
//
// The bearer token and the refresh token both travel to this URL, so a plain
// http address that is not loopback is a credential in the clear on the way to
// a host that is not this one — the one downgrade this client can refuse
// before it has a token to lose.
func coordinatorFrontDoorAddress(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fcli.UserErr("the installation's mcp_server output is not a URL: %v", err)
	}
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && isLoopbackHost(u.Hostname()):
		return nil
	default:
		return fcli.UserErr("the installation's front door is at %q, which is neither https nor a loopback address, and this command sends no token there", raw)
	}
}

// isLoopbackHost reports whether host is one of the addresses a door on this
// machine is served at. The host is compared as a whole and never as a suffix,
// so a name that merely ends in one of these is somebody else's host.
func isLoopbackHost(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "[::1]":
		return true
	default:
		return strings.EqualFold(host, "localhost")
	}
}

// readInstallationOutputs reads the installation root's outputs from its
// state. It runs in a workdir of its own, leaving the installation's own as it
// is — the same workdir, and the same three calls, as the core's own read of
// these outputs (internal/cli's installationOutputs).
func readInstallationOutputs(ctx context.Context, lc *localcfg.Config) (map[string]json.RawMessage, error) {
	spec, err := infra.Installation(lc, infra.InstallOptions{})
	if err != nil {
		return nil, fcli.UserErr("%v", err)
	}
	bin, err := exec.LookPath("terraform")
	if err != nil {
		return nil, fcli.UserErr("reading the installation's front-door URL needs terraform (>= 1.9, < 2) on PATH: %v", err)
	}
	dir, err := infra.InstallationOutputsWorkdir(os.Getenv, lc.GCPProject)
	if err != nil {
		return nil, fcli.UserErr("%v", err)
	}
	wd, err := infra.PrepareWorkdir(dir, "installation")
	if err != nil {
		return nil, fcli.UserErr("the Terraform workdir: %v", err)
	}
	cache, err := infra.PluginCache(os.Getenv)
	if err != nil {
		return nil, fcli.UserErr("%v", err)
	}
	env, err := tf.Env(os.Environ(), wd.Dir, cache)
	if err != nil {
		return nil, fcli.UserErr("%v", err)
	}
	t, err := tf.New(bin, wd.Root, env)
	if err != nil {
		return nil, fcli.RemoteErr(err)
	}
	backend, err := wd.WriteBackend(spec.StateBucket, infra.StatePrefixInstallation)
	if err != nil {
		return nil, fcli.UserErr("%v", err)
	}
	if err := t.Init(ctx, backend); err != nil {
		return nil, fcli.RemoteErr(err)
	}
	raw, err := t.Output(ctx)
	if err != nil {
		return nil, fcli.RemoteErr(err)
	}
	if len(raw) == 0 {
		return nil, fcli.UserErr("the installation's state has no outputs: run `fugaro setup` first")
	}
	return raw, nil
}

// openCoordinatorURL hands a URL to this platform's opener, and returns at
// once: the browser is the person's, and a login that waited for it to exit
// would be a login that waits for a window to be closed. A platform with no
// opener is not an error — the caller has already printed the URL — and the
// process is reaped so that no zombie is left behind.
func openCoordinatorURL(raw string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		name = "xdg-open"
	}
	cmd := exec.Command(name, append(args, raw)...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
