# Telltale 后端异常检测 Agent

Telltale 是一个证据驱动的后端异常检测与诊断 Agent，负责通过结构化工具结果、上下文记忆和可验证的 Evidence 协助定位应用、数据库和业务链路问题。运行时采用可替换存储和可选集成。

主 Agent 维护竞争假设、选择调查问题、整合证据并形成最终结论。默认只提供两个子 Agent：`observability_agent` 调查实际运行状态，使用指标、日志、链路、基础设施、业务状态及获授权的只读数据库工具；`mechanism_agent` 调查设计和实现，使用文档及代码检索读取规则、配置和可用版本证据，返回可验证预测。数据库与知识不再作为独立委派角色。简单问题无需同时调用两者。工具仍保留原数据访问权限和能力检查；现有 MCP 的 database、knowledge 工具域及授权配置继续沿用。

## 运行服务

会话压缩在准备下一次诊断请求时按消息数和 token 阈值触发，调用 quick 模型合并旧状态与待归档消息，保存目标、约束、报告事实、假设状态、证据缺口和下一步。每项携带原文引用与消息哈希；程序校验来源、JSON 完整性及长度，结构化 Claim 事实另行保留。最近消息保持完整，SQLite 原始消息不删除，并通过已压缩消息数量恢复读取位置。旧文本摘要兼容读取。模型失败、超时、输出截断、来源无效或持久化失败会保留原状态并返回压缩错误。压缩调用超时60秒，最多输出8192 token，状态长度沿用 `context.compaction.max_summary_chars`；触发时会增加一次模型调用和延迟。原文匹配校验不能证明摘要语义完整，需持续核查纠正、否定和版本边界。

环境要求：Go 1.24.x、一个 OpenAI-compatible chat model provider，以及知识检索启用时所需的 embedding provider。

长期记忆使用 Profile/Event：`memory.mode: shadow` 异步提取但不注入回答上下文，`active` 启用上下文读取，`off` 关闭提取与读取。会话历史、摘要和工具记录独立保留。管理使用 Profile、Profile Candidate 和 Event 接口；旧 `/memories`、`/memory_candidates` 接口已移除。已有数据库中的旧记忆表保留数据，但不再读取、写入或自动迁移；新数据库不再创建旧表。

```bash
go run .
```

根目录入口继续启动兼容 HTTP 服务，默认监听 `http://127.0.0.1:6872`。SQLite 路径可通过环境变量覆盖：

```powershell
$env:SUPERBIZ_SQLITE_PATH = "data/demo.db"
go run .
```

配置示例见 [`manifest/config/config.yaml`](manifest/config/config.yaml)。

## 命令行入口

```bash
telltale diagnose "支付回调最近大量失败，请分析影响范围和可能原因"
telltale diagnose --json "数据库连接池耗尽"
telltale sql slow-sql --last 30m --limit 20 --format json
telltale sql slow-sql --since 2026-08-11T01:00:00Z --until 2026-08-11T01:30:00Z
telltale watch --once
telltale incident list --status firing --service card-service
telltale incident show inc_xxx --json
telltale knowledge recall --query "支付回调超时"
```

通过 `--conversation-id` 可以显式恢复 SQLite 会话；未提供时每次诊断创建隔离会话。`telltale serve` 用于显式启动 HTTP 适配器，`--config-dir` 可放在命令前或命令参数中指定配置目录。CLI 默认只读，不提供自动修复入口。

以下旧入口保留为兼容包装器，业务逻辑与统一 CLI 共用：

```bash
go run ./cmd/sre-sql-observe slow-sql
go run ./cmd/sre-code-search search --query callback_worker
go run ./internal/ai/cmd/watch_cmd -once
go run ./internal/ai/cmd/knowledge_cmd index
```

知识文档默认读取 `data/docs`。索引命令会把 embedding 和元数据写入 `knowledge_documents`。

使用 `go run ./internal/ai/cmd/knowledge_cmd index --docs <目录> --config-dir <配置目录>` 重建指定目录内的 Markdown 来源。默认通过 `quick_chat_model` 为每个分块生成简短上下文，embedding 与重排序使用“生成上下文 + 原始分块”；返回的 `content`、证据中的 `runbook_rule` 保留分块原文，metadata 的 `_context_prefix` 属于生成内容。旧索引保持可用，重新索引后才具有 contextual embeddings。

