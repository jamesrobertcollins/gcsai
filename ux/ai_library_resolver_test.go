package ux

import (
	"strings"
	"testing"
	"time"

	"github.com/richardwilkes/gcs/v5/model/gurps"
)

func TestAIResolveSkillUsesCanonicalSpecialization(t *testing.T) {
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-auto", Name: "Driving", DisplayName: "Driving (Automobile)", BaseName: "Driving", Specialization: "Automobile"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-heavy", Name: "Driving", DisplayName: "Driving (Heavy Wheeled)", BaseName: "Driving", Specialization: "Heavy Wheeled"},
	)

	resolved, retryItem, warning := catalog.resolveSkillAction(aiSkillAction{
		Name:   aiFlexibleString("Driving (Car)"),
		Points: aiFlexibleString("2"),
	})

	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if retryItem != nil {
		t.Fatalf("expected no retry item, got %#v", retryItem)
	}
	if resolved == nil {
		t.Fatal("expected a resolved skill action")
	}
	if got := resolved.ID.String(); got != "skill-auto" {
		t.Fatalf("expected skill-auto, got %q", got)
	}
	if got := resolved.Name.String(); got != "Driving (Automobile)" {
		t.Fatalf("expected resolved name Driving (Automobile), got %q", got)
	}
}

func TestAIResolveSkillDeduplicatesEquivalentLibraryEntries(t *testing.T) {
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-brawl-a", Name: "Brawling", DisplayName: "Brawling", BaseName: "Brawling"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-brawl-b", Name: "Brawling", DisplayName: "Brawling", BaseName: "Brawling"},
	)

	resolved, retryItem, warning := catalog.resolveSkillAction(aiSkillAction{
		Name:   aiFlexibleString("Brawling"),
		Points: aiFlexibleString("4"),
	})

	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if retryItem != nil {
		t.Fatalf("expected no retry item, got %#v", retryItem)
	}
	if resolved == nil {
		t.Fatal("expected a resolved skill action")
	}
	if got := resolved.Name.String(); got != "Brawling" {
		t.Fatalf("expected resolved name Brawling, got %q", got)
	}
}

func TestAIResolveSkillUsesAliasMap(t *testing.T) {
	captured := captureResolverDebugLog(t)
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-pistol", Name: "Guns (Pistol)", DisplayName: "Guns (Pistol)", BaseName: "Guns", Specialization: "Pistol"},
	)

	resolved, retryItem, warning := catalog.resolveSkillAction(aiSkillAction{
		Name:   aiFlexibleString("Handguns"),
		Points: aiFlexibleString("4"),
	})

	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if retryItem != nil {
		t.Fatalf("expected no retry item, got %#v", retryItem)
	}
	if resolved == nil {
		t.Fatal("expected a resolved skill action")
	}
	if got := resolved.ID.String(); got != "skill-pistol" {
		t.Fatalf("expected skill-pistol, got %q", got)
	}
	if got := resolved.Name.String(); got != "Guns (Pistol)" {
		t.Fatalf("expected resolved name Guns (Pistol), got %q", got)
	}
	if len(*captured) == 0 {
		t.Fatal("expected alias resolution to emit a debug log entry")
	}
}

func TestAIResolveSkillUsesAliasMapCaseInsensitive(t *testing.T) {
	captured := captureResolverDebugLog(t)
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-shotgun", Name: "Guns (Shotgun)", DisplayName: "Guns (Shotgun)", BaseName: "Guns", Specialization: "Shotgun"},
	)

	resolved, retryItem, warning := catalog.resolveSkillAction(aiSkillAction{
		Name:   aiFlexibleString("SHOTGUN SHOOTING"),
		Points: aiFlexibleString("2"),
	})

	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if retryItem != nil {
		t.Fatalf("expected no retry item, got %#v", retryItem)
	}
	if resolved == nil {
		t.Fatal("expected a resolved skill action")
	}
	if got := resolved.ID.String(); got != "skill-shotgun" {
		t.Fatalf("expected skill-shotgun, got %q", got)
	}
	if got := resolved.Name.String(); got != "Guns (Shotgun)" {
		t.Fatalf("expected resolved name Guns (Shotgun), got %q", got)
	}
	if len(*captured) == 0 {
		t.Fatal("expected alias resolution to emit a debug log entry")
	}
}

