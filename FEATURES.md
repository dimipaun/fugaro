# Fugaro features

Fugaro runs each task you hand it as its own throwaway job in your cloud project, and opens the pull request when it is done. This file is what it can do, and how far along each capability is.

**Available** — works today. **Coming soon** — under way, or built and not shipped yet. **Planned** — not started.

A ✓ under Community, Team or Enterprise means the feature is included there, and a dash means it is not.

as_of: v0.29.0

| Feature | What it does | Status | Community | Team | Enterprise |
| --- | --- | --- | --- | --- | --- |
| Run a task in your own cloud | Hand Fugaro a task and it runs the coding agent to completion as a job in the Google Cloud project you own, opening a pull request when it is done. A run can be launched for a repository you have not cloned, and it carries its own limits of time, tokens and money that a restart cannot reset. | Available | ✓ | ✓ | ✓ |
| Watch a run as it happens | Follow what a run is doing while it works, and refer to it by a short ID that reads the same everywhere it is shown. | Available | ✓ | ✓ | ✓ |
| Find the runs on a pull request in a moment | Asking which runs a pull request has, or which are going, reads a small index instead of the whole history, so the answer comes back in about a second. | Coming soon | ✓ | ✓ | ✓ |
| Send a message to a run that is going | A person can tell a running task something new, to be read at its next coder stage, at its next quiet point or as a request to wrap up, and the run's record says what became of each message. | Coming soon | ✓ | ✓ | ✓ |
| Set up and update from the command line | Fugaro provisions everything it needs in your own Google Cloud project from the command line, and brings the whole installation to a new version the same way. The update reports which known issues it resolves. | Available | ✓ | ✓ | ✓ |
| Manage the repositories a project holds | The repositories enrolled in a project are added, removed, listed and checked with one command each, and the check says what is left of an enrolment a step done out of order left half-finished. A removal undoes the enrolment and leaves a run that is going alone. | Coming soon | ✓ | ✓ | ✓ |
| Drive Fugaro from Claude Code | Fugaro's commands are driven from Claude Code through a plugin installed for you, and the plugin says when an update is due. In a managed project the agent runs the cloud-changing commands itself, behind a deterministic gate instead of a prompt. | Available | ✓ | ✓ | ✓ |
| A model that did not write the code reviews it | Every run is reviewed by a model other than the one that wrote the code, and a triage step decides which findings hold the merge and which do not. | Available | ✓ | ✓ | ✓ |
| Reviews check the change against the task | A review is given the task the pull request was written for, so it can say that part of the task is unmet or that the change differs from it, and a review of a pull request no run produced can be given a task by hand. | Available | ✓ | ✓ | ✓ |
| Review a pull request on its own, at a chosen depth | A pull request can be reviewed by a separate review job, with the review depth chosen for the project and a second model offered as the reviewer. | Coming soon | ✓ | ✓ | ✓ |
| Choose the model and the account behind every stage | Each stage of a run can use a different model and a different account, with named accounts, aliases that can be repointed, and a vision model that reads pictures for a coder that cannot. | Available | ✓ | ✓ | ✓ |
| Change any setting with a command | Every setting at every level — local, repository and project — is read and changed by a command, so no configuration file is edited by hand. | Available | ✓ | ✓ | ✓ |
| One write path behind every setting | A setting is changed through one write path, whichever command owns the field, so a field with invariants or a publish step is written only by the command that understands it. A generic write to a field another command owns is refused, naming the command to use. | Available | ✓ | ✓ | ✓ |
| Choose how thorough a run is | A project picks a rigor mode, from a quick proof of concept to a change that must not break anything, and every stage of a run follows it. | Coming soon | ✓ | ✓ | ✓ |
| Nothing stops to ask you a question | No command prompts, and none waits on a closed input: a step that needs a decision takes a flag, a budget cap or a refusal that prints the fix. | Available | ✓ | ✓ | ✓ |
| Bring your own job image | A project keeps a small catalogue of named images, each built from a Dockerfile you already have or from a base image of your own, and a skill proposes and validates them. An image's own environment variables reach the run that uses it. | Available | ✓ | ✓ | ✓ |
| Work with Bitbucket repositories | A Bitbucket repository is driven like a GitHub one: the agent pushes a branch and opens a pull request with credentials the installation holds. | Available | ✓ | ✓ | ✓ |
| Connect Bitbucket with the token you have | A workspace, project or repository access token can be used to reach Bitbucket, and each token reaches only what it was granted. | Coming soon | ✓ | ✓ | ✓ |
| Commits credited to the person who asked | A commit the agent makes is authored by the person who launched the run, and carries trailers that tie it back to the run and the model that produced it. | Coming soon | ✓ | ✓ | ✓ |
| See which work a pull request belongs to | The pull requests Fugaro opens are labelled with the state of the run and the work it came from, so a list of pull requests says at a glance what each one is. | Available | ✓ | ✓ | ✓ |
| Your credentials stay in your own cloud | Model and forge credentials are held by a service in your own project, and a workflow's own secret is stored once for the project and reaches only the runs allowed to use it. | Available | ✓ | ✓ | ✓ |
| A run is handed its access, not a stored key | A job is given the access it needs when it starts, through a broker in your own project, rather than a stored key. | Coming soon | ✓ | ✓ | ✓ |
| Say what counts as security-sensitive | A project names the paths that count as security-sensitive and the executables a run may use, so a change to a sensitive path is escalated and a run nobody reviewed is reported as such. | Available | ✓ | ✓ | ✓ |
| Decide who may launch and cancel | A project grants roles — viewer, operator and admin — each with a scope, so a person can read, launch or cancel only within the projects and repositories they were given. | Available | ✓ | ✓ | ✓ |
| Find out what went wrong | A failed command's log can be read by a local diagnostic agent, and what it finds can be sent to the issue tracker from the command line. An issue already filed can be followed up to see what happened to it. | Available | ✓ | ✓ | ✓ |
| Explain a failure with your project's own credential | When the machine in front of you holds no model login of its own, the evidence a failed command collected is handed to one bounded job in your own project, and a person standing at that machine still gets the cause and the fix. The job reads the redacted evidence and nothing else. | Available | ✓ | ✓ | ✓ |
| Reports on how your runs are going | Every run ends with a classified failure, and a periodic digest reports what a project's runs did — the failures, the spend and the patterns worth acting on. | Coming soon | ✓ | ✓ | ✓ |
| Recorded spend matches what the provider billed | A run's spend is settled on the cost the model provider reports, priced conservatively where the provider reports none, and an alarm is raised when settled and estimated spend drift apart. | Available | ✓ | ✓ | ✓ |
| Runs share a project's jobs | A project's runs share a small pool of jobs and image caches instead of one job per repository, and what each run may reach is decided per job. | Coming soon | ✓ | ✓ | ✓ |
| Let Fugaro work through your queue | An orchestrator works through a queue of tasks on its own, keeps its state in the cloud rather than on a machine, and pauses when a repository refuses it. | Available | ✓ | ✓ | ✓ |
| Know when the work will be ready and what it will cost | A task graph shows a range for when the work will be ready and roughly what it will cost, and ad hoc items join the same graph as nodes. The estimate is informational and reserves no budget. | Available | ✓ | ✓ | ✓ |
| Pull requests that stop converging | When a pull request stops converging, the orchestrator escalates through a ladder of rungs — rebase and retest, resume, one stronger pass, merge with follow-ups, or split — instead of retrying forever. | Available | ✓ | ✓ | ✓ |
| Read the docs, the support matrix and the change log | The public site carries the documentation, a matrix of what Fugaro supports on each platform and integration, and a change log of what every release brought. | Available | ✓ | ✓ | ✓ |
| The desktop console | A desktop app for macOS and Linux shows a project's settings, its runs and its coordinator queue, and reads the same data the command line does. | Available | — | ✓ | ✓ |
| Manage a project from the console | The console lists a project's models and keys and shows the change a command would make before it is made. | Coming soon | — | ✓ | ✓ |
| See how releases depend on each other | A graph draws the project's releases and tasks as boxes, with the dependencies between them and the path that matters. | Available | — | ✓ | ✓ |
| Fugaro on your phone | A mobile app shows your projects, tasks and runs, and pairs a device with your installation to sign in. | Coming soon | — | ✓ | ✓ |
| Reach a project from any agent | A hosted endpoint exposes a project's tasks, runs and reports to an agent outside Fugaro, over a protocol the agent already speaks. | Coming soon | — | ✓ | ✓ |
| Group projects into a workspace | Several projects can be grouped into a workspace with its own budget, so a team's projects share one cap and one view. | Available | — | ✓ | ✓ |
| Share a workspace's access with its projects | A workspace holds the roster of its people and the credentials its projects share, and serves them to the projects that are members. | Coming soon | — | ✓ | ✓ |
| Catch a runaway run early | Every task carries a tier, and a run that spends far more than its tier expects is stopped and reported instead of running to its cap. | Available | — | ✓ | ✓ |
| Every run is held to a budget | A run is held to a spend budget whichever way it is launched, and a model whose price Fugaro cannot work out is refused rather than run unmetered. | Coming soon | ✓ | ✓ | ✓ |
| Compare models on the same task | Another model can be run against the same task beside the first, and the results are scored and reported together. | Available | — | ✓ | ✓ |

