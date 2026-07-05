# Packaging for the KeyPath fork. keypath.yml fills https://github.com/prateek/forks/releases/download/keypath-v20260705.3.1/keypath-asset.tar.gz/66cf347c840635f4ec47ecee6469cba3db1d2fbf9dd11da3db2c5d7ef6c0a7e1/20260705.3.1 from
# each release and pushes the result to the tap. Unlike the ad-hoc-signed forks,
# KeyPath is signed with a real Developer ID: its privileged helper registers via
# SMAppService.register(), which fails (-67028) without a Developer-ID signature.
# It is deliberately NOT notarized, so Gatekeeper still blocks a quarantined copy —
# the postflight strips quarantine on every install/upgrade, same as the ad-hoc forks.
cask "keypath-fork" do
  version "20260705.3.1"
  sha256 "66cf347c840635f4ec47ecee6469cba3db1d2fbf9dd11da3db2c5d7ef6c0a7e1"

  url "https://github.com/prateek/forks/releases/download/keypath-v20260705.3.1/keypath-asset.tar.gz"
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
