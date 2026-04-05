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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
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
	aiChatDockKey            = "ai.chat"
	aiChatBubbleRadius       = 12
	aiChatBubbleMinWidth     = 240
	aiChatBubbleMaxWidth     = 1200
	aiChatInputSideMarginPct = 0.10
	aiChatInputBottomMargin  = 250
	aiChatInputRadius        = 14
	aiChatInputMinHeight     = 120
	aiChatInputFixedWidth    = 1200
	aiChatPDFPageWidth       = 612
	aiChatPDFPageHeight      = 792
	aiChatPDFHorizontalInset = 54
	aiChatPDFVerticalInset   = 54
)

var (
	_                     unison.Dockable  = &aiChatDockable{}
	_                     KeyedDockable    = &aiChatDockable{}
	_                     unison.TabCloser = &aiChatDockable{}
	aiChatHumanBubble                      = &unison.ThemeColor{Light: unison.White, Dark: unison.White}
	aiChatHumanText                        = unison.ThemeOnSurface
	aiChatAIBubble                         = &unison.ThemeColor{Light: unison.RGB(213, 240, 214), Dark: unison.RGB(62, 111, 64)}
	aiChatAIText                           = &unison.ThemeColor{Light: unison.Black, Dark: unison.White}
	aiChatAIBubbleEdge                     = &unison.ThemeColor{Light: unison.RGB(120, 173, 122), Dark: unison.RGB(98, 168, 101)}
	aiChatAuthorText                       = &unison.ThemeColor{Light: unison.Black.SetAlphaIntensity(0.65), Dark: unison.White.SetAlphaIntensity(0.78)}
	aiChatInputBackground                  = &unison.ThemeColor{Light: unison.White, Dark: unison.White}
	aiChatInputEdge                        = &unison.ThemeColor{Light: unison.White, Dark: unison.White}
)

type aiChatMessage struct {
	Author    string
	Text      string
	Timestamp time.Time
}

type aiChatPDFExporter struct {
	lines        []*unison.Text
	pageSize     geom.Size
	linesPerPage int
	topInset     float32
	leftInset    float32
}

type aiChatDockable struct {
	unison.Panel
	historyPanel     *unison.Panel
	scroll           *unison.ScrollPanel
	inputField       *unison.Field
	newSessionButton *unison.Button
	submitButton     *unison.Button
	chatHistory      []*genai.Content
	chatMessages     []aiChatMessage
	buildSession     *aiBuildSessionContext
	geminiBuild      *aiGeminiBuildSession
	isThinking       bool
	thinkingMessage  *unison.Panel
}

var (
	_ unison.Layout = &aiChatMessageRowLayout{}
	_ unison.Layout = &aiChatBubbleLayout{}
	_ unison.Layout = &aiChatInputLayout{}
)

type aiChatMessageRowLayout struct {
	bubble     *unison.Panel
	alignRight bool
}

type aiChatBubbleLayout struct {
	dockable *aiChatDockable
	message  string
	isHuman  bool
	header   *unison.Panel
	markdown *unison.Markdown
	field    *unison.Field
	spacing  float32
}

type aiChatBubbleMeasurement struct {
	maxWidth     float32
	contentWidth float32
	minSize      geom.Size
	prefSize     geom.Size
	headerPref   geom.Size
	contentPref  geom.Size
}

type aiChatInputLayout struct {
	shell   *unison.Panel
	button  *unison.Button
	spacing float32
}

func (l *aiChatMessageRowLayout) LayoutSizes(_ *unison.Panel, hint geom.Size) (minSize, prefSize, maxSize geom.Size) {
	measureHint := hint
	if measureHint.Width <= 0 {
		measureHint.Width = float32(aiChatBubbleMinWidth)
	}
	bubbleMin, bubblePref, _ := l.bubble.Sizes(measureHint)
	compactMinWidth := min(float32(aiChatBubbleMinWidth), bubbleMin.Width)
	compactPrefWidth := min(float32(aiChatBubbleMinWidth), bubblePref.Width)
	minSize = geom.NewSize(compactMinWidth, bubbleMin.Height)
	prefSize = geom.NewSize(compactPrefWidth, bubblePref.Height)
	maxSize = geom.NewSize(unison.DefaultMaxSize, max(bubbleMin.Height, bubblePref.Height))
	return minSize, prefSize, maxSize
}

func (l *aiChatMessageRowLayout) PerformLayout(target *unison.Panel) {
	rect := target.ContentRect(false)
	if rect.Width <= 0 {
		return
	}
	_, prefSize, _ := l.bubble.Sizes(rect.Size)
	frame := rect
	frame.Width = min(prefSize.Width, rect.Width)
	frame.Height = prefSize.Height
	if l.alignRight {
		frame.X = rect.Right() - frame.Width
	}
	l.bubble.SetFrameRect(frame)
}

func (l *aiChatBubbleLayout) plainTextContentSize(maxWidth float32) geom.Size {
	if maxWidth < 1 {
		maxWidth = 1
	}
	_, fieldPref, _ := l.field.Sizes(geom.NewSize(maxWidth, 0))
	decoration := &unison.TextDecoration{Font: l.field.Font}
	lines := unison.NewTextWrappedLines(l.message, decoration, maxWidth)
	width := float32(0)
	for _, line := range lines {
		width = max(width, line.Width())
	}
	if width > 0 {
		width += 2
	}
	if width > maxWidth {
		width = maxWidth
	}
	return geom.NewSize(width, fieldPref.Height)
}

func (l *aiChatBubbleLayout) contentSizes(maxWidth float32) (minSize, prefSize geom.Size) {
	if l.isHuman || !l.field.Hidden {
		plainPref := l.plainTextContentSize(maxWidth)
		if !l.field.Hidden {
			return plainPref, plainPref
		}
		if plainPref.Width < 1 {
			plainPref.Width = 1
		}
		l.markdown.SetContent(l.message, plainPref.Width)
		_, markdownPref, _ := l.markdown.Sizes(geom.NewSize(plainPref.Width, 0))
		prefSize = geom.NewSize(plainPref.Width, markdownPref.Height)
		return prefSize, prefSize
	}
	l.markdown.SetContent(l.message, maxWidth)
	minSize, prefSize, _ = l.markdown.Sizes(geom.NewSize(maxWidth, 0))
	return minSize, prefSize
}

func (l *aiChatBubbleLayout) measure(target *unison.Panel, hint geom.Size) aiChatBubbleMeasurement {
	var insets geom.Size
	if border := target.Border(); border != nil {
		insets = border.Insets().Size()
	}
	maxWidth := float32(aiChatBubbleMaxWidth)
	if l.dockable != nil {
		maxWidth = l.dockable.maxBubbleWidth(hint.Width, maxWidth)
	}
	if maxWidth <= 0 {
		maxWidth = float32(aiChatBubbleMaxWidth)
	}
	if hint.Width > 0 {
		maxWidth = min(maxWidth, hint.Width)
	}
	if maxWidth < insets.Width+1 {
		maxWidth = insets.Width + 1
	}
	maxContentWidth := maxWidth - insets.Width
	if maxContentWidth < 1 {
		maxContentWidth = 1
	}
	headerHint := geom.NewSize(maxContentWidth, 0)
	headerMin, headerPref, _ := l.header.Sizes(headerHint)
	contentMin, contentPref := l.contentSizes(maxContentWidth)
	minContentWidth := max(headerMin.Width, contentMin.Width)
	prefContentWidth := max(headerPref.Width, contentPref.Width)
	if minContentWidth > maxContentWidth {
		minContentWidth = maxContentWidth
	}
	if prefContentWidth > maxContentWidth {
		prefContentWidth = maxContentWidth
	}
	if prefContentWidth < minContentWidth {
		prefContentWidth = minContentWidth
	}
	spacing := float32(0)
	if headerPref.Height > 0 && contentPref.Height > 0 {
		spacing = l.spacing
	}
	return aiChatBubbleMeasurement{
		maxWidth:     maxWidth,
		contentWidth: prefContentWidth,
		minSize:      geom.NewSize(minContentWidth+insets.Width, headerMin.Height+contentMin.Height+spacing+insets.Height),
		prefSize:     geom.NewSize(prefContentWidth+insets.Width, headerPref.Height+contentPref.Height+spacing+insets.Height),
		headerPref:   headerPref,
		contentPref:  contentPref,
	}
}

func (l *aiChatBubbleLayout) LayoutSizes(target *unison.Panel, hint geom.Size) (minSize, prefSize, maxSize geom.Size) {
	measurement := l.measure(target, hint)
	return measurement.minSize, measurement.prefSize, geom.NewSize(measurement.maxWidth, unison.DefaultMaxSize)
}

func (l *aiChatBubbleLayout) PerformLayout(target *unison.Panel) {
	rect := target.ContentRect(false)
	if rect.Width <= 0 {
		return
	}
	measurement := l.measure(target, target.ContentRect(true).Size)
	headerHeight := measurement.headerPref.Height
	l.header.SetFrameRect(geom.NewRect(rect.X, rect.Y, rect.Width, headerHeight))
	y := rect.Y + headerHeight
	if headerHeight > 0 && measurement.contentPref.Height > 0 {
		y += l.spacing
	}
	if l.field.Hidden {
		l.markdown.SetContent(l.message, rect.Width)
		_, markdownPref, _ := l.markdown.Sizes(geom.NewSize(rect.Width, 0))
		l.markdown.SetFrameRect(geom.NewRect(rect.X, y, rect.Width, markdownPref.Height))
		l.field.SetFrameRect(geom.NewRect(rect.X, y, 0, 0))
		return
	}
	plainPref := l.plainTextContentSize(rect.Width)
	l.field.SetFrameRect(geom.NewRect(rect.X, y, rect.Width, plainPref.Height))
	l.markdown.SetFrameRect(geom.NewRect(rect.X, y, 0, 0))
}

func (l *aiChatInputLayout) desiredShellWidth(availableWidth, shellMinWidth, buttonWidth float32) float32 {
	if availableWidth <= 0 {
		return max(float32(aiChatInputFixedWidth), shellMinWidth)
	}
	width := availableWidth * (1 - 2*aiChatInputSideMarginPct)
	if width <= 0 {
		width = availableWidth
	}
	maxCenteredWidth := availableWidth - 2*(buttonWidth+l.spacing)
	if maxCenteredWidth > 0 {
		width = min(width, maxCenteredWidth)
	}
	if width < shellMinWidth {
		width = min(shellMinWidth, availableWidth)
	}
	return max(width, 1)
}

func (l *aiChatInputLayout) LayoutSizes(target *unison.Panel, hint geom.Size) (minSize, prefSize, maxSize geom.Size) {
	shellMin, shellPref, _ := l.shell.Sizes(geom.NewSize(float32(aiChatInputFixedWidth), 0))
	buttonMin, buttonPref, _ := l.button.Sizes(geom.Size{})
	minHeight := max(shellMin.Height, buttonMin.Height)
	prefHeight := max(shellPref.Height, buttonPref.Height)
	minWidth := shellMin.Width + l.spacing + buttonMin.Width
	prefWidth := shellPref.Width + l.spacing + buttonPref.Width
	if hint.Width > 0 {
		desiredShellWidth := l.desiredShellWidth(hint.Width, shellMin.Width, buttonPref.Width)
		_, shellPrefAtWidth, _ := l.shell.Sizes(geom.NewSize(desiredShellWidth, 0))
		prefWidth = max(prefWidth, desiredShellWidth+l.spacing+buttonPref.Width)
		prefHeight = max(prefHeight, shellPrefAtWidth.Height)
	}
	return geom.NewSize(minWidth, minHeight), geom.NewSize(prefWidth, prefHeight), geom.NewSize(unison.DefaultMaxSize, prefHeight)
}

