# 单 Agent 与多 Agent 对照测试

此实验比较当前 RCAEval 排障任务的执行结构：

- `multi`：协调 Agent 通过 `observability_agent` 委派调查。
- `single`：一个可观测性 Agent 直接调用相同遥测工具，不创建或暴露子 Agent 工具。

单 Agent 保留模型循环、竞争假设、证据处理、查询计划及提交检查，直接使用观测调查角色。数据库运行工具已归入该角色，文档与代码由多 Agent 模式的 `mechanism_agent` 处理；各工具继续保留原调用者权限与能力检查。本题集的隔离配置只授权 RCAEval 遥测工具。角色划分变更后应使用同一新二进制重跑两组，历史成绩不能直接视为新版结构的对照结果。

两组均使用 `baseline_change_v1`、完整假设机制、隔离 SQLite 和关闭长期记忆。每题最多 16 次提供商请求、150000 token、360 秒，输出上限 4096 token。题目及模型请求串行，服务端临时错误最多重试两次且计入预算，上游限流、额度错误或重试后错误会停止后续题目。子 Agent 调用本身消耗模型请求，单 Agent 节省的委派开销属于本次比较内容。

两组提示存在必要的角色差异：多 Agent 由协调者组织委派，单 Agent 使用原可观测性子 Agent 的提示直接调查；题目提示相应选择“委派”或“直接调用”。因此实验衡量整套执行结构，不能将差异全部解释为 Agent 数量本身。

## 构建

在 PowerShell 中执行：

```powershell
cd E:\edgeDownLoad\SuperBizAgent-release-2026-03-22\telltale
$env:PYTHONUTF8 = '1'
go build -o reports/agent-topology-eval.exe ./cmd/telltale
```

## 仅启动单 Agent 组

默认三次重复，每次六道回归题和两道留出题，共 24 次运行：

```powershell
& reports/rcaeval-20261107/venv/Scripts/python.exe scripts/agent_topology_eval.py `
  --topology single `
  --binary reports/agent-topology-eval.exe `
  --output reports/single-agent-eval-20261108
```

先检查计划而不创建目录或调用模型，可在同一命令末尾添加 `--dry-run`。缩小规模可使用 `--repeats 1`，对应八题；重复启动必须换一个输出目录，已有结果不会被覆盖。

## 启动两组对照

使用 `--topology both` 在同一新二进制下串行执行两组，每组 24 次，共 48 次：

```powershell
& reports/rcaeval-20261107/venv/Scripts/python.exe scripts/agent_topology_eval.py `
  --topology both `
  --binary reports/agent-topology-eval.exe `
  --output reports/agent-topology-comparison-20261108
```

仅运行多 Agent 组使用 `--topology multi`。所有方式都读取 `manifest/config/config.local.yaml` 中的 API 模型配置，不会使用 WorkBuddy。

## 结果与生效核对

输出目录中的 `protocol.json` 保存组别、重复次数、预算和二进制 SHA256；`results.json` 汇总有效提交、服务与指标 Top1、token 和委派调用次数，并保留逐题信息。每轮原始模型请求、工具 schema、工具返回、SQLite 和评分位于 `single` 或 `multi` 子目录。

汇总会检查单 Agent 的实际模型请求：三种领域子 Agent 既不能出现在工具 schema 中，也不能被调用。假设登记工具属于内部状态工具，继续保留。进程失败与未提交题均保留；发生接口错误停止时，未运行题不视为已完成评测。

题集沿用当前固定六题与两题留出，复制使用同磁盘硬链接，建议将输出目录放在当前 E 盘仓库的 reports 中。脚本不会修改题目文件。跨模型成绩、此前中断记录以及 WorkBuddy 尝试不得混入这一对照。

当前交付只完成代码检查、构建和不发模型请求的计划检查，尚无单 Agent 的真实诊断成绩。
