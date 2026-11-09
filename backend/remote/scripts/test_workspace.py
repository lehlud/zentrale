import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock

specification = importlib.util.spec_from_file_location(
    "workspace", Path(__file__).with_name("workspace.py")
)
workspace = importlib.util.module_from_spec(specification)
specification.loader.exec_module(workspace)


class PersistentWorkspaceTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "persistent projects"
        self.configuration = {
            "projectId": "project-id",
            "root": str(self.root),
            "token": "private-receipt",
            "initialized": False,
        }

    def test_reuses_data_and_recovers_an_interrupted_initialization(self):
        first = workspace.prepare(self.configuration)
        directory = Path(first["directory"])
        (directory / "checkpoint").write_text("saved")
        self.assertEqual(first, workspace.prepare(self.configuration))
        self.configuration["initialized"] = True
        self.assertEqual(first, workspace.prepare(self.configuration))
        self.assertEqual((directory / "checkpoint").read_text(), "saved")

    def test_missing_shared_storage_is_never_recreated(self):
        workspace.prepare(self.configuration)
        missing = self.root.parent / "unmounted share"
        self.configuration.update(root=str(missing), initialized=True)
        with self.assertRaisesRegex(ValueError, "same persistent storage"):
            workspace.prepare(self.configuration)
        self.assertFalse(missing.exists())

    def test_concurrent_initialization_keeps_the_winning_receipt(self):
        marker = self.root / ".zentrale-workspaces" / "project-project-id.json"

        def publish_from_another_connection(_source, _destination):
            marker.write_text(json.dumps({"token": self.configuration["token"]}))
            raise FileExistsError("another connection already published the receipt")

        with mock.patch.object(
            workspace.os, "link", side_effect=publish_from_another_connection
        ):
            result = workspace.prepare(self.configuration)
        self.assertTrue(Path(result["directory"]).is_dir())
        self.assertEqual(
            json.loads(marker.read_text()), {"token": self.configuration["token"]}
        )
        self.assertEqual(list(marker.parent.iterdir()), [marker])

    def test_another_installation_cannot_claim_the_directory(self):
        receipt = workspace.prepare(self.configuration)
        self.configuration["token"] = "some-other-installation"
        with self.assertRaisesRegex(ValueError, "different installation"):
            workspace.prepare(self.configuration)
        self.assertTrue(Path(receipt["directory"]).is_dir())

    def test_existing_files_are_never_adopted_without_a_receipt(self):
        directory = self.root / "project-project-id"
        directory.mkdir(parents=True)
        (directory / "important.txt").write_text("keep")
        with self.assertRaisesRegex(ValueError, "non-empty"):
            workspace.prepare(self.configuration)
        self.assertEqual((directory / "important.txt").read_text(), "keep")

    def test_symlinks_cannot_redirect_the_project_directory(self):
        self.root.mkdir()
        (self.root / "project-project-id").symlink_to(self.root.parent)
        with self.assertRaisesRegex(ValueError, "symlinks"):
            workspace.prepare(self.configuration)

    def test_home_is_expanded_on_the_remote_and_pinned_locations_are_retained(self):
        self.configuration["root"] = "~/.local/share/zentrale/projects"
        with mock.patch.object(
            workspace.os.path, "expanduser", return_value=str(self.root)
        ):
            first = workspace.prepare(self.configuration)
        self.configuration.update(initialized=True, directory=first["directory"])
        with mock.patch.object(
            workspace.os.path,
            "expanduser",
            side_effect=AssertionError("pinned path changed"),
        ):
            self.assertEqual(first, workspace.prepare(self.configuration))


if __name__ == "__main__":
    unittest.main()
