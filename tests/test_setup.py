import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import tarfile
import tempfile
import tomllib
import unittest
ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("setup_sum", ROOT / "scripts/setup.py")
setup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(setup)

class SetupTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def test_generated_configuration_is_valid_and_local(self):
        setup.configure(self.root)
        claude = json.loads((self.root / '.mcp.json').read_text())
        self.assertEqual(claude['mcpServers']['sum-herdr']['command'], str(self.root / 'bin/herdr-mesh'))
        codex = tomllib.loads((self.root / '.codex/config.toml').read_text())
        self.assertEqual(codex['mcp_servers']['sum-herdr']['tool_timeout_sec'], 70)
        self.assertIn('HERDR_SOCKET_PATH', codex['mcp_servers']['sum-herdr']['env_vars'])
        opencode = json.loads((self.root / 'opencode.json').read_text())
        self.assertEqual(opencode['mcp']['sum-herdr']['type'], 'local')

    def test_setup_designates_installation_without_claiming_a_coordinator(self):
        setup.configure(self.root)
        state = json.loads((self.root / '.sum/state.json').read_text())
        self.assertEqual(state['schema'], 1)
        self.assertTrue(state['instance'])
        self.assertFalse((self.root / '.sum/context.json').exists())
        (self.root / '.sum/state.json').write_text('{"schema": 1, "sum_version": "0.1.0", "created_at": "x"}')
        setup.configure(self.root)
        self.assertNotIn('instance', json.loads((self.root / '.sum/state.json').read_text()))  # Never rewrites existing state.

    def test_setup_in_a_development_checkout_never_designates_it(self):
        (self.root / '.sum').mkdir()
        marker = json.dumps({'schema': 1, 'kind': 'development', 'name': 'x', 'installation': '/elsewhere'})
        (self.root / '.sum/dev.json').write_text(marker)
        setup.configure(self.root)
        self.assertFalse((self.root / '.sum/state.json').exists())
        self.assertEqual((self.root / '.sum/dev.json').read_text(), marker)
        self.assertEqual(json.loads((self.root / '.mcp.json').read_text())['mcpServers']['sum-herdr']['command'], str(self.root / 'bin/herdr-mesh'))

    def test_setup_is_idempotent(self):
        setup.configure(self.root)
        before = {p.relative_to(self.root): p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        setup.configure(self.root)
        after = {p.relative_to(self.root): p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        self.assertEqual(before, after)

    def test_existing_other_mcp_servers_are_preserved(self):
        (self.root / '.mcp.json').write_text(json.dumps({'mcpServers': {'existing': {'command': 'keep-me'}}}))
        setup.configure(self.root)
        value = json.loads((self.root / '.mcp.json').read_text())
        self.assertEqual(value['mcpServers']['existing']['command'], 'keep-me')

    def test_conflicting_mcp_entry_is_not_overwritten(self):
        path = self.root / '.mcp.json'
        path.write_text(json.dumps({'mcpServers': {'sum-herdr': {'command': 'user-owned'}}}))
        before = path.read_text()
        with self.assertRaises(RuntimeError):
            setup.configure(self.root)
        self.assertEqual(path.read_text(), before)

    def test_existing_codex_settings_are_preserved(self):
        path = self.root / '.codex/config.toml'
        path.parent.mkdir()
        path.write_text('model = "keep-my-model"\n[mcp_servers.other]\ncommand = "keep-me"\n')
        setup.configure(self.root)
        setup.configure(self.root)
        data = tomllib.loads(path.read_text())
        self.assertEqual(data['model'], 'keep-my-model')
        self.assertEqual(data['mcp_servers']['other']['command'], 'keep-me')
        self.assertEqual(path.read_text().count('# BEGIN sum-managed MCP'), 1)

    def test_unmanaged_codex_entry_is_not_overwritten(self):
        path = self.root / 'config.toml'
        path.write_text('[mcp_servers.sum-herdr]\ncommand = "user-owned"\n')
        with self.assertRaises(RuntimeError):
            setup.codex_config(path, '/new/bin')

    def test_all_skill_links_resolve(self):
        for parent in (ROOT / '.agents/skills', ROOT / '.claude/skills'):
            for name in ('dispatch', 'worker', 'rundown', 'delivery', 'develop', 'update'):
                self.assertTrue((parent / ('sum-' + name) / 'SKILL.md').is_file())
        self.assertEqual((ROOT / 'CLAUDE.md').resolve(), ROOT / 'AGENTS.md')

    def test_tool_links_are_created_once_and_never_retargeted(self):
        link = self.root / '.local/bin/node'
        first = setup.sumctl.link_tool(link, '/opt/node-22.19.0/bin/node')
        self.assertEqual((first['created'], os.readlink(link)), (True, '/opt/node-22.19.0/bin/node'))
        second = setup.sumctl.link_tool(link, '/opt/node-22.20.0/bin/node')  # A new pin never moves a link a live process may use.
        self.assertEqual((second['created'], second['differs'], os.readlink(link)), (False, True, '/opt/node-22.19.0/bin/node'))
        (self.root / 'regular').write_text('x')
        with self.assertRaises(setup.sumctl.SumError):
            setup.sumctl.link_tool(self.root / 'regular', '/elsewhere')

    def test_installed_mesh_is_never_rewritten_and_drift_is_only_reported(self):
        mesh = self.root / '.deps/herdr-mesh'
        self.assertEqual(setup.sumctl.mesh_state(ROOT, mesh)['installed'], False)
        (mesh / 'dist').mkdir(parents=True)
        self.assertEqual(setup.sumctl.mesh_state(ROOT, mesh), {'installed': True, 'patched': False, 'matches_source': False})
        setup.sumctl.apply_overlay(ROOT, mesh)
        self.assertTrue(setup.sumctl.mesh_state(ROOT, mesh)['matches_source'])
        (mesh / 'dist/server.js').write_text('// older overlay\n')
        (mesh / '.sum-patched').write_text(json.dumps(
            {'upstream': setup.sumctl.MESH_REV, 'server_sha256': 'old', 'commands_sha256': 'old'}
        ))
        state = setup.sumctl.mesh_state(ROOT, mesh)
        self.assertEqual((state['patched'], state['matches_source']), (True, False))
        self.assertEqual((mesh / 'dist/server.js').read_text(), '// older overlay\n')
        with self.assertRaisesRegex(setup.sumctl.SumError, 'never rewritten in place'):
            setup.sumctl.install_mesh(mesh, ROOT)
        self.assertEqual((mesh / 'dist/server.js').read_text(), '// older overlay\n')

    def test_remainder_is_not_a_mise_which_tool(self):
        self.assertNotIn("remainder", setup.sumctl.TOOLS)

    def test_install_remainder_verifies_checksum_extracts_once_and_never_retargets(self):
        pkg = self.root / "pkg"
        pkg.mkdir()
        binary = pkg / "remainder"
        binary.write_text("#!/bin/sh\necho remainder-fixture\n")
        binary.chmod(0o755)
        archive = self.root / "remainder_v0.0.0_test.tar.gz"
        with tarfile.open(archive, "w:gz") as bundle:
            bundle.add(binary, arcname="remainder")
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        pin = {"version": "0.0.0", "sha256": digest, "asset": archive.name}
        fetched = []

        def fetch(url, dest):
            fetched.append(url)
            shutil.copyfile(archive, dest)

        runtime = self.root / "runtime"
        first = setup.sumctl.install_remainder(runtime, fetch=fetch, pin=pin, platform_name="darwin-arm64")
        link = runtime / ".local/bin/remainder"
        dest = runtime / ".deps/remainder/0.0.0-darwin-arm64"
        self.assertTrue(first["installed"])
        self.assertTrue(first["created"])
        self.assertFalse(first["rewritten"])
        self.assertTrue(dest.is_dir())
        self.assertEqual(os.readlink(link), str(dest / "remainder"))
        self.assertEqual(len(fetched), 1)
        second = setup.sumctl.install_remainder(runtime, fetch=fetch, pin=pin, platform_name="darwin-arm64")
        self.assertEqual(len(fetched), 1)
        self.assertFalse(second["created"])
        self.assertFalse(second["rewritten"])
        self.assertEqual(os.readlink(link), str(dest / "remainder"))
        later = {"version": "0.0.1", "sha256": digest, "asset": archive.name}
        third = setup.sumctl.install_remainder(runtime, fetch=fetch, pin=later, platform_name="darwin-arm64")
        self.assertEqual(len(fetched), 2)
        self.assertTrue(third["differs"])
        self.assertEqual(os.readlink(link), str(dest / "remainder"))

    def test_install_remainder_refuses_checksum_mismatch_without_leaving_dest(self):
        archive = self.root / "remainder_v0.0.0_test.tar.gz"
        archive.write_bytes(b"not-a-valid-remainder-archive")
        pin = {"version": "0.0.0", "sha256": "0" * 64, "asset": archive.name}

        def fetch(url, dest):
            shutil.copyfile(archive, dest)

        runtime = self.root / "runtime"
        with self.assertRaisesRegex(setup.sumctl.SumError, "checksum mismatch"):
            setup.sumctl.install_remainder(runtime, fetch=fetch, pin=pin, platform_name="linux-amd64")
        self.assertFalse((runtime / ".deps/remainder/0.0.0-linux-amd64").exists())
        self.assertFalse((runtime / ".local/bin/remainder").exists())

    def test_remainder_default_fetch_honors_no_download(self):
        os.environ["SUM_REMAINDER_NO_DOWNLOAD"] = "1"
        self.addCleanup(os.environ.pop, "SUM_REMAINDER_NO_DOWNLOAD", None)
        pin = {"version": "0.2.1", "sha256": "abc", "asset": "remainder_v0.2.1_darwin_arm64.tar.gz"}
        with self.assertRaisesRegex(setup.sumctl.SumError, "disabled"):
            setup.sumctl.install_remainder(self.root / "runtime", pin=pin, platform_name="darwin-arm64")

    def test_mise_and_all_python_sources_parse(self):
        tomllib.loads((ROOT / 'mise.toml').read_text())
        for parent in ('lib', 'scripts', 'tests'):
            for path in (ROOT / parent).rglob('*.py'):
                compile(path.read_text(), str(path), 'exec')

if __name__ == '__main__':
    unittest.main()
