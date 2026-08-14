package render

import (
	"strings"
	"testing"
	"time"

	"github.com/btj93/herdr-tabline/internal/model"
)

func TestCompatibilityTemplate(t *testing.T) {
	e := New(nil, nil)
	tpl, err := e.Compile("default", ` {{ .Tab.Number }}: {{ .Pane.Directory }} > {{ .Process.Name }} `)
	if err != nil {
		t.Fatal(err)
	}
	ctx := model.Context{Tab: model.Tab{Number: 2}, Pane: model.Pane{Directory: "herdr"}, Process: model.Process{Name: "nvim"}}
	got, err := e.Execute(tpl, ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != " 2: herdr > nvim " {
		t.Fatalf("got %q", got)
	}
}

func TestExecuteOptionalMapValueAndStructTypo(t *testing.T) {
	e := New(nil, nil)
	emptyTokens := model.Context{Pane: model.Pane{Tokens: map[string]any{}}}

	tpl, err := e.Compile("optional", `{{ default "none" .Pane.Tokens.branch }}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Execute(tpl, emptyTokens, 0)
	if err != nil || got != "none" {
		t.Fatalf("got %q, err %v", got, err)
	}

	typo, err := e.Compile("typo", `{{ .Pane.Directorry }}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(typo, model.Context{}, 0); err == nil {
		t.Fatal("expected struct-field typo to fail")
	}
}

func TestExecuteSanitizesControlsAndRejectsControlOnlyOutput(t *testing.T) {
	e := New(nil, nil)
	tpl, err := e.Compile("controls", " left\n\tright ")
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Execute(tpl, model.Context{}, 0)
	if err != nil || got != " left  right " {
		t.Fatalf("got %q, err %v", got, err)
	}

	controls, err := e.Compile("only-controls", "\n\t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(controls, model.Context{}, 0); err == nil {
		t.Fatal("expected control-only output to fail")
	}
}

func TestExecuteMaxWidthPreservesOuterPadding(t *testing.T) {
	e := New(nil, nil)
	tpl, err := e.Compile("padded", ` 12: some-long-directory > nvim `)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Execute(tpl, model.Context{}, 16)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, " ") || !strings.HasSuffix(got, " ") {
		t.Fatalf("padding lost: %q", got)
	}
	if width := displayWidth(got); width != 16 {
		t.Fatalf("width = %d, want 16 for %q", width, got)
	}

	short, err := e.Execute(tpl, model.Context{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if width := displayWidth(short); width != 2 {
		t.Fatalf("width = %d, want 2 for %q", width, short)
	}
}

func TestFormatTimeUsesContextNow(t *testing.T) {
	e := New(nil, nil)
	tpl, err := e.Compile("time", `{{ formatTime "2006-01-02 15:04" .Now }}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Execute(tpl, model.Context{Now: time.Date(2026, 8, 14, 9, 5, 0, 0, time.UTC)}, 0)
	if err != nil || got != "2026-08-14 09:05" {
		t.Fatalf("got %q, err %v", got, err)
	}
}
