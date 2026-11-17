import argparse
import contextlib
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pandas as pd
import requests
import yaml
import jsonschema

from rcaeval_diagnostics import diagnostic_envelope, rank_changes


def read_json(path):
    return json.loads(Path(path).read_text(encoding="utf-8"))


def write_json(path, value):
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    Path(path).write_text(
        json.dumps(value, ensure_ascii=False, indent=2, default=str),
        encoding="utf-8",
    )


def append_json(path, value):
    with Path(path).open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(value, ensure_ascii=False, default=str) + "\n")


def prepare(root):
    from huggingface_hub import HfApi, hf_hub_download
    from RCAEval.agent.export import export

    repo = "phamquiluan/RCAEval"
    revision = HfApi().dataset_info(repo).sha
    index = hf_hub_download(
        repo, "cases.parquet", repo_type="dataset", revision=revision,
        local_dir=root / "raw",
    )
    frame = pd.read_parquet(index)
    eligible = frame[(frame.dataset == "RE2-OB") & (frame.repetition == 1)]
    services = sorted(eligible.root_cause_service.unique())
    selected = []
    hashes = {}
    # 每类故障一个案例，服务轮换；选题规则先于模型运行固定。
    for index, fault in enumerate(["cpu", "mem", "disk", "socket", "delay", "loss"]):
        row = eligible[
            (eligible.fault == fault)
            & (eligible.root_cause_service == services[index % len(services)])
        ].iloc[0]
        destination = root / "converted" / f"{row.root_cause_service}_{fault}" / "1"
        destination.mkdir(parents=True, exist_ok=True)
        for modality in ["metrics", "logs", "traces"]:
            filename = f"{row['case']}/{modality}.parquet"
            path = Path(hf_hub_download(
                repo, filename, repo_type="dataset", revision=revision,
                local_dir=root / "raw",
            ))
            hashes[filename] = hashlib.sha256(path.read_bytes()).hexdigest()
            data = pd.read_parquet(path)
            output_name = "simple_metrics.csv" if modality == "metrics" else f"{modality}.csv"
            data.to_csv(destination / output_name, index=False)
            if modality == "metrics":
                assert "time" in data.columns and len(data) > 0
        (destination / "inject_time.txt").write_text(str(int(row.inject_time)), encoding="utf-8")
        selected.append({"source_case": row["case"], "fault": fault})
        print(f"imported {row['case']}", flush=True)
    task_dir, answers = export(
        "re2-ob", out_dir=str(root / "tasks"), copy=True,
        download=False, data_root=str(root / "converted"),
    )
    write_json(root / "import.json", {
        "dataset": repo,
        "revision": revision,
        "selection": selected,
        "sha256": hashes,
        "task_dir": task_dir,
        "answers": answers,
        "conversion": "Parquet to CSV without resampling, filtering or value filling",
        "scope": "6-case RE2-OB subset; local anonymous IDs are subset-specific",
        "modalities": "raw metrics/logs/traces; preaggregated log/trace series unavailable in this distribution",
    })


def available_tools(task, tools, ranking_strategy="baseline_change_v1"):
    specs = copy.deepcopy(tools)
    modes = {
        "metrics": "metrics",
        "logs": "logts",
        "trace_latency": "tracets_lat",
        "trace_errors": "tracets_err",
    }
    for spec in specs:
        if spec["name"] == "rank_anomalies":
            available = [mode for mode, file in modes.items() if file in task["files"]]
            spec["parameters"]["properties"]["modality"]["enum"] = available
            spec["description"] = "Rank available anomaly series. Available modalities: " + ", ".join(available)
            if ranking_strategy == "baro":
                spec["description"] += ". Metrics use original BARO robust z-score candidate generation."
            else:
                spec["description"] += ". Metrics use baseline_change_v1 candidate generation, retaining constant-before/changing-after signals."
            spec["description"] += " Maximum top_k is 8. Inspect other resources of a candidate service directly before inferring cause."
            spec["parameters"]["properties"]["top_k"]["maximum"] = 8
    return specs


