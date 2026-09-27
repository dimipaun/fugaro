package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// validRepoDockerfile is the smallest repository Dockerfile that meets the
// derived-image contract.
const validRepoDockerfile = `# syntax=docker/dockerfile:1.10
ARG FUGARO_BASE
FROM ${FUGARO_BASE}
ARG REPO_URL
ARG BASE_BRANCH
RUN --mount=type=bind,target=/src \
    git clone --quiet --branch "$BASE_BRANCH" --single-branch "$REPO_URL" /work/repo
RUN /usr/local/lib/fugaro/finalize-checkout /work/repo
`

func TestLintDockerfileAccepts(t *testing.T) {
	cases := map[string]string{
		"minimal":                  validRepoDockerfile,
		"literal base with digest": "FROM ghcr.io/dimipaun/fugaro-web-node:1.2.3@sha256:" + strings.Repeat("a", 64) + "\nCOPY --chown=fugaro . /work/repo\n",
		"multi-stage chain": `ARG FUGARO_BASE
FROM node:24 AS tools
RUN echo not the final stage
FROM ${FUGARO_BASE} AS checkout
RUN git clone "$REPO_URL" /work/repo
FROM checkout
USER root
RUN apt-get update
USER fugaro
WORKDIR /work/repo
`,
		"heredoc and comments": `ARG FUGARO_BASE
# FROM ubuntu:24.04 in a comment does not count
FROM $FUGARO_BASE
RUN <<EOF
set -e
git clone "$REPO_URL" /work/repo
FROM this-line-is-heredoc-body
EOF
`,
	}
	for name, df := range cases {
		t.Run(name, func(t *testing.T) {
			if msgs := LintDockerfile([]byte(df), "web-node"); len(msgs) != 0 {
				t.Fatalf("unexpected problems: %v", msgs)
			}
		})
	}
}

func TestLintDockerfileRejects(t *testing.T) {
	cases := []struct{ name, df, want string }{
		{"no FROM", "RUN true\n", "has no FROM instruction"},
		{"foreign base", "FROM node:24\nRUN git clone x /work/repo\n", "must build its final stage FROM ${FUGARO_BASE} or ghcr.io/dimipaun/fugaro-web-node, not node:24"},
		{"final stage not on base", validRepoDockerfile + "FROM ubuntu:24.04\nRUN true /work/repo\n", "not ubuntu:24.04"},
		{"wrong base", "FROM ghcr.io/dimipaun/fugaro-server-jvm:1\nRUN git clone x /work/repo\n", "builds FROM fugaro-server-jvm, but the workflow's base is web-node"},
		{"undeclared arg", "FROM ${FUGARO_BASE}\nRUN git clone x /work/repo\n", "without declaring ARG FUGARO_BASE"},
		{"no checkout", "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\nRUN npm ci\n", "must bake the checkout into /work/repo"},
		{"workdir moved", validRepoDockerfile + "WORKDIR /app\n", "must leave WORKDIR at /work/repo, not /app"},
		{"ends as root", validRepoDockerfile + "USER root\n", "must end as USER fugaro, not root"},
		{"entrypoint", validRepoDockerfile + "ENTRYPOINT [\"fugaro\"]\n", "must not replace the base image's ENTRYPOINT"},
		{"volume", validRepoDockerfile + "VOLUME /work/repo\n", "must not declare a VOLUME under /work"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := LintDockerfile([]byte(tc.df), "web-node")
			if !strings.Contains(strings.Join(msgs, "\n"), tc.want) {
				t.Fatalf("want a problem containing %q, got %v", tc.want, msgs)
			}
		})
	}
}

func TestCheckLintsDockerfile(t *testing.T) {
	root := t.TempDir()
	cfg, problems := Parse([]byte(webYAML + "    dockerfile: .fugaro/web.Dockerfile\n"))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if err := os.MkdirAll(filepath.Join(root, ".fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".fugaro", "web.Dockerfile"), []byte("FROM node:24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps := Check(cfg, root)
	if !hasProblem(ps, "workflows.web.dockerfile", ".fugaro/web.Dockerfile must build its final stage FROM", 0) ||
		!hasProblem(ps, "workflows.web.dockerfile", "must bake the checkout", 0) {
		t.Fatalf("problems = %v", ps)
	}
}

// TestParseDockerfileContinuations pins two corners of Docker's own parser:
// an escape character followed only by trailing spaces or tabs still
// continues the line, and a leading `# escape=` directive changes the escape
// character (so a trailing backslash is then literal).
func TestParseDockerfileContinuations(t *testing.T) {
	cases := []struct {
		name, df string
		want     []instruction
	}{
		{"trailing whitespace after backslash", "FROM base\nRUN echo a \\ \t\n    && echo b\n",
			[]instruction{{"FROM", "base"}, {"RUN", "echo a && echo b"}}},
		{"escape directive", "# escape=`\nFROM base\nRUN echo a `\n    && echo b\nRUN dir C:\\\nWORKDIR /w\n",
			[]instruction{{"FROM", "base"}, {"RUN", "echo a && echo b"}, {"RUN", `dir C:\`}, {"WORKDIR", "/w"}}},
		{"escape directive after syntax, any case and spacing", "# syntax=docker/dockerfile:1\n#  ESCAPE = `  \nFROM base\nRUN a `  \n b\n",
			[]instruction{{"FROM", "base"}, {"RUN", "a b"}}},
		{"escape comment after an instruction is not a directive", "FROM base\n# escape=`\nRUN a `\nRUN b \\\n c\n",
			[]instruction{{"FROM", "base"}, {"RUN", "a `"}, {"RUN", "b c"}}},
		{"escape after an unknown directive is not honoured", "# foo=bar\n# escape=`\nFROM base\nRUN a `\nRUN b \\\n c\n",
			[]instruction{{"FROM", "base"}, {"RUN", "a `"}, {"RUN", "b c"}}},
		{"escape after a check directive is honoured", "# check=skip=all\n# escape=`\nFROM base\nRUN a `\n b\n",
			[]instruction{{"FROM", "base"}, {"RUN", "a b"}}},
		{"escape comment after a plain comment is not a directive", "# hello\n# escape=`\nFROM base\nRUN b \\\n c\n",
			[]instruction{{"FROM", "base"}, {"RUN", "b c"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDockerfile([]byte(tc.df))
			// Only the instruction boundaries are pinned, not the exact run
			// of spaces a join leaves behind.
			for i := range got {
				got[i].args = strings.Join(strings.Fields(got[i].args), " ")
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("parseDockerfile = %q, want %q", got, tc.want)
			}
		})
	}
}
