package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/routerarchitects/olg-renderer-vyos/internal/normalize"
	"github.com/routerarchitects/olg-renderer-vyos/internal/templates"
)

const (
	rendererName        = "olg-renderer-vyos"
	rendererVersion     = "dev"
	rendererTarget      = "vyos"
	rendererSchemaName  = "olg-ucentral"
	rendererSchemaVer42 = "4.2.0"
)

// Option mutates renderer construction behavior.
type Option func(*Renderer) error

// Renderer is the public render facade.
type Renderer struct {
	engine  *templates.Engine
	portMap map[string][]string
}

// New constructs an MVP renderer instance.
func New(opts ...Option) (*Renderer, error) {
	engine, err := templates.New()
	if err != nil {
		return nil, newError(CodeTemplateFailed, "failed to initialize templates", err)
	}

	r := &Renderer{
		engine:  engine,
		portMap: defaultPortMap(),
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(r); err != nil {
			return nil, newError(CodeInvalidInput, "invalid renderer option", err)
		}
	}

	if r.engine == nil {
		return nil, newError(CodeTemplateFailed, "template engine is required", nil)
	}

	return r, nil
}

// WithPortMap extends or overrides the default selector-to-interface-list mapping.
func WithPortMap(portMap map[string][]string) Option {
	return func(r *Renderer) error {
		if portMap == nil {
			return fmt.Errorf("port map must not be nil")
		}
		validated := make(map[string][]string, len(portMap))
		for selector, ifaces := range portMap {
			if strings.TrimSpace(selector) == "" {
				return fmt.Errorf("port map selector must not be empty")
			}
			if selector != strings.TrimSpace(selector) || strings.ContainsAny(selector, "\r\n\t") {
				return fmt.Errorf("port map selector %q contains unsupported whitespace", selector)
			}
			if len(ifaces) == 0 {
				return fmt.Errorf("port map selector %q must include at least one interface", selector)
			}
			interfaces, err := normalizePortMapInterfaces(selector, ifaces)
			if err != nil {
				return err
			}
			validated[selector] = interfaces
		}

		next := clonePortMap(r.portMap)
		keys := make([]string, 0, len(validated))
		for selector := range validated {
			keys = append(keys, selector)
		}
		sort.Strings(keys)
		for _, selector := range keys {
			next[selector] = append([]string(nil), validated[selector]...)
		}
		r.portMap = next
		return nil
	}
}

// GetInfo returns renderer metadata.
func GetInfo() Info {
	return Info{
		Name:                    rendererName,
		Version:                 rendererVersion,
		Target:                  rendererTarget,
		SupportedSchemaName:     rendererSchemaName,
		SupportedSchemaVersions: []string{rendererSchemaVer42},
	}
}

// Info returns renderer metadata from an instance.
func (r *Renderer) Info() Info {
	return GetInfo()
}

// Render validates input, normalizes payload, and renders deterministic set commands.
func (r *Renderer) Render(ctx context.Context, input Input) (Output, error) {
	if r == nil || r.engine == nil {
		return Output{}, newError(CodeRenderFailed, "renderer is not initialized", nil)
	}

	if err := validateInput(ctx, input); err != nil {
		return Output{}, err
	}

	if err := validateCompatibility(input); err != nil {
		return Output{}, err
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(input.PayloadJSON, &root); err != nil {
		return Output{}, newError(CodeInvalidJSON, "payload_json is not valid JSON", err)
	}

	if err := validatePayloadMetadata(input, root); err != nil {
		return Output{}, err
	}

	normalized, err := normalize.Normalize(root, clonePortMap(r.portMap))
	if err != nil {
		var nErr *normalize.Error
		if errors.As(err, &nErr) {
			code := nErr.Code
			if code == "" {
				code = CodeNormalizeFailed
			}
			return Output{}, newError(code, nErr.Message, nErr.Err)
		}
		return Output{}, newError(CodeNormalizeFailed, "failed to normalize payload", err)
	}

	renderedText, err := r.engine.Render(normalized)
	if err != nil {
		return Output{}, newError(CodeTemplateFailed, "failed to render templates", err)
	}
	if strings.TrimSpace(renderedText) == "" {
		return Output{}, newError(CodeMissingConfig, "payload contains no renderable config", nil)
	}

	return Output{
		Target:        input.Target,
		ConfigUUID:    input.ConfigUUID,
		SchemaName:    input.SchemaName,
		SchemaVersion: input.SchemaVersion,
		RenderedText:  renderedText,
	}, nil
}

func defaultPortMap() map[string][]string {
	return map[string][]string{
		"WAN*": {"eth0"},
		"LAN*": {"eth1", "eth2"},
		"LAN1": {"eth1"},
		"LAN2": {"eth2"},
	}
}

func clonePortMap(portMap map[string][]string) map[string][]string {
	clone := make(map[string][]string, len(portMap))
	for selector, ifaces := range portMap {
		clone[selector] = append([]string(nil), ifaces...)
	}
	return clone
}

func normalizePortMapInterfaces(selector string, ifaces []string) ([]string, error) {
	seen := make(map[string]struct{}, len(ifaces))
	interfaces := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		if strings.TrimSpace(iface) == "" {
			return nil, fmt.Errorf("port map selector %q interface must not be empty", selector)
		}
		if err := normalize.ValidateInterfaceToken(iface, fmt.Sprintf("port map selector %q interface", selector)); err != nil {
			return nil, err
		}
		if _, exists := seen[iface]; exists {
			continue
		}
		seen[iface] = struct{}{}
		interfaces = append(interfaces, iface)
	}
	sort.Strings(interfaces)
	return interfaces, nil
}

