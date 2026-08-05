import io
import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import worker


class FakeTransport:
    def __init__(self):
        self.calls = []

    def start(self, headless):
        self.calls.append(("start", headless))

    def fetch(self, url, wait_until, wait_for_selector):
        self.calls.append(("fetch", url, wait_until, wait_for_selector))
        return {"ok": True, "html": "<main>ok</main>", "blocked": False}

    def fetch_gupy(self, url):
        self.calls.append(("fetch_gupy", url))
        return {"ok": True, "records": [], "html": ""}

    def warm_indeed(self):
        self.calls.append(("warm_indeed",))
        return {"ok": True}

    def close(self):
        self.calls.append(("close",))


def job(job_id):
    return {"id": job_id, "title": f"Role {job_id}"}


class WorkerTests(unittest.TestCase):
    def test_ndjson_contract_fixture_is_one_json_record_per_line(self):
        path = Path(__file__).resolve().parents[3] / "contracts" / "browser-worker.ndjson"
        lines = path.read_text(encoding="utf-8").splitlines()
        self.assertTrue(lines)

        cases = [json.loads(line) for line in lines]
        names = {case["name"] for case in cases}
        self.assertTrue({"start", "fetch_normal", "fetch_blocked", "fetch_gupy", "warm_indeed", "close"} <= names)
        self.assertTrue({"invalid_json", "unknown_command"} <= names)

        for case in cases:
            self.assertIsInstance(case["response"], dict)
            self.assertIn("ok", case["response"])
            if not case["response"]["ok"]:
                self.assertIsInstance(case["response"].get("error"), str)
                self.assertTrue(case["response"]["error"].strip())
            if "requestLine" in case:
                with self.assertRaises(json.JSONDecodeError):
                    json.loads(case["requestLine"])
            else:
                self.assertIsInstance(case["request"], dict)
                self.assertIn("cmd", case["request"])

        blocked = next(case for case in cases if case["name"] == "fetch_blocked")
        self.assertTrue(blocked["response"]["blocked"])
        normal = next(case for case in cases if case["name"] == "fetch_normal")
        self.assertFalse(normal["response"]["blocked"])

    def test_resume_fixture_versions_async_states_and_error_codes(self):
        path = Path(__file__).resolve().parents[3] / "contracts" / "resume-studio.json"
        fixture = json.loads(path.read_text(encoding="utf-8"))

        self.assertEqual(set(fixture["saveVersionResponse"]), {"id"})
        self.assertTrue(fixture["saveVersionResponse"]["id"])
        self.assertEqual(set(fixture["renameVersionResponse"]), {"id", "name"})
        self.assertTrue(fixture["renameVersionResponse"]["id"])
        self.assertTrue(fixture["renameVersionResponse"]["name"])

        async_status = fixture["asyncJobStatus"]
        self.assertEqual(set(async_status), {"running", "done"})
        self.assertEqual(async_status["running"], {"state": "running"})
        self.assertEqual(async_status["done"]["state"], "done")
        self.assertEqual(async_status["done"]["status"], 200)
        self.assertIsInstance(async_status["done"]["result"], dict)

        real_error_codes = {
            "missing_name",
            "invalid_ai_json",
            "ai_timeout",
            "ai_consent_required",
            "ai_operation_budget_spent",
            "ai_budget_spent",
            "ai_quota_exhausted",
            "ai_model_unavailable",
            "ai_rate_limited",
            "ai_key_invalid",
            "ai_key_required",
            "ai_unavailable",
            "invalid_file",
            "unsupported_format",
            "file_too_large",
            "empty_resume_text",
            "ocr_not_installed",
            "ocr_no_text",
            "ocr_timeout",
            "ocr_failed",
            "invalid_request",
            "internal_error",
            "empty_job_description",
            "unknown_operation",
            "job_not_found",
            "version_not_found",
        }
        self.assertTrue(set(fixture["errorCodes"]))
        self.assertTrue(set(fixture["errorCodes"]) <= real_error_codes)
        self.assertEqual(
            {entry["code"] for entry in fixture["errorResponses"]},
            set(fixture["errorCodes"]),
        )
        for entry in fixture["errorResponses"]:
            self.assertIsInstance(entry["message"], str)
            self.assertTrue(entry["message"].strip())

    def test_block_detection_is_case_insensitive(self):
        self.assertFalse(worker.looks_blocked("normal results"))
        self.assertTrue(worker.looks_blocked("SECURITY VERIFICATION required"))

    def test_recursive_job_array_search(self):
        value = {"payload": {"items": [[{"title": "Engineer", "id": "1"}] ]}}
        self.assertEqual(worker.find_job_array(value), [{"title": "Engineer", "id": "1"}])

    def test_job_array_limits_depth_quantity_and_children(self):
        nested = job("deep")
        for _ in range(worker.MAX_JOB_ARRAY_DEPTH):
            nested = {"child": nested}
        self.assertEqual(worker.find_job_array(nested), [])

        deep_enough = [job("deep-enough")]
        for _ in range(worker.MAX_JOB_ARRAY_DEPTH - 1):
            deep_enough = {"child": deep_enough}
        self.assertEqual(worker.find_job_array(deep_enough), [job("deep-enough")])

        records = worker.find_job_array([job(str(i)) for i in range(worker.MAX_JOB_RECORDS + 1)])
        self.assertEqual(len(records), worker.MAX_JOB_RECORDS)

        children = {f"child-{i}": {"items": [job(str(i))]} for i in range(worker.MAX_CHILDREN_PER_NODE + 1)}
        self.assertEqual(len(worker.find_job_array(children)), worker.MAX_CHILDREN_PER_NODE)

    def test_deduplicate_records_by_id_or_url(self):
        records = [
            job("1"),
            job("1"),
            {"title": "A", "careerPageUrl": "https://example.test/a"},
            {"title": "A copy", "careerPageUrl": "https://example.test/a"},
            {"title": "No key"},
        ]
        self.assertEqual(worker.deduplicate_records(records), [records[0], records[2], records[4]])

    def test_dispatch_and_main_keep_stdout_one_json_response_per_line(self):
        transport = FakeTransport()
        output = io.StringIO()
        request_lines = "not-json\n" + json.dumps({"cmd": "wat"}) + "\n" + json.dumps({"cmd": "start"}) + "\n" + json.dumps({"cmd": "close"}) + "\n"
        self.assertEqual(worker.main(transport, io.StringIO(request_lines), output), 0)

        responses = [json.loads(line) for line in output.getvalue().splitlines()]
        self.assertEqual(len(responses), 4)
        self.assertFalse(responses[0]["ok"])
        self.assertFalse(responses[1]["ok"])
        self.assertTrue(responses[2]["ok"])
        self.assertTrue(responses[3]["ok"])
        self.assertEqual([call[0] for call in transport.calls], ["start", "close"])

    def test_dispatch_unknown_and_close(self):
        transport = FakeTransport()
        responses = []
        self.assertTrue(worker.dispatch({"cmd": "unknown"}, transport, responses.append))
        self.assertFalse(responses[-1]["ok"])
        self.assertFalse(worker.dispatch({"cmd": "close"}, transport, responses.append))
        self.assertEqual(transport.calls, [("close",)])


if __name__ == "__main__":
    unittest.main()
