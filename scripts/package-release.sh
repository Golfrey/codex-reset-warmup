#!/usr/bin/env bash
set -euo pipefail

PLUGIN_ID="codex-reset-warmup"
VERSION="${1:-}"

if [[ -z "${VERSION}" ]]; then
  VERSION="$(git describe --tags --abbrev=0 2>/dev/null || true)"
  VERSION="${VERSION#v}"
fi

if [[ -z "${VERSION}" ]]; then
  echo "usage: $0 <version>" >&2
  echo "example: $0 0.1.0" >&2
  exit 2
fi

if [[ "${VERSION}" == v* || "${VERSION}" == V* ]]; then
  VERSION="${VERSION#v}"
  VERSION="${VERSION#V}"
fi

GOOS_VALUE="${GOOS:-$(go env GOOS)}"
GOARCH_VALUE="${GOARCH:-$(go env GOARCH)}"

case "${GOOS_VALUE}" in
  darwin) EXT="dylib" ;;
  windows) EXT="dll" ;;
  *) EXT="so" ;;
esac

DIST_DIR="${DIST_DIR:-dist}"
WORK_DIR="${DIST_DIR}/work/${GOOS_VALUE}_${GOARCH_VALUE}"
LIB_NAME="${PLUGIN_ID}-v${VERSION}.${EXT}"
ARCHIVE_NAME="${PLUGIN_ID}_${VERSION}_${GOOS_VALUE}_${GOARCH_VALUE}.zip"

mkdir -p "${DIST_DIR}" "${WORK_DIR}"
rm -f "${WORK_DIR:?}/${PLUGIN_ID}.${EXT}" "${WORK_DIR}/${LIB_NAME}" "${DIST_DIR}/${ARCHIVE_NAME}"

echo "building ${PLUGIN_ID} ${VERSION} for ${GOOS_VALUE}/${GOARCH_VALUE}"
CGO_ENABLED=1 GOOS="${GOOS_VALUE}" GOARCH="${GOARCH_VALUE}" \
  go build -trimpath -buildmode=c-shared -ldflags="-s -w" -o "${WORK_DIR}/${LIB_NAME}" .

rm -f "${WORK_DIR}/${PLUGIN_ID}-v${VERSION}.h"

(
  cd "${WORK_DIR}"
  zip -q -X "../../${ARCHIVE_NAME}" "${LIB_NAME}"
)

if [[ ! -f "${DIST_DIR}/checksums.txt" ]]; then
  : > "${DIST_DIR}/checksums.txt"
fi

if command -v sha256sum >/dev/null 2>&1; then
  HASH="$(sha256sum "${DIST_DIR}/${ARCHIVE_NAME}" | awk '{print $1}')"
else
  HASH="$(shasum -a 256 "${DIST_DIR}/${ARCHIVE_NAME}" | awk '{print $1}')"
fi

grep -v "  ${ARCHIVE_NAME}$" "${DIST_DIR}/checksums.txt" > "${DIST_DIR}/checksums.txt.tmp" || true
printf "%s  %s\n" "${HASH}" "${ARCHIVE_NAME}" >> "${DIST_DIR}/checksums.txt.tmp"
mv "${DIST_DIR}/checksums.txt.tmp" "${DIST_DIR}/checksums.txt"

echo "wrote ${DIST_DIR}/${ARCHIVE_NAME}"
echo "updated ${DIST_DIR}/checksums.txt"
