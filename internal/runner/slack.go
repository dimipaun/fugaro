package runner

// TaskTimeoutSlack is taskTimeoutSlack exported, for the job spec: the
// Cloud Run task timeout is timeouts.total plus exactly this, which the
// lock expiry and the writeback window rely on. It lives in its own file
// so the fix wave's parallel edits of lockcache.go don't collide; a later
// pass can fold it into one declaration (or a shared package).
const TaskTimeoutSlack = taskTimeoutSlack
