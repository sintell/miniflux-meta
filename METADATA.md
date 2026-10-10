# Client-namespaced article metadata

This fork adds user-owned string metadata to Miniflux's native API and authenticated
web reader. It does not execute processors or assign special semantics to any field.
Publisher title, content and tags remain independent. Metadata edits do not update
`entries.changed_at`, reorder history, or appear in changed-since synchronization.

## Names and values

Fields use `client_id.name`. Each component contains 1–64 lowercase ASCII letters,
digits, underscores or hyphens. A dot is the namespace separator. An authenticated
user owns the namespace; client IDs are application names, not credentials or
additional permission scopes. That user's applications can read and write each
other's registered fields using their ordinary Miniflux credentials.

Values and descriptions are text. Values are never trimmed, parsed as numbers,
interpreted as HTML, or converted to native tags. Empty string is an assignment;
missing assignment is different. PostgreSQL text requires UTF-8 without NUL.
Metadata write request bodies are limited to 4 MiB.

## Registry

```http
POST /v1/meta/register
Content-Type: application/json

{"client_id":"application_a","name":"rating","description":"Producer-defined relevance convention"}
```

Response (`201` for new definitions; `200` for repeat registration):

```json
{"client_id":"application_a","name":"rating","qualified_name":"application_a.rating","description":"Producer-defined relevance convention"}
```

Repeated registration returns the canonical definition, preserving its existing
description even when the supplied description differs. There is no registry edit
or delete operation. `GET /v1/meta` lists the current user's definitions; optional
`client_id=application_a` restricts the list. The response is a JSON array.

## Assignments

```http
PUT /v1/entries/123/meta/application_a/rating
Content-Type: application/json

{"value":"8.5"}
```

```http
PATCH /v1/entries/123/meta/application_a
Content-Type: application/json

{"rating":"8.5","summary":"The release introduces…"}
```

Both return `204`. PATCH merges supplied fields and preserves omitted fields and
other namespaces. Every definition must already exist. An invalid/unregistered
field or non-string value rejects the entire batch with `400`, preserving all
assignments. Null is not deletion. Different concurrent fields survive; the last
committed write to a field wins.

```json
{"error_message":"Metadata field 'application_a.rating' is not registered"}
```

`DELETE /v1/entries/123/meta/application_a/rating` returns `204`, including when an
assignment is already absent. It still requires a registered definition and an
owned entry. Inaccessible/nonexistent entries return `404`. Deleting an assignment
does not delete its definition. Normal entry and account deletion cascade to
associated metadata.

## Projection and filters

These parameters apply to native entry lists, feed/category entry lists, and
single-entry GETs. The ID list supports filters; `meta` is validated there but does
not change its ID-only response.

```text
/v1/entries?status=unread&filter=application_a.rating:8.*&meta=application_a.summary
/v1/entries?filter=application_a.rating:7.*&filter=application_b.rating:recommended
/v1/entries/123?meta=*
```

Use your HTTP library's repeated query-parameter support to encode these URLs.

* No `meta`: no metadata property or metadata-value query.
* Repeated `meta=client.field`: only assigned requested fields.
* Explicit projection with no assignments: `"meta": {}`.
* `meta=*`: all assigned fields; combining it with named selections still selects all.
* Projection never excludes an article.
* Repeated `filter` conditions are OR, inside the surrounding user, feed, category,
  tag, status, visibility and search constraints. Sorting is unchanged.
* Split filters at the first colon; the pattern can contain further colons.
* Syntactically invalid or unregistered query fields return `400`. Registered but
  unassigned fields are normal: absent from projection and do not match filters.

Matching is case-sensitive and covers the entire string. `*` matches zero or more
characters; **a standalone `*` requires a nonempty assignment**. `7.*` is a string
prefix, not a numeric range. `\*` means literal asterisk and `\\` literal backslash.
Other escapes and trailing backslashes are invalid. `%`, `_`, and regex punctuation
are literal. An empty pattern matches an assigned empty string.

## Web settings and bulk actions

Settings → Meta provides repeatable OR conditions for the Unread shortcut and
ordered selections for title badges and content headers. Applications register
definitions through the API; the panel only selects them. Remove a row (or clear
its field selection) to remove that preference. New strings are translated into
English and Russian; other catalogs currently contain English placeholders.

The Unread menu URL contains the saved filters and its badge counts that saved
query, including normal feed/category visibility rules. Opening `/unread` directly
is unfiltered. Native API calls and compatibility APIs do not inherit the shortcut.
The badge refreshes after web status actions and on subsequent page loads; writes
from external producers do not push live updates into an already open browser tab.

