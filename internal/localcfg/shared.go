package localcfg

import (
	"reflect"
	"slices"

	"gopkg.in/yaml.v3"
)

// SharedMaxBytes caps the published file when read back.
const SharedMaxBytes = 64 << 10

// Shared is the installation-wide, non-secret part of the config, the part
// that is published to the runs bucket. It drops what is owner-only
// (terraform:), personal (user:, endpoints:), derived (bucket_url, the
// legacy registry), deprecated (build.service_account) or local-only:
// providers:, whose base_url is where a key and a repository's code are
// sent, and each repo's base_branch, which the checkout's reviewed
// fugaro.yaml names. The receiver is not modified. The copy is shallow
// except for repos: its other maps, slices and pointers alias the
// receiver's, which is safe because the result is only marshalled, and
// Marshal only reads.
func (c *Config) Shared() *Config {
	s := *c
	s.Terraform = Terraform{}
	s.User = ""
	s.Endpoints = Endpoints{}
	s.Bucket = ""
	s.Registry = ""
	s.Providers = nil
	s.Build.ServiceAccount = ""
	s.Repos = withoutBaseBranches(c.Repos)
	return &s
}

// withoutBaseBranches is a copy of repos with every base_branch cleared.
func withoutBaseBranches(repos map[string]Repo) map[string]Repo {
	if repos == nil {
		return nil
	}
	out := make(map[string]Repo, len(repos))
	for k, r := range repos {
		r.BaseBranch = ""
		out[k] = r
	}
	return out
}

// SameInstallation reports whether a published object names the same
// installation as local: the same project name and GCP project. A foreign
// one must never be merged from.
func SameInstallation(published, local *Config) bool {
	return published != nil && published.Name == local.Name && published.GCPProject == local.GCPProject
}

// MergeShared is what to publish when published is what the runs bucket
// already holds (nil when absent) and local is this machine's config: the
// shared subset of local (Shared), filled in from published so that a
// machine that lacks what another onboarded does not erase it. Map fields
// (repos, base_images, model_prices, compute_prices) are unioned, the local
// entry winning a key clash; scalar and pointer fields take the local value
// when it is non-zero, else the published one; name, gcp_project,
// runs_bucket and version always come from local. What Shared drops is
// never taken from published either. A published object of another
// installation (SameInstallation) is ignored. Neither argument is modified.
func MergeShared(published, local *Config) *Config {
	out := local.Shared()
	if !SameInstallation(published, local) {
		return out
	}
	out.Repos = withoutBaseBranches(unionMaps(published.Repos, out.Repos))
	out.BaseImages = unionMaps(published.BaseImages, local.BaseImages)
	out.ModelPrices = unionMaps(published.ModelPrices, local.ModelPrices)
	out.ComputePrices = unionMaps(published.ComputePrices, local.ComputePrices)
	keep := func(dst *string, old string) {
		if *dst == "" {
			*dst = old
		}
	}
	keep(&out.Region, published.Region)
	keep(&out.RegistryHost, published.RegistryHost)
	keep(&out.BackendName, published.BackendName)
	keep(&out.LogView, published.LogView)
	keep(&out.SchedulerRegion, published.SchedulerRegion)
	if out.Build == (Build{}) {
		out.Build = published.Build
		out.Build.ServiceAccount = ""
	}
	if out.Budget == nil {
		out.Budget = published.Budget
	}
	if out.Watch == nil {
		out.Watch = published.Watch
	}
	if out.MaxParallel == 0 {
		out.MaxParallel = published.MaxParallel
	}
	return out
}

// unionMaps is a new map with every entry of a, and every entry of b over
// it; nil when both are empty.
func unionMaps[K comparable, V any](a, b map[K]V) map[K]V {
	if len(a)+len(b) == 0 {
		return b
	}
	m := make(map[K]V, len(a)+len(b))
	for k, v := range a {
		m[k] = v
	}
	for k, v := range b {
		m[k] = v
	}
	return m
}

// SharedDiff names the top-level fields on which the published shared
// config differs from what local would publish (local.Shared()), sorted;
// none when they agree. It is for `fugaro doctor`: a local config wins over
// the published one, and a difference is worth saying.
func SharedDiff(published, local *Config) []string {
	toMap := func(c *Config) map[string]any {
		data, err := c.Marshal()
		m := map[string]any{}
		if err == nil {
			_ = yaml.Unmarshal(data, &m)
		}
		return m
	}
	p, l := toMap(published.Shared()), toMap(local.Shared())
	var out []string
	for k, v := range l {
		if !reflect.DeepEqual(v, p[k]) {
			out = append(out, k)
		}
	}
	for k := range p {
		if _, ok := l[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}
