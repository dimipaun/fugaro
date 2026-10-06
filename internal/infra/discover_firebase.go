package infra

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"google.golang.org/api/apikeys/v2"
	firebasedatabase "google.golang.org/api/firebasedatabase/v1beta"
	iam "google.golang.org/api/iam/v1"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// The APIs DiscoverFirebase reads besides IAM.
const (
	serviceFirebaseDatabase = "firebasedatabase.googleapis.com"
	serviceAPIKeys          = "apikeys.googleapis.com"
)

// The marks of the Firebase root's singletons, exactly as the Firebase
// module sets them (deploy/terraform/gcp/modules/firebase). They are
// public, so they only stop an accidental adoption: the shape checks of
// DiscoverFirebase are what stop a squatter.
const (
	firebaseDBRegion    = "us-central1"
	firebaseDBType      = "DEFAULT_DATABASE"
	webKeyDisplayName   = "Fugaro run sign-in"
	signerDisplayName   = "Fugaro token signer"
	minterRoleTitle     = "Fugaro token minter"
	signerKeyTypeSystem = "SYSTEM_MANAGED"
)

// firebaseDBInstanceID is the default instance's ID in the Firebase
// project fp.
func firebaseDBInstanceID(fp string) string { return fp + "-default-rtdb" }

// signerDescription is the signer's description for the Fugaro project
// name, which a signer of another Fugaro project does not carry.
func signerDescription(name string) string {
	return "Signs the custom tokens of the Fugaro project " + name + "'s runs. Holds no roles."
}

// DiscoverFirebase finds the Firebase root's singletons that exist in the
// Firebase project (spec.Project) and are ours: the default Realtime
// Database instance, the web API key, the token signer account and the
// token minter role. Each that passes its mark and shape checks is
// imported; each that fails is refused, and every refusal is returned
// together. What does not exist (404, or its API disabled) is left for the
// plan to create.
//
// It only reads: it never reads the API key's string nor any key of the
// signer (it lists the signer's key metadata only). IAM members, project
// services and google_firebase_project are never imported: their creates
// succeed when they exist.
func DiscoverFirebase(ctx context.Context, c *Clients, spec FirebaseSpec) (Imports, error) {
	if c.FirebaseDB == nil {
		return Imports{}, errors.New("no Realtime Database management client (endpoints.firebase_database is not set)")
	}
	if c.APIKeys == nil {
		return Imports{}, errors.New("no API Keys client (endpoints.api_keys is not set)")
	}
	if c.IAM == nil {
		return Imports{}, errors.New("no IAM client")
	}
	if c.CRM == nil {
		return Imports{}, errors.New("no Resource Manager client")
	}
	// The Firebase project's number: only its own SERVICE_DISABLED reads
	// as absent, not one that names another project (the quota project's).
	num, err := ProjectNumber(ctx, c, spec.Project)
	if err != nil {
		return Imports{}, err
	}
	d := &discovery{ctx: ctx, c: c, project: spec.Project, number: num}
	for _, step := range []func(FirebaseSpec) error{d.firebaseDB, d.webKey, d.tokenSigner, d.minterRole} {
		if err := step(spec); err != nil {
			return Imports{}, err
		}
	}
	return d.result()
}

