package ux

import (
	"io/fs"
	"sort"
	"strings"

	"github.com/richardwilkes/gcs/v5/model/gurps"
	"github.com/richardwilkes/gcs/v5/svg"
	"github.com/richardwilkes/toolbox/v2/i18n"
	"github.com/richardwilkes/unison"
	"github.com/richardwilkes/unison/enums/align"
)

type aiResolverAliasMappingsDockable struct {
	SettingsDockable
	content      *unison.Panel
	rows         []*aiResolverAliasRow
	pendingFocus *aiResolverAliasRow
}

type aiResolverAliasRow struct {
	Category aiLibraryCategory
	Alias    string
	Mapped   string
}

func ShowAIResolverAliasMappings() {
	if Activate(func(d unison.Dockable) bool {
		_, ok := d.AsPanel().Self.(*aiResolverAliasMappingsDockable)
		return ok
	}) {
		return
	}
	d := &aiResolverAliasMappingsDockable{rows: aiResolverAliasRowsFromSettings()}
	d.Self = d
	d.TabTitle = i18n.Text("AI Resolver Alias Mappings")
	d.TabIcon = svg.Bot
	d.Extensions = []string{gurps.AIResolverAliasesExt}
	d.Loader = d.load
	d.Saver = d.save
	d.Resetter = d.reset
	d.Setup(d.addToStartToolbar, nil, d.initContent)
}

func (d *aiResolverAliasMappingsDockable) addToStartToolbar(toolbar *unison.Panel) {
	addButton := unison.NewSVGButton(svg.CircledAdd)
	addButton.Tooltip = newWrappedTooltip(i18n.Text("Add Alias Mapping"))
	addButton.ClickCallback = d.addRow
	toolbar.AddChild(addButton)
}

func (d *aiResolverAliasMappingsDockable) initContent(content *unison.Panel) {
	d.content = content
	d.content.SetLayout(&unison.FlexLayout{
		Columns:  4,
		HSpacing: unison.StdHSpacing,
		VSpacing: unison.StdVSpacing,
	})
	d.sync()
}

func (d *aiResolverAliasMappingsDockable) reset() {
	settings := gurps.GlobalSettings()
	settings.AI.ResolverAliases = gurps.DefaultAIResolverAliases()
	if err := settings.Save(); err != nil {
		Workspace.ErrorHandler(i18n.Text("Unable to save AI resolver alias mappings"), err)
	}
	d.rows = aiResolverAliasRowsFromSettings()
	d.sync()
}

func (d *aiResolverAliasMappingsDockable) addRow() {
	row := &aiResolverAliasRow{Category: aiLibraryCategorySkill}
	d.rows = append(d.rows, row)
	d.pendingFocus = row
	d.sync()
}

func (d *aiResolverAliasMappingsDockable) sync() {
	d.content.RemoveAllChildren()
	d.content.AddChild(unison.NewLabel())
	d.content.AddChild(aiResolverAliasHeaderLabel(i18n.Text("Category")))
	d.content.AddChild(aiResolverAliasHeaderLabel(i18n.Text("Alias")))
	d.content.AddChild(aiResolverAliasHeaderLabel(i18n.Text("Canonical Name")))
	if len(d.rows) == 0 {
		label := unison.NewLabel()
		label.SetTitle(i18n.Text("No alias mappings configured. Use the add button to create one."))
		d.content.AddChild(WrapWithSpan(4, label))
		d.pendingFocus = nil
		d.MarkForLayoutAndRedraw()
		return
	}
	for _, row := range d.rows {
		d.createTrashField(row)
		d.createCategoryField(row)
		aliasField := d.createAliasField(row)
		d.createMappedField(row)
		if row == d.pendingFocus {
			aliasField.RequestFocus()
		}
	}
	d.pendingFocus = nil
	d.MarkForLayoutAndRedraw()
}

func (d *aiResolverAliasMappingsDockable) createTrashField(row *aiResolverAliasRow) {
	b := unison.NewSVGButton(svg.Trash)
	b.Tooltip = newWrappedTooltip(i18n.Text("Remove Alias Mapping"))
	b.ClickCallback = func() {
		for i, candidate := range d.rows {
			if candidate != row {
				continue
			}
			d.rows = append(d.rows[:i], d.rows[i+1:]...)
			d.persistRows()
			d.sync()
			return
		}
	}
	b.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Middle, VAlign: align.Middle})
	d.content.AddChild(b)
}

func (d *aiResolverAliasMappingsDockable) createCategoryField(row *aiResolverAliasRow) {
	popup := unison.NewPopupMenu[aiLibraryCategory]()
	for _, category := range aiResolverAliasCategoryChoices(row.Category) {
		popup.AddItem(category)
	}
	if row.Category == "" {
		row.Category = aiLibraryCategorySkill
	}
	popup.Select(row.Category)
	popup.SelectionChangedCallback = func(p *unison.PopupMenu[aiLibraryCategory]) {
		if category, ok := p.Selected(); ok {
			row.Category = category
			d.persistRows()
		}
	}
	popup.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill, HGrab: true})
	d.content.AddChild(popup)
}

