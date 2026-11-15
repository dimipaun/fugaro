<#
check311.ps1 -- Check 311: the CLI's default output on Windows, in Windows
Terminal and in the legacy console.

Reads, in this order, on the machine you run it on:

  1. which console this run is in, and what that console says about virtual
     terminal sequences: WT_SESSION and TERM_PROGRAM for Windows Terminal, the
     console's own code page, and $Host.UI.SupportsVirtualTerminal (the console
     host's own answer, which is the thing termenv's ANSI profile needs on
     Windows). This is the reading Check 310 cannot take on macOS or Linux,
     because neither has a console that may or may not draw an escape byte.
  2. `fugaro update` at this console, bare, so you watch it: colour, the
     reporter's own marks, the line in flight turning in place and taken down
     before the step's own line.
  3. `fugaro setup --plan-only`, the same way: the plan view's own rows.
  4. NO_COLOR=1 `fugaro update` at this console, where the rule has something to
     turn off.
  5. `fugaro update` piped (PowerShell captures it): a pipe is not a console and
     the account has to be plain text there.
  6. `fugaro update --json`: ONE document on stdout, the human lines on stderr.
  7. a forced failure: this script writes its own `terraform.cmd`, puts it first
     on PATH, and `fugaro update` fails inside a stage with the reporter open --
     so the failed step's line, the error's context, the log's path and the
     `fugaro troubleshoot <id>` line are all read from a real run.
  8. the log directory under %LOCALAPPDATA%, the newest update log's name, and
     the ACL of the log and of its directory.

What it never does: it changes no setting of your console, writes nothing
outside its own scratch directory and the logs the CLI files itself, removes no
log of yours, names no cloud project, and calls no API. It never runs `fugaro`
with a project other than the one you name, and it refuses two sessions before
it runs anything: a project that names the owner's own installation (`belong`),
and a coding agent's own session -- the same door `internal/agentenv.Session`
closes for the CLI's own commands, because a converge in an agent's session is
not the run this check is about. Both refusals are rows of the block, and both
end the run 2.

Usage:      pwsh -NoProfile -File scripts/live-checks/check311.ps1 -Project <fugaro project>
              [-Phase all|look|failure|logs] [-FugaroFlags '<flags>']
              [-DryRun] [-SelfTest]
            Run it TWICE: once in Windows Terminal, once in the legacy console
            (conhost -- open `cmd.exe` from a shortcut that is not Windows
            Terminal, or `conhost.exe pwsh.exe`). The two blocks are the point:
            the same five commands, in the two consoles, with the console named
            in each block. `-DryRun` prints the plan and runs nothing at all.
            `-SelfTest` parses this file and checks that its plan, its block
            keys and its phases agree with its code -- it runs no `fugaro`, and
            it is the only mode that works off Windows.

Needs:      PowerShell 5.1 (the one Windows ships) or PowerShell 7, and a
            `fugaro` on PATH. Nothing is installed and nothing is fetched; a
            missing tool stops the run with one line naming it.

Where:      a Windows machine, at a real console -- the console is the subject,
            so a run whose own stdout is redirected is not this check, and it
            stops rather than reading a console that is not there. Run it on
            your own installation and a project you name, never `belong`, and
            never from a coding agent's session.

Costs:      five real converge runs (`fugaro update` three times, a
            `setup --plan-only`, and the piped and --json updates), plus one
            forced-failure run of a second or two. No model call, no run, no
            image build, nothing billable. This check deliberately does NOT
            repeat Check 310's retention half (51 failing converges): retention
            is a property of the log directory, not of the console, and Check
            310 reads it once on macOS or Linux.

Runs:       by YOU. No agent runs any step of it.

Output:     progress on stderr, ONE block on stdout headed CHECK 311 RESULT.
            Exit 0 when every fact held, 1 when one did not (each row says
            which), and 2 when the run could not tell (a guard stopped it, this
            is not Windows, a tool is missing, the forced failure produced no
            failure block at all, an internal error ended it).

Why the watched runs are the owner's reading here and not this script's: on
macOS and Linux Check 310 borrows a pty with `script`, so it can read the bytes
of a run at a terminal. Windows has no `script`, and PowerShell captures a
native command's output through a pipe -- which is not a console, and turns
colour off -- so the bytes of a run you watch cannot be read here the way 310
reads them. What the console did with those bytes is therefore YOUR reading,
asked as two yes/no questions per console (the view, and NO_COLOR), and every
other row here is read from a file or from a run that is meant to be a pipe. If
the view question comes back "no colour", the row says how to tell a product
fault from your shell's own piping: run `cmd /c fugaro update` in the same
console, which hands the child the console's own handle.

This file is UTF-8 WITH a byte order mark, on purpose: Windows PowerShell 5.1
reads a script without one as ANSI, and that is the one thing that would mangle
a script whose whole subject is what a console draws. Its own bytes are ASCII on
top of that -- every mark and dash it prints is built from its code point
([char]0x2014, [char]0x2714) rather than written into the file -- so the reading
does not depend on the host guessing this file's encoding right. The captured
runs are decoded as UTF-8, which is what the CLI writes; the console's own code
page is READ and reported, never set, because it is what decides how the marks
are drawn and a check that set it would be reading its own setting.

