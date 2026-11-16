package cli

import (
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// update_licence.go is the update's own half of the licence renewal job (plan
// K4.2 of docs/licence-key-v2), and it is deliberately the smallest half:
// `fugaro update` is the only command that creates the job, its account and the
// empty container the renewal secret is written into, and the whole of what it
// has to decide is whether THIS plan is one the module's own condition admits.
//
// What the job is, in one place (infra.LicenceSpec, whose doc comments carry
// the reasoning): a scheduled `fugaro licence renew --job`, running as its own
// account, reading and writing one object of the runs bucket — the key — and
// one Secret Manager container the module creates EMPTY. Nothing in the core
// reads a key (D524): an installation that buys no licence holds a job that
// no-ops and says so, an account with two grants and a container with no
// version in it, which is the state every installation with a coordinator ends
// up in and the state K3.2's `fugaro licence set` writes into.
//
// Why the switch is the coordinator's and not a flag of its own: the job's
// image is the coordinator's image (one CLI binary serves the sweep, the front
// door and this job, infra.Licence's spec takes HistoryImage), so an
// installation that has not opted into the coordinator has no tagged image to
// run and no job — and `fugaro licence status` says renewal is by hand there
// (TN5). That is one fact, and both sides read the same one: this function
// asks spec.Coordinator.DeployJob, which coordinatorJob has just computed from
// the image's existence and the credentials the sweep needs, and
// modules/installation/licence.tf asks local.deploy_coordinator. A new flag
// would be a second switch over one fact, and the pair would drift the first
// time a run set one and not the other.
//
// Nothing here is a prompt, and nothing here may be: on a managed project the
// plan carries the new resources and the gate applies it, and on every other
// installation the resources arrive in the ordinary confirmation the update
// already asks for a plan that creates something (docs/no-prompt-policy). The
// licence job asks no question of its own, exactly as it has none to ask: it
// creates a job that does nothing until a person runs `fugaro licence set`.

// licenceJob completes the installation spec's licence renewal job: it is part
// of the plan exactly where the coordinator's own job is deployed, and the
// block is dropped where it is not.
//
// Dropping it is not decoration. The module reads the same condition, so a
// block left in a plan that does not deploy the coordinator's job creates
// nothing — but it would be a key in the tfvars that plans nothing and says
// nothing, the trap the module's own variable comments name, and it would make
// the first `fugaro update --firebase-project` of an installation that has just
// opted in write a document no apply of its reads. With the block dropped, the
// state every installation that never opted in plans is byte for byte the
// document the release before this one wrote, which is what makes a converge of
// such an installation a no-op (TestUpdateAddsTheRenewalJobAndLeavesARunInFlightRunning).
func (r *initRun) licenceJob(lc *localcfg.Config, spec infra.InstallationSpec) (infra.InstallationSpec, error) {
	if spec.Coordinator == nil || !spec.Coordinator.DeployJob {
		spec.Licence = nil
		return spec, nil
	}
	if spec.Licence == nil {
		l, err := infra.Licence(lc, spec.RunsBucket)
		if err != nil {
			return spec, initErr(err)
		}
		spec.Licence = &l
	}
	return spec, nil
}
