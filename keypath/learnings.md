# keypath — fork learnings

Durable, fork-specific gotchas for building and syncing keypath. Cross-fork
lessons belong in the `fork-ops` skill's "Gotchas" instead.

## Why this fork exists

KeyPath (malpern/KeyPath) is signed against upstream's team `X2RKZ5TG99`. To run it
on Prateek's machines with his own Developer ID (team `2ZB5WTYXC6`), the app↔helper
mutual code-signing trust must be re-pointed. That's the `patches/0001` team-id patch —
a permanent local divergence that never upstreams, so this fork never auto-retires.
There are no tracked upstream PRs.

## Signing (the hard part)

- **KeyPath needs a real Developer ID, not ad-hoc.** Its privileged helper registers
  via `SMAppService.daemon(...).register()`, which fails `-67028` (`errSecCSReqFailed`,
  "codesigning failure loading plist") when the helper isn't Developer-ID-signed to a
  team matching the app's `SMPrivilegedExecutables` requirement. Verified on a macOS 26
  mini: Developer-ID-signed + the team-id patch → `register()` succeeds
  (status `requiresApproval`, no `-67028`); **notarization is NOT required** for
  registration. Gatekeeper (`spctl`) still rejects the unnotarized app on launch — the
  cask strips quarantine in a postflight, same as the ad-hoc forks.

- **The team id must be QUOTED in requirement strings.** `2ZB5WTYXC6` starts with a
  digit, which is not a valid bareword in the code-requirement language; `subject.OU = 2ZB5WTYXC6`
  is a hard syntax error (`csreq` / `SecRequirementCreateWithString` reject it).
  `patches/0001` writes `subject.OU = "2ZB5WTYXC6"` in Info.plist, the helper's
  DEBUG/RELEASE `SecRequirement` strings, and the lint test. `X2RKZ5TG99` was a legal
  bareword only because it starts with a letter. The `HelperTrustContractTests`
  requirement-parse test is the guardrail that catches a bareword.

- **Signing is structural, so the CI trust split uses a sign-only mode.**
  `build-and-sign.sh` signs the bundle inside-out (helper w/ `com.keypath.helper` +
  its entitlements, Kanata Engine.app, launcher, host-bridge dylib, simulator,
  `keypath-cli` w/ `com.keypath.KeyPath.CLI`, Insights, Sparkle, then the app w/
  `KeyPath.entitlements`). A generic `codesign --deep` can't reproduce it. `patches/0002`
  adds `KEYPATH_SIGN_ONLY=1`: the secrets-free build job builds UNSIGNED
  (`SKIP_CODESIGN=1`), and the sign job re-runs `build-and-sign.sh` in sign-only mode on
  the opaque prebuilt bundle so the Developer ID cert never shares a runner with the
  Rust/Swift build. `SKIP_DEPLOY=1` keeps both jobs non-interactive. The sign-only mode
  also skips the `find-identity -v` precheck (a throwaway CI keychain reports the cert
  invalid under `-v` even though codesign signs fine).
  **Trust nuance:** the sign job DOES run upstream's signing shell (build-and-sign.sh +
  lib/signing.sh + the entitlements files) with the cert present — only the codesign
  pass, never the Rust/Swift build. This is a narrower boundary than the ad-hoc forks
  ("no upstream code with secrets"); accepted for a personal daily-driver fork, contained
  by the ephemeral Tart VM + keychain scrub.

## Build

- SwiftPM `swift build --configuration release`, NOT xcodebuild/xcodegen.
- kanata builds from the `External/kanata` submodule (malpern/kanata @ keypath/bundled)
  via cargo; ARM64-only (`build/kanata-universal` is a misnomer). The build job installs
  rustup if absent (the Tahoe-Xcode runner image ships Swift/Xcode but not Rust) and
  runs `git submodule update --init External/kanata` first.
- Build flags for CI: `SKIP_CODESIGN=1 SKIP_NOTARIZE=1 SKIP_SPARKLE=1 SKIP_SNAPSHOTS=1
  SKIP_DEPLOY=1` (build job) then `KEYPATH_SIGN_ONLY=1 SKIP_NOTARIZE=1 SKIP_DEPLOY=1`
  (sign job). Sparkle EdDSA / create-dmg / xcodegen are unneeded with these skips.
- A cold build is Rust + Swift release + Sparkle — heavier than the ad-hoc forks; the
  build job timeout is 60m. Two heavy VMs on a 16 GB mini hit memory pressure (a local
  validation VM was reaped mid-run), so keep concurrency to one heavy build per host.

## Publish / assembled branch

- **Upstream ships `.github/workflows/`, the fork-automation app is contents-only.**
  The first `publish` failed: GitHub rejects a push that creates/updates workflow
  files from an app token without `workflows` permission. The app is deliberately
  contents-only (ADR 0015), so `keypath.yml`'s publish strips `.github/workflows/`
  from the assembled branch before pushing (it's also better not to run malpern's CI
  on Prateek's fork). Any future fork whose upstream has workflow files needs the same
  handling — a candidate for `templates/fork.yml` if a second one shows up.

## Post-install (user step, not the cask)

SMAppService daemons land in `.requiresApproval`; the user approves the helper and the
kanata LaunchDaemon once in System Settings → General → Login Items & Extensions.
