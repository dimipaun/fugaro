#!/bin/sh
# byproducts.sh normalizes what a package install leaves behind that carries
# the build's own clock or scheduler rather than the tree's content, so that
# two builds of the same tree produce the same image (issue #1745; the catalog
# check compares the published base's digest, internal/imagecheck). The base
# image build bind-mounts it into the RUN that installed the packages and into
# the RUN that runs npm, and never copies it in; running it in the same RUN is
# what keeps the unnormalized bytes out of a layer, since a later layer that
# only rewrites a file would leave the first layer's digest different between
# two builds. images/base_repro_test.go builds a fixture image with this same
# script twice and compares the layer digests, so the two cannot drift apart.
#
# 1. git-lfs's postinst runs `git lfs install --skip-repo --system`, which
#    writes /etc/gitconfig through `git config`. Its four filter keys land in
#    the order git's scheduler gives them, and two runs against a fresh file
#    disagree (six local runs: the fifth wrote the section in another order),
#    which changes the file's bytes. The values are read back and the section
#    is re-written in a fixed order; a missing key fails the build, because an
#    /etc/gitconfig without them is an image whose checkouts of an LFS
#    repository silently keep the pointers instead of the content.
# 2. useradd and chage write /etc/shadow's last-change day from the wall
#    clock, so two builds differ unless the day comes from SOURCE_DATE_EPOCH,
#    which the caller (images/build-base.sh) sets to the commit's date. The
#    account tools also copy the file they are about to change to /etc/shadow-,
#    so that copy is made to match the file it backs up rather than left as
#    whatever the second write happened to see.
# 3. /var/log holds what the install logged (dpkg.log, apt's history and
#    term.log, alternatives.log), and /var/cache/ldconfig/aux-cache records the
#    libraries' mtimes. Both were named by comparing the two published images
#    of the same tree, which differ in exactly eight files: these six, the two
#    above, and the cmake help files' whitespace (a false positive of that
#    comparison's filename handling, not a difference at all). /var/cache's
#    other files -- apt's archives, debconf's database -- do not differ, so
#    they stay: debconf's is state a derived build's install reads.
# 4. `npm ci` for the harnesses leaves ~540 V8 code-cache files under
#    /tmp/node-compile-cache (2.7 MB) whose content is the Node build's own
#    cache format. Nothing in the image reads them.
set -eu

# The date is required, and 0 is not a date: `chage -d 1970-01-01` writes a
# last-change day of 0, which PAM reads as "the password must be changed", so
# `su` and every PAM account check against this image's fugaro user would fail
# (a NOPASSWD sudo is exempt, which is why nothing else noticed). A build with
# no date from its caller is a build that cannot be normalized, not one to
# date 1970.
epoch=${SOURCE_DATE_EPOCH:-}
[ -n "$epoch" ] && [ "$epoch" != 0 ] || {
  echo "byproducts: SOURCE_DATE_EPOCH is ${epoch:-empty}, and 0 dates the image 1970, whose last-change day PAM reads as a required password change; pass the last commit's date (images/build-base.sh does)" >&2
  exit 1
}

{
  printf '[filter "lfs"]\n'
  for key in clean smudge process required; do
    value=$(git config --system --get "filter.lfs.$key" || true)
    [ -n "$value" ] || { echo "byproducts: /etc/gitconfig has no filter.lfs.$key" >&2; exit 1; }
    printf '\t%s = %s\n' "$key" "$value"
  done
} > /etc/gitconfig

chage -d "$(date -u -d "@$epoch" +%Y-%m-%d)" fugaro
cp -f /etc/shadow /etc/shadow-

rm -f /var/cache/ldconfig/aux-cache
find /var/log -mindepth 1 ! -type d -delete

rm -rf /tmp/node-compile-cache
