# ADR: Agent-driven paper translation pipeline

- Status: Accepted
- Date: 2026-08-02
- Decision owner: MDHub owner

## Context

MDHub can already accept raw Markdown through its document write path, render
long-form documents, keep drafts out of published surfaces through
`publish: false`, and project
published documents into search, categories, embeddings, collisions, and the
Knowledge Universe.

MDHub also has an OpenAI-compatible chat-completions caller configured through
`MDHUB_LLM_BASE_URL`, `MDHUB_LLM_API_KEY`, and `MDHUB_LLM_MODEL`. Today that
caller lives in `classify.go` and is reused by classification and collision
insight generation. It is a useful provider implementation, but it is not yet
a provider module: configuration is global, requests are limited to one system
and one user message, and timeout and output behavior are tuned for short
background tasks.

The current paper-translation workflow happens outside the product. The user
gives an arXiv, DOI, publisher, or direct PDF URL to an agent, asks for a full
translation, and then asks the agent to upload the result to MDHub. This has
several problems:

- translation intent and progress are invisible in MDHub;
- long-running work is lost when an agent or process is interrupted;
- source retrieval, text extraction, translation, validation, and publication
  are not one recoverable workflow;
- agents can silently summarize or truncate a paper instead of translating it
  fully;
- source version, provider, model, and validation provenance are not retained;
- users must know the prompt and the `_translations/<slug>` publication
  convention.

This decision concerns direct paper URLs and uploaded PDFs. RSS subscription
and feed-item ingestion are separate workflows and are out of scope.

## Decision drivers

- A user should be able to start from an arXiv, DOI, publisher, or PDF URL.
- Full and accurate translation is the default contract; a summary is not an
  acceptable substitute.
- Translation must survive browser, backend, and worker restarts.
- The existing LLM configuration should be reused rather than introducing a
  second credential and provider stack.
- Agent execution must not require the web process to run arbitrary shell
  commands.
- Source and output provenance must be inspectable.
- Translation must land as a reviewable draft before it becomes public.
- The first implementation should fit MDHub's PostgreSQL-first architecture.

## Decision

Add a durable Translation module to MDHub and run the translation pipeline in
an independently deployed Translation Agent Worker.

The user-facing flow is:

```text
paper URL or PDF upload
        -> source identification and preview
        -> user confirms "full translation"
        -> durable translation job
        -> Translation Agent Worker
        -> completeness validation
        -> bilingual draft preview
        -> user publishes to _translations/<slug>
```

The Translation module owns the job lifecycle and exposes a small interface to
the rest of MDHub:

```text
Submit(source, options) -> Job
Get(jobID)              -> Job
Cancel(jobID)           -> Job
Publish(jobID)          -> Document
```

Worker claim, heartbeat, progress, completion, and failure operations are an
internal interface at the worker seam. URL resolution, artifact storage,
chunking, retries, validation, and document publication remain implementation
details behind the Translation module.

### User input and source resolution

The primary entry point is `/translations/new`. It accepts one of:

- an arXiv abstract or PDF URL;
- an arXiv identifier;
- a DOI or DOI URL;
- a publisher or other paper landing page;
- a direct HTTP(S) PDF URL;
- an uploaded PDF as the fallback for inaccessible sources.

Pasting a value starts source identification but does not start translation.
The preview shows the resolved title, authors, source, version, page or text
size, and whether the same source version already has a job or published
translation. The user then confirms the job with one primary action:
`Start full translation`.

Provider and prompt controls are not required fields. The default profile is
`paper-translate-v1`, targeting Simplified Chinese and requiring:

- complete translation from abstract through the final section;
- preservation of headings, lists, tables, figure captions, formulas,
  footnotes, citation markers, links, and reference numbering;
- no replacement of source content with a summary;
- paragraph or block identifiers sufficient for bilingual review;
- retention of the source URL, source version, and translation provenance.

Model overrides, terminology, and reference-translation preferences may be
offered under advanced settings.

### Supported source behavior

Source resolution is deterministic before the Agent Worker is involved:

- arXiv input is normalized to an identifier and explicit version, and its
  metadata and accessible full-text artifact are captured;
- a direct PDF is accepted only after MIME sniffing and size validation;
- DOI and publisher inputs are followed to a canonical landing page and an
  accessible full-text or PDF link when one is available;
- inaccessible, authenticated, anti-bot, or paywalled sources transition to
  `needs_input` and ask the user to upload the PDF;
