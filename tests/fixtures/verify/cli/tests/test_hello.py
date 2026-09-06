import os, subprocess, sys, unittest
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)
import hello

class HelloTest(unittest.TestCase):
    def test_greet(self):
        self.assertEqual(hello.greet("Ada"), "Hello, Ada!")
        self.assertEqual(hello.greet("Ada", shout=True), "HELLO, ADA!")

    def test_cli_prints_greeting(self):
        out = subprocess.run([sys.executable, os.path.join(ROOT, "hello.py"), "Ada"], text=True, capture_output=True, check=True).stdout
        self.assertEqual(out.strip(), "Hello, Ada!")

if __name__ == "__main__":
    unittest.main()
