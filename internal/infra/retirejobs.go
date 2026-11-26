package infra

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	run "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// This file is issue #3686's second half: the shared project jobs an
// installation no longer declares.
//
// A project job is one instance of modules/project per (profile, provider,
// account) — roots/installation/main.tf calls the module once per entry of
// var.project_jobs, and the entry's KEY is the job's Cloud Run name. So a
// job stops existing when the configuration stops naming its triple, and the
// instance's addresses are then the plan of its destruction. Nothing but the
// job itself is protected from deletion, which is why the retirement is two
// halves: the job address out of Terraform's state (nothing can delete it
// while its deletion_protection says true), the API delete for the job
// itself, and the apply for everything the instance owned beside it — its
// service account and its grants.
//
// Nothing here decides whether a job is quiet: ProjectInFlightJobs does, and
// retiring a job with an execution on it is what it refuses. Deleting the job
// cancels that execution, and the run behind it is a person's work going away
// with no report anywhere (InFlightJobs' own rule, and the reason the
// question is asked before anything leaves the state).

// The two module instances of the installation root that hold one instance
// per job: the coding jobs, and the review-only variants of CSR7 (the same
// module over the same axis, so a triple's variant goes with its coding job).
const (
	ProjectJobAxis       = "project"
	ProjectReviewJobAxis = "project_review"
)

// ProjectJobRetiredCause is why a project job is retired, in the user's
// words: the configuration stopped naming its (profile, provider, account).
const ProjectJobRetiredCause = "no profile, provider and account names this job any more: the config no longer declares it"

// ProjectJobRetiredKind is the Kind of a retired instance's own Cloud Run
// job. The rows beside it — the service account and the grants — keep the
// kind this file gives them, because what a caller does with them is decided
// by their resource type and never by a word (FillFromState's own rule).
const ProjectJobRetiredKind = "project job"

// ProjectJobRetirements is every address the installation's state still holds
// under one of the two project module instances whose for_each key vars no
// longer declares: the instance whole, so the job, its service account and
// its grants go together and nothing of it is left managed.
//
// vars is the document the plan of this run reads (the workdir's
// terraform.tfvars.json), never a document this package composes: the carries
// (CarriedProjectJobs and its neighbours) write a job the configuration
// stopped composing back into exactly those bytes, and a job those bytes
// declare is one the plan produces. Asking the config instead would name the
// job of a carry as a retirement and delete what a release is keeping.
//
// The set is the state's, not the config's, for the same reason one step on:
// what the plan DELETES is what the state holds and the document does not
// name, and an address the state does not hold is not Fugaro's to remove.
func ProjectJobRetirements(vars []byte, st *tf.State) ([]Retirement, error) {
	if st == nil {
		return nil, nil
	}
	declared, err := DeclaredProjectJobs(vars)
	if err != nil {
		return nil, err
	}
	var rets []Retirement
	for _, addr := range st.Addresses() {
		instance, key, ok := projectJobKey(addressSegments(addr))
		if !ok || declared[instance][key] {
			continue
		}
		rets = append(rets, Retirement{Address: addr, Kind: ProjectJobRetiredKind, Cause: ProjectJobRetiredCause})
	}
	slices.SortFunc(rets, byAddress)
	rets = slices.CompactFunc(rets, func(a, b Retirement) bool { return a.Address == b.Address })
	// The state's own values fill what an address cannot say: a job's name,
	// its region, its labels and whether an ordinary apply can delete it at
	// all. The default is the protective one for a job and only for a job: a
	// service account and a grant stay in the state, because the apply
	// deletes them and an address taken out of the state is an address
	// nothing destroys.
	FillFromState(st, rets)
	return rets, nil
}

