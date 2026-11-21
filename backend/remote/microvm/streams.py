"""Bounded, lossless byte streams over the host/guest shared control directory.

Each direction has one writer and one reader. Published chunks are immutable;
deletion acknowledges consumption. Backpressure bounds disk use independently
of the lifetime transfer size. No FIFO semantics across different kernels.
"""

import hmac
import os
from pathlib import Path
import struct
import time


class MailboxStream:
    CHUNK_BYTES = 65536
    QUEUED_CHUNKS = 32

    def __init__(self, directory, name, cancelled=lambda: False):
        self.directory = Path(directory)
        self.name = name
        self.sequence = 0
        self.cancelled = cancelled

    def chunk(self, sequence):
        return self.directory / (self.name + ".%016x.chunk" % sequence)

    def write(self, data):
        for offset in range(0, len(data), self.CHUNK_BYTES):
            # The reader removes each chunk only after copying its bytes.
            while (
                self.sequence >= self.QUEUED_CHUNKS
                and self.chunk(self.sequence - self.QUEUED_CHUNKS).exists()
            ):
                self.check_cancelled()
                time.sleep(0.005)

            self.check_cancelled()
            destination = self.chunk(self.sequence)
            temporary = destination.with_suffix(".new")
            temporary.write_bytes(data[offset : offset + self.CHUNK_BYTES])
            os.replace(temporary, destination)
            self.sequence += 1

    def read(self):
        """Return a chunk, None while waiting, or b'' at the end of the stream."""
        source = self.chunk(self.sequence)
        try:
            data = source.read_bytes()
        except FileNotFoundError:
            if (self.directory / (self.name + ".eof")).exists():
                return b""
            return None

        source.unlink()
        self.sequence += 1
        return data

    def close(self):
        try:
            (self.directory / (self.name + ".eof")).touch()
        except FileNotFoundError:
            pass  # A cancelled invocation may already have been removed.

    def check_cancelled(self):
        if self.cancelled() or not self.directory.exists():
            raise InterruptedError("MicroVM stream cancelled")


TERMINAL_HOST = "10.0.2.2"  # Host address on the VM's QEMU user network.
TERMINAL_HEADER = struct.Struct("!BI")
TERMINAL_CHUNK_BYTES = 65536


def receive_exact(connection, size):
    data = bytearray(size)
    remaining = memoryview(data)
    while remaining:
        count = connection.recv_into(remaining)
        if not count:
            raise EOFError("MicroVM terminal stream closed")
        remaining = remaining[count:]
    return data


def authenticate_terminal(connection, key, host):
    guest_proof = hmac.digest(key, b"zentrale-terminal-v1/guest", "sha256")
    host_proof = hmac.digest(key, b"zentrale-terminal-v1/host", "sha256")
    if not host:
        connection.sendall(guest_proof)
    expected = guest_proof if host else host_proof
    if not hmac.compare_digest(receive_exact(connection, len(expected)), expected):
        raise OSError("invalid MicroVM terminal stream authentication")
    if host:
        connection.sendall(host_proof)


def send_terminal_frame(connection, channel, data):
    connection.sendall(TERMINAL_HEADER.pack(channel, len(data)) + data)


def receive_terminal_frame(connection):
    channel, size = TERMINAL_HEADER.unpack(
        receive_exact(connection, TERMINAL_HEADER.size)
    )
    if channel not in (0, 1, 2) or size > TERMINAL_CHUNK_BYTES or (channel == 0 and size != 1):
        raise OSError("invalid MicroVM terminal stream frame")
    return channel, receive_exact(connection, size)
