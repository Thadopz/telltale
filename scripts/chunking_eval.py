import argparse
import csv
import hashlib
import json
import os
import random
import subprocess
import sqlite3
import time
import urllib.error
import urllib.request
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "reports/chunking-optimization"
DATA = OUT / "upstream/chunking_evaluation/evaluation_framework/general_evaluation_data"


def digest(data):
    return hashlib.sha256(data).hexdigest()


def save(path, value):
    temporary = path.with_suffix(path.suffix + ".pending")
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding="utf-8")
    temporary.replace(path)


def freeze():
    groups = json.loads((OUT / "chunks.json").read_text(encoding="utf-8"))
    names = sorted({d["meta_data"]["_source"] for d in groups["A"]})
    corpora = {name: (DATA / "corpora" / (name + ".md")).read_text(encoding="utf-8") for name in names}
    questions = []
    with (DATA / "questions_df.csv").open(encoding="utf-8", newline="") as handle:
        for index, row in enumerate(csv.DictReader(handle)):
            name = row["corpus_id"]
            if name not in names:
                continue
            references = json.loads(row["references"])
            spans = []
            for ref in references:
                start, end = ref["start_index"], ref["end_index"]
                assert corpora[name][start:end] == ref["content"], (index, name)
                spans.append([len(corpora[name][:start].encode()), len(corpora[name][:end].encode())])
            questions.append({"id": index, "source": name, "question": row["question"], "ranges": spans,
                              "split": "dev" if name == "chatlogs" else "holdout"})
    rng = random.Random(20261114)
    selected = []
    for split, count in [("dev", 16), ("holdout", 64)]:
        candidates = [q for q in questions if q["split"] == split]
        rng.shuffle(candidates)
        selected.extend(candidates[:count])
    cost = {group: sum(len(d["content"].encode()) for d in docs) for group, docs in groups.items()}
    cost["queries"] = sum(len(q["question"].encode()) for q in selected)
    assert sum(cost.values()) * 1.1 < 800000, cost
    manifest = {"commit": "e708410d1c61cb76a85cd9d433630ef89b9c6b85", "seed": 20261114,
                "estimator": "utf8-byte-upper-bound", "cost": cost, "questions": selected,
                "files": {name: digest((DATA / "corpora" / (name + ".md")).read_bytes()) for name in names},
                "chunks_sha256": digest((OUT / "chunks.json").read_bytes())}
    path = OUT / "frozen.json"
    if path.exists():
        assert json.loads(path.read_text(encoding="utf-8")) == manifest, "frozen experiment changed"
    else:
        save(path, manifest)
    print(json.dumps({"cost": cost, "questions": len(selected), "documents": names}))
    return groups, manifest


class Embeddings:
    def __init__(self):
        config_path = ROOT / "manifest/config/config.local.yaml"
        if not config_path.exists():
            config_path = ROOT / "manifest/config/config.yaml"
        self.config = yaml.safe_load(config_path.read_text(encoding="utf-8"))["embedding_model"]
        self.multimodal = any(value in self.config["model"].lower() for value in ["multimodal", "vision"])
        self.endpoint = (self.config.get("endpoint") or "https://dashscope.aliyuncs.com/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding") if self.multimodal else "https://dashscope.aliyuncs.com/compatible-mode/v1/embeddings"
        assert self.config.get("api_key") and "YOUR_" not in self.config["api_key"], "embedding key unavailable"
        self.identity = digest(json.dumps([self.endpoint, self.config["model"], self.config["dimension"]]).encode())
        self.db = sqlite3.connect(OUT / "embedding-cache.sqlite")
        self.db.execute("CREATE TABLE IF NOT EXISTS cache (id TEXT PRIMARY KEY, vector TEXT)")
        self.ledger_path = OUT / "budget.json"
        self.ledger = json.loads(self.ledger_path.read_text()) if self.ledger_path.exists() else {"charged_upper_bound": 0, "requests": []}
        assert not self.ledger.get("stopped"), "paid tests previously stopped; inspect budget.json"

    def embed(self, text):
        identity = digest((self.identity + text).encode())
        cached = self.db.execute("SELECT vector FROM cache WHERE id=?", (identity,)).fetchone()
        if cached:
            return json.loads(cached[0])
        reservation = len(text.encode())
        assert self.ledger["charged_upper_bound"] + reservation < 800000, "budget stop line"
        entry = {"hash": identity, "reserved": reservation, "status": "reserved"}
        self.ledger["charged_upper_bound"] += reservation
        self.ledger["requests"].append(entry)
        save(self.ledger_path, self.ledger)
        payload = {"model": self.config["model"], "input": {"contents": [{"text": text}]},
                   "parameters": {"dimension": self.config["dimension"]}}
        if not self.multimodal:
            payload = {"model": self.config["model"], "input": [text], "dimensions": self.config["dimension"], "encoding_format": "float"}
        request = urllib.request.Request(self.endpoint, json.dumps(payload).encode(),
                                         {"Authorization": "Bearer " + self.config["api_key"], "Content-Type": "application/json"})
        started = time.monotonic()
        try:
            with urllib.request.urlopen(request, timeout=40) as response:
                result = json.load(response)
            vector = result["output"]["embeddings"][0]["embedding"] if self.multimodal else result["data"][0]["embedding"]
            assert len(vector) == self.config["dimension"]
            entry.update(status="success", usage=result.get("usage"), seconds=time.monotonic()-started)
            usage = result.get("usage") or {}
            actual = usage.get("total_tokens", usage.get("input_tokens", reservation))
            self.ledger["charged_upper_bound"] += max(0, actual-reservation)
            self.db.execute("INSERT INTO cache VALUES (?,?)", (identity, json.dumps(vector)))
            self.db.commit()
            save(self.ledger_path, self.ledger)
            return vector
        except Exception as error:
            entry.update(status="failed", error_type=type(error).__name__, http_status=getattr(error, "code", None))
            self.ledger["stopped"] = True
            save(self.ledger_path, self.ledger)
            raise RuntimeError("paid experiment stopped; see sanitized budget.json") from None


