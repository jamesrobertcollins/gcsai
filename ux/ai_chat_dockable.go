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
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/generative-ai-go/genai"
	"github.com/richardwilkes/gcs/v5/model/fxp"
	"github.com/richardwilkes/gcs/v5/model/gurps"
	"github.com/richardwilkes/gcs/v5/model/gurps/enums/dgroup"
	"github.com/richardwilkes/gcs/v5/svg"
	"github.com/richardwilkes/toolbox/v2/geom"
	"github.com/richardwilkes/toolbox/v2/i18n"
	"github.com/richardwilkes/toolbox/v2/tid"
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
	availableItems := d.getCompleteAvailableLibraryItems()
	return strings.TrimSpace(fmt.Sprintf(`You are a GURPS Fourth Edition character builder assistant.
Use the current character sheet state and the player's concept to make recommendations.
Always validate choices, compute point costs, and keep Tech Level constraints in mind.
Evaluate available library options against the character concept, game-world setting, and Tech Level.
IMPORTANT: You MUST choose advantages, disadvantages, quirks, and skills ONLY from the complete lists provided below.
Do NOT suggest any items not in these lists. Do NOT invent custom ability names.
When returning actions for advantages, disadvantages, quirks, skills, and equipment: ALWAYS include the exact id from the list entry.
Use only the id value shown after "id=" in the list. Never use a library/source label (for example, never use "GURPS" as an id).
Prefer existing library equipment first; only create a custom item if no suitable library equipment exists.
If the user asks for changes, propose specific character-sheet updates and a clear CP breakdown.
If no sheet is active, ask the user to open or create a character sheet before proceeding.
Some traits and skills require a subject or specialization to be filled in. These are shown in the library list with a "requires:" tag listing placeholder names (e.g. "requires: subject"). When adding such a trait or skill, you MUST supply a "notes" field containing the appropriate value. For example, Code of Honor (@subject@) with subject "Pirate's" would be: {"id":"<id>","name":"Code of Honor","notes":"Pirate's","points":"-10"}.

%s

AVAILABLE LIBRARY ITEMS - Choose ONLY from these lists:
%s

When asked to apply changes, include a top-level JSON object showing character updates using items from the lists above. Keys:
- profile: {"name":"John Smith","gender":"M","age":"25","height":"5'10\"","weight":"180 lbs","hair":"brown","eyes":"blue","skin":"fair","handedness":"Right","title":"Adventurer","organization":"","religion":"","tech_level":"3"}
- attributes: [{"id":"ST","value":"12"}]
- advantages: [{"id":"<adv-id>","name":"Combat Reflexes","points":"15"}]
- disadvantages: [{"id":"<disadv-id>","name":"Code of Honor","notes":"Pirate's","points":"-10"}]
- quirks: [{"id":"<quirk-id>","name":"Must make an entrance","points":"-1"}]
- skills: [{"id":"<skill-id>","name":"Brawling","points":"4"}]
- equipment: [{"id":"<equipment-id>","name":"Leather Armor","quantity":1}]
- spend_all_cp: true

Only include profile fields if you have determined suitable values for them based on the character concept. For height and weight, use common formats like "5'10\"" or "175 lbs". Include only the profile fields that should be updated; omit others.
If you include JSON, return exactly one top-level JSON object for the entire update.
Do not split updates across multiple JSON objects.
Do not include comments inside the JSON.
Put that JSON object first in the response.
When responding, keep answers concise, factual, and directly tied to GURPS 4e rules.`, summary, availableItems))
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

func (d *aiChatDockable) getCompleteAvailableLibraryItems() string {
	libraries := gurps.GlobalSettings().Libraries()
	if libraries == nil {
		_ = os.WriteFile("ai-debug-skills.txt", []byte("AI skill library dump\nNo libraries loaded (nil).\n"), 0o644)
		return "No libraries loaded."
	}
	d.dumpEntireSkillListToFile(libraries)
	skills, advantages, disadvantages, quirks, equipment := d.collectAvailableLibraryItemNames(libraries)
	if len(skills) == 0 && len(advantages) == 0 && len(disadvantages) == 0 && len(quirks) == 0 && len(equipment) == 0 {
		return "No library items are available."
	}
	var builder strings.Builder
	if len(skills) > 0 {
		builder.WriteString("SKILLS:\n")
		for _, skill := range skills {
			builder.WriteString(fmt.Sprintf("  - id=%s | name=%s | %s\n", skill.ID, skillDisplayName(skill.Name, skill.Specialization), skill.SourcePath))
		}
		builder.WriteString("\n")
	}
	if len(advantages) > 0 {
		builder.WriteString("ADVANTAGES:\n")
		for _, adv := range advantages {
			line := fmt.Sprintf("  - id=%s | name=%s | %s", adv.ID, adv.Name, adv.SourcePath)
			if len(adv.Nameables) > 0 {
				line += fmt.Sprintf(" | requires: %s", strings.Join(adv.Nameables, ", "))
			}
			builder.WriteString(line + "\n")
		}
		builder.WriteString("\n")
	}
	if len(disadvantages) > 0 {
		builder.WriteString("DISADVANTAGES:\n")
		for _, dis := range disadvantages {
			line := fmt.Sprintf("  - id=%s | name=%s | %s", dis.ID, dis.Name, dis.SourcePath)
			if len(dis.Nameables) > 0 {
				line += fmt.Sprintf(" | requires: %s", strings.Join(dis.Nameables, ", "))
			}
			builder.WriteString(line + "\n")
		}
		builder.WriteString("\n")
	}
	if len(quirks) > 0 {
		builder.WriteString("QUIRKS:\n")
		for _, quirk := range quirks {
			line := fmt.Sprintf("  - id=%s | name=%s | %s", quirk.ID, quirk.Name, quirk.SourcePath)
			if len(quirk.Nameables) > 0 {
				line += fmt.Sprintf(" | requires: %s", strings.Join(quirk.Nameables, ", "))
			}
			builder.WriteString(line + "\n")
		}
		builder.WriteString("\n")
	}
	if len(equipment) > 0 {
		builder.WriteString("EQUIPMENT:\n")
		for _, eq := range equipment {
			builder.WriteString(fmt.Sprintf("  - id=%s | name=%s | %s\n", eq.ID, eq.Name, eq.SourcePath))
		}
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

// writeSystemPromptDebugFile writes the system prompt sent to the AI to ai-debug-system-prompt.txt.
func writeSystemPromptDebugFile(prompt string) {
	_ = os.WriteFile("ai-debug-system-prompt.txt", []byte(prompt), 0o644)
}

// WriteAIDebugSkillDumpFile writes the current runtime-discovered skill list to ai-debug-skills.txt.
// This is safe to call at startup and during AI interactions.
func WriteAIDebugSkillDumpFile() {
	libraries := gurps.GlobalSettings().Libraries()
	if libraries == nil {
		_ = os.WriteFile("ai-debug-skills.txt", []byte("AI skill library dump\nNo libraries loaded (nil).\n"), 0o644)
		return
	}
	var d aiChatDockable
	d.dumpEntireSkillListToFile(libraries)
}

func scanNamedFileSetsWithFallback(libraries gurps.Libraries, ext string) []*gurps.NamedFileSet {
	sets := gurps.ScanForNamedFileSets(nil, "", false, libraries, ext)
	if len(sets) != 0 {
		return sets
	}

	fallback := make([]*gurps.NamedFileSet, 0)
	for _, lib := range libraries.List() {
		base := lib.Path()
		refs := make([]*gurps.NamedFileRef, 0)
		_ = fs.WalkDir(os.DirFS(base), ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if strings.EqualFold(path.Ext(p), ext) {
				refs = append(refs, &gurps.NamedFileRef{Name: strings.TrimSuffix(path.Base(p), path.Ext(p)), FileSystem: os.DirFS(base), FilePath: p})
			}
			return nil
		})
		if len(refs) == 0 {
			continue
		}
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].Name == refs[j].Name {
				return refs[i].FilePath < refs[j].FilePath
			}
			return refs[i].Name < refs[j].Name
		})
		fallback = append(fallback, &gurps.NamedFileSet{Name: lib.Title, List: refs})
	}
	return fallback
}