func TestAIResolveSkillUsesNameableNotes(t *testing.T) {
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-area", Name: "Area Knowledge (@area@)", DisplayName: "Area Knowledge", BaseName: "Area Knowledge", Nameables: []string{"area"}},
	)

	resolved, retryItem, warning := catalog.resolveSkillAction(aiSkillAction{
		Name:   aiFlexibleString("Area Knowledge (Mesa)"),
		Points: aiFlexibleString("2"),
	})

	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if retryItem != nil {
		t.Fatalf("expected no retry item, got %#v", retryItem)
	}
	if resolved == nil {
		t.Fatal("expected a resolved skill action")
	}
	if got := resolved.ID.String(); got != "skill-area" {
		t.Fatalf("expected skill-area, got %q", got)
	}
	if got := resolved.Notes.String(); got != "Mesa" {
		t.Fatalf("expected notes Mesa, got %q", got)
	}
	if got := resolved.Name.String(); got != "Area Knowledge (Mesa)" {
		t.Fatalf("expected resolved name Area Knowledge (Mesa), got %q", got)
	}
}

func TestAIResolveSkillNormalizesLegacyMechanicsPlural(t *testing.T) {
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-mechanic", Name: "Mechanic (@type@)", DisplayName: "Mechanic", BaseName: "Mechanic", Nameables: []string{"type"}},
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-vertol", Name: "Mechanic", DisplayName: "Mechanic (Vertol)", BaseName: "Mechanic", Specialization: "Vertol"},
	)

	resolved, retryItem, warning := catalog.resolveSkillAction(aiSkillAction{
		Name:   aiFlexibleString("Mechanics (Vehicles)"),
		Points: aiFlexibleString("12"),
	})

	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if retryItem != nil {
		t.Fatalf("expected no retry item, got %#v", retryItem)
	}
	if resolved == nil {
		t.Fatal("expected a resolved skill action")
	}
	if got := resolved.ID.String(); got != "skill-mechanic" {
		t.Fatalf("expected skill-mechanic, got %q", got)
	}
	if got := resolved.Notes.String(); got != "Vehicles" {
		t.Fatalf("expected notes Vehicles, got %q", got)
	}
	if got := resolved.Name.String(); got != "Mechanic (Vehicles)" {
		t.Fatalf("expected resolved name Mechanic (Vehicles), got %q", got)
	}
}

func TestAIResolveSkillLeavesAmbiguousBaseForCorrection(t *testing.T) {
	captured := captureResolverDebugLog(t)
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-auto", Name: "Driving", DisplayName: "Driving (Automobile)", BaseName: "Driving", Specialization: "Automobile"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-heavy", Name: "Driving", DisplayName: "Driving (Heavy Wheeled)", BaseName: "Driving", Specialization: "Heavy Wheeled"},
	)

	resolved, retryItem, warning := catalog.resolveSkillAction(aiSkillAction{Name: aiFlexibleString("Driving")})

	if resolved != nil {
		t.Fatalf("expected no resolved skill, got %#v", resolved)
	}
	if retryItem == nil {
		t.Fatal("expected a retry item for ambiguous Driving")
	}
	if len(retryItem.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(retryItem.Candidates))
	}
	if !strings.Contains(warning, "waiting for correction") {
		t.Fatalf("expected ambiguity warning, got %q", warning)
	}
	if len(*captured) == 0 {
		t.Fatal("expected ambiguous resolution to emit a debug log entry")
	}
}

