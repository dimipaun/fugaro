#!/usr/bin/env bash
# Tests for scripts/live-checks/check311.ps1, the Windows console check.
#
# PowerShell is NOT installed on this machine, so most of this file is a reading
# of the script's own TEXT: its block keys, its phases, its guards, the readings
# it takes and the ones it refuses to take, its exit codes, and the sentences it
# promises. That is a weaker reading than running it, and it is the honest one
# here -- a check whose whole subject is a Windows console cannot be driven
# without a Windows console, and pretending otherwise with a stub would be a test
# of the stub.
#
# The script carries the half of itself that can run anywhere: -SelfTest parses
# the file and checks that its block keys, its phases and its code agree, and it
# runs no `fugaro` and reads no file of yours. -DryRun, and the three refusals
# that happen before anything runs (a project that names the owner's
# installation, a coding agent's session, and a phase that is not one), are the
# same kind of thing. Where pwsh is on PATH those five, and the stop a machine
# that is not Windows makes before any of them, are RUN here, and where it is
# not, every case says so in one line rather than passing quietly: a green run
# on a machine without PowerShell must not read as "the script works".
#
# The one rule that is checked twice is the self-test's own: its first rule
# compares the keys the code sets with the block's static list and with the one
# key a branch owns, and the defect it had was that 'dry-run' -- set by the
# dry-run branch, which replaces that list before it prints -- was in neither.
# test_the_self_tests_own_rules_hold_here re-implements those rules over the
# script's text in bash, so the reading is taken here and not only on a machine
# with pwsh: it fails on the tree that carried the defect.
#
# Nothing here reaches the network, there is no cloud project and no credential,
# and no `fugaro` command is run: the cases that could run one are refused before
# they get there, which is itself part of what they test.
#
# Usage: bash scripts/live-checks/check311_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check311.ps1
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK311_TEST_KEEP:-}" ]; then
    echo "kept: $work" >&2
  else
    rm -rf "$work"
  fi
}
trap cleanup_all EXIT

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}
expect_contains() { # haystack needle what
  case "$1" in
  *"$2"*) ;;
  *) fail "$3: expected to contain '$2'" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1"
}
expect_pass_row() { # key what
  expect_contains "$out" "FACT $1: PASS" "$2"
}
expect_fail_row() { # key what
  expect_contains "$out" "FACT $1: FAIL" "$2"
}
expect_info_row() { # key what
  expect_contains "$out" "FACT $1: INFO" "$2"
}
# expect_block_keys: every key the block is defined to carry is in it, whatever
# the run measured. A block that lost a row cannot be diffed between two runs,
# which is the whole point of running this check twice.
# expect_block_keys: the rows a block carries, named by the case and not read
# from the run's own key list: a dry run replaces that list with its own five
# (check311.ps1 sets $script:BlockKeys in the dry-run branch), so a case that
# asserted BLOCK_KEYS against a dry run would be asserting rows the run never
# prints -- and would contradict the same case's assertion that the console row
# is absent.
expect_block_keys() { # what key...
  local what=$1 key
  shift
  for key in "$@"; do
    expect_contains "$out" "FACT $key: " "$what: the block carries the $key row"
  done
  expect_contains "$out" "CHECK 311 RESULT END" "$what: the block ends with its own line"
}
# line_of: the first line of the script carrying a needle, so a case can say one
# thing happens BEFORE another rather than only that both are in the file.
line_of() {
  grep -n -F -- "$1" "$script" | head -1 | cut -d: -f1
}
# body_of: a function's own lines, from its `function NAME {` -- with or without
# a parameter list -- to the first `}` that starts a line, which is the house
# style and the only brace that is one.
body_of() {
  sed -n "/^function $1[ (]/,/^}/p" "$script"
}

# The project every case names: a Fugaro project name, and never the owner's.
PROJECT=sandbox-demo
# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(project belong agent console vt-mode state-dir log-dir console-view
  tty-summary no-color piped json fail-step fail-context fail-log-path
  fail-log-protection fail-log-context log-name log-protection
  log-dir-protection teardown output)
# The five rows a dry run prints instead: the branch replaces the run's key list
# with its own, because a dry run reads no console and runs no command.
DRY_RUN_KEYS=(project belong agent state-dir dry-run)

# The text of the script, read once: most of the cases below are readings of it.
ps=$(cat "$script")
# The same text with every run of whitespace flattened to one space, for the
# sentences of the header, which wraps at 80 columns: a case that pins a sentence
# pins what it says, not where the line breaks fell.
ps_flat=$(printf '%s' "$ps" | tr '\n' ' ' | tr -s ' ')
# The functions whose bodies have to carry (or must never carry) a thing.
bare_body=$(body_of Invoke-Bare)
capture_body=$(body_of Invoke-Capture)
stub_body=$(body_of Write-Stub)
self_body=$(body_of Invoke-SelfTest)
block_body=$(body_of Write-Block)
acl_body=$(body_of Get-AclFinding)
ask_body=$(body_of Ask-Owner)

# ------------------------------------------------------------------ the file
test_the_file_is_utf8_with_a_bom_and_ascii_inside() {
  local bom rest
  bom=$(head -c 3 "$script" | od -An -tx1 | tr -d ' \n')
  [ "$bom" = efbbbf ] || fail "the file must start with a UTF-8 BOM (Windows PowerShell 5.1 reads a script without one as ANSI, which would mangle a check about what a console draws); its first three bytes are $bom"
  # Every byte after the BOM has to be ASCII: the marks and the dash this script
  # prints are built from their code points, so the file itself never depends on
  # a host guessing its encoding.
  rest=$(tail -c +4 "$script" | od -An -v -tx1 | tr -s ' ' '\n' | grep -E '^[89a-f][0-9a-f]$' || true)
  [ -z "$rest" ] || fail "the file must be ASCII after its BOM, and these bytes are not: $(printf '%s' "$rest" | tr '\n' ' ')"
}

