package renderer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

/*
TC-INFO-001
Type: Positive
Title: Renderer info metadata
Summary:
Verifies that the exported renderer metadata matches the MVP contract.
This covers both the package-level metadata accessor and the instance-level
metadata accessor to ensure they stay consistent.

Validates:
  - Renderer name, target, and schema metadata are correct
  - Supported schema versions match the MVP contract
  - Instance metadata matches package metadata
*/
func TestGetInfoMetadata(t *testing.T) {
	info := GetInfo()
	if info.Name != "olg-renderer-vyos" {
		t.Fatalf("unexpected name: %q", info.Name)
	}
	if info.Target != "vyos" {
		t.Fatalf("unexpected target: %q", info.Target)
	}
	if info.SupportedSchemaName != "olg-ucentral" {
		t.Fatalf("unexpected schema name: %q", info.SupportedSchemaName)
	}
	if len(info.SupportedSchemaVersions) != 1 || info.SupportedSchemaVersions[0] != "1.0.0" {
		t.Fatalf("unexpected supported versions: %#v", info.SupportedSchemaVersions)
	}

	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	if r.Info().Target != info.Target {
		t.Fatalf("instance info mismatch")
	}
}

/*
TC-INPUT-001
Type: Negative
Title: Invalid render inputs
Summary:
Exercises the renderer's required input validation for context, metadata,
and payload presence. Each subtest passes one invalid condition and expects
the stable invalid_input error code.

Validates:
  - Nil and canceled contexts are rejected
  - Required metadata fields must be non-empty
  - Payload JSON must be present and non-blank
*/
func TestRenderInvalidInput(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	base := Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-1",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "1.0.0",
		PayloadJSON:   []byte(`{"interfaces":[]}`),
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name  string
		ctx   context.Context
		input Input
	}{
		{name: "nil context", ctx: nil, input: base},
		{name: "canceled context", ctx: canceledCtx, input: base},
		{name: "missing target", ctx: context.Background(), input: withInput(base, func(in *Input) { in.Target = "" })},
		{name: "missing config uuid", ctx: context.Background(), input: withInput(base, func(in *Input) { in.ConfigUUID = "" })},
		{name: "missing schema name", ctx: context.Background(), input: withInput(base, func(in *Input) { in.SchemaName = "" })},
		{name: "missing schema version", ctx: context.Background(), input: withInput(base, func(in *Input) { in.SchemaVersion = "" })},
		{name: "missing payload", ctx: context.Background(), input: withInput(base, func(in *Input) { in.PayloadJSON = nil })},
		{name: "blank payload", ctx: context.Background(), input: withInput(base, func(in *Input) { in.PayloadJSON = []byte("  \n\t") })},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Render(tc.ctx, tc.input)
			assertErrorCode(t, err, CodeInvalidInput)
		})
	}
}

/*
TC-RENDER-001
Type: Negative
Title: Uninitialized renderer instance
Summary:
Confirms that calling Render on a zero-value renderer does not panic and
instead returns a typed renderer error. This protects callers from misuse
while keeping failure behavior deterministic.

Validates:
  - Zero-value renderer does not panic
  - Render returns a typed render_failed error
*/
func TestRenderUninitializedRenderer(t *testing.T) {
	var r Renderer

	_, err := r.Render(context.Background(), Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-1",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "1.0.0",
		PayloadJSON:   []byte(`{"interfaces":[]}`),
	})
	assertErrorCode(t, err, CodeRenderFailed)
}

/*
TC-COMPAT-001
Type: Negative
Title: Unsupported compatibility metadata
Summary:
Verifies that target, schema name, and schema version compatibility checks
fail with the correct stable error codes. Each subtest mutates one field
while keeping the rest of the input valid.

Validates:
  - Unsupported target returns unsupported_target
  - Unsupported schema name returns unsupported_schema
  - Unsupported schema version returns unsupported_schema_version
*/
func TestRenderCompatibilityChecks(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	input := Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-1",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "1.0.0",
		PayloadJSON:   []byte(`{"interfaces":[]}`),
	}

	_, err = r.Render(context.Background(), withInput(input, func(in *Input) { in.Target = "ios" }))
	assertErrorCode(t, err, CodeUnsupportedTarget)

	_, err = r.Render(context.Background(), withInput(input, func(in *Input) { in.SchemaName = "other" }))
	assertErrorCode(t, err, CodeUnsupportedSchema)

	_, err = r.Render(context.Background(), withInput(input, func(in *Input) { in.SchemaVersion = "9.9.9" }))
	assertErrorCode(t, err, CodeUnsupportedSchemaVer)
}

