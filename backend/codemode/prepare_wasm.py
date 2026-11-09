"""Constrain the pinned upstream WASM memory before embedding it.

Usage: python3 prepare_wasm.py /path/to/github.com/fastschema/qjs@v0.0.6/qjs.wasm
The upstream module has no memory maximum. Keep its instructions unchanged and
add a 64 MiB maximum to the single memory section, enforced by WebAssembly.
"""

from pathlib import Path
import hashlib
import sys


def unsigned(value):
    encoded = bytearray()
    while value >= 128:
        encoded.append((value & 127) | 128)
        value >>= 7
    encoded.append(value)
    return bytes(encoded)


def constrained_module(data):
    def read_integer(offset):
        value, shift = 0, 0
        while True:
            part = data[offset]
            offset += 1
            value |= (part & 127) << shift
            if part < 128:
                return value, offset
            shift += 7

    offset = 8
    while offset < len(data):
        section = data[offset]
        length, start = read_integer(offset + 1)
        if section == 5:
            if data[start:start + length] != bytes([1, 0, 3]):
                raise ValueError("upstream memory layout changed; review before upgrading")
            memory = bytes([1, 1, 3]) + unsigned(1024)
            return data[:offset] + bytes([5]) + unsigned(len(memory)) + memory + data[start + length:]
        offset = start + length
    raise ValueError("module has no memory section")


if __name__ == "__main__":
    original = Path(sys.argv[1]).read_bytes()
    expected = Path(__file__).with_name("qjs.upstream.sha256").read_text().strip()
    if hashlib.sha256(original).hexdigest() != expected:
        raise ValueError("unexpected upstream binary; review source and update the pinned checksum")
    Path(__file__).with_name("qjs.wasm").write_bytes(constrained_module(original))
