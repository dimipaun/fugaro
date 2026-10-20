package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// adcFilePath is the one place the CLI decides where gcloud's Application
// Default Credentials file is (F29, T1.4). In order:
//
//   - $GOOGLE_APPLICATION_CREDENTIALS, the file the Google libraries
//     themselves would read, which names any path at all;
//   - $CLOUDSDK_CONFIG/gcloud's file, gcloud's own documented override of
//     its config directory — first among the defaults and unchanged on every
//     platform;
//   - <config directory>/gcloud/application_default_credentials.json, where
//     the config directory is localcfg.UserDir's: $XDG_CONFIG_HOME on any
//     platform when it is set, else %APPDATA% on Windows and ~/.config
//     elsewhere.
//
// Before this, three copies of the rule existed and disagreed: init's
// adcPath had no $XDG_CONFIG_HOME step at all, so with that variable set the
// wizard looked in ~/.config/gcloud while the rest of the CLI looked in
// $XDG_CONFIG_HOME/gcloud, and all three put the file under `.config` on
// Windows, where gcloud keeps it under %APPDATA%.
//
// goos is a parameter rather than a read of runtime.GOOS so the Windows rule
// is testable from a Linux checkout; the callers pass runtime.GOOS. The
// returned path is where the file would be, whether or not it exists.
//
// U11 is the open check that %APPDATA%\gcloud is where the pinned gcloud
// actually keeps it; the plan's T1.4 says that if it is not, this answer
// changes and nothing else does.
func adcFilePath(getenv func(string) string, goos string) (string, error) {
	if p := getenv("GOOGLE_APPLICATION_CREDENTIALS"); p != "" {
		return p, nil
	}
	if dir := getenv("CLOUDSDK_CONFIG"); dir != "" {
		return filepath.Join(dir, "application_default_credentials.json"), nil
	}
	dir, err := localcfg.UserDir(getenv, goos, localcfg.DirConfig)
	if err != nil {
		return "", fmt.Errorf("no Application Default Credentials: GOOGLE_APPLICATION_CREDENTIALS and CLOUDSDK_CONFIG are unset and %w (run gcloud auth application-default login)", err)
	}
	return filepath.Join(dir, "gcloud", "application_default_credentials.json"), nil
}

// adcFile is adcFilePath with the process environment: what
// GOOGLE_APPLICATION_CREDENTIALS names, else gcloud's well-known file, and a
// refusal when neither names one.
func adcFile() (string, error) { return adcFilePath(os.Getenv, runtime.GOOS) }
