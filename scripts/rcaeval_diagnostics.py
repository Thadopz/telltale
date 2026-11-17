import datetime
import hashlib
import json

import numpy as np


def metric_observation(toolkit, name):
    result = toolkit.get_metric(name=name, points=4)
    start = toolkit.window["start"]
    end = toolkit.window["end"]
    return {
        "kind": "metric_value",
        "subject": {"type": "service", "service": name.rsplit("_", 1)[0]},
        "scope": {
            "start_time": datetime.datetime.fromtimestamp(start, datetime.UTC).isoformat().replace("+00:00", "Z"),
            "end_time": datetime.datetime.fromtimestamp(end, datetime.UTC).isoformat().replace("+00:00", "Z"),
        },
        "fields": {
            "metric": name,
            "before_mean": result["before_inject"]["mean"],
            "after_mean": result["after_inject"]["mean"],
        },
        "summary": "Historical telemetry comparison; a change does not establish causality.",
    }


def rank_changes(toolkit, top_k=8):
    data = toolkit.metrics()
    data = data[(data.time >= toolkit.window["start"]) & (data.time < toolkit.window["end"])]
    before = data[data.time < toolkit.inject_time]
    after = data[data.time >= toolkit.inject_time]
    if before.empty or after.empty:
        raise ValueError("comparison requires samples on both sides")
    ranked = []
    for name in data.columns:
        if name == "time":
            continue
        normal = before[name].to_numpy(dtype=float)
        fault = after[name].to_numpy(dtype=float)
        if not np.isfinite(normal).all() or not np.isfinite(fault).all():
            raise ValueError(f"nonfinite metric after upstream cleaning: {name}")
        normal_mean = float(normal.mean())
        fault_mean = float(fault.mean())
        constant = bool(np.ptp(normal) == 0)
        changed_fraction = float(np.mean(np.abs(fault - normal_mean) > max(abs(normal_mean) * 0.1, 1e-12)))
        shifted_constant = constant and changed_fraction >= 0.5
        scale = max(float(np.quantile(normal, 0.75) - np.quantile(normal, 0.25)), abs(normal_mean) * 0.1, 1e-12)
        # 均值分量保留稀疏突发，避免中位数不变时丢失信号。
        score = max(abs(float(np.median(fault)) - float(np.median(normal))), abs(fault_mean - normal_mean)) / scale
        if score == 0 and not shifted_constant:
            continue
        ranked.append({
            "name": name,
            "score": round(score, 3),
            "constant_baseline_shift": shifted_constant,
            "changed_fraction": round(changed_fraction, 3),
        })
    ranked.sort(key=lambda row: (-row["constant_baseline_shift"], -row["score"], -row["changed_fraction"], row["name"]))
    selected = ranked[:max(1, min(int(top_k), 8))]
    return {
        "modality": "metrics",
        "strategy": "baseline_change_v1",
        "ranked": selected,
        "n_series": len(ranked),
        "limit": 8,
        "limitations": "Candidate generation only. Scores describe observed changes, never causal proof. Constant-before/changing-after series are retained. Original BARO remains the evaluation baseline.",
    }


def diagnostic_envelope(toolkit, tool, arguments, result):
    if "error" in result:
        return result
    observations = []
    if tool == "get_metric":
        observation = metric_observation(toolkit, arguments["name"])
        # 显式非默认窗口的查询不能被标注为默认窗口证据。
        if arguments.get("start", toolkit.window["start"]) == toolkit.window["start"] and arguments.get("end", toolkit.window["end"]) == toolkit.window["end"]:
            observations.append(observation)
    elif tool == "rank_anomalies":
        for row in result.get("ranked", [])[:4]:
            observations.append(metric_observation(toolkit, row["name"]))
    if not observations:
        return result
    identity = json.dumps([toolkit.task["case_id"], tool, arguments, observations], sort_keys=True)
    return {
        "protocol_version": "2",
        "status": "success",
        "data_status": "present",
        "data": result,
        "evidence": {
            "evidence_id": "ev_rcaeval_" + hashlib.sha256(identity.encode()).hexdigest()[:20],
            "provenance": {"source_kind": "runtime_metric", "runtime_verified": False},
            "observations": observations,
        },
        "summary": "Historical metric observations, scoped to the recorded incident window.",
    }
