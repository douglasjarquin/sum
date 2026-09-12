import json
from pathlib import Path
import shutil
import tempfile
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
        self.assertTrue({"go", "cobra", "mcp-go-sdk", "sumctl-go", "herdr-mesh", "quota-axi", "remainder"} <= ids)
        self.assertNotIn("herdr-mesh-go", ids)
        mesh = next(entry for entry in entries if entry["id"] == "herdr-mesh")
        self.assertEqual(mesh["source"], "go/cmd/herdr-mesh")

    def test_go_module_uses_the_reviewed_official_mcp_sdk(self):
        go_mod = (ROOT / "go/go.mod").read_text()
        self.assertRegex(go_mod, r"github\.com/modelcontextprotocol/go-sdk\s+v1\.6\.1")
        self.assertIn("github.com/spf13/cobra v1.9.1", go_mod)
        self.assertNotRegex(go_mod, r"(?m)^\s*github.com/spf13/viper\s")

    def test_prior_packager_can_copy_mesh_overlay_from_this_tree(self):
        src = ROOT / "patches" / "herdr-mesh"
        self.assertTrue((src / "server.js").is_file())
        self.assertTrue((src / "commands.mjs").is_file())
        dest = Path(tempfile.mkdtemp()) / "dist"
        dest.mkdir(parents=True)
        shutil.copyfile(src / "server.js", dest / "server.js")
        shutil.copyfile(src / "commands.mjs", dest / "sum-commands.mjs")
        self.assertGreater((dest / "server.js").stat().st_size, 0)
        self.assertGreater((dest / "sum-commands.mjs").stat().st_size, 0)

    def test_herdr_mesh_wrapper_execs_the_prior_packager_artifact_name(self):
        text = (ROOT / "bin" / "herdr-mesh").read_text()
        self.assertIn('.local/bin/herdr-mesh"', text)
        self.assertIn(".local/bin/herdr-mesh-go", text)
        herdr_line = text.index('.local/bin/herdr-mesh"')
        go_line = text.index(".local/bin/herdr-mesh-go")
        self.assertLess(herdr_line, go_line)


if __name__ == "__main__":
    unittest.main()
