package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
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
// terraform: the read is independent of the plan (an empty plan is decided
// before the confirmation and still reported), and a read that cannot be made
// must not be able to launch terraform at all.
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
	assets, err := managedAssets(ctx, lc)
	if err != nil {
		return managedmode.CheckedInventory(managedmode.Unread(lc.Name, lc.GCPProject, managedmode.CauseNoReader,
			"no whole-project read could be built: "+err.Error()))
	}
	accounted, err := managedAccounted(ctx, c, lc.GCPProject)
	if err != nil {
		return managedmode.CheckedInventory(managedmode.Unread(lc.Name, lc.GCPProject, managedmode.CauseStateUnread, err.Error()))
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
// this command would believe instead of the project's own.
//
// What it does NOT account for is written down where the table of Terraform
// types is (internal/managed/stateaccounts.go): the resources a creating run
// makes outside Terraform — the two state buckets themselves, the Firestore
// database and the Firebase project infra.AddFirebase adds — and the objects a
// job writes into the runs bucket at run time are in no state, so they stay
// foreign and fall a managed apply back to its confirmations until a task
// accounts for them. That is the safe direction, and Check 67a — the owner-run
// live check — is where the listing of a real managed project is read and the
// remaining gaps are named by the warning itself.
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
				return nil, fmt.Errorf("gs://%s/%s: %w", b.bucket, name, err)
			}
			out = append(out, res...)
		}
	}
	return out, nil
}
