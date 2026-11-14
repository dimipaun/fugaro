#!/usr/bin/env bash
# Tests for scripts/live-checks/check69.sh, against a fake curl, a fake openssl
# and a fake npm on PATH and a throwaway checkout this test makes: nothing here
# reaches the network, there is no cloud project and no credential, and no step
# of the live check is run.
#
# The fake curl answers from $FAKE69_DIR: the origin probe (the call whose
# write-out asks for %{scheme}), the four pages' bodies, the header print step 1
# makes, and one status per HEAD — and it records every call verbatim, so a test
# says what the check read by writing files and can tell a run that fetched from
# one that never did. A page's body is a file, and the check's happy path needs
# the deployed home page to be byte-identical to the artifact `npm run build`
# produces: the fake npm copies $FAKE69_DIR/build/index.html into the tree, so
# the fixture writes the same bytes to both and a test that wants them different
# changes one of them.
#
# The fixture is the site's own facts and nothing else — a canonical_url, a
# repository, the two feature flags, the release block, the release's checksum
# list and the pages. The check reads all of them out of the tree, so the
# fixture is where a test says what the site claims; the checksums it prints are
# real sha256 values of the artifact NAMES, which is what the release's own list
# holds and what the site's own build is held to.
#
# Usage: bash scripts/live-checks/check69_test.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script=$here/check69.sh
work=$(cd "$(mktemp -d)" && pwd -P)
cleanup_all() {
  if [ -n "${CHECK69_TEST_KEEP:-}" ]; then
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
  *) fail "$3: expected to contain '$2'; got: $(printf '%s' "$1" | head -c 900)" ;;
  esac
}
expect_absent() { # haystack needle what
  case "$1" in
  *"$2"*) fail "$3: found '$2' where it must never appear" ;;
  esac
}
expect_status() { # status want what
  [ "$1" = "$2" ] || fail "$3: expected exit $2, got $1$(printf '%s\n' "${out:-}" | grep -E '^FACT ' | sed 's/^/ ... last run: /')"
}

# The site the fixture claims to be: the canonical origin is what the check
# fetches, and the repository and the tag are what a download URL has to carry.
CANONICAL=https://fugaro.dev
HOST=fugaro.dev
REPO=https://github.com/dimipaun/fugaro
TAG=console-v
VERSION=0.122.0
# The release's own asset names, spelled as the release spells them (a space
# between the product and the version). GitHub serves the same name with a dot
# there, and that served spelling is what a page's link carries.
ARTIFACTS=(
  "Fugaro Console-${VERSION}-universal.dmg"
  "Fugaro Console-${VERSION}-universal-mac.zip"
  "Fugaro.Console-${VERSION}-linux-x86_64.AppImage"
  "Fugaro.Console-${VERSION}-linux-amd64.deb"
)
# Every fact key the run's block must carry, whatever it measured.
BLOCK_KEYS=(site canonical belong site-facts https certificate pages build
  deployed-hash player downloads checksums links markup-origins restore)

sha_of() { # sha_of TEXT — the digest the release's own list would record
  printf '%s' "$1" | sha256sum | awk '{print $1}'
}
served_of() { # served_of NAME — the name a link carries: the space becomes a dot
  printf '%s' "$1" | tr ' ' '.'
}
url_of() { # url_of NAME — the download URL a page carries for that asset
  printf '%s/releases/download/%s%s/%s' "$REPO" "$TAG" "$VERSION" "$(served_of "$1")"
}

# --------------------------------------------------------------------- fakes
write_fakes() { # dir
  mkdir -p "$1/bin"
  cat >"$1/bin/curl" <<'CURL_EOF'
#!/usr/bin/env python3
# A fake curl for check69.sh's test: it answers out of $FAKE69_DIR and reaches
# nothing. Every call is appended to $FAKE69_DIR/calls, argv verbatim, so a test
# can tell a run that fetched the four pages from one that fetched none.
import os
import sys

state = os.environ["FAKE69_DIR"]
mode = os.environ.get("FAKE69_MODE", "ok")
args = sys.argv[1:]
with open(os.path.join(state, "calls"), "a", encoding="utf-8") as fh:
    fh.write("curl " + " ".join(args) + "\n")


def flag(name):
    return args[args.index(name) + 1] if name in args else None


url = args[-1]
fmt = flag("-w") or ""
out = flag("-o")
head = "-I" in args


def status_for(target):
    # $FAKE69_DIR/status holds '<substring> <code>' lines, first match wins, so a
    # test says which URL answers what without spelling every other one.
    path = os.path.join(state, "status")
    if os.path.exists(path):
        with open(path, encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                key, code = line.split(None, 1)
                if key in target:
                    return int(code)
    return 200


if "%{scheme}" in fmt:
    if mode == "origin-dead":
        sys.stderr.write("curl: (6) Could not resolve host: %s\n" % url)
        sys.exit(6)
    if mode == "origin-plain-http":
        sys.stdout.write("http 301 203.0.113.7 0 %s\n" % url)
        sys.exit(0)
    if mode == "origin-bad-chain":
        sys.stderr.write(
            "curl: (60) SSL certificate problem: unable to get local issuer certificate\n"
        )
        sys.exit(60)
    sys.stdout.write(
        "https 200 203.0.113.7 %s %s\n"
        % ("1" if mode == "origin-unverified" else "0", url)
    )
    sys.exit(0)

code = status_for(url)

if head and out == "/dev/null":
    # A download URL redirects to the release's own storage, which is a pass:
    # the check accepts a 200 after following it and refuses a 404, and the
    # effective URL it prints is the one it landed on.
    effective = url
    if code in (301, 302, 307, 308):
        effective = "https://objects.githubusercontent.com/fake/" + url.rsplit("/", 1)[-1]
    sys.stdout.write("%d %s\n" % (code, effective))
    sys.exit(0)

if head:
    # Step 1's `curl -sSI "$C"`: the response's own first lines.
    sys.stdout.write(
        "HTTP/2 200 \r\nserver: Google Frontend\r\netag: \"a1b2c3\"\r\n"
        "last-modified: Thu, 13 Nov 2026 10:00:00 GMT\r\ncache-control: max-age=3600\r\n"
    )
    sys.exit(0)

body = ""
path = url.split("://", 1)[-1]
path = path[path.index("/"):] if "/" in path else "/"
name = {"/": "home", "/console/": "console", "/faq": "faq", "/docs/": "docs"}.get(path)
if name:
    page = os.path.join(state, "pages", name + ".html")
    if os.path.exists(page):
        with open(page, encoding="utf-8") as fh:
            body = fh.read()
if out:
    with open(out, "w", encoding="utf-8") as fh:
        fh.write(body)
sys.stdout.write("%d %s\n" % (code, url))
CURL_EOF

  # The certificate's two readings: the pipeline hands s_client's output to
  # x509, and the fake's x509 prints what the test wrote for the subject and the
  # names. A certificate for another host is one of 69a's own failures, so both
  # files are writable per case.
  cat >"$1/bin/openssl" <<'OPENSSL_EOF'
#!/usr/bin/env bash
printf 'openssl %s\n' "$*" >>"$FAKE69_DIR/calls"
case "$*" in
*s_client*)
  cat >/dev/null
  printf 'CONNECTED(00000003)\n'
  exit 0
  ;;
*-ext*)
  cat "$FAKE69_DIR/cert.san" 2>/dev/null || exit 1
  ;;
