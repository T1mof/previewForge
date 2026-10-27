import copy
import importlib.util
import sys
from pathlib import Path
import unittest

sys.dont_write_bytecode = True

spec = importlib.util.spec_from_file_location("pr_event", Path(__file__).with_name("pr-event.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class EventTests(unittest.TestCase):
    def setUp(self):
        repo = {"id": 123, "full_name": "owner/repo"}
        self.live = {"number": 42, "state": "open", "author_association": "OWNER", "base": {"repo": repo}, "head": {"repo": repo, "sha": "a" * 40}}
        self.event = {"number": 42, "action": "synchronize", "repository": repo, "pull_request": copy.deepcopy(self.live)}

    def decision(self):
        return module.decide(self.event, self.live, "owner/repo", 123, "preview.example.test")

    def test_current_trusted_head_deploys(self):
        result = self.decision()
        self.assertEqual(result["action"], "deploy")
        self.assertEqual(result["host"], "pr-123-42.preview.example.test")

    def test_closed_live_pr_deletes_even_after_late_build(self):
        self.live["state"] = "closed"
        self.assertEqual(self.decision()["action"], "delete")

    def test_stale_build_is_skipped(self):
        self.live["head"]["sha"] = "b" * 40
        self.assertEqual(self.decision()["action"], "skip")

    def test_old_close_event_cannot_delete_reopened_pr(self):
        self.event["action"] = "closed"
        self.assertEqual(self.decision()["action"], "skip")

    def test_fork_and_untrusted_author_are_skipped(self):
        self.event["pull_request"]["head"]["repo"] = {"id": 999, "full_name": "fork/repo"}
        self.assertEqual(self.decision()["action"], "skip")
        self.setUp()
        self.live["author_association"] = "CONTRIBUTOR"
        self.assertEqual(self.decision()["action"], "skip")

    def test_wrong_repository_fails_closed(self):
        self.event["repository"] = {"id": 456, "full_name": "owner/repo"}
        with self.assertRaises(ValueError):
            self.decision()

    def test_invalid_sha_and_domain_are_rejected(self):
        self.live["head"]["sha"] = "bad\nsha"
        with self.assertRaises(ValueError):
            self.decision()
        self.setUp()
        with self.assertRaises(ValueError):
            module.decide(self.event, self.live, "owner/repo", 123, "domain\ninjected")

    def test_cleanup_does_not_require_current_domain(self):
        self.live["state"] = "closed"
        self.assertEqual(module.decide(self.event, self.live, "owner/repo", 123, "")["action"], "delete")


if __name__ == "__main__":
    unittest.main()
