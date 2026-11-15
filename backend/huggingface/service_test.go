package huggingface

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
)

const testToken = "hf_DummyCredentialForTests123456789"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (run roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return run(request)
}

func testService(t *testing.T, run roundTripFunc) *Service {
	t.Helper()
	store, err := datastore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	service := New(store)
	service.Client.Transport = run
	t.Cleanup(service.Close)
	return service
}

func testResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       request,
	}
}

func TestConnectionValidationSecrecyAndDisconnect(t *testing.T) {
	status := http.StatusOK
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != origin+"/api/whoami-v2" || request.Header.Get("Authorization") != "Bearer "+testToken {
			t.Fatal("unexpected credential request", request.URL)
		}

		body := `{"type":"user","name":"dataset-user","auth":{"accessToken":"` + testToken + `"}}`
		return testResponse(request, status, body), nil
	})

	for _, invalid := range []string{"", "token", "hf_with\naHeader", "hf_" + strings.Repeat("x", 513)} {
		if _, err := service.Connect(context.Background(), invalid); err == nil {
			t.Fatal("invalid token accepted")
		}
	}

	connection, err := service.Connect(context.Background(), " "+testToken+" ")
	if err != nil || !connection.Connected || connection.UserName != "dataset-user" || connection.CheckedAt.IsZero() {
		t.Fatal("connect failed", connection, err)
	}

	encoded, _ := json.Marshal(connection)
	if strings.Contains(string(encoded), testToken) {
		t.Fatal("connection exposed a credential")
	}

	status = http.StatusUnauthorized
	if _, err := service.Connect(context.Background(), testToken); err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatal("authentication failure was not sanitized", err)
	}

	current, err := service.Status()
	if err != nil || current != connection {
		t.Fatal("failed login replaced the old connection", current, err)
	}

	if err := service.Disconnect(); err != nil {
		t.Fatal(err)
	}

	current, err = service.Status()
	if err != nil || current.Connected || current.UserName != "" {
		t.Fatal("disconnect retained account state", current, err)
	}
}

func TestDisconnectInvalidatesInFlightLogin(t *testing.T) {
	started := make(chan struct{})
	proceed := make(chan struct{})
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		close(started)
		<-proceed // Simulate a transport which returns even after cancellation.
		return testResponse(request, 200, `{"type":"user","name":"old-login"}`), nil
	})

	done := make(chan error, 1)
	go func() {
		_, err := service.Connect(context.Background(), testToken)
		done <- err
	}()
	<-started

	if err := service.Disconnect(); err != nil {
		close(proceed)
		t.Fatal(err)
	}
	close(proceed)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an old login resurrected a disconnected account")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("login did not finish after cancellation")
	}

	current, err := service.Status()
	if err != nil || current.Connected {
		t.Fatal("connection resurrected", current, err)
	}
}

func TestDatasetFilesPaginationAndValidation(t *testing.T) {
	digest := strings.Repeat("a", 64)
	calls := 0
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("Authorization") != "" || request.URL.EscapedPath() != "/api/datasets/org/data/tree/refs%2Fpr%2F1" {
			t.Fatal("unexpected anonymous tree request", request.URL)
		}

		response := testResponse(request, 200, `[{"type":"file","path":"data/train.parquet","size":42,"lfs":{"oid":"`+digest+`"}}]`)
		response.Header.Set("Link", `<https://huggingface.co/api/datasets/org/data/tree/refs%2Fpr%2F1?cursor=next-page>; rel=\"next\"`)
		return response, nil
	})

	page, err := service.Files(context.Background(), DatasetFilesRequest{Dataset: "org/data", Revision: "refs/pr/1", Limit: 1})
	if err != nil || len(page.Items) != 1 || !page.HasMore || page.NextCursor != "next-page" || page.Items[0].SHA256 != digest {
		t.Fatal("pagination/metadata failed", page, err)
	}

	for _, input := range []DatasetFilesRequest{
		{Dataset: "https://evil.invalid/dataset"},
		{Dataset: "org/../data"},
		{Dataset: "org/data", Revision: "a/../main"},
		{Dataset: "org/data", Prefix: "../secret"},
		{Dataset: "org/data", Limit: 101},
		{Dataset: "org/data", Cursor: strings.Repeat("x", 4097)},
	} {
		if _, err := service.Files(context.Background(), input); err == nil {
			t.Fatal("invalid file request accepted", input)
		}
	}

	if calls != 1 {
		t.Fatal("invalid input made network requests", calls)
	}
}

func TestRedirectsStripCredentialsAndRejectOtherOrigins(t *testing.T) {
	seenCDN := false
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "huggingface.co" {
			if request.Header.Get("Authorization") != "Bearer "+testToken {
				t.Fatal("initial authorization missing")
			}

			response := testResponse(request, 302, "")
			response.Header.Set("Location", "https://cdn-lfs.huggingface.co/object?signature=private-signed-value")
			return response, nil
		}

		seenCDN = true
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatal("credential forwarded to CDN")
		}

		return testResponse(request, 200, "content"), nil
	})

	response, err := service.get(context.Background(), origin+"/datasets/org/data/resolve/main/file", testToken)
	if err != nil || !seenCDN {
		t.Fatal("approved CDN redirect failed", err)
	}
	response.Body.Close()

	for _, address := range []string{
		"http://huggingface.co/file", "https://huggingface.co.evil.invalid/file",
		"https://127.0.0.1/file", "https://user@hf.co/file", "https://hf.co:8443/file",
	} {
		request, _ := http.NewRequest("GET", address, nil)
		if err := checkRedirect(request, nil); err == nil {
			t.Fatal("unsafe redirect accepted", address)
		}
	}

	request, _ := http.NewRequest("GET", "https://hf.co/file", nil)
	if err := checkRedirect(request, make([]*http.Request, 6)); err == nil {
		t.Fatal("unbounded redirects accepted")
	}
}

