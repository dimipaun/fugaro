package workspace

import "gopkg.in/yaml.v3"

// FileForm writes a Record as the file writes it: the shape
// schemas/workspace.schema.json describes, with the cap back in dollars rather
// than in the micro-dollars Record holds. Nothing in production writes the
// record -- a person writes it by hand, and W2 publishes a database node and
// not this file -- so the conversion has no production caller, and the test
// that holds the schema, the fixture and the struct to one shape is what
// needs it. It is the other half of Parse: what Parse reads, this writes, and
// a record that does not survive the pair is a struct and a schema that
// disagree.
func FileForm(r *Record) ([]byte, error) {
	members := make([]fileMember, 0, len(r.Members))
	for _, m := range r.Members {
		repos := m.Repos
		if repos == nil {
			repos = []string{}
		}
		members = append(members, fileMember{Project: m.Project, Repos: &repos})
	}
	f := fileRecord{Name: r.Name, Members: &members}
	if r.Cap.Set {
		f.Cap = &fileCap{Monthly: r.Cap.Monthly.USD()}
	}
	return yaml.Marshal(f)
}
