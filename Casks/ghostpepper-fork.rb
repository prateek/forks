# Packaging template for an app fork. Setup fills ghostpepper/GhostPepper/
# prateek/forks and commits this as .fork/packaging.rb; fork.yml fills
# https://github.com/prateek/forks/releases/download/ghostpepper-v20260707.16.1/ghostpepper-asset.tar.gz/a75be23db4200ef65ff1cc9b9901da6970518ee489d294e81b954bdb429160ca/20260707.16.1 from each release and pushes the result to the
# tap. Fork app builds are ad-hoc signed, so the cask strips quarantine in a
# postflight (Gatekeeper only blocks quarantined apps). It runs on every
# install/upgrade/reinstall, so plain `brew upgrade` keeps the fork launchable.
cask "ghostpepper-fork" do
  version "20260707.16.1"
  sha256 "a75be23db4200ef65ff1cc9b9901da6970518ee489d294e81b954bdb429160ca"

  url "https://github.com/prateek/forks/releases/download/ghostpepper-v20260707.16.1/ghostpepper-asset.tar.gz"
  name "GhostPepper (fork build)"
  desc "Downstream fork build of GhostPepper (auto-built by prateek/forks)"
  homepage "https://github.com/prateek/forks"

  app "GhostPepper.app"

  postflight do
    system_command "/usr/bin/xattr",
                   args: ["-dr", "com.apple.quarantine", "#{appdir}/GhostPepper.app"]
  end
end
