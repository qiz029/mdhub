#!/bin/bash
# Production start script for MDHub Next.js app.
# Usage: set MDHUB_API_URL in .env.local, then run this script.
# Typically invoked by launchd or systemd.

set -a
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/../.env.local" 2>/dev/null || true
set +a

exec node "$SCRIPT_DIR/../node_modules/.bin/next" start -p "${PORT:-10001}"
