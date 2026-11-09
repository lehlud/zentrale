package remote

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestCommandOutputKeepsLateFailureAndStartupContext(t *testing.T) {
	startup := "starting training\n"
	failure := "\nTraceback: final checkpoint could not be saved\n"
	full := startup + strings.Repeat("progress\n", maximumOutputBytes/4) + failure

	for _, chunkSize := range []int{1, 8192, len(full)} {
		t.Run(fmt.Sprint(chunkSize), func(t *testing.T) {
			var finalSnapshot string
			output := &limitedOutput{}

			// Avoid materializing a 1 MiB streaming snapshot for every byte.
			if chunkSize > 1 {
				output.onOutput = func(snapshot string) { finalSnapshot = snapshot }
			}

			for offset := 0; offset < len(full); offset += chunkSize {
				chunk := []byte(full[offset:min(offset+chunkSize, len(full))])
				if count, err := output.Write(chunk); err != nil || count != len(chunk) {
					t.Fatal(count, err)
				}
			}

			result := output.String()
			marker := fmt.Sprintf("%d bytes omitted", len(full)-maximumOutputBytes)
			if !strings.HasPrefix(result, startup) ||
				!strings.HasSuffix(result, failure) ||
				!strings.Contains(result, marker) {
				t.Fatal("startup context, omitted byte count or final failure was lost")
			}

			if len(output.head)+len(output.tail) > maximumOutputBytes {
				t.Fatal("unbounded captured output")
			}

			if chunkSize > 1 && finalSnapshot != result {
				t.Fatal("streaming output lost final diagnostics")
			}
		})
	}
}

func TestCommandOutputUTF8AndConcurrentStreams(t *testing.T) {
	output := &limitedOutput{}
	original := strings.Repeat("Grüße 🌍\n", maximumOutputBytes/18)

	for _, value := range []byte(original) {
		_, _ = output.Write([]byte{value})
	}

	if output.String() != original {
		t.Fatal("split UTF-8 writes changed unabridged output")
	}

	var writers sync.WaitGroup

	for range 4 {
		writers.Go(func() {
			for range 80 {
				_, _ = output.Write([]byte(strings.Repeat("🧪", 5000)))
			}
		})
	}

	writers.Wait()

	if !utf8.ValidString(output.String()) || !strings.Contains(output.String(), "bytes omitted") {
		t.Fatal("invalid truncated UTF-8")
	}
}
