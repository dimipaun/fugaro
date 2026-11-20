# The lists behind the mapping

`internal/place` answers where a new installation's data goes with no cloud call, so it carries
Google's own lists instead of asking an API. This is those lists for a person to read, with the page
and the date each was read from. `TestTheListsAreDated` reads this file and the package together, so
a list that moves in one place and not in the other fails a test rather than quietly becoming two
lists.

All three were read on **2026-11-20**, the date `place.ListsReadOn` carries.

## The regions Fugaro accepts

https://cloud.google.com/run/docs/locations (last updated 2026-11-18). These are Cloud Run's
locations, and a `region` in the project config is one of them: everything an installation creates
in its own project is offered where Cloud Run is.

```
asia-east1
asia-east2
asia-northeast1
asia-northeast2
asia-northeast3
asia-south1
asia-south2
asia-southeast1
asia-southeast2
asia-southeast3
australia-southeast1
australia-southeast2
europe-central2
europe-north1
europe-north2
europe-southwest1
europe-west1
europe-west2
europe-west3
europe-west4
europe-west6
europe-west8
europe-west9
europe-west10
europe-west12
me-central1
me-central2
me-west1
northamerica-northeast1
northamerica-northeast2
northamerica-south1
southamerica-east1
southamerica-west1
us-central1
us-east1
us-east4
us-east5
us-south1
us-west1
us-west2
us-west3
us-west4
us-west8
```

Two regions are not on this page and are the only ones the change that added it met:
`africa-south1` and `asia-southeast4`, both named among Cloud Run's tier 1 regions on
https://cloud.google.com/run/pricing#tiers (read 2026-09-27, `internal/backend/gcp/prices.go`).
They are not refused: `place.Of` places a region by its prefix, so both are in neither place, and
`place.For` states every fallback for them. What has no answer for a region outside this list is its
Cloud Scheduler location — `gcp.SchedulerRegion` refuses one it does not know, and
`--scheduler-region` is the fix (F3).

## Where a Firestore database is offered

https://firebase.google.com/docs/firestore/locations (last updated 2026-11-18). A new
installation's run history database goes in its own region where this page names it, and in the
place's default region where it does not (`us-east5`, `europe-west1`; D3085). Firestore also offers
the multi-regions `nam5`, `nam7` and `eur3`, which the mapping never chooses: a multi-region is a
higher price for a database that is not on a run's path.

```
asia-east1
asia-east2
asia-northeast1
asia-northeast2
asia-northeast3
asia-south1
asia-south2
asia-southeast1
asia-southeast2
australia-southeast1
australia-southeast2
europe-central2
europe-north1
europe-north2
europe-southwest1
europe-west1
europe-west2
europe-west3
europe-west4
europe-west6
europe-west8
europe-west9
europe-west10
europe-west12
me-central1
me-central2
me-west1
northamerica-northeast1
northamerica-northeast2
northamerica-south1
southamerica-east1
southamerica-west1
us-central1
us-east1
us-east4
us-east5
us-south1
us-west1
us-west2
us-west3
us-west4
```

## Where Cloud Logging offers a bucket

https://cloud.google.com/logging/docs/region-support (last updated 2026-11-20). A new
installation's run log bucket goes in its own region where this page names it. Where it does not,
the place's multi-region takes it — `us` for the United States, `eu` for the European Union —
and a region in neither place gets `global`; each is printed with its reason before anything is
created (D3082, D3093).

```
asia-east1
asia-east2
asia-northeast1
asia-northeast2
asia-northeast3
asia-south1
asia-south2
asia-southeast1
asia-southeast2
australia-southeast1
australia-southeast2
europe-central2
europe-north1
europe-west1
europe-west2
europe-west3
europe-west4
europe-west6
europe-west8
europe-west9
europe-west12
me-central1
me-west1
northamerica-northeast1
northamerica-northeast2
southamerica-east1
us-central1
us-east1
us-east4
us-south1
us-west1
us-west2
us-west3
us-west4
```

Cloud Logging also offers the `global` bucket and the multi-regions `us` and `eu`; those three are
the mapping's fallbacks and are not a region an installation's own region is compared against.

## Where the Realtime Database is offered

https://firebase.google.com/docs/database/locations (last updated 2026-11-18): `us-central1`,
`europe-west1` and `asia-southeast1`, and no other location. The budget's database is the one thing
that cannot be in the installation's own region unless the region is one of those three, so every
other region gets the nearest of them with the reason printed. Which of the three is nearest is
`nearestDatabase` in `internal/place`: the product's guess at it, by continent, and the owner's to
correct (O4).

## What these lists are not

They are not the scheduler's. Cloud Scheduler's own locations — a different set, with a fixed
nearby location per region Google does not offer it in — are `gcp.SchedulerRegions` and
`gcp.nearestSchedulerRegion` (`internal/backend/gcp/names.go`), read from
https://cloud.google.com/scheduler/docs/locations. Nor are they the secret containers' replication:
a container created from now on is user-managed in the installation's region, so there is nothing to
fall back from (D3082, D3092).
