# Packaging for the KeyPath fork. keypath.yml fills https://github.com/prateek/forks/releases/download/keypath-v20260705.2.1/keypath-asset.tar.gz/d9625ac0c63e49969be3034cc1456d3b50bfebb240310ed6757c7364808e2b48/20260705.2.1 from
# each release and pushes the result to the tap. Unlike the ad-hoc-signed forks,
# KeyPath is signed with a real Developer ID: its privileged helper registers via
# SMAppService.register(), which fails (-67028) without a Developer-ID signature.
# It is deliberately NOT notarized, so Gatekeeper still blocks a quarantined copy —
# the postflight strips quarantine on every install/upgrade, same as the ad-hoc forks.
cask "keypath-fork" do
  version "20260705.2.1"
  sha256 "d9625ac0c63e49969be3034cc1456d3b50bfebb240310ed6757c7364808e2b48"

  url "https://github.com/prateek/forks/releases/download/keypath-v20260705.2.1/keypath-asset.tar.gz"
  name "KeyPath (fork build)"
  desc "Downstream fork build of KeyPath (auto-built by prateek/forks)"
  homepage "https://github.com/prateek/forks"

  app "KeyPath.app"

  postflight do
    system_command "/usr/bin/xattr",
                   args: ["-dr", "com.apple.quarantine", "#{appdir}/KeyPath.app"]
  end

  caveats <<~EOS
    KeyPath installs a privileged helper and a kanata LaunchDaemon via SMAppService.
    On first launch, approve them in System Settings → General → Login Items & Extensions.
  EOS
end
