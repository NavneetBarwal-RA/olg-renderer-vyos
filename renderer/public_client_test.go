package renderer_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/routerarchitects/olg-renderer-vyos/renderer"
)

/*
TC-CLIENT-001
Type: Mixed
Title: Public client full MVP render flow
Summary:
Simulates a dummy public client calling the renderer with the canonical
full-mvp fixture through the exported API only. The test prints the input,
prints the generated output, and reports PASS or FAIL against the golden file.

Validates:
  - Public client usage through renderer.New and Render works cleanly
  - Canonical full-mvp input renders to the expected golden output
  - Input, output, and final status are visible in verbose test logs
*/
func TestPublicClientRenderFullMVPFlow(t *testing.T) {
	r, err := renderer.New()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	inputJSON, err := os.ReadFile(filepath.Join("..", "testdata", "valid", "full-mvp.json"))
	if err != nil {
		t.Fatalf("read input fixture: %v", err)
	}

	expectedOutput, err := os.ReadFile(filepath.Join("..", "testdata", "golden", "full-mvp.set"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}

	input := renderer.Input{
		Target:        "vyos",
		ConfigUUID:    "cfg-client-sample",
		SchemaName:    "olg-ucentral",
		SchemaVersion: "1.0.0",
		PayloadJSON:   inputJSON,
	}

	t.Logf("Dummy client input:\n%s", string(inputJSON))

	output, err := r.Render(context.Background(), input)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	t.Logf("Dummy client rendered output:\n%s", output.RenderedText)

	if output.RenderedText != string(expectedOutput) {
		t.Log("Status: FAIL (rendered output does not match testdata/golden/full-mvp.set)")
		t.Fatalf("full-mvp output mismatch")
	}

	t.Log("Status: PASS (rendered output matches testdata/golden/full-mvp.set)")
}
