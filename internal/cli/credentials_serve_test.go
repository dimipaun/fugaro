package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/credsvc"
)

// The credentials service's entry point (plan task C1.14) is not a command
// anyone is offered: it is what the image's own entrypoint runs, in the Cloud
// Run service the installation module creates, and it takes no argument, no
// flag and no config file. So it is hidden twice over -- the group and the
// subcommand -- and the root help is where that is checked, because a hidden
// cobra command is one that does not appear there and nowhere else.
func TestCredentialsServeHidden(t *testing.T) {
	out, _, err := execute(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if offersCommand(out, "credentials") {
		t.Fatalf("fugaro --help lists the credentials service's command:\n%s", out)
	}
	out, _, err = execute(t, "credentials", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if offersCommand(out, "serve") {
		t.Fatalf("fugaro credentials --help lists serve:\n%s", out)
	}
	// And the hiding is the commands' own Hidden field, not an accident of
	// cobra's rules. The help output above would still be clean without it: a
	// group with no Run of its own and only hidden subcommands is left out of
	// its parent's list anyway, and the day this group gains a visible
	// subcommand `fugaro --help` would list it. The field is what the image's
	// entrypoint is held to.
	root := NewRootCmd()
	group := findSub(t, root, "credentials")
	if !group.Hidden {
		t.Error("the credentials group is not hidden: fugaro --help would list it as soon as it gains a visible subcommand")
	}
	if !findSub(t, group, "serve").Hidden {
		t.Error("credentials serve is not hidden: fugaro credentials --help would list it")
	}
}

// offersCommand reports whether a help page offers a command called name:
// cobra lists a command as an indented line whose first field is its name
// ("  credentials  Run the credentials service ..."). It is a line test and
// not a substring test on purpose -- the `creds` command's own Short names the
// credentials service ("Set and list the credentials service's one bundle
// secret"), so a substring test would fail on a tree where nothing is wrong
// (this test failed exactly that way when C1.25's command landed after C1.14
// was written).
func offersCommand(help, name string) bool {
	for _, line := range strings.Split(help, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == name {
			return true
		}
	}
	return false
}

// findSub returns the named direct subcommand of cmd, failing when there is
// none: a test that walked an empty list instead would pass for the wrong
// reason.
func findSub(t *testing.T, cmd *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("%s has no %q subcommand", cmd.CommandPath(), name)
	return nil
}

// The service's environment is the installation module's: credentials.tf sets
// seven names on the service's container, internal/infra/tf's
// credentialsServiceEnv is the allow-list that pins exactly those, and
// credentialsServiceEnvNames is what this command reads. A service started
// without one of them cannot say which installation it is serving, so the
// command refuses by name before anything else -- the same refusal the history
// job makes for the same variables, and one that never quotes the value it
// found (or did not find).
func TestCredentialsServeRefusesWithoutItsEnvironment(t *testing.T) {
	for _, k := range credentialsServiceEnvNames {
		t.Run(k, func(t *testing.T) {
			// Every name is set, then the one under test is emptied: an empty
			// variable is not a value the module can set (Terraform writes
			// each of these from an input the installation has), so the
			// refusal must be about that name and not about another.
			for _, n := range credentialsServiceEnvNames {
				t.Setenv(n, credentialsServeEnv[n])
			}
			t.Setenv(k, "")
			_, _, err := execute(t, "credentials", "serve")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error (exit 1)", err)
			}
			if !strings.Contains(err.Error(), k) {
				t.Fatalf("err = %v, want a refusal naming %s", err, k)
			}
		})
	}
}

// credentialsServeEnv is a complete, valid service environment: the values the
// installation module would set, with the Fugaro project, GCP project and
// Firebase project the golden fixtures use (internal/infra/testdata's
// installation-budget-job.tfvars.json) so the derived names here are the names
// a real installation derives.
var credentialsServeEnv = map[string]string{
	envProject:               "aurora",
	envGCPProject:            "proj-1234",
	envRegion:                "us-east5",
	envRTDBURL:               "https://aurora-fp-default-rtdb.firebaseio.com",
	envFirebaseFP:            "aurora-fp",
	envCredentialsServiceURL: "https://fugaro-credentials-abcde-uc.a.run.app",
	envJobAccounts:           `[{"provider":"github","repo":"acme/webapp","workflow":"api"}]`,
}

// TestCredentialsServiceConfigBuildsFromAFullEnvironment is the headline: the
// whole of this task is that the service can be configured at all, so a
// complete environment must build a credsvc.Config that NewServer accepts --
// the object the host below serves. Until this task the builder could not: it
// read the three names the module set and refused, because the four facts a
// Config needs (the database, the Firebase project, the audience and the job
// accounts) were not in the container.
//
// The two things the builder needs that reach Google as the service itself --
// the token source the database is read with and the Secret Manager client the
// bundle is read through -- are the test seam (credentialsTest); nothing else
// here is faked, so the derivation of the job accounts and every validation
// the verifier makes are the production ones.
//
// The derived table is pinned by value and not by comparison with the
// function that made it: these emails are the ones internal/infra deploys
// (gcp.ServiceAccountID over task.Slug), so a change in either derivation
// fails here rather than in a run that cannot authenticate.
func TestCredentialsServiceConfigBuildsFromAFullEnvironment(t *testing.T) {
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	fakeCredentialsService(t)

	cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("credentialsServiceConfig: %v", err)
	}
	want := []credsvc.JobAccount{{
		Email: "fugaro-acme-webapp-ap-7daad322@proj-1234.iam.gserviceaccount.com",
		Slug:  "acme-webapp-cf67624405f4121a",
		Repo:  "github/acme/webapp",
	}}
	if len(cfg.JobAccounts) != 1 || cfg.JobAccounts[0] != want[0] {
		t.Fatalf("job accounts = %+v, want %+v", cfg.JobAccounts, want)
	}
	// Every piece NewServer requires is built here, and the one thing that is
	// deliberately left empty is the shared project jobs' accounts (Global
	// Constraint 6: per-repository jobs only until C3.9).
	for _, missing := range []struct {
		name string
		set  bool
	}{
		{"Auth", cfg.Auth != nil},
		{"Bundles", cfg.Bundles != nil},
		{"Policy", cfg.Policy != nil},
		{"State", cfg.State != nil},
		{"Kills", cfg.Kills != nil},
		{"DB", cfg.DB != nil},
		{"Probes", cfg.Probes != nil},
		{"AuditLog", cfg.AuditLog != nil},
		{"Log", cfg.Log != nil},
	} {
		if !missing.set {
			t.Errorf("the configuration has no %s: the service cannot serve without it", missing.name)
		}
	}
	if _, err := credsvc.NewServer(cfg); err != nil {
		t.Fatalf("NewServer refused the configuration a complete environment builds: %v", err)
	}
}

