package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// gcloudConfigDir is the directory gcloud itself keeps its configuration in,
// and it is deliberately *not* localcfg.UserDir's answer: UserDir is fugaro's
// own rule (XDG first, then the platform's directory), while gcloud's is
// gcloud's. gcloud's default is %APPDATA%\gcloud on Windows and
// ~/.config/gcloud everywhere else, and it honours $CLOUDSDK_CONFIG and
// neither $XDG_CONFIG_HOME nor the CLI's config directory (F29), so asking
// UserDir here would look for the credentials in a directory gcloud never
// writes. In order:
//
//   - $CLOUDSDK_CONFIG, gcloud's own documented override of its config
//     directory, taken verbatim because gcloud takes it verbatim — a set
//     value is where gcloud looks, on every platform;
//   - on Windows, %APPDATA%\gcloud;
//   - elsewhere, $HOME/.config/gcloud, with %USERPROFILE% standing in for HOME
//     where it is unset.
//
// The last two are joined only from a variable that is set to an absolute
// path, as everywhere else in the CLI: a relative directory variable names a
// directory of the current working directory, not of the user's, so it is
// ignored rather than joined.
//
// goos is a parameter rather than a read of runtime.GOOS so the Windows rule
// is testable from a Linux checkout; the callers pass runtime.GOOS.
func gcloudConfigDir(getenv func(string) string, goos string) (string, error) {
	if dir := getenv("CLOUDSDK_CONFIG"); dir != "" {
		return dir, nil
	}
	if goos == "windows" {
		if d := getenv("APPDATA"); filepath.IsAbs(d) {
			return filepath.Join(d, "gcloud"), nil
		}
		return "", fmt.Errorf("%q is not set to an absolute path, so gcloud's config directory on Windows is unknown", "APPDATA")
	}
	for _, e := range []string{"HOME", "USERPROFILE"} {
		if d := getenv(e); filepath.IsAbs(d) {
			return filepath.Join(d, ".config", "gcloud"), nil
		}
	}
	return "", fmt.Errorf("neither %q nor %q is set to an absolute path, so gcloud's config directory is unknown", "HOME", "USERPROFILE")
}

// adcFilePath is the one place the CLI decides where gcloud's Application
// Default Credentials file is (F29, T1.4). In order:
//
//   - $GOOGLE_APPLICATION_CREDENTIALS, the file the Google libraries
//     themselves would read, which names any path at all;
//   - gcloud's own config directory (gcloudConfigDir above) plus
//     application_default_credentials.json.
//
// Before this, three copies of the rule existed and disagreed: init's adcPath
// had no $XDG_CONFIG_HOME step at all, so with that variable set the wizard
// looked in ~/.config/gcloud while the rest of the CLI looked in
// $XDG_CONFIG_HOME/gcloud; all three put the file under `.config` on Windows,
// where gcloud keeps it under %APPDATA%; and the XDG step itself was wrong
// everywhere, because gcloud reads $CLOUDSDK_CONFIG and no XDG variable (F29).
// One builder now, and it is gcloud's rule rather than the CLI's.
//
// The returned path is where the file would be, whether or not it exists.
// U11 is the open check that %APPDATA%\gcloud is where the pinned gcloud
// actually keeps it; the plan's T1.4 says that if it is not, this answer
// changes and nothing else does.
func adcFilePath(getenv func(string) string, goos string) (string, error) {
	if p := getenv("GOOGLE_APPLICATION_CREDENTIALS"); p != "" {
		return p, nil
	}
	dir, err := gcloudConfigDir(getenv, goos)
	if err != nil {
		return "", fmt.Errorf("no Application Default Credentials: GOOGLE_APPLICATION_CREDENTIALS and CLOUDSDK_CONFIG are unset and %w (run gcloud auth application-default login)", err)
	}
	return filepath.Join(dir, "application_default_credentials.json"), nil
}

// adcFile is adcFilePath with the process environment: what
// GOOGLE_APPLICATION_CREDENTIALS names, else gcloud's well-known file, and a
// refusal when neither names one.
func adcFile() (string, error) { return adcFilePath(os.Getenv, runtime.GOOS) }
