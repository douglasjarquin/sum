import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]


class PackagingInventoryTest(unittest.TestCase):
    def test_inventory_covers_pins_and_native_contracts_without_duplicate_ids(self):
        subprocess.run([sys.executable, str(ROOT / "scripts/generate_dependency_inventory.py"), "--check"], check=True)
        inventory = json.loads((ROOT / "docs/dependency-inventory.json").read_text())
        entries = inventory["dependencies"]
        self.assertEqual(len({entry["id"] for entry in entries}), len(entries))
        required = {"source", "version", "checksum", "license", "platforms", "requirements", "role", "owner", "contracts"}
        for entry in entries:
            self.assertTrue(required <= entry.keys(), entry["id"])
            self.assertTrue(entry["checksum"], entry["id"])
            self.assertIn(entry["role"], ("build", "runtime", "build-and-runtime", "setup"), entry["id"])
        ids = {entry["id"] for entry in entries}
        self.assertTrue({"go", "cobra", "go-toon", "sumctl", "herdr-core", "quota-axi", "remainder"} <= ids)
        self.assertTrue({"herdr", "herdr-mesh", "herdr-mesh-go", "mcp-go-sdk", "sumctl-go"}.isdisjoint(ids))
        self.assertFalse((ROOT / "lib" / "sumctl.py").exists())
        hc = next(entry for entry in entries if entry["id"] == "herdr-core")
        selected = json.loads((ROOT / "config/hc-release.json").read_text())
        self.assertEqual(hc["version"], selected["version"])
        self.assertEqual(selected["generation"], 1)
        self.assertEqual(set(selected["targets"]), {"macos-aarch64", "macos-x86_64", "linux-aarch64", "linux-x86_64", "windows-x86_64"})

    def test_sumctl_wrapper_keeps_an_inherited_install_root(self):
        root = Path(tempfile.mkdtemp())
        (root / "bin").mkdir()
        (root / ".local" / "bin").mkdir(parents=True)
        shutil.copy(ROOT / "bin" / "sumctl", root / "bin" / "sumctl")
        os.chmod(root / "bin" / "sumctl", 0o755)
        artifact = root / ".local" / "bin" / "sumctl"
        artifact.write_text('#!/bin/sh\necho "root=$SUM_INSTALL_ROOT"\n')
        os.chmod(artifact, 0o755)
        env = {k: v for k, v in os.environ.items() if k != "SUM_INSTALL_ROOT"}
        out = subprocess.check_output([str(root / "bin" / "sumctl"), "--version"], text=True, env=env)
        self.assertEqual(out, f"root={root}\n")
        env["SUM_INSTALL_ROOT"] = "/some/installation"
        out = subprocess.check_output([str(root / "bin" / "sumctl"), "--version"], text=True, env=env)
        self.assertEqual(out, "root=/some/installation\n")

    def test_relink_rewrites_absolute_hc_link_so_a_renamed_tree_still_resolves(self):
        staging = Path(tempfile.mkdtemp(prefix=".staging-hc-"))
        dest = staging / ".deps" / "hc" / "0.1.14-darwin-arm64"
        dest.mkdir(parents=True)
        binary = dest / "herdr-core"
        binary.write_text("#!/bin/sh\necho hc\n")
        os.chmod(binary, 0o755)
        link = staging / ".local" / "bin" / "hc"
        link.parent.mkdir(parents=True)
        link.symlink_to(binary)
        self.assertTrue(os.path.isabs(os.readlink(link)))
        node = shutil.which("node")
        self.assertTrue(node)
        subprocess.check_call([node, str(ROOT / "scripts" / "relink_runtime_links.mjs"), str(staging)])
        self.assertFalse(os.path.isabs(os.readlink(link)))
        released = Path(tempfile.mkdtemp(prefix="release-hc-")) / "tree"
        shutil.move(str(staging), str(released))
        moved = released / ".local" / "bin" / "hc"
        self.assertTrue(moved.resolve().is_file())


if __name__ == "__main__":
    unittest.main()
