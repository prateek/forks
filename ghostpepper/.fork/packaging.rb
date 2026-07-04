# Packaging template for an app fork. Setup fills ghostpepper/GhostPepper/
# prateek/forks and commits this as .fork/packaging.rb; fork.yml fills
# @URL@/@SHA256@/@VERSION@ from each release and pushes the result to the
# tap. Fork app builds are ad-hoc signed, so the cask strips quarantine in a
# postflight (Gatekeeper only blocks quarantined apps). It runs on every
# install/upgrade/reinstall, so plain `brew upgrade` keeps the fork launchable.
cask "ghostpepper-fork" do
  version "@VERSION@"
  sha256 "@SHA256@"

  url "@URL@"
  name "GhostPepper (fork build)"
  desc "Downstream fork build of GhostPepper (auto-built by prateek/forks)"
  homepage "https://github.com/prateek/forks"

  app "GhostPepper.app"

  postflight do
    system_command "/usr/bin/xattr",
                   args: ["-dr", "com.apple.quarantine", "#{appdir}/GhostPepper.app"]
  end
end
