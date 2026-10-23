package gcpfake

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Compute is a stateful fake of the two Compute Engine calls the reaper
// makes (design compute-backend.md §3.4): instances.aggregatedList, which is
// how it finds a Fugaro-labelled VM in any of the region's zones, and
// instances.delete, which is how a leaked VM is removed (one per VM, with the
// zone the listing named).
//
// It implements exactly those two plus instances.get, and the three
// read-only gets `fugaro doctor` makes about a Batch installation's cloud
// facts (an image family, a network, a subnetwork), and fails the test on
// anything else, so a reaper that started sending a third call -- or a
// doctor check reaching for a call nobody wrote -- is noticed rather than
// silently served. The delete is answered with a finished
// Operation; whether the real one is finished when the API answers is not
// documented on the pages read (UNVERIFIED -- the reaper does not read the
// operation, so the difference is invisible to it).
//
// An instance is stored under the project and zone its create named, as the
// API scopes one: an aggregated list of another project is empty and a delete
// naming another zone is NOT_FOUND, never the instance. A fake that keyed
// instances by name alone would be more permissive than the API, and a reaper
// that lost the zone on the way to a request would pass every test.
type Compute struct {
	*Server

	// Project and Zones are what the aggregated list answers under, which no
	// request path supplies for one. Project defaults to "fake-project", and
	// Zones to a single "fake-region-a".
	Project string
	Zones   []string
	// FailDeleteWith, when non-zero, makes instances.delete answer that HTTP
	// status, deleting nothing (a permission refusal, say).
	FailDeleteWith int
	// IgnoreFilter makes the aggregated list answer every instance it holds
	// whatever its filter was, as a server that does not honour a label
	// filter would. It is how a caller's own client-side label check is
	// exercised: whether the API honours the filter or ignores it, the answer
	// must be the same (internal/backend/batch's List and reaper both say so).
	IgnoreFilter bool

	mu        sync.Mutex
	instances map[string]*computeInstance // by zone and name
	order     []string                    // zone/name, oldest first
	deletes   []string                    // zone/name, in order
	// families, networks and subnetworks are what the three read-only gets
	// `fugaro doctor` makes for a Batch installation answer from: the
	// preload image family a run's bootDisk names, and the network and
	// subnetwork the batch block names. A name the test did not add is
	// NOT_FOUND, which is a fact doctor reports rather than a failure.
	families   map[string]bool   // image family, under defProject
	networks   map[string]bool   // network name, under defProject
	subnetwork map[string]string // "region/name" → name, under defProject
	ops        int
}

type computeInstance struct {
	zone   string
	name   string
	labels map[string]string
	status string
	// created is what the listing renders as creationTimestamp; rawCreated,
	// when set, is rendered verbatim instead: the API answers a string, and a
	// string this build cannot parse is a fact about the instance rather than
	// a failure of the call.
	created    time.Time
	rawCreated *string
}

// NewCompute starts a Compute Engine fake that lives until the test ends.
func NewCompute(t *testing.T) *Compute {
	t.Helper()
	f := &Compute{instances: map[string]*computeInstance{}}
	f.Server = newServer(t, f.handle)
	return f
}

// AddInstance makes an instance named name exist in zone with labels, as a
// VM Batch created would: RUNNING, created now. Zero labels is a VM with no
// labels at all, which is what a design that set only Job.labels leaves
// behind (design §2.4).
func (f *Compute) AddInstance(zone, name string, labels map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.add(zone, name, labels, time.Now())
}

// AddInstanceCreated is AddInstance with an explicit creation time, which is
// what the reaper's age filter reads.
func (f *Compute) AddInstanceCreated(zone, name string, labels map[string]string, created time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.add(zone, name, labels, created)
}

// SetInstanceCreatedRaw makes an instance's creationTimestamp the literal
// string raw, whatever it is: the aggregated list then answers what the API
// would have answered, so a caller's own reading of a stamp it cannot parse is
// exercised. The instance has to exist already; a name the fake does not hold
// is left alone (there is nothing to render).
func (f *Compute) SetInstanceCreatedRaw(zone, name, raw string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.instances[zone+"/"+name]; i != nil {
		i.rawCreated = &raw
	}
}

func (f *Compute) add(zone, name string, labels map[string]string, created time.Time) {
	if labels == nil {
		labels = map[string]string{}
	}
	f.instances[zone+"/"+name] = &computeInstance{zone: zone, name: name, labels: labels, status: "RUNNING", created: created}
	f.order = append(f.order, zone+"/"+name)
}

// Deletes returns every instances.delete the fake answered, as "zone/name",
// in order. A refused call is not recorded: the API refused it, so nothing
// was deleted.
func (f *Compute) Deletes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deletes...)
}