def serve(case, record, port, ranking_strategy="baseline_change_v1"):
    import anyio
    import mcp.types as types
    from mcp.server.lowlevel import Server
    from mcp.server.streamable_http_manager import StreamableHTTPSessionManager
    from starlette.applications import Starlette
    from starlette.routing import Route
    from starlette.responses import JSONResponse
    import uvicorn
    from RCAEval.agent.toolkit import TOOLS, CaseToolkit

    record.mkdir(parents=True, exist_ok=True)
    toolkit = CaseToolkit(str(case))
    lock = threading.Lock()

    async def list_tools(ctx, params):
        specs = available_tools(toolkit.task, TOOLS, ranking_strategy)
        return types.ListToolsResult(tools=[
            types.Tool(
                name=tool["name"],
                description=tool["description"],
                input_schema=tool["parameters"],
            )
            for tool in specs
        ])

    def invoke(name, arguments):
        with lock:
            started = time.monotonic()
            if name == "rank_anomalies" and arguments.get("modality", "metrics") == "metrics":
                spec = next(spec for spec in available_tools(toolkit.task, TOOLS, ranking_strategy) if spec["name"] == name)
                parameters = {**spec["parameters"], "additionalProperties": False}
                try:
                    jsonschema.validate(arguments, parameters)
                    if ranking_strategy == "baro":
                        result = toolkit.rank_anomalies(modality="metrics", top_k=arguments.get("top_k", 8))
                        result["strategy"] = "baro"
                    else:
                        result = rank_changes(toolkit, arguments.get("top_k", 8))
                except jsonschema.ValidationError as error:
                    result = {"error": error.message}
                toolkit.calls.append({
                    "seq": len(toolkit.calls) + 1,
                    "tool": name, "args": arguments,
                    "ok": "error" not in result, "error": result.get("error"),
                    "result_chars": len(json.dumps(result)),
                    "ms": round((time.monotonic() - started) * 1000),
                })
            else:
                result = toolkit.call(name, arguments)
            append_json(record / "calls.jsonl", {
                "type": "tool_call",
                **toolkit.calls[-1],
                "result": result,
            })
            if toolkit.submission is not None:
                write_json(record / "submission.json", toolkit.submission)
            return diagnostic_envelope(toolkit, name, arguments, result)

    async def call_tool(ctx, params):
        result = await anyio.to_thread.run_sync(invoke, params.name, params.arguments or {})
        return types.CallToolResult(
            content=[types.TextContent(type="text", text=json.dumps(result, default=str))],
            is_error="error" in result,
        )

    server = Server("rcaeval", on_list_tools=list_tools, on_call_tool=call_tool)
    manager = StreamableHTTPSessionManager(server, json_response=True, stateless=True)

    @contextlib.asynccontextmanager
    async def lifespan(app):
        async with manager.run():
            yield

    async def health(request):
        return JSONResponse({"case_id": toolkit.task["case_id"]})

    class MCPApp:
        async def __call__(self, scope, receive, send):
            await manager.handle_request(scope, receive, send)

    app = Starlette(
        routes=[
            Route("/health", health),
            Route("/mcp", MCPApp(), methods=["GET", "POST", "DELETE"]),
        ],
        lifespan=lifespan,
    )
    uvicorn.run(app, host="127.0.0.1", port=port, log_level="warning")


