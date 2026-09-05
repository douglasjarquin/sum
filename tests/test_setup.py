import importlib.util
import json
from pathlib import Path
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
        with self.assertRaises(RuntimeError): setup.configure(self.root)
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
        with self.assertRaises(RuntimeError): setup.codex_config(path, '/new/bin')

    def test_all_skill_links_resolve(self):
        for parent in (ROOT / '.agents/skills', ROOT / '.claude/skills'):
            for name in ('dispatch', 'worker', 'rundown', 'delivery'):
                self.assertTrue((parent / ('sum-' + name) / 'SKILL.md').is_file())
        self.assertEqual((ROOT / 'CLAUDE.md').resolve(), ROOT / 'AGENTS.md')

    def test_mise_and_all_python_sources_parse(self):
        tomllib.loads((ROOT / 'mise.toml').read_text())
        for parent in ('lib', 'scripts', 'tests'):
            for path in (ROOT / parent).rglob('*.py'):
                compile(path.read_text(), str(path), 'exec')

if __name__ == '__main__': unittest.main()
