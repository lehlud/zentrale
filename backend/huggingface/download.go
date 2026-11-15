package huggingface

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sync"
)

const frameSize = 1024 * 1024

type DownloadRequest struct {
	Dataset  string `json:"dataset"`
	Revision string `json:"revision"`
	File     string `json:"file"`
	MaxBytes int64  `json:"maxBytes"`
}

type downloadStream struct {
	*io.PipeReader
	release func()
}

func (stream *downloadStream) Close() error {
	err := stream.PipeReader.Close()
	stream.release()
	return err
}

// OpenDownload returns a bounded framed stream, not a URL, token or local cache.
// Only a successful complete HTTP body produces the mandatory completion trailer.
func (service *Service) OpenDownload(ctx context.Context, input DownloadRequest) (io.ReadCloser, error) {
	if input.Revision == "" {
		input.Revision = "main"
	}

	if err := ValidateDataset(input.Dataset, input.Revision); err != nil {
		return nil, err
	}

	if !ValidRelativePath(input.File, false) || input.MaxBytes < 1 {
		return nil, errors.New("a literal dataset file and positive maxBytes are required")
	}

	ctx, token, release, err := service.beginRequest(ctx)
	if err != nil {
		return nil, err
	}

	address := origin + "/datasets/" + escapedPath(input.Dataset) + "/resolve/" +
		url.PathEscape(input.Revision) + "/" + escapedPath(input.File) + "?download=true"
	response, err := service.get(ctx, address, token)
	if err != nil {
		release()
		return nil, err
	}

	if response.ContentLength > input.MaxBytes {
		response.Body.Close()
		release()
		return nil, errors.New("dataset file exceeds approved maxBytes")
	}

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			release()
			response.Body.Close()
		})
	}
	reader, writer := io.Pipe()
	stream := &downloadStream{PipeReader: reader, release: cleanup}
	stop := context.AfterFunc(ctx, func() { stream.Close() })

	go func() {
		defer cleanup()
		defer writer.Close()
		defer stop()

		if err := frameDownload(writer, response.Body, response.ContentLength, input.MaxBytes); err != nil {
			// A transport error can embed the signed redirect URL. Do not forward
			// it through SSH copy errors, persisted jobs, or model-visible errors.
			_ = writer.CloseWithError(errors.New("Hugging Face transfer interrupted; no complete file was committed"))
		}
	}()

	return stream, nil
}

func frameDownload(writer io.Writer, source io.Reader, expectedSize, maxBytes int64) error {
	if _, err := fmt.Fprintf(writer, "ZHF1\n{\"size\":%d}\n", expectedSize); err != nil {
		return err
	}

	digest := sha256.New()
	buffer := make([]byte, frameSize)
	var total int64
	var length [4]byte

	for {
		count, readErr := source.Read(buffer)
		if count > 0 {
			if int64(count) > maxBytes-total {
				return errors.New("dataset file exceeds approved maxBytes")
			}

			total += int64(count)
			_, _ = digest.Write(buffer[:count])
			binary.BigEndian.PutUint32(length[:], uint32(count))
			if _, err := writer.Write(length[:]); err != nil {
				return err
			}

			if _, err := writer.Write(buffer[:count]); err != nil {
				return err
			}
		}

		if readErr == io.EOF {
			break
		}

		if readErr != nil {
			return readErr
		}
	}

	if expectedSize >= 0 && total != expectedSize {
		return errors.New("dataset file length does not match the HTTP response")
	}

	if _, err := writer.Write([]byte{0, 0, 0, 0}); err != nil {
		return err
	}

	return json.NewEncoder(writer).Encode(struct {
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	}{Size: total, SHA256: hex.EncodeToString(digest.Sum(nil))})
}
