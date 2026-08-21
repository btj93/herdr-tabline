package render_test

import (
	"strings"
	"testing"

	"github.com/btj93/herdr-tabline/internal/model"
	"github.com/btj93/herdr-tabline/internal/render"
)

// TestAbsentTokensRenderEmptyNotAPlaceholder covers running without a metadata producer.
// Token maps are map[string]any, so a missing key yields an untyped nil that
// text/template prints as "<no value>" even under missingkey=zero — straight into the
// user's tab bar, on every refresh, for a template that is otherwise perfectly valid.
func TestAbsentTokensRenderEmptyNotAPlaceholder(t *testing.T) {
	engine := render.New(nil, nil)
	for _, source := range []string{
		`{{ .Workspace.Tokens.st_working }}`,
		`{{ .Agent.Tokens.anything }}`,
		`{{ .Pane.Tokens.anything }}`,
	} {
		tpl, err := engine.Compile("t", "["+source+"]")
		if err != nil {
			t.Fatal(err)
		}
		got, err := engine.Execute(tpl, model.Context{Tab: model.Tab{Number: 1}}, 0)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if strings.Contains(got, "no value") {
			t.Errorf("%s rendered %q, want the placeholder stripped", source, got)
		}
	}
}

func TestAbsentTokensInASessionTemplateAlsoRenderEmpty(t *testing.T) {
	engine := render.New(nil, nil)
	tpl, err := engine.Compile("status", `[{{ .Workspace.Tokens.st_working }}]`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := engine.ExecuteSession(tpl, model.Session{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != "[]" {
		t.Fatalf("session template rendered %q, want %q", got, "[]")
	}
}
