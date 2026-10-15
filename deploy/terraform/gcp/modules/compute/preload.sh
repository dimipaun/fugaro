#!/bin/bash
# The preloaded boot image's build (design compute-backend §1.2, C19; the
# procedure is Check 57a step 3b's, measured 2026-10-15). A Batch container
# runnable starts from a local image and pulls nothing, which is the whole
# point: the plain pull measured 145 s against the preload's 66 s, and the
# 120 s-above-baseline budget is met by the preload and by nothing else a
# Docker run can use.
#
# It runs as a Cloud Build step, as the preload builder account, which holds
# compute.instances.{create,get,list,getSerialPortOutput,stop,delete},
# compute.images.{create,get,getFromFamily,delete} and nothing else: no
# instance or image admin role, no setMetadata (how SSH keys reach a VM), no
# disk create or delete.
#
# What it must never do: delete a VM or an image it did not create. Every
# delete below is guarded by the fugaro=managed label AND by the name, so a
# project's own VM that happens to sit in the zone is left alone -- and the
# build fails loudly instead of clearing the way.
set -euo pipefail

: "${PROJECT:?}" "${ZONE:?}" "${IMAGE:?}" "${BASE_IMAGE:?}" "${BASE_PROJECT:?}" "${BASE_FAMILY:?}"
: "${REGISTRY_HOST:?}" "${RUN_ACCOUNT:?}" "${MACHINE_TYPE:?}" "${IMAGE_PREFIX:?}"

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

# 2. The image this build owns. One image per pinned base version: the name
#    carries a hash of the pin, so a run's boot disk is the image that
#    contains the image the job pulls. An image already there is left alone
#    (idempotent re-runs), and a rebuild after the family rolls produces a
#    new name.
if gcloud compute images describe "$IMAGE" --project="$PROJECT" >/dev/null 2>&1; then
  say "image $IMAGE is already there; nothing to build"
else
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
  #    last line is read from the SERIAL PORT below.
  say "creating the builder VM $BUILDER from projects/$BASE_PROJECT/global/images/$BASE_RELEASE"
  gcloud compute instances create "$BUILDER" --project="$PROJECT" --zone="$ZONE" \
    --image="projects/$BASE_PROJECT/global/images/$BASE_RELEASE" \
    --machine-type="$MACHINE_TYPE" --no-address \
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

  if [ -n "$BUILT" ]; then
    say "creating the image $IMAGE"
    gcloud compute images create "$IMAGE" --project="$PROJECT" \
      --source-disk="$BUILDER" --source-disk-zone="$ZONE" \
      --labels=fugaro=managed
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
