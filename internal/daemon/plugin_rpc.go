package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const pluginAPIVersion = 1

const (
	jsonRPCParseError     = -32700
	jsonRPCInvalidRequest = -32600
	jsonRPCMethodNotFound = -32601
	jsonRPCInternalError  = -32603
)

type pluginHelloParams struct {
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	AttnAPIVersion   int      `json:"attn_api_version"`
	Roles            []string `json:"roles"`
	ProviderSurfaces []string `json:"provider_surfaces,omitempty"`
}

type pluginHelloResult struct {
	OK bool `json:"ok"`
}

type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type pluginRegistry struct {
	mu        sync.RWMutex
	plugins   map[string]*pluginConnection
	providers map[string][]pluginProvider
}

func newPluginRegistry() *pluginRegistry {
	return &pluginRegistry{
		plugins:   make(map[string]*pluginConnection),
		providers: make(map[string][]pluginProvider),
	}
}

func (r *pluginRegistry) register(plugin *pluginConnection) error {
	if plugin == nil || plugin.name == "" {
		return errors.New("plugin name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.plugins[plugin.name]; exists {
		return fmt.Errorf("plugin %q is already connected", plugin.name)
	}
	r.plugins[plugin.name] = plugin
	return nil
}

func (r *pluginRegistry) unregister(plugin *pluginConnection) {
	if plugin == nil || plugin.name == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plugins[plugin.name] == plugin {
		delete(r.plugins, plugin.name)
		r.unregisterProvidersLocked(plugin.name)
	}
}

func (r *pluginRegistry) get(name string) *pluginConnection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.plugins[name]
}

type pluginProvider struct {
	PluginName string
	Surface    string
}

func (r *pluginRegistry) registerProviderSurfaces(plugin *pluginConnection, values []string) error {
	if plugin == nil || plugin.name == "" {
		return errors.New("plugin name is required")
	}

	surfaces := normalizeProviderSurfaces(values)
	if len(surfaces) == 0 {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plugins[plugin.name] != plugin {
		return fmt.Errorf("plugin %q is not connected", plugin.name)
	}

	r.unregisterProvidersLocked(plugin.name)
	for _, surface := range surfaces {
		r.providers[surface] = append(r.providers[surface], pluginProvider{
			PluginName: plugin.name,
			Surface:    surface,
		})
		sort.Slice(r.providers[surface], func(i, j int) bool {
			left := r.providers[surface][i]
			right := r.providers[surface][j]
			return left.PluginName < right.PluginName
		})
	}
	return nil
}

func (r *pluginRegistry) providersForSurface(surface string) []pluginProvider {
	surface = strings.TrimSpace(surface)
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := r.providers[surface]
	if len(providers) == 0 {
		return nil
	}
	out := make([]pluginProvider, len(providers))
	copy(out, providers)
	return out
}

func (r *pluginRegistry) unregisterProvidersLocked(pluginName string) {
	for surface, providers := range r.providers {
		filtered := providers[:0]
		for _, provider := range providers {
			if provider.PluginName != pluginName {
				filtered = append(filtered, provider)
			}
		}
		if len(filtered) == 0 {
			delete(r.providers, surface)
			continue
		}
		r.providers[surface] = filtered
	}
}

func normalizeProviderSurfaces(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	surfaces := make([]string, 0, len(values))
	for _, value := range values {
		surface := strings.TrimSpace(value)
		if surface == "" {
			continue
		}
		if _, exists := seen[surface]; exists {
			continue
		}
		seen[surface] = struct{}{}
		surfaces = append(surfaces, surface)
	}
	sort.Strings(surfaces)
	return surfaces
}

type pluginConnection struct {
	name    string
	version string
	roles   []string

	conn   net.Conn
	reader *bufio.Reader

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan jsonRPCMessage
	nextID    uint64
	closed    bool
}

func newPluginConnection(conn net.Conn, reader *bufio.Reader, params pluginHelloParams) *pluginConnection {
	return &pluginConnection{
		name:    strings.TrimSpace(params.Name),
		version: strings.TrimSpace(params.Version),
		roles:   append([]string(nil), params.Roles...),
		conn:    conn,
		reader:  reader,
		pending: make(map[string]chan jsonRPCMessage),
	}
}

func (p *pluginConnection) hasRole(role string) bool {
	role = strings.TrimSpace(role)
	for _, candidate := range p.roles {
		if strings.TrimSpace(candidate) == role {
			return true
		}
	}
	return false
}

func (p *pluginConnection) send(msg jsonRPCMessage) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return json.NewEncoder(p.conn).Encode(msg)
}