/*
TC-INPUT-002
Type: Negative
Title: Invalid payload JSON
Summary:
Ensures that malformed JSON in PayloadJSON is detected during decoding.
The renderer should reject the payload before normalization and return
the stable invalid_json error code.

Validates:
  - Malformed JSON is rejected
  - Decoding failures return invalid_json
*/
func TestRenderInvalidJSON(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	_, err = r.Render(context.Background(), Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-1",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "1.0.0",
		PayloadJSON:   []byte(`{"interfaces":`),
	})
	assertErrorCode(t, err, CodeInvalidJSON)
}

/*
TC-META-001
Type: Negative
Title: Payload metadata mismatch
Summary:
Checks that optional metadata embedded inside the payload cannot contradict
the authoritative renderer input metadata. The test uses a mismatched target
inside the payload and expects a metadata_mismatch error.

Validates:
  - Optional payload metadata is inspected when present
  - Conflicting payload metadata is rejected
  - Mismatch failures return metadata_mismatch
*/
func TestRenderPayloadMetadataMismatch(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	payload := []byte(`{
		"target": "not-vyos",
		"schema": {
			"name": "olg-ucentral",
			"version": "1.0.0"
		},
		"interfaces": []
	}`)

	_, err = r.Render(context.Background(), Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-1",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "1.0.0",
		PayloadJSON:   payload,
	})
	assertErrorCode(t, err, CodeMetadataMismatch)
}

/*
TC-GOLDEN-001
Type: Positive
Title: Golden fixture rendering
Summary:
Renders each canonical MVP input fixture and compares the output byte-for-byte
with the expected golden set-command file. This verifies deterministic command
ordering, exact formatting, and section rendering behavior.

Validates:
  - Interface-only fixtures match expected output
  - NAT-only and NAT-absent fixtures match expected output
  - Combined full MVP fixture matches expected output exactly
*/
func TestRenderGoldenFixtures(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	fixtures := []string{
		"interface-basic",
		"interface-vlan",
		"nat-explicit",
		"nat-absent",
		"full-mvp",
	}

	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			payload := mustReadFile(t, filepath.Join("..", "testdata", "valid", fixture+".json"))
			expected := mustReadFile(t, filepath.Join("..", "testdata", "golden", fixture+".set"))

			out, err := r.Render(context.Background(), Input{
				Target:        "vyos",
				ConfigUUID:    "cfg-123",
				SchemaName:    "olg-ucentral",
				SchemaVersion: "1.0.0",
				PayloadJSON:   payload,
			})
			if err != nil {
				t.Fatalf("render failed: %v", err)
			}

			if out.RenderedText != string(expected) {
				t.Fatalf("golden mismatch\nexpected:\n%s\nactual:\n%s", string(expected), out.RenderedText)
			}
		})
	}
}

/*
TC-DETERMINISM-001
Type: Positive
Title: Deterministic repeated rendering
Summary:
Renders the same full MVP fixture multiple times and compares every result
against the first output. This protects against accidental nondeterminism
from ordering, template behavior, or internal normalization changes.

Validates:
  - Repeated renders produce identical text
  - Output ordering is stable across runs
  - No hidden nondeterministic behavior leaks into rendering
*/
func TestRenderDeterministic(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	payload := mustReadFile(t, filepath.Join("..", "testdata", "valid", "full-mvp.json"))
	input := Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-123",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "1.0.0",
		PayloadJSON:   payload,
	}

	first, err := r.Render(context.Background(), input)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	for i := 0; i < 20; i++ {
		next, err := r.Render(context.Background(), input)
		if err != nil {
			t.Fatalf("render repeat %d failed: %v", i, err)
		}
		if next.RenderedText != first.RenderedText {
			t.Fatalf("non-deterministic output at iteration %d", i)
		}
	}
}

func assertErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %q, got nil", code)
	}
	var rerr *Error
	if !errors.As(err, &rerr) {
		t.Fatalf("expected renderer error, got %T (%v)", err, err)
	}
	if rerr.Code != code {
		t.Fatalf("expected error code %q, got %q (%v)", code, rerr.Code, err)
	}
}

func withInput(base Input, mutate func(*Input)) Input {
	clone := base
	mutate(&clone)
	return clone
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
