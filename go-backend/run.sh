#!/bin/bash
# Run the MDHub Go search backend.
# Set environment variables or source a .env file first.
#   MDHUB_VAULT  – path to markdown files to index
#   MDHUB_PG     – PostgreSQL DSN
#   MDHUB_LISTEN – listen address (default :10002)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# Source .env if present
[ -f .env ] && set -a && source .env && set +a

exec ./mdhub-go
