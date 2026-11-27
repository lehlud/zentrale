package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/fileshare"
	"github.com/lehlud/zentrale/remote"
)

const fileShareKind = "file_share"

// FileShareConfig is the administrative projection. The complete definition,
// including credentials, lives separately in the encrypted credential store.
type FileShareConfig struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Protocol      string   `json:"protocol"`
	Endpoint      string   `json:"endpoint"`
	Root          string   `json:"root"`
	Username      string   `json:"username"`
	HostKeySHA256 string   `json:"hostKeySha256"`
	Restricted    bool     `json:"restricted"`
	UserIDs       []string `json:"userIds"`
}

func (config FileShareConfig) allows(userID string) bool {
	return !config.Restricted || slices.Contains(config.UserIDs, userID)
}

type chatFileShareInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

type FileShareCheck struct {
	Entries int `json:"entries"`
}

var errChatShareUnavailable = errors.New("file share is not available in this chat")

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

	definition, _, err := engine.prepareFileShare(config, password)
	if err != nil {
		return config, err
	}
	if definition.ID == "" {
		definition.ID = NewID()
	}

	secret, err := json.Marshal(definition)
	if err != nil {
		return config, err
	}
	err = engine.Store.SaveCredentialResource(fileShareKind+"/"+definition.ID, secret, fileShareKind,
		definition.ID, "", definition.FileShareConfig)
	return definition.FileShareConfig, err
}

func (engine *Engine) prepareFileShare(config FileShareConfig, password *string) (fileShareDefinition, *fileshare.Share, error) {
	config.Name = strings.TrimSpace(config.Name)
	config.Protocol = strings.ToLower(strings.TrimSpace(config.Protocol))
	config.Endpoint = strings.TrimSpace(config.Endpoint)
	if config.Name == "" || len(config.Name) > 128 {
		return fileShareDefinition{}, nil, errors.New("a file share name of at most 128 bytes is required")
	}
	if err := engine.normalizeShareAccess(&config); err != nil {
		return fileShareDefinition{}, nil, err
	}
	if config.Protocol == fileshare.ProtocolDomainShare {
		config.Root, config.Username, config.HostKeySHA256 = "", "", ""
	}

	definition := fileShareDefinition{FileShareConfig: config}
	if config.ID != "" {
		previous, err := engine.fileShareDefinition(config.ID)
		if err != nil {
			return fileShareDefinition{}, nil, err
		}
		// Neither saving nor testing a draft may send a stored secret to a new address.
		sameEndpoint := previous.Protocol == config.Protocol && strings.TrimSpace(previous.Endpoint) == config.Endpoint
		if sameEndpoint {
			definition.Password = previous.Password
		} else if previous.Password != "" && password == nil {
			return fileShareDefinition{}, nil, errors.New("re-enter the credential when changing a file share endpoint or protocol")
		}
	}
	if password != nil {
		definition.Password = *password
	}
	share, err := definition.connection()
	return definition, share, err
}

func (engine *Engine) normalizeShareAccess(config *FileShareConfig) error {
	if !config.Restricted {
		config.UserIDs = []string{}
		return nil
	}
	if len(config.UserIDs) == 0 {
		return errors.New("select at least one user for the file share")
	}

	ids := slices.Clone(config.UserIDs)
	slices.Sort(ids)
	config.UserIDs = slices.Compact(ids)
	for _, id := range config.UserIDs {
		if _, err := engine.Store.UserAccount(id); err != nil {
			return errors.New("the file share selection contains an unknown user")
		}
	}
	return nil
}

// CheckFileShare uses the same root and identity as a chat. The caller has
// already passed the administrator check. No files, names or credentials are returned.
func (engine *Engine) CheckFileShare(ctx context.Context, config FileShareConfig, password *string, userID string) (FileShareCheck, error) {
	definition, share, err := engine.prepareFileShare(config, password)
	if err != nil {
		return FileShareCheck{}, err
	}
	user, err := engine.Store.UserAccount(userID)
	if err != nil || user.Disabled || !definition.allows(userID) {
		return FileShareCheck{}, errors.New("choose an active user with access to this share")
	}

	entries, err := share.List(ctx, user.Username, "")
	return FileShareCheck{Entries: len(entries)}, err
}

func (engine *Engine) FileShares() ([]map[string]any, error) {
	rows, err := engine.Store.List(fileShareKind, nil, 500, 0)
	for _, row := range rows {
		// Shares saved before per-user grants were available to everyone.
		if row["restricted"] == nil {
			row["restricted"] = false
		}
		if row["userIds"] == nil {
			row["userIds"] = []string{}
		}
	}
	return rows, err
}

func (engine *Engine) DeleteFileShare(id string) error {
	if !remote.ValidID(id) {
		return errors.New("invalid file share ID")
	}
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	return engine.Store.DeleteCredentialResource(fileShareKind+"/"+id, fileShareKind, id)
}

func (engine *Engine) chatShareOwner(chatID string) (domain.User, error) {
	var chat domain.Agent
	if err := engine.Store.Load("agent", chatID, &chat); err != nil || chat.ChatOwnerID == "" || chat.ProjectID != "" || chat.Role != "main" || chat.ParentID != "" {
		return domain.User{}, errors.New("file shares are only available in short chats")
	}
	owner, err := engine.Store.UserAccount(chat.ChatOwnerID)
	if err != nil || owner.Disabled {
		return domain.User{}, errors.New("chat owner is not active")
	}
	return owner.User, nil
}

func (engine *Engine) chatFileShares(chatID string) ([]chatFileShareInfo, error) {
	owner, err := engine.chatShareOwner(chatID)
	if err != nil {
		return nil, err
	}
	rows, err := engine.Store.ListFields(fileShareKind, nil, 500, 0, []string{"id", "name", "protocol", "restricted", "userIds"})
	if err != nil {
		return nil, err
	}
	visible := []chatFileShareInfo{}
	for _, row := range rows {
		var share FileShareConfig
		if err := decode(JSON(row), &share); err != nil {
			return nil, err
		}
		if share.allows(owner.ID) {
			visible = append(visible, chatFileShareInfo{ID: share.ID, Name: share.Name, Protocol: share.Protocol})
		}
	}
	return visible, nil
}

// The app username comes from the chat owner, never from tool arguments.
func (engine *Engine) chatFileShare(chatID, shareID string) (*fileshare.Share, string, error) {
	owner, err := engine.chatShareOwner(chatID)
	if err != nil {
		return nil, "", err
	}
	definition, err := engine.fileShareDefinition(shareID)
	if err != nil || !definition.allows(owner.ID) {
		return nil, "", errChatShareUnavailable
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

func (engine *Engine) SearchChatFiles(ctx context.Context, chatID, shareID, query, cursor string) (fileshare.ShareSearchPage, error) {
	share, username, err := engine.chatFileShare(chatID, shareID)
	if err != nil {
		return fileshare.ShareSearchPage{}, err
	}

	return share.Search(ctx, username, query, cursor)
}
