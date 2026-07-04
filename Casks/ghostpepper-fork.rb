# Packaging template for an app fork. Setup fills ghostpepper/GhostPepper/
# prateek/forks and commits this as .fork/packaging.rb; fork.yml fills
# https://github.com/prateek/forks/releases/download/ghostpepper-v20260704.5.1/ghostpepper-asset.tar.gz/74533be0906e894d4b922255f5af5c52ebd77626039cf48095a40514384e9ea8/20260704.5.1 from each release and pushes the result to the
# tap. Fork app builds are ad-hoc signed; the reconciler installs with
# --no-quarantine.
cask "ghostpepper-fork" do
  version "20260704.5.1"
  sha256 "74533be0906e894d4b922255f5af5c52ebd77626039cf48095a40514384e9ea8"

  url "https://github.com/prateek/forks/releases/download/ghostpepper-v20260704.5.1/ghostpepper-asset.tar.gz"
  name "GhostPepper (fork build)"
  desc "Downstream fork build of GhostPepper (auto-built by prateek/forks)"
  homepage "https://github.com/prateek/forks"

  app "GhostPepper.app"
end