检索使用 SQLite FTS5 BM25 与 dense cosine 双路召回，通过 RRF 融合后交给现有重排序。`knowledge_retrieval.hybrid_candidates` 为每路及融合后的候选上限（默认12），`rrf_k` 为正数平滑参数（默认60）；融合按排名计算，不直接比较 BM25 与向量分数。同分时优先关键词排名，再按文档ID固定排序。英文标识保留下划线，中文按相邻双字生成词项；不执行模型查询扩写。结果元数据记录关键词/向量排名、原始分数和融合分数，原文保持独立。

启动时通过第7次数据库迁移补建关键词索引，回填已有分块、生成上下文和来源标题，不重新调用 embedding 或 chat 模型。后续索引写入、关键词更新和索引版本变化在同一事务内完成。向量按数据库和索引版本保留一份进程内快照，版本未变时复用已解析、归一化的向量；跨进程 CLI 更新后自动刷新。向量相似度仍为精确扫描，尚未使用 ANN，大规模数据仍需评估计算和内存成本。已有实验结果保持原样，新运行使用混合检索，不能与旧 dense-only 数字直接归因比较。

文档状态由同路径的 JSON 文件显式声明，例如 `runbook.md.meta.json`：

```json
{
  "state": "current",
  "version": "v2",
  "verified_at": "2026-11-04T00:00:00Z",
  "verified_by": "service-owner",
  "valid_from": "2026-11-04T00:00:00Z",
  "valid_until": "2027-01-01T00:00:00Z"
}
```

`state` 支持 `unverified`、`current`、`planned`、`deprecated`。缺少声明的旧资料按 `unverified` 读取，不根据文件名、正文日期或修改时间推断有效性。`current` 必须声明版本、核验时间与核验者；被替代的资料可设置 `superseded_by` 为替代来源标识，并使用 `deprecated`，不能同时声明 `current`。所有时间使用 RFC3339；有效期为左闭右开区间。声明不证明当前代码或生产部署，`planned` 也不证明已实现。

默认 `current` 检索只允许处于有效期内的 `current` 和 `unverified` 资料，排除计划、废弃、被替代、尚未生效及已经失效的资料；相同融合分数时优先已核验资料。未核验资料仍可返回，但会明确标记。历史查询需显式使用 `history`，可结合精确版本和 `as_of` 时间；历史模式未提供时间时允许返回失效资料以支持版本比较。筛选发生在两路候选数量限制之前，缓存按模式、版本和时间分开，并在生效、失效或核验时间边界到期。检索结果、原文证据和任务段落选择保留 `document_policy` 及适用范围。Agent 对方案或历史问题使用历史模式，并保留版本限制。

```bash
go run ./cmd/telltale knowledge recall --query "授权规则" --mode history --version v1
go run ./cmd/telltale knowledge recall --query "授权规则" --as-of 2026-11-04T00:00:00Z
go run ./cmd/telltale knowledge annotate --source data/docs/runbook.md
```

索引命令自动读取 `.meta.json`。已有索引仅修改状态时，可用 `knowledge annotate`，默认读取 `SOURCE.meta.json`，也可使用 `--metadata FILE`；`--source` 必须与索引中的来源标识完全一致。该命令在事务内更新所有对应分块的声明并使缓存失效，保留原文、分块ID和向量，不调用模型。现有文档未被批量核验，需由维护者补充声明；旧实验使用的冻结文件与结果不会被修改。

`knowledge_index.contextual_enabled: false` 可生成普通索引用于对照；开启后生成失败会直接报错。设置 `embedding_model.max_input_tokens` 为实际服务的输入上限（默认2048）；索引使用 UTF-8 字节数作为保守预算并逐块生成向量。上下文最多160个估算 tokens，生成请求最多1024 tokens（包含提供方的推理消耗），模型调用超时60秒，embedding 超时30秒。父文档超过 quick 模型配置窗口时改用完整章节，并在 `_context_scope` 标明 `section`；章节仍超出预算时要求拆分源章节，不截取资料。Markdown 标题分割沿用现有 Eino splitter 的空白规范化行为。

每个来源的全部上下文和向量生成成功后，SQLite 事务替换该来源，清除修改或删除的旧分块；失败保留该来源原索引，其他来源不受影响。空文件会清除自身旧分块；目录中已经删除的文件不会自动清除。元数据保存来源、章节路径、源文档/分块 SHA-256、提示词版本和模型身份，仅在这些信息匹配时复用上下文。查询缓存读取数据库索引版本，CLI 更新后无需等待 TTL。指定目录的多个文件逐个提交，后续文件失败不会撤销此前成功文件。

