import importlib.util
import json
from pathlib import Path
import socket
import unittest

spec = importlib.util.spec_from_file_location("hc_fixture", Path(__file__).parent / "fixtures/hc.py")
hc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hc)

class NativeFixtureTests(unittest.TestCase):
    def call(self, core, method, **params):
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(2)
            connection.connect(str(core.socket))
            connection.sendall((json.dumps(dict(jsonrpc="2.0", id=1, method=method, params=params)) + "\n").encode())
            with connection.makefile("rb") as stream:
                line = stream.readline()
                return json.loads(line) if line else None

    def test_flat_launch_blocked_and_uncertain_send(self):
        with hc.Core() as core:
            self.assertEqual(self.call(core, "hello")["result"]["version"], "0.1.14")
            result = self.call(core, "terminal.run", argv=["fake"], labels={"sum.task": "one"})["result"]
            self.assertEqual(result["address"], "t1")
            self.assertNotIn("terminal", result)
            self.assertIsNone(result["status"])
            core.set_status("t1", "blocked")
            self.assertEqual(self.call(core, "terminal.send", terminal="t1", text="no")["error"]["data"]["kind"], "blocked")
            self.assertEqual(core.inputs["t1"], [])
            core.set_status("t1", "done")
            core.queue_fault("terminal.send", lose_response=True)
            self.assertIsNone(self.call(core, "terminal.send", terminal="t1", text="once"))
            self.assertEqual(core.inputs["t1"], ["once"])

    def test_first_message_and_outliving_child(self):
        with hc.Core() as core:
            core.queue_fault("terminal.run", first_message={"sent": False, "reason": "blocked", "started": None, "turn": None})
            parent = self.call(core, "terminal.run", argv=["fake"])["result"]
            self.assertFalse(parent["first_message"]["sent"])
            child = self.call(core, "terminal.run", argv=["fake"], parent=parent["address"], outlive_parent=True)["result"]
            core.exit(parent["address"])
            self.assertEqual(core.terminals[child["address"]]["state"], "live")
            self.assertIsNone(core.terminals[parent["address"]]["foreground"])

if __name__ == "__main__":
    unittest.main()
