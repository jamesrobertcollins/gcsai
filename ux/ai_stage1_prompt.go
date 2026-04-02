package ux

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/richardwilkes/gcs/v5/model/fxp"
)

type aiCharacterRequestParams struct {
	TotalCP           int
	TechLevel         string
	Concept           string
	DisadvantageLimit int
}

type aiStage1SystemPromptData struct {
	aiCharacterRequestParams
	Summary string
}

type aiBuildSessionContext struct {
	OriginalRequest string
	Params          aiCharacterRequestParams
}

type aiPreparedChatRequest struct {
	SystemPrompt   string
	UserPrompt     string
	BuildParams    aiCharacterRequestParams
	IsInitialBuild bool
}

var (
	aiStage1SystemPromptTemplate = template.Must(template.New("ai-stage1-system-prompt").Parse(`You are an expert GURPS 4e Game Master. Your task is to generate a robust, fully fleshed-out character based on the following concept: [{{.Concept}}].

Budget Constraint: You have exactly {{.TotalCP}} Character Points (CP) to spend. You MUST spend all of it, leaving 0 unspent points.
Tech Level: The campaign is TL {{.TechLevel}}. Restrict equipment and skills to this TL unless specified otherwise.

Character Generation Framework:
To ensure the character feels realistic and complete, follow these budgeting guidelines:

Disadvantages & Quirks: Select up to {{.DisadvantageLimit}} points in disadvantages that fit the concept to give yourself more CP to spend. Always select exactly 5 Quirks (-5 points).

Attributes: Spend roughly 40-50% of your total budget on core attributes (ST, DX, IQ, HT) and secondary characteristics.

Advantages: Spend 15-25% of your budget on advantages that fit the background.

Skills: Use the remaining points to select a wide, realistic variety of skills. Do not just pick 5 combat skills and stop. A living character has background skills, hobbies, and professional training. For a {{.TotalCP}} point character, you should aim to generate an expansive list of skills that directly reflect the provided concept.

Do not stop generating until your spent points exactly match the starting budget plus any points gained from disadvantages.

Current character sheet context:
{{.Summary}}

Execution Requirements:
The application will resolve your suggested advantages, disadvantages, quirks, skills, traits, and equipment against the local GCS library after you respond.
Do not invent database ids. Leave the "id" field empty unless you are certain.
Use canonical GURPS Fourth Edition names instead of descriptive paraphrases.
If a fixed specialization is part of the canonical library name, include it in "name". Example: "Driving (Automobile)".
If an item needs a user-defined subject, place, profession, specialty, or other nameable value, put only that value in "notes" and keep "name" focused on the base item. Example: "Area Knowledge" with notes "Mesa".
Use "description" for lore, behavior, magical effects, and special handling notes. Do not put that material in "notes".
Do not invent non-library advantages, disadvantages, skills, or equipment names.
If the concept includes a magical, signature, or supernatural item, represent it through canonical GURPS mechanics such as Signature Gear, Innate Attack, Ally, Blessed, Patron, or Striking ST, and put the lore and special behavior in "description".
Only include an equipment entry when it matches a real library item; otherwise keep the special concept on the trait side.
For attributes, use only attribute ids that already exist on the current character sheet summary above when updating an existing sheet. Do not invent ids such as BX.
If no sheet is active, say so plainly; the application can create one when applying changes.
On your first response to a character-build request, do not stop after only name, title, or other minimal profile data.
Do not wait for the user to ask for attributes, advantages, disadvantages, quirks, skills, or equipment.
If the concept is workable, make reasonable assumptions and produce a substantial first-pass build immediately.
Only ask follow-up questions if the request is critically underspecified and you genuinely cannot build a credible first draft without them.

ADDITIONAL GUIDANCE:
When asked to apply changes, include a single top-level JSON object describing the character-sheet updates.
Below is an EXAMPLE. Do not reuse the example content; it is only a formatting reference:
Keys:
- profile: {"name":"John Smith","gender":"M","age":"25","height":"5'10\"","weight":"180 lbs","hair":"brown","eyes":"blue","skin":"fair","handedness":"Right","title":"Adventurer","organization":"","religion":"","tech_level":"3"}
- attributes: [{"id":"ST","value":"12"}]
- advantages: [{"name":"Signature Gear","description":"Groucho: A sentient magical hammer with a soul.","points":"5"}]
- disadvantages: [{"name":"Code of Honor","notes":"Honor among thieves","points":"-10"}]
- quirks: [{"name":"Must make an entrance","notes":"with Groucho","points":"-1"}]
- skills: [{"name":"Area Knowledge","notes":"Mesa","points":"2"}]
- equipment: [{"name":"Leather Armor","quantity":1}]
- spend_all_cp: true

Only include profile fields if you have determined suitable values for them based on the character concept. For a fresh build, include profile fields when they help complete the concept. For height and weight, use common formats like "5'10\"" or "175 lbs". Include only the profile fields that should be updated; omit others.
If you include JSON, return exactly one top-level JSON object for the entire update.
Do not split updates across multiple JSON objects.
Do not include comments inside the JSON.
Put that JSON object first in the response.
When responding outside JSON, keep the answer concise, factual, and directly tied to GURPS 4e rules.`))
	aiRequestTechLevelPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bTL\s*([0-9]+(?:\^[0-9]+)?(?:/[0-9]+)?)\b`),
		regexp.MustCompile(`(?i)\btech(?:nology)? level\s*[:=]?\s*([0-9]+(?:\^[0-9]+)?(?:/[0-9]+)?)\b`),
	}
	aiRequestTotalCPPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(\d{2,4})\s*[- ]?(?:cp|pts?|point|points)\b`),
		regexp.MustCompile(`(?i)\b(\d{2,4})\s+character points?\b`),
	}
	aiRequestDisadvantageLimitPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bup to\s*-?(\d{1,3})\s*(?:cp|points?)\s+in disadvantages\b`),
		regexp.MustCompile(`(?i)\bdisadvantages?\s+(?:capped at|cap(?:ped)? at|limit(?:ed)? to|up to|worth|total(?:ing)?|maximum of)\s*-?(\d{1,3})\b`),
		regexp.MustCompile(`(?i)\bdisadvantage(?:s)?(?: limit| cap)?\s*[:=]?\s*-?(\d{1,3})\b`),
		regexp.MustCompile(`(?i)\b-?(\d{1,3})\s*(?:cp|points?)\s+(?:of|in)\s+disadvantages\b`),
	}
	aiStage1LeadInPattern        = regexp.MustCompile(`(?i)^\s*(?:please\s+)?(?:help\s+me\s+)?(?:create|build|generate|make|design|write|draft)\s+(?:me\s+)?(?:a|an)?\s*`)
	aiStage1CharacterWordPattern = regexp.MustCompile(`(?i)\b(?:gurps|4e|fourth edition|character|pc|npc|sheet)\b`)
	aiStage1ConstraintCleanup    = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bup to\s*-?\d{1,3}\s*(?:cp|points?)\s+in disadvantages\b`),
		regexp.MustCompile(`(?i)\bdisadvantages?\s+(?:capped at|cap(?:ped)? at|limit(?:ed)? to|up to|worth|total(?:ing)?|maximum of)\s*-?\d{1,3}\b`),
		regexp.MustCompile(`(?i)\bdisadvantage(?:s)?(?: limit| cap)?\s*[:=]?\s*-?\d{1,3}\b`),
		regexp.MustCompile(`(?i)\b-?\d{1,3}\s*(?:cp|points?)\s+(?:of|in)\s+disadvantages\b`),
		regexp.MustCompile(`(?i)\b\d{2,4}\s*[- ]?(?:cp|pts?|point|points)\b`),
		regexp.MustCompile(`(?i)\b\d{2,4}\s+character points?\b`),
		regexp.MustCompile(`(?i)\bTL\s*[0-9]+(?:\^[0-9]+)?(?:/[0-9]+)?\b`),
		regexp.MustCompile(`(?i)\btech(?:nology)? level\s*[:=]?\s*[0-9]+(?:\^[0-9]+)?(?:/[0-9]+)?\b`),
	}
	aiStage1GenerationVerbPattern       = regexp.MustCompile(`(?i)\b(?:create|build|generate|make|design|draft)\b`)
	aiStage1CharacterHintPattern        = regexp.MustCompile(`(?i)\b(?:character|pc|npc|hero|adventurer|detective|knight|wizard|soldier|thief|merchant|swashbuckler|investigator|template)\b`)
	aiStage1BudgetExclusionPattern      = regexp.MustCompile(`(?i)\b(?:disadvantages?|quirks?|advantages?|skills?|spells?|perks?)\b`)
	aiStage1BudgetPrefixPattern         = regexp.MustCompile(`(?i)(?:disadvantages?|quirks?|advantages?|skills?|spells?|perks?)(?:\s+(?:limit|limits|cap|capped|maximum|worth|total|totaling|up|to|of|in|at)){0,3}\s*$`)
	aiStage1ConnectorCleanupPattern     = regexp.MustCompile(`(?i)\b(?:for|in|using|with)\s*$`)
	aiStage1WhitespacePattern           = regexp.MustCompile(`\s+`)
	aiBuildContinuationVerbPattern      = regexp.MustCompile(`(?i)\b(?:add|give|include|pick|choose|apply|continue|finish|complete|expand|fill(?:\s+out)?|spend|use)\b`)
	aiBuildContinuationCategoryPattern  = regexp.MustCompile(`(?i)\b(?:advantages?|disadvantages?|quirks?|skills?|attributes?|equipment|gear|traits?|profile)\b`)
	aiBuildContinuationRemainingPattern = regexp.MustCompile(`(?i)\b(?:remaining|rest|left|leftover|unspent)\b`)
)