func (d *aiResolverAliasMappingsDockable) createAliasField(row *aiResolverAliasRow) *StringField {
	field := NewStringField(nil, "", i18n.Text("Alias"),
		func() string { return row.Alias },
		func(value string) {
			row.Alias = strings.TrimSpace(value)
			d.persistRows()
		})
	field.Tooltip = newWrappedTooltip(i18n.Text("The phrase the AI tends to guess, such as Handguns."))
	d.content.AddChild(field)
	return field
}

func (d *aiResolverAliasMappingsDockable) createMappedField(row *aiResolverAliasRow) {
	field := NewStringField(nil, "", i18n.Text("Canonical Name"),
		func() string { return row.Mapped },
		func(value string) {
			row.Mapped = strings.TrimSpace(value)
			d.persistRows()
		})
	field.Tooltip = newWrappedTooltip(i18n.Text("The canonical GURPS library name the resolver should use."))
	d.content.AddChild(field)
}

func (d *aiResolverAliasMappingsDockable) persistRows() {
	aliases := make(map[string]map[string]string)
	for _, row := range d.rows {
		category := strings.TrimSpace(string(row.Category))
		alias := strings.ToLower(strings.TrimSpace(row.Alias))
		mapped := strings.TrimSpace(row.Mapped)
		if category == "" || alias == "" || mapped == "" {
			continue
		}
		bucket := aliases[category]
		if bucket == nil {
			bucket = make(map[string]string)
			aliases[category] = bucket
		}
		bucket[alias] = mapped
	}
	settings := gurps.GlobalSettings()
	settings.AI.ResolverAliases = aliases
	if err := settings.Save(); err != nil {
		Workspace.ErrorHandler(i18n.Text("Unable to save AI resolver alias mappings"), err)
	}
}

func (d *aiResolverAliasMappingsDockable) load(fileSystem fs.FS, filePath string) error {
	aliases, err := gurps.LoadAIResolverAliases(fileSystem, filePath)
	if err != nil {
		return err
	}
	settings := gurps.GlobalSettings()
	settings.AI.ResolverAliases = aliases
	if err = settings.Save(); err != nil {
		return err
	}
	d.rows = aiResolverAliasRowsFromSettings()
	d.sync()
	return nil
}

func (d *aiResolverAliasMappingsDockable) save(filePath string) error {
	return gurps.SaveAIResolverAliases(filePath, gurps.GlobalSettings().AI.ResolverAliases)
}

func aiResolverAliasHeaderLabel(title string) *unison.Label {
	label := unison.NewLabel()
	label.SetTitle(title)
	label.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill})
	return label
}

func aiResolverAliasCategoryChoices(current aiLibraryCategory) []aiLibraryCategory {
	choices := append([]aiLibraryCategory(nil), aiLibraryCategories...)
	if current == "" || aiResolverAliasCategoryKnown(current) {
		return choices
	}
	return append(choices, current)
}

func aiResolverAliasCategoryKnown(category aiLibraryCategory) bool {
	for _, one := range aiLibraryCategories {
		if one == category {
			return true
		}
	}
	return false
}

func aiResolverAliasRowsFromSettings() []*aiResolverAliasRow {
	settings := gurps.GlobalSettings().AI.ResolverAliases
	var rows []*aiResolverAliasRow
	for _, category := range aiLibraryCategories {
		rows = append(rows, aiResolverAliasRowsForCategory(category, settings[string(category)])...)
	}
	var extraCategories []string
	for category := range settings {
		if aiResolverAliasCategoryKnown(aiLibraryCategory(category)) {
			continue
		}
		extraCategories = append(extraCategories, category)
	}
	sort.Strings(extraCategories)
	for _, category := range extraCategories {
		rows = append(rows, aiResolverAliasRowsForCategory(aiLibraryCategory(category), settings[category])...)
	}
	return rows
}

func aiResolverAliasRowsForCategory(category aiLibraryCategory, aliases map[string]string) []*aiResolverAliasRow {
	if len(aliases) == 0 {
		return nil
	}
	keys := make([]string, 0, len(aliases))
	for alias := range aliases {
		keys = append(keys, alias)
	}
	sort.Strings(keys)
	rows := make([]*aiResolverAliasRow, 0, len(keys))
	for _, alias := range keys {
		rows = append(rows, &aiResolverAliasRow{
			Category: category,
			Alias:    alias,
			Mapped:   aliases[alias],
		})
	}
	return rows
}
