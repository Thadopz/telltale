import json
import statistics
from pathlib import Path


OUT = Path(__file__).resolve().parents[1] / "reports/chunking-optimization"


def read(name):
    return json.loads((OUT / name).read_text(encoding="utf-8"))


groups = read("chunks.json")
ledger = read("budget.json")
requests = ledger["requests"]
assert all(entry["status"] == "success" for entry in requests)
assert ledger["charged_upper_bound"] < 800000
assert sum(entry["reserved"] for entry in requests) == ledger["charged_upper_bound"]
assert all((entry["usage"] or {}).get("total_tokens", 0) <= entry["reserved"] for entry in requests)
costs = {"embedding": {"reserved": ledger["charged_upper_bound"], "calls": len(requests),
                        "provider_tokens": sum((entry["usage"] or {}).get("total_tokens", 0) for entry in requests),
                        "request_seconds": sum(entry.get("seconds", 0) for entry in requests)}, "groups": {}}
for group, docs in groups.items():
    lengths = [len(doc["content"].encode()) for doc in docs]
    costs["groups"][group] = {"chunks": len(docs), "min_bytes": min(lengths), "median_bytes": statistics.median(lengths),
                              "max_bytes": max(lengths), "index_upper_bound": sum(lengths),
                              "overlap_bytes": sum(d["meta_data"].get("_overlap_bytes", 0) for d in docs),
                              "sqlite_bytes": (OUT / ("hybrid-"+group+".sqlite")).stat().st_size}
offset = 0
for group, docs in groups.items():
    entries = requests[offset:offset+len(docs)]
    costs["groups"][group]["provider_index_tokens"] = sum(entry["usage"]["total_tokens"] for entry in entries)
    costs["groups"][group]["request_seconds"] = sum(entry["seconds"] for entry in entries)
    offset += len(docs)
hybrid = read("hybrid-retrievals.json")
costs["hybrid_mean_query_seconds"] = {group: statistics.mean(row["seconds"] for row in hybrid if row["group"] == group) for group in groups}
costs["other"] = read("other-budget.json")
pairs = {}
for row in read("per-question.json"):
    if row["retrieval"] == "hybrid" and row["mode"] == "budget2048" and row["question"]["split"] == "holdout":
        pairs.setdefault(row["question"]["id"], {})[row["group"]] = row
costs["worst_cases"] = [{"id": index, "question": pair["A"]["question"]["question"], "A_recall": pair["A"]["recall"], "B_recall": pair["B"]["recall"]}
                         for index, pair in sorted(pairs.items(), key=lambda item: item[1]["B"]["recall"]-item[1]["A"]["recall"])[:3]]
(OUT / "costs.json").write_text(json.dumps(costs, indent=2), encoding="utf-8")
print(json.dumps(costs, indent=2))