func (l *aiChatInputLayout) PerformLayout(target *unison.Panel) {
	rect := target.ContentRect(false)
	if rect.Width <= 0 {
		return
	}
	shellMin, _, _ := l.shell.Sizes(geom.NewSize(0, 0))
	_, buttonPref, _ := l.button.Sizes(geom.Size{})
	shellWidth := l.desiredShellWidth(rect.Width, shellMin.Width, buttonPref.Width)
	_, shellPref, _ := l.shell.Sizes(geom.NewSize(shellWidth, 0))
	rowHeight := max(shellPref.Height, buttonPref.Height)
	groupWidth := shellWidth + l.spacing + buttonPref.Width
	shellX := rect.X + (rect.Width-shellWidth)/2
	buttonX := shellX + shellWidth + l.spacing
	if groupWidth > rect.Width {
		shellWidth = max(1, rect.Width-buttonPref.Width-l.spacing)
		_, shellPref, _ = l.shell.Sizes(geom.NewSize(shellWidth, 0))
		rowHeight = max(shellPref.Height, buttonPref.Height)
		groupWidth = shellWidth + l.spacing + buttonPref.Width
		shellX = rect.X + max(0, (rect.Width-groupWidth)/2)
		buttonX = shellX + shellWidth + l.spacing
	}
	y := rect.Y
	if rect.Height > rowHeight {
		y += (rect.Height - rowHeight) / 2
	}
	l.shell.SetFrameRect(geom.NewRect(shellX, y, shellWidth, shellPref.Height))
	l.button.SetFrameRect(geom.NewRect(buttonX, y+(rowHeight-buttonPref.Height)/2, buttonPref.Width, buttonPref.Height))
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
	d.newSessionButton = unison.NewButton()
	d.newSessionButton.SetTitle(i18n.Text("New Session"))
	d.newSessionButton.Tooltip = newWrappedTooltip(i18n.Text("Start a fresh AI session and clear the current conversation memory"))
	d.newSessionButton.ClickCallback = d.clearHistory
	toolbar.AddChild(d.newSessionButton)
	exportPDFButton := unison.NewSVGButton(svg.PDFFile)
	exportPDFButton.Tooltip = newWrappedTooltip(i18n.Text("Export Chat History as PDF"))
	exportPDFButton.ClickCallback = d.exportChatHistoryAsPDF
	toolbar.AddChild(exportPDFButton)
	configButton := unison.NewSVGButton(svg.Gears)
	configButton.Tooltip = newWrappedTooltip(i18n.Text("Configure AI Settings"))
	configButton.ClickCallback = ShowAISettings
	toolbar.AddChild(configButton)
	d.AddChild(toolbar)
	// Chat History
	d.historyPanel = unison.NewPanel()
	d.historyPanel.SetBorder(unison.NewEmptyBorder(geom.Insets{Top: 8, Left: 4, Bottom: 8, Right: 4}))
	d.historyPanel.SetLayout(&unison.FlexLayout{Columns: 1, VSpacing: unison.StdVSpacing / 2, HAlign: align.Fill})
	d.scroll = unison.NewScrollPanel()
	d.scroll.SetContent(d.historyPanel, behavior.Follow, behavior.Unmodified)
	d.scroll.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill, VAlign: align.Fill, HGrab: true, VGrab: true})
	d.AddChild(d.scroll)
	// Input area
	inputArea := unison.NewPanel()
	inputArea.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill, HGrab: true})
	inputArea.SetBorder(unison.NewEmptyBorder(geom.Insets{Top: unison.StdVSpacing / 2, Bottom: unison.StdVSpacing*2 + aiChatInputBottomMargin}))
	inputShell := unison.NewPanel()
	inputShell.SetLayout(&unison.FlexLayout{Columns: 1})
	inputShell.SetBorder(unison.NewEmptyBorder(geom.Insets{Top: 10, Left: 12, Bottom: 10, Right: 12}))
	inputShell.DrawCallback = func(gc *unison.Canvas, rect geom.Rect) {
		unison.DrawRoundedRectBase(gc, rect, geom.NewUniformSize(aiChatInputRadius), 0, aiChatInputBackground, aiChatInputEdge)
	}
	d.inputField = unison.NewMultiLineField()
	unison.UninstallFocusBorders(d.inputField, d.inputField)
	d.inputField.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill, VAlign: align.Fill, HGrab: true, MinSize: geom.Size{Height: aiChatInputMinHeight}})
	d.inputField.SetBorder(nil)
	d.inputField.BackgroundInk = unison.White
	d.inputField.EditableInk = unison.White
	d.inputField.ErrorInk = unison.White
	d.inputField.OnBackgroundInk = unison.Black
	d.inputField.OnEditableInk = unison.Black
	d.inputField.OnErrorInk = unison.Black
	d.inputField.SelectionInk = unison.Gray.SetAlphaIntensity(0.25)
	d.inputField.OnSelectionInk = unison.Black
	d.inputField.NoSelectAllOnFocus = true
	d.inputField.KeyDownCallback = func(keyCode unison.KeyCode, mod unison.Modifiers, repeat bool) bool {
		if keyCode == unison.KeyReturn || keyCode == unison.KeyNumPadEnter {
			if mod.ShiftDown() {
				return d.inputField.DefaultKeyDown(keyCode, mod, repeat)
			}
			d.submit()
			return true
		}
		return d.inputField.DefaultKeyDown(keyCode, mod, repeat)
	}
	inputShell.AddChild(d.inputField)
	d.submitButton = unison.NewButton()
	d.submitButton.SetTitle(i18n.Text("Submit"))
	d.submitButton.ClickCallback = d.submit
	d.submitButton.BackgroundInk = unison.White
	d.submitButton.OnBackgroundInk = unison.Black
	d.submitButton.SelectionInk = unison.Gray.SetAlphaIntensity(0.2)
	d.submitButton.OnSelectionInk = unison.Black
	d.submitButton.EdgeInk = aiChatInputEdge
	inputArea.SetLayout(&aiChatInputLayout{shell: inputShell, button: d.submitButton, spacing: unison.StdHSpacing / 2})
	inputArea.AddChild(inputShell)
	inputArea.AddChild(d.submitButton)
	d.AddChild(inputArea)
}

func (d *aiChatDockable) clearHistory() {
	if d.isThinking {
		return
	}
	message := i18n.Text("Start a new AI session? This clears the current chat history and conversation memory so the next request starts fresh.")
	if unison.QuestionDialog(message, "") == unison.ModalResponseOK {
		d.chatHistory = nil
		d.chatMessages = nil
		d.buildSession = nil
		d.geminiBuild = nil
		d.inputField.SetText("")
		d.historyPanel.RemoveAllChildren()
		d.historyPanel.MarkForLayoutAndRedraw()
	}
}

func (d *aiChatDockable) maxBubbleWidth(availableWidth, targetWidth float32) float32 {
	if availableWidth <= 0 && d.scroll != nil {
		availableWidth = d.scroll.ContentRect(false).Width
	}
	if availableWidth <= 0 && d.historyPanel != nil {
		availableWidth = d.historyPanel.ContentRect(false).Width
	}
	if availableWidth <= 0 {
		return targetWidth
	}
	if availableWidth < 1 {
		return 1
	}
	return min(availableWidth, targetWidth)
}

func (d *aiChatDockable) exportChatHistoryAsPDF() {
	dialog := unison.NewSaveDialog()
	dialog.SetAllowedExtensions("pdf")
	dialog.SetInitialDirectory(gurps.GlobalSettings().LastDir(gurps.DefaultLastDirKey))
	dialog.SetInitialFileName("ai-chat-history")
	if !dialog.RunModal() {
		return
	}
	filePath, ok := unison.ValidateSaveFilePath(dialog.Path(), "pdf", false)
	if !ok {
		return
	}
	gurps.GlobalSettings().SetLastDir(gurps.DefaultLastDirKey, filepath.Dir(filePath))
	if err := os.Remove(filePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		Workspace.ErrorHandler(i18n.Text("Unable to export chat history as PDF"), err)
		return
	}
	stream, err := unison.NewFileStream(filePath)
	if err != nil {
		Workspace.ErrorHandler(i18n.Text("Unable to export chat history as PDF"), err)
		return
	}
	defer stream.Close()
	exporter := newAIChatPDFExporter(d.chatMessages)
	if err = unison.CreatePDF(stream, &unison.PDFMetaData{Title: i18n.Text("AI Chat History")}, exporter); err != nil {
		Workspace.ErrorHandler(i18n.Text("Unable to export chat history as PDF"), err)
		return
	}
	d.addMessage("AI", fmt.Sprintf(i18n.Text("Chat history exported to %s"), filePath))
}

func newAIChatPDFExporter(messages []aiChatMessage) *aiChatPDFExporter {
	decoration := &unison.TextDecoration{Font: unison.DefaultLabelTheme.Font, OnBackgroundInk: unison.Black}
	var textBuilder strings.Builder
	for _, msg := range messages {
		textBuilder.WriteString(msg.Author)
		if !msg.Timestamp.IsZero() {
			textBuilder.WriteString(" [")
			textBuilder.WriteString(msg.Timestamp.Format("2006-01-02 3:04 PM"))
			textBuilder.WriteString("]")
		}
		textBuilder.WriteString(":\n")
		textBuilder.WriteString(msg.Text)
		textBuilder.WriteString("\n\n")
	}
	if strings.TrimSpace(textBuilder.String()) == "" {
		textBuilder.WriteString(i18n.Text("No chat messages available."))
	}
	usableWidth := float32(aiChatPDFPageWidth - aiChatPDFHorizontalInset*2)
	lines := unison.NewTextWrappedLines(textBuilder.String(), decoration, usableWidth)
	lineHeight := decoration.Font.LineHeight()
	if len(lines) != 0 {
		lineHeight = lines[0].Height()
	}
	usableHeight := float32(aiChatPDFPageHeight - aiChatPDFVerticalInset*2)
	linesPerPage := int(usableHeight / lineHeight)
	if linesPerPage < 1 {
		linesPerPage = 1
	}
	return &aiChatPDFExporter{
		lines:        lines,
		pageSize:     geom.NewSize(aiChatPDFPageWidth, aiChatPDFPageHeight),
		linesPerPage: linesPerPage,
		topInset:     aiChatPDFVerticalInset,
		leftInset:    aiChatPDFHorizontalInset,
	}
}

func (p *aiChatPDFExporter) HasPage(pageNumber int) bool {
	start := (pageNumber - 1) * p.linesPerPage
	return start < len(p.lines)
}

func (p *aiChatPDFExporter) PageSize() geom.Size {
	return p.pageSize
}