// fakeCredentialsService replaces the two constructors that would need
// Application Default Credentials with ones that build nothing: the token
// source is never asked for a token (nothing in the configuration reads the
// database) and the Secret Manager client is never asked for a version.
func fakeCredentialsService(t *testing.T) {
	t.Helper()
	credentialsTest = &credentialsWiring{
		tokens: func(context.Context) (oauth2.TokenSource, error) {
			return oauth2.StaticTokenSource(&oauth2.Token{}), nil
		},
		secrets: func(context.Context) (*secretmanager.Service, error) { return &secretmanager.Service{}, nil },
	}
	t.Cleanup(func() { credentialsTest = nil })
}

// TestCredentialsServiceConfigRefusesAMalformedVariable: every name has a
// shape, and a value that is not it is refused by name -- before the
// configuration is built, so a deployment that is wrong never reaches a
// database read or a caller. The three that matter most are the ones a wrong
// value would silently mis-serve with: a database URL that is another
// installation's (the pair with the Firebase project, which is what a copied
// environment looks like), an audience that is not this service's URL (a
// service that admits nobody, or the wrong somebody), and a job account table
// that cannot be derived (a service whose account names do not exist).
func TestCredentialsServiceConfigRefusesAMalformedVariable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   map[string]string
		names []string // the variables the refusal must name
	}{
		{
			name:  "the database URL is not one",
			set:   map[string]string{envRTDBURL: "aurora-fp-default-rtdb.firebaseio.com"},
			names: []string{envRTDBURL},
		},
		{
			name:  "the database URL has a path",
			set:   map[string]string{envRTDBURL: "https://aurora-fp-default-rtdb.firebaseio.com/fugaro"},
			names: []string{envRTDBURL},
		},
		{
			name:  "the database is another Firebase project's",
			set:   map[string]string{envRTDBURL: "https://other-default-rtdb.firebaseio.com"},
			names: []string{envRTDBURL, envFirebaseFP},
		},
		{
			name:  "the audience has no scheme",
			set:   map[string]string{envCredentialsServiceURL: "fugaro-credentials-abcde-uc.a.run.app"},
			names: []string{envCredentialsServiceURL},
		},
		{
			name:  "the audience is http",
			set:   map[string]string{envCredentialsServiceURL: "http://fugaro-credentials-abcde-uc.a.run.app"},
			names: []string{envCredentialsServiceURL},
		},
		{
			name:  "the audience carries a path",
			set:   map[string]string{envCredentialsServiceURL: "https://fugaro-credentials-abcde-uc.a.run.app/v1/messages"},
			names: []string{envCredentialsServiceURL},
		},
		{
			name:  "the job accounts are not JSON",
			set:   map[string]string{envJobAccounts: "acme/webapp api"},
			names: []string{envJobAccounts},
		},
		{
			name:  "the job accounts are an object",
			set:   map[string]string{envJobAccounts: `{"provider":"github"}`},
			names: []string{envJobAccounts},
		},
		{
			name:  "the job accounts are empty",
			set:   map[string]string{envJobAccounts: `[]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "a job has no workflow",
			set:   map[string]string{envJobAccounts: `[{"provider":"github","repo":"acme/webapp"}]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "a job carries a field nothing reads",
			set:   map[string]string{envJobAccounts: `[{"provider":"github","repo":"acme/webapp","workflow":"api","project":"aurora"}]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "a job's provider is not a provider kind",
			set:   map[string]string{envJobAccounts: `[{"provider":"GitHub","repo":"acme/webapp","workflow":"api"}]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "a job's repository cannot be named",
			set:   map[string]string{envJobAccounts: `[{"provider":"github","repo":"acme","workflow":"api"}]`},
			names: []string{envJobAccounts},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, n := range credentialsServiceEnvNames {
				t.Setenv(n, credentialsServeEnv[n])
			}
			for k, v := range tc.set {
				t.Setenv(k, v)
			}
			// No seam: every refusal below is made before the configuration
			// reaches Google, which is the property this pins -- a deployment
			// that is wrong costs nothing but the refusal.
			_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error (exit 1)", err)
			}
			for _, n := range tc.names {
				if !strings.Contains(err.Error(), n) {
					t.Fatalf("err = %v, want a refusal naming %s", err, n)
				}
			}
		})
	}
}

