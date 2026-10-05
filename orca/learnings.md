# orca — fork learnings

Durable, fork-specific gotchas for building and syncing the Orca iOS fork.
Cross-fork lessons belong in the `fork-ops` skill's "Gotchas" instead. Apple
account, certificate and TestFlight operations live in the infra repo's
`services/fork-fleet/README.md`.

## Why this fork exists

Orca Mobile (stablyai/orca, `mobile/`) ships under upstream's bundle id
`com.stably.orca.mobile` and Apple team. This fork is upstream plus Prateek's
patches, delivered to his own devices through his own internal TestFlight group.
The identity patches never upstream, so the fork never auto-retires. Only the iOS
app is built; desktop and Android are untouched.

## What the local patches change

- `0001` — bundle id `com.prateek.orca.mobile` and `CFBundleDisplayName` "Orca
  Fork". `expo.name` stays `Orca` on purpose: Expo derives the Xcode workspace,
  scheme and `.app` names from it, and the workflow addresses `Orca.xcworkspace`,
  scheme `Orca` and `Orca.app`.
- `0002` — iOS remote push off. Upstream's push gateway holds upstream's APNs
  credentials, so a token from this bundle id is undeliverable. `app.config.js`
  drops the `expo-notifications` config plugin so no `aps-environment`
  entitlement is generated (an entitlement the profile lacks fails signing), and
  `push-token.ts` returns no iOS token so nothing registers with a host.
- `0003` — the iOS update check is off. It reads upstream's App Store listing,
  which says nothing about a TestFlight install under another bundle id.

Left as upstream: the `orca://` URL scheme. With the App Store app also
installed, iOS picks one of the two for an `orca://pair` link; pair with the
in-app QR scanner or paste field, which never go through the scheme.
`ProtocolBlockScreen` still links to upstream's App Store page when the desktop
demands a newer mobile app; the fix there is a fresh fork build.

## Build

- The pipeline is pnpm install → `expo prebuild --platform ios --no-install` →
  `pod-install` → `xcodebuild archive`. No EAS.
- Expo SDK 55 needs the Swift 6 toolchain: `macos-26` with Xcode 26.5, the pins
  upstream's own release workflow uses. Node 24, pnpm via corepack from
  `packageManager`.
- `EXPO_PUBLIC_MOBILE_SHELL=native` bundles the patched screens. The `ota` shell
  loads pages delivered by upstream at run time, which would bypass the patches.
- The build number is `run_number * 100 + run_attempt`, written into `app.json`
  before prebuild. Upstream asks App Store Connect for the next number; taking it
  from the run keeps every credential out of the build job. The marketing
  version stays upstream's.
- The archive is built with `CODE_SIGNING_ALLOWED=NO`. The sign job runs
  `xcodebuild -exportArchive` with manual signing on it, so upstream's Fastfile
  and its hardcoded bundle id are never used.
- The state artifact is about 1 GB because it carries the full upstream git
  history; publish needs that history to push `assembled`.

## Publish

- Upstream ships dozens of workflow files, and the fork-automation app is
  contents-only: GitHub rejects a push that creates or updates workflow files
  from a token without the `workflows` permission. Publish strips
  `.github/workflows` from `assembled` before pushing, which also keeps
  upstream's CI from running on the fork.
- The release carries no asset. It records which assembled commit became which
  TestFlight version and build.