// firebaseDB adopts the project's default Realtime Database instance when
// it is the one the module defines: DEFAULT_DATABASE, in us-central1,
// named <fp>-default-rtdb, and ACTIVE. Its data is checked by DB.Check,
// which refuses data without our mark; an empty, unmarked instance is
// adopted (design decision 1).
func (d *discovery) firebaseDB(spec FirebaseSpec) error {
	wantID := firebaseDBInstanceID(d.project)
	var found []*firebasedatabase.DatabaseInstance
	call := d.c.FirebaseDB.Projects.Locations.Instances.List("projects/" + d.project + "/locations/-").Context(d.ctx)
	err := call.Pages(d.ctx, func(r *firebasedatabase.ListDatabaseInstancesResponse) error {
		found = append(found, r.Instances...)
		return nil
	})
	switch {
	case d.absent(err, serviceFirebaseDatabase):
		return nil
	case err != nil:
		return fmt.Errorf("listing the Realtime Database instances of project %s (fugaro init --firebase needs firebasedatabase.instances.list there): %w", d.project, err)
	}
	ok := false
	for _, i := range found {
		loc, id := instanceLocationID(i.Name)
		// The default instance, and anything else under our ID: either
		// would make the module's create fail, so each must be the one the
		// module defines.
		if i.Type != firebaseDBType && id != wantID {
			continue
		}
		where := "Realtime Database instance " + i.Name
		switch {
		case i.Type != firebaseDBType:
			d.refuse(fmt.Errorf("%s is a %s, not the project's %s: the Firebase project cannot host Fugaro's backend as the module defines it (the default instance %s in %s)", where, i.Type, firebaseDBType, wantID, firebaseDBRegion))
		case loc != firebaseDBRegion || id != wantID:
			d.refuse(fmt.Errorf("the Firebase project %s's default Realtime Database is %s (location %s, ID %s), not %s in %s: a default instance can neither move nor be renamed, and a project has only one, so this Firebase project cannot host Fugaro's backend as the module defines it; use another Firebase project",
				d.project, i.Name, loc, id, wantID, firebaseDBRegion))
		case i.State == "DISABLED":
			d.refuse(fmt.Errorf("%s is DISABLED, not ACTIVE: re-enable it in the Firebase console (Realtime Database, the database's menu), then rerun fugaro init --firebase %s", where, d.project))
		case i.State == "DELETED":
			d.refuse(fmt.Errorf("%s is DELETED, not ACTIVE: it is being deleted; wait until it is gone (the plan then creates it), then rerun fugaro init --firebase %s", where, d.project))
		case i.State != "ACTIVE":
			d.refuse(fmt.Errorf("%s is %s, not ACTIVE: rerun fugaro init --firebase %s once it is ACTIVE", where, cmp.Or(i.State, "in no state"), d.project))
		case i.DatabaseUrl == "":
			d.refuse(fmt.Errorf("%s has no database URL, so neither fugaro nor its runs could reach it; fugaro refuses to adopt it. Rerun fugaro init --firebase %s once the Firebase console shows its URL", where, d.project))
		default:
			ok = true
		}
	}
	if ok {
		d.add(importFirebaseDB, firebaseDBRegion, "", wantID)
	}
	return nil
}

// instanceLocationID splits projects/<p>/locations/<loc>/instances/<id>;
// both are empty when name has another shape.
func instanceLocationID(name string) (loc, id string) {
	p := strings.Split(name, "/")
	if len(p) != 6 || p[0] != "projects" || p[2] != "locations" || p[4] != "instances" {
		return "", ""
	}
	return p[3], p[5]
}

// webKey adopts the web API key when it carries our display name, is not
// deleted, is bound to no service account and its restrictions are
// exactly the module's. It reads the key's metadata only (keys.get), never
// its string.
func (d *discovery) webKey(spec FirebaseSpec) error {
	id := spec.Names.APIKey
	name := "projects/" + d.project + "/locations/global/keys/" + id
	k, err := d.c.APIKeys.Projects.Locations.Keys.Get(name).Context(d.ctx).Do()
	switch {
	case d.absent(err, serviceAPIKeys):
		return nil
	case err != nil:
		return fmt.Errorf("reading API key %s (fugaro init --firebase needs apikeys.keys.get there): %w", name, err)
	}
	where := "API key " + name
	switch {
	case k.DisplayName != webKeyDisplayName:
		d.refuse(&ForeignError{Resource: where, Found: "display name " + strconv.Quote(k.DisplayName), Want: "display name " + strconv.Quote(webKeyDisplayName)})
	case k.DeleteTime != "":
		d.refuse(fmt.Errorf("%s is deleted (since %s; a deleted key keeps its ID for 30 days), so it can be neither imported nor created: restore it with gcloud services api-keys undelete %s --project %s, then rerun fugaro init --firebase %s",
			where, k.DeleteTime, id, d.project, d.project))
	case k.ServiceAccountEmail != "":
		d.refuse(fmt.Errorf("%s is bound to service account %s, so it authenticates as that account; the web key is bound to none. fugaro refuses to adopt it", where, k.ServiceAccountEmail))
	default:
		if found, ok := webKeyRestrictions(k.Restrictions); !ok {
			d.refuse(fmt.Errorf("%s has these restrictions: %s. The web key's are exactly API targets %s, with no methods and no browser, server, Android or iOS restriction. It may be in use by something else, so fugaro refuses to adopt and narrow it: if it is Fugaro's, set exactly those restrictions on it (Credentials in the Google Cloud console), then rerun fugaro init --firebase %s",
				where, found, strings.Join(tf.APITargets(), " and "), d.project))
			return nil
		}
		d.add(importAPIKey, "", "", id)
	}
	return nil
}

