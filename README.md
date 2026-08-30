# Telltale 后端异常检测 Agent

Telltale 是一个证据驱动的后端异常检测与诊断 Agent，负责通过结构化工具结果、上下文记忆和可验证的 Evidence 协助定位应用、数据库和业务链路问题。运行时采用可替换存储和可选集成。

## 运行服务

环境要求：Go 1.24.x、一个 OpenAI-compatible chat model provider，以及知识/长期记忆启用时所需的 embedding provider。

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

## 按需启用的集成

支付观测、Prometheus、外部数据库诊断和通用 MCP 工具仍可作为显式配置的外部能力使用；默认不会主动连接这些服务。设置 `watch.enabled=true`、`watch.diagnosis.enabled=true`，并在具体规则上设置 `auto_diagnose: true` 后，watcher 会在事故打开时创建可恢复的 SQLite 诊断任务；该能力只读且不会自动修复。数据库 Agent 只使用通用 `db_*` 工具，由 `database.targets` 选择实际适配器。MySQL 时间窗口查询严格读取 `mysql.slow_log`；PostgreSQL 由 `telltale watch` 定时采样 `pg_stat_statements` 增量。两种历史能力均须显式配置，来源缺失时会返回证据缺口，不会把累计快照伪装成窗口数据。

`db_slow_queries` 未指定时间时，以目标数据库的 `CURRENT_TIMESTAMP` 为终点查询最近 30 分钟；最大跨度为 24 小时。CLI 的 `--window` 是 `--last` 的兼容别名。`server-health`、`active-queries`、`wait-events` 和 `locks` 仍是当前快照，不接受历史时间参数。watcher 规则只有同时启用 `database_probe.enabled` 并配置逻辑 target 时，才会为 observability 事故增加一次受控慢 SQL 查询；target 与事故窗口由运行时锁定。

## 验证

```bash
go test ./...
```

`manifest/docker/docker-compose.yml` 提供 Telltale 容器和 SQLite 数据卷的默认部署方式。
