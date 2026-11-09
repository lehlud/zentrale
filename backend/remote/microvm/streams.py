"""Bounded, lossless byte streams over the host/guest shared control directory.

Each direction has one writer and one reader. Published chunks are immutable;
deletion acknowledges consumption. Backpressure bounds disk use independently
of the lifetime transfer size. No FIFO semantics across different kernels.
"""

import os
from pathlib import Path
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
            while self.sequence >= self.QUEUED_CHUNKS and self.chunk(
                self.sequence - self.QUEUED_CHUNKS
            ).exists():
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
