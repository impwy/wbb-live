import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

from harness_lifecycle import run_owned


class LifecycleTest(unittest.TestCase):
    def test_timeout_kills_child_and_grandchild(self):
        with tempfile.TemporaryDirectory() as folder:
            marker = Path(folder) / 'late-marker'
            code = ('import subprocess,sys,time;'
                    'subprocess.Popen([sys.executable,"-c",'
                    '"import time,pathlib; time.sleep(.7); pathlib.Path(sys.argv[1]).write_text(\'late\')",'
                    'sys.argv[1]]); time.sleep(3)')
            with self.assertRaises(TimeoutError):
                run_owned([sys.executable, '-c', code, str(marker)], cwd=folder,
                          timeout=.15, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            time.sleep(.8)
            self.assertFalse(marker.exists(), 'grandchild survived command timeout')


if __name__ == '__main__':
    unittest.main()
