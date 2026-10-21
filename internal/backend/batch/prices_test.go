package batch

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
)

// The table's own consistency: a machine type's quantities are its family's
// shape (a standard type has 4 GiB per vCPU), and every machine type it
// carries has a family price, so nothing in it can be priced at zero. The
// design names e2-standard-4 as the type 57a measured on, and the type an
// installation may select (§1.3), and c2-standard-60 as Check 57a step 11's
// quota probe (§7.2); both must be in it.
func TestMachineTypeTableIsInternallyConsistent(t *testing.T) {
	for _, want := range []string{"e2-standard-4", "c2-standard-60"} {
		if _, ok := machineTypes[want]; !ok {
			t.Errorf("the design names %s and the table does not carry it", want)
		}
	}
	for name, mt := range machineTypes {
		if mt.Name != name {
			t.Errorf("%s: Name is %q", name, mt.Name)
		}
		if mt.VCPU <= 0 || mt.MemoryGiB <= 0 {
			t.Errorf("%s: %v vCPU, %v GiB", name, mt.VCPU, mt.MemoryGiB)
		}
		if mt.MemoryGiB != 4*mt.VCPU {
			t.Errorf("%s: %v GiB for %v vCPU, want a standard type's 4 GiB per vCPU", name, mt.MemoryGiB, mt.VCPU)
		}
		fam, err := FamilyOf(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if want := name[:strings.LastIndex(name, "-")]; fam != want {
			t.Errorf("%s: family %q, want %q", name, fam, want)
		}
	}
}

// An unknown machine type is refused with a message naming what the table
// does carry: the machine type comes from the operator's profile, so the
// answer to a type the table has never heard of is "unknown", never a price.
func TestMachineTypeOfRefusesWhatTheTableDoesNotCarry(t *testing.T) {
	if _, err := MachineTypeOf("n2-standard-4"); err == nil {
		t.Fatal("n2-standard-4 is priced, and the table does not carry it")
	} else if !strings.Contains(err.Error(), "e2-standard-4") {
		t.Errorf("the error does not list the known types: %v", err)
	}
	if mt, err := MachineTypeOf("e2-standard-4"); err != nil || mt.VCPU != 4 || mt.MemoryGiB != 16 {
		t.Errorf("e2-standard-4 = %+v, %v", mt, err)
	}
}

// The figures are the pricing page's per-hour ones, divided by 3600: a
// family's two prices must reproduce the machine type's own hourly price,
// which is the cross-check the comment on the table names.
func TestPricesAreThePagesPerHourFigures(t *testing.T) {
	p, err := PricesFor("us-central1", "e2-standard-4")
	if err != nil {
		t.Fatal(err)
	}
	if !near(p.VCPUSecondUSD, 0.02181159/3600) || !near(p.GiBSecondUSD, 0.00292353/3600) {
		t.Errorf("e2-standard-4 = %+v, want the page's $0.02181159/vCPU-hour and $0.00292353/GiB-hour", p)
	} else if got := 3600 * (4*p.VCPUSecondUSD + 16*p.GiBSecondUSD); !near(got, 0.13402284) {
		t.Errorf("e2-standard-4 is $%v/hour, want the page's $0.13402284", got)
	}
	q, err := PricesFor("us-central1", "c2-standard-60")
	if err != nil {
		t.Fatal(err)
	}
	if got := 3600 * (60*q.VCPUSecondUSD + 240*q.GiBSecondUSD); !near(got, 3.13212) {
		t.Errorf("c2-standard-60 is $%v/hour, want the page's $3.13212", got)
	}
	if p == q {
		t.Error("an e2 and a c2 machine type price the same")
	}
}

// publishedPrices are the hourly on-demand prices read off each region's own
// "Default* (USD)" table on the Compute Engine pricing page on 2026-10-15
// (the citation at the top of prices.go): the region's machine type against
// what the page says it costs. land is the price a region's own table gives
// for that machine type, so a mistyped rate, a wrong region group, or a size
// without its price fails here rather than on a bill.
var publishedPrices = []struct {
	region, machineType string
	usd                 float64
}{
	{"us-central1", "e2-standard-2", 0.06701142},
	{"us-central1", "e2-standard-4", 0.13402284},
	{"us-central1", "e2-standard-8", 0.26804568},
	{"us-central1", "e2-standard-16", 0.53609136},
	{"us-central1", "e2-standard-32", 1.07218272},
	{"us-central1", "c2-standard-4", 0.208808},
	{"us-central1", "c2-standard-8", 0.417616},
	{"us-central1", "c2-standard-16", 0.835232},
	{"us-central1", "c2-standard-30", 1.56606},
	{"us-central1", "c2-standard-60", 3.13212},

	// us-east1, us-east5 and us-west1 are the rest of groupUS: the page's
	// rows for them read the same as us-central1's, which is what makes the
	// grouping a record of the page rather than an assumption.
	{"us-east1", "e2-standard-4", 0.13402284},
	{"us-east5", "e2-standard-4", 0.13402284},
	{"us-east5", "c2-standard-60", 3.13212},
	{"us-west1", "e2-standard-4", 0.13402284},

	// us-east4 is dearer, which is why it is its own group: pricing it as a
	// groupUS region under-reported every estimate there by about 12%.
	{"us-east4", "e2-standard-2", 0.07547082},
	{"us-east4", "e2-standard-4", 0.15094164},
	{"us-east4", "e2-standard-8", 0.30188328},
	{"us-east4", "e2-standard-16", 0.60376656},
	{"us-east4", "e2-standard-32", 1.20753312},
	{"us-east4", "c2-standard-4", 0.235144},
	{"us-east4", "c2-standard-8", 0.470288},
	{"us-east4", "c2-standard-16", 0.940576},
	{"us-east4", "c2-standard-30", 1.76358},
	{"us-east4", "c2-standard-60", 3.52716},
}

// TestCheckMachineTypePrices is the table against the page: every machine
// type the table carries, in every region it carries, must cost what the
// region's own row on the pricing page says, to a hundredth of a cent. It is
// the check the table's comment promises, and it is what makes "correct the
// region table" a standing test instead of a reading.
func TestCheckMachineTypePrices(t *testing.T) {
	for _, c := range publishedPrices {
		p, err := PricesFor(c.region, c.machineType)
		if err != nil {
			t.Errorf("%s in %s: %v", c.machineType, c.region, err)
			continue
		}
		cpu, mem := p.Quantities(0, 0)
		got := 3600 * (cpu*p.VCPUSecondUSD + mem*p.GiBSecondUSD)
		// A hundredth of a cent per hour: the page's own rows are exact to
		// eight decimals, and this leaves room for the c2 family's rate pair.
		if math.Abs(got-c.usd) > 0.0001 {
			t.Errorf("%s in %s is $%.8f/hour, the page says $%.8f", c.machineType, c.region, got, c.usd)
		}
	}
	// Every machine type and every region the table carries is covered
	// above, so a type or region added without a published price fails here.
	covered := map[string]bool{}
	for _, c := range publishedPrices {
		covered[c.machineType] = true
		covered[c.region] = true
	}
	for _, mt := range KnownMachineTypes() {
		if !covered[mt] {
			t.Errorf("the table carries %s and no test pins its published price", mt)
		}
	}
	for _, r := range KnownRegions() {
		if !covered[r] {
			t.Errorf("the table carries region %s and no test pins its published price", r)
		}
	}
}

// TestMachineTypesMatchThePublishedSizes pins every machine type's vCPU and
// memory against Google's own machine-type pages
// (cloud.google.com/compute/docs/general-purpose-machines and
// .../compute-optimized-machines, read 2026-10-15), so a size cannot drift
// from the type it names: an e2-standard-16 that was really an 8's quantities
// would halve or double an estimate while looking plausible.
func TestMachineTypesMatchThePublishedSizes(t *testing.T) {
	published := map[string][2]float64{
		"e2-standard-2":  {2, 8},
		"e2-standard-4":  {4, 16},
		"e2-standard-8":  {8, 32},
		"e2-standard-16": {16, 64},
		"e2-standard-32": {32, 128},
		"c2-standard-4":  {4, 16},
		"c2-standard-8":  {8, 32},
		"c2-standard-16": {16, 64},
		"c2-standard-30": {30, 120},
		"c2-standard-60": {60, 240},
	}
	for name, want := range published {
		mt, err := MachineTypeOf(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if mt.VCPU != want[0] || mt.MemoryGiB != want[1] {
			t.Errorf("%s is %v vCPU / %v GiB, the published size is %v / %v", name, mt.VCPU, mt.MemoryGiB, want[0], want[1])
		}
	}
	for _, name := range KnownMachineTypes() {
		if _, ok := published[name]; !ok {
			t.Errorf("the table carries %s and no test pins its published size", name)
		}
	}
}

// us-east5 is the owner's installation region: the row must exist (an
// unpriced region is a refusal, so a run there would read "not estimated")
// and must cost what us-central1 costs, which is what the page's own row for
// it says. us-east4 is the counter-example: same family, dearer region, so
// the two must not share a group.
func TestUsEast5IsPricedLikeUsCentral1AndUsEast4IsNot(t *testing.T) {
	base, err := PricesFor("us-central1", "e2-standard-4")
	if err != nil {
		t.Fatalf("us-central1: %v", err)
	}
	east5, err := PricesFor("us-east5", "e2-standard-4")
	if err != nil {
		t.Fatalf("us-east5 (the installation's region) is not priced: %v", err)
	}
	if east5.VCPUSecondUSD != base.VCPUSecondUSD || east5.GiBSecondUSD != base.GiBSecondUSD {
		t.Errorf("us-east5 = %+v, want us-central1's figures", east5)
	}
	if east5.Source == base.Source {
		t.Error("the source line does not name the region it priced")
	} else if !strings.Contains(east5.Source, "us-east5") {
		t.Errorf("source %q does not name us-east5", east5.Source)
	}
	east4, err := PricesFor("us-east4", "e2-standard-4")
	if err != nil {
		t.Fatalf("us-east4: %v", err)
	}
	if group := regionGroups["us-east4"]; group == regionGroups["us-central1"] {
		t.Error("us-east4 shares us-central1's group, and the page prices it dearer")
	}
	if east4.VCPUSecondUSD <= base.VCPUSecondUSD {
		t.Errorf("us-east4's vCPU price %v is not above us-central1's %v", east4.VCPUSecondUSD, base.VCPUSecondUSD)
	}
	if got := 3600 * (4*east4.VCPUSecondUSD + 16*east4.GiBSecondUSD); !near(got, 0.15094164) {
		t.Errorf("e2-standard-4 in us-east4 is $%v/hour, the page says $0.15094164", got)
	}
}

// FamilyOf is exported and slices the family out of the name it is given, so
// a type the table does not carry must be an error and not a family read off
// an arbitrary string (the exported check that would otherwise look
// redundant next to MachineTypeOf).
func TestFamilyOfRefusesATypeTheTableDoesNotCarry(t *testing.T) {
	if fam, err := FamilyOf("n2-standard-4"); err == nil {
		t.Errorf("FamilyOf(n2-standard-4) = %q, want an error: the table prices no n2 family", fam)
	}
	if fam, err := FamilyOf("not-a-machine-type"); err == nil {
		t.Errorf("FamilyOf(not-a-machine-type) = %q, want an error", fam)
	}
	if fam, err := FamilyOf("e2-standard-4"); err != nil || fam != "e2-standard" {
		t.Errorf("FamilyOf(e2-standard-4) = %q, %v", fam, err)
	}
}

// A region the table carries no price for is refused, not priced from
// another region's figure: Compute Engine's prices differ by region, and a
// guessed one would be a wrong number the reader could not tell from a right
// one (design compute-backend.md §5.2).
func TestPricesForRefusesARegionTheTableDoesNotCarry(t *testing.T) {
	if _, err := PricesFor("europe-west2", "e2-standard-4"); err == nil {
		t.Fatal("europe-west2 is priced, and the table carries no price for it")
	} else if !strings.Contains(err.Error(), "us-central1") {
		t.Errorf("the error does not list the priced regions: %v", err)
	}
	if _, err := PricesFor("us-central1", "n2-standard-4"); err == nil {
		t.Fatal("an unknown machine type is priced")
	}
}

// The source line is what a reader tells a VM estimate from Cloud Run's, and
// it names the two costs the figure leaves out (design C17).
func TestSourceLineNamesTheTableAndWhatItLeavesOut(t *testing.T) {
	p, err := PricesFor("us-central1", "e2-standard-4")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Compute Engine VM list price", "e2-standard", "us-central1", priceReadAt, "UNVERIFIED", "boot disk", "Cloud NAT"} {
		if !strings.Contains(p.Source, want) {
			t.Errorf("source %q does not name %q", p.Source, want)
		}
	}
	// It is a price table's line, not Cloud Run's: the two must never read
	// the same, or a reader cannot tell which estimate they are looking at.
	if strings.Contains(p.Source, "Cloud Run") {
		t.Errorf("source %q reads like Cloud Run's", p.Source)
	}
}

// The estimate itself: the machine type's quantities over the billed window,
// with Compute Engine's one-minute minimum applied. The floor is the seam's
// (backend.VMMinimumBilled), so this pins that the two agree.
func TestVMCostIsTheMachineTypesQuantitiesOverTheFlooredWindow(t *testing.T) {
	p, err := PricesFor("us-central1", "e2-standard-4")
	if err != nil {
		t.Fatal(err)
	}
	// The prices carry the machine type's quantities, which is what the
	// estimate bills: Compute Engine bills the VM, not what the workflow's
	// resources asked for (design §5.2).
	if p.VCPU != 4 || p.MemoryGiB != 16 {
		t.Fatalf("prices carry %v vCPU / %v GiB, want the e2-standard-4's 4/16", p.VCPU, p.MemoryGiB)
	}
	// The execution's own limits are a 2 vCPU / 8 GiB workflow's: the
	// machine type's quantities decide the figure anyway.
	short := backend.Execution{MachineType: "e2-standard-4", CPU: 2, MemoryGiB: 8,
		Created: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	now := short.Created.Add(40 * time.Second)
	d, ok := short.BilledWindow(now)
	if !ok {
		t.Fatal("a VM with a boot window is not priceable")
	}
	if d != backend.VMMinimumBilled {
		t.Fatalf("a 40 s VM is billed for %v, want the %v minimum", d, backend.VMMinimumBilled)
	}
	cpu, mem := p.Quantities(short.CPU, short.MemoryGiB)
	if cpu != 4 || mem != 16 {
		t.Fatalf("the estimate bills %v vCPU / %v GiB, want the machine type's 4/16", cpu, mem)
	}
	if got, want := p.ComputeUSD(cpu, mem, d), 60*(4*p.VCPUSecondUSD+16*p.GiBSecondUSD); got != want {
		t.Errorf("compute = %v, want %v", got, want)
	}
}

// near compares two dollar figures: the table's per-hour prices are divided
// by 3600 (at run time, where a constant expression is rounded differently),
// multiplied back and summed over a machine type's vCPUs and GiB, so they
// land within a rounding error of the page's own figure and never exactly on
// it. A hundredth of a cent is 1e-4, so 1e-6 asserts cents while tolerating
// the arithmetic.
func near(got, want float64) bool { return math.Abs(got-want) < 1e-6 }
