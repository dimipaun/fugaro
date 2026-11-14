// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// `fugaro licence renew --job` (plan K4.1): the installation's own renewal and
// revocation job, the half of K4.2 that is code. K4.2 applies the Scheduler job
// and its secret; this is what that job runs.
//
// It is the loop the specification describes: read the project's key and the
// renewal secret that renews it, ask the licence server's status route once a
// check_interval has passed, ask its renewal route from renew_before_days, and
// write the next key beside the old one. It is not `licence refresh`, and the
// differences are the ones a timer makes:
//
//   - nobody is watching, so nothing here fails over something a person cannot
//     act on tonight: a server that cannot be reached is a check recorded as
//     unreachable and a line on the job's log, and a key that is revoked is a
//     line and a record rather than a refusal. The installation's state is the
//     object, and `fugaro licence status` reads it there. The one failure is a
//     store or a container that cannot be written at all, which is a broken
//     installation and not a licence server's answer.
//   - it runs on every installation with a coordinator (K4.2's condition), and
//     an installation with no key is the ordinary state of one that never
//     bought a licence: the command no-ops and says so.
//   - it must never be the thing that revokes a key: only an explicit answer
//     from the server revokes (D2140), and a blocked route to the server is a
//     warning to admins after thirty days of it rather than a licence that
//     lapses.
//
// A renewal's two writes are made in the order `refresh` makes them — the new
// secret version first, then the key — because that is the order that leaves a
// pair an installation can recover from rather than one it cannot (see
// licenceHalfWritten in licence.go). What the job adds is the other half of
// that: a stored key whose hash matches no stored secret is a crash between the
// two writes, so it proves itself with the version before the newest one and
// the pair is completed rather than abandoned (licenceSecretVersions).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/api/googleapi"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/ee/keyservice"
	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/ee/licence/project"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The two limits the job's own arithmetic rests on, and nothing else here is a
// tunable.
const (
	// licenceRevocationSilence is how long an installation may go without an
	// answer from the licence server before the job says so out loud: D2140's
	// thirty days. Silence never revokes (D2140), and a block that lasts is
	// exactly how a revocation stops reaching an installation — so an
	// installation that cannot verify one is a fact somebody has to see, and
	// the job's log is where they see it today.
	licenceRevocationSilence = 30 * 24 * time.Hour
	// licenceSecretVersions is how many versions of the renewal container the
	// job will prove a key with: the newest, and the one before it. Two is what
	// a rotation spans — the installed key's hash names the secret before the
	// rotation, the successor's names the one after it, and a crash between the
	// two writes leaves the pair one version apart (licenceHalfWritten). A
	// container whose newest two versions name neither is a pair this job
	// cannot renew, and reading further back would only test secrets the
	// licence server has already refused.
	licenceSecretVersions = 2
	// licenceVersionEnabled is the state Secret Manager lists a version in that
	// is still readable: a destroyed version is listed for a while after, and a
	// disabled one resolves to nothing.
	licenceVersionEnabled = "ENABLED"
)

func init() { addLicenceSubcommand("renew", newLicenceRenewCmd) }

// licenceRenewOptions is `fugaro licence renew`'s own flags.
type licenceRenewOptions struct {
	cloud fcli.CloudFlags
	job   bool
}

