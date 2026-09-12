import json
import os
from pathlib import Path
import shutil
import subprocess
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
        self.assertTrue({"go", "cobra", "mcp-go-sdk", "sumctl", "herdr-mesh", "herdr-mesh-go", "quota-axi", "remainder"} <= ids)
        self.assertNotIn("sumctl-go", ids)
        self.assertFalse((ROOT / "lib" / "sumctl.py").exists())
        mesh = next(entry for entry in entries if entry["id"] == "herdr-mesh")
        prior = next(entry for entry in entries if entry["id"] == "herdr-mesh-go")
        self.assertEqual(mesh["source"], "go/cmd/herdr-mesh")
        self.assertEqual(prior["source"], mesh["source"])
        self.assertIn("darwin-arm64", prior["platforms"])

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

    def test_sumctl_wrapper_execs_the_prior_packager_artifact_name(self):
        text = (ROOT / "bin" / "sumctl").read_text()
        self.assertIn(".local/bin/sumctl\"", text)
        self.assertIn(".local/bin/sumctl-go", text)
        sumctl_line = text.index(".local/bin/sumctl\"")
        go_line = text.index(".local/bin/sumctl-go")
        self.assertLess(sumctl_line, go_line)

    def test_mcp_smoke_launches_the_public_wrapper(self):
        text = (ROOT / "scripts" / "mcp_smoke.mjs").read_text()
        self.assertIn('path.join(root, "bin", "herdr-mesh")', text)
        self.assertNotIn('path.join(root, ".local", "bin", "herdr-mesh")', text)

    def test_wrapper_execs_herdr_mesh_go_when_that_is_the_only_artifact(self):
        root = Path(tempfile.mkdtemp())
        (root / "bin").mkdir()
        (root / ".local" / "bin").mkdir(parents=True)
        shutil.copy(ROOT / "bin" / "herdr-mesh", root / "bin" / "herdr-mesh")
        os.chmod(root / "bin" / "herdr-mesh", 0o755)
        artifact = root / ".local" / "bin" / "herdr-mesh-go"
        artifact.write_text("#!/bin/sh\necho herdr-mesh 0.1.0\n")
        os.chmod(artifact, 0o755)
        out = subprocess.check_output([str(root / "bin" / "herdr-mesh"), "--version"], text=True)
        self.assertEqual(out, "herdr-mesh 0.1.0\n")

    def test_relink_rewrites_absolute_remainder_link_so_a_renamed_tree_still_resolves(self):
        staging = Path(tempfile.mkdtemp(prefix=".staging-remainder-"))
        dest = staging / ".deps" / "remainder" / "0.2.1-darwin-arm64" / "remainder_v0.2.1_darwin_arm64"
        dest.mkdir(parents=True)
        binary = dest / "remainder"
        binary.write_text("#!/bin/sh\necho remainder\n")
        os.chmod(binary, 0o755)
        link = staging / ".local" / "bin" / "remainder"
        link.parent.mkdir(parents=True)
        link.symlink_to(binary)
        self.assertTrue(os.path.isabs(os.readlink(link)))
        node = shutil.which("node")
        self.assertTrue(node)
        subprocess.check_call([node, str(ROOT / "scripts" / "relink_runtime_links.mjs"), str(staging)])
        self.assertFalse(os.path.isabs(os.readlink(link)))
        released = Path(tempfile.mkdtemp(prefix="release-remainder-")) / "tree"
        shutil.move(str(staging), str(released))
        moved = released / ".local" / "bin" / "remainder"
        self.assertTrue(moved.resolve().is_file())
        smoke = (ROOT / "scripts" / "mcp_smoke.mjs").read_text()
        self.assertIn("relinkRuntimeBin", smoke)


if __name__ == "__main__":
    unittest.main()
