package renderer

import "fmt"

const (
	CodeInvalidInput         = "invalid_input"
	CodeInvalidJSON          = "invalid_json"
	CodeUnsupportedTarget    = "unsupported_target"
	CodeUnsupportedSchema    = "unsupported_schema"
	CodeUnsupportedSchemaVer = "unsupported_schema_version"
	CodeMetadataMismatch     = "metadata_mismatch"
	CodeMissingConfig        = "missing_config"
	CodeNormalizeFailed      = "normalize_failed"
	CodeTemplateFailed       = "template_failed"
	CodeRenderFailed         = "render_failed"
)

// Error is a typed renderer error.
type Error struct {
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message == "" {
		if e.Err != nil {
			return fmt.Sprintf("%s: %v", e.Code, e.Err)
		}
		return e.Code
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is allows errors.Is with code-level matching.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	if t.Code != "" {
		return e.Code == t.Code
	}
	if t.Message != "" {
		return e.Message == t.Message
	}
	return false
}

func newError(code, msg string, err error) *Error {
	return &Error{Code: code, Message: msg, Err: err}
}
