# Packaging template for an app fork. Setup fills ghostpepper/GhostPepper/
# prateek/forks and commits this as .fork/packaging.rb; fork.yml fills
# https://github.com/prateek/forks/releases/download/ghostpepper-v20260704.10.1/ghostpepper-asset.tar.gz/43da9ce47b0852f947fd9ccbfce44dfe5c6c351b48859f4af1cff29b539bc564/20260704.10.1 from each release and pushes the result to the
# tap. Fork app builds are ad-hoc signed; the reconciler installs with
# --no-quarantine.
cask "ghostpepper-fork" do
  version "20260704.10.1"
  sha256 "43da9ce47b0852f947fd9ccbfce44dfe5c6c351b48859f4af1cff29b539bc564"

  url "https://github.com/prateek/forks/releases/download/ghostpepper-v20260704.10.1/ghostpepper-asset.tar.gz"
  name "GhostPepper (fork build)"
  desc "Downstream fork build of GhostPepper (auto-built by prateek/forks)"
  homepage "https://github.com/prateek/forks"

  app "GhostPepper.app"
end