- a source content hash detects duplicate submissions and source-version
  changes.

Only `http` and `https` URLs are accepted. Server-side fetching must enforce
redirect, timeout, response-size, and content-type limits, reject loopback,
link-local, private-network, and metadata-service destinations after every DNS
resolution and redirect, and avoid reflecting upstream response bodies in
errors. PDF upload receives equivalent size and file-signature checks.

### Durable job model

Translation jobs are persisted in PostgreSQL. The existing in-memory
`keyedJobQueue` remains appropriate for short, reconstructable classification,
embedding, and collision work, but it is not used as the source of truth for a
long paper translation.

The initial state machine is:

```text
queued
  -> claimed
  -> fetching
  -> extracting
  -> translating
  -> validating
  -> draft_ready
  -> published

Any active state -> needs_input | failed | cancelled
needs_input      -> queued | cancelled
failed           -> queued | cancelled
```

`claimed` and later active states carry a renewable lease. If the lease
expires, another worker may resume from the last committed chunk. Progress is
derived from persisted stages and chunks rather than trusted as a free-form
percentage from the worker.

The initial schema consists of:

- `translation_jobs`: identity, normalized source, source hash, target
  language, profile, state, current stage, worker lease, provider/model
  provenance, output slug, validation result, error summary, and timestamps;
- `translation_chunks`: ordered source blocks, source hashes, translated
  blocks, state, attempts, and timestamps;
- `translation_artifacts`: content-addressed source PDF or extracted text,
  MIME type, bytes, hash, and creation time.

Artifacts are stored in PostgreSQL for the first implementation, consistent
with MDHub's database-first document and image storage. File size is bounded.
An object-storage adapter is not introduced until a second storage
implementation is actually needed.

### Translation Agent Worker

The Translation Agent Worker is an MDHub-owned process, separate from the Go
web process. It polls for a job, claims it with a lease, sends heartbeats while
working, persists each completed chunk, and reports a terminal result.

The worker performs this pipeline:

```text
resolve/capture source
  -> extract structured text
  -> normalize sections and blocks
  -> build translation context and terminology
  -> translate ordered chunks
  -> assemble Markdown
  -> run deterministic and model-assisted validation
  -> submit draft and provenance
```

The worker may use tools for PDF extraction, OCR, or HTML parsing. Those tools
are internal adapters and do not change the Translation module's interface.
The MDHub web process does not spawn Codex, Kimi, or arbitrary local commands.

The worker authenticates through the same trusted edge or private deployment
topology as other MDHub writes. No provider credential is sent to the browser
or stored in a job payload.

### Reuse and deepen the existing LLM provider

Extract the OpenAI-compatible caller from `classify.go` into a provider module.
Its external seam is context-aware completion, not task-specific translation
behavior. Classification, collision insight, and the Translation Agent Worker
reuse the same implementation.

The provider continues to use:

- `MDHUB_LLM_BASE_URL`;
- `MDHUB_LLM_API_KEY`;
- `MDHUB_LLM_MODEL` as the default model.

Translation may override only the model with `MDHUB_TRANSLATION_MODEL`, falling
back to `MDHUB_LLM_MODEL`. Translation prompts, chunking, glossary management,
retries, and completeness rules belong to the Agent Worker, not the provider.

The provider module must add context cancellation, bounded response handling,
typed errors, configurable per-request model and output limits, and testable
retry behavior. Existing classification and collision behavior must remain
unchanged when migrated to it.

Embedding remains a separate provider path because it has a different
interface and configuration (`MDHUB_EMBED_URL` and `MDHUB_EMBED_MODEL`).

### Validation contract

A translation cannot enter `draft_ready` only because the model returned text.
Validation must produce a stored report and verify at least:

- every required source section and block is represented exactly once;
- the final source section is present, preventing prefix-only truncation;
- heading order and citation identifiers are preserved;
- tables, formulas, figures, and footnotes are either preserved or explicitly
  reported as extraction limitations;
- no placeholder such as "remaining content omitted" is present;
- every translated chunk corresponds to its recorded source hash;
- assembled Markdown parses and stays within document limits;
- source URL, version, artifact hash, profile, provider, and model are recorded.

Length ratios are useful anomaly signals but are not sufficient proof of
completeness. A failed validation remains inspectable and retryable.

### Draft and publication behavior

The worker returns the assembled translation to the Translation module. The
module uses MDHub's existing document publication implementation directly; it
does not make a loopback HTTP request to itself.

