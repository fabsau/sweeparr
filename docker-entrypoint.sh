#!/bin/sh
set -eu

# If already started as a non-root user (e.g. via Docker's --user / user: flag),
# run Sweeparr directly without touching ownership or capabilities.
if [ "$(id -u)" -ne 0 ]; then
  exec /app/sweeparr "$@"
fi

# Resolve target UID/GID from PUID/PGID (or SWEEPARR_UID/SWEEPARR_GID),
# defaulting to secure non-root 1000:1000 (sweeparr).
TARGET_UID="${PUID:-${SWEEPARR_UID:-1000}}"
TARGET_GID="${PGID:-${SWEEPARR_GID:-1000}}"

case "$TARGET_UID" in
  ''|*[!0-9]*)
    echo "ERROR: PUID/SWEEPARR_UID must be a positive integer (got: '$TARGET_UID')" >&2
    exit 1
    ;;
esac
case "$TARGET_GID" in
  ''|*[!0-9]*)
    echo "ERROR: PGID/SWEEPARR_GID must be a positive integer (got: '$TARGET_GID')" >&2
    exit 1
    ;;
esac

if [ "$TARGET_UID" -eq 0 ] || [ "$TARGET_GID" -eq 0 ]; then
  echo "ERROR: Refusing to run Sweeparr as root (PUID=0 or PGID=0 is not allowed)" >&2
  exit 1
fi

STATE_DIR="${SWEEPARR_STATE_DIR:-/state}"
if [ -d "$STATE_DIR" ]; then
  CUR_OWNER="$(stat -c '%u:%g' "$STATE_DIR" 2>/dev/null || true)"
  if [ "$CUR_OWNER" != "${TARGET_UID}:${TARGET_GID}" ]; then
    chown -R "${TARGET_UID}:${TARGET_GID}" "$STATE_DIR" 2>/dev/null || true
  fi
fi

exec su-exec "${TARGET_UID}:${TARGET_GID}" /app/sweeparr "$@"
