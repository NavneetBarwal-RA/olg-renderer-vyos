package renderer

import "encoding/json"

// Input is the canonical render request payload.
type Input struct {
	Target        string
	ConfigUUID    string
	SchemaName    string
	SchemaVersion string
	PayloadJSON   json.RawMessage
}

// Output is the canonical render response payload.
type Output struct {
	Target        string
	ConfigUUID    string
	SchemaName    string
	SchemaVersion string
	RenderedText  string
}

// Info describes renderer capabilities.
type Info struct {
	Name                    string
	Version                 string
	Target                  string
	SupportedSchemaName     string
	SupportedSchemaVersions []string
}
