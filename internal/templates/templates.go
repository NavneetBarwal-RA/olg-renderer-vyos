package templates

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"

	"github.com/routerarchitects/olg-renderer-vyos/internal/normalize"
)

//go:embed *.tmpl interface/*.tmpl
var embedFS embed.FS

type Engine struct {
	tmpl *template.Template
}

func New() (*Engine, error) {
	parsed, err := template.New("root").Funcs(template.FuncMap{
		"dict":      dict,
		"vyosQuote": vyosQuote,
	}).ParseFS(embedFS,
		"interface.tmpl",
		"nat.tmpl",
		"interface/bridge.tmpl",
		"interface/ethernet.tmpl",
		"interface/vlan.tmpl",
	)
	if err != nil {
		return nil, err
	}
	return &Engine{tmpl: parsed}, nil
}

func (e *Engine) Render(data normalize.RenderData) (string, error) {
	if e == nil || e.tmpl == nil {
		return "", fmt.Errorf("template engine is not initialized")
	}

	sections := make([]string, 0, 2)

	interfaces, err := e.execute("interface.tmpl", data.Interfaces)
	if err != nil {
		return "", err
	}
	interfaces = normalizeSection(interfaces)
	if interfaces != "" {
		sections = append(sections, interfaces)
	}

	nat, err := e.execute("nat.tmpl", data.NAT)
	if err != nil {
		return "", err
	}
	nat = normalizeSection(nat)
	if nat != "" {
		sections = append(sections, nat)
	}

	return strings.Join(sections, ""), nil
}

func (e *Engine) execute(name string, data any) (string, error) {
	var b bytes.Buffer
	if err := e.tmpl.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func normalizeSection(in string) string {
	in = strings.ReplaceAll(in, "\r\n", "\n")
	in = strings.Trim(in, "\n")
	if in == "" {
		return ""
	}
	return in + "\n"
}

func dict(values ...any) (map[string]any, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("dict requires key/value pairs")
	}
	out := make(map[string]any, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict keys must be strings")
		}
		out[key] = values[i+1]
	}
	return out, nil
}

func vyosQuote(value string) (string, error) {
	for _, r := range value {
		if r == '\'' || r == '\n' || r == '\r' || r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("value cannot be safely single-quoted")
		}
	}
	return "'" + value + "'", nil
}
