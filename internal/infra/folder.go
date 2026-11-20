package infra

// The GCP folder calls (WU2): the Cloud Resource Manager v3 surface behind the
// workspace folder authority — creating a folder, reading and changing the
// members bound to roles/owner on one, reading a project's parent and moving a
// project under another parent (D2803's preferred mechanism, D2804).
//
// Everything here runs with the caller's own credentials: a member owner moves
// their own project into the workspace's folder (D2819, A6), so nothing in this
// file impersonates and nothing here is reachable from a run.
//
// The shapes are the two stripped ones — a folder's display name and parent,
// an operation that has finished — because *Folders is what satisfies
// internal/workspace's Folders interface, and the two packages cannot name each
// other's types: the dependency between them runs through
// internal/projectpolicy, which imports internal/workspace. A struct here would
// be a second shape of the same two strings and an adapter for no gain.
//
// UNVERIFIED against the real service, as the project stage is (see
// projectcreate.go's list): the fakes in internal/gcpfake encode what the
// documentation says rather than what was observed. What is not verified here
// is when a moved project's inherited policies change over (the API's own model
// says immediately, and WU18 reports to the member owner what the move costs
// them before it is made) and whether folders.create's operation is the only
// asynchronous part of a create (it is the API's own long-running operation,
// polled here as a project's create is).

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	crm3 "google.golang.org/api/cloudresourcemanager/v3"
)

// FolderOwnerRole is the role a workspace admin holds on the workspace's
// folder (D2804), inherited by every project in it.
const FolderOwnerRole = "roles/owner"

// Folders is Resource Manager v3's folder surface.
type Folders struct{ crm *crm3.Service }

// NewFolders wraps the Resource Manager client the caller already built
// (NewProjectClients or NewClients); it connects to nothing of its own.
func NewFolders(crm *crm3.Service) *Folders { return &Folders{crm: crm} }

// folderNameRE is a folder's display name: 1 to 30 characters, starting and
// ending with a letter or a digit, of letters, digits, spaces, hyphens and
// underscores (the API's own grammar, quoted on the Folder resource).
var folderNameRE = regexp.MustCompile(`^[\p{L}\p{N}]([\p{L}\p{N}_\- ]{0,28}[\p{L}\p{N}])?$`)

// ValidateFolderName refuses a display name Google would refuse, before any
// call is made.
func ValidateFolderName(name string) error {
	if !folderNameRE.MatchString(name) {
		return userErr("%q is not a folder display name (1 to 30 characters, starting and ending with a letter or a digit, of letters, digits, spaces, hyphens and underscores)", name)
	}
	return nil
}

// Organizations returns the resource names (organizations/N) of the
// organizations the caller can see, in the API's order. It is how `workspace
// init` detects that a workspace can use a folder (D2803); a caller with no
// organization gets an empty list, not an error.
func (f *Folders) Organizations(ctx context.Context) ([]string, error) {
	var out []string
	page := ""
	for {
		call := f.crm.Organizations.Search().Context(ctx)
		if page != "" {
			call = call.PageToken(page)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("listing the organizations this credential can see: %w", err)
		}
		for _, o := range resp.Organizations {
			out = append(out, o.Name)
		}
		if resp.NextPageToken == "" {
			return out, nil
		}
		page = resp.NextPageToken
	}
}

// Create creates a folder named displayName under parent and returns its
// resource name (folders/N). The create is a long-running operation, polled to
// its end. The caller is not made an owner by this call: the authority adds
// that binding itself, so what a new folder grants is one visible step.
func (f *Folders) Create(ctx context.Context, displayName, parent string) (string, error) {
	if err := ValidateFolderName(displayName); err != nil {
		return "", err
	}
	if err := ValidateParent(parent); err != nil {
		return "", err
	}
	op, err := f.crm.Folders.Create(&crm3.Folder{DisplayName: displayName, Parent: parent}).Context(ctx).Do()
	if err != nil {
		return "", classify(err, "creating folder "+displayName)
	}
	op, err = f.poll(ctx, op, "creating folder "+displayName)
	if err != nil {
		return "", err
	}
	var made crm3.Folder
	if len(op.Response) > 0 {
		if err := json.Unmarshal(op.Response, &made); err != nil {
			return "", fmt.Errorf("creating folder %s: reading the operation's answer: %w", displayName, err)
		}
	}
	if made.Name == "" {
		return "", fmt.Errorf("creating folder %s: the operation finished without naming the folder it made", displayName)
	}
	return made.Name, nil
}

// Get answers the two facts a reader outside has any use for about a folder:
// its display name and its parent (organizations/N, folders/N or "").
func (f *Folders) Get(ctx context.Context, folder string) (displayName, parent string, err error) {
	fo, err := f.crm.Folders.Get(folder).Context(ctx).Do()
	if err != nil {
		return "", "", classify(err, "reading folder "+folder)
	}
	return fo.DisplayName, fo.Parent, nil
}

