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
# WHAT IT BUILDS, and why it is a family rather than one name. The runs boot
# the image FAMILY the pin derives (projects/<project>/global/images/family/
# <IMAGE_FAMILY>), and every build makes a NEW generation inside it. That is
# what makes a rebuild safe: an image's name is not interchangeable with
# another's, so replacing a name in place would mean deleting the image before
# its replacement exists, and a create that then failed would leave the
# installation with no image at all. In a family the previous generation keeps
# serving until the new one exists, and the family switches over the moment it
# does (cloud.google.com/compute/docs/images/image-families-best-practices,
# checked 2026-10-16).
#
# WHY IT RUNS ON A SCHEDULE: the image it makes goes stale two ways and it
# must leave neither in place (design §1.2, C4, C19). The base family in
# Google's own project rolls -- the pinned base_image does not move with it,
# and neither does the family name this build owns, so the release an image
# was built from is recorded in its base-release label and read back here --
# and a Batch custom image's supported lifetime can end 60 days after its
# starting date, whatever it was built from, so age alone replaces it. A
# newest image that is this build's own, built from the family's current
# release and younger than REFRESH_DAYS is the only thing left alone.
#
# WHAT IT RECORDS BESIDE THE IMAGE: the socket gid. A run that asks for the
# Docker socket reaches it through `--group-add <gid>` (design §2.2, C2), and
# the gid belongs to the boot image rather than to a run, so it is a fact
# about the image this build makes -- and one that a rebuild can change, which
# is why it is read here rather than once. The builder VM's startup script
# reads it off the VM itself (`stat -c %g /var/run/docker.sock`, the command
# 57a-6 used) and prints it on the serial port beside the marker; this script
# takes it from there, refuses a value that is not a positive integer, and
# writes it as the SOCKET_GID_LABEL label on every generation it creates. The
# label's name is not written here: it arrives from the build's substitutions
# (preload.tf), so the writer and the module's published output cannot drift.
# The value itself is never a literal in this repository: an installation's
# gid is whatever its boot image has.
#
# What it must never do: delete a VM or an image it did not create. Every
# delete below is guarded by the fugaro=managed label AND by the name, so a
# project's own VM or image that happens to carry this name is left alone --
# and the build fails loudly instead of clearing the way.
set -euo pipefail

: "${PROJECT:?}" "${ZONE:?}" "${IMAGE_FAMILY:?}" "${BASE_IMAGE:?}" "${BASE_PROJECT:?}" "${BASE_FAMILY:?}"
: "${REGISTRY_HOST:?}" "${RUN_ACCOUNT:?}" "${MACHINE_TYPE:?}" "${IMAGE_PREFIX:?}"
: "${NETWORK:?}" "${SUBNETWORK:?}" "${REFRESH_DAYS:?}" "${SOCKET_GID_LABEL:?}"

BUILDER="fugaro-preload-build"
MARKER="fugaro-preload-built"
# The line the builder VM's startup script prints the socket's gid on, read
# with the marker below and parsed in step 5.
GID_MARKER="fugaro-preload-socket-gid="

say() { printf 'fugaro-preload: %s\n' "$*"; }

# 0. The builder VM never outlives the build, whatever ends it. set -e aborts
#    on the first failing call, and an abort between the create and step 6's
#    delete would leave a RUNNING VM and its disk behind -- money, and a name
#    the next build then has to clear. The trap is the backstop; step 6 is the
#    ordinary path and clears the flag so this stays quiet.
#
#    One ending it does NOT cover: Cloud Build's own timeout (1800 s,
#    preload.tf) kills the step's process tree, and a SIGKILL runs no trap. A
#    build that times out can therefore leave its builder VM running until the
#    next scheduled build's step 3 finds it (the same name, the same
#    fugaro=managed label) and deletes it. A leak of up to a week, bounded by
#    the schedule, and the price of the timeout being Cloud Build's rather
#    than this script's.
BUILDER_UP=""
cleanup() {
  rc=$?
  if [ -n "$BUILDER_UP" ]; then
    say "deleting the builder VM $BUILDER (the build exited $rc)"
    gcloud compute instances delete "$BUILDER" --project="$PROJECT" --zone="$ZONE" --quiet >/dev/null 2>&1 || true
  fi
  exit "$rc"
}
trap cleanup EXIT

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