test_the_header_says_what_it_is_and_what_it_needs() {
  expect_contains "$ps_flat" "check311.ps1 -- Check 311" "the header names the check"
  expect_contains "$ps_flat" "Windows Terminal and in the legacy console" "and the two consoles it is run in"
  expect_contains "$ps_flat" "Usage: pwsh -NoProfile -File scripts/live-checks/check311.ps1 -Project" "the usage line names the interpreter and the one required flag"
  expect_contains "$ps_flat" "Run it TWICE" "the header says the two runs are the point"
  expect_contains "$ps_flat" "Needs: PowerShell 5.1" "what it needs"
  expect_contains "$ps_flat" "Runs: by YOU. No agent runs any step of it." "who runs it"
  expect_contains "$ps_flat" "Exit 0 when every fact held, 1 when one did not" "the exit codes"
  expect_contains "$ps_flat" "2 when the run could not tell" "including the one that means it could not tell"
  expect_contains "$ps_flat" "calls no API" "that it calls nothing"
  expect_contains "$ps_flat" "never \`belong\`" "the installation it never reads"
}

test_the_block_shape_is_the_siblings_shape() {
  expect_contains "$ps" "'CHECK 311 RESULT'" "the block's own heading"
  expect_contains "$ps" "'CHECK 311 RESULT END (exit '" "and its own closing line, with the exit code in it"
  expect_contains "$ps" "'FACT ' + \$key + ': ' + \$fact.Status + ' '" "the row shape every sibling check prints"
  expect_contains "$ps" "INFO ' + [char]0x2014 + ' not reached (the run stopped before this step)" "a row that was never reached says so rather than reading as a pass"
}

test_the_marks_and_the_dash_are_built_from_their_code_points() {
  local code
  for code in 0x2014 0x2714 0x2716 0x25CB 0x2013 0x21B3; do
    expect_contains "$ps" "[char]$code" "the mark $code is built from its code point, not written into the file"
  done
  expect_contains "$ps" "'^' + [char]0x2716 + ' '" "the failed step is recognised by the reporter's own mark"
}

test_the_block_carries_every_pinned_key() {
  local key
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$ps" "'$key'" "the block's key list carries $key"
  done
  expect_contains "$ps" "\$script:BlockKeys = @(" "the list is the script's own"
}

test_the_block_is_printed_once_and_the_encoding_is_put_back() {
  expect_contains "$block_body" 'if ($script:BlockPrinted) { return }' "a second block can never be printed"
  expect_contains "$block_body" "\$script:BlockPrinted = \$true" "the first one marks itself"
  expect_contains "$block_body" "[Console]::OutputEncoding = [Text.Encoding]::UTF8" "the block is printed as UTF-8, because its dash is not in every code page"
  expect_contains "$block_body" "finally" "and the console's own encoding is put back"
  expect_contains "$block_body" "[Console]::OutputEncoding = \$old" "to what it was before the block"
}

test_the_phases_are_the_ones_the_plan_names() {
  expect_contains "$ps" "\$script:Phases = @('all', 'look', 'failure', 'logs')" "the phases the usage names"
  expect_contains "$ps" "'-Phase takes all, look, failure or logs" "and the refusal says what it takes"
  expect_contains "$ps" "function Invoke-LookPhase {" "the look phase"
  expect_contains "$ps" "function Invoke-FailurePhase {" "the failure phase"
  expect_contains "$ps" "function Invoke-LogsPhase {" "the logs phase"
  expect_contains "$self_body" "'Invoke-' + \$phase.Substring(0, 1).ToUpper() + \$phase.Substring(1) + 'Phase'" "the self-test checks every phase has its function"
}

test_a_row_of_an_unselected_phase_is_not_measured() {
  expect_contains "$ps" "belongs to another phase (-Phase all runs every one of them)" "a row of another phase says so"
  expect_contains "$ps" "'not measured: -Phase ' + \$script:Selected + ' was selected" "and names the phase that was selected"
}

