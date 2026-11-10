package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
)

// TestServicesStampAlwaysWritten: the launch stamps the services the job's
// catalog entry declares into the run's environment, and it does so on EVERY
// launch — the empty value included (D1904).
//
// The always is the whole of the rule (backend.ServicesEnv): the runner reads
// its own environment, and on a job whose image a catalog entry chose, that
// environment carries the image's own ENV. An image that set FUGARO_SERVICES
// would then be answering for the entry — a list read from somewhere other
// than the entry, which is what TestServicesFromEntryOnly forbids — so the
// launch writes the key whatever the answer is, and the empty value overrides
// what the image holds with "no entry declared a service".
//
// The key is therefore the LAUNCHER's to write, and the second half pins the
// other side of that: a launch entry naming it is not refused by the backends
// (it is not one of backend.ReservedEnvNames), which is what lets the value
// above travel at all — while the deploy's own stamp, which the launcher must
// not be able to rewrite, is (ImageEntryEnv).
func TestServicesStampAlwaysWritten(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry config.Entry
		want  string
	}{
		{"one service", config.Entry{Services: []string{"postgres"}}, backend.ServicesEnv + "=postgres"},
		{"three services", config.Entry{Services: []string{"postgres", "redis", "firebase"}}, backend.ServicesEnv + "=postgres redis firebase"},
		{"no service declared", config.Entry{}, backend.ServicesEnv + "="},
		{"an entry the catalog does not speak for", config.Entry{Name: "web", Tags: []string{"node"}}, backend.ServicesEnv + "="},
	} {
		got := servicesLaunchEnv(tc.entry)
		if got != tc.want {
			t.Errorf("%s: servicesLaunchEnv = %q, want %q", tc.name, got, tc.want)
		}
		if !strings.HasPrefix(got, backend.ServicesEnv+"=") {
			t.Errorf("%s: servicesLaunchEnv = %q: the key is written on every launch", tc.name, got)
		}
	}

	// The launcher's entry reaches the job, empty value and all: LaunchEnv is
	// the seam both backends build an execution's environment through, so a
	// name it refuses is one no backend can carry, and the key above has to
	// travel for the runner to hear the entry at all.
	env, err := backend.LaunchEnv(backend.LaunchSpec{Env: []string{
		servicesLaunchEnv(config.Entry{}),
	}}, backend.ReservedEnvNames("FUGARO_RUN")...)
	if err != nil {
		t.Fatalf("a launch entry naming %s was refused: %v — the launcher is the writer of that key", backend.ServicesEnv, err)
	}
	if len(env) != 1 || env[0].Name != backend.ServicesEnv || env[0].Value != "" {
		t.Errorf("the carried entry = %+v, want %s with the empty value", env, backend.ServicesEnv)
	}
	// The contrast, one line further: the deploy's own stamp is the one a
	// launch may not rewrite, and the backends refuse an entry naming it.
	if _, err := backend.LaunchEnv(backend.LaunchSpec{Env: []string{backend.ImageEntryEnv + "=web"}},
		backend.ReservedEnvNames(backend.ImageEntryEnv)...); err == nil {
		t.Errorf("a launch entry naming %s was accepted, and the deploy is its writer", backend.ImageEntryEnv)
	}
}
