package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// A client of the Model Context Protocol over streamable HTTP (JSON-RPC 2.0 by POST; the answer is JSON or a text/event-stream with the
// message in it). It does what Genie's connections need and no more: initialize, list the tools, call one. The server is the client, never
// the page: the page cannot keep a token out of reach of its own scripts, and most MCP servers refuse a call from a page anyway.
//
// Every call goes out through newExtClientFor: public addresses only (checked at the connection), https, no proxy.

const (
	mcpProtocolVersion = "2025-06-18"
	mcpMaxBody         = 2 << 20
	mcpCallTimeout     = 30 * time.Second
	mcpMaxToolPages    = 5
)

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations struct {
		ReadOnlyHint    *bool `json:"readOnlyHint"`
		DestructiveHint *bool `json:"destructiveHint"`
	} `json:"annotations"`
}

type mcpResult struct {
	Text    string
	IsError bool
}

type mcpClient struct {
	url     string
	token   string
	http    *http.Client
	session string
	nextID  int
}

func newMCPClient(url, token string) *mcpClient {
	return &mcpClient{url: url, token: token, http: newExtClientFor(mcpCallTimeout), nextID: 1}
}

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      *int        `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     *int `json:"id"`
	Result json.RawMessage
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// post sends one JSON-RPC message. A request (wantID) returns its result; a notification returns nothing.
func (c *mcpClient) post(ctx context.Context, method string, params interface{}, request bool) (json.RawMessage, error) {
	msg := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	id := 0
	if request {
		id = c.nextID
		c.nextID++
		msg.ID = &id
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	req.Header.Set("User-Agent", "openrepl-genie-connection")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the service could not be reached")
	}
	defer resp.Body.Close()
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" && c.session == "" {
		c.session = s
	}
	if !request {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("the service answered %d", resp.StatusCode)
		}
		return nil, nil
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, errMCPAuth
	case resp.StatusCode >= 300:
		return nil, fmt.Errorf("the service answered %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		sc := bufio.NewScanner(io.LimitReader(resp.Body, mcpMaxBody))
		sc.Buffer(make([]byte, 64<<10), mcpMaxBody)
		var data bytes.Buffer
		flush := func() (json.RawMessage, bool, error) {
			if data.Len() == 0 {
				return nil, false, nil
			}
			var r rpcResponse
			err := json.Unmarshal(data.Bytes(), &r)
			data.Reset()
			if err != nil || r.ID == nil || *r.ID != id {
				return nil, false, nil // a notification or another message: not the answer
			}
			if r.Error != nil {
				return nil, true, fmt.Errorf("the service said: %s", clipRunes(r.Error.Message, 200))
			}
			return r.Result, true, nil
		}
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if res, ok, err := flush(); ok {
					return res, err
				}
				continue
			}
			if strings.HasPrefix(line, "data:") {
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if res, ok, err := flush(); ok {
			return res, err
		}
		return nil, fmt.Errorf("the service sent no answer")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, mcpMaxBody+1))
	if err != nil || len(b) > mcpMaxBody {
		return nil, fmt.Errorf("the answer was too long or stopped")
	}
	var r rpcResponse
	if err := json.Unmarshal(b, &r); err != nil || r.ID == nil || *r.ID != id {
		return nil, fmt.Errorf("the service did not answer as an MCP server does")
	}
	if r.Error != nil {
		return nil, fmt.Errorf("the service said: %s", clipRunes(r.Error.Message, 200))
	}
	return r.Result, nil
}

var errMCPAuth = fmt.Errorf("the service did not accept the token")

func (c *mcpClient) initialize(ctx context.Context) error {
	_, err := c.post(ctx, "initialize", map[string]interface{}{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]string{"name": "openrepl-genie", "version": "1"},
	}, true)
	if err != nil {
		return err
	}
	_, err = c.post(ctx, "notifications/initialized", nil, false)
	return err
}

func (c *mcpClient) listTools(ctx context.Context) ([]mcpTool, error) {
	var out []mcpTool
	cursor := ""
	for page := 0; page < mcpMaxToolPages; page++ {
		var params interface{}
		if cursor != "" {
			params = map[string]string{"cursor": cursor}
		}
		raw, err := c.post(ctx, "tools/list", params, true)
		if err != nil {
			return nil, err
		}
		var r struct {
			Tools      []mcpTool `json:"tools"`
			NextCursor string    `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("the list of tools could not be read")
		}
		out = append(out, r.Tools...)
		if r.NextCursor == "" {
			break
		}
		cursor = r.NextCursor
	}
	return out, nil
}

func (c *mcpClient) callTool(ctx context.Context, name string, args map[string]interface{}) (mcpResult, error) {
	if args == nil {
		args = map[string]interface{}{}
	}
	raw, err := c.post(ctx, "tools/call", map[string]interface{}{"name": name, "arguments": args}, true)
	if err != nil {
		return mcpResult{}, err
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return mcpResult{}, fmt.Errorf("the result could not be read")
	}
	var b strings.Builder
	for _, part := range r.Content {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		if part.Type == "text" {
			b.WriteString(part.Text)
		} else {
			fmt.Fprintf(&b, "[%s left out]", clipRunes(part.Type, 20))
		}
	}
	return mcpResult{Text: b.String(), IsError: r.IsError}, nil
}

// connect is initialize and tools/list in one: what a Connect does to see that the token works.
func (c *mcpClient) connect(ctx context.Context) ([]mcpTool, error) {
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	return c.listTools(ctx)
}
