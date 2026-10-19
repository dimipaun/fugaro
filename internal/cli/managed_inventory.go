package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The whole-project inventory of `fugaro managed apply` (Task 4.5, design
// D59): the two reads that make up the comparison, and the Decision the apply
// takes.
//
// What it is: the Cloud Asset API's SearchAllResources over the whole project
// — the read Task 4.1 built, through the installation's own
// fugaroCloudAssetReader, which the module grants to the operators and to
// nobody else — set against what the installation's own Terraform states
// account for. A resource in the project that neither the state nor GCP itself
// accounts for is the case D59 is about, and the answer to it is a fallback:
// the run keeps the confirmations it would have had without the managed case,
// which in a coding agent's session means the plan is not applied unattended.
//
// Why the read is made HERE, with the caller's own clients: the read role is
// granted to the operators (deployer.tf and iam.tf), the deployer account
// holds neither it nor the installation's state bucket, and this is the last
// place in the command where the operator's credential is the one in hand —
// everything after the impersonation runs as the deployer. It is also before
// terraform: the read is made whatever the plan turns out to hold — an empty
// plan is decided without ever reaching the confirmation — and a read that
// cannot be made must not be able to launch terraform at all. Where a fallback
// is SAID is narrower: managed.Apply prints its sentence at the confirmation,
// so a run that applies nothing (an empty plan, a refused plan, a closed gate)
// carries the answer without warning about it.
//
// Nothing here writes, and nothing here can: the Cloud Asset read is an
// interface with one read method (managed.Assets), and the state reads are
// infra's own object reads.

// managedAssets is the command's whole-project read, built from the caller's
// ADC. It is a variable so a test can hand the command a read of its own
// without a Cloud Asset server, the way managedCaller is for the caller's
// identity: the read itself is Task 4.1's own code and is tested there.
var managedAssets = func(ctx context.Context, lc *localcfg.Config) (managedmode.Assets, error) {
	return managedmode.NewCloudAssets(ctx, doctorAPIOpts(lc, lc.Endpoints.CloudAsset)...)
}

// managedInventoryTimeout bounds the whole inventory: the Cloud Asset read and
// every state read. It is a variable only so a test can end a read that never
// answers without waiting minutes for it.
var managedInventoryTimeout = 3 * time.Minute

// managedInventory is the whole of the wiring: it returns a check and never an
// error, because every way this read can fail is a fallback and not a halt
// (D59: never halt and never delete; Global Constraint 3: a read that could
// not be made is not a clean answer). The caller hands the result to the apply
// (managed.Request.Inventory), where it keeps the run's confirmations.
//
// Three things can go wrong before there is a decision at all, and each one is
// its own cause in the same sentence: no Cloud Asset client to read with
// (CauseNoReader), a read that fails (Inventory.Check's own causes, from
// classify), and the installation's own state not being readable
// (CauseStateUnread) — the accounted side of the comparison, without which
// every listed resource would read as foreign.
func managedInventory(ctx context.Context, lc *localcfg.Config, c *infra.Clients, number uint64) managedmode.InventoryCheck {
	// One deadline over both halves: the Cloud Asset read bounds itself when
	// its context has none, but the state reads below are plain storage calls,
	// and a state bucket that never answered would hold the command forever —
	// a halt, which D59 rules out. A read the deadline ends is an error, and an
	// error is the fallback.
	ctx, cancel := context.WithTimeout(ctx, managedInventoryTimeout)
	defer cancel()
	assets, err := managedAssets(ctx, lc)
	if err != nil {
		return managedmode.CheckedInventory(managedmode.Unread(lc.Name, lc.GCPProject, managedmode.CauseNoReader,
			"no whole-project read could be built: "+safetext.Field(err.Error())))
	}
	accounted, err := managedAccounted(ctx, c, lc.GCPProject)
	if err != nil {
		return managedmode.CheckedInventory(managedmode.Unread(lc.Name, lc.GCPProject, managedmode.CauseStateUnread, safetext.Field(err.Error())))
	}
	in := managedmode.Inventory{
		Project:       lc.Name,
		GCPProject:    lc.GCPProject,
		ProjectNumber: int64(number),
		Assets:        assets,
		Accounted:     accounted,
	}
	return managedmode.CheckedInventory(in.Check(ctx))
}

// managedAccounted is what the installation's own state accounts for, read out
// of the two state buckets this project has: the installation's own
// (fugaro-tfstate-<gcp_project>: the installation root, the Firebase root,
// every repository's root and the compute root) and D77's dedicated managed
// bucket (fugaro-managed-tfstate-<gcp_project>, prefix fugaro/managed: the
// agent-authored root the apply is about).
//
// Both names are functions of the GCP project and are never taken from a flag
// or from the project's config (managed.ManagedStateBucketName,
// managed.InstallationsStateBucket): a bucket a caller could name is a bucket
// this command would believe instead of the project's own. A project whose
// config names another installation state bucket (terraform.state_bucket) is
// not one a managed creation or `fugaro managed flag` admits — both refuse a
// state bucket that is not the derived name — so the derived name is the
// installation's state bucket for every managed project.
//
// What LEAVES this list is written down where the table of Terraform types is
// (internal/managed/stateaccounts.go): the state holds no entry for the
// Firebase project infra.AddFirebase adds, nor for the creation fact and
// allow-delete list internal/infra/managed writes into the runs bucket, nor
// for the records a job writes there at run time, so none can be accounted for
// here. The resources a creating run makes outside Terraform that DO have a
// name this program derives — the two state buckets themselves, the default
// Firestore database Firebase provisions, and those two well-known objects
// under the runs bucket — are the inventory's third category and are
// ignored by their exact names (managed.Inventory's installationCreated), not
// by this function: the state says no word about them, and building them into
// Accounted would make the state read out to be something it is not. What
// remains uncovered falls the run back to its confirmations, which is the safe
// direction, and Check 67a — the owner-run live check — is where the listing of
// a real managed project is read and any remaining gap is named by the warning
// itself.
//
// A state that cannot be listed or parsed is an error and never an empty list
// of accounts, and the caller turns it into the fallback: reading "the state
// accounts for nothing" would make every resource of the project foreign and
// blame the project for a read this program could not make.
func managedAccounted(ctx context.Context, c *infra.Clients, gcpProject string) ([]managedmode.StateResource, error) {
	buckets := []struct{ bucket, prefix string }{
		{managedmode.InstallationsStateBucket(gcpProject), ""},
		{managedmode.ManagedStateBucketName(gcpProject), infra.StatePrefixManaged},
	}
	var out []managedmode.StateResource
	for _, b := range buckets {
		names, err := infra.StateObjects(ctx, c, b.bucket, b.prefix)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			data, err := infra.ReadStateObject(ctx, c, b.bucket, name)
			if err != nil {
				return nil, err
			}
			res, err := managedmode.AccountsOfState(data)
			if err != nil {
				return nil, fmt.Errorf("gs://%s/%s: %w", safetext.Field(b.bucket), safetext.Field(name), safetext.FieldErr(err))
			}
			out = append(out, res...)
		}
	}
	return out, nil
}
