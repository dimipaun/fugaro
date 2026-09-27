package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// baseImageRE matches a published Fugaro base image reference and captures the base name.
var baseImageRE = regexp.MustCompile(`^ghcr\.io/dimipaun/fugaro-([a-z0-9-]+)(:[A-Za-z0-9._-]+)?(@sha256:[0-9a-f]{64})?$`)

// heredocRE finds heredoc openers such as <<EOF, <<-EOF and <<'EOF'.
var heredocRE = regexp.MustCompile(`<<-?["']?([A-Za-z_][A-Za-z0-9_]*)["']?`)

type instruction struct {
	cmd  string // upper-cased, such as RUN
	args string
}

type stage struct {
	image, alias string
	body         []instruction
}

// directiveRE matches a parser directive such as "# escape=`", which Docker
// honours only in the comment lines at the very top of the file.
var directiveRE = regexp.MustCompile(`^#\s*([A-Za-z][A-Za-z0-9]*)\s*=\s*(.*?)\s*$`)

// parseDockerfile splits a Dockerfile into instructions. It joins
// continuation lines, drops comment lines, and folds heredoc bodies into
// their instruction's arguments, so a heredoc line that happens to start
// with FROM is not read as an instruction. Like Docker's own parser, it
// honours a leading "# escape=" directive (a backtick or a backslash), and
// treats the escape character followed only by spaces or tabs as a
// continuation.
func parseDockerfile(data []byte) []instruction {
	var out []instruction
	var cur strings.Builder
	var heredocs []string // delimiters still open for the last instruction
	escape := `\`
	directives := true // still in the leading run of parser directives
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		if directives {
			m := directiveRE.FindStringSubmatch(line)
			if m == nil {
				directives = false
			} else {
				if strings.EqualFold(m[1], "escape") && (m[2] == "`" || m[2] == `\`) {
					escape = m[2]
				}
				continue
			}
		}
		if len(heredocs) > 0 {
			last := &out[len(out)-1]
			last.args += "\n" + line
			if strings.TrimSpace(line) == heredocs[0] {
				heredocs = heredocs[1:]
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if trimmed := strings.TrimRight(line, " \t"); strings.HasSuffix(trimmed, escape) {
			cur.WriteString(strings.TrimSuffix(trimmed, escape) + " ")
			continue
		}
		cur.WriteString(line)
		text := strings.TrimSpace(cur.String())
		cur.Reset()
		if text == "" {
			continue
		}
		cmd, args := text, ""
		if i := strings.IndexFunc(text, unicode.IsSpace); i >= 0 {
			cmd, args = text[:i], strings.TrimSpace(text[i:])
		}
		out = append(out, instruction{cmd: strings.ToUpper(cmd), args: args})
		for _, m := range heredocRE.FindAllStringSubmatch(args, -1) {
			heredocs = append(heredocs, m[1])
		}
	}
	return out
}

// splitStages groups instructions by FROM. It returns the ARGs declared
// before the first FROM and the stages in order.
func splitStages(ins []instruction) (globalArgs []string, stages []stage) {
	for _, in := range ins {
		if in.cmd == "FROM" {
			var s stage
			fields := strings.Fields(in.args)
			for i := 0; i < len(fields); i++ {
				switch {
				case strings.HasPrefix(fields[i], "--"):
				case s.image == "":
					s.image = fields[i]
				case strings.EqualFold(fields[i], "AS") && i+1 < len(fields):
					s.alias = strings.ToLower(fields[i+1])
					i++
				}
			}
			stages = append(stages, s)
			continue
		}
		if len(stages) == 0 {
			if in.cmd == "ARG" {
				name, _, _ := strings.Cut(in.args, "=")
				globalArgs = append(globalArgs, strings.TrimSpace(name))
			}
			continue
		}
		stages[len(stages)-1].body = append(stages[len(stages)-1].body, in)
	}
	return globalArgs, stages
}

// LintDockerfile checks a repository Dockerfile (the dockerfile: escape
// hatch) against the derived-image contract for a workflow on base (design
// §7.2). The final stage, followed back through any earlier stages it builds
// on, must start FROM a Fugaro base image, bake the checkout into
// /work/repo, and leave the base's user, working directory and init process
// in place. It returns one message per broken rule. `fugaro image build
// --local` checks the rest when it smoke-tests the built image.
func LintDockerfile(data []byte, base string) []string {
	globalArgs, stages := splitStages(parseDockerfile(data))
	if len(stages) == 0 {
		return []string{"has no FROM instruction"}
	}
	// chain is the final stage and the stages it builds on, root first.
	chain := []stage{stages[len(stages)-1]}
	for len(chain) <= len(stages) {
		i := slices.IndexFunc(stages, func(s stage) bool { return s.alias != "" && s.alias == strings.ToLower(chain[0].image) })
		if i < 0 {
			break
		}
		chain = append([]stage{stages[i]}, chain...)
	}
	var msgs []string
	root := chain[0].image
	switch m := baseImageRE.FindStringSubmatch(root); {
	case root == "${FUGARO_BASE}" || root == "$FUGARO_BASE":
		if !slices.Contains(globalArgs, "FUGARO_BASE") {
			msgs = append(msgs, "uses ${FUGARO_BASE} without declaring ARG FUGARO_BASE before the first FROM")
		}
	case m != nil:
		if m[1] != base {
			msgs = append(msgs, fmt.Sprintf("builds FROM fugaro-%s, but the workflow's base is %s", m[1], base))
		}
	default:
		msgs = append(msgs, fmt.Sprintf("must build its final stage FROM ${FUGARO_BASE} or ghcr.io/dimipaun/fugaro-%s, not %s", base, root))
	}
	var workdir, user string
	bakes := false
	for _, s := range chain {
		for _, in := range s.body {
			switch in.cmd {
			case "WORKDIR":
				workdir = in.args
			case "USER":
				user = in.args
			case "ENTRYPOINT":
				msgs = append(msgs, "must not replace the base image's ENTRYPOINT (tini reaps the processes Fugaro kills)")
			case "VOLUME":
				if strings.Contains(in.args, "/work") {
					msgs = append(msgs, "must not declare a VOLUME under /work; it would hide the baked checkout")
				}
			case "RUN", "COPY", "ADD":
				if strings.Contains(in.args, "/work/repo") {
					bakes = true
				}
			}
		}
	}
	if !bakes {
		msgs = append(msgs, "must bake the checkout into /work/repo (no RUN, COPY or ADD writes there); start from the output of `fugaro image render`")
	}
	if workdir != "" && workdir != "/work/repo" {
		msgs = append(msgs, fmt.Sprintf("must leave WORKDIR at /work/repo, not %s", workdir))
	}
	if user != "" && !slices.Contains([]string{"fugaro", "1000", "fugaro:fugaro", "1000:1000"}, user) {
		msgs = append(msgs, fmt.Sprintf("must end as USER fugaro, not %s (Claude Code refuses to skip permission prompts as root)", user))
	}
	return msgs
}
