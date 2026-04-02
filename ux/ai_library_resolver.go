package ux

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/richardwilkes/gcs/v5/model/gurps"
	"github.com/richardwilkes/gcs/v5/model/nameable"
	"github.com/richardwilkes/toolbox/v2/tid"
)

type aiLibraryCategory string

const (
	aiLibraryCategorySkill        aiLibraryCategory = "skills"
	aiLibraryCategoryAdvantage    aiLibraryCategory = "advantages"
	aiLibraryCategoryDisadvantage aiLibraryCategory = "disadvantages"
	aiLibraryCategoryQuirk        aiLibraryCategory = "quirks"
	aiLibraryCategoryEquipment    aiLibraryCategory = "equipment"
)

var aiLibraryCategories = []aiLibraryCategory{
	aiLibraryCategorySkill,
	aiLibraryCategoryAdvantage,
	aiLibraryCategoryDisadvantage,
	aiLibraryCategoryQuirk,
	aiLibraryCategoryEquipment,
}

const (
	aiResolverDebugLogFile     = "ai-debug-resolver.txt"
	aiResolverDebugSummaryFile = "ai-debug-resolver-counters.json"
)

var (
	aiResolverDebugLogLock       sync.Mutex
	aiResolverDebugLogWriter     = aiAppendResolverDebugLog
	aiResolverDebugCounterWriter = aiUpdateResolverDebugCounters
	aiNameableTemplatePattern    = regexp.MustCompile(`@[^@]+@`)
	aiConceptSearchStopWords     = map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "at": {}, "for": {}, "from": {}, "in": {}, "into": {},
		"of": {}, "on": {}, "or": {}, "the": {}, "to": {}, "with": {}, "using": {},
		"character": {}, "build": {}, "gurps": {}, "fourth": {}, "edition": {}, "point": {},
		"points": {}, "cp": {}, "tl": {},
	}
)

func (c aiLibraryCategory) String() string {
	switch c {
	case aiLibraryCategorySkill:
		return "Skills"
	case aiLibraryCategoryAdvantage:
		return "Advantages"
	case aiLibraryCategoryDisadvantage:
		return "Disadvantages"
	case aiLibraryCategoryQuirk:
		return "Quirks"
	case aiLibraryCategoryEquipment:
		return "Equipment"
	default:
		return string(c)
	}
}

type aiResolverDebugCounterState struct {
	Entries []aiResolverDebugCounterEntry `json:"entries,omitempty"`
}

type aiResolverDebugCounterEntry struct {
	Signature string   `json:"signature"`
	Kind      string   `json:"kind"`
	Count     int      `json:"count"`
	LastSeen  string   `json:"last_seen,omitempty"`
	Fields    []string `json:"fields,omitempty"`
}

type aiRetryCandidate struct {
	ID       string
	Name     string
	Requires []string
}

type aiRetryItem struct {
	Category    string
	Name        string
	Notes       string
	Description string
	Points      string
	Quantity    int
	Candidates  []aiRetryCandidate
	Similar     []string
}

type aiLibraryCatalogEntry struct {
	Category       aiLibraryCategory
	ID             string
	Name           string
	DisplayName    string
	BaseName       string
	Specialization string
	SourcePath     string
	LibraryFile    gurps.LibraryFile
	Nameables      []string
}

type aiLibraryCatalog struct {
	signature  string
	byCategory map[aiLibraryCategory][]*aiLibraryCatalogEntry
	byID       map[aiLibraryCategory]map[string]*aiLibraryCatalogEntry
}

type aiRankedCatalogEntry struct {
	Entry *aiLibraryCatalogEntry
	Score float64
}

type aiLibraryCatalogCache struct {
	lock      sync.Mutex
	signature string
	catalog   *aiLibraryCatalog
}

var globalAILibraryCatalogCache aiLibraryCatalogCache

func (c *aiLibraryCatalogCache) catalogFor(signature string, builder func() (*aiLibraryCatalog, error)) (*aiLibraryCatalog, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.catalog != nil && c.signature == signature {
		return c.catalog, nil
	}
	catalog, err := builder()
	if err != nil {
		return nil, err
	}
	c.signature = signature
	c.catalog = catalog
	return catalog, nil
}

func aiResolvedTraitName(trait *gurps.Trait) string {
	if trait == nil {
		return ""
	}
	return strings.TrimSpace(nameable.Apply(trait.Name, trait.NameableReplacements()))
}

func aiResolvedSkillName(skill *gurps.Skill) string {
	if skill == nil {
		return ""
	}
	return strings.TrimSpace(nameable.Apply(skill.Name, skill.NameableReplacements()))
}

func aiResolvedSkillSpecialization(skill *gurps.Skill) string {
	if skill == nil {
		return ""
	}
	return strings.TrimSpace(nameable.Apply(skill.Specialization, skill.NameableReplacements()))
}

func aiResolvedSkillDisplayName(skill *gurps.Skill) string {
	return skillDisplayName(aiResolvedSkillName(skill), aiResolvedSkillSpecialization(skill))
}

func aiResolvedEquipmentName(equipment *gurps.Equipment) string {
	if equipment == nil {
		return ""
	}
	return strings.TrimSpace(nameable.Apply(equipment.Name, equipment.NameableReplacements()))
}

func aiSortedNameableKeys(keys map[string]string) []string {
	if len(keys) == 0 {
		return nil
	}
	sorted := make([]string, 0, len(keys))
	for key := range keys {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)
	return sorted
}

func aiCategoryJSONField(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "skill", string(aiLibraryCategorySkill):
		return string(aiLibraryCategorySkill)
	case "advantage", string(aiLibraryCategoryAdvantage):
		return string(aiLibraryCategoryAdvantage)
	case "disadvantage", string(aiLibraryCategoryDisadvantage):
		return string(aiLibraryCategoryDisadvantage)
	case "quirk", string(aiLibraryCategoryQuirk):
		return string(aiLibraryCategoryQuirk)
	case "equipment":
		return string(aiLibraryCategoryEquipment)
	default:
		return strings.ToLower(strings.TrimSpace(category))
	}
}

func aiCategorySingular(category string) string {
	switch aiCategoryJSONField(category) {
	case string(aiLibraryCategorySkill):
		return "skill"
	case string(aiLibraryCategoryAdvantage):
		return "advantage"
	case string(aiLibraryCategoryDisadvantage):
		return "disadvantage"
	case string(aiLibraryCategoryQuirk):
		return "quirk"
	case string(aiLibraryCategoryEquipment):
		return "equipment"
	default:
		return strings.TrimSpace(category)
	}
}

