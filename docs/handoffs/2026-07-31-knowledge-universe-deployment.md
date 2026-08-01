# Knowledge Universe deployment handoff

Audience: the SRE agent deploying MDHub. This is an execution handoff, not a
replacement for the general [Operations Manual](../OPERATIONS.md).

## Objective

Deploy the Knowledge Universe release while preserving the currently deployed,
uncommitted SRE fixes:

- `go-backend/classify.go`: `batchSize=40`, three retries, classifier timeout
  raised from 30 seconds to 120 seconds.
- `go-backend/go.mod` (and normally `go.sum`): `github.com/lib/pq v1.10.9`.

Do not reset, clean, stash, or overwrite the live checkout. Build this release
in a separate detached worktree and apply a binary patch copied from the live
checkout.

## Release source

- Feature branch: `feat/knowledge-universe`
- Runtime commits, in order:
  - `6fc44ad feat: add semantic knowledge universe`
  - `7f70b0f fix: harden semantic universe construction`
- General deployment runbook: [docs/OPERATIONS.md](../OPERATIONS.md)
- Semantic graph implementation: [go-backend/universe.go](../../go-backend/universe.go)
- Embedding migration: [go-backend/embed.go](../../go-backend/embed.go)
- Frontend route: [src/app/universe/page.tsx](../../src/app/universe/page.tsx)

At handoff time, `feat/knowledge-universe` is a local branch with no upstream.
Do not assume `git fetch` can recover it. The deploy host must use a repository
that already contains commits `6fc44ad` and `7f70b0f`, or the owner must publish
the branch first.

There is no database schema migration and no new environment variable. The
frontend adds the locked npm dependency `d3-force`.

## Pre-deploy safety gate

Use explicit paths. Do not point either variable at `/`, `$HOME`, or a workspace
root containing unrelated repositories.

```bash
export MDHUB_LIVE_DIR="/absolute/path/to/current/deployed/mdhub"
export MDHUB_SRE_PATCH="/tmp/mdhub-sre-runtime-fixes.patch"
export MDHUB_RELEASE_DIR="$(mktemp -d /tmp/mdhub-universe-release.XXXXXX)"

cd "$MDHUB_LIVE_DIR"
git status --short --branch
git diff --binary -- \
  go-backend/classify.go \
  go-backend/go.mod \
  go-backend/go.sum > "$MDHUB_SRE_PATCH"
test -s "$MDHUB_SRE_PATCH"
shasum -a 256 "$MDHUB_SRE_PATCH"

# Confirm the patch actually contains the deployed fixes before continuing.
rg -n "batchSize|120.*Second|v1\.10\.9" "$MDHUB_SRE_PATCH"

# Create an isolated release tree. The live dirty checkout remains untouched.
git worktree add --detach "$MDHUB_RELEASE_DIR" 7f70b0f
cd "$MDHUB_RELEASE_DIR"
git apply --check "$MDHUB_SRE_PATCH"
git apply "$MDHUB_SRE_PATCH"

# Copy runtime configuration locally without printing or committing secrets.
install -m 600 "$MDHUB_LIVE_DIR/go-backend/.env" go-backend/.env
install -m 600 "$MDHUB_LIVE_DIR/.env.local" .env.local

# Verify the intended combined source state.
git status --short
rg -n "batchSize|120.*Second" go-backend/classify.go
rg -n "github.com/lib/pq" go-backend/go.mod go-backend/go.sum
git diff --check
```

Stop here if:

- either runtime commit is missing;
- the SRE patch is empty or does not show all expected changes;
- `git apply --check` fails;
- `.env` or `.env.local` is missing;
- `go.mod` does not resolve to `lib/pq v1.10.9` after applying the patch.

Do not reconstruct the SRE patch from this prose. Obtain the exact deployed
diff from the live checkout.

## Build and test gate

Run all commands from the isolated release worktree:

```bash
cd "$MDHUB_RELEASE_DIR/go-backend"
env GOCACHE=/tmp/mdhub-go-cache go test -count=1 ./...
go build -o mdhub-go .

cd "$MDHUB_RELEASE_DIR"
npm ci
npx tsc --noEmit
npm run build
```

Expected results:

- all Go tests pass;
- TypeScript exits successfully;
- Next.js lists `/universe` as a dynamic route;
- the existing `::highlight(mdhub-draft)` Turbopack warning may appear, but the
  build must still finish successfully;
- `npm ci` may report the existing audit findings. Do not run `npm audit fix`
  as part of this deployment.

## Pre-restart observation

Capture the old service state for comparison and rollback evidence:

```bash
curl -fsS http://localhost:10002/health
curl -fsS http://localhost:10002/api/documents \
  > /tmp/mdhub-documents-before-universe.json
curl -fsS -o /dev/null -w '%{http_code}\n' \
  http://localhost:10001/mdhub/
```

