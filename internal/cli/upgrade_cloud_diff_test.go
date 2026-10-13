package cli

import (
	"strings"
	"testing"
)

// These are direct, fast unit tests of the diff machinery jobConfigCopyNote
// uses (mapDiff, providersDiff, pricesDiff, scalarConfigDiff, jobConfigDiffs):
// no rig, no cloud, no refresh. Review follow-up (PR #324): the changed,
// removed and unreadable branches had no test at all, so a mutation making
// `eq` always true, dropping `removed`, or turning "unreadable copy" into ""
// all passed the suite silently.

// TestProvidersDiffEqualNonEmptyIgnoresOrderAndFormatting: two providers,
// each with its own fields and the object's own keys in a different order
// (map decoding does not care), stay quiet.
func TestProvidersDiffEqualNonEmptyIgnoresOrderAndFormatting(t *testing.T) {
	job := `{"openrouter":{"kind":"anthropic-compat","base_url":"https://openrouter.ai/api","auth":"bearer","secret":"openrouter-api-key","models":["deepseek/*"],"allow_data_to":["acme/app"]},` +
		`"other":{"models":["other/*"],"allow_data_to":["acme/app"],"kind":"anthropic-compat","base_url":"https://other.example/api","auth":"x-api-key","secret":"other-api-key"}}`
	now := `{"other":{"kind":"anthropic-compat","base_url":"https://other.example/api","auth":"x-api-key","secret":"other-api-key","models":["other/*"],"allow_data_to":["acme/app"]},` +
		`"openrouter":{"allow_data_to":["acme/app"],"models":["deepseek/*"],"secret":"openrouter-api-key","auth":"bearer","base_url":"https://openrouter.ai/api","kind":"anthropic-compat"}}`
	if d := providersDiff(job, now); d != "" {
		t.Fatalf("providersDiff = %q, want \"\" (same entries, different key order)", d)
	}
}

// TestPricesDiffEqualIgnoresNumberFormatting: "0.140" and "0.14" are the same
// float64, so the same formatted-differently price is not a false
// difference (mapDiff compares the decoded Rates, not the raw text).
func TestPricesDiffEqualIgnoresNumberFormatting(t *testing.T) {
	job := `{"deepseek/deepseek-v4-flash":{"input_per_m":0.140,"output_per_m":0.280,"cache_write_5m":1.25,"cache_write_1h":2,"cache_read":0.1,"web_search_per_1k":10}}`
	now := `{"deepseek/deepseek-v4-flash":{"input_per_m":0.14,"output_per_m":0.28,"cache_write_5m":1.25,"cache_write_1h":2,"cache_read":0.1,"web_search_per_1k":10}}`
	if d := pricesDiff(job, now); d != "" {
		t.Fatalf("pricesDiff = %q, want \"\" (0.140 == 0.14)", d)
	}
}

// TestProvidersDiffNamesAChangedEntry: the same provider name, a different
// field (route_fee_pct), is named "changed", not silently accepted.
//
// Mutation (verified, run and restored): in mapDiff, change `!eq(v, now[k])`
// to `!eq(v, v)` (eq effectively always true): this test fails, finding no
// diff.
func TestProvidersDiffNamesAChangedEntry(t *testing.T) {
	job := `{"openrouter":{"kind":"anthropic-compat","base_url":"https://openrouter.ai/api","auth":"bearer","secret":"openrouter-api-key","route_fee_pct":5.5,"models":["deepseek/*"],"allow_data_to":["acme/app"]}}`
	now := `{"openrouter":{"kind":"anthropic-compat","base_url":"https://openrouter.ai/api","auth":"bearer","secret":"openrouter-api-key","route_fee_pct":9,"models":["deepseek/*"],"allow_data_to":["acme/app"]}}`
	d := providersDiff(job, now)
	if !strings.Contains(d, "changed") || !strings.Contains(d, "openrouter") {
		t.Fatalf("providersDiff = %q, want it to name openrouter changed", d)
	}
}

