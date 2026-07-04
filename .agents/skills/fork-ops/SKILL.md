---
name: fork-ops
description: "Operate the prateek/forks fleet monorepo: add a new downstream fork, run/resume/abort the sync engine, check fleet or per-fork status, carry a new upstream PR / branch / patch, resolve a paused sync conflict, hack on a fork's source, retire a fork after its PRs land, or rotate the fork-automation credentials. Use in the prateek/forks checkout. Trigger on 'add a fork', 'fork X and track my PRs', 'fork sync conflict', 'resume the fork sync', 'retire the X fork', 'why isn't my fork updating', 'rotate the fork app key', 'run my PRs as my daily driver'."
---

# fork-ops

`prateek/forks` is the fleet monorepo and Homebrew tap for daily-driver forks.
The repo layout, the three-job workflow, the four source kinds, and the
credential model are in [`../../../AGENTS.md`](../../../AGENTS.md); read it first.
This skill is the operator playbook: the concrete commands for each operation.

Adopt and retire in `prateek/dotfiles` are a **separate** manual step — the
`fork-lifecycle` skill over there, run as an ordinary PR. This repo never edits
dotfiles.

## Add a fork

1. **Fork upstream** under `prateek/` if you don't already have one for PR work:
   `gh repo fork <upstream-owner>/<upstream-repo> --org prateek --fork-name <name>`
   (or `--clone=false` if you only need the remote). The fork holds your PR
   branches, `[branches]` work, and the CI-maintained `assembled` branch.
