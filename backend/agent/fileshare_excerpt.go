package agent

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/lehlud/zentrale/fileshare"
)

const (
	defaultShareExcerptBytes = 4 << 10
	maxShareExcerptBytes     = 8 << 10
)

type shareExcerpt struct {
	Text       string `json:"text"`
	Offset     int64  `json:"offset"`
	NextOffset *int64 `json:"nextOffset,omitempty"`
}

func (engine *Engine) readChatShareExcerpt(ctx context.Context, chatID, shareID, path string, args map[string]any) (shareExcerpt, error) {
	offset, validOffset := excerptArgument(args, "offset", 0, 0, fileshare.MaxReadBytes)
	limit, validLimit := excerptArgument(args, "limit", defaultShareExcerptBytes, 1, maxShareExcerptBytes)
	if !validOffset || !validLimit {
		return shareExcerpt{}, errors.New("invalid share read offset or limit")
	}

	share, username, err := engine.chatFileShare(chatID, shareID)
	if err != nil {
		return shareExcerpt{}, chatShareActionError(err, "could not read this share file")
	}
	// Even a one-byte limit can return a complete UTF-8 code point.
	data, err := share.ReadChunk(ctx, username, path, offset, max(limit, utf8.UTFMax))
	if err != nil {
		return shareExcerpt{}, chatShareActionError(err, "could not read this share file")
	}
	return textExcerpt(data, offset, int(limit))
}

func excerptArgument(args map[string]any, key string, fallback, minimum, maximum int64) (int64, bool) {
	value, present := args[key]
	if !present {
		return fallback, true
	}

	var integer int64
	switch value := value.(type) {
	case float64:
		if value < float64(minimum) || value > float64(maximum) || value != float64(int64(value)) {
			return 0, false
		}
		integer = int64(value)
	case int:
		integer = int64(value)
	case json.Number:
		var err error
		integer, err = value.Int64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return integer, integer >= minimum && integer <= maximum
}

func textExcerpt(data []byte, offset int64, limit int) (shareExcerpt, error) {
	end := 0
	for end < min(limit, len(data)) {
		remaining := data[end:]
		r, size := utf8.DecodeRune(remaining)
		if r == 0 || (r == utf8.RuneError && size == 1) {
			if len(data) > limit && !utf8.FullRune(remaining) {
				break // Lookahead ended inside the next rune.
			}
			return shareExcerpt{}, errors.New("file is not UTF-8 text at this offset; continue with nextOffset")
		}
		if end > 0 && end+size > limit {
			break
		}
		end += size
	}

	result := shareExcerpt{Text: string(data[:end]), Offset: offset}
	if end < len(data) && offset+int64(end) <= fileshare.MaxReadBytes {
		next := offset + int64(end)
		result.NextOffset = &next
	}
	return result, nil
}
