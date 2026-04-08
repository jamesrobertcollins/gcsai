package ux

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/richardwilkes/gcs/v5/model/criteria"
	"github.com/richardwilkes/gcs/v5/model/fxp"
	"github.com/richardwilkes/gcs/v5/model/gurps"
	"github.com/richardwilkes/toolbox/v2/i18n"
	"github.com/richardwilkes/unison"
)

var aiAutoBalanceFallbackSkillNames = []string{
	"Hiking",
	"Observation",
	"Search",
	"First Aid",
	"Stealth",
	"Brawling",
}

const aiLocalCorrectionMaxPasses = 2

var (
	aiLocalCorrectionQueryModel = func(d *aiChatDockable, endpoint, model string, messages []aiLocalChatMessage, schema any) (string, error) {
		return d.queryLocalModel(endpoint, model, messages, schema)
	}
	aiLocalCorrectionResolveFiltered = func(d *aiChatDockable, responseText string, filter func(aiActionPlan) aiActionPlan) (aiPlanResolutionResult, error) {
		return d.resolveFilteredAIResponseText(responseText, filter)
	}
	aiLocalCorrectionResolvePlan = func(d *aiChatDockable, plan aiActionPlan) (aiPlanResolutionResult, error) {
		return d.resolveAIActionPlanResult(plan)
	}
)

type aiLocalGenerationPhaseApplyResult struct {
	RemainingCP int
	Summary     string
	Warnings    []string
	Err         error
}

type aiEquipmentBudgetInfo struct {
	BaseStartingWealth     int
	AdjustedStartingWealth int
	WealthTrait            string
	CurrentEquipmentValue  int
}

type aiCharacterAuditSummary struct {
	TotalCPLimit            int
	SpentCP                 int
	UnspentCP               int
	AllowedUnspent          int
	AddedPrerequisiteSkills []string
	TrimmedBackgroundSkills []string
}

type GenerationBudget struct {
	TotalCP          int
	Attributes       int
	Advantages       int
	CoreSkills       int
	BackgroundSkills int
}

type aiBlueprintBudgetPercentages struct {
	Attributes       aiFlexibleInt `json:"attributes,omitempty"`
	Advantages       aiFlexibleInt `json:"advantages,omitempty"`
	CoreSkills       aiFlexibleInt `json:"core_skills,omitempty"`
	BackgroundSkills aiFlexibleInt `json:"background_skills,omitempty"`
}

type aiGenerationBlueprintResponse struct {
	Themes            []string                     `json:"themes,omitempty"`
	BudgetPercentages aiBlueprintBudgetPercentages `json:"budget_percentages,omitempty"`
	Budget            aiBlueprintBudgetPercentages `json:"budget,omitempty"`
}

type aiReferencedSkill struct {
	Name           string
	Specialization string
}

type aiMissingSkillSpec struct {
	Name           string
	Specialization string
}

type aiBackgroundSkillCandidate struct {
	Skill           *gurps.Skill
	DependencyCount int
	Level           int
	Points          int
}

func (b GenerationBudget) SkillPoints() int {
	return b.CoreSkills + b.BackgroundSkills
}

func (b GenerationBudget) Summary() string {
	return fmt.Sprintf("Attributes %d CP, Advantages %d CP, Core Skills %d CP, Background Skills %d CP", b.Attributes, b.Advantages, b.CoreSkills, b.BackgroundSkills)
}

func (b *GenerationBudget) AddDisadvantageBonus(bonus int) {
	if b == nil || bonus <= 0 {
		return
	}
	advantageBonus := bonus / 2
	skillBonus := bonus - advantageBonus
	b.Advantages += advantageBonus
	b.addSkillBonus(skillBonus)
}

func (b *GenerationBudget) addSkillBonus(skillBonus int) {
	if b == nil || skillBonus <= 0 {
		return
	}
	totalSkills := b.CoreSkills + b.BackgroundSkills
	if totalSkills <= 0 {
		b.CoreSkills += skillBonus
		return
	}
	allocations := aiAllocateWeightedBudget(skillBonus, []int{b.CoreSkills, b.BackgroundSkills})
	coreBonus := allocations[0]
	backgroundBonus := allocations[1]
	b.CoreSkills += coreBonus
	b.BackgroundSkills += backgroundBonus
}

func aiNormalizeBlueprintThemes(themes []string, concept string) []string {
	normalized := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	add := func(theme string) {
		theme = strings.TrimSpace(theme)
		if theme == "" {
			return
		}
		key := strings.ToLower(theme)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		normalized = append(normalized, theme)
	}
	for _, theme := range themes {
		add(theme)
		if len(normalized) == 3 {
			return normalized
		}
	}
	for _, token := range aiConceptSearchTokens(concept) {
		add(token)
		if len(normalized) == 3 {
			return normalized
		}
	}
	return normalized
}

func aiDefaultGenerationBudget(totalCP int) GenerationBudget {
	allocations := aiAllocateWeightedBudget(totalCP, []int{40, 20, 25, 15})
	return GenerationBudget{
		TotalCP:          totalCP,
		Attributes:       allocations[0],
		Advantages:       allocations[1],
		CoreSkills:       allocations[2],
		BackgroundSkills: allocations[3],
	}
}

func aiAllocateWeightedBudget(total int, weights []int) []int {
	allocations := make([]int, len(weights))
	if total <= 0 || len(weights) == 0 {
		return allocations
	}
	sanitized := make([]int, len(weights))
	weightSum := 0
	for i, weight := range weights {
		if weight < 0 {
			weight = 0
		}
		sanitized[i] = weight
		weightSum += weight
	}
	if weightSum <= 0 {
		return aiAllocateWeightedBudget(total, []int{40, 20, 25, 15})
	}
	type remainderEntry struct {
		Index     int
		Remainder int
		Weight    int
	}
	remainders := make([]remainderEntry, 0, len(sanitized))
	allocated := 0
	for i, weight := range sanitized {
		product := total * weight
		allocations[i] = product / weightSum
		allocated += allocations[i]
		remainders = append(remainders, remainderEntry{Index: i, Remainder: product % weightSum, Weight: weight})
	}
	remaining := total - allocated
	sort.Slice(remainders, func(i, j int) bool {
		if remainders[i].Remainder != remainders[j].Remainder {
			return remainders[i].Remainder > remainders[j].Remainder
		}
		if remainders[i].Weight != remainders[j].Weight {
			return remainders[i].Weight > remainders[j].Weight
		}
		return remainders[i].Index < remainders[j].Index
	})
	for i := 0; i < remaining && i < len(remainders); i++ {
		allocations[remainders[i].Index]++
	}
	return allocations
}

func aiGenerationBudgetFromPercentages(totalCP int, percentages aiBlueprintBudgetPercentages) GenerationBudget {
	weights := []int{
		max(0, percentages.Attributes.Int()),
		max(0, percentages.Advantages.Int()),
		max(0, percentages.CoreSkills.Int()),
		max(0, percentages.BackgroundSkills.Int()),
	}
	weightSum := 0
	for _, weight := range weights {
		weightSum += weight
	}
	if weightSum <= 0 {
		return aiDefaultGenerationBudget(totalCP)
	}
	allocations := aiAllocateWeightedBudget(totalCP, weights)
	return GenerationBudget{
		TotalCP:          totalCP,
		Attributes:       allocations[0],
		Advantages:       allocations[1],
		CoreSkills:       allocations[2],
		BackgroundSkills: allocations[3],
	}
}

func aiParseGenerationBlueprintResponse(responseText string, totalCP int, concept string) ([]string, GenerationBudget, error) {
	for _, payload := range extractJSONPayloads(responseText) {
		cleaned := sanitizeAIJSONPayload(payload)
		if cleaned == "" {
			continue
		}
		var response aiGenerationBlueprintResponse
		if err := json.Unmarshal([]byte(cleaned), &response); err != nil {
			continue
		}
		budget := response.BudgetPercentages
		if budget.Attributes.Int() == 0 && budget.Advantages.Int() == 0 && budget.CoreSkills.Int() == 0 && budget.BackgroundSkills.Int() == 0 {
			budget = response.Budget
		}
		themes := aiNormalizeBlueprintThemes(response.Themes, concept)
		if len(themes) < 3 {
			continue
		}
		return themes[:3], aiGenerationBudgetFromPercentages(totalCP, budget), nil
	}
	return nil, GenerationBudget{}, fmt.Errorf("Step 1 blueprint did not return parseable JSON with 3 themes and budget percentages")
}

