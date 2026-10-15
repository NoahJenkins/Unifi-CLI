"""Exercise the actual inline canary validators without a guest or controller."""
import ast
import pathlib
import stat
import textwrap
import types
import unittest
from unittest.mock import Mock, patch


WORKFLOW = pathlib.Path(__file__).resolve().parents[1] / '.github/workflows/dell-ci-canary.yml'
SOURCE = textwrap.dedent(WORKFLOW.read_text().split("python3 - <<'PYTHON'\n", 1)[1].split('\n          PYTHON', 1)[0])


class GuestIdentityTests(unittest.TestCase):
    def setUp(self):
        # Compile the workflow's own functions with assertions stripped as well.
        tree = ast.parse(SOURCE)
        tree.body = [node for node in tree.body if isinstance(node, (ast.Import, ast.FunctionDef))]
        self.validators = {}
        exec(compile(tree, str(WORKFLOW), 'exec', optimize=2), self.validators)

    def marker(self, mode=stat.S_IFREG | 0o444, uid=0, writable=False, content='ubuntu24\n'):
        path = Mock()
        path.lstat.return_value = types.SimpleNamespace(st_mode=mode, st_uid=uid)
        path.read_text.return_value = content
        with patch('os.access', return_value=writable):
            return self.validators['verify_profile'](path, 'ubuntu24')

    def port(self, mode=stat.S_IFCHR | 0o600, uid=1000):
        path = Mock()
        # stat follows the intentionally symlinked udev path to its target.
        path.stat.return_value = types.SimpleNamespace(st_mode=mode, st_uid=uid)
        with patch('os.geteuid', return_value=1000):
            self.validators['verify_port'](path)
        path.stat.assert_called_once_with()

    def test_protected_profile(self):
        self.assertEqual(self.marker(), 'ubuntu24')

    def test_profile_rejects_runner_owner(self):
        with self.assertRaises(SystemExit):
            self.marker(uid=1000)

    def test_profile_rejects_runner_write_access(self):
        with self.assertRaises(SystemExit):
            self.marker(mode=stat.S_IFREG | 0o666, writable=True)

    def test_profile_rejects_symlink_and_directory(self):
        for kind in (stat.S_IFLNK, stat.S_IFDIR):
            with self.subTest(kind=kind), self.assertRaises(SystemExit):
                self.marker(mode=kind | 0o555)

    def test_profile_rejects_wrong_contents(self):
        with self.assertRaises(SystemExit):
            self.marker(content='ubuntu22')

    def test_resolved_character_device(self):
        self.port()

    def test_port_rejects_regular_file_target(self):
        with self.assertRaises(SystemExit):
            self.port(mode=stat.S_IFREG | 0o600)

    def test_port_rejects_wrong_owner_and_mode(self):
        with self.assertRaises(SystemExit):
            self.port(uid=0)
        with self.assertRaises(SystemExit):
            self.port(mode=stat.S_IFCHR | 0o660)


if __name__ == '__main__':
    unittest.main()
