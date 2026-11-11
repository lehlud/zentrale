import pathlib
import tempfile
import unittest

from canonical_ubuntu_mirror import canonicalize


class TestCanonicalMirror(unittest.TestCase):
    def test_only_exact_archive_hostname_changes(self):
        original = (
            "URIs: https://azure.archive.ubuntu.com/ubuntu/\n"
            "Suites: noble noble-updates\nComponents: main universe\n"
            "Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n\n"
            "URIs: https://security.ubuntu.com/ubuntu/\nSuites: noble-security\n"
            "# https://not-azure.archive.ubuntu.com/ubuntu/\n"
        )
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "ubuntu.sources"
            path.write_text(original)
            canonicalize(path)
            expected = original.replace("https://azure.archive.ubuntu.com/", "https://archive.ubuntu.com/")
            self.assertEqual(path.read_text(), expected)
            canonicalize(path)
            self.assertEqual(path.read_text(), expected)
            canonicalize(path.with_name("missing.list"))


if __name__ == "__main__":
    unittest.main()