Record the currently running process/supervisor arrangement. The commands below
match the repository's manual `run.sh` / `start.sh` deployment. If launchd,
systemd, or another supervisor owns the processes, update its working directory
to `MDHUB_RELEASE_DIR` and use that supervisor instead of starting duplicate
processes.

## Restart onto the release worktree

```bash
mkdir -p "$MDHUB_RELEASE_DIR/go-backend/logs"

pkill -f 'mdhub-go$|./mdhub-go'
nohup "$MDHUB_RELEASE_DIR/go-backend/run.sh" \
  > "$MDHUB_RELEASE_DIR/go-backend/logs/api.log" 2>&1 &

# The process is named next-server; killing "next start" is insufficient.
pkill -f next-server
nohup "$MDHUB_RELEASE_DIR/scripts/start.sh" \
  > /tmp/mdhub-web-universe.log 2>&1 &
```

Wait for both services to become ready, then verify the new route before
starting the embedding migration:

```bash
curl -fsS http://localhost:10002/health
curl -fsS http://localhost:10002/api/universe | jq '.meta'
curl -fsS -o /dev/null -w '%{http_code}\n' \
  http://localhost:10001/mdhub/
curl -fsS -o /dev/null -w '%{http_code}\n' \
  http://localhost:10001/mdhub/universe/
```

Expected:

- backend health is `ok`;
- `/api/universe` returns `nodes`, `edges`, and `meta` rather than 404;
- both frontend routes return HTTP 200;
- `meta.documents` matches the published document count;
- `meta.embedded_documents` may initially lag behind `meta.documents`.

## Required one-time embedding migration

This release changes a document vector from “title plus first 512 runes” to a
normalized pool of up to six chunks sampled across the full document. Existing
rows remain readable but do not gain the new representation until re-embedded.

Run this during a low-traffic window. The worker is sequential, and a long
document can make up to six embedding requests.

```bash
curl -fsS -X POST http://localhost:10002/api/reembed | jq .
tail -f "$MDHUB_RELEASE_DIR/go-backend/logs/api.log"
```

The POST response confirms how many documents were queued, not how many
finished. In another terminal, poll coverage:

```bash
curl -fsS http://localhost:10002/api/universe \
  | jq '.meta | {documents, embedded_documents, edges, min_similarity, max_similarity}'
```

Do not declare the migration complete until:

- `embedded_documents == documents`, or every missing document has a recorded,
  understood embedding error;
- backend logs show no repeating timeout/model errors;
- `edges > 0` when at least two semantically related documents exist.

## UI acceptance

Open `http://localhost:10001/mdhub/` and verify:

1. The header shows `Documents`, then `Universe` immediately to its right.
2. `Documents` is active on the existing document page.
3. `Universe` opens `/mdhub/universe/` and becomes active.
4. Nodes and edges render without a browser-console error.
5. Drag, zoom, node selection, document search, and `Focused / Balanced / Full`
   density controls work.
6. Selecting a node shows only the semantic neighbours visible at the current
   density and `打开文档` reaches the correct document.
7. At a narrow mobile width, search wraps below the tabs and graph controls do
   not overlap.

## Rollback

The release uses the existing schema and embedding byte format. Re-embedding
changes only derived rows, so a code rollback does not require a database
restore.

Because the old checkout was never modified, rollback is process-only:

```bash
pkill -f 'mdhub-go$|./mdhub-go'
nohup "$MDHUB_LIVE_DIR/go-backend/run.sh" \
  > "$MDHUB_LIVE_DIR/go-backend/logs/api.log" 2>&1 &

pkill -f next-server
nohup "$MDHUB_LIVE_DIR/scripts/start.sh" \
  > /tmp/mdhub-web-rollback.log 2>&1 &

curl -fsS http://localhost:10002/health
curl -fsS -o /dev/null -w '%{http_code}\n' \
  http://localhost:10001/mdhub/
```

Keep `MDHUB_RELEASE_DIR`, the SRE patch, old process evidence, and logs until the
rollback window has passed. Cleanup is intentionally not part of this handoff.

## Required deployment report

Report back with:

- deployed runtime commits and the local SRE patch checksum;
- Go test, TypeScript, and Next.js build results;
- health and HTTP status results;
- `/api/universe` meta before and after re-embedding;
- number of queued embeddings and any failures;
- desktop/mobile UI acceptance result;
- whether the old checkout remains ready for rollback.

Do not include environment files, DSNs, API keys, or other secrets in the
report.

## Suggested skills

- `diagnose`: use if build, embedding, process restart, or route verification
  fails; separate source, runtime configuration, and process ownership before
  changing code.
- `review`: use if the SRE patch must be converted into a permanent repository
  commit before future deployments.
- `github:yeet`: use only if the owner explicitly asks to publish the local
  feature branch or SRE-fix commit to GitHub.
