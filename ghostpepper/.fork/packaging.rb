# Packaging template for an app fork. Setup fills ghostpepper/GhostPepper/
# prateek/forks and commits this as .fork/packaging.rb; fork.yml fills
# @URL@/@SHA256@/@VERSION@ from each release and pushes the result to the
# tap. Fork app builds are ad-hoc signed; the reconciler installs with
# --no-quarantine.
cask "ghostpepper-fork" do
  version "@VERSION@"
  sha256 "@SHA256@"

  url "@URL@"
  name "GhostPepper (fork build)"
  desc "Downstream fork build of GhostPepper (auto-built by prateek/forks)"
  homepage "https://github.com/prateek/forks"

  app "GhostPepper.app"
end
