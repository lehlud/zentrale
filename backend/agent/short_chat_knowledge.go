package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lehlud/zentrale/datastore"
)

const shortChatPromptSetting = "short_chat_system_prompt"

func (engine *Engine) ShortChatSystemPrompt() (string, error) {
	return engine.Store.Setting(shortChatPromptSetting)
}

func (engine *Engine) SaveShortChatSystemPrompt(prompt string) error {
	if !utf8.ValidString(prompt) || strings.ContainsRune(prompt, '\x00') {
		return errors.New("system instructions must be valid UTF-8 text")
	}
	if err := engine.Store.SetSetting(shortChatPromptSetting, strings.TrimSpace(prompt)); err != nil {
		return err
	}
	engine.Emit("settings", "", nil)
	return nil
}

var knowledgeIndexSlots = make(chan struct{}, 2)

func (engine *Engine) AddShortChatDocument(ctx context.Context, name string, source io.Reader) (datastore.ShortChatDocument, error) {
	var doc datastore.ShortChatDocument
	name, err := attachmentFilename(name)
	if err != nil {
		return doc, err
	}
	extension := strings.ToLower(filepath.Ext(name))
	if extension != ".pdf" && extension != ".md" && extension != ".markdown" && extension != ".txt" {
		return doc, errors.New("choose a PDF, Markdown or text file")
	}
	select {
	case knowledgeIndexSlots <- struct{}{}:
		defer func() { <-knowledgeIndexSlots }()
	case <-ctx.Done():
		return doc, ctx.Err()
	}
	data, err := io.ReadAll(io.LimitReader(source, MaxAttachmentBytes+1))
	if err != nil {
		return doc, err
	}
	if len(data) > MaxAttachmentBytes {
		return doc, errors.New("file exceeds 25 MiB")
	}
	var text string
	if extension == ".pdf" {
		text, err = extractReferencePDF(ctx, data)
		if err != nil {
			return doc, err
		}
	} else {
		if !utf8.Valid(data) || bytes.ContainsRune(data, '\x00') {
			return doc, errors.New("file is not UTF-8 text")
		}
		text = string(data)
	}
	passages := referencePassages(text, extension == ".pdf")
	if len(passages) == 0 {
		return doc, errors.New("file contains no searchable text")
	}
	doc = datastore.ShortChatDocument{ID: NewID(), Name: name, Size: len(data), CreatedAt: time.Now().UTC()}
	if err := engine.Store.SaveShortChatDocument(ctx, doc, data, passages); err != nil {
		return datastore.ShortChatDocument{}, err
	}
	engine.Emit("short_chat_knowledge", "", nil)
	return doc, nil
}

func extractReferencePDF(ctx context.Context, source []byte) (string, error) {
	if !bytes.Contains(source[:min(len(source), 1024)], []byte("%PDF-")) {
		return "", errors.New("invalid PDF file")
	}
	executable, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", errors.New("PDF indexing requires pdftotext")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-enc", "UTF-8", "-", "-")
	cmd.Stdin = bytes.NewReader(source)
	output := &referencePDFOutput{}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", errors.New("PDF text extraction timed out")
		}
		return "", errors.New("PDF text could not be extracted")
	}
	return strings.ToValidUTF8(output.text.String(), "�"), nil
}

type referencePDFOutput struct{ text bytes.Buffer }

func (output *referencePDFOutput) Write(data []byte) (int, error) {
	if output.text.Len()+len(data) > 8<<20 {
		return 0, errors.New("extracted PDF text exceeds indexing limit")
	}
	return output.text.Write(data)
}

func referencePassages(text string, pdf bool) []datastore.ShortChatPassage {
	pages := []string{strings.TrimPrefix(text, "\ufeff")}
	if pdf {
		pages = strings.Split(text, "\f")
	}
	const size, overlap = 1600, 160
	var passages []datastore.ShortChatPassage
	for index, page := range pages {
		pageNumber := 0
		if pdf {
			pageNumber = index + 1
		}
		for remaining := strings.TrimSpace(page); remaining != ""; {
			end, next, count := len(remaining), 0, 0
			for position := range remaining {
				if count == size {
					end = position
					break
				}
				if count == size-overlap {
					next = position
				}
				count++
			}
			if text := strings.TrimSpace(remaining[:end]); text != "" {
				passages = append(passages, datastore.ShortChatPassage{Section: len(passages) + 1, Page: pageNumber, Text: text})
			}
			if end == len(remaining) {
				break
			}
			remaining = remaining[next:]
		}
	}
	return passages
}