func aiRenderStage1SystemPrompt(data aiStage1SystemPromptData) string {
	var builder bytes.Buffer
	_ = aiStage1SystemPromptTemplate.Execute(&builder, data)
	return strings.TrimSpace(builder.String())
}

func aiShouldUseDynamicStage1Prompt(request string, hasPriorUserMessages bool) bool {
	if hasPriorUserMessages {
		return false
	}
	request = strings.TrimSpace(request)
	if request == "" {
		return false
	}
	if aiStage1GenerationVerbPattern.MatchString(request) || aiStage1CharacterHintPattern.MatchString(request) {
		return true
	}
	return aiExtractTotalCP(request) > 0 || aiExtractDisadvantageLimit(request) > 0
}

func aiExtractCharacterRequestParams(request string, defaults aiCharacterRequestParams) aiCharacterRequestParams {
	params := defaults
	request = strings.TrimSpace(request)
	if request == "" {
		return aiNormalizeCharacterRequestParams(params)
	}
	if totalCP := aiExtractTotalCP(request); totalCP > 0 {
		params.TotalCP = totalCP
	}
	if techLevel := aiExtractTechLevel(request); techLevel != "" {
		params.TechLevel = techLevel
	}
	if disadvantageLimit := aiExtractDisadvantageLimit(request); disadvantageLimit > 0 {
		params.DisadvantageLimit = disadvantageLimit
	}
	if concept := aiExtractCharacterConcept(request); concept != "" {
		params.Concept = concept
	}
	if strings.TrimSpace(params.Concept) == "" {
		params.Concept = request
	}
	return aiNormalizeCharacterRequestParams(params)
}

