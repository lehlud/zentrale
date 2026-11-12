# Chat attachments: backend contract

## HTTP (authenticated session, same origin)

* `POST /attachments/:agent/:id?name=<URL-encoded-original-name>` accepts a raw
  binary request body (including an empty body). Clients send `File.type` or
  `application/octet-stream`; the server **sniffs** the actual MIME type instead
  of trusting this header. Every non-image file type is allowed.
* `id` is a client-generated UUID or another receipt-safe identifier matching
  `[A-Za-z0-9_-]{1,128}`. It is scoped to the agent and its current project.
* Successful uploads return HTTP 200 and JSON
  `{id,agentId,projectId,name,mimeType,size,path,machineId}`. Repeating identical
  bytes and the original name returns the same metadata; changed retries fail.
* `GET /attachments/:agent/:id` serves only stored, sniffed PNG/JPEG/WebP/GIF
  bytes, with their sniffed MIME type, `X-Content-Type-Options: nosniff`, and
  `Cache-Control: private, no-store`. SVG, HTML, other files and unknown IDs are
  never served inline. This URL is suitable for persistent chat thumbnails.
* Both methods reject unauthenticated calls, foreign origins / cross-site Fetch
  Metadata, missing projects/agents, and subagent/approver targets. The API has
  the same root-main-agent boundary as the existing prompt/submitInput API.
* Limits: 25 MiB per file, 10 files / 100 MiB per submitted message. There is no
  additional agent-wide file-count/storage quota and no automatic expiry or
  eviction of uploaded files. Agent/project deletion performs scope cleanup.
* Oversized bodies return 413, authentication errors 401, target/origin errors
  403, unavailable raster previews 404, other upload validation/conflict errors
  400. Bodies are bounded before remote execution; uploads are serialized through
  a cancellation-aware engine gate to bound in-flight buffer memory. HTTP read
  and engine staging deadlines are ten minutes and cancellation closes reads/SSH.

## GraphQL and durable messages

The backwards-compatible mutation adds an optional argument:

```graphql
submitInput(agentId: String!, id: String!, text: String!, mode: String!,
            attachmentIds: [String!]): AgentInput
```

`AgentInput` gains `attachmentIds: [String!]!` and
`attachments: [Attachment!]!`; `Message` gains `attachments: [Attachment!]!`.
`Attachment` fields are non-null: `id`, `agentId`, `projectId`, `name`,
`mimeType`, `path`, `machineId` (`String!`) and `size` (`Int!`, bytes).
The existing reflection-based backend schema exposes these fields; frontend
generated types are generated separately by Main.

The old `Engine.SubmitInput` signature remains available and calls
`SubmitInputWithAttachments(..., nil)`. Empty text is allowed only with valid
attachments; the server never invents a replacement user task. All IDs, sizes,
ownership, duplicate IDs and aggregate limits are checked before the receipt is
committed. Receipt identity includes the ordered attachment IDs. Uploaded IDs
are not consumed: queue-edit / resubmit can reuse an existing own upload.
Queued receipts retain metadata but do not enter conversation or original-user-
instruction history until normal delivery. Delivery atomically writes receipt,
message and instruction records, preserving the metadata across queue/restart.

## Storage, sandbox and cleanup

Each file is staged at the fixed
`agentTemporaryDirectory(agentID)/attachments/<id>/<sanitized-name>` location.
There is no caller-selected destination. Unsafe path separators/traversal names
are rejected; display names are preserved in metadata, while actual basenames
are bounded and sanitized. Staging uses `connectForJob`,
`prepareRemoteWorkspace` and the engine sandbox. Disabled isolation is rejected.
The upload-specific sandbox path explicitly disables automatic VM recovery:
uploads may prepare/start a guest, but never reset it or widen host access.

The fixed Python helper streams binary stdin through a bounded temporary file,
checks exact size and SHA-256, fsyncs and commits with atomic no-replace links.
Directory descriptors and `O_NOFOLLOW` protect the root, intermediate directories,
manifest and final file; FIFOs/non-regular targets cannot block or be overwritten.
An immutable per-ID manifest also binds the original name/hash across a crash
before the database acknowledgement. Ordinary errors/cancellation signals remove
staging files; uncatchable process/host death can leave hidden `.upload-*` staging
files, never a partially committed visible attachment. Scratch-root preparation
itself no longer chmods through a symlink.

The separate `attachment_blobs` table retains raw binary bytes and metadata,
outside generic resources/events/snapshots. Original images and other files can
therefore survive backend restart and missing guest `/tmp` files. Before outgoing
main-agent requests, referenced files are checked/restaged idempotently on their
recorded assigned machine, without replacing changed files. Existing-file
conflicts or an unavailable original machine are explicit errors, not permission
to overwrite, move data to an unrelated machine, or reset a VM.

Agent/project deletion removes corresponding BLOB rows in the same transaction
as the existing resource cleanup. Existing agent-family VM removal handles the
remote environment. Shared repository content and other projects are untouched.
There is no new unauthenticated file server or generic download endpoint.

## Model, approver and instruction handling

Messages/receipts/history contain **metadata only**, never base64 or file text.
Only a private outgoing request copy hydrates raster image bytes, resolved by
agent/project/ID rather than user paths. `Message.Images` is excluded from JSON
and GraphQL. Provider adapters produce native image blocks:

* Responses: `input_image` with data URL, alongside `input_text`.
* Chat Completions: `image_url` block, alongside `text`.
* Anthropic: `image` block with `base64` source and `media_type`.
* Gemini: `inlineData` with `mimeType` and `data` (GIF remains a file reference
  because Gemini's inline-image protocol does not support it).

Non-image files, including large text pastes, are represented only by immutable
metadata / VM path and a clear untrusted-user-attachment note. No file is executed
or automatically expanded into prompt text. Provider/model-specific image size,
format and vision capability restrictions still apply and are reported by the
provider; the backend does not silently fall back to base64 text.

Original-instruction records and approval snapshots retain the same attachment
metadata. Approver/main system guidance explicitly treats attachments as
untrusted data, not authorization. Compaction transcripts preserve metadata/file
locations, and context cost estimates count metadata without counting base64 as
text. No sensitive blob bytes enter UI history, runtime snapshots or outboxes.

## Verification

* Python tests exercise binary/empty/idempotent writes, conflicting names/data,
  limits, traversal, symlinks at every level, FIFOs, truncated/digest-mismatched
  bodies, temporary-file cleanup, cancellation signals and missing-file restore.
* Provider HTTP tests cover the native image request blocks for all four existing
  protocols, non-image references and JSON blob exclusion.
* Backend tests additionally cover authenticated API/origin/project/kind bounds,
  MIME-spoofed raster previews, binary retries, message size/count limits, queue
  delivery/idempotence, original-instruction metadata, persisted BLOB restart and
  transactional deletion, and the no-automatic-VM-reset sandbox boundary.
* Local evidence and environment limits are reported to Main separately. Known
  Go linker `mapping output file failed` / DuckDB execution constraints are not
  bypassed; full datastore/agent/webapi execution must be verified in normal CI.
