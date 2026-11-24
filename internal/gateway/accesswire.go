package gateway

// The access contract's header names, as the gateway spells them (C1 (e), R5):
// the craft a call is declared for, the task's grade where it names one, and
// the credential the run chose where it chose one. The run's own calls go
// through this gateway to the credentials service, so these are the names the
// gateway's upstream sends and the names the service reads.
//
// They are a copy, and the copy is the point. The service's own file
// (internal/credsvc/access_wire.go) declares the same three names with the rules
// that validate them, and this package cannot import it: the gateway is the
// loopback proxy a run's calls come through and the service is the process on
// the other side of the wire, so importing one into the other would put the
// service's whole surface — its scope, its audit, its bundle — into the job the
// gateway runs in. The two spellings are held to each other by
// credsvc's TestTheGatewayAndTheServiceSpellTheHeaderAlike, which may import
// both, so a name changed on one side fails a test rather than reaching a
// customer as "the service refused a header it never read".
//
// The gateway sends the declaration and never decides from it: what a call may
// be served by is the repository's access record, read by the service (CM6).
const (
	// HeaderAccessCraft is the craft a call is declared for (config.CraftNames).
	HeaderAccessCraft = "X-Fugaro-Access-Craft"
	// HeaderAccessGrade is the task's own model grade where it names one
	// (config.GradeNames, F38).
	HeaderAccessGrade = "X-Fugaro-Access-Grade"
	// HeaderAccessChosen is the credential the run chose, where it chose one
	// (F44, D3069).
	HeaderAccessChosen = "X-Fugaro-Access-Chosen"
)
