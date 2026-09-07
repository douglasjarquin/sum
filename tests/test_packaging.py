import json
from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[1]


class PackagingInventoryTest(unittest.TestCase):
    def test_inventory_covers_pins_and_native_contracts_without_duplicate_ids(self):
        inventory = json.loads((ROOT / "docs/dependency-inventory.json").read_text())
        entries = inventory["dependencies"]
        self.assertEqual(len({entry["id"] for entry in entries}), len(entries))
        required = {"source", "version", "checksum", "license", "platforms", "requirements", "role", "owner", "contracts"}
        for entry in entries:
            self.assertTrue(required <= entry.keys(), entry["id"])
            self.assertTrue(entry["checksum"], entry["id"])
            self.assertIn(entry["role"], ("build", "runtime", "build-and-runtime"), entry["id"])
        ids = {entry["id"] for entry in entries}
        self.assertTrue({"go", "cobra", "mcp-go-sdk", "sumctl-go", "herdr-mesh", "quota-axi"} <= ids)

    def test_go_module_uses_the_reviewed_official_mcp_sdk(self):
        go_mod = (ROOT / "go/go.mod").read_text()
        self.assertRegex(go_mod, r"github\.com/modelcontextprotocol/go-sdk\s+v1\.6\.1")
        self.assertIn("github.com/spf13/cobra v1.9.1", go_mod)
        self.assertNotRegex(go_mod, r"(?m)^\s*github\.com/spf13/viper\s")


if __name__ == "__main__":
    unittest.main()