func newLicenceRenewCmd() *cobra.Command {
	var o licenceRenewOptions
	cmd := &cobra.Command{
		Use:   "renew",
		Short: "Run the installation's licence check and renewal, as its job does (Subscription Edition)",
		Long: `Run the installation's licence check and renewal.

This is the command K4.2's Scheduler job runs on the installation's timer, and
` + "`--job`" + ` is what says so: it is the job's own verb, and running it by hand is
not what a person means — ` + "`fugaro licence refresh`" + ` renews the key they are
watching, now.

What the job does, in the order it does it: it reads the project's key and the
renewal secret that renews it, asks the licence server's status route once the
key's own ` + "`check_interval`" + ` has passed, asks its renewal route from
` + "`renew_before_days`" + `, and writes the next key beside the old one (the new secret
version first, then the key). It records the check — the answer, whether it
reached the server, and how many checks in a row have not — and warns after
thirty days without an answer, because that is how long an installation may be
unable to verify a revocation at all (D2140).

It never revokes: only an explicit answer from the licence server does, so a
server that cannot be reached leaves the key exactly as it was. An offline key
contacts nothing at all, an installation with no key does nothing, and a key the
server answers revoked is recorded and left in place — nothing is deleted.

It is refused inside a coding agent's session, before anything is read, as every
cloud write is. No line of its output carries the key or the renewal secret.`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fcli.UserErr("fugaro licence renew takes no argument, and the argument is not echoed: this position is one a pasted key can land in. Nothing was read")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return licenceRenew(cmd, &o) },
	}
	cmd.Flags().BoolVar(&o.job, "job", false, "run as the installation's renewal job, which is the only way this verb runs")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