func aiGenerationBlueprintJSONSchema() any {
	integerField := map[string]any{"type": "integer", "minimum": 0, "maximum": 100}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"themes", "budget_percentages"},
		"properties": map[string]any{
			"themes": map[string]any{
				"type":        "array",
				"minItems":    3,
				"maxItems":    3,
				"items":       map[string]any{"type": "string"},
				"uniqueItems": true,
			},
			"budget_percentages": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"attributes", "advantages", "core_skills", "background_skills"},
				"properties": map[string]any{
					"attributes":        integerField,
					"advantages":        integerField,
					"core_skills":       integerField,
					"background_skills": integerField,
				},
			},
		},
	}
}

func aiDisadvantagesQuirksOnlyActionPlan(plan aiActionPlan) aiActionPlan {
	return aiActionPlan{
		Disadvantages: append([]aiNamedAction(nil), plan.Disadvantages...),
		Quirks:        append([]aiNamedAction(nil), plan.Quirks...),
	}
}

func aiAttributesOnlyActionPlan(plan aiActionPlan) aiActionPlan {
	return aiActionPlan{Attributes: append([]aiAttributeAction(nil), plan.Attributes...)}
}

func aiAdvantagesOnlyActionPlan(plan aiActionPlan) aiActionPlan {
	return aiActionPlan{Advantages: append([]aiNamedAction(nil), plan.Advantages...)}
}

func aiSkillsAndSpellsOnlyActionPlan(plan aiActionPlan) aiActionPlan {
	return aiSnapSkillPointsInPlan(aiActionPlan{
		Skills: append([]aiSkillAction(nil), plan.Skills...),
		Spells: append([]aiSkillAction(nil), plan.Spells...),
	})
}

func aiEquipmentOnlyActionPlan(plan aiActionPlan) aiActionPlan {
	return aiActionPlan{Equipment: append([]aiNamedAction(nil), plan.Equipment...)}
}

func aiBuildLocalBlueprintPrompts(originalPrompt string, params aiCharacterRequestParams) (systemPrompt, userPrompt string) {
	systemPrompt = strings.TrimSpace(`You are a deterministic GURPS 4e character-planning function.
Return exactly one top-level JSON object and nothing else.
Do NOT generate a character sheet, traits, skills, equipment, or attributes.
Infer exactly 3 core background themes from the approved character concept.
Then assign a strict integer percentage budget across these four buckets only:
- attributes
- advantages
- core_skills
- background_skills
The four percentages must sum to 100.
Use this exact JSON shape:
{"themes":["theme 1","theme 2","theme 3"],"budget_percentages":{"attributes":0,"advantages":0,"core_skills":0,"background_skills":0}}`)
	userPrompt = strings.TrimSpace(fmt.Sprintf(`Approved character concept: %s
Target total budget: exactly %d CP.
Approved baseline data:
%s

Generate the Step 1 blueprint now.`, params.Concept, params.TotalCP, originalPrompt))
	return systemPrompt, userPrompt
}

func aiBuildLocalStoryEnginePrompts(originalPrompt string, params aiCharacterRequestParams, themes []string, vocabulary string) (systemPrompt, userPrompt string) {
	systemPrompt = strings.TrimSpace(`You are a deterministic GURPS 4e disadvantage and quirk generator.
Return exactly one top-level JSON object and nothing else.
You may output ONLY these JSON fields:
- disadvantages
- quirks
Do not include attributes, advantages, skills, equipment, profile, or spend_all_cp.
Use canonical GURPS Fourth Edition names.
Use only concept-fitting disadvantages and quirks. Do not pad the list.
Quirks may range from 0 to 5 entries depending on the concept and themes.
Stay within the campaign disadvantage limit supplied by the user prompt.`)
	userPrompt = strings.TrimSpace(fmt.Sprintf(`Step 2: Story Engine.
Approved character concept: %s
Core themes: %s
Campaign disadvantage limit: up to %d points in disadvantages.
Approved baseline data:
%s

Use ONLY the concept and themes above to propose disadvantages and quirks.
Dynamic quirk count: 0 to 5, whichever best fits the concept.
Prefer canonical names from this targeted vocabulary when they fit:
%s

Return exactly one JSON object with disadvantages and quirks only.`, params.Concept, strings.Join(themes, ", "), params.DisadvantageLimit, originalPrompt, strings.TrimSpace(vocabulary)))
	return systemPrompt, userPrompt
}

func aiBuildLocalAttributePrompts(originalPrompt string, params aiCharacterRequestParams, themes []string, attributeBucket int, summary string) (systemPrompt, userPrompt string) {
	systemPrompt = strings.TrimSpace(fmt.Sprintf(`You are a deterministic GURPS 4e attribute generator.
Return exactly one top-level JSON object and nothing else.
You may output ONLY the "attributes" field.
Generate attribute and secondary-characteristic adjustments that fit the approved concept.
Do not include advantages, disadvantages, quirks, skills, equipment, profile, or spend_all_cp.
Stay within the attribute bucket supplied by the user prompt.

Current character sheet context:
%s`, summary))
	userPrompt = strings.TrimSpace(fmt.Sprintf(`Step 3: Attributes.
Approved character concept: %s
Core themes: %s
Attribute bucket: do not exceed %d CP.
Approved baseline data:
%s

Generate attributes and secondary characteristics only, without exceeding the attribute bucket.
Return exactly one JSON object with attributes only.`, params.Concept, strings.Join(themes, ", "), attributeBucket, originalPrompt))
	return systemPrompt, userPrompt
}

func aiBuildLocalAdvantagesPrompts(originalPrompt string, params aiCharacterRequestParams, themes []string, advantageBucket int, summary, vocabulary string) (systemPrompt, userPrompt string) {
	systemPrompt = strings.TrimSpace(fmt.Sprintf(`You are a deterministic GURPS 4e advantages and perks generator.
Return exactly one top-level JSON object and nothing else.
You may output ONLY the "advantages" field.
Generate advantages and perks that fit the approved concept, themes, and current sheet state.
Do not include attributes, disadvantages, quirks, skills, spells, equipment, profile, or spend_all_cp.
Stay within the advantages bucket supplied by the user prompt.

Current character sheet context:
%s`, summary))
	userPrompt = strings.TrimSpace(fmt.Sprintf(`Step 4: Advantages & Perks.
Approved character concept: %s
Core themes: %s
Advantages bucket: do not exceed %d CP.
Approved baseline data:
%s

Spend this budget on advantages and perks that fit the character concept.
Consider purchasing Languages and Cultural Familiarities if the background implies them.
Prefer canonical names from this targeted vocabulary when they fit:
%s

Return exactly one JSON object with advantages only.`, params.Concept, strings.Join(themes, ", "), advantageBucket, originalPrompt, strings.TrimSpace(vocabulary)))
	return systemPrompt, userPrompt
}

func aiBuildLocalSkillsPrompts(originalPrompt string, params aiCharacterRequestParams, themes []string, budget GenerationBudget, summary, vocabulary string) (systemPrompt, userPrompt string) {
	totalSkillBucket := budget.SkillPoints()
	systemPrompt = strings.TrimSpace(fmt.Sprintf(`You are a deterministic GURPS 4e skills and spells generator.
Return exactly one top-level JSON object and nothing else.
You may output ONLY these JSON fields:
- skills
- spells
Generate a broad, concept-fitting professional package.
Do not include profile, attributes, advantages, disadvantages, quirks, equipment, or spend_all_cp.
If the character concept is a magic user, treat Spells as Skills and put them in the "spells" field.
Format TL-dependent skills correctly by appending the Tech Level, for example: Computer Operation/TL8.
Stay within the combined skills bucket supplied by the user prompt.

Current character sheet context:
%s`, summary))
	userPrompt = strings.TrimSpace(fmt.Sprintf(`Step 5: Skills & Spells.
Approved character concept: %s
Core themes: %s
Remaining combined Skills bucket: do not exceed %d CP total.
Budget split guidance: spend roughly 70-80%% on Core Professional skills and 20-30%% on Background/Hobby skills.
Current planning target: about %d CP for Core Professional skills and %d CP for Background/Hobby skills.
Approved baseline data:
%s

Build an expansive skill package that fits the concept and themes.
If the character concept is a magic user, treat Spells as Skills. Allocate a portion of your Core Professional budget to Spells.
Format TL-dependent skills correctly by appending the Tech Level (e.g., Computer Operation/TL8).
Use integer point requests. The Go application will snap skills and spells to valid GURPS point values.
Prefer canonical names from this targeted vocabulary when they fit:
%s

Return exactly one JSON object with skills and spells only.`, params.Concept, strings.Join(themes, ", "), totalSkillBucket, budget.CoreSkills, budget.BackgroundSkills, originalPrompt, strings.TrimSpace(vocabulary)))
	return systemPrompt, userPrompt
}