// Owners lists the members bound to roles/owner on folder, in the API's order.
// A member marked deleted (Google's "deleted:user:..." spelling, which a policy
// keeps until the principal's grants are cleaned up) is not listed: it is no
// longer a person who holds anything.
func (f *Folders) Owners(ctx context.Context, folder string) ([]string, error) {
	pol, err := f.iamPolicy(ctx, folder)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, b := range pol.Bindings {
		if b.Role != FolderOwnerRole {
			continue
		}
		for _, m := range b.Members {
			if strings.HasPrefix(m, "deleted:") {
				continue
			}
			out = append(out, m)
		}
	}
	return out, nil
}

// AddOwner binds member to roles/owner on folder. The policy is read and
// written back whole, under the etag of the read, so no other binding of the
// folder is disturbed and two writers cannot both act on one stale picture.
func (f *Folders) AddOwner(ctx context.Context, folder, member string) error {
	return f.changeOwners(ctx, folder, func(owners []string) []string {
		if slices.Contains(owners, member) {
			return owners
		}
		return append(owners, member)
	})
}

// RemoveOwner drops member's roles/owner binding on folder, on the same terms.
func (f *Folders) RemoveOwner(ctx context.Context, folder, member string) error {
	return f.changeOwners(ctx, folder, func(owners []string) []string {
		return slices.DeleteFunc(owners, func(o string) bool { return o == member })
	})
}

// changeOwners rewrites the folder's roles/owner binding through change and
// writes the policy back, leaving every other binding and the policy's other
// fields as they were read.
func (f *Folders) changeOwners(ctx context.Context, folder string, change func([]string) []string) error {
	pol, err := f.iamPolicy(ctx, folder)
	if err != nil {
		return err
	}
	var (
		owners   []string
		rest     []*crm3.Binding
		replaced bool
	)
	for _, b := range pol.Bindings {
		if b.Role != FolderOwnerRole {
			rest = append(rest, b)
			continue
		}
		owners = append(owners, b.Members...)
		replaced = true
	}
	members := change(owners)
	if slices.Equal(members, owners) {
		// Nothing to add or to drop: the policy is not written, so a no-op
		// never moves the etag another writer is holding.
		return nil
	}
	out := append([]*crm3.Binding{}, rest...)
	if len(members) > 0 {
		out = append(out, &crm3.Binding{Role: FolderOwnerRole, Members: members})
	} else if !replaced {
		return nil
	}
	_, err = f.crm.Folders.SetIamPolicy(folder, &crm3.SetIamPolicyRequest{
		Policy: &crm3.Policy{Etag: pol.Etag, Version: 3, Bindings: out},
	}).Context(ctx).Do()
	if err != nil {
		return classify(err, "changing the owners of folder "+folder)
	}
	return nil
}

// iamPolicy reads folder's IAM policy at the version that carries conditions.
func (f *Folders) iamPolicy(ctx context.Context, folder string) (*crm3.Policy, error) {
	pol, err := f.crm.Folders.GetIamPolicy(folder, &crm3.GetIamPolicyRequest{
		Options: &crm3.GetPolicyOptions{RequestedPolicyVersion: 3},
	}).Context(ctx).Do()
	if err != nil {
		return nil, classify(err, "reading the IAM policy of folder "+folder)
	}
	return pol, nil
}

// Parent is where project sits: organizations/N, folders/N, or "" for a project
// under no parent (one created outside any organization).
func (f *Folders) Parent(ctx context.Context, project string) (string, error) {
	p, err := f.crm.Projects.Get("projects/" + project).Context(ctx).Do()
	if err != nil {
		return "", classify(err, "reading project "+project)
	}
	return p.Parent, nil
}

// Move moves project under parent. The move is a long-running operation, polled
// to its end; a caller that reads the project's parent afterwards sees the
// destination.
func (f *Folders) Move(ctx context.Context, project, parent string) error {
	if err := ValidateParent(parent); err != nil {
		return err
	}
	op, err := f.crm.Projects.Move("projects/"+project, &crm3.MoveProjectRequest{DestinationParent: parent}).Context(ctx).Do()
	if err != nil {
		return classify(err, "moving project "+project+" to "+parent)
	}
	_, err = f.poll(ctx, op, "moving project "+project+" to "+parent)
	return err
}

// poll waits for a Resource Manager operation to finish, as CreateProject
// waits for its own: Wait.Tries reads, Wait.Interval apart. An operation that
// ends in an error is that error, with the API's own words and code kept.
func (f *Folders) poll(ctx context.Context, op *crm3.Operation, what string) (*crm3.Operation, error) {
	for i := 0; !op.Done; i++ {
		if i >= Wait.Tries {
			return nil, fmt.Errorf("%s: the operation %s is not done yet", what, op.Name)
		}
		if err := sleep(ctx); err != nil {
			return nil, err
		}
		var err error
		if op, err = f.crm.Operations.Get(op.Name).Context(ctx).Do(); err != nil {
			return nil, fmt.Errorf("%s: waiting for %s: %w", what, op.Name, err)
		}
	}
	if op.Error != nil {
		return nil, fmt.Errorf("%s: %s (code %d)", what, op.Error.Message, op.Error.Code)
	}
	return op, nil
}