func aiNormalizeCharacterRequestParams(params aiCharacterRequestParams) aiCharacterRequestParams {
	if params.TotalCP <= 0 {
		params.TotalCP = 150
	}
	params.TechLevel = normalizeAIRequestTechLevel(params.TechLevel)
	if params.TechLevel == "" {
		params.TechLevel = "3"
	}
	params.Concept = strings.TrimSpace(params.Concept)
	if params.Concept == "" {
		params.Concept = "Adventurer"
	}
	if params.DisadvantageLimit <= 0 {
		params.DisadvantageLimit = aiDefaultDisadvantageLimit(params.TotalCP)
	}
	return params
}

func aiDefaultDisadvantageLimit(totalCP int) int {
	if totalCP <= 0 {
		return 50
	}
	limit := totalCP / 2
	if limit < 10 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	return limit
}

func normalizeAIRequestTechLevel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, pattern := range aiRequestTechLevelPatterns {
		if match := pattern.FindStringSubmatch(raw); len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
	}
	upper := strings.ToUpper(raw)
	upper = strings.TrimPrefix(upper, "TL")
	return strings.TrimSpace(upper)
}

func aiExtractTechLevel(request string) string {
	for _, pattern := range aiRequestTechLevelPatterns {
		if match := pattern.FindStringSubmatch(request); len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

func aiExtractTotalCP(request string) int {
	type candidate struct {
		value   int
		ranking int
	}
	var candidates []candidate
	for _, pattern := range aiRequestTotalCPPatterns {
		matches := pattern.FindAllStringSubmatchIndex(request, -1)
		for _, match := range matches {
			if len(match) < 4 {
				continue
			}
			fullStart, fullEnd := match[0], match[1]
			valueStart, valueEnd := match[2], match[3]
			if fullStart < 0 || fullEnd > len(request) || valueStart < 0 || valueEnd > len(request) {
				continue
			}
			prefixStart := fullStart - 20
			if prefixStart < 0 {
				prefixStart = 0
			}
			suffixEnd := fullEnd + 20
			if suffixEnd > len(request) {
				suffixEnd = len(request)
			}
			prefix := request[prefixStart:fullStart]
			suffix := request[fullEnd:suffixEnd]
			if aiStage1BudgetPrefixPattern.MatchString(prefix) || aiStage1BudgetExclusionPattern.MatchString(suffix) {
				continue
			}
			value, err := strconv.Atoi(request[valueStart:valueEnd])
			if err != nil || value <= 0 {
				continue
			}
			ranking := value
			fullMatch := strings.ToLower(strings.TrimSpace(request[fullStart:fullEnd]))
			if strings.Contains(fullMatch, "cp") || strings.Contains(fullMatch, "character points") {
				ranking += 10000
			}
			if strings.Contains(fullMatch, "-") {
				ranking += 2000
			}
			if strings.Contains(fullMatch, "point") {
				ranking += 5000
			}
			candidates = append(candidates, candidate{value: value, ranking: ranking})
		}
	}
	if len(candidates) == 0 {
		return 0
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ranking != candidates[j].ranking {
			return candidates[i].ranking > candidates[j].ranking
		}
		return candidates[i].value > candidates[j].value
	})
	return candidates[0].value
}

func aiExtractDisadvantageLimit(request string) int {
	for _, pattern := range aiRequestDisadvantageLimitPatterns {
		if match := pattern.FindStringSubmatch(request); len(match) == 2 {
			value, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(match[1]), "-"))
			if err == nil && value > 0 {
				return value
			}
		}
	}
	return 0
}

