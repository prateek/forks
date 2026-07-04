# Packaging template for an app fork. Setup fills @TOOL@/@APP_NAME@/
# @FORK_REPO@ and commits this as .fork/packaging.rb; fork.yml fills
# @URL@/@SHA256@/@VERSION@ from each release and pushes the result to the
# tap. Fork app builds are ad-hoc signed; the reconciler installs with
# --no-quarantine.
cask "@TOOL@-fork" do
  version "@VERSION@"
  sha256 "@SHA256@"

  url "@URL@"
  name "@APP_NAME@ (fork build)"
  desc "Downstream fork build of @APP_NAME@ (auto-built by @FORK_REPO@)"
  homepage "https://github.com/@FORK_REPO@"

  app "@APP_NAME@.app"
end