<!-- The table above is rendered from the block below, which the features page on the website reads. The two cannot disagree about a row. -->

```json
{
  "version": 1,
  "as_of": "v0.29.0",
  "rows": [
    {
      "id": "run-a-task",
      "name": "Run a task in your own cloud",
      "explanation": "Hand Fugaro a task and it runs the coding agent to completion as a job in the Google Cloud project you own, opening a pull request when it is done. A run can be launched for a repository you have not cloned, and it carries its own limits of time, tokens and money that a restart cannot reset.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "watch-a-run",
      "name": "Watch a run as it happens",
      "explanation": "Follow what a run is doing while it works, and refer to it by a short ID that reads the same everywhere it is shown.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "find-a-pull-requests-runs",
      "name": "Find the runs on a pull request in a moment",
      "explanation": "Asking which runs a pull request has, or which are going, reads a small index instead of the whole history, so the answer comes back in about a second.",
      "status": "coming-soon",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "message-a-running-run",
      "name": "Send a message to a run that is going",
      "explanation": "A person can tell a running task something new, to be read at its next coder stage, at its next quiet point or as a request to wrap up, and the run's record says what became of each message.",
      "status": "coming-soon",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "install-and-update",
      "name": "Set up and update from the command line",
      "explanation": "Fugaro provisions everything it needs in your own Google Cloud project from the command line, and brings the whole installation to a new version the same way. The update reports which known issues it resolves.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "manage-repositories",
      "name": "Manage the repositories a project holds",
      "explanation": "The repositories enrolled in a project are added, removed, listed and checked with one command each, and the check says what is left of an enrolment a step done out of order left half-finished. A removal undoes the enrolment and leaves a run that is going alone.",
      "status": "coming-soon",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "drive-from-claude-code",
      "name": "Drive Fugaro from Claude Code",
      "explanation": "Fugaro's commands are driven from Claude Code through a plugin installed for you, and the plugin says when an update is due. In a managed project the agent runs the cloud-changing commands itself, behind a deterministic gate instead of a prompt.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "review-and-fix",
      "name": "A model that did not write the code reviews it",
      "explanation": "Every run is reviewed by a model other than the one that wrote the code, and a triage step decides which findings hold the merge and which do not.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "review-against-the-task",
      "name": "Reviews check the change against the task",
      "explanation": "A review is given the task the pull request was written for, so it can say that part of the task is unmet or that the change differs from it, and a review of a pull request no run produced can be given a task by hand.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "review-on-its-own",
      "name": "Review a pull request on its own, at a chosen depth",
      "explanation": "A pull request can be reviewed by a separate review job, with the review depth chosen for the project and a second model offered as the reviewer.",
      "status": "coming-soon",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "model-access",
      "name": "Choose the model and the account behind every stage",
      "explanation": "Each stage of a run can use a different model and a different account, with named accounts, aliases that can be repointed, and a vision model that reads pictures for a coder that cannot.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "settings-and-config",
      "name": "Change any setting with a command",
      "explanation": "Every setting at every level — local, repository and project — is read and changed by a command, so no configuration file is edited by hand.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "one-write-path",
      "name": "One write path behind every setting",
      "explanation": "A setting is changed through one write path, whichever command owns the field, so a field with invariants or a publish step is written only by the command that understands it. A generic write to a field another command owns is refused, naming the command to use.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "run-thoroughness",
      "name": "Choose how thorough a run is",
      "explanation": "A project picks a rigor mode, from a quick proof of concept to a change that must not break anything, and every stage of a run follows it.",
      "status": "coming-soon",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "unattended-commands",
      "name": "Nothing stops to ask you a question",
      "explanation": "No command prompts, and none waits on a closed input: a step that needs a decision takes a flag, a budget cap or a refusal that prints the fix.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "job-images",
      "name": "Bring your own job image",
      "explanation": "A project keeps a small catalogue of named images, each built from a Dockerfile you already have or from a base image of your own, and a skill proposes and validates them. An image's own environment variables reach the run that uses it.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "bitbucket",
      "name": "Work with Bitbucket repositories",
      "explanation": "A Bitbucket repository is driven like a GitHub one: the agent pushes a branch and opens a pull request with credentials the installation holds.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "bitbucket-tokens",
      "name": "Connect Bitbucket with the token you have",
      "explanation": "A workspace, project or repository access token can be used to reach Bitbucket, and each token reaches only what it was granted.",
      "status": "coming-soon",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "commit-authorship",
      "name": "Commits credited to the person who asked",
      "explanation": "A commit the agent makes is authored by the person who launched the run, and carries trailers that tie it back to the run and the model that produced it.",
      "status": "coming-soon",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "pull-request-labels",
      "name": "See which work a pull request belongs to",
      "explanation": "The pull requests Fugaro opens are labelled with the state of the run and the work it came from, so a list of pull requests says at a glance what each one is.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "credentials-and-secrets",
      "name": "Your credentials stay in your own cloud",
      "explanation": "Model and forge credentials are held by a service in your own project, and a workflow's own secret is stored once for the project and reaches only the runs allowed to use it.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "runs-handed-their-access",
      "name": "A run is handed its access, not a stored key",
      "explanation": "A job is given the access it needs when it starts, through a broker in your own project, rather than a stored key.",
      "status": "coming-soon",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "project-security-rules",
      "name": "Say what counts as security-sensitive",
      "explanation": "A project names the paths that count as security-sensitive and the executables a run may use, so a change to a sensitive path is escalated and a run nobody reviewed is reported as such.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "roles-and-access",
      "name": "Decide who may launch and cancel",
      "explanation": "A project grants roles — viewer, operator and admin — each with a scope, so a person can read, launch or cancel only within the projects and repositories they were given.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "diagnose-and-report",
      "name": "Find out what went wrong",
      "explanation": "A failed command's log can be read by a local diagnostic agent, and what it finds can be sent to the issue tracker from the command line. An issue already filed can be followed up to see what happened to it.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "cloud-diagnosis-job",
      "name": "Explain a failure with your project's own credential",
      "explanation": "When the machine in front of you holds no model login of its own, the evidence a failed command collected is handed to one bounded job in your own project, and a person standing at that machine still gets the cause and the fix. The job reads the redacted evidence and nothing else.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "run-reports",
      "name": "Reports on how your runs are going",
      "explanation": "Every run ends with a classified failure, and a periodic digest reports what a project's runs did — the failures, the spend and the patterns worth acting on.",
      "status": "coming-soon",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "billed-spend-matches",
      "name": "Recorded spend matches what the provider billed",
      "explanation": "A run's spend is settled on the cost the model provider reports, priced conservatively where the provider reports none, and an alarm is raised when settled and estimated spend drift apart.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "shared-jobs",
      "name": "Runs share a project's jobs",
      "explanation": "A project's runs share a small pool of jobs and image caches instead of one job per repository, and what each run may reach is decided per job.",
      "status": "coming-soon",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "orchestrator",
      "name": "Let Fugaro work through your queue",
      "explanation": "An orchestrator works through a queue of tasks on its own, keeps its state in the cloud rather than on a machine, and pauses when a repository refuses it.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "orchestrator-estimates",
      "name": "Know when the work will be ready and what it will cost",
      "explanation": "A task graph shows a range for when the work will be ready and roughly what it will cost, and ad hoc items join the same graph as nodes. The estimate is informational and reserves no budget.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "escalation",
      "name": "Pull requests that stop converging",
      "explanation": "When a pull request stops converging, the orchestrator escalates through a ladder of rungs — rebase and retest, resume, one stronger pass, merge with follow-ups, or split — instead of retrying forever.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "documentation",
      "name": "Read the docs, the support matrix and the change log",
      "explanation": "The public site carries the documentation, a matrix of what Fugaro supports on each platform and integration, and a change log of what every release brought.",
      "status": "available",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "console",
      "name": "The desktop console",
      "explanation": "A desktop app for macOS and Linux shows a project's settings, its runs and its coordinator queue, and reads the same data the command line does.",
      "status": "available",
      "plans": {
        "community": false,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "console-manage",
      "name": "Manage a project from the console",
      "explanation": "The console lists a project's models and keys and shows the change a command would make before it is made.",
      "status": "coming-soon",
      "plans": {
        "community": false,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "release-graph",
      "name": "See how releases depend on each other",
      "explanation": "A graph draws the project's releases and tasks as boxes, with the dependencies between them and the path that matters.",
      "status": "available",
      "plans": {
        "community": false,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "mobile-app",
      "name": "Fugaro on your phone",
      "explanation": "A mobile app shows your projects, tasks and runs, and pairs a device with your installation to sign in.",
      "status": "coming-soon",
      "plans": {
        "community": false,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "agent-access",
      "name": "Reach a project from any agent",
      "explanation": "A hosted endpoint exposes a project's tasks, runs and reports to an agent outside Fugaro, over a protocol the agent already speaks.",
      "status": "coming-soon",
      "plans": {
        "community": false,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "project-groups",
      "name": "Group projects into a workspace",
      "explanation": "Several projects can be grouped into a workspace with its own budget, so a team's projects share one cap and one view.",
      "status": "available",
      "plans": {
        "community": false,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "workspace-members",
      "name": "Share a workspace's access with its projects",
      "explanation": "A workspace holds the roster of its people and the credentials its projects share, and serves them to the projects that are members.",
      "status": "coming-soon",
      "plans": {
        "community": false,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "run-governance",
      "name": "Catch a runaway run early",
      "explanation": "Every task carries a tier, and a run that spends far more than its tier expects is stopped and reported instead of running to its cap.",
      "status": "available",
      "plans": {
        "community": false,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "every-run-has-a-budget",
      "name": "Every run is held to a budget",
      "explanation": "A run is held to a spend budget whichever way it is launched, and a model whose price Fugaro cannot work out is refused rather than run unmetered.",
      "status": "coming-soon",
      "plans": {
        "community": true,
        "team": true,
        "enterprise": true
      }
    },
    {
      "id": "model-comparison",
      "name": "Compare models on the same task",
      "explanation": "Another model can be run against the same task beside the first, and the results are scored and reported together.",
      "status": "available",
      "plans": {
        "community": false,
        "team": true,
        "enterprise": true
      }
    }
  ]
}
```