*)
  cat "$FAKE69_DIR/cert.subject" 2>/dev/null || exit 1
  ;;
esac
OPENSSL_EOF

  # 69a's step 2 is `cd website && npm run build`, and the artifact it leaves is
  # what the deployed page has to BE: the fake copies the test's own bytes into
  # the tree, so the hash comparison is a real one over real files.
  cat >"$1/bin/npm" <<'NPM_EOF'
#!/usr/bin/env bash
printf 'npm %s\n' "$*" >>"$FAKE69_DIR/calls"
case "${FAKE69_MODE:-ok}" in
build-fails)
  echo 'npm ERR! the build failed' >&2
  exit 1
  ;;
build-writes-nothing)
  exit 0
  ;;
esac
if [ "${1:-}" = run ] && [ "${2:-}" = build ]; then
  mkdir -p "$PWD/dist"
  cp "$FAKE69_DIR/build/index.html" "$PWD/dist/index.html"
fi
exit 0
NPM_EOF

  # Three tools this check must never reach, whatever the case: any call is
  # recorded and fails the fake, and the tests assert the record is empty. The
  # live check names no cloud project, runs no `fugaro` command and never runs
  # anything named after the refusal.
  cat >"$1/bin/gcloud" <<'GCLOUD_EOF'
#!/usr/bin/env bash
printf 'gcloud %s\n' "$*" >>"$FAKE69_DIR/gcloud.calls"
echo 'fake gcloud: check69 must not reach a cloud tool' >&2
exit 64
GCLOUD_EOF
  cat >"$1/bin/fugaro" <<'FUGARO_EOF'
#!/usr/bin/env bash
printf 'fugaro %s\n' "$*" >>"$FAKE69_DIR/fugaro.calls"
echo 'fake fugaro: check69 must not reach an installation' >&2
exit 64
FUGARO_EOF
  cat >"$1/bin/belong" <<'BELONG_EOF'
#!/usr/bin/env bash
printf 'belong %s\n' "$*" >>"$FAKE69_DIR/belong.calls"
echo 'fake belong: check69 must never run a command named after the refusal' >&2
exit 64
BELONG_EOF

  chmod +x "$1/bin/curl" "$1/bin/openssl" "$1/bin/npm" "$1/bin/gcloud" "$1/bin/fugaro" "$1/bin/belong"
}

# ------------------------------------------------------------------ the fixture
site_yaml() { # dir CANONICAL REPOSITORY DOWNLOADS_FLAG VIDEO_FLAG
  cat >"$1/checkout/website/data/site.yaml" <<YAML
canonical_url: $2
repository: $3

analytics:
  enabled: true
  measurement_id: "G-EXAMPLE123"

features:
  console_downloads: $4
  video: $5

min_runs: 20

allowed_hosts:
  - github.com
  - raw.githubusercontent.com
  - firebase.google.com
  - calendly.com
  - wa.me
  - $HOST
  - keys.$HOST
YAML
}

downloads_json() { # dir AVAILABLE
  cat >"$1/checkout/website/content/downloads.json" <<JSON
{
  "version": 1,
  "available": $2,
  "release": {
    "tag_prefix": "$TAG",
    "version": "$VERSION",
    "source": "docs/release.md"
  },
  "downloads": [
    {
      "id": "macos-dmg",
      "platform": "macOS",
      "file": "Fugaro Console-{version}-universal.dmg",
      "sha256": "$(sha_of "${ARTIFACTS[0]}")"
    }
  ]
}
JSON
}

