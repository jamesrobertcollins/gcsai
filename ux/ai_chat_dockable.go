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
	"net/http"
	"strings"

	"github.com/google/generative-ai-go/genai"
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
		chat.History = d.chatHistory
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
		unison.InvokeTask(func() { d.addMessage("AI", responseStr) })
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
		unison.InvokeTask(func() { d.addMessage("AI", responseStr) })
		d.chatHistory = append(d.chatHistory, &genai.Content{Parts: []genai.Part{genai.Text(prompt)}, Role: "user"})
		d.chatHistory = append(d.chatHistory, &genai.Content{Parts: []genai.Part{genai.Text(responseStr)}, Role: "assistant"})
	}()
}
