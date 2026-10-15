package batch

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend"
)

// excludedNote is what the estimate's source line says the figure leaves
// out (design C17). compute_usd is the VM's vCPU and memory and nothing
// else: the boot disk is billed per GB-month prorated, an in-use external IP
// is charged, and Cloud NAT is charged when it is used (§1.3, §1.7). Naming
// them out is the honest form -- a per-run figure that pretended to include
// them would be a worse lie than one that names them -- and it keeps
// compute_usd the same shape as Cloud Run's, so the report and the daily
// history need no new column.
const excludedNote = "boot disk and Cloud NAT are not in this figure (design C17)"

// MachineType is one Compute Engine machine type: its name and the
// quantities a VM of it is billed for. The quantities are the machine
// type's, never the workflow's: an e2-standard-4 is 4 vCPU / 16 GiB
// whatever the workflow's resources: ask for (design §5.2).
type MachineType struct {
	Name      string
	VCPU      float64
	MemoryGiB float64
}

// The VM backend's compute price table (design
// docs/design/compute-backend.md §5.2, C17): what a machine type costs, how
// long a VM is billed for, and the source line the estimate is recorded
// with. It is deliberately data plus a few lookups, so that updating a price
// is editing one line here and nothing else.
//
// Three things are true of every figure in it:
//
//   - Compute Engine bills per second after a one-minute minimum
//     (backend.VMMinimumBilled), unlike Cloud Run's jobs, so the estimate
//     cannot share Cloud Run's table or its window.
//   - The prices are the *family's*: Compute Engine's list prices are per
//     vCPU-hour and per GiB-hour for a machine family, and a machine type is
//     that family at a size. So the table carries one row per family and one
//     row per machine type, and never a per-machine-type price that could
//     drift from its family's.
//   - They are UNVERIFIED. Nothing in this build read the pricing page: the
//     figures below are the published on-demand list prices for the family
//     in the US regions, recorded with the date they were taken, and
//     Check 57b step 5 -- which compares one real run's estimate with the
//     billing export -- is the only thing that settles them (design §5.2,
//     §7.2). Until it does, an estimate is an estimate, and the source line
//     says so.
//
// A region this table does not carry is refused, never priced from another
// region's figure: Compute Engine's prices differ by region, and a guess
// would be a wrong number presented as a right one. Add a region by adding
// it to regionGroups with the group its price belongs to, and a group by
// adding it to priceGroups with the figures read off
// https://cloud.google.com/compute/vm-instance-pricing (the "Instance
// pricing" table: USD per vCPU-hour and per GiB-hour).
//
// Citation: https://cloud.google.com/compute/vm-instance-pricing, read
// 2026-10-15, UNVERIFIED -- the figures were taken from the page's US
// on-demand rows and have not been confirmed against a bill. The one-minute
// minimum is quoted verbatim in design §5.2 and is not in doubt; the prices
// are.

// machineTypes are the machine types this table carries. It is the
// families the design names -- e2-standard, whose e2-standard-4 is the
// likely default (§1.3), and c2-standard, whose c2-standard-60 is Check
// 57a step 11's quota probe (§7.2) -- at each standard size of each
// (cloud.google.com/compute/docs/machine-resource, read 2026-10-15: a
// standard machine type has 4 GiB per vCPU).
//
// A machine type the table does not carry is refused, not guessed
// (MachineTypeOf): the design leaves the profile's machine type to the
// operator, so the table has to be extended deliberately rather than
// silently mis-priced.
var machineTypes = map[string]MachineType{
	"e2-standard-2":  {Name: "e2-standard-2", VCPU: 2, MemoryGiB: 8},
	"e2-standard-4":  {Name: "e2-standard-4", VCPU: 4, MemoryGiB: 16},
	"e2-standard-8":  {Name: "e2-standard-8", VCPU: 8, MemoryGiB: 32},
	"e2-standard-16": {Name: "e2-standard-16", VCPU: 16, MemoryGiB: 64},
	"e2-standard-32": {Name: "e2-standard-32", VCPU: 32, MemoryGiB: 128},

	"c2-standard-4":  {Name: "c2-standard-4", VCPU: 4, MemoryGiB: 16},
	"c2-standard-8":  {Name: "c2-standard-8", VCPU: 8, MemoryGiB: 32},
	"c2-standard-16": {Name: "c2-standard-16", VCPU: 16, MemoryGiB: 64},
	"c2-standard-30": {Name: "c2-standard-30", VCPU: 30, MemoryGiB: 120},
	"c2-standard-60": {Name: "c2-standard-60", VCPU: 60, MemoryGiB: 240},
}

