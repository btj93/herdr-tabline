package config_test

import (
	"testing"
)

// TestCompiledOwnsItsAliasAndIconMaps stops a consumer mutating configuration that the
// loader already validated. The controller rebuilds a renderer from these maps on every
// pass, so a mutation would silently change rendering without passing through Load.
func TestCompiledOwnsItsAliasAndIconMaps(t *testing.T) {
	compiled := loadConfig(t, "schema_version = 1\n[aliases.process]\nzsh = \"shell\"\n")

	aliases := compiled.Aliases()
	aliases["process"]["zsh"] = "TAMPERED"
	aliases["injected"] = map[string]string{"a": "b"}

	icons := compiled.Icons()
	icons["agent_status"]["working"] = "TAMPERED"

	fresh := compiled.Aliases()
	if fresh["process"]["zsh"] != "shell" {
		t.Errorf("alias was mutated through the accessor: %q", fresh["process"]["zsh"])
	}
	if _, injected := fresh["injected"]; injected {
		t.Error("a new alias category was injected through the accessor")
	}
	if got := compiled.Icons()["agent_status"]["working"]; got != "◐" {
		t.Errorf("icon was mutated through the accessor: %q", got)
	}
}
