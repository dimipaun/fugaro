# OpenRouter snapshots for the generator

These are saved responses from OpenRouter's public API, read on **2026-10-14**.
They are the generator's input on the test path: `gencatalog`'s tests and
`TestGeneratedFileMatchesSnapshots` regenerate `catalog.json` from them, so the
checked-in file can be reproduced without the network, and a diff of these
files shows what a regeneration would change.

They are **trimmed**, not full responses. The live API returned 462 models in
`/api/v1/models` and 946 endpoint rows over 356 models in
`/api/v1/endpoints/zdr` on that date; the snapshots keep only what the
generator reads:

| file | kept | of what |
|---|---|---|
| `models.json` | the 5 allowlisted models | 462 models |
| `models_<id>_endpoints.json` | every endpoint row of those 5 models | one file per model, of 462 |
| `zdr.json` | the 67 rows whose `model_id` is one of the 5 | 946 rows over 356 models |

Within a row, only the fields the generator reads are kept:

- `models.json`: `id` and `pricing.{prompt,completion,input_cache_read}`.
- `models_<id>_endpoints.json`: `tag`, `context_length`,
  `max_completion_tokens` and `pricing.{prompt,completion,input_cache_read,discount}`.
- `zdr.json`: `model_id` and `tag` (the row's own `pricing` is not read; the
  prices come from the endpoint rows above).

Everything else the API sends — names, descriptions, context lengths at the
model level, `alias_target`, `created`, provider metadata — is dropped: the
generator never reads it, and a full response would be megabytes of noise
around the prices. Dropping a field the generator *does* read is therefore the
one way to make these files lie, which is why the shape above is worth keeping
in step with `apiDims` and `buildEntry` in `../gencatalog/main.go`.

## Regenerating offline

The generator takes a directory of snapshots instead of the network, so a
regeneration from these files needs no credentials and no API key:

```
scripts/gen-price-catalog.sh -from internal/pricing/catalog/testdata/openrouter -date 2026-10-14
```

`-date` is explicit because the generator never reads the wall clock: the same
inputs and the same date produce the same bytes.

Refreshing the snapshots themselves does need the network, and is a deliberate
step of its own — a new snapshot set is a new set of prices, so it belongs in a
reviewable diff next to the regenerated `catalog.json`, with the date in this
file and in `catalog.json`'s `checked_at` updated together.
