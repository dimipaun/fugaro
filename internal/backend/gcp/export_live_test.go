//go:build live

package gcp

import (
	artifactregistry "google.golang.org/api/artifactregistry/v1"
	cloudbuild "google.golang.org/api/cloudbuild/v1"
	logging "google.golang.org/api/logging/v2"
	run "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/backend"
)

// The live tests are in package gcp_test because localcfg reaches back into
// this package through projectpolicy, an import cycle for an in-package test;
// these are the unexported parts they still need.
var (
	ParseTimeForLive      = parseTime
	ParseCPUForLive       = parseCPU
	DiscardLoggerForLive  = discardLogger
	RegistryHostREForLive = registryHostRE
)

func (b *Backend) RunForLive() *run.Service                          { return b.run }
func (b *Backend) LogsForLive() *logging.Service                     { return b.logs }
func (b *Backend) LocationForLive() string                           { return b.location() }
func (b *Backend) CanonicalForLive(n string) (backend.ExecID, error) { return b.canonical(n) }

func (b *Builder) ParentForLive() string                        { return b.parent() }
func (b *Builder) SvcForLive() *cloudbuild.Service              { return b.svc }
func (b *Builder) RegistriesForLive() *artifactregistry.Service { return b.registries }