func (p *aiChatPDFExporter) DrawPage(canvas *unison.Canvas, pageNumber int) error {
	start := (pageNumber - 1) * p.linesPerPage
	end := min(start+p.linesPerPage, len(p.lines))
	y := p.topInset
	for i := start; i < end; i++ {
		line := p.lines[i]
		line.Draw(canvas, geom.NewPoint(p.leftInset, y+line.Baseline()))
		y += line.Height()
	}
	return nil
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
	author = aiNormalizeExternalText("ui.chat.author", author)
	message = aiNormalizeExternalText("ui.chat.message", message)
	timestamp := time.Now()
	d.chatMessages = append(d.chatMessages, aiChatMessage{Author: author, Text: message, Timestamp: timestamp})
	isHuman := strings.EqualFold(author, i18n.Text("You"))
	bubbleInk := unison.Ink(aiChatAIBubble)
	textInk := unison.Ink(aiChatAIText)
	edgeInk := unison.Ink(aiChatAIBubbleEdge)
	if isHuman {
		bubbleInk = aiChatHumanBubble
		textInk = aiChatHumanText
		edgeInk = unison.ThemeSurfaceEdge
	}

	var row *unison.Panel
	var bubble *unison.Panel

	row = unison.NewPanel()
	row.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill, HGrab: true})
	row.SetBorder(unison.NewEmptyBorder(geom.Insets{Top: 2, Bottom: 2}))

	authorLabel := unison.NewLabel()
	authorLabel.SetTitle(author)
	authorLabel.Font = &unison.DynamicFont{Resolver: func() unison.FontDescriptor {
		fd := unison.DefaultLabelTheme.Font.Descriptor()
		fd.Size = max(fd.Size-1, float32(10))
		return fd
	}}
	authorLabel.OnBackgroundInk = aiChatAuthorText
	timestampLabel := unison.NewLabel()
	timestampLabel.SetTitle(timestamp.Format("3:04 PM"))
	timestampLabel.Font = &unison.DynamicFont{Resolver: func() unison.FontDescriptor {
		fd := unison.DefaultLabelTheme.Font.Descriptor()
		fd.Size = max(fd.Size-2, float32(9))
		return fd
	}}
	timestampLabel.OnBackgroundInk = aiChatAuthorText
	messageMarkdown := unison.NewMarkdown(false)
	adjustMarkdownThemeForPage(messageMarkdown, unison.DefaultLabelTheme.Font)
	messageMarkdown.OnBackgroundInk = textInk
	messageMarkdown.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill})
	messageMarkdown.SetContent(message, 0)
	messageField := unison.NewMultiLineField()
	messageField.SetText(message)
	messageField.SetWrap(true)
	messageField.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill})
	messageField.SetBorder(unison.NewEmptyBorder(geom.Insets{}))
	messageField.BackgroundInk = unison.Transparent
	messageField.EditableInk = unison.Transparent
	messageField.ErrorInk = unison.Transparent
	messageField.OnBackgroundInk = textInk
	messageField.OnEditableInk = textInk
	messageField.OnErrorInk = textInk
	messageField.RuneTypedCallback = func(_ rune) bool {
		// Keep message history read-only while still allowing selection/copy.
		return true
	}
	messageField.KeyDownCallback = func(keyCode unison.KeyCode, mod unison.Modifiers, repeat bool) bool {
		if (mod.OSMenuCmdModifierDown() || mod.ControlDown()) && (keyCode == unison.KeyC || keyCode == unison.KeyInsert) {
			if messageField.CanCopy() {
				messageField.Copy()
			}
			return true
		}
		if (mod.OSMenuCmdModifierDown() || mod.ControlDown()) && (keyCode == unison.KeyX || keyCode == unison.KeyV) {
			return true
		}
		if keyCode == unison.KeyBackspace || keyCode == unison.KeyDelete || keyCode == unison.KeyReturn || keyCode == unison.KeyNumPadEnter {
			return true
		}
		return messageField.DefaultKeyDown(keyCode, mod, repeat)
	}
	messageField.Hidden = true

	toggleSelectButton := unison.NewSVGButton(svg.Copy)
	toggleSelectButton.Hidden = false
	toggleSelectButton.Tooltip = newWrappedTooltip(i18n.Text("Select and copy this message"))
	updateToggleVisibility := func(_ bool) {
		// Keep the control always visible for quicker copy/select access.
		toggleSelectButton.Hidden = false
	}
	toggleSelectButton.ClickCallback = func() {
		selectMode := messageField.Hidden
		messageField.Hidden = !selectMode
		messageMarkdown.Hidden = selectMode
		if selectMode {
			toggleSelectButton.Tooltip = newWrappedTooltip(i18n.Text("Done selecting text"))
			messageField.RequestFocus()
		} else {
			toggleSelectButton.Tooltip = newWrappedTooltip(i18n.Text("Select and copy this message"))
		}
		updateToggleVisibility(false)
		bubble.MarkForLayoutAndRedraw()
		row.MarkForLayoutAndRedraw()
		d.historyPanel.MarkForLayoutAndRedraw()
		d.historyPanel.ValidateLayout()
		d.scroll.Sync()
		row.ScrollIntoView()
	}

	header := unison.NewPanel()
	header.SetLayout(&unison.FlowLayout{HSpacing: unison.StdHSpacing / 2, VSpacing: 0})
	header.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill})
	header.AddChild(authorLabel)
	header.AddChild(timestampLabel)
	header.AddChild(toggleSelectButton)

	bubble = unison.NewPanel()
	if isHuman {
		bubble.SetLayoutData(&unison.FlexLayoutData{HAlign: align.End})
	} else {
		bubble.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Start})
	}
	bubble.SetBorder(unison.NewEmptyBorder(geom.Insets{Top: 8, Left: 12, Bottom: 8, Right: 12}))
	bubble.AddChild(header)
	bubble.AddChild(messageMarkdown)
	bubble.AddChild(messageField)
	bubble.SetLayout(&aiChatBubbleLayout{
		dockable: d,
		message:  message,
		isHuman:  isHuman,
		header:   header,
		markdown: messageMarkdown,
		field:    messageField,
		spacing:  4,
	})
	bubble.MouseEnterCallback = func(_ geom.Point, _ unison.Modifiers) bool {
		updateToggleVisibility(true)
		bubble.MarkForLayoutAndRedraw()
		return false
	}
	bubble.MouseExitCallback = func() bool {
		updateToggleVisibility(false)
		bubble.MarkForLayoutAndRedraw()
		return false
	}
	bubble.DrawCallback = func(gc *unison.Canvas, rect geom.Rect) {
		unison.DrawRoundedRectBase(gc, rect, geom.NewUniformSize(aiChatBubbleRadius), 1, bubbleInk, edgeInk)
	}
	row.SetLayout(&aiChatMessageRowLayout{bubble: bubble, alignRight: isHuman})
	row.AddChild(bubble)
	d.historyPanel.AddChild(row)
	d.historyPanel.MarkForLayoutAndRedraw()
	d.historyPanel.ValidateLayout()
	d.scroll.Sync()
	row.ScrollIntoView()
	return row
}

func (d *aiChatDockable) aiAssistantSystemPrompt() string {
	summary := d.currentCharacterSummary()
	return strings.TrimSpace(fmt.Sprintf(`You are a GURPS Fourth Edition character builder assistant. You help design and optimize characters based on user input and GURPS 4e rules.
	You can suggest specific character-sheet updates, including advantages, disadvantages, quirks, skills, traits, equipment, and attribute changes.
Use the current character sheet state and the player's concept to propose character-sheet updates.
Always validate choices, compute point costs, and keep Tech Level constraints in mind.
The application will resolve your suggested advantages, disadvantages, quirks, skills, traits, and equipment against the local GCS library after you respond.
Do not invent database ids. Leave the "id" field empty unless you are certain.
Use canonical GURPS Fourth Edition names instead of descriptive paraphrases.
If a fixed specialization is part of the canonical library name, include it in "name". Example: "Driving (Automobile)".
If an item needs a user-defined subject, place, profession, specialty, or other nameable value, put only that value in "notes" and keep "name" focused on the base item. Example: "Area Knowledge" with notes "Mesa".
Use "description" for lore, behavior, magical effects, and special handling notes. Do not put that material in "notes".
Do not invent non-library advantages, disadvantages, skills, or equipment names.
If the concept includes a magical, signature, or supernatural item, represent it through canonical GURPS mechanics such as Signature Gear, Innate Attack, Ally, Blessed, Patron, or Striking ST, and put the lore and special behavior in "description".
Only include an equipment entry when it matches a real library item; otherwise keep the special concept on the trait side.
For attributes, use only attribute ids that already exist on the current character sheet summary below. Do not invent ids such as BX.
If the user asks for changes, propose specific character-sheet updates and a clear CP breakdown.
If no sheet is active, say so plainly; the application can create one when applying changes.

%s

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

Only include profile fields if you have determined suitable values for them based on the character concept. For height and weight, use common formats like "5'10\"" or "175 lbs". Include only the profile fields that should be updated; omit others.
If you include JSON, return exactly one top-level JSON object for the entire update.
Do not split updates across multiple JSON objects.
Do not include comments inside the JSON.
Put that JSON object first in the response.
When responding outside JSON, keep the answer concise, factual, and directly tied to GURPS 4e rules.`, summary))
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
			if def := attr.AttributeDef(); def != nil && strings.TrimSpace(def.Name) != "" {
				name = def.Name
			}
			builder.WriteString(fmt.Sprintf("%s (%s) %s", attr.AttrID, name, attr.Current().String()))
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
	defer func() {
		_ = recover()
	}()
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
			line := fmt.Sprintf("  - id=%s | name=%s | %s", skill.ID, firstNonEmptyString(skill.DisplayName, skillDisplayName(skill.Name, skill.Specialization)), skill.SourcePath)
			if len(skill.Nameables) > 0 {
				line += fmt.Sprintf(" | requires: %s", strings.Join(skill.Nameables, ", "))
			}
			builder.WriteString(line + "\n")
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
		root := item.(*gurps.Skill)
		gurps.Traverse(func(skill *gurps.Skill) bool {
			if skill.Name != "" && !skill.Container() {
				nameableKeys := make(map[string]string)
				skill.FillWithNameableKeys(nameableKeys, nil)
				skillsMap[string(skill.TID)] = aiLibraryItemRef{ID: string(skill.TID), Name: skill.Name, DisplayName: aiCatalogEntryDisplayName(skill.Name, skill.Specialization), Specialization: skill.Specialization, SourcePath: sourcePath, Nameables: aiSortedNameableKeys(nameableKeys)}
			}
			return false
		}, false, true, root)
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
		root := item.(*gurps.Trait)
		gurps.Traverse(func(trait *gurps.Trait) bool {
			if trait.Name == "" || trait.Container() {
				return false
			}
			points := trait.AdjustedPoints()
			name := trait.Name
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
				return false
			}
			if points < 0 {
				if strings.Contains(strings.ToLower(name), "quirk") || strings.Contains(strings.ToLower(strings.Join(trait.Tags, " ")), "quirk") {
					quirksMap[key] = ref
					return false
				}
				disadvantagesMap[key] = ref
			}
			return false
		}, false, true, root)
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
		root := item.(*gurps.Equipment)
		gurps.Traverse(func(eqp *gurps.Equipment) bool {
			if eqp.Name != "" && !eqp.Container() {
				equipmentMap[string(eqp.TID)] = aiLibraryItemRef{ID: string(eqp.TID), Name: eqp.Name, SourcePath: sourcePath}
			}
			return false
		}, false, true, root)
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
	if d.newSessionButton != nil {
		d.newSessionButton.SetEnabled(!thinking)
	}
	if thinking {
		d.thinkingMessage = d.addMessage("AI", i18n.Text("Thinking..."))
	} else if d.thinkingMessage != nil {
		d.historyPanel.RemoveChild(d.thinkingMessage)
		d.thinkingMessage = nil
		d.historyPanel.MarkForLayoutAndRedraw()
	}
}

func (d *aiChatDockable) handleAIResponseWithCh(responseStr string, retryCh chan<- []aiRetryItem) {
	d.addMessage("AI", responseStr)
	plan, ok := d.parseAIActionPlan(responseStr)
	if !ok {
		if strings.Contains(responseStr, "{") {
			d.addMessage("AI", i18n.Text("Structured update data was detected, but it could not be parsed into a character-sheet update."))
		}
		retryCh <- nil
		return
	}
	catalog, err := d.aiLibraryCatalog()
	if err != nil {
		d.addMessage("AI", fmt.Sprintf(i18n.Text("AI library catalog could not be prepared: %v"), err))
		retryCh <- nil
		return
	}
	resolvedPlan, retryItems, warnings := catalog.resolveAIActionPlan(plan)
	if hasAIActionPlanContent(resolvedPlan) {
		applyWarnings, applyRetryItems, applyErr := d.applyAIActionPlan(resolvedPlan)
		if applyErr != nil {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI plan could not be applied: %v"), applyErr))
			retryCh <- nil
			return
		}
		d.replaceLastAssistantHistoryWithAppliedSummary(resolvedPlan)
		warnings = append(warnings, applyWarnings...)
		retryItems = append(retryItems, applyRetryItems...)
	}
	for _, warning := range warnings {
		d.addMessage("AI", warning)
	}
	if len(retryItems) == 0 {
		d.addMessage("AI", i18n.Text("AI plan has been applied to the active character sheet."))
	}
	retryCh <- retryItems
}

func (d *aiChatDockable) applyCorrectionResponse(responseStr string, retryItems []aiRetryItem) {
	plan, ok := d.parseAIActionPlan(responseStr)
	if !ok {
		d.addMessage("AI", i18n.Text("AI corrections could not be parsed."))
		d.addMessage("AI", i18n.Text("AI plan has been applied to the active character sheet."))
		return
	}
	filteredPlan := aiFilterCorrectionPlan(plan, retryItems)
	ignoredCorrections := aiActionPlanItemCount(plan) - aiActionPlanItemCount(filteredPlan)
	if !hasAIActionPlanContent(filteredPlan) {
		if ignoredCorrections > 0 {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("Warning: ignored %d unrelated AI follow-up correction entries."), ignoredCorrections))
		}
		d.addMessage("AI", i18n.Text("Some corrected items still need exact library selection and were skipped."))
		d.addMessage("AI", i18n.Text("AI corrections have been processed."))
		return
	}
	catalog, err := d.aiLibraryCatalog()
	if err != nil {
		d.addMessage("AI", fmt.Sprintf(i18n.Text("AI library catalog could not be prepared for corrections: %v"), err))
		return
	}
	resolvedPlan, remainingRetryItems, warnings := catalog.resolveAIActionPlan(filteredPlan)
	if ignoredCorrections > 0 {
		warnings = append(warnings, fmt.Sprintf(i18n.Text("Warning: ignored %d unrelated AI follow-up correction entries."), ignoredCorrections))
	}
	corrections := aiCollectResolvedCorrections(resolvedPlan, retryItems)
	if hasAIActionPlanContent(resolvedPlan) {
		applyWarnings, _, applyErr := d.applyAIActionPlan(resolvedPlan)
		warnings = append(warnings, applyWarnings...)
		if applyErr != nil {
			d.addMessage("AI", fmt.Sprintf(i18n.Text("AI correction could not be applied: %v"), applyErr))
			return
		}
		if len(corrections) != 0 {
			aiLogResolvedCorrections(corrections)
			d.addMessage("AI", aiBuildCorrectionSummary(corrections))
		}
	}
	for _, warning := range warnings {
		d.addMessage("AI", warning)
	}
	if len(remainingRetryItems) != 0 {
		d.addMessage("AI", i18n.Text("Some corrected items still need exact library selection and were skipped."))
	}
	d.addMessage("AI", i18n.Text("AI corrections have been applied to the active character sheet."))
}

