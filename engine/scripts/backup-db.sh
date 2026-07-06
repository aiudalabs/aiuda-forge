#!/usr/bin/env sh
# Consistent backup of Forja's SQLite databases from the compose `forge-data` volume.
# Uses sqlite3 `.backup` (safe while the control container is running) via a throwaway
# container, so no downtime is needed.
#
# Usage:   ./backup-db.sh [DEST_DIR] [VOLUME_NAME] [STAMP]
#   DEST_DIR     where to write the backup     (default: ./forge-backups)
#   VOLUME_NAME  docker volume with the DBs    (default: aiuda-forge_forge-data)
#   STAMP        subfolder name / timestamp    (default: passed by cron, else "latest")
#
# Cron example (hourly, keep it simple; prune old folders separately):
#   0 * * * * cd /opt/aiuda-forge && ./engine/scripts/backup-db.sh /backups aiuda-forge_forge-data "$(date +\%Y\%m\%d-\%H\%M)"
set -eu
DEST="${1:-./forge-backups}"
VOL="${2:-aiuda-forge_forge-data}"
STAMP="${3:-latest}"
OUT="$DEST/$STAMP"
mkdir -p "$OUT"

for db in vibeforge tickets projects auth brain billing; do
  docker run --rm \
    -v "$VOL":/data:ro \
    -v "$(cd "$OUT" && pwd)":/backup \
    keinos/sqlite3 \
    sh -c "[ -f /data/$db.db ] && sqlite3 /data/$db.db \".backup /backup/$db.db\" || true"
done

echo "backup -> $OUT"
