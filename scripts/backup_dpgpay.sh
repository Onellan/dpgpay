#!/usr/bin/env sh
set -eu

VOLUME_NAME="${DPGPAY_VOLUME_NAME:-dpgpay_dpgpay_data}"
BACKUP_DIR="${DPGPAY_BACKUP_DIR:-./backups}"
RETENTION_DAYS="${DPGPAY_BACKUP_RETENTION_DAYS:-14}"
TS="$(date +%Y%m%d-%H%M%S)"
OUT_FILE="${BACKUP_DIR}/dpgpay-${TS}.db"

mkdir -p "${BACKUP_DIR}"

docker run --rm \
  -v "${VOLUME_NAME}:/from" \
  -v "$(pwd):/work" \
  alpine sh -c "cp /from/dpgpay.db /work/${OUT_FILE}" >/dev/null

find "${BACKUP_DIR}" -name 'dpgpay-*.db' -type f -mtime +"${RETENTION_DAYS}" -delete

echo "Backup complete: ${OUT_FILE}"
