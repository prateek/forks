# Agent Notes — prateek/forks

This repo is the **fleet monorepo and Homebrew tap** for Prateek's daily-driver
forks. Each fork keeps "upstream + my open PRs (+ local patches)" installable and
current with almost no hand-holding: CI re-assembles upstream, drops patches as
they land, and retires a fork when nothing is left to carry.

Rationale and threat model live in `prateek/dotfiles`
(`docs/adr/0015-downstream-fork-daily-driver.md`). The operator playbook is
[`.agents/skills/fork-ops/SKILL.md`](.agents/skills/fork-ops/SKILL.md).

## Layout

```
engine/                       shared Go sync engine (one copy, every fork)
templates/                    fork.yml / fork.toml / formula / cask templates
<tool>/.fork/fork.toml        per-fork manifest (upstream, PRs, branches, patches)
<tool>/.fork/lock.json        pinned heads/digests from the last good assembly
<tool>/.fork/packaging.rb     rendered formula/cask fragment
<tool>/patches/*.patch        local patches, applied in manifest order
<tool>/rerere/                recorded conflict resolutions, replayed on later syncs
<tool>/src                    gitlink -> prateek/<upstream> @ assembled
<tool>/learnings.md           durable fork-specific build/sync gotchas
.github/workflows/<tool>.yml  one rendered workflow per fork
Formula/  Casks/              the tap; <tool>-fork.rb points at release assets
```

`<tool>/.fork/work/` and `<tool>/.fork/assemble.lock` are assembly scratch and are
gitignored. The engine never runs git against the outer monorepo — the workflow
stages `<tool>/...` paths explicitly and commits.

## The engine

`engine/` is functional-core / imperative-shell Go. It assembles the carried tree
and returns exactly one result: `no_op`, `synced`, `conflict`, or `retire`.

- `go -C engine test ./...` runs the txtar scenarios under `engine/testdata`.
- Local run inside a fork dir: `go -C engine run . --repo <tool>` to assemble,
  `--resume` after resolving a paused conflict, `--abort` to discard an
  in-flight assembly.
- On `conflict` the engine writes `<tool>/.fork/work/.git/fork-conflict-prompt.md`
  listing the files to fix. Resolve them, `git add`, then `--resume`. Never commit
  inside the worktree; the engine finishes the merge or patch application itself.
  Resolutions are harvested into `rerere/` and replay on later syncs.

## Local development

`mise.toml` pins the toolchain (go, python, ruff, actionlint, shellcheck, prek).
Set a checkout up once:

```
mise trust && mise install    # go/python/lint at pinned versions
mise run hooks                # install the prek pre-commit hook
```

`mise run test` runs the engine suite; `mise run lint` runs every pre-commit hook
(gofmt, go vet, actionlint, shellcheck, ruff, whitespace) over the tree. CI
(`.github/workflows/ci.yml`) runs the same two. The macOS build job is the one
non-hermetic edge: it carries no repo checkout, so it installs xcodegen via brew
at build time rather than from mise.

When you resolve a build or sync failure for a fork, append the durable lesson —
fork-specific ones to `<tool>/learnings.md`, cross-fork ones to the `fork-ops`
skill's "Gotchas". They exist so the next sync and the resolver don't relearn
the same thing.

## Sources a fork can carry

All four live in `<tool>/.fork/fork.toml` (plus `patches/`) and fold into the same
drift / lock / retire / rerere machinery:

- `[prs] track = [...]` — upstream PR heads, merged in list order. `author = "<login>"`
  auto-discovery is the opt-in alternative; a fork takes exactly one of the two.
- `[branches] track = [...]` — internal branches on `prateek/<upstream>`, merged
  after PRs; auto-dropped when the diff reverse-applies to pristine upstream.
- `patches/*.patch` — local patches, applied after merges.
- `[[patches.remote]]` `{url, sha256}` — curl-able patches, sha-pinned; a mismatch
  is a loud failure, never an apply.

A PR, branch, or patch that has landed upstream drops automatically. When nothing
is left to carry, the fork retires itself.

## How a sync runs

One workflow per tool (`.github/workflows/<tool>.yml`), triggered by
`workflow_dispatch` or a randomized daily cron (off :00/:30, one minute per fork).
`concurrency: {group: fork-<tool>, cancel-in-progress: false}` keeps at most one
run plus one pending; a dropped cron tick is fine because every run reconverges
from scratch. Three jobs hand off via tar artifacts so untrusted upstream code
never shares a runner with credentials:

1. `resolve` (hosted ubuntu, `CLAUDE_CODE_OAUTH_TOKEN` only, read-only token) —
   assemble; on conflict, run `claude-sonnet-5` resume rounds under a narrow git
   allowlist. Exits `no_op` fast when nothing changed. Emits the state sha.
2. `build` (Tartelet mini for macOS forks, hosted otherwise; **no secrets**,
   `permissions: {}`) — run BUILD + SMOKE on the assembled tree, and ad-hoc-sign
   app casks (`codesign --force --deep --sign -`). Upstream code executes only
   here. Forks are ad-hoc signed, not notarized; the cask strips quarantine in a
   `postflight` so they launch even on a Jamf Mac. See the `fork-ops` skill's
   "Signing & Gatekeeper" for why, and the notarization escape hatch.
3. `publish` (hosted ubuntu, app token) — verify the state sha against resolve's
   output, push `assembled` + `assembled-<tag>` to `prateek/<upstream>`, record
   the `src` gitlink, cut the release with the built asset, commit the
   formula/cask. Default-token pushes do not retrigger workflows.

`lifecycle` handles the conflict path (a needs-human self-issue) and the retire
path (marker, self-issue, `gh workflow disable`).

## Credentials

CI never talks to 1Password. Three GitHub secrets on this repo, referenced
one-job-each:

- `CLAUDE_CODE_OAUTH_TOKEN` — `resolve` only.
- `FORK_APP_ID` + `FORK_APP_PRIVATE_KEY` — `publish` only; mint a contents-only
  token scoped to `prateek/<upstream>`.

The `prateek-fork-automation` app is installed **only** on the upstream forks —
never on this repo, dotfiles, or the old homebrew-tap. A stolen app key reaches
public upstream forks and nothing else. `op read | gh secret set` from vault
`gh-prateek-fork-automation` is the only path values reach CI, at creation or
rotation time. The per-fork installation click and rotation steps are in the
[`fork-ops` skill](.agents/skills/fork-ops/SKILL.md).

## The tap

This repo is its own tap:

```sh
brew tap prateek/forks https://github.com/prateek/forks
brew install prateek/forks/<tool>-fork
```

The explicit URL is required because the repo is not named `homebrew-*`.
Formula/cask URLs point at uploaded **release assets**, never GitHub's
auto-generated source tarballs, which would be empty of submodule content.
`prateek/homebrew-tap` stays for non-fork packages only.

## Adopt / retire is manual in dotfiles

This repo publishes fork state; it does not touch dotfiles and holds no dotfiles
credential. Adopting or retiring a `<tool>-fork` package in `prateek/dotfiles`
`packages.toml` is an ordinary human/agent PR via the `fork-lifecycle` skill
there. `chezmoi apply` then swaps the install either way.