func licenceRenew(cmd *cobra.Command, o *licenceRenewOptions) error {
	ctx := cmd.Context()
	if !o.job {
		// The verb exists for one caller, and that caller passes a flag rather
		// than being guessed at from an environment: a job is a Cloud Run
		// execution, and a run's own container is a Cloud Run execution too.
		return fcli.UserErr("fugaro licence renew is the installation's renewal job and runs nowhere else: the job's own command is `fugaro licence renew --job`, and a person renews the key they are watching with `fugaro licence refresh`")
	}
	if err := fcli.AgentSessionRefusal(os.Getenv, "nothing was renewed: `fugaro licence renew` is the installation's renewal job and writes the installation's own key, which is the job's to do and not a coding agent's"); err != nil {
		return err
	}
	cloud, err := fcli.OpenCloud(ctx, &o.cloud)
	if err != nil {
		return err
	}
	defer cloud.Close()
	lc := cloud.Config()
	if licenceFirebaseProject(lc) == "" {
		return licenceNoWorkspace(lc)
	}
	store := project.New(cloud.RunsBucket())
	now := licenceNow()
	// The project's own key and only the project's: the job writes what it is
	// given into the project, and a machine's fallback key is not the
	// installation's licence (D2137).
	applied, err := store.Active(ctx, now, licenceRing, licenceFirebaseProject(lc), nil)
	switch {
	case errors.Is(err, licence.ErrNoKey):
		// K4.2 applies the job to every installation that has a coordinator, so
		// this is the ordinary state of one that never bought a licence rather
		// than a failure: the job says what would install one and stops.
		fmt.Fprintf(cmd.OutOrStdout(), "no licence key is installed in project %s (%s): there is nothing to check and nothing to renew, and `fugaro licence set` installs a key\n",
			safetext.Field(lc.Name), project.Object)
		return nil
	case err != nil:
		return licenceObjectErr(err, "")
	}
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	claims := applied.Claims
	switch {
	case applied.Revoked:
		// The server has answered that this key is revoked, and the record is
		// already written: there is nothing to check it against and nothing to
		// renew, so the job stops talking to the server until a person installs
		// a new key (nothing here deletes the old one, which is the record of
		// what was revoked).
		fmt.Fprintf(out, "key %s is revoked, so project %s runs on Free until a new key is installed (`fugaro licence set`): nothing was checked, nothing was renewed, and nothing has been deleted\n",
			safetext.Field(claims.KeyID), safetext.Field(lc.Name))
		return nil
	case claims.RenewalMode == licence.RenewalNone:
		// An offline key contacts nothing at all (spec section 7), which
		// includes the container the installation's last key left a version in.
		fmt.Fprintf(out, "this key is offline (renewal_mode: none): nothing contacts the licence server. The key is %s until %s, and a new key is pasted in by hand with `fugaro licence set`\n",
			safetext.Field(string(applied.State)), claims.PaidThrough.UTC().Format(time.RFC3339))
		return nil
	}
	view, err := store.View(ctx, now, licenceRing, licenceFirebaseProject(lc), nil)
	if err != nil {
		return licenceObjectErr(err, "")
	}
	interval, err := licenceCheckInterval(claims)
	if err != nil {
		return err
	}
	due := licenceCheckDue(view, interval, claims.KeyID, now)
	inWindow := licenceRenewalWindowOpen(claims, now)
	if !due && !inWindow {
		fmt.Fprintf(out, "nothing to do: key %s is paid through %s, its renewal window opens %s, and its last check was %s\n",
			safetext.Field(claims.KeyID), claims.PaidThrough.UTC().Format(time.RFC3339),
			licenceRenewalWindow(claims).UTC().Format(time.RFC3339), licenceLastCheck(view))
		return nil
	}
	secrets, err := openLicenceSecrets(ctx, lc)
	if err != nil {
		return err
	}
	if err := secrets.check(ctx); err != nil {
		return err
	}
	proof, newest, err := secrets.renewalProof(ctx, claims)
	if err != nil {
		if errors.Is(err, errNoRenewalSecret) {
			return fcli.UserErr("the installation's renewal secret container %s holds no version, so key %s cannot prove itself to the licence server: `fugaro licence set` writes the secret that came with the key",
				safetext.Field(licenceRenewalSecretID(lc.Name)), safetext.Field(claims.KeyID))
		}
		return err
	}
	if proof == nil {
		return fcli.UserErr("the renewal secret this installation holds is not the one key %s renews with: the key carries the digest of its own secret, and the container %s's newest %d versions carry neither it nor the one beside it. This is the state a renewal interrupted between its two writes leaves, and `fugaro licence set` with the key and the renewal secret this installation was issued writes the pair again",
			safetext.Field(claims.KeyID), safetext.Field(licenceRenewalSecretID(lc.Name)), licenceSecretVersions)
	}
	call := keyservice.RenewalCall{KeyID: claims.KeyID, WorkspaceID: claims.WorkspaceID}

	renewNow := inWindow
	if due {
		call.RenewalSecret = string(proof)
		answer, err := licenceStatusRequest(ctx, call, string(proof))
		if err != nil {
			return licenceCheckFailed(ctx, errOut, store, claims, view, interval, now, err)
		}
		switch answer.Status {
		case keyservice.StatusRevoked:
			if err := store.RecordCheck(ctx, now, project.CheckRevoked, claims.KeyID); err != nil {
				return fmt.Errorf("the licence server answered that key %s is revoked, and the record of it could not be written, so `fugaro licence status` reads the key as active until a check lands: %w",
					safetext.Field(claims.KeyID), licenceObjectErr(err, ""))
			}
			fmt.Fprintf(out, "the licence server answered that key %s is revoked: project %s runs on Free from now on, nothing has been deleted, and a new key restores everything (`fugaro licence set`)\n",
				safetext.Field(claims.KeyID), safetext.Field(lc.Name))
			return nil
		case keyservice.StatusOK:
			if err := store.RecordCheck(ctx, now, project.CheckOK, claims.KeyID); err != nil {
				return fmt.Errorf("the licence server answered that key %s is good, and the record of it could not be written, so the installation's check streak and last answer are the ones `fugaro licence status` reads: %w",
					safetext.Field(claims.KeyID), licenceObjectErr(err, ""))
			}
			fmt.Fprintf(out, "checked: the licence server answered that key %s is good\n", safetext.Field(claims.KeyID))
			// D2200: the store may hold a key issued after this one — the
			// successor of a rotation this installation has not read yet — and
			// then there is a key to fetch whatever the window says.
			renewNow = renewNow || answer.NewerKey
		default:
			// A status is a field of a body, and the body is the one place a
			// key this process never held can come back from
			// (licenceStatusRequest): the clip below bounds it and removes
			// nothing from it, because the front of a key is the key.
			return licenceCheckFailed(ctx, errOut, store, claims, view, interval, now,
				fcli.RemoteErr(fmt.Errorf("the licence server answered a status this Fugaro does not read (%s)",
					licenceRedact(fcli.ClipRunes(answer.Status, 40)))))
		}
	}
	if !renewNow {
		return nil
	}

	call.RenewalSecret = string(proof)
	answer, err := licenceRenewRequest(ctx, call, string(proof))
	if err != nil {
		// An error is not an answer: a renewal that failed changes nothing, and
		// nothing about it revokes (D2140). The job's log carries it, and the
		// next run asks again.
		fmt.Fprintf(errOut, "note: the licence server did not answer the renewal of key %s, so nothing was renewed and the key is unchanged: %s\n",
			safetext.Field(claims.KeyID), err.Error())
		return nil
	}
	switch answer.Status {
	case keyservice.StatusRevoked:
		if err := store.RecordCheck(ctx, now, project.CheckRevoked, claims.KeyID); err != nil {
			return fmt.Errorf("the licence server answered that key %s is revoked, and the record of it could not be written, so `fugaro licence status` reads the key as active until a check lands: %w",
				safetext.Field(claims.KeyID), licenceObjectErr(err, ""))
		}
		fmt.Fprintf(out, "the licence server answered that key %s is revoked: project %s runs on Free from now on, nothing has been deleted, and a new key restores everything (`fugaro licence set`)\n",
			safetext.Field(claims.KeyID), safetext.Field(lc.Name))
		return nil
	case keyservice.StatusOK:
	default:
		fmt.Fprintf(errOut, "note: the licence server answered the renewal with a status this Fugaro does not read (%s), so nothing was renewed\n",
			licenceRedact(fcli.ClipRunes(answer.Status, 40)))
		return nil
	}
	if answer.Key == "" {
		fmt.Fprintf(out, "nothing to renew yet: the key is paid through %s, and its own renewal window opens %d days before that\n",
			claims.PaidThrough.UTC().Format(time.RFC3339), claims.RenewBeforeDays)
		return nil
	}
	next, state, err := licence.Verify([]byte(answer.Key), now, licenceRing, licenceFirebaseProject(lc))
	if err != nil {
		// The key's own workspace_id is what Verify weighs against the
		// installation's Firebase project (TN2): a key for another workspace is
		// not one this installation may run on, whatever else it says.
		return fcli.UserErr("the licence server answered with a key this Fugaro does not accept, and nothing was written: %s",
			licenceRedact(err.Error(), answer.Key, answer.RenewalSecret, string(proof)))
	}
	secret := answer.RenewalSecret
	if secret == "" {
		// D2200: an answer that brings no secret keeps the one the installation
		// has, and the key it carries names that secret's digest.
		secret = string(proof)
	}
	if !licenceSecretMatches(next, secret) {
		return fcli.RemoteErr(fmt.Errorf("the licence server answered key %s and a renewal secret that is not the one it renews with, and nothing was written: the key carries the digest of another secret, so this installation would be left unable to renew again",
			safetext.Field(next.KeyID)))
	}
	if next.KeyID == claims.KeyID && !next.PaidThrough.After(claims.PaidThrough) {
		// D2200's second half: the entitlement's newest key can be the very key
		// this installation runs on — a reissue of the term that is running —
		// and a key the installation already holds is not a renewal. Writing it
		// would be a rotation's second secret for a key that did not change.
		fmt.Fprintf(out, "the licence server answered key %s, which is the key this installation already holds (paid through %s): nothing was written\n",
			safetext.Field(next.KeyID), next.PaidThrough.UTC().Format(time.RFC3339))
		return nil
	}
	if would, err := store.WouldApply(ctx, now, licenceRing, licenceFirebaseProject(lc), []byte(answer.Key)); err == nil {
		// The project keeps the later term (T26), so an answer that would not
		// be the key the installation applies would be written beside the key
		// it does not replace — and the renewal secret written with it would
		// leave the applied key unable to renew. The store is the judge and not
		// this command's arithmetic: `set` refuses the same shape.
		if would.Claims.KeyID != next.KeyID || next.PaidThrough.Before(would.Claims.PaidThrough) {
			return fcli.RemoteErr(fmt.Errorf("the licence server answered key %s (paid through %s) and this installation applies key %s (paid through %s): nothing was written, because the project keeps the later term and the answer would not be the key this installation runs on",
				safetext.Field(next.KeyID), next.PaidThrough.UTC().Format(time.RFC3339),
				safetext.Field(would.Claims.KeyID), would.Claims.PaidThrough.UTC().Format(time.RFC3339)))
		}
	}
	added := answer.RenewalSecret != "" && !bytes.Equal([]byte(answer.RenewalSecret), newest)
	if added {
		// The new secret first, then the key: the key that is installed names
		// the new secret's hash, so a key without it is a key that cannot renew
		// again, while a container one version ahead is completed by the run
		// that follows (renewalProof tries the version beside the newest).
		if err := secrets.add(ctx, []byte(answer.RenewalSecret)); err != nil {
			return err
		}
	}
	if err := store.Install(ctx, []byte(answer.Key)); err != nil {
		if added {
			return fcli.UserErr("%s. The project write failed with: %s",
				licenceHalfWritten(lc, "run `fugaro licence set` with the key and the renewal secret this installation was issued, which is the bundle that last worked: the job proves itself with the version beside the newest one, so the pair it completes is the one the server already minted"),
				licenceRedact(err.Error(), answer.Key, answer.RenewalSecret, string(proof)))
		}
		return licenceObjectErr(err, answer.Key)
	}
	fmt.Fprintf(out, "renewed: key %s is installed in project %s beside the key it replaces (%s)\n",
		safetext.Field(next.KeyID), safetext.Field(lc.Name), project.Object)
	licenceWriteClaims(out, next, state)
	if answer.RenewalSecret == "" {
		fmt.Fprintln(errOut, "note: the licence server answered the key without a new renewal secret, so this installation keeps the one it holds, and the key names that secret's digest (D2200)")
	}
	return nil
}

