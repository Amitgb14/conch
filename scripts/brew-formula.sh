#!/bin/sh
# Prints the Homebrew formula for a release, from the checksums.txt that
# scripts/release.sh wrote beside its archives:
#   scripts/brew-formula.sh VERSION dist/checksums.txt > Formula/conch.rb
# The release workflow commits it to the tap (Amitgb14/homebrew-conch), so
#   brew install Amitgb14/conch/conch
# installs the same archives install.sh does.
set -eu

version=${1:?usage: scripts/brew-formula.sh VERSION CHECKSUMS}
sums=${2:?usage: scripts/brew-formula.sh VERSION CHECKSUMS}
version=${version#v}
[ -r "$sums" ] || { echo "brew-formula: cannot read $sums" >&2; exit 1; }
base="https://github.com/Amitgb14/conch/releases/download/v$version"

# sha prints the checksum of one platform's archive, or fails naming it.
sha() {
	asset="conch_${version}_$1.tar.gz"
	s=$(awk -v a="$asset" '$2 == a || $2 == "*"a { print $1 }' "$sums")
	[ -n "$s" ] || { echo "brew-formula: $asset is not in $sums" >&2; exit 1; }
	[ ${#s} -eq 64 ] && echo "$s" | grep -Eqx '[0-9a-f]{64}' || { echo "brew-formula: $asset: bad checksum $s" >&2; exit 1; }
	echo "$s"
}

darwin_arm64=$(sha darwin_arm64)
darwin_amd64=$(sha darwin_amd64)
linux_arm64=$(sha linux_arm64)
linux_amd64=$(sha linux_amd64)

cat <<EOF
# Written by scripts/brew-formula.sh in Amitgb14/conch for each release.
class Conch < Formula
  desc "Terminal orchestrator for AI coding agents"
  homepage "https://amitgb14.github.io/conch/"
  version "$version"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "$base/conch_${version}_darwin_arm64.tar.gz"
      sha256 "$darwin_arm64"
    end
    on_intel do
      url "$base/conch_${version}_darwin_amd64.tar.gz"
      sha256 "$darwin_amd64"
    end
  end

  on_linux do
    on_arm do
      url "$base/conch_${version}_linux_arm64.tar.gz"
      sha256 "$linux_arm64"
    end
    on_intel do
      url "$base/conch_${version}_linux_amd64.tar.gz"
      sha256 "$linux_amd64"
    end
  end

  def install
    bin.install "conch"
  end

  def caveats
    <<~TEXT
      Start it with: conch
      Update it with brew upgrade conch, then press u in conch (or run
      conch server reload): the server moves onto the new build and your
      agents keep running.
    TEXT
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/conch version")
  end
end
EOF