// webKeyRestrictions describes r, and reports whether it is exactly the
// module's: one API target per service of tf.APITargets(), no methods, and
// no restriction of another kind (the rule tf.Cover applies to a planned
// key, plus the other kinds).
func webKeyRestrictions(r *apikeys.V2Restrictions) (string, bool) {
	if r == nil {
		return "none", false
	}
	var parts, services []string
	methods := false
	for _, t := range r.ApiTargets {
		if t == nil {
			continue
		}
		s := "API target " + t.Service
		if len(t.Methods) > 0 {
			s += " (methods " + strings.Join(t.Methods, ", ") + ")"
			methods = true
		}
		parts = append(parts, s)
		services = append(services, t.Service)
	}
	other := false
	for kind, set := range map[string]bool{"browser": r.BrowserKeyRestrictions != nil, "server": r.ServerKeyRestrictions != nil,
		"Android": r.AndroidKeyRestrictions != nil, "iOS": r.IosKeyRestrictions != nil} {
		if set {
			parts = append(parts, "a "+kind+" restriction")
			other = true
		}
	}
	slices.Sort(parts)
	slices.Sort(services)
	desc := strings.Join(parts, ", ")
	if desc == "" {
		desc = "none"
	}
	return desc, !methods && !other && slices.Equal(services, tf.APITargets())
}

// tokenSigner adopts the signer account when it carries our display name
// and this Fugaro project's description, and its shape is ours: not
// disabled, no user-managed key, and an IAM policy that grants nothing but
// this project's minter role, to none but the spec's launchers and
// operators. Its signature admits a run to the database, so anything else
// is refused.
func (d *discovery) tokenSigner(spec FirebaseSpec) error {
	email := serviceAccountEmail(spec.Names.SignerAccountID, d.project)
	name := "projects/" + d.project + "/serviceAccounts/" + email
	a, err := d.c.IAM.Projects.ServiceAccounts.Get(name).Context(d.ctx).Do()
	switch {
	case d.absent(err, serviceIAM):
		return nil
	case err != nil:
		return fmt.Errorf("reading service account %s (fugaro init --firebase needs iam.serviceAccounts.get in project %s): %w", email, d.project, err)
	}
	wantDesc := signerDescription(spec.FugaroProject)
	if a.DisplayName != signerDisplayName || a.Description != wantDesc {
		d.refuse(&ForeignError{Resource: "token signer " + email,
			Found: "display name " + strconv.Quote(a.DisplayName) + " and description " + strconv.Quote(a.Description),
			Want:  "display name " + strconv.Quote(signerDisplayName) + " and description " + strconv.Quote(wantDesc)})
		return nil
	}
	refused := len(d.refusals)
	if a.Disabled {
		d.refuse(fmt.Errorf("token signer %s is disabled, so no run could sign in; fugaro refuses to adopt it. If it is Fugaro's, enable it (gcloud iam service-accounts enable %s --project %s), then rerun fugaro init --firebase %s",
			email, email, d.project, d.project))
	}

	keys, err := d.c.IAM.Projects.ServiceAccounts.Keys.List(name).KeyTypes("USER_MANAGED").Context(d.ctx).Do()
	if err != nil {
		return fmt.Errorf("listing the keys of service account %s (fugaro init --firebase needs iam.serviceAccountKeys.list in project %s): %w", email, d.project, err)
	}
	var userKeys []string
	for _, k := range keys.Keys {
		// The filter asked for user-managed keys only; anything that is
		// not Google's own counts, so an answer that ignored it fails closed.
		if k.KeyType != signerKeyTypeSystem {
			userKeys = append(userKeys, k.Name[strings.LastIndex(k.Name, "/")+1:])
		}
	}
	if len(userKeys) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "token signer %s has %d user-managed key(s) (%s): whoever holds one signs run tokens without IAM, and Fugaro never creates one. fugaro refuses to adopt it. Delete them, then rerun fugaro init --firebase %s:", email, len(userKeys), strings.Join(userKeys, ", "), d.project)
		for _, id := range userKeys {
			fmt.Fprintf(&b, "\n  gcloud iam service-accounts keys delete %s --iam-account %s --project %s", shellWord(id), email, d.project)
		}
		d.refuse(errors.New(b.String()))
	}

	p, err := d.c.IAM.Projects.ServiceAccounts.GetIamPolicy(name).OptionsRequestedPolicyVersion(3).Context(d.ctx).Do()
	if err != nil {
		return fmt.Errorf("reading the IAM policy of service account %s (fugaro init --firebase needs iam.serviceAccounts.getIamPolicy in project %s): %w", email, d.project, err)
	}
	d.signerPolicy(spec, email, p)

	if len(d.refusals) == refused {
		d.add(importSignerSA, "", "", email)
		d.im.Notes = append(d.im.Notes, fmt.Sprintf("the token signer %s was adopted; it holds no grants but the minter role of this installation's launchers and operators, which was checked, but roles on project %s (or inherited) that can sign as it are not visible here: fugaro doctor's token-signers check lists them", email, d.project))
	}
	return nil
}