sums_file() { # dir
  local dir=$1 name
  : >"$dir/checkout/website/data/console-sha256sums.txt"
  for name in "${ARTIFACTS[@]}"; do
    printf '%s  %s\n' "$(sha_of "$name")" "$name" >>"$dir/checkout/website/data/console-sha256sums.txt"
  done
  # A release also publishes files that are not versioned artifacts — the update
  # feed beside the installers. It is a line of the list and no page offers it.
  printf '%s  latest-mac.yml\n' "$(sha_of 'latest-mac.yml')" >>"$dir/checkout/website/data/console-sha256sums.txt"
}

# download_markup NAME — one `<a>` with the checksum the page prints beside it
download_markup() { # NAME
  printf '<a class="download-link" href="%s" rel="noopener">%s</a> <code class="download-sha256">%s</code>\n' \
    "$(url_of "$1")" "$(served_of "$1")" "$(sha_of "$1")"
}

home_html() { # dir — the home page, which the build must produce byte for byte
  {
    printf '<!doctype html>\n<html lang="en">\n<head>\n<meta charset="utf-8">\n'
    printf '<title>Fugaro</title>\n<link rel="canonical" href="%s/">\n' "$CANONICAL"
    printf '<link rel="stylesheet" href="/_astro/base.css">\n</head>\n<body>\n'
    printf '<a href="/how-it-works/">How it works</a>\n<a href="/console/">Console</a>\n'
    printf '<a href="/faq/">FAQ</a>\n<a href="/docs/">Docs</a>\n'
    printf '<section class="downloads">\n'
    download_markup "${ARTIFACTS[0]}"
    download_markup "${ARTIFACTS[1]}"
    printf '</section>\n</body>\n</html>\n'
  } >"$1/state/pages/home.html"
  cp "$1/state/pages/home.html" "$1/state/build/index.html"
}

console_html() { # dir
  {
    printf '<!doctype html>\n<html lang="en">\n<head><title>Console</title>\n'
    printf '<link rel="canonical" href="%s/console/">\n</head>\n<body>\n' "$CANONICAL"
    printf '<section class="downloads">\n'
    download_markup "${ARTIFACTS[2]}"
    download_markup "${ARTIFACTS[3]}"
    printf '</section>\n</body>\n</html>\n'
  } >"$1/state/pages/console.html"
}

plain_page() { # dir NAME BODY-MARKUP
  {
    printf '<!doctype html>\n<html lang="en">\n<head><title>%s</title>\n' "$2"
    printf '<link rel="canonical" href="%s/%s">\n</head>\n<body>\n%s\n</body>\n</html>\n' \
      "$CANONICAL" "$2" "$3"
  } >"$1/state/pages/$2.html"
}

# tweak_home dir EXPR — a sed program over the home page, applied to the page
# the site SERVES and to the bytes the build produces in one call. A test that
# changed only one of them would move two facts at once, and the deployed hash
# is the fact that says the two are the same file.
tweak_home() { # dir SED-PROGRAM
  sed -i "$2" "$1/state/pages/home.html"
  cp "$1/state/pages/home.html" "$1/state/build/index.html"
}

write_site() { # dir [CANONICAL] [REPOSITORY] [DOWNLOADS] [VIDEO] [AVAILABLE]
  local dir=$1
  mkdir -p "$dir/checkout/website/data" "$dir/checkout/website/content" \
    "$dir/checkout/website/node_modules" "$dir/state/pages" "$dir/state/build"
  # `${2-A}` and not `${2:-A}`: a case whose whole point is an EMPTY
  # canonical_url has to be able to pass one, and the colon form would quietly
  # put the fixture's own origin back.
  site_yaml "$dir" "${2-$CANONICAL}" "${3-$REPO}" "${4-true}" "${5-false}"
  downloads_json "$dir" "${6-true}"
  sums_file "$dir"
  home_html "$dir"
  console_html "$dir"
  plain_page "$dir" faq "Frequently asked questions"
  plain_page "$dir" docs "Documentation"
}

# setup_case NAME [ARGS TO write_site] — a case's own directory, fakes, state and
# fixture checkout.
setup_case() { # NAME [write_site ARGS...]
  local name=$1
  shift
  dir="$work/$name"
  mkdir -p "$dir/bin" "$dir/state" "$dir/tmp" "$dir/home"
  write_fakes "$dir"
  : >"$dir/state/calls"
  : >"$dir/state/gcloud.calls"
  : >"$dir/state/fugaro.calls"
  : >"$dir/state/belong.calls"
  : >"$dir/state/status"
  write_site "$dir" "$@"
  printf 'CN=%s\nsubject=CN = %s\nissuer=C = US, O = Lets Encrypt\nnotBefore=Nov 1 00:00:00 2026 GMT\nnotAfter=Feb 1 00:00:00 2027 GMT\n' \
    "$HOST" "$HOST" >"$dir/state/cert.subject"
  printf 'X509v3 Subject Alternative Name:\n    DNS:%s, DNS:www.%s\n' "$HOST" "$HOST" >"$dir/state/cert.san"
}

# run69 [ARGS...] — runs the check on the case at hand, from the case's
# checkout. Sets $status and $out. RUN69_MODE is which answer the fakes give
# (`ok` by default, so a case that says nothing reads the happy path), and
# RUN69_DIR runs the check from somewhere else. Both are cleared after each run:
# a mode that outlived its case would make the next case read another answer.
RUN69_MODE=""
RUN69_DIR=""
RUN69_TMP=""
run69_exec() { # [ARGS...]
  cd "${RUN69_DIR:-$dir/checkout}" || exit 1
  exec env TMPDIR="${RUN69_TMP:-$dir/tmp}" FAKE69_DIR="$dir/state" \
    FAKE69_MODE="${RUN69_MODE:-ok}" HOME="$dir/home" \
    PATH="$dir/bin:$PATH" bash "$script" "$@"
}
run69() {
  status=0
  (run69_exec "$@") >"$dir/out" 2>"$dir/err" || status=$?
  RUN69_MODE=""
  RUN69_DIR=""
  RUN69_TMP=""
  out=$(cat "$dir/out")
  err=$(cat "$dir/err")
}

