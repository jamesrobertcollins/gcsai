// Copyright (c) 1998-2025 by Richard A. Wilkes. All rights reserved.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, version 2.0. If a copy of the MPL was not distributed with
// this file, You can obtain one at http://mozilla.org/MPL/2.0/.
//
// This Source Code Form is "Incompatible With Secondary Licenses", as
// defined by the Mozilla Public License, version 2.0.

package ux

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"github.com/richardwilkes/gcs/v5/model/fxp"
	"github.com/richardwilkes/gcs/v5/model/gurps"
	"github.com/richardwilkes/gcs/v5/model/gurps/enums/dgroup"
	"github.com/richardwilkes/gcs/v5/svg"
	"github.com/richardwilkes/toolbox/v2/geom"
	"github.com/richardwilkes/toolbox/v2/i18n"
	"github.com/richardwilkes/unison"
	"github.com/richardwilkes/unison/enums/align"
	"github.com/richardwilkes/unison/enums/behavior"
	"google.golang.org/api/option"
)

const (
	aiChatDockKey = "ai.chat"
)

var (
	_ unison.Dockable  = &aiChatDockable{}
	_ KeyedDockable    = &aiChatDockable{}
	_ unison.TabCloser = &aiChatDockable{}
)

type aiChatDockable struct {
	unison.Panel
	historyPanel    *unison.Panel
	scroll          *unison.ScrollPanel
	inputField      *unison.Field
	submitButton    *unison.Button
	chatHistory     []*genai.Content
	isThinking      bool
	thinkingMessage *unison.Panel
}

// ShowAIChat shows the AI Chat window.
func ShowAIChat() {
	if Activate(func(d unison.Dockable) bool {
		_, ok := d.AsPanel().Self.(*aiChatDockable)
		return ok
	}) {
		return
	}
	d := &aiChatDockable{}
	d.Self = d
	d.initContent()
	PlaceInDock(d, dgroup.Editors, false)
	ActivateDockable(d)
	d.inputField.RequestFocus()
}

// DockKey implements KeyedDockable.
func (d *aiChatDockable) DockKey() string {
	return aiChatDockKey
}

// Title implements unison.Dockable.
func (d *aiChatDockable) Title() string {
	return i18n.Text("AI Chat")
}

// TitleIcon implements unison.Dockable.
func (d *aiChatDockable) TitleIcon(suggestedSize geom.Size) unison.Drawable {
	return &unison.DrawableSVG{SVG: svg.Bot, Size: suggestedSize}
}

// Tooltip implements unison.Dockable.
func (d *aiChatDockable) Tooltip() string {
	return ""
}

// Modified implements unison.Dockable.
func (d *aiChatDockable) Modified() bool {
	return false
}

// MayAttemptClose implements unison.TabCloser.
func (d *aiChatDockable) MayAttemptClose() bool {
	return !d.isThinking
}

// AttemptClose implements unison.TabCloser.
func (d *aiChatDockable) AttemptClose() bool {
	if !d.MayAttemptClose() {
		return false
	}
	AttemptCloseForDockable(d)
	return true
}

func (d *aiChatDockable) initContent() {
	d.SetLayout(&unison.FlexLayout{Columns: 1})
	// Toolbar
	toolbar := unison.NewPanel()
	toolbar.SetLayout(&unison.FlowLayout{HSpacing: unison.StdHSpacing})
	clearButton := unison.NewSVGButton(svg.Trash)
	clearButton.Tooltip = newWrappedTooltip(i18n.Text("Clear Chat History"))
	clearButton.ClickCallback = d.clearHistory
	toolbar.AddChild(clearButton)
	configButton := unison.NewSVGButton(svg.Gears)
	configButton.Tooltip = newWrappedTooltip(i18n.Text("Configure AI Settings"))
	configButton.ClickCallback = ShowAISettings
	toolbar.AddChild(configButton)
	d.AddChild(toolbar)
	// Chat History
	d.historyPanel = unison.NewPanel()
	d.historyPanel.SetLayout(&unison.FlexLayout{Columns: 1, VSpacing: unison.StdVSpacing, HAlign: align.Fill})
	d.scroll = unison.NewScrollPanel()
	d.scroll.SetContent(d.historyPanel, behavior.Fill, behavior.Unmodified)
	d.scroll.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill, VAlign: align.Fill, HGrab: true, VGrab: true})
	d.AddChild(d.scroll)
	// Input area
	inputArea := unison.NewPanel()
	inputArea.SetLayout(&unison.FlexLayout{Columns: 2, HSpacing: unison.StdHSpacing})
	inputArea.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill, HGrab: true})
	d.inputField = unison.NewMultiLineField()
	d.inputField.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill, VAlign: align.Fill, HGrab: true, MinSize: geom.Size{Height: 50}})
	d.inputField.KeyDownCallback = func(keyCode unison.KeyCode, mod unison.Modifiers, repeat bool) bool {
		if (keyCode == unison.KeyReturn || keyCode == unison.KeyNumPadEnter) && mod.ControlDown() {
			d.submit()
			return true
		}
		return d.inputField.DefaultKeyDown(keyCode, mod, repeat)
	}
	inputArea.AddChild(d.inputField)
	d.submitButton = unison.NewButton()
	d.submitButton.SetTitle(i18n.Text("Submit"))
	d.submitButton.ClickCallback = d.submit
	d.submitButton.SetLayoutData(align.End)
	inputArea.AddChild(d.submitButton)
	d.AddChild(inputArea)
}