// licenceCheckFailed records a check that did not answer about the key, warns
// where the installation has been without an answer for thirty days, and
// answers nil: nothing here is a failure a person can act on tonight, and the
// record is what makes tomorrow's `fugaro licence status` true.
//
// The record's own write is the exception. An unreachable server leaves a job
// with one thing to do — write down that it could not reach it — and an
// installation whose object cannot be written is broken in a way nobody else
// will notice, so that failure is returned.
func licenceCheckFailed(ctx context.Context, w io.Writer, store *project.Store, claims licence.Claims, view project.View, interval time.Duration, now time.Time, cause error) error {
	if err := store.RecordCheck(ctx, now, project.CheckUnreachable, claims.KeyID); err != nil {
		return fmt.Errorf("the licence server could not be reached (%s), and the record of the check could not be written either, so `fugaro licence status` reads the last check the installation ran as its newest: %w",
			cause.Error(), licenceObjectErr(err, ""))
	}
	fmt.Fprintf(w, "note: the check of key %s did not complete, so the key keeps its state and nothing was renewed (only an explicit answer revokes, D2140): %s\n",
		safetext.Field(claims.KeyID), cause.Error())
	// D2140's warning, counted from the last answer the server gave rather than
	// from the streak: an installation whose route to the licence server has
	// been blocked for a month cannot verify a revocation at all, and that is
	// the one thing about a licence this job can see and nobody else can.
	if silence := licenceSilence(view, interval, now); silence >= licenceRevocationSilence {
		fmt.Fprintf(w, "warning: cannot verify revocation: the licence server has not answered a check of this installation for %d days — key %s is still good and still running, but a revocation would not reach it. An admin should look at the installation's route to the licence server (`fugaro licence status` reads the last check it ran)\n",
			int(silence.Hours()/24), safetext.Field(claims.KeyID))
	}
	return nil
}