func aiAppendResolverDebugLog(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	file, err := os.OpenFile(aiResolverDebugLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s | %s\n", time.Now().UTC().Format(time.RFC3339Nano), line)
}

func aiWriteResolverDebugLog(kind string, fields ...string) {
	kind = strings.TrimSpace(kind)
	parts := make([]string, 0, len(fields)+1)
	parts = append(parts, kind)
	cleanedFields := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		cleanedFields = append(cleanedFields, field)
		parts = append(parts, field)
	}
	aiResolverDebugLogLock.Lock()
	defer aiResolverDebugLogLock.Unlock()
	if aiResolverDebugCounterWriter != nil {
		aiResolverDebugCounterWriter(kind, append([]string(nil), cleanedFields...))
	}
	if aiResolverDebugLogWriter != nil {
		aiResolverDebugLogWriter(strings.Join(parts, " | "))
	}
}

func aiResolverDebugEventSignature(kind string, fields []string) string {
	parts := make([]string, 0, len(fields)+1)
	parts = append(parts, strings.TrimSpace(kind))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			parts = append(parts, field)
		}
	}
	return strings.Join(parts, "\x1f")
}

func aiSortResolverDebugCounterEntries(entries []aiResolverDebugCounterEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Signature < entries[j].Signature
	})
}

func aiApplyResolverDebugCounterEvent(state *aiResolverDebugCounterState, kind string, fields []string, now time.Time) {
	if state == nil {
		return
	}
	signature := aiResolverDebugEventSignature(kind, fields)
	lastSeen := now.UTC().Format(time.RFC3339Nano)
	for i := range state.Entries {
		if state.Entries[i].Signature != signature {
			continue
		}
		state.Entries[i].Count++
		state.Entries[i].LastSeen = lastSeen
		aiSortResolverDebugCounterEntries(state.Entries)
		return
	}
	state.Entries = append(state.Entries, aiResolverDebugCounterEntry{
		Signature: signature,
		Kind:      strings.TrimSpace(kind),
		Count:     1,
		LastSeen:  lastSeen,
		Fields:    append([]string(nil), fields...),
	})
	aiSortResolverDebugCounterEntries(state.Entries)
}

func aiLoadResolverDebugCounterState() aiResolverDebugCounterState {
	var state aiResolverDebugCounterState
	data, err := os.ReadFile(aiResolverDebugSummaryFile)
	if err != nil {
		return state
	}
	if err = json.Unmarshal(data, &state); err != nil {
		return aiResolverDebugCounterState{}
	}
	aiSortResolverDebugCounterEntries(state.Entries)
	return state
}

func aiSaveResolverDebugCounterState(state aiResolverDebugCounterState) {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(aiResolverDebugSummaryFile, data, 0o644)
}

func aiUpdateResolverDebugCounters(kind string, fields []string) {
	state := aiLoadResolverDebugCounterState()
	aiApplyResolverDebugCounterEvent(&state, kind, fields, time.Now())
	aiSaveResolverDebugCounterState(state)
}

