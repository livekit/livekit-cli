# typed: false
# frozen_string_literal: true

# This formula is meant for a custom Homebrew tap (e.g. livekit/homebrew-livekit).
# It installs a prebuilt binary with console support (PortAudio + WebRTC AEC).
# Usage: brew install livekit/livekit/lk
class Lk < Formula
  desc "Command-line interface to LiveKit (with console support)"
  homepage "https://livekit.io"
  license "Apache-2.0"
  version "VERSION"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/livekit/livekit-cli/releases/download/vVERSION/lk_VERSION_darwin_arm64.tar.gz"
      sha256 "SHA256_DARWIN_ARM64"
    end
  end

  on_linux do
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/livekit/livekit-cli/releases/download/vVERSION/lk_VERSION_linux_arm64.tar.gz"
      sha256 "SHA256_LINUX_ARM64"
    elsif Hardware::CPU.arm?
      url "https://github.com/livekit/livekit-cli/releases/download/vVERSION/lk_VERSION_linux_arm.tar.gz"
      sha256 "SHA256_LINUX_ARM"
    else
      url "https://github.com/livekit/livekit-cli/releases/download/vVERSION/lk_VERSION_linux_amd64.tar.gz"
      sha256 "SHA256_LINUX_AMD64"
    end
  end

  def install
    bin.install "lk"
    bin.install_symlink "lk" => "livekit-cli"

    bash_completion.install "autocomplete/bash_autocomplete" => "lk"
    fish_completion.install "autocomplete/fish_autocomplete" => "lk.fish"
    zsh_completion.install "autocomplete/zsh_autocomplete" => "_lk"
  end

  test do
    output = shell_output("#{bin}/lk token create --list --api-key key --api-secret secret")
    assert_match "valid for (mins):  5", output
    assert_match "lk version #{version}", shell_output("#{bin}/lk --version")
  end
end