# ---------------------------------------------------------------- the assertions
expect_block_keys() { # what
  local key
  expect_contains "$out" "CHECK 69 RESULT" "$1: the block"
  for key in "${BLOCK_KEYS[@]}"; do
    expect_contains "$out" "FACT $key:" "$1: the $key row"
  done
}

expect_fact() { # KEY VERDICT what
  expect_contains "$out" "FACT $1: $2 — " "$3"
}

expect_fetched_nothing() { # what
  [ ! -s "$dir/state/calls" ] \
    || fail "$1: a request was made where none may be: $(tr '\n' ';' <"$dir/state/calls")"
}

expect_no_cloud_tool() { # what
  [ ! -s "$dir/state/gcloud.calls" ] || fail "$1: gcloud was called: $(tr '\n' ';' <"$dir/state/gcloud.calls")"
  [ ! -s "$dir/state/fugaro.calls" ] || fail "$1: fugaro was called: $(tr '\n' ';' <"$dir/state/fugaro.calls")"
  [ ! -s "$dir/state/belong.calls" ] || fail "$1: a command named belong was run: $(tr '\n' ';' <"$dir/state/belong.calls")"
}

expect_called() { # NEEDLE what
  grep -qF -- "$1" "$dir/state/calls" || fail "$2: no call matched '$1': $(tr '\n' ';' <"$dir/state/calls")"
}

expect_not_called() { # NEEDLE what
  grep -qF -- "$1" "$dir/state/calls" && fail "$2: a call matched '$1' where none may: $(tr '\n' ';' <"$dir/state/calls")"
  return 0
}

scratch_of() { # dir — the scratch directory the run announced
  sed -n 's/^.*scratch directory: \([^ ]*\).*/\1/p' "$1/err" | head -n 1
}

expect_scratch_gone() { # what
  local scratch
  scratch=$(scratch_of "$dir")
  if [ -z "$scratch" ]; then
    fail "$1: the script did not say where its scratch directory is"
    return 0
  fi
  [ -d "$scratch" ] && fail "$1: the scratch directory $scratch survived the run"
  [ -z "$(find "$dir/tmp" -mindepth 1 2>/dev/null)" ] || fail "$1: files were left in TMPDIR: $(find "$dir/tmp" -mindepth 1)"
  return 0
}

# ------------------------------------------------------------------- the tests
test_happy_path() {
  setup_case happy
  run69
  expect_status "$status" 0 "happy"
  expect_contains "$out" "CHECK 69 RESULT: PASS" "happy: the verdict"
  expect_block_keys "happy"
  local key
  for key in site canonical belong site-facts https certificate pages build \
    deployed-hash player downloads checksums links markup-origins restore; do
    expect_fact "$key" PASS "happy: the $key row is a PASS"
  done
  expect_contains "$out" "features.console_downloads=true, features.video=false" "happy: the flags it read out of the tree"
  expect_contains "$out" "the release ${TAG}${VERSION}, 4 artifact(s)" "happy: the release and its assets, from downloads.json and the release's own list"
  expect_contains "$out" "the live home page IS the artifact this commit's build produced" "happy: the deployed hash is the build's"
  expect_contains "$out" "every artifact the release's own list names is offered" "happy: all four artifacts are offered somewhere"
  expect_contains "$out" "all 4 checksum(s) printed beside a download link" "happy: every checksum is the release's own"
  # The home page carries four nav links plus its own stylesheet: five paths the
  # deployed page offers a reader, and every one of them has to resolve.
  expect_contains "$out" "every one of the 5 distinct same-origin link(s)" "happy: the home page's links resolve"
  # 69b's step 4 is the three pages of its own grep — /, /faq and /docs/ — and
  # the fixture's markup carries five absolute URLs across them: the home page's
  # canonical link and its two macOS downloads, and one canonical link on each of
  # the other two. The console page is NOT among them, which is the step's own
  # list and not an omission here: 69a's step 3 is what covers that page.
  expect_contains "$out" "every one of the 5 absolute URL(s)" "happy: 69b's markup scan over the three pages"
  expect_contains "$out" "read by hand rather than with shasum -c" "happy: why the release's own list is read by hand"
  # The four steps a browser takes are printed verbatim and never taken.
  expect_contains "$out" "MANUAL: open $CANONICAL/ and $CANONICAL/console/ in a browser" "happy: 69a's step 3 prompt"
  expect_contains "$out" "MANUAL: open $CANONICAL/ , $CANONICAL/faq and $CANONICAL/docs/ with the Network panel open" "happy: 69b's steps 1 and 2 prompt"
  expect_contains "$out" "MANUAL: still on /privacy/" "happy: 69b's step 3 prompt"
  expect_contains "$out" "Check 69c (the published ranges are real) is Task W2.3's" "happy: 69c is named as not scripted"
  # The manual lines are the ONLY thing this script does not measure, and it
  # says which steps they are.
  [ "$(grep -c '^MANUAL: ' "$dir/out")" = 4 ] || fail "happy: expected 4 MANUAL lines, saw $(grep -c '^MANUAL: ' "$dir/out")"
  expect_called "$CANONICAL" "happy: the canonical origin was probed"
  expect_called "$(url_of "${ARTIFACTS[0]}")" "happy: a macOS asset URL was probed"
  expect_called "$(url_of "${ARTIFACTS[3]}")" "happy: and so was a Linux one, off the console page"
  expect_called "npm run build" "happy: the site's own build ran"
  expect_called "$CANONICAL/faq" "happy: the FAQ was fetched for 69b's markup scan"
  expect_no_cloud_tool "happy"
  expect_scratch_gone "happy"
}