// DeclaredProjectJobs is the for_each keys vars declares, one set per module
// instance: the coding jobs under project_jobs and the review-only variants
// under project_review_jobs. The two are kept apart because one job name
// answers in both sets — a review variant carries its coding job's name
// (ImplicitReviewJobName) — and a name declared on one axis says nothing
// about the other.
func DeclaredProjectJobs(vars []byte) (map[string]map[string]bool, error) {
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	out := map[string]map[string]bool{}
	for _, axis := range []struct{ instance, key string }{
		{ProjectJobAxis, ProjectJobsKey},
		{ProjectReviewJobAxis, ProjectReviewJobsKey},
	} {
		set := map[string]bool{}
		if jobs, ok := doc[axis.key].(map[string]any); ok {
			for name := range jobs {
				set[name] = true
			}
		}
		out[axis.instance] = set
	}
	return out, nil
}

// projectJobKey reads the module instance and the for_each key out of an
// address's own segments: module.project["fugaro-x"]... is the coding job of
// that name and module.project_review["fugaro-x"]... its review variant. The
// key is the module's own for_each key — the job's Cloud Run name — and no
// name is derived here.
func projectJobKey(segs []string) (instance, key string, ok bool) {
	if len(segs) < 3 || segs[0] != "module" {
		return "", "", false
	}
	for _, inst := range []string{ProjectJobAxis, ProjectReviewJobAxis} {
		key, found := strings.CutPrefix(segs[1], inst+`["`)
		if !found || !strings.HasSuffix(key, `"]`) {
			continue
		}
		return inst, strings.TrimSuffix(key, `"]`), true
	}
	return "", "", false
}

// ProjectJobRetireStops is the rows of rets no update retires by itself, each
// with its reason: the same allowlist RetireStops reads for a repository
// root, asked of the project module's own tree. A row that is not in this
// list is derived — the job, its service account, its grants — and one row
// here stops ALL of them, because the retirement is one state removal, one
// delete and one apply: taking the derived addresses down and leaving a
// container behind would destroy half of an instance and leave the rest.
func ProjectJobRetireStops(rets []Retirement) []RetireStop {
	var out []RetireStop
	for _, r := range rets {
		segs := addressSegments(r.Address)
		if _, _, ok := projectJobKey(segs); !ok {
			out = append(out, RetireStop{Address: r.Address, Why: "it is not an address of a project module instance: the retirement takes the instance a job name keys and nothing else"})
			continue
		}
		typ, _, _ := resourceOf(segs)
		if retirable, why := tf.RetirableType(typ); !retirable {
			out = append(out, RetireStop{Address: r.Address, Why: typ + ": " + why})
		}
	}
	return out
}

// The labels modules/project/job.tf writes on a shared job: the mark every
// Fugaro job carries, and the (profile, provider, account) triple that
// identifies it. They are the cloud's own record of what a job is — a
// project job carries no repository's name, and nothing else on the job says
// which triple deployed it — so both readers here ask for them and never
// derive a name.
const (
	labelProvider = "fugaro_provider"
	labelAccount  = "fugaro_account"
)

// LiveProjectJob is one shared job of an installation as the cloud holds it:
// the name Cloud Run knows it by, its region, its own labels, and the triple
// those labels name.
type LiveProjectJob struct {
	Name     string
	Region   string
	Labels   map[string]string
	Profile  string
	Provider string
	Account  string
}

