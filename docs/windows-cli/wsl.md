# The CLI on Windows today: the WSL route

**Status: the route that works today.** No Windows archive is published and
`fugaro.exe` does not build yet; what a Windows reader can run now is a
**Linux `fugaro` inside WSL 2**, which is the Linux path exactly (**F39**).
The native port is planned and not written — [implementation-plan.md](implementation-plan.md)
Slice 1 is the work that makes a Windows binary build at all, and Slice 0, this
document, is the part of the plan the owner approved first (**D16**,
2026-10-20).

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-20. **Read at:**
`main` at `54cb02a8` — the commit [reconciliation.md](reconciliation.md) reads,
whose **F39** and **F40** are this page's two sources. Nothing here was
verified by running Fugaro on Windows: every run of this repository's own
container is a Linux one, which is exactly why the route below is a Linux
binary rather than a Windows one.

## Why WSL is the route that works today

Nothing in the CLI detects WSL and nothing needs to (**F39**). A `fugaro` built
for linux/amd64 inside WSL **is** the Linux path exactly — the same binary, the
same code and the same test suite this repository already runs on Linux — so
nothing about it is a Windows claim anybody has to take on trust. That is the
whole of the argument: the Windows port is real work (nine packages of build
fixes, four platform lookups and a release matrix, per the plan) and WSL is
documentation.

What WSL is *not* is a substitute for the port, for two reasons the plan states
as **D1**'s own cost: the **desktop console** cannot reach a WSL-only
installation, for the same reason it cannot reach a Linux-only one — the
console's data source is a CLI it has to be able to run itself (**F40**, and
[desktop-console.md](../design/desktop-console.md) **D10** owns that decision) —
and the credential boundary below is a real source of confusion.

## Install it, inside WSL

1. Install a distribution (WSL 2; Ubuntu is the usual one) and open **its**
   shell. Everything below runs inside the distribution, not in PowerShell and
   not in `cmd.exe`.
2. Install the **linux** build of `fugaro` inside the distribution. There is no
   Windows build to install: the release archives are darwin and linux
   ([release.md](../release.md#verifying-a-release)), so take the `linux` one
   and verify it the way that page says:

   ```sh
   V=X.Y.Z
   gh release download v$V -R dimipaun/fugaro -p checksums.txt -p checksums.txt.sigstore.json -p "fugaro_${V}_linux_amd64.tar.gz"
   cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
     --certificate-identity "https://github.com/dimipaun/fugaro/.github/workflows/release.yml@refs/tags/v$V" \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com
   grep " fugaro_${V}_linux_amd64.tar.gz\$" checksums.txt | sha256sum -c -
   tar -xzf fugaro_${V}_linux_amd64.tar.gz
   ```

   The `grep` names the one archive the line above downloaded, and the `\$` is
   what makes it that one: `checksums.txt` carries a line for every published
   archive — darwin and linux, amd64 and arm64 — and for each archive's SBOM, so
   a pattern matching them all leaves `sha256sum -c` reading seven files this
   block never fetched, and it exits 1 with `FAILED open or read` for each of
   them. [release.md](../release.md#verifying-a-release) names this OS's entries
   for the same reason; it downloads this OS's archives rather than one.

   Put the extracted `fugaro` on the distribution's `PATH` (`~/.local/bin` is
   the usual place) and check it with `fugaro version`. A `go install
   github.com/dimipaun/fugaro/cmd/fugaro@latest` build prints `dev` and copies
   no release images and wires no plugin pin, exactly as on Linux: use it for
   development, not for a first setup.
3. `/fugaro:setup` and the rest of [gcp-setup.md](../gcp-setup.md) then apply
   unchanged — it is the Linux runbook, because this is Linux.

## The three things that bite

### 1. Where the checkout lives: not under `/mnt/c`

A checkout under `/mnt/c` is on the **Windows** filesystem, where file modes
are not real, `os.Chmod` does nothing and every git operation is far slower
than on the distribution's own ext4 side (**F39**). Keep the checkout on the
Linux side — `~/code/<repo>`, not `/mnt/c/Users/<you>/code/<repo>` — and run
`fugaro` there. This is the trap that produces no error message at all: the
commands work, and the files they read are on the slow side of a filesystem
with weaker guarantees than the code assumes.

### 2. Credentials: the ADC inside WSL is a *different file*

`gcloud auth application-default login` run **inside WSL** writes
`~/.config/gcloud/application_default_credentials.json` *inside the
distribution*. Run in Windows it writes
`%APPDATA%\gcloud\application_default_credentials.json`. They are two different
files (**F39**), so **signing in on Windows does not sign you in inside WSL**
and signing in inside WSL does not sign you in on Windows. Run the login — and
`gcloud auth application-default set-quota-project` — in the same environment
that runs `fugaro`:
[gcp-setup.md](../gcp-setup.md)'s Windows and WSL section states both forms of
the two commands that page has you run.

### 3. Claude Code has to be inside WSL too

The Fugaro plugin's skills call `fugaro`. Claude Code installed on Windows runs
outside the distribution and its skills find no `fugaro` to call, so install
Claude Code **inside WSL**, in the same distribution as the binary (**F39**),
and open the checkout there.

## What native Windows does not do yet

Said plainly, because "Windows works through WSL" is not "the CLI works on
Windows". None of these is fixed by installing WSL, and each is a task in
[implementation-plan.md](implementation-plan.md) rather than a note here:

| What | State on native Windows |
|---|---|
| The binary | No Windows archive is published (**F31**), and the tree did not cross-compile until the plan's Slice 1: nine packages of build fixes away from `fugaro.exe --version`. |
| `fugaro verify` | Wants a POSIX `sh` on `PATH` (**F3**); the platform's own shell is the plan's **D7**, still open. |
| The commands the CLI prints | The `Fix` lines `doctor` and `init` print are POSIX, "unset K" included (**F6**); the plan's T2.2 is where they gain a platform. |
| A Windows checkout of this repository | The goldens do not compare under Git for Windows' default CRLF handling; `.gitattributes` is the plan's T3.1 (**F20**). |
| `fugaro doctor` | Does not check `git` or the `gcloud` binary at all (**F26**). |
| `fugaro secrets set` | The hidden prompt is expected to work through `golang.org/x/term` but is unverified on Windows (**U3**). |
| CI | No Windows job exists in `.github/workflows/` (**F38**). |
| The desktop console | Out of scope: it needs a Windows CLI and an Authenticode-grade decision first (**F40**). |

The per-command promise the plan ends at — which command groups are supported,
best effort or out of scope on Windows — is [implementation-plan.md §1](implementation-plan.md#1-support-tiers-per-command-group).

## Related

- [implementation-plan.md](implementation-plan.md) — the plan behind the table
  above and the owner's approval of Slice 0 and Slice 1 (**D16**).
- [reconciliation.md](reconciliation.md) — **F39** (WSL) and **F40** (how the
  console follows), the two findings this page is written from.
- [gcp-setup.md](../gcp-setup.md) — the runbook the WSL route runs unchanged,
  with the Windows and WSL forms of the two commands it has you run.
- [release.md](../release.md#verifying-a-release) — the archive and the cosign
  and checksum verification this page's install step reuses.
