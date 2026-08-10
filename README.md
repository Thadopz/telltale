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
telltale sql slow-sql --limit 20 --format json
telltale watch --once
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

支付观测、Prometheus、外部数据库诊断和通用 MCP 工具仍可作为显式配置的外部能力使用；默认不会主动连接这些服务。数据库 Agent 只使用通用 `db_*` 工具，由 `database.targets` 选择实际适配器；当前仅实现 MySQL 适配器，未支持的数据库会返回能力缺口，不会伪造结果。

## 验证

```bash
go test ./...
```

`manifest/docker/docker-compose.yml` 提供 Telltale 容器和 SQLite 数据卷的默认部署方式。
