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

// TestPresentTokensRender covers the transition herdr-tokens will cause: until a producer
// exists, tokens are always absent, so every test to date exercised only that path. When a
// producer starts, this path runs for the first time in production.
func TestPresentTokensRender(t *testing.T) {
	engine := render.New(nil, nil)
	context := model.Context{
		Tab: model.Tab{Number: 1},
		Workspace: model.Workspace{Label: "config", Tokens: map[string]any{
			"st_working": "config",
			"n_agents":   "7",
		}},
	}
	cases := []struct{ name, source, want string }{
		{"bare access", `{{ .Workspace.Tokens.st_working }}`, "config"},
		{"guarded access", `{{ with .Workspace.Tokens.st_working }}[{{ . }}]{{ end }}`, "[config]"},
		{"count token", `{{ .Workspace.Tokens.n_agents }}`, "7"},
		// A token the producer legitimately omits, alongside ones it publishes.
		{"absent among present", `{{ .Workspace.Tokens.att_blocked }}x`, "x"},
		{"guarded absent", `{{ with .Workspace.Tokens.att_blocked }}![{{ . }}]{{ end }}ok`, "ok"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tpl, err := engine.Compile("t", test.source)
			if err != nil {
				t.Fatal(err)
			}
			got, err := engine.Execute(tpl, context, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("rendered %q, want %q", got, test.want)
			}
		})
	}
}

// TestPresentTokensRenderInASessionTemplate is the same flip for the status line.
func TestPresentTokensRenderInASessionTemplate(t *testing.T) {
	engine := render.New(nil, nil)
	session := model.Session{Workspace: model.Workspace{
		Label:  "projects",
		Tokens: map[string]any{"n_agents": "3"},
	}}
	tpl, err := engine.Compile("status", `{{ .Workspace.Label }}/{{ .Workspace.Tokens.n_agents }}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := engine.ExecuteSession(tpl, session, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != "projects/3" {
		t.Fatalf("rendered %q, want %q", got, "projects/3")
	}
}