// licenceStatusRequest calls the licence server's status route once (K2.3's
// other endpoint) and answers what it said. It is licenceRenewRequest's twin in
// licence.go, with the same two rules: the renewal secret is the whole of the
// proof the server asks for, so it travels over TLS and nowhere else, and every
// failure has been redacted of the secret and of whatever the server said back.
func licenceStatusRequest(ctx context.Context, call keyservice.RenewalCall, secret string) (keyservice.Answer, error) {
	body, err := json.Marshal(call)
	if err != nil {
		return keyservice.Answer{}, fcli.RemoteErr(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(licenceServerURL, "/")+keyservice.RouteStatus, bytes.NewReader(body))
	if err != nil {
		return keyservice.Answer{}, fcli.RemoteErr(errors.New(licenceRedact(err.Error(), secret)))
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Timeout: licenceRenewTimeout,
		// A redirect is a second host to carry the secret to, and a check
		// travels the same route as a renewal for the same reason.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return keyservice.Answer{}, fcli.RemoteErr(errors.New(licenceRedact(err.Error(), secret)))
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, licenceMaxAnswerBytes+1))
	if err != nil {
		return keyservice.Answer{}, fcli.RemoteErr(errors.New(licenceRedact(err.Error(), secret)))
	}
	if len(payload) > licenceMaxAnswerBytes {
		return keyservice.Answer{}, fcli.RemoteErr(fmt.Errorf("the licence server's answer is over %d bytes", licenceMaxAnswerBytes))
	}
	if resp.StatusCode == http.StatusForbidden {
		return keyservice.Answer{}, fcli.UserErr("the licence server refused this installation's renewal secret for key %s: the secret this installation holds is not the one that key renews with, or the key is bound to another workspace. `fugaro licence set`, with the key and the renewal secret this installation was issued, writes the pair",
			safetext.Field(call.KeyID))
	}
	if resp.StatusCode != http.StatusOK {
		return keyservice.Answer{}, fcli.RemoteErr(fmt.Errorf("the licence server answered HTTP %d: %s", resp.StatusCode, licenceRedact(string(payload), secret)))
	}
	var answer keyservice.Answer
	if err := json.Unmarshal(payload, &answer); err != nil {
		return keyservice.Answer{}, fcli.RemoteErr(errors.New(licenceRedact(err.Error(), secret)))
	}
	return answer, nil
}

