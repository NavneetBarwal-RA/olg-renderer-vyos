package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	engine *templates.Engine
}

// New constructs an MVP renderer instance.
func New(opts ...Option) (*Renderer, error) {
	engine, err := templates.New()
	if err != nil {
		return nil, newError(CodeTemplateFailed, "failed to initialize templates", err)
	}

	r := &Renderer{engine: engine}
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

	normalized, err := normalize.Normalize(root)
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

	return Output{
		Target:        input.Target,
		ConfigUUID:    input.ConfigUUID,
		SchemaName:    input.SchemaName,
		SchemaVersion: input.SchemaVersion,
		RenderedText:  renderedText,
	}, nil
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
