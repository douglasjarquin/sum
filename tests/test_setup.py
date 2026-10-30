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
from unittest.mock import patch
from contextlib import redirect_stdout
import io
ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("setup_sum", ROOT / "scripts/setup.py")
setup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(setup)

class SetupTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def test_generated_configuration_has_no_custom_sum_mcp(self):
        setup.configure(self.root)
        for name in ('.mcp.json', '.cursor/mcp.json', 'opencode.json', '.codex/config.toml', '.local/mcp.example.json'):
            self.assertFalse((self.root / name).exists(), name)

    def test_setup_designates_installation_without_claiming_a_coordinator(self):
        setup.configure(self.root)
        state = json.loads((self.root / '.sum/state.json').read_text())
        self.assertEqual(state['schema'], 2)
        self.assertEqual(state['backend'], 'hc')
        physical = self.root / '.sum/generations' / state['generation']
        self.assertTrue(json.loads((physical / 'state.json').read_text())['instance'])
        self.assertFalse((self.root / '.sum/context.json').exists())
        (self.root / '.sum/state.json').write_text('{"schema": 1, "sum_version": "0.1.0", "created_at": "x"}')
        with self.assertRaisesRegex(RuntimeError, 'explicit HC migration'):
            setup.configure(self.root)
        self.assertEqual(json.loads((self.root / '.sum/state.json').read_text())['schema'], 1)

    def test_setup_in_a_development_checkout_never_designates_it(self):
        (self.root / '.sum').mkdir()
        marker = json.dumps({'schema': 1, 'kind': 'development', 'name': 'x', 'installation': '/elsewhere'})
        (self.root / '.sum/dev.json').write_text(marker)
        setup.configure(self.root)
        self.assertFalse((self.root / '.sum/state.json').exists())
        self.assertEqual((self.root / '.sum/dev.json').read_text(), marker)
        self.assertFalse((self.root / '.mcp.json').exists())

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
        self.assertNotIn('sum-herdr', path.read_text())

    def test_unmanaged_codex_entry_is_not_overwritten(self):
        path = self.root / 'config.toml'
        path.write_text('[mcp_servers.sum-herdr]\ncommand = "user-owned"\n')
        with self.assertRaises(RuntimeError):
            setup.remove_codex_mcp(path, '/new/bin/herdr-mesh')

    def test_owned_legacy_mcp_is_removed_without_replacing_unrelated_entries(self):
        command = str(self.root / 'bin/herdr-mesh')
        path = self.root / '.mcp.json'
        path.write_text(json.dumps({'mcpServers': {'sum-herdr': {'command': command, 'args': []}, 'keep': {'command': 'keep'}}}))
        codex = self.root / '.codex/config.toml'
        codex.parent.mkdir()
        codex.write_text('model = "keep"\n# BEGIN sum-managed MCP\n[mcp_servers.sum-herdr]\ncommand = ' + json.dumps(command) + '\nstartup_timeout_sec = 20\ntool_timeout_sec = 70\nenv_vars = ["HERDR_ENV", "HERDR_PANE_ID", "HERDR_SESSION", "HERDR_SOCKET_PATH", "HERDR_BIN_PATH", "SUM_SESSION"]\n# END sum-managed MCP\n')
        setup.configure(self.root, retire_legacy=True)
        self.assertEqual(json.loads(path.read_text()), {'mcpServers': {'keep': {'command': 'keep'}}})
        self.assertEqual(tomllib.loads(codex.read_text()), {'model': 'keep'})

    def test_customized_managed_block_is_not_removed(self):
        path = self.root / '.codex/config.toml'
        path.parent.mkdir()
        text = '# BEGIN sum-managed MCP\n[mcp_servers.sum-herdr]\ncommand = "custom"\n# END sum-managed MCP\n'
        path.write_text(text)
        with self.assertRaisesRegex(RuntimeError, 'ownership'):
            setup.configure(self.root)
        self.assertEqual(path.read_text(), text)

    def test_all_skill_links_resolve(self):
        for parent in (ROOT / '.agents/skills', ROOT / '.claude/skills'):
            for name in ('dispatch', 'worker', 'status', 'delivery', 'develop', 'update',
                         'delegate', 'assign', 'work', 'deliver', 'ship', 'pr', 'inbox', 'sweep', 'recover', 'rundown', 'dev', 'upgrade', 'rollback'):
                self.assertTrue((parent / ('sum-' + name) / 'SKILL.md').is_file())
        self.assertEqual((ROOT / 'CLAUDE.md').resolve(), ROOT / 'AGENTS.md')

    def test_tool_links_are_created_once_and_never_retargeted(self):
        link = self.root / '.local/bin/node'
        first = setup.runtime_install.link_tool(link, '/opt/node-22.19.0/bin/node')
        expected = os.path.relpath('/opt/node-22.19.0/bin/node', start=link.parent)
        self.assertEqual((first['created'], os.readlink(link)), (True, expected))
        second = setup.runtime_install.link_tool(link, '/opt/node-22.20.0/bin/node')  # A new pin never moves a link a live process may use.
        self.assertEqual((second['created'], second['differs'], os.readlink(link)), (False, True, expected))
        (self.root / 'regular').write_text('x')
        with self.assertRaises(setup.runtime_install.InstallError):
            setup.runtime_install.link_tool(self.root / 'regular', '/elsewhere')

    def test_remainder_is_not_a_mise_which_tool(self):
        self.assertNotIn("remainder", setup.runtime_install.TOOLS)

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
        first = setup.runtime_install.install_remainder(runtime, fetch=fetch, pin=pin, platform_name="darwin-arm64")
        link = runtime / ".local/bin/remainder"
        dest = runtime / ".deps/remainder/0.0.0-darwin-arm64"
        self.assertTrue(first["installed"])
        self.assertTrue(first["created"])
        self.assertFalse(first["rewritten"])
        self.assertTrue(dest.is_dir())
        self.assertEqual(os.readlink(link), os.path.relpath(dest / "remainder", start=link.parent))
        self.assertTrue((link.parent / os.readlink(link)).resolve().is_file())
        self.assertEqual(len(fetched), 1)
        second = setup.runtime_install.install_remainder(runtime, fetch=fetch, pin=pin, platform_name="darwin-arm64")
        self.assertEqual(len(fetched), 1)
        self.assertFalse(second["created"])
        self.assertFalse(second["rewritten"])
        self.assertEqual(os.readlink(link), os.path.relpath(dest / "remainder", start=link.parent))
        later = {"version": "0.0.1", "sha256": digest, "asset": archive.name}
        third = setup.runtime_install.install_remainder(runtime, fetch=fetch, pin=later, platform_name="darwin-arm64")
        self.assertEqual(len(fetched), 2)
        self.assertTrue(third["differs"])
        self.assertEqual(os.readlink(link), os.path.relpath(dest / "remainder", start=link.parent))

    def test_install_remainder_refuses_checksum_mismatch_without_leaving_dest(self):
        archive = self.root / "remainder_v0.0.0_test.tar.gz"
        archive.write_bytes(b"not-a-valid-remainder-archive")
        pin = {"version": "0.0.0", "sha256": "0" * 64, "asset": archive.name}

        def fetch(url, dest):
            shutil.copyfile(archive, dest)

        runtime = self.root / "runtime"
        with self.assertRaisesRegex(setup.runtime_install.InstallError, "checksum mismatch"):
            setup.runtime_install.install_remainder(runtime, fetch=fetch, pin=pin, platform_name="linux-amd64")
        self.assertFalse((runtime / ".deps/remainder/0.0.0-linux-amd64").exists())
        self.assertFalse((runtime / ".local/bin/remainder").exists())

    def test_remainder_default_fetch_honors_no_download(self):
        os.environ["SUM_REMAINDER_NO_DOWNLOAD"] = "1"
        self.addCleanup(os.environ.pop, "SUM_REMAINDER_NO_DOWNLOAD", None)
        pin = {"version": "0.2.1", "sha256": "abc", "asset": "remainder_v0.2.1_darwin_arm64.tar.gz"}
        with self.assertRaisesRegex(setup.runtime_install.InstallError, "disabled"):
            setup.runtime_install.install_remainder(self.root / "runtime", pin=pin, platform_name="darwin-arm64")

    def test_setup_guidance_uses_qualified_launch_and_explicit_core_config(self):
        output = io.StringIO()
        with patch.object(setup.sys, 'argv', ['setup.py', '--configure-only']), patch.object(setup, 'configure'), redirect_stdout(output):
            setup.main()
        text = output.getvalue()
        self.assertIn('Codex 0.166.0', text)
        self.assertIn('Tab', text)
        self.assertIn('--hc-config-home', text)
        self.assertIn('refused', text)
        self.assertNotIn('then launch codex, claude', text)

    def test_mise_and_all_python_sources_parse(self):
        tomllib.loads((ROOT / 'mise.toml').read_text())
        for parent in ('lib', 'scripts', 'tests'):
            for path in (ROOT / parent).rglob('*.py'):
                compile(path.read_text(), str(path), 'exec')

if __name__ == '__main__':
    unittest.main()
