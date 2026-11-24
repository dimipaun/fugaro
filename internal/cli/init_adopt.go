package cli

import (
	"context"

	"github.com/dimipaun/fugaro/internal/infra"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// Adopting an existing GCP project (the owner's ruling of 2026-11-22):
// --gcp-project may name a project that is already there, as long as it holds
// nothing of Fugaro's, and setup then uses it as if it had created it — the
// run marks it managed exactly as a creating run does, and the "Fugaro only
// starts fresh" refusal of 2026-10-23 stands for every project that holds
// anything of Fugaro's. This file is the read that decides, and the label the
// run that adopts writes.
//
// What "nothing of Fugaro's" is, and no more (the ruling's list):
//
//   - no origin label, infra.CreatedLabelKey: a project carrying it is one a
//     run of fugaro setup made Fugaro's — created, or adopted by an earlier
//     run — and the stage resumes it instead. A project carrying the key with
//     a value this program does not write is one Fugaro did not write, and it
//     is refused like any other project of Fugaro's;
//   - no installation: the runs bucket fugaro-runs-<project>, which holds
//     both of the installation's markers — fugaro/project.json, the name
//     freshOnlyInstalled reads before the wizard asks for one, and
//     fugaro/managed.json, the creation fact — and the installation Terraform
//     state, fugaro-tfstate-<project>. The two markers are inside the bucket,
//     so a bucket that is there is the whole of that check, and no read of an
//     object can say anything a read of its bucket has not.
//
// Everything else in the project — other workloads, other buckets, other
// databases — is not Fugaro's and does not stop the adoption.
//
// A bucket name is global, so nothing here is believed on its name: each
// bucket is asked for its own project number and only a bucket of this
// project counts (infra.BucketInProject). A bucket that cannot be read is an
// error and not "not there": the run refuses rather than adopts a project
// whose footprint it could not see.

// fugaroFootprint is what of Fugaro's an existing project holds.
type fugaroFootprint struct {
	// runs is the runs bucket fugaro-runs-<project>: the installation's own,
	// with its project marker and the creation fact in it.
	runs bool
	// state is the installation Terraform state, fugaro-tfstate-<project>:
	// what fugaro setup creates and what fugaro update applies through.
	state bool
}

// holds is whether the project holds anything of Fugaro's, which is what
// stops the adoption.
func (f fugaroFootprint) holds() bool { return f.runs || f.state }

// footprint reads what of Fugaro's the existing project holds. It is asked
// only of a project that exists, carries no origin label and is not being
// deleted, and it changes nothing.
func (s *projectStage) footprint(ctx context.Context) (fugaroFootprint, error) {
	var f fugaroFootprint
	c, err := s.initClients(ctx)
	if err != nil {
		return f, err
	}
	// The project's number is what makes a bucket name about this project:
	// the same read infra.ReadProjectMarker makes before it believes a
	// bucket, and the same one markCreated makes before it writes the fact.
	num, err := infra.ProjectNumber(ctx, c, s.id)
	if err != nil {
		return f, err
	}
	if f.runs, err = infra.BucketInProject(ctx, c, "fugaro-runs-"+s.id, num); err != nil {
		return f, err
	}
	f.state, err = infra.BucketInProject(ctx, c, managedmode.InstallationsStateBucket(s.id), num)
	return f, err
}

// markAdopted writes the label that makes the adopted project resumable:
// infra.CreatedLabelKey=infra.AdoptedLabelValue, in one write, on the
// project's own labels as they are. The value is "adopted" and not "true"
// because the label states the project's origin, and this project's origin is
// not this program (infra.WriteAdoptedLabel).
//
// It is the first thing Apply does that touches the project, and it is what a
// later run reads to resume a run that died before it marked the project
// managed — the stranding the label exists to prevent, since by then the
// installation's runs bucket is there and the project is no longer adoptable.
func (s *projectStage) markAdopted(ctx context.Context) error {
	c, err := s.initClients(ctx)
	if err != nil {
		return err
	}
	if err := infra.WriteAdoptedLabel(ctx, c, s.id); err != nil {
		return s.fail(err)
	}
	return nil
}

// initClients is the run's own clients — the installation's endpoints, the
// user's own credentials — built once for this stage: the footprint read and
// the label write are the two calls that need them, and neither is a call
// ProjectClients (the create, the Firebase and the billing calls) can make.
func (s *projectStage) initClients(ctx context.Context) (*infra.Clients, error) {
	if s.ic != nil {
		return s.ic, nil
	}
	c, err := newInitClients(ctx, s.e.lc)
	if err != nil {
		return nil, err
	}
	s.ic = c
	return c, nil
}