func (p *pluginConnection) closePending(err error) {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	for key, ch := range p.pending {
		delete(p.pending, key)
		ch <- jsonRPCMessage{
			Error: &jsonRPCError{Code: jsonRPCInternalError, Message: err.Error()},
		}
	}
}

func (p *pluginConnection) routeResponse(msg jsonRPCMessage) bool {
	key := jsonRPCIDKey(msg.ID)
	if key == "" {
		return false
	}

	p.pendingMu.Lock()
	ch, exists := p.pending[key]
	if exists {
		delete(p.pending, key)
	}
	p.pendingMu.Unlock()
	if !exists {
		return false
	}
	ch <- msg
	return true
}

func (p *pluginConnection) request(ctx context.Context, method string, params interface{}, result interface{}) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal plugin request params: %w", err)
	}

	p.pendingMu.Lock()
	if p.closed {
		p.pendingMu.Unlock()
		return errors.New("plugin connection is closed")
	}
	p.nextID++
	id := strconv.FormatUint(p.nextID, 10)
	responseCh := make(chan jsonRPCMessage, 1)
	p.pending[id] = responseCh
	p.pendingMu.Unlock()

	request := jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(id),
		Method:  method,
		Params:  payload,
	}
	if err := p.send(request); err != nil {
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
		return fmt.Errorf("send plugin request: %w", err)
	}

	select {
	case <-ctx.Done():
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
		return ctx.Err()
	case response := <-responseCh:
		if response.Error != nil {
			return fmt.Errorf("plugin %s: %s", method, response.Error.Message)
		}
		if result == nil {
			return nil
		}
		if len(response.Result) == 0 {
			return fmt.Errorf("plugin %s returned no result", method)
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode plugin %s result: %w", method, err)
		}
		return nil
	}
}

func readSocketFrame(reader *bufio.Reader) ([]byte, error) {
	for {
		data, err := reader.ReadBytes('\n')
		data = bytes.TrimSpace(data)
		if len(data) > 0 {
			return data, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func readInitialSocketFrame(reader *bufio.Reader, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("initial socket frame size limit must be positive")
	}

	var (
		frame    []byte
		started  bool
		depth    int
		inString bool
		escaped  bool
	)

	for len(frame) < maxBytes {
		b, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		frame = append(frame, b)

		if !started {
			if isJSONWhitespace(b) {
				continue
			}
			if b != '{' {
				return nil, errors.New("initial socket frame must be a JSON object")
			}
			started = true
			depth = 1
			continue
		}

		if inString {
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
			continue
		}

		switch b {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return bytes.TrimSpace(frame), nil
			}
		}
	}

	return nil, fmt.Errorf("initial socket frame exceeds %d bytes", maxBytes)
}

func isJSONWhitespace(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func parsePluginHello(data []byte) (json.RawMessage, pluginHelloParams, bool, error) {
	var msg jsonRPCMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, pluginHelloParams{}, false, nil
	}
	if msg.JSONRPC == "" && msg.Method == "" {
		return nil, pluginHelloParams{}, false, nil
	}
	if msg.JSONRPC != "2.0" {
		return msg.ID, pluginHelloParams{}, true, errors.New(`jsonrpc must be "2.0"`)
	}
	if msg.Method != "hello" {
		return msg.ID, pluginHelloParams{}, true, fmt.Errorf("first plugin method must be hello, got %q", msg.Method)
	}
	if jsonRPCIDKey(msg.ID) == "" {
		return msg.ID, pluginHelloParams{}, true, errors.New("hello requires an id")
	}

	var params pluginHelloParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return msg.ID, pluginHelloParams{}, true, fmt.Errorf("decode hello params: %w", err)
	}
	if strings.TrimSpace(params.Name) == "" {
		return msg.ID, pluginHelloParams{}, true, errors.New("hello params.name is required")
	}
	if params.AttnAPIVersion != pluginAPIVersion {
		return msg.ID, pluginHelloParams{}, true, fmt.Errorf("unsupported attn_api_version %d", params.AttnAPIVersion)
	}
	return msg.ID, params, true, nil
}