func aiExtractCharacterConcept(request string) string {
	concept := strings.TrimSpace(request)
	if concept == "" {
		return ""
	}
	for _, pattern := range aiStage1ConstraintCleanup {
		concept = pattern.ReplaceAllString(concept, " ")
	}
	concept = aiStage1LeadInPattern.ReplaceAllString(concept, "")
	concept = aiStage1CharacterWordPattern.ReplaceAllString(concept, " ")
	concept = aiStage1WhitespacePattern.ReplaceAllString(concept, " ")
	concept = strings.TrimSpace(strings.Trim(concept, "[](){}.,;:-"))
	concept = aiStage1ConnectorCleanupPattern.ReplaceAllString(concept, "")
	concept = strings.TrimSpace(strings.Trim(concept, "[](){}.,;:-"))
	if concept == "" {
		return strings.TrimSpace(request)
	}
	return concept
}

func (d *aiChatDockable) aiSystemPromptForRequest(request string) string {
	if aiShouldUseDynamicStage1Prompt(request, d.buildSession != nil) {
		return d.aiStage1SystemPrompt(request)
	}
	return d.aiAssistantSystemPrompt()
}

func aiShouldUseBuildContinuationPrompt(request string) bool {
	request = strings.TrimSpace(request)
	if request == "" || aiShouldUseDynamicStage1Prompt(request, false) {
		return false
	}
	if strings.Contains(request, "?") && !aiBuildContinuationCategoryPattern.MatchString(request) && !aiBuildContinuationRemainingPattern.MatchString(request) {
		return false
	}
	if aiBuildContinuationRemainingPattern.MatchString(request) {
		return true
	}
	if aiBuildContinuationVerbPattern.MatchString(request) && (aiBuildContinuationCategoryPattern.MatchString(request) || len(strings.Fields(request)) <= 8) {
		return true
	}
	return aiBuildContinuationCategoryPattern.MatchString(request) && len(strings.Fields(request)) <= 6
}

