# Packaging template for an app fork. Setup fills ghostpepper/GhostPepper/
# prateek/forks and commits this as .fork/packaging.rb; fork.yml fills
# https://github.com/prateek/forks/releases/download/ghostpepper-v20260704.7.1/ghostpepper-asset.tar.gz/1906940b1cb243b36e3f921b06db4d5a2052a43ec4798d7eb5f223a3744006a8/20260704.7.1 from each release and pushes the result to the
# tap. Fork app builds are ad-hoc signed; the reconciler installs with
# --no-quarantine.
cask "ghostpepper-fork" do
  version "20260704.7.1"
  sha256 "1906940b1cb243b36e3f921b06db4d5a2052a43ec4798d7eb5f223a3744006a8"

  url "https://github.com/prateek/forks/releases/download/ghostpepper-v20260704.7.1/ghostpepper-asset.tar.gz"
  name "GhostPepper (fork build)"
  desc "Downstream fork build of GhostPepper (auto-built by prateek/forks)"
  homepage "https://github.com/prateek/forks"

  app "GhostPepper.app"
end
