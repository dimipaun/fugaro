package infra

import "github.com/dimipaun/fugaro/internal/localcfg"

// MCPServerServiceName is the coordinator's front door's Cloud Run service
// name, and it may start with fugaro-: that prefix is reserved by the *job*
// listings (HistoryJob), and a Cloud Run service is not one of them
// (CredentialsServiceName's rule). The name is fixed rather than derived: an
// installation has one front door, and a name the owner can read back — and
// that a client's own configuration names in a URL — is worth more than a
// second name to compute.
const MCPServerServiceName = "fugaro-mcp"

// MCPServerAccountID is the front door's own service account, and it is the
// same name as the service because the two are one endpoint (the
// CredentialsAccountID/CredentialsServiceName pair, and the same reason). It
// is deliberately NOT the coordinator's account (CoordinatorAccountID,
// internal/infra's Coordinator): that one reads the GitHub App's private key,
// mints Bitbucket tokens at the credentials service and holds the FP's
// database admin, and a public endpoint whose identity holds any of those
// would make spec.md §8's "there is no credential to reach" false. The front
// door's account holds the runs bucket's read (D932) and nothing else.
const MCPServerAccountID = "fugaro-mcp"

// MCPServerSpec is the installation root's var.mcp_server (D920, D934): the
// front door's own service account, its Cloud Run service and the image it
// runs. It is the whole opt-in — an installation whose root carries no such
// variable plans no account, no service, no public invoker binding and no
// container, which is D934's OFF by default.
type MCPServerSpec struct {
	AccountID string `json:"account_id"`
	Service   string `json:"service"`
	Image     string `json:"image"`
}

// MCPServer is the front door's spec for lc. Its image is the coordinator's
// own, which is the history image's: one CLI binary serves the aggregation
// job and the front door, so the front door needs no image of its own, no
// second push and no second entry in the mirror that fills the base registry
// (Coordinator's comment says the same of the job).
//
// Nothing else about the installation is read here. The state the front door
// serves is the coordinator's, so what it needs is the coordinator's own
// opt-in beside it; whether the coordinator's job itself is deployed is a
// separate opt-in and is not decided here.
func MCPServer(lc *localcfg.Config) (MCPServerSpec, error) {
	host, err := RegistryHost(lc)
	if err != nil {
		return MCPServerSpec{}, err
	}
	return MCPServerSpec{AccountID: MCPServerAccountID, Service: MCPServerServiceName, Image: HistoryImage(host)}, nil
}
