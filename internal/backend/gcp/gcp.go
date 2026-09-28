package gcp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	logging "google.golang.org/api/logging/v2"
	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"
)

// Endpoints override API roots, for fakes and emulators. Empty means the
// real Google endpoint.
type Endpoints struct {
	Run, Logging, SecretManager, CloudBuild string
	NoAuth                                  bool
}

// Options configure a Backend.
type Options struct {
	Project, Region string
	Endpoints       Endpoints
	HTTPClient      *http.Client  // optional
	LogSettle       time.Duration // follow's quiet period after the execution ends; zero means 30s
}

// Backend is the Cloud Run backend.
type Backend struct {
	o    Options
	run  *run.Service
	logs *logging.Service
	// listPageSize is List's page size; tests shrink it to exercise paging.
	listPageSize int64
}

func (o Options) client(endpoint string) []option.ClientOption {
	var opts []option.ClientOption
	if endpoint != "" {
		opts = append(opts, option.WithEndpoint(endpoint))
	}
	if o.Endpoints.NoAuth {
		opts = append(opts, option.WithoutAuthentication())
	}
	if o.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(o.HTTPClient))
	}
	if !o.Endpoints.NoAuth && o.Project != "" {
		// User ADC has no project of its own, and some APIs refuse it
		// without a quota project (minor 9).
		opts = append(opts, option.WithQuotaProject(o.Project))
	}
	return opts
}

// New connects to Cloud Run and Cloud Logging with Application Default
// Credentials (gcloud auth application-default login) unless NoAuth.
func New(ctx context.Context, o Options) (*Backend, error) {
	if o.LogSettle == 0 {
		o.LogSettle = 30 * time.Second
	}
	rs, err := run.NewService(ctx, o.client(o.Endpoints.Run)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Run: %w", err)
	}
	ls, err := logging.NewService(ctx, o.client(o.Endpoints.Logging)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Logging: %w", err)
	}
	return &Backend{o: o, run: rs, logs: ls, listPageSize: 100}, nil
}