// TestCredentialsServiceRefusalsNeverCarryAValue: this process is the one that
// holds the bundle's values in memory, and every refusal below is a line an
// operator reads in a log, so none of them may quote what the environment
// held. The values here are shaped like a credential (and deliberately not
// like the names the installation really uses), and every refusal path is
// walked with them set: the variable that is wrong is the one that gets
// named, never the text that was in it, and never the text in the others.
func TestCredentialsServiceRefusalsNeverCarryAValue(t *testing.T) {
	secret := func(n string) string { return "s3cr3t-" + strings.ToLower(strings.TrimPrefix(n, "FUGARO_")) }
	values := map[string]string{
		envProject:               secret(envProject),
		envGCPProject:            secret(envGCPProject),
		envRegion:                secret(envRegion),
		envRTDBURL:               "https://" + secret(envFirebaseFP) + "-default-rtdb.firebaseio.com",
		envFirebaseFP:            secret(envFirebaseFP),
		envCredentialsServiceURL: "https://" + secret(envCredentialsServiceURL) + ".run.app",
		envJobAccounts:           `[{"provider":"github","repo":"` + secret("repo") + `","workflow":"api"}]`,
	}
	for _, tc := range []struct {
		name string
		set  map[string]string
	}{
		{"every name is missing", map[string]string{
			envProject: "", envGCPProject: "", envRegion: "", envRTDBURL: "",
			envFirebaseFP: "", envCredentialsServiceURL: "", envJobAccounts: "",
		}},
		{"the database URL is not one", map[string]string{envRTDBURL: "s3cr3t-db"}},
		{"the database is another project's", map[string]string{envRTDBURL: "https://s3cr3t-other-default-rtdb.firebaseio.com"}},
		{"the audience is malformed", map[string]string{envCredentialsServiceURL: "s3cr3t-url"}},
		{"the job accounts are malformed", map[string]string{envJobAccounts: "s3cr3t-jobs"}},
		{"a job cannot be derived", map[string]string{envJobAccounts: `[{"provider":"github","repo":"s3cr3t-nope","workflow":"api"}]`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for n, v := range values {
				t.Setenv(n, v)
			}
			for n, v := range tc.set {
				t.Setenv(n, v)
			}
			_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
			if err == nil {
				t.Fatal("want a refusal")
			}
			for n, v := range values {
				if v == "" {
					continue
				}
				if strings.Contains(err.Error(), v) {
					t.Errorf("err = %v, which carries the value of %s", err, n)
				}
			}
		})
	}
}