type aiActionPlan struct {
	Profile       *aiProfileAction    `json:"profile,omitempty"`
	Attributes    []aiAttributeAction `json:"attributes,omitempty"`
	Advantages    []aiNamedAction     `json:"advantages,omitempty"`
	Disadvantages []aiNamedAction     `json:"disadvantages,omitempty"`
	Quirks        []aiNamedAction     `json:"quirks,omitempty"`
	Skills        []aiSkillAction     `json:"skills,omitempty"`
	Spells        []aiSkillAction     `json:"spells,omitempty"`
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

func aiNormalizedJSONKey(key string) string {
	trimmed := strings.TrimSpace(strings.ToLower(key))
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range trimmed {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	normalized := strings.Trim(builder.String(), "_")
	switch normalized {
	case "tl":
		return "tech_level"
	case "cp_limit", "cplimit":
		return "cp_limit"
	case "eye_color", "eyecolor", "eyes":
		return "eye_color"
	case "hair_color", "haircolor":
		return "hair_color"
	case "game_setting", "world", "setting":
		return "world_setting"
	case "world_setting", "game_world_setting":
		return "world_setting"
	case "draftprofile":
		return "draft_profile"
	case "characterprofile":
		return "character_profile"
	case "charactersheet":
		return "character_sheet"
	default:
		return normalized
	}
}

func aiNormalizeJSONObjectKeys(value any) any {
	return aiNormalizeJSONObjectKeysForParent(value, "")
}

func aiNormalizeJSONObjectKeysForParent(value any, parentKey string) any {
	switch typed := value.(type) {
	case map[string]any:
		preserveKeys := parentKey == "attributes" || parentKey == "skills" || parentKey == "spells"
		normalized := make(map[string]any, len(typed))
		for key, val := range typed {
			normalizedKey := key
			if !preserveKeys {
				normalizedKey = aiNormalizedJSONKey(key)
			}
			normalized[normalizedKey] = aiNormalizeJSONObjectKeysForParent(val, normalizedKey)
		}
		return normalized
	case []any:
		for i, item := range typed {
			typed[i] = aiNormalizeJSONObjectKeysForParent(item, parentKey)
		}
		return typed
	default:
		return value
	}
}

func aiMarshalNormalizedJSONPayload(payload string) ([]byte, bool) {
	var raw any
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return nil, false
	}
	normalized := aiNormalizeJSONObjectKeys(raw)
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, false
	}
	return data, true
}

func aiDraftProfileHasMeaningfulData(profile aiDraftProfile) bool {
	profile = aiNormalizeDraftProfile(profile)
	return strings.TrimSpace(profile.CharacterConcept.String()) != "" ||
		strings.TrimSpace(profile.Name.String()) != "" ||
		strings.TrimSpace(profile.Title.String()) != "" ||
		strings.TrimSpace(profile.Age.String()) != "" ||
		strings.TrimSpace(profile.Weight.String()) != "" ||
		strings.TrimSpace(profile.Height.String()) != "" ||
		strings.TrimSpace(profile.EyeColor.String()) != "" ||
		strings.TrimSpace(profile.HairColor.String()) != "" ||
		strings.TrimSpace(profile.Size.String()) != "" ||
		strings.TrimSpace(profile.Religion.String()) != "" ||
		strings.TrimSpace(profile.TechLevel.String()) != "" ||
		strings.TrimSpace(profile.CPLimit.String()) != "" ||
		strings.TrimSpace(profile.StartingWealth.String()) != "" ||
		strings.TrimSpace(profile.GameLimitations.String()) != "" ||
		strings.TrimSpace(profile.WorldSetting.String()) != ""
}

func aiDraftProfileReadyForApproval(status string, profile aiDraftProfile) bool {
	profile = aiNormalizeDraftProfile(profile)
	if strings.EqualFold(strings.TrimSpace(status), "complete") {
		return strings.TrimSpace(profile.CharacterConcept.String()) != ""
	}
	return false
}

func aiStringFromValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func aiMapStringAny(value any) map[string]any {
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

func aiSliceAny(value any) []any {
	slice, ok := value.([]any)
	if !ok {
		return nil
	}
	return slice
}

func aiProfileActionFromMap(raw map[string]any) *aiProfileAction {
	if raw == nil {
		return nil
	}
	profile := &aiProfileAction{
		Name:         aiFlexibleString(aiStringFromValue(raw["name"])),
		Title:        aiFlexibleString(aiStringFromValue(raw["title"])),
		Organization: aiFlexibleString(aiStringFromValue(raw["organization"])),
		Religion:     aiFlexibleString(aiStringFromValue(raw["religion"])),
		TechLevel:    aiFlexibleString(firstNonEmptyString(aiStringFromValue(raw["tech_level"]), aiStringFromValue(raw["tl"]))),
		Gender:       aiFlexibleString(aiStringFromValue(raw["gender"])),
		Age:          aiFlexibleString(aiStringFromValue(raw["age"])),
		Birthday:     aiFlexibleString(aiStringFromValue(raw["birthday"])),
		Eyes:         aiFlexibleString(firstNonEmptyString(aiStringFromValue(raw["eyes"]), aiStringFromValue(raw["eye_color"]))),
		Hair:         aiFlexibleString(firstNonEmptyString(aiStringFromValue(raw["hair"]), aiStringFromValue(raw["hair_color"]))),
		Skin:         aiFlexibleString(aiStringFromValue(raw["skin"])),
		Handedness:   aiFlexibleString(aiStringFromValue(raw["handedness"])),
		Height:       aiFlexibleString(aiStringFromValue(raw["height"])),
		Weight:       aiFlexibleString(aiStringFromValue(raw["weight"])),
	}
	if !hasAIActionPlanContent(aiActionPlan{Profile: profile}) {
		return nil
	}
	return profile
}

func aiNamedActionsFromArray(value any) []aiNamedAction {
	items := aiSliceAny(value)
	if len(items) == 0 {
		return nil
	}
	actions := make([]aiNamedAction, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			name := strings.TrimSpace(typed)
			if name != "" {
				actions = append(actions, aiNamedAction{Name: aiFlexibleString(name)})
			}
		case map[string]any:
			name := firstNonEmptyString(aiStringFromValue(typed["name"]), aiStringFromValue(typed["title"]))
			if name == "" {
				continue
			}
			actions = append(actions, aiNamedAction{
				ID:          aiFlexibleString(aiStringFromValue(typed["id"])),
				Name:        aiFlexibleString(name),
				Notes:       aiFlexibleString(aiStringFromValue(typed["notes"])),
				Description: aiFlexibleString(aiStringFromValue(typed["description"])),
				Points:      aiFlexibleString(aiStringFromValue(typed["points"])),
				Quantity:    aiFlexibleInt(aiParseLoosePositiveInt(aiStringFromValue(typed["quantity"]))),
			})
		}
	}
	return actions
}

func aiSkillActionsFromValue(value any) []aiSkillAction {
	if rawMap := aiMapStringAny(value); rawMap != nil {
		actions := make([]aiSkillAction, 0, len(rawMap))
		for name, level := range rawMap {
			trimmedName := strings.TrimSpace(name)
			if trimmedName == "" {
				continue
			}
			actions = append(actions, aiSkillAction{Name: aiFlexibleString(trimmedName), Level: aiFlexibleString(aiStringFromValue(level))})
		}
		return actions
	}
	items := aiSliceAny(value)
	if len(items) == 0 {
		return nil
	}
	actions := make([]aiSkillAction, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			name := strings.TrimSpace(typed)
			if name != "" {
				actions = append(actions, aiSkillAction{Name: aiFlexibleString(name)})
			}
		case map[string]any:
			name := firstNonEmptyString(aiStringFromValue(typed["name"]), aiStringFromValue(typed["title"]))
			if name == "" {
				continue
			}
			actions = append(actions, aiSkillAction{
				ID:          aiFlexibleString(aiStringFromValue(typed["id"])),
				Name:        aiFlexibleString(name),
				Notes:       aiFlexibleString(aiStringFromValue(typed["notes"])),
				Description: aiFlexibleString(aiStringFromValue(typed["description"])),
				Points:      aiFlexibleString(aiStringFromValue(typed["points"])),
				Value:       aiFlexibleString(aiStringFromValue(typed["value"])),
				Level:       aiFlexibleString(aiStringFromValue(typed["level"])),
			})
		}
	}
	return actions
}

func aiAttributeActionsFromValue(value any) []aiAttributeAction {
	if rawMap := aiMapStringAny(value); rawMap != nil {
		actions := make([]aiAttributeAction, 0, len(rawMap))
		for name, attrValue := range rawMap {
			trimmedName := strings.TrimSpace(name)
			if trimmedName == "" {
				continue
			}
			actions = append(actions, aiAttributeAction{ID: aiFlexibleString(trimmedName), Value: aiFlexibleString(aiStringFromValue(attrValue))})
		}
		return actions
	}
	items := aiSliceAny(value)
	if len(items) == 0 {
		return nil
	}
	actions := make([]aiAttributeAction, 0, len(items))
	for _, item := range items {
		if typed := aiMapStringAny(item); typed != nil {
			actions = append(actions, aiAttributeAction{
				ID:    aiFlexibleString(aiStringFromValue(typed["id"])),
				Name:  aiFlexibleString(aiStringFromValue(typed["name"])),
				Value: aiFlexibleString(aiStringFromValue(typed["value"])),
			})
		}
	}
	return actions
}

func aiActionPlanFromWrappedSection(raw map[string]any) aiActionPlan {
	if raw == nil {
		return aiActionPlan{}
	}
	plan := aiActionPlan{
		Profile:       aiProfileActionFromMap(raw),
		Attributes:    aiAttributeActionsFromValue(raw["attributes"]),
		Advantages:    aiNamedActionsFromArray(raw["advantages"]),
		Disadvantages: aiNamedActionsFromArray(raw["disadvantages"]),
		Quirks:        aiNamedActionsFromArray(raw["quirks"]),
		Skills:        aiSkillActionsFromValue(raw["skills"]),
		Spells:        aiSkillActionsFromValue(raw["spells"]),
		Equipment:     aiNamedActionsFromArray(raw["equipment"]),
	}
	if spend, ok := raw["spend_all_cp"].(bool); ok {
		plan.SpendAllCP = spend
	}
	return plan
}

func aiActionPlanFromWrapperMap(raw map[string]any) (aiActionPlan, bool) {
	var plan aiActionPlan
	found := false
	for _, key := range []string{"character_sheet", "character_profile"} {
		if section := aiMapStringAny(raw[key]); section != nil {
			mergeAIActionPlan(&plan, aiActionPlanFromWrappedSection(section))
			found = true
		}
	}
	if section := aiMapStringAny(raw["draft_profile"]); section != nil && !found {
		plan.Profile = aiProfileActionFromMap(section)
		found = plan.Profile != nil
	}
	if !found || !hasAIActionPlanContent(plan) {
		return aiActionPlan{}, false
	}
	return plan, true
}

type aiLibraryItemRef struct {
	ID             string
	Name           string
	DisplayName    string
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
	ID          aiFlexibleString `json:"id,omitempty"`
	Name        aiFlexibleString `json:"name"`
	Notes       aiFlexibleString `json:"notes,omitempty"`
	Description aiFlexibleString `json:"description,omitempty"`
	Points      aiFlexibleString `json:"points,omitempty"`
	Quantity    aiFlexibleInt    `json:"quantity,omitempty"`
}