func aiBuildLocalEquipmentPrompts(originalPrompt string, params aiCharacterRequestParams, themes []string, startingWealth int, summary, vocabulary string) (systemPrompt, userPrompt string) {
	startingWealthText := aiCurrencyString(startingWealth)
	systemPrompt = strings.TrimSpace(fmt.Sprintf(`You are a deterministic GURPS 4e equipment generator.
Return exactly one top-level JSON object and nothing else.
You may output ONLY the "equipment" field.
Purchase mundane equipment, weapons, armor, clothing, tools, and travel gear that fit the approved concept, themes, tech level, and current sheet state.
Do not include profile, attributes, advantages, disadvantages, quirks, skills, spells, or spend_all_cp.
Do NOT attempt to generate custom Attack blocks or combat stats; simply purchase the weapons.
Do not assign CP values to items unless it is explicitly customized Signature Gear.
Stay within the exact cash limit supplied by the user prompt.

Current character sheet context:
%s`, summary))
	userPrompt = strings.TrimSpace(fmt.Sprintf(`Step 6: Equipment.
Approved character concept: %s
Core themes: %s
Tech Level: TL %s
Exact adjusted starting wealth: %s.
Approved baseline data:
%s

You have exactly %s to spend on mundane equipment, weapons, and armor. Do not exceed this cash limit.
Do NOT attempt to generate custom Attack blocks or combat stats; simply purchase the weapons.
Do not assign CP values to items unless it is explicitly customized Signature Gear.
Prefer canonical names from this targeted vocabulary when they fit:
%s

Return exactly one JSON object with equipment only.`, params.Concept, strings.Join(themes, ", "), params.TechLevel, startingWealthText, originalPrompt, startingWealthText, strings.TrimSpace(vocabulary)))
	return systemPrompt, userPrompt
}

func (d *aiChatDockable) localGenerationPointsBreakdown() (gurps.PointsBreakdown, error) {
	resultCh := make(chan struct {
		breakdown gurps.PointsBreakdown
		err       error
	}, 1)
	unison.InvokeTask(func() {
		sheet := d.sheetOrCreateNew()
		if sheet == nil || sheet.entity == nil {
			resultCh <- struct {
				breakdown gurps.PointsBreakdown
				err       error
			}{err: fmt.Errorf("no active sheet to apply changes to")}
			return
		}
		resultCh <- struct {
			breakdown gurps.PointsBreakdown
			err       error
		}{breakdown: *sheet.entity.PointsBreakdown()}
	})
	result := <-resultCh
	return result.breakdown, result.err
}

func aiDisadvantageBonusFromPointBreakdowns(before, after gurps.PointsBreakdown) int {
	delta := (after.Disadvantages + after.Quirks) - (before.Disadvantages + before.Quirks)
	bonus := -fxp.AsInteger[int](delta)
	if bonus < 0 {
		return 0
	}
	return bonus
}

func aiAttributeSpendFromPointBreakdowns(before, after gurps.PointsBreakdown) int {
	spent := fxp.AsInteger[int](after.Attributes - before.Attributes)
	if spent < 0 {
		return 0
	}
	return spent
}

func aiAdvantageSpendFromPointBreakdowns(before, after gurps.PointsBreakdown) int {
	spent := fxp.AsInteger[int](after.Advantages - before.Advantages)
	if spent < 0 {
		return 0
	}
	return spent
}

func aiSkillSpendFromPointBreakdowns(before, after gurps.PointsBreakdown) int {
	spent := fxp.AsInteger[int](after.Skills - before.Skills)
	if spent < 0 {
		return 0
	}
	return spent
}

func aiSpellSpendFromPointBreakdowns(before, after gurps.PointsBreakdown) int {
	spent := fxp.AsInteger[int](after.Spells - before.Spells)
	if spent < 0 {
		return 0
	}
	return spent
}

func aiCurrencyString(amount int) string {
	if amount < 0 {
		amount = 0
	}
	return "$" + fxp.FromInteger(amount).Comma()
}

func aiAbsInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func (s aiCharacterAuditSummary) String() string {
	parts := []string{fmt.Sprintf(i18n.Text("Final audit complete. Spent %d/%d CP. Unspent: %d CP. Allowed float: %d CP."), s.SpentCP, s.TotalCPLimit, s.UnspentCP, s.AllowedUnspent)}
	if len(s.AddedPrerequisiteSkills) != 0 {
		parts = append(parts, fmt.Sprintf(i18n.Text("Added prerequisite skills: %s."), strings.Join(s.AddedPrerequisiteSkills, ", ")))
	}
	if len(s.TrimmedBackgroundSkills) != 0 {
		parts = append(parts, fmt.Sprintf(i18n.Text("Trimmed background skills: %s."), strings.Join(s.TrimmedBackgroundSkills, ", ")))
	}
	switch {
	case s.UnspentCP < 0:
		parts = append(parts, fmt.Sprintf(i18n.Text("Warning: character remains %d CP over budget after the audit."), -s.UnspentCP))
	case s.UnspentCP > s.AllowedUnspent:
		parts = append(parts, fmt.Sprintf(i18n.Text("Warning: final unspent CP exceeds the allowed float by %d CP."), s.UnspentCP-s.AllowedUnspent))
	}
	return strings.Join(parts, " ")
}

func aiTotalEquipmentValue(entity *gurps.Entity) int {
	if entity == nil {
		return 0
	}
	return fxp.AsInteger[int](entity.WealthCarried() + entity.WealthNotCarried())
}

func aiAdjustedStartingWealthForEntity(entity *gurps.Entity, baseStartingWealth int) (int, string) {
	if entity == nil || baseStartingWealth <= 0 {
		return max(0, baseStartingWealth), ""
	}
	bestScore := 0
	adjusted := baseStartingWealth
	traitLabel := ""
	gurps.Traverse(func(trait *gurps.Trait) bool {
		candidateAdjusted, candidateLabel, candidateScore, ok := aiAdjustedStartingWealthForTrait(trait, baseStartingWealth)
		if !ok || candidateScore < bestScore {
			return false
		}
		bestScore = candidateScore
		adjusted = candidateAdjusted
		traitLabel = candidateLabel
		return false
	}, true, false, entity.Traits...)
	return adjusted, traitLabel
}

func aiAdjustedStartingWealthForTrait(trait *gurps.Trait, baseStartingWealth int) (adjusted int, label string, score int, ok bool) {
	if trait == nil || trait.Container() || baseStartingWealth <= 0 {
		return 0, "", 0, false
	}
	text := strings.ToLower(strings.TrimSpace(strings.Join([]string{trait.NameWithReplacements(), trait.LocalNotesWithReplacements(), trait.UserDescWithReplacements(), strings.Join(trait.Tags, " ")}, " ")))
	if text == "" {
		return 0, "", 0, false
	}
	points := aiAbsInt(fxp.AsInteger[int](trait.AdjustedPoints()))
	apply := func(name string, numerator, denominator, fallbackScore int) (int, string, int, bool) {
		if denominator <= 0 {
			return 0, "", 0, false
		}
		score := points
		if score <= 0 {
			score = fallbackScore
		}
		return baseStartingWealth * numerator / denominator, name, score, true
	}
	switch {
	case strings.Contains(text, "dead broke"):
		return apply("Dead Broke", 0, 1, 100)
	case strings.Contains(text, "very wealthy"):
		return apply("Very Wealthy", 20, 1, 30)
	case strings.Contains(text, "filthy rich"):
		return apply("Filthy Rich", 100, 1, 50)
	case strings.Contains(text, "multimillionaire"):
		levels := 1
		if trait.IsLeveled() {
			levels = max(1, fxp.AsInteger[int](trait.CurrentLevel()))
		} else if idx := strings.Index(text, "multimillionaire"); idx >= 0 {
			if parsed := aiParseLoosePositiveInt(text[idx+len("multimillionaire"):]); parsed > 0 {
				levels = parsed
			}
		}
		multiplier := 100
		for i := 0; i < levels; i++ {
			multiplier *= 10
		}
		return apply(fmt.Sprintf("Multimillionaire %d", levels), multiplier, 1, 50+levels)
	case strings.Contains(text, "wealthy"):
		return apply("Wealthy", 5, 1, 20)
	case strings.Contains(text, "comfortable"):
		return apply("Comfortable", 2, 1, 10)
	case strings.Contains(text, "struggling"):
		return apply("Struggling", 1, 2, 10)
	case strings.Contains(text, "poor"):
		return apply("Poor", 1, 5, 15)
	default:
		return 0, "", 0, false
	}
}

