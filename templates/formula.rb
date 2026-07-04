# Packaging template for a CLI fork. Setup fills @CLASS@/@TOOL@/@FORK_REPO@/
# @BUILD_OUTPUT@ and commits this as .fork/packaging.rb; fork.yml fills
# @URL@/@SHA256@/@VERSION@ from each release and pushes the result to the tap.
class @CLASS@Fork < Formula
  desc "Downstream fork build of @TOOL@ (auto-built by @FORK_REPO@)"
  homepage "https://github.com/@FORK_REPO@"
  url "@URL@"
  sha256 "@SHA256@"
  version "@VERSION@"

  def install
    # The release asset holds the build output at its root by basename.
    bin.install File.basename("@BUILD_OUTPUT@") => "@TOOL@"
  end

  test do
    system "#{bin}/@TOOL@", "--version"
  end
end