func TestAIResolveSkillAliasHitLogsMappedName(t *testing.T) {
	captured := captureResolverDebugLog(t)
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-pistol", Name: "Guns (Pistol)", DisplayName: "Guns (Pistol)", BaseName: "Guns", Specialization: "Pistol"},
	)

	resolved, retryItem, warning := catalog.resolveSkillAction(aiSkillAction{
		Name:   aiFlexibleString("Handguns"),
		Points: aiFlexibleString("4"),
	})

	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if retryItem != nil {
		t.Fatalf("expected no retry item, got %#v", retryItem)
	}
	if resolved == nil {
		t.Fatal("expected a resolved skill action")
	}
	if len(*captured) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(*captured))
	}
	if got := (*captured)[0]; !strings.Contains(got, "alias-hit") || !strings.Contains(got, `input="Handguns"`) || !strings.Contains(got, `mapped="Guns (Pistol)"`) {
		t.Fatalf("expected alias-hit log for Handguns, got %q", got)
	}
}

func TestAIResolveSkillUnresolvedLogsCandidates(t *testing.T) {
	captured := captureResolverDebugLog(t)
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-auto", Name: "Driving", DisplayName: "Driving (Automobile)", BaseName: "Driving", Specialization: "Automobile"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-heavy", Name: "Driving", DisplayName: "Driving (Heavy Wheeled)", BaseName: "Driving", Specialization: "Heavy Wheeled"},
	)

	resolved, retryItem, warning := catalog.resolveSkillAction(aiSkillAction{
		Name:   aiFlexibleString("Driving"),
		Points: aiFlexibleString("2"),
	})

	if warning == "" {
		t.Fatal("expected ambiguity warning")
	}
	if retryItem == nil {
		t.Fatal("expected retry item")
	}
	if resolved != nil {
		t.Fatalf("expected unresolved skill action, got %#v", resolved)
	}
	if len(*captured) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(*captured))
	}
	if got := (*captured)[0]; !strings.Contains(got, "unresolved") || !strings.Contains(got, `input="Driving"`) || !strings.Contains(got, "candidates=[skill-auto:Driving (Automobile), skill-heavy:Driving (Heavy Wheeled)]") {
		t.Fatalf("expected unresolved log with top candidates, got %q", got)
	}
}

func TestAIResolverDebugCountersDeduplicateRepeatedEvents(t *testing.T) {
	state := aiResolverDebugCounterState{}
	fields := []string{"category=skills", `input="Driving"`, "candidates=[skill-auto:Driving (Automobile)]"}
	now := time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC)

	aiApplyResolverDebugCounterEvent(&state, "unresolved", fields, now)
	aiApplyResolverDebugCounterEvent(&state, "unresolved", fields, now.Add(time.Minute))

	if len(state.Entries) != 1 {
		t.Fatalf("expected 1 deduped entry, got %d", len(state.Entries))
	}
	if got := state.Entries[0].Count; got != 2 {
		t.Fatalf("expected deduped count of 2, got %d", got)
	}
	if got := state.Entries[0].Kind; got != "unresolved" {
		t.Fatalf("expected unresolved kind, got %q", got)
	}
}

func TestAIResolveTraitUsesNameableNotes(t *testing.T) {
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryDisadvantage, ID: "trait-code-honor", Name: "Code of Honor (@subject@)", DisplayName: "Code of Honor", BaseName: "Code of Honor", Nameables: []string{"subject"}},
	)

	resolved, retryItem, warning := catalog.resolveNamedAction(aiLibraryCategoryDisadvantage, aiNamedAction{
		Name:   aiFlexibleString("Code of Honor (Pirate)"),
		Points: aiFlexibleString("-10"),
	})

	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if retryItem != nil {
		t.Fatalf("expected no retry item, got %#v", retryItem)
	}
	if resolved == nil {
		t.Fatal("expected a resolved trait action")
	}
	if got := resolved.ID.String(); got != "trait-code-honor" {
		t.Fatalf("expected trait-code-honor, got %q", got)
	}
	if got := resolved.Notes.String(); got != "Pirate" {
		t.Fatalf("expected notes Pirate, got %q", got)
	}
	if got := resolved.Name.String(); got != "Code of Honor (Pirate)" {
		t.Fatalf("expected resolved name Code of Honor (Pirate), got %q", got)
	}
}