func (d *aiChatDockable) prepareAIRequest(request string) aiPreparedChatRequest {
	request = strings.TrimSpace(request)
	if aiShouldUseDynamicStage1Prompt(request, d.buildSession != nil) {
		params := aiExtractCharacterRequestParams(request, d.aiDefaultCharacterRequestParams(request))
		d.buildSession = &aiBuildSessionContext{OriginalRequest: request, Params: params}
		return aiPreparedChatRequest{
			SystemPrompt:   d.aiStage1SystemPrompt(request),
			UserPrompt:     request,
			BuildParams:    params,
			IsInitialBuild: true,
		}
	}
	if d.buildSession != nil && aiShouldUseBuildContinuationPrompt(request) {
		return aiPreparedChatRequest{
			SystemPrompt: d.aiBuildContinuationSystemPrompt(request, d.buildSession.Params),
			UserPrompt:   aiBuildContinuationUserPrompt(request, d.buildSession.Params),
			BuildParams:  d.buildSession.Params,
		}
	}
	return aiPreparedChatRequest{
		SystemPrompt: d.aiAssistantSystemPrompt(),
		UserPrompt:   request,
		BuildParams:  d.aiDefaultCharacterRequestParams(request),
	}
}

func (d *aiChatDockable) aiStage1SystemPrompt(request string) string {
	return aiRenderStage1SystemPrompt(aiStage1SystemPromptData{
		aiCharacterRequestParams: aiExtractCharacterRequestParams(request, d.aiDefaultCharacterRequestParams(request)),
		Summary:                  d.currentCharacterSummary(),
	})
}

func (d *aiChatDockable) aiBuildContinuationSystemPrompt(_ string, params aiCharacterRequestParams) string {
	return strings.TrimSpace(fmt.Sprintf(`You are continuing an in-progress GURPS Fourth Edition character build for concept [%s].

Target budget: exactly %d CP.
Tech Level: TL %s.
Disadvantage limit: up to %d points.

Keep using the same budgeting guidance as the initial build:
- Disadvantages and quirks: use up to %d points in disadvantages and maintain a full quirk set when building out the character.
- Attributes: spend roughly 40-50%% of the total budget on core attributes and secondary characteristics.
- Advantages: spend roughly 15-25%% of the total budget on concept-fitting advantages.
- Skills: spend the remaining budget on a broad, realistic list of skills, not just a few headline combat skills.

Treat the latest user instruction as an incremental request against the current character sheet.
Return only the new or changed JSON needed for this turn.
Do not repeat profile fields, attributes, skills, advantages, disadvantages, quirks, or equipment that are already on the sheet unless you are intentionally changing them.
If the user asks for a category such as advantages, disadvantages, quirks, skills, attributes, or equipment, focus on that category first while keeping the overall build coherent.
If the sheet still has large unspent CP or obvious gaps, continue filling the build instead of replaying the previous output.

Current character sheet context:
%s

Execution Requirements:
The application will resolve your suggested advantages, disadvantages, quirks, skills, traits, and equipment against the local GCS library after you respond.
Do not invent database ids. Leave the "id" field empty unless you are certain.
Use canonical GURPS Fourth Edition names instead of descriptive paraphrases.
If a fixed specialization is part of the canonical library name, include it in "name". Example: "Driving (Automobile)".
If an item needs a user-defined subject, place, profession, specialty, or other nameable value, put only that value in "notes" and keep "name" focused on the base item. Example: "Area Knowledge" with notes "Mesa".
Use "description" for lore, behavior, magical effects, and special handling notes. Do not put that material in "notes".
Do not invent non-library advantages, disadvantages, skills, or equipment names.
If the concept includes a magical, signature, or supernatural item, represent it through canonical GURPS mechanics such as Signature Gear, Innate Attack, Ally, Blessed, Patron, or Striking ST, and put the lore and special behavior in "description".
Only include an equipment entry when it matches a real library item; otherwise keep the special concept on the trait side.
For attributes, use only attribute ids that already exist on the current character sheet summary above when updating an existing sheet. Do not invent ids such as BX.
If you include JSON, return exactly one top-level JSON object for the entire update.
Do not split updates across multiple JSON objects.
Do not include comments inside the JSON.
Put that JSON object first in the response.
When responding outside JSON, keep the answer concise, factual, and directly tied to GURPS 4e rules.`, params.Concept, params.TotalCP, params.TechLevel, params.DisadvantageLimit, params.DisadvantageLimit, d.currentCharacterSummary()))
}