func (d *aiChatDockable) localGenerationEquipmentBudgetInfo(baseStartingWealth int) (aiEquipmentBudgetInfo, error) {
	resultCh := make(chan struct {
		info aiEquipmentBudgetInfo
		err  error
	}, 1)
	unison.InvokeTask(func() {
		sheet := d.sheetOrCreateNew()
		if sheet == nil || sheet.entity == nil {
			resultCh <- struct {
				info aiEquipmentBudgetInfo
				err  error
			}{err: fmt.Errorf("no active sheet to apply changes to")}
			return
		}
		adjusted, wealthTrait := aiAdjustedStartingWealthForEntity(sheet.entity, baseStartingWealth)
		resultCh <- struct {
			info aiEquipmentBudgetInfo
			err  error
		}{info: aiEquipmentBudgetInfo{
			BaseStartingWealth:     max(0, baseStartingWealth),
			AdjustedStartingWealth: adjusted,
			WealthTrait:            wealthTrait,
			CurrentEquipmentValue:  aiTotalEquipmentValue(sheet.entity),
		}}
	})
	result := <-resultCh
	return result.info, result.err
}

func aiAuditSkillDisplayName(name, specialization string) string {
	name = strings.TrimSpace(name)
	specialization = strings.TrimSpace(specialization)
	if specialization == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, specialization)
}

func aiPreviousValidSkillPointCost(currentPoints int) int {
	if currentPoints <= 1 {
		return 0
	}
	if currentPoints == 2 {
		return 1
	}
	if currentPoints <= 4 {
		return 2
	}
	return aiLargestValidSkillPointCostNotExceeding(currentPoints - 1)
}

func aiCollectMissingSkillPrereqs(prereq gurps.Prereq, entity *gurps.Entity, exclude any, additions map[string]aiMissingSkillSpec) {
	switch typed := prereq.(type) {
	case *gurps.PrereqList:
		if typed == nil {
			return
		}
		for _, child := range typed.Prereqs {
			aiCollectMissingSkillPrereqs(child, entity, exclude, additions)
		}
	case *gurps.SkillPrereq:
		if typed == nil || !typed.Has || typed.NameCriteria.Compare != criteria.IsText {
			return
		}
		if typed.SpecializationCriteria.Compare != criteria.AnyText && typed.SpecializationCriteria.Compare != criteria.IsText {
			return
		}
		hasEquipmentPenalty := false
		if typed.Satisfied(entity, exclude, nil, "", &hasEquipmentPenalty) {
			return
		}
		name := strings.TrimSpace(typed.NameCriteria.Qualifier)
		if name == "" {
			return
		}
		specialization := ""
		if typed.SpecializationCriteria.Compare == criteria.IsText {
			specialization = strings.TrimSpace(typed.SpecializationCriteria.Qualifier)
		}
		key := strings.ToLower(name) + "\x00" + strings.ToLower(specialization)
		if _, exists := additions[key]; exists {
			return
		}
		additions[key] = aiMissingSkillSpec{Name: name, Specialization: specialization}
	}
}

func aiApplyMissingPrerequisiteSkills(entity *gurps.Entity) []string {
	if entity == nil {
		return nil
	}
	additions := make(map[string]aiMissingSkillSpec)
	gurps.Traverse(func(skill *gurps.Skill) bool {
		if skill == nil || skill.Container() || skill.Prereq == nil {
			return false
		}
		aiCollectMissingSkillPrereqs(skill.Prereq, entity, skill, additions)
		return false
	}, false, true, entity.Skills...)
	gurps.Traverse(func(spell *gurps.Spell) bool {
		if spell == nil || spell.Container() || spell.Prereq == nil {
			return false
		}
		aiCollectMissingSkillPrereqs(spell.Prereq, entity, spell, additions)
		return false
	}, false, true, entity.Spells...)
	if len(additions) == 0 {
		return nil
	}
	added := make([]string, 0, len(additions))
	orderedKeys := make([]string, 0, len(additions))
	for key := range additions {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)
	skills := append([]*gurps.Skill(nil), entity.Skills...)
	for _, key := range orderedKeys {
		spec := additions[key]
		existing := entity.BestSkillNamed(spec.Name, spec.Specialization, false, nil)
		if existing != nil {
			if fxp.AsInteger[int](existing.Points) <= 0 {
				existing.SetRawPoints(fxp.One)
				added = append(added, aiAuditSkillDisplayName(spec.Name, spec.Specialization))
			}
			continue
		}
		skill := gurps.NewSkill(entity, nil, false)
		skill.Name = spec.Name
		skill.Specialization = spec.Specialization
		skill.SetRawPoints(fxp.One)
		skills = append(skills, skill)
		added = append(added, aiAuditSkillDisplayName(spec.Name, spec.Specialization))
	}
	if len(skills) != len(entity.Skills) {
		entity.SetSkillList(skills)
	}
	if len(added) != 0 {
		entity.Recalculate()
	}
	return added
}

func aiCollectReferencedSkillPrereqs(prereq gurps.Prereq, refs *[]aiReferencedSkill) {
	switch typed := prereq.(type) {
	case *gurps.PrereqList:
		if typed == nil {
			return
		}
		for _, child := range typed.Prereqs {
			aiCollectReferencedSkillPrereqs(child, refs)
		}
	case *gurps.SkillPrereq:
		if typed == nil || !typed.Has || typed.NameCriteria.Compare != criteria.IsText {
			return
		}
		if typed.SpecializationCriteria.Compare != criteria.AnyText && typed.SpecializationCriteria.Compare != criteria.IsText {
			return
		}
		ref := aiReferencedSkill{Name: strings.TrimSpace(typed.NameCriteria.Qualifier)}
		if ref.Name == "" {
			return
		}
		if typed.SpecializationCriteria.Compare == criteria.IsText {
			ref.Specialization = strings.TrimSpace(typed.SpecializationCriteria.Qualifier)
		}
		*refs = append(*refs, ref)
	}
}

func aiBackgroundSkillTrimCandidates(entity *gurps.Entity) []aiBackgroundSkillCandidate {
	if entity == nil {
		return nil
	}
	references := make([]aiReferencedSkill, 0)
	gurps.Traverse(func(skill *gurps.Skill) bool {
		if skill != nil && !skill.Container() && skill.Prereq != nil {
			aiCollectReferencedSkillPrereqs(skill.Prereq, &references)
		}
		return false
	}, false, true, entity.Skills...)
	gurps.Traverse(func(spell *gurps.Spell) bool {
		if spell != nil && !spell.Container() && spell.Prereq != nil {
			aiCollectReferencedSkillPrereqs(spell.Prereq, &references)
		}
		return false
	}, false, true, entity.Spells...)
	candidates := make([]aiBackgroundSkillCandidate, 0, len(entity.Skills))
	gurps.Traverse(func(skill *gurps.Skill) bool {
		if skill == nil || skill.Container() || skill.IsTechnique() || skill.AdjustedPoints(nil) <= 0 {
			return false
		}
		candidate := aiBackgroundSkillCandidate{
			Skill:  skill,
			Level:  fxp.AsInteger[int](skill.CalculateLevel(nil).Level),
			Points: fxp.AsInteger[int](skill.Points),
		}
		for _, ref := range references {
			if !strings.EqualFold(skill.NameWithReplacements(), ref.Name) {
				continue
			}
			if ref.Specialization != "" && !strings.EqualFold(skill.SpecializationWithReplacements(), ref.Specialization) {
				continue
			}
			candidate.DependencyCount++
		}
		candidates = append(candidates, candidate)
		return false
	}, false, true, entity.Skills...)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].DependencyCount != candidates[j].DependencyCount {
			return candidates[i].DependencyCount < candidates[j].DependencyCount
		}
		if candidates[i].Level != candidates[j].Level {
			return candidates[i].Level < candidates[j].Level
		}
		if candidates[i].Points != candidates[j].Points {
			return candidates[i].Points < candidates[j].Points
		}
		return aiResolvedSkillDisplayName(candidates[i].Skill) < aiResolvedSkillDisplayName(candidates[j].Skill)
	})
	return candidates
}

func aiFinalizeCharacterAuditDetailed(entity *gurps.Entity, totalCPLimit int) aiCharacterAuditSummary {
	summary := aiCharacterAuditSummary{TotalCPLimit: totalCPLimit}
	if entity == nil {
		return summary
	}
	if totalCPLimit <= 0 {
		totalCPLimit = fxp.AsInteger[int](entity.TotalPoints)
		summary.TotalCPLimit = totalCPLimit
	}
	entity.TotalPoints = fxp.FromInteger(totalCPLimit)
	entity.Recalculate()
	summary.AllowedUnspent = max(1, int(float64(totalCPLimit)*0.02))
	summary.AddedPrerequisiteSkills = aiApplyMissingPrerequisiteSkills(entity)
	entity.Recalculate()
	for fxp.AsInteger[int](entity.UnspentPoints()) < 0 {
		trimmed := false
		for _, candidate := range aiBackgroundSkillTrimCandidates(entity) {
			currentPoints := fxp.AsInteger[int](candidate.Skill.Points)
			nextPoints := aiPreviousValidSkillPointCost(currentPoints)
			if nextPoints >= currentPoints {
				continue
			}
			candidate.Skill.SetRawPoints(fxp.FromInteger(nextPoints))
			summary.TrimmedBackgroundSkills = append(summary.TrimmedBackgroundSkills, fmt.Sprintf("%s %d->%d", aiResolvedSkillDisplayName(candidate.Skill), currentPoints, nextPoints))
			entity.Recalculate()
			trimmed = true
			break
		}
		if !trimmed {
			break
		}
	}
	entity.TotalPoints = fxp.FromInteger(totalCPLimit)
	entity.Recalculate()
	points := entity.PointsBreakdown()
	summary.SpentCP = fxp.AsInteger[int](points.Total())
	summary.UnspentCP = fxp.AsInteger[int](entity.UnspentPoints())
	return summary
}