// TestCredentialsServiceEnvIsTheDeploymentsOwn: the seven names this command
// reads are the seven credentials.tf sets on the service's container, and
// neither side may move without the other. The direction that matters is the
// module setting a name this command does not read: an operator reading
// credentials.tf would believe the fact was in effect while the service
// refused to start without it (or, worse, served with a value it never read).
// The other direction is a name this command reads that nothing sets, which is
// a fact the service silently does not have.
//
// internal/infra/tf's credentialsServiceEnv is the third side of this, and it
// is held to the module by that package's own test (the cover's allow-list
// refuses a container that sets a name outside it, which is what makes the
// module's list a boundary rather than a convention).
func TestCredentialsServiceEnvIsTheDeploymentsOwn(t *testing.T) {
	tf := readRepoFile(t, "deploy/terraform/gcp/modules/installation/credentials.tf")
	inModule := map[string]bool{}
	for _, m := range regexp.MustCompile(`name\s*=\s*"(FUGARO_[A-Z0-9_]+)"`).FindAllStringSubmatch(tf, -1) {
		inModule[m[1]] = true
	}
	for _, n := range credentialsServiceEnvNames {
		if !inModule[n] {
			t.Errorf("this command reads %s, which credentials.tf does not set on the service: the service would refuse to start on a deployment that is right", n)
		}
		delete(inModule, n)
	}
	for n := range inModule {
		t.Errorf("credentials.tf sets %s, which this command does not read: an operator would believe the fact was in effect and the service would refuse to start without it", n)
	}
}

// serveForTest runs the host's own server -- credentialsHTTPServer over a real
// listener, driven by serveUntilStop -- with a test's handler in place of the
// service's, and returns its address and the server object. Every protocol test
// below is written against this and not against httptest.NewServer, because the
// object they pin is the one the deployment is served by: a protocol set on the
// standard library's own test server would be a different server.
func serveForTest(t *testing.T, h http.Handler) (string, *http.Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := credentialsHTTPServer(ctx, h, io.Discard)
	done := make(chan error, 1)
	go func() { done <- serveUntilStop(ctx, ln, srv, io.Discard) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("the host did not stop")
		}
	})
	return "http://" + ln.Addr().String(), srv
}

// h2cClient is a caller that speaks HTTP/2 to a cleartext address with prior
// knowledge: it opens the connection, sends the HTTP/2 preface and no TLS
// handshake at all. That is what Cloud Run's front end does to the container
// once the service's port is named h2c, and it is why the service must serve
// unencrypted HTTP/2 rather than merely announce it.
//
// Only HTTP/2 is offered, so a server that answers over HTTP/1.1 cannot pass
// by negotiation: the client would fail before any request was made.
func h2cClient() *http.Client {
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	return &http.Client{Transport: &http.Transport{Protocols: &protocols}}
}

// TestCredentialsServeSpeaksH2C is what B1.0 exists for (owner-run Check 190,
// 2026-10-30): a 33 MiB pack over HTTP/1.1 was cut by the platform's front end
// with a 413 before the service saw four bytes of it, and the front end speaks
// HTTP/2 cleartext to a container whose port is named h2c. So the server this
// command builds must answer an HTTP/2 client that does no TLS at all -- and
// answer it AS HTTP/2, because a server that merely ignored the preface would
// be serving HTTP/1.1 to a front end that has already committed to h2c.
func TestCredentialsServeSpeaksH2C(t *testing.T) {
	addr, _ := serveForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "answered over HTTP/%d.%d", r.ProtoMajor, r.ProtoMinor)
	}))
	resp, err := h2cClient().Get(addr + "/v1/messages")
	if err != nil {
		t.Fatalf("an h2c call failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the h2c call is %d %q, want 200", resp.StatusCode, body)
	}
	if resp.ProtoMajor != 2 {
		t.Fatalf("the answer is HTTP/%d.%d (%q), want HTTP/2: the front end speaks h2c to this container, and a large push is cut at the HTTP/1 limit otherwise", resp.ProtoMajor, resp.ProtoMinor, body)
	}
}