func (d *aiChatDockable) aiDefaultCharacterRequestParams(request string) aiCharacterRequestParams {
	params := aiCharacterRequestParams{
		TotalCP:   150,
		TechLevel: "3",
		Concept:   strings.TrimSpace(request),
	}
	if sheet := d.activeOrOpenSheet(); sheet != nil && sheet.entity != nil {
		entity := sheet.entity
		if total := fxp.AsInteger[int](entity.TotalPoints); total > 0 {
			params.TotalCP = total
		}
		if techLevel := normalizeAIRequestTechLevel(entity.Profile.TechLevel); techLevel != "" {
			params.TechLevel = techLevel
		}
		if concept := strings.TrimSpace(entity.Profile.Title); concept != "" {
			params.Concept = concept
		}
	}
	params.DisadvantageLimit = aiDefaultDisadvantageLimit(params.TotalCP)
	return aiNormalizeCharacterRequestParams(params)
}

func aiCharacterBuildActionCount(plan aiActionPlan) int {
	return len(plan.Attributes) + len(plan.Advantages) + len(plan.Disadvantages) + len(plan.Quirks) + len(plan.Skills) + len(plan.Equipment)
}

func aiActionPlanNeedsCharacterBuildCompletion(plan aiActionPlan) bool {
	if !hasAIActionPlanContent(plan) {
		return true
	}
	traitCount := len(plan.Advantages) + len(plan.Disadvantages) + len(plan.Quirks)
	categoryCount := 0
	if len(plan.Attributes) > 0 {
		categoryCount++
	}
	if traitCount > 0 {
		categoryCount++
	}
	if len(plan.Skills) > 0 {
		categoryCount++
	}
	if len(plan.Equipment) > 0 {
		categoryCount++
	}
	if len(plan.Attributes) == 0 || len(plan.Skills) == 0 {
		return true
	}
	if traitCount == 0 && len(plan.Equipment) == 0 {
		return true
	}
	if categoryCount < 3 {
		return true
	}
	return aiCharacterBuildActionCount(plan) < 6
}

func aiCharacterBuildMissingSections(plan aiActionPlan) []string {
	missing := make([]string, 0, 4)
	if len(plan.Attributes) == 0 {
		missing = append(missing, "attributes")
	}
	if len(plan.Advantages)+len(plan.Disadvantages)+len(plan.Quirks) == 0 {
		missing = append(missing, "advantages/disadvantages/quirks")
	}
	if len(plan.Skills) == 0 {
		missing = append(missing, "skills")
	}
	if len(plan.Equipment) == 0 {
		missing = append(missing, "equipment")
	}
	if aiCharacterBuildActionCount(plan) < 6 {
		missing = append(missing, "overall build depth")
	}
	return missing
}

