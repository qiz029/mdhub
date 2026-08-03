#!/bin/bash
# Run the MDHub Go backend.
# Set environment variables or source a .env file first.
#   MDHUB_PG     – PostgreSQL DSN
#   MDHUB_LISTEN – listen address (default 127.0.0.1:10002)
# One-shot vault import (then exits); pass the vault ROOT:
#   ./mdhub-go -import "/path/to/Obsidian Vault"
# Durable paper-translation worker (run as a separate process):
#   ./run.sh -translation-worker
# Claim at most one queued job for an operator smoke test:
#   ./run.sh -translation-worker-once

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# Source .env if present
[ -f .env ] && set -a && source .env && set +a

exec ./mdhub-go "$@"