// TestCredentialsServeStillSpeaksHTTP1 is the other half of the protocol set,
// and the half that keeps this task from halting an installation: every caller
// the service has today speaks HTTP/1.1 (the runner's gateway is a plain
// net/http client), so a server that answered only an h2c client would refuse
// every model call. The client below offers HTTP/1.1 alone, so h2c cannot carry
// this request by negotiation: what it pins is that the same listener still
// serves the older protocol, and that the h2c addition did not replace it.
func TestCredentialsServeStillSpeaksHTTP1(t *testing.T) {
	addr, srv := serveForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "answered over HTTP/%d.%d", r.ProtoMajor, r.ProtoMinor)
	}))
	// The protocol set is stated, not inferred from an answer: both protocols
	// on, TLS-based HTTP/2 off (this process terminates no TLS; Cloud Run's
	// front end does, and it speaks h2c to the container).
	if srv.Protocols == nil || !srv.Protocols.HTTP1() || !srv.Protocols.UnencryptedHTTP2() {
		t.Fatalf("the host's protocols = %+v, want HTTP/1.1 and unencrypted HTTP/2", srv.Protocols)
	}
	if srv.Protocols.HTTP2() {
		t.Error("the host offers TLS-based HTTP/2: it serves no TLS, and the front end's h2c is the unencrypted protocol")
	}
	// The HTTP/2 side of the server is the block net/http reads when it builds
	// the h2 handler, and neither number in it may fall back to a default: Go's
	// own MaxConcurrentStreams is 250 (net/http/internal/http2/server.go, whose
	// comment there is a TODO to make it 100 "as the GFE seems to"), and its
	// WriteByteTimeout default of 0 would let a peer that stopped reading hold a
	// goroutine and an upstream connection carrying a model credential for as
	// long as the route's own timeout allows. Read off the server object because
	// both are invisible to a client that behaves, and written as literals
	// rather than as the constants: the two numbers are the decision, and a test
	// that compared them to the constants would follow a change of one
	// silently.
	if srv.HTTP2 == nil {
		t.Fatal("the host has no HTTP2 config: the h2c server takes net/http's defaults for the stream count and the write bound")
	}
	if srv.HTTP2.MaxConcurrentStreams != 100 {
		t.Errorf("the host allows %d concurrent streams, want 100, the limit the front end in front of it allows a caller", srv.HTTP2.MaxConcurrentStreams)
	}
	if srv.HTTP2.WriteByteTimeout != 2*time.Minute {
		t.Errorf("the host's write-byte timeout = %v, want 2m: the bound on a peer that has stopped reading, not on a stream still moving", srv.HTTP2.WriteByteTimeout)
	}
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	client := &http.Client{Transport: &http.Transport{Protocols: &protocols}}
	resp, err := client.Get(addr + "/v1/messages")
	if err != nil {
		t.Fatalf("an HTTP/1.1 call failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.ProtoMajor != 1 {
		t.Fatalf("the HTTP/1.1 call is %d over HTTP/%d.%d (%q), want 200 over HTTP/1", resp.StatusCode, resp.ProtoMajor, resp.ProtoMinor, body)
	}
}

// largeBodyReader produces n bytes of a repeating pattern without ever holding
// them, and counts how many it has handed out: what the test below needs is a
// body big enough to matter that the test itself never allocates (a 40 MiB
// []byte here would make the test's own footprint the largest thing in the
// room, which is the measurement the assertion below turns into a claim about
// the server).
type largeBodyReader struct {
	left int64
	next byte
	sent *atomic.Int64
}

func (r *largeBodyReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > r.left {
		n = r.left
	}
	for i := int64(0); i < n; i++ {
		r.next++
		p[i] = r.next
	}
	r.left -= n
	r.sent.Add(n)
	return int(n), nil
}

