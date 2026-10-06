package localcfg

// SharedMaxBytes caps the published file when read back.
const SharedMaxBytes = 64 << 10

// Shared is the installation-wide, non-secret part of the config, the part
// that is published to the runs bucket. It drops what is owner-only
// (terraform:), personal (user:, endpoints:) or derived (bucket_url, the
// legacy registry). The receiver is not modified. The copy is shallow: its
// maps, slices and pointers alias the receiver's, which is safe because the
// result is only marshalled, and Marshal only reads.
func (c *Config) Shared() *Config {
	s := *c
	s.Terraform = Terraform{}
	s.User = ""
	s.Endpoints = Endpoints{}
	s.Bucket = ""
	s.Registry = ""
	return &s
}

// SameInstallation reports whether a published object names the same
// installation as local: the same project name and GCP project. A foreign
// one must never be merged from.
func SameInstallation(published, local *Config) bool {
	return published != nil && published.Name == local.Name && published.GCPProject == local.GCPProject
}

// MergeShared is what to publish when published is what the runs bucket
// already holds (nil when absent) and local is this machine's config: the
// shared subset of local, filled in from published so that a machine that
// lacks what another onboarded does not erase it. Map fields (repos,
// base_images, model_prices, compute_prices, providers) are unioned, the
// local entry winning a key clash; scalar and pointer fields take the local
// value when it is non-zero, else the published one; name, gcp_project,
// runs_bucket and version always come from local. A published object of
// another installation (SameInstallation) is ignored. Neither argument is
// modified.
func MergeShared(published, local *Config) *Config {
	out := local.Shared()
	if !SameInstallation(published, local) {
		return out
	}
	out.Repos = unionMaps(published.Repos, local.Repos)
	out.BaseImages = unionMaps(published.BaseImages, local.BaseImages)
	out.ModelPrices = unionMaps(published.ModelPrices, local.ModelPrices)
	out.ComputePrices = unionMaps(published.ComputePrices, local.ComputePrices)
	out.Providers = unionMaps(published.Providers, local.Providers)
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
