#!/bin/bash
# Run the MDHub Go backend.
# Set environment variables or source a .env file first.
#   MDHUB_PG     – PostgreSQL DSN
#   MDHUB_LISTEN – listen address (default :10002)
# One-shot vault import (then exits); pass the vault ROOT:
#   ./mdhub-go -import "/path/to/Obsidian Vault"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# Source .env if present
[ -f .env ] && set -a && source .env && set +a

exec ./mdhub-go "$@"