func TestDownloadFramingAndLimits(t *testing.T) {
	content := bytes.Repeat([]byte("dataset\x00\n"), 2048)
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		return testResponse(request, 200, string(content)), nil
	})
	input := DownloadRequest{Dataset: "org/data", File: "train.parquet", MaxBytes: int64(len(content))}
	stream, err := service.OpenDownload(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	reader := bufio.NewReader(stream)
	header, _ := reader.ReadString('\n')
	var metadata map[string]int64
	if header != "ZHF1\n" || json.NewDecoder(strings.NewReader(readLine(t, reader))).Decode(&metadata) != nil || metadata["size"] != int64(len(content)) {
		t.Fatal("invalid transfer header", header, metadata)
	}

	var received bytes.Buffer
	for {
		var length uint32
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
			t.Fatal(err)
		}

		if length == 0 {
			break
		}

		if length > frameSize {
			t.Fatal("unbounded frame", length)
		}

		if _, err := io.CopyN(&received, reader, int64(length)); err != nil {
			t.Fatal(err)
		}
	}

	var trailer struct {
		Size   int64
		SHA256 string
	}
	if err := json.Unmarshal([]byte(readLine(t, reader)), &trailer); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if !bytes.Equal(received.Bytes(), content) || trailer.Size != int64(len(content)) || trailer.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("content/digest mismatch", trailer)
	}

	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatal("normal completion did not end cleanly", err)
	}

	input.MaxBytes--
	if _, err := service.OpenDownload(context.Background(), input); err == nil {
		t.Fatal("known oversized download accepted")
	}

	for _, size := range []int64{-1, int64(len(content)) + 1} {
		var output bytes.Buffer
		if err := frameDownload(&output, bytes.NewReader(content), size, int64(len(content))-1); err == nil {
			t.Fatal("streamed oversized download accepted")
		}
	}

	var truncated bytes.Buffer
	if err := frameDownload(&truncated, bytes.NewReader(content), int64(len(content))+1, int64(len(content))+2); err == nil {
		t.Fatal("truncated response accepted")
	}
}

func readLine(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}

	return line
}

func TestGatedFailuresAndTransportErrorsNeverExposeSecrets(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			service := testService(t, func(request *http.Request) (*http.Response, error) {
				return testResponse(request, status, `{"error":"`+testToken+` private server response"}`), nil
			})

			_, err := service.Files(context.Background(), DatasetFilesRequest{Dataset: "org/gated"})
			if err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "UNAUTHENTICATED") {
				t.Fatal("unsafe dataset error", err)
			}
		})
	}

	service := testService(t, func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("https://cdn-lfs.hf.co/object?signature=" + testToken)
	})
	_, err := service.Files(context.Background(), DatasetFilesRequest{Dataset: "org/data"})
	if err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "signature") {
		t.Fatal("transport URL leaked", err)
	}
}

func TestDisconnectCancelsActiveStreamAndReleasesRequest(t *testing.T) {
	body, producer := io.Pipe()
	t.Cleanup(func() { producer.Close() })
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		response := testResponse(request, 200, "")
		response.Body, response.ContentLength = body, -1
		return response, nil
	})

	stream, err := service.OpenDownload(context.Background(), DownloadRequest{Dataset: "org/data", File: "file", MaxBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stream.Close() })

	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(stream)
		done <- err
	}()

	if err := service.Disconnect(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled download reported success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not abort the active transfer")
	}
	stream.Close()

	service.mu.Lock()
	active := len(service.active)
	service.mu.Unlock()
	if active != 0 {
		t.Fatal("active request leaked", active)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("signed URL containing " + testToken)
}

func TestInterruptedBodyHasNoCompletionTrailerOrRawError(t *testing.T) {
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		response := testResponse(request, 200, "")
		response.ContentLength = -1
		response.Body = io.NopCloser(io.MultiReader(strings.NewReader("partial"), failingReader{}))
		return response, nil
	})

	stream, err := service.OpenDownload(context.Background(), DownloadRequest{Dataset: "org/data", File: "file", MaxBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	encoded, err := io.ReadAll(stream)
	if err == nil || strings.Contains(err.Error(), testToken) || bytes.Contains(encoded, []byte(`"sha256"`)) {
		t.Fatal("interrupted transfer was complete or exposed its upstream error", string(encoded), err)
	}
}

func TestClosedServiceRejectsNewRequestsWithoutDeletingCredentials(t *testing.T) {
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		return testResponse(request, 200, `{"type":"user","name":"retained-account"}`), nil
	})
	if _, err := service.Connect(context.Background(), testToken); err != nil {
		t.Fatal(err)
	}

	service.Close()
	if _, err := service.Files(context.Background(), DatasetFilesRequest{Dataset: "org/data"}); err == nil {
		t.Fatal("shutdown allowed a new download request")
	}

	if _, err := service.Connect(context.Background(), testToken); err == nil {
		t.Fatal("shutdown allowed a new login")
	}

	connection, err := service.Status()
	if err != nil || !connection.Connected || connection.UserName != "retained-account" {
		t.Fatal("shutdown deleted a saved connection", connection, err)
	}
}
