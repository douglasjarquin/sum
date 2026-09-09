import importlib
from pathlib import Path
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
fixture = importlib.import_module("tests.test_evidence_skill")

print(f"selected_browser={fixture.BROWSER} node_supported={fixture.NODE_OK}", flush=True)
if fixture.BROWSER:
    version = subprocess.run([fixture.BROWSER, "--version"], capture_output=True, text=True, timeout=30)
    print(f"browser_version_exit={version.returncode} stdout={version.stdout.strip()} stderr={version.stderr.strip()}", flush=True)
case = fixture.EvidenceLab("test_seeded_browser_bug_is_red_at_base_and_green_at_candidate_with_screenshots_and_playable_video")
case.setUp()
try:
    case.test_seeded_browser_bug_is_red_at_base_and_green_at_candidate_with_screenshots_and_playable_video()
finally:
    for diagnostic in case.root.rglob("browser-diagnostics.log"):
        print(f"browser_diagnostic={diagnostic.relative_to(case.root)}", flush=True)
        print(diagnostic.read_text(errors="replace")[-8000:], flush=True)
    case.tearDown()
    case.doCleanups()
    print(f"fixture_removed={not case.root.exists()}", flush=True)