func libraryKeyForSetName(libraries gurps.Libraries, setName string) string {
	for key, lib := range libraries {
		if lib != nil && lib.Title == setName {
			return key
		}
	}
	return setName
}

func libraryFileForSet(setName, filePath string) gurps.LibraryFile {
	libs := gurps.GlobalSettings().Libraries()
	return gurps.LibraryFile{Library: libraryKeyForSetName(libs, setName), Path: filePath}
}

func (d *aiChatDockable) dumpEntireSkillListToFile(libraries gurps.Libraries) {
	var builder strings.Builder
	builder.WriteString("AI skill library dump\n")
	builder.WriteString("Format: id | name | specialization | display_name | normalized | library | path\n\n")
	builder.WriteString("Library roots:\n")
	for _, lib := range libraries.List() {
		if info, err := os.Stat(lib.Path()); err == nil {
			builder.WriteString(fmt.Sprintf("- %s | %s | exists=true | is_dir=%t\n", lib.Title, lib.Path(), info.IsDir()))
		} else {
			builder.WriteString(fmt.Sprintf("- %s | %s | exists=false | err=%v\n", lib.Title, lib.Path(), err))
		}
	}
	builder.WriteString("\n")

	total := 0
	sets := scanNamedFileSetsWithFallback(libraries, gurps.SkillsExt)
	builder.WriteString(fmt.Sprintf("Named file sets found: %d\n", len(sets)))
	for _, set := range sets {
		builder.WriteString(fmt.Sprintf("Set %q file refs: %d\n", set.Name, len(set.List)))
	}
	builder.WriteString("\n")
	for _, set := range sets {
		for _, ref := range set.List {
			skills, err := gurps.NewSkillsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				builder.WriteString(fmt.Sprintf("ERROR loading %s/%s: %v\n", set.Name, ref.FilePath, err))
				continue
			}
			for _, skill := range skills {
				if skill.Container() || strings.TrimSpace(skill.Name) == "" {
					continue
				}
				total++
				displayName := skillDisplayName(skill.Name, skill.Specialization)
				builder.WriteString(fmt.Sprintf("%s | %s | %s | %s | %s | %s | %s\n",
					string(skill.TID),
					skill.Name,
					skill.Specialization,
					displayName,
					normalizeLookupText(displayName),
					set.Name,
					ref.FilePath,
				))
			}
		}
	}
	builder.WriteString(fmt.Sprintf("\nTotal non-container skills scanned: %d\n", total))
	_ = os.WriteFile("ai-debug-skills.txt", []byte(builder.String()), 0o644)
}

