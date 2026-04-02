package gurps

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAISettingsEnsureValidityPreservesExplicitEmptyAliases(t *testing.T) {
	settings := AISettings{ResolverAliases: map[string]map[string]string{}}

	settings.EnsureValidity()

	if settings.ResolverAliases == nil {
		t.Fatal("expected explicit empty resolver aliases to remain non-nil")
	}
	if len(settings.ResolverAliases) != 0 {
		t.Fatalf("expected no resolver aliases after normalization, got %#v", settings.ResolverAliases)
	}
	if settings.IsZero() {
		t.Fatal("expected explicit empty resolver aliases to keep AI settings non-zero for persistence")
	}
}

func TestAISettingsEffectiveGeminiModel(t *testing.T) {
	var settings AISettings
	if got := settings.EffectiveGeminiModel(); got != DefaultGeminiModel {
		t.Fatalf("expected default Gemini model %q, got %q", DefaultGeminiModel, got)
	}
	settings.GeminiModel = " gemini-3.1-flash "
	if got := settings.EffectiveGeminiModel(); got != "gemini-3.1-flash" {
		t.Fatalf("expected configured Gemini model to be trimmed, got %q", got)
	}
	settings.GeminiModel = " gemini-3.1-pro "
	if got := settings.EffectiveGeminiModel(); got != DefaultGeminiModel {
		t.Fatalf("expected Gemini 3.1 Pro alias to normalize to %q, got %q", DefaultGeminiModel, got)
	}
	settings.GeminiModel = " models/gemini-pro "
	if got := settings.EffectiveGeminiModel(); got != FallbackGeminiModel {
		t.Fatalf("expected legacy gemini-pro alias to normalize to %q, got %q", FallbackGeminiModel, got)
	}
}

func TestSaveAndLoadAIResolverAliases(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "custom"+AIResolverAliasesExt)
	input := map[string]map[string]string{
		" Skills ": {
			" Handguns ":       " Guns (Pistol) ",
			"Shotgun Shooting": "Guns (Shotgun)",
		},
		"equipment": {
			" medkit ": " First Aid Kit ",
		},
	}

	if err := SaveAIResolverAliases(filePath, input); err != nil {
		t.Fatalf("expected alias export to succeed, got %v", err)
	}

	loaded, err := LoadAIResolverAliases(os.DirFS(dir), filepath.Base(filePath))
	if err != nil {
		t.Fatalf("expected alias import to succeed, got %v", err)
	}

	want := map[string]map[string]string{
		"skills": {
			"handguns":         "Guns (Pistol)",
			"shotgun shooting": "Guns (Shotgun)",
		},
		"equipment": {
			"medkit": "First Aid Kit",
		},
	}
	if !reflect.DeepEqual(want, loaded) {
		t.Fatalf("expected %#v, got %#v", want, loaded)
	}
}

func TestLoadAIResolverAliasesSupportsLegacyRawMap(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "legacy"+AIResolverAliasesExt)
	if err := os.WriteFile(filePath, []byte(`{"skills":{"Handguns":"Guns (Pistol)"}}`), 0o644); err != nil {
		t.Fatalf("expected legacy alias file write to succeed, got %v", err)
	}

	loaded, err := LoadAIResolverAliases(os.DirFS(dir), filepath.Base(filePath))
	if err != nil {
		t.Fatalf("expected legacy alias import to succeed, got %v", err)
	}

	want := map[string]map[string]string{
		"skills": {
			"handguns": "Guns (Pistol)",
		},
	}
	if !reflect.DeepEqual(want, loaded) {
		t.Fatalf("expected %#v, got %#v", want, loaded)
	}
}
