package render

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/btj93/herdr-tabline/internal/model"
)

func TestFixedHelpers(t *testing.T) {
	engine := New(
		map[string]map[string]string{"process": {"nvim": "editor"}},
		map[string]map[string]string{"agent_status": {"working": "●"}},
	)
	context := model.Context{
		Process: model.Process{Name: "nvim", Argv: []string{"nvim", "main.go"}},
		Agent:   model.Agent{Status: "working"},
		Now:     time.Date(2026, 8, 14, 9, 5, 0, 0, time.UTC),
	}

	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{"basename", `{{ basename "/a/b/file.txt" }}`, "file.txt"},
		{"dirname", `{{ dirname "/a/b/file.txt" }}`, "/a/b"},
		{"cleanPath", `{{ cleanPath "/a//b/../c" }}`, "/a/c"},
		// /Users/alice deliberately is NOT the fixture placeholder /Users/user. homeRelative
		// must shorten ANY supported home layout, so testing it with the placeholder would
		// not distinguish that from a hardcoded special case. This is test input for a
		// transformation, not a placeholder anyone should copy, so the fixture schema
		// correctly never sees it. Do not "sanitize" it to /Users/user.
		{"homeRelative", `{{ homeRelative "/Users/alice/project" }}`, "~/project"},
		{"default", `{{ default "fallback" "" }}`, "fallback"},
		{"coalesce", `{{ coalesce "" "" "present" }}`, "present"},
		{"lower", `{{ lower "MIXED" }}`, "mixed"},
		{"upper", `{{ upper "mixed" }}`, "MIXED"},
		{"title", `{{ title "hello world" }}`, "Hello World"},
		{"trim", `{{ trim "  trim me  " }}`, "trim me"},
		{"replace", `{{ replace "a" "o" "banana" }}`, "bonono"},
		{"contains", `{{ contains "ana" "banana" }}`, "true"},
		{"hasPrefix", `{{ hasPrefix "ba" "banana" }}`, "true"},
		{"hasSuffix", `{{ hasSuffix "na" "banana" }}`, "true"},
		{"matches", `{{ matches "^ba.*a$" "banana" }}`, "true"},
		{"join", `{{ join "," .Process.Argv }}`, "nvim,main.go"},
		{"first", `{{ first .Process.Argv }}`, "nvim"},
		{"last", `{{ last .Process.Argv }}`, "main.go"},
		{"truncate", `{{ truncate "abcdef" 4 }}`, "abc…"},
		{"truncateMiddle", `{{ truncateMiddle "abcdef" 5 }}`, "ab…ef"},
		{"padLeft", `{{ padLeft "界" 4 }}`, "  界"},
		{"padRight", `{{ padRight "é" 3 }}`, "é  "},
		{"alias", `{{ alias "process" .Process.Name }}`, "editor"},
		{"statusIcon", `{{ statusIcon .Agent.Status }}`, "●"},
		{"formatTime", `{{ formatTime "2006-01-02 15:04" .Now }}`, "2026-08-14 09:05"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tpl, err := engine.Compile(tc.name, tc.source)
			if err != nil {
				t.Fatal(err)
			}
			got, err := engine.Execute(tpl, context, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHomeRelativeUsesOnlySupportedUnixHomeLayouts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{"users child", "/Users/alice/project", "~/project"},
		{"users root", "/Users/alice", "~"},
		{"home child", "/home/alice/project", "~/project"},
		{"home root", "/home/alice", "~"},
		{"outside standard homes", "/opt/project", "/opt/project"},
		{"users misleading prefix", "/Usersish/alice/project", "/Usersish/alice/project"},
		{"home misleading prefix", "/homebrew/alice/project", "/homebrew/alice/project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := homeRelative(tc.value); got != tc.want {
				t.Fatalf("homeRelative(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestTemplateHelpers(t *testing.T) {
	e := New(
		map[string]map[string]string{"process": {"nvim": "editor"}},
		map[string]map[string]string{"agent_status": {"working": "●"}},
	)
	tpl, err := e.Compile("helpers", `{{ basename "/a/b/file.txt" }}|{{ dirname "/a/b/file.txt" }}|{{ cleanPath "/a//b/../c" }}|{{ homeRelative "/tmp/project" }}|{{ default "fallback" "" }}|{{ coalesce "" "" "present" }}|{{ lower "MIXED" }}|{{ upper "mixed" }}|{{ title "hello world" }}|{{ trim "  trim me  " }}|{{ replace "a" "o" "banana" }}|{{ contains "ana" "banana" }}|{{ hasPrefix "ba" "banana" }}|{{ hasSuffix "na" "banana" }}|{{ matches "^ba.*a$" "banana" }}|{{ join "," .Process.Argv }}|{{ first .Process.Argv }}|{{ last .Process.Argv }}|{{ alias "process" .Process.Name }}|{{ statusIcon .Agent.Status }}`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := model.Context{Process: model.Process{Name: "nvim", Argv: []string{"nvim", "main.go"}}, Agent: model.Agent{Status: "working"}}
	got, err := e.Execute(tpl, ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "file.txt|/a/b|/a/c|/tmp/project|fallback|present|mixed|MIXED|Hello World|trim me|bonono|true|true|true|true|nvim,main.go|nvim|main.go|editor|●"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestTemplateHelpersFallbacksAndInvalidRegex(t *testing.T) {
	e := New(nil, nil)
	tpl, err := e.Compile("fallbacks", `{{ alias "process" "nvim" }}|{{ statusIcon "missing" }}|{{ first .Process.Argv }}|{{ last .Process.Argv }}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Execute(tpl, model.Context{}, 0)
	if err != nil || got != "nvim|missing||" {
		t.Fatalf("got %q, err %v", got, err)
	}

	invalid, err := e.Compile("invalid-regex", `{{ matches "[" "anything" }}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(invalid, model.Context{}, 0); err == nil {
		t.Fatal("expected invalid regexp to fail during execution")
	}
}

func TestCollectionEndsReturnElementZeroValueForEmptyCollections(t *testing.T) {
	if got := first([]int{}); got != 0 {
		t.Fatalf("first empty []int = %#v, want 0", got)
	}
	if got := last([]int{}); got != 0 {
		t.Fatalf("last empty []int = %#v, want 0", got)
	}
}

func TestDisplayWidthHelpers(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"abc", 3},
		{"界", 2},
		{"e\u0301", 1},
		{"●", 1},
	} {
		if got := displayWidth(tc.value); got != tc.want {
			t.Errorf("displayWidth(%q) = %d, want %d", tc.value, got, tc.want)
		}
	}

	if got := truncate("ab界cd", 4); got != "ab…" {
		t.Fatalf("truncate = %q", got)
	}
	for _, tc := range []struct {
		name     string
		got      string
		want     string
		maxWidth int
	}{
		{"truncate preserves combining cluster", truncate("e\u0301xy", 2), "e\u0301…", 2},
		{"truncateMiddle preserves combining cluster", truncateMiddle("e\u0301xyz", 3), "e\u0301…z", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q, want %q", tc.got, tc.want)
			}
			if !utf8.ValidString(tc.got) {
				t.Fatalf("invalid UTF-8: %q", tc.got)
			}
			if width := displayWidth(tc.got); width != tc.maxWidth {
				t.Fatalf("display width = %d, want %d for %q", width, tc.maxWidth, tc.got)
			}
		})
	}
	if got := truncateMiddle("abcdef", 5); got != "ab…ef" {
		t.Fatalf("truncateMiddle = %q", got)
	}
	if got := padLeft("界", 4); got != "  界" {
		t.Fatalf("padLeft = %q", got)
	}
	if got := padRight("e\u0301", 3); got != "e\u0301  " {
		t.Fatalf("padRight = %q", got)
	}
}
