package config_test

import (
	"strings"
	"testing"
)

// TestInvalidValuesAreDiagnosedPrecisely covers the first thing a new user hits: a typo in
// config.toml. An unparseable duration was previously reported as a RANGE violation, which
// sends the reader looking for a bounds problem they do not have. Each message must name
// the field, echo the offending value, and distinguish "not parseable" from "out of range".
func TestInvalidValuesAreDiagnosedPrecisely(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		contains []string
		absent   []string
	}{
		{
			name:     "unparseable duration is not a range error",
			body:     "schema_version = 1\npoll_interval = \"banana\"\n",
			contains: []string{"poll_interval", `"banana"`, "not a valid duration"},
			absent:   []string{"between 100ms"},
		},
		{
			name:     "out-of-range duration echoes the value and the bounds",
			body:     "schema_version = 1\npoll_interval = \"5m\"\n",
			contains: []string{"poll_interval", "5m", "between 100ms and 1m"},
		},
		{
			name:     "unparseable debounce is not a range error",
			body:     "schema_version = 1\nrefresh_debounce = \"soon\"\n",
			contains: []string{"refresh_debounce", `"soon"`, "not a valid duration"},
			absent:   []string{"between 0s"},
		},
		{
			name:     "out-of-range debounce echoes the value",
			body:     "schema_version = 1\nrefresh_debounce = \"9s\"\n",
			contains: []string{"refresh_debounce", "9s", "between 0s and 2s"},
		},
		{
			name:     "out-of-range width echoes the value",
			body:     "schema_version = 1\nmax_width = 5000\n",
			contains: []string{"max_width", "5000", "1024"},
		},
		{
			name:     "negative width echoes the value",
			body:     "schema_version = 1\nmax_width = -3\n",
			contains: []string{"max_width", "-3"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := loadConfigError(t, test.body)
			if err == nil {
				t.Fatal("configuration was accepted")
			}
			message := err.Error()
			for _, want := range test.contains {
				if !strings.Contains(message, want) {
					t.Errorf("error %q is missing %q", message, want)
				}
			}
			for _, unwanted := range test.absent {
				if strings.Contains(message, unwanted) {
					t.Errorf("error %q wrongly suggests %q", message, unwanted)
				}
			}
		})
	}
}
