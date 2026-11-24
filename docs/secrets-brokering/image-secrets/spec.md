# Spec: Route Image Secrets Through the Credential Service

**Status:** Draft, to be finalized against the findings of the Fugaro Secrets Audit
**Owner:** Dimi

## 1. Problem

Fugaro originally mapped Secret Manager secrets directly onto Cloud Run jobs through Terraform (`--set-secrets`, `secretKeyRef` env vars and volume mounts). This setup is static. Changing any secret means Terraform has to rerun and every affected job has to be redefined.

The credential service replaced this for most secrets. Jobs call the service at runtime, the service checks the request against policy, and it reads Secret Manager on the job's behalf. It also proxies Bitbucket and similar services, so some credentials never reach the job at all.

We suspect that secrets declared on an **image** still use the old path: when a job is created from an image, those secrets get turned into direct job secret mappings. This breaks the rule and makes the system hard to reason about.

## 2. The Rule

> Every secret a job uses is obtained from the credential service at runtime. Nothing else may read Secret Manager. No job spec contains a secret reference.

The rule is enforced by IAM, not by convention. Only the credential service's service account holds `roles/secretmanager.secretAccessor`. Any leftover direct mapping therefore fails loudly instead of quietly working.

## 3. Goals and Non-Goals

**Goals**
- Image secrets are delivered through the credential service, the same way as every other secret.
- Job specs (in every backend: gen-1 Cloud Run, gen-2 Cloud Run, VM) contain no secret references.
- Job service accounts have no Secret Manager permissions.
- Changing a secret or an image's secret list never requires Terraform or redefining a job.
- An automated check prevents regressions.

**Non-goals**
- Redesigning the credential service's internal storage or its Bitbucket proxy.
- Secret rotation policy (it follows naturally once there's a single path, but it's a separate project).

## 4. Design

### 4.1 Image declares names, not values

An image's metadata lists the **logical names** of the secrets it needs, plus how each one should be exposed:

```yaml
image: belong-server-test
secrets:
  - name: FIREBASE_SA_KEY
    expose: file          # written to tmpfs, path given in an env var
  - name: NPM_TOKEN
    expose: env
```

This declaration is just data. Job creation code does **not** turn it into Cloud Run secret mappings. It passes the list (or the image ID) to the job as ordinary, non-secret configuration.

### 4.2 Job identity

Each job proves who it is to the credential service using its Cloud Run / VM service-account identity token (audience = credential service URL). The service resolves the token to a job, which tells it the project, task, image and rigor level.

Open question: should we also issue a short-lived, per-task job token at launch, so the service can revoke a single task without affecting the whole service account? Leaning yes.

### 4.3 Bootstrap step

The job's entrypoint gets a small bootstrap stage that runs before the harness:

1. Read the image's secret list from job config.
2. Call the credential service: `POST /v1/secrets:resolve` with the identity token and the list of names.
3. The service checks policy (is this image/project allowed to have these names?) and returns the values, or an error listing which names were denied.
4. Expose each secret as declared: as an env var on the **child** process only (never the bootstrap's own environment that might get logged), or as a file on tmpfs with `0400` permissions.
5. If anything fails, stop the job with a clear error. Never continue with partial secrets.
6. `exec` the harness.

Wherever possible, a proxy is better than handing over a raw value. If the credential service can proxy a provider (as it already does for Bitbucket), the image should declare the proxy rather than the raw secret.

### 4.4 Credential service changes

- Add a policy entry that maps an image (or image + project) to the secret names it's allowed.
- Add the `secrets:resolve` endpoint, if the current API doesn't already cover batch lookups.
- Write an audit log entry for every resolve call, recording job, image, names and the decision. Never log values.

### 4.5 IAM

- Remove `roles/secretmanager.secretAccessor` (and anything broader) from all job service accounts.
- Grant job service accounts `roles/run.invoker` on the credential service (or the matching permission for whatever fronts it).
- The credential service's service account is the only identity that can read Secret Manager.

### 4.6 Regression guard

- A CI check fails the build if job-creation code or Terraform produces any `secretKeyRef`, secret volume or `--set-secrets` flag.
- A CI check fails the build if any Secret Manager SDK import appears outside the credential service package.
- Optionally, a periodic job runs `gcloud run jobs describe` across the project and raises an alert on any secret reference.

## 5. Migration

1. **Audit:** run the audit brief and get the full list of violations.
2. **Policy:** load each image's current secret list into the credential service policy.
3. **Bootstrap:** ship the bootstrap stage. It runs alongside the old mappings for one release, and if both are present the credential-service value wins.
4. **Remove mappings:** delete the image-secret code paths from job creation in all backends, and remove the matching Terraform.
5. **Lock down IAM:** remove `secretAccessor` from job service accounts. From here on, any leftover direct mapping fails.
6. **Guard:** turn on the CI checks.
7. **Clean up:** handle any other violations the audit found (baked-in secrets, plaintext env vars, secrets in payloads or logs).

Rollback: until step 5, putting the old mappings back restores the old behaviour. After step 5, rolling back also means re-granting IAM, which should be a deliberate decision.

## 6. Acceptance Criteria

- `gcloud run jobs describe` shows zero secret references on every Fugaro job.
- No job service account has any Secret Manager permission.
- A job built from an image with declared secrets runs successfully, gets every secret through the credential service, and appears in the service's audit log.
- Changing an image's secret list needs only a policy update, with no Terraform run and no job redefinition.
- Both CI guards are on and have been seen to fail on a deliberately bad change.

## 7. Open Questions

- Should there be a per-task job token on top of the service-account identity (§4.2)?
- Where should image → secret policy live: in the image metadata (reviewed in code) or in the credential service's own store?
- How should the VM backend get its identity token? Probably the metadata server; this needs confirming.
- Should secrets be cached during long jobs, or fetched once at startup? Startup-only is simpler; refetching handles rotation mid-job.
