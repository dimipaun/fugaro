package infra

// ProjectMarkerObject is the object in the runs bucket that names the
// installation's Fugaro project. The installation's Terraform writes it,
// beside the bucket's fugaro_project label and the project_name output;
// launchers can't read a label, so every cloud command reads this object
// to check its project config (design §2.5). It is a safety label, not a
// boundary: no job account can write it, but a launcher could.
const ProjectMarkerObject = "fugaro/project.json"

// ProjectMarker is ProjectMarkerObject's content.
type ProjectMarker struct {
	Version    int    `json:"version"`
	Name       string `json:"name"`
	GCPProject string `json:"gcp_project"`
}