func FinalizeCharacterAudit(entity *gurps.Entity, totalCPLimit int) {
	fmt.Println(aiFinalizeCharacterAuditDetailed(entity, totalCPLimit).String())
}

func (d *aiChatDockable) finalizeLocalGenerationAudit(totalCPLimit int) (string, error) {
	resultCh := make(chan struct {
		summary string
		err     error
	}, 1)
	unison.InvokeTask(func() {
		sheet := d.sheetOrCreateNew()
		if sheet == nil || sheet.entity == nil {
			resultCh <- struct {
				summary string
				err     error
			}{err: fmt.Errorf("no active sheet to apply changes to")}
			return
		}
		auditSummary := aiFinalizeCharacterAuditDetailed(sheet.entity, totalCPLimit)
		fmt.Println(auditSummary.String())
		sheet.Rebuild(true)
		ActivateDockable(sheet)
		MarkModified(sheet)
		resultCh <- struct {
			summary string
			err     error
		}{summary: auditSummary.String()}
	})
	result := <-resultCh
	return result.summary, result.err
}

func aiResolvedActionPlanCount(plan aiActionPlan) int {
	return len(plan.Attributes) + len(plan.Advantages) + len(plan.Disadvantages) + len(plan.Quirks) + len(plan.Skills) + len(plan.Spells) + len(plan.Equipment)
}

func aiRetryItemsContainPointBearingCategories(items []aiRetryItem) bool {
	for _, item := range items {
		switch aiCategoryJSONField(item.Category) {
		case string(aiLibraryCategoryEquipment):
			continue
		default:
			return true
		}
	}
	return false
}

func aiRetryItemsSummary(items []aiRetryItem) string {
	if len(items) == 0 {
		return ""
	}
	limit := min(len(items), 4)
	parts := make([]string, 0, limit+1)
	for _, item := range items[:limit] {
		parts = append(parts, fmt.Sprintf("%s %q", aiCategorySingular(item.Category), item.Name))
	}
	if len(items) > limit {
		parts = append(parts, fmt.Sprintf("and %d more", len(items)-limit))
	}
	return strings.Join(parts, ", ")
}

func aiLocalPhaseMessage(label, responseText string) string {
	if strings.TrimSpace(label) == "" {
		return responseText
	}
	return fmt.Sprintf("%s\n%s", label, responseText)
}

func aiSnapSkillPointsInPlan(plan aiActionPlan) aiActionPlan {
	if len(plan.Skills) == 0 && len(plan.Spells) == 0 {
		return plan
	}
	plan.Skills = aiSnapSkillActions(plan.Skills)
	plan.Spells = aiSnapSkillActions(plan.Spells)
	return plan
}

func aiSnapSkillActions(actions []aiSkillAction) []aiSkillAction {
	if len(actions) == 0 {
		return nil
	}
	actions = append([]aiSkillAction(nil), actions...)
	for i, action := range actions {
		pointsText := strings.TrimSpace(firstNonEmptyString(action.Points.String(), action.Value.String()))
		if pointsText == "" {
			continue
		}
		requestedPoints, err := strconv.Atoi(pointsText)
		if err != nil {
			continue
		}
		actions[i].Points = aiFlexibleString(strconv.Itoa(SnapToValidSkillPoints(requestedPoints)))
		actions[i].Value = ""
	}
	return actions
}

func SnapToValidSkillPoints(requestedPoints int) int {
	if requestedPoints <= 1 {
		return 1
	}
	if requestedPoints == 2 {
		return 2
	}
	if requestedPoints <= 4 {
		if requestedPoints-2 <= 4-requestedPoints {
			return 2
		}
		return 4
	}
	lower := 4 * (requestedPoints / 4)
	upper := lower + 4
	if lower < 4 {
		lower = 4
	}
	if requestedPoints-lower <= upper-requestedPoints {
		return lower
	}
	return upper
}

func aiNextValidSkillPointCost(currentPoints int) int {
	if currentPoints <= 0 {
		return 1
	}
	if currentPoints == 1 {
		return 2
	}
	if currentPoints == 2 {
		return 4
	}
	if currentPoints < 4 {
		return 4
	}
	return currentPoints + 4
}

func aiLargestValidSkillPointCostNotExceeding(limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit == 1 {
		return 1
	}
	if limit == 2 || limit == 3 {
		return 2
	}
	if limit <= 4 {
		return 4
	}
	return 4 * (limit / 4)
}

func (d *aiChatDockable) prepareLocalGenerationTarget(targetCP int) (string, error) {
	resultCh := make(chan struct {
		summary string
		err     error
	}, 1)
	unison.InvokeTask(func() {
		sheet := d.sheetOrCreateNew()
		if sheet == nil || sheet.entity == nil {
			resultCh <- struct {
				summary string
				err     error
			}{err: fmt.Errorf("no active sheet to apply changes to")}
			return
		}
		sheet.entity.TotalPoints = fxp.FromInteger(targetCP)
		sheet.entity.Recalculate()
		sheet.Rebuild(true)
		resultCh <- struct {
			summary string
			err     error
		}{summary: d.currentCharacterSummary()}
	})
	result := <-resultCh
	return result.summary, result.err
}

func (d *aiChatDockable) resolveFilteredAIResponseText(responseText string, filter func(aiActionPlan) aiActionPlan) (aiPlanResolutionResult, error) {
	plan, ok := d.parseAIActionPlan(responseText)
	if !ok {
		return aiPlanResolutionResult{Parsed: false}, nil
	}
	filtered := filter(plan)
	if !hasAIActionPlanContent(filtered) {
		return aiPlanResolutionResult{Parsed: true, Plan: filtered, ResolvedPlan: filtered}, nil
	}
	resolution, err := d.resolveAIActionPlanResult(filtered)
	if err != nil {
		return aiPlanResolutionResult{}, err
	}
	resolution.Plan = filtered
	return resolution, nil
}

func (d *aiChatDockable) recommendedTermsForLocalPhase(concept string, limits map[aiLibraryCategory]int) string {
	catalog, err := d.aiLibraryCatalog()
	if err != nil || catalog == nil {
		return ""
	}
	return catalog.recommendedTermsForConcept(concept, limits)
}