func (d *aiChatDockable) clearHistory() {
	if unison.QuestionDialog(i18n.Text("Are you sure you want to clear the chat history?"), "") == unison.ModalResponseOK {
		d.chatHistory = nil
		d.historyPanel.RemoveAllChildren()
		d.historyPanel.MarkForLayoutAndRedraw()
	}
}

func (d *aiChatDockable) submit() {
	if d.isThinking {
		return
	}
	text := d.inputField.Text()
	if strings.TrimSpace(text) == "" {
		return
	}
	d.inputField.SetText("")
	d.addMessage(i18n.Text("You"), text)
	settings := gurps.GlobalSettings().AI
	switch settings.Provider {
	case gurps.AIProviderGemini:
		d.queryGemini(text)
	case gurps.AIProviderLocal:
		d.queryLocal(text)
	default:
		d.addMessage("AI", i18n.Text("No AI provider is configured. Please configure one in the AI Settings."))
	}
}

func (d *aiChatDockable) addMessage(author, message string) *unison.Panel {
	authorLabel := unison.NewLabel()
	authorLabel.SetTitle(author)
	messageMarkdown := unison.NewMarkdown(false)
	messageMarkdown.SetContent(message, 0)
	adjustMarkdownThemeForPage(messageMarkdown, unison.DefaultLabelTheme.Font)
	wrapper := unison.NewPanel()
	wrapper.SetLayout(&unison.FlexLayout{Columns: 1, VSpacing: unison.StdVSpacing / 2})
	wrapper.AddChild(authorLabel)
	wrapper.AddChild(messageMarkdown)
	wrapper.SetBorder(unison.NewCompoundBorder(unison.NewLineBorder(unison.ThemeSurfaceEdge, geom.Size{}, geom.NewUniformInsets(1), false), unison.NewEmptyBorder(unison.StdInsets())))
	d.historyPanel.AddChild(wrapper)
	d.historyPanel.MarkForLayoutAndRedraw()
	return wrapper
}

func (d *aiChatDockable) aiAssistantSystemPrompt() string {
	summary := d.currentCharacterSummary()
	libraryReference := d.availableLibrarySummary()
	return strings.TrimSpace(fmt.Sprintf(`You are a GURPS Fourth Edition character builder assistant.
Use the current character sheet state and the player's concept to make recommendations.
Always validate choices, compute point costs, and keep Tech Level constraints in mind.
Evaluate available library options against the character concept, game-world setting, and Tech Level.
Choose advantages, disadvantages, quirks, and skills only from loaded library entries. Do not invent custom ability names.
Prefer existing library equipment first; only create a custom item if no suitable library equipment exists.
If the user asks for changes, propose specific character-sheet updates and a clear CP breakdown.
If no sheet is active, ask the user to open or create a character sheet before proceeding.

%s

%s

When asked to apply changes, include a top-level JSON object with keys such as:
- profile: {"name":"John Smith","gender":"M","age":"25","height":"5'10\"","weight":"180 lbs","hair":"brown","eyes":"blue","skin":"fair","handedness":"Right","title":"Adventurer","organization":"","religion":"","tech_level":"3"}
- attributes: [{"id":"ST","value":"12"}]
- advantages: [{"name":"Combat Reflexes","points":"15"}]
- disadvantages: [{"name":"Code of Honor","points":"-10"}]
- quirks: [{"name":"Must make an entrance","points":"-1"}]
- skills: [{"name":"Brawling","points":"4"}]
- equipment: [{"name":"Leather Armor","quantity":1}]
- spend_all_cp: true

Only include profile fields if you have determined suitable values for them based on the character concept. For height and weight, use common formats like "5'10\"" or "175 lbs". Include only the profile fields that should be updated; omit others.
If you include JSON, put the JSON object as the first top-level object in the response.
When responding, keep answers concise, factual, and directly tied to GURPS 4e rules.`, summary, libraryReference))
}

