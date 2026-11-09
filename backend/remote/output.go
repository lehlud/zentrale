package remote

import (
	"fmt"
	"strings"
	"sync"
)

// Keep startup context and the newest diagnostics. A compiler error or traceback
// at the end of a long command must survive the same limit as its progress log.
type OutputBuffer struct {
	mutex     sync.Mutex
	head      []byte
	tail      []byte
	tailStart int
	total     uint64
	onOutput  func(string)
}

type limitedOutput = OutputBuffer

// OutputWindow keeps raw bytes so cursors survive split UTF-8 writes.
type OutputWindow struct {
	Head  []byte `json:"head"`
	Tail  []byte `json:"tail"`
	Total uint64 `json:"total"`
}

func (output *OutputBuffer) Window() OutputWindow {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return OutputWindow{
		Head:  append([]byte(nil), output.head...),
		Tail:  append(append([]byte(nil), output.tail[output.tailStart:]...), output.tail[:output.tailStart]...),
		Total: output.total,
	}
}

func (output *OutputBuffer) Write(chunk []byte) (int, error) {
	length := len(chunk)
	output.mutex.Lock()
	output.total += uint64(length)
	const half = maximumOutputBytes / 2
	headBytes := min(half-len(output.head), length)
	output.head = append(output.head, chunk[:headBytes]...)
	chunk = chunk[headBytes:]
	if len(chunk) >= half {
		output.tail = append(output.tail[:0], chunk[len(chunk)-half:]...)
		output.tailStart = 0
	} else {
		fill := min(half-len(output.tail), len(chunk))
		output.tail = append(output.tail, chunk[:fill]...)
		chunk = chunk[fill:]
		if len(chunk) > 0 {
			// A circular tail avoids copying half a megabyte for tiny writes.
			first := copy(output.tail[output.tailStart:], chunk)
			copy(output.tail, chunk[first:])
			output.tailStart = (output.tailStart + len(chunk)) % half
		}
	}
	var snapshot string
	if output.onOutput != nil {
		snapshot = output.snapshotLocked()
	}
	output.mutex.Unlock()
	if output.onOutput != nil {
		output.onOutput(snapshot)
	}
	return length, nil
}

func (output *OutputBuffer) snapshotLocked() string {
	omitted := output.total - uint64(len(output.head)+len(output.tail))
	tail := string(output.tail[output.tailStart:]) + string(output.tail[:output.tailStart])
	if omitted == 0 {
		return strings.ToValidUTF8(string(output.head)+tail, "�")
	}
	return strings.ToValidUTF8(string(output.head), "�") +
		fmt.Sprintf("\n[output truncated: %d bytes omitted]\n", omitted) +
		strings.ToValidUTF8(tail, "�")
}

func (output *OutputBuffer) String() string {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return output.snapshotLocked()
}
