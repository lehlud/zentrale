package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/lehlud/zentrale/netguard"
	"golang.org/x/net/html"
)

var chatWebClient = newChatWebClient()

func newChatWebClient() *http.Client {
	client := netguard.HTTPClient(false)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many web redirects")
		}
		return validateChatWebURL(req.URL)
	}
	return client
}

func validateChatWebURL(address *url.URL) error {
	if len(address.String()) > 4096 || address.Opaque != "" || address.User != nil || address.Hostname() == "" ||
		(address.Scheme != "http" && address.Scheme != "https") || strings.ContainsAny(address.Host, "%\\") {
		return errors.New("a public HTTP(S) URL is required")
	}
	if port := address.Port(); port != "" && !(address.Scheme == "http" && port == "80") && !(address.Scheme == "https" && port == "443") {
		return errors.New("web research uses only standard HTTP(S) ports")
	}
	return nil
}

const webBodyLimit = 512 << 10

func openPublicWeb(ctx context.Context, client *http.Client, address, accept string) (*http.Response, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		return nil, errors.New("invalid web address")
	}
	if err := validateChatWebURL(parsed); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	return doPublicWeb(client, request)
}

func doPublicWeb(client *http.Client, request *http.Request) (*http.Response, error) {
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("could not load the public web page")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("public web page returned HTTP %d (%s)", response.StatusCode, http.StatusText(response.StatusCode))
	}
	return response, nil
}

func fetchChatWeb(ctx context.Context, address string) (any, error) {
	return readPublicWeb(ctx, chatWebClient, address)
}

func readPublicWeb(ctx context.Context, client *http.Client, address string) (any, error) {
	response, err := openPublicWeb(ctx, client, address, "text/html, application/xhtml+xml, text/plain")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	reader := io.LimitReader(response.Body, webBodyLimit)
	var text string
	switch contentType {
	case "text/html", "application/xhtml+xml":
		text = chatHTMLText(reader, response.Request.URL)
	case "text/plain":
		body, err := io.ReadAll(io.LimitReader(reader, maxChatTextBytes))
		if err != nil {
			return nil, errors.New("could not read the public web page")
		}
		text = strings.ToValidUTF8(string(body), "�")
	default:
		return nil, errors.New("the web page is not HTML or plain text")
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("public web page contains no readable text; try another source")
	}
	return map[string]string{"url": response.Request.URL.String(), "text": text}, nil
}

func chatHTMLText(reader io.Reader, base *url.URL) string {
	var text strings.Builder
	tokens := html.NewTokenizer(reader)
	skip := ""
	for text.Len() < maxChatTextBytes {
		switch tokens.Next() {
		case html.ErrorToken:
			return strings.TrimSpace(text.String())
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokens.Token()
			if skip != "" {
				continue
			}
			switch token.Data {
			case "script", "style", "head", "template", "noscript":
				skip = token.Data
			case "a":
				for _, attr := range token.Attr {
					if attr.Key != "href" {
						continue
					}
					link, err := base.Parse(attr.Val)
					if err == nil && validateChatWebURL(link) == nil && link.Host != "" {
						text.WriteString(" [" + link.String() + "] ")
					}
					break
				}
			case "p", "li", "br", "article", "section", "h1", "h2", "h3":
				text.WriteByte('\n')
			}
		case html.EndTagToken:
			if skip == tokens.Token().Data {
				skip = ""
			}
		case html.TextToken:
			if skip == "" {
				plain := strings.TrimSpace(string(tokens.Text()))
				if plain != "" {
					remaining := maxChatTextBytes - text.Len() - 1
					text.WriteString(strings.ToValidUTF8(plain[:min(len(plain), remaining)], ""))
					text.WriteByte(' ')
				}
			}
		}
	}
	return strings.TrimSpace(text.String())
}