// TestCredentialsServeH2CStreamsALargeBody is B1.0's own claim, measured: a
// 40 MiB body (a pack larger than the 33 MiB one Check 190 lost) reaches the
// handler through an h2c connection, and it reaches it as a stream rather than
// as a buffer.
//
// The assertion is about WHEN the handler sees the body, and it is the one a
// buffering front end cannot pass: the client counts every byte it has handed
// to its transport, and the handler records that count as it reads its first
// chunk. HTTP/2 flow control bounds how far a truthful peer may run ahead of a
// handler that has not read yet, so a server that streams shows a count in the
// low megabytes; a server that reads the whole body into memory before it calls
// the handler shows 40 MiB there, which is exactly what the front end did to
// Check 190's push (it answered 413 having read the body it would not stream).
//
// What is deliberately not asserted is the process's heap: a delta measured
// around a transfer is a GC-timing observation and not a property of this
// server, while the count above is deterministic. The handler's own buffer is
// 32 KiB and the total is exact, so a body that arrived truncated (which is
// what a cut at the HTTP/1 limit looks like) fails here too.
func TestCredentialsServeH2CStreamsALargeBody(t *testing.T) {
	const body = 40 << 20
	var sent, read, sentAtFirstRead atomic.Int64
	addr, _ := serveForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 32<<10)
		first := true
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				if first {
					sentAtFirstRead.Store(sent.Load())
					first = false
				}
				read.Add(int64(n))
			}
			if err != nil {
				break
			}
		}
		io.WriteString(w, "read")
	}))
	src := &largeBodyReader{left: body, sent: &sent}
	resp, err := h2cClient().Post(addr+"/v1/messages", "application/octet-stream", src)
	if err != nil {
		t.Fatalf("an h2c push failed: %v", err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.ProtoMajor != 2 {
		t.Fatalf("the push is %d over HTTP/%d.%d (%q), want 200 over HTTP/2", resp.StatusCode, resp.ProtoMajor, resp.ProtoMinor, answer)
	}
	if got := read.Load(); got != body {
		t.Fatalf("the handler read %d bytes, want %d: a body that arrives short is what a cut at the HTTP/1 limit looks like", got, body)
	}
	if got := sentAtFirstRead.Load(); got >= body/4 {
		t.Fatalf("the handler's first chunk arrived after the client had handed over %d of %d bytes: this server read the whole body before it served it, which is the behaviour that lost Check 190's 33 MiB push", got, body)
	}
}

// TestCredentialsServePort: PORT is the platform's, and a value that is not a
// port is a deployment that is wrong -- refused by name rather than defaulted,
// because a service listening where the front end does not reach it looks
// started and answers nothing.
func TestCredentialsServePort(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		bad      bool
	}{
		{in: "", want: defaultServePort}, // Cloud Run always sets it; a developer's run does not
		{in: "8080", want: "8080"},
		{in: "1", want: "1"},
		{in: "65535", want: "65535"},
		{in: "0", bad: true},
		{in: "65536", bad: true},
		{in: "-1", bad: true},
		{in: "http", bad: true},
		{in: "8080 ", bad: true}, // strconv.Atoi refuses the space; a port is digits
	} {
		got, err := servePort(func(string) string { return tc.in })
		switch {
		case tc.bad && err == nil:
			t.Errorf("servePort(%q) = %q, want a refusal", tc.in, got)
		case tc.bad:
			if ExitCode(err) != ExitUserError {
				t.Errorf("servePort(%q) err = %v, want a user error", tc.in, err)
			}
		case err != nil:
			t.Errorf("servePort(%q): %v", tc.in, err)
		case got != tc.want:
			t.Errorf("servePort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCredentialsServeHostStopsOnSignal drives the host's own machinery: a
// real listener, a real request and a real shutdown, with the handler of a
// test rather than the service's (the service needs the deployment's facts
// above, and none of them is what this asserts). What it pins is what the
// owner asked this task for -- the port the platform gives (PORT), the bounds
// a long-lived streamed call allows, and a SIGTERM that drains instead of
// dying -- and it pins them on the object the service is served by, so a
// timeout added to it fails here.
func TestCredentialsServeHostStopsOnSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs bytes.Buffer
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := credentialsHTTPServer(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "answered")
	}), &logs)
	// The bounds, stated as the values and not as "non-zero": a
	// ReadHeaderTimeout is what stops a caller that connects and says nothing
	// from holding a connection for ever, and a WriteTimeout (or a
	// ReadTimeout) would cut a model call in flight, which is a run that fails
	// for a reason nothing in its record explains.
	if srv.ReadHeaderTimeout != serveReadHeaderTimeout || srv.IdleTimeout != serveIdleTimeout {
		t.Errorf("host bounds = read-header %s, idle %s, want %s and %s", srv.ReadHeaderTimeout, srv.IdleTimeout, serveReadHeaderTimeout, serveIdleTimeout)
	}
	if srv.WriteTimeout != 0 || srv.ReadTimeout != 0 {
		t.Errorf("the host sets WriteTimeout %s / ReadTimeout %s: a proxied streaming call is long-lived and either would cut it", srv.WriteTimeout, srv.ReadTimeout)
	}
	done := make(chan error, 1)
	go func() { done <- serveUntilStop(ctx, ln, srv, &logs) }()
	// The listener is serving: a request is answered before the signal.
	resp, err := http.Get("http://" + ln.Addr().String() + "/v1/messages")
	if err != nil {
		t.Fatalf("the host did not answer: %v\n%s", err, logs.String())
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "answered" {
		t.Fatalf("host answered %d %q", resp.StatusCode, body)
	}
	// The signal: the context ends, and the host returns with no error -- a
	// platform that is replacing this revision is not a failure.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveUntilStop: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serveUntilStop did not return after the signal")
	}
	if !strings.Contains(logs.String(), "listening on") {
		t.Errorf("the host's own line does not say what it listens on: %q", logs.String())
	}
}

