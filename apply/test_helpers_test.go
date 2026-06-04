package apply

import (
	"context"
	"reflect"
	"testing"
)

type fakeExecutor struct {
	calls  int
	plans  []Plan
	result ExecutionResult
	err    error
}

func (f *fakeExecutor) Execute(ctx context.Context, plan Plan) (ExecutionResult, error) {
	f.calls++
	f.plans = append(f.plans, clonePlan(plan))
	return f.result, f.err
}

func baseInput(commands string) Input {
	return Input{
		Target:          "vyos",
		ConfigUUID:      "cfg-123",
		DesiredCommands: commands,
	}
}

func sampleCommands() string {
	return stringsJoinLines(
		"set interfaces bridge br0 address dhcp",
		"set interfaces ethernet eth0 description 'WAN uplink'",
		"set service ssh port 22",
		"set nat source rule 100 translation address masquerade",
	)
}

func defaultDeleteCommands() []string {
	return []string{
		"delete interfaces bridge",
		"delete nat source",
		"delete service dhcp-server",
		"delete service dns forwarding",
		"delete service ssh",
	}
}

func stringsJoinLines(lines ...string) string {
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	return out
}

func assertApplyCode(t *testing.T, err error, code Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %q, got nil", code)
	}
	if !IsCode(err, code) {
		t.Fatalf("expected error code %q, got %q: %v", code, CodeOf(err), err)
	}
}

func assertNoApplyError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertStringSlicesEqual(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected slice:\n got: %#v\nwant: %#v", got, want)
	}
}

func assertPlanEqual(t *testing.T, got, want Plan) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected plan:\n got: %#v\nwant: %#v", got, want)
	}
}
