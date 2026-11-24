#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 3 ]; then
  echo "Usage: $0 <version> <binary-path> <output-dir>" >&2
  exit 1
fi

VERSION="$1"
BIN_PATH="$2"
OUT_DIR="$3"
ARCH="amd64"
PKG_NAME="mic-relay"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

PKG_ROOT="${WORK_DIR}/${PKG_NAME}_${VERSION}_${ARCH}"
mkdir -p "${PKG_ROOT}/DEBIAN"
mkdir -p "${PKG_ROOT}/usr/bin"
mkdir -p "${PKG_ROOT}/usr/share/applications"
mkdir -p "${PKG_ROOT}/usr/share/icons/hicolor/scalable/apps"

install -m 0755 "${BIN_PATH}" "${PKG_ROOT}/usr/bin/mic-relay"
install -m 0644 "${SCRIPT_DIR}/mic-relay.desktop" "${PKG_ROOT}/usr/share/applications/mic-relay.desktop"
install -m 0644 "${SCRIPT_DIR}/icon.svg" "${PKG_ROOT}/usr/share/icons/hicolor/scalable/apps/mic-relay.svg"

cat > "${PKG_ROOT}/DEBIAN/control" <<EOF
Package: ${PKG_NAME}
Version: ${VERSION}
Section: sound
Priority: optional
Architecture: ${ARCH}
Maintainer: Mic Relay Maintainers <noreply@example.com>
Depends: libc6 (>= 2.36), libgl1, libx11-6, libxcursor1, libxrandr2, libxinerama1, libxi6, libxxf86vm1, libwayland-client0, libwayland-cursor0, libwayland-egl1, libxkbcommon0
Recommends: pipewire-bin, pipewire-pulse | pulseaudio, fonts-noto-cjk | fonts-ipafont-gothic
Description: Bidirectional low-latency microphone relay for Linux (PipeWire) and Windows (VB-Cable / VRChat)
 Mic Relay streams microphone audio over LAN or Tailscale with optional
 Pure-Go RNNoise noise suppression, PCM/Opus codec switching, adaptive
 clock-drift jitter buffering, and system tray minimization.
EOF

mkdir -p "${OUT_DIR}"
OUT_DEB="${OUT_DIR%/}/${PKG_NAME}_${VERSION}_${ARCH}.deb"
dpkg-deb --root-owner-group --build "${PKG_ROOT}" "${OUT_DEB}"
echo "Built ${OUT_DEB}"
