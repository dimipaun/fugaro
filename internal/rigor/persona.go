package rigor

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed personas/*.md
var personaFS embed.FS

// Role is which side of a run a persona text is written for: the coder that
// writes the change, or the reviewer that judges it (LD1). The two are one
// file's two halves, so a level's stance — what it cares about, what it
// deliberately ignores, what it hands back — is written once and read with the
// part the stage needs.
type Role string

const (
	// Coder is the part the implement, fix and follow-up stages carry.
	Coder Role = "coder"
	// Reviewer is the part the review stages carry: the same level's standard
	// turned on the change rather than on the work (spec §5).
	Reviewer Role = "reviewer"
)

// The two headings a persona file is split on. A file carries one of each, in
// this order, under its own title; anything that looks like a third heading is
// an error rather than text, so a misspelled one cannot silently swallow the
// half it was meant to open.
const (
	coderHeading    = "## Coder"
	reviewerHeading = "## Reviewer"
)

// Persona is the stance a level's coder or reviewer runs with: the text a
// stage's prompt carries, so the level shapes both the work and the standard
// it is judged by (LD1, LD10, spec §2, §5).
//
// The texts are embedded in the binary and are not overridable (LD10): a
// persona a repository could write would be a repository choosing how strictly
// its own change is made and judged.
//
// level is one of Rigors and role one of Coder and Reviewer. Any other pair
// answers "" — a caller never passes one, since Resolve checks Known before it
// answers a level and the role is the caller's own constant — and it must not
// fall through to a default: a level running under another level's stance
// would be rigour nobody chose.
func Persona(level Rigor, role Role) string {
	return personaTexts[level][role]
}

// personaTexts is every level's coder and reviewer text, read from the
// embedded files once, when the package loads.
var personaTexts = loadPersonas()

// loadPersonas reads one embedded file per level into its two parts.
//
// A file it cannot read or split is a panic and not an empty text: the files
// are embedded from this repository's own tree and the wording tests pin every
// part, so the only way here is a broken edit — and a stage silently handed no
// persona would be a stage asked nothing.
func loadPersonas() map[Rigor]map[Role]string {
	texts := make(map[Rigor]map[Role]string, len(Rigors))
	for _, level := range Rigors {
		name := "personas/" + string(level) + ".md"
		src, err := personaFS.ReadFile(name)
		if err != nil {
			panic("rigor: the embedded persona " + name + " cannot be read: " + err.Error())
		}
		parts, err := personaParts(name, string(src))
		if err != nil {
			panic(err.Error())
		}
		texts[level] = parts
	}
	return texts
}

// personaParts splits one persona file into its coder and reviewer text: the
// prose under `## Coder` and under `## Reviewer`, each trimmed, with the title
// above them dropped.
//
// A missing, empty, duplicated or out-of-order part is an error, as is any
// other `## ` heading: the format is the whole of the parsing, and each of
// those would otherwise reach an agent as a level with half a stance.
func personaParts(name, src string) (map[Role]string, error) {
	out := make(map[Role]string, 2)
	var role Role
	var body []string
	flush := func() error {
		if role == "" {
			return nil
		}
		text := strings.TrimSpace(strings.Join(body, "\n"))
		if text == "" {
			return fmt.Errorf("rigor: %s: the %s part is empty", name, role)
		}
		if _, seen := out[role]; seen {
			return fmt.Errorf("rigor: %s: the %s part appears twice", name, role)
		}
		out[role] = text
		return nil
	}
	for _, line := range strings.Split(src, "\n") {
		switch line {
		case coderHeading:
			if _, seen := out[Reviewer]; seen || role == Reviewer {
				return nil, fmt.Errorf("rigor: %s: the coder part comes after the reviewer part", name)
			}
			if err := flush(); err != nil {
				return nil, err
			}
			role, body = Coder, nil
			continue
		case reviewerHeading:
			if err := flush(); err != nil {
				return nil, err
			}
			role, body = Reviewer, nil
			continue
		}
		if strings.HasPrefix(line, "## ") {
			return nil, fmt.Errorf("rigor: %s: unknown heading %q", name, line)
		}
		if role != "" {
			body = append(body, line)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	for _, r := range []Role{Coder, Reviewer} {
		if _, ok := out[r]; !ok {
			return nil, fmt.Errorf("rigor: %s: no %s part", name, r)
		}
	}
	return out, nil
}