func TestAIResolveTraitMatchesTemplateNameablePhrase(t *testing.T) {
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryAdvantage, ID: "trait-heroic", Name: "Heroic Feats of @One of Strength, Dexterity, or Health@", DisplayName: "Heroic Feats of @One of Strength, Dexterity, or Health@", BaseName: "Heroic Feats of @One of Strength, Dexterity, or Health@", Nameables: []string{"One of Strength, Dexterity, or Health"}},
	)

	resolved, retryItem, warning := catalog.resolveNamedAction(aiLibraryCategoryAdvantage, aiNamedAction{
		Name:   aiFlexibleString("Heroic Feat of Strength"),
		Points: aiFlexibleString("5"),
	})

	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if retryItem != nil {
		t.Fatalf("expected no retry item, got %#v", retryItem)
	}
	if resolved == nil {
		t.Fatal("expected a resolved trait action")
	}
	if got := resolved.ID.String(); got != "trait-heroic" {
		t.Fatalf("expected trait-heroic, got %q", got)
	}
	if got := resolved.Notes.String(); got != "Strength" {
		t.Fatalf("expected notes Strength, got %q", got)
	}
	if got := resolved.Name.String(); got != "Heroic Feats of @One of Strength, Dexterity, or Health@ (Strength)" {
		t.Fatalf("expected resolved display to preserve template base with extracted notes, got %q", got)
	}
}

func TestAIResolveTraitDeduplicatesEquivalentNameableEntries(t *testing.T) {
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryDisadvantage, ID: "trait-code-honor-a", Name: "Code of Honor (@subject@)", DisplayName: "Code of Honor", BaseName: "Code of Honor", Nameables: []string{"subject"}},
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryDisadvantage, ID: "trait-code-honor-b", Name: "Code of Honor (@subject@)", DisplayName: "Code of Honor", BaseName: "Code of Honor", Nameables: []string{"subject"}},
	)

	resolved, retryItem, warning := catalog.resolveNamedAction(aiLibraryCategoryDisadvantage, aiNamedAction{
		Name:   aiFlexibleString("Code of Honor (Soldier's)"),
		Points: aiFlexibleString("-10"),
	})

	if warning != "" {
		t.Fatalf("expected no warning, got %q", warning)
	}
	if retryItem != nil {
		t.Fatalf("expected no retry item, got %#v", retryItem)
	}
	if resolved == nil {
		t.Fatal("expected a resolved trait action")
	}
	if got := resolved.Name.String(); got != "Code of Honor (Soldier's)" {
		t.Fatalf("expected resolved name Code of Honor (Soldier's), got %q", got)
	}
	if got := resolved.Notes.String(); got != "Soldier's" {
		t.Fatalf("expected notes Soldier's, got %q", got)
	}
}

func TestAIResolveNamedActionRetryCandidatesDeduplicateEquivalentEntries(t *testing.T) {
	captured := captureResolverDebugLog(t)
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryAdvantage, ID: "adv-magic-res-a", Name: "Magic Resistance", DisplayName: "Magic Resistance", BaseName: "Magic Resistance"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryAdvantage, ID: "adv-magic-res-b", Name: "Magic Resistance", DisplayName: "Magic Resistance", BaseName: "Magic Resistance"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryAdvantage, ID: "adv-improved-magic-res", Name: "Improved Magic Resistance", DisplayName: "Improved Magic Resistance", BaseName: "Improved Magic Resistance"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryAdvantage, ID: "adv-damage-res", Name: "Damage Resistance", DisplayName: "Damage Resistance", BaseName: "Damage Resistance"},
	)

	resolved, retryItem, warning := catalog.resolveNamedAction(aiLibraryCategoryAdvantage, aiNamedAction{
		Name:   aiFlexibleString("Magical Resistance"),
		Points: aiFlexibleString("15"),
	})

	if resolved != nil {
		t.Fatalf("expected unresolved named action, got %#v", resolved)
	}
	if warning == "" {
		t.Fatal("expected ambiguity warning")
	}
	if retryItem == nil {
		t.Fatal("expected retry item")
	}
	if len(retryItem.Candidates) != 3 {
		t.Fatalf("expected duplicate equivalent candidates to be collapsed to 3, got %d", len(retryItem.Candidates))
	}
	magicResistanceCount := 0
	for _, candidate := range retryItem.Candidates {
		if candidate.Name == "Magic Resistance" {
			magicResistanceCount++
		}
	}
	if magicResistanceCount != 1 {
		t.Fatalf("expected exactly one Magic Resistance candidate after deduplication, got %d", magicResistanceCount)
	}
	if len(*captured) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(*captured))
	}
	if got := (*captured)[0]; strings.Count(got, ":Magic Resistance") != 1 {
		t.Fatalf("expected deduped log candidates for Magic Resistance, got %q", got)
	}
}

