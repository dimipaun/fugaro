---
id: 20261104-01
title: Run 20261104-040522-0c2c: review round 2 left 3 findings open
mode: startup
area: project
status: open
filed-by: unknown
filed-in: run:20261104-040522-0c2c
date: 2026-11-04
---

## What was left open

Round 2's senior review ended `changes` with 3 findings, and the run that received them shipped none of them, so they are carried here rather than fixed in that run's pull request. As relayed to the run that took them up:

1. **The ten `internal/cli` tests whose fixtures carry a removed key.** Each builds an inline `fugaro.yaml` with `image.tools`, `image.apt`, `image.setup`, `image.skip_build_scripts`, `dockerfile:` or `rebuild.check`, so each fails on the refusals R2 adds: `TestImagePromoteCopiesTheDefinitionAndRepointsTheRepo`, `TestImagePromoteDropsTheDockerfileItCopied`, `TestImageRenderRepositoryDockerfile`, `TestCheckJobResolvesAndSubmitsWithTheLayer`, `TestRepoEngineBuildsOnlyThePerRepositoryWorkflow`, `TestDoctorImageConfigNamesEachDifferingSource`, `TestDoctorImageConfigSkipsDockerfileWorkflows`, and the cases `TestLsWarnsInOtherCases` and `TestCheckNotInstalledWorkflow` carried.
2. **The dead `imageLeafKeys` entries in `internal/cli/doctor_layer.go`** — entries a key this removal takes out fed, which nothing walks any more.
3. **`internal/cli/image_promote.go`'s command doc comment** — it never said that R7 deletes `image promote`, which is why the tests around it are the ones the removal takes with it.

All three are shipped by pull request #1694 (`d1aaec38`), under the owner's ruling of 2026-11-04 that a test whose only subject is a removed key is deleted with the code it alone exercised.

## Why

The run left them open: they are not what that pull request's outcome was decided on, and an item filed here is a record a later reader can cite, which a finding in a transcript is not (D9). An item that names only the count of what it carries is not that record — a reader has to find the round's transcript to learn what to fix.

## What done looks like

The pull request that addresses them closes this item (D31).

## Links

- run `20261104-040522-0c2c`
- pull request #1694
