package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// The image build's gate and record steps: they run inside Cloud Build,
// from the base image's fugaro, and read and write the build record.

// gateState is what the gate read, next to the record as gate.json, so
// the record step writes only over that same object.
type gateState struct {
	Exists     bool   `json:"exists"`
	Generation int64  `json:"generation"`
	Previous   []byte `json:"previous,omitempty"`
}

// buildNameRE is what a slug or workflow in a record key may be.
var buildNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// buildRecordKey is the record's key for slug and ours' workflow.
func buildRecordKey(slug string, ours *imagecheck.Record) (string, error) {
	if !buildNameRE.MatchString(slug) || !buildNameRE.MatchString(ours.Workflow) {
		return "", userErr("the slug %q or workflow %q is not a name", slug, ours.Workflow)
	}
	return imagecheck.RecordKey(slug, ours.Workflow), nil
}

// readBuildRecord reads the record file the render step wrote.
func readBuildRecord(path string) (*imagecheck.Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, userErr("%v", err)
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		return nil, userErr("%s: %v", path, err)
	}
	return rec, nil
}

// newImageGateCmd is the build's gate step: `fugaro image gate`. It reads
// the current record and, when that is newer than the one this build
// would write, writes superseded next to --record, so the promote, record
// and untag steps do nothing. Otherwise it writes gate.json there, the
// generation it read, which the record step writes against.
func newImageGateCmd() *cobra.Command {
	var record, slug, bucket string
	cmd := &cobra.Command{
		Use:    "gate",
		Short:  "Stop an image build from promoting over a newer one (a Cloud Build step)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			ours, err := readBuildRecord(record)
			if err != nil {
				return err
			}
			key, err := buildRecordKey(slug, ours)
			if err != nil {
				return err
			}
			b, err := blobx.Open(ctx, bucket)
			if err != nil {
				return userErr("opening the bucket %s: %v", bucket, err)
			}
			defer b.Close()
			dir := filepath.Dir(record)
			cur, gen, err := b.Read(ctx, key)
			state := gateState{Exists: err == nil, Generation: gen, Previous: cur}
			switch {
			case errors.Is(err, blobx.ErrNotExist):
				fmt.Fprintf(cmd.OutOrStdout(), "no record at %s yet: promoting\n", key)
			case err != nil:
				return remote(fmt.Errorf("reading %s: %w", key, err))
			default:
				curRec, perr := imagecheck.ParseRecord(cur)
				switch {
				case perr != nil:
					fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: the record at %s is unreadable (%v); replacing it\n", key, perr)
				case curRec.NewerThan(ours):
					if err := os.WriteFile(filepath.Join(dir, "superseded"), nil, 0o644); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "superseded: %s records commit %s (committed %s, built %s), newer than this build's %s (committed %s): not promoting\n",
						key, oneLine(curRec.SourceCommit), curRec.SourceCommitTime.Format(time.RFC3339), curRec.BuiltAt.Format(time.RFC3339),
						oneLine(ours.SourceCommit), ours.SourceCommitTime.Format(time.RFC3339))
					return nil
				default:
					fmt.Fprintf(cmd.OutOrStdout(), "%s records commit %s (committed %s), not newer: promoting\n",
						key, oneLine(curRec.SourceCommit), curRec.SourceCommitTime.Format(time.RFC3339))
				}
			}
			data, err := json.Marshal(state)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, "gate.json"), data, 0o644)
		},
	}
	f := cmd.Flags()
	f.StringVar(&record, "record", "", "the record this build would write (render's record.json); superseded and gate.json go next to it")
	f.StringVar(&slug, "slug", "", "the repository's storage slug")
	f.StringVar(&bucket, "bucket", "", "the runs bucket, as a gocloud URL (gs://…)")
	for _, name := range []string{"record", "slug", "bucket"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}

// readDigest reads a digest file (sha256:…, or <image>@sha256:…).
func readDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", userErr("%v", err)
	}
	v := strings.TrimSpace(string(data))
	if _, after, ok := strings.Cut(v, "@"); ok {
		v = after
	}
	if !gcp.IsDigest(v) {
		return "", userErr("%s does not hold an image digest (sha256:…)", path)
	}
	return v, nil
}

// newImageRecordCmd is the build's record step: `fugaro image record`. It
// completes render's record with what the build made, and writes it to
// the runs bucket, only over the object the gate read: a record written
// in between (a concurrent build) fails this step instead.
func newImageRecordCmd() *cobra.Command {
	var in, digestFrom, baseDigestFrom, img, base, buildID, slug, bucket string
	cmd := &cobra.Command{
		Use:    "record",
		Short:  "Record what an image build built (a Cloud Build step)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			dir := filepath.Dir(in)
			if _, err := os.Stat(filepath.Join(dir, "superseded")); err == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "superseded: a newer build is recorded; nothing to record")
				return nil
			}
			rec, err := readBuildRecord(in)
			if err != nil {
				return err
			}
			key, err := buildRecordKey(slug, rec)
			if err != nil {
				return err
			}
			if rec.ImageDigest, err = readDigest(digestFrom); err != nil {
				return err
			}
			if rec.BaseDigest, err = readDigest(baseDigestFrom); err != nil {
				return err
			}
			if img == "" || base == "" || buildID == "" {
				return userErr("--image, --base and --build-id are required")
			}
			rec.Image, rec.BaseRef, rec.BuildID, rec.Adopted = img, base, buildID, false
			var state gateState
			data, err := os.ReadFile(filepath.Join(dir, "gate.json"))
			if err == nil {
				err = json.Unmarshal(data, &state)
			}
			if err != nil {
				return userErr("reading what the gate step read (gate.json): %v; the gate must run first", err)
			}
			out, err := json.MarshalIndent(rec, "", "  ")
			if err != nil {
				return err
			}
			b, err := blobx.Open(ctx, bucket)
			if err != nil {
				return userErr("opening the bucket %s: %v", bucket, err)
			}
			defer b.Close()
			if state.Exists {
				_, err = b.ReplaceIf(ctx, key, out, state.Generation, state.Previous)
			} else {
				_, err = b.Create(ctx, key, out, "application/json")
			}
			switch {
			case errors.Is(err, blobx.ErrConflict), errors.Is(err, blobx.ErrExists):
				return remote(fmt.Errorf("%s changed since the gate read it (another build recorded meanwhile); not overwriting it: the next image check compares that record with latest", key))
			case err != nil:
				return remote(fmt.Errorf("writing %s: %w", key, err))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "recorded %s: %s@%s from commit %s\n", key, oneLine(rec.Image), rec.ImageDigest, oneLine(rec.SourceCommit))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&in, "in", "", "render's record.json; gate.json and superseded are read next to it")
	f.StringVar(&digestFrom, "digest-from", "", "file holding the promoted image's digest")
	f.StringVar(&baseDigestFrom, "base-digest-from", "", "file holding the base image's digest (<image>@sha256:…)")
	f.StringVar(&img, "image", "", "the image, without a tag")
	f.StringVar(&base, "base", "", "the base image as the build was given it")
	f.StringVar(&buildID, "build-id", "", "the Cloud Build build ID")
	f.StringVar(&slug, "slug", "", "the repository's storage slug")
	f.StringVar(&bucket, "bucket", "", "the runs bucket, as a gocloud URL (gs://…)")
	for _, name := range []string{"in", "digest-from", "base-digest-from", "slug", "bucket"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}