// TestProvidersDiffNamesARemovedEntry: the job has a provider the local
// config no longer names; it is "removed", not silently dropped.
//
// Mutation (verified, run and restored): in mapDiff, delete the second loop
// (the one building `removed`): this test fails, finding no diff.
func TestProvidersDiffNamesARemovedEntry(t *testing.T) {
	job := `{"openrouter":{"kind":"anthropic-compat","base_url":"https://openrouter.ai/api","auth":"bearer","secret":"openrouter-api-key","models":["deepseek/*"],"allow_data_to":["acme/app"]}}`
	now := `{}`
	d := providersDiff(job, now)
	if !strings.Contains(d, "removed") || !strings.Contains(d, "openrouter") {
		t.Fatalf("providersDiff = %q, want it to name openrouter removed", d)
	}
}

// TestProvidersDiffUnreadableCopy: a job's copy that is not valid JSON (an
// old or hand-edited form this fugaro cannot parse) is named "unreadable",
// not silently treated as matching, whenever the two raw strings actually
// differ.
//
// Mutation (verified, run and restored): in providersDiff, change `return
// "unreadable copy"` to `return ""`: this test fails, finding no diff.
func TestProvidersDiffUnreadableCopy(t *testing.T) {
	job := `{not json`
	now := `{"openrouter":{"kind":"anthropic-compat","base_url":"https://openrouter.ai/api","auth":"bearer","secret":"openrouter-api-key","models":["deepseek/*"],"allow_data_to":["acme/app"]}}`
	if d := providersDiff(job, now); d != "unreadable copy" {
		t.Fatalf("providersDiff = %q, want %q", d, "unreadable copy")
	}
	// The same unreadable text on both sides is not a difference: a real
	// difference could not be named by field either way, but byte-identical
	// copies are not stale.
	if d := providersDiff(job, job); d != "" {
		t.Fatalf("providersDiff(job, job) = %q, want \"\" (byte-identical)", d)
	}
}

// TestPricesDiffNamesAChangedEntry: review follow-up (PR #324, MINOR): every
// pricesDiff test so far used prices that matched or were byte-garbage, so a
// mutation making mapDiff's eq always true (reported alongside
// TestProvidersDiffNamesAChangedEntry) passed every pricesDiff test too. The
// same model, a real price change (0.14 to 0.15), is named "changed".
//
// Mutation (verified, run and restored): in mapDiff, change `!eq(v, now[k])`
// to `!eq(v, v)` (eq effectively always true): this test fails, finding no
// diff.
func TestPricesDiffNamesAChangedEntry(t *testing.T) {
	job := `{"deepseek/deepseek-v4-flash":{"input_per_m":0.14,"output_per_m":0.28}}`
	now := `{"deepseek/deepseek-v4-flash":{"input_per_m":0.15,"output_per_m":0.28}}`
	d := pricesDiff(job, now)
	if !strings.Contains(d, "changed") || !strings.Contains(d, "deepseek/deepseek-v4-flash") {
		t.Fatalf("pricesDiff = %q, want it to name deepseek/deepseek-v4-flash changed", d)
	}
}

// TestPricesDiffNamesAnAddedEntry: the local config prices a model the job's
// copy never had; it is "added", not silently accepted.
func TestPricesDiffNamesAnAddedEntry(t *testing.T) {
	job := `{}`
	now := `{"deepseek/deepseek-v4-flash":{"input_per_m":0.14,"output_per_m":0.28}}`
	d := pricesDiff(job, now)
	if !strings.Contains(d, "added") || !strings.Contains(d, "deepseek/deepseek-v4-flash") {
		t.Fatalf("pricesDiff = %q, want it to name deepseek/deepseek-v4-flash added", d)
	}
}

// TestPricesDiffNamesARemovedEntry: the job's copy prices a model the local
// config no longer does; it is "removed", not silently dropped.
//
// Mutation (verified, run and restored): in mapDiff, delete the second loop
// (the one building `removed`): this test fails, finding no diff.
func TestPricesDiffNamesARemovedEntry(t *testing.T) {
	job := `{"deepseek/deepseek-v4-flash":{"input_per_m":0.14,"output_per_m":0.28}}`
	now := `{}`
	d := pricesDiff(job, now)
	if !strings.Contains(d, "removed") || !strings.Contains(d, "deepseek/deepseek-v4-flash") {
		t.Fatalf("pricesDiff = %q, want it to name deepseek/deepseek-v4-flash removed", d)
	}
}

