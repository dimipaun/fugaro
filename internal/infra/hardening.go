package infra

import (
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

const bucketGrantPrefix = "module.installation.google_storage_bucket_iam_member."

// grantMember is the member of address name["<member>"], ok false for any
// other address.
func grantMember(address, name string) (string, bool) {
	rest, ok := strings.CutPrefix(address, bucketGrantPrefix+name+"[")
	if !ok || !strings.HasSuffix(rest, "]") {
		return "", false
	}
	m, err := strconv.Unquote(strings.TrimSuffix(rest, "]"))
	return m, err == nil
}

// HardeningAllowDelete is the 0.7.0 bucket hardening's allow-list for p
// (docs/design/bucket-iam.md H7): each delete of a launcher's old objectAdmin
// grant on the runs bucket, runs["<m>"], where m is a launcher and not an
// operator and p also creates m's runs_reader and runs_launcher grants.
// A replace, another role, another resource, an operator's grant or a
// member on neither list is never allowed: the guard refuses it as before.
func HardeningAllowDelete(p *tf.Plan, launchers, operators []string) []string {
	if p == nil {
		return nil
	}
	created := map[string]bool{}
	for _, rc := range p.ResourceChanges {
		if slices.Equal(rc.Change.Actions, []string{"create"}) {
			created[rc.Address] = true
		}
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		m, ok := grantMember(rc.Address, "runs")
		if !ok || !slices.Equal(rc.Change.Actions, []string{"delete"}) {
			continue
		}
		if role, _ := rc.Change.Before["role"].(string); role != "roles/storage.objectAdmin" {
			continue
		}
		if !slices.Contains(launchers, m) || slices.Contains(operators, m) {
			continue
		}
		q := strconv.Quote(m)
		if !created[bucketGrantPrefix+"runs_reader["+q+"]"] || !created[bucketGrantPrefix+"runs_launcher["+q+"]"] {
			continue
		}
		out = append(out, rc.Address)
	}
	slices.Sort(out)
	return out
}

// HardeningBanner names the launchers whose old grant the plan deletes, ""
// when there are none.
func HardeningBanner(allowed []string) string {
	if len(allowed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("bucket access (0.7.0): these launchers keep reading the runs bucket and writing runs/, and lose write access to fugaro/, builds/, cache/ and locks/:\n")
	for _, a := range allowed {
		m, _ := grantMember(a, "runs")
		b.WriteString("  " + pluginwire.Printable(m) + "\n")
	}
	return b.String()
}
