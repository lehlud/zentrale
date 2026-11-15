package huggingface

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var repositoryNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,95}$`)
var digestPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

type DatasetFilesRequest struct {
	Dataset   string `json:"dataset"`
	Revision  string `json:"revision"`
	Prefix    string `json:"prefix"`
	Recursive bool   `json:"recursive"`
	Cursor    string `json:"cursor"`
	Limit     int    `json:"limit"`
}

type DatasetFile struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

type DatasetFilePage struct {
	Items      []DatasetFile `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
	HasMore    bool          `json:"hasMore"`
}

func ValidateDataset(dataset, revision string) error {
	parts := strings.Split(dataset, "/")
	if len(parts) > 2 {
		return errors.New("dataset must be a Hugging Face dataset ID, not a URL")
	}

	for _, part := range parts {
		if !repositoryNamePattern.MatchString(part) || strings.Contains(part, "..") {
			return errors.New("invalid Hugging Face dataset ID")
		}
	}

	if len(revision) > 200 || !ValidRelativePath(revision, false) {
		return errors.New("invalid Hugging Face revision")
	}

	return nil
}

func ValidRelativePath(value string, allowEmpty bool) bool {
	if value == "" {
		return allowEmpty
	}

	if len(value) > 1024 || strings.Contains(value, "\\") || strings.ContainsFunc(value, unicode.IsControl) {
		return false
	}

	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}

	return true
}

func ValidSHA256(value string) bool {
	return value == "" || digestPattern.MatchString(value)
}

func escapedPath(value string) string {
	parts := strings.Split(value, "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}

	return strings.Join(parts, "/")
}

func (service *Service) Files(ctx context.Context, input DatasetFilesRequest) (DatasetFilePage, error) {
	if input.Revision == "" {
		input.Revision = "main"
	}

	if err := ValidateDataset(input.Dataset, input.Revision); err != nil {
		return DatasetFilePage{}, err
	}

	if !ValidRelativePath(input.Prefix, true) || len(input.Cursor) > 4096 {
		return DatasetFilePage{}, errors.New("invalid dataset prefix or pagination cursor")
	}

	if input.Limit == 0 {
		input.Limit = 20
	}

	if input.Limit < 1 || input.Limit > 100 {
		return DatasetFilePage{}, errors.New("limit must be between 1 and 100")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx, token, release, err := service.beginRequest(ctx)
	if err != nil {
		return DatasetFilePage{}, err
	}
	defer release()

	address := origin + "/api/datasets/" + escapedPath(input.Dataset) + "/tree/" + url.PathEscape(input.Revision)
	if input.Prefix != "" {
		address += "/" + escapedPath(input.Prefix)
	}
	query := url.Values{
		"limit":     {fmt.Sprint(input.Limit)},
		"recursive": {fmt.Sprint(input.Recursive)},
	}
	if input.Cursor != "" {
		query.Set("cursor", input.Cursor)
	}

	response, err := service.get(ctx, address+"?"+query.Encode(), token)
	if err != nil {
		return DatasetFilePage{}, err
	}
	defer response.Body.Close()

	var entries []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		Size int64  `json:"size"`
		LFS  struct {
			OID string `json:"oid"`
		} `json:"lfs"`
	}
	if err := decodeResponse(response.Body, &entries, 2*1024*1024); err != nil {
		return DatasetFilePage{}, err
	}

	if len(entries) > input.Limit {
		return DatasetFilePage{}, errors.New("Hugging Face exceeded the requested page size")
	}

	page := DatasetFilePage{Items: make([]DatasetFile, 0, len(entries))}
	for _, entry := range entries {
		if !ValidRelativePath(entry.Path, false) || entry.Size < 0 ||
			(entry.Type != "file" && entry.Type != "directory") {
			return DatasetFilePage{}, errors.New("invalid Hugging Face file metadata")
		}

		item := DatasetFile{Path: entry.Path, Type: entry.Type, Size: entry.Size}
		if entry.LFS.OID != "" && ValidSHA256(entry.LFS.OID) {
			item.SHA256 = strings.ToLower(entry.LFS.OID)
		}
		page.Items = append(page.Items, item)
	}

	for _, link := range strings.Split(response.Header.Get("Link"), ",") {
		// The Hub also emits backslash-escaped relation quotes. Inspect only
		// the relation this way; keep the actual URL/cursor bytes unchanged.
		relation := strings.ReplaceAll(link, `\"`, `"`)
		if !strings.Contains(relation, `rel="next"`) {
			continue
		}

		start, end := strings.Index(link, "<"), strings.Index(link, ">")
		if start < 0 || end <= start {
			return DatasetFilePage{}, errors.New("invalid Hugging Face pagination")
		}
		next, err := url.Parse(link[start+1 : end])
		current, _ := url.Parse(address)
		if err != nil || next.Scheme != "https" || next.Host != "huggingface.co" ||
			next.User != nil || next.Path != current.Path || len(next.Query().Get("cursor")) > 4096 {
			return DatasetFilePage{}, errors.New("invalid Hugging Face pagination address")
		}

		page.NextCursor = next.Query().Get("cursor")
		page.HasMore = page.NextCursor != ""
	}

	return page, nil
}
