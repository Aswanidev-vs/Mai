package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/user/mai/pkg/interfaces"
)

type TransportType string

const (
	TransportHTTP TransportType = "http"
	TransportSSE  TransportType = "sse"
)

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type Client struct {
	serverURL     string
	transport     TransportType
	client        *http.Client
	serverInfo    *ServerInfo
	tools         []ToolDefinition
	initialized   bool
	sessionID     string
}

func NewClient(url string) *Client {
	return &Client{
		serverURL: url,
		transport: TransportHTTP,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) Initialize(ctx context.Context) error {
	log.Printf("[MCP] Initializing connection to %s...", c.serverURL)

	initParams := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "mai-agent",
			"version": "1.0.0",
		},
	}

	resp, err := c.sendRequest(ctx, "initialize", initParams)
	if err != nil {
		return fmt.Errorf("MCP initialize failed: %w", err)
	}

	var result struct {
		ServerInfo ServerInfo `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return fmt.Errorf("parse init response: %w", err)
	}

	c.serverInfo = &result.ServerInfo
	c.initialized = true
	log.Printf("[MCP] Connected to %s v%s", c.serverInfo.Name, c.serverInfo.Version)

	// Send initialized notification
	c.sendNotification(ctx, "notifications/initialized", nil)

	return nil
}

func (c *Client) DiscoverTools(ctx context.Context) ([]interfaces.ToolMetadata, error) {
	if !c.initialized {
		if err := c.Initialize(ctx); err != nil {
			return nil, err
		}
	}

	resp, err := c.sendRequest(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("MCP tools/list failed: %w", err)
	}

	var result struct {
		Tools []ToolDefinition `json:"tools"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse tools response: %w", err)
	}

	c.tools = result.Tools

	var metadata []interfaces.ToolMetadata
	for _, tool := range result.Tools {
		metadata = append(metadata, interfaces.ToolMetadata{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.InputSchema,
		})
	}

	log.Printf("[MCP] Discovered %d tools from %s", len(metadata), c.serverURL)
	return metadata, nil
}

func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	if !c.initialized {
		return "", fmt.Errorf("MCP client not initialized")
	}

	params := map[string]interface{}{
		"name":      toolName,
		"arguments": args,
	}

	resp, err := c.sendRequest(ctx, "tools/call", params)
	if err != nil {
		return "", fmt.Errorf("MCP tools/call failed: %w", err)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return string(resp), nil
	}

	if result.IsError {
		return "", fmt.Errorf("MCP tool error: %s", result.Content[0].Text)
	}

	if len(result.Content) > 0 {
		return result.Content[0].Text, nil
	}

	return "", nil
}

func (c *Client) ListResources(ctx context.Context) ([]map[string]interface{}, error) {
	if !c.initialized {
		return nil, fmt.Errorf("MCP client not initialized")
	}

	resp, err := c.sendRequest(ctx, "resources/list", nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Resources []map[string]interface{} `json:"resources"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}

	return result.Resources, nil
}

func (c *Client) sendRequest(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.serverURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rpcResp jsonrpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("invalid JSON-RPC response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

func (c *Client) sendNotification(ctx context.Context, method string, params interface{}) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.serverURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return
	}
	resp.Body.Close()
}

type MCPToolAdapter struct {
	metadata interfaces.ToolMetadata
	client   *Client
}

func NewMCPToolAdapter(metadata interfaces.ToolMetadata, client *Client) *MCPToolAdapter {
	return &MCPToolAdapter{metadata: metadata, client: client}
}

func (t *MCPToolAdapter) Metadata() interfaces.ToolMetadata {
	return t.metadata
}

func (t *MCPToolAdapter) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args map[string]interface{}
	if err := json.Unmarshal(params, &args); err != nil {
		args = make(map[string]interface{})
	}

	output, err := t.client.CallTool(ctx, t.metadata.Name, args)
	if err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	return interfaces.ToolResult{Output: output}, nil
}
