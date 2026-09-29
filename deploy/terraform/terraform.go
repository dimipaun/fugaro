// Package terraform holds the Terraform that sets up Fugaro's cloud
// resources: the modules under gcp/modules and the roots under gcp/roots
// that call them. It embeds the tree so fugaro init can write a root and
// its modules into a workdir, and the binary always plans with the modules
// it was built with.
package terraform

import "embed"

// FS is the gcp/ tree. The all: prefix keeps the dot files the roots need
// (.terraform.lock.hcl, .tflint.hcl). Run terraform with TF_DATA_DIR
// outside the tree, or remove any .terraform/ directory before building, so
// no provider binary is embedded.
//
//go:embed all:gcp
var FS embed.FS
