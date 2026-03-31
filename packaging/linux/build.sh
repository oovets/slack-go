#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
PKG_DIR="${ROOT_DIR}/packaging/linux"
VERSION=""
APPIMAGE_ONLY=0

usage() {
  cat <<EOF
Usage: $(basename "$0") --version <semver> [--appimage-only]

Options:
  --version <semver>  Release version (required)
  --appimage-only     Build only tarball/AppDir assets
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --appimage-only)
      APPIMAGE_ONLY=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ -z "${VERSION}" ]]; then
  echo "Missing required --version" >&2
  usage
  exit 1
fi

mkdir -p "${DIST_DIR}"

echo "==> Building binary"
(cd "${ROOT_DIR}" && go build -trimpath -ldflags "-s -w" -o "${DIST_DIR}/slack-gui" ./cmd/slack-gui)

if [[ ! -f "${PKG_DIR}/icons/256x256/slack-gui.png" ]]; then
  echo "warning: missing icon ${PKG_DIR}/icons/256x256/slack-gui.png"
fi
if [[ ! -f "${PKG_DIR}/icons/512x512/slack-gui.png" ]]; then
  echo "warning: missing icon ${PKG_DIR}/icons/512x512/slack-gui.png"
fi

echo "==> Creating tar.gz"
(cd "${DIST_DIR}" && tar -czf "slack-gui_${VERSION}_linux_amd64.tar.gz" slack-gui)

if command -v appimagetool >/dev/null 2>&1; then
  APPDIR="${DIST_DIR}/AppDir"
  rm -rf "${APPDIR}"
  mkdir -p "${APPDIR}/usr/bin" "${APPDIR}/usr/share/applications" "${APPDIR}/usr/share/icons/hicolor/256x256/apps"
  cp "${DIST_DIR}/slack-gui" "${APPDIR}/usr/bin/slack-gui"
  cp "${PKG_DIR}/slack-gui.desktop" "${APPDIR}/slack-gui.desktop"
  cp "${PKG_DIR}/slack-gui.desktop" "${APPDIR}/usr/share/applications/slack-gui.desktop"
  if [[ -f "${PKG_DIR}/icons/256x256/slack-gui.png" ]]; then
    cp "${PKG_DIR}/icons/256x256/slack-gui.png" "${APPDIR}/slack-gui.png"
    cp "${PKG_DIR}/icons/256x256/slack-gui.png" "${APPDIR}/usr/share/icons/hicolor/256x256/apps/slack-gui.png"
  fi
  appimagetool "${APPDIR}" "${DIST_DIR}/slack-gui_${VERSION}_linux_amd64.AppImage"
else
  echo "info: appimagetool not found; skipping AppImage build"
fi

if [[ "${APPIMAGE_ONLY}" -eq 1 ]]; then
  echo "Done (appimage-only mode)."
  exit 0
fi

if command -v nfpm >/dev/null 2>&1; then
  echo "==> Building deb and rpm via nfpm"
  (cd "${ROOT_DIR}" && VERSION="${VERSION}" nfpm pkg --config packaging/linux/nfpm.yaml --packager deb --target "dist/slack-gui_${VERSION}_amd64.deb")
  (cd "${ROOT_DIR}" && VERSION="${VERSION}" nfpm pkg --config packaging/linux/nfpm.yaml --packager rpm --target "dist/slack-gui_${VERSION}_amd64.rpm")
else
  echo "info: nfpm not found; skipping deb/rpm build"
fi

echo "==> Checksums"
(cd "${DIST_DIR}" && sha256sum * > "checksums_${VERSION}.txt")

echo "Done. Artifacts are in ${DIST_DIR}"