test_belong_is_refused() {
  setup_case belong "$CANONICAL" "https://github.com/dimipaun/belong.git"
  run69
  expect_status "$status" 2 "belong"
  expect_fact belong FAIL "belong: the refusal is a FAILed fact"
  expect_contains "$out" "Check 69 never runs against 'belong'" "belong: the stop"
  expect_contains "$out" "CHECK 69 RESULT: FAIL" "belong: the verdict"
  expect_fetched_nothing "belong: the refusal lands before any request"
  expect_block_keys "belong"
  expect_scratch_gone "belong"
}

test_the_canonical_host_is_refused_too() {
  setup_case belong-host "$(printf 'https://belong.%s' "$HOST")"
  run69
  expect_status "$status" 2 "belong-host"
  expect_fact belong FAIL "belong-host: a canonical host naming the installation is refused"
  expect_fetched_nothing "belong-host"
}

test_dry_run_runs_nothing() {
  setup_case dry
  run69 --dry-run
  expect_status "$status" 0 "dry-run"
  expect_fetched_nothing "dry-run: guards and the plan only"
  [ -s "$dir/state/calls" ] && fail "dry-run: a tool was run at all"
  expect_contains "$err" "[network] curl -sS -L -o /dev/null -w '%{scheme}" "dry-run: the origin probe in the plan"
  expect_contains "$err" "echo | openssl s_client -connect $HOST:443" "dry-run: the certificate read in the plan"
  expect_contains "$err" "[network, reads] curl -sS -L -H 'Accept-Encoding: identity'" "dry-run: the four page fetches in the plan"
  expect_contains "$err" "[local, writes] cd website && npm run build" "dry-run: the build in the plan"
  expect_contains "$err" "and its Restore (also from the exit trap) would remove: the scratch directory" "dry-run: the Restore in the plan"
  expect_contains "$out" "MANUAL: open $CANONICAL/ and $CANONICAL/console/" "dry-run: the manual prompts are still printed"
  expect_contains "$out" "CHECK 69 RESULT: NOT RUN (--dry-run" "dry-run: no verdict"
  expect_absent "$out" "CHECK 69 RESULT: PASS" "dry-run: not a pass"
  expect_absent "$out" "CHECK 69 RESULT: FAIL" "dry-run: not a fail"
  expect_contains "$out" "FACT dry-run: PASS" "dry-run: the row that says what it did"
  expect_absent "$out" "not reached" "dry-run: no row reads not reached"
  expect_no_cloud_tool "dry-run"
  expect_scratch_gone "dry-run"
}

test_a_tree_without_the_site_stops() {
  setup_case nosync
  rm -rf "$dir/checkout/website"
  run69
  expect_status "$status" 2 "no site"
  expect_contains "$out" "there is no website directory in" "no site: the stop says where"
  expect_contains "$out" "bash scripts/live-checks/check69.sh" "no site: the stop says how to run it"
  expect_fetched_nothing "no site"
  expect_block_keys "no site"
  expect_scratch_gone "no site"
}

test_a_missing_fact_file_stops() {
  setup_case noconsole
  rm -f "$dir/checkout/website/data/console-sha256sums.txt"
  run69
  expect_status "$status" 2 "no sums"
  expect_contains "$out" "data/console-sha256sums.txt is not in this tree" "no sums: the stop names the file"
  expect_fetched_nothing "no sums"
}

test_a_canonical_url_that_is_not_https_stops() {
  setup_case plain-http "$(printf 'http://%s' "$HOST")"
  run69
  expect_status "$status" 2 "plain http"
  expect_contains "$out" "which is not an https origin" "plain http: the stop"
  expect_fetched_nothing "plain http"
}

test_an_empty_canonical_url_stops() {
  setup_case empty-canonical ""
  run69
  expect_status "$status" 2 "empty canonical"
  expect_contains "$out" "an empty value is a stop rather than a host to invent" "empty canonical: the stop"
  expect_fetched_nothing "empty canonical"
}

test_an_origin_that_does_not_answer() {
  setup_case origin-dead
  RUN69_MODE=origin-dead
  run69
  expect_status "$status" 1 "origin dead"
  expect_fact https FAIL "origin dead: the row"
  expect_contains "$out" "Could not resolve host" "origin dead: curl's own line is carried"
  expect_contains "$out" "your registrar and hosting step" "origin dead: where the fix belongs"
  expect_contains "$out" "CHECK 69 RESULT: FAIL" "origin dead: the verdict"
  expect_contains "$out" "FACT pages: FAIL — not reached" "origin dead: a row below reads not reached"
  expect_contains "$out" "FACT certificate: FAIL — not reached" "origin dead: the certificate is a row below it"
  expect_not_called "console.html" "origin dead: no page was fetched after the origin failed"
  expect_no_cloud_tool "origin dead"
  expect_scratch_gone "origin dead"
}

