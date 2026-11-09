package agent

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func (engine *Engine) searchWeb(ctx context.Context, agentRecord domain.Agent, query string) (any, error) {
	address := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	return engine.research(ctx, agentRecord, address, false)
}

//go:embed scripts/fetch.py
var fetchScript string

//go:embed scripts/git.py
var gitScript string

func (engine *Engine) research(ctx context.Context, agentRecord domain.Agent, address string, raw bool) (any, error) {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, errors.New("HTTP(S) URL required")
	}
	return engine.execute(ctx, agentRecord, "python3 -I -c "+remote.Quote(fetchScript)+" "+remote.Quote(address)+" "+remote.Quote(fmt.Sprint(raw))+" "+remote.Quote(agentTemporaryDirectory(agentRecord.ID)), true, false)
}
func (engine *Engine) github(ctx context.Context, args map[string]any) (any, error) {
	token, err := engine.GitForge.Token(ctx, "github")
	if err != nil {
		token, err = engine.Store.Setting("github_token")
	}
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, errors.New("authenticate GitHub in settings first")
	}
	method := strings.ToUpper(stringArg(args, "method"))
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return nil, errors.New("invalid GitHub method")
	}
	p := stringArg(args, "path")
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\r\n#") {
		return nil, errors.New("GitHub API path required")
	}
	body := stringArg(args, "body")
	if body != "" && !json.Valid([]byte(body)) {
		return nil, errors.New("body must be JSON")
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.github.com"+p, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Timeout:       40 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 4*1024*1024 {
		return nil, errors.New("GitHub response too large")
	}
	return map[string]any{"status": response.StatusCode, "body": string(data)}, nil
}
