# ADR: Reading-first emergence and user-owned Sparks

- Status: Accepted
- Date: 2026-08-02
- Decision owner: MDHub owner

## Context

MDHub currently exposes Documents, Sparks, Translations, and Universe as
separate destinations. The article reader can show semantic related documents
and collision insights, but both appear after the article. The Sparks surface
then asks the user to visit another page to capture fragments, curate
collisions, and inspect growth.

This puts the product's value behind a data flywheel: the user must first
publish or capture enough material, wait for embeddings and collisions, and
then remember to visit Sparks or Universe. It is a weak cold start for a
personal reader. It also risks mixing machine-generated prompts and imported
material with thoughts the user actually authored.

MDHub's primary job is reading. New connections and thoughts should emerge in
the reading context, using the current document as sufficient initial context.
Personal history may improve ranking later, but it must not be required before
the product becomes useful.

## Decision drivers

- Reading, including a newly translated paper, is the primary entry point.
- The first document must be enough to produce a useful reflective affordance.
- Existing semantic neighbours and collision insights should be reused.
- Machine suggestions must remain distinct from user-authored ideas.
- Suggestions must be sparse, explainable, and dismissible rather than an
  interrupting generated feed.
- Source Markdown and the selectable article DOM must remain unchanged.
- The first slice should not add a new synchronous LLM call to page rendering.
- Mobile readers need the same capability without reserving a permanent rail.

## Decision

Introduce **Reading Emergence** as a reader-owned interaction layer.

The product roles are:

- **Reader** is the entry point and the place where thinking happens.
- **Universe** is a background relationship engine and optional exploration
  view.
- **Emergence** is an ephemeral, system-proposed connection, question, or
  reading direction.
- **Spark** is a durable thought explicitly accepted or written by the user.

An Emergence is never persisted as a Spark automatically. The system may
provide context, but a Spark is created only after the user writes and submits
their own response.

### User flow

```text
open or translate a document
        -> read normally
        -> a quiet Emergence affordance becomes available
        -> inspect up to three connections or questions
        -> follow a related document, ignore the suggestion, or write a response
        -> explicit response is saved as a user-owned Spark
```

The reader keeps source content untouched. Desktop and mobile use an
out-of-flow companion panel, so opening Emergence does not rewrite article
HTML, move comment anchors, or inject generated prose into the document.

### Emergence model

The initial public model is intentionally small:

```text
ReadingContext
  - source slug and title
  - optional currently visible excerpt

Emergence
  - stable client identity
  - kind: reflection | question | connection | related
  - title and explanation
  - provenance and optional related document
  - relevance score when one exists
```

Initial candidates come from existing data:

1. non-dismissed collision questions;
2. non-dismissed collision explanations;
3. semantic related documents not already represented by a collision;
4. one current-document reflection when no corpus-backed candidate exists.

Questions are ranked ahead of passive connections, then by collision verdict
and score. Related documents fill remaining slots. The feed is capped at three
items. Dismissed collisions are not resurfaced.

The cold-start reflection is deliberately deterministic and requires no user
profile or model request. It asks the reader to test an assumption in the
current document and may quote the currently visible block. It is a fallback,
not a claim that the system discovered a specific fact.

### Saving a response

Each Emergence may open a small response form. Only non-empty user text can be
saved. The resulting note:

- uses the existing `_sparks/<timestamp>-<suffix>` storage convention;
- keeps `type: fleeting`;
- records `source: reading/<document-slug>`;
- places the user's response first;
- appends clearly labelled reading context and a Wiki Link to the source;
- remains unpublished unless the existing Spark workflow changes that policy.

This provenance makes the thought traceable without representing the machine
prompt as the user's authorship.

### First implementation slice

The first slice will:

- add a Reader Companion to published document pages;
- reuse `/api/related` and `/api/collisions?slug=...`;
- merge and rank candidates in a pure, tested frontend module;
- expose a compact fixed trigger and a responsive companion panel;
- support opening related documents and explicitly saving a response;
- remove the duplicate related/collision sections from the article footer;
- retain the existing Sparks destination as the archive and curation surface.

## Deferred work

- paragraph embeddings and claim-level retrieval;
- automatic extraction of assumptions, counterexamples, and citations;
- external paper discovery from arXiv, DOI, or citation graphs;
- LLM-generated explanations tied to exact source spans;
- per-suggestion dismiss/helpful feedback and personalized ranking;
- scroll-aware continuous updates while the panel is open;
- renaming or demoting the top-level Sparks navigation item.

These additions must preserve the same authorship boundary and should run
asynchronously or on explicit demand. They must not make article rendering wait
for an LLM.

## Consequences

### Positive

- MDHub provides value from the first document instead of requiring a mature
  personal corpus.
- Existing semantic and collision investments become visible in the reading
  flow.
- The system can inspire without silently manufacturing user-owned notes.
- The Reader, Sparks, Translation, and Universe features become one loop.
- The initial implementation adds no provider cost or page-render latency.

### Negative

- A generic cold-start reflection is less specific than a grounded model
  insight.
- A floating companion adds another reader control and must remain quiet.
- Collision data is loaded client-side, so candidates may appear shortly after
  the article itself.
- Existing footer relationship sections are replaced by a JavaScript-driven
  interaction.

## Success signals

Future privacy-preserving product telemetry should distinguish:

- companion opened;
- related document followed;
- response form opened;
- response saved as a Spark;
- suggestion dismissed.

The feature is successful when readers follow or save useful emergence items,
not when the graph or suggestion count grows.
