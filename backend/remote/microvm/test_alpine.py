"""Resolve moving Alpine packages from a signed snapshot, including mirror races."""

import base64
import gzip
import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import urllib.error

from test_runner import runner


# Public, test-only RSA key; never used by the production trust store.
TEST_N = (
    "d0d051075efde0327814381304fa61ef663b2ddbf6eedeba77c099f3948ca720"
    "401e1ba34161573daed72fd0660a1314bb52e511bf533e81197c9ac984c63b0a"
    "6f18702df96419645a3ce2232910bf78e3a4075a466d741461d3d3405e816418"
    "713db50cd2b7c50c49882e19ad377742ca15801e1f90663be693b48215066499"
    "6e3b28e2d63d03a49b1018a0d037937abf519396fa9495277f170861906a7596"
    "c60239627acffee8c9891f8d2d9858a4b49f4baa9e1ad86e3087628519fe82ad"
    "450074e983dc7ac7dd3eccfab6ca73de6c99d6fb75ca861b74d93868d5fc2a90"
    "268c178d31e1d1d054f8ca77e43cdebeacf8916c555a1821bc7ccc0bef62fbd9"
)
TEST_D = (
    "09260cb172a5fe314d35859215166b3d5e909a87c29fe0939c34e209dbfe4aec"
    "377844e462deed959442caa37f74fd8a5ba806c53f9eaeb9b54627a4a09e96c3"
    "9eee712bc7d0c23eb552bee2fa24f3b3f6354a3a82fa14ab37f0b1ec10b7ebe2"
    "18eeb284a74ec9e1549a14d051d880ef24da204cdc3bd55ed67c0387ecd28f8c"
    "5d07fc1d6c0de1f26386a39dcea30ab04c87074c7e45fb2aca9c7be3d294551e"
    "d862f7c6eb718b8eb67f42f3a75085e91413e424a7ccb987d7cd10df680ea9dc"
    "904abf7e60c8d083a87ffc4d0222065f6a4e95aaf45b98a4a359334a64b3d31b"
    "0098b6fbd1fa08b49232da9c27edef2d503e5221192755dc94ec105ded5185a1"
)
KEY_NAME = "test.rsa.pub"


def archive(files, terminated=False, pax=False):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w", format=tarfile.PAX_FORMAT if pax else tarfile.USTAR_FORMAT) as bundle:
        for name, content in files.items():
            member = tarfile.TarInfo(name)
            member.size = len(content)
            if pax:
                member.pax_headers = {"ctime": "0", "atime": "0"}
            bundle.addfile(member, io.BytesIO(content))
        length = bundle.offset
    return gzip.compress(output.getvalue() if terminated else output.getvalue()[:length], mtime=0)


def signed_index(index, malformed_padding=False):
    data = archive({"APKINDEX": index.encode()}, terminated=True)
    digest = bytes.fromhex("3021300906052b0e03021a05000414") + hashlib.sha1(data).digest()
    padding = b"\xff" * (256 - len(digest) - 3)
    if malformed_padding:
        padding = b"\x00" + padding[1:]
    encoded = b"\x00\x01" + padding + b"\x00" + digest
    signature = pow(int.from_bytes(encoded, "big"), int(TEST_D, 16), int(TEST_N, 16)).to_bytes(256, "big")
    return archive({".SIGN.RSA." + KEY_NAME: signature}) + data


def package(name="busybox-static", version="1.2.3-r0", content=b"busybox"):
    payload = archive({"bin/busybox.static": content}, terminated=True)
    info = ("pkgname = " + name + "\npkgver = " + version + "\narch = x86_64\n"
            + "datahash = " + hashlib.sha256(payload).hexdigest() + "\n").encode()
    control = archive({".PKGINFO": info})
    data = archive({".SIGN.RSA." + KEY_NAME: b"signature"}, pax=True) + control + payload
    checksum = "Q1" + base64.b64encode(hashlib.sha1(control).digest()).decode()
    return data, {"name": name, "version": version, "arch": "x86_64",
                  "size": len(data), "checksum": checksum}


class AlpineRepositoryTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.cache = Path(directory.name)
        self.settings = {"repository": "https://example.test/alpine/v3.22/main/x86_64",
                         "keys": {KEY_NAME: {"n": TEST_N, "e": 65537}}}
        self.release = {
            "version": "test", "alpine": self.settings, "runtime": {},
            "kernel": {"package": "linux-virt", "member": "boot/vmlinuz-virt"},
            "busybox": {"package": "busybox-static", "member": "bin/busybox.static"},
            "nvidia": {"headers": {"package": "linux-virt-dev"}, "musl": {"package": "musl"}},
        }

    def index(self, version="6.12.117-r0", headers=None):
        records = []
        for name, selected in (("linux-virt", version), ("linux-virt-dev", headers or version),
                               ("busybox-static", "1.2.3-r0"), ("musl", "1.2.3-r0")):
            _, entry = package(name, selected)
            records.append("\n".join(("P:" + name, "V:" + selected, "A:x86_64",
                                      "S:" + str(entry["size"]), "C:" + entry["checksum"])))
        return "\n\n".join(records) + "\n"

    def test_signed_index_drives_all_urls_and_matching_kernel_versions(self):
        snapshot = signed_index(self.index())
        original = json.dumps(self.release, sort_keys=True)
        with patch.object(runner.urllib.request, "urlopen", return_value=io.BytesIO(snapshot)) as fetch:
            resolved = runner.resolve_alpine_release(self.cache, self.release)
        self.assertEqual(fetch.call_args.args[0].full_url, self.settings["repository"] + "/APKINDEX.tar.gz")
        self.assertEqual(resolved["kernel"]["version"], "6.12.117-0-virt")
        self.assertEqual(resolved["nvidia"]["headers"]["version"], "6.12.117-0-virt")
        self.assertEqual(resolved["kernel"]["config"], "boot/config-6.12.117-0-virt")
        self.assertTrue(resolved["kernel"]["url"].endswith("/linux-virt-6.12.117-r0.apk"))
        self.assertTrue(resolved["nvidia"]["headers"]["url"].endswith("/linux-virt-dev-6.12.117-r0.apk"))
        self.assertEqual(json.dumps(self.release, sort_keys=True), original)

    def test_cached_signed_index_is_reused_without_network(self):
        snapshot = signed_index(self.index())
        with patch.object(runner.urllib.request, "urlopen", return_value=io.BytesIO(snapshot)):
            first = runner.resolve_alpine_release(self.cache, self.release)
        with patch.object(runner.urllib.request, "urlopen", side_effect=AssertionError("network used")):
            self.assertEqual(runner.resolve_alpine_release(self.cache, self.release), first)

    def test_refresh_replaces_snapshot_and_trust_store_change_invalidates_cache(self):
        with patch.object(runner.urllib.request, "urlopen", side_effect=[
            io.BytesIO(signed_index(self.index())),
            io.BytesIO(signed_index(self.index("6.12.118-r1"))),
        ]) as fetch:
            runner.resolve_alpine_release(self.cache, self.release)
            resolved = runner.resolve_alpine_release(self.cache, self.release, refresh=True)
        self.assertEqual(fetch.call_count, 2)
        self.assertEqual(resolved["kernel"]["version"], "6.12.118-1-virt")
        changed = json.loads(json.dumps(self.settings))
        changed["keys"]["new.rsa.pub"] = changed["keys"][KEY_NAME]
        with patch.object(runner.urllib.request, "urlopen", return_value=io.BytesIO(signed_index(self.index()))) as fetch:
            runner.alpine_index(self.cache, changed)
            fetch.assert_called_once()

    def test_tampering_untrusted_signers_trailing_data_and_bad_padding_are_rejected(self):
        valid = signed_index(self.index())
        _, _, index_stream = runner.gzip_member(valid, 65536)
        unknown = archive({".SIGN.RSA.unknown.rsa.pub": b"unknown"}) + index_stream
        # Recompressing edited metadata also breaks the signature.
        signatures, _, _ = runner.gzip_member(valid, 65536)
        changed = signatures + archive({"APKINDEX": self.index("6.12.999-r0").encode()}, True)
        for data in (unknown, changed, valid + b"trailing", signed_index(self.index(), True)):
            with self.subTest(data=data[:16]), self.assertRaises(RuntimeError):
                runner.verified_alpine_index(data, self.settings)

    def test_invalid_snapshot_is_never_published(self):
        with patch.object(runner.urllib.request, "urlopen", return_value=io.BytesIO(signed_index(self.index(), True))):
            with self.assertRaisesRegex(RuntimeError, "signature"):
                runner.resolve_alpine_release(self.cache, self.release)
        self.assertEqual(list(self.cache.iterdir()), [])

    def test_index_compression_and_download_limits(self):
        with self.assertRaisesRegex(RuntimeError, "size limit"):
            runner.gzip_member(gzip.compress(b"x" * 100), 10)
        with self.assertRaisesRegex(RuntimeError, "truncated"):
            runner.gzip_member(gzip.compress(b"content")[:-5], 100)
        with patch.object(runner.urllib.request, "urlopen", return_value=io.BytesIO(b"x" * (4*1024*1024+1))):
            with self.assertRaisesRegex(RuntimeError, "download limit"):
                runner.alpine_index(self.cache, self.settings)

    def test_mismatching_headers_missing_packages_unsafe_versions_and_duplicate_names_fail(self):
        valid = self.index()
        for text in (self.index(headers="6.12.116-r0"), valid.replace("P:linux-virt\n", "P:missing\n"),
                     valid.replace("V:6.12.117-r0", "V:../../unsafe-r0"), valid + "\n" + valid,
                     valid.replace("A:x86_64", "A:aarch64"), valid.replace("C:Q1", "C:Q2")):
            with patch.object(runner, "alpine_index", return_value=text), self.assertRaises(RuntimeError):
                runner.resolve_alpine_release(self.cache, self.release)

    def test_package_matches_authenticated_control_and_data_hashes(self):
        data, apk = package()
        artifact = {"url": "https://example.test/current.apk", "apk": apk}
        with patch.object(runner.urllib.request, "urlopen", return_value=io.BytesIO(data)):
            self.assertEqual(runner.download(artifact), archive({"bin/busybox.static": b"busybox"}, terminated=True))
        for key, value in (("checksum", "Q1" + "A" * 27 + "="), ("name", "wrong"),
                           ("version", "wrong"), ("arch", "aarch64"), ("size", len(data)+1)):
            with self.subTest(key=key), self.assertRaises(RuntimeError):
                runner.verify_alpine_package(data, {"apk": dict(apk, **{key: value})})
        with self.assertRaisesRegex(RuntimeError, "data SHA-256"):
            runner.verify_alpine_package(data[:-1] + bytes([data[-1] ^ 1]), artifact)
        _, altered = package(content=b"changed data")
        with self.assertRaises(RuntimeError):
            runner.verify_alpine_package(data, {"apk": altered})

    def test_only_missing_dynamic_packages_request_an_index_refresh(self):
        for code in (404, 410, 403, 500):
            for dynamic in (True, False):
                artifact = {"url": "https://example.test/file", "sha256": "0" * 64}
                if dynamic:
                    artifact["apk"] = {}
                error = urllib.error.HTTPError(artifact["url"], code, "failed", {}, None)
                self.addCleanup(error.close)
                expected = runner.AlpinePackageUnavailable if dynamic and code in (404, 410) else urllib.error.HTTPError
                with patch.object(runner.urllib.request, "urlopen", side_effect=error), self.assertRaises(expected):
                    runner.download(artifact)

    def test_unsigned_signature_stream_cannot_inject_files_before_verified_payload(self):
        data, apk = package()
        _, _, authenticated = runner.gzip_member(data, 65536)
        malicious = archive({"lib/modules/injected.ko": b"unverified"}) + authenticated
        payload = runner.verify_alpine_package(malicious, {"apk": dict(apk, size=len(malicious))})
        with tarfile.open(fileobj=io.BytesIO(payload), mode="r:gz") as bundle:
            self.assertEqual(bundle.getnames(), ["bin/busybox.static"])

    def test_package_disappearing_during_setup_refreshes_once_and_keeps_kernel_headers_paired(self):
        with (
            patch.object(runner, "alpine_index", side_effect=[self.index(), self.index("6.12.118-r0")]) as index,
            patch.object(runner, "install_kernel", side_effect=[self.cache / "old", self.cache / "new"]) as kernel,
            patch.object(runner, "install_runtime", side_effect=[runner.AlpinePackageUnavailable(), "runtime"]),
            patch.object(runner, "provision_base", return_value="base") as provision,
        ):
            config = {"release": self.release}
            self.assertEqual(runner.ensure_ready(self.cache, config, None), ("runtime", "base", self.cache / "new"))
        self.assertEqual(index.call_count, 2)
        self.assertTrue(index.call_args.args[2])
        self.assertEqual(kernel.call_args.args[1]["version"], "6.12.118-0-virt")
        self.assertEqual(config["release"]["nvidia"]["headers"]["version"], "6.12.118-0-virt")
        provision.assert_called_once()

    def test_repeated_disappearance_or_checksum_failure_does_not_retry_forever(self):
        for error, expected_count in ((runner.AlpinePackageUnavailable(), 2), (RuntimeError("SHA-256"), 1)):
            with (
                patch.object(runner, "alpine_index", return_value=self.index()) as index,
                patch.object(runner, "install_kernel", side_effect=error),
                patch.object(runner, "install_runtime") as runtime,
                self.assertRaises(type(error)),
            ):
                runner.ensure_ready(self.cache, {"release": self.release}, None)
            self.assertEqual(index.call_count, expected_count)
            runtime.assert_not_called()

    def test_changed_busybox_uses_a_new_runtime_directory_and_preserves_old_install(self):
        import zipfile
        wheel = io.BytesIO()
        with zipfile.ZipFile(wheel, "w") as bundle:
            bundle.writestr("virtme/guest/virtme-init", "fixture")
        data, apk = package()
        release = dict(self.release, busybox=dict(self.release["busybox"], apk=apk))
        with patch.object(runner, "download", side_effect=[wheel.getvalue(), data, wheel.getvalue(), data]):
            old = runner.install_runtime(self.cache, release)
            new_release = json.loads(json.dumps(release))
            new_release["busybox"]["apk"]["version"] = "new"
            new = runner.install_runtime(self.cache, new_release)
        self.assertNotEqual(old, new)
        self.assertEqual((old / "busybox").read_bytes(), b"busybox")
        self.assertEqual((new / "busybox").read_bytes(), b"busybox")


if __name__ == "__main__":
    unittest.main()