test_an_origin_on_plain_http() {
  setup_case origin-plain-http
  RUN69_MODE=origin-plain-http
  run69
  expect_status "$status" 1 "plain http origin"
  expect_fact https FAIL "plain http origin: the row carries what was read"
  expect_contains "$out" "http 301" "plain http origin: the reading"
  expect_contains "$out" "The reading that passes is https with status 200 and ssl_verify_result 0" "plain http origin: the rule is stated"
}

test_a_certificate_for_another_host() {
  setup_case cert-other
  printf 'subject=CN = other.example\nissuer=C = US, O = Lets Encrypt\n' >"$dir/state/cert.subject"
  printf 'X509v3 Subject Alternative Name:\n    DNS:other.example\n' >"$dir/state/cert.san"
  run69
  expect_status "$status" 1 "cert-other"
  expect_fact certificate FAIL "cert-other: the row"
  expect_contains "$out" "the certificate does not name $HOST" "cert-other: the reading"
  expect_contains "$out" "the domain-attachment step rather than a site bug" "cert-other: where the fix belongs"
  expect_fact deployed-hash PASS "cert-other: the run carries on — a bad certificate does not stop the page facts"
}

test_a_certificate_named_only_in_the_san() {
  setup_case cert-san
  printf 'subject=CN = fugaro.dev.example.net\nissuer=C = US, O = Lets Encrypt\n' >"$dir/state/cert.subject"
  printf 'X509v3 Subject Alternative Name:\n    DNS:%s\n' "$HOST" >"$dir/state/cert.san"
  run69
  expect_status "$status" 0 "cert-san"
  expect_fact certificate PASS "cert-san: the names a certificate carries are read too"
}

test_a_deployed_page_that_is_not_this_build() {
  setup_case stale
  # Only the artifact the build produces moves: the deployed page is the one
  # that was there before, which is 69a's whole point.
  printf '\n<!-- a deploy that never happened -->\n' >>"$dir/state/build/index.html"
  run69
  expect_status "$status" 1 "stale"
  expect_fact deployed-hash FAIL "stale: the row"
  expect_contains "$out" "a deploy of an older commit" "stale: 69a's own clause"
  expect_fact build PASS "stale: the build itself held"
  expect_fact player PASS "stale: the rest of the run still happened"
}

test_a_player_on_the_deployed_page() {
  setup_case player
  tweak_home "$dir" 's#</body>#<video src="/explainer.mp4"></video></body>#'
  run69
  expect_status "$status" 1 "player"
  expect_fact player FAIL "player: the row"
  expect_contains "$out" "carries a player for a video that does not exist" "player: the reading"
  expect_contains "$out" "<video src=\"/explainer.mp4\"></video>" "player: the hit is carried, so a word in a sentence can be told from a player"
  expect_contains "$out" "the home page: " "player: and the page it came from"
  expect_fact deployed-hash PASS "player: the hash is unaffected — the page is still the build's"
}

test_a_player_on_the_console_page() {
  # Step 3 greps the console page too: the video the site does not have is a
  # hero on the home page today, and a page that grows one later is a hit here
  # whichever page it is.
  setup_case player-console
  sed -i 's#</body>#<iframe src="https://www.youtube.com/embed/x"></iframe></body>#' \
    "$dir/state/pages/console.html"
  run69
  expect_status "$status" 1 "player on the console page"
  expect_fact player FAIL "player on the console page: the row"
  expect_contains "$out" "the console page: " "player on the console page: the page is named"
}

test_a_download_that_does_not_resolve() {
  setup_case dead-download
  printf '%s 404\n' "$(url_of "${ARTIFACTS[2]}")" >"$dir/state/status"
  run69
  expect_status "$status" 1 "dead download"
  expect_fact downloads FAIL "dead download: the row"
  expect_contains "$out" "answered HTTP 404" "dead download: the reading"
  expect_contains "$out" "a redirect to the release's own storage is fine and a 404 is not" "dead download: the rule"
  expect_fact checksums PASS "dead download: the checksums are a separate row and they held"
}

test_a_download_the_release_does_not_hold() {
  setup_case unknown-download
  # A page offering an artifact the release's own list does not name: the drift
  # the site's own tests cannot see, because they read the same list.
  tweak_home "$dir" "s#$(served_of "${ARTIFACTS[1]}")#Fugaro.Console-${VERSION}-universal.pkg#"
  run69
  expect_status "$status" 1 "unknown download"
  expect_fact downloads FAIL "unknown download: the row"
  expect_contains "$out" "which no line of" "unknown download: the reading"
  expect_contains "$out" "the release's own spelling nor the one GitHub serves" "unknown download: both spellings were tried"
  expect_contains "$out" "$(served_of "${ARTIFACTS[1]}") is an artifact of ${TAG}${VERSION} and no page" "unknown download: and the artifact no page offers any more"
}

test_a_release_name_that_is_offered_with_an_encoded_space() {
  setup_case encoded-space
  # The release's own spelling, percent-encoded: the URL a hand-written link
  # would carry, and a 404 on every reader's machine.
  tweak_home "$dir" "s#$(served_of "${ARTIFACTS[0]}")#$(printf '%s' "${ARTIFACTS[0]}" | sed 's/ /%20/g')#"
  run69
  expect_status "$status" 1 "encoded space"
  expect_fact downloads FAIL "encoded space: the row"
  expect_contains "$out" "carries an encoded space" "encoded space: the reading"
}

