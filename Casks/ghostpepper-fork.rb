# Packaging template for an app fork. Setup fills ghostpepper/GhostPepper/
# prateek/forks and commits this as .fork/packaging.rb; fork.yml fills
# https://github.com/prateek/forks/releases/download/ghostpepper-v20260705.14.1/ghostpepper-asset.tar.gz/f15e9e1a18d3bbe438468441f935febfbc68be5633af828e9413235a0c8b59b1/20260705.14.1 from each release and pushes the result to the
# tap. Fork app builds are ad-hoc signed, so the cask strips quarantine in a
# postflight (Gatekeeper only blocks quarantined apps). It runs on every
# install/upgrade/reinstall, so plain `brew upgrade` keeps the fork launchable.
cask "ghostpepper-fork" do
  version "20260705.14.1"
  sha256 "f15e9e1a18d3bbe438468441f935febfbc68be5633af828e9413235a0c8b59b1"

  url "https://github.com/prateek/forks/releases/download/ghostpepper-v20260705.14.1/ghostpepper-asset.tar.gz"
  name "GhostPepper (fork build)"
  desc "Downstream fork build of GhostPepper (auto-built by prateek/forks)"
  homepage "https://github.com/prateek/forks"

  app "GhostPepper.app"

  postflight do
    system_command "/usr/bin/xattr",
                   args: ["-dr", "com.apple.quarantine", "#{appdir}/GhostPepper.app"]
  end
end