func aiBuildCharacterExpansionPrompt(originalRequest string, params aiCharacterRequestParams, plan aiActionPlan) string {
	missing := aiCharacterBuildMissingSections(plan)
	var builder strings.Builder
	builder.WriteString("Your previous response was too incomplete for an initial GURPS 4e character build.\n")
	builder.WriteString("Do not wait for the user to ask for more details. Expand the build now.\n")
	builder.WriteString("Return ONLY a single JSON object with a substantially complete first-pass character build.\n")
	builder.WriteString("Original request: ")
	builder.WriteString(strconvQuote(originalRequest))
	builder.WriteByte('\n')
	builder.WriteString(fmt.Sprintf("Budget: %d CP | TL %s | Concept: %s | Disadvantage limit: %d\n", params.TotalCP, params.TechLevel, params.Concept, params.DisadvantageLimit))
	if len(missing) > 0 {
		builder.WriteString("The previous JSON was missing or underfilled: ")
		builder.WriteString(strings.Join(missing, ", "))
		builder.WriteString(".\n")
	}
	builder.WriteString("Requirements:\n")
	builder.WriteString("- Include attribute adjustments that define the character.\n")
	builder.WriteString("- Include meaningful advantages, disadvantages, and quirks appropriate to the concept.\n")
	builder.WriteString("- Include a broad skill list, not just one or two headline skills.\n")
	builder.WriteString("- Include relevant starting equipment for the concept and TL when appropriate.\n")
	builder.WriteString("- Set spend_all_cp to true once the build is substantially complete.\n")
	builder.WriteString("- Make reasonable assumptions instead of waiting for another user message.\n")
	builder.WriteString("Return ONLY the JSON object.\n")
	return builder.String()
}

func aiBuildContinuationUserPrompt(request string, params aiCharacterRequestParams) string {
	var builder strings.Builder
	builder.WriteString("Continue the same GURPS 4e character build.\n")
	builder.WriteString("Latest user instruction: ")
	builder.WriteString(strconvQuote(request))
	builder.WriteByte('\n')
	builder.WriteString(fmt.Sprintf("Target budget remains %d CP at TL %s for concept %s.\n", params.TotalCP, params.TechLevel, params.Concept))
	builder.WriteString("Return ONLY incremental JSON updates for this turn.\n")
	builder.WriteString("Do not repeat items already on the character sheet unless you are changing them.\n")
	return builder.String()
}

func aiRecommendedTermsPromptBlock(recommendedTerms string) string {
	recommendedTerms = strings.TrimSpace(recommendedTerms)
	if recommendedTerms == "" {
		return ""
	}
	return recommendedTerms + "\nStrongly prefer selecting items from this list when they fit the concept.\n\n"
}

