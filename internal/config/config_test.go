package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
	"pgregory.net/rapid"
)

// Feature: gortex-enhancements, Property 3: Guard rule config round-trip

// genGuardRule generates a random GuardRule with valid field values.
func genGuardRule() *rapid.Generator[GuardRule] {
	return rapid.Custom(func(t *rapid.T) GuardRule {
		kind := rapid.SampledFrom([]string{"co-change", "boundary"}).Draw(t, "kind")
		return GuardRule{
			Name:    rapid.StringMatching(`[a-z][a-z0-9\-]{0,29}`).Draw(t, "name"),
			Kind:    kind,
			Source:  rapid.StringMatching(`[a-z][a-z0-9/]{0,49}`).Draw(t, "source"),
			Target:  rapid.StringMatching(`[a-z][a-z0-9/]{0,49}`).Draw(t, "target"),
			Message: rapid.StringMatching(`[A-Za-z0-9 .,!?]{1,100}`).Draw(t, "message"),
		}
	})
}

// genGuardsConfig generates a random GuardsConfig with 0-10 rules.
func genGuardsConfig() *rapid.Generator[GuardsConfig] {
	return rapid.Custom(func(t *rapid.T) GuardsConfig {
		n := rapid.IntRange(0, 10).Draw(t, "numRules")
		rules := make([]GuardRule, n)
		for i := range n {
			rules[i] = genGuardRule().Draw(t, "rule")
		}
		return GuardsConfig{Rules: rules}
	})
}

// yamlConfig is a helper struct for writing YAML that viper can load.
// We use explicit yaml tags to ensure the keys match what viper/mapstructure expects.
type yamlConfig struct {
	Guards yamlGuardsConfig `yaml:"guards"`
}

type yamlGuardsConfig struct {
	Rules []yamlGuardRule `yaml:"rules"`
}

type yamlGuardRule struct {
	Name    string `yaml:"name"`
	Kind    string `yaml:"kind"`
	Source  string `yaml:"source"`
	Target  string `yaml:"target"`
	Message string `yaml:"message"`
}

func toYAMLConfig(gc GuardsConfig) yamlConfig {
	rules := make([]yamlGuardRule, len(gc.Rules))
	for i, r := range gc.Rules {
		rules[i] = yamlGuardRule(r)
	}
	return yamlConfig{Guards: yamlGuardsConfig{Rules: rules}}
}

func TestPropertyGuardConfigRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		original := genGuardsConfig().Draw(rt, "guardsConfig")

		// Serialize to YAML
		yc := toYAMLConfig(original)
		data, err := yaml.Marshal(yc)
		if err != nil {
			rt.Fatalf("failed to marshal YAML: %v", err)
		}

		// Write to a temp file named .gortex.yaml so viper can find it
		dir := t.TempDir()
		configPath := filepath.Join(dir, ".gortex.yaml")
		if err := os.WriteFile(configPath, data, 0644); err != nil {
			rt.Fatalf("failed to write config file: %v", err)
		}

		// Load via config.Load
		loaded, err := Load(configPath)
		if err != nil {
			rt.Fatalf("failed to load config: %v", err)
		}

		// Assert the loaded GuardsConfig matches the original
		if len(loaded.Guards.Rules) != len(original.Rules) {
			rt.Fatalf("rule count mismatch: got %d, want %d",
				len(loaded.Guards.Rules), len(original.Rules))
		}

		for i, want := range original.Rules {
			got := loaded.Guards.Rules[i]
			if got.Name != want.Name {
				rt.Errorf("rule[%d].Name: got %q, want %q", i, got.Name, want.Name)
			}
			if got.Kind != want.Kind {
				rt.Errorf("rule[%d].Kind: got %q, want %q", i, got.Kind, want.Kind)
			}
			if got.Source != want.Source {
				rt.Errorf("rule[%d].Source: got %q, want %q", i, got.Source, want.Source)
			}
			if got.Target != want.Target {
				rt.Errorf("rule[%d].Target: got %q, want %q", i, got.Target, want.Target)
			}
			if got.Message != want.Message {
				rt.Errorf("rule[%d].Message: got %q, want %q", i, got.Message, want.Message)
			}
		}
	})
}