func (d *aiChatDockable) collectAvailableLibraryItemNames(libraries gurps.Libraries) (skills, advantages, disadvantages, quirks, equipment []aiLibraryItemRef) {
	skillsMap := make(map[string]aiLibraryItemRef)
	advantagesMap := make(map[string]aiLibraryItemRef)
	disadvantagesMap := make(map[string]aiLibraryItemRef)
	quirksMap := make(map[string]aiLibraryItemRef)
	equipmentMap := make(map[string]aiLibraryItemRef)

	loadItems := func(ext string, loader func(fs.FS, string) ([]any, error), categorize func(any, string)) {
		for _, set := range scanNamedFileSetsWithFallback(libraries, ext) {
			for _, ref := range set.List {
				items, err := loader(ref.FileSystem, ref.FilePath)
				if err != nil {
					continue
				}
				for _, item := range items {
					categorize(item, ref.FilePath)
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
	}, func(item any, sourcePath string) {
		skill := item.(*gurps.Skill)
		if skill.Name != "" && !skill.Container() {
			skillsMap[string(skill.TID)] = aiLibraryItemRef{ID: string(skill.TID), Name: skill.Name, Specialization: skill.Specialization, SourcePath: sourcePath}
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
	}, func(item any, sourcePath string) {
		trait := item.(*gurps.Trait)
		if trait.Name == "" || trait.Container() {
			return
		}
		points := trait.AdjustedPoints()
		name := trait.Name
		// Discover nameable keys (@key@ placeholders) in this trait.
		nameableKeys := make(map[string]string)
		trait.FillWithNameableKeys(nameableKeys, nil)
		sortedKeys := make([]string, 0, len(nameableKeys))
		for k := range nameableKeys {
			sortedKeys = append(sortedKeys, k)
		}
		sort.Strings(sortedKeys)
		ref := aiLibraryItemRef{ID: string(trait.TID), Name: name, SourcePath: sourcePath, Nameables: sortedKeys}
		key := string(trait.TID)
		if points > 0 {
			advantagesMap[key] = ref
			return
		}
		if points < 0 {
			if strings.Contains(strings.ToLower(name), "quirk") || strings.Contains(strings.ToLower(strings.Join(trait.Tags, " ")), "quirk") {
				quirksMap[key] = ref
				return
			}
			disadvantagesMap[key] = ref
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
	}, func(item any, sourcePath string) {
		eqp := item.(*gurps.Equipment)
		if eqp.Name != "" && !eqp.Container() {
			equipmentMap[string(eqp.TID)] = aiLibraryItemRef{ID: string(eqp.TID), Name: eqp.Name, SourcePath: sourcePath}
		}
	})

	for _, item := range skillsMap {
		skills = append(skills, item)
	}
	for _, item := range advantagesMap {
		advantages = append(advantages, item)
	}
	for _, item := range disadvantagesMap {
		disadvantages = append(disadvantages, item)
	}
	for _, item := range quirksMap {
		quirks = append(quirks, item)
	}
	for _, item := range equipmentMap {
		equipment = append(equipment, item)
	}

	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Name != skills[j].Name {
			return skills[i].Name < skills[j].Name
		}
		return skills[i].Specialization < skills[j].Specialization
	})
	sort.Slice(advantages, func(i, j int) bool { return advantages[i].Name < advantages[j].Name })
	sort.Slice(disadvantages, func(i, j int) bool { return disadvantages[i].Name < disadvantages[j].Name })
	sort.Slice(quirks, func(i, j int) bool { return quirks[i].Name < quirks[j].Name })
	sort.Slice(equipment, func(i, j int) bool { return equipment[i].Name < equipment[j].Name })
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
		if strings.Contains(responseStr, "{") {
			d.addMessage("AI", i18n.Text("Structured update data was detected, but it could not be parsed into a character-sheet update."))
		}
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

type aiFlexibleString string

func (s *aiFlexibleString) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*s = ""
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = aiFlexibleString(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err == nil {
		*s = aiFlexibleString(number.String())
		return nil
	}
	var value float64
	if err := json.Unmarshal(data, &value); err == nil {
		*s = aiFlexibleString(strconv.FormatFloat(value, 'f', -1, 64))
		return nil
	}
	return fmt.Errorf("unsupported JSON value for text field: %s", trimmed)
}

func (s aiFlexibleString) String() string {
	return string(s)
}

type aiLibraryItemRef struct {
	ID             string
	Name           string
	Specialization string
	SourcePath     string
	Nameables      []string // @key@ placeholders the AI must fill via "notes"
}

type aiFlexibleInt int

func (i *aiFlexibleInt) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*i = 0
		return nil
	}
	var value int
	if err := json.Unmarshal(data, &value); err == nil {
		*i = aiFlexibleInt(value)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			*i = 0
			return nil
		}
		parsed, err := strconv.Atoi(text)
		if err != nil {
			return err
		}
		*i = aiFlexibleInt(parsed)
		return nil
	}
	return fmt.Errorf("unsupported JSON value for integer field: %s", trimmed)
}

func (i aiFlexibleInt) Int() int {
	return int(i)
}

type aiProfileAction struct {
	Name         aiFlexibleString `json:"name,omitempty"`
	Title        aiFlexibleString `json:"title,omitempty"`
	Organization aiFlexibleString `json:"organization,omitempty"`
	Religion     aiFlexibleString `json:"religion,omitempty"`
	TechLevel    aiFlexibleString `json:"tech_level,omitempty"`
	Gender       aiFlexibleString `json:"gender,omitempty"`
	Age          aiFlexibleString `json:"age,omitempty"`
	Birthday     aiFlexibleString `json:"birthday,omitempty"`
	Eyes         aiFlexibleString `json:"eyes,omitempty"`
	Hair         aiFlexibleString `json:"hair,omitempty"`
	Skin         aiFlexibleString `json:"skin,omitempty"`
	Handedness   aiFlexibleString `json:"handedness,omitempty"`
	Height       aiFlexibleString `json:"height,omitempty"`
	Weight       aiFlexibleString `json:"weight,omitempty"`
}

type aiAttributeAction struct {
	ID    aiFlexibleString `json:"id,omitempty"`
	Name  aiFlexibleString `json:"name,omitempty"`
	Value aiFlexibleString `json:"value,omitempty"`
}

type aiNamedAction struct {
	ID       aiFlexibleString `json:"id,omitempty"`
	Name     aiFlexibleString `json:"name"`
	Notes    aiFlexibleString `json:"notes,omitempty"`
	Points   aiFlexibleString `json:"points,omitempty"`
	Quantity aiFlexibleInt    `json:"quantity,omitempty"`
}

type aiSkillAction struct {
	ID     aiFlexibleString `json:"id,omitempty"`
	Name   aiFlexibleString `json:"name"`
	Notes  aiFlexibleString `json:"notes,omitempty"`
	Points aiFlexibleString `json:"points,omitempty"`
	Value  aiFlexibleString `json:"value,omitempty"`
	Level  aiFlexibleString `json:"level,omitempty"`
}

func (d *aiChatDockable) parseAIActionPlan(text string) (aiActionPlan, bool) {
	payloads := extractJSONPayloads(text)
	if len(payloads) == 0 {
		return aiActionPlan{}, false
	}
	var merged aiActionPlan
	found := false
	for _, payload := range payloads {
		cleaned := sanitizeAIJSONPayload(payload)
		if cleaned == "" {
			continue
		}
		var plan aiActionPlan
		if err := json.Unmarshal([]byte(cleaned), &plan); err != nil {
			continue
		}
		if !hasAIActionPlanContent(plan) {
			continue
		}
		mergeAIActionPlan(&merged, plan)
		found = true
	}
	return merged, found
}

func extractJSONPayloads(text string) []string {
	payloads := make([]string, 0)
	start := -1
	depth := 0
	inString := false
	escape := false
	for i := 0; i < len(text); i++ {
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
			if depth == 0 {
				start = i
			}
			depth++
			continue
		}
		if ch == '}' && depth > 0 {
			depth--
			if depth == 0 && start >= 0 {
				payloads = append(payloads, text[start:i+1])
				start = -1
			}
		}
	}
	return payloads
}

func sanitizeAIJSONPayload(payload string) string {
	withoutComments := stripJSONLineComments(payload)
	return stripTrailingJSONCommas(withoutComments)
}

func stripJSONLineComments(payload string) string {
	var builder strings.Builder
	builder.Grow(len(payload))
	inString := false
	escape := false
	for i := 0; i < len(payload); i++ {
		ch := payload[i]
		if escape {
			builder.WriteByte(ch)
			escape = false
			continue
		}
		if ch == '\\' && inString {
			builder.WriteByte(ch)
			escape = true
			continue
		}
		if ch == '"' {
			builder.WriteByte(ch)
			inString = !inString
			continue
		}
		if !inString && ch == '/' && i+1 < len(payload) && payload[i+1] == '/' {
			for i+1 < len(payload) && payload[i+1] != '\n' {
				i++
			}
			continue
		}
		builder.WriteByte(ch)
	}
	return builder.String()
}