type aiSkillAction struct {
	ID          aiFlexibleString `json:"id,omitempty"`
	Name        aiFlexibleString `json:"name"`
	Notes       aiFlexibleString `json:"notes,omitempty"`
	Description aiFlexibleString `json:"description,omitempty"`
	Points      aiFlexibleString `json:"points,omitempty"`
	Value       aiFlexibleString `json:"value,omitempty"`
	Level       aiFlexibleString `json:"level,omitempty"`
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
		if normalized, ok := aiMarshalNormalizedJSONPayload(cleaned); ok {
			if err := json.Unmarshal(normalized, &plan); err == nil && hasAIActionPlanContent(plan) {
				mergeAIActionPlan(&merged, plan)
				found = true
				continue
			}
			var raw map[string]any
			if err := json.Unmarshal(normalized, &raw); err == nil {
				if wrappedPlan, ok := aiActionPlanFromWrapperMap(raw); ok {
					mergeAIActionPlan(&merged, wrappedPlan)
					found = true
				}
			}
			continue
		}
		if err := json.Unmarshal([]byte(cleaned), &plan); err != nil || !hasAIActionPlanContent(plan) {
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
		len(plan.Quirks) != 0 || len(plan.Skills) != 0 || len(plan.Spells) != 0 || len(plan.Equipment) != 0 || plan.SpendAllCP
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
	dst.Spells = append(dst.Spells, src.Spells...)
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

func (d *aiChatDockable) applyAIActionPlan(plan aiActionPlan) ([]string, []aiRetryItem, error) {
	sheet := d.sheetOrCreateNew()
	if sheet == nil || sheet.entity == nil {
		return nil, nil, fmt.Errorf("no active sheet to apply changes to")
	}
	entity := sheet.entity
	warnings := make([]string, 0)
	retryItems := make([]aiRetryItem, 0)

	for _, attr := range plan.Attributes {
		if err := applyAttributeAction(entity, attr); err != nil {
			warnings = append(warnings, fmt.Sprintf("Warning: could not apply attribute action: %v", err))
			continue
		}
	}

	if plan.Profile != nil {
		applyProfileAction(entity, plan.Profile)
		UpdateTitleForDockable(sheet)
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
			expandedActions, splitWarning := splitCombinedAISkillActions(action)
			if splitWarning != "" {
				warnings = append(warnings, splitWarning)
			}
			for _, expanded := range expandedActions {
				var warning string
				var retryItem *aiRetryItem
				var err error
				skills, warning, retryItem, err = d.addOrUpdateSkill(entity, skills, expanded)
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("Warning: could not apply skill action: %v", err))
					continue
				}
				if warning != "" {
					warnings = append(warnings, warning)
				}
				if retryItem != nil {
					retryItems = append(retryItems, *retryItem)
				}
			}
		}
		entity.SetSkillList(skills)
	}

	if len(plan.Spells) > 0 {
		spells := entity.Spells
		for _, action := range plan.Spells {
			var warning string
			var retryItem *aiRetryItem
			var err error
			spells, warning, retryItem, err = d.addOrUpdateSpell(entity, spells, action)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("Warning: could not apply spell action: %v", err))
				continue
			}
			if warning != "" {
				warnings = append(warnings, warning)
			}
			if retryItem != nil {
				retryItems = append(retryItems, *retryItem)
			}
		}
		entity.SetSpellList(spells)
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
	return warnings, retryItems, nil
}

func normalizeAIAttributeIdentifier(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	key := strings.ToLower(trimmed)
	replacer := strings.NewReplacer(" ", "", "-", "", "_", "", "/", "", "'", "", "\"", "")
	key = replacer.Replace(key)
	aliases := map[string]string{
		"st":            "ST",
		"strength":      "ST",
		"dx":            "DX",
		"dexterity":     "DX",
		"iq":            "IQ",
		"intelligence":  "IQ",
		"ht":            "HT",
		"health":        "HT",
		"will":          "Will",
		"willpower":     "Will",
		"wp":            "Will",
		"per":           "Per",
		"perception":    "Per",
		"perc":          "Per",
		"hp":            "HP",
		"hitpoint":      "HP",
		"hitpoints":     "HP",
		"fp":            "FP",
		"fatiguepoint":  "FP",
		"fatiguepoints": "FP",
		"bs":            "Basic Speed",
		"basicspeed":    "Basic Speed",
		"speed":         "Basic Speed",
		"bx":            "Basic Speed",
		"bm":            "Basic Move",
		"basicmove":     "Basic Move",
		"move":          "Basic Move",
		"bl":            "Basic Lift",
		"basiclift":     "Basic Lift",
		"lift":          "Basic Lift",
	}
	if canonical, ok := aliases[key]; ok {
		return canonical
	}
	return trimmed
}