// TestPricesDiffUnreadableCopy mirrors TestProvidersDiffUnreadableCopy for
// FUGARO_MODEL_PRICES.
//
// Mutation (verified, run and restored): in pricesDiff, change `return
// "unreadable copy"` to `return ""`: this test fails, finding no diff.
func TestPricesDiffUnreadableCopy(t *testing.T) {
	job := `{not json`
	now := `{"deepseek/deepseek-v4-flash":{"input_per_m":0.14,"output_per_m":0.28}}`
	if d := pricesDiff(job, now); d != "unreadable copy" {
		t.Fatalf("pricesDiff = %q, want %q", d, "unreadable copy")
	}
}

// TestScalarConfigDiffExcludesPlatformAndSecretEnvsAndJSONKeys: the platform
// identity keys, FUGARO_SECRET_ENVS and the two JSON-valued keys never show
// up in the scalar diff, even when they differ; a real scalar key that
// differs does.
func TestScalarConfigDiffExcludesPlatformAndSecretEnvsAndJSONKeys(t *testing.T) {
	job := map[string]string{
		"FUGARO_BUCKET": "gs://old-bucket", "FUGARO_GCP_PROJECT": "old-proj", "FUGARO_REGION": "us-west1",
		"FUGARO_SECRET_ENVS":     "GITHUB_APP_KEY",
		"FUGARO_MODEL_PROVIDERS": `{"openrouter":{}}`,
		"FUGARO_MODEL_PRICES":    `{"x":{}}`,
		"FUGARO_BUDGET_MODE":     "enforce",
	}
	now := map[string]string{
		"FUGARO_BUCKET": "gs://new-bucket", "FUGARO_GCP_PROJECT": "new-proj", "FUGARO_REGION": "us-east5",
		"FUGARO_SECRET_ENVS":     "GITHUB_APP_KEY,ANTHROPIC_API_KEY",
		"FUGARO_MODEL_PROVIDERS": `{"openrouter":{},"other":{}}`,
		"FUGARO_MODEL_PRICES":    `{"y":{}}`,
		"FUGARO_BUDGET_MODE":     "off",
	}
	d := scalarConfigDiff(job, now)
	if !strings.Contains(d, "FUGARO_BUDGET_MODE") {
		t.Fatalf("scalarConfigDiff = %q, want it to name FUGARO_BUDGET_MODE", d)
	}
	for _, excluded := range []string{"FUGARO_BUCKET", "FUGARO_GCP_PROJECT", "FUGARO_REGION", "FUGARO_SECRET_ENVS", "FUGARO_MODEL_PROVIDERS", "FUGARO_MODEL_PRICES"} {
		if strings.Contains(d, excluded) {
			t.Fatalf("scalarConfigDiff = %q, must not name %s", d, excluded)
		}
	}
}

// TestJobConfigDiffsNamesTheBudgetFieldsTheOriginalBugMissed: the review's
// own reproduction (PR #324 review, finding 1): matching providers/prices
// but a local budget the job's copy lacks must still be named. This is the
// pure, fast version of TestUpgradeCloudReportsStaleBudgetConfigCopy
// (upgrade_cloud_step_test.go), which proves the same thing end to end.
func TestJobConfigDiffsNamesTheBudgetFieldsTheOriginalBugMissed(t *testing.T) {
	providers := `{"openrouter":{"kind":"anthropic-compat","base_url":"https://openrouter.ai/api","auth":"bearer","secret":"openrouter-api-key","models":["deepseek/*"],"allow_data_to":["acme/app"]}}`
	prices := `{"deepseek/deepseek-v4-flash":{"input_per_m":0.14,"output_per_m":0.28}}`
	job := map[string]string{"FUGARO_MODEL_PROVIDERS": providers, "FUGARO_MODEL_PRICES": prices}
	now := map[string]string{"FUGARO_MODEL_PROVIDERS": providers, "FUGARO_MODEL_PRICES": prices,
		"FUGARO_BUDGET_MODE": "enforce", "FUGARO_MAX_RUN_USD": "5"}
	diffs := jobConfigDiffs(job, now)
	joined := strings.Join(diffs, "; ")
	if strings.Contains(joined, "providers:") || strings.Contains(joined, "model_prices:") {
		t.Fatalf("jobConfigDiffs = %q, falsely names providers/model_prices (they match)", joined)
	}
	if !strings.Contains(joined, "FUGARO_BUDGET_MODE") || !strings.Contains(joined, "FUGARO_MAX_RUN_USD") {
		t.Fatalf("jobConfigDiffs = %q, want it to name FUGARO_BUDGET_MODE and FUGARO_MAX_RUN_USD", joined)
	}
}