class ModelProxy(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def fail(self, status, message):
        payload = json.dumps({
            "error": {
                "message": message,
                "type": "evaluation_error",
            },
        }).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(payload)

    def do_POST(self):
        if self.server.state.get("serial_provider"):
            with self.server.state["provider_lock"]:
                self.forward()
        else:
            self.forward()

    def forward(self, body=None):
        state = self.server.state
        if body is None:
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        if state.get("provider_error"):
            self.fail(503, "provider circuit open; experiment stopped after upstream error")
            return
        if self.path != "/v1/chat/completions" or body.get("stream"):
            self.fail(400, "only nonstream chat completions supported")
            return
        estimated = len(json.dumps(body, ensure_ascii=False)) + 4096
        with state["lock"]:
            exhausted = state["requests"] >= 16 or state["tokens"] + estimated > 150000
            if not exhausted:
                state["requests"] += 1
        if exhausted:
            append_json(state["usage"], {
                "error": "evaluation_budget_exhausted", "messages": body.get("messages"),
            })
            self.fail(429, "evaluation budget exhausted")
            return
        started = time.monotonic()
        try:
            response = requests.post(
                state["url"].rstrip("/") + "/chat/completions",
                headers={"Authorization": "Bearer " + state["key"]},
                json=body, timeout=150,
            )
            result = response.json()
            usage = result.get("usage", {})
            with state["lock"]:
                state["tokens"] += usage.get("total_tokens", estimated)
                retry = (
                    response.status_code in (500, 502, 503, 504)
                    and state.get("transient_retries_remaining", 0) > 0
                    and state["requests"] < 16
                    and state["tokens"] + estimated <= 150000
                )
                if retry:
                    state["transient_retries_remaining"] -= 1
                elif state.get("stop_on_provider_error") and response.status_code >= 400:
                    state["provider_error"] = response.status_code
                append_json(state["usage"], {
                    "model": body.get("model"), "status": response.status_code,
                    "usage": usage, "wall_time_s": time.monotonic() - started,
                    "usage_unknown": not bool(usage),
                    "finish_reasons": [x.get("finish_reason") for x in result.get("choices", [])],
                    "messages": body.get("messages"),
                    "tool_schemas": body.get("tools", []),
                    "response": result.get("choices"),
                    "retry_scheduled": retry,
                })
            if retry:
                time.sleep(5)
                self.forward(body)
                return
            self.send_response(response.status_code)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(response.content)
        except requests.RequestException as error:
            append_json(state["usage"], {"error_type": type(error).__name__, "usage_unknown": True})
            self.fail(502, "provider request failed")


def run(root, source, binary, retry_transport=False, diagnostic_hypotheses=True,
        diagnostic_ablation="full", ranking_strategy="baseline_change_v1", mcp_port=18765,
        serial_provider=False, stop_on_provider_error=False, transient_retries=0, agent_topology="multi"):
    from RCAEval.agent.predictions import run as score
    from RCAEval.agent.toolkit import CaseToolkit

    config_path = Path("manifest/config/config.local.yaml")
    config = yaml.safe_load(config_path.read_text(encoding="utf-8"))
    model_config = config["quick_chat_model"]
    task_dir = root / "tasks" / "re2-ob"
    manifest = read_json(task_dir / "manifest.json")
    predictions_path = root / "predictions.jsonl"
    previous = {}
    case_ids = manifest["case_ids"]
    if retry_transport:
        previous = {
            row["case_id"]: row
            for row in map(json.loads, predictions_path.read_text(encoding="utf-8").splitlines())
        }
        case_ids = [
            case_id for case_id in case_ids
            if previous[case_id].get("provider_usage_unknown") and not previous[case_id]["ranks"]
        ]
        predictions_path = root / "transport-retry.predictions.jsonl"
    if predictions_path.exists():
        raise FileExistsError("predictions already exist; use a separate run directory")
    baseline_path = root / "baro.jsonl"
    manifest_name = "transport-retry.run.json" if retry_transport else "run.json"
    write_json(root / manifest_name, {
        "model": model_config["model"], "cases": case_ids,
        "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        "diagnostic_hypotheses": diagnostic_hypotheses,
        "diagnostic_ablation": diagnostic_ablation,
        "agent_topology": agent_topology,
        "ranking_strategy": ranking_strategy,
        "mcp_port": mcp_port,
        "serial_provider": serial_provider,
        "stop_on_provider_error": stop_on_provider_error,
        "transient_retries_per_case": transient_retries,
        "agent": "Telltale diagnose full diagnostic pipeline, configured MCP observability tools",
        "source_commit": source.name.removeprefix("RCAEval-"),
        "limits": {
            "cases": len(case_ids),
            "model_requests_per_case": 16,
            "tokens_per_case": 150000,
            "seconds_per_case": 360,
        },
        "config_changes": [
            "isolated SQLite",
            "memory off",
            "only RCAEval MCP tools authorized",
            "4096 output tokens",
            "full evidence enforcement",
        ],
    })
    for case_id in case_ids:
        case = task_dir / "cases" / case_id
        record = root / "runs" / case_id
        if retry_transport:
            record = record / "transport-retry"
        record.mkdir(parents=True, exist_ok=True)
        if not retry_transport:
            toolkit = CaseToolkit(str(case))
            ranked = toolkit.rank_anomalies(modality="metrics", top_k=100)
            # 工具中的指标排名直接使用上游 BARO robust z-score，保存同题基线。
            append_json(baseline_path, {
                "case_id": case_id,
                "ranks": [item["name"] for item in ranked["ranked"]],
                "model": "BARO-toolkit",
            })
        mcp_log = (record / "mcp.log").open("w", encoding="utf-8")
        mcp = subprocess.Popen([
            sys.executable, str(Path(__file__).resolve()), "--source", str(source),
            "serve", "--case", str(case), "--record", str(record), "--port", str(mcp_port),
            "--ranking-strategy", ranking_strategy,
        ], stdout=mcp_log, stderr=subprocess.STDOUT)
        proxy = ThreadingHTTPServer(("127.0.0.1", 0), ModelProxy)
        proxy.state = {
            "serial_provider": serial_provider, "provider_lock": threading.Lock(),
            "stop_on_provider_error": stop_on_provider_error,
            "transient_retries_remaining": transient_retries,
            "url": model_config["base_url"], "key": model_config["api_key"],
            "requests": 0, "tokens": 0, "lock": threading.Lock(), "usage": record / "provider.jsonl",
        }
        threading.Thread(target=proxy.serve_forever, daemon=True).start()
        started = time.monotonic()
        try:
            deadline = time.monotonic() + 30
            while True:
                if mcp.poll() is not None:
                    raise RuntimeError(f"MCP exited; inspect {record / 'mcp.log'}")
                try:
                    ready = requests.get(f"http://127.0.0.1:{mcp_port}/health", timeout=1)
                    if ready.ok and ready.json()["case_id"] == case_id:
                        break
                except requests.ConnectionError:
                    pass
                if time.monotonic() >= deadline:
                    raise TimeoutError("MCP startup timeout")
                time.sleep(0.2)
            isolated = {
                key: config[key]
                for key in ["context", "project_context", "skills", "knowledge_compression"]
                if key in config
            }
            isolated["logger"] = {"level": "error", "stdout": False}
            isolated["runtime"] = {"environment": "eval"}
            isolated["evaluation"] = {"evidence_mode": "full"}
            isolated["memory"] = {"mode": "off"}
            isolated["storage"] = {"sqlite_path": str(record / "telltale.db")}
            isolated["identity"] = {
                "user_id": "rcaeval",
                "tenant_id": "rcaeval",
                "roles": ["rcaeval"],
            }
            isolated["permissions"] = {
                "enabled": True,
                "roles": {"rcaeval": {"scopes": ["tool:code.read"]}},
            }
            isolated["agent"] = {
                "topology": agent_topology,
                "diagnostic_hypotheses": diagnostic_hypotheses,
                "diagnostic_ablation": diagnostic_ablation,
                "invoke_model_retries": 0,
                "failover": {"enabled": False},
                "tool_search": {"enabled": False},
            }
            isolated["tools"] = {"mcp_servers": [{
                "name": "rcaeval", "domain": "observability", "url": f"http://127.0.0.1:{mcp_port}/mcp",
                "permission": "tool:code.read", "agent_scopes": ["observability_agent"],
                "enabled": True, "required": True, "read_only": True, "timeout": 30,
                "max_output_bytes": 65536, "schema_version": "1",
                "terminal_tools": ["submit"], "cache_per_turn": True,
            }]}
            for key in ["quick_chat_model", "think_chat_model", "failover_chat_model"]:
                isolated[key] = {
                    **model_config, "api_key": "evaluation-proxy",
                    "base_url": f"http://127.0.0.1:{proxy.server_port}/v1",
                    "max_output_tokens": 4096,
                }
            # 测试配置不复制任何真实凭据。
            config_dir = record / "config"
            config_dir.mkdir(exist_ok=True)
            (config_dir / "config.yaml").write_text(yaml.safe_dump(isolated, allow_unicode=True), encoding="utf-8")
            task = read_json(case / "task.json")
            question = (
                "这是 RCAEval 的历史遥测故障诊断任务。仅使用 RCAEval 对应的 MCP 工具，"
                + ("直接调用遥测工具调查。" if agent_topology == "single" else "通过 observability_agent 调查。")
                + "案例数据是历史记录，不查询当前环境。"
                "先 describe_case，再根据证据检查候选服务自己的指标、日志或链路。"
                "仅 metrics、logs、traces 三种原始遥测可用，其他聚合模态未导入。"
                "完成后必须调用 MCP submit(ranks,reasoning) 提交最多五个候选，最可能的根因排在首位；"
                "候选必须来自 candidate_services 或 candidate_indicators。"
                "不要对上游传播的症状直接作出根因结论；证据不足需要说明。"
                "调查限于八次遥测查询。工具 submit 只记录测试答案。"
                f"案例 {case_id}，故障开始 Unix 时间 {task['inject_time']}，"
                f"窗口 {json.dumps(task['standard_window'])}。"
            )
            completed = subprocess.run(
                [str(binary), "--config-dir", str(config_dir), "diagnose", "--json", question],
                capture_output=True, timeout=360,
            )
            (record / "diagnosis.stdout").write_bytes(completed.stdout)
            (record / "diagnosis.stderr").write_bytes(completed.stderr)
            write_json(record / "process.json", {"exit_code": completed.returncode})
        except subprocess.TimeoutExpired as error:
            (record / "diagnosis.stdout").write_bytes(error.stdout or b"")
            (record / "diagnosis.stderr").write_bytes(error.stderr or b"")
            write_json(record / "process.json", {"error": "case timeout"})
        finally:
            mcp.terminate()
            mcp.wait(timeout=15)
            mcp_log.close()
            proxy.shutdown()
            proxy.server_close()
        submission = read_json(record / "submission.json") if (record / "submission.json").exists() else {}
        calls = [json.loads(line) for line in (record / "calls.jsonl").read_text(encoding="utf-8").splitlines()] if (record / "calls.jsonl").exists() else []
        usages = [json.loads(line) for line in (record / "provider.jsonl").read_text(encoding="utf-8").splitlines()] if (record / "provider.jsonl").exists() else []
        prediction = {
            "case_id": case_id, "ranks": submission.get("ranks", []),
            "reasoning": submission.get("reasoning", "No valid submit received"),
            "model": model_config["model"], "trajectory": str((record / "calls.jsonl").relative_to(root)),
            "usage": {
                "input_tokens": sum(x.get("usage", {}).get("prompt_tokens", 0) for x in usages),
                "output_tokens": sum(x.get("usage", {}).get("completion_tokens", 0) for x in usages),
                "wall_time_s": round(time.monotonic() - started, 3), "tool_calls": len(calls),
            },
            "provider_usage_unknown": any(x.get("usage_unknown") for x in usages),
        }
        append_json(predictions_path, prediction)
        print(f"completed {case_id}: {prediction['ranks']} usage={prediction['usage']}", flush=True)
        if stop_on_provider_error and proxy.state.get("provider_error"):
            write_json(root / "stopped.json", {
                "case_id": case_id,
                "reason": "upstream provider error",
                "statuses": [row["status"] for row in usages if row.get("status", 0) >= 400],
            })
            raise RuntimeError("upstream provider error; stopped before the next case")
    if retry_transport:
        for line in predictions_path.read_text(encoding="utf-8").splitlines():
            row = json.loads(line)
            previous[row["case_id"]] = row
        predictions_path = root / "final.predictions.jsonl"
        if predictions_path.exists():
            raise FileExistsError(predictions_path)
        for case_id in manifest["case_ids"]:
            append_json(predictions_path, previous[case_id])
    for name, predictions in [("telltale", predictions_path), ("baro", baseline_path)]:
        score(
            str(predictions), tasks_dir=str(task_dir), report_chance=True,
            per_case_out=str(root / f"{name}.cases.jsonl"), json_out=str(root / f"{name}.summary.json"),
        )


def check(root):
    from RCAEval.agent.toolkit import CaseToolkit

    task_dir = root / "tasks" / "re2-ob"
    manifest = read_json(task_dir / "manifest.json")
    assert len(manifest["case_ids"]) == 6
    for case_id in manifest["case_ids"]:
        toolkit = CaseToolkit(str(task_dir / "cases" / case_id))
        from RCAEval.agent.toolkit import TOOLS
        specs = available_tools(toolkit.task, TOOLS)
        ranking = next(spec for spec in specs if spec["name"] == "rank_anomalies")
        assert ranking["parameters"]["properties"]["modality"]["enum"] == ["metrics"]
        assert "root_cause_service" not in toolkit.task
        for name, arguments in [
            ("describe_case", {}),
            ("rank_anomalies", {"modality": "metrics", "top_k": 5}),
            ("get_metric", {"name": toolkit.task["candidate_indicators"][0], "points": 10}),
            ("search_logs", {"pattern": "error|exception|timeout", "limit": 3}),
            ("trace_summary", {"top_k": 3}),
            ("call_graph", {}),
        ]:
            result = toolkit.call(name, arguments)
            assert "error" not in result, (case_id, name, result)
        print(f"checked {case_id}: 6 telemetry queries passed", flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--root", type=Path, default=Path("reports/rcaeval-20261107"))
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("prepare")
    commands.add_parser("check")
    runner = commands.add_parser("run")
    runner.add_argument("--binary", type=Path, required=True)
    runner.add_argument("--retry-transport-failures", action="store_true")
    runner.add_argument("--disable-diagnostic-hypotheses", action="store_true")
    runner.add_argument("--diagnostic-ablation", choices=[
        "full", "free_query", "free_submit", "no_feedback", "single_hypothesis", "no_retention",
    ], default="full")
    runner.add_argument("--mcp-port", type=int, default=18765)
    runner.add_argument("--serial-provider", action="store_true")
    runner.add_argument("--agent-topology", choices=["multi", "single"], default="multi")
    runner.add_argument("--stop-on-provider-error", action="store_true")
    runner.add_argument("--transient-retries", type=int, choices=range(3), default=0)
    runner.add_argument("--ranking-strategy", choices=["baseline_change_v1", "baro"], default="baseline_change_v1")
    server = commands.add_parser("serve")
    server.add_argument("--case", type=Path, required=True)
    server.add_argument("--record", type=Path, required=True)
    server.add_argument("--port", type=int, required=True)
    server.add_argument("--ranking-strategy", choices=["baseline_change_v1", "baro"], default="baseline_change_v1")
    args = parser.parse_args()
    sys.path.insert(0, str(args.source.resolve()))
    if args.command == "prepare":
        prepare(args.root.resolve())
    elif args.command == "check":
        check(args.root.resolve())
    elif args.command == "serve":
        serve(args.case, args.record, args.port, args.ranking_strategy)
    else:
        run(
            args.root.resolve(), args.source.resolve(), args.binary.resolve(),
            args.retry_transport_failures,
            not args.disable_diagnostic_hypotheses,
            args.diagnostic_ablation, args.ranking_strategy, args.mcp_port,
            args.serial_provider, args.stop_on_provider_error,
            args.transient_retries,
            args.agent_topology,
        )


if __name__ == "__main__":
    main()