func TestAIRecommendedTermsForConceptUsesCategoryLimits(t *testing.T) {
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryAdvantage, ID: "adv-signature", Name: "Signature Gear", DisplayName: "Signature Gear", BaseName: "Signature Gear"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryAdvantage, ID: "adv-blessed", Name: "Blessed", DisplayName: "Blessed", BaseName: "Blessed"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-pistol", Name: "Guns", DisplayName: "Guns (Pistol)", BaseName: "Guns", Specialization: "Pistol"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-beam", Name: "Beam Weapons", DisplayName: "Beam Weapons (Pistol)", BaseName: "Beam Weapons", Specialization: "Pistol"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryEquipment, ID: "eq-pistol", Name: "Pistol", DisplayName: "Pistol", BaseName: "Pistol"},
	)

	terms := catalog.recommendedTermsForConcept("signature guns pistol", map[aiLibraryCategory]int{
		aiLibraryCategoryAdvantage: 1,
		aiLibraryCategorySkill:     1,
		aiLibraryCategoryEquipment: 1,
	})

	checks := []string{
		"Recommended Canonical GURPS Terms:",
		"Advantages: Signature Gear",
		"Skills: Guns (Pistol)",
		"Equipment: Pistol",
	}
	for _, check := range checks {
		if !strings.Contains(terms, check) {
			t.Fatalf("expected recommended terms to contain %q, got %q", check, terms)
		}
	}
	if strings.Contains(terms, "Blessed") || strings.Contains(terms, "Beam Weapons") {
		t.Fatalf("expected category limits to suppress lower-ranked entries, got %q", terms)
	}
}

func TestAIRecommendedTermsForConceptDiversifiesSkillFamilies(t *testing.T) {
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-area-town", Name: "Area Knowledge", DisplayName: "Area Knowledge (Village or Town)", BaseName: "Area Knowledge", Specialization: "Village or Town"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-area-nation", Name: "Area Knowledge", DisplayName: "Area Knowledge (Large Nation)", BaseName: "Area Knowledge", Specialization: "Large Nation"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-politics", Name: "Politics", DisplayName: "Politics", BaseName: "Politics"},
	)

	terms := catalog.recommendedTermsForConcept("village nation politics", map[aiLibraryCategory]int{
		aiLibraryCategorySkill: 2,
	})

	if strings.Count(terms, "Area Knowledge (") != 1 {
		t.Fatalf("expected only one Area Knowledge recommendation, got %q", terms)
	}
	if !strings.Contains(terms, "Politics") {
		t.Fatalf("expected diversified skill recommendation to include Politics, got %q", terms)
	}
}