// enabledVersions answers the container's enabled versions, newest first: the
// order the container is read in is the order a rotation happened in, which is
// what lets a key prove itself with the version beside the newest one.
func (s *licenceSecrets) enabledVersions(ctx context.Context) ([]int, error) {
	var numbers []int
	err := s.svc.Projects.Secrets.Versions.List(s.parent()).Pages(ctx, func(page *secretmanager.ListSecretVersionsResponse) error {
		for _, v := range page.Versions {
			if v.State != licenceVersionEnabled {
				continue
			}
			// The version's name is the last path segment of
			// projects/P/secrets/S/versions/N, and a name this build cannot
			// read is a container it must not guess about.
			n, err := strconv.Atoi(v.Name[strings.LastIndex(v.Name, "/")+1:])
			if err != nil {
				return fcli.RemoteErr(fmt.Errorf("secret %s has a version whose name is not a version number (%s)",
					safetext.Field(s.id), safetext.Field(v.Name)))
			}
			numbers = append(numbers, n)
		}
		return nil
	})
	if err != nil {
		var ge *googleapi.Error
		// A name this build cannot read is already the error the caller is
		// owed; everything else Secret Manager answered goes through apiErr,
		// which names the two an operator fixes at their own terminal.
		if errors.As(err, &ge) {
			return nil, s.apiErr("read", err, nil)
		}
		return nil, err
	}
	slices.Sort(numbers)
	slices.Reverse(numbers)
	return numbers, nil
}

// access reads one version's value. A version that is not there answers
// errNoRenewalSecret, which is the same legitimate state a container with no
// version is: the job's answer to both is that the key cannot prove itself.
func (s *licenceSecrets) access(ctx context.Context, version int) ([]byte, error) {
	r, err := s.svc.Projects.Secrets.Versions.Access(s.parent() + "/versions/" + strconv.Itoa(version)).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
			return nil, errNoRenewalSecret
		}
		return nil, s.apiErr("read", err, nil)
	}
	if r.Payload == nil || r.Payload.Data == "" {
		return nil, errNoRenewalSecret
	}
	data, err := base64.StdEncoding.DecodeString(r.Payload.Data)
	if err != nil {
		return nil, fcli.RemoteErr(fmt.Errorf("reading secret %s: the payload is not base64", safetext.Field(s.id)))
	}
	return data, nil
}