Pagination, collection article links, and previous/next links preserve metadata
query parameters. Title values are compact escaped text. Content-header values
preserve line breaks and format backtick-delimited inline code as `<code>` and
backtick-fenced code blocks as `<pre><code>`. Fences may include a language label;
it is not rendered or used for syntax highlighting. Other markup remains escaped
text, including HTML inside code. API values remain the original strings, and
existing stored values receive this formatting without regeneration. Empty/missing
values render no badge or section. Anonymous shared pages do not load or render
metadata.

Collection “mark all as read” actions update matching unread articles across all
pages, evaluated in one SQL statement at action execution. They retain collection
scope, feed checked-at/category time cutoffs, authentication and CSRF protection.
Explicit-ID status updates remain unrestricted by saved metadata preferences.

Fever and Google Reader retain their existing wire contracts; they neither expose
metadata nor use the saved Unread query.

## Storage, schema initialization and maintenance

The extension uses `meta_registry`, `entry_meta`, and `user_meta_preferences`.
Assignment primary keys prevent duplicate values and support entry-scoped lookup.
Definition/entry and ownership indexes support filtering and cleanup. Projection
uses one selective query per page, avoiding N+1 queries and ordinary API summary
loading. Arbitrary wildcard searches still require inspecting candidate text;
there is no claim that an index accelerates every substring pattern.

Extension version 1 is tracked in `miniflux_extensions`, independently from the
upstream positional `schema_version` sequence. Normal Miniflux migration execution
also initializes the extension. Extension initialization is transactional,
advisory-lock serialized and safe to rerun. Startup checks both schemas.

**Legacy-data conversion is deliberately outside this change.** Existing injected
summaries and other older enrichment will be handled by a later one-shot script.

Before any future rollout, back up the full database and pin the fork image by
version/digest. Rolling back to a compatible fork keeps extension tables and data;
do not drop them. Returning to an official image hides this feature. The inspected
upstream schema check rejects older schema versions but accepts newer versions;
that is not proof that an older official binary is compatible with a newer schema.
Test the exact binary/database pairing on a restored backup before such a rollback.
Upstream merges retain their migration numbers; future extension changes increment
the independent extension version.

The fork source is https://github.com/sintell/miniflux-meta on branch
`feat/client-namespaced-metadata`. Its Go application image identity is
`sintell/miniflux-meta`, **not** `sintell/miniflux-ai`. The first metadata release
tag is `2.3.3-meta.1`, targeting Linux amd64 and arm64. The application version
remains `2.3.3` for client compatibility; the image tag identifies the extension
release and OCI revision identifies its exact source commit.

Build with `packaging/docker/alpine/Dockerfile.meta`, passing `VERSION`,
`REVISION` and `BUILD_DATE` as build arguments. Existing PostgreSQL and Compose
networking suffice. Image publication does not deploy the NAS. Production image
selection, migration execution, ownership and NAS verification require a
separately authorized rollout.

## Testing

Unit/client tests run with ordinary `go test ./...`. The end-to-end test is opt-in:

```sh
docker run --detach --rm --name miniflux-meta-test-db \
  --publish 127.0.0.1:55439:5432 \
  --env POSTGRES_PASSWORD=metadata-test-only \
  --env POSTGRES_DB=miniflux_meta_test postgres:17

TEST_META_DATABASE_URL='postgres://postgres:metadata-test-only@127.0.0.1:55439/miniflux_meta_test?sslmode=disable' \
  go test -race -v ./internal/metatest

docker stop miniflux-meta-test-db
```

These are **local development commands**. The test requires a loopback database
whose name ends in `_test`, creates an isolated schema, and removes only that
schema. It runs native HTTP requests, actual web login/CSRF/settings/navigation,
transactional storage/concurrency and refresh-preservation checks. It also runs a
20,000-entry synthetic filtered count and prints `EXPLAIN (ANALYZE, BUFFERS)` for
the OR/EXISTS query shape. This is not a NAS performance guarantee. Browser layout,
keyboard/gesture behavior and actual production performance need rollout testing.

## Companion integration checklist (separate follow-up)

1. Register `miniflux_ai.rating`, `miniflux_ai.summary`, and `miniflux_ai.topics`
   idempotently before processing.
2. Read the ordinary API without the user's saved shortcut filters so unprocessed
   articles remain discoverable. Request useful existing metadata explicitly.
3. Use original title/content, native publisher tags, and selected metadata as input.
4. Serialize every result as a string and PATCH one namespace atomically. Any
   numeric rating convention belongs to the producer.
5. Keep producer retry/deduplication state; retry failed batches without assuming
   any field was written. Treat an unregistered-field 400 as a registration/config
   problem rather than a successful processing attempt.
6. Change future summary writes from content prepending to metadata. Handle old
   enrichment with the later one-shot script; do not rewrite content incidentally.
7. Keep Newscope feed cutover, backlog processing and cross-feed deduplication as
   separate operations.