func jsonRPCIDKey(id json.RawMessage) string {
	return string(bytes.TrimSpace(id))
}

func jsonRPCResult(id json.RawMessage, result interface{}) jsonRPCMessage {
	payload, err := json.Marshal(result)
	if err != nil {
		return jsonRPCFailure(id, jsonRPCInternalError, "marshal JSON-RPC result")
	}
	return jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Result:  payload,
	}
}

func jsonRPCFailure(id json.RawMessage, code int, message string) jsonRPCMessage {
	return jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: message},
	}
}

func (d *Daemon) ensurePluginRegistry() *pluginRegistry {
	if d.plugins == nil {
		d.plugins = newPluginRegistry()
	}
	return d.plugins
}

func (d *Daemon) handlePluginConnection(conn net.Conn, reader *bufio.Reader, helloID json.RawMessage, params pluginHelloParams) {
	plugin := newPluginConnection(conn, reader, params)
	registry := d.ensurePluginRegistry()
	if err := registry.register(plugin); err != nil {
		_ = plugin.send(jsonRPCFailure(helloID, jsonRPCInvalidRequest, err.Error()))
		return
	}
	if err := registry.registerProviderSurfaces(plugin, params.ProviderSurfaces); err != nil {
		registry.unregister(plugin)
		_ = plugin.send(jsonRPCFailure(helloID, jsonRPCInvalidRequest, err.Error()))
		return
	}
	defer func() {
		registry.unregister(plugin)
		plugin.closePending(io.EOF)
	}()

	if err := plugin.send(jsonRPCResult(helloID, pluginHelloResult{OK: true})); err != nil {
		return
	}

	for {
		data, err := readSocketFrame(reader)
		if err != nil {
			return
		}

		var msg jsonRPCMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			_ = plugin.send(jsonRPCFailure(nil, jsonRPCParseError, "parse JSON-RPC message"))
			continue
		}
		if msg.JSONRPC != "2.0" {
			_ = plugin.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, `jsonrpc must be "2.0"`))
			continue
		}
		if msg.Method != "" {
			d.handlePluginMethod(plugin, msg)
			continue
		}
		if !plugin.routeResponse(msg) {
			_ = plugin.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, "response id is not pending"))
		}
	}
}

func (d *Daemon) handlePluginMethod(plugin *pluginConnection, msg jsonRPCMessage) {
	if jsonRPCIDKey(msg.ID) == "" {
		_ = plugin.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, "plugin method calls require an id"))
		return
	}

	_ = plugin.send(jsonRPCFailure(msg.ID, jsonRPCMethodNotFound, fmt.Sprintf("unknown method %q", msg.Method)))
}

func (d *Daemon) callPlugin(ctx context.Context, name, method string, params interface{}, result interface{}) error {
	plugin := d.ensurePluginRegistry().get(strings.TrimSpace(name))
	if plugin == nil {
		return fmt.Errorf("plugin %q is not connected", name)
	}
	return plugin.request(ctx, method, params, result)
}