2. **Install the app on the fork.** Add `prateek/<upstream-fork>` to the
   `prateek-fork-automation` installation (see [Credentials](#credentials)).
   One click per fork; without it `publish` cannot push `assembled`.
3. **Scaffold the `<tool>/` dir and workflow** from `templates/`. One `sed` per
   file, delimiter absent from the values:

   | Template | Destination | Setup placeholders |
   |---|---|---|
   | `templates/fork.toml` | `<tool>/.fork/fork.toml` | `UPSTREAM_REPO` (real upstream `owner/repo`), `UPSTREAM_BRANCH`, `TRACK` (comma-separated PR numbers), `BUILD_COMMAND`, `BUILD_OUTPUT`, `SMOKE_COMMAND`, `KIND`, `TOOL` |
   | `templates/fork.yml` | `.github/workflows/<tool>.yml` | `TOOL`, `UPSTREAM_FORK_OWNER`, `UPSTREAM_FORK_NAME` (the `prateek/<fork>`), `RUNS_ON`, `CRON`, `BUILD_COMMAND`, `SMOKE_COMMAND`, `BUILD_OUTPUT`, `KIND`, `PACKAGING_PATH`, `REPLACES` |
   | `templates/formula.rb` or `cask.rb` | `<tool>/.fork/packaging.rb` | formula: `CLASS` (CamelCase of TOOL), `TOOL`, `FORK_REPO` (=`prateek/forks`), `BUILD_OUTPUT` · cask: `TOOL`, `APP_NAME`, `FORK_REPO` |

   `PACKAGING_PATH` is `Formula/<tool>-fork.rb` (formula) or `Casks/<tool>-fork.rb`
   (cask). `@URL@`, `@SHA256@`, `@VERSION@` are release-time — CI fills them per
   release; leave them alone. `CRON` must be a randomized daily minute off `:00`
   and `:30`, unique per fork, so the whole fleet doesn't wake at once. `RUNS_ON`
   is the Tartelet label list `[self-hosted, tartelet, homelab]` for macOS app
   builds, or `macos-latest` / `ubuntu-latest` as a fallback. After rendering,
   grep the outputs for surviving `@NAME@` placeholders and fail loudly if any
   remain.

4. **First assembly, locally** — proves the manifest and build config before CI:

   ```sh
   go -C engine run . --repo <tool>
   ( cd <tool>/.fork/work && <BUILD_COMMAND> && <SMOKE_COMMAND> )
   ```

   The engine fills `[upstream].sha` and writes `<tool>/.fork/lock.json`.

5. **Add the `src` gitlink** to the fork's `assembled` branch once it exists
   (after the first `publish`, or push `.fork/work` HEAD to `assembled` by hand
   the first time), then `git submodule add -b assembled
   https://github.com/prateek/<upstream-fork>.git <tool>/src`.
6. **Commit and push** `<tool>/`, the workflow, and the packaging fragment.
7. **Dispatch:** `gh workflow run <tool>.yml`, then `gh run watch`. Expect
   `no_op` (step 4 already synced) or `synced` with a release and a tap commit.
8. **Adopt in dotfiles** with the `fork-lifecycle` skill there, then
   `chezmoi apply`.

## Daily sync

Each tool's workflow runs from `workflow_dispatch` or its randomized cron. What
the three jobs do is in [AGENTS.md](../../../AGENTS.md#how-a-sync-runs). Nothing
to do by hand unless a run reports `conflict` (a needs-human self-issue lands on
this repo) — see [Resolve a paused conflict](#resolve-a-paused-conflict).

## Signing & Gatekeeper (macOS app forks)

App-cask forks are **ad-hoc signed, not notarized** — and that's enough, even on
a Jamf-managed Mac whose Gatekeeper is MDM-locked to "App Store & Known
Developers." Gatekeeper only blocks *quarantined* apps; a validly ad-hoc-signed,
non-quarantined app launches. Two rules make it work:

- **Build a valid ad-hoc signature.** `xcodebuild … CODE_SIGNING_ALLOWED=NO`
  leaves an unsigned bundle with no sealed resources — it launches as "damaged"
  (`code has no resources but signature indicates they must be present`) on *any*
  Mac. The build seals it with `codesign --force --deep --sign -` (in
  `templates/fork.yml`).
- **Strip quarantine in the cask, not with `--no-quarantine`.** brew
  re-quarantines on `upgrade` and dropped the install CLI flag, so the cask
  carries a `postflight` that runs `xattr -dr com.apple.quarantine`. It fires on
  every install/upgrade/reinstall, so plain `brew upgrade` keeps the fork
  launchable.

**Developer ID + notarization is deliberately avoided** — unnecessary given the
above, and Apple stalls new-account notary submissions "In Progress" for days (a
known 2026 issue). If a policy ever *requires* it, the sign/notarize job is in
git history (search `notarytool`); revive it only once the account is warmed up.
Notary status has no web UI — query it with an ES256 JWT against
`notary/v2/submissions` (no Xcode needed).

## Carry more work

Edit `<tool>/.fork/fork.toml` (or drop a file in `<tool>/patches/`), then push:

- **Upstream PR:** add its number to `[prs] track`.
- **Internal branch** on the fork: add a `[branches]` block (`url`, `track`).
- **Local patch:** drop `<tool>/patches/NNNN-name.patch` (applied in order).
- **Remote patch:** add a `[[patches.remote]]` entry with `url` + `sha256`.

The next sync folds it in. Anything that has landed upstream drops itself.

## Hack on the source

```sh
git submodule update --init <tool>/src   # detached HEAD at the assembled commit
```

Use that for browsing and as the daily driver. For **upstream PR work**, branch
from `upstream/<default>`, never from the assembled commit — `assembled` carries
every tracked PR and patch. Add the remote once
(`git -C <tool>/src remote add upstream https://github.com/<upstream>.git`),
cherry-pick from `assembled` when useful, push to `prateek/<upstream-fork>`, open
the upstream PR, then track it in `fork.toml`.

## Status

- Per-fork sync health: `gh run list --workflow <tool>.yml --limit 5`
- Escalations: `gh issue list --author app/prateek-fork-automation` (needs-human
  and retire self-issues; filter by author because the repo is public)
- What a fork carries: `[prs]`/`[branches]`/`[[patches.remote]]` in
  `<tool>/.fork/fork.toml` plus `<tool>/patches/`
- Installed vs latest: `brew info prateek/forks/<tool>-fork` against the latest
  `<tool>-v*` release
- Fleet at a glance: the **Fleet status** table in `README.md`, refreshed by
  `.github/workflows/fleet-digest.yml` (daily cron, and after every release)

## Local engine runs

Inside the monorepo checkout:

```sh
go -C engine run . --repo <tool>            # assemble
go -C engine run . --repo <tool> --resume   # after resolving + staging a conflict
go -C engine run . --repo <tool> --abort    # discard an in-flight assembly
```

### Resolve a paused conflict

On `conflict`, read `<tool>/.fork/work/.git/fork-conflict-prompt.md`, fix the
listed files, `git add` them, then `--resume`. Do **not** commit inside the
worktree — the engine finishes the merge or patch application itself, and harvests
the resolution into `<tool>/rerere/` so it replays next time. When CI can't
resolve (or the resolver declines), it files a needs-human self-issue and pushes
nothing; resolve locally and push.

## Retire a fork

Retirement is automatic: when every tracked PR/branch has landed and no patches
remain, a sync returns `retire`, and `lifecycle` writes `<tool>/.fork/retired`,
files a self-issue, and disables the workflow. To force it early, run the engine
and let it report `retire`, or `gh workflow disable <tool>.yml` and file the
issue by hand.

After the dotfiles `packages.toml` PR merges (via the `fork-lifecycle` skill) and
`chezmoi apply` swaps back to the official package, clean the monorepo: remove
`<tool>/`, `Formula|Casks/<tool>-fork.rb`, `.github/workflows/<tool>.yml`, and the
`.gitmodules` entry. Leave `prateek/<upstream-fork>` — it holds the PR history.

## Credentials

CI never talks to 1Password. Two apps, one vault, three GitHub secrets.

- **App `prateek-fork-automation`** — Contents: read and write, nothing else.
  Installed **only** on each `prateek/<upstream-fork>`, never on this repo,
  dotfiles, or `prateek/homebrew-tap`. `publish` mints a per-run token scoped to
  the one fork. A stolen key reaches public upstream forks and nothing else.
  Add a new fork to the installation:
  `gh api -X PUT /user/installations/<installation-id>/repositories/<repo-id>`
  or the Settings → Applications → Configure UI (one click).
- **App for Tartelet runner registration** — separate app, credentials live only
  in the mini's keychain; no workflow references them. `prateek/forks` must be in
  its installation so the minis can register as runners.
- **Vault `gh-prateek-fork-automation`** holds the GitHub App credentials and
  the Claude OAuth token (from `claude setup-token`). `sync-fork-secrets` pins
  its `op://` refs to vault/item/field **UUIDs** so a rename in 1Password can't
  silently break the sync.
- **Three GitHub secrets on `prateek/forks`**, referenced one-job-each:
  `CLAUDE_CODE_OAUTH_TOKEN` (resolve), `FORK_APP_ID` + `FORK_APP_PRIVATE_KEY`
  (publish). They are **repo-level** — every per-tool workflow shares them — so
  **adding a fork needs no new secret**, only the app-install click above.

Seeding and rotation both run one command, `scripts/sync-fork-secrets`. It reads
the three items with a 1Password **service account scoped read-only to the
`gh-prateek-fork-automation` vault**, and `gh secret set`s them; nothing is ever
printed. To rotate, edit the 1P item and re-run it.

```sh
# one-time: store the service-account token in the login keychain
security add-generic-password -a "$USER" -s op-fork-automation-sa -w
# seed or rotate (also accepts OP_SERVICE_ACCOUNT_TOKEN in the env instead)
scripts/sync-fork-secrets              # defaults to prateek/forks
```

The service-account token lives only in the keychain (or an env var) — never in
1Password itself (you can't fetch the unlock key from the thing it unlocks) and
never in CI.

## Developing the engine and templates

- Set the checkout up once: `mise trust && mise install`, then `mise run hooks`
  to install the prek pre-commit hook. `mise.toml` pins go/python/ruff/actionlint/
  shellcheck/prek so dev and CI share versions.
- `mise run test` runs the engine suite (`go -C engine test ./...`, txtar
  scenarios under `engine/testdata`). `mise run lint` runs every pre-commit hook
  (gofmt, go vet, actionlint, shellcheck, ruff, whitespace). CI runs both.
- Full loop against a real forge: `evals/forge-harness/harness` — `up`,
  `bootstrap`, `seed`, `engine`, `seed-conflict`, `down`, `clean`. It verifies
  the engine against Forgejo; the three-job CI shape is a real-GitHub property
  (see the harness header).
- After editing `templates/fork.yml`, re-render each rendered `<tool>.yml` and run
  `mise run lint` (actionlint runs there). `templates/fork.yml` itself isn't valid
  YAML until rendered, so the hooks skip it.
- **Record what you learn.** When you resolve a build or sync failure, append the
  durable lesson to `<tool>/learnings.md` (fork-specific) or the "Gotchas" below
  (cross-fork), so the next sync and the resolver don't relearn it.

## Gotchas (learned from ghost-pepper)

- **Re-run `sync-fork-secrets` after any vault edit.** GitHub secrets are a
  snapshot; renaming or replacing a vault item leaves CI on stale values — a 401
  that looks like bad creds but isn't. Refs are UUID-pinned so a rename doesn't
  break the *refs*, only the already-pushed *values* go stale.
- **Diagnose creds without Xcode:** an ES256 JWT (App Store Connect API key)
  against `/v1/apps` or `/notary/v2/submissions` tells you if the creds are valid
  vs. the environment (clock, stale secret) is the problem.
- **App build quirks surface only on the real mini build:** a gitignored
  `Secrets.swift` (copy the committed `Secrets.example` first); `@testable` tests
  need a Debug build; the release asset needs the `.app` at the tarball **root**
  (`tar -C "$(dirname OUT)" "$(basename OUT)"`). Prefer a build-succeeded smoke
  over the full test suite — network-dependent tests hang in the secrets-free
  build job. The mock-based reconcile test won't catch real-`brew` breakage
  (that's how the dropped `--no-quarantine` CLI flag slipped to a live apply).
- **The `src` gitlink needs a `.gitmodules` entry** or the next `checkout` fails
  with "No url found for submodule path"; `publish` writes it.
- **`publish` pushes to `main`,** so a local clone goes stale after every run —
  `git fetch && git rebase origin/main` before pushing more changes.
- **Heavy Xcode builds can wedge a mini** (OOM → SSH + runner heartbeat die); the
  build is `nice`d, `-jobs`-bounded, build-only-smoke, and short-timeout to avoid
  it. A networked smart plug / autoping PDU makes a headless mini self-recover.
- **The lint hooks need structural exclusions.** `templates/*.yml` isn't valid
  YAML — an `@VAR@` scalar starts with `@`, a YAML-reserved indicator — so
  check-yaml and actionlint skip `templates/`; render first, then lint. `patches/`
  and `rerere/` are whitespace-significant, so the whitespace hooks skip them.
  Re-apply these in `.pre-commit-config.yaml` if you add a hook that walks the
  whole tree.
