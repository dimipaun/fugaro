package cli

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/ciselect"
)

// TestCISelectorLegKeysAreTheMatrixKeys holds the keys `ci.yml`'s shard job
// reads off its matrix to the field names of ciselect.Leg. The selector's
// output is the matrix, so a renamed field that the workflow still reads under
// the old name hands every runner an empty value, and the run fails or, worse,
// runs the wrong tests; the packer's own tests cannot see it because the text
// that reads the keys is a workflow file.
func TestCISelectorLegKeysAreTheMatrixKeys(t *testing.T) {
	var legKeys []string
	typ := reflect.TypeOf(ciselect.Leg{})
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		legKeys = append(legKeys, name)
	}
	slices.Sort(legKeys)

	ci := readDoc(t, ".github/workflows/ci.yml")
	var used []string
	for _, m := range regexp.MustCompile(`\bmatrix\.(\w+)`).FindAllStringSubmatch(ci, -1) {
		if !slices.Contains(used, m[1]) {
			used = append(used, m[1])
		}
	}
	slices.Sort(used)

	if !slices.Equal(legKeys, used) {
		t.Errorf(".github/workflows/ci.yml reads the matrix keys %v and ciselect.Leg's JSON keys are %v: the selector's output is the matrix, so the two lists are one list", used, legKeys)
	}
}
