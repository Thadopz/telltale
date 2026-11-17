import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys


REPO = Path(__file__).resolve().parents[1]
SOURCE = REPO / "reports/rcaeval-20261107/vendor/source/RCAEval-90b63a46ac1e969f1fa9015318b51e455c62f8c4"
REFERENCES = {
    "regression": REPO / "reports/hypothesis-20261107/regression-validated/tasks",
    "holdout": REPO / "reports/hypothesis-20261107/holdout/tasks",
}
DELEGATES = {"observability_agent", "mechanism_agent", "database_agent", "knowledge_agent"}


def rows(path):
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines()] if path.exists() else []


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--topology", choices=["single", "multi", "both"], default="single")
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    if args.repeats < 1:
        parser.error("repeats must be positive")
    binary = args.binary.resolve()
    output = args.output.resolve()
    if not binary.is_file():
        parser.error("build the evaluation binary first")
    if output.exists():
        parser.error("output already exists; preserve prior results and choose a new directory")
    topologies = ["multi", "single"] if args.topology == "both" else [args.topology]
    jobs = []
    for repeat in range(1, args.repeats + 1):
        for topology in topologies:
            for dataset, reference in REFERENCES.items():
                assert (reference / "re2-ob/manifest.json").is_file()
                directory = output / topology / f"repeat-{repeat}" / dataset
                command = [
                    sys.executable, str(REPO / "scripts/rcaeval.py"),
                    "--source", str(SOURCE), "--root", str(directory), "run",
                    "--binary", str(binary), "--agent-topology", topology,
                    "--diagnostic-ablation", "full", "--ranking-strategy", "baseline_change_v1",
                    "--mcp-port", "18900", "--serial-provider", "--stop-on-provider-error",
                    "--transient-retries", "2",
                ]
                jobs.append((directory, reference, command))
    protocol = {
        "topologies": topologies, "repeats": args.repeats,
        "planned_cases": len(topologies) * args.repeats * 8,
        "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        "serial_cases_and_provider": True,
        "diagnostic_ablation": "full", "ranking_strategy": "baseline_change_v1",
        "limits": {"requests": 16, "tokens": 150000, "seconds": 360},
    }
    print(json.dumps(protocol, indent=2), flush=True)
    if args.dry_run:
        return
    output.mkdir(parents=True)
    (output / "protocol.json").write_text(json.dumps(protocol, indent=2), encoding="utf-8")
    os.environ["PYTHONUTF8"] = "1"
    stopped = False
    for directory, reference, command in jobs:
        shutil.copytree(reference, directory / "tasks", copy_function=os.link)
        with (directory / "execution.log").open("w", encoding="utf-8") as log:
            result = subprocess.run(command, cwd=REPO, stdout=log, stderr=subprocess.STDOUT)
        print(f"{directory.relative_to(output)} exit={result.returncode}", flush=True)
        if result.returncode:
            stopped = True
            break
    cases = []
    statuses = Counter()
    for directory, _, _ in jobs:
        scores = {row["case_id"]: row for row in rows(directory / "telltale.cases.jsonl")}
        for prediction in rows(directory / "predictions.jsonl"):
            record = directory / "runs" / prediction["case_id"]
            process = json.loads((record / "process.json").read_text(encoding="utf-8"))
            provider = rows(record / "provider.jsonl")
            delegates = []
            exposed_delegates = set()
            for request in provider:
                if "status" in request:
                    statuses[request["status"]] += 1
                exposed_delegates.update(
                    tool["function"]["name"]
                    for tool in request.get("tool_schemas", [])
                    if tool["function"]["name"] in DELEGATES
                )
                for choice in request.get("response") or []:
                    delegates.extend(
                        call["function"]["name"]
                        for call in choice["message"].get("tool_calls", [])
                        if call["function"]["name"] in DELEGATES
                    )
            topology = directory.relative_to(output).parts[0]
            assert topology != "single" or not (delegates or exposed_delegates), "single agent exposed delegation"
            cases.append({
                "topology": topology, "run": str(directory.relative_to(output)),
                "case_id": prediction["case_id"], "completed": process.get("exit_code") == 0,
                "submitted": bool(prediction["ranks"]), "usage": prediction["usage"],
                "provider_usage_unknown": prediction["provider_usage_unknown"],
                "delegation_calls": len(delegates),
                "service_rank": scores.get(prediction["case_id"], {}).get("service_rank"),
                "indicator_rank": scores.get(prediction["case_id"], {}).get("indicator_rank"),
            })
    summary = {}
    for topology in topologies:
        selected = [row for row in cases if row["topology"] == topology]
        summary[topology] = {
            "ended": len(selected), "completed": sum(row["completed"] for row in selected),
            "submitted": sum(row["submitted"] for row in selected),
            "service_top1": sum(row["service_rank"] == 1 for row in selected),
            "indicator_top1": sum(row["indicator_rank"] == 1 for row in selected),
            "tokens": sum(row["usage"]["input_tokens"] + row["usage"]["output_tokens"] for row in selected),
            "delegation_calls": sum(row["delegation_calls"] for row in selected),
        }
    result = {"stopped": stopped, "summary": summary, "provider_statuses": dict(statuses), "cases": cases}
    (output / "results.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(summary, indent=2))
    if stopped:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
