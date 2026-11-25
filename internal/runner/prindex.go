package runner

// The runner wrote the GCS pull-request index from here: one object per run
// under runs/<slug>/pr-index/<N>/<run-id>.json, written at each of the three
// moments a run comes to be on a pull request, so `runs ls --pr N` found the
// runs of a pull request without scanning the repository (docs/runs-ls-speed,
// RL2W, D2962).
//
// It stopped writing with D3204. `runs ls --pr N` reads the run index now
// (internal/runindex), which the history job's sweep folds every run into
// whatever path it took, so a runner has nothing to add: a run whose own
// record names the pull request is a row the index already holds.
//
// Nothing deletes what is in a bucket, and no path of the runner reads it
// either — the write that is left is the launcher's and the backfill's, which
// retire together (IR5b). runstore keeps the format, because the objects in
// place are read until they are not.