// renewalProof answers the value this key proves itself to the licence server
// with, and the value the container's newest version holds. The two differ in
// exactly one state, and it is the state this job exists to get out of: a
// renewal that wrote its secret and did not write its key leaves the newest
// version naming the successor, so the installed key proves itself with the
// version before it, which is still enabled because nothing in this package
// retires a version.
//
// A nil proof with a nil error is a container whose newest versions name
// neither the key's secret nor the one beside it: the caller names the command
// that writes the pair again.
func (s *licenceSecrets) renewalProof(ctx context.Context, claims licence.Claims) (proof, newest []byte, err error) {
	versions, err := s.enabledVersions(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(versions) == 0 {
		return nil, nil, errNoRenewalSecret
	}
	newest, err = s.access(ctx, versions[0])
	if err != nil {
		return nil, nil, err
	}
	for i, n := range versions[:min(len(versions), licenceSecretVersions)] {
		value := newest
		if i > 0 {
			if value, err = s.access(ctx, n); err != nil {
				return nil, nil, err
			}
		}
		if licenceSecretMatches(claims, string(value)) {
			return value, newest, nil
		}
	}
	return nil, newest, nil
}

// licenceCheckInterval is the key's own check cadence, and none at all where
// the key carries no interval or an empty one: an offline key is never checked
// (spec section 8), and a zero-length interval is a claim that would otherwise
// have the job ask on every run it makes.
//
// The claim has been verified by the time this reads it (Verify parses it with
// ParseISODuration), so a failure here is a claim set this build cannot read
// rather than a caller's mistake, and it is refused as one.
func licenceCheckInterval(claims licence.Claims) (time.Duration, error) {
	if claims.CheckInterval == nil {
		return 0, nil
	}
	interval, err := licence.ParseISODuration(*claims.CheckInterval)
	if err != nil {
		return 0, fcli.UserErr("the key's check_interval (%s) is not a duration this Fugaro reads, so the installation's checks cannot be scheduled: %v",
			safetext.Field(*claims.CheckInterval), err)
	}
	return interval, nil
}

// licenceCheckDue is whether the check is due: the key's own interval since the
// last check the installation recorded, and at once where the record is about
// another key — a key installed since the last check has never been checked at
// all, whatever figures the installation carries beside it.
func licenceCheckDue(view project.View, interval time.Duration, keyID string, now time.Time) bool {
	if interval <= 0 {
		return false
	}
	if !view.HasCheck || view.Check.KeyID != keyID {
		return true
	}
	return !now.Before(view.Check.At.Add(interval))
}

// licenceRenewalWindow is when the key's renewal window opens: renew_before_days
// ahead of the end of its term (spec section 6 step 5).
func licenceRenewalWindow(claims licence.Claims) time.Time {
	days := claims.RenewBeforeDays
	if days < 0 {
		days = 0
	}
	return claims.PaidThrough.Add(-time.Duration(days) * 24 * time.Hour)
}

// licenceRenewalWindowOpen is the window's own test, in the one place both the
// decision and the line the job prints read it from.
func licenceRenewalWindowOpen(claims licence.Claims, now time.Time) bool {
	return !now.Before(licenceRenewalWindow(claims))
}

// licenceLastCheck is the last check the installation ran, for a line that has
// to say why nothing was due.
func licenceLastCheck(view project.View) string {
	if !view.HasCheck {
		return "never"
	}
	return view.Check.At.UTC().Format(time.RFC3339)
}

// licenceSilence is how long this installation has now gone without an answer
// from the licence server, as of the check the caller is about to record: the
// span since the last answer where there is one, and otherwise the span the
// streak itself covers — the failures happened one check interval apart, and
// the record keeps no start for them.
func licenceSilence(view project.View, interval time.Duration, now time.Time) time.Duration {
	failures, last := 1, time.Time{}
	if view.HasCheck {
		failures, last = view.Check.Failures+1, view.Check.LastOK
	}
	if !last.IsZero() {
		return now.Sub(last)
	}
	return time.Duration(failures) * interval
}
