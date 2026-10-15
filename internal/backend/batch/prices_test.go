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
// design names e2-standard-4 as the likely default (§1.3) and c2-standard-60
// as Check 57a step 11's quota probe (§7.2); both must be in it.
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
	if p.VCPUSecondUSD != 0.021811/3600 || p.GiBSecondUSD != 0.002923/3600 {
		t.Errorf("e2-standard-4 = %+v, want the page's $0.021811/vCPU-hour and $0.002923/GiB-hour", p)
	} else if got := 3600 * (4*p.VCPUSecondUSD + 16*p.GiBSecondUSD); !near(got, 0.134012) {
		t.Errorf("e2-standard-4 is $%v/hour, want $%v", got, 0.134012)
	}
	q, err := PricesFor("us-central1", "c2-standard-60")
	if err != nil {
		t.Fatal(err)
	}
	if got := 3600 * (60*q.VCPUSecondUSD + 240*q.GiBSecondUSD); !near(got, 3.1332) {
		t.Errorf("c2-standard-60 is $%v/hour, want $%v", got, 3.1332)
	}
	if p == q {
		t.Error("an e2 and a c2 machine type price the same")
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

// near is the per-hour comparison above: the table's figures are divided by
// 3600 and multiplied back, so they agree to within a few ulps and not
// exactly.
func near(got, want float64) bool { return math.Abs(got-want) < 1e-9 }
