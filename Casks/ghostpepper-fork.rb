# Packaging template for an app fork. Setup fills ghostpepper/GhostPepper/
# prateek/forks and commits this as .fork/packaging.rb; fork.yml fills
# https://github.com/prateek/forks/releases/download/ghostpepper-v20260723.32.1/ghostpepper-asset.tar.gz/0e4193f2311a5548652c6f663d05915da1868e8c34adb120673dc6fa8f639530/20260723.32.1 from each release and pushes the result to the
# tap. Fork app builds are ad-hoc signed, so the cask strips quarantine in a
# postflight (Gatekeeper only blocks quarantined apps). It runs on every
# install/upgrade/reinstall, so plain `brew upgrade` keeps the fork launchable.
cask "ghostpepper-fork" do
  version "20260723.32.1"
  sha256 "0e4193f2311a5548652c6f663d05915da1868e8c34adb120673dc6fa8f639530"

  url "https://github.com/prateek/forks/releases/download/ghostpepper-v20260723.32.1/ghostpepper-asset.tar.gz"
  name "GhostPepper (fork build)"
  desc "Downstream fork build of GhostPepper (auto-built by prateek/forks)"
  homepage "https://github.com/prateek/forks"

  app "GhostPepper.app"

  postflight do
    system_command "/usr/bin/xattr",
                   args: ["-dr", "com.apple.quarantine", "#{appdir}/GhostPepper.app"]
  end
end