func (d *aiChatDockable) currentCharacterSummary() string {
	sheet := d.activeOrOpenSheet()
	if sheet == nil || sheet.entity == nil {
		return "No active GURPS sheet is open. If you ask to build a character, I will create a new sheet and apply the changes there."
	}
	entity := sheet.entity
	var builder strings.Builder
	concept := strings.TrimSpace(entity.Profile.Title)
	if concept == "" {
		concept = "(not specified)"
	}
	tl := strings.TrimSpace(entity.Profile.TechLevel)
	if tl == "" {
		tl = "(not specified)"
	}
	builder.WriteString(fmt.Sprintf("Current character concept: %s\n", concept))
	builder.WriteString(fmt.Sprintf("Tech Level: %s\n", tl))
	unspent := entity.UnspentPoints()
	builder.WriteString(fmt.Sprintf("Total CP: %s, Unspent CP: %s\n", entity.TotalPoints.String(), unspent.String()))
	if unspent < 0 {
		builder.WriteString(fmt.Sprintf("Overspent by: %s\n", (-unspent).String()))
	}

	attributes := entity.Attributes.List()
	if len(attributes) > 0 {
		builder.WriteString("Attributes: ")
		for i, attr := range attributes {
			if i > 0 {
				builder.WriteString(", ")
			}
			name := attr.AttrID
			if def := attr.AttributeDef(); def != nil {
				name = def.Name
			}
			builder.WriteString(fmt.Sprintf("%s %s", name, attr.Current().String()))
		}
		builder.WriteString("\n")
	}

	advantages := make([]string, 0)
	disadvantages := make([]string, 0)
	for _, trait := range entity.Traits {
		if trait.Disabled || trait.Container() {
			continue
		}
		points := trait.AdjustedPoints()
		if points > 0 {
			advantages = append(advantages, fmt.Sprintf("%s (+%s)", trait.Name, points.String()))
		} else if points < 0 {
			disadvantages = append(disadvantages, fmt.Sprintf("%s (%s)", trait.Name, points.String()))
		}
	}
	if len(advantages) > 0 {
		builder.WriteString("Advantages: ")
		builder.WriteString(strings.Join(advantages, ", "))
		builder.WriteString("\n")
	}
	if len(disadvantages) > 0 {
		builder.WriteString("Disadvantages: ")
		builder.WriteString(strings.Join(disadvantages, ", "))
		builder.WriteString("\n")
	}

	skills := make([]string, 0, len(entity.Skills))
	for _, skill := range entity.Skills {
		if skill.Container() {
			continue
		}
		name := skill.Name
		if name == "" {
			name = "Unnamed Skill"
		}
		skills = append(skills, fmt.Sprintf("%s (%s pts)", name, skill.Points.String()))
	}
	if len(skills) > 0 {
		builder.WriteString("Skills: ")
		builder.WriteString(strings.Join(skills, ", "))
		builder.WriteString("\n")
	}

	return strings.TrimSpace(builder.String())
}

func (d *aiChatDockable) activeOrOpenSheet() *Sheet {
	if sheet := ActiveSheet(); sheet != nil {
		return sheet
	}
	openSheets := OpenSheets(nil)
	if len(openSheets) > 0 {
		return openSheets[0]
	}
	return nil
}

func (d *aiChatDockable) sheetOrCreateNew() *Sheet {
	sheet := d.activeOrOpenSheet()
	if sheet != nil {
		ActivateDockable(sheet)
		return sheet
	}
	entity := gurps.NewEntity()
	sheet = NewSheet(i18n.Text("Untitled")+gurps.SheetExt, entity)
	DisplayNewDockable(sheet)
	ActivateDockable(sheet)
	d.addMessage("AI", i18n.Text("No active sheet was open, so a new character sheet has been created for AI updates."))
	return sheet
}

func (d *aiChatDockable) availableLibrarySummary() string {
	libraries := gurps.GlobalSettings().Libraries()
	if libraries == nil {
		return "Library availability is unknown."
	}
	skills, advantages, disadvantages, quirks, equipment := d.collectAvailableLibraryItemNames(libraries)
	if len(skills) == 0 && len(advantages) == 0 && len(disadvantages) == 0 && len(quirks) == 0 && len(equipment) == 0 {
		return "No library items were available to summarize."
	}
	var builder strings.Builder
	builder.WriteString("Library reference:\n")
	if len(skills) > 0 {
		builder.WriteString(fmt.Sprintf("Skills available: %d names. Examples: %s\n", len(skills), strings.Join(firstN(skills, 6), ", ")))
	}
	if len(advantages) > 0 {
		builder.WriteString(fmt.Sprintf("Advantages available: %d names. Examples: %s\n", len(advantages), strings.Join(firstN(advantages, 6), ", ")))
	}
	if len(disadvantages) > 0 {
		builder.WriteString(fmt.Sprintf("Disadvantages available: %d names. Examples: %s\n", len(disadvantages), strings.Join(firstN(disadvantages, 6), ", ")))
	}
	if len(quirks) > 0 {
		builder.WriteString(fmt.Sprintf("Quirks available: %d names. Examples: %s\n", len(quirks), strings.Join(firstN(quirks, 6), ", ")))
	}
	if len(equipment) > 0 {
		builder.WriteString(fmt.Sprintf("Equipment available: %d names. Examples: %s\n", len(equipment), strings.Join(firstN(equipment, 6), ", ")))
	}
	return strings.TrimSpace(builder.String())
}

func firstN(list []string, n int) []string {
	if len(list) <= n {
		return list
	}
	return list[:n]
}

