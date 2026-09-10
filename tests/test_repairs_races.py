from __future__ import annotations

import concurrent.futures
import json
import sys

import test_fleet


class RepairRaceTest(test_fleet.FleetLab):
    def setUp(self):
        super().setUp()
        self.install, self.store = self.installation()
        self.env = {"FAKE_PARENT_CWD": str(self.install.resolve())}
        self.ctl(self.install, self.store, "settings", "set", "--global", "4", env=self.env)
        self.task = self.ctl(
            self.install,
            self.store,
            "dispatch",
            "--repo",
            self.project("repair-race"),
            "--brief",
            self.brief(),
            "--harness",
            "codex",
            "--approved",
            env=self.env,
        )
        self.pane_state(self.task["pane"], agent_status="idle")

    def cli_repair(self, key):
        attempt = self.store.read(self.task["id"])["execution"]["worker"]["id"]
        return self.cli(
            [
                sys.executable,
                self.install / "lib/sumctl.py",
                "--home",
                self.store.home,
                "repair",
                "send",
                self.task["id"],
                "--attempt",
                attempt,
                "--key",
                key,
                "--text",
                "Apply one bounded correction.",
            ],
            env=self.env,
        )

    def prompt_count(self):
        return sum(1 for call in self.calls() if call[:2] == ["agent", "prompt"])

    def test_two_contenders_for_last_iteration_produce_at_most_one_delivery(self):
        # Given: one consumed iteration and one remaining iteration.
        first = self.cli_repair("repair-1")
        self.assertEqual(first.returncode, 0, first.stderr)
        self.pane_state(self.task["pane"], agent_status="idle")
        prompts = self.prompt_count()

        # When: distinct operations race for the final iteration.
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            results = list(pool.map(self.cli_repair, ("repair-2a", "repair-2b")))

        # Then: exactly one succeeds, one is exhausted, and only one prompt is added.
        self.assertEqual(sorted(result.returncode for result in results), [0, 1])
        self.assertEqual(self.prompt_count(), prompts + 1)
        saved = self.store.read(self.task["id"])
        self.assertEqual(saved["repairs"]["consumed"], 2)
        self.assertEqual(len([q for q in saved["questions"] if q.get("decision")]), 1)

    def test_same_operation_race_charges_and_prompts_once(self):
        # Given: a settled worker and one stable operation identity.
        prompts = self.prompt_count()

        # When: two coordinators calls submit that identity concurrently.
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            results = list(pool.map(lambda _: self.cli_repair("same-key"), range(2)))

        # Then: both calls are successful observations of one charged delivery.
        self.assertEqual([result.returncode for result in results], [0, 0])
        payloads = [json.loads(result.stdout) for result in results]
        self.assertEqual(sum(1 for payload in payloads if payload["duplicate"]), 1)
        self.assertEqual(self.prompt_count(), prompts + 1)
        self.assertEqual(self.store.read(self.task["id"])["repairs"]["consumed"], 1)


if __name__ == "__main__":
    import unittest

    unittest.main()
