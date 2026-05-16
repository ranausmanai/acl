#!/usr/bin/env bash
#
# deploy.sh — build, atomically swap, restart, verify acl.fyi.
#
# Designed for the production VPS at /opt/acl. The intended workflow is:
#
#   cd /opt/acl-src
#   git pull
#   ./deploy.sh
#
# The script never deletes anything. On any verification failure it puts the
# old binary back and restarts the service before exiting non-zero.
#
# Overridable via environment variables:
#   ACL_DEPLOY_LIVE     Binary path on disk         (default /opt/acl/acl)
#   ACL_DEPLOY_OWNER    chown target for the binary (default acl:acl)
#   ACL_DEPLOY_SERVICE  systemd unit to restart     (default acl.service)
#   ACL_DEPLOY_PORT     Port to probe for /health   (default 8080)
#
# The script is idempotent: re-running it without git pull rebuilds and
# redeploys the same code. The receipt of the old binary is preserved in
# /opt/acl/acl.bak-<UTC-timestamp>-deploy until you delete it yourself.

set -euo pipefail

LIVE="${ACL_DEPLOY_LIVE:-/opt/acl/acl}"
OWNER="${ACL_DEPLOY_OWNER:-acl:acl}"
SERVICE="${ACL_DEPLOY_SERVICE:-acl.service}"
PORT="${ACL_DEPLOY_PORT:-8080}"

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "${REPO_ROOT}"

TS="$(date -u +%Y%m%d%H%M%S)"
NEW="${REPO_ROOT}/.deploy/acl-${TS}"
BACKUP="${LIVE}.bak-${TS}-deploy"

log() { printf '\033[36m▸\033[0m %s\n' "$*"; }
err() { printf '\033[31m✗\033[0m %s\n' "$*" >&2; }

log "Step 1/6  build linux/amd64 binary"
mkdir -p "${REPO_ROOT}/.deploy"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.Version=$(git rev-parse --short HEAD 2>/dev/null || echo dev)" \
    -o "${NEW}" ./cmd/acl

NEW_SHA=$(sha256sum "${NEW}" | awk '{print $1}')
log "      → ${NEW}  ($(du -h "${NEW}" | awk '{print $1}'), sha256=${NEW_SHA:0:12})"

log "Step 2/6  parse-check the canonical demo file"
"${NEW}" check examples/support_refund.acl

log "Step 3/6  backup current binary → ${BACKUP}"
if [ -f "${LIVE}" ]; then
    cp -p "${LIVE}" "${BACKUP}"
    log "      → backup is $(stat -c %s "${BACKUP}" 2>/dev/null || stat -f %z "${BACKUP}") bytes"
else
    log "      → no live binary at ${LIVE}; first-time install"
fi

log "Step 4/6  atomic swap"
chmod 755 "${NEW}"
chown "${OWNER}" "${NEW}" 2>/dev/null || true
mv "${NEW}" "${LIVE}"
log "      → ${LIVE} replaced"

log "Step 5/6  restart ${SERVICE}"
systemctl restart "${SERVICE}"
sleep 3
ACTIVE=$(systemctl is-active "${SERVICE}" || true)
log "      → systemctl is-active: ${ACTIVE}"

log "Step 6/6  verify HTTP routes"
HEALTH=$(curl -fsS -o /dev/null -w "%{http_code}" "http://127.0.0.1:${PORT}/health" || echo FAIL)
AGENT=$(curl -fsS -o /dev/null -w "%{http_code}"  "http://127.0.0.1:${PORT}/agenticflow" || echo FAIL)
log "      → /health=${HEALTH}  /agenticflow=${AGENT}"

# Decide.
if [ "${ACTIVE}" = "active" ] && [ "${HEALTH}" = "200" ] && [ "${AGENT}" = "200" ]; then
    printf '\n\033[32m✓\033[0m deploy ok — service active, /health and /agenticflow both 200\n'
    printf '  backup kept at %s\n' "${BACKUP}"
    exit 0
fi

err "deploy failed verification — rolling back"
if [ -f "${BACKUP}" ]; then
    cp -p "${BACKUP}" "${LIVE}"
    chown "${OWNER}" "${LIVE}" 2>/dev/null || true
    systemctl restart "${SERVICE}"
    sleep 2
    err "rollback complete; service is $(systemctl is-active "${SERVICE}" || true)"
else
    err "no backup available — manual recovery needed"
fi
exit 1