func aiBuildLocalPhase1Prompts(originalRequest string, params aiCharacterRequestParams, summary, recommendedTerms string) (systemPrompt, userPrompt string) {
	recommendedTermsBlock := aiRecommendedTermsPromptBlock(recommendedTerms)
	systemPrompt = strings.TrimSpace(fmt.Sprintf(`You are a deterministic GURPS 4e JSON generation function.
Return exactly one top-level JSON object and nothing else.
The Go application will resolve library entries, compute exact point totals, and perform final balancing after you respond.
Use canonical GURPS Fourth Edition names. Leave the "id" field empty unless you are certain.
If a fixed specialization is part of the canonical library name, include it in "name".
If an item needs a user-defined subject, place, profession, specialty, or other nameable value, put only that value in "notes" and keep "name" focused on the base item.
Use "description" for lore, behavior, magical effects, and special handling notes. Do not put that material in "notes".
Do not invent non-library advantages, disadvantages, skills, or equipment names.
If the concept includes a magical, signature, or supernatural item, represent it through canonical GURPS mechanics such as Signature Gear, Innate Attack, Ally, Blessed, Patron, or Striking ST, and put the lore and special behavior in "description".
Only include an equipment entry when it matches a real library item; otherwise keep the special concept on the trait side.
For a fresh build, include a credible profile block when it helps complete the concept. Prefer setting name, title, age, gender, height, weight, and other obvious identity details when you can infer them reasonably.

Current character sheet context:
%s`, summary))

	userPrompt = strings.TrimSpace(fmt.Sprintf(`Phase 1: The Core Chassis.
Original request: %s
Concept: %s
Target total budget: exactly %d CP.
Tech Level: TL %s.
Disadvantage limit: up to %d points.

%sThis phase may output ONLY these JSON fields:
- profile
- attributes
- advantages
- disadvantages
- quirks

Do not include skills, equipment, or spend_all_cp in this phase.
For a fresh build, include profile fields that materially complete the concept. Prefer setting a name plus age, gender, height, weight, eyes, hair, skin, handedness, and title when you can infer them reasonably.

Budget guidance for this chassis phase:
- Spend roughly 40-50%% of the total budget on attributes and secondary characteristics.
- Spend roughly 15-25%% of the total budget on advantages.
- Use up to %d points in disadvantages that fit the concept.
- Include exactly 5 quirks when the concept supports them.
- Leave enough budget for a large Phase 2 skill package.
- If the concept includes a magical, signature, or supernatural item, model it here through canonical advantages, disadvantages, or quirks and put the lore in "description".

Build a strong mechanical chassis for the concept and return exactly one JSON object.`, strconvQuote(originalRequest), params.Concept, params.TotalCP, params.TechLevel, params.DisadvantageLimit, recommendedTermsBlock, params.DisadvantageLimit))
	return systemPrompt, userPrompt
}

func aiBuildLocalPhase2Prompts(originalRequest string, params aiCharacterRequestParams, remainingCP int, summary, recommendedTerms string) (systemPrompt, userPrompt string) {
	recommendedTermsBlock := aiRecommendedTermsPromptBlock(recommendedTerms)
	systemPrompt = strings.TrimSpace(fmt.Sprintf(`You are a deterministic GURPS 4e JSON generation function.
Return exactly one top-level JSON object and nothing else.
The Go application will resolve library entries, snap skill points to valid GURPS 4e values, and perform final balancing after you respond.
Use canonical GURPS Fourth Edition names. Leave the "id" field empty unless you are certain.
If a fixed specialization is part of the canonical library name, include it in "name".
If an item needs a user-defined subject, place, profession, specialty, or other nameable value, put only that value in "notes" and keep "name" focused on the base item.
Use "description" for lore, behavior, magical effects, and special handling notes. Do not put that material in "notes".
Do not invent non-library advantages, disadvantages, skills, or equipment names.
Only include an equipment entry when it matches a real library item; otherwise keep the special concept on the trait side.

Current character sheet context:
%s`, summary))

	userPrompt = strings.TrimSpace(fmt.Sprintf(`Phase 2: The Professional Package.
Original request: %s
Concept: %s
Target total budget: exactly %d CP.
Tech Level: TL %s.
Exactly %d CP remain after Phase 1.

%sThis phase may output ONLY these JSON fields:
- skills
- equipment

Do not include profile, attributes, advantages, disadvantages, quirks, or spend_all_cp in this phase.

Instructions:
- Spend all remaining character points on an expansive, concept-appropriate list of skills.
- Equipment may be included when relevant, but the Go application handles CP math; focus your budgeting on skills.
- Prefer a broad professional package with occupational, background, hobby, and practical skills, not just a few headline combat skills.
- Avoid padding the build with multiple near-duplicate skill-family variants. In most concepts, choose at most one variant each of Area Knowledge, Current Affairs, Connoisseur, Savoir-Faire, Expert Skill, Hobby Skill, and similar broad families unless the concept clearly requires more.
- Use integer skill point requests that reflect intended emphasis. The Go application will snap them to valid GURPS 4e point costs.
- Only include equipment when it resolves to a real library item. If the concept's special item is primarily magical or signature, keep that concept on the trait side and use "description" there instead of inventing equipment.

Return exactly one JSON object.`, strconvQuote(originalRequest), params.Concept, params.TotalCP, params.TechLevel, remainingCP, recommendedTermsBlock))
	return systemPrompt, userPrompt
}
