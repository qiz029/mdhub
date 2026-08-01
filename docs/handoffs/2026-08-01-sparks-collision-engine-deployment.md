# Sparks & collision engine deployment handoff

Audience: the SRE agent deploying MDHub. This is an execution handoff, not a
replacement for the general [Operations Manual](../OPERATIONS.md).

## Objective

Deploy the Sparks & collision-engine release: fleeting notes (`type: fleeting`
frontmatter), the embedding-driven collision engine, and the `/sparks` UI.

This release **includes a database schema migration** and reuses existing
environment variables — there is no new env var.

## Release source

- The changes are **uncommitted in the development working tree** at handoff
  time (owner has not committed them yet). Do not assume any branch or commit
  contains them. Either the owner commits first, or transfer the exact diff:

  ```bash
  cd /path/to/dev/mdhub
  git status --short          # expect only the Sparks change set
  git diff --binary > /tmp/mdhub-sparks-release.patch
  test -s /tmp/mdhub-sparks-release.patch
  shasum -a 256 /tmp/mdhub-sparks-release.patch
  ```

  The patch must touch, at minimum: `go-backend/schema.sql`,
  `go-backend/main.go`, `go-backend/collide.go` (new), `go-backend/embed.go`,
  `go-backend/document_publication.go`, `go-backend/document_store.go`,
  `src/app/sparks/`, `src/app/api/sparks/`, `src/app/api/collisions/`,
  `src/components/SparksClient.tsx`, `src/components/DocumentCollisions.tsx`,
  `src/lib/sparks.ts`, `src/components/Nav.tsx`,
  `src/app/view/[...slug]/page.tsx`, `README.md`.

- Collision engine implementation: [go-backend/collide.go](../../go-backend/collide.go)
- Schema migration: [go-backend/schema.sql](../../go-backend/schema.sql)
- Sparks UI: [src/app/sparks/page.tsx](../../src/app/sparks/page.tsx)

If the deploy host runs a checkout with its own uncommitted SRE fixes (see the
[previous handoff](2026-07-31-knowledge-universe-deployment.md)), preserve them
with the same patch-and-worktree procedure. Do not reset, clean, or stash the
live checkout.

## What the migration changes

`go-backend/schema.sql` is idempotent and additive only:

- `documents` gains `kind TEXT NOT NULL DEFAULT 'note'` — existing rows become
  `'note'`, no data rewrite beyond the default backfill.
- New table `collisions` (pair uniqueness on ordered `slug_a, slug_b`, FK
  `ON DELETE CASCADE`).

The old binary is forward-compatible with the new schema (explicit column
lists everywhere), so rollback does not require a database restore.

Feature gates:

- Collision engine is active only when `MDHUB_EMBED_URL` is set.
- LLM connection/question text only when `MDHUB_LLM_API_KEY` is set; without
  it, collisions still land as score-only rows. Both are existing variables.

## Pre-deploy safety gate

```bash
export MDHUB_LIVE_DIR="/absolute/path/to/current/deployed/mdhub"
export MDHUB_RELEASE_DIR="$(mktemp -d /tmp/mdhub-sparks-release.XXXXXX)"

cd "$MDHUB_LIVE_DIR"
git status --short --branch   # record, do not clean

# Isolated release tree from the live HEAD, with the release patch applied.
git worktree add --detach "$MDHUB_RELEASE_DIR" HEAD
cd "$MDHUB_RELEASE_DIR"
git apply --check /tmp/mdhub-sparks-release.patch   # or: owner-committed ref
git apply /tmp/mdhub-sparks-release.patch

install -m 600 "$MDHUB_LIVE_DIR/go-backend/.env" go-backend/.env
install -m 600 "$MDHUB_LIVE_DIR/.env.local" .env.local

git status --short
git diff --check
rg -n "collisionSimThreshold" go-backend/collide.go
rg -n "kind" go-backend/schema.sql
```

Stop here if the patch is missing expected files, `git apply --check` fails, or
either env file is absent.

## Build and test gate

Run from the isolated release worktree:

```bash
cd "$MDHUB_RELEASE_DIR/go-backend"
env GOCACHE=/tmp/mdhub-go-cache go test -count=1 ./...
./check-coverage.sh           # gate: coverage >= 70%
go build -tags nodynamic -o mdhub-go .

cd "$MDHUB_RELEASE_DIR"
npm ci
npm test
npm run build
```

Expected results:

- all Go tests pass (go-sqlmock based, no live PG needed);
- frontend tests pass (34 at handoff time, including 11 new `src/lib/sparks`
  tests);
- Next.js build lists `/sparks`, `/api/sparks`, `/api/collisions`, and
  `/api/collisions/[id]` as routes.

## Pre-restart observation

```bash
curl -fsS http://localhost:10002/health
curl -fsS http://localhost:10002/api/documents \
  > /tmp/mdhub-documents-before-sparks.json
psql -d mdhub -c '\d documents' > /tmp/mdhub-schema-before-sparks.txt
curl -fsS -o /dev/null -w '%{http_code}\n' http://localhost:10001/mdhub/
```

Record the process/supervisor arrangement as in the previous handoff; if a
supervisor owns the processes, point it at `MDHUB_RELEASE_DIR` instead of
starting duplicates.