// LiveProjectJobs is every job of the installation's region wearing the
// project module's own labels, and answered=false for a listing that could
// not be read at all.
//
// It is the second half of the retirement and the whole of the switch's own
// check: what a config stops declaring is a fact of the config, and what the
// installation still RUNS is a fact of the cloud. A job whose name the
// document no longer carries is an orphan of the installation whether or not
// the state still manages it, so a later update finds it again by these
// labels alone — which is what makes a job this run could not destroy (a run
// in flight, a call this operator may not make) a job the NEXT run retires.
func LiveProjectJobs(ctx context.Context, c *Clients, gcpProject, region string) (jobs []LiveProjectJob, answered bool, warning string) {
	if c == nil || c.Run == nil {
		return nil, false, fmt.Sprintf("the installation's shared jobs could not be read (%v)", errNoRunClient)
	}
	parent := "projects/" + gcpProject + "/locations/" + region
	err := c.Run.Projects.Locations.Jobs.List(parent).Pages(ctx, func(p *run.GoogleCloudRunV2ListJobsResponse) error {
		for _, j := range p.Jobs {
			if j == nil || j.Labels[gcp.LabelManaged] != gcp.ManagedValue || j.Labels[gcp.LabelProfile] == "" {
				continue
			}
			jobs = append(jobs, LiveProjectJob{
				// The listing names a job by its full resource path; the API
				// delete and the executions read both want the short one.
				Name: lastSegment(j.Name), Region: region, Labels: maps.Clone(j.Labels),
				Profile: j.Labels[gcp.LabelProfile], Provider: j.Labels[labelProvider], Account: j.Labels[labelAccount],
			})
		}
		return nil
	})
	if err != nil {
		return nil, false, fmt.Sprintf("the installation's shared Cloud Run jobs could not be listed: %v. List them by hand with `gcloud run jobs list --project %s --region %s`", err, gcpProject, region)
	}
	slices.SortFunc(jobs, func(a, b LiveProjectJob) int { return strings.Compare(a.Name, b.Name) })
	return jobs, true, ""
}

// ProjectInFlightJobs is the retired rows an execution is still running on,
// and answered=false for a question that could not be read at all. It is
// InFlightJobs' rule for the retirement this file is about, and the reason it
// exists beside it rather than inside it is the shape of the question: a
// repository root's jobs are named by a variant the state carries, and a
// project job is named by its for_each key.
//
// The job a row with no name is one nobody can delete and nobody can ask
// about, and the caller refuses on it exactly as retireBlockers does: a
// retirement that cannot name the job it is about is not one that may remove
// the address.
func ProjectInFlightJobs(ctx context.Context, c *Clients, gcpProject string, rets []Retirement) (jobs []Retirement, answered bool, warning string) {
	if c == nil || c.Run == nil {
		return nil, false, fmt.Sprintf("whether an execution is running on this installation's project jobs could not be read (%v)", errNoRunClient)
	}
	for _, ret := range rets {
		if ret.Kind != ProjectJobRetiredKind {
			continue
		}
		if ret.Job == "" || ret.Region == "" {
			return nil, false, fmt.Sprintf("the Terraform state names no Cloud Run job behind %s, so whether an execution is running on it cannot be read", ret.Address)
		}
		busy, read, why := JobInFlight(ctx, c, gcpProject, ret.Region, ret.Job)
		if !read {
			return nil, false, fmt.Sprintf("whether an execution is running on %s (%s) could not be read: %s", ret.Job, ret.Address, why)
		}
		if busy {
			jobs = append(jobs, ret)
		}
	}
	return jobs, true, ""
}

// JobInFlight is whether job has an execution that has not finished, and
// answered=false for a question that could not be read: the one read both
// retirement flows rest on, so that "could not look" and "nothing is running"
// are never the same answer for a call that deletes a job.
//
// An execution is in flight unless it has a completion time, which is the
// same rule stateOf reads for a run's own status: pending is as much a run as
// running is, and deleting the job under it cancels it. A job that is not
// there at all is 404 and answers "nothing is running on it", never a
// failure: the state may outlive a job somebody deleted by hand.
func JobInFlight(ctx context.Context, c *Clients, gcpProject, region, job string) (busy, answered bool, warning string) {
	if c == nil || c.Run == nil {
		return false, false, fmt.Sprintf("whether an execution is running on job %s could not be read (%v)", job, errNoRunClient)
	}
	parent := "projects/" + gcpProject + "/locations/" + region + "/jobs/" + job
	err := c.Run.Projects.Locations.Jobs.Executions.List(parent).Pages(ctx, func(p *run.GoogleCloudRunV2ListExecutionsResponse) error {
		for _, e := range p.Executions {
			if e != nil && e.CompletionTime == "" {
				return errExecutionRunning
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, errExecutionRunning):
		return true, true, ""
	case err != nil && !notFound(err):
		return false, false, fmt.Sprintf("%v. Ask it by hand with `gcloud run jobs executions list --project %s --region %s --job %s`", err, gcpProject, region, job)
	}
	return false, true, ""
}