test_a_page_checksum_that_is_not_the_releases() {
  setup_case bad-checksum
  tweak_home "$dir" "s#$(sha_of "${ARTIFACTS[0]}")#$(printf '%064d' 0)#"
  run69
  expect_status "$status" 1 "bad checksum"
  expect_fact checksums FAIL "bad checksum: the row"
  expect_contains "$out" "$(printf '%064d' 0) and" "bad checksum: both values are printed"
  expect_contains "$out" "is not the release's own" "bad checksum: why it is a failure"
  expect_fact downloads PASS "bad checksum: the URLs themselves resolved"
}

test_a_link_that_does_not_resolve() {
  setup_case dead-link
  tweak_home "$dir" 's#<a href="/docs/">Docs</a>#<a href="/caps-and-costs/">Costs</a>#'
  printf '/caps-and-costs/ 404\n' >"$dir/state/status"
  run69
  expect_status "$status" 1 "dead link"
  expect_fact links FAIL "dead link: the row"
  expect_contains "$out" "/caps-and-costs/ (HTTP 404)" "dead link: the path and the status"
  expect_contains "$out" "Step 4 of Check 69a" "dead link: which step it is"
}

test_a_markup_origin_off_the_list() {
  setup_case off-list
  tweak_home "$dir" 's#</body>#<script src="https://cdn.example.net/tag.js"></script></body>#'
  run69
  expect_status "$status" 1 "off list"
  expect_fact markup-origins FAIL "off list: the row"
  expect_contains "$out" "/ carries an absolute URL on cdn.example.net" "off list: the page and the host"
  expect_contains "$out" "the fix is never to add the host to allowed_hosts" "off list: what the list is for"
}

test_the_analytics_origin_in_the_markup_is_off_the_list() {
  setup_case analytics-tag
  # The consent gate the site's own file implements is what makes an analytics
  # request legitimate; a tag in the markup is what 69b's step 4 refuses, and
  # the origin is not on allowed_hosts for exactly that reason.
  tweak_home "$dir" 's#</body>#<script src="https://www.googletagmanager.com/gtag/js"></script></body>#'
  run69
  expect_status "$status" 1 "analytics tag"
  expect_fact markup-origins FAIL "analytics tag: the row"
  expect_contains "$out" "www.googletagmanager.com" "analytics tag: the host"
}

test_a_page_that_does_not_answer() {
  setup_case dead-page
  printf '/faq 404\n' >"$dir/state/status"
  run69
  expect_status "$status" 1 "dead page"
  expect_fact pages FAIL "dead page: the row"
  expect_contains "$out" "/faq (HTTP 404)" "dead page: the page and its status"
  expect_contains "$out" "a grep over an empty body or another site's page would pass for the wrong reason" "dead page: why it stops"
  expect_fact player FAIL "dead page: the rows below read not reached"
  expect_contains "$out" "FACT player: FAIL — not reached" "dead page: the row below it"
  expect_fact https PASS "dead page: the origin did answer"
}

test_a_build_that_fails() {
  setup_case build-fails
  RUN69_MODE=build-fails
  run69
  expect_status "$status" 1 "build fails"
  expect_fact build FAIL "build fails: the row"
  expect_contains "$out" "npm run build' failed" "build fails: the reading"
  expect_contains "$out" "the deployed page has nothing to be compared against" "build fails: why"
  expect_fact deployed-hash FAIL "build fails: the row below reads not reached"
  expect_fact player FAIL "build fails: and so does this one"
}

test_a_tree_without_the_dependencies() {
  setup_case no-modules
  rm -rf "$dir/checkout/website/node_modules"
  run69
  expect_status "$status" 1 "no modules"
  expect_fact build FAIL "no modules: the row"
  expect_contains "$out" "npm ci" "no modules: the stop names the command that fixes it"
}

test_a_build_that_writes_nothing() {
  setup_case writes-nothing
  RUN69_MODE=build-writes-nothing
  run69
  expect_status "$status" 1 "writes nothing"
  expect_fact build FAIL "writes nothing: the row"
  expect_contains "$out" "wrote no website/dist/index.html" "writes nothing: the reading"
}

test_the_unreleased_state() {
  # The other state the flag can be in: no console is offered, so no page may
  # carry a release download URL at all, and the checksum row says there is
  # nothing to pair rather than failing for the absence.
  setup_case unreleased "$CANONICAL" "$REPO" false false false
  tweak_home "$dir" '/class="download-link"/d'
  sed -i '/class="download-sha256"/d' "$dir/state/pages/console.html"
  run69
  expect_status "$status" 0 "unreleased"
  expect_contains "$out" "CHECK 69 RESULT: PASS" "unreleased: the verdict"
  expect_fact downloads PASS "unreleased: the row"
  expect_contains "$out" "features.console_downloads is false and neither the home page nor the console page carries a release download URL" "unreleased: the reading"
  expect_contains "$out" "FACT checksums: INFO" "unreleased: nothing to pair, so no verdict for that row"
}

test_the_unreleased_state_with_a_link_left_on_a_page() {
  # The home page keeps its two links while the console page loses its pair: the
  # flag says no console is offered, so ONE link is one too many.
  setup_case unreleased-dirty "$CANONICAL" "$REPO" false false false
  sed -i '/class="download-sha256"/d' "$dir/state/pages/console.html"
  run69
  expect_status "$status" 1 "unreleased with a link"
  expect_fact downloads FAIL "unreleased with a link: the row"
  expect_contains "$out" "must meet no link for a release the site's own facts say is not offered" "unreleased with a link: the rule"
  # The home page keeps its two links AND their two checksums, so the checksum
  # row is a reading of its own and it held: the flag being off does not make a
  # checksum wrong, it makes the link wrong.
  expect_fact checksums PASS "unreleased with a link: the printed checksums are still the release's own"
}