// signerPolicy refuses every grant on the signer but the minter role of
// this project to a launcher or operator, one refusal per role, naming
// each member and the command that removes it. Members are compared as
// the spec writes them, byte for byte.
func (d *discovery) signerPolicy(spec FirebaseSpec, email string, p *iam.Policy) {
	minter := minterRoleName(d.project, spec.Names.MinterRoleID)
	allowed := map[string]bool{}
	for _, m := range slices.Concat(spec.Launchers, spec.Operators) {
		allowed[memberKey(m)] = true
	}
	foreign := map[string][]signerGrant{}
	for _, b := range p.Bindings {
		if b == nil {
			continue
		}
		for _, m := range b.Members {
			if b.Role == minter && allowed[memberKey(m)] {
				continue // ours; a condition only narrows it
			}
			g := signerGrant{member: m, conditional: b.Condition != nil}
			if !slices.Contains(foreign[b.Role], g) {
				foreign[b.Role] = append(foreign[b.Role], g)
			}
		}
	}
	for _, role := range slices.Sorted(maps.Keys(foreign)) {
		gs := foreign[role]
		slices.SortFunc(gs, func(a, b signerGrant) int {
			return cmp.Or(cmp.Compare(a.member, b.member), cmp.Compare(strconv.FormatBool(a.conditional), strconv.FormatBool(b.conditional)))
		})
		d.refuse(errors.New(signerGrantMessage(email, d.project, role, gs)))
	}
}

// memberKey is the IAM member m as it compares: IAM keeps the emails of
// user:, group: and serviceAccount: members lower-cased, so their email
// compares without case; the type, and every other kind of member
// (allUsers, domain:, deleted:, principal://...), compares byte for byte.
func memberKey(m string) string {
	for _, prefix := range []string{"user:", "group:", "serviceAccount:"} {
		if email, ok := strings.CutPrefix(m, prefix); ok {
			return prefix + strings.ToLower(email)
		}
	}
	return m
}

// minterRoleName is the minter role's full name in project fp.
func minterRoleName(fp, id string) string { return "projects/" + fp + "/roles/" + id }

// signerGrant is one member's grant of a role on the signer.
type signerGrant struct {
	member      string
	conditional bool
}

// foreignMinterMessage is the refusal of a signer that grants the minter
// role to members (of unconditional grants) who are not this
// installation's launchers or operators (design §3.3).
func foreignMinterMessage(sa, fp string, members []string) string {
	gs := make([]signerGrant, len(members))
	for i, m := range members {
		gs[i] = signerGrant{member: m}
	}
	return signerGrantMessage(sa, fp, minterRoleName(fp, MinterRoleID), gs)
}