func stripTrailingJSONCommas(payload string) string {
	var builder strings.Builder
	builder.Grow(len(payload))
	inString := false
	escape := false
	for i := 0; i < len(payload); i++ {
		ch := payload[i]
		if escape {
			builder.WriteByte(ch)
			escape = false
			continue
		}
		if ch == '\\' && inString {
			builder.WriteByte(ch)
			escape = true
			continue
		}
		if ch == '"' {
			builder.WriteByte(ch)
			inString = !inString
			continue
		}
		if !inString && ch == ',' {
			next := i + 1
			for next < len(payload) && isJSONWhitespace(payload[next]) {
				next++
			}
			if next < len(payload) && (payload[next] == '}' || payload[next] == ']') {
				continue
			}
		}
		builder.WriteByte(ch)
	}
	return builder.String()
}

func isJSONWhitespace(ch byte) bool {
	return ch == ' ' || ch == '\n' || ch == '\r' || ch == '\t'
}

func hasAIActionPlanContent(plan aiActionPlan) bool {
	if plan.Profile != nil {
		if strings.TrimSpace(plan.Profile.Name.String()) != "" || strings.TrimSpace(plan.Profile.Title.String()) != "" ||
			strings.TrimSpace(plan.Profile.Organization.String()) != "" || strings.TrimSpace(plan.Profile.Religion.String()) != "" ||
			strings.TrimSpace(plan.Profile.TechLevel.String()) != "" || strings.TrimSpace(plan.Profile.Gender.String()) != "" ||
			strings.TrimSpace(plan.Profile.Age.String()) != "" || strings.TrimSpace(plan.Profile.Birthday.String()) != "" ||
			strings.TrimSpace(plan.Profile.Eyes.String()) != "" || strings.TrimSpace(plan.Profile.Hair.String()) != "" ||
			strings.TrimSpace(plan.Profile.Skin.String()) != "" || strings.TrimSpace(plan.Profile.Handedness.String()) != "" ||
			strings.TrimSpace(plan.Profile.Height.String()) != "" || strings.TrimSpace(plan.Profile.Weight.String()) != "" {
			return true
		}
	}
	return len(plan.Attributes) != 0 || len(plan.Advantages) != 0 || len(plan.Disadvantages) != 0 ||
		len(plan.Quirks) != 0 || len(plan.Skills) != 0 || len(plan.Equipment) != 0 || plan.SpendAllCP
}

func mergeAIActionPlan(dst *aiActionPlan, src aiActionPlan) {
	if src.Profile != nil {
		if dst.Profile == nil {
			dst.Profile = &aiProfileAction{}
		}
		mergeAIProfileAction(dst.Profile, src.Profile)
	}
	dst.Attributes = append(dst.Attributes, src.Attributes...)
	dst.Advantages = append(dst.Advantages, src.Advantages...)
	dst.Disadvantages = append(dst.Disadvantages, src.Disadvantages...)
	dst.Quirks = append(dst.Quirks, src.Quirks...)
	dst.Skills = append(dst.Skills, src.Skills...)
	dst.Equipment = append(dst.Equipment, src.Equipment...)
	dst.SpendAllCP = dst.SpendAllCP || src.SpendAllCP
}

func mergeAIProfileAction(dst, src *aiProfileAction) {
	if dst == nil || src == nil {
		return
	}
	if strings.TrimSpace(src.Name.String()) != "" {
		dst.Name = src.Name
	}
	if strings.TrimSpace(src.Title.String()) != "" {
		dst.Title = src.Title
	}
	if strings.TrimSpace(src.Organization.String()) != "" {
		dst.Organization = src.Organization
	}
	if strings.TrimSpace(src.Religion.String()) != "" {
		dst.Religion = src.Religion
	}
	if strings.TrimSpace(src.TechLevel.String()) != "" {
		dst.TechLevel = src.TechLevel
	}
	if strings.TrimSpace(src.Gender.String()) != "" {
		dst.Gender = src.Gender
	}
	if strings.TrimSpace(src.Age.String()) != "" {
		dst.Age = src.Age
	}
	if strings.TrimSpace(src.Birthday.String()) != "" {
		dst.Birthday = src.Birthday
	}
	if strings.TrimSpace(src.Eyes.String()) != "" {
		dst.Eyes = src.Eyes
	}
	if strings.TrimSpace(src.Hair.String()) != "" {
		dst.Hair = src.Hair
	}
	if strings.TrimSpace(src.Skin.String()) != "" {
		dst.Skin = src.Skin
	}
	if strings.TrimSpace(src.Handedness.String()) != "" {
		dst.Handedness = src.Handedness
	}
	if strings.TrimSpace(src.Height.String()) != "" {
		dst.Height = src.Height
	}
	if strings.TrimSpace(src.Weight.String()) != "" {
		dst.Weight = src.Weight
	}
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
			var err error
			traits, warning, err = d.addOrUpdateTrait(entity, traits, action)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("Warning: could not apply advantage action: %v", err))
				continue
			}
			if warning != "" {
				warnings = append(warnings, warning)
			}
		}
		for _, action := range plan.Disadvantages {
			var warning string
			var err error
			traits, warning, err = d.addOrUpdateTrait(entity, traits, action)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("Warning: could not apply disadvantage action: %v", err))
				continue
			}
			if warning != "" {
				warnings = append(warnings, warning)
			}
		}
		for _, action := range plan.Quirks {
			var warning string
			var err error
			traits, warning, err = d.addOrUpdateTrait(entity, traits, action)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("Warning: could not apply quirk action: %v", err))
				continue
			}
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
			var err error
			skills, warning, err = d.addOrUpdateSkill(entity, skills, action)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("Warning: could not apply skill action: %v", err))
				continue
			}
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
			var err error
			equipment, warning, err = d.addOrUpdateEquipment(entity, equipment, action)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("Warning: could not apply equipment action: %v", err))
				continue
			}
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
	name := strings.TrimSpace(action.Name.String())
	id := strings.TrimSpace(action.ID.String())
	valueText := strings.TrimSpace(action.Value.String())
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
	if name := strings.TrimSpace(action.Name.String()); name != "" {
		entity.Profile.Name = name
	}
	if title := strings.TrimSpace(action.Title.String()); title != "" {
		entity.Profile.Title = title
	}
	if org := strings.TrimSpace(action.Organization.String()); org != "" {
		entity.Profile.Organization = org
	}
	if religion := strings.TrimSpace(action.Religion.String()); religion != "" {
		entity.Profile.Religion = religion
	}
	if tl := strings.TrimSpace(action.TechLevel.String()); tl != "" {
		entity.Profile.TechLevel = tl
	}
	if gender := strings.TrimSpace(action.Gender.String()); gender != "" {
		entity.Profile.Gender = gender
	}
	if age := strings.TrimSpace(action.Age.String()); age != "" {
		entity.Profile.Age = age
	}
	if birthday := strings.TrimSpace(action.Birthday.String()); birthday != "" {
		entity.Profile.Birthday = birthday
	}
	if eyes := strings.TrimSpace(action.Eyes.String()); eyes != "" {
		entity.Profile.Eyes = eyes
	}
	if hair := strings.TrimSpace(action.Hair.String()); hair != "" {
		entity.Profile.Hair = hair
	}
	if skin := strings.TrimSpace(action.Skin.String()); skin != "" {
		entity.Profile.Skin = skin
	}
	if handedness := strings.TrimSpace(action.Handedness.String()); handedness != "" {
		entity.Profile.Handedness = handedness
	}

	// Parse and update height
	if heightStr := strings.TrimSpace(action.Height.String()); heightStr != "" {
		if height, err := fxp.LengthFromString(heightStr, fxp.Inch); err == nil {
			entity.Profile.Height = height
		}
	}

	// Parse and update weight
	if weightStr := strings.TrimSpace(action.Weight.String()); weightStr != "" {
		if weight, err := fxp.WeightFromString(weightStr, fxp.Pound); err == nil {
			entity.Profile.Weight = weight
		}
	}
}

