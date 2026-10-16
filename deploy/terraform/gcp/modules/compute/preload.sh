#!/bin/bash
# The preloaded boot image's build (design compute-backend §1.2, C19; the
# procedure is Check 57a step 3b's, measured 2026-10-15). A Batch container
# runnable starts from a local image and pulls nothing, which is the whole
# point: the plain pull measured 145 s against the preload's 66 s, and the
# 120 s-above-baseline budget is met by the preload and by nothing else a
# Docker run can use.
#
# It runs as a Cloud Build step, as the preload builder account, whose custom
# role carries exactly what these gcloud calls need and nothing else: the
# instances permissions for the builder VM (create, and the setMetadata,
# setLabels and setServiceAccount its arguments are each checked against; get,
# list, getSerialPortOutput, stop, delete), compute.disks.create and
# compute.subnetworks.use for its boot disk and its interface on the named
# subnetwork, compute.images.useReadOnly for the image it boots from,
# compute.disks.useReadOnly for imaging its stopped disk, and
# compute.images.{create,setLabels,get,getFromFamily,list,delete}. No instance
# or image admin role, and no permission anywhere to create a job or a second
# VM. setMetadata is the one permission here wider than this script's use of
# it (it is also how SSH keys reach a VM) and it cannot be dropped: a VM with
# no startup script pulls nothing.
#
# What it must never do: delete a VM or an image it did not create. Every
# delete below is guarded by the fugaro=managed label AND by the name, so a
# project's own VM or image that happens to carry this name is left alone --
# and the build fails loudly instead of clearing the way.
#
# This build RUNS ON A SCHEDULE because its image goes stale two ways, and it
# must leave neither in place (design §1.2, C4, C19): the base family in
# Google's own project rolls (the pinned base_image does not move with it, and
# the image's name -- the pin's hash -- cannot say which release it holds, so
# the release is recorded in the image's base-release label at build time),
# and a Batch custom image's supported lifetime can end 60 days after its
# starting date, whatever it was built from (so age alone replaces it). An
# image that is this build's own, built from the family's current release and
# younger than REFRESH_DAYS, is the only thing this build leaves alone.
set -euo pipefail

: "${PROJECT:?}" "${ZONE:?}" "${IMAGE:?}" "${BASE_IMAGE:?}" "${BASE_PROJECT:?}" "${BASE_FAMILY:?}"
: "${REGISTRY_HOST:?}" "${RUN_ACCOUNT:?}" "${MACHINE_TYPE:?}" "${IMAGE_PREFIX:?}"
: "${NETWORK:?}" "${SUBNETWORK:?}" "${REFRESH_DAYS:?}"

BUILDER="fugaro-preload-build"
MARKER="fugaro-preload-built"

say() { printf 'fugaro-preload: %s\n' "$*"; }

# 1. The base family is GOOGLE's, not the installation's: measured 2026-10-15,
#    batch-cos resolves to the family batch-cos-stable-official in project
#    batch-custom-image, and an installation project has no such family. So
#    the image path below is full, and it names $BASE_PROJECT.
BASE_RELEASE="$(gcloud compute images describe-from-family "$BASE_FAMILY" \
  --project="$BASE_PROJECT" --format='value(name)')"
[ -n "$BASE_RELEASE" ] || { say "the family $BASE_FAMILY is not in project $BASE_PROJECT"; exit 1; }
say "base release: $BASE_RELEASE"

# What this build records on the image it makes: which release of the family
# is inside it. A label value is lower-case letters, digits, - and _, at most
# 63 of them, and a family can hand out a longer release name than that.
RELEASE_LABEL="$(printf '%s' "$BASE_RELEASE" | tr '[:upper:]' '[:lower:]' | cut -c1-63)"

# 2. The image this build owns. One image per pinned base version: the name
#    carries a hash of the pin, so a run's boot disk is the image that
#    contains the image the job pulls. API answer by API answer, from here on:
#    the release the image holds (its base-release label -- the name cannot
#    carry it, because the pin does not move when Google rolls the family) and
#    its age (the creation date a Batch image's 60-day lifetime runs from).
#    Only an image that is this build's own, current and inside the window is
#    left alone; a family roll, an image that predates this label, and an
#    image past the window all mean this build replaces it.
BUILD=yes
REPLACE=""
if gcloud compute images describe "$IMAGE" --project="$PROJECT" >/dev/null 2>&1; then
  # csv[no-heading] rather than value(): a value() row leaves an absent field
  # out, and the three answers here have to stay in their columns -- an image
  # with no base-release label is a fact this build acts on, not a shift.
  read -r IMG_OWNER IMG_RELEASE IMG_CREATED < <(gcloud compute images describe "$IMAGE" \
    --project="$PROJECT" --format='csv[no-heading](labels.fugaro,labels.base-release,creationTimestamp)')
  # An image under this name that this build did not make is left alone and
  # said so: the delete below is the one this script makes outside its own
  # prefix sweep, and it needs the label to be sure.
  [ "$IMG_OWNER" = "managed" ] || { say "the image $IMAGE exists and is not fugaro=managed; refusing to touch it"; exit 1; }
  # A stamp the parser cannot read is read as 0, i.e. as old as possible: an
  # image whose age is unknown is replaced rather than kept.
  IMG_EPOCH="$(date -u -d "$IMG_CREATED" +%s 2>/dev/null || echo 0)"
  IMG_AGE_DAYS="$(( ( $(date -u +%s) - IMG_EPOCH ) / 86400 ))"
  if [ "$IMG_RELEASE" = "$RELEASE_LABEL" ] && [ "$IMG_AGE_DAYS" -lt "$REFRESH_DAYS" ]; then
    say "image $IMAGE was built from $BASE_RELEASE and is $IMG_AGE_DAYS days old (the window is $REFRESH_DAYS); nothing to build"
    BUILD=""
  else
    REPLACE=yes
    say "image $IMAGE is stale (built from ${IMG_RELEASE:-a release this build never recorded}, $IMG_AGE_DAYS days old) and $BASE_FAMILY is now $BASE_RELEASE; this build replaces it"
  fi
