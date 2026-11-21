# Credentials belong to models, not repositories

Owner: Dimi Paun. Date: 2026-11-20. Written by the coordinator from the
owner's rulings; the owner's words are quoted where they are his.

### The ruling

Asked twice what to do with "a repository on another auth mode", the owner
rejected the question itself.

D3007 (keys plan, 2026-11-19):

> There shouldn't be any repo tie-in to keys. Api-keys, oauth tokens etc are
> there for the models only which should use the most appropriate way to
> authenticate.

D3026 (always-a-budget plan, 2026-11-20):

> The entire notion of "key repository" or "oauth repository" is stupid and
> should die. There should be no such thing. keys/oauth should be there for
> models to get access to their servers, that's it.

### What is asked

1. A repository has no auth mode. `agent.auth` (api-key, oauth, cascade,
   vertex) stops being a setting a repository chooses.
2. A credential (an API key, a Claude subscription token, a provider key, a
   cloud identity such as Vertex's) is declared once, for the models it
   reaches. A run that uses a model gets whatever credential reaches that
   model, by the most appropriate way to authenticate.
3. No rule, refusal, check, doctor line or document is phrased in terms of
   "an oauth repository" or "an api-key repository".
4. Every run goes through its gateway and the credentials service, whatever
   credential its models use. No run keeps a token in the agent's own
   environment because of the kind of credential it uses.
5. `fugaro update` is the whole upgrade: it migrates every config, policy and
   record that names an auth mode, in place, with no other command, deletion
   or manual step, and it is safe with runs in flight.
6. Nothing that works today stops without the update saying so first, with
   the fix.

### What the brief knows it does not know

- What replaces `agent.auth` where one model can be reached by more than one
  credential (a subscription and an API key for the same Claude model), and
  who chooses the order: the project, the model's declaration, or the
  existing cascade.
- What happens to `cascade`, which is today both an auth mode and the
  ordering of credentials.
- What happens to Vertex, which has no key and is today the default auth.
- What happens to per-stage accounts and the mixed stage account.
- Which formats carry the auth mode (fugaro.yaml, the project layer, the
  published policy, the run record, `--json` outputs, the console and mobile
  app) and which need a version bump.
- Whether this ships before, with or after the two plans it follows.

### Out of scope

- Moving keys off job mounts (the keys plan) and removing budget mode off
  (the always-a-budget plan), except where this work changes what they
  planned.
- Per-person tokens at the workspace (waits on the threat model).