CHECK311_FUGARO names the CLI to run (this script's own test replaces it).
#>
[CmdletBinding()]
param(
    [string]$Project = '',
    [string]$Phase = 'all',
    [string]$FugaroFlags = '',
    [switch]$DryRun,
    [switch]$SelfTest
)

$ErrorActionPreference = 'Stop'

# The fact keys the block carries, in the order it prints them. The self-test
# reads this list against the keys the code really sets, so a row that is
# printed but never measured -- or measured and never printed -- is a failure of
# the self-test rather than a block nobody can diff.
$script:BlockKeys = @(
    'project', 'belong', 'agent', 'console', 'vt-mode', 'state-dir', 'log-dir',
    'console-view', 'tty-summary', 'no-color', 'piped', 'json',
    'fail-step', 'fail-context', 'fail-log-path', 'fail-log-protection', 'fail-log-context',
    'log-name', 'log-protection', 'log-dir-protection',
    'teardown', 'output'
)

# The phases and the rows each one owns. A row of a phase that was not selected
# is recorded as not measured with the phase named, so a run of one phase is
# never read as a run in which everything held.
$script:Phases = @('all', 'look', 'failure', 'logs')
$script:PhaseKeys = @{
    'look'    = @('console', 'vt-mode', 'console-view', 'tty-summary', 'no-color', 'piped', 'json', 'log-dir')
    'failure' = @('fail-step', 'fail-context', 'fail-log-path', 'fail-log-protection', 'fail-log-context')
    'logs'    = @('log-name', 'log-protection', 'log-dir-protection')
}

$script:Facts = @{}
$script:BlockPrinted = $false
$script:ExitCode = 0
$script:Scratch = ''
$script:StateBase = ''
$script:LogDir = ''
$script:IsWin = ($env:OS -eq 'Windows_NT') -or ($PSVersionTable.PSEdition -eq 'Desktop')

# ---------------------------------------------------------------- the facts
function Say([string]$message) {
    [Console]::Error.WriteLine('check311.ps1: ' + $message)
}

function Set-Fact([string]$key, [string]$status, [string]$detail) {
    $script:Facts[$key] = @{ Status = $status; Detail = $detail }
}

function Get-Status([string]$key) {
    if ($script:Facts.ContainsKey($key)) { return $script:Facts[$key].Status }
    return ''
}

# Stop-Run: this check could not tell. The block is printed with what was read
# before it, and the run ends 2: a reading nobody could take is never reported as
# a failure of the thing being read.
function Stop-Run([string]$why) {
    Say 'STOPPING: ' + $why
    Write-Block 2
    exit 2
}

# Write-Block: the block, in the same shape every sibling check prints
# (FACT <key>: PASS|FAIL|INFO -- <detail>), on stdout, once, whatever happened.
# It is printed as UTF-8 because the dash in it is not in every console's code
# page -- on the legacy console a code page of 437 would draw it as '?' -- and
# the console's own encoding is put back before the run ends, so this script
# changes no setting of your console. The look phase reads the code page long
# before this, so the reading is still of your console's own setting. The once is
# enforced here rather than trusted to the callers, so no path can print two
# blocks for one run.
function Write-Block([int]$code) {
    if ($script:BlockPrinted) { return }
    $script:BlockPrinted = $true
    $old = [Console]::OutputEncoding
    try { [Console]::OutputEncoding = [Text.Encoding]::UTF8 } catch { }
    try {
        [Console]::Out.WriteLine('CHECK 311 RESULT')
        foreach ($key in $script:BlockKeys) {
            if ($script:Facts.ContainsKey($key)) {
                $fact = $script:Facts[$key]
                [Console]::Out.WriteLine('FACT ' + $key + ': ' + $fact.Status + ' ' + [char]0x2014 + ' ' + $fact.Detail)
            } else {
                [Console]::Out.WriteLine('FACT ' + $key + ': INFO ' + [char]0x2014 + ' not reached (the run stopped before this step)')
            }
        }
        [Console]::Out.WriteLine('CHECK 311 RESULT END (exit ' + $code + ')')
    } finally {
        try { [Console]::OutputEncoding = $old } catch { }
    }
}

# ------------------------------------------------------------ the environment
# resolve_state_dir mirrors localcfg.UserDir for DirState: %LOCALAPPDATA% where
# it is set and absolute, else the profile's .local\state -- the same rule the
# CLI resolves its own state directory by, so the logs read here are the logs it
# writes.
function Resolve-StateDir {
    if ($env:LOCALAPPDATA -and [IO.Path]::IsPathRooted($env:LOCALAPPDATA)) { return $env:LOCALAPPDATA }
    foreach ($name in @('HOME', 'USERPROFILE')) {
        $value = [Environment]::GetEnvironmentVariable($name)
        if ($value -and [IO.Path]::IsPathRooted($value)) { return (Join-Path $value '.local\state') }
    }
    return ''
}

# Get-ConsoleKind: which console this run is in, and the evidence for it. A
# reading that named a console without saying what it read it from could not be
# checked by the person reading the block.
function Get-ConsoleKind {
    if ($env:WT_SESSION) { return 'Windows Terminal (WT_SESSION is set)' }
    if ($env:TERM_PROGRAM) { return 'TERM_PROGRAM=' + $env:TERM_PROGRAM }
    if ($env:ConEmuANSI) { return 'ConEmu (ConEmuANSI=' + $env:ConEmuANSI + ')' }
    if ($env:ANSICON) { return 'ANSICON=' + $env:ANSICON }
    return 'the legacy console (no WT_SESSION, no TERM_PROGRAM, no ConEmu)'
}

# Get-CodePage: the console's own output code page, read with chcp.com and never
# set. It is what decides whether the marks are drawn or come out as boxes.
function Get-CodePage {
    try {
        $out = (& chcp.com 2>$null | Out-String)
    } catch {
        return 'unknown'
    }
    $m = [regex]::Match($out, '([0-9]{3,5})')
    if ($m.Success) { return $m.Groups[1].Value }
    return 'unknown'
}

# Get-VTSupport: the console host's own answer about virtual terminal sequences.
# The property arrived with PowerShell 5.1 and is absent on an older host, which
# is itself a reading, so it is read defensively rather than assumed.
function Get-VTSupport {
    $property = $Host.UI.PSObject.Properties['SupportsVirtualTerminal']
    if (-not $property) { return $null }
    return [bool]$property.Value
}

# --------------------------------------------------------------- the CLI calls
$script:Fugaro = 'fugaro'
if ($env:CHECK311_FUGARO) { $script:Fugaro = $env:CHECK311_FUGARO }
$script:FlagList = @()
if ($FugaroFlags.Trim().Length -gt 0) { $script:FlagList = @($FugaroFlags.Trim() -split '\s+') }
$script:LastExit = 0

function Get-Argv([string[]]$Extra) {
    $argv = @('--project', $Project)
    if ($script:FlagList.Count -gt 0) { $argv += $script:FlagList }
    if ($Extra -and $Extra.Count -gt 0) { $argv += $Extra }
    return $argv
}

function Require-Tool([string]$name, [string]$why) {
    $found = Get-Command -Name $name -CommandType Application -ErrorAction SilentlyContinue
    if (-not $found) { Stop-Run ('Check 311 needs ''' + $name + ''' on PATH and it is not there (' + $why + ')') }
}

# Invoke-Bare: the CLI at THIS console, with nothing capturing it -- no pipe, no
# redirection, no Out-String -- so it is the run you watch, and so its stdout is
# the console's own handle rather than a pipe. It returns NOTHING on purpose: a
# call whose value is assigned is a call whose output is captured, which is both
# the end of the run being watchable and the end of the colour; the exit code is
# left in $script:LastExit, which is what the call sites read.
function Invoke-Bare([string[]]$Extra, [switch]$NoColor, [string]$ExtraPath) {
    $oldNoColor = $env:NO_COLOR
    $oldPath = $env:PATH
    if ($NoColor) { $env:NO_COLOR = '1' }
    if ($ExtraPath) { $env:PATH = $ExtraPath + [IO.Path]::PathSeparator + $oldPath }
    $script:LastExit = 0
    try {
        $argv = @(Get-Argv $Extra)
        & $script:Fugaro @argv
        $script:LastExit = $LASTEXITCODE
    } finally {
        $env:NO_COLOR = $oldNoColor
        $env:PATH = $oldPath
    }
}

# Invoke-Capture: the CLI with its output captured, which is what the readings
# about a PIPE and a DOCUMENT are about. The capture is decoded as UTF-8 -- what
# the CLI writes -- and the console's own encoding is put back afterwards, so a
# captured run cannot change how the next watched one is drawn.
function Invoke-Capture([string[]]$Extra, [string]$ExtraPath, [switch]$NoColor) {
    $oldNoColor = $env:NO_COLOR
    $oldPath = $env:PATH
    $oldEncoding = [Console]::OutputEncoding
    if ($NoColor) { $env:NO_COLOR = '1' }
    if ($ExtraPath) { $env:PATH = $ExtraPath + [IO.Path]::PathSeparator + $oldPath }
    $errFile = Join-Path $script:Scratch 'capture.err'
    $script:LastExit = 0
    $text = ''
    try {
        [Console]::OutputEncoding = [Text.Encoding]::UTF8
        $argv = @(Get-Argv $Extra)
        $text = (& $script:Fugaro @argv 2>$errFile | Out-String)
        $script:LastExit = $LASTEXITCODE
    } finally {
        [Console]::OutputEncoding = $oldEncoding
        $env:NO_COLOR = $oldNoColor
        $env:PATH = $oldPath
    }
    return $text
}

# ------------------------------------------------------------- the log reads
function Get-NewestLog([string]$command) {
    if (-not (Test-Path -LiteralPath $script:LogDir)) { return '' }
    $pattern = '^' + [regex]::Escape($command) + '-[0-9]{14}-[0-9]+\.log$'
    $names = @(Get-ChildItem -LiteralPath $script:LogDir -File -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -match $pattern } | Sort-Object -Property Name)
    if ($names.Count -eq 0) { return '' }
    return $names[$names.Count - 1].FullName
}

function Get-ClosingLine([string]$path) {
    if (-not (Test-Path -LiteralPath $path)) { return '' }
    $lines = @(Get-Content -LiteralPath $path -ErrorAction SilentlyContinue)
    $pattern = '^(Done|Failed|Left for you) in [0-9]+\.[0-9]s, ([0-9]+ steps|1 step)$'
    $found = ''
    foreach ($line in $lines) { if ($line -match $pattern) { $found = $line } }
    return $found
}

# Get-AclFinding: Windows has no POSIX mode, so Go's 0600 is the file's ACL
# inside the user's own profile. The rule this reads is the one that stands for
# Check 310's `ls -l`: the log is readable and writable by the person it belongs
# to, by SYSTEM and by the administrators, and by nobody else. An entry that
# grants anything to anyone else is a finding, named in the detail.
function Get-AclFinding([string]$path) {
    $me = ''
    try { $me = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name } catch { $me = '' }
    $acl = Get-Acl -LiteralPath $path
    $allowed = @('NT AUTHORITY\SYSTEM', 'BUILTIN\Administrators', 'CREATOR OWNER', 'NT AUTHORITY\OWNER RIGHTS', 'OWNER RIGHTS')
    $seen = @()
    $bad = @()
    foreach ($rule in $acl.Access) {
        if ($rule.AccessControlType -ne 'Allow') { continue }
        $id = $rule.IdentityReference.Value
        $seen += $id
        $ok = $false
        if ($me -and ($id -ieq $me)) { $ok = $true }
        foreach ($name in $allowed) { if ($id -ieq $name) { $ok = $true } }
        if (-not $ok) { $bad += $id }
    }
    return @{ Bad = $bad; Seen = ($seen -join ', ') }
}

# ------------------------------------------------------------- the questions
# Ask-Owner: the one kind of reading no script can take here -- what the console
# DID with the bytes. It is asked only where there is a console to answer on, and
# a run whose input is redirected records the row as not measured rather than
# reading an end of file as an answer.
function Ask-Owner([string]$key, [string]$question) {
    if ([Console]::IsInputRedirected) {
        Set-Fact $key 'INFO' 'not measured: this run has no console on its input to ask the question at, so the reading only your eyes can take is unread here. Run it at a real console to answer it'
        return
    }
    [Console]::Error.WriteLine('QUESTION -- ' + $question)
    $answer = [Console]::In.ReadLine()
    $flat = ''
    if ($answer) { $flat = $answer.Trim().ToLower() }
    if ($flat -eq 'y' -or $flat -eq 'yes') {
        Set-Fact $key 'PASS' 'you watched it at this console and said it looked right: ' + $question
    } elseif ($flat -eq 'n' -or $flat -eq 'no') {
        Set-Fact $key 'FAIL' 'you watched it at this console and said it did NOT look right: ' + $question
    } else {
        Set-Fact $key 'INFO' ('not measured: the answer was ''' + $answer + ''', which is neither yes nor no')
    }
}

# ------------------------------------------------------------- phase: look
function Invoke-LookPhase {
    # 1. This console, and what it says about virtual terminal sequences.
    $kind = Get-ConsoleKind
    $codePage = Get-CodePage
    Set-Fact 'console' 'PASS' ('this run is in ' + $kind + ', code page ' + $codePage + ' (' + $PSVersionTable.PSEdition + ' ' + $PSVersionTable.PSVersion + ', host ' + $Host.Name + '); the block names the console so the two runs -- Windows Terminal and the legacy console -- can be compared line for line')
    $vt = Get-VTSupport
    if ($null -eq $vt) {
        Set-Fact 'vt-mode' 'INFO' ('this console host does not answer the question (SupportsVirtualTerminal arrived with PowerShell 5.1): whether the console can draw an escape sequence is unread here, and the view question below is the reading')
    } elseif ($vt) {
        Set-Fact 'vt-mode' 'PASS' ('the console host says it supports virtual terminal sequences ($Host.UI.SupportsVirtualTerminal is true), which is what termenv''s ANSI profile needs on Windows for a colour to be drawn rather than printed as its escape characters')
    } else {
        Set-Fact 'vt-mode' 'INFO' ('the console host says it does NOT support virtual terminal sequences ($Host.UI.SupportsVirtualTerminal is false): colour that is written as an escape sequence would then be printed literally, and the view question below is the reading that settles what you saw')
    }

    # 2. `fugaro update` at this console, which you watch.
    Say ('1/5: ' + $script:Fugaro + ' update at this console (apply; watch it)')
    Invoke-Bare @('update')
    $watchedExit = $script:LastExit
    Say ('  (exit ' + $watchedExit + ')')

    # 3. The plan view: the same reporter, the rows of a run that is not a run.
    Say ('2/5: ' + $script:Fugaro + ' setup --plan-only at this console (creates nothing)')
    Invoke-Bare @('setup', '--plan-only')
    Say ('  (exit ' + $script:LastExit + ')')

    Ask-Owner 'console-view' ('in the two runs above, at THIS console: were they painted (colour, not literal escape characters), did the step lines carry the reporter''s own marks (' + [char]0x2714 + ' ' + [char]0x2716 + ' ' + [char]0x25CB + ' ' + [char]0x2013 + ' ' + [char]0x21B3 + ') drawn as marks, did the line in flight turn in place and get taken down before the step''s own result line, and did the plan view show its own rows? (If you saw no colour at all: run cmd /c ' + $script:Fugaro + ' update in this same console and answer for what THAT does -- a difference between the two is your shell''s own handling of a native command, not the CLI.) Answer y or n')

    # 4. NO_COLOR at this console, where the rule has something to turn off.
    Say ('3/5: NO_COLOR=1 ' + $script:Fugaro + ' update at this console (watch it: it must be plain)')
    Invoke-Bare @('update') -NoColor
    Say ('  (exit ' + $script:LastExit + ')')
    Ask-Owner 'no-color' 'the run just now had NO_COLOR=1 and the run before it did not: was the first one PLAIN (no colour at all, no escape characters) while the second one was painted? Answer y or n'

    # 5. A pipe: no escape byte may appear at all.
    Say ('4/5: ' + $script:Fugaro + ' update piped (PowerShell captures it: stdout is not a console)')
    $piped = Invoke-Capture @('update')
    $pipedEsc = ([regex]::Matches($piped, [string][char]27)).Count
    $pipedClosing = ''
    foreach ($line in ($piped -split "`n")) {
        if ($line.Trim() -match '^(Done|Failed|Left for you) in [0-9]+\.[0-9]s, ([0-9]+ steps|1 step)$') { $pipedClosing = $line.Trim() }
    }
    if ($pipedEsc -eq 0) {
        Set-Fact 'piped' 'PASS' ('a pipe is not a console and the run wrote no escape byte to it (exit ' + $script:LastExit + '; its closing line was ''' + $pipedClosing + '''): the account is plain text, which is what a log, a CI job and a pipeline all need (CL5b, T4(a))')
    } else {
        Set-Fact 'piped' 'FAIL' ('the run wrote ' + $pipedEsc + ' escape bytes to a pipe: colour has to be off wherever the output is not a console (T4(a))')
    }

    # 6. --json: one document on stdout, the human lines on stderr.
    Say ('5/5: ' + $script:Fugaro + ' update --json')
    $json = Invoke-Capture @('update', '--json')
    $doc = $null
    $problem = ''
    try {
        $doc = $json | ConvertFrom-Json
    } catch {
        $problem = 'stdout is not one JSON document (' + $_.Exception.Message + ')'
    }
    if ($problem -eq '' -and $doc -and $doc.project -ne $Project) {
        $problem = 'the document names project ''' + $doc.project + ''' and this run was made on ''' + $Project + ''''
    }
    if ($problem -eq '') {
        Set-Fact 'json' 'PASS' ('stdout is ONE JSON document and it names this run''s own project (''' + $doc.project + ''', exit ' + $script:LastExit + '), so a person''s viewer, the console and the mobile app can read the run without a terminal (CL5b, G4)')
    } else {
        Set-Fact 'json' 'FAIL' ($problem + ': --json puts one document on stdout and the human lines on stderr, and nothing else may be on stdout at all')
    }

    # 7. The log directory the five runs above filed their logs in.
    if (-not (Test-Path -LiteralPath $script:LogDir)) {
        Set-Fact 'log-dir' 'FAIL' ('the log directory ' + $script:LogDir + ' is not there after five converges: a converge that keeps no log leaves a failure with no path to print (CL3, D1352)')
    } else {
        $newest = Get-NewestLog 'update'
        if ($newest -eq '') {
            Set-Fact 'log-dir' 'FAIL' ($script:LogDir + ' holds no update-*.log after five converges: the log is what a failure''s ''full output:'' line names, and there is none')
        } else {
            Set-Fact 'log-dir' 'PASS' ('the runs above filed their logs in ' + $script:LogDir + ' (the newest is ' + (Split-Path -Leaf $newest) + '), which is where a failure''s path points')
            # The watched run's own closing line, read from the log it filed:
            # the bytes of a run at a console cannot be captured here, but the
            # run writes its own lines to the log, so the closing line is a real
            # reading of a real run rather than a claim about one.
            $closing = Get-ClosingLine $newest
            if ($closing -ne '') {
                Set-Fact 'tty-summary' 'PASS' ('the run closed with its own line, read from the log it filed (' + (Split-Path -Leaf $newest) + '): ''' + $closing + '''')
            } else {
                Set-Fact 'tty-summary' 'FAIL' ('the newest update log (' + (Split-Path -Leaf $newest) + ') carries no closing line: the shape is ''Done in 3.2s, 5 steps'' (or Failed / Left for you), and a run whose account has no last line cannot be read at a glance')
            }
        }
    }
    Say ('  (the watched update exited ' + $watchedExit + ')')
}

# ---------------------------------------------------------- phase: failure
# write_stub: the terraform this check forces its failure with. It answers the
# one read every converge makes before it plans anything (`version -json`, which
# tf.New parses) and refuses every other subcommand, so the run fails inside a
# stage with the reporter open and prints the block G8 is about. The two lines it
# writes to stderr are the context that block has to carry.
$script:StubLineOne = 'Error: check311 forced this failure: the terraform on PATH refused to plan or apply anything, so nothing was created and nothing was changed'
$script:StubLineTwo = 'check311''s own context line: this failure was forced by scripts/live-checks/check311.ps1, not by your installation'

function Write-Stub {
    $dir = Join-Path $script:Scratch 'stub'
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
    $lines = @(
        '@echo off',
        'rem Check 311''s forced failure. The version read is answered, because every',
        'rem converge makes it before it plans anything and a run that failed there',
        'rem would fail before the stage this reading is about.',
        'if "%~1"=="version" (',
        '  echo {"terraform_version":"1.9.5"}',
        '  exit /b 0',
        ')',
        '1>&2 echo ' + $script:StubLineOne,
        '1>&2 echo ' + $script:StubLineTwo,
        'exit /b 1'
    )
    $path = Join-Path $dir 'terraform.cmd'
    Set-Content -LiteralPath $path -Value $lines -Encoding ASCII
    return $dir
}

function Invoke-FailurePhase {
    $stubDir = Write-Stub
    Say ($script:Fugaro + ' update, with this check''s own terraform first on PATH')
    $cap = Invoke-Capture @('update') -ExtraPath $stubDir
    Say ('  (exit ' + $script:LastExit + '; nothing was applied: the only terraform on that run''s PATH refuses every plan and apply)')

    $step = ''
    $context = @()
    $path = ''
    $id = ''
    foreach ($line in ($cap -split "`n")) {
        $flat = $line.TrimEnd("`r")
        if ($flat -match '^' + [char]0x2716 + ' ' -and $step -eq '') { $step = $flat }
        elseif ($flat -match '^    [^ ]' -and $flat -notmatch '^    full output: ' -and $flat -notmatch '^    Something went wrong\.') { $context += $flat }
        if ($flat -match '^    full output: (.+)$') { $path = $Matches[1].Trim() }
        if ($flat -match 'fugaro troubleshoot ([A-Za-z0-9._-]+)') { $id = $Matches[1] }
    }
    if ($step -eq '' -and $path -eq '') {
        Set-Fact 'fail-step' 'FAIL' ('the forced failure printed no failed step line and no log path: the run refused before it reached a stage. First line: ' + (($cap -split "`n")[0]))
        Stop-Run 'the forced failure produced no failure block at all, so there is nothing to read'
    }
    if ($step -ne '') {
        Set-Fact 'fail-step' 'PASS' ('the failure is one step''s own line, with the reporter''s failed mark and the reason after it: ''' + $step.Substring(0, [Math]::Min(200, $step.Length)) + '''')
    } else {
        Set-Fact 'fail-step' 'FAIL' ('no line carries the failed step''s mark (' + [char]0x2716 + '): the run failed without saying which step it failed in')
    }
    if ($context.Count -gt 0) {
        Set-Fact 'fail-context' 'PASS' ('the error''s context is printed under the step''s line, four spaces in: ''' + $context[0].Substring(0, [Math]::Min(200, $context[0].Length)) + '''')
    } else {
        Set-Fact 'fail-context' 'FAIL' ('nothing is printed under the failed step''s line: the error''s own context is what a reader needs to act on it, and one line with no context is a line that says only that something went wrong')
    }
    if ($path -ne '' -and $id -ne '') {
        $stem = [IO.Path]::GetFileNameWithoutExtension($path)
        if ($stem -eq $id) {
            Set-Fact 'fail-log-path' 'PASS' ('the failure names its log (' + $path + ') and the id ''fugaro troubleshoot ' + $id + ''' routes by, which is that file''s own stem, so one word finds the whole output of the run that failed')
        } else {
            Set-Fact 'fail-log-path' 'FAIL' ('the failure names the log ' + $path + ' and the id ''fugaro troubleshoot ' + $id + ''', which is not that file''s stem: the one word the block prints would open another run''s output')
        }
    } elseif ($path -ne '') {
        Set-Fact 'fail-log-path' 'FAIL' ('the failure names its log (' + $path + ') and prints no ''fugaro troubleshoot'' id: the id is the one word a person can retype from a screenshot')
    } else {
        Set-Fact 'fail-log-path' 'FAIL' ('the failure prints no ''full output:'' line at all, so there is no path to the run''s own output and nothing to read afterwards (CL3, D1352)')
    }
    if ($path -ne '' -and (Test-Path -LiteralPath $path)) {
        $acl = Get-AclFinding $path
        if ($acl.Bad.Count -eq 0) {
            Set-Fact 'fail-log-protection' 'PASS' ('the log the failure named is readable and writable by you, by SYSTEM and by the administrators, and by nobody else (its access rules name ' + $acl.Seen + '): a log holds a run''s own output, which can carry a value, and Windows has no 0600 -- the ACL inside your own profile is what stands for Check 310''s mode reading')
        } else {
            Set-Fact 'fail-log-protection' 'FAIL' ('the log the failure named grants access to ' + ($acl.Bad -join ', ') + ', which is neither you, nor SYSTEM, nor the administrators: a log holds a run''s own output, which can carry a value')
        }
        $logText = Get-Content -LiteralPath $path -Raw -ErrorAction SilentlyContinue
        $hasContext = $logText -and ($logText -like ('*' + $script:StubLineTwo + '*'))
        $hasClosing = $logText -and ($logText -match '(?m)^(Done|Failed|Left for you) in [0-9]+\.[0-9]s, ')
        if ($hasContext -and $hasClosing) {
            Set-Fact 'fail-log-context' 'PASS' ('the log holds the run''s own lines whole: the context the screen carried and the closing line are both in ' + (Split-Path -Leaf $path) + ' (D1352)')
        } else {
            Set-Fact 'fail-log-context' 'FAIL' ('the log ' + (Split-Path -Leaf $path) + ' does not hold the run''s own lines whole (context: ' + $hasContext + ', closing line: ' + $hasClosing + '): the log is the full output of the run, which is what makes a failure readable a day later (D1352)')
        }
    } else {
        Set-Fact 'fail-log-protection' 'FAIL' 'there is no log file at the path the failure named, so there is nothing to read a protection off'
        Set-Fact 'fail-log-context' 'FAIL' 'there is no log file at the path the failure named, so the run''s own output is nowhere'
    }
}

# ------------------------------------------------------------- phase: logs
function Invoke-LogsPhase {
    if (-not (Test-Path -LiteralPath $script:LogDir)) {
        Stop-Run ('the log directory ' + $script:LogDir + ' is not there, so there is no log to read: run -Phase look (or the whole check), which makes one')
    }
    $newest = Get-NewestLog 'update'
    if ($newest -eq '') {
        Stop-Run ($script:LogDir + ' holds no update-*.log, so there is no log to read: run -Phase look (or the whole check) first')
    }
    $name = Split-Path -Leaf $newest
    if ($name -match '^update-[0-9]{14}-[0-9]+\.log$') {
        Set-Fact 'log-name' 'PASS' ('the newest update log is named ' + $name + ': <command>-<14 digits UTC>-<pid>.log, which is the name the retention and the ''fugaro troubleshoot'' id both read back')
    } else {
        Set-Fact 'log-name' 'FAIL' ('the newest update log is named ' + $name + ' and not <command>-<14 digits UTC>-<pid>.log: the name is what the pruning and the troubleshoot id read, so a log outside the rule is one nothing finds again')
    }
    $acl = Get-AclFinding $newest
    if ($acl.Bad.Count -eq 0) {
        Set-Fact 'log-protection' 'PASS' ('the newest log is readable and writable by you, by SYSTEM and by the administrators, and by nobody else (its access rules name ' + $acl.Seen + ')')
    } else {
        Set-Fact 'log-protection' 'FAIL' ('the newest log grants access to ' + ($acl.Bad -join ', ') + ': a log holds a run''s own output, which can carry a value')
    }
    $dirAcl = Get-AclFinding $script:LogDir
    if ($dirAcl.Bad.Count -eq 0) {
        Set-Fact 'log-dir-protection' 'PASS' ('the log directory ' + $script:LogDir + ' is readable and writable by you, by SYSTEM and by the administrators, and by nobody else (its access rules name ' + $dirAcl.Seen + '): the logs are the owner''s own (CL3, D1351)')
    } else {
        Set-Fact 'log-dir-protection' 'FAIL' ('the log directory grants access to ' + ($dirAcl.Bad -join ', ') + ': every log in it holds a run''s own output')
    }
}

# ------------------------------------------------------------- the plan
function Write-Plan {
    Say 'dry run: NOTHING below was done. A real run would do, in this order:'
    Say ('  [console] read which console this is, its code page and its own answer about virtual terminal sequences ($Host.UI.SupportsVirtualTerminal) -- the reading Check 310 cannot take off Windows')
    Say ('  [look]    ' + $script:Fugaro + ' --project ' + $Project + ' update, at this console, bare, so you watch it')
    Say ('  [look]    ' + $script:Fugaro + ' --project ' + $Project + ' setup --plan-only, the same way: the plan view''s own rows')
    Say ('  [look]    QUESTION 1: at this console, were the run and the plan view painted, marked, and was the line in flight drawn and taken down?')
    Say ('  [look]    NO_COLOR=1 ' + $script:Fugaro + ' --project ' + $Project + ' update, at this console')
    Say ('  [look]    QUESTION 2: was that one plain while the run before it was painted?')
    Say ('  [look]    ' + $script:Fugaro + ' --project ' + $Project + ' update piped (a pipe, captured: no escape byte may appear)')
    Say ('  [look]    ' + $script:Fugaro + ' --project ' + $Project + ' update --json (one document on stdout, the human lines on stderr)')
    Say ('  [look]    read ' + $script:LogDir + ' for the newest update log and the closing line inside it')
    Say ('  [failure] write a terraform.cmd that answers `version -json` and refuses everything else, put it first on PATH, and run ' + $script:Fugaro + ' --project ' + $Project + ' update: read the failed step''s line, the context under it, the log''s path, the troubleshoot id, and the log''s own lines and ACL')
    Say ('  [logs]    read the newest update log''s name and the ACL of the log and of ' + $script:LogDir)
    Say ('  [all]     print ONE block on stdout, headed CHECK 311 RESULT, and end 0, 1 or 2')
    Say 'Nothing is written outside the run''s own scratch directory, and no cloud API is called: the converge runs are real, and they are the point.'
}

# ------------------------------------------------------------- the self test
# Invoke-SelfTest parses this file and checks that the three lists that have to
# agree do: every key the code sets is a key the block prints, every key the
# block prints is one the code sets or one a phase owns, and every phase has a
# function. It runs no `fugaro`, reads no file of yours and works off Windows --
# it is the half of this check that can run where PowerShell is and Windows is
# not (pwsh on Linux), which is what its sibling test drives.
function Invoke-SelfTest {
    $ok = $true
    $tokens = $null
    $errors = $null
    [System.Management.Automation.Language.Parser]::ParseFile($PSCommandPath, [ref]$tokens, [ref]$errors) | Out-Null
    if ($errors -and $errors.Count -gt 0) {
        foreach ($e in $errors) {
            [Console]::Error.WriteLine('check311.ps1: parse error at line ' + $e.Extent.StartLineNumber + ': ' + $e.Message)
        }
        $ok = $false
    }
    $text = [IO.File]::ReadAllText($PSCommandPath)
    $literal = @()
    foreach ($m in [regex]::Matches($text, "Set-Fact\s+'([a-z-]+)'")) { $literal += $m.Groups[1].Value }
    # The keys the code sets through something other than a literal Set-Fact, so
    # the two rules below have to be told about them rather than left to read a
    # literal that is not there:
    #   'dry-run'  set by the dry-run branch, which REPLACES the block's key list
    #              with its own five before it prints, so it is in no list above;
    #   a phase's keys  written by the "not measured" loop for a phase that was
    #              not selected -- a Set-Fact whose key is the loop's variable;
    #   'console-view' and 'no-color'  the two rows only your own eyes can take:
    #              Ask-Owner is handed the key, so neither is ever a literal
    #              Set-Fact in this file (and both are in the look phase's list).
    # Rule 1 consults this set as well, which is what makes the two rules mirrors
    # of each other and the self-test pass by construction.
    $owned = @('dry-run')
    foreach ($phase in $script:PhaseKeys.Keys) { $owned += $script:PhaseKeys[$phase] }
    foreach ($key in $literal) {
        if (($script:BlockKeys -notcontains $key) -and ($owned -notcontains $key)) {
            [Console]::Error.WriteLine('check311.ps1: the code sets the key ''' + $key + ''' and the block does not print it: a reading nobody sees is a reading nobody can act on')
            $ok = $false
        }
    }
    foreach ($key in $script:BlockKeys) {
        if (($literal -notcontains $key) -and ($owned -notcontains $key)) {
            [Console]::Error.WriteLine('check311.ps1: the block prints the key ''' + $key + ''' and neither the code nor a branch of it owns it: a row that is never measured reads as a pass to whoever scans the block')
            $ok = $false
        }
    }
    foreach ($phase in $script:Phases) {
        if ($phase -eq 'all') { continue }
        $fn = 'Invoke-' + $phase.Substring(0, 1).ToUpper() + $phase.Substring(1) + 'Phase'
        if (-not (Get-Command -Name $fn -CommandType Function -ErrorAction SilentlyContinue)) {
            [Console]::Error.WriteLine('check311.ps1: -Phase ' + $phase + ' has no ' + $fn + ': a phase the plan names and nothing runs is a reading nobody takes')
            $ok = $false
        }
    }
    if (-not $ok) {
        [Console]::Error.WriteLine('check311.ps1: self-test FAILED')
        exit 2
    }
    [Console]::Out.WriteLine('check311.ps1: self-test passed -- the file parses, ' + $script:BlockKeys.Count + ' block keys and ' + ($script:Phases.Count - 1) + ' phases agree with the code, and nothing was run')
    exit 0
}

# ------------------------------------------------------------------- main
function Invoke-Main {
    if ($SelfTest) { Invoke-SelfTest }

    if ($script:Phases -notcontains $Phase.ToLower()) {
        Say 'usage: check311.ps1 -Project <fugaro project> [-Phase all|look|failure|logs] [-FugaroFlags ''<flags>''] [-DryRun] [-SelfTest]'
        Stop-Run ('-Phase takes all, look, failure or logs, and ''' + $Phase + ''' is none of them')
    }
    $script:Selected = $Phase.ToLower()

    # The state directory is resolved before the dry-run branch, because
    # resolving it reads the environment and touches nothing, and the plan can
    # then name the log directory it would read.
    $script:StateBase = Resolve-StateDir
    $script:LogDir = ''
    if ($script:StateBase -ne '') { $script:LogDir = Join-Path (Join-Path $script:StateBase 'fugaro') 'logs' }

    # guard_agent: the door internal/agentenv.Session closes for the CLI's own
    # commands, and the one Check 310 closes for its own runs. It is judged
    # before the dry-run branch as well, because a dry run of an agent's session
    # is still a plan an agent would then run. The IDE marker is refused only
    # where no person is at the console, exactly as the CLI reads it: with a real
    # console on its input, CLAUDE_CODE_SSE_PORT alone is a person's terminal.
    $markers = @()
    foreach ($name in @('CLAUDECODE', 'CLAUDE_CODE_ENTRYPOINT', 'CLAUDE_CODE_REMOTE', 'CURSOR_AGENT', 'AI_AGENT')) {
        if ([Environment]::GetEnvironmentVariable($name)) { $markers += $name }
    }
    if ($markers.Count -gt 0) {
        Set-Fact 'agent' 'FAIL' ('this session carries the coding agent''s marker ' + ($markers -join ', ') + ', so this is an agent''s session and not yours: Check 311 drives five converges and reads what they print at a person''s console, and no agent runs any step of it')
        Stop-Run ('this is a coding agent''s session (' + ($markers -join ', ') + ' is set). Check 311 is OWNER-RUN: run it in your own console')
    }
    if ([Environment]::GetEnvironmentVariable('CLAUDE_CODE_SSE_PORT') -and [Console]::IsInputRedirected) {
        Set-Fact 'agent' 'FAIL' 'this session carries CLAUDE_CODE_SSE_PORT and has no console on its input: that is an IDE session''s shape, and Check 311 is owner-run'
        Stop-Run 'this looks like an IDE session (CLAUDE_CODE_SSE_PORT is set and there is no console on its input). Run Check 311 in your own console'
    }
    Set-Fact 'agent' 'PASS' 'this is not a coding agent''s session (no agentenv marker is set), so the five converges below are the ones a person runs and not a session''s own'

    if ($DryRun) {
        if ($Project -eq '') {
            Set-Fact 'project' 'INFO' 'no -Project was typed, and a dry run runs nothing: a real run requires one, because a converge with no -Project falls back to the one project config in the working directory'
            Set-Fact 'belong' 'INFO' 'no project was typed, so the guard had nothing to judge: a real run refuses a name that matches the installation ''belong'' before it runs anything'
        } else {
            Set-Fact 'project' 'PASS' ($Project + ' is the project this run would name on every command')
            if ($Project.ToLower().Contains('belong')) {
                Set-Fact 'belong' 'FAIL' ('the project names the owner''s own installation (what was typed contains ''belong''), which this check never reads')
            } else {
                Set-Fact 'belong' 'PASS' ('the project (' + $Project + ') does not name the owner''s own installation')
            }
        }
        Set-Fact 'state-dir' 'PASS' ('the state directory is ' + $script:StateBase + ' and the logs would be read at ' + $script:LogDir + ' (the same rule localcfg.UserDir uses: %LOCALAPPDATA% where it is set and absolute, else the profile''s .local\state)')
        Set-Fact 'dry-run' 'PASS' 'the guards and the plan only: no command was run, no file was read, nothing was applied'
        $script:BlockKeys = @('project', 'belong', 'agent', 'state-dir', 'dry-run')
        Write-Plan
        Write-Block 0
        exit 0
    }

    # The guard on what was typed comes first, on every machine: a project that
    # names the owner's own installation is refused before this script looks at
    # the console, the state directory or a single file.
    if ($Project.ToLower().Contains('belong')) {
        Set-Fact 'belong' 'FAIL' ('the project names the owner''s own installation (what was typed contains ''belong''), which this check never touches')
        Stop-Run 'the project names the owner''s own installation, and this check never reads it'
    }
    Set-Fact 'belong' 'PASS' ('the project (' + $Project + ') does not name the owner''s own installation (what was typed contains no ''belong'')')

    if (-not $script:IsWin) {
        Say 'usage: check311.ps1 -Project <fugaro project> [-Phase all|look|failure|logs] [-FugaroFlags ''<flags>''] [-DryRun] [-SelfTest]'
        Stop-Run 'Check 311 is the WINDOWS console check and this is not Windows: Check 310 (scripts/live-checks/check310.sh) is the one for macOS and Linux, and -DryRun and -SelfTest are the only modes this script has off Windows'
    }

    # The rest of the guards, judged on what was typed before any command runs.
    if ($Project -eq '') {
        Set-Fact 'project' 'FAIL' 'no -Project was typed, and a converge with no -Project falls back to ''the only project config'' on this machine, which would make every reading one of a project you did not name'
        Stop-Run 'a project is required: type -Project <fugaro project>'
    }
    if ($Project -notmatch '^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$') {
        Set-Fact 'project' 'FAIL' ('''' + $Project + ''' is not a Fugaro project name (the config''s own pattern: ^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$)')
        Stop-Run 'the project name is not one the config accepts, so nothing was run'
    }
    Set-Fact 'project' 'PASS' ($Project + ' is a Fugaro project name, and every command this check runs carries it: the CLI is never left to fall back to ''the only project config'' on this machine')

    if ($script:StateBase -eq '') {
        Stop-Run 'there is no state directory to read the logs at: %LOCALAPPDATA%, HOME and USERPROFILE are all unset or relative, and that is the directory the CLI resolves its own logs by'
    }
    Set-Fact 'state-dir' 'PASS' ('the state directory is ' + $script:StateBase + ' and the logs are read at ' + $script:LogDir + ' (the same rule localcfg.UserDir uses: %LOCALAPPDATA% where it is set and absolute, else the profile''s .local\state)')

    if ([Console]::IsOutputRedirected) {
        Stop-Run 'this script''s own stdout is redirected, and the console is the subject of this check: a run whose output is a file or a pipe cannot show colour or a line in flight. Run it at a real console (Windows Terminal or the legacy console) and redirect nothing'
    }

    Require-Tool $script:Fugaro 'every reading is a command of the build''s own'
    if ($script:Selected -eq 'all' -or $script:Selected -eq 'look' -or $script:Selected -eq 'failure') {
        Require-Tool 'chcp.com' 'the console''s own code page is read with it'
    }

    $script:Scratch = Join-Path ([IO.Path]::GetTempPath()) ('chk311.' + [Guid]::NewGuid().ToString('N').Substring(0, 8))
    New-Item -ItemType Directory -Path $script:Scratch -Force | Out-Null
    Say ('scratch directory: ' + $script:Scratch + ' (removed at the end)')

    # Rows of a phase that was not selected are recorded as not measured with the
    # phase named, so a run of one phase is never read as a run where everything
    # held.
    foreach ($phase in $script:PhaseKeys.Keys) {
        if ($script:Selected -ne 'all' -and $script:Selected -ne $phase) {
            foreach ($key in $script:PhaseKeys[$phase]) {
                Set-Fact $key 'INFO' ('not measured: -Phase ' + $script:Selected + ' was selected and this row belongs to another phase (-Phase all runs every one of them)')
            }
        }
    }

    try {
        if ($script:Selected -eq 'all' -or $script:Selected -eq 'look') { Invoke-LookPhase }
        if ($script:Selected -eq 'all' -or $script:Selected -eq 'failure') { Invoke-FailurePhase }
        if ($script:Selected -eq 'all' -or $script:Selected -eq 'logs') { Invoke-LogsPhase }
    } catch {
        Stop-Run ('an internal error ended the run: ' + $_.Exception.Message)
    }

    # Teardown: the scratch directory goes, and nothing of yours was touched.
    if ($script:Scratch -and (Test-Path -LiteralPath $script:Scratch)) {
        Remove-Item -LiteralPath $script:Scratch -Recurse -Force
        Set-Fact 'teardown' 'PASS' 'the scratch directory is gone; no log of yours was deleted and no setting of your console was changed'
    } else {
        Set-Fact 'teardown' 'PASS' 'the scratch directory was already gone; no log of yours was deleted and no setting of your console was changed'
    }

    # The verdict: the console's own readings, named one by one.
    $failed = @()
    foreach ($key in @('console-view', 'tty-summary', 'no-color', 'piped', 'json', 'fail-step', 'fail-context', 'fail-log-path', 'fail-log-protection', 'fail-log-context', 'log-name', 'log-protection', 'log-dir-protection', 'log-dir', 'console', 'vt-mode')) {
        if ((Get-Status $key) -eq 'FAIL') { $failed += $key }
    }
    if ($failed.Count -eq 0) {
        Set-Fact 'output' 'PASS' ('the default output holds in this console (' + (Get-ConsoleKind) + '): the account is painted and marked where it is watched, plain where it is piped, one document under --json, and a failure carries its context and the path of its own log. Run the same block in the other console and compare the two')
        $script:ExitCode = 0
    } else {
        Set-Fact 'output' 'FAIL' ('the default output does not hold in this console (' + (Get-ConsoleKind) + '): ' + ($failed -join ', ') + '. Each row above says which reading it is and what it found')
        $script:ExitCode = 1
    }
    Write-Block $script:ExitCode
    exit $script:ExitCode
}

# The last net. Every path that can stop this run stops it with Stop-Run, which
# prints the block and ends 2; this catches what is left, so an unexpected error
# ends with a block and exit 2 rather than a red PowerShell error and exit 1 that
# an owner would read as a failure of the CLI. Write-Block prints once, so a stop
# that somehow arrives here as well cannot print a second block.
try {
    Invoke-Main
} catch {
    if (-not $script:BlockPrinted) {
        Set-Fact 'output' 'INFO' ('an internal error ended the run before it reached a verdict: ' + $_.Exception.Message)
        Write-Block 2
    }
    exit 2
}