def run():
    import numpy as np

    groups, manifest = freeze()
    model = Embeddings()
    matrices = {}
    for group, docs in groups.items():
        vectors = []
        for index, doc in enumerate(docs):
            vectors.append(model.embed(doc["content"]))
            if index % 100 == 0:
                print(group, index, len(docs), flush=True)
        matrix = np.asarray(vectors)
        matrices[group] = matrix / np.linalg.norm(matrix, axis=1, keepdims=True)
    results = []
    for question in manifest["questions"]:
        vector = np.asarray(model.embed(question["question"]))
        vector /= np.linalg.norm(vector)
        for group, matrix in matrices.items():
            scores = matrix @ vector
            ranks = np.argsort(-scores, kind="stable")
            retrieved = [{"rank": rank+1, "score": float(scores[i]), "document": groups[group][int(i)]}
                         for rank, i in enumerate(ranks[:30])]
            results.append({"group": group, "question": question, "retrieved": retrieved})
        save(OUT / "retrievals.json", results)
    print("dense retrieval complete", flush=True)


def export_cache():
    groups, manifest = freeze()
    model = Embeddings()
    vectors = {}
    texts = [d["content"] for docs in groups.values() for d in docs]
    texts.extend(q["question"] for q in manifest["questions"])
    for text in texts:
        identity = digest((model.identity + text).encode())
        row = model.db.execute("SELECT vector FROM cache WHERE id=?", (identity,)).fetchone()
        assert row, "cache incomplete"
        vectors[digest(text.encode())] = json.loads(row[0])
    save(OUT / "vectors.json", vectors)


def project_probe():
    _, manifest = freeze()
    model = Embeddings()
    reservation = len(manifest["questions"][0]["question"].encode())
    assert model.ledger["charged_upper_bound"]+reservation < 800000
    assert not any(entry.get("kind") == "production-probe" for entry in model.ledger["requests"]), "probe already reserved"
    entry = {"kind": "production-probe", "reserved": reservation, "status": "reserved", "usage": None}
    model.ledger["requests"].append(entry)
    model.ledger["charged_upper_bound"] += reservation
    save(model.ledger_path, model.ledger)
    environment = dict(os.environ, TELLTALE_CHUNKING_PROBE=str(OUT))
    result = subprocess.run(["go", "test", "./internal/ai/agent/knowledge_index_pipeline", "-run", "TestProductionEmbeddingProbe", "-count=1", "-v"], cwd=ROOT, env=environment, capture_output=True, text=True)
    (OUT / "production-probe.txt").write_text(result.stdout+result.stderr, encoding="utf-8")
    entry["status"] = "success" if result.returncode == 0 else "failed"
    save(model.ledger_path, model.ledger)
    assert result.returncode == 0, "production probe failed; inspect saved output"


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--export-cache", action="store_true")
    parser.add_argument("--project-probe", action="store_true")
    args = parser.parse_args()
    if args.project_probe:
        project_probe()
    elif args.export_cache:
        export_cache()
    elif args.live:
        lock = OUT / "paid-experiment.lock"
        descriptor = os.open(lock, os.O_CREAT | os.O_EXCL | os.O_WRONLY)
        try:
            run()
        finally:
            os.close(descriptor)
            lock.unlink()
    else:
        freeze()