// Instances returns every instance's "zone/name", oldest first.
func (f *Compute) Instances() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.order...)
}

// AddImageFamily makes the preloaded boot image family exist in the fake's
// project, which is what a run's bootDisk.image names (design §1.2): the
// family, never one of its generations. The get answers the family with a
// generation behind it, as the real API does once the preload stage has built
// one.
func (f *Compute) AddImageFamily(family string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.families == nil {
		f.families = map[string]bool{}
	}
	f.families[family] = true
}

// AddNetwork makes the network named (its bare name, as the full resource
// name's last element) exist in the fake's project.
func (f *Compute) AddNetwork(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.networks == nil {
		f.networks = map[string]bool{}
	}
	f.networks[name] = true
}

// AddSubnetwork makes the subnetwork named exist in region in the fake's
// project.
func (f *Compute) AddSubnetwork(region, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subnetwork == nil {
		f.subnetwork = map[string]string{}
	}
	f.subnetwork[region+"/"+name] = name
}

// Labels returns an instance's labels, or nil when it does not exist.
func (f *Compute) Labels(zone, name string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.instances[zone+"/"+name]
	if i == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range i.labels {
		out[k] = v
	}
	return out
}

func (f *Compute) defProject() string {
	if f.Project == "" {
		return "fake-project"
	}
	return f.Project
}

func (f *Compute) defZones() []string {
	if len(f.Zones) == 0 {
		return []string{"fake-region-a"}
	}
	return f.Zones
}

// computePathRE matches the two paths the fake answers, after the
// "/compute/v1" base path is stripped: the project-wide aggregated list, and
// one instance in one zone.
var (
	computeAggregatedRE = regexp.MustCompile(`^/projects/([^/]+)/aggregated/instances$`)
	computeInstanceRE   = regexp.MustCompile(`^/projects/([^/]+)/zones/([^/]+)/instances/([^/]+)$`)
	// The three read-only gets `fugaro doctor` makes about a Batch
	// installation's cloud facts: the preload image family, and the network
	// and subnetwork its batch block names.
	computeImageFamilyRE = regexp.MustCompile(`^/projects/([^/]+)/global/images/family/([^/]+)$`)
	computeNetworkRE     = regexp.MustCompile(`^/projects/([^/]+)/global/networks/([^/]+)$`)
	computeSubnetworkRE  = regexp.MustCompile(`^/projects/([^/]+)/regions/([^/]+)/subnetworks/([^/]+)$`)
)

func (f *Compute) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	p := strings.TrimPrefix(r.URL.Path, "/compute/v1")
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := computeAggregatedRE.FindStringSubmatch(p); m != nil && r.Method == http.MethodGet {
		f.aggregated(w, r, m[1])
		return
	}
	if m := computeImageFamilyRE.FindStringSubmatch(p); m != nil && r.Method == http.MethodGet {
		f.getImageFamily(w, m[1], m[2])
		return
	}
	if m := computeNetworkRE.FindStringSubmatch(p); m != nil && r.Method == http.MethodGet {
		if m[1] == f.defProject() && f.networks[m[2]] {
			writeJSON(w, http.StatusOK, map[string]any{
				"kind": "compute#network", "name": m[2],
				"selfLink": "https://www.googleapis.com/compute/v1/projects/" + m[1] + "/global/networks/" + m[2],
			})
			return
		}
		writeError(w, http.StatusNotFound, "NOT_FOUND", "network "+m[2]+" not found")
		return
	}
	if m := computeSubnetworkRE.FindStringSubmatch(p); m != nil && r.Method == http.MethodGet {
		if m[1] == f.defProject() && f.subnetwork[m[2]+"/"+m[3]] != "" {
			writeJSON(w, http.StatusOK, map[string]any{
				"kind": "compute#subnetwork", "name": m[3], "region": m[2],
			})
			return
		}
		writeError(w, http.StatusNotFound, "NOT_FOUND", "subnetwork "+m[3]+" not found")
		return
	}
	if m := computeInstanceRE.FindStringSubmatch(p); m != nil {
		switch r.Method {
		case http.MethodGet:
			if i := f.instances[m[2]+"/"+m[3]]; i != nil && m[1] == f.defProject() {
				writeJSON(w, http.StatusOK, f.render(i))
				return
			}
			writeError(w, http.StatusNotFound, "NOT_FOUND", "instance "+m[3]+" not found")
			return
		case http.MethodDelete:
			f.delete(w, r, m[1], m[2], m[3])
			return
		}
	}
	// No insert, no setLabels, no patch: the reaper only reads and deletes,
	// so a client that sends anything else is noticed here rather than in a
	// project (design §3.4).
	f.unhandled(w, r)
}

