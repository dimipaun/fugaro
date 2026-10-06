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