func TestGetThematicVocabularySplitsPerksFromAdvantages(t *testing.T) {
	previousCache := globalAILibraryCatalogCache
	globalAILibraryCatalogCache = aiLibraryCatalogCache{}
	t.Cleanup(func() {
		globalAILibraryCatalogCache = previousCache
	})
	libraries := gurps.GlobalSettings().Libraries()
	signature := aiLibraryCatalogSignature(libraries)

	globalAILibraryCatalogCache.catalog = newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryAdvantage, ID: "adv-signature", Name: "Signature Gear", DisplayName: "Signature Gear", BaseName: "Signature Gear", PointCost: 5},
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryAdvantage, ID: "perk-craft", Name: "Craftiness", DisplayName: "Craftiness", BaseName: "Craftiness", PointCost: 1},
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryDisadvantage, ID: "dis-overconfidence", Name: "Overconfidence", DisplayName: "Overconfidence", BaseName: "Overconfidence", PointCost: -5},
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryQuirk, ID: "quirk-tools", Name: "Keeps tools immaculate", DisplayName: "Keeps tools immaculate", BaseName: "Keeps tools immaculate", PointCost: -1},
		&aiLibraryCatalogEntry{Category: aiLibraryCategorySkill, ID: "skill-mechanic", Name: "Mechanic", DisplayName: "Mechanic (Automobile)", BaseName: "Mechanic", Specialization: "Automobile"},
	)
	globalAILibraryCatalogCache.signature = signature

	vocabulary := GetThematicVocabulary("mechanic", []string{"garage", "crafty", "overconfident"})
	checks := []string{
		"Thematic Canonical GURPS Vocabulary:",
		"Skills: Mechanic (Automobile)",
		"Advantages: Signature Gear",
		"Perks: Craftiness",
		"Disadvantages: Overconfidence",
		"Quirks: Keeps tools immaculate",
	}
	for _, check := range checks {
		if !strings.Contains(vocabulary, check) {
			t.Fatalf("expected thematic vocabulary to contain %q, got %q", check, vocabulary)
		}
	}
}

func TestAIResolveNamedActionUnresolvedLogsDescription(t *testing.T) {
	captured := captureResolverDebugLog(t)
	catalog := newTestAILibraryCatalog(
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryAdvantage, ID: "adv-signature", Name: "Signature Gear", DisplayName: "Signature Gear", BaseName: "Signature Gear"},
		&aiLibraryCatalogEntry{Category: aiLibraryCategoryAdvantage, ID: "adv-blessed", Name: "Blessed", DisplayName: "Blessed", BaseName: "Blessed"},
	)

	resolved, retryItem, warning := catalog.resolveNamedAction(aiLibraryCategoryAdvantage, aiNamedAction{
		Name:        aiFlexibleString("Soulbound Hammer"),
		Description: aiFlexibleString("Sentient relic"),
		Points:      aiFlexibleString("5"),
	})

	if resolved != nil {
		t.Fatalf("expected unresolved named action, got %#v", resolved)
	}
	if retryItem == nil {
		t.Fatal("expected retry item")
	}
	if retryItem.Description != "Sentient relic" {
		t.Fatalf("expected retry description to be preserved, got %#v", retryItem)
	}
	if warning == "" {
		t.Fatal("expected unresolved warning")
	}
	if len(*captured) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(*captured))
	}
	if got := (*captured)[0]; !strings.Contains(got, `description="Sentient relic"`) {
		t.Fatalf("expected unresolved log to contain description, got %q", got)
	}
}

func newTestAILibraryCatalog(entries ...*aiLibraryCatalogEntry) *aiLibraryCatalog {
	catalog := &aiLibraryCatalog{
		byCategory: map[aiLibraryCategory][]*aiLibraryCatalogEntry{},
		byID:       map[aiLibraryCategory]map[string]*aiLibraryCatalogEntry{},
	}
	for _, category := range aiLibraryCategories {
		catalog.byCategory[category] = nil
		catalog.byID[category] = map[string]*aiLibraryCatalogEntry{}
	}
	for _, entry := range entries {
		catalog.addEntry(entry)
	}
	return catalog
}

func captureResolverDebugLog(t *testing.T) *[]string {
	t.Helper()
	var lines []string
	previousLogWriter := aiResolverDebugLogWriter
	previousCounterWriter := aiResolverDebugCounterWriter
	aiResolverDebugLogWriter = func(line string) {
		lines = append(lines, strings.TrimSpace(line))
	}
	aiResolverDebugCounterWriter = nil
	t.Cleanup(func() {
		aiResolverDebugLogWriter = previousLogWriter
		aiResolverDebugCounterWriter = previousCounterWriter
	})
	return &lines
}