知识结果压缩默认使用 `knowledge_compression.strategy: baseline`。显式设置 `task` 后，仅对超过当前工具内容预算的知识结果调用 quick 模型选择原文段落编号；`generic` 使用同一选择器与预算，供移除问题目标的对照。选择不会改写引文，保留邻近条件段落和来源元数据，移除 data 中的重复文档表示，返回部分结果标记后重新签名。失败或超出预算会返回证据缺口。选择器输入同时受自身模型窗口、输出预留、安全余量和12,000个估算 tokens上限约束，输出最多4,096 tokens，超时60秒，每个请求最多调用5次。原始授权结果另存为 `audit_only` 记录，排除于模型记忆和 Claim 输入；更换问题或诊断状态会使旧选择失效，需要重新检索。

上下文预算由 `internal/ai/contextbudget` 集中分配：在扣除模型输出、系统/工具声明和安全余量后，预留可用输入的四分之一作为工具总空间，最多8,000个估算 tokens；历史与其他上下文层使用剩余空间。单次工具输出不超过总空间且最多2,000 tokens，其中预留400 tokens用于签名和元数据；知识选择目标为剩余内容空间的75%。模型窗口较小时自动收紧这些预算。同步、流式和子Agent共用本轮预算，投影、签名和最终模型请求检查使用同一份分配；并发调用先预约再按实际输出结算。未建立请求分配的独立工具调用和旧评测入口使用集中定义的默认限额。预算报告包含工具总预留、单次上限和知识选择目标。生产压缩策略默认值不变。

可重复实验程序为 `internal/ai/cmd/compression_eval`，使用 `reports/contextual-compression/` 中的独立数据库。`prepare` 建立所有组共用的普通索引，`calls` 保存真实模型的检索调用和真实召回快照，`freeze` 保存历史基线投影视图，`run` 运行成对比较，`e2e` 运行知识 Agent 和 Claim 收敛，`summarize` 汇总。原始基线源码、输入哈希、固定语料、逐例结果与 usage 均保存在该目录；开发与留出按来源分离。服务余额不足与选择器失败单列，缺失 usage 不视为零消耗。此次真实调用受模型余额不足及重排序404限制，尚无充分净收益证据，默认保留 `baseline`。

`run` 默认使用 `--baseline-view current`：三组共用当前标识保留逻辑，仅切换固定投影、任务选择或通用选择。`--baseline-view historical` 用于回放保存的旧基线；旧比较混入标识修复，引用差异不能单独归因于压缩。结果记录对照模式，程序拒绝将不同模式追加到同一结果文件。

运行相同固定输入的公平对照时，复制已有 `snapshots.json` 到新的结果目录，避免覆盖已保存的测量：

```powershell
New-Item -ItemType Directory -Force reports/contextual-compression-shared-identifiers
Copy-Item reports/contextual-compression/snapshots.json reports/contextual-compression-shared-identifiers/
go run ./internal/ai/cmd/compression_eval -phase run --baseline-view current -out reports/contextual-compression-shared-identifiers
go run ./internal/ai/cmd/compression_eval -phase summarize -out reports/contextual-compression-shared-identifiers
```

## 按需启用的集成

支付观测、Prometheus、外部数据库诊断和通用 MCP 工具仍可作为显式配置的外部能力使用；默认不会主动连接这些服务。设置 `watch.enabled=true`、`watch.diagnosis.enabled=true`，并在具体规则上设置 `auto_diagnose: true` 后，watcher 会在事故打开时创建可恢复的 SQLite 诊断任务；该能力只读且不会自动修复。观测 Agent 使用通用只读 `db_*` 工具进行数据库运行诊断，由 `database.targets` 选择实际适配器。MySQL 时间窗口查询严格读取 `mysql.slow_log`；PostgreSQL 由 `telltale watch` 定时采样 `pg_stat_statements` 增量。两种历史能力均须显式配置，来源缺失时会返回证据缺口，不会把累计快照伪装成窗口数据。

`db_slow_queries` 未指定时间时，以目标数据库的 `CURRENT_TIMESTAMP` 为终点查询最近 30 分钟；最大跨度为 24 小时。CLI 的 `--window` 是 `--last` 的兼容别名。`server-health`、`active-queries`、`wait-events` 和 `locks` 仍是当前快照，不接受历史时间参数。watcher 规则只有同时启用 `database_probe.enabled` 并配置逻辑 target 时，才会为 observability 事故增加一次受控慢 SQL 查询；target 与事故窗口由运行时锁定。

## 验证

```bash
go test ./...
```

`manifest/docker/docker-compose.yml` 提供 Telltale 容器和 SQLite 数据卷的默认部署方式。