// TestCredentialsServeRefusesAnUnbuildableConfiguration: NewServer names the
// piece a configuration is missing (credsvc.NewServer's own refusals), and the
// host turns that into a user error -- a revision whose deployment is wrong
// must fail to start, not serve a service that answers every call with a
// refusal.
func TestCredentialsServeRefusesAnUnbuildableConfiguration(t *testing.T) {
	err := hostCredentialsService(context.Background(), credsvc.Config{}, func(string) string { return "" }, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	if !strings.Contains(err.Error(), "verifier") {
		t.Fatalf("err = %v, want NewServer's own refusal (the missing verifier)", err)
	}
}

// GODEBUG=http2debug makes Go print every HTTP request it handles, headers
// included, to stderr -- and this process handles the calls that carry a
// model credential. The refusal is the one every command that touches a
// credential makes (refuseHTTP2Debug), and it comes before the environment is
// read, so nothing at all happens under it.
func TestCredentialsServeRefusesHTTP2Debug(t *testing.T) {
	t.Setenv("GODEBUG", "http2debug=2")
	_, _, err := execute(t, "credentials", "serve")
	if err == nil || !strings.Contains(err.Error(), "http2debug") {
		t.Fatalf("err = %v, want the http2debug refusal", err)
	}
}

// TestCredentialsServeRefusesHTTP2DebugStill is the same refusal held against
// the h2c path this task adds, and it is the test that has to exist because the
// new path is the one http2debug would be loudest about: at 2 it prints HTTP/2
// frames, and the frames of a proxied call are a model credential and, from
// B1.5, the credential a git push carries. So the refusal has to be true of
// the process and not merely of the HTTP/1.1 branch of it.
//
// What "still" means here is the order, and it is asserted from both ends: the
// refusal comes before the environment is read (so an EMPTY environment is
// refused for http2debug and not for its first missing variable), and it comes
// before anything is served (so a COMPLETE environment -- the one that builds a
// configuration NewServer accepts -- is refused too, with the h2c server never
// built). The context is bounded so that a version of this code that served
// instead of refusing fails the test rather than hanging it.
func TestCredentialsServeRefusesHTTP2DebugStill(t *testing.T) {
	t.Setenv("GODEBUG", "http2debug=2")
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{name: "empty", env: map[string]string{}},
		{name: "complete", env: credentialsServeEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"GODEBUG": "http2debug=2"}
			for k, v := range tc.env {
				env[k] = v
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := serveCredentials(ctx, newCredentialsServeCmd(), func(k string) string { return env[k] })
			if err == nil || !strings.Contains(err.Error(), "http2debug") {
				t.Fatalf("err = %v, want the http2debug refusal: a process that serves the h2c path prints the frames of a call that carries a credential", err)
			}
		})
	}
}