## Apply the schema migration

Additive and idempotent; safe while the old binary is still running:

```bash
psql -d mdhub -f "$MDHUB_RELEASE_DIR/go-backend/schema.sql"
psql -d mdhub -c '\d documents' | rg 'kind'
psql -d mdhub -c '\d collisions'
```

## Restart onto the release worktree

```bash
mkdir -p "$MDHUB_RELEASE_DIR/go-backend/logs"

pkill -f 'mdhub-go$|./mdhub-go'
nohup "$MDHUB_RELEASE_DIR/go-backend/run.sh" \
  > "$MDHUB_RELEASE_DIR/go-backend/logs/api.log" 2>&1 &

# The process is named next-server; killing "next start" is insufficient.
pkill -f next-server
nohup "$MDHUB_RELEASE_DIR/scripts/start.sh" \
  > /tmp/mdhub-web-sparks.log 2>&1 &
```

## Post-restart verification

```bash
curl -fsS http://localhost:10002/health

# Anonymous privacy gates — these must NOT leak fleeting content:
curl -fsS -o /dev/null -w '%{http_code}\n' http://localhost:10002/api/sparks
#   expect 401 (token configured) or 503 (token unset)
curl -fsS "http://localhost:10002/api/collisions" | jq 'length'
#   anonymous: only pairs where both documents are published

# Frontend routes:
curl -fsS -o /dev/null -w '%{http_code}\n' http://localhost:10001/mdhub/
curl -fsS -o /dev/null -w '%{http_code}\n' http://localhost:10001/mdhub/sparks/
```

Expected: health `ok`; `/api/sparks` rejects anonymous callers; both frontend
routes return 200.

## Required one-time collision backfill

Existing embeddings are reused — no re-embedding needed. Queue a collision
pass over every embedded document (edit token required):

```bash
curl -fsS -X POST \
  -H "X-MDHub-Edit-Token: $MDHUB_EDIT_TOKEN" \
  http://localhost:10002/api/recollide | jq .
tail -f "$MDHUB_RELEASE_DIR/go-backend/logs/api.log"
```

The response counts queued slugs, not finished jobs. Poll progress:

```bash
psql -d mdhub -c \
  'SELECT verdict, count(*) FROM collisions GROUP BY verdict ORDER BY 1;'
```

Do not declare the backfill complete until:

- the collision count stops growing and the log shows no repeating
  embed/LLM errors;
- pairs above the similarity threshold (`0.55`, see
  `collisionSimThreshold` in collide.go) have rows, or their absence is
  understood (e.g. library too small or genuinely dissimilar);
- with `MDHUB_LLM_API_KEY` set, a sample of rows has non-empty
  `explanation`/`question`.

Note: `/api/reembed` covers published documents only; fleeting notes are
embedded on write. This is intentional.

## UI acceptance

Open `http://localhost:10001/mdhub/` and verify:

1. The header shows `Documents`, `Sparks`, `Universe` in that order.
2. `/mdhub/sparks/` asks for the edit token on a fresh browser profile
   (sessionStorage pattern, same as the editor); anonymous visitors see no
   spark or collision data.
3. Quick capture: submit one line of text; it appears in the 灵感流 list with
   an age badge. Verify anonymously that it does NOT appear in `/`,
   `/api/documents`, search, or Universe.
4. After the backfill (or after capturing a spark semantically close to an
   existing note), the 碰撞流 shows a pair with connection text and an open
   question; 确认 / 忽略 update the verdict and dismissed items collapse.
5. On a document page, the collision section renders only when the browser
   already holds the edit token; it never prompts and never renders for
   anonymous visitors.

## Rollback

The migration is additive and the old binary tolerates the new column/table.
Rollback is process-only, using the untouched old checkout:

```bash
pkill -f 'mdhub-go$|./mdhub-go'
nohup "$MDHUB_LIVE_DIR/go-backend/run.sh" \
  > "$MDHUB_LIVE_DIR/go-backend/logs/api.log" 2>&1 &

pkill -f next-server
nohup "$MDHUB_LIVE_DIR/scripts/start.sh" \
  > /tmp/mdhub-web-rollback.log 2>&1 &

curl -fsS http://localhost:10002/health
curl -fsS -o /dev/null -w '%{http_code}\n' http://localhost:10001/mdhub/
```

The `collisions` table and `kind` column may be left in place; dropping them
is optional and NOT part of a routine rollback.

Keep `MDHUB_RELEASE_DIR`, the release patch, and logs until the rollback
window has passed.

## Required deployment report

Report back with:

- release patch checksum (or commit, if the owner committed first);
- Go test / coverage-gate / frontend test / Next.js build results;
- schema verification output (`kind` column, `collisions` table);
- anonymous-access results for `/api/sparks` and `/api/collisions`;
- `/api/recollide` queued count and final collision row counts by verdict;
- UI acceptance result, including the anonymous-invisibility checks;
- whether the old checkout remains ready for rollback.

Do not include environment files, DSNs, edit tokens, API keys, or other
secrets in the report.

## Suggested skills

- `diagnose`: use if the migration, collision backfill, or privacy-gate
  verification fails; separate schema state, runtime configuration, and
  process ownership before changing code.
- `review`: use if the uncommitted release patch should first become a
  permanent repository commit.
