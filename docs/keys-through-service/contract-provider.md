# The provider wire (C1)

Task **C1** of [implementation-plan.md](implementation-plan.md). What it states is **R1** (the
gateway keeps the provider's work, the service holds the key) and **R2** (one entry per repository
and provider credential, and the caller never names an entry), with **D3008** (one entry per
repository; a shared key is copied once for each), **D3015** (an account's OAuth token keeps its
entry by account name), **D3014** (an entry the copy wrote carries where it came from),
**D1418**/**F45** (the provider's address is the entry's) and **F26** (the gateway cannot import the
service, so the names it needs are mirrored) behind the rules; **R12** and **U6** are the two facts
it names, and **F33** is why the entry's family is new
([reconciliation.md](reconciliation.md)). The Go is `internal/credsvc/provider_wire.go`, the
gateway's mirror is `internal/gateway/providerwire.go`, the facts' names are
`internal/config/provider_served.go`, and the stub the contract ships is
`internal/credsvc/provider_wire_stub.go`.

**KS1** (named entries in the bundle), **KS2** (the writers), **KS3** (what the service offers),
**KS4** (the provider route), **KS5** (the run's gateway), **KS6** (the pins), **KS12** (the copy)
and **KS19** (the record and the job) are written against this document; **KS10** reads the second
fact from it, and **I1** deletes the stub and wires the two sides. A side that finds this contract
wrong or incomplete raises a change request
([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than working around
it; the one change already anticipated is **R-entry-name**, [below](#the-change-anticipated).

## What a routed call is

A run's provider call is the call it makes today — a Messages request to a model a provider serves —
and what changes is its last hop. Instead of reaching the provider's own address with a key the job
mounted, it reaches the credentials service on the service's **provider route**, names the provider
it is for, and the service attaches the key it holds for that repository and forwards to the
provider's own address (R1).

| What the call carries | Where | Rule |
| --- | --- | --- |
| The provider's name | the `X-Fugaro-Provider` header | a providers block key of the owner's local config, held to `ValidProviderName` on both sides — never the name of a bundle entry |
| The provider's own path | `ProviderRoutePath` (`/v1/provider/v1/messages`) or `ProviderCountTokensPath` | the prefix is the service's, the rest is the endpoint's: a provider is an endpoint that takes Anthropic Messages requests, so the path behind the prefix is Anthropic's own |

The route is a path of its own rather than the Messages path with a header on it, and that is the
service's one-path-one-class rule where it costs something: the class decides whose rules the call is
checked against — a run's cascade selection, or the provider rules of the published policy — so a
path that served both would be a route whose answer nothing could bound (R11). Both endpoints are
declared because both are really called: a routed model's token count goes to the provider exactly as
its messages do.

**What a routed call never carries.** Not the name of an entry (R2): the service derives the entry
from the run's VERIFIED repository — the `fs` claim of the identity it verified, `creds.Identity.Slug`
— and this header, so a call cannot ask for another repository's key, and a run whose repository has
no entry is refused rather than served someone else's. Not the provider's address (F45, D1418): the
destination is the bundle entry's, and a caller that named one would be choosing where its own
credential — a key the service holds — is sent, which is what KS4's
`TestTheUpstreamIsTheEntrysNotTheCallers` pins. And no credential's value, in this header or any
other: the value is attached by the service, on the call it makes, and never travels toward the run.

The header does not reach the provider: every `X-Fugaro-`-prefixed header is dropped before a call
goes upstream, by the prefix rule `internal/credsvc/forward.go` already applies — which is also why
a header this contract gains later is dropped without a line of its own.

## The refusals

Two typed refusals are this contract's own; the rest of the wire's apply to a provider call
unchanged (an unverified caller, an unreadable state, the gateway's own request check, and the
exhausted-until answer the cascade gives).

| Type | Status | What it means, and where it sends the reader |
| --- | --- | --- |
| `ErrorProviderNotServed` (`provider_not_served`) | `StatusUnavailable` | no entry is held for this repository and this provider: the bundle holds no member of the family, its value was never copied (KS12), or the copy wrote it for another repository. The operator looks at the bundle's entries and at the copy's record |
| `ErrorProviderNotAllowed` (`provider_not_allowed`) | `StatusRequestRefused` | the published policy does not allow the model or the provider credential (R11, D3003). The operator looks at the project's allow-list and at the repository's admission, not at a key |

They are two types and not one word for both, for the reason `ErrorNoCandidateCredentials` is told
apart from `ErrorStateUnavailable`: one is a missing key and the other is a rule, and a single name
would send half its readers to the wrong document. Both are refusals a run halts on (D143) — a job
that carries R12's fact carries the owner's own record that the keys are served, so an absent entry
there is a snapshot of the installation and not a state a run papers over with a mounted key.

## The bundle's entry

One repository's own provider credential is entered under one name of a new family:

```
repo-credential--<slug>--<credential>
```

- **the family** (`RepoCredentialPrefix`) is `repo-credential--`. Its doubled dash is its marker, in
  the sense the account axis uses one: it puts every name of the family outside
  `config.ClaudeAccountNameRE`, so no entry of it can be spelled as an account's name and no account
  name is reserved for it.
- **`<slug>`** is the repository, as `task.Slug` derives it for the repository's git provider
  (`acme/web` under github is `acme-web-<16 hex>`). It is the slug and not the `owner/name` path
  because the slug is what a verified run's identity carries and what the service derives from, and
  because it is one safe token where the path is not.
- **`<credential>`** is the credential's own name: a provider's name for a provider key — the same
  name the header carries — or `anthropic-api-key--project` for the repository's own Anthropic key
  (D3008). The project's key is spelled with the account axis's value
  (`config.AnthropicKeyAccount`) and not with `anthropic-api-key` alone, because a provider may
  legally be NAMED `anthropic-api-key` and one spelling for both would make the repository's own key
  and that provider's key the same entry; `anthropic-api-key--project` is longer than a provider's
  name may be and cannot be spelled by one.

**Why the family is new.** The name that exists for a key credential, `credential-<name>-key`, is
read by the deployed service as a tier B Anthropic key (`CredentialKey`, MC23), so a provider's key
entered under it would change what that name means on a service nobody re-deployed (F33). The two
families cannot be confused: this one begins `repo-`, the other begins `credential-`, and both
`config.KeyCredentialSecret` and `config.CredentialBySecret` read a prefix. C1's
`TestAProviderEntryIsNeverAnAnthropicKeyName` pins it, and no writer may enter a provider key under
the old family whatever else it does.

**The name is readable back, and injective.** `RepoCredentialByKey` splits at the FIRST `--` after
the prefix, not the last, and that is what makes the rule unambiguous: a slug this tree derives never
carries one (`task.Slug` collapses every run of unsafe characters to a single dash, and the rule
refuses a slug that carries one anyway), while the credential's half may carry as many as it likes —
`anthropic-api-key--project` does. So the two halves are recoverable from the name and no two pairs
share one. The name is refused rather than built when either half is not one of the two shapes it
takes, and the refusal names the field and never the value it refused.

**The entry.** `ProviderKeyEntry` holds the repository and the credential's name, the provider's
address and the auth form (the closed set `bearer` / `x-api-key`, D1418), the value — through
`Value()` alone, rendered by nothing, as `Entry` is — and the **source marker** `ProviderKeySource`:
the container the copy read the value from, named by its Secret Manager **id** (the container that
answered, not the logical name a person types), and that container's version (D3014). The scope is
part of the marker because one logical name is held in a container per scope and the two number
their versions independently: a marker carrying the logical name alone leaves the next copy
comparing a version of the repository's container against a version of the project-wide one, which
is a number that answers nothing about either. The zero marker is the whole of
"an entry the copy did not write", which is what makes that phrase checkable without guessing at a
spelling, and the copy overwrites no entry it did not write without saying so (KS12).

**The loader and the schema.** KS1 adds the family to `Bundle` and to
`schemas/creds-bundle.schema.json`: one `propertyNames` branch matching this family, bounded at
`MaxRepoCredentialBytes` — the prefix, a slug's own 63 bytes and the longest credential name the
rule admits — because one bound cannot serve every family and a branch that admitted a name the
loader refuses would be a document read differently by its two readers. The size of the document at
the stated worst case (repositories times credentials) is KS1's `TestAWorstCaseBundleFits` (U5), not
a rule of this contract.

**What is unchanged.** An ACCOUNT's OAuth token keeps the entry it has, under the account's own name
(D3015): it is shared by design, so D3008's one-entry-per-repository rule does not reach it, and
nothing in this contract moves it.

## The accessor

`ReadProviderKey(b *Bundle, slug, credential string) (ProviderKeyEntry, bool)` is how the service
reads a named entry, and it answers two things and never three: the entry, or "no such credential"
(the caller answers `ErrorProviderNotServed`). There is no error arm because there is no read that
can fail — the bundle is in memory and the loader refused any entry it could not serve — and a third
answer every caller had to invent a sentence for is a worse contract than one that cannot be given.

It is a package variable because the contract ships before the side that fills it: while C1 is the
only thing that has landed, `provider_wire_stub.go` assigns a stub that answers "no such
credential" for everything, so KS3, KS4 and KS5 compile and run their own tests against a signature
that exists. The stub decides nothing — it reads no bundle, no repository and no policy — and **I1
deletes it**; KS1 assigns the real accessor from the bundle. The stub's `init` stands only while
nothing else has assigned one, which is deliberate: the files that fill it sort before it in name
order, so an unguarded `init` would put the stub back over the real reader.

C1's `TestStubAccessorHoldsNothing` is named for the stub but asserts the accessor's contract — a
bundle that holds no member of the family answers "no such credential" — so it stays true when the
real accessor lands and when the stub is deleted, which is why it lives in a file I1 does not touch.

## The gateway's mirror

The run's side of this wire is `internal/gateway`, and it cannot import `internal/credsvc`: the
service imports the gateway (its provider route forwards with the gateway's helpers and its
`Route`), so the dependency cannot run the other way (F26). The names the gateway needs are therefore
declared twice — once in `internal/credsvc/provider_wire.go`, which is the contract, and once in
`internal/gateway/providerwire.go`, which is a mirror — and
`TestProviderWireNamesAreDistinct` (an external test file, `package gateway_test`, which may import
both) holds them together: the values are equal, neither is empty, every one of them is a name the
wire did not already carry, the route's paths are the prefix and the endpoint's own paths, and the
rule for the header's value is the local config's own provider-name rule.

This is the shape `internal/gateway/service.go` already uses for the model wire, and the reason is
the same: the two ends of the wire are one repository's code released together, so a name that
changed on one side alone has to be a test failure rather than a call that carries nothing.

## R12's two facts

Release A adds two recorded facts per repository (R12, U6). Their names are
`config.ProviderKeysServedEnv` (`FUGARO_PROVIDER_KEYS_SERVED`) and `config.StageAccountServedEnv`
(`FUGARO_STAGE_ACCOUNT_SERVED`), the true value is `config.ServedValue` (`1`), and an absent entry is
not served.

| Fact | True when | Who reads it |
| --- | --- | --- |
| this repository's provider keys are served | the bundle holds an entry for every provider credential the repository's config names, the deployed service can read that entry kind, and the project publishes an allow-list (F41) | the run's gateway, which sends a routed provider call through the service instead of using the mounted key (KS5) |
| this repository's stage account is served | the mixed variant's second account is one the service selects like any named account, so the runner holds no token for it (R9) | the runner, where it picks the account its stages go out on (KS10, F44) |

**Amended 2026-11-20 (F50): the second fact is not needed.** KS10 is withdrawn (F48), and with a
stage's account the access of that stage's craft, one fact per repository — "its credentials are
served", which [credentials belong to models](../credentials-belong-to-models/README.md) declares —
covers both. The name stays in `internal/config` as C1 wrote it; nothing records or reads it.

**Where they are recorded.** The owner's `fugaro update` is what reads each fact and decides it,
because it is the one command that can see everything the fact is about; it records the answer on
that repository's own entry in the LOCAL CONFIG, beside the switch already there
(`localcfg.Repo.CredentialsServiceReady`), and the repository's root is applied with the fact planned
into the job's environment from that record. That is the road the existing switch travels (F31), and
it is why the names live in `internal/config`: `internal/infra` plans a job with them and
`internal/runner` reads them out of one, and config is the package both may import. The record's own
shape is KS19's, not this contract's; the names are this contract's, as U6 says.

A fact set where it should not be gains the setter nothing (R12): it decides whether a run SENDS a
provider call through the service, never whether the service serves one. The service derives the
entry from the run's verified repository and refuses a credential it does not hold for it whatever
the job claimed, so the worst a forged fact reaches is that run's own refused call — and never
another repository's key. The names are `FUGARO_`-prefixed, which is what keeps them Fugaro's own:
`config.ReservedEnv` refuses a workflow secret's variable with that prefix and the image contract
refuses it too, so nothing a repository's workflow declares can write one into a job.

## The change anticipated

**R-entry-name** (raised by KS12, reaching KS1, KS2 and KS4): if U2 turns out to show that a
provider key's three sources — a repository's own container, a project-wide one, and a
`credential-<name>-key` entry that predates this release (F27) — cannot be named as this document
writes the rule. The rule above names the repository and the credential and says nothing about the
source, on the reading that the entry is what the run is served and the source is a fact the copy
records rather than a part of the name: one repository served one provider credential is one entry,
wherever the value came from. A source that had to be part of the name would change this document,
the naming rule and the schema branch together, in the change request's own sweep.