func (d *aiChatDockable) findLibraryTraitByID(idStr string) (*gurps.Trait, gurps.LibraryFile, error) {
	idStr = normalizeAISelectionID(idStr)
	if idStr == "" {
		return nil, gurps.LibraryFile{}, nil
	}
	for _, set := range scanNamedFileSetsWithFallback(gurps.GlobalSettings().Libraries(), gurps.TraitsExt) {
		for _, ref := range set.List {
			traits, err := gurps.NewTraitsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, trait := range traits {
				if trait.Container() {
					continue
				}
				if string(trait.TID) == idStr {
					trait.SetDataOwner(nil)
					return trait, libraryFileForSet(set.Name, ref.FilePath), nil
				}
			}
		}
	}
	return nil, gurps.LibraryFile{}, nil
}

func (d *aiChatDockable) findLibraryTraitByName(name string) (*gurps.Trait, gurps.LibraryFile, error) {
	searchBase, _ := splitSkillNameAndSpecialization(name)
	for _, set := range scanNamedFileSetsWithFallback(gurps.GlobalSettings().Libraries(), gurps.TraitsExt) {
		for _, ref := range set.List {
			traits, err := gurps.NewTraitsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, trait := range traits {
				if trait.Container() {
					continue
				}
				libBase := traitBaseNameForLookup(trait.Name)
				if strings.EqualFold(trait.Name, name) ||
					strings.EqualFold(libBase, name) ||
					(searchBase != "" && (strings.EqualFold(trait.Name, searchBase) || strings.EqualFold(libBase, searchBase))) {
					trait.SetDataOwner(nil)
					return trait, libraryFileForSet(set.Name, ref.FilePath), nil
				}
			}
		}
	}
	return nil, gurps.LibraryFile{}, nil
}

func (d *aiChatDockable) findLibrarySkillByID(idStr string) (*gurps.Skill, gurps.LibraryFile, error) {
	idStr = normalizeAISelectionID(idStr)

	if idStr == "" {
		return nil, gurps.LibraryFile{}, nil
	}
	for _, set := range scanNamedFileSetsWithFallback(gurps.GlobalSettings().Libraries(), gurps.SkillsExt) {
		for _, ref := range set.List {
			skills, err := gurps.NewSkillsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, skill := range skills {
				if skill.Container() {
					continue
				}
				if string(skill.TID) == idStr {
					skill.SetDataOwner(nil)
					return skill, libraryFileForSet(set.Name, ref.FilePath), nil
				}
			}
		}
	}
	return nil, gurps.LibraryFile{}, nil
}

func normalizeLookupText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	var tokenBuilder strings.Builder
	tokens := make([]string, 0, 8)
	flushToken := func() {
		if tokenBuilder.Len() == 0 {
			return
		}
		tokens = append(tokens, tokenBuilder.String())
		tokenBuilder.Reset()
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			tokenBuilder.WriteRune(r)
			continue
		}
		flushToken()
	}
	flushToken()
	filtered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if isTLToken(token) {
			continue
		}
		token = canonicalizeSkillLookupToken(token)
		filtered = append(filtered, token)
	}
	return strings.Join(filtered, "")
}

// extractParenthetical returns the content inside the last set of parentheses, or "".
// e.g. "Code of Honor (Deadite-Only)" → "Deadite-Only"
func extractParenthetical(text string) string {
	text = strings.TrimSpace(text)
	open := strings.LastIndex(text, "(")
	close := strings.LastIndex(text, ")")
	if open == -1 || close <= open {
		return ""
	}
	return strings.TrimSpace(text[open+1 : close])
}

// applyNameablesToClonedTrait fills @key@ slots in a freshly cloned trait.
// It looks for the replacement value in (in priority order):
//  1. action.Notes field — explicit override
//  2. Parenthetical in action.Name that differs from the library's raw name
//     e.g. AI sends "Code of Honor (Deadite-Only)" → extracts "Deadite-Only"
func applyNameablesToClonedTrait(cloned *gurps.Trait, aiName, aiNotes string) {
	keys := make(map[string]string)
	cloned.FillWithNameableKeys(keys, nil)
	if len(keys) == 0 {
		return
	}
	value := strings.TrimSpace(aiNotes)
	if value == "" {
		value = extractParenthetical(aiName)
	}
	if value == "" {
		return
	}
	replacements := make(map[string]string, len(keys))
	for k := range keys {
		replacements[k] = value
	}
	cloned.Replacements = replacements
}

