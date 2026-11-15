import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import struct
import tempfile
import unittest

import huggingface


def framed(content, size=None, trailer=None):
    size = len(content) if size is None else size
    trailer = trailer or {
        "size": len(content),
        "sha256": hashlib.sha256(content).hexdigest(),
    }
    header = b"ZHF1\n" + json.dumps({"size": size}).encode() + b"\n"
    frames = []

    for offset in range(0, len(content), 1024 * 1024):
        chunk = content[offset:offset + 1024 * 1024]
        frames.append(struct.pack(">I", len(chunk)) + chunk)

    return header + b"".join(frames) + b"\x00" * 4 + json.dumps(trailer).encode() + b"\n"


@contextlib.contextmanager
def workspace():
    previous = os.getcwd()

    with tempfile.TemporaryDirectory() as directory:
        os.chdir(directory)

        try:
            yield Path(directory)
        finally:
            os.chdir(previous)


class DatasetWriterTests(unittest.TestCase):
    def options(self, destination="data/train.parquet", maximum=100):
        return {"destination": destination, "maxBytes": maximum}

    def assert_no_staging(self, root):
        self.assertEqual(list(root.rglob(".zentrale-hf-*")), [])

    def test_verified_binary_file_and_private_permissions(self):
        content = b"\x00parquet\xff\n"

        with workspace() as root:
            options = self.options()
            options["sha256"] = hashlib.sha256(content).hexdigest().upper()
            result = huggingface.receive(options, io.BytesIO(framed(content)))

            self.assertEqual((root / options["destination"]).read_bytes(), content)
            self.assertEqual(result["bytes"], len(content))
            self.assertEqual(result["sha256"], options["sha256"].lower())
            self.assertEqual((root / options["destination"]).stat().st_mode & 0o777, 0o600)
            self.assert_no_staging(root)

    def test_empty_and_unknown_length_files(self):
        for content, size in ((b"", 0), (b"unknown", -1)):
            with self.subTest(size=size), workspace() as root:
                huggingface.receive(self.options(), io.BytesIO(framed(content, size=size)))
                self.assertEqual((root / "data/train.parquet").read_bytes(), content)
                self.assert_no_staging(root)

    def test_multiple_bounded_frames(self):
        content = b"x" * (1024 * 1024 + 3)

        with workspace() as root:
            options = self.options(maximum=len(content))
            result = huggingface.receive(options, io.BytesIO(framed(content)))
            self.assertEqual(result["bytes"], len(content))
            self.assertEqual((root / options["destination"]).read_bytes(), content)

    def test_traversal_absolute_controls_and_symlink_parents_are_rejected(self):
        invalid_destinations = (
            "../escape", "/tmp/escape", "a/../escape", "./escape",
            "a//b", "a\\b", "bad\nname", "bad\x85name",
        )

        for destination in invalid_destinations:
            with self.subTest(destination=destination), workspace() as root:
                with self.assertRaises(huggingface.DownloadError):
                    huggingface.receive(self.options(destination), io.BytesIO(framed(b"data")))

                self.assert_no_staging(root)

        with workspace() as root, tempfile.TemporaryDirectory() as outside:
            (root / "linked").symlink_to(outside, target_is_directory=True)

            with self.assertRaises(OSError):
                huggingface.receive(self.options("linked/file"), io.BytesIO(framed(b"data")))

            self.assertEqual(list(Path(outside).iterdir()), [])
            self.assert_no_staging(root)

    def test_existing_files_and_dangling_symlinks_are_never_overwritten(self):
        for symlink in (False, True):
            with self.subTest(symlink=symlink), workspace() as root:
                target = root / "existing"

                if symlink:
                    target.symlink_to("missing")
                else:
                    target.write_bytes(b"preserved")

                with self.assertRaises(huggingface.DownloadError):
                    huggingface.receive(self.options("existing"), io.BytesIO(framed(b"replacement")))

                if symlink:
                    self.assertTrue(target.is_symlink())
                    self.assertFalse((root / "missing").exists())
                else:
                    self.assertEqual(target.read_bytes(), b"preserved")

                self.assert_no_staging(root)

    def test_no_partial_destination_for_bad_or_incomplete_streams(self):
        content = b"dataset"
        valid = framed(content)
        bad_streams = (
            valid[:2],
            valid[:-1],
            valid[:30],
            valid + b"unexpected extra bytes",
            framed(content, size=99),
            framed(content, trailer={"size": 7, "sha256": "0" * 64}),
            framed(content, trailer={
                "size": 1,
                "sha256": hashlib.sha256(content).hexdigest(),
            }),
            b"ZHF1\n{\"size\":-1}\n" + struct.pack(">I", 1024 * 1024 + 1),
        )

        for stream in bad_streams:
            with self.subTest(stream=stream[:30]), workspace() as root:
                with self.assertRaises(huggingface.DownloadError):
                    huggingface.receive(self.options(), io.BytesIO(stream))

                self.assertFalse((root / "data/train.parquet").exists())
                self.assert_no_staging(root)

    def test_known_and_streamed_byte_limits(self):
        for size in (8, -1):
            with self.subTest(size=size), workspace() as root:
                with self.assertRaises(huggingface.DownloadError):
                    huggingface.receive(self.options(maximum=7), io.BytesIO(framed(b"12345678", size=size)))

                self.assertFalse((root / "data/train.parquet").exists())
                self.assert_no_staging(root)

    def test_expected_digest_is_required_before_committing(self):
        with workspace() as root:
            options = self.options()
            options["sha256"] = "0" * 64

            with self.assertRaises(huggingface.DownloadError):
                huggingface.receive(options, io.BytesIO(framed(b"content")))

            self.assertFalse((root / options["destination"]).exists())
            self.assert_no_staging(root)


if __name__ == "__main__":
    unittest.main()