// getImageFamily answers an image family's get: the request Batch's
// bootDisk.image resolves, which the API answers with the family's newest
// generation. A family with no generation behind it is NOT_FOUND, as the real
// API answers for a family the preload stage never published.
func (f *Compute) getImageFamily(w http.ResponseWriter, project, family string) {
	if project != f.defProject() || !f.families[family] {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "The resource 'projects/"+project+"/global/images/family/"+family+"' was not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind": "compute#image", "name": family + "-1", "family": family, "status": "READY",
		"selfLink": "https://www.googleapis.com/compute/v1/projects/" + project + "/global/images/" + family + "-1",
	})
}

func (f *Compute) render(i *computeInstance) map[string]any {
	created := stamp(i.created)
	if i.rawCreated != nil {
		created = *i.rawCreated
	}
	return map[string]any{
		"kind":              "compute#instance",
		"name":              i.name,
		"zone":              "https://www.googleapis.com/compute/v1/projects/" + f.defProject() + "/zones/" + i.zone,
		"status":            i.status,
		"labels":            i.labels,
		"creationTimestamp": created,
	}
}

// computeFilterRE is the one aggregatedList filter shape the fake answers:
// label equalities joined by AND, as the Compute Engine filter syntax writes
// them (`labels.fugaro = "managed"`, docs/design/compute-backend.md §7.2 uses
// the same form for gcloud). Any other filter fails the test rather than being
// silently ignored. The reaper checks the labels client-side as well, so its
// answer is the same whether or not the server honours the filter.
var computeFilterRE = regexp.MustCompile(`^(?:labels\.([a-z0-9_-]+)\s*=\s*"?([^"\s]*)"?)(?:\s+AND\s+labels\.([a-z0-9_-]+)\s*=\s*"?([^"\s]*)"?)?$`)

func (f *Compute) aggregated(w http.ResponseWriter, r *http.Request, project string) {
	want := map[string]string{}
	if filter := r.URL.Query().Get("filter"); filter != "" {
		m := computeFilterRE.FindStringSubmatch(filter)
		if m == nil {
			f.failf("gcpfake: instances.aggregatedList filter the fake cannot answer: %q", filter)
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unsupported filter")
			return
		}
		want[m[1]] = m[2]
		if m[3] != "" {
			want[m[3]] = m[4]
		}
		// The filter is still parsed -- a caller sending a shape the fake
		// cannot answer is failed above -- but not applied: this is the
		// server that ignores it.
		if f.IgnoreFilter {
			want = map[string]string{}
		}
	}
	items := map[string]any{}
	if project == f.defProject() {
		for _, key := range f.order {
			i := f.instances[key]
			if i == nil || !labelsMatch(i.labels, want) {
				continue
			}
			scope := "zones/" + i.zone
			s, _ := items[scope].(map[string]any)
			if s == nil {
				s = map[string]any{"instances": []any{}}
				items[scope] = s
			}
			s["instances"] = append(s["instances"].([]any), f.render(i))
		}
	}
	// Every zone of the region the fake was told about, even the empty ones:
	// the real aggregated list answers a scope per zone, and a caller that
	// reads the scope key rather than the instance's own zone field is
	// exercised by it. Another project's listing has no scopes at all.
	if project == f.defProject() {
		for _, z := range f.defZones() {
			if _, ok := items["zones/"+z]; !ok {
				items["zones/"+z] = map[string]any{}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": "compute#instanceAggregatedList", "items": items})
}

func labelsMatch(labels, want map[string]string) bool {
	for k, v := range want {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func (f *Compute) delete(w http.ResponseWriter, r *http.Request, project, zone, name string) {
	if project != f.defProject() {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "instance "+name+" not found")
		return
	}
	key := zone + "/" + name
	if f.instances[key] == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "instance "+name+" not found")
		return
	}
	if f.FailDeleteWith != 0 {
		writeError(w, f.FailDeleteWith, http.StatusText(f.FailDeleteWith), "injected failure")
		return
	}
	f.deletes = append(f.deletes, key)
	delete(f.instances, key)
	f.order = slices.DeleteFunc(f.order, func(x string) bool { return x == key })
	f.ops++
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":          "compute#operation",
		"name":          fmt.Sprintf("operation-%d", f.ops),
		"operationType": "delete",
		"status":        "DONE",
		"zone":          "https://www.googleapis.com/compute/v1/projects/" + project + "/zones/" + zone,
		"targetLink":    "https://www.googleapis.com/compute/v1/projects/" + project + "/zones/" + zone + "/instances/" + name,
	})
}