// applyNameablesToClonedSkill does the same for skills that have @key@ placeholders.
func applyNameablesToClonedSkill(cloned *gurps.Skill, aiName, aiNotes string) {
	keys := make(map[string]string)
	cloned.FillWithNameableKeys(keys, nil)
	if len(keys) == 0 {
		return
	}
	value := strings.TrimSpace(aiNotes)
	if value == "" {
		value = extractParenthetical(aiName)
	}
	if value == "" {
		return
	}
	replacements := make(map[string]string, len(keys))
	for k := range keys {
		replacements[k] = value
	}
	cloned.Replacements = replacements
}

// traitBaseNameForLookup strips nameable placeholder sections like "(@subject@)"
// from a library trait name so "Code of Honor (@subject@)" matches "Code of Honor".
func traitBaseNameForLookup(name string) string {
	for {
		open := strings.LastIndex(name, "(@")
		if open == -1 {
			break
		}
		closeIdx := strings.Index(name[open:], "@)")
		if closeIdx == -1 {
			break
		}
		name = strings.TrimSpace(name[:open] + name[open+closeIdx+2:])
	}
	return name
}

func normalizeAISelectionID(raw string) string {
	value := strings.Trim(strings.TrimSpace(raw), "\"'")
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "|"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	if idx := strings.IndexAny(value, "=:"); idx > 0 && strings.EqualFold(strings.TrimSpace(value[:idx]), "id") {
		value = strings.TrimSpace(value[idx+1:])
		if fields := strings.Fields(value); len(fields) > 0 {
			value = fields[0]
		}
	}
	// Strip wrapping punctuation but do NOT split on spaces —
	// multi-word IDs like "Code of Honor" must stay intact.
	return strings.Trim(value, "\"'<>[]{}()")
}

func canonicalizeSkillLookupToken(token string) string {
	switch token {
	case "car", "cars", "auto", "autos", "automobiles":
		return "automobile"
	default:
		return token
	}
}

func splitSkillNameAndSpecialization(text string) (name, specialization string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", ""
	}
	open := strings.LastIndex(trimmed, "(")
	close := strings.LastIndex(trimmed, ")")
	if open == -1 || close == -1 || close < open {
		return trimmed, ""
	}
	base := strings.TrimSpace(trimmed[:open])
	spec := strings.TrimSpace(trimmed[open+1 : close])
	if base == "" {
		return trimmed, ""
	}
	return base, spec
}

func skillDisplayName(name, specialization string) string {
	name = strings.TrimSpace(name)
	specialization = strings.TrimSpace(specialization)
	if specialization == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, specialization)
}

func isTLToken(token string) bool {
	if token == "tl" {
		return true
	}
	if !strings.HasPrefix(token, "tl") || len(token) <= 2 {
		return false
	}
	for _, r := range token[2:] {
		if !unicode.IsNumber(r) {
			return false
		}
	}
	return true
}

func (d *aiChatDockable) findLibrarySkillByName(name string) (*gurps.Skill, gurps.LibraryFile, error) {
	requestedBase, requestedSpec := splitSkillNameAndSpecialization(name)
	requestedNorm := normalizeLookupText(name)
	requestedBaseNorm := normalizeLookupText(requestedBase)
	requestedSpecNorm := normalizeLookupText(requestedSpec)
	if requestedBaseNorm == "" {
		requestedBaseNorm = normalizeLookupText(name)
	}
	var fuzzyMatch *gurps.Skill
	var fuzzyFile gurps.LibraryFile
	bestScore := int(^uint(0) >> 1)
	for _, set := range scanNamedFileSetsWithFallback(gurps.GlobalSettings().Libraries(), gurps.SkillsExt) {
		for _, ref := range set.List {
			skills, err := gurps.NewSkillsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, skill := range skills {
				if skill.Container() {
					continue
				}
				displayName := skillDisplayName(skill.Name, skill.Specialization)
				candidateBaseNorm := normalizeLookupText(skill.Name)
				candidateSpecNorm := normalizeLookupText(skill.Specialization)
				if strings.EqualFold(displayName, name) || strings.EqualFold(skill.Name, name) {
					skill.SetDataOwner(nil)
					return skill, libraryFileForSet(set.Name, ref.FilePath), nil
				}
				if requestedSpecNorm != "" && candidateBaseNorm == requestedBaseNorm && candidateSpecNorm == requestedSpecNorm {
					skill.SetDataOwner(nil)
					return skill, libraryFileForSet(set.Name, ref.FilePath), nil
				}
				candidateNorm := normalizeLookupText(displayName)
				if requestedNorm != "" && candidateNorm == requestedNorm {
					skill.SetDataOwner(nil)
					return skill, libraryFileForSet(set.Name, ref.FilePath), nil
				}
				if requestedNorm == "" || candidateNorm == "" {
					continue
				}
				if requestedSpecNorm != "" && candidateBaseNorm != requestedBaseNorm {
					continue
				}
				if strings.Contains(candidateNorm, requestedNorm) || strings.Contains(requestedNorm, candidateNorm) {
					score := len(candidateNorm) - len(requestedNorm)
					if score < 0 {
						score = -score
					}
					if score < bestScore {
						fuzzyMatch = skill
						fuzzyFile = libraryFileForSet(set.Name, ref.FilePath)
						bestScore = score
					}
				}
			}
		}
	}
	if fuzzyMatch != nil {
		fuzzyMatch.SetDataOwner(nil)
		return fuzzyMatch, fuzzyFile, nil
	}
	return nil, gurps.LibraryFile{}, nil
}

func (d *aiChatDockable) skillLookupDebugDetails(name, idStr string) string {
	requestedNorm := normalizeLookupText(name)
	if requestedNorm == "" {
		requestedNorm = "(empty)"
	}
	similar := make([]string, 0, 5)
	seen := make(map[string]struct{})
	total := 0
	for _, set := range scanNamedFileSetsWithFallback(gurps.GlobalSettings().Libraries(), gurps.SkillsExt) {
		for _, ref := range set.List {
			skills, err := gurps.NewSkillsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, skill := range skills {
				if skill.Container() {
					continue
				}
				total++
				if len(similar) >= 5 {
					continue
				}
				displayName := skillDisplayName(skill.Name, skill.Specialization)
				candNorm := normalizeLookupText(displayName)
				if candNorm == "" {
					continue
				}
				if strings.Contains(candNorm, requestedNorm) || strings.Contains(requestedNorm, candNorm) {
					if _, exists := seen[displayName]; exists {
						continue
					}
					seen[displayName] = struct{}{}
					similar = append(similar, displayName)
				}
			}
		}
	}
	if len(similar) == 0 {
		return fmt.Sprintf("skill_lookup_debug={id:%q normalized:%q scanned:%d similar:none}", idStr, requestedNorm, total)
	}
	return fmt.Sprintf("skill_lookup_debug={id:%q normalized:%q scanned:%d similar:%q}", idStr, requestedNorm, total, strings.Join(similar, ", "))
}