# ------------------------------------------------------------------ the guards
test_the_guards_are_all_there_and_run_before_anything_else() {
  expect_contains "$ps" "if (\$Project.ToLower().Contains('belong'))" "the installation the owner never lets a check read"
  expect_contains "$ps" "the project names the owner''s own installation, and this check never reads it" "the refusal names it"
  expect_contains "$ps" "\$Project -notmatch '^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?\$'" "the project-name pattern the config itself enforces"
  expect_contains "$ps" "'a project is required: type -Project <fugaro project>'" "a project is required, because the CLI would otherwise fall back to the one config on this machine"
  expect_contains "$ps" "if ([Console]::IsOutputRedirected) {" "a redirected stdout is refused, because the console is the subject"
  expect_contains "$ps" "if ([Console]::IsInputRedirected) {" "and a question is never asked at an end of file"
  # The agent's session is REFUSED, as Check 310 refuses it: a converge in an
  # agent's session is not the run this check is about.
  local markers=(CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_REMOTE CURSOR_AGENT AI_AGENT)
  local marker
  for marker in "${markers[@]}"; do
    expect_contains "$ps" "'$marker'" "an agent session is refused by $marker"
  done
  expect_contains "$ps" "Stop-Run ('this is a coding agent''s session ('" "an agent's session stops the run rather than being noted"
  expect_contains "$ps" "Set-Fact 'agent' 'FAIL'" "and the refusal is a row of the block"
  expect_contains "$ps" "'CLAUDE_CODE_SSE_PORT'" "the IDE marker is read on its own"
  expect_contains "$ps" "if ([Environment]::GetEnvironmentVariable('CLAUDE_CODE_SSE_PORT') -and [Console]::IsInputRedirected) {" "and refused only where no person is at the console"
  expect_contains "$ps" "Set-Fact 'agent' 'PASS' 'this is not a coding agent''s session" "and a person's session is recorded as a pass"
  local belong_line guard_line stop_line
  belong_line=$(line_of "if (\$Project.ToLower().Contains('belong'))")
  stop_line=$(line_of "Stop-Run 'Check 311 is the WINDOWS console check")
  guard_line=$(line_of "Require-Tool \$script:Fugaro 'every reading is a command")
  [ -n "$belong_line" ] && [ -n "$stop_line" ] && [ -n "$guard_line" ] ||
    fail "the guards must be findable by line"
  [ "$belong_line" -lt "$stop_line" ] ||
    fail "the installation guard must be judged before the platform one (line $belong_line vs $stop_line): it holds on every machine"
  [ "$stop_line" -lt "$guard_line" ] ||
    fail "the tools must be required only after the platform is known (line $stop_line vs $guard_line)"
}

test_the_dry_run_branch_comes_after_the_agent_guard_and_before_the_platform_one() {
  local agent dry stop tools plan
  agent=$(line_of "if ([Environment]::GetEnvironmentVariable('CLAUDE_CODE_SSE_PORT') -and [Console]::IsInputRedirected) {")
  dry=$(line_of "if (\$DryRun) {")
  stop=$(line_of "Stop-Run 'Check 311 is the WINDOWS console check")
  tools=$(line_of "Require-Tool \$script:Fugaro 'every reading is a command")
  plan=$(body_of Write-Plan)
  [ -n "$agent" ] && [ -n "$dry" ] && [ -n "$stop" ] && [ -n "$tools" ] || fail "the dry-run branch must be findable"
  [ "$agent" -lt "$dry" ] ||
    fail "the agent's session must be refused before the dry-run branch (line $agent vs $dry): a dry run of an agent's session is still a plan an agent would then run"
  [ "$dry" -lt "$stop" ] || fail "a dry run must be able to run off Windows (line $dry vs $stop)"
  [ "$dry" -lt "$tools" ] || fail "a dry run must not need any tool (line $dry vs $tools)"
  expect_contains "$ps" "\$script:BlockKeys = @('project', 'belong', 'agent', 'state-dir', 'dry-run')" "a dry run prints the five rows it really measured"
  expect_contains "$ps" "'the guards and the plan only: no command was run, no file was read, nothing was applied'" "and says so in the block"
  expect_contains "$plan" "NOTHING below was done" "the plan says nothing was done"
  expect_contains "$plan" "print ONE block on stdout, headed CHECK 311 RESULT, and end 0, 1 or 2" "and ends by naming the block"
}

test_the_non_windows_stop_names_the_sibling_check() {
  expect_contains "$ps" "this is not Windows: Check 310 (scripts/live-checks/check310.sh) is the one for macOS and Linux" "the stop sends the owner to the check for their machine"
  expect_contains "$ps" "the only modes this script has off Windows" "and names the two modes that do work there"
}