func applyAttributeAction(entity *gurps.Entity, action aiAttributeAction) error {
	name := normalizeAIAttributeIdentifier(action.Name.String())
	id := normalizeAIAttributeIdentifier(action.ID.String())
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
		if id != "" && (strings.EqualFold(candidate.AttrID, id) || candidate.NameMatches(id)) {
			attr = candidate
			break
		}
		if name != "" && (strings.EqualFold(candidate.AttrID, name) || candidate.NameMatches(name)) {
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
			var match *gurps.Trait
			gurps.Traverse(func(trait *gurps.Trait) bool {
				if trait.Container() {
					return false
				}
				if string(trait.TID) == idStr {
					trait.SetDataOwner(nil)
					match = trait
					return true
				}
				return false
			}, false, true, traits...)
			if match != nil {
				return match, libraryFileForSet(set.Name, ref.FilePath), nil
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
			var match *gurps.Trait
			gurps.Traverse(func(trait *gurps.Trait) bool {
				if trait.Container() {
					return false
				}
				libBase := traitBaseNameForLookup(trait.Name)
				if strings.EqualFold(trait.Name, name) ||
					strings.EqualFold(libBase, name) ||
					(searchBase != "" && (strings.EqualFold(trait.Name, searchBase) || strings.EqualFold(libBase, searchBase))) {
					trait.SetDataOwner(nil)
					match = trait
					return true
				}
				return false
			}, false, true, traits...)
			if match != nil {
				return match, libraryFileForSet(set.Name, ref.FilePath), nil
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
			var match *gurps.Skill
			gurps.Traverse(func(skill *gurps.Skill) bool {
				if skill.Container() {
					return false
				}
				if string(skill.TID) == idStr {
					skill.SetDataOwner(nil)
					match = skill
					return true
				}
				return false
			}, false, true, skills...)
			if match != nil {
				return match, libraryFileForSet(set.Name, ref.FilePath), nil
			}
		}
	}
	return nil, gurps.LibraryFile{}, nil
}

func (d *aiChatDockable) findLibrarySpellByID(idStr string) (*gurps.Spell, gurps.LibraryFile, error) {
	idStr = normalizeAISelectionID(idStr)
	if idStr == "" {
		return nil, gurps.LibraryFile{}, nil
	}
	for _, set := range scanNamedFileSetsWithFallback(gurps.GlobalSettings().Libraries(), gurps.SpellsExt) {
		for _, ref := range set.List {
			spells, err := gurps.NewSpellsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, spell := range spells {
				if spell.Container() {
					continue
				}
				if string(spell.TID) == idStr {
					spell.SetDataOwner(nil)
					return spell, libraryFileForSet(set.Name, ref.FilePath), nil
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

func applyNameablesToClonedSpell(cloned *gurps.Spell, aiName, aiNotes string) {
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

func applyAIItemDescriptionToTrait(trait *gurps.Trait, description string) {
	if trait == nil {
		return
	}
	if description = strings.TrimSpace(description); description != "" {
		trait.UserDesc = description
	}
}

func applyAIItemDescriptionToSkill(skill *gurps.Skill, description string) {
	if skill == nil {
		return
	}
	if description = strings.TrimSpace(description); description != "" {
		skill.LocalNotes = description
	}
}

func applyAIItemDescriptionToSpell(spell *gurps.Spell, description string) {
	if spell == nil {
		return
	}
	if description = strings.TrimSpace(description); description != "" {
		spell.LocalNotes = description
	}
}

func applyAIItemDescriptionToEquipment(equipment *gurps.Equipment, description string) {
	if equipment == nil {
		return
	}
	if description = strings.TrimSpace(description); description != "" {
		equipment.LocalNotes = description
	}
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
	}
	// Strip wrapping punctuation but preserve meaningful parentheses in names,
	// e.g. "Code of Honor (Deadite Hunter)".
	return strings.Trim(value, "\"'<>[]{}")
}

func normalizeAISkillName(raw string) string {
	value := strings.Trim(strings.TrimSpace(raw), "\"'")
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "|"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	if idx := strings.Index(value, " - "); idx > 0 {
		value = strings.TrimSpace(value[:idx])
	}
	base, specialization := splitSkillNameAndSpecialization(value)
	if canonicalBase := canonicalizeAISkillBaseName(base); canonicalBase != "" && !strings.EqualFold(canonicalBase, base) {
		if strings.TrimSpace(specialization) != "" {
			return skillDisplayName(canonicalBase, specialization)
		}
		return canonicalBase
	}
	return value
}

func canonicalizeAISkillBaseName(base string) string {
	trimmed := strings.TrimSpace(base)
	switch normalizeLookupText(trimmed) {
	case "mechanics":
		return "Mechanic"
	default:
		return trimmed
	}
}

func normalizeAINamedItemName(raw string) string {
	value := strings.Trim(strings.TrimSpace(raw), "\"'")
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "|"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	if idx := strings.Index(value, " - "); idx > 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return value
}

func traitAliasName(name string) string {
	key := normalizeLookupText(name)
	switch key {
	case "combateffective", "combateffectivemilitary":
		return "Combat Reflexes"
	default:
		return ""
	}
}

func traitNameLookupCandidates(raw string) []string {
	trimmed := normalizeAINamedItemName(raw)
	if trimmed == "" {
		return nil
	}
	seen := make(map[string]struct{}, 6)
	add := func(value string, out *[]string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		*out = append(*out, value)
	}
	results := make([]string, 0, 6)
	add(trimmed, &results)
	base, _ := splitSkillNameAndSpecialization(trimmed)
	add(base, &results)
	lower := strings.ToLower(trimmed)
	if idx := strings.Index(lower, " with "); idx > 0 {
		add(trimmed[:idx], &results)
	}
	if idx := strings.Index(trimmed, ":"); idx > 0 {
		add(trimmed[:idx], &results)
	}
	if alias := traitAliasName(trimmed); alias != "" {
		add(alias, &results)
	}
	trimmedLower := strings.ToLower(trimmed)
	if strings.HasSuffix(trimmedLower, "s") {
		add(strings.TrimSpace(trimmed[:len(trimmed)-1]), &results)
	} else {
		add(trimmed+"s", &results)
	}
	return results
}

func splitCombinedAISkillActions(action aiSkillAction) ([]aiSkillAction, string) {
	name := normalizeAISkillName(action.Name.String())
	if name == "" {
		name = normalizeAISkillName(action.ID.String())
	}
	base, spec := splitSkillNameAndSpecialization(name)
	if base == "" || spec == "" || !strings.Contains(spec, ",") {
		return []aiSkillAction{action}, ""
	}
	parts := strings.Split(spec, ",")
	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cleanParts = append(cleanParts, part)
	}
	if len(cleanParts) < 2 {
		return []aiSkillAction{action}, ""
	}
	pointsText := strings.TrimSpace(action.Points.String())
	pointsPerSkill := pointsText
	if pointsText != "" {
		if total, err := strconv.Atoi(pointsText); err == nil && total%len(cleanParts) == 0 {
			pointsPerSkill = strconv.Itoa(total / len(cleanParts))
		}
	}
	result := make([]aiSkillAction, 0, len(cleanParts))
	for _, part := range cleanParts {
		expanded := action
		expanded.Name = aiFlexibleString(skillDisplayName(base, part))
		expanded.ID = ""
		if pointsPerSkill != "" {
			expanded.Points = aiFlexibleString(pointsPerSkill)
		}
		result = append(result, expanded)
	}
	return result, fmt.Sprintf("Warning: combined skill %q was split into separate skills: %s.", name, strings.Join(cleanParts, ", "))
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
	for _, set := range scanNamedFileSetsWithFallback(gurps.GlobalSettings().Libraries(), gurps.SkillsExt) {
		for _, ref := range set.List {
			skills, err := gurps.NewSkillsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			var match *gurps.Skill
			gurps.Traverse(func(skill *gurps.Skill) bool {
				if skill.Container() {
					return false
				}
				displayName := skillDisplayName(skill.Name, skill.Specialization)
				candidateBaseNorm := normalizeLookupText(skill.Name)
				candidateSpecNorm := normalizeLookupText(skill.Specialization)
				if strings.EqualFold(displayName, name) || strings.EqualFold(skill.Name, name) {
					skill.SetDataOwner(nil)
					match = skill
					return true
				}
				if requestedSpecNorm != "" && candidateBaseNorm == requestedBaseNorm && candidateSpecNorm == requestedSpecNorm {
					skill.SetDataOwner(nil)
					match = skill
					return true
				}
				candidateNorm := normalizeLookupText(displayName)
				if requestedNorm != "" && candidateNorm == requestedNorm {
					skill.SetDataOwner(nil)
					match = skill
					return true
				}
				return false
			}, false, true, skills...)
			if match != nil {
				return match, libraryFileForSet(set.Name, ref.FilePath), nil
			}
		}
	}
	return nil, gurps.LibraryFile{}, nil
}

func (d *aiChatDockable) findLibrarySpellByName(name string) (*gurps.Spell, gurps.LibraryFile, error) {
	requestedNorm := normalizeLookupText(name)
	for _, set := range scanNamedFileSetsWithFallback(gurps.GlobalSettings().Libraries(), gurps.SpellsExt) {
		for _, ref := range set.List {
			spells, err := gurps.NewSpellsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, spell := range spells {
				if spell.Container() {
					continue
				}
				displayName := strings.TrimSpace(spell.String())
				if strings.EqualFold(displayName, name) || strings.EqualFold(spell.Name, name) {
					spell.SetDataOwner(nil)
					return spell, libraryFileForSet(set.Name, ref.FilePath), nil
				}
				if requestedNorm != "" {
					if normalizeLookupText(displayName) == requestedNorm || normalizeLookupText(spell.Name) == requestedNorm {
						spell.SetDataOwner(nil)
						return spell, libraryFileForSet(set.Name, ref.FilePath), nil
					}
				}
			}
		}
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

func (d *aiChatDockable) spellLookupDebugDetails(name, idStr string) string {
	requestedNorm := normalizeLookupText(name)
	if requestedNorm == "" {
		requestedNorm = "(empty)"
	}
	similar := make([]string, 0, 5)
	seen := make(map[string]struct{})
	total := 0
	for _, set := range scanNamedFileSetsWithFallback(gurps.GlobalSettings().Libraries(), gurps.SpellsExt) {
		for _, ref := range set.List {
			spells, err := gurps.NewSpellsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, spell := range spells {
				if spell.Container() {
					continue
				}
				total++
				if len(similar) >= 5 {
					continue
				}
				displayName := strings.TrimSpace(spell.String())
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
		return fmt.Sprintf("spell_lookup_debug={id:%q normalized:%q scanned:%d similar:none}", idStr, requestedNorm, total)
	}
	return fmt.Sprintf("spell_lookup_debug={id:%q normalized:%q scanned:%d similar:%q}", idStr, requestedNorm, total, strings.Join(similar, ", "))
}

func (d *aiChatDockable) findSimilarLibrarySkillNames(name string) []string {
	requestedNorm := normalizeLookupText(name)
	if requestedNorm == "" {
		return nil
	}
	similar := make([]string, 0, 5)
	seen := make(map[string]struct{})
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
				if len(similar) >= 5 {
					break
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
	return similar
}

func (d *aiChatDockable) findSimilarLibrarySpellNames(name string) []string {
	requestedNorm := normalizeLookupText(name)
	if requestedNorm == "" {
		return nil
	}
	similar := make([]string, 0, 5)
	seen := make(map[string]struct{})
	for _, set := range scanNamedFileSetsWithFallback(gurps.GlobalSettings().Libraries(), gurps.SpellsExt) {
		for _, ref := range set.List {
			spells, err := gurps.NewSpellsFromFile(ref.FileSystem, ref.FilePath)
			if err != nil {
				continue
			}
			for _, spell := range spells {
				if spell.Container() {
					continue
				}
				if len(similar) >= 5 {
					break
				}
				displayName := strings.TrimSpace(spell.String())
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
	return similar
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
			var match *gurps.Equipment
			gurps.Traverse(func(eqp *gurps.Equipment) bool {
				if eqp.Container() {
					return false
				}
				if strings.EqualFold(eqp.Name, name) {
					eqp.SetDataOwner(nil)
					match = eqp
					return true
				}
				return false
			}, false, true, equipment...)
			if match != nil {
				return match, libraryFileForSet(set.Name, ref.FilePath), nil
			}
		}
	}
	return nil, gurps.LibraryFile{}, nil
}

func (d *aiChatDockable) addOrUpdateTrait(entity *gurps.Entity, traits []*gurps.Trait, action aiNamedAction) ([]*gurps.Trait, string, error) {
	rawName := action.Name.String()
	name := normalizeAINamedItemName(rawName)
	idStr := normalizeAISelectionID(action.ID.String())
	useTIDLookup := idStr != "" && tid.IsValid(tid.TID(idStr))
	if name == "" && idStr != "" && !useTIDLookup {
		// Some models place the trait name in "id" instead of "name".
		name = normalizeAINamedItemName(idStr)
	}
	if name == "" && !useTIDLookup {
		return traits, "", fmt.Errorf("trait action is missing a name or valid id")
	}
	if name != "" {
		if existing := d.findExistingTrait(entity, name); existing != nil {
			if pointsText := strings.TrimSpace(action.Points.String()); pointsText != "" {
				if points, err := fxp.FromString(pointsText); err == nil {
					existing.BasePoints = points
				}
			}
			applyAIItemDescriptionToTrait(existing, action.Description.String())
			return traits, "", nil
		}
	}
	if useTIDLookup {
		if existing := d.findExistingTraitByID(entity, idStr, name); existing != nil {
			if pointsText := strings.TrimSpace(action.Points.String()); pointsText != "" {
				if points, err := fxp.FromString(pointsText); err == nil {
					existing.BasePoints = points
				}
			}
			applyAIItemDescriptionToTrait(existing, action.Description.String())
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

	// Fall back to name lookup if ID lookup didn't find anything.
	if libraryTrait == nil {
		for _, candidate := range traitNameLookupCandidates(name) {
			libraryTrait, libFile, err = d.findLibraryTraitByName(candidate)
			if err != nil {
				return traits, "", err
			}
			if libraryTrait != nil {
				name = candidate
				break
			}
		}
	}

	// Also try the id string itself as a library name — the AI often puts the
	// library name in the "id" field rather than the actual TID.
	if libraryTrait == nil && idStr != "" && !strings.EqualFold(idStr, name) {
		for _, candidate := range traitNameLookupCandidates(idStr) {
			libraryTrait, libFile, err = d.findLibraryTraitByName(candidate)
			if err != nil {
				return traits, "", err
			}
			if libraryTrait != nil {
				name = candidate
				break
			}
		}
	}

	if libraryTrait == nil {
		return traits, fmt.Sprintf("%sWarning: trait %q was not found in the library and was skipped. Advantages, disadvantages and quirks must come from library entries.", warningPrefix, name), nil
	}
	cloned := libraryTrait.Clone(libFile, entity, nil, false)
	applyNameablesToClonedTrait(cloned, rawName, action.Notes.String())
	applyAIItemDescriptionToTrait(cloned, action.Description.String())
	if pointsText := strings.TrimSpace(action.Points.String()); pointsText != "" {
		if points, err := fxp.FromString(pointsText); err == nil {
			cloned.BasePoints = points
		}
	}
	return append(traits, cloned), "", nil
}

func (d *aiChatDockable) addOrUpdateSkill(entity *gurps.Entity, skills []*gurps.Skill, action aiSkillAction) ([]*gurps.Skill, string, *aiRetryItem, error) {
	name := normalizeAISkillName(action.Name.String())
	idStr := normalizeAISelectionID(action.ID.String())
	useTIDLookup := idStr != "" && tid.IsValid(tid.TID(idStr))
	if name == "" && idStr != "" && !useTIDLookup {
		// Some models place the skill name in "id" instead of "name".
		name = normalizeAISkillName(idStr)
	}
	if name == "" && !useTIDLookup {
		return skills, "", nil, fmt.Errorf("skill action is missing a name or valid id")
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
	if name != "" {
		if existing := findExistingSkillInList(skills, name); existing != nil {
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
			applyAIItemDescriptionToSkill(existing, action.Description.String())
			return skills, "", nil, nil
		}
	}
	if useTIDLookup {
		if existing := findExistingSkillByIDInList(skills, idStr, name); existing != nil {
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
			applyAIItemDescriptionToSkill(existing, action.Description.String())
			return skills, "", nil, nil
		}
	}
	var librarySkill *gurps.Skill
	var libFile gurps.LibraryFile
	var err error

	// Try ID lookup first if ID is provided
	if useTIDLookup {
		librarySkill, libFile, err = d.findLibrarySkillByID(idStr)
		if err != nil {
			return skills, "", nil, err
		}
	}

	// Fall back to name lookup if ID lookup didn't find anything
	if librarySkill == nil && name != "" {
		librarySkill, libFile, err = d.findLibrarySkillByName(name)
		if err != nil {
			return skills, "", nil, err
		}
	}

	// Also try the id string itself as a skill name — the AI often puts the
	// library name in the "id" field rather than the actual TID.
	if librarySkill == nil && idStr != "" && !strings.EqualFold(idStr, name) && !useTIDLookup {
		librarySkill, libFile, err = d.findLibrarySkillByName(idStr)
		if err != nil {
			return skills, "", nil, err
		}
	}

	if librarySkill == nil {
		similar := d.findSimilarLibrarySkillNames(name)
		retryItem := &aiRetryItem{Category: "skill", Name: name, Similar: similar}
		details := d.skillLookupDebugDetails(name, idStr)
		return skills, fmt.Sprintf("%sWarning: skill %q was not found in the library and was skipped. Skills must be chosen from available database entries. %s", warningPrefix, name, details), retryItem, nil
	}
	cloned := librarySkill.Clone(libFile, entity, nil, false)
	applyNameablesToClonedSkill(cloned, name, action.Notes.String())
	applyAIItemDescriptionToSkill(cloned, action.Description.String())
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
	return append(skills, cloned), "", nil, nil
}

func (d *aiChatDockable) addOrUpdateSpell(entity *gurps.Entity, spells []*gurps.Spell, action aiSkillAction) ([]*gurps.Spell, string, *aiRetryItem, error) {
	name := normalizeAINamedItemName(action.Name.String())
	idStr := normalizeAISelectionID(action.ID.String())
	useTIDLookup := idStr != "" && tid.IsValid(tid.TID(idStr))
	if name == "" && idStr != "" && !useTIDLookup {
		name = normalizeAINamedItemName(idStr)
	}
	if name == "" && !useTIDLookup {
		return spells, "", nil, fmt.Errorf("spell action is missing a name or valid id")
	}
	pointsText := strings.TrimSpace(action.Points.String())
	if pointsText == "" {
		pointsText = strings.TrimSpace(action.Value.String())
	}
	warningPrefix := ""
	if idStr != "" && !useTIDLookup {
		warningPrefix = fmt.Sprintf("Warning: spell %q provided invalid id %q; falling back to name lookup. ", name, idStr)
	}
	if name != "" {
		if existing := findExistingSpellInList(spells, name); existing != nil {
			if pointsText != "" {
				if points, err := fxp.FromString(pointsText); err == nil {
					existing.SetRawPoints(points)
				}
			}
			applyAIItemDescriptionToSpell(existing, action.Description.String())
			return spells, "", nil, nil
		}
	}
	if useTIDLookup {
		if existing := findExistingSpellByIDInList(spells, idStr, name); existing != nil {
			if pointsText != "" {
				if points, err := fxp.FromString(pointsText); err == nil {
					existing.SetRawPoints(points)
				}
			}
			applyAIItemDescriptionToSpell(existing, action.Description.String())
			return spells, "", nil, nil
		}
	}
	var librarySpell *gurps.Spell
	var libFile gurps.LibraryFile
	var err error

	if useTIDLookup {
		librarySpell, libFile, err = d.findLibrarySpellByID(idStr)
		if err != nil {
			return spells, "", nil, err
		}
	}
	if librarySpell == nil && name != "" {
		librarySpell, libFile, err = d.findLibrarySpellByName(name)
		if err != nil {
			return spells, "", nil, err
		}
	}
	if librarySpell == nil && idStr != "" && !strings.EqualFold(idStr, name) && !useTIDLookup {
		librarySpell, libFile, err = d.findLibrarySpellByName(idStr)
		if err != nil {
			return spells, "", nil, err
		}
	}
	if librarySpell == nil {
		similar := d.findSimilarLibrarySpellNames(name)
		retryItem := &aiRetryItem{Category: "spell", Name: name, Similar: similar}
		details := d.spellLookupDebugDetails(name, idStr)
		return spells, fmt.Sprintf("%sWarning: spell %q was not found in the library and was skipped. Spells must be chosen from available database entries. %s", warningPrefix, name, details), retryItem, nil
	}
	cloned := librarySpell.Clone(libFile, entity, nil, false)
	applyNameablesToClonedSpell(cloned, name, action.Notes.String())
	applyAIItemDescriptionToSpell(cloned, action.Description.String())
	if pointsText != "" {
		if points, err := fxp.FromString(pointsText); err == nil {
			cloned.SetRawPoints(points)
		}
	}
	return append(spells, cloned), "", nil, nil
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
	if name != "" {
		if existing := d.findExistingEquipment(entity, name); existing != nil {
			if action.Quantity.Int() != 0 {
				existing.Quantity = fxp.FromInteger(action.Quantity.Int())
			}
			applyAIItemDescriptionToEquipment(existing, action.Description.String())
			return equipment, "", nil
		}
	}
	if useTIDLookup {
		if existing := d.findExistingEquipmentByID(entity, idStr, name); existing != nil {
			if action.Quantity.Int() != 0 {
				existing.Quantity = fxp.FromInteger(action.Quantity.Int())
			}
			applyAIItemDescriptionToEquipment(existing, action.Description.String())
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
		return equipment, fmt.Sprintf("%sWarning: equipment %q was not found in the library and was skipped. Equipment must be chosen from provided library entries.", warningPrefix, name), nil
	}
	cloned := libraryEquipment.Clone(libFile, entity, nil, false)
	if action.Quantity.Int() != 0 {
		cloned.Quantity = fxp.FromInteger(action.Quantity.Int())
	}
	applyAIItemDescriptionToEquipment(cloned, action.Description.String())
	return append(equipment, cloned), "", nil
}

func (d *aiChatDockable) findExistingTrait(entity *gurps.Entity, name string) *gurps.Trait {
	name = strings.TrimSpace(name)
	requestedNorm := normalizeLookupText(name)
	for _, trait := range entity.Traits {
		if trait.Container() {
			continue
		}
		resolvedName := aiResolvedTraitName(trait)
		if strings.EqualFold(trait.Name, name) || strings.EqualFold(resolvedName, name) {
			return trait
		}
		if requestedNorm != "" && normalizeLookupText(resolvedName) == requestedNorm {
			return trait
		}
	}
	return nil
}

func (d *aiChatDockable) findExistingTraitByID(entity *gurps.Entity, idStr, name string) *gurps.Trait {
	idStr = normalizeAISelectionID(idStr)
	requestedNorm := normalizeLookupText(name)
	if idStr == "" {
		return nil
	}
	for _, trait := range entity.Traits {
		if trait.Container() {
			continue
		}
		sourceID := normalizeAISelectionID(string(trait.Source.TID))
		localID := normalizeAISelectionID(string(trait.TID))
		if sourceID != idStr && localID != idStr {
			continue
		}
		if requestedNorm != "" && normalizeLookupText(aiResolvedTraitName(trait)) != requestedNorm {
			continue
		}
		return trait
	}
	return nil
}

func (d *aiChatDockable) findExistingSkill(entity *gurps.Entity, name string) *gurps.Skill {
	if entity == nil {
		return nil
	}
	return findExistingSkillInList(entity.Skills, name)
}

func findExistingSkillInList(skills []*gurps.Skill, name string) *gurps.Skill {
	name = strings.TrimSpace(name)
	requestedNorm := normalizeLookupText(name)
	for _, skill := range skills {
		if skill.Container() {
			continue
		}
		displayName := aiResolvedSkillDisplayName(skill)
		if strings.EqualFold(skill.Name, name) || strings.EqualFold(displayName, name) || strings.EqualFold(aiResolvedSkillName(skill), name) {
			return skill
		}
		if requestedNorm != "" && normalizeLookupText(displayName) == requestedNorm {
			return skill
		}
	}
	return nil
}

func (d *aiChatDockable) findExistingSkillByID(entity *gurps.Entity, idStr, name string) *gurps.Skill {
	if entity == nil {
		return nil
	}
	return findExistingSkillByIDInList(entity.Skills, idStr, name)
}

func findExistingSkillByIDInList(skills []*gurps.Skill, idStr, name string) *gurps.Skill {
	idStr = normalizeAISelectionID(idStr)
	requestedNorm := normalizeLookupText(name)
	if idStr == "" {
		return nil
	}
	for _, skill := range skills {
		if skill.Container() {
			continue
		}
		sourceID := normalizeAISelectionID(string(skill.Source.TID))
		localID := normalizeAISelectionID(string(skill.TID))
		if sourceID != idStr && localID != idStr {
			continue
		}
		if requestedNorm != "" && normalizeLookupText(aiResolvedSkillDisplayName(skill)) != requestedNorm {
			continue
		}
		return skill
	}
	return nil
}

func (d *aiChatDockable) findExistingSpell(entity *gurps.Entity, name string) *gurps.Spell {
	if entity == nil {
		return nil
	}
	return findExistingSpellInList(entity.Spells, name)
}

func findExistingSpellInList(spells []*gurps.Spell, name string) *gurps.Spell {
	name = strings.TrimSpace(name)
	requestedNorm := normalizeLookupText(name)
	for _, spell := range spells {
		if spell.Container() {
			continue
		}
		resolvedName := aiResolvedSpellName(spell)
		if strings.EqualFold(spell.Name, name) || strings.EqualFold(resolvedName, name) {
			return spell
		}
		if requestedNorm != "" && normalizeLookupText(resolvedName) == requestedNorm {
			return spell
		}
	}
	return nil
}

func (d *aiChatDockable) findExistingSpellByID(entity *gurps.Entity, idStr, name string) *gurps.Spell {
	if entity == nil {
		return nil
	}
	return findExistingSpellByIDInList(entity.Spells, idStr, name)
}

func findExistingSpellByIDInList(spells []*gurps.Spell, idStr, name string) *gurps.Spell {
	idStr = normalizeAISelectionID(idStr)
	requestedNorm := normalizeLookupText(name)
	if idStr == "" {
		return nil
	}
	for _, spell := range spells {
		if spell.Container() {
			continue
		}
		sourceID := normalizeAISelectionID(string(spell.Source.TID))
		localID := normalizeAISelectionID(string(spell.TID))
		if sourceID != idStr && localID != idStr {
			continue
		}
		if requestedNorm != "" && normalizeLookupText(aiResolvedSpellName(spell)) != requestedNorm {
			continue
		}
		return spell
	}
	return nil
}

func (d *aiChatDockable) findExistingEquipment(entity *gurps.Entity, name string) *gurps.Equipment {
	name = strings.TrimSpace(name)
	requestedNorm := normalizeLookupText(name)
	for _, eqp := range append(append([]*gurps.Equipment(nil), entity.CarriedEquipment...), entity.OtherEquipment...) {
		if eqp.Container() {
			continue
		}
		resolvedName := aiResolvedEquipmentName(eqp)
		if strings.EqualFold(eqp.Name, name) || strings.EqualFold(resolvedName, name) {
			return eqp
		}
		if requestedNorm != "" && normalizeLookupText(resolvedName) == requestedNorm {
			return eqp
		}
	}
	return nil
}

func (d *aiChatDockable) findExistingEquipmentByID(entity *gurps.Entity, idStr, name string) *gurps.Equipment {
	idStr = normalizeAISelectionID(idStr)
	requestedNorm := normalizeLookupText(name)
	if idStr == "" {
		return nil
	}
	for _, eqp := range entity.CarriedEquipment {
		if eqp.Container() {
			continue
		}
		sourceID := normalizeAISelectionID(string(eqp.Source.TID))
		localID := normalizeAISelectionID(string(eqp.TID))
		if sourceID != idStr && localID != idStr {
			continue
		}
		if requestedNorm != "" && normalizeLookupText(aiResolvedEquipmentName(eqp)) != requestedNorm {
			continue
		}
		return eqp
	}
	for _, eqp := range entity.OtherEquipment {
		if eqp.Container() {
			continue
		}
		sourceID := normalizeAISelectionID(string(eqp.Source.TID))
		localID := normalizeAISelectionID(string(eqp.TID))
		if sourceID != idStr && localID != idStr {
			continue
		}
		if requestedNorm != "" && normalizeLookupText(aiResolvedEquipmentName(eqp)) != requestedNorm {
			continue
		}
		return eqp
	}
	return nil
}

func buildAIRetryPrompt(items []aiRetryItem) string {
	var b strings.Builder
	b.WriteString("Some items could not be resolved to exact library entries.\n")
	b.WriteString("Return a JSON object with ONLY corrected entries for these unresolved items.\n")
	b.WriteString("Use only the candidate ids and candidate names shown below. Preserve the original points, quantity, notes, and description unless the selected candidate requires a different nameable value.\n\n")
	for _, item := range items {
		b.WriteString(fmt.Sprintf("- category=%s | requested=%q", aiCategoryJSONField(item.Category), item.Name))
		if strings.TrimSpace(item.Notes) != "" {
			b.WriteString(fmt.Sprintf(" | notes=%q", item.Notes))
		}
		if strings.TrimSpace(item.Description) != "" {
			b.WriteString(fmt.Sprintf(" | description=%q", item.Description))
		}
		if strings.TrimSpace(item.Points) != "" {
			b.WriteString(fmt.Sprintf(" | points=%q", item.Points))
		}
		if item.Quantity != 0 {
			b.WriteString(fmt.Sprintf(" | quantity=%d", item.Quantity))
		}
		b.WriteString("\n")
		if len(item.Candidates) > 0 {
			for _, candidate := range item.Candidates {
				b.WriteString(fmt.Sprintf("  - id=%s | name=%s", candidate.ID, candidate.Name))
				if len(candidate.Requires) > 0 {
					b.WriteString(fmt.Sprintf(" | requires: %s", strings.Join(candidate.Requires, ", ")))
				}
				b.WriteString("\n")
			}
		} else if len(item.Similar) > 0 {
			b.WriteString(fmt.Sprintf("  - alternatives: %s\n", strings.Join(item.Similar, ", ")))
		} else {
			b.WriteString("  - no close alternatives found; omit this item\n")
		}
	}
	b.WriteString("\nReturn ONLY the corrected JSON for these items, using the exact candidate ids and candidate names shown.")
	return b.String()
}

func (d *aiChatDockable) queryGemini(prompt string) {
	settings := gurps.GlobalSettings().AI
	if settings.GeminiAPIKey == "" {
		d.addMessage("AI", i18n.Text("Gemini API Key is not set. Please configure it in the AI Settings."))
		return
	}
	prepared := d.prepareGeminiRequest(prompt)
	if prepared.NeedsUserInput {
		d.addMessage("AI", prepared.UserFacingMessage)
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
		resolvedModel, modelWarning := d.resolveGeminiModelName(ctx, client)
		if modelWarning != "" {
			unison.InvokeTask(func() { d.addMessage("AI", modelWarning) })
		}
		writeSystemPromptDebugFile(prepared.SystemPrompt)
		responseStr, err := d.sendGeminiRequest(ctx, resolvedModel, prepared.SystemPrompt, prepared.UserPrompt, prepared.ExpectJSON)
		if err != nil {
			unison.InvokeTask(func() { d.addMessage("AI", fmt.Sprintf(i18n.Text("Error generating content: %v"), err)) })
			return
		}
		prepared.UserPrompt = aiNormalizeExternalText("gemini.user-prompt.history", prepared.UserPrompt)
		responseStr = aiNormalizeExternalText("gemini.response.history", responseStr)
		d.chatHistory = append(d.chatHistory, &genai.Content{Parts: []genai.Part{genai.Text(prepared.UserPrompt)}, Role: "user"})
		d.chatHistory = append(d.chatHistory, &genai.Content{Parts: []genai.Part{genai.Text(responseStr)}, Role: "model"})
		retryCh := make(chan []aiRetryItem, 1)
		unison.InvokeTask(func() { d.handleAIResponseWithCh(responseStr, retryCh) })
		retryItems := <-retryCh
		if len(retryItems) > 0 {
			correctionPrompt := buildAIRetryPrompt(retryItems)
			correctionStr, err2 := d.sendGeminiRequest(ctx, resolvedModel, d.aiGeminiCorrectionSystemPrompt(), correctionPrompt, true)
			if err2 != nil {
				unison.InvokeTask(func() {
					d.addMessage("AI", fmt.Sprintf(i18n.Text("Warning: AI follow-up alternatives could not be generated; unresolved items were skipped: %v"), err2))
				})
				return
			}
			doneCh := make(chan struct{}, 1)
			unison.InvokeTask(func() { d.applyCorrectionResponse(correctionStr, retryItems); doneCh <- struct{}{} })
			<-doneCh
		}
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
	prepared := d.prepareAIRequest(prompt)

	d.setThinking(true)
	go func() {
		defer unison.InvokeTask(func() { d.setThinking(false) })

		if session := d.buildSession; session != nil {
			if session.State == aiBuildSessionStatePendingApproval {
				if aiIsExplicitApproval(prompt) {
					session.State = aiBuildSessionStateGenerating
					session.Params = aiDraftProfileToCharacterRequestParams(session.DraftProfile, session.Params)
					session.OriginalRequest = aiBuildGenerationRequestFromDraftProfile(session.OriginalRequest, session.DraftProfile)
					session.GatheringLog = nil
					unison.InvokeTask(func() {
						d.addMessage("AI", i18n.Text("Baseline approved. Starting character generation."))
					})
					d.executeLocalThreePhaseGeneration(endpoint, model, session.OriginalRequest, session.Params)
					return
				}
				unison.InvokeTask(func() {
					d.addMessage("AI", aiBuildBaselineEditModeMessage())
				})
				session.State = aiBuildSessionStateGathering
				session.GatheringLog = nil
			}
			if session.State == aiBuildSessionStateGathering {
				if aiIsExplicitApproval(prompt) {
					unison.InvokeTask(func() {
						d.addMessage("AI", aiBuildBaselineEditModeMessage())
					})
					return
				}
				sysPrompt := aiLocalBaselineGatheringSystemPrompt(session.DraftProfile)
				writeSystemPromptDebugFile(sysPrompt)
				messages := make([]aiLocalChatMessage, 0, len(session.GatheringLog)+2)
				messages = append(messages, aiLocalChatMessage{Role: "system", Content: sysPrompt})
				messages = append(messages, session.GatheringLog...)
				messages = append(messages, aiLocalChatMessage{Role: "user", Content: prompt})
				responseStr, err := d.queryLocalModel(endpoint, model, messages, aiLocalBaselineDraftProfileJSONSchema())
				if err != nil {
					unison.InvokeTask(func() { d.addMessage("AI", err.Error()) })
					return
				}
				if response, ok := aiParseLocalBaselineDraftProfileResponse(responseStr); ok {
					session.DraftProfile = aiMergeDraftProfile(session.DraftProfile, response.DraftProfile)
					session.Params = aiDraftProfileToCharacterRequestParams(session.DraftProfile, session.Params)
					if aiDraftProfileReadyForApproval(response.Status.String(), session.DraftProfile) {
						session.State = aiBuildSessionStatePendingApproval
						session.GatheringLog = nil
						unison.InvokeTask(func() {
							d.addMessage("AI", aiBuildBaselineApprovalMessage(session.DraftProfile))
						})
					} else {
						session.State = aiBuildSessionStateGathering
						session.GatheringLog = append(session.GatheringLog,
							aiLocalChatMessage{Role: "user", Content: prompt},
							aiLocalChatMessage{Role: "assistant", Content: responseStr},
						)
						unison.InvokeTask(func() { d.addMessage("AI", responseStr) })
					}
					return
				}
				session.GatheringLog = append(session.GatheringLog,
					aiLocalChatMessage{Role: "user", Content: prompt},
					aiLocalChatMessage{Role: "assistant", Content: responseStr},
				)
				unison.InvokeTask(func() { d.addMessage("AI", responseStr) })
				return
			}
		}

		if prepared.IsInitialBuild {
			d.executeLocalThreePhaseGeneration(endpoint, model, prompt, prepared.BuildParams)
			return
		}

		sysPrompt := prepared.SystemPrompt
		writeSystemPromptDebugFile(sysPrompt)
		schema := aiActionPlanJSONSchema()
		responseStr, err := d.queryLocalModel(endpoint, model, buildLocalStatelessMessages(sysPrompt, prepared.UserPrompt), schema)
		if err != nil {
			unison.InvokeTask(func() { d.addMessage("AI", err.Error()) })
			return
		}

		resolution, err := d.resolveAIResponseText(responseStr)
		if err != nil {
			unison.InvokeTask(func() {
				d.addMessage("AI", responseStr)
				d.addMessage("AI", fmt.Sprintf(i18n.Text("AI library catalog could not be prepared: %v"), err))
			})
			return
		}
		if resolution.Parsed && len(resolution.RetryItems) > 0 {
			followUpPrompt := aiBuildLocalResolverAlternativePrompt(resolution.RetryItems)
			followUpResponse, retryErr := d.queryLocalModel(endpoint, model, buildLocalStatelessMessages(sysPrompt, followUpPrompt), schema)
			if retryErr != nil {
				resolution.Warnings = append(resolution.Warnings, i18n.Text("Warning: AI follow-up alternatives could not be generated; unresolved items were skipped."))
			} else if strings.TrimSpace(followUpResponse) != "" {
				followUpResolution, followUpResolveErr := d.resolveAIResponseText(followUpResponse)
				switch {
				case followUpResolveErr != nil:
					resolution.Warnings = append(resolution.Warnings, fmt.Sprintf(i18n.Text("Warning: AI follow-up alternatives could not be resolved: %v"), followUpResolveErr))
				case followUpResolution.Parsed:
					filteredFollowUpPlan := aiFilterCorrectionPlan(followUpResolution.Plan, resolution.RetryItems)
					ignoredCorrections := aiActionPlanItemCount(followUpResolution.Plan) - aiActionPlanItemCount(filteredFollowUpPlan)
					if ignoredCorrections > 0 {
						resolution.Warnings = append(resolution.Warnings, fmt.Sprintf(i18n.Text("Warning: ignored %d unrelated AI follow-up correction entries."), ignoredCorrections))
					}
					mergedPlan := aiActionPlanWithoutRetryItems(resolution.Plan, resolution.RetryItems)
					mergeAIActionPlan(&mergedPlan, filteredFollowUpPlan)
					mergedResolution, mergedErr := d.resolveAIActionPlanResult(mergedPlan)
					if mergedErr != nil {
						resolution.Warnings = append(resolution.Warnings, fmt.Sprintf(i18n.Text("Warning: AI follow-up alternatives could not be resolved: %v"), mergedErr))
					} else {
						if aiActionPlanItemCount(filteredFollowUpPlan) < len(resolution.RetryItems) {
							mergedResolution.Warnings = append(mergedResolution.Warnings, i18n.Text("Warning: some unresolved items were skipped because the follow-up response did not provide alternatives for all of them."))
						}
						resolution = mergedResolution
					}
				case strings.Contains(followUpResponse, "{"):
					resolution.Warnings = append(resolution.Warnings, i18n.Text("Warning: AI follow-up alternatives could not be parsed and were skipped."))
				}
			}
		}

		doneCh := make(chan struct{}, 1)
		unison.InvokeTask(func() {
			d.addMessage("AI", responseStr)
			if !resolution.Parsed {
				if strings.Contains(responseStr, "{") {
					d.addMessage("AI", i18n.Text("Structured update data was detected, but it could not be parsed into a character-sheet update."))
				}
				doneCh <- struct{}{}
				return
			}

			warnings := append([]string(nil), resolution.Warnings...)
			retryItems := append([]aiRetryItem(nil), resolution.RetryItems...)
			applied := false
			if hasAIActionPlanContent(resolution.ResolvedPlan) {
				applyWarnings, applyRetryItems, applyErr := d.applyAIActionPlan(resolution.ResolvedPlan)
				if applyErr != nil {
					d.addMessage("AI", fmt.Sprintf(i18n.Text("AI plan could not be applied: %v"), applyErr))
					doneCh <- struct{}{}
					return
				}
				warnings = append(warnings, applyWarnings...)
				retryItems = append(retryItems, applyRetryItems...)
				applied = true
			}
			for _, warning := range warnings {
				d.addMessage("AI", warning)
			}
			if len(retryItems) != 0 {
				d.addMessage("AI", i18n.Text("Some items still need exact library selection and were skipped."))
			}
			if applied {
				d.addMessage("AI", i18n.Text("AI plan has been applied to the active character sheet."))
			}
			doneCh <- struct{}{}
		})
		<-doneCh
	}()
}

type aiLocalBaselineDraftProfileResponse struct {
	Status       aiFlexibleString `json:"status,omitempty"`
	DraftProfile aiDraftProfile   `json:"draft_profile,omitempty"`
}

func aiParseLocalBaselineDraftProfileResponse(text string) (aiLocalBaselineDraftProfileResponse, bool) {
	for _, payload := range extractJSONPayloads(text) {
		cleaned := sanitizeAIJSONPayload(payload)
		if cleaned == "" {
			continue
		}
		var response aiLocalBaselineDraftProfileResponse
		if normalized, ok := aiMarshalNormalizedJSONPayload(cleaned); ok {
			if err := json.Unmarshal(normalized, &response); err != nil {
				continue
			}
		} else if err := json.Unmarshal([]byte(cleaned), &response); err != nil {
			continue
		}
		response.DraftProfile = aiNormalizeDraftProfile(response.DraftProfile)
		if !aiDraftProfileHasMeaningfulData(response.DraftProfile) {
			continue
		}
		return response, true
	}
	return aiLocalBaselineDraftProfileResponse{}, false
}

func aiHistoryWithLastAssistantSummary(history []*genai.Content, plan aiActionPlan) []*genai.Content {
	if len(history) == 0 {
		return history
	}
	last := history[len(history)-1]
	if last == nil || aiNormalizeLocalRole(last.Role) != "assistant" {
		return history
	}
	updated := append([]*genai.Content(nil), history...)
	replacement := &genai.Content{Role: last.Role, Parts: []genai.Part{genai.Text(aiAppliedPlanHistorySummary(plan))}}
	updated[len(updated)-1] = replacement
	return updated
}

func (d *aiChatDockable) replaceLastAssistantHistoryWithAppliedSummary(plan aiActionPlan) {
	d.chatHistory = aiHistoryWithLastAssistantSummary(d.chatHistory, plan)
}

func aiAppliedPlanHistorySummary(plan aiActionPlan) string {
	updated := make([]string, 0, 6)
	if plan.Profile != nil {
		updated = append(updated, "profile")
	}
	if len(plan.Attributes) > 0 {
		updated = append(updated, fmt.Sprintf("%d attribute changes", len(plan.Attributes)))
	}
	if len(plan.Advantages) > 0 {
		updated = append(updated, fmt.Sprintf("%d advantages", len(plan.Advantages)))
	}
	if len(plan.Disadvantages) > 0 {
		updated = append(updated, fmt.Sprintf("%d disadvantages", len(plan.Disadvantages)))
	}
	if len(plan.Quirks) > 0 {
		updated = append(updated, fmt.Sprintf("%d quirks", len(plan.Quirks)))
	}
	if len(plan.Skills) > 0 {
		updated = append(updated, fmt.Sprintf("%d skills", len(plan.Skills)))
	}
	if len(plan.Spells) > 0 {
		updated = append(updated, fmt.Sprintf("%d spells", len(plan.Spells)))
	}
	if len(plan.Equipment) > 0 {
		updated = append(updated, fmt.Sprintf("%d equipment items", len(plan.Equipment)))
	}
	var builder strings.Builder
	builder.WriteString("Applied character-sheet update.")
	if len(updated) > 0 {
		builder.WriteString(" Updated: ")
		builder.WriteString(strings.Join(updated, ", "))
		builder.WriteString(".")
	}
	return builder.String()
}