# 2. What the family holds now, API answer by answer: the newest image in this
#    build's own family, the release it was built from (its base-release
#    label -- the name cannot carry it, because neither the pin nor this
#    family name moves when Google rolls the family) and its age (the creation
#    date a Batch image's 60-day lifetime runs from). Only an image that is
#    this build's own, current and inside the window is left alone; a family
#    roll, an image that predates this label, and an image past the window all
#    mean this build makes a new generation.
BUILD=yes
if gcloud compute images describe-from-family "$IMAGE_FAMILY" --project="$PROJECT" >/dev/null 2>&1; then
  # csv[no-heading] rather than value(): a value() row leaves an absent field
  # out, and the four answers here have to stay in their columns -- an image
  # with no base-release label is a fact this build acts on, not a shift. And
  # IFS=,: without it `read` splits on whitespace only, so the whole
  # comma-joined line lands in the first variable and every existing image
  # reads as one that is not fugaro=managed.
  # The fifth column is the socket gid this build's own earlier generation
  # recorded: an image without it predates this label, so it is replaced
  # rather than kept, whatever its age.
  IFS=, read -r IMG_NAME IMG_OWNER IMG_RELEASE IMG_CREATED IMG_GID < <(gcloud compute images describe-from-family "$IMAGE_FAMILY" \
    --project="$PROJECT" --format="csv[no-heading](name,labels.fugaro,labels.base-release,creationTimestamp,labels.${SOCKET_GID_LABEL})")
  # An image under this family that this build did not make is left alone and
  # said so: the build fails rather than building beside it or clearing it.
  [ "$IMG_OWNER" = "managed" ] || { say "the newest image in $IMAGE_FAMILY is $IMG_NAME and is not fugaro=managed; refusing to touch it"; exit 1; }
  # A stamp the parser cannot read is read as 0, i.e. as old as possible: an
  # image whose age is unknown is replaced rather than kept.
  IMG_EPOCH="$(date -u -d "$IMG_CREATED" +%s 2>/dev/null || echo 0)"
  IMG_AGE_DAYS="$(( ( $(date -u +%s) - IMG_EPOCH ) / 86400 ))"
  if [ "$IMG_RELEASE" = "$RELEASE_LABEL" ] && [ "$IMG_AGE_DAYS" -lt "$REFRESH_DAYS" ] && [ -n "$IMG_GID" ]; then
    say "$IMG_NAME is built from $BASE_RELEASE and is $IMG_AGE_DAYS days old (the window is $REFRESH_DAYS), and records the socket gid $IMG_GID; nothing to build"
    BUILD=""
  elif [ -z "$IMG_GID" ]; then
    say "$IMG_NAME records no ${SOCKET_GID_LABEL} label, so this build makes a generation that does"
  else
    say "$IMG_NAME is built from ${IMG_RELEASE:-a release this build never recorded} and is $IMG_AGE_DAYS days old; $BASE_FAMILY is now $BASE_RELEASE, so this build makes a new generation"
  fi
fi