func (d *aiChatDockable) executeLocalCorrectionLoop(endpoint, model, systemPrompt, phaseLabel string, resolution aiPlanResolutionResult, filter func(aiActionPlan) aiActionPlan) (aiPlanResolutionResult, error) {
	current := resolution
	if !current.Parsed || len(current.RetryItems) == 0 {
		return current, nil
	}
	correctionWarnings := make([]string, 0, aiLocalCorrectionMaxPasses+1)
	for pass := 1; pass <= aiLocalCorrectionMaxPasses && len(current.RetryItems) > 0; pass++ {
		prompt := aiBuildLocalResolverAlternativePrompt(current.RetryItems)
		responseText, err := aiLocalCorrectionQueryModel(d, endpoint, model, buildLocalStatelessMessages(systemPrompt, prompt), aiActionPlanJSONSchema())
		if err != nil {
			correctionWarnings = append(correctionWarnings, fmt.Sprintf(i18n.Text("%s correction pass %d could not query follow-up alternatives: %v"), phaseLabel, pass, err))
			aiWriteResolverDebugLog("correction-pass",
				fmt.Sprintf("phase=%q", phaseLabel),
				fmt.Sprintf("pass=%d", pass),
				"result=query-error",
				fmt.Sprintf("remaining=%d", len(current.RetryItems)),
			)
			break
		}
		followUpResolution, resolveErr := aiLocalCorrectionResolveFiltered(d, responseText, filter)
		if resolveErr != nil {
			correctionWarnings = append(correctionWarnings, fmt.Sprintf(i18n.Text("%s correction pass %d could not resolve follow-up alternatives: %v"), phaseLabel, pass, resolveErr))
			aiWriteResolverDebugLog("correction-pass",
				fmt.Sprintf("phase=%q", phaseLabel),
				fmt.Sprintf("pass=%d", pass),
				"result=resolve-error",
				fmt.Sprintf("remaining=%d", len(current.RetryItems)),
			)
			break
		}
		if !followUpResolution.Parsed {
			correctionWarnings = append(correctionWarnings, fmt.Sprintf(i18n.Text("%s correction pass %d returned no parseable correction JSON."), phaseLabel, pass))
			aiWriteResolverDebugLog("correction-pass",
				fmt.Sprintf("phase=%q", phaseLabel),
				fmt.Sprintf("pass=%d", pass),
				"result=not-parsed",
				fmt.Sprintf("remaining=%d", len(current.RetryItems)),
			)
			break
		}
		filteredFollowUpPlan := aiFilterCorrectionPlan(followUpResolution.Plan, current.RetryItems)
		ignoredCorrections := aiActionPlanItemCount(followUpResolution.Plan) - aiActionPlanItemCount(filteredFollowUpPlan)
		if ignoredCorrections > 0 {
			correctionWarnings = append(correctionWarnings, fmt.Sprintf(i18n.Text("%s correction pass %d ignored %d unrelated correction entries."), phaseLabel, pass, ignoredCorrections))
		}
		followUpResolution.Plan = filteredFollowUpPlan

		mergedPlan := aiActionPlanWithoutRetryItems(current.Plan, current.RetryItems)
		mergeAIActionPlan(&mergedPlan, followUpResolution.Plan)
		mergedPlan = filter(mergedPlan)
		mergedResolution, err := aiLocalCorrectionResolvePlan(d, mergedPlan)
		if err != nil {
			return current, err
		}
		mergedResolution.Plan = mergedPlan

		beforeRetryCount := len(current.RetryItems)
		afterRetryCount := len(mergedResolution.RetryItems)
		beforeResolvedCount := aiResolvedActionPlanCount(current.ResolvedPlan)
		afterResolvedCount := aiResolvedActionPlanCount(mergedResolution.ResolvedPlan)
		if afterRetryCount > beforeRetryCount || (afterRetryCount == beforeRetryCount && afterResolvedCount <= beforeResolvedCount) {
			correctionWarnings = append(correctionWarnings, fmt.Sprintf(i18n.Text("%s correction pass %d made no progress and was stopped."), phaseLabel, pass))
			aiWriteResolverDebugLog("correction-pass",
				fmt.Sprintf("phase=%q", phaseLabel),
				fmt.Sprintf("pass=%d", pass),
				"result=no-progress",
				fmt.Sprintf("before=%d", beforeRetryCount),
				fmt.Sprintf("after=%d", afterRetryCount),
			)
			current = mergedResolution
			break
		}

		progressMessage := fmt.Sprintf(i18n.Text("%s correction pass %d reduced unresolved items from %d to %d."), phaseLabel, pass, beforeRetryCount, afterRetryCount)
		if afterRetryCount == beforeRetryCount {
			progressMessage = fmt.Sprintf(i18n.Text("%s correction pass %d refined candidate selections but still has %d unresolved items."), phaseLabel, pass, afterRetryCount)
		}
		correctionWarnings = append(correctionWarnings, progressMessage)
		aiWriteResolverDebugLog("correction-pass",
			fmt.Sprintf("phase=%q", phaseLabel),
			fmt.Sprintf("pass=%d", pass),
			"result=progress",
			fmt.Sprintf("before=%d", beforeRetryCount),
			fmt.Sprintf("after=%d", afterRetryCount),
		)
		current = mergedResolution
	}
	current.Warnings = append(correctionWarnings, current.Warnings...)
	if len(current.RetryItems) != 0 && aiRetryItemsContainPointBearingCategories(current.RetryItems) {
		aiWriteResolverDebugLog("correction-pass",
			fmt.Sprintf("phase=%q", phaseLabel),
			"result=hard-stop",
			fmt.Sprintf("remaining=%d", len(current.RetryItems)),
		)
		return current, fmt.Errorf(i18n.Text("%s still has unresolved point-bearing items after correction: %s"), phaseLabel, aiRetryItemsSummary(current.RetryItems))
	}
	return current, nil
}

func (d *aiChatDockable) applyLocalGenerationPhase(label, responseText string, resolution aiPlanResolutionResult, targetCP int) aiLocalGenerationPhaseApplyResult {
	resultCh := make(chan aiLocalGenerationPhaseApplyResult, 1)
	unison.InvokeTask(func() {
		message := responseText
		if strings.TrimSpace(label) != "" {
			message = fmt.Sprintf("%s\n%s", label, responseText)
		}
		d.addMessage("AI", message)
		if !resolution.Parsed {
			if strings.Contains(responseText, "{") {
				d.addMessage("AI", i18n.Text("Structured update data was detected, but it could not be parsed into a character-sheet update."))
			}
			resultCh <- aiLocalGenerationPhaseApplyResult{Err: fmt.Errorf("%s could not be parsed", label)}
			return
		}

		sheet := d.sheetOrCreateNew()
		if sheet == nil || sheet.entity == nil {
			resultCh <- aiLocalGenerationPhaseApplyResult{Err: fmt.Errorf("no active sheet to apply changes to")}
			return
		}
		entity := sheet.entity
		entity.TotalPoints = fxp.FromInteger(targetCP)

		warnings := append([]string(nil), resolution.Warnings...)
		if hasAIActionPlanContent(resolution.ResolvedPlan) {
			applyWarnings, applyRetryItems, applyErr := d.applyAIActionPlan(resolution.ResolvedPlan)
			if applyErr != nil {
				resultCh <- aiLocalGenerationPhaseApplyResult{Err: applyErr}
				return
			}
			warnings = append(warnings, applyWarnings...)
			if len(applyRetryItems) != 0 {
				warnings = append(warnings, i18n.Text("Warning: some requested items still needed exact library selection and were skipped."))
			}
		} else {
			entity.Recalculate()
			sheet.Rebuild(true)
		}
		if len(resolution.RetryItems) != 0 {
			warnings = append(warnings, i18n.Text("Warning: some requested items could not be resolved to exact library entries and were skipped."))
		}
		for _, warning := range warnings {
			d.addMessage("AI", warning)
		}
		remainingCP := fxp.AsInteger[int](entity.UnspentPoints())
		d.addMessage("AI", fmt.Sprintf(i18n.Text("%s complete. Remaining CP: %d."), label, remainingCP))
		resultCh <- aiLocalGenerationPhaseApplyResult{
			RemainingCP: remainingCP,
			Summary:     d.currentCharacterSummary(),
			Warnings:    warnings,
		}
	})
	return <-resultCh
}

func (d *aiChatDockable) autoBalanceLocalGeneration(targetCP int) (before, after int, err error) {
	resultCh := make(chan struct {
		before int
		after  int
		err    error
	}, 1)
	unison.InvokeTask(func() {
		sheet := d.sheetOrCreateNew()
		if sheet == nil || sheet.entity == nil {
			resultCh <- struct {
				before int
				after  int
				err    error
			}{err: fmt.Errorf("no active sheet to apply changes to")}
			return
		}
		entity := sheet.entity
		entity.TotalPoints = fxp.FromInteger(targetCP)
		entity.Recalculate()
		before = fxp.AsInteger[int](entity.UnspentPoints())
		AutoBalanceUnspentPoints(entity, targetCP)
		entity.Recalculate()
		sheet.Rebuild(true)
		ActivateDockable(sheet)
		MarkModified(sheet)
		after = fxp.AsInteger[int](entity.UnspentPoints())
		resultCh <- struct {
			before int
			after  int
			err    error
		}{before: before, after: after}
	})
	result := <-resultCh
	return result.before, result.after, result.err
}