func (d *aiChatDockable) collectAvailableLibraryItemNames(libraries gurps.Libraries) (skills, advantages, disadvantages, quirks, equipment []string) {
	skillsSet := make(map[string]struct{})
	advantagesSet := make(map[string]struct{})
	disadvantagesSet := make(map[string]struct{})
	quirksSet := make(map[string]struct{})
	equipmentSet := make(map[string]struct{})

	loadItems := func(ext string, loader func(fs.FS, string) ([]any, error), categorize func(any)) {
		for _, set := range gurps.ScanForNamedFileSets(nil, "", false, libraries, ext) {
			for _, ref := range set.List {
				items, err := loader(ref.FileSystem, ref.FilePath)
				if err != nil {
					continue
				}
				for _, item := range items {
					categorize(item)
				}
			}
		}
	}

	loadItems(gurps.SkillsExt, func(fsys fs.FS, filePath string) ([]any, error) {
		rows, err := gurps.NewSkillsFromFile(fsys, filePath)
		if err != nil {
			return nil, err
		}
		result := make([]any, len(rows))
		for i, r := range rows {
			result[i] = r
		}
		return result, nil
	}, func(item any) {
		skill := item.(*gurps.Skill)
		if skill.Name != "" && !skill.Container() {
			skillsSet[skill.Name] = struct{}{}
		}
	})

	loadItems(gurps.TraitsExt, func(fsys fs.FS, filePath string) ([]any, error) {
		rows, err := gurps.NewTraitsFromFile(fsys, filePath)
		if err != nil {
			return nil, err
		}
		result := make([]any, len(rows))
		for i, r := range rows {
			result[i] = r
		}
		return result, nil
	}, func(item any) {
		trait := item.(*gurps.Trait)
		if trait.Name == "" || trait.Container() {
			return
		}
		points := trait.AdjustedPoints()
		name := trait.Name
		if points > 0 {
			advantagesSet[name] = struct{}{}
			return
		}
		if points < 0 {
			if strings.Contains(strings.ToLower(name), "quirk") || strings.Contains(strings.ToLower(strings.Join(trait.Tags, " ")), "quirk") {
				quirksSet[name] = struct{}{}
				return
			}
			disadvantagesSet[name] = struct{}{}
		}
	})

	loadItems(gurps.EquipmentExt, func(fsys fs.FS, filePath string) ([]any, error) {
		rows, err := gurps.NewEquipmentFromFile(fsys, filePath)
		if err != nil {
			return nil, err
		}
		result := make([]any, len(rows))
		for i, r := range rows {
			result[i] = r
		}
		return result, nil
	}, func(item any) {
		eqp := item.(*gurps.Equipment)
		if eqp.Name != "" {
			equipmentSet[eqp.Name] = struct{}{}
		}
	})

	for name := range skillsSet {
		skills = append(skills, name)
	}
	for name := range advantagesSet {
		advantages = append(advantages, name)
	}
	for name := range disadvantagesSet {
		disadvantages = append(disadvantages, name)
	}
	for name := range quirksSet {
		quirks = append(quirks, name)
	}
	for name := range equipmentSet {
		equipment = append(equipment, name)
	}

	sort.Strings(skills)
	sort.Strings(advantages)
	sort.Strings(disadvantages)
	sort.Strings(quirks)
	sort.Strings(equipment)
	return
}

func (d *aiChatDockable) setThinking(thinking bool) {
	d.isThinking = thinking
	d.submitButton.SetEnabled(!thinking)
	if thinking {
		d.thinkingMessage = d.addMessage("AI", i18n.Text("Thinking..."))
	} else if d.thinkingMessage != nil {
		d.historyPanel.RemoveChild(d.thinkingMessage)
		d.thinkingMessage = nil
		d.historyPanel.MarkForLayoutAndRedraw()
	}
}

func (d *aiChatDockable) handleAIResponse(responseStr string) {
	d.addMessage("AI", responseStr)
	plan, ok := d.parseAIActionPlan(responseStr)
	if !ok {
		return
	}
	warnings, err := d.applyAIActionPlan(plan)
	if err != nil {
		d.addMessage("AI", fmt.Sprintf(i18n.Text("AI plan could not be applied: %v"), err))
		return
	}
	for _, warning := range warnings {
		d.addMessage("AI", warning)
	}
	d.addMessage("AI", i18n.Text("AI plan has been applied to the active character sheet."))
}

type aiActionPlan struct {
	Profile       *aiProfileAction    `json:"profile,omitempty"`
	Attributes    []aiAttributeAction `json:"attributes,omitempty"`
	Advantages    []aiNamedAction     `json:"advantages,omitempty"`
	Disadvantages []aiNamedAction     `json:"disadvantages,omitempty"`
	Quirks        []aiNamedAction     `json:"quirks,omitempty"`
	Skills        []aiSkillAction     `json:"skills,omitempty"`
	Equipment     []aiNamedAction     `json:"equipment,omitempty"`
	SpendAllCP    bool                `json:"spend_all_cp,omitempty"`
}

type aiProfileAction struct {
	Name         string `json:"name,omitempty"`
	Title        string `json:"title,omitempty"`
	Organization string `json:"organization,omitempty"`
	Religion     string `json:"religion,omitempty"`
	TechLevel    string `json:"tech_level,omitempty"`
	Gender       string `json:"gender,omitempty"`
	Age          string `json:"age,omitempty"`
	Birthday     string `json:"birthday,omitempty"`
	Eyes         string `json:"eyes,omitempty"`
	Hair         string `json:"hair,omitempty"`
	Skin         string `json:"skin,omitempty"`
	Handedness   string `json:"handedness,omitempty"`
	Height       string `json:"height,omitempty"`
	Weight       string `json:"weight,omitempty"`
}

