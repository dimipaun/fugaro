# OpenRouter snapshots for the generator

These are saved responses from OpenRouter's public API, read on **2026-10-23**.
They are the generator's input on the test path: `gencatalog`'s tests and
`TestGeneratedFileMatchesSnapshots` regenerate `catalog.json` from them, so the
checked-in file can be reproduced without the network, and a diff of these
files shows what a regeneration would change.

They are **trimmed**, not full responses. The live API returned 468 models in
`/api/v1/models` and 1060 endpoint rows over 376 models in
`/api/v1/endpoints/zdr` on that date; the snapshots keep only what the
generator reads:

| file | kept | of what |
|---|---|---|
| `models.json` | the 5 allowlisted models | 468 models |
| `models_<id>_endpoints.json` | every endpoint row of those 5 models | one file per model, of 468 |
| `zdr.json` | the 81 rows whose `model_id` is one of the 5 | 1060 rows over 376 models |

Within a row, only the fields the generator reads are kept. **This list is
machine-read**: `TestSnapshotsAreTrimmedToTheDocumentedFields` strips the
snapshots down to exactly the fields below, regenerates, and requires the
result to be the checked-in `catalog.json` byte for byte — so a field the
generator reads and this list leaves out fails that test instead of quietly
dropping a dimension from the file.

<!-- kept-fields: begin -->
models: id pricing.prompt pricing.completion pricing.input_cache_read
endpoints: tag context_length max_completion_tokens pricing.prompt pricing.completion pricing.input_cache_read pricing.input_cache_write pricing.input_cache_write_1h pricing.discount
zdr: model_id tag
<!-- kept-fields: end -->

`models.json`'s row is a model (`data` is a list); the endpoints file's is one
endpoint (`data.endpoints` is the list); `zdr.json`'s is a ZDR-eligible
endpoint, and only its `model_id` and `tag` are read — its own `pricing` is
ignored, because the prices come from the endpoint rows above. The
`pricing.`-prefixed names are the endpoint's pricing fields the generator reads
(`apiDims` in `../gencatalog/main.go`); `discount` is kept because it is part
of the published row, and is deliberately not read (design D8).

Everything else the API sends — names, descriptions, context lengths at the
model level, `alias_target`, `created`, provider metadata — is dropped: the
generator never reads it, and a full response would be megabytes of noise
around the prices. Dropping a field the generator *does* read is therefore the
one way to make these files lie, which is why the list above is checked
against the generator rather than kept in step by hand.

## Regenerating offline

The generator takes a directory of snapshots instead of the network, so a
regeneration from these files needs no credentials and no API key:

```
scripts/gen-price-catalog.sh -from internal/pricing/catalog/testdata/openrouter -date 2026-10-23
```

`-date` is explicit because the generator never reads the wall clock: the same
inputs and the same date produce the same bytes.

Refreshing the snapshots themselves does need the network, and is a deliberate
step of its own — a new snapshot set is a new set of prices, so it belongs in a
reviewable diff next to the regenerated `catalog.json`, with the date in this
file and in `catalog.json`'s `checked_at` updated together.
