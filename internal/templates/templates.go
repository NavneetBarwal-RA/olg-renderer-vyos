package templates

import (
	"bytes"
	"embed"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/routerarchitects/olg-renderer-vyos/internal/normalize"
)

//go:embed *.tmpl interface/*.tmpl service/*.tmpl
var embedFS embed.FS

type Engine struct {
	tmpl *template.Template
}

func New() (*Engine, error) {
	parsed, err := template.New("root").Funcs(template.FuncMap{
		"allowedVLANsForMember": allowedVLANsForMember,
		"dict":                  dict,
		"vyosQuote":             vyosQuote,
	}).ParseFS(embedFS,
		"interface.tmpl",
		"service.tmpl",
		"nat.tmpl",
		"interface/bridge.tmpl",
		"interface/ethernet.tmpl",
		"interface/vlan.tmpl",
		"service/dhcp-server.tmpl",
		"service/dns-forwarding.tmpl",
		"service/ssh.tmpl",
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

	sections := make([]string, 0, 3)

	interfaces, err := e.execute("interface.tmpl", data.Interfaces)
	if err != nil {
		return "", err
	}
	interfaces = normalizeSection(interfaces)
	if interfaces != "" {
		sections = append(sections, interfaces)
	}

	services, err := e.execute("service.tmpl", data.Services)
	if err != nil {
		return "", err
	}
	services = normalizeSection(services)
	if services != "" {
		sections = append(sections, services)
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
	lines := strings.Split(in, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

func allowedVLANsForMember(vifs []normalize.VIF, member string) []int {
	seen := make(map[int]struct{}, len(vifs))
	ids := make([]int, 0, len(vifs))
	for _, vif := range vifs {
		if !vifIncludesMember(vif, member) {
			continue
		}
		if _, exists := seen[vif.ID]; exists {
			continue
		}
		seen[vif.ID] = struct{}{}
		ids = append(ids, vif.ID)
	}
	sort.Ints(ids)
	return ids
}

func vifIncludesMember(vif normalize.VIF, member string) bool {
	for _, candidate := range vif.MemberInterfaces {
		if candidate == member {
			return true
		}
	}
	return false
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