type aiAttributeAction struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type aiNamedAction struct {
	Name     string `json:"name"`
	Points   string `json:"points,omitempty"`
	Quantity int    `json:"quantity,omitempty"`
}

type aiSkillAction struct {
	Name   string `json:"name"`
	Points string `json:"points,omitempty"`
	Level  string `json:"level,omitempty"`
}

func (d *aiChatDockable) parseAIActionPlan(text string) (aiActionPlan, bool) {
	payload := extractJSONPayload(text)
	if payload == "" {
		return aiActionPlan{}, false
	}
	var plan aiActionPlan
	if err := json.Unmarshal([]byte(payload), &plan); err != nil {
		return aiActionPlan{}, false
	}
	return plan, true
}

func extractJSONPayload(text string) string {
	start := strings.Index(text, "{")
	if start == -1 {
		return ""
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && inString {
			escape = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return ""
}

func (d *aiChatDockable) applyAIActionPlan(plan aiActionPlan) ([]string, error) {
	sheet := d.sheetOrCreateNew()
	if sheet == nil || sheet.entity == nil {
		return nil, fmt.Errorf("no active sheet to apply changes to")
	}
	entity := sheet.entity
	warnings := make([]string, 0)

	for _, attr := range plan.Attributes {
		if err := applyAttributeAction(entity, attr); err != nil {
			return nil, err
		}
	}

	if plan.Profile != nil {
		applyProfileAction(entity, plan.Profile)
	}

	if len(plan.Advantages) > 0 || len(plan.Disadvantages) > 0 || len(plan.Quirks) > 0 {
		traits := entity.Traits
		for _, action := range plan.Advantages {
			var warning string
			traits, warning, _ = d.addOrUpdateTrait(entity, traits, action)
			if warning != "" {
				warnings = append(warnings, warning)
			}
		}
		for _, action := range plan.Disadvantages {
			var warning string
			traits, warning, _ = d.addOrUpdateTrait(entity, traits, action)
			if warning != "" {
				warnings = append(warnings, warning)
			}
		}
		for _, action := range plan.Quirks {
			var warning string
			traits, warning, _ = d.addOrUpdateTrait(entity, traits, action)
			if warning != "" {
				warnings = append(warnings, warning)
			}
		}
		entity.SetTraitList(traits)
	}

	if len(plan.Skills) > 0 {
		skills := entity.Skills
		for _, action := range plan.Skills {
			var warning string
			skills, warning, _ = d.addOrUpdateSkill(entity, skills, action)
			if warning != "" {
				warnings = append(warnings, warning)
			}
		}
		entity.SetSkillList(skills)
	}

	if len(plan.Equipment) > 0 {
		equipment := entity.CarriedEquipment
		for _, action := range plan.Equipment {
			var warning string
			equipment, warning, _ = d.addOrUpdateEquipment(entity, equipment, action)
			if warning != "" {
				warnings = append(warnings, warning)
			}
		}
		entity.SetCarriedEquipmentList(equipment)
	}

	entity.Recalculate()
	sheet.Rebuild(true)
	updateRandomizedProfileFieldsWithoutUndo(sheet)
	ActivateDockable(sheet)

	if plan.SpendAllCP {
		// If the assistant requested to spend all CP, leave the sheet in a modified state.
	}
	MarkModified(sheet)
	return warnings, nil
}

func applyAttributeAction(entity *gurps.Entity, action aiAttributeAction) error {
	name := strings.TrimSpace(action.Name)
	id := strings.TrimSpace(action.ID)
	valueText := strings.TrimSpace(action.Value)
	if name == "" && id == "" {
		return fmt.Errorf("attribute action is missing id or name")
	}
	if valueText == "" {
		return fmt.Errorf("attribute action is missing a value")
	}
	value, err := fxp.FromString(valueText)
	if err != nil {
		return fmt.Errorf("invalid attribute value %q: %w", valueText, err)
	}
	var attr *gurps.Attribute
	for _, candidate := range entity.Attributes.List() {
		if id != "" && strings.EqualFold(candidate.AttrID, id) {
			attr = candidate
			break
		}
		if name != "" && candidate.NameMatches(name) {
			attr = candidate
			break
		}
	}
	if attr == nil {
		return fmt.Errorf("unknown attribute %q", name+id)
	}
	attr.SetMaximum(value)
	return nil
}

func applyProfileAction(entity *gurps.Entity, action *aiProfileAction) {
	if action == nil {
		return
	}

	// Update simple string fields
	if name := strings.TrimSpace(action.Name); name != "" {
		entity.Profile.Name = name
	}
	if title := strings.TrimSpace(action.Title); title != "" {
		entity.Profile.Title = title
	}
	if org := strings.TrimSpace(action.Organization); org != "" {
		entity.Profile.Organization = org
	}
	if religion := strings.TrimSpace(action.Religion); religion != "" {
		entity.Profile.Religion = religion
	}
	if tl := strings.TrimSpace(action.TechLevel); tl != "" {
		entity.Profile.TechLevel = tl
	}
	if gender := strings.TrimSpace(action.Gender); gender != "" {
		entity.Profile.Gender = gender
	}
	if age := strings.TrimSpace(action.Age); age != "" {
		entity.Profile.Age = age
	}
	if birthday := strings.TrimSpace(action.Birthday); birthday != "" {
		entity.Profile.Birthday = birthday
	}
	if eyes := strings.TrimSpace(action.Eyes); eyes != "" {
		entity.Profile.Eyes = eyes
	}
	if hair := strings.TrimSpace(action.Hair); hair != "" {
		entity.Profile.Hair = hair
	}
	if skin := strings.TrimSpace(action.Skin); skin != "" {
		entity.Profile.Skin = skin
	}
	if handedness := strings.TrimSpace(action.Handedness); handedness != "" {
		entity.Profile.Handedness = handedness
	}

	// Parse and update height
	if heightStr := strings.TrimSpace(action.Height); heightStr != "" {
		if height, err := fxp.LengthFromString(heightStr, fxp.Inch); err == nil {
			entity.Profile.Height = height
		}
	}

	// Parse and update weight
	if weightStr := strings.TrimSpace(action.Weight); weightStr != "" {
		if weight, err := fxp.WeightFromString(weightStr, fxp.Pound); err == nil {
			entity.Profile.Weight = weight
		}
	}
}

func (d *aiChatDockable) findLibraryTraitByName(name string) (*gurps.Trait, gurps.LibraryFile, error) {
	for _, set := range gurps.ScanForNamedFileSets(nil, "", false, gurps.GlobalSettings().Libraries(), gurps.TraitsExt) {
		for _, ref := range set.List {
			traits, err := gurps.NewTraitsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, trait := range traits {
				if trait.Container() {
					continue
				}
				if strings.EqualFold(trait.Name, name) {
					trait.SetDataOwner(nil)
					return trait, gurps.LibraryFile{Library: set.Name, Path: ref.FilePath}, nil
				}
			}
		}
	}
	return nil, gurps.LibraryFile{}, nil
}

func (d *aiChatDockable) findLibrarySkillByName(name string) (*gurps.Skill, gurps.LibraryFile, error) {
	for _, set := range gurps.ScanForNamedFileSets(nil, "", false, gurps.GlobalSettings().Libraries(), gurps.SkillsExt) {
		for _, ref := range set.List {
			skills, err := gurps.NewSkillsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, skill := range skills {
				if skill.Container() {
					continue
				}
				if strings.EqualFold(skill.Name, name) {
					skill.SetDataOwner(nil)
					return skill, gurps.LibraryFile{Library: set.Name, Path: ref.FilePath}, nil
				}
			}
		}
	}
	return nil, gurps.LibraryFile{}, nil
}

func (d *aiChatDockable) findLibraryEquipmentByName(name string) (*gurps.Equipment, gurps.LibraryFile, error) {
	for _, set := range gurps.ScanForNamedFileSets(nil, "", false, gurps.GlobalSettings().Libraries(), gurps.EquipmentExt) {
		for _, ref := range set.List {
			equipment, err := gurps.NewEquipmentFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, eqp := range equipment {
				if eqp.Container() {
					continue
				}
				if strings.EqualFold(eqp.Name, name) {
					eqp.SetDataOwner(nil)
					return eqp, gurps.LibraryFile{Library: set.Name, Path: ref.FilePath}, nil
				}
			}
		}
	}
	return nil, gurps.LibraryFile{}, nil
}

func (d *aiChatDockable) addOrUpdateTrait(entity *gurps.Entity, traits []*gurps.Trait, action aiNamedAction) ([]*gurps.Trait, string, error) {
	if strings.TrimSpace(action.Name) == "" {
		return traits, "", fmt.Errorf("trait action is missing a name")
	}
	if existing := d.findExistingTrait(entity, action.Name); existing != nil {
		if action.Points != "" {
			if points, err := fxp.FromString(strings.TrimSpace(action.Points)); err == nil {
				existing.BasePoints = points
			}
		}
		return traits, "", nil
	}
	libraryTrait, libFile, err := d.findLibraryTraitByName(action.Name)
	if err != nil {
		return traits, "", err
	}
	if libraryTrait == nil {
		return traits, fmt.Sprintf("Warning: trait %q was not found in the library and was skipped. Advantages, disadvantages and quirks must come from library entries.", action.Name), nil
	}
	cloned := libraryTrait.Clone(libFile, entity, nil, false)
	if action.Points != "" {
		if points, err := fxp.FromString(strings.TrimSpace(action.Points)); err == nil {
			cloned.BasePoints = points
		}
	}
	return append(traits, cloned), "", nil
}

func (d *aiChatDockable) addOrUpdateSkill(entity *gurps.Entity, skills []*gurps.Skill, action aiSkillAction) ([]*gurps.Skill, string, error) {
	if strings.TrimSpace(action.Name) == "" {
		return skills, "", fmt.Errorf("skill action is missing a name")
	}
	if existing := d.findExistingSkill(entity, action.Name); existing != nil {
		if action.Points != "" {
			if points, err := fxp.FromString(strings.TrimSpace(action.Points)); err == nil {
				existing.Points = points
			}
		}
		if action.Level != "" {
			if level, err := fxp.FromString(strings.TrimSpace(action.Level)); err == nil {
				existing.LevelData.Level = level
			}
		}
		return skills, "", nil
	}
	librarySkill, libFile, err := d.findLibrarySkillByName(action.Name)
	if err != nil {
		return skills, "", err
	}
	if librarySkill == nil {
		return skills, fmt.Sprintf("Warning: skill %q was not found in the library and was skipped. Skills must be chosen from available database entries.", action.Name), nil
	}
	cloned := librarySkill.Clone(libFile, entity, nil, false)
	if action.Points != "" {
		if points, err := fxp.FromString(strings.TrimSpace(action.Points)); err == nil {
			cloned.Points = points
		}
	}
	if action.Level != "" {
		if level, err := fxp.FromString(strings.TrimSpace(action.Level)); err == nil {
			cloned.LevelData.Level = level
		}
	}
	return append(skills, cloned), "", nil
}

func (d *aiChatDockable) addOrUpdateEquipment(entity *gurps.Entity, equipment []*gurps.Equipment, action aiNamedAction) ([]*gurps.Equipment, string, error) {
	if strings.TrimSpace(action.Name) == "" {
		return equipment, "", fmt.Errorf("equipment action is missing a name")
	}
	if existing := d.findExistingEquipment(entity, action.Name); existing != nil {
		if action.Quantity != 0 {
			existing.Quantity = fxp.FromInteger(action.Quantity)
		}
		return equipment, "", nil
	}
	libraryEquipment, libFile, err := d.findLibraryEquipmentByName(action.Name)
	if err != nil {
		return equipment, "", err
	}
	if libraryEquipment == nil {
		custom := gurps.NewEquipment(entity, nil, false)
		custom.Name = action.Name
		custom.Quantity = fxp.One
		if action.Quantity > 0 {
			custom.Quantity = fxp.FromInteger(action.Quantity)
		}
		return append(equipment, custom), fmt.Sprintf("Notice: custom equipment %q was added because no library match was found. Library equipment is preferred.", action.Name), nil
	}
	cloned := libraryEquipment.Clone(libFile, entity, nil, false)
	if action.Quantity != 0 {
		cloned.Quantity = fxp.FromInteger(action.Quantity)
	}
	return append(equipment, cloned), "", nil
}

func (d *aiChatDockable) findExistingTrait(entity *gurps.Entity, name string) *gurps.Trait {
	name = strings.TrimSpace(name)
	for _, trait := range entity.Traits {
		if trait.Container() {
			continue
		}
		if strings.EqualFold(trait.Name, name) {
			return trait
		}
	}
	return nil
}

func (d *aiChatDockable) findExistingSkill(entity *gurps.Entity, name string) *gurps.Skill {
	name = strings.TrimSpace(name)
	for _, skill := range entity.Skills {
		if skill.Container() {
			continue
		}
		if strings.EqualFold(skill.Name, name) {
			return skill
		}
	}
	return nil
}

func (d *aiChatDockable) findExistingEquipment(entity *gurps.Entity, name string) *gurps.Equipment {
	name = strings.TrimSpace(name)
	for _, eqp := range entity.CarriedEquipment {
		if eqp.Container() {
			continue
		}
		if strings.EqualFold(eqp.Name, name) {
			return eqp
		}
	}
	return nil
}

func (d *aiChatDockable) queryGemini(prompt string) {
	settings := gurps.GlobalSettings().AI
	if settings.GeminiAPIKey == "" {
		d.addMessage("AI", i18n.Text("Gemini API Key is not set. Please configure it in the AI Settings."))
		return
	}
	d.setThinking(true)
	go func() {
		defer unison.InvokeTask(func() { d.setThinking(false) })
		ctx := context.Background()
		client, err := genai.NewClient(ctx, option.WithAPIKey(settings.GeminiAPIKey))
		if err != nil {
			unison.InvokeTask(func() { d.addMessage("AI", fmt.Sprintf(i18n.Text("Error creating Gemini client: %v"), err)) })
			return
		}
		defer client.Close()
		model := client.GenerativeModel("gemini-pro")
		chat := model.StartChat()
		systemPrompt := d.aiAssistantSystemPrompt()
		chat.History = append([]*genai.Content{{Role: "system", Parts: []genai.Part{genai.Text(systemPrompt)}}}, d.chatHistory...)
		resp, err := chat.SendMessage(ctx, genai.Text(prompt))
		if err != nil {
			unison.InvokeTask(func() { d.addMessage("AI", fmt.Sprintf(i18n.Text("Error generating content: %v"), err)) })
			return
		}
		var responseText strings.Builder
		for _, cand := range resp.Candidates {
			if cand.Content != nil {
				for _, part := range cand.Content.Parts {
					if txt, ok := part.(genai.Text); ok {
						responseText.WriteString(string(txt))
					}
				}
			}
		}
		responseStr := responseText.String()
		unison.InvokeTask(func() { d.handleAIResponse(responseStr) })
		d.chatHistory = append(d.chatHistory, &genai.Content{Parts: []genai.Part{genai.Text(prompt)}, Role: "user"})
		d.chatHistory = append(d.chatHistory, &genai.Content{Parts: []genai.Part{genai.Text(responseStr)}, Role: "model"})
	}()
}

func (d *aiChatDockable) queryLocal(prompt string) {
	settings := gurps.GlobalSettings().AI
	endpoint := strings.TrimSpace(settings.LocalServerURL)
	if endpoint == "" {
		d.addMessage("AI", i18n.Text("Local server URL is not set. Please configure it in the AI Settings."))
		return
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	endpoint = strings.TrimSuffix(endpoint, "/")

	model := strings.TrimSpace(settings.LocalModel)
	if model == "" {
		model = "llama3"
	}

	d.setThinking(true)
	go func() {
		defer unison.InvokeTask(func() { d.setThinking(false) })

		type message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}

		normalizeRole := func(role string) string {
			switch strings.ToLower(strings.TrimSpace(role)) {
			case "model":
				return "assistant"
			case "assistant", "user", "system", "tool":
				return strings.ToLower(strings.TrimSpace(role))
			default:
				return "assistant"
			}
		}

		marshalContent := func(content *genai.Content) string {
			var b strings.Builder
			for _, part := range content.Parts {
				if txt, ok := part.(genai.Text); ok {
					b.WriteString(string(txt))
				}
			}
			return strings.TrimSpace(b.String())
		}

		var messages []message
		messages = append(messages, message{Role: "system", Content: d.aiAssistantSystemPrompt()})
		for _, entry := range d.chatHistory {
			contentText := marshalContent(entry)
			if contentText == "" {
				continue
			}
			messages = append(messages, message{Role: normalizeRole(entry.Role), Content: contentText})
		}
		messages = append(messages, message{Role: "user", Content: prompt})

		reqBody := struct {
			Model    string    `json:"model"`
			Messages []message `json:"messages"`
			Stream   bool      `json:"stream"`
		}{
			Model:    model,
			Messages: messages,
			Stream:   false,
		}
		b, err := json.Marshal(reqBody)
		if err != nil {
			unison.InvokeTask(func() { d.addMessage("AI", fmt.Sprintf(i18n.Text("Error preparing local request: %v"), err)) })
			return
		}

		tryRequest := func(path string) (*http.Response, error) {
			return http.Post(endpoint+path, "application/json", bytes.NewReader(b))
		}

		paths := []string{"/api/chat", "/api/generate"}
		var resp *http.Response
		for _, path := range paths {
			debugPath := endpoint + path
			unison.InvokeTask(func() {
				d.addMessage("Debug", fmt.Sprintf("POST %s (model=%s)", debugPath, model))
			})
			resp, err = tryRequest(path)
			if err != nil {
				unison.InvokeTask(func() { d.addMessage("AI", fmt.Sprintf(i18n.Text("Error querying local AI server: %v"), err)) })
				return
			}
			if resp.StatusCode == http.StatusOK {
				break
			}
			if resp.StatusCode != http.StatusNotFound {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				unison.InvokeTask(func() {
					d.addMessage("AI", fmt.Sprintf(i18n.Text("Local AI server returned %d: %s"), resp.StatusCode, strings.TrimSpace(string(body))))
				})
				return
			}
			resp.Body.Close()
		}

		if resp == nil {
			unison.InvokeTask(func() { d.addMessage("AI", i18n.Text("Local AI server did not respond.")) })
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			unison.InvokeTask(func() {
				d.addMessage("AI", fmt.Sprintf(i18n.Text("Local AI server returned %d: %s"), resp.StatusCode, strings.TrimSpace(string(body))))
			})
			return
		}

		var result struct {
			Error   string `json:"error,omitempty"`
			Message *struct {
				Role    string `json:"role,omitempty"`
				Content string `json:"content,omitempty"`
			} `json:"message,omitempty"`
			Response string `json:"response,omitempty"`
			Choices  []struct {
				Text    string `json:"text,omitempty"`
				Message struct {
					Content string `json:"content,omitempty"`
				} `json:"message,omitempty"`
			} `json:"choices,omitempty"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			unison.InvokeTask(func() { d.addMessage("AI", fmt.Sprintf(i18n.Text("Error parsing local AI response: %v"), err)) })
			return
		}
		if result.Error != "" {
			unison.InvokeTask(func() { d.addMessage("AI", fmt.Sprintf(i18n.Text("Local AI server error: %s"), result.Error)) })
			return
		}

		responseStr := strings.TrimSpace(result.Response)
		if responseStr == "" && result.Message != nil {
			responseStr = strings.TrimSpace(result.Message.Content)
		}
		if responseStr == "" && len(result.Choices) > 0 {
			responseStr = strings.TrimSpace(result.Choices[0].Text)
			if responseStr == "" {
				responseStr = strings.TrimSpace(result.Choices[0].Message.Content)
			}
		}
		if responseStr == "" {
			unison.InvokeTask(func() { d.addMessage("AI", i18n.Text("Local AI server returned no text.")) })
			return
		}
		unison.InvokeTask(func() { d.handleAIResponse(responseStr) })
		d.chatHistory = append(d.chatHistory, &genai.Content{Parts: []genai.Part{genai.Text(prompt)}, Role: "user"})
		d.chatHistory = append(d.chatHistory, &genai.Content{Parts: []genai.Part{genai.Text(responseStr)}, Role: "assistant"})
	}()
}
