import json, os, subprocess, sys, tempfile, unittest, urllib.error, urllib.request
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


class ServiceTest(unittest.TestCase):
    def setUp(self):
        self.data = tempfile.TemporaryDirectory()
        self.addCleanup(self.data.cleanup)
        self.server = None
        self.start()

    def start(self):
        self.server = subprocess.Popen([sys.executable, os.path.join(ROOT, "app.py")], stdout=subprocess.PIPE, text=True, env={**os.environ, "ITEMS_DATA_DIR": self.data.name})
        self.addCleanup(self.stop)
        self.url = self.server.stdout.readline().strip()

    def stop(self):
        if self.server and self.server.poll() is None:
            self.server.terminate(); self.server.wait(timeout=5)

    def request(self, method, path, data=None):
        request = urllib.request.Request(self.url + path, data=json.dumps(data).encode() if data is not None else None, method=method)
        try:
            with urllib.request.urlopen(request, timeout=5) as response:
                return response.status, json.loads(response.read())
        except urllib.error.HTTPError as exc:
            return exc.code, json.loads(exc.read())

    def test_health(self):
        self.assertEqual(self.request("GET", "/health"), (200, {"ok": True}))

    def test_items_empty_create_and_persist_across_restart(self):
        self.assertEqual(self.request("GET", "/items"), (200, []))
        self.assertEqual(self.request("POST", "/items", {"name": "pen"}), (201, {"id": 1, "name": "pen"}))
        self.assertEqual(self.request("POST", "/items", {})[0], 400)
        self.stop(); self.start()
        self.assertEqual(self.request("GET", "/items"), (200, [{"id": 1, "name": "pen"}]))

    def test_unknown_route(self):
        self.assertEqual(self.request("GET", "/nope")[0], 404)


if __name__ == "__main__":
    unittest.main()
