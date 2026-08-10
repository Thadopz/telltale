# Telltale Backend Anomaly Detection Agent

Telltale is an evidence-driven backend anomaly detection and diagnosis agent. It uses structured tool results, context memory, and verifiable Evidence to help investigate application, database, and business-path failures. The runtime uses replaceable storage and optional integrations while keeping the core agents, tools, conversation memory, action confirmation, knowledge indexing, and watcher logic consistent. Gateway authentication, customer/business database tables, MySQL-backed session storage, and Milvus are not default runtime dependencies.

## Storage, identity, and integrations

- SQLite is created automatically at `data/telltale.db` by `utility/storage`.
- Conversations, summaries, session state, tool results, durable memories, watcher state, and incidents are stored in SQLite.
- Knowledge and durable-memory vectors are stored as JSON arrays in SQLite and searched with in-process cosine similarity. The configured embedding provider is still used to create vectors.
- ActionStore uses memory by default. Set `actions.store: sqlite` to persist pending confirmations across restarts.
- Identity comes from `identity.user_id`, `identity.tenant_id`, and `identity.roles`. Gateway request headers are no longer read. The legacy `userId` request field is accepted for compatibility but does not determine identity.
- MCP is optional. With the default empty `tools.mcp_servers`, no log MCP or SREGym MCP is contacted. Only explicitly configured generic MCP servers are registered.

## Context compaction

- When conversation history reaches the Token threshold configured by `context.compaction.trigger_ratio`, older messages are compacted while recent messages remain in the model context.
- Raw conversation messages remain in SQLite. The compacted summary and exact facts such as Evidence IDs, status codes, paths, request IDs, and Trace IDs are stored in `context_snapshots`.
- Before each model call, the pipeline dynamically calculates an aggregate budget from `context_window_tokens`, `max_output_tokens`, the default 128K working cap, the system prompt, Agent instruction, and the actual registered tool schemas. Missing model-window settings conservatively fall back to 32K. History, tool results, project context, and skills are trimmed against that budget while the current request and exact facts are preserved first.
- `context.max_prompt_tokens` remains an optional compatibility hard cap; when positive, the smaller of the dynamic budget and this value is used. When it is 0, the dynamic budget is used. When `context.compaction.max_tokens` is 0, the compaction limit is calculated dynamically as 50% of the available content budget and clamped to 8192~65536 Tokens.
- Model-call traces record estimated and observed PromptTokens, the budget, trimmed layers, and model window. Calibration stores aggregate values only in `context_budget_calibration`; prompt content is never persisted.

## Run the service

Requirements: Go 1.24.x, an OpenAI-compatible chat model provider, and an embedding provider when knowledge or durable memory is enabled.

```bash
go run .
```

The compatibility HTTP API listens on `http://127.0.0.1:6872`. Override the SQLite path with:

```powershell
$env:SUPERBIZ_SQLITE_PATH = "data/demo.db"
go run .
```

See [`manifest/config/config.yaml`](manifest/config/config.yaml) for the configuration template.

## Command-line entrypoint

The CLI calls the Telltale core runtime directly and does not depend on a local HTTP loopback:

```bash
telltale diagnose "Payment callbacks are failing heavily; analyze scope and likely causes"
telltale diagnose --json "The database connection pool is exhausted"
telltale sql slow-sql --limit 20 --format json
telltale watch --once
telltale knowledge recall --query "payment callback timeout"
```

Use `--conversation-id` to explicitly resume a SQLite conversation. Without it, each diagnosis gets an isolated conversation. Use `telltale serve` to start the HTTP adapter explicitly; `--config-dir` may appear before or after the command. The CLI is read-only by default and does not expose automatic remediation.

The historical entrypoints remain compatibility wrappers and share the same handlers:

```bash
go run ./cmd/sre-sql-observe slow-sql
go run ./cmd/sre-code-search search --query callback_worker
go run ./internal/ai/cmd/watch_cmd -once
go run ./internal/ai/cmd/knowledge_cmd index
```

Knowledge indexing reads `data/docs` by default and writes embeddings and metadata to `knowledge_documents`; Milvus, etcd, and MinIO are not required.

## On-demand integrations

Payment observability, Prometheus, external database diagnostics, and generic MCP tools remain available as explicitly configured external capabilities. They are not contacted by default. The database agent exposes only generic `db_*` tools; `database.targets` selects the adapter. The first release implements a MySQL adapter, while unsupported engines return an explicit capability gap instead of fabricated data.

## Verification

```bash
go test ./...
```

`manifest/docker/docker-compose.yml` provides the default Telltale container and SQLite data volume deployment; it does not automatically start MySQL, Milvus, etcd, MinIO, or Attu.