func (d *aiChatDockable) findLibraryEquipmentByID(idStr string) (*gurps.Equipment, gurps.LibraryFile, error) {
	idStr = normalizeAISelectionID(idStr)
	if idStr == "" {
		return nil, gurps.LibraryFile{}, nil
	}
	for _, set := range scanNamedFileSetsWithFallback(gurps.GlobalSettings().Libraries(), gurps.EquipmentExt) {
		for _, ref := range set.List {
			equipment, err := gurps.NewEquipmentFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, eqp := range equipment {
				if eqp.Container() {
					continue
				}
				if string(eqp.TID) == idStr {
					eqp.SetDataOwner(nil)
					return eqp, libraryFileForSet(set.Name, ref.FilePath), nil
				}
			}
		}
	}
	return nil, gurps.LibraryFile{}, nil
}

func (d *aiChatDockable) findLibraryEquipmentByName(name string) (*gurps.Equipment, gurps.LibraryFile, error) {
	for _, set := range scanNamedFileSetsWithFallback(gurps.GlobalSettings().Libraries(), gurps.EquipmentExt) {
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
					return eqp, libraryFileForSet(set.Name, ref.FilePath), nil
				}
			}
		}
	}
	return nil, gurps.LibraryFile{}, nil
}

func (d *aiChatDockable) addOrUpdateTrait(entity *gurps.Entity, traits []*gurps.Trait, action aiNamedAction) ([]*gurps.Trait, string, error) {
	name := strings.TrimSpace(action.Name.String())
	idStr := normalizeAISelectionID(action.ID.String())
	useTIDLookup := idStr != "" && tid.IsValid(tid.TID(idStr))
	if name == "" && idStr != "" && !useTIDLookup {
		// Some models place the trait name in "id" instead of "name".
		name = idStr
	}
	if name == "" && !useTIDLookup {
		return traits, "", fmt.Errorf("trait action is missing a name or valid id")
	}
	if useTIDLookup {
		if existing := d.findExistingTraitByID(entity, idStr); existing != nil {
			if pointsText := strings.TrimSpace(action.Points.String()); pointsText != "" {
				if points, err := fxp.FromString(pointsText); err == nil {
					existing.BasePoints = points
				}
			}
			return traits, "", nil
		}
	}
	if name != "" {
		if existing := d.findExistingTrait(entity, name); existing != nil {
			if pointsText := strings.TrimSpace(action.Points.String()); pointsText != "" {
				if points, err := fxp.FromString(pointsText); err == nil {
					existing.BasePoints = points
				}
			}
			return traits, "", nil
		}
	}
	var libraryTrait *gurps.Trait
	var libFile gurps.LibraryFile
	var err error
	warningPrefix := ""
	if idStr != "" && !useTIDLookup {
		warningPrefix = fmt.Sprintf("Warning: trait %q provided invalid id %q; falling back to name lookup. ", name, idStr)
	}

	// Try ID lookup first if ID is provided
	if useTIDLookup {
		libraryTrait, libFile, err = d.findLibraryTraitByID(idStr)
		if err != nil {
			return traits, "", err
		}
	}

	// Fall back to name lookup if ID lookup didn't find anything
	if libraryTrait == nil && name != "" {
		libraryTrait, libFile, err = d.findLibraryTraitByName(name)
		if err != nil {
			return traits, "", err
		}
	}

	// Also try the id string itself as a library name — the AI often puts the
	// library name in the "id" field rather than the actual TID.
	if libraryTrait == nil && idStr != "" && !strings.EqualFold(idStr, name) {
		libraryTrait, libFile, err = d.findLibraryTraitByName(idStr)
		if err != nil {
			return traits, "", err
		}
	}

	if libraryTrait == nil {
		return traits, fmt.Sprintf("%sWarning: trait %q was not found in the library and was skipped. Advantages, disadvantages and quirks must come from library entries.", warningPrefix, name), nil
	}
	cloned := libraryTrait.Clone(libFile, entity, nil, false)
	applyNameablesToClonedTrait(cloned, name, action.Notes.String())
	if pointsText := strings.TrimSpace(action.Points.String()); pointsText != "" {
		if points, err := fxp.FromString(pointsText); err == nil {
			cloned.BasePoints = points
		}
	}
	return append(traits, cloned), "", nil
}

func (d *aiChatDockable) addOrUpdateSkill(entity *gurps.Entity, skills []*gurps.Skill, action aiSkillAction) ([]*gurps.Skill, string, error) {
	name := strings.TrimSpace(action.Name.String())
	idStr := normalizeAISelectionID(action.ID.String())
	useTIDLookup := idStr != "" && tid.IsValid(tid.TID(idStr))
	if name == "" && idStr != "" && !useTIDLookup {
		// Some models place the skill name in "id" instead of "name".
		name = idStr
	}
	if name == "" && !useTIDLookup {
		return skills, "", fmt.Errorf("skill action is missing a name or valid id")
	}
	pointsText := strings.TrimSpace(action.Points.String())
	if pointsText == "" {
		// Accept alternate field used by some model responses.
		pointsText = strings.TrimSpace(action.Value.String())
	}
	warningPrefix := ""
	if idStr != "" && !useTIDLookup {
		warningPrefix = fmt.Sprintf("Warning: skill %q provided invalid id %q; falling back to name lookup. ", name, idStr)
	}
	if useTIDLookup {
		if existing := d.findExistingSkillByID(entity, idStr); existing != nil {
			if pointsText != "" {
				if points, err := fxp.FromString(pointsText); err == nil {
					existing.Points = points
				}
			}
			if levelText := strings.TrimSpace(action.Level.String()); levelText != "" {
				if level, err := fxp.FromString(levelText); err == nil {
					existing.LevelData.Level = level
				}
			}
			return skills, warningPrefix, nil
		}
	}
	if name != "" {
		if existing := d.findExistingSkill(entity, name); existing != nil {
			if pointsText != "" {
				if points, err := fxp.FromString(pointsText); err == nil {
					existing.Points = points
				}
			}
			if levelText := strings.TrimSpace(action.Level.String()); levelText != "" {
				if level, err := fxp.FromString(levelText); err == nil {
					existing.LevelData.Level = level
				}
			}
			return skills, warningPrefix, nil
		}
	}
	var librarySkill *gurps.Skill
	var libFile gurps.LibraryFile
	var err error

	// Try ID lookup first if ID is provided
	if useTIDLookup {
		librarySkill, libFile, err = d.findLibrarySkillByID(idStr)
		if err != nil {
			return skills, "", err
		}
	}

	// Fall back to name lookup if ID lookup didn't find anything
	if librarySkill == nil && name != "" {
		librarySkill, libFile, err = d.findLibrarySkillByName(name)
		if err != nil {
			return skills, "", err
		}
	}

	// Also try the id string itself as a skill name — the AI often puts the
	// library name in the "id" field rather than the actual TID.
	if librarySkill == nil && idStr != "" && !strings.EqualFold(idStr, name) && !useTIDLookup {
		librarySkill, libFile, err = d.findLibrarySkillByName(idStr)
		if err != nil {
			return skills, "", err
		}
	}

	if librarySkill == nil {
		details := d.skillLookupDebugDetails(name, idStr)
		return skills, fmt.Sprintf("%sWarning: skill %q was not found in the library and was skipped. Skills must be chosen from available database entries. %s", warningPrefix, name, details), nil
	}
	cloned := librarySkill.Clone(libFile, entity, nil, false)
	applyNameablesToClonedSkill(cloned, name, action.Notes.String())
	if pointsText != "" {
		if points, err := fxp.FromString(pointsText); err == nil {
			cloned.Points = points
		}
	}
	if levelText := strings.TrimSpace(action.Level.String()); levelText != "" {
		if level, err := fxp.FromString(levelText); err == nil {
			cloned.LevelData.Level = level
		}
	}
	return append(skills, cloned), warningPrefix, nil
}