test_a_page_that_prints_no_checksum() {
  # Step 3 asks for the checksum BESIDE each link, so a link without one is the
  # failure the count is for — and the row must not read as a pass because the
  # links that DID have one were right.
  setup_case no-checksum
  sed -i 's#<code class="download-sha256">[0-9a-f]*</code>##' \
    "$dir/state/pages/home.html" "$dir/state/build/index.html"
  run69
  expect_status "$status" 1 "no checksum"
  expect_fact checksums FAIL "no checksum: the row"
  expect_contains "$out" "print 4 download link(s) and 2 checksum(s)" "no checksum: the counts"
  expect_contains "$out" "is missing from 2 of them" "no checksum: and how many"
  expect_fact downloads FAIL "no checksum: the downloads row says the same thing"
}

test_a_video_flag_that_is_on_is_not_a_verdict() {
  # W3.3 is parked: the flag is false on today's tree, and if a checkout has it
  # true the player row has no rule to apply — it says so rather than passing or
  # failing a criterion the parked task owns.
  setup_case video-flag "$CANONICAL" "$REPO" true true
  run69
  expect_status "$status" 0 "video flag"
  expect_contains "$out" "FACT player: INFO" "video flag: the row carries no verdict"
  expect_contains "$out" "W3.3 is the task that puts one there" "video flag: the parked task is named"
}

test_the_block_shape_is_fixed_on_a_stopped_run() {
  setup_case stopped
  printf '%s\n' 'this is not a sha256sums line at all' >>"$dir/checkout/website/data/console-sha256sums.txt"
  run69
  expect_status "$status" 2 "stopped"
  expect_contains "$out" "FACT aborted: FAIL" "stopped: the reason is a row"
  expect_contains "$out" "the shape sha256sum writes" "stopped: the reading"
  expect_block_keys "stopped"
  expect_fetched_nothing "stopped: the stop lands before any request"
  expect_scratch_gone "stopped"
}

test_a_scratch_directory_inside_a_work_tree_stops() {
  setup_case in-repo
  git -C "$dir/checkout" init -q
  mkdir -p "$dir/checkout/tmp"
  RUN69_TMP="$dir/checkout/tmp"
  run69
  expect_status "$status" 2 "in repo"
  expect_contains "$out" "is inside a git work tree" "in repo: the stop"
  expect_fetched_nothing "in repo"
}

test_xtrace_is_refused() {
  setup_case xtrace
  status=0
  (cd "$dir/checkout" &&
    exec env TMPDIR="$dir/tmp" FAKE69_DIR="$dir/state" HOME="$dir/home" \
      PATH="$dir/bin:$PATH" bash -x "$script") >"$dir/out" 2>"$dir/err" || status=$?
  out=$(cat "$dir/out")
  expect_status "$status" 2 "xtrace"
  expect_fact xtrace FAIL "xtrace: the row"
  expect_contains "$out" "this shell is tracing (bash -x)" "xtrace: the stop, naming the flag"
  expect_contains "$out" "CHECK 69 RESULT: FAIL" "xtrace: the verdict"
  expect_fetched_nothing "xtrace"
  expect_scratch_gone "xtrace"
}

test_usage() {
  setup_case usage
  run69 --help
  expect_status "$status" 0 "help"
  expect_contains "$out" "usage: check69.sh [--dry-run]" "help: the usage line"
  expect_absent "$out" "CHECK 69 RESULT" "help: no block at all"
  expect_fetched_nothing "help"

  run69 --origin https://example.com
  expect_status "$status" 2 "unknown argument"
  expect_contains "$out" "FACT aborted: FAIL" "unknown argument: a fact, so the block is still printable"
  expect_contains "$out" "unknown argument '--origin'" "unknown argument: the argument it names"
  expect_fetched_nothing "unknown argument"
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

t test_happy_path
t test_belong_is_refused
t test_the_canonical_host_is_refused_too
t test_dry_run_runs_nothing
t test_a_tree_without_the_site_stops
t test_a_missing_fact_file_stops
t test_a_canonical_url_that_is_not_https_stops
t test_an_empty_canonical_url_stops
t test_an_origin_that_does_not_answer
t test_an_origin_on_plain_http
t test_a_certificate_for_another_host
t test_a_certificate_named_only_in_the_san
t test_a_deployed_page_that_is_not_this_build
t test_a_player_on_the_deployed_page
t test_a_player_on_the_console_page
t test_a_download_that_does_not_resolve
t test_a_download_the_release_does_not_hold
t test_a_release_name_that_is_offered_with_an_encoded_space
t test_a_page_checksum_that_is_not_the_releases
t test_a_link_that_does_not_resolve
t test_a_markup_origin_off_the_list
t test_the_analytics_origin_in_the_markup_is_off_the_list
t test_a_page_that_does_not_answer
t test_a_build_that_fails
t test_a_tree_without_the_dependencies
t test_a_build_that_writes_nothing
t test_the_unreleased_state
t test_the_unreleased_state_with_a_link_left_on_a_page
t test_a_page_that_prints_no_checksum
t test_a_video_flag_that_is_on_is_not_a_verdict
t test_the_block_shape_is_fixed_on_a_stopped_run
t test_a_scratch_directory_inside_a_work_tree_stops
t test_xtrace_is_refused
t test_usage

if [ "$failures" -ne 0 ]; then
  echo "check69_test: $failures failure(s)" >&2
  exit 1
fi
echo "check69_test: all tests passed" >&2