# The new generation's name. It ends in the build's UTC second, so the family
# has a total order by name -- newest is greatest -- and two builds that
# overlap sort the way they ran. Two builds inside the same second collide on
# the name instead, and the loser fails loudly rather than racing.
NEW_IMAGE=""

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
        "socket_gid=\"\$(stat -c %g /var/run/docker.sock 2>/dev/null || true)\"" \
        "echo '${GID_MARKER}'\"\${socket_gid}\"" \
        "echo ${MARKER}")"
  BUILDER_UP=yes

  # 5. The pull is what this waits for. Bounded: a pull that fails never
  #    prints the marker, and this loop has to end anyway.
  BUILT=""
  SERIAL=""
  for _ in $(seq 1 60); do
    SERIAL="$(gcloud compute instances get-serial-port-output "$BUILDER" --project="$PROJECT" \
        --zone="$ZONE" --port=1 2>/dev/null || true)"
    if printf '%s\n' "$SERIAL" | grep -q "$MARKER"; then
      BUILT=yes
      break
    fi
    sleep 10
  done
  [ -n "$BUILT" ] || say "the starter script never printed $MARKER; no image is created"

  # The socket gid the builder VM's own startup script read off its own socket
  # (step 4), and the last thing that can be wrong before an image is made. A
  # build whose gid is missing, or is not a positive integer, creates NOTHING:
  # an image with no ${SOCKET_GID_LABEL} label is one every run's `--group-add`
  # would be a guess against, and the family keeps serving the generation it
  # already has while this build fails loudly.
  SOCKET_GID=""
  if [ -n "$BUILT" ]; then
    SOCKET_GID="$(printf '%s\n' "$SERIAL" | sed -n "s/.*${GID_MARKER}//p" | tail -n 1)"
    case "$SOCKET_GID" in
      '' | *[!0-9]*)
        say "the builder VM printed no usable socket gid (${GID_MARKER}${SOCKET_GID}): expected the gid of /var/run/docker.sock, so no image is created"
        SOCKET_GID=""
        BUILT=""
        ;;
      0)
        say "the builder VM's socket gid is 0, which is not a group a container can be added to; no image is created"
        SOCKET_GID=""
        BUILT=""
        ;;
      *) say "the builder VM's socket gid is $SOCKET_GID" ;;
    esac
  fi

  # The instance must be stopped before its disk is imaged.
  gcloud compute instances stop "$BUILDER" --project="$PROJECT" --zone="$ZONE" --quiet >/dev/null 2>&1 || true

  # 6. The new generation, and only once the pull has finished: an image taken
  #    from a disk whose pull has not finished is an image with nothing in it,
  #    and the runs would silently pay the pull the preload exists to remove.
  #    Creating BEFORE deleting anything is what keeps the family serving: a
  #    failure here leaves the previous generation as the newest, and the
  #    build fails with the runs' boot reference intact.
  if [ -n "$BUILT" ]; then
    NEW_IMAGE="${IMAGE_FAMILY}-$(date -u +%Y%m%d%H%M%S)"
    say "creating the image $NEW_IMAGE in family $IMAGE_FAMILY"
    gcloud compute images create "$NEW_IMAGE" --project="$PROJECT" \
      --source-disk="$BUILDER" --source-disk-zone="$ZONE" \
      --family="$IMAGE_FAMILY" \
      --labels=fugaro=managed,base-release="$RELEASE_LABEL","${SOCKET_GID_LABEL}=${SOCKET_GID}"
    say "$NEW_IMAGE records the socket gid $SOCKET_GID under the ${SOCKET_GID_LABEL} label"
  fi

  # 7. The builder is this build's, and always goes -- built or not. The flag
  #    is cleared ONLY when the delete has actually returned, so a delete that
  #    fails leaves it raised and the EXIT trap tries once more; clearing it
  #    either way would leak the VM and its disk to the next build's step 3.
  if gcloud compute instances delete "$BUILDER" --project="$PROJECT" \
      --zone="$ZONE" --quiet >/dev/null 2>&1; then
    BUILDER_UP=""
  else
    say "the builder VM $BUILDER could not be deleted; the trap tries once more"
  fi
  [ -n "$BUILT" ] || exit 1
fi

# 8. Superseded images: every image of this installation's own name prefix
#    that carries fugaro=managed and is neither the generation this build just
#    made nor the newest of the family the runs boot. That is the previous
#    generation, and the whole family of a pin that has moved on -- and an
#    image that predates families and carries none. The newest of a family is
#    never deleted, and neither is this build's own, so two builds that
#    overlap cannot delete each other's work; a delete that fails (an image
#    still held, a permission Google changes) leaves the image for the next
#    run of the schedule rather than failing the build.
#
#    Anything else is left alone: an image that is not fugaro=managed is not
#    this module's to delete, whatever its name.
IMAGES="$(gcloud compute images list --project="$PROJECT" \
  --filter="labels.fugaro=managed AND name~^${IMAGE_PREFIX}-" \
  --format='csv[no-heading](name,family)' 2>/dev/null || true)"
if [ -n "$IMAGES" ]; then
  DOOMED="$(printf '%s\n' "$IMAGES" | awk -F, -v keep="$NEW_IMAGE" -v fam="$IMAGE_FAMILY" '
    NF >= 1 && $1 != "" {
      n++; name[n] = $1; family[n] = $2
      if ($2 == fam && $1 > best) best = $1
    }
    END {
      for (i = 1; i <= n; i++) {
        if (name[i] == keep) continue
        if (family[i] == fam && name[i] == best) continue
        print name[i]
      }
    }')"
  for img in $DOOMED; do
    say "deleting the superseded preload image $img"
    gcloud compute images delete "$img" --project="$PROJECT" --quiet || true
  done
fi

say "done: family $IMAGE_FAMILY"
