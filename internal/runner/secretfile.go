package runner

// A workflow secret exposed as a file (D3327, F19, G3, R5).
//
// A repository declares `file: true` on a secret entry when the tool that reads
// it insists on a path: the value is written to a file of the run's own in the
// temporary directory, mode 0400, and the variable the entry names is set to
// that path instead of to the value (config.Secret.File). Nothing else about
// the secret changes -- it is fetched the same way, from the job's mount or
// from the credentials service, and it is registered with the redactor before
// it is written.
//
// Three rules shape this file, and each is a fact about the run rather than
// about the file:
//
//   - The value is registered with the redactor BEFORE the file exists (G3).
//     The file is a place the value is written, and a run that published a
//     value before its own redactor held it could leak it from the first line
//     after the write. writeSecretFile is the only writer, and it registers
//     first; the wiring that opens the file is never reached with a value the
//     redactor does not hold.
//   - A file is treated exactly as the environment secret it stands in for
//     (F19): present for a coding run's stages, and NEVER written in an
//     independent review run. The agent runs as the runner's own uid, so 0400
//     keeps no in-job stage from reading it -- no more than it keeps one from
//     the environment variable -- which is why the round-1 "remove it before
//     every review stage" step was dropped rather than kept: it protected
//     nothing the environment does not already expose. A review run's agent is
//     the case that IS protected, and it is protected by the secret never
//     reaching the file at all (bootstrap's review arm hands it no secret,
//     and exposeSecretFiles writes none).
//   - It is removed at run end, root and all (G3), on the same terms as the
//     commit hooks of writeStampHooks: only a directory this run made directly
//     under the temporary directory, so a path that is not this run's own is
//     refused rather than having its parent taken with it.
//
// The name of the file is the VARIABLE the declaration names (config's
// envNameRE: upper-case letters, digits and underscores), which is not a path a
// declaration can bend -- nothing a repository's fugaro.yaml holds can name a
// file outside the run's own directory -- and which is the one field a
// declaration cannot repeat: Validate refuses a workflow that names the same
// env twice, while the same LOGICAL name under two variables is legal (one
// secret exposed to two tools as two files). Keying the file by the name would
// make the second declaration a collision at a path both variables point at,
// and one variable would either abort the run or read the other's value.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// secretFileMode is the mode a file secret's value is written with: readable by
// the run's own uid, writable by nobody, and readable by no other user of the
// container.
const secretFileMode = 0o400

// secretFileTempPattern is the MkdirTemp pattern of the run's own directory.
// The runner's environment is handed to the agent's stages, so a directory of
// the run's is one the agent can name: the value in it is readable by the
// agent by design (F19), and the mode, the root and the removal are what keep
// it from outliving the run.
const secretFileTempPattern = "fugaro-secrets-"

// secretFileHook is called once per file secret, after the value is registered
// with the redactor and before the file is created: the one moment G3's
// ordering is observable, and the reason it is a hook rather than a comment
// (export_test.go's SetSecretFileHook). It is nil in every real run.
var secretFileHook func(value string, registered bool)

// exposeSecretFiles writes every `file: true` secret this run declares and
// points its variable at the file (D3327).
//
// The value is the one the run already holds for that variable, whether the
// job's own mount left it there or the credentials service answered it, so a
// mounted file secret is exposed exactly as a fetched one is: the declaration
// decides the variable's shape, and the flip of the mount changes nothing about
// it.
//
// A review run writes none (F19). Its own arm registers the values it holds for
// redaction and hands the agent no secret at all, so the value in the runner's
// environment is the whole of what it needs -- and a file an untrusted checkout
// could read is the one thing the review path must not leave behind.
func (r *run) exposeSecretFiles(declared []config.Secret) error {
	if r.review {
		return nil
	}
	for _, s := range declared {
		if !s.File {
			continue
		}
		value := envLookup(r.d.Env, s.Env)
		if value == "" {
			// No value to write: agent.BuildEnv's own "secret %s is not set
			// in the runner environment" is the refusal this run is owed, and
			// it names the variable and the fix where a sentence written here
			// would say the same thing twice.
			continue
		}
		path, err := r.writeSecretFile(s.Env, value)
		if err != nil {
			return fmt.Errorf("exposing workflow secret %s as a file: %w", s.Name, err)
		}
		r.d.Env = setEnvValue(r.d.Env, s.Env, path)
	}
	return nil
}

// writeSecretFile registers value with the redactor and writes it to the file
// env names it, returning the path.
//
// The registration comes first and is not moved: everything the run publishes
// is redacted of the values this list holds (runner.go's redact), and the write
// is the first thing that puts the value anywhere a reader of the run's
// container could find it.
func (r *run) writeSecretFile(env, value string) (string, error) {
	r.addSecret(value)
	if secretFileHook != nil {
		secretFileHook(value, slices.Contains(r.secretList(), value))
	}
	root, err := r.secretFileRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, env)
	// O_EXCL: a variable is declared once (config's envNameRE rule), so this
	// name is a second writer only if something above it stopped holding that,
	// and overwriting would leave two variables' worth of answers in one file.
	// What failed is carried rather than swallowed: the reason names the path
	// and never the value. The mode is set again after the open because the
	// kernel masks it with the process's umask, which is not this run's to know.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, secretFileMode)
	if err != nil {
		return "", fmt.Errorf("writing the value to a file of the run's own failed: %w", err)
	}
	_, werr := f.WriteString(value)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		return "", fmt.Errorf("writing the value to a file of the run's own failed: %w", errors.Join(werr, cerr))
	}
	if err := os.Chmod(path, secretFileMode); err != nil {
		return "", fmt.Errorf("setting the mode of the run's own secret file failed: %w", err)
	}
	return path, nil
}

// secretFileRoot is the directory this run's file secrets are written in, made
// once: the first file secret makes it, and every later one shares it.
func (r *run) secretFileRoot() (string, error) {
	if r.secretFiles != "" {
		return r.secretFiles, nil
	}
	root, err := os.MkdirTemp("", secretFileTempPattern)
	if err != nil {
		return "", fmt.Errorf("making a directory for the run's file secrets: %w", err)
	}
	r.secretFiles = root
	return root, nil
}

// removeSecretFiles takes the run's file-secret directory back out of the
// temporary directory, root and all. It is called from Run's deferred handler
// beside removeStampHooks, for the same reason: a directory of a finished run
// is the temporary directory filling up, and the values in this one are the
// credentials of a run that has ended.
func (r *run) removeSecretFiles() {
	if r.secretFiles == "" {
		return
	}
	// Only a root MkdirTemp made directly under the temporary directory: this
	// removes the directory it is given, and one that is not the run's own is
	// refused rather than taken.
	if filepath.Dir(filepath.Clean(r.secretFiles)) != filepath.Clean(os.TempDir()) {
		r.d.Log.Warn("refusing to remove a directory that is not the run's own", "dir", r.secretFiles)
		return
	}
	if err := os.RemoveAll(r.secretFiles); err != nil {
		r.d.Log.Warn("removing the run's file secrets failed", "dir", r.secretFiles, "err", err)
	}
}

// setEnvValue returns env with key set to value, replacing what the key held.
// A variable's value is what a reader of the environment sees, and the last
// entry is what every reader of this slice takes (envLookup, agent.BuildEnv),
// so the file's path replaces the value rather than being added beside it.
func setEnvValue(env []string, key, value string) []string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, _, ok := strings.Cut(env[i], "="); ok && k == key {
			env[i] = key + "=" + value
			return env
		}
	}
	return append(env, key+"="+value)
}