fi

if [ -n "$BUILD" ]; then
  # 3. A leftover builder from an interrupted build. It is deleted only when
  #    it is ours by BOTH name and label.
  if gcloud compute instances describe "$BUILDER" --project="$PROJECT" --zone="$ZONE" >/dev/null 2>&1; then
    LABELS="$(gcloud compute instances describe "$BUILDER" --project="$PROJECT" --zone="$ZONE" \
      --format='value(labels.fugaro)')"
    [ "$LABELS" = "managed" ] || { say "$BUILDER exists and is not fugaro=managed; refusing to touch it"; exit 1; }
    say "deleting the leftover builder VM $BUILDER"
    gcloud compute instances delete "$BUILDER" --project="$PROJECT" --zone="$ZONE" --quiet
  fi

  # 4. The builder. It boots Google's COS image and pulls the pinned base
  #    image into its Docker data root from its startup script -- the same
  #    procedure 57a-3b used, and the only one measured to work: no package
  #    manager is involved. It has no external IP, so its startup script's
  #    last line is read from the SERIAL PORT below, and its egress is the
  #    module's NAT -- which is on ONE network and ONE subnetwork, the ones
  #    the job's VMs are on. Both are named explicitly for that reason: a
  #    builder left on whatever the project's default placement is would pull
  #    through an egress path the runs do not use, and --no-address with no
  #    NAT behind it pulls nothing at all.
  say "creating the builder VM $BUILDER from projects/$BASE_PROJECT/global/images/$BASE_RELEASE"
  gcloud compute instances create "$BUILDER" --project="$PROJECT" --zone="$ZONE" \
    --image="projects/$BASE_PROJECT/global/images/$BASE_RELEASE" \
    --machine-type="$MACHINE_TYPE" --no-address \
    --network="$NETWORK" --subnet="$SUBNETWORK" \
    --service-account="$RUN_ACCOUNT" --scopes=cloud-platform \
    --labels=fugaro=managed \
    --metadata=startup-script="$(printf '%s\n' \
        '#!/bin/bash' 'set -e' \
        "docker-credential-gcr configure-docker --registries=${REGISTRY_HOST}" \
        "docker pull '${BASE_IMAGE}'" \
        "docker image inspect '${BASE_IMAGE}' >/dev/null" \
        "echo ${MARKER}")"

  # 5. The pull is what this waits for. Bounded: a pull that fails never
  #    prints the marker, and this loop has to end anyway.
  BUILT=""
  for _ in $(seq 1 60); do
    if gcloud compute instances get-serial-port-output "$BUILDER" --project="$PROJECT" \
        --zone="$ZONE" --port=1 2>/dev/null | grep -q "$MARKER"; then
      BUILT=yes
      break
    fi
    sleep 10
  done
  [ -n "$BUILT" ] || say "the starter script never printed $MARKER; no image is created"

  # The instance must be stopped before its disk is imaged.
  gcloud compute instances stop "$BUILDER" --project="$PROJECT" --zone="$ZONE" --quiet >/dev/null 2>&1 || true

  # The stale image goes HERE, and only once the pull has finished: its
  # replacement needs the name, so the two cannot sit side by side, and a
  # delete before the pull would leave an installation with no image at all
  # when the pull failed. GCE refuses to delete an image whose bytes a disk
  # still references and a run's boot disk is such a reference: that refusal
  # ends this build with the old image untouched (the runs keep booting it)
  # and the next scheduled rebuild tries again, rather than the schedule being
  # a no-op for as long as the image is in use.
  if [ -n "$BUILT" ]; then
    if [ -n "$REPLACE" ]; then
      say "deleting the stale image $IMAGE"
      gcloud compute images delete "$IMAGE" --project="$PROJECT" --quiet
    fi
    say "creating the image $IMAGE"
    gcloud compute images create "$IMAGE" --project="$PROJECT" \
      --source-disk="$BUILDER" --source-disk-zone="$ZONE" \
      --labels=fugaro=managed,base-release="$RELEASE_LABEL"
  fi

  # 6. The builder is this build's, and always goes -- built or not.
  gcloud compute instances delete "$BUILDER" --project="$PROJECT" --zone="$ZONE" --quiet >/dev/null 2>&1 || true
  [ -n "$BUILT" ] || exit 1
fi

# 7. Stale preloads: images of this installation's own name prefix that carry
#    fugaro=managed and are not the one the pin resolves to. They are images
#    THIS module's builds made, and a base image past its version is dead
#    weight; anything else is left alone.
STALE="$(gcloud compute images list --project="$PROJECT" \
  --filter="labels.fugaro=managed AND name~^${IMAGE_PREFIX}-" --format='value(name)' 2>/dev/null || true)"
for img in $STALE; do
  [ "$img" = "$IMAGE" ] && continue
  say "deleting the stale preload image $img"
  gcloud compute images delete "$img" --project="$PROJECT" --quiet || true
done

say "done: $IMAGE"
