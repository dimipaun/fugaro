# GCP setup with `fugaro setup`

`fugaro setup` creates one dedicated Google Cloud project for Fugaro and manages it from then on: the enabled APIs, the runs bucket, the image registries, the service accounts and custom roles, log isolation, and each repository's jobs. It shows every Terraform plan and applies it only after you confirm.

## Prerequisites

- **A Google account.** None yet: <https://accounts.google.com/signup>.
- **Personal Google accounts work as they are.** An organization (Workspace) account may be blocked: you need permission to create projects and to link a billing account. If you cannot, ask your IT admin for **Project Creator** (`roles/resourcemanager.projectCreator` on the organization or folder) and **Billing Account User** (`roles/billing.user` on a billing account).
- **A billing account you can link to the new project.** <https://console.cloud.google.com/billing> shows yours. Billing is required: Cloud Run, Cloud Build, Artifact Registry and Secret Manager are not free.
- **Terraform 1.9 or newer, before 2.0** (`terraform version`), on your `PATH`: `fugaro setup` applies every plan through it. [Install](https://developer.hashicorp.com/terraform/install).
- **gcloud** (`gcloud version`), signed in with Application Default Credentials. [Install](https://cloud.google.com/sdk/docs/install).

  ```bash
  gcloud auth application-default login
  ```

## Run it

```bash
fugaro setup
```

It asks for one thing: a name for the project. The GCP project ID (`fugaro-<name>`) and the region (`us-east5`) come with defaults that Enter takes, and the project's ID is what you type at each confirmation. Fugaro only starts fresh: it creates the project, and refuses an ID that already exists.

**If billing cannot be linked it pauses**, rather than failing: the project and its Firebase exist, the run ends with one `gcloud billing projects link <id> --billing-account=<account-id>` block to forward to whoever administers billing, and rerunning the same `fugaro setup` resumes the project it created.

## Verify

```bash
fugaro doctor
```

It reads and changes nothing: the environment, Terraform, billing and the project's IAM, the local project config, a checkout's `fugaro.yaml` and the plugin wiring. Run it first whenever something looks off; it exits 1 with a one-line fix per failure.

<a id="not-an-owner"></a>

## If it fails

- **`Fugaro could not create the project <id>: your Google account is not allowed to create projects in your organization.`** Your organization restricts project creation. Your IT admin grants you Project Creator (`roles/resourcemanager.projectCreator`) on the organization or on a folder, and you rerun `fugaro setup --parent folders/<n>` (or `organizations/<n>`) if they name one.
- **The billing picker lists no account, or the link is refused.** Your account is missing **Billing Account User** (`roles/billing.user`) on the billing account the link needs. Forward the block `fugaro setup` printed: whoever administers billing runs the `gcloud billing projects link` line with their account's id (`gcloud billing accounts list` shows it), you run the rerun line after them.
- **`terraform` is missing, or outside 1.9 to 2.0.** Install a supported version and rerun.
- **`gcloud` is missing, or its credentials are not found.** Install it, then run `gcloud auth application-default login` in the environment that runs `fugaro` (a Windows `gcloud` and a WSL one write two different files: [windows-cli/wsl.md](windows-cli/wsl.md)).
- **`GOOGLE_IMPERSONATE_SERVICE_ACCOUNT is set`, or a `GOOGLE_PROJECT` variable names another project.** Unset it and rerun (`Remove-Item Env:GOOGLE_PROJECT` on PowerShell, `set GOOGLE_PROJECT=` in `cmd.exe`).
