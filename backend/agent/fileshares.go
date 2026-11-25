package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/fileshare"
	"github.com/lehlud/zentrale/remote"
)

const fileShareKind = "file_share"

// FileShareConfig is the administrative projection. The complete definition,
// including credentials, lives separately in the encrypted credential store.
type FileShareConfig struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	Endpoint      string `json:"endpoint"`
	Root          string `json:"root"`
	Username      string `json:"username"`
	HostKeySHA256 string `json:"hostKeySha256"`
}

type fileShareDefinition struct {
	FileShareConfig
	Password string `json:"password"`
}

func (definition fileShareDefinition) connection() (*fileshare.Share, error) {
	return fileshare.New(fileshare.Config{
		Protocol: definition.Protocol, Endpoint: definition.Endpoint, Root: definition.Root,
		Username: definition.Username, Password: definition.Password, HostKeySHA256: definition.HostKeySHA256,
	})
}

func (engine *Engine) fileShareDefinition(id string) (fileShareDefinition, error) {
	var definition fileShareDefinition
	if !remote.ValidID(id) {
		return definition, errors.New("invalid file share ID")
	}
	secret, err := engine.Store.Credential(fileShareKind + "/" + id)
	if err != nil {
		return definition, err
	}
	err = json.Unmarshal(secret, &definition)
	return definition, err
}

// SaveFileShare validates configuration without accessing the network. A nil
// password retains the existing credential; an explicit empty value clears it.
func (engine *Engine) SaveFileShare(config FileShareConfig, password *string) (FileShareConfig, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" || len(config.Name) > 128 {
		return config, errors.New("a file share name of at most 128 bytes is required")
	}
	definition := fileShareDefinition{FileShareConfig: config}
	if config.ID == "" {
		definition.ID = NewID()
	} else {
		previous, err := engine.fileShareDefinition(config.ID)
		if err != nil {
			return config, err
		}
		definition.Password = previous.Password
	}
	if password != nil {
		definition.Password = *password
	}
	if _, err := definition.connection(); err != nil {
		return config, err
	}

	secret, err := json.Marshal(definition)
	if err != nil {
		return config, err
	}
	err = engine.Store.SaveCredentialResource(fileShareKind+"/"+definition.ID, secret, fileShareKind,
		definition.ID, "", definition.FileShareConfig)
	return definition.FileShareConfig, err
}

func (engine *Engine) FileShares(adminView bool) ([]map[string]any, error) {
	var fields []string
	if !adminView {
		fields = []string{"id", "name"}
	}
	return engine.Store.ListFields(fileShareKind, nil, 500, 0, fields)
}

func (engine *Engine) DeleteFileShare(id string) error {
	if !remote.ValidID(id) {
		return errors.New("invalid file share ID")
	}
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	return engine.Store.DeleteCredentialResource(fileShareKind+"/"+id, fileShareKind, id)
}

// chatFileShare is independent of the remote execution subsystem. The app
// username comes from the chat owner, never from tool or request arguments.
func (engine *Engine) chatFileShare(chatID, shareID string) (*fileshare.Share, string, error) {
	var chat domain.Agent
	if err := engine.Store.Load("agent", chatID, &chat); err != nil || chat.ChatOwnerID == "" || chat.ProjectID != "" {
		return nil, "", errors.New("file shares are only available in short chats")
	}
	owner, err := engine.Store.UserAccount(chat.ChatOwnerID)
	if err != nil || owner.Disabled {
		return nil, "", errors.New("chat owner is not active")
	}
	definition, err := engine.fileShareDefinition(shareID)
	if err != nil {
		return nil, "", errors.New("file share is not available")
	}
	share, err := definition.connection()
	return share, owner.Username, err
}

func (engine *Engine) ListChatFiles(ctx context.Context, chatID, shareID, path string) ([]fileshare.Entry, error) {
	share, username, err := engine.chatFileShare(chatID, shareID)
	if err != nil {
		return nil, err
	}
	return share.List(ctx, username, path)
}

func (engine *Engine) ReadChatFile(ctx context.Context, chatID, shareID, path string, maxBytes int64) ([]byte, error) {
	share, username, err := engine.chatFileShare(chatID, shareID)
	if err != nil {
		return nil, err
	}
	return share.Read(ctx, username, path, maxBytes)
}
