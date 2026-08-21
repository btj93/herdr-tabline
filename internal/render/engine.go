package render

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"unicode"

	"github.com/btj93/herdr-tabline/internal/model"
)

// Engine compiles and executes tab-label templates with a fixed set of safe helpers.
type Engine struct {
	aliases map[string]map[string]string
	icons   map[string]map[string]string
}

// Template is a compiled label template.
type Template struct {
	source string
	parsed *template.Template
}

// Source returns the original template text.
func (t *Template) Source() string {
	if t == nil {
		return ""
	}
	return t.source
}

// New constructs a renderer with the configured display aliases and icons.
func New(aliases map[string]map[string]string, icons map[string]map[string]string) *Engine {
	return &Engine{
		aliases: cloneMaps(aliases),
		icons:   cloneMaps(icons),
	}
}

// Compile parses a template using only the renderer's fixed helper map.
func (e *Engine) Compile(name, source string) (*Template, error) {
	t, err := template.New(name).Option("missingkey=zero").Funcs(e.funcMap()).Parse(source)
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", name, err)
	}
	return &Template{source: source, parsed: t}, nil
}

// Execute renders a template, sanitizes control characters, and applies its width ceiling.
func (e *Engine) Execute(tpl *Template, context model.Context, maxWidth int) (string, error) {
	if tpl == nil || tpl.parsed == nil {
		return "", fmt.Errorf("template is nil")
	}

	var output bytes.Buffer
	if err := tpl.parsed.Execute(&output, context); err != nil {
		return "", fmt.Errorf("execute template %s: %w", tpl.parsed.Name(), err)
	}

	rendered := output.String()
	if rendered == "" || isControlOnly(rendered) {
		return "", fmt.Errorf("template %s rendered no displayable text", tpl.parsed.Name())
	}
	return applyMaxWidth(sanitizeControls(rendered), maxWidth), nil
}

// ExecuteSession renders a whole-session template for the status line. It shares the
// control sanitization and width ceiling of Execute, because the output lands in the same
// plain-text tab bar, but permits an empty result: a quiet session legitimately has nothing
// to report, where an empty tab label would be a bug.
func (e *Engine) ExecuteSession(tpl *Template, session model.Session, maxWidth int) (string, error) {
	if tpl == nil || tpl.parsed == nil {
		return "", fmt.Errorf("template is nil")
	}
	var output bytes.Buffer
	if err := tpl.parsed.Execute(&output, session); err != nil {
		return "", fmt.Errorf("execute template %s: %w", tpl.parsed.Name(), err)
	}
	return strings.TrimSpace(applyMaxWidth(sanitizeControls(output.String()), maxWidth)), nil
}

func sanitizeControls(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
}

func isControlOnly(value string) bool {
	for _, r := range value {
		if !unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// applyMaxWidth preserves tmux-style outer padding while truncating the interior.
func applyMaxWidth(label string, maxWidth int) string {
	if maxWidth <= 0 || displayWidth(label) <= maxWidth {
		return label
	}
	if maxWidth >= 3 && strings.HasPrefix(label, " ") && strings.HasSuffix(label, " ") {
		interior := label[1 : len(label)-1]
		return " " + truncate(interior, maxWidth-2) + " "
	}
	return truncate(label, maxWidth)
}
