package plugin_test

// IS12's own checks over the fugaro:images skill: every command and flag it
// names is one the released CLI answers to (the real tree, `cli.NewRootCmd()`),
// it never builds an image locally, the Dockerfiles it teaches carry no
// credential in a layer, and a rerun proposes only what differs from the
// project's own setup.
//
// What these tests pin is the skill's *text*, because a skill is what an agent
// reads: the words are the product (the plan's honest limit). The behaviour is
// Check 500, which the owner runs on the sandbox project. The code's own half of
// the secrets rule is elsewhere: a Dockerfile whose ARG or ENV is named like a
// credential, or whose clone URL carries userinfo, is refused when the catalog
// reads it (IS11b, internal/catalog/fetch.go).

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// imagesSkillFile is the skill's SKILL.md, read from the repository.
func imagesSkillFile(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, "plugin/skills/images/SKILL.md")
}

// imagesSkillFiles returns the images skill's files, parsed the way the shipped
// lint parses them, so these tests read the same units — a fenced line and an
// inline code span — it does.
func imagesSkillFiles(t *testing.T) []*skillFile {
	t.Helper()
	files, err := loadSkillFiles(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []*skillFile
	skill := false
	for _, f := range files {
		if f.skill != "images" {
			continue
		}
		out = append(out, f)
		skill = skill || f.isSkill
	}
	if !skill {
		t.Fatal("the plugin carries no images skill (SKILL.md): IS12 adds fugaro:images")
	}
	return out
}

// TestImagesSkillNamesRealCommands: every command and flag the skill names is
// one the released CLI answers to, and the skill names the commands its flow is
// made of — the survey a rerun compares against, the entry and map writers, the
// one build, the bake switch and the file's own validation.
//
// The flag half matters as much as the name half: `fugaro image add` gained its
// source flags (IS1's era, D1150/D1151) and a skill that spells `--image-name`
// would be teaching a command no release has. The commands are resolved against
// the real tree, so all of it is the tree's answer and not this file's memory.
func TestImagesSkillNamesRealCommands(t *testing.T) {
	ci := newCommandIndex()
	var named []string
	for _, f := range imagesSkillFiles(t) {
		for _, u := range f.units {
			if u.userRuns || strings.Contains(u.info, "fugaro.yaml") {
				continue // the user's own terminal, or a file that is not a command
			}
			for _, c := range ci.commands(u.text) {
				if len(c.args) == 0 || strings.ContainsAny(c.args[0], "<[…") {
					continue // a mention of the program, or a placeholder
				}
				for _, v := range ci.checkCommand(f, u.line, u.text, c) {
					t.Error(v)
				}
				named = append(named, c.path)
			}
		}
	}
	// A parser that stopped finding commands must not make the check above pass
	// by checking nothing, so the commands the flow is made of are required.
	for _, want := range []string{
		"fugaro image survey",  // step 1, and the setup a rerun compares against
		"fugaro image add",     // an entry, however its Dockerfile arrives
		"fugaro image map set", // a repository onto an entry
		"fugaro image ls",      // what the catalog holds today
		"fugaro image build",   // the only build there is: Cloud Build, never local
		"fugaro settings set",  // the bake switch
		"fugaro doctor",        // the installation, and the catalog's own state
	} {
		if !slices.Contains(named, want) {
			t.Errorf("the skill never names %q, which is one of the commands its flow is made of (named: %v)", want, named)
		}
	}
	// And the check bites: a subcommand the tree does not have is refused, and
	// so is a flag the command does not carry, so the pass above is the tree's
	// answer and not an empty loop.
	f := imagesSkillFiles(t)[0]
	missing := ci.commands("fugaro image propose")
	if len(missing) != 1 || len(ci.checkCommand(f, 1, "fugaro image propose", missing[0])) == 0 {
		t.Error("`fugaro image propose` was accepted against the real tree, and no release has that subcommand")
	}
	wrongFlag := ci.commands("fugaro image add web --image-name app")
	if len(wrongFlag) != 1 || len(ci.checkCommand(f, 1, "fugaro image add web --image-name app", wrongFlag[0])) == 0 {
		t.Error("`fugaro image add --image-name` was accepted against the real tree, and the flag is `--image`")
	}
	// D1903 keeps the cap in the skill and nowhere else: the catalog validates
	// no count, so this text is the whole of the rule, and a session that loses
	// it proposes the sixth image a project may not have.
	if skill := imagesSkillFile(t); !strings.Contains(skill, "four") || !strings.Contains(skill, "five") {
		t.Error("the skill never states the cap on how many images a project may have (fewer than five, D1903)")
	}
}

// localBuildRE is what no skill may run or teach: an image built on the machine
// a person happens to sit at. The flag is `fugaro image build --local` and the
// raw daemon call is `docker build`. Both are wrong for the reason the spec
// states, and it is an architecture rule rather than a preference: Cloud Run
// runs linux/amd64, Cloud Build builds it there, and a laptop that builds the
// image itself is a laptop whose architecture the job may then run on.
var localBuildRE = regexp.MustCompile(`--local\b|\bdocker build\b`)

// TestImagesSkillNeverLocalBuild: the skill builds no image locally, says Cloud
// Build is where every image it proposes is built, and the setup skill's own
// per-repository local build loop is gone with it (F11).
func TestImagesSkillNeverLocalBuild(t *testing.T) {
	for _, f := range imagesSkillFiles(t) {
		for _, u := range f.units {
			if m := localBuildRE.FindString(u.text); m != "" {
				t.Errorf("%s:%d: the skill names %q, and an image is built by Cloud Build and never on the user's machine:\n%s", f.path, u.line, m, u.text)
			}
		}
	}
	skill := imagesSkillFile(t)
	if !strings.Contains(skill, "Cloud Build") {
		t.Error("the skill never names Cloud Build, and that is where every image it proposes is built")
	}
	// The setup skill's per-repository loop was the local build; IS12 replaces
	// it with the catalog path, so the command it looped on is gone from it.
	if setup := readRepoFile(t, "plugin/skills/setup/SKILL.md"); strings.Contains(setup, "fugaro image build --local") {
		t.Error("skills/setup/SKILL.md still loops on `fugaro image build --local`: repo-level images are disabled, and its build loop is the catalog path (F11, D1900)")
	}
	// Predicates that matched nothing would pass both loops by saying nothing:
	// these are the lines they exist to catch.
	for _, bad := range []string{
		"Run `fugaro image build --local --json` and read the smoke checks.",
		"Build it with `docker build -t fugaro:test .` first.",
		"`fugaro image build --local` writes the image the job runs.",
	} {
		if localBuildRE.FindString(bad) == "" {
			t.Errorf("the check does not catch %q, so the pass above proves nothing", bad)
		}
	}
	// And the shape it does teach passes it, so the pass is Cloud Build's own
	// and not a check that refuses every build line.
	if localBuildRE.MatchString("fugaro image build web") {
		t.Error("the check refuses the cloud build the skill exists to run")
	}
}

var (
	// secretArgRE is a build argument or environment variable named like a
	// credential. An ARG is in the image's build history and an ENV is in the
	// layer, and neither can be taken back out.
	secretArgRE = regexp.MustCompile(`(?i)^\s*(ARG|ENV)\s+\S*(TOKEN|SECRET|PASSWORD|PASSWD|APIKEY|API_KEY|CREDENTIAL|_KEY)\b`)
	// secretCopyRE copies a file that holds one into the image: a registry
	// dotfile, a key, an ssh directory, a credential file by its own name.
	secretCopyRE = regexp.MustCompile(`(?i)^\s*COPY\b[^\n]*(token|secret|password|passwd|credential|\.npmrc|\.yarnrc|\.netrc|id_rsa|\.ssh)`)
	// userinfoURLRE is a clone URL carrying a credential in its own text:
	// https://user:token@host/repo. The catalog refuses one when it reads the
	// Dockerfile (IS11b), and a skill must not teach one either.
	userinfoURLRE = regexp.MustCompile(`(?i)https?://[^\s/'"]+:[^\s@/'"]+@`)
)

// TestImagesSkillNoBuildArgSecrets: the Dockerfiles the skill teaches carry no
// credential, and it says how a token reaches a build instead — a BuildKit
// secret mount, never a build argument or a COPY (F14, F22, IS11b).
//
// Every fenced line is judged, and an inline span only for the clone URL: prose
// has to be able to name a forbidden instruction to forbid it, and a Dockerfile
// cannot be written in a sentence.
func TestImagesSkillNoBuildArgSecrets(t *testing.T) {
	for _, f := range imagesSkillFiles(t) {
		for _, u := range f.units {
			if u.inBlock {
				if m := secretArgRE.FindString(u.text); m != "" {
					t.Errorf("%s:%d: %q is a credential in an image's build history: a token reaches a build in a secret mount, never an ARG or ENV:\n%s", f.path, u.line, strings.TrimSpace(m), u.text)
				}
				if m := secretCopyRE.FindString(u.text); m != "" {
					t.Errorf("%s:%d: %q copies a credential into the image:\n%s", f.path, u.line, strings.TrimSpace(m), u.text)
				}
			}
			if m := userinfoURLRE.FindString(u.text); m != "" {
				t.Errorf("%s:%d: %q is a clone URL with userinfo, which puts the credential in the layer:\n%s", f.path, u.line, m, u.text)
			}
		}
	}
	skill := imagesSkillFile(t)
	if !strings.Contains(skill, "--mount=type=secret") {
		t.Error("the skill never names `--mount=type=secret`, and that is how a token reaches a build without reaching a layer")
	}
	lowered := strings.ToLower(skill)
	if !strings.Contains(lowered, "layer") {
		t.Error("the skill never says a secret does not reach a layer, which is the whole of the rule")
	}
	// The predicates, on the lines they exist to catch: a check that matched
	// nothing would pass the loop above by looking at nothing.
	for name, c := range map[string]struct {
		re   *regexp.Regexp
		text string
	}{
		"a build argument":  {secretArgRE, "ARG NPM_TOKEN"},
		"an ENV token":      {secretArgRE, "ENV GIT_TOKEN=abc"},
		"a copied dotfile":  {secretCopyRE, "COPY .npmrc /home/fugaro/.npmrc"},
		"a userinfo remote": {userinfoURLRE, "RUN git clone https://user:tok3n@github.com/acme/app"},
	} {
		if c.re.FindString(c.text) == "" {
			t.Errorf("the check does not catch %s (%q)", name, c.text)
		}
	}
	// And the shape the skill does teach passes all three, so the pass above is
	// the mount's own shape and not silence.
	mount := `RUN --mount=type=secret,id=NPM_TOKEN,uid=1000 if test -e /run/secrets/NPM_TOKEN; then export NPM_TOKEN="$(cat /run/secrets/NPM_TOKEN)"; fi; npm ci`
	for name, re := range map[string]*regexp.Regexp{"an argument": secretArgRE, "a copy": secretCopyRE, "a userinfo URL": userinfoURLRE} {
		if re.MatchString(mount) {
			t.Errorf("the check refuses the secret-mount shape it exists to allow, as %s", name)
		}
	}
}

// TestImagesSkillRerunProposesDiffOnly: a rerun compares what it found with the
// project's own setup — the catalog, the map and the bake switch, all three in
// the survey's `setup` member — and proposes only what differs, never reshuffling
// a working project (F16).
func TestImagesSkillRerunProposesDiffOnly(t *testing.T) {
	body := ""
	for _, f := range imagesSkillFiles(t) {
		body += strings.Join(f.lines, "\n") + "\n"
	}
	section := imageSkillSection(t, body, "Rerunning")
	for _, want := range []string{
		"fugaro image survey --json", "setup", "catalog", "image_map", "bake_repos",
		"only", "differs", "never", "reshuffle",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the rerun section never says %q, so a rerun cannot tell a change from what the project already decided:\n%s", want, section)
		}
	}
	// The three the comparison is made of are the survey's `setup` member's, and
	// the command's own document says so: a skill that invented a fourth source
	// would be reading a state the CLI never reports. This is the half of the
	// claim that is not the skill's to make.
	cmd, _, err := newCommandIndex().root.Find([]string{"image", "survey"})
	if err != nil || cmd == nil {
		t.Fatalf("the tree has no `fugaro image survey`: %v", err)
	}
	if long := strings.Join(strings.Fields(cmd.Long), " "); !strings.Contains(long, "catalog, repository-to-image map and bake setting") {
		t.Errorf("`fugaro image survey`'s long help no longer names the three a rerun compares against: %q", long)
	}
	// Nothing is proposed again either: a rerun that re-proposed an image the
	// project already runs would churn every repository's mapping.
	lowered := strings.ToLower(section)
	if strings.Contains(lowered, "propose the images again") || strings.Contains(lowered, "start over") {
		t.Errorf("the rerun section re-proposes a project's images rather than its differences:\n%s", section)
	}
}

// imageSkillSection returns the `## title` section of a skill file, up to the
// next top-level heading.
func imageSkillSection(t *testing.T, body, title string) string {
	t.Helper()
	head := "## " + title + "\n"
	i := strings.Index(body, head)
	if i < 0 {
		t.Fatalf("the skill has no %q section", head)
	}
	rest := body[i+len(head):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}
