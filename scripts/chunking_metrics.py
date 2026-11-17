import json
import random
import statistics
from collections import defaultdict
from pathlib import Path


OUT = Path(__file__).resolve().parents[1] / "reports/chunking-optimization"


def positions(doc):
    source = doc["meta_data"]["_source"]
    return {(source, i) for span in doc["meta_data"]["_source_ranges"] for i in range(span["start"], span["end"])}


def metrics(row, mode):
    gold = {(row["question"]["source"], i) for start, end in row["question"]["ranges"] for i in range(start, end)}
    retrieved = set()
    size = 0
    reciprocal = 0
    selected = []
    for item in row["retrieved"]:
        doc = item["document"]
        length = len(doc["content"].encode())
        if mode == "budget2048":
            if size + length > 2048:
                break
        elif len(selected) >= int(mode[3:]):
            break
        selected.append(item)
        size += length
        chunk = positions(doc)
        if not reciprocal and len(chunk & gold)/len(gold) >= 0.5:
            reciprocal = 1/item["rank"]
        retrieved |= chunk
    intersection = len(gold & retrieved)
    return {"recall": intersection/len(gold), "precision": intersection/max(1, len(retrieved)),
            "iou": intersection/len(gold | retrieved), "complete": int(gold <= retrieved),
            "hit": int(intersection > 0), "mrr": reciprocal, "returned_bytes": size,
            "duplicate_and_synthetic_bytes": size-len(retrieved), "selected": selected}


def main():
    rows = json.loads((OUT / "retrievals.json").read_text(encoding="utf-8"))
    hybrid = OUT / "hybrid-retrievals.json"
    a, b = OUT / "hybrid-A-retrievals.json", OUT / "hybrid-B-retrievals.json"
    if a.exists() and b.exists():
        combined = json.loads(a.read_text(encoding="utf-8"))+json.loads(b.read_text(encoding="utf-8"))
        hybrid.write_text(json.dumps(combined, ensure_ascii=False), encoding="utf-8")
    for row in rows:
        row["retrieval"] = "dense"
    if hybrid.exists():
        rows.extend(json.loads(hybrid.read_text(encoding="utf-8")))
    chain = OUT / "chain-retrievals.json"
    if chain.exists():
        rows.extend(json.loads(chain.read_text(encoding="utf-8")))
    individual = []
    aggregate = defaultdict(list)
    for row in rows:
        for mode in ["top3", "top5", "budget2048"]:
            result = metrics(row, mode)
            record = {"group": row["group"], "retrieval": row["retrieval"], "question": row["question"], "mode": mode, **result}
            individual.append(record)
            key = (row["retrieval"], row["group"], row["question"]["split"], mode)
            aggregate[key].append(record)
    keys = ["recall", "precision", "iou", "complete", "hit", "mrr", "returned_bytes", "duplicate_and_synthetic_bytes"]
    summary = {"/".join(key): {metric: statistics.mean(r[metric] for r in values) for metric in keys}
               for key, values in aggregate.items()}
    differences = {}
    for retrieval in sorted({r["retrieval"] for r in individual}):
        for mode in ["top3", "top5", "budget2048"]:
            paired = defaultdict(dict)
            for row in individual:
                if row["retrieval"] == retrieval and row["mode"] == mode and row["question"]["split"] == "holdout":
                    paired[row["question"]["id"]][row["group"]] = row
            for metric in keys:
                by_document = defaultdict(list)
                for pair in paired.values():
                    by_document[pair["A"]["question"]["source"]].append(pair["B"][metric]-pair["A"][metric])
                documents = sorted(by_document)
                rng = random.Random(20261114)
                samples = []
                for _ in range(2000):
                    sample = [delta for source in rng.choices(documents, k=len(documents)) for delta in by_document[source]]
                    samples.append(statistics.mean(sample))
                samples.sort()
                absolute = statistics.mean(delta for values in by_document.values() for delta in values)
                baseline = summary["/".join([retrieval, "A", "holdout", mode])][metric]
                differences["/".join([retrieval, mode, metric])] = {"absolute": absolute, "relative": absolute/baseline if baseline else None,
                                                                                "cluster_bootstrap_95": [samples[49], samples[1949]], "documents": len(documents)}
    (OUT / "per-question.json").write_text(json.dumps(individual, ensure_ascii=False), encoding="utf-8")
    (OUT / "metrics.json").write_text(json.dumps({"summary": summary, "differences": differences}, indent=2), encoding="utf-8")
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
