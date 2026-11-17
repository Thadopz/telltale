import json
import os
from pathlib import Path
import unittest

from rcaeval_diagnostics import diagnostic_envelope, rank_changes


class RealTelemetryTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        import sys
        cls.root = Path(os.environ["TELLTALE_RCAEVAL_ROOT"])
        sys.path.insert(0, os.environ["TELLTALE_RCAEVAL_SOURCE"])

    def test_constant_baseline_signals_survive_candidate_generation(self):
        from RCAEval.agent.toolkit import CaseToolkit
        for case, metric in [
            ("re2-ob-0004", "productcatalogservice_socket"),
            ("re2-ob-0006", "emailservice_diskio"),
        ]:
            with self.subTest(case=case):
                toolkit = CaseToolkit(str(self.root / "tasks/re2-ob/cases" / case))
                improved = rank_changes(toolkit, 8)
                entry = next(row for row in improved["ranked"] if row["name"] == metric)
                self.assertTrue(entry["constant_baseline_shift"])
                original = toolkit.rank_anomalies(modality="metrics", top_k=100)
                self.assertNotIn(metric, [row["name"] for row in original["ranked"]])
                envelope = diagnostic_envelope(toolkit, "rank_anomalies", {}, improved)
                observation = next(row for row in envelope["evidence"]["observations"] if row["fields"]["metric"] == metric)
                actual = toolkit.get_metric(name=metric)
                self.assertEqual(observation["fields"]["before_mean"], actual["before_inject"]["mean"])
                self.assertEqual(observation["fields"]["after_mean"], actual["after_inject"]["mean"])
                self.assertFalse(envelope["evidence"]["provenance"]["runtime_verified"])

    def test_all_six_cases_produce_finite_bounded_candidates(self):
        from RCAEval.agent.toolkit import CaseToolkit
        task_dir = self.root / "tasks/re2-ob"
        manifest = json.loads((task_dir / "manifest.json").read_text())
        for case in manifest["case_ids"]:
            toolkit = CaseToolkit(str(task_dir / "cases" / case))
            result = rank_changes(toolkit, 100)
            self.assertLessEqual(len(result["ranked"]), 8)
            envelope = diagnostic_envelope(toolkit, "rank_anomalies", {}, result)
            json.dumps(envelope, allow_nan=False)
            self.assertLessEqual(len(envelope["evidence"]["observations"]), 4)

    def test_ranking_ablation_preserves_tool_parameters(self):
        from RCAEval.agent.toolkit import CaseToolkit, TOOLS
        from rcaeval import available_tools

        toolkit = CaseToolkit(str(self.root / "tasks/re2-ob/cases/re2-ob-0004"))
        baseline = available_tools(toolkit.task, TOOLS)
        ablation = available_tools(toolkit.task, TOOLS, "baro")
        for current, original in zip(baseline, ablation, strict=True):
            self.assertEqual(current["parameters"], original["parameters"])
            if current["name"] == "rank_anomalies":
                self.assertIn("baseline_change_v1", current["description"])
                self.assertIn("original BARO", original["description"])
            else:
                self.assertEqual(current, original)


if __name__ == "__main__":
    unittest.main()
