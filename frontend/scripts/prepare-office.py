"""Install the pinned CryptPad build of Euro-Office and its WASM converter."""

import gzip
import hashlib
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path, PurePosixPath
from zipfile import ZipFile


EDITOR = (
    "onlyoffice-editor", "9.3.2+3", "onlyoffice-editor.zip",
    "181978176e443b9ff9cce127e6aa9668a4ee8e81473cb0c0c4cf0d83dea9b6636c730d40e704ebe8fdf313498a3106bafe5c4021ae937b269d05a07b7be6bbb4",
)
CONVERTER = (
    "onlyoffice-x2t-wasm", "9.3.2+4", "x2t.zip",
    "8fa965d86ef81c8615f2cc7c67eb0da270f88c573d137dcf82bb5b54d7ffacd66adb0e29f16b46a537279fc1c9ddf13ebc2b6e2f491c08cc4f266e6669763af6",
)
EDITORS = {"spreadsheeteditor": "cell", "documenteditor": "word", "presentationeditor": "slide"}
HERE = Path(__file__).resolve().parent


def archive(spec, cache):
    repository, version, filename, checksum = spec
    path = cache / filename
    if not path.exists():
        url = f"https://github.com/cryptpad/{repository}/releases/download/v{version.replace('+', '%2B')}/{filename}"
        temporary = path.with_suffix(".part")
        subprocess.run(["curl", "-fLsS", "--retry", "2", "--max-time", "420", url, "-o", temporary], check=True)
        temporary.rename(path)

    with path.open("rb") as source:
        if hashlib.file_digest(source, "sha512").hexdigest() != checksum:
            raise ValueError(f"Office archive checksum mismatch: {path}")

    return ZipFile(path)


def editor_asset(path):
    parts = PurePosixPath(path).parts
    if not parts or parts[0] not in {"web-apps", "sdkjs", "fonts"}:
        return False

    return not (
        path.endswith((".br", ".map"))
        or path.startswith("web-apps/vendor/monaco/")
        or "/help/" in path
    )


def install(output, cache):
    cache.mkdir(parents=True, exist_ok=True)
    output.parent.mkdir(parents=True, exist_ok=True)
    fingerprint = hashlib.sha256()
    for source in (Path(__file__), HERE / "office-frame.js", HERE / "office-worker.js", HERE / "office-notices.txt"):
        fingerprint.update(source.read_bytes())
    version = fingerprint.hexdigest()[:20]
    with tempfile.TemporaryDirectory(dir=output.parent) as stage:
        staging = Path(stage) / "office"
        root = staging / version
        with archive(EDITOR, cache) as bundle:
            bundle.extractall(root, [p for p in bundle.namelist() if editor_asset(p)])
        with archive(CONVERTER, cache) as bundle:
            bundle.extractall(root / "x2t", ["x2t.js", "x2t.wasm"])

        # CryptPad normally keeps APP in the parent document. Zentrale leaves
        # the iframe opaque and supplies its small, local image bridge inside it.
        for app, sdk in EDITORS.items():
            entry = root / "web-apps/apps" / app / "main/index.html"
            source = entry.read_text().replace("window.parent.APP", "window.APP")
            source = re.sub(r"^\+function registerServiceWorker\(\).*\n", "", source, flags=re.M)
            bridge = '<script src="../../../../preview-frame.js"></script>'
            entry.write_text(source.replace("<head>", "<head>" + bridge, 1))

            for sdk_file in (root / "sdkjs" / sdk).glob("sdk-all*.js"):
                source = sdk_file.read_text().replace("window.parent.APP", "window.APP")
                source = re.sub(r"window\.APP=([a-zA-Z_$]+)&&\1\.APP;", "", source)
                # Spellchecking is not needed in the read-only viewer and
                # upstream's same-origin worker cannot start in an opaque frame.
                source = source.replace(
                    "_coSpellCheckInit=function(){", "_coSpellCheckInit=function(){return;"
                )
                sdk_file.write_text(source)

            for asset in (entry, entry.with_name("app.js"), *(root / "sdkjs" / sdk).glob("sdk-all*.js")):
                compress_asset(asset)

        shutil.copyfile(HERE / "office-frame.js", root / "preview-frame.js")
        shutil.copyfile(HERE / "office-worker.js", root / "x2t/preview-worker.js")
        shutil.copyfile(HERE / "office-notices.txt", root / "NOTICE.txt")
        compress_asset(root / "x2t/x2t.js")
        compress_asset(root / "x2t/x2t.wasm")

        (staging / "VERSION").write_text(version)
        if output.exists():
            shutil.rmtree(output)
        staging.rename(output)


def compress_asset(path):
    with path.open("rb") as original, path.with_name(path.name + ".gz").open("wb") as encoded:
        with gzip.GzipFile(filename="", fileobj=encoded, mode="wb", mtime=0, compresslevel=6) as compressed:
            shutil.copyfileobj(original, compressed)


if __name__ == "__main__":
    output = Path(sys.argv[1]) if len(sys.argv) > 1 else HERE.parent / "public/office"
    if len(sys.argv) > 2:
        install(output.resolve(), Path(sys.argv[2]).resolve())
    else:
        with tempfile.TemporaryDirectory() as cache:
            install(output.resolve(), Path(cache))