The first stored result uses a derived, collision-resistant slug under
`_translations/<paper-slug>-<language>-<job-id>` and contains `publish: false`. This is a curation
state, not access control; the authenticated edge still protects the personal
deployment. The preview page
supports:

- source/translation side-by-side navigation;
- the validation report;
- direct Markdown editing;
- per-block retranslation;
- retrying failed or flagged chunks.

Publishing changes the document to `publish: true` through the existing
document write path. Only then does the translation enter the normal published
feed, search, categorization, embedding, collision, related-document, and
Universe projections. Automatic publication is not the default; it may later
be enabled per trusted translation profile.

### Initial HTTP and worker surface

Exact route names may follow repository conventions, but the initial HTTP
surface must support these capabilities:

```text
POST   /api/translation-sources/resolve
POST   /api/translation-jobs
GET    /api/translation-jobs
GET    /api/translation-jobs/{id}
POST   /api/translation-jobs/{id}/cancel
POST   /api/translation-jobs/{id}/publish
```

The separately launched worker uses the Translation module's PostgreSQL store
directly. Claim, heartbeat, progress, completion, and failure are not exposed
as unauthenticated HTTP routes. Store operations enforce lease ownership and
idempotency; repeated completion or progress writes must not duplicate
documents or regress a terminal job state.

## Consequences

### Positive

- Paper translation becomes an observable product workflow rather than a
  manual conversation followed by an upload.
- Jobs survive process restarts and can resume at chunk granularity.
- The existing provider configuration is reused by three workloads.
- Agent implementation details remain local to the worker.
- Full-text fidelity becomes a verifiable contract.
- Draft review prevents incomplete output from silently becoming public.
- Published translations automatically benefit from MDHub's existing
  knowledge projections.

### Negative

- PostgreSQL gains job, chunk, artifact, lease, and provenance state.
- A second long-running process must be deployed and monitored.
- PDF extraction and OCR quality vary by source and require explicit failure
  reporting.
- Provider costs and latency grow with paper length.
- Publisher pages and redirects create a new server-side fetch security
  surface.
- Bilingual block identity makes later editing and source-version upgrades more
  complex than storing one opaque Markdown result.

## Alternatives considered

### Call the LLM directly from the browser

Rejected. It exposes credentials, ties completion to a browser session, and
provides no durable recovery or trustworthy validation.

### Run the whole translation inside the Go web process

Rejected. Long extraction and translation work would compete with serving
requests, and process restart would complicate recovery. The web process owns
job state; a worker owns execution.

### Spawn an arbitrary local Agent CLI from MDHub

Rejected. It couples deployment to one host and CLI, expands command-execution
authority, and makes credentials and process supervision harder to reason
about.

### Use the existing in-memory job queue

Rejected. Translation is long-running, expensive, and not safely
reconstructable after a restart. PostgreSQL is the source of truth.

### Translate the entire paper in one model request

Rejected. Context and output limits make truncation likely and prevent
chunk-level retry, progress, and completeness evidence.

### Publish automatically as soon as the worker returns

Rejected for the default profile. A successful model response is not proof of
complete translation. The initial workflow requires validation and user review.

## Delivery sequence

1. Extract and test the shared LLM provider module; migrate classification and
   collision callers without behavior changes.
2. Add the durable translation schema, state transitions, leases, and module
   tests.
3. Add safe source resolution for arXiv and direct PDFs, plus PDF upload.
4. Implement the Translation Agent Worker with extraction, chunk persistence,
   provider calls, and resume behavior.
5. Add deterministic validation and the stored validation report.
6. Add `/translations/new`, job progress, bilingual draft preview, retry, edit,
   and publish flows.
7. Add DOI and generic publisher-page resolution after the arXiv/direct-PDF
   path is reliable.

## Acceptance criteria

- A user can paste an arXiv abstract URL, confirm the detected paper, leave the
  page, and later return to the same durable job.
- A worker restart during translation resumes without redoing completed chunks
  or duplicating the output document.
- A direct public PDF URL and an uploaded PDF use the same pipeline after
  source capture.
- An inaccessible publisher source transitions to `needs_input` with a PDF
  upload action rather than a generic failure.
- A deliberately truncated translation fails validation and cannot reach
  `draft_ready`.
- A completed job creates an unpublished `_translations/<slug>` document with
  source and model provenance.
- Publishing uses the existing document path and makes the translation
  searchable and eligible for embeddings and the Universe.
- Existing classification and collision tests pass against the extracted LLM
  provider module.