func (d *aiChatDockable) addOrUpdateEquipment(entity *gurps.Entity, equipment []*gurps.Equipment, action aiNamedAction) ([]*gurps.Equipment, string, error) {
	name := strings.TrimSpace(action.Name.String())
	idStr := normalizeAISelectionID(action.ID.String())
	useTIDLookup := idStr != "" && tid.IsValid(tid.TID(idStr))
	if name == "" && idStr != "" && !useTIDLookup {
		// Some models place the equipment name in "id" instead of "name".
		name = idStr
	}
	if name == "" && !useTIDLookup {
		return equipment, "", fmt.Errorf("equipment action is missing a name or valid id")
	}
	if useTIDLookup {
		if existing := d.findExistingEquipmentByID(entity, idStr); existing != nil {
			if action.Quantity.Int() != 0 {
				existing.Quantity = fxp.FromInteger(action.Quantity.Int())
			}
			return equipment, "", nil
		}
	}
	if name != "" {
		if existing := d.findExistingEquipment(entity, name); existing != nil {
			if action.Quantity.Int() != 0 {
				existing.Quantity = fxp.FromInteger(action.Quantity.Int())
			}
			return equipment, "", nil
		}
	}
	var libraryEquipment *gurps.Equipment
	var libFile gurps.LibraryFile
	var err error
	warningPrefix := ""
	if idStr != "" && !useTIDLookup {
		warningPrefix = fmt.Sprintf("Warning: equipment %q provided invalid id %q; falling back to name lookup. ", name, idStr)
	}

	// Try ID lookup first if ID is provided
	if useTIDLookup {
		libraryEquipment, libFile, err = d.findLibraryEquipmentByID(idStr)
		if err != nil {
			return equipment, "", err
		}
	}

	// Fall back to name lookup if ID lookup didn't find anything
	if libraryEquipment == nil && name != "" {
		libraryEquipment, libFile, err = d.findLibraryEquipmentByName(name)
		if err != nil {
			return equipment, "", err
		}
	}

	if libraryEquipment == nil {
		custom := gurps.NewEquipment(entity, nil, false)
		custom.Name = name
		custom.Quantity = fxp.One
		if action.Quantity.Int() > 0 {
			custom.Quantity = fxp.FromInteger(action.Quantity.Int())
		}
		return append(equipment, custom), fmt.Sprintf("%sNotice: custom equipment %q was added because no library match was found. Library equipment is preferred.", warningPrefix, name), nil
	}
	cloned := libraryEquipment.Clone(libFile, entity, nil, false)
	if action.Quantity.Int() != 0 {
		cloned.Quantity = fxp.FromInteger(action.Quantity.Int())
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

func (d *aiChatDockable) findExistingTraitByID(entity *gurps.Entity, idStr string) *gurps.Trait {
	idStr = normalizeAISelectionID(idStr)
	if idStr == "" {
		return nil
	}
	for _, trait := range entity.Traits {
		if trait.Container() {
			continue
		}
		if string(trait.TID) == idStr {
			return trait
		}
	}
	return nil
}

func (d *aiChatDockable) findExistingSkill(entity *gurps.Entity, name string) *gurps.Skill {
	name = strings.TrimSpace(name)
	requestedNorm := normalizeLookupText(name)
	for _, skill := range entity.Skills {
		if skill.Container() {
			continue
		}
		displayName := skillDisplayName(skill.Name, skill.Specialization)
		if strings.EqualFold(skill.Name, name) || strings.EqualFold(displayName, name) {
			return skill
		}
		if requestedNorm != "" && normalizeLookupText(displayName) == requestedNorm {
			return skill
		}
	}
	return nil
}

func (d *aiChatDockable) findExistingSkillByID(entity *gurps.Entity, idStr string) *gurps.Skill {
	idStr = normalizeAISelectionID(idStr)
	if idStr == "" {
		return nil
	}
	for _, skill := range entity.Skills {
		if skill.Container() {
			continue
		}
		if string(skill.TID) == idStr {
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

func (d *aiChatDockable) findExistingEquipmentByID(entity *gurps.Entity, idStr string) *gurps.Equipment {
	idStr = normalizeAISelectionID(idStr)
	if idStr == "" {
		return nil
	}
	for _, eqp := range entity.CarriedEquipment {
		if eqp.Container() {
			continue
		}
		if string(eqp.TID) == idStr {
			return eqp
		}
	}
	for _, eqp := range entity.OtherEquipment {
		if eqp.Container() {
			continue
		}
		if string(eqp.TID) == idStr {
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
		writeSystemPromptDebugFile(systemPrompt)
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

		sysPrompt := d.aiAssistantSystemPrompt()
		writeSystemPromptDebugFile(sysPrompt)
		var messages []message
		messages = append(messages, message{Role: "system", Content: sysPrompt})
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
