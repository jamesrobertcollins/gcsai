package ux

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

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
	aiPhase1RecommendedTermLimits = map[aiLibraryCategory]int{
		aiLibraryCategoryAdvantage:    8,
		aiLibraryCategoryDisadvantage: 8,
		aiLibraryCategoryQuirk:        6,
	}
	aiPhase2RecommendedTermLimits = map[aiLibraryCategory]int{
		aiLibraryCategorySkill:     12,
		aiLibraryCategoryEquipment: 8,
	}
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

func aiResolvedActionPlanCount(plan aiActionPlan) int {
	return len(plan.Attributes) + len(plan.Advantages) + len(plan.Disadvantages) + len(plan.Quirks) + len(plan.Skills) + len(plan.Equipment)
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

func aiPhase1OnlyActionPlan(plan aiActionPlan) aiActionPlan {
	return aiActionPlan{
		Profile:       plan.Profile,
		Attributes:    append([]aiAttributeAction(nil), plan.Attributes...),
		Advantages:    append([]aiNamedAction(nil), plan.Advantages...),
		Disadvantages: append([]aiNamedAction(nil), plan.Disadvantages...),
		Quirks:        append([]aiNamedAction(nil), plan.Quirks...),
	}
}

func aiPhase2OnlyActionPlan(plan aiActionPlan) aiActionPlan {
	return aiActionPlan{
		Skills:    append([]aiSkillAction(nil), plan.Skills...),
		Equipment: append([]aiNamedAction(nil), plan.Equipment...),
	}
}

func aiSnapSkillPointsInPlan(plan aiActionPlan) aiActionPlan {
	if len(plan.Skills) == 0 {
		return plan
	}
	plan.Skills = append([]aiSkillAction(nil), plan.Skills...)
	for i, action := range plan.Skills {
		pointsText := strings.TrimSpace(firstNonEmptyString(action.Points.String(), action.Value.String()))
		if pointsText == "" {
			continue
		}
		requestedPoints, err := strconv.Atoi(pointsText)
		if err != nil {
			continue
		}
		plan.Skills[i].Points = aiFlexibleString(strconv.Itoa(SnapToValidSkillPoints(requestedPoints)))
		plan.Skills[i].Value = ""
	}
	return plan
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
		d.addMessage("AI", i18n.Text("Local generation pipeline paused after Step 3. Later redesigned stages are not implemented yet."))
	})
}

func (d *aiChatDockable) executeLocalThreePhaseGeneration(endpoint, model, originalPrompt string, params aiCharacterRequestParams) {
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
