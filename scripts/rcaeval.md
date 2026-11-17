# RCAEval 真实遥测评测

`rcaeval.py` 将 RCAEval RE2-OB 的六类故障各选取一个案例，使用 Telltale 的 `diagnose` 完整诊断链路查询真实指标、日志和链路。通过项目已有的 configured MCP 注册机制接入，生产配置保持原状。

依赖 Python 3.12、Go，以及 `scripts/rcaeval-requirements.txt`。上游工具和判分代码固定为 [RCAEval commit 90b63a4](https://github.com/phamquiluan/RCAEval/tree/90b63a46ac1e969f1fa9015318b51e455c62f8c4)。下载该提交的源码后，将其根目录作为 `--source` 传入。

在仓库根目录执行：

```powershell
$root = 'reports/rcaeval-20261107'
$source = "$root/vendor/source/RCAEval-90b63a46ac1e969f1fa9015318b51e455c62f8c4"
$env:PYTHONUTF8 = '1'
uv venv --python 3.12 "$root/venv"
uv pip install --python "$root/venv/Scripts/python.exe" -r scripts/rcaeval-requirements.txt
go build -o "$root/telltale.exe" ./cmd/telltale
& "$root/venv/Scripts/python.exe" scripts/rcaeval.py --root $root --source $source prepare
& "$root/venv/Scripts/python.exe" scripts/rcaeval.py --root $root --source $source check
& "$root/venv/Scripts/python.exe" scripts/rcaeval.py --root $root --source $source run --binary "$root/telltale.exe"
```

`prepare` 固定数据集 revision，保存原始文件 SHA256，将 Parquet 原样转换为 CSV，再使用上游 exporter 匿名化。六题是自定义子集，匿名编号只在该子集中有效。答案文件保存在 task 目录之外；MCP 服务仅能打开当前匿名案例，无文件或网络检索工具。

`check` 在六题上分别验证六种真实遥测查询。数据发行版没有预聚合的日志模板、日志和链路时间序列，因此模型任务明确只提供原始指标、日志和链路。

`run` 使用本地配置中的 quick 模型与凭据，经回环代理调用。代理只在内存中持有真实凭据；临时配置使用占位凭据、独立 SQLite 和仅能访问 RCAEval MCP 的权限。每题关闭长期记忆、模型重试与 failover，保留完整证据约束，模型输出上限 4096 token。每题最多 16 次模型请求、150000 token 预算及 360 秒。token 预算使用已收到的 provider usage 加上对下一次请求的保守预估；并非计费金额上限。

冻结案例的 MCP 配置启用 `cache_per_turn: true`，相同规范化参数在一轮诊断中复用成功结果，并合并同时进行的重复请求。实时 MCP 服务默认关闭该选项。`terminal_tools: [submit]` 明确声明成功接受提交即完成任务；失败提交不会结束任务。完成状态传回协调和诊断层，后续无需调用模型，CLI 返回提交回执。该回执表示外部工具接受答案，不表示根因正确。不存在的终止工具名称会导致注册失败。

子 Agent 同时接收原始用户任务与当前委托问题，保留时间窗口、限制和提交要求。评测 MCP 的异常排名 schema 根据实际文件公布可用模态，本批数据只公布 `metrics`。

输出保存在 `--root` 下：

- `import.json`：选题规则、数据版本与原始文件散列。
- `run.json`：模型、执行范围、限制与评测配置差异。
- `runs/<id>/calls.jsonl`、`provider.jsonl`：工具轨迹、模型消息与实际 provider usage。
- `runs/<id>/diagnosis.stdout`、`diagnosis.stderr`：完整诊断进程输出。
- `predictions.jsonl`：仅从模型主动调用 `submit` 的合法提交取答案；未提交记为空排名。
- `baro.jsonl`：同题上游 BARO 指标异常排名基线。
- `telltale.summary.json`、`baro.summary.json` 与对应 `*.cases.jsonl`：上游评分器计算的服务和指标 AC@k、Avg@k、逐题结果。

一次结果目录禁止重复执行付费测试。另一次独立测试需要新的结果目录、对应导入和构建产物。工具 `submit` 的结果衡量根因排名；最终自然语言答案保存在诊断输出中，需要结合其证据和不确定性单独查看。六题成绩不能代表完整数据集，也不验证线上故障修复能力。

出现模型接口连接失败时，可以在原 `run` 命令追加 `--retry-transport-failures` 补跑一次。只有用量未知且没有合法提交的案例会进入补跑；已有合法答案和预算耗尽案例保持原始成绩。原始记录、补跑记录和合并后的 `final.predictions.jsonl` 分别保存，全部实际消耗需要汇总统计。
