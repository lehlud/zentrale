package datastore

import (
	"context"
	"strings"
	"time"
	"unicode"
)

type ShortChatDocument struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Size      int       `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
}

type ShortChatPassage struct {
	DocumentID string `json:"documentId"`
	Name       string `json:"name"`
	Section    int    `json:"section"`
	Page       int    `json:"page,omitempty"`
	Text       string `json:"text"`
}

func (i *Instance) SaveShortChatDocument(ctx context.Context, doc ShortChatDocument, source []byte, passages []ShortChatPassage) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO short_chat_documents(id,name,bytes,size,created_at) VALUES (?,?,?,?,?)`, doc.ID, doc.Name, source, doc.Size, doc.CreatedAt); err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, `INSERT INTO short_chat_reference_sections(document_id,section,page,text,title) VALUES (?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	for _, passage := range passages {
		if _, err := insert.ExecContext(ctx, doc.ID, passage.Section, passage.Page, passage.Text, doc.Name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (i *Instance) DeleteShortChatDocument(ctx context.Context, id string) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM short_chat_reference_sections WHERE document_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM short_chat_documents WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (i *Instance) ShortChatDocuments(ctx context.Context, limit, offset int) ([]ShortChatDocument, error) {
	rows, err := i.readDB().QueryContext(ctx, `SELECT id,name,size,created_at FROM short_chat_documents ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, min(max(limit, 1), 100), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	documents := []ShortChatDocument{}
	for rows.Next() {
		var doc ShortChatDocument
		if err := rows.Scan(&doc.ID, &doc.Name, &doc.Size, &doc.CreatedAt); err != nil {
			return nil, err
		}
		documents = append(documents, doc)
	}
	return documents, rows.Err()
}

func (i *Instance) HasShortChatDocuments(ctx context.Context) bool {
	var found bool
	return i.readDB().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM short_chat_documents)`).Scan(&found) == nil && found
}

func (i *Instance) ShortChatDocument(ctx context.Context, id string) (string, []byte, error) {
	var name string
	var source []byte
	err := i.readDB().QueryRowContext(ctx, `SELECT name,bytes FROM short_chat_documents WHERE id=?`, id).Scan(&name, &source)
	return name, source, err
}

const shortChatPassages = `SELECT document_id,name,section,page,text FROM short_chat_reference_sections AS ref
    JOIN short_chat_documents AS doc ON doc.id=ref.document_id`

func (i *Instance) ShortChatPassage(ctx context.Context, id string, section int) (ShortChatPassage, error) {
	var passage ShortChatPassage
	row := i.readDB().QueryRowContext(ctx, shortChatPassages+` WHERE ref.document_id=? AND ref.section=?`, id, section)
	err := row.Scan(&passage.DocumentID, &passage.Name, &passage.Section, &passage.Page, &passage.Text)
	return passage, err
}

func (i *Instance) SearchShortChatDocuments(ctx context.Context, query string) ([]ShortChatPassage, error) {
	words := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	terms := make([]string, 0, min(len(words), 12))
	seen := make(map[string]bool)
	for _, word := range words {
		if len(word) < 2 || seen[word] {
			continue
		}
		seen[word] = true
		terms = append(terms, word+"*")
		if len(terms) == 12 {
			break
		}
	}
	if len(terms) == 0 {
		return []ShortChatPassage{}, nil
	}
	passages, err := i.searchShortChatPassages(ctx, strings.Join(terms, " "))
	if err != nil || len(passages) != 0 || len(terms) == 1 {
		return passages, err
	}
	return i.searchShortChatPassages(ctx, strings.Join(terms, " OR "))
}

func (i *Instance) searchShortChatPassages(ctx context.Context, expression string) ([]ShortChatPassage, error) {
	rows, err := i.readDB().QueryContext(ctx, shortChatPassages+` WHERE short_chat_reference_sections MATCH ?
		ORDER BY length(offsets(short_chat_reference_sections)) DESC, document_id, section LIMIT 6`, expression)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	passages := []ShortChatPassage{}
	for rows.Next() {
		var passage ShortChatPassage
		if err := rows.Scan(&passage.DocumentID, &passage.Name, &passage.Section, &passage.Page, &passage.Text); err != nil {
			return nil, err
		}
		passages = append(passages, passage)
	}
	return passages, rows.Err()
}