func (d *aiChatDockable) executeLocalGenerationPipeline(endpoint, model, originalPrompt string, params aiCharacterRequestParams) {
	targetCP := params.TotalCP
	_, err := d.prepareLocalGenerationTarget(targetCP)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI build could not be started: %v"), err))
		})
		return
	}

	blueprintLabel := i18n.Text("Step 1: Blueprint")
	blueprintSystemPrompt, blueprintUserPrompt := aiBuildLocalBlueprintPrompts(originalPrompt, params)
	writeSystemPromptDebugFile(blueprintSystemPrompt)
	blueprintResponse, err := d.queryLocalModel(endpoint, model, buildLocalStatelessMessages(blueprintSystemPrompt, blueprintUserPrompt), aiGenerationBlueprintJSONSchema())
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", err.Error())
		})
		return
	}
	themes, budget, err := aiParseGenerationBlueprintResponse(blueprintResponse, targetCP, params.Concept)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", aiLocalPhaseMessage(blueprintLabel, blueprintResponse))
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI blueprint could not be resolved: %v"), err))
		})
		return
	}
	unison.InvokeTask(func() {
		d.addMessage("AI", aiLocalPhaseMessage(blueprintLabel, blueprintResponse))
		d.addMessage("AI", fmt.Sprintf(i18n.Text("Blueprint themes: %s."), strings.Join(themes, ", ")))
		d.addMessage("AI", fmt.Sprintf(i18n.Text("Blueprint budget: %s."), budget.Summary()))
	})

	thematicVocabulary := GetThematicVocabulary(params.Concept, themes)
	storyVocabulary := aiFilterThematicVocabularySections(thematicVocabulary, "Disadvantages", "Quirks")

	beforeStoryBreakdown, err := d.localGenerationPointsBreakdown()
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 2 could not start: %v"), err))
		})
		return
	}
	storyLabel := i18n.Text("Step 2: Story Engine")
	storySystemPrompt, storyUserPrompt := aiBuildLocalStoryEnginePrompts(originalPrompt, params, themes, storyVocabulary)
	writeSystemPromptDebugFile(storySystemPrompt)
	storyResponse, err := d.queryLocalModel(endpoint, model, buildLocalStatelessMessages(storySystemPrompt, storyUserPrompt), aiActionPlanJSONSchema())
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", err.Error())
		})
		return
	}
	storyResolution, err := d.resolveFilteredAIResponseText(storyResponse, aiDisadvantagesQuirksOnlyActionPlan)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", storyResponse)
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 2 could not be resolved: %v"), err))
		})
		return
	}
	storyResolution, err = d.executeLocalCorrectionLoop(endpoint, model, storySystemPrompt, storyLabel, storyResolution, aiDisadvantagesQuirksOnlyActionPlan)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", aiLocalPhaseMessage(storyLabel, storyResponse))
			for _, warning := range storyResolution.Warnings {
				d.addMessage("AI", warning)
			}
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 2 could not continue: %v"), err))
		})
		return
	}
	storyApply := d.applyLocalGenerationPhase(storyLabel, storyResponse, storyResolution, targetCP)
	if storyApply.Err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 2 could not be applied: %v"), storyApply.Err))
		})
		return
	}
	afterStoryBreakdown, err := d.localGenerationPointsBreakdown()
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 2 budget update could not be calculated: %v"), err))
		})
		return
	}
	bonusCP := aiDisadvantageBonusFromPointBreakdowns(beforeStoryBreakdown, afterStoryBreakdown)
	budget.AddDisadvantageBonus(bonusCP)
	unison.InvokeTask(func() {
		d.addMessage("AI", fmt.Sprintf(i18n.Text("Story Engine generated %d bonus CP from disadvantages and quirks. Updated budget: %s."), bonusCP, budget.Summary()))
	})

	beforeAttributeBreakdown, err := d.localGenerationPointsBreakdown()
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 3 could not start: %v"), err))
		})
		return
	}
	attributeLabel := i18n.Text("Step 3: Attributes")
	attributeSystemPrompt, attributeUserPrompt := aiBuildLocalAttributePrompts(originalPrompt, params, themes, budget.Attributes, storyApply.Summary)
	writeSystemPromptDebugFile(attributeSystemPrompt)
	attributeResponse, err := d.queryLocalModel(endpoint, model, buildLocalStatelessMessages(attributeSystemPrompt, attributeUserPrompt), aiActionPlanJSONSchema())
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", err.Error())
		})
		return
	}
	attributeResolution, err := d.resolveFilteredAIResponseText(attributeResponse, aiAttributesOnlyActionPlan)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", attributeResponse)
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 3 could not be resolved: %v"), err))
		})
		return
	}
	attributeApply := d.applyLocalGenerationPhase(attributeLabel, attributeResponse, attributeResolution, targetCP)
	if attributeApply.Err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 3 could not be applied: %v"), attributeApply.Err))
		})
		return
	}
	afterAttributeBreakdown, err := d.localGenerationPointsBreakdown()
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 3 budget check could not be calculated: %v"), err))
		})
		return
	}
	attributeSpend := aiAttributeSpendFromPointBreakdowns(beforeAttributeBreakdown, afterAttributeBreakdown)
	unison.InvokeTask(func() {
		if attributeSpend > budget.Attributes {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Warning: Step 3 spent %d CP on attributes, exceeding the %d CP attribute bucket."), attributeSpend, budget.Attributes))
		} else {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Step 3 spent %d CP from the %d CP attribute bucket."), attributeSpend, budget.Attributes))
		}
	})

	advantageVocabulary := aiFilterThematicVocabularySections(thematicVocabulary, "Advantages", "Perks")
	beforeAdvantageBreakdown, err := d.localGenerationPointsBreakdown()
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 4 could not start: %v"), err))
		})
		return
	}
	advantageLabel := i18n.Text("Step 4: Advantages & Perks")
	advantageSystemPrompt, advantageUserPrompt := aiBuildLocalAdvantagesPrompts(originalPrompt, params, themes, budget.Advantages, attributeApply.Summary, advantageVocabulary)
	writeSystemPromptDebugFile(advantageSystemPrompt)
	advantageResponse, err := d.queryLocalModel(endpoint, model, buildLocalStatelessMessages(advantageSystemPrompt, advantageUserPrompt), aiActionPlanJSONSchema())
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", err.Error())
		})
		return
	}
	advantageResolution, err := d.resolveFilteredAIResponseText(advantageResponse, aiAdvantagesOnlyActionPlan)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", advantageResponse)
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 4 could not be resolved: %v"), err))
		})
		return
	}
	advantageResolution, err = d.executeLocalCorrectionLoop(endpoint, model, advantageSystemPrompt, advantageLabel, advantageResolution, aiAdvantagesOnlyActionPlan)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", aiLocalPhaseMessage(advantageLabel, advantageResponse))
			for _, warning := range advantageResolution.Warnings {
				d.addMessage("AI", warning)
			}
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 4 could not continue: %v"), err))
		})
		return
	}
	advantageApply := d.applyLocalGenerationPhase(advantageLabel, advantageResponse, advantageResolution, targetCP)
	if advantageApply.Err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 4 could not be applied: %v"), advantageApply.Err))
		})
		return
	}
	afterAdvantageBreakdown, err := d.localGenerationPointsBreakdown()
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 4 budget check could not be calculated: %v"), err))
		})
		return
	}
	advantageSpend := aiAdvantageSpendFromPointBreakdowns(beforeAdvantageBreakdown, afterAdvantageBreakdown)
	unison.InvokeTask(func() {
		if advantageSpend > budget.Advantages {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Warning: Step 4 spent %d CP on advantages and perks, exceeding the %d CP advantages bucket."), advantageSpend, budget.Advantages))
		} else {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Step 4 spent %d CP from the %d CP advantages bucket."), advantageSpend, budget.Advantages))
		}
	})

	skillsVocabulary := aiFilterThematicVocabularySections(thematicVocabulary, "Skills", "Spells")
	beforeSkillBreakdown, err := d.localGenerationPointsBreakdown()
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 5 could not start: %v"), err))
		})
		return
	}
	skillLabel := i18n.Text("Step 5: Skills & Spells")
	skillSystemPrompt, skillUserPrompt := aiBuildLocalSkillsPrompts(originalPrompt, params, themes, budget, advantageApply.Summary, skillsVocabulary)
	writeSystemPromptDebugFile(skillSystemPrompt)
	skillResponse, err := d.queryLocalModel(endpoint, model, buildLocalStatelessMessages(skillSystemPrompt, skillUserPrompt), aiActionPlanJSONSchema())
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", err.Error())
		})
		return
	}
	skillResolution, err := d.resolveFilteredAIResponseText(skillResponse, aiSkillsAndSpellsOnlyActionPlan)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", skillResponse)
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 5 could not be resolved: %v"), err))
		})
		return
	}
	skillResolution, err = d.executeLocalCorrectionLoop(endpoint, model, skillSystemPrompt, skillLabel, skillResolution, aiSkillsAndSpellsOnlyActionPlan)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", aiLocalPhaseMessage(skillLabel, skillResponse))
			for _, warning := range skillResolution.Warnings {
				d.addMessage("AI", warning)
			}
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 5 could not continue: %v"), err))
		})
		return
	}
	skillApply := d.applyLocalGenerationPhase(skillLabel, skillResponse, skillResolution, targetCP)
	if skillApply.Err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 5 could not be applied: %v"), skillApply.Err))
		})
		return
	}
	afterSkillBreakdown, err := d.localGenerationPointsBreakdown()
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 5 budget check could not be calculated: %v"), err))
		})
		return
	}
	skillSpend := aiSkillSpendFromPointBreakdowns(beforeSkillBreakdown, afterSkillBreakdown)
	spellSpend := aiSpellSpendFromPointBreakdowns(beforeSkillBreakdown, afterSkillBreakdown)
	totalSkillSpend := skillSpend + spellSpend
	unison.InvokeTask(func() {
		if totalSkillSpend > budget.SkillPoints() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Warning: Step 5 spent %d CP from the combined skills bucket, exceeding the %d CP limit (skills: %d CP, spells: %d CP)."), totalSkillSpend, budget.SkillPoints(), skillSpend, spellSpend))
		} else {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Step 5 spent %d CP from the %d CP combined skills bucket (skills: %d CP, spells: %d CP)."), totalSkillSpend, budget.SkillPoints(), skillSpend, spellSpend))
		}
	})

	equipmentVocabulary := aiFilterThematicVocabularySections(thematicVocabulary, "Equipment")
	equipmentBudgetInfo, err := d.localGenerationEquipmentBudgetInfo(params.StartingWealth)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 6 could not calculate the equipment budget: %v"), err))
		})
		return
	}
	unison.InvokeTask(func() {
		if equipmentBudgetInfo.WealthTrait != "" && equipmentBudgetInfo.AdjustedStartingWealth != equipmentBudgetInfo.BaseStartingWealth {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Step 6 equipment budget: %s after applying %s to the base starting wealth of %s."), aiCurrencyString(equipmentBudgetInfo.AdjustedStartingWealth), equipmentBudgetInfo.WealthTrait, aiCurrencyString(equipmentBudgetInfo.BaseStartingWealth)))
		} else {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Step 6 equipment budget: %s."), aiCurrencyString(equipmentBudgetInfo.AdjustedStartingWealth)))
		}
	})
	equipmentLabel := i18n.Text("Step 6: Equipment")
	equipmentSystemPrompt, equipmentUserPrompt := aiBuildLocalEquipmentPrompts(originalPrompt, params, themes, equipmentBudgetInfo.AdjustedStartingWealth, skillApply.Summary, equipmentVocabulary)
	writeSystemPromptDebugFile(equipmentSystemPrompt)
	equipmentResponse, err := d.queryLocalModel(endpoint, model, buildLocalStatelessMessages(equipmentSystemPrompt, equipmentUserPrompt), aiActionPlanJSONSchema())
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", err.Error())
		})
		return
	}
	equipmentResolution, err := d.resolveFilteredAIResponseText(equipmentResponse, aiEquipmentOnlyActionPlan)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", equipmentResponse)
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 6 could not be resolved: %v"), err))
		})
		return
	}
	equipmentResolution, err = d.executeLocalCorrectionLoop(endpoint, model, equipmentSystemPrompt, equipmentLabel, equipmentResolution, aiEquipmentOnlyActionPlan)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", aiLocalPhaseMessage(equipmentLabel, equipmentResponse))
			for _, warning := range equipmentResolution.Warnings {
				d.addMessage("AI", warning)
			}
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 6 could not continue: %v"), err))
		})
		return
	}
	equipmentApply := d.applyLocalGenerationPhase(equipmentLabel, equipmentResponse, equipmentResolution, targetCP)
	if equipmentApply.Err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 6 could not be applied: %v"), equipmentApply.Err))
		})
		return
	}
	afterEquipmentBudgetInfo, err := d.localGenerationEquipmentBudgetInfo(params.StartingWealth)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 6 budget check could not be calculated: %v"), err))
		})
		return
	}
	equipmentSpend := afterEquipmentBudgetInfo.CurrentEquipmentValue - equipmentBudgetInfo.CurrentEquipmentValue
	if equipmentSpend < 0 {
		equipmentSpend = 0
	}
	unison.InvokeTask(func() {
		if afterEquipmentBudgetInfo.CurrentEquipmentValue > afterEquipmentBudgetInfo.AdjustedStartingWealth {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Warning: Step 6 equipment totals %s, exceeding the cash limit of %s."), aiCurrencyString(afterEquipmentBudgetInfo.CurrentEquipmentValue), aiCurrencyString(afterEquipmentBudgetInfo.AdjustedStartingWealth)))
		} else {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Step 6 purchased %s of additional equipment within the %s cash limit. Current equipment total: %s."), aiCurrencyString(equipmentSpend), aiCurrencyString(afterEquipmentBudgetInfo.AdjustedStartingWealth), aiCurrencyString(afterEquipmentBudgetInfo.CurrentEquipmentValue)))
		}
	})

	auditSummary, err := d.finalizeLocalGenerationAudit(targetCP)
	if err != nil {
		unison.InvokeTask(func() {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI Step 7 could not complete the final audit: %v"), err))
		})
		return
	}
	unison.InvokeTask(func() {
		d.addMessage("AI", fmt.Sprintf("%s\n%s", i18n.Text("Step 7: The Go Audit"), auditSummary))
	})
}