func aiClearResolverDebugTelemetry() error {
	var failures []string
	for _, one := range []string{aiResolverDebugLogFile, aiResolverDebugSummaryFile} {
		if err := os.Remove(one); err != nil && !os.IsNotExist(err) {
			failures = append(failures, fmt.Sprintf("%s: %v", one, err))
		}
	}
	if len(failures) != 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

func aiFormatResolverCandidates(ranked []aiRankedCatalogEntry) string {
	if len(ranked) == 0 {
		return "[]"
	}
	limit := min(len(ranked), 5)
	parts := make([]string, 0, limit)
	for _, rankedEntry := range ranked[:limit] {
		parts = append(parts, fmt.Sprintf("%s:%s", rankedEntry.Entry.ID, rankedEntry.Entry.DisplayName))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func aiLogAliasHit(category aiLibraryCategory, input, mapped string) {
	aiWriteResolverDebugLog("alias-hit",
		fmt.Sprintf("category=%s", aiCategoryJSONField(string(category))),
		fmt.Sprintf("input=%q", strings.TrimSpace(input)),
		fmt.Sprintf("mapped=%q", strings.TrimSpace(mapped)),
	)
}

func aiLogUnresolvedIntent(category aiLibraryCategory, originalIntent, lookupIntent, notes, description, points string, quantity int, ranked []aiRankedCatalogEntry) {
	fields := []string{
		fmt.Sprintf("category=%s", aiCategoryJSONField(string(category))),
		fmt.Sprintf("input=%q", strings.TrimSpace(originalIntent)),
	}
	lookupIntent = strings.TrimSpace(lookupIntent)
	if lookupIntent != "" && !strings.EqualFold(strings.TrimSpace(originalIntent), lookupIntent) {
		fields = append(fields, fmt.Sprintf("lookup=%q", lookupIntent))
	}
	if trimmedNotes := strings.TrimSpace(notes); trimmedNotes != "" {
		fields = append(fields, fmt.Sprintf("notes=%q", trimmedNotes))
	}
	if trimmedDescription := strings.TrimSpace(description); trimmedDescription != "" {
		fields = append(fields, fmt.Sprintf("description=%q", trimmedDescription))
	}
	if trimmedPoints := strings.TrimSpace(points); trimmedPoints != "" {
		fields = append(fields, fmt.Sprintf("points=%q", trimmedPoints))
	}
	if quantity != 0 {
		fields = append(fields, fmt.Sprintf("quantity=%d", quantity))
	}
	fields = append(fields, fmt.Sprintf("candidates=%s", aiFormatResolverCandidates(ranked)))
	aiWriteResolverDebugLog("unresolved", fields...)
}

func aiResolveAlias(category aiLibraryCategory, intent string) string {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return ""
	}
	aliases, ok := gurps.GlobalSettings().AI.ResolverAliases[string(category)]
	if !ok {
		return intent
	}
	if mapped, ok := aliases[strings.ToLower(intent)]; ok {
		aiLogAliasHit(category, intent, mapped)
		return mapped
	}
	return intent
}

func aiDisplayNameWithNotes(entry *aiLibraryCatalogEntry, notes string) string {
	if entry == nil {
		return ""
	}
	displayName := strings.TrimSpace(entry.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(entry.Name)
	}
	notes = strings.TrimSpace(notes)
	if notes == "" || len(entry.Nameables) == 0 {
		return displayName
	}
	return fmt.Sprintf("%s (%s)", displayName, notes)
}

func aiLookupBaseName(name string) string {
	base, _ := splitSkillNameAndSpecialization(name)
	base = traitBaseNameForLookup(base)
	if strings.TrimSpace(base) != "" {
		return strings.TrimSpace(base)
	}
	return strings.TrimSpace(traitBaseNameForLookup(name))
}

func aiCatalogEntryDisplayName(name, specialization string) string {
	return strings.TrimSpace(traitBaseNameForLookup(skillDisplayName(name, specialization)))
}

func aiCandidateSpecNorm(entry *aiLibraryCatalogEntry) string {
	if entry == nil {
		return ""
	}
	if strings.TrimSpace(entry.Specialization) != "" {
		return normalizeLookupText(entry.Specialization)
	}
	_, spec := splitSkillNameAndSpecialization(entry.DisplayName)
	return normalizeLookupText(spec)
}

func aiLibraryCatalogSignature(libraries gurps.Libraries) string {
	if libraries == nil {
		return ""
	}
	var builder strings.Builder
	for _, ext := range []string{gurps.SkillsExt, gurps.TraitsExt, gurps.EquipmentExt} {
		for _, set := range scanNamedFileSetsWithFallback(libraries, ext) {
			libraryKey := libraryKeyForSetName(libraries, set.Name)
			library := libraries[libraryKey]
			for _, ref := range set.List {
				builder.WriteString(ext)
				builder.WriteByte('|')
				builder.WriteString(libraryKey)
				builder.WriteByte('|')
				builder.WriteString(ref.FilePath)
				builder.WriteByte('|')
				if library == nil {
					builder.WriteString("missing-library")
					builder.WriteByte('\n')
					continue
				}
				fullPath := filepath.Join(library.Path(), filepath.FromSlash(ref.FilePath))
				info, err := os.Stat(fullPath)
				if err != nil {
					builder.WriteString("missing-file")
					builder.WriteByte('\n')
					continue
				}
				builder.WriteString(info.ModTime().UTC().Format(time.RFC3339Nano))
				builder.WriteByte('|')
				builder.WriteString(fmt.Sprintf("%d", info.Size()))
				builder.WriteByte('\n')
			}
		}
	}
	return builder.String()
}

func aiConceptSearchTokens(concept string) []string {
	concept = strings.TrimSpace(strings.ToLower(concept))
	if concept == "" {
		return nil
	}
	var tokenBuilder strings.Builder
	tokens := make([]string, 0, 8)
	seen := make(map[string]struct{}, 8)
	flushToken := func() {
		if tokenBuilder.Len() == 0 {
			return
		}
		token := canonicalizeSkillLookupToken(tokenBuilder.String())
		tokenBuilder.Reset()
		if len(token) < 2 || isTLToken(token) {
			return
		}
		if _, stopWord := aiConceptSearchStopWords[token]; stopWord {
			return
		}
		if _, exists := seen[token]; exists {
			return
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	for _, r := range concept {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			tokenBuilder.WriteRune(r)
			continue
		}
		flushToken()
	}
	flushToken()
	return tokens
}

func aiConceptEntryScore(entry *aiLibraryCatalogEntry, conceptNorm string, tokens []string) float64 {
	if entry == nil {
		return 0
	}
	displayNorm := normalizeLookupText(entry.DisplayName)
	baseNorm := normalizeLookupText(entry.BaseName)
	specNorm := aiCandidateSpecNorm(entry)
	if displayNorm == "" && baseNorm == "" && specNorm == "" {
		return 0
	}
	score := 0.0
	if conceptNorm != "" {
		if displayNorm != "" {
			score += aiNormalizedSimilarity(conceptNorm, displayNorm) * 4
			if strings.Contains(displayNorm, conceptNorm) || strings.Contains(conceptNorm, displayNorm) {
				score += 10
			}
		}
		if baseNorm != "" && baseNorm != displayNorm {
			score += aiNormalizedSimilarity(conceptNorm, baseNorm) * 2
		}
	}
	for _, token := range tokens {
		tokenScore := 0.0
		if displayNorm != "" {
			if strings.Contains(displayNorm, token) {
				tokenScore += 7
			}
			if token == displayNorm {
				tokenScore += 5
			}
		}
		if baseNorm != "" {
			if strings.Contains(baseNorm, token) {
				tokenScore += 5
			}
			if token == baseNorm {
				tokenScore += 4
			}
		}
		if specNorm != "" {
			if strings.Contains(specNorm, token) {
				tokenScore += 4
			}
			if token == specNorm {
				tokenScore += 3
			}
		}
		score += tokenScore
	}
	return score
}

func (c *aiLibraryCatalog) searchConceptEntries(concept string, limits map[aiLibraryCategory]int) map[aiLibraryCategory][]*aiLibraryCatalogEntry {
	results := make(map[aiLibraryCategory][]*aiLibraryCatalogEntry, len(limits))
	if c == nil || len(limits) == 0 {
		return results
	}
	conceptNorm := normalizeLookupText(concept)
	tokens := aiConceptSearchTokens(concept)
	if conceptNorm == "" && len(tokens) == 0 {
		return results
	}
	for category, limit := range limits {
		if limit <= 0 {
			continue
		}
		ranked := make([]aiRankedCatalogEntry, 0, len(c.byCategory[category]))
		seen := make(map[string]struct{}, len(c.byCategory[category]))
		for _, entry := range c.byCategory[category] {
			if entry == nil {
				continue
			}
			semanticKey := aiCatalogEntrySemanticKey(entry)
			if _, exists := seen[semanticKey]; exists {
				continue
			}
			seen[semanticKey] = struct{}{}
			score := aiConceptEntryScore(entry, conceptNorm, tokens)
			if score <= 0 {
				continue
			}
			ranked = append(ranked, aiRankedCatalogEntry{Entry: entry, Score: score})
		}
		sort.Slice(ranked, func(i, j int) bool {
			if ranked[i].Score == ranked[j].Score {
				if ranked[i].Entry.DisplayName == ranked[j].Entry.DisplayName {
					return ranked[i].Entry.ID < ranked[j].Entry.ID
				}
				return ranked[i].Entry.DisplayName < ranked[j].Entry.DisplayName
			}
			return ranked[i].Score > ranked[j].Score
		})
		ranked = aiSelectConceptRecommendationEntries(category, ranked, limit)
		entries := make([]*aiLibraryCatalogEntry, 0, len(ranked))
		for _, rankedEntry := range ranked {
			entries = append(entries, rankedEntry.Entry)
		}
		if len(entries) != 0 {
			results[category] = entries
		}
	}
	return results
}

func aiSelectConceptRecommendationEntries(category aiLibraryCategory, ranked []aiRankedCatalogEntry, limit int) []aiRankedCatalogEntry {
	if limit <= 0 || len(ranked) == 0 {
		return nil
	}
	if len(ranked) <= limit {
		return ranked
	}
	groupLimit := aiConceptRecommendationGroupLimit(category)
	if groupLimit <= 0 {
		return ranked[:limit]
	}
	selected := make([]aiRankedCatalogEntry, 0, limit)
	overflow := make([]aiRankedCatalogEntry, 0, len(ranked))
	groupCounts := make(map[string]int, len(ranked))
	for _, rankedEntry := range ranked {
		groupKey := aiConceptRecommendationGroupKey(category, rankedEntry.Entry)
		if groupKey != "" && groupCounts[groupKey] >= groupLimit {
			overflow = append(overflow, rankedEntry)
			continue
		}
		selected = append(selected, rankedEntry)
		if groupKey != "" {
			groupCounts[groupKey]++
		}
		if len(selected) == limit {
			return selected
		}
	}
	for _, rankedEntry := range overflow {
		selected = append(selected, rankedEntry)
		if len(selected) == limit {
			break
		}
	}
	return selected
}

func aiConceptRecommendationGroupLimit(category aiLibraryCategory) int {
	switch category {
	case aiLibraryCategorySkill:
		return 1
	default:
		return 0
	}
}

func aiConceptRecommendationGroupKey(category aiLibraryCategory, entry *aiLibraryCatalogEntry) string {
	if entry == nil {
		return ""
	}
	switch category {
	case aiLibraryCategorySkill:
		if base := normalizeLookupText(entry.BaseName); base != "" {
			return base
		}
	}
	return ""
}

func (c *aiLibraryCatalog) recommendedTermsForConcept(concept string, limits map[aiLibraryCategory]int) string {
	results := c.searchConceptEntries(concept, limits)
	if len(results) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Recommended Canonical GURPS Terms:\n")
	for _, category := range aiLibraryCategories {
		entries := results[category]
		if len(entries) == 0 {
			continue
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry == nil {
				continue
			}
			names = append(names, entry.DisplayName)
		}
		if len(names) == 0 {
			continue
		}
		builder.WriteString("- ")
		builder.WriteString(category.String())
		builder.WriteString(": ")
		builder.WriteString(strings.Join(names, ", "))
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String())
}

func buildAILibraryCatalog(libraries gurps.Libraries, signature string) (*aiLibraryCatalog, error) {
	if libraries == nil {
		return nil, fmt.Errorf("no libraries loaded")
	}
	catalog := &aiLibraryCatalog{
		signature: signature,
		byCategory: map[aiLibraryCategory][]*aiLibraryCatalogEntry{
			aiLibraryCategorySkill:        nil,
			aiLibraryCategoryAdvantage:    nil,
			aiLibraryCategoryDisadvantage: nil,
			aiLibraryCategoryQuirk:        nil,
			aiLibraryCategoryEquipment:    nil,
		},
		byID: map[aiLibraryCategory]map[string]*aiLibraryCatalogEntry{
			aiLibraryCategorySkill:        make(map[string]*aiLibraryCatalogEntry),
			aiLibraryCategoryAdvantage:    make(map[string]*aiLibraryCatalogEntry),
			aiLibraryCategoryDisadvantage: make(map[string]*aiLibraryCatalogEntry),
			aiLibraryCategoryQuirk:        make(map[string]*aiLibraryCatalogEntry),
			aiLibraryCategoryEquipment:    make(map[string]*aiLibraryCatalogEntry),
		},
	}

	for _, set := range scanNamedFileSetsWithFallback(libraries, gurps.SkillsExt) {
		for _, ref := range set.List {
			rows, err := gurps.NewSkillsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, skill := range rows {
				if skill == nil || skill.Container() || strings.TrimSpace(skill.Name) == "" {
					continue
				}
				keys := make(map[string]string)
				skill.FillWithNameableKeys(keys, nil)
				catalog.addEntry(&aiLibraryCatalogEntry{
					Category:       aiLibraryCategorySkill,
					ID:             string(skill.TID),
					Name:           strings.TrimSpace(skill.Name),
					DisplayName:    aiCatalogEntryDisplayName(skill.Name, skill.Specialization),
					BaseName:       aiLookupBaseName(skill.Name),
					Specialization: strings.TrimSpace(skill.Specialization),
					SourcePath:     ref.FilePath,
					LibraryFile:    libraryFileForSet(set.Name, ref.FilePath),
					Nameables:      aiSortedNameableKeys(keys),
				})
			}
		}
	}

	for _, set := range scanNamedFileSetsWithFallback(libraries, gurps.TraitsExt) {
		for _, ref := range set.List {
			rows, err := gurps.NewTraitsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, trait := range rows {
				if trait == nil || trait.Container() || strings.TrimSpace(trait.Name) == "" {
					continue
				}
				points := trait.AdjustedPoints()
				category := aiLibraryCategoryDisadvantage
				if points > 0 {
					category = aiLibraryCategoryAdvantage
				} else if strings.Contains(strings.ToLower(trait.Name), "quirk") || strings.Contains(strings.ToLower(strings.Join(trait.Tags, " ")), "quirk") {
					category = aiLibraryCategoryQuirk
				}
				keys := make(map[string]string)
				trait.FillWithNameableKeys(keys, nil)
				catalog.addEntry(&aiLibraryCatalogEntry{
					Category:       category,
					ID:             string(trait.TID),
					Name:           strings.TrimSpace(trait.Name),
					DisplayName:    strings.TrimSpace(traitBaseNameForLookup(trait.Name)),
					BaseName:       strings.TrimSpace(traitBaseNameForLookup(trait.Name)),
					Specialization: "",
					SourcePath:     ref.FilePath,
					LibraryFile:    libraryFileForSet(set.Name, ref.FilePath),
					Nameables:      aiSortedNameableKeys(keys),
				})
			}
		}
	}

	for _, set := range scanNamedFileSetsWithFallback(libraries, gurps.EquipmentExt) {
		for _, ref := range set.List {
			rows, err := gurps.NewEquipmentFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, equipment := range rows {
				if equipment == nil || equipment.Container() || strings.TrimSpace(equipment.Name) == "" {
					continue
				}
				keys := make(map[string]string)
				equipment.FillWithNameableKeys(keys, nil)
				catalog.addEntry(&aiLibraryCatalogEntry{
					Category:       aiLibraryCategoryEquipment,
					ID:             string(equipment.TID),
					Name:           strings.TrimSpace(equipment.Name),
					DisplayName:    strings.TrimSpace(traitBaseNameForLookup(equipment.Name)),
					BaseName:       strings.TrimSpace(traitBaseNameForLookup(equipment.Name)),
					Specialization: "",
					SourcePath:     ref.FilePath,
					LibraryFile:    libraryFileForSet(set.Name, ref.FilePath),
					Nameables:      aiSortedNameableKeys(keys),
				})
			}
		}
	}

	for _, category := range aiLibraryCategories {
		sort.Slice(catalog.byCategory[category], func(i, j int) bool {
			left := catalog.byCategory[category][i]
			right := catalog.byCategory[category][j]
			if left.DisplayName != right.DisplayName {
				return left.DisplayName < right.DisplayName
			}
			if left.Specialization != right.Specialization {
				return left.Specialization < right.Specialization
			}
			return left.ID < right.ID
		})
	}
	return catalog, nil
}

func (c *aiLibraryCatalog) addEntry(entry *aiLibraryCatalogEntry) {
	if c == nil || entry == nil || strings.TrimSpace(entry.ID) == "" {
		return
	}
	entry.Name = strings.TrimSpace(entry.Name)
	entry.DisplayName = strings.TrimSpace(entry.DisplayName)
	if entry.DisplayName == "" {
		entry.DisplayName = entry.Name
	}
	entry.BaseName = strings.TrimSpace(entry.BaseName)
	if entry.BaseName == "" {
		entry.BaseName = aiLookupBaseName(entry.DisplayName)
	}
	entry.Specialization = strings.TrimSpace(entry.Specialization)
	c.byCategory[entry.Category] = append(c.byCategory[entry.Category], entry)
	c.byID[entry.Category][entry.ID] = entry
}

func (d *aiChatDockable) aiLibraryCatalog() (*aiLibraryCatalog, error) {
	libraries := gurps.GlobalSettings().Libraries()
	signature := aiLibraryCatalogSignature(libraries)
	return globalAILibraryCatalogCache.catalogFor(signature, func() (*aiLibraryCatalog, error) {
		return buildAILibraryCatalog(libraries, signature)
	})
}

func (c *aiLibraryCatalog) resolveAIActionPlan(plan aiActionPlan) (aiActionPlan, []aiRetryItem, []string) {
	resolved := aiActionPlan{
		Profile:    plan.Profile,
		Attributes: append([]aiAttributeAction(nil), plan.Attributes...),
		SpendAllCP: plan.SpendAllCP,
	}
	var retryItems []aiRetryItem
	var warnings []string

	for _, action := range plan.Advantages {
		resolvedAction, retryItem, warning := c.resolveNamedAction(aiLibraryCategoryAdvantage, action)
		if resolvedAction != nil {
			resolved.Advantages = append(resolved.Advantages, *resolvedAction)
		}
		if retryItem != nil {
			retryItems = append(retryItems, *retryItem)
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	for _, action := range plan.Disadvantages {
		resolvedAction, retryItem, warning := c.resolveNamedAction(aiLibraryCategoryDisadvantage, action)
		if resolvedAction != nil {
			resolved.Disadvantages = append(resolved.Disadvantages, *resolvedAction)
		}
		if retryItem != nil {
			retryItems = append(retryItems, *retryItem)
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	for _, action := range plan.Quirks {
		resolvedAction, retryItem, warning := c.resolveNamedAction(aiLibraryCategoryQuirk, action)
		if resolvedAction != nil {
			resolved.Quirks = append(resolved.Quirks, *resolvedAction)
		}
		if retryItem != nil {
			retryItems = append(retryItems, *retryItem)
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	for _, action := range plan.Skills {
		resolvedAction, retryItem, warning := c.resolveSkillAction(action)
		if resolvedAction != nil {
			resolved.Skills = append(resolved.Skills, *resolvedAction)
		}
		if retryItem != nil {
			retryItems = append(retryItems, *retryItem)
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	for _, action := range plan.Equipment {
		resolvedAction, retryItem, warning := c.resolveNamedAction(aiLibraryCategoryEquipment, action)
		if resolvedAction != nil {
			resolved.Equipment = append(resolved.Equipment, *resolvedAction)
		}
		if retryItem != nil {
			retryItems = append(retryItems, *retryItem)
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	return resolved, retryItems, warnings
}

func (c *aiLibraryCatalog) resolveNamedAction(category aiLibraryCategory, action aiNamedAction) (*aiNamedAction, *aiRetryItem, string) {
	rawName := action.Name.String()
	idStr := normalizeAISelectionID(action.ID.String())
	useTIDLookup := idStr != "" && tid.IsValid(tid.TID(idStr))
	name := normalizeAINamedItemName(rawName)
	if name == "" && idStr != "" && !useTIDLookup {
		name = normalizeAINamedItemName(idStr)
	}
	originalName := name
	if name != "" {
		name = aiResolveAlias(category, name)
	}
	if useTIDLookup {
		if entry := c.byID[category][idStr]; entry != nil {
			resolved := action
			resolved.ID = aiFlexibleString(entry.ID)
			resolved.Notes = aiFlexibleString(aiResolvedNotes(action.Notes.String(), rawName, entry))
			resolved.Name = aiFlexibleString(aiDisplayNameWithNotes(entry, resolved.Notes.String()))
			return &resolved, nil, ""
		}
	}
	if name == "" {
		warning := fmt.Sprintf("Warning: %s action is missing a usable name and was skipped.", aiCategorySingular(string(category)))
		return nil, nil, warning
	}

	if entry := c.exactMatch(category, name); entry != nil {
		resolved := action
		resolved.ID = aiFlexibleString(entry.ID)
		resolved.Notes = aiFlexibleString(aiResolvedNotes(action.Notes.String(), rawName, entry))
		resolved.Name = aiFlexibleString(aiDisplayNameWithNotes(entry, resolved.Notes.String()))
		return &resolved, nil, ""
	}

	requestedBase, requestedSpec := splitSkillNameAndSpecialization(name)
	requestedBaseNorm := normalizeLookupText(aiLookupBaseName(requestedBase))
	requestedSpecNorm := normalizeLookupText(requestedSpec)
	if requestedBaseNorm != "" {
		baseMatches := c.baseMatches(category, requestedBaseNorm)
		if requestedSpecNorm != "" {
			if entry := aiUniqueNameableMatch(baseMatches); entry != nil {
				resolved := action
				resolved.ID = aiFlexibleString(entry.ID)
				resolved.Notes = aiFlexibleString(aiResolvedNotes(action.Notes.String(), rawName, entry))
				resolved.Name = aiFlexibleString(aiDisplayNameWithNotes(entry, resolved.Notes.String()))
				return &resolved, nil, ""
			}
		}
		if requestedSpecNorm == "" && len(baseMatches) == 1 && strings.TrimSpace(baseMatches[0].Specialization) == "" {
			resolved := action
			resolved.ID = aiFlexibleString(baseMatches[0].ID)
			resolved.Notes = aiFlexibleString(aiResolvedNotes(action.Notes.String(), rawName, baseMatches[0]))
			resolved.Name = aiFlexibleString(aiDisplayNameWithNotes(baseMatches[0], resolved.Notes.String()))
			return &resolved, nil, ""
		}
	}

	if entry, derivedNotes := c.templateNameableMatch(category, rawName); entry != nil {
		resolved := action
		resolved.ID = aiFlexibleString(entry.ID)
		if strings.TrimSpace(resolved.Notes.String()) == "" {
			resolved.Notes = aiFlexibleString(derivedNotes)
		}
		resolved.Notes = aiFlexibleString(aiResolvedNotes(resolved.Notes.String(), rawName, entry))
		resolved.Name = aiFlexibleString(aiDisplayNameWithNotes(entry, resolved.Notes.String()))
		return &resolved, nil, ""
	}

	ranked := c.rankCandidates(category, name)
	if entry := aiAutoselectCandidate(category, name, ranked); entry != nil {
		resolved := action
		resolved.ID = aiFlexibleString(entry.ID)
		resolved.Notes = aiFlexibleString(aiResolvedNotes(action.Notes.String(), rawName, entry))
		resolved.Name = aiFlexibleString(aiDisplayNameWithNotes(entry, resolved.Notes.String()))
		return &resolved, nil, ""
	}
	aiLogUnresolvedIntent(category, originalName, name, action.Notes.String(), action.Description.String(), action.Points.String(), action.Quantity.Int(), ranked)
	retry := aiBuildRetryItem(string(category), name, action.Notes.String(), action.Description.String(), action.Points.String(), action.Quantity.Int(), ranked)
	warning := fmt.Sprintf("Warning: %s %q could not be resolved to an exact library entry and is waiting for correction.", aiCategorySingular(string(category)), name)
	return nil, retry, warning
}

func (c *aiLibraryCatalog) resolveSkillAction(action aiSkillAction) (*aiSkillAction, *aiRetryItem, string) {
	rawName := action.Name.String()
	idStr := normalizeAISelectionID(action.ID.String())
	useTIDLookup := idStr != "" && tid.IsValid(tid.TID(idStr))
	name := normalizeAISkillName(rawName)
	if name == "" && idStr != "" && !useTIDLookup {
		name = normalizeAISkillName(idStr)
	}
	originalName := name
	if name != "" {
		name = aiResolveAlias(aiLibraryCategorySkill, name)
	}
	if useTIDLookup {
		if entry := c.byID[aiLibraryCategorySkill][idStr]; entry != nil {
			resolved := action
			resolved.ID = aiFlexibleString(entry.ID)
			resolved.Notes = aiFlexibleString(aiResolvedNotes(action.Notes.String(), rawName, entry))
			resolved.Name = aiFlexibleString(aiDisplayNameWithNotes(entry, resolved.Notes.String()))
			return &resolved, nil, ""
		}
	}
	if name == "" {
		return nil, nil, "Warning: skill action is missing a usable name and was skipped."
	}

	if entry := c.exactMatch(aiLibraryCategorySkill, name); entry != nil {
		resolved := action
		resolved.ID = aiFlexibleString(entry.ID)
		resolved.Notes = aiFlexibleString(aiResolvedNotes(action.Notes.String(), rawName, entry))
		resolved.Name = aiFlexibleString(aiDisplayNameWithNotes(entry, resolved.Notes.String()))
		return &resolved, nil, ""
	}

	requestedBase, requestedSpec := splitSkillNameAndSpecialization(name)
	requestedBaseNorm := normalizeLookupText(aiLookupBaseName(requestedBase))
	requestedSpecNorm := normalizeLookupText(requestedSpec)
	if requestedBaseNorm != "" {
		baseMatches := c.baseMatches(aiLibraryCategorySkill, requestedBaseNorm)
		if requestedSpecNorm != "" {
			if entry := aiUniqueNameableMatch(baseMatches); entry != nil {
				resolved := action
				resolved.ID = aiFlexibleString(entry.ID)
				resolved.Notes = aiFlexibleString(aiResolvedNotes(action.Notes.String(), rawName, entry))
				resolved.Name = aiFlexibleString(aiDisplayNameWithNotes(entry, resolved.Notes.String()))
				return &resolved, nil, ""
			}
		}
		if requestedSpecNorm == "" && len(baseMatches) == 1 && strings.TrimSpace(baseMatches[0].Specialization) == "" {
			resolved := action
			resolved.ID = aiFlexibleString(baseMatches[0].ID)
			resolved.Notes = aiFlexibleString(aiResolvedNotes(action.Notes.String(), rawName, baseMatches[0]))
			resolved.Name = aiFlexibleString(aiDisplayNameWithNotes(baseMatches[0], resolved.Notes.String()))
			return &resolved, nil, ""
		}
	}

	ranked := c.rankCandidates(aiLibraryCategorySkill, name)
	if entry := aiAutoselectCandidate(aiLibraryCategorySkill, name, ranked); entry != nil {
		resolved := action
		resolved.ID = aiFlexibleString(entry.ID)
		resolved.Notes = aiFlexibleString(aiResolvedNotes(action.Notes.String(), rawName, entry))
		resolved.Name = aiFlexibleString(aiDisplayNameWithNotes(entry, resolved.Notes.String()))
		return &resolved, nil, ""
	}
	aiLogUnresolvedIntent(aiLibraryCategorySkill, originalName, name, action.Notes.String(), action.Description.String(), firstNonEmptyString(action.Points.String(), action.Value.String()), 0, ranked)
	retry := aiBuildRetryItem(string(aiLibraryCategorySkill), name, action.Notes.String(), action.Description.String(), firstNonEmptyString(action.Points.String(), action.Value.String()), 0, ranked)
	warning := fmt.Sprintf("Warning: skill %q could not be resolved to an exact library entry and is waiting for correction.", name)
	return nil, retry, warning
}

func aiUniqueNameableMatch(entries []*aiLibraryCatalogEntry) *aiLibraryCatalogEntry {
	entries = aiDistinctCatalogEntries(entries)
	var match *aiLibraryCatalogEntry
	for _, entry := range entries {
		if len(entry.Nameables) == 0 {
			continue
		}
		if match != nil {
			return nil
		}
		match = entry
	}
	return match
}

func aiDistinctCatalogEntries(entries []*aiLibraryCatalogEntry) []*aiLibraryCatalogEntry {
	if len(entries) < 2 {
		return entries
	}
	seen := make(map[string]struct{}, len(entries))
	distinct := make([]*aiLibraryCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		key := aiCatalogEntrySemanticKey(entry)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		distinct = append(distinct, entry)
	}
	return distinct
}

func aiCatalogEntrySemanticKey(entry *aiLibraryCatalogEntry) string {
	if entry == nil {
		return ""
	}
	nameables := make([]string, 0, len(entry.Nameables))
	for _, nameable := range entry.Nameables {
		if normalized := normalizeLookupText(nameable); normalized != "" {
			nameables = append(nameables, normalized)
		}
	}
	sort.Strings(nameables)
	return strings.Join([]string{
		string(entry.Category),
		normalizeLookupText(entry.Name),
		normalizeLookupText(entry.DisplayName),
		normalizeLookupText(entry.BaseName),
		normalizeLookupText(entry.Specialization),
		strings.Join(nameables, ","),
	}, "|")
}

func (c *aiLibraryCatalog) exactMatch(category aiLibraryCategory, requestedName string) *aiLibraryCatalogEntry {
	requestedName = strings.TrimSpace(requestedName)
	if requestedName == "" {
		return nil
	}
	requestedNorm := normalizeLookupText(requestedName)
	requestedBaseNorm := normalizeLookupText(aiLookupBaseName(requestedName))
	var exact []*aiLibraryCatalogEntry
	var baseOnly []*aiLibraryCatalogEntry
	for _, entry := range c.byCategory[category] {
		entryDisplayNorm := normalizeLookupText(entry.DisplayName)
		entryNameNorm := normalizeLookupText(entry.Name)
		if strings.EqualFold(entry.DisplayName, requestedName) || strings.EqualFold(entry.Name, requestedName) || (requestedNorm != "" && (entryDisplayNorm == requestedNorm || entryNameNorm == requestedNorm)) {
			exact = append(exact, entry)
			continue
		}
		if requestedBaseNorm != "" && normalizeLookupText(entry.BaseName) == requestedBaseNorm && len(entry.Nameables) > 0 {
			baseOnly = append(baseOnly, entry)
		}
	}
	exact = aiDistinctCatalogEntries(exact)
	baseOnly = aiDistinctCatalogEntries(baseOnly)
	if len(exact) == 1 {
		return exact[0]
	}
	if len(exact) == 0 && len(baseOnly) == 1 {
		return baseOnly[0]
	}
	return nil
}

func (c *aiLibraryCatalog) baseMatches(category aiLibraryCategory, requestedBaseNorm string) []*aiLibraryCatalogEntry {
	if requestedBaseNorm == "" {
		return nil
	}
	var matches []*aiLibraryCatalogEntry
	for _, entry := range c.byCategory[category] {
		if normalizeLookupText(entry.BaseName) == requestedBaseNorm {
			matches = append(matches, entry)
		}
	}
	return aiDistinctCatalogEntries(matches)
}

func (c *aiLibraryCatalog) rankCandidates(category aiLibraryCategory, requestedName string) []aiRankedCatalogEntry {
	requestedName = strings.TrimSpace(requestedName)
	if requestedName == "" {
		return nil
	}
	requestedNorm := normalizeLookupText(requestedName)
	requestedBase, requestedSpec := splitSkillNameAndSpecialization(requestedName)
	requestedBaseNorm := normalizeLookupText(aiLookupBaseName(requestedBase))
	requestedSpecNorm := normalizeLookupText(requestedSpec)
	ranked := make([]aiRankedCatalogEntry, 0, len(c.byCategory[category]))
	seen := make(map[string]struct{}, len(c.byCategory[category]))
	for _, entry := range c.byCategory[category] {
		key := aiCatalogEntrySemanticKey(entry)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		score := aiNormalizedSimilarity(requestedNorm, normalizeLookupText(entry.DisplayName)) * 100
		if requestedBaseNorm != "" && normalizeLookupText(entry.BaseName) == requestedBaseNorm {
			score += 25
		}
		candidateSpecNorm := aiCandidateSpecNorm(entry)
		if requestedSpecNorm != "" {
			if candidateSpecNorm == requestedSpecNorm {
				score += 30
			} else if candidateSpecNorm != "" {
				score += aiNormalizedSimilarity(requestedSpecNorm, candidateSpecNorm) * 15
			}
			if len(entry.Nameables) > 0 {
				score += 8
			}
		} else if category == aiLibraryCategorySkill && strings.TrimSpace(entry.Specialization) != "" {
			score -= 15
		}
		entryNorm := normalizeLookupText(entry.DisplayName)
		if requestedNorm != "" && (strings.Contains(entryNorm, requestedNorm) || strings.Contains(requestedNorm, entryNorm)) {
			score += 10
		}
		if len(entry.Nameables) > 0 && requestedSpecNorm != "" {
			score += 5
		}
		if score <= 0 {
			continue
		}
		ranked = append(ranked, aiRankedCatalogEntry{Entry: entry, Score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].Entry.DisplayName < ranked[j].Entry.DisplayName
		}
		return ranked[i].Score > ranked[j].Score
	})
	return ranked
}

func aiAutoselectCandidate(category aiLibraryCategory, requestedName string, ranked []aiRankedCatalogEntry) *aiLibraryCatalogEntry {
	if len(ranked) == 0 {
		return nil
	}
	top := ranked[0]
	_, requestedSpec := splitSkillNameAndSpecialization(requestedName)
	if category == aiLibraryCategorySkill && strings.TrimSpace(requestedSpec) == "" && strings.TrimSpace(top.Entry.Specialization) != "" {
		return nil
	}
	if top.Score < 96 {
		return nil
	}
	if len(ranked) == 1 {
		return top.Entry
	}
	if top.Score-ranked[1].Score >= 8 {
		return top.Entry
	}
	return nil
}

func aiResolvedNotes(explicitNotes, rawName string, entry *aiLibraryCatalogEntry) string {
	if entry == nil {
		return strings.TrimSpace(explicitNotes)
	}
	notes := strings.TrimSpace(explicitNotes)
	if notes != "" {
		return notes
	}
	if len(entry.Nameables) == 0 {
		return ""
	}
	baseName := strings.TrimSpace(entry.DisplayName)
	requestedBase, requestedSpec := splitSkillNameAndSpecialization(rawName)
	if strings.EqualFold(strings.TrimSpace(aiLookupBaseName(requestedBase)), aiLookupBaseName(baseName)) && strings.TrimSpace(requestedSpec) != "" {
		return strings.TrimSpace(requestedSpec)
	}
	if templateValue := aiExtractTemplateNameableValue(rawName, entry); templateValue != "" {
		return templateValue
	}
	return strings.TrimSpace(extractParenthetical(rawName))
}

func (c *aiLibraryCatalog) templateNameableMatch(category aiLibraryCategory, rawName string) (*aiLibraryCatalogEntry, string) {
	rawName = strings.TrimSpace(rawName)
	if c == nil || rawName == "" {
		return nil, ""
	}
	var match *aiLibraryCatalogEntry
	var derivedNotes string
	for _, entry := range c.byCategory[category] {
		if entry == nil || len(entry.Nameables) == 0 {
			continue
		}
		value := aiExtractTemplateNameableValue(rawName, entry)
		if value == "" {
			continue
		}
		if match != nil && aiCatalogEntrySemanticKey(match) != aiCatalogEntrySemanticKey(entry) {
			return nil, ""
		}
		match = entry
		derivedNotes = value
	}
	return match, derivedNotes
}

func aiExtractTemplateNameableValue(rawName string, entry *aiLibraryCatalogEntry) string {
	if entry == nil || len(entry.Nameables) == 0 {
		return ""
	}
	rawTokens := aiTemplateComparableTokens(rawName)
	if len(rawTokens) == 0 {
		return ""
	}
	for _, candidate := range []string{entry.Name, entry.DisplayName, entry.BaseName} {
		prefixTokens := aiTemplateComparableTokens(aiStripNameableTemplate(candidate))
		if len(prefixTokens) == 0 || len(rawTokens) <= len(prefixTokens) {
			continue
		}
		matched := true
		for i := range prefixTokens {
			if aiTemplateComparableToken(prefixTokens[i]) != aiTemplateComparableToken(rawTokens[i]) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		value := strings.TrimSpace(strings.Join(rawTokens[len(prefixTokens):], " "))
		if value != "" {
			return value
		}
	}
	return ""
}

func aiStripNameableTemplate(text string) string {
	text = aiNameableTemplatePattern.ReplaceAllString(text, " ")
	text = strings.NewReplacer("(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ", ",", " ", ";", " ", ":", " ").Replace(text)
	return strings.Join(strings.Fields(text), " ")
}

func aiTemplateComparableTokens(text string) []string {
	text = aiStripNameableTemplate(text)
	if text == "" {
		return nil
	}
	return strings.Fields(text)
}

func aiTemplateComparableToken(token string) string {
	token = normalizeLookupText(token)
	if strings.HasSuffix(token, "ies") && len(token) > 3 {
		return token[:len(token)-3] + "y"
	}
	if strings.HasSuffix(token, "s") && len(token) > 3 && !strings.HasSuffix(token, "ss") {
		return strings.TrimSuffix(token, "s")
	}
	return token
}

func aiBuildRetryItem(category, name, notes, description, points string, quantity int, ranked []aiRankedCatalogEntry) *aiRetryItem {
	item := &aiRetryItem{
		Category:    aiCategoryJSONField(category),
		Name:        strings.TrimSpace(name),
		Notes:       strings.TrimSpace(notes),
		Description: strings.TrimSpace(description),
		Points:      strings.TrimSpace(points),
		Quantity:    quantity,
	}
	if len(ranked) == 0 {
		return item
	}
	limit := min(len(ranked), 5)
	item.Candidates = make([]aiRetryCandidate, 0, limit)
	for _, candidate := range ranked[:limit] {
		item.Candidates = append(item.Candidates, aiRetryCandidate{
			ID:       candidate.Entry.ID,
			Name:     candidate.Entry.DisplayName,
			Requires: append([]string(nil), candidate.Entry.Nameables...),
		})
	}
	return item
}

func aiNormalizedSimilarity(left, right string) float64 {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return 0
	}
	if left == right {
		return 1
	}
	distance := aiLevenshteinDistance(left, right)
	maxLen := max(len(left), len(right))
	if maxLen == 0 {
		return 1
	}
	return 1 - float64(distance)/float64(maxLen)
}

func aiLevenshteinDistance(left, right string) int {
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	if len(leftRunes) == 0 {
		return len(rightRunes)
	}
	if len(rightRunes) == 0 {
		return len(leftRunes)
	}
	previous := make([]int, len(rightRunes)+1)
	current := make([]int, len(rightRunes)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, leftRune := range leftRunes {
		current[0] = i + 1
		for j, rightRune := range rightRunes {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			deletion := previous[j+1] + 1
			insertion := current[j] + 1
			substitution := previous[j] + cost
			current[j+1] = min(deletion, min(insertion, substitution))
		}
		copy(previous, current)
	}
	return previous[len(rightRunes)]
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func aiActionPlanJSONSchema() map[string]any {
	stringField := map[string]any{"type": "string"}
	profileProperties := map[string]any{
		"name":         stringField,
		"gender":       stringField,
		"age":          stringField,
		"birthday":     stringField,
		"height":       stringField,
		"weight":       stringField,
		"hair":         stringField,
		"eyes":         stringField,
		"skin":         stringField,
		"handedness":   stringField,
		"title":        stringField,
		"organization": stringField,
		"religion":     stringField,
		"tech_level":   stringField,
	}
	namedAction := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          stringField,
			"name":        stringField,
			"notes":       stringField,
			"description": stringField,
			"points":      stringField,
			"quantity":    map[string]any{"type": "integer"},
		},
	}
	skillAction := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          stringField,
			"name":        stringField,
			"notes":       stringField,
			"description": stringField,
			"points":      stringField,
			"value":       stringField,
			"level":       stringField,
		},
	}
	attributeAction := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":    stringField,
			"name":  stringField,
			"value": stringField,
		},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"profile": map[string]any{
				"type":       "object",
				"properties": profileProperties,
			},
			"attributes":    map[string]any{"type": "array", "items": attributeAction},
			"advantages":    map[string]any{"type": "array", "items": namedAction},
			"disadvantages": map[string]any{"type": "array", "items": namedAction},
			"quirks":        map[string]any{"type": "array", "items": namedAction},
			"skills":        map[string]any{"type": "array", "items": skillAction},
			"equipment":     map[string]any{"type": "array", "items": namedAction},
			"spend_all_cp":  map[string]any{"type": "boolean"},
		},
	}
}