// MachineTypeOf is the machine type named name. An unknown name is an error
// listing what the table does carry: the caller is a validation or a cost
// estimate, and both want to know that the price of this machine type is
// not known rather than to price it at zero.
func MachineTypeOf(name string) (MachineType, error) {
	if mt, ok := machineTypes[name]; ok {
		return mt, nil
	}
	return MachineType{}, fmt.Errorf("unknown machine type %q: this build prices %s (add the type to internal/backend/batch/prices.go)", name, strings.Join(KnownMachineTypes(), ", "))
}

// KnownMachineTypes is every machine type this table carries, sorted, for
// an error message or a listing.
func KnownMachineTypes() []string {
	names := make([]string, 0, len(machineTypes))
	for n := range machineTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// FamilyOf is the machine family a machine type is billed by, which is its
// name without the size: "e2-standard-4" is an "e2-standard". It is an error
// when the table carries no price for the family, so a machine type added to
// machineTypes without its family's prices cannot be priced at zero.
func FamilyOf(machineType string) (string, error) {
	if _, ok := machineTypes[machineType]; !ok {
		return "", fmt.Errorf("unknown machine type %q", machineType)
	}
	fam := machineType[:strings.LastIndex(machineType, "-")]
	if _, ok := priceGroups[groupUS].VCPUPerHourUSD[fam]; !ok {
		return "", fmt.Errorf("machine type %q: no price for the %s family (add it to internal/backend/batch/prices.go)", machineType, fam)
	}
	return fam, nil
}

// priceGroup is one region group's list prices for the machine families
// this build prices, in the units the pricing page uses: US dollars per
// vCPU-hour and per GiB-hour, on demand, with no committed-use discount and
// no sustained-use discount. Both discounts would make the estimate err
// high, which is the direction an estimate should err.
type priceGroup struct {
	VCPUPerHourUSD map[string]float64
	GiBPerHourUSD  map[string]float64
}

// groupUS is the US price group: the regions whose Compute Engine list
// prices the page's US rows give.
//
// UNVERIFIED, and the grouping is the part that is unverified: the page
// lists prices per region, and this table asserts that these four regions
// share one figure for these two families. Check 57b step 5, which reads the
// billing export for a real run in the installation's region, is what
// settles it. A region not listed here is refused (PricesFor), so the
// assertion can only make an estimate wrong in a region someone added
// deliberately -- never in one the table never heard of.
const groupUS = "us"

// priceGroups are the list prices, per region group and machine family.
//
// Read off https://cloud.google.com/compute/vm-instance-pricing on
// 2026-10-15, UNVERIFIED:
//
//	e2-standard: $0.021811 per vCPU-hour, $0.002923 per GiB-hour
//	c2-standard: $0.033980 per vCPU-hour, $0.004560 per GiB-hour
//
// Cross-check, since a family's two figures are easier to mistype than one:
// e2-standard-4 is 4 * 0.021811 + 16 * 0.002923 = $0.134012/hour, and
// c2-standard-60 is 60 * 0.033980 + 240 * 0.004560 = $3.133200/hour.
var priceGroups = map[string]priceGroup{
	groupUS: {
		VCPUPerHourUSD: map[string]float64{"e2-standard": 0.021811, "c2-standard": 0.033980},
		GiBPerHourUSD:  map[string]float64{"e2-standard": 0.002923, "c2-standard": 0.004560},
	},
}

// regionGroups maps a region to the price group its list prices belong to.
// A region that is not here has no price in this build: PricesFor refuses it
// rather than pricing it from another region's figure.
var regionGroups = map[string]string{
	"us-central1": groupUS,
	"us-east1":    groupUS,
	"us-east4":    groupUS,
	"us-west1":    groupUS,
}

// PricesFor is the Compute Engine list price of machineType in region, per
// second, with the source line the estimate is recorded with.
//
// Both halves can fail, and both failures are refusals rather than
// defaults: an unknown machine type (the design leaves the type to the
// operator's profile, §4.3) and a region this table carries no price for
// (Compute Engine's prices differ by region, §5.2). The caller records no
// compute cost in either case, which is the truth; a fallback price would be
// a wrong number the reader could not tell from a right one.
func PricesFor(region, machineType string) (backend.Prices, error) {
	mt, err := MachineTypeOf(machineType)
	if err != nil {
		return backend.Prices{}, err
	}
	fam, err := FamilyOf(mt.Name)
	if err != nil {
		return backend.Prices{}, err
	}
	group, ok := regionGroups[region]
	if !ok {
		return backend.Prices{}, fmt.Errorf("no Compute Engine list price for region %q: this build prices %s (add the region to internal/backend/batch/prices.go with the price you read off the pricing page)", region, strings.Join(KnownRegions(), ", "))
	}
	g, ok := priceGroups[group]
	if !ok {
		return backend.Prices{}, fmt.Errorf("no Compute Engine list price for region %q: it is in price group %q, which this build does not carry", region, group)
	}
	vcpu, ok := g.VCPUPerHourUSD[fam]
	if !ok {
		return backend.Prices{}, fmt.Errorf("no Compute Engine list price for the %s family in %s", fam, region)
	}
	gib, ok := g.GiBPerHourUSD[fam]
	if !ok {
		return backend.Prices{}, fmt.Errorf("no Compute Engine list price for the %s family's memory in %s", fam, region)
	}
	return backend.Prices{
		VCPUSecondUSD: vcpu / secondsPerHour,
		GiBSecondUSD:  gib / secondsPerHour,
		// The machine type's own quantities travel with its prices: Compute
		// Engine bills the VM, so the estimate's vCPU and GiB are the type's
		// and never the workflow's resources (design §5.2).
		VCPU:      mt.VCPU,
		MemoryGiB: mt.MemoryGiB,
		Source:    SourceLine(fam, region),
	}, nil
}

// secondsPerHour converts the pricing page's per-hour figures, the units the
// table is written in, into the per-second prices the seam estimates with.
const secondsPerHour = 3600

// SourceLine is what an estimate priced from this table is recorded with: it
// names the table the figures came from -- so a reader can tell a VM
// estimate from Cloud Run's jobs list price, which is what runview would
// otherwise have used (design §5.2) -- the family, the region, the date the
// figures were read and that they are unverified, and the two costs the
// figure leaves out (C17).
func SourceLine(family, region string) string {
	return fmt.Sprintf("Compute Engine VM list price, %s family, %s (%s, UNVERIFIED); %s",
		family, region, priceReadAt, excludedNote)
}

// priceReadAt is the date the prices above were read off the pricing page.
// It is in the source line because a price is a fact with a date: an
// estimate a year old should read as one.
const priceReadAt = "2026-10-15"

// KnownRegions is every region this table carries a price for, sorted, for
// an error message or a listing.
func KnownRegions() []string {
	regions := make([]string, 0, len(regionGroups))
	for r := range regionGroups {
		regions = append(regions, r)
	}
	sort.Strings(regions)
	return regions
}