// signerGrantMessage is the refusal of the grants gs of role on the signer
// sa of fp: the minter role's to members who are not launchers or
// operators (design §3.3, byte for byte when no grant is conditional), or
// any other role's. A conditional grant's removal needs --all.
func signerGrantMessage(sa, fp, role string, gs []signerGrant) string {
	var b strings.Builder
	minter := role == minterRoleName(fp, MinterRoleID)
	if minter {
		fmt.Fprintf(&b, "token signer %s grants %s to members who are not this installation's launchers or operators:\n", sa, MinterRoleID)
	} else {
		fmt.Fprintf(&b, "token signer %s grants %s, which Fugaro never grants on it, to:\n", sa, role)
	}
	for _, g := range gs {
		b.WriteString("  " + shellWord(g.member))
		if g.conditional {
			b.WriteString(" (under a condition)")
		}
		b.WriteByte('\n')
	}
	if minter {
		b.WriteString("Each could mint run tokens.")
	} else {
		b.WriteString("Each could act as the signer and mint run tokens.")
	}
	fmt.Fprintf(&b, " Remove them, then rerun fugaro init --firebase %s:\n", fp)
	for _, g := range gs {
		fmt.Fprintf(&b, "  gcloud iam service-accounts remove-iam-policy-binding %s --project %s --member=%s --role=%s", sa, fp, shellWord(g.member), shellWord(role))
		if g.conditional {
			b.WriteString(" --all")
		}
		b.WriteByte('\n')
	}
	if minter {
		b.WriteString("Or add them as launchers or operators if they should keep it.")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// plainWord is what a shell passes through unquoted and unexpanded.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9@%+=:,./_-]+$`)

// shellWord is s as one shell word on one line: as is when nothing in it
// is special, else single-quoted, or ANSI-C quoted ($'...') when it holds
// a control character, so a line break in a member can never print a line
// of its own (one that reads as a command to copy).
func shellWord(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	if !strings.ContainsFunc(s, unicode.IsControl) {
		return shellQuote(s)
	}
	var b strings.Builder
	b.WriteString("$'")
	for _, r := range s {
		switch {
		case r == '\\' || r == '\'':
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case unicode.IsControl(r) && r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		case unicode.IsControl(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString("'")
	return b.String()
}

// minterRole adopts the minter role when it carries our title and exactly
// the pinned permissions (tf.RolePermissions): it is granted to people on
// the signer, so a wider role would make each grant reach further. A
// deleted role is not imported; the plan's create restores it.
func (d *discovery) minterRole(spec FirebaseSpec) error {
	id := spec.Names.MinterRoleID
	name := minterRoleName(d.project, id)
	role, err := d.c.IAM.Projects.Roles.Get(name).Context(d.ctx).Do()
	switch {
	case d.absent(err, serviceIAM):
		return nil
	case err != nil:
		return fmt.Errorf("reading custom role %s (fugaro init --firebase needs iam.roles.get in project %s): %w", name, d.project, err)
	}
	want := tf.RolePermissions[MinterRoleID]
	got := slices.Sorted(slices.Values(role.IncludedPermissions))
	switch {
	case role.Title != minterRoleTitle:
		d.refuse(&ForeignError{Resource: "custom role " + name, Found: "title " + strconv.Quote(role.Title), Want: "title " + strconv.Quote(minterRoleTitle)})
	case role.Deleted:
		// The plan's create undeletes it; a deleted role can't be imported.
		d.im.Notes = append(d.im.Notes, fmt.Sprintf("custom role %s is deleted; the plan's create restores it", name))
	case !slices.Equal(got, slices.Sorted(slices.Values(want))):
		d.refuse(fmt.Errorf("custom role %s includes %s, not exactly %s: it is granted to people on the token signer, so a wider role makes each grant reach further. fugaro refuses to adopt it; if it is Fugaro's, set its permissions back (gcloud iam roles update %s --project %s --permissions=%s), then rerun fugaro init --firebase %s",
			name, cmp.Or(strings.Join(got, ", "), "no permission"), strings.Join(want, ", "), id, d.project, strings.Join(want, ","), d.project))
	default:
		d.add(importMinterRole, "", "", id)
	}
	return nil
}