func (d *aiChatDockable) executeLocalBuildPipeline(endpoint, model, originalPrompt string, params aiCharacterRequestParams) {
	d.executeLocalGenerationPipeline(endpoint, model, originalPrompt, params)
}

func AutoBalanceUnspentPoints(entity *gurps.Entity, targetCP int) {
	if entity == nil || targetCP <= 0 {
		return
	}
	entity.TotalPoints = fxp.FromInteger(targetCP)
	entity.Recalculate()
	for i := 0; i < 64; i++ {
		remaining := fxp.AsInteger[int](entity.UnspentPoints())
		if remaining <= 0 {
			break
		}
		if autoBalanceIncrementHighestSkill(entity, remaining) {
			entity.Recalculate()
			continue
		}
		if autoBalanceAddFit(entity, remaining) {
			entity.Recalculate()
			continue
		}
		if autoBalanceAddFallbackSkill(entity, remaining) {
			entity.Recalculate()
			continue
		}
		break
	}
	entity.Recalculate()
}

func autoBalanceIncrementHighestSkill(entity *gurps.Entity, remaining int) bool {
	if entity == nil || remaining <= 0 {
		return false
	}
	skills := make([]*gurps.Skill, 0, len(entity.Skills))
	for _, skill := range entity.Skills {
		if skill == nil || skill.Container() {
			continue
		}
		skills = append(skills, skill)
	}
	if len(skills) == 0 {
		return false
	}
	sort.Slice(skills, func(i, j int) bool {
		leftLevel := skills[i].CalculateLevel(nil).Level
		rightLevel := skills[j].CalculateLevel(nil).Level
		if leftLevel != rightLevel {
			return leftLevel > rightLevel
		}
		leftPoints := skills[i].Points
		rightPoints := skills[j].Points
		if leftPoints != rightPoints {
			return leftPoints > rightPoints
		}
		return aiResolvedSkillDisplayName(skills[i]) < aiResolvedSkillDisplayName(skills[j])
	})
	for _, skill := range skills {
		currentPoints := fxp.AsInteger[int](skill.Points)
		nextPoints := aiNextValidSkillPointCost(currentPoints)
		increment := nextPoints - currentPoints
		if increment <= 0 || increment > remaining {
			continue
		}
		skill.SetRawPoints(fxp.FromInteger(nextPoints))
		return true
	}
	return false
}

func autoBalanceAddFit(entity *gurps.Entity, remaining int) bool {
	if entity == nil || remaining < 5 {
		return false
	}
	before := entity.UnspentPoints()
	var d aiChatDockable
	traits := append([]*gurps.Trait(nil), entity.Traits...)
	updatedTraits, _, err := d.addOrUpdateTrait(entity, traits, aiNamedAction{Name: aiFlexibleString("Fit"), Points: aiFlexibleString("5")})
	if err != nil {
		return false
	}
	entity.SetTraitList(updatedTraits)
	entity.Recalculate()
	return entity.UnspentPoints() < before
}

func autoBalanceAddFallbackSkill(entity *gurps.Entity, remaining int) bool {
	if entity == nil || remaining <= 0 {
		return false
	}
	desiredPoints := aiLargestValidSkillPointCostNotExceeding(remaining)
	if desiredPoints <= 0 {
		return false
	}
	var d aiChatDockable
	for _, name := range aiAutoBalanceFallbackSkillNames {
		before := entity.UnspentPoints()
		skills := append([]*gurps.Skill(nil), entity.Skills...)
		updatedSkills, _, retryItem, err := d.addOrUpdateSkill(entity, skills, aiSkillAction{Name: aiFlexibleString(name), Points: aiFlexibleString(strconv.Itoa(desiredPoints))})
		if err != nil || retryItem != nil {
			continue
		}
		entity.SetSkillList(updatedSkills)
		entity.Recalculate()
		if entity.UnspentPoints() < before {
			return true
		}
	}
	return false
}
