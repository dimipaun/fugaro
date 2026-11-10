# A run on a text-only coder model needs a vision-capable model for image input

*The spec as received: issue #1865, opened by the owner on 2026-11-07. It is reproduced verbatim and
is not edited here; where [reconciliation.md](reconciliation.md) disagrees with it, the reconciliation
wins.*

---

Run 20261107-104613-b2af (C4) failed at implement: the coder, deepseek/deepseek-v4.1-flash, is
text-only, and the gateway refused an image block because the model has no
`pricing.Model.ImageTokens` (`internal/gateway/forward.go`; `internal/pricing/table.go` gives it only
to Claude models). The agent read a screenshot golden because the task's fixture work involved console
`__screenshots__` PNGs.

Wanted (owner 2026-11-07: 'we need to handle this case and provision for a vision-capable model in
case we're using a text-only model'): when the coder model has no image rate, an image input is routed
to a configured vision-capable model (a `models.vision` setting, resolved like the other model slots,
priced with its own `ImageTokens`) instead of failing the run; a run whose model is text-only and has
no vision model set says so at bootstrap when the task or repo names image files (fail early, with the
fix), not mid-stage. The router, record and doctor line must name which model served the image.

Until then: briefs for text-only coders say never to read image files.