test_no_call_writes_a_concatenation_outside_parentheses() {
  # In PowerShell `Name 'lit' + $x` is a command invocation with three
  # positional arguments: the quoted string, the operator and the variable. A
  # function that declares parameters binds the first argument to its first
  # parameter and drops the rest into $args, so the call does not fail -- it
  # quietly loses half its message, cut at the quoted string. `Say 'STOPPING: '
  # + $why` wrote 'STOPPING: ' and nothing else, so every Stop-Run caller lost
  # the reason for the stop, and `Set-Fact $key 'PASS' 'lit: ' + $question` cut
  # the row's detail at a dangling colon. Both shipped, and both hid behind a
  # machine without pwsh, where every runtime case of this file returns early.
  #
  # The reading is taken HERE, where no interpreter is needed, and it is the
  # general rule rather than the two names it started as: every call to a
  # function this script defines -- those are the ones that declare parameters
  # and so drop the extra arguments into $args -- must parenthesise its
  # concatenation. A quoted string followed by a bare `+` at paren depth 0
  # inside such a call is the defect, whatever it concatenates.
  local loose
  loose=$(awk -v sq="'" -v dq='"' '
    NR == FNR {
      if ($0 ~ /^function /) { n = $2; sub(/[^A-Za-z0-9_-].*$/, "", n); fn[n] = 1 }
      next
    }
    {
      line = $0
      called = 0
      for (n in fn) { if (line ~ ("(^|[^A-Za-z0-9_-])" n "([ (]|$)")) { called = 1; break } }
      if (!called) next
      depth = 0; q = ""; prev = ""; bad = 0
      for (i = 1; i <= length(line); i++) {
        c = substr(line, i, 1)
        if (q != "") {
          if (q == sq) {
            if (c == sq) { if (substr(line, i + 1, 1) == sq) { i++ } else { q = "" } }
          } else if (c == "`") { i++ } else if (c == dq) { q = "" }
          prev = c
          continue
        }
        if (c == "#") break
        if (c == sq || c == dq) { q = c; prev = c; continue }
        if (c == "(") depth++
        else if (c == ")") depth--
        else if (c == "+" && depth == 0 && (prev == sq || prev == dq)) bad = 1
        if (c != " " && c != "\t") prev = c
      }
      if (bad) print FNR ": " line
    }
  ' "$script" "$script")
  [ -z "$loose" ] ||
    fail "a call concatenates outside parentheses, so the extra arguments land in \$args and the message is cut at the quoted string: $loose"
  expect_contains "$(body_of Stop-Run)" "Say ('STOPPING: ' + \$why)" "Stop-Run writes its sentence through a parenthesised concatenation"
}

# ------------------------------------------------------------- what it runs
test_the_watched_runs_are_never_captured() {
  expect_contains "$bare_body" "& \$script:Fugaro @argv" "the watched run is started as a command, not through a pipe"
  expect_absent "$bare_body" "Out-String" "nothing captures the watched run: a capture is a pipe, and a pipe has no colour and no line in flight"
  expect_absent "$bare_body" "return \$script:LastExit" "and nothing is returned: a call whose value is assigned is a call whose output is captured"
  expect_contains "$bare_body" "\$script:LastExit = \$LASTEXITCODE" "the exit code is left in the script's own variable instead"
  # The call sites, which are where the mistake would really be made.
  expect_contains "$ps" "
    Invoke-Bare @('update')" "the watched update is called as a statement"
  expect_absent "$ps" "= Invoke-Bare" "no call of it is assigned: that is the bug this shape exists to prevent"
  expect_contains "$ps" "\$piped = Invoke-Capture @('update')" "the piped run's text is taken, because a pipe is what that reading is about"
  expect_contains "$ps" "\$cap = Invoke-Capture @('update') -ExtraPath \$stubDir" "and so is the forced failure's"
}

test_the_pipe_and_the_document_are_read_from_a_capture() {
  expect_contains "$capture_body" "| Out-String" "the pipe reading captures the output, which is what makes it a pipe"
  expect_contains "$capture_body" "2>\$errFile" "and the human lines are kept apart from it"
  expect_contains "$capture_body" "[Console]::OutputEncoding = [Text.Encoding]::UTF8" "the capture is decoded as UTF-8, which is what the CLI writes"
  expect_contains "$capture_body" "[Console]::OutputEncoding = \$oldEncoding" "and the console's own encoding is put back, so a captured run cannot change the next watched one"
  expect_contains "$ps" "([regex]::Matches(\$piped, [string][char]27)).Count" "the pipe reading counts real escape bytes"
  expect_contains "$ps" "\$doc = \$json | ConvertFrom-Json" "the --json reading parses the document"
  expect_contains "$ps" "\$doc.project -ne \$Project" "and checks that the document is this run's own"
  expect_contains "$ps" "'stdout is ONE JSON document and it names this run''s own project" "the row says what it read"
}

test_the_forced_failure_is_a_terraform_that_answers_version() {
  expect_contains "$stub_body" "'if \"%~1\"==\"version\" ('" "the stub answers the one read every converge makes before it plans"
  expect_contains "$stub_body" 'echo {"terraform_version":"1.9.5"}' "with a version a parser accepts"
  expect_contains "$stub_body" "'terraform.cmd'" "it is a .cmd, which is what PATH resolves a name to on Windows"
  expect_contains "$stub_body" "'exit /b 1'" "and it refuses everything else"
  expect_contains "$stub_body" "'1>&2 echo ' + \$script:StubLineOne" "its refusal is the error the stage sees, on stderr"
  expect_contains "$ps" "\$cap = Invoke-Capture @('update') -ExtraPath \$stubDir" "the failure phase runs the converge with the stub's directory first on PATH"
  expect_contains "$capture_body" "\$ExtraPath + [IO.Path]::PathSeparator + \$oldPath" "and first is first"
  expect_contains "$ps" "'  (exit ' + \$script:LastExit + '; nothing was applied: the only terraform on that run''s PATH refuses every plan and apply)'" "the run says nothing was applied"
}

test_the_failure_readings_are_the_reporter_s_own_lines() {
  expect_contains "$ps" "'^    full output: (.+)\$'" "the log's path is read from the line that carries it"
  expect_contains "$ps" "'fugaro troubleshoot ([A-Za-z0-9._-]+)'" "and the id from the line that routes by it"
  expect_contains "$ps" "[IO.Path]::GetFileNameWithoutExtension(\$path)" "the id is compared with the log's own stem, so one word opens the run that failed"
  expect_contains "$ps" "'^    [^ ]'" "the context is the error's own lines, four spaces in"
  expect_contains "$ps" "'^    full output: '" "which is not the path line"
  expect_contains "$ps" "'^    Something went wrong\.'" "and not the diagnose line"
  expect_contains "$ps" "Get-Content -LiteralPath \$path -Raw" "the log itself is read whole"
  expect_contains "$ps" "\$logText -like ('*' + \$script:StubLineTwo + '*')" "and has to hold the context the screen carried"
  expect_contains "$ps" "the forced failure produced no failure block at all" "a run that refused before a stage stops the phase rather than reading FAILs off it"
}

# ------------------------------------------------------------------ the logs
test_the_log_rules_are_the_cli_s_own() {
  expect_contains "$ps" "Resolve-StateDir" "the state directory is resolved, not assumed"
  expect_contains "$ps" "if (\$env:LOCALAPPDATA -and [IO.Path]::IsPathRooted(\$env:LOCALAPPDATA)) { return \$env:LOCALAPPDATA }" "on Windows that is %LOCALAPPDATA%, the same rule localcfg.UserDir uses"
  expect_contains "$ps" "Join-Path (Join-Path \$script:StateBase 'fugaro') 'logs'" "and the logs are under fugaro\logs"
  expect_contains "$ps" "'^' + [regex]::Escape(\$command) + '-[0-9]{14}-[0-9]+\\.log\$'" "a log is matched by the name the retention and the troubleshoot id both read"
  expect_contains "$ps" "'^update-[0-9]{14}-[0-9]+\\.log\$'" "and the name reading uses the same pattern"
  expect_contains "$ps" "'^(Done|Failed|Left for you) in [0-9]+\\.[0-9]s, ([0-9]+ steps|1 step)\$'" "the closing line has summaryLine's own shape"
}

test_the_log_protection_is_read_as_an_acl() {
  expect_contains "$acl_body" "Get-Acl -LiteralPath \$path" "Windows has no 0600, so the protection is read as the file's ACL"
  expect_contains "$acl_body" "'NT AUTHORITY\\SYSTEM'" "SYSTEM is allowed"
  expect_contains "$acl_body" "'BUILTIN\\Administrators'" "the administrators are allowed"
  expect_contains "$acl_body" "[System.Security.Principal.WindowsIdentity]::GetCurrent().Name" "and the person it belongs to"
  expect_contains "$acl_body" "if (-not \$ok) { \$bad += \$id }" "anything else is a finding, named in the row"
  expect_contains "$ps" "the ACL inside your own profile is what stands for Check 310''s mode reading" "the row says why an ACL stands in for a mode"
}

test_the_retention_half_is_deliberately_not_repeated() {
  expect_contains "$ps" "This check deliberately does NOT" "the header says what is left out"
  expect_contains "$ps" "repeat Check 310's retention half" "and which half it is"
  expect_contains "$ps" "51 failing converges" "with the cost of the half it leaves out"
  expect_contains "$ps" "retention" "and the reason: retention is a property of the log directory, not of the console"
}

# ------------------------------------------------------------- the questions
test_only_two_things_are_the_owner_s_reading() {
  expect_contains "$ps" "Ask-Owner 'console-view'" "the view is asked"
  expect_contains "$ps" "Ask-Owner 'no-color'" "and NO_COLOR is asked"
  [ "$(grep -c "Ask-Owner '" "$script")" = 2 ] ||
    fail "exactly two rows are the owner's own reading (the view and NO_COLOR); the file has $(grep -c "Ask-Owner '" "$script") calls"
  expect_contains "$ask_body" "if ([Console]::IsInputRedirected) {" "a question is only asked where a console can answer it"
  expect_contains "$ask_body" "'not measured: this run has no console on its input" "and a redirected run records it as not measured rather than as an answer"
  expect_contains "$ask_body" "'QUESTION -- ' + \$question" "the question is printed on stderr, apart from the block"
  expect_contains "$ps" "run cmd /c ' + \$script:Fugaro + ' update in this same console" "the view question says how to tell a shell's own piping from the CLI"
}

test_the_console_is_named_and_its_own_answer_is_read() {
  expect_contains "$ps" "\$env:WT_SESSION" "Windows Terminal is recognised"
  expect_contains "$ps" "\$env:TERM_PROGRAM" "so is a terminal that names itself"
  expect_contains "$ps" "the legacy console (no WT_SESSION, no TERM_PROGRAM, no ConEmu)" "and the legacy console is named as what is left"
  expect_contains "$ps" "\$Host.UI.PSObject.Properties['SupportsVirtualTerminal']" "the console host's own answer about virtual terminal sequences is read defensively"
  expect_contains "$ps" "Get-CodePage" "and the code page is read"
  expect_contains "$ps" "(& chcp.com 2>\$null | Out-String)" "with chcp.com"
  expect_contains "$ps" "the console''s own code page is read with it" "which the check requires as a tool"
  expect_contains "$ps" "READ and reported, never set" "and never sets it: a check that set the code page would be reading its own setting"
  expect_absent "$ps" "SetConsoleOutputCP" "nothing sets the console's code page"
}

test_the_self_test_only_parses() {
  expect_contains "$self_body" "[System.Management.Automation.Language.Parser]::ParseFile(\$PSCommandPath, [ref]\$tokens, [ref]\$errors)" "it parses the file it is in"
  expect_contains "$self_body" "[regex]::Matches(\$text, \"Set-Fact\s+'([a-z-]+)'\")" "and reads the keys the code really sets"
  expect_contains "$self_body" "the block does not print it" "a key measured and never printed is a failure"
  expect_contains "$self_body" "neither the code nor a branch of it owns it" "and so is a row printed and never measured"
  expect_contains "$self_body" "(\$script:BlockKeys -notcontains \$key) -and (\$owned -notcontains \$key)" "rule 1 consults the owned set, which is what makes it the mirror of rule 2"
  expect_contains "$self_body" "\$owned = @('dry-run')" "and that set carries 'dry-run', the one key a branch owns: without it the self-test can never pass"
  expect_contains "$self_body" "Get-Command -Name \$fn -CommandType Function" "every phase has to have its function"
  expect_contains "$self_body" "'check311.ps1: self-test passed -- the file parses, '" "it says what it checked"
  expect_contains "$self_body" "and nothing was run" "and that it ran nothing"
  expect_absent "$self_body" "\$script:Fugaro" "the self-test runs no fugaro"
  expect_contains "$ps" "if (\$SelfTest) { Invoke-SelfTest }" "and it is the first thing main does"
}

# test_the_self_tests_own_rules_hold_here: the self-test's three rules, taken
# again over the script's text, in bash. The script runs them itself where pwsh
# is; this runs them on every machine, which is what makes the tree that carried
# the defect -- rule 1 comparing every key the code sets against the static list
# alone, with 'dry-run' in neither -- red here rather than only on Windows.
test_the_self_tests_own_rules_hold_here() {
  local block_keys literals owned asked phase_keys key k found phases phase fn first rest
  # The block's static list, out of its own @( ... ) literal.
  block_keys=$(sed -n '/^[$]script:BlockKeys = @(/,/^)/p' "$script" | grep -o "'[a-z-]*'" | tr -d "'")
  # Every key the code sets as a literal, anywhere in the file. A Set-Fact whose
  # key is a variable is not one of these, which is the point: the block's own
  # rows are what it must be able to print.
  literals=$(grep -o "Set-Fact '[a-z-]*'" "$script" | sed "s/Set-Fact '//; s/'//" | sort -u)
  # The keys the code sets through something else, which the self-test's $owned
  # carries: 'dry-run' (the branch that replaces the block's list before it
  # prints) and every phase key (the "not measured" loop's own Set-Fact).
  owned=$(sed -n 's/^ *[$]owned = @(\(.*\))$/\1/p' "$script" | tr -d " '@")
  phase_keys=$(sed -n '/^[$]script:PhaseKeys = @{/,/^}/p' "$script" |
    grep -o "= @(.*)" | grep -o "'[a-z-]*'" | tr -d "'")
  # The two rows Ask-Owner sets, which are therefore in no literal Set-Fact and
  # have to be owned by the phase that asks them.
  asked=$(grep -o "Ask-Owner '[a-z-]*'" "$script" | sed "s/Ask-Owner '//; s/'//" | sort -u)
  [ -n "$block_keys" ] || fail "the block's static key list cannot be read out of the script"
  [ -n "$literals" ] || fail "the keys the code sets cannot be read out of the script"
  [ -n "$owned" ] || fail "the owned set cannot be read out of the script"
  [ -n "$phase_keys" ] || fail "the phases' key lists cannot be read out of the script"
  [ -n "$asked" ] || fail "the keys Ask-Owner is called with cannot be read out of the script"
  # The regression this whole reading exists for: 'dry-run' is set by the
  # dry-run branch, which replaces the block's list with its own five before it
  # prints, so it is in neither the static list nor the phase keys -- and a rule
  # 1 that does not consult $owned can never pass.
  found=no
  for k in $owned; do if [ "$k" = "dry-run" ]; then found=yes; fi; done
  [ "$found" = yes ] || fail "the owned set does not carry 'dry-run', which the dry-run branch sets and the block's static list does not: rule 1 would fail on every run"
  found=no
  for k in $literals; do if [ "$k" = "dry-run" ]; then found=yes; fi; done
  [ "$found" = yes ] || fail "the dry-run branch no longer sets 'dry-run' as a literal, so the owned set is carrying a key nothing sets"
  # The rows only the owner's own eyes take: Ask-Owner is handed the key, so the
  # code sets them through no literal, and the phase that asks them has to own
  # them or rule 2 fails on a row that IS measured.
  for key in $asked; do
    found=no
    for k in $block_keys; do if [ "$k" = "$key" ]; then found=yes; fi; done
    [ "$found" = yes ] || fail "Ask-Owner is called with '$key' and the block prints no such row"
    found=no
    for k in $phase_keys; do if [ "$k" = "$key" ]; then found=yes; fi; done
    [ "$found" = yes ] || fail "Ask-Owner is called with '$key', which no phase's key list owns: rule 2 would fail, because the code sets it through no literal Set-Fact"
  done
  # Rule 1: every key the code sets is a key the block prints or one $owned carries.
  for key in $literals; do
    found=no
    for k in $block_keys $owned $phase_keys; do if [ "$k" = "$key" ]; then found=yes; fi; done
    if [ "$found" != yes ]; then
      fail "the self-test's rule 1 would fail: the code sets '$key' and neither the block's key list nor \$owned carries it, so a reading nobody sees is a reading nobody can act on"
    fi
  done
  # Rule 2: every key the block prints is one the code sets or $owned carries.
  for key in $block_keys; do
    found=no
    for k in $literals $owned $phase_keys; do if [ "$k" = "$key" ]; then found=yes; fi; done
    if [ "$found" != yes ]; then
      fail "the self-test's rule 2 would fail: the block prints '$key' and the code never sets it, so a row that is never measured reads as a pass"
    fi
  done
  # The two lists this test reads and the list the cases above pin are the same
  # list: a key added to one and not the other is a block the two disagree about.
  for key in "${BLOCK_KEYS[@]}"; do
    found=no
    for k in $block_keys; do if [ "$k" = "$key" ]; then found=yes; fi; done
    [ "$found" = yes ] || fail "the script's block key list does not carry '$key', which this test's own BLOCK_KEYS does"
  done
  for key in $block_keys; do
    found=no
    for k in "${BLOCK_KEYS[@]}"; do if [ "$k" = "$key" ]; then found=yes; fi; done
    [ "$found" = yes ] || fail "the script's block key list carries '$key' and this test's own BLOCK_KEYS does not"
  done
  # Rule 3: every phase but 'all' has its own function.
  phases=$(sed -n 's/^[$]script:Phases = @(\(.*\))$/\1/p' "$script" | tr ',' '\n' | tr -d " '")
  found=no
  for phase in $phases; do if [ "$phase" = "all" ]; then found=yes; fi; done
  [ "$found" = yes ] || fail "the phase list cannot be read out of the script"
  for phase in $phases; do
    [ "$phase" = "all" ] && continue
    first=$(printf '%s' "$phase" | cut -c1 | tr '[:lower:]' '[:upper:]')
    rest=$(printf '%s' "$phase" | cut -c2-)
    fn="Invoke-${first}${rest}Phase"
    [ "$(grep -c "^function $fn {" "$script")" = 1 ] ||
      fail "the self-test's rule 3 would fail: -Phase $phase has no $fn"
  done
}

test_nothing_is_written_outside_the_scratch_directory() {
  [ "$(grep -c 'Set-Content' "$script")" = 1 ] ||
    fail "the only file this script writes is its own terraform.cmd stub; Set-Content appears $(grep -c 'Set-Content' "$script") times"
  expect_contains "$stub_body" "Join-Path \$script:Scratch 'stub'" "the stub is written inside the scratch directory"
  expect_contains "$ps" "Join-Path ([IO.Path]::GetTempPath()) ('chk311.' + [Guid]::NewGuid().ToString('N').Substring(0, 8))" "the scratch directory is a fresh one of its own"
  expect_contains "$ps" "Remove-Item -LiteralPath \$script:Scratch -Recurse -Force" "and it is removed at the end"
  expect_contains "$ps" "'the scratch directory is gone; no log of yours was deleted and no setting of your console was changed'" "the teardown row says what was not touched"
  [ "$(grep -c 'Remove-Item' "$script")" = 1 ] ||
    fail "the only thing this script ever removes is its own scratch directory; Remove-Item appears $(grep -c 'Remove-Item' "$script") times"
}

test_the_verdict_names_the_readings_that_failed() {
  expect_contains "$ps" "\$failed += \$key" "the failing rows are collected"
  expect_contains "$ps" "if (\$failed.Count -eq 0) {" "and the verdict turns on them"
  expect_contains "$ps" "'the default output holds in this console ('" "a pass names the console it held in"
  expect_contains "$ps" "'the default output does not hold in this console ('" "and a failure names it too"
  expect_contains "$ps" "Run the same block in the other console and compare the two" "the block asks for the second run"
}

test_the_last_net_prints_a_block_and_ends_two() {
  expect_contains "$ps" "if (-not \$script:BlockPrinted) {" "an unexpected error still prints a block"
  expect_contains "$ps" "'an internal error ended the run before it reached a verdict: '" "and says it was the check that broke"
  expect_contains "$ps" "function Stop-Run([string]\$why) {" "a stop prints the block before it ends"
  expect_contains "$ps" "Write-Block 2" "with the code that means it could not tell"
}

# --------------------------------------------------------- where pwsh exists
# The six cases below run the script itself. They are the ones that cannot run
# a converge: the self-test parses and runs nothing, the dry run runs nothing,
# the three refusals happen before the first tool is looked for, and the
# non-Windows stop comes before all of them on a machine that is not Windows. On
# a machine without pwsh each of them says so in one line rather than passing
# quietly.
have_pwsh() { command -v pwsh >/dev/null 2>&1; }

run311() { # args...
  status=0
  out=""
  err=""
  # The agent markers are cleared with CHECK311_FUGARO and NO_COLOR: the guard
  # refuses an agent's session, so the developer's own session must not reach a
  # case that is about something else -- and the cases that ARE about the guard
  # put a marker back themselves.
  out=$(env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_REMOTE \
    -u CURSOR_AGENT -u AI_AGENT -u CLAUDE_CODE_SSE_PORT -u NO_COLOR \
    -u CHECK311_FUGARO \
    HOME="$work/home" USERPROFILE="$work/home" LOCALAPPDATA="$work/state" \
    pwsh -NoProfile -File "$script" "$@" 2>"$work/err" </dev/null) || status=$?
  err=$(cat "$work/err" 2>/dev/null || true)
}

skip_no_pwsh() {
  echo "note: pwsh is not installed, so '$1' cannot be run here; its half of the" >&2
  echo "      check is read as text above, and the script's own -SelfTest is the" >&2
  echo "      mode that says whether that half is consistent." >&2
}

test_the_self_test_passes_where_pwsh_is() {
  if ! have_pwsh; then skip_no_pwsh "test_the_self_test_passes_where_pwsh_is"; return 0; fi
  run311 -SelfTest
  expect_status "$status" 0 "the self-test"
  expect_contains "$out" "self-test passed" "it says what it checked"
  expect_contains "$out" "block keys and 3 phases agree with the code" "and how much"
  expect_absent "$out" "FACT " "the self-test prints no block: it is not a run"
}

test_a_dry_run_runs_nothing_where_pwsh_is() {
  if ! have_pwsh; then skip_no_pwsh "test_a_dry_run_runs_nothing_where_pwsh_is"; return 0; fi
  run311 -DryRun -Project "$PROJECT"
  expect_status "$status" 0 "a dry run"
  expect_pass_row project "the project that was typed"
  expect_pass_row belong "and the guard on it"
  expect_pass_row agent "and the guard on the session it was run in"
  expect_pass_row state-dir "the state directory it would read"
  expect_pass_row dry-run "and that it ran nothing"
  expect_block_keys "a dry run" "${DRY_RUN_KEYS[@]}"
  expect_contains "$err" "dry run: NOTHING below was done" "the plan is printed"
  expect_contains "$err" "[failure] write a terraform.cmd" "and names the forced failure"
  expect_absent "$out" "FACT console: " "a dry run measures no console: it reads no file and runs no command"
}

test_the_agent_guard_fires_where_pwsh_is() {
  if ! have_pwsh; then skip_no_pwsh "test_the_agent_guard_fires_where_pwsh_is"; return 0; fi
  status=0
  out=$(env CLAUDECODE=1 HOME="$work/home" USERPROFILE="$work/home" LOCALAPPDATA="$work/state" \
    pwsh -NoProfile -File "$script" -Project "$PROJECT" 2>"$work/err" </dev/null) || status=$?
  err=$(cat "$work/err" 2>/dev/null || true)
  expect_status "$status" 2 "a coding agent's session"
  expect_fail_row agent "the guard names the marker it refused"
  expect_contains "$err" "Check 311 is OWNER-RUN" "and says who runs it, on stderr where the stop is written"
  # The refusal is judged before the dry-run branch: a dry run of an agent's
  # session is still a plan an agent would then run.
  status=0
  out=$(env CLAUDECODE=1 HOME="$work/home" USERPROFILE="$work/home" LOCALAPPDATA="$work/state" \
    pwsh -NoProfile -File "$script" -DryRun -Project "$PROJECT" 2>/dev/null </dev/null) || status=$?
  expect_status "$status" 2 "a dry run in a coding agent's session"
  expect_fail_row agent "the same guard, judged before the dry-run branch"
  expect_absent "$out" "FACT dry-run: " "so the plan was never printed"
}

test_the_belong_guard_fires_where_pwsh_is() {
  if ! have_pwsh; then skip_no_pwsh "test_the_belong_guard_fires_where_pwsh_is"; return 0; fi
  run311 -Project belong
  expect_status "$status" 2 "the owner's own installation"
  expect_fail_row belong "the guard names what it refused"
  expect_contains "$err" "this check never reads it" "and why, on stderr where the stop is written: the row says 'never touches', the stop says 'never reads it'"
}

test_a_phase_that_is_not_one_is_refused_where_pwsh_is() {
  if ! have_pwsh; then skip_no_pwsh "test_a_phase_that_is_not_one_is_refused_where_pwsh_is"; return 0; fi
  run311 -Phase later -Project "$PROJECT"
  expect_status "$status" 2 "a phase that is not one"
  expect_contains "$err" "-Phase takes all, look, failure or logs" "the refusal says what it takes, on stderr where the stop is written"
  expect_contains "$out" "FACT project: INFO" "and the block is printed with what was measured, which is nothing"
}

test_off_windows_the_check_stops_and_names_check310_where_pwsh_is() {
  if ! have_pwsh; then skip_no_pwsh "test_off_windows_the_check_stops_and_names_check310_where_pwsh_is"; return 0; fi
  case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*)
    echo "note: this is a Windows shell, so the non-Windows stop is not this machine's case" >&2
    return 0
    ;;
  esac
  run311 -Project "$PROJECT"
  expect_status "$status" 2 "the wrong platform"
  expect_contains "$err" "Check 311 is the WINDOWS console check and this is not Windows" "the stop says which check this machine needs"
  expect_contains "$err" "check310.sh" "and names it"
  expect_pass_row belong "the guard that holds everywhere was still judged"
}

# Sharding, so the package's Go test can run the tests of this file side by
# side: CHECK_TEST_SHARD="i/n" runs every n-th test starting at i, and
# CHECK_TEST_ONLY=name runs that one test alone. Neither set: all run.
test_index=0
t() {
  local i=$test_index
  test_index=$((test_index + 1))
  if [ -n "${CHECK_TEST_ONLY:-}" ]; then
    [ "$1" = "$CHECK_TEST_ONLY" ] || return 0
  elif [ -n "${CHECK_TEST_SHARD:-}" ]; then
    [ $((i % ${CHECK_TEST_SHARD#*/})) -eq "${CHECK_TEST_SHARD%/*}" ] || return 0
  fi
  "$1"
}

t test_the_file_is_utf8_with_a_bom_and_ascii_inside
t test_the_header_says_what_it_is_and_what_it_needs
t test_the_block_shape_is_the_siblings_shape
t test_the_marks_and_the_dash_are_built_from_their_code_points
t test_the_block_carries_every_pinned_key
t test_the_block_is_printed_once_and_the_encoding_is_put_back
t test_the_phases_are_the_ones_the_plan_names
t test_a_row_of_an_unselected_phase_is_not_measured
t test_the_guards_are_all_there_and_run_before_anything_else
t test_the_dry_run_branch_comes_after_the_agent_guard_and_before_the_platform_one
t test_the_non_windows_stop_names_the_sibling_check
t test_no_call_writes_a_concatenation_outside_parentheses
t test_the_watched_runs_are_never_captured
t test_the_pipe_and_the_document_are_read_from_a_capture
t test_the_forced_failure_is_a_terraform_that_answers_version
t test_the_failure_readings_are_the_reporter_s_own_lines
t test_the_log_rules_are_the_cli_s_own
t test_the_log_protection_is_read_as_an_acl
t test_the_retention_half_is_deliberately_not_repeated
t test_only_two_things_are_the_owner_s_reading
t test_the_console_is_named_and_its_own_answer_is_read
t test_the_self_test_only_parses
t test_the_self_tests_own_rules_hold_here
t test_nothing_is_written_outside_the_scratch_directory
t test_the_verdict_names_the_readings_that_failed
t test_the_last_net_prints_a_block_and_ends_two
t test_the_self_test_passes_where_pwsh_is
t test_a_dry_run_runs_nothing_where_pwsh_is
t test_the_agent_guard_fires_where_pwsh_is
t test_the_belong_guard_fires_where_pwsh_is
t test_a_phase_that_is_not_one_is_refused_where_pwsh_is
t test_off_windows_the_check_stops_and_names_check310_where_pwsh_is

if [ "$failures" -ne 0 ]; then
  echo "check311_test: $failures failure(s)" >&2
  exit 1
fi
echo "check311_test: all tests passed" >&2