func validateInput(ctx context.Context, input Input) error {
	if ctx == nil {
		return newError(CodeInvalidInput, "context is required", nil)
	}

	select {
	case <-ctx.Done():
		return newError(CodeInvalidInput, "context already canceled", ctx.Err())
	default:
	}

	if strings.TrimSpace(input.Target) == "" {
		return newError(CodeInvalidInput, "target is required", nil)
	}
	if strings.TrimSpace(input.ConfigUUID) == "" {
		return newError(CodeInvalidInput, "config_uuid is required", nil)
	}
	if strings.TrimSpace(input.SchemaName) == "" {
		return newError(CodeInvalidInput, "schema_name is required", nil)
	}
	if strings.TrimSpace(input.SchemaVersion) == "" {
		return newError(CodeInvalidInput, "schema_version is required", nil)
	}
	if len(bytesTrimSpace(input.PayloadJSON)) == 0 {
		return newError(CodeInvalidInput, "payload_json is required", nil)
	}

	return nil
}

func validateCompatibility(input Input) error {
	meta := GetInfo()

	if input.Target != meta.Target {
		return newError(CodeUnsupportedTarget, fmt.Sprintf("unsupported target %q", input.Target), nil)
	}
	if input.SchemaName != meta.SupportedSchemaName {
		return newError(CodeUnsupportedSchema, fmt.Sprintf("unsupported schema_name %q", input.SchemaName), nil)
	}

	for _, supported := range meta.SupportedSchemaVersions {
		if input.SchemaVersion == supported {
			return nil
		}
	}

	return newError(CodeUnsupportedSchemaVer, fmt.Sprintf("unsupported schema_version %q", input.SchemaVersion), nil)
}

func validatePayloadMetadata(input Input, root map[string]json.RawMessage) error {
	if err := matchOptionalString(root, "target", input.Target); err != nil {
		return err
	}
	if err := matchOptionalString(root, "schema_name", input.SchemaName); err != nil {
		return err
	}
	if err := matchOptionalString(root, "schema_version", input.SchemaVersion); err != nil {
		return err
	}

	rawSchema, ok := root["schema"]
	if !ok {
		return nil
	}

	var schemaObj map[string]json.RawMessage
	if err := json.Unmarshal(rawSchema, &schemaObj); err != nil {
		return newError(CodeMetadataMismatch, "payload schema metadata must be an object", err)
	}

	if err := matchOptionalString(schemaObj, "name", input.SchemaName); err != nil {
		return err
	}
	if err := matchOptionalString(schemaObj, "version", input.SchemaVersion); err != nil {
		return err
	}

	return nil
}

func matchOptionalString(obj map[string]json.RawMessage, key, expected string) error {
	raw, ok := obj[key]
	if !ok {
		return nil
	}

	var actual string
	if err := json.Unmarshal(raw, &actual); err != nil {
		return newError(CodeMetadataMismatch, fmt.Sprintf("payload metadata %q must be a string", key), err)
	}
	if actual != expected {
		return newError(CodeMetadataMismatch, fmt.Sprintf("payload metadata %q mismatch: %q != %q", key, actual, expected), nil)
	}
	return nil
}

func bytesTrimSpace(data []byte) []byte {
	start := 0
	for start < len(data) {
		if data[start] != ' ' && data[start] != '\n' && data[start] != '\r' && data[start] != '\t' {
			break
		}
		start++
	}
	end := len(data)
	for end > start {
		c := data[end-1]
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			break
		}
		end--
	}
	return data[start:end]
}
