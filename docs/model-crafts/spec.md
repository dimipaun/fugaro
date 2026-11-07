# Fugaro Model Roles and Credentials Spec

## Summary

The fixed setup (an OpenRouter key for the DeepSeek coder, plus an optional Anthropic key and Claude OAuth for the Opus reviewers) is replaced by three general ideas: **credentials**, **pools** and **roles**. Each job role uses a model plus a credential or a pool. Roles that aren't set inherit from another role.

## Background

- Fugaro grew around one setup: a cheap DeepSeek coder through OpenRouter, with Opus doing the senior review and the security review through Claude OAuth or an Anthropic key.
- A job involves up to four model roles, but providers and models can't currently be mixed freely per role, and there's no support for several accounts.

## 1. Credentials

- A credential is a named entry with a type. Users can add as many as they want.
- Supported types:
    - OpenRouter API key
    - Anthropic API key
    - OpenAI API key
    - Vertex (through gcloud)
    - Claude OAuth (subscription)
    - Codex/OpenAI OAuth *(future)*
- Example: three Claude accounts are three credentials, `claude-1`, `claude-2` and `claude-3`.
- Secrets are stored in Secret Manager. Config only refers to credentials by name.

## 2. Pools

- A pool is a named, ordered list of credentials, with a strategy:
    - **failover**: try them in order, and move to the next on errors, rate limits or exhausted quota.
    - **round-robin**: share the load across them, and skip any that fail.
- Example: pool `claude` = `claude-1`, `claude-2`, `claude-3` (round-robin), then the Anthropic API key as the last fallback.
- Whenever a role can take a credential, it can take a pool instead.

## 3. Roles

| Role | Default if not set |
|---|---|
| Coder | Required |
| Review (level 1) | Same as Coder |
| Senior review | Required |
| Security review (optional) | Same as Senior review |

- Each role = a **model** + a **credential or pool**.
- Inheritance applies to model and access separately. For example, the security review can change only the model and keep the senior reviewer's pool.
- The security review stays optional. Turning it on without settings makes it use the senior review's settings.

## Configuration

- Credentials, pools and roles are managed only through Fugaro management commands, never by editing YAML by hand (see the Composite Management Commands Spec).
- They follow the existing project, repo and local layering, within the project guardrails. Only admins can change them for their scope (see the User Roles and Scopes Spec).
- The diagnose command uses the same credentials (see the Diagnose Spec).
- A command shows each role's effective model and access, and where each setting came from (set directly, or inherited).

## Migration

- Turn the current OpenRouter key, Anthropic key and Claude OAuth into named credentials automatically.
- Map the current behaviour to roles: Coder = DeepSeek through OpenRouter, Senior review = Opus through Claude OAuth. Existing setups keep working with no changes.

## Acceptance criteria

- Any role can use any supported provider and model.
- With only Coder and Senior review set, Review and Security review inherit correctly.
- A pool with three OAuth credentials and a final API key fails over when one runs out of quota, and round-robin spreads the load.
- Existing projects migrate without anyone having to step in.
