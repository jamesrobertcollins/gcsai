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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/richardwilkes/gcs/v5/model/gurps"
	"github.com/richardwilkes/gcs/v5/model/gurps/enums/dgroup"
	"github.com/richardwilkes/gcs/v5/svg"
	"github.com/richardwilkes/toolbox/v2/i18n"
	"github.com/richardwilkes/unison"
	"github.com/richardwilkes/unison/enums/align"
)

type aiSettingsDockable struct {
	SettingsDockable
	content *unison.Panel
}

// ShowAISettings shows the AI settings.
func ShowAISettings() {
	if Activate(func(d unison.Dockable) bool {
		_, ok := d.AsPanel().Self.(*aiSettingsDockable)
		return ok
	}) {
		return
	}
	d := &aiSettingsDockable{}
	d.Self = d
	d.TabTitle = i18n.Text("AI Settings")
	d.TabIcon = svg.Bot
	d.Setup(nil, nil, d.initContent)
	PlaceInDock(d, dgroup.SubEditors, false)
	ActivateDockable(d)
}

func (d *aiSettingsDockable) initContent(content *unison.Panel) {
	d.content = content
	content.SetLayout(&unison.FlexLayout{
		Columns:  2,
		HSpacing: unison.StdHSpacing,
		VSpacing: unison.StdVSpacing,
	})

	settings := &gurps.GlobalSettings().AI

	providerPopup := addLabelAndPopup(content, i18n.Text("AI Provider"), i18n.Text("Select the AI provider to use."), gurps.AIProviders, &settings.Provider)

	geminiAPIKeyLabel := NewFieldLeadingLabel(i18n.Text("Gemini API Key"), false)
	geminiAPIKeyLabel.Tooltip = newWrappedTooltip(i18n.Text("Your Google AI Gemini API Key."))
	content.AddChild(geminiAPIKeyLabel)

	apiKeyField := NewStringField(nil, "", i18n.Text("Gemini API Key"),
		func() string { return settings.GeminiAPIKey },
		func(value string) {
			settings.GeminiAPIKey = value
			MarkModified(content)
		})
	// apiKeyField.Field.Password = true
	content.AddChild(apiKeyField)

	localURLLabel := NewFieldLeadingLabel(i18n.Text("Local Server URL"), false)
	localURLLabel.Tooltip = newWrappedTooltip(i18n.Text("Complete URL of the Ollama server, for example http://localhost:11434."))
	content.AddChild(localURLLabel)

	var refreshLocalModels func()
	var refreshTimer *time.Timer

	scheduleRefresh := func() {
		if refreshTimer != nil {
			refreshTimer.Stop()
		}
		refreshTimer = time.AfterFunc(400*time.Millisecond, func() {
			if settings.Provider != gurps.AIProviderLocal {
				return
			}
			if strings.TrimSpace(settings.LocalServerURL) == "" {
				return
			}
			refreshLocalModels()
		})
	}

	localURLField := NewStringField(nil, "", i18n.Text("Local Server URL"),
		func() string { return settings.LocalServerURL },
		func(value string) {
			settings.LocalServerURL = value
			MarkModified(content)
			if settings.Provider == gurps.AIProviderLocal {
				scheduleRefresh()
			}
		})
	content.AddChild(localURLField)

	localModelLabel := NewFieldLeadingLabel(i18n.Text("Local Model"), false)
	localModelLabel.Tooltip = newWrappedTooltip(i18n.Text("The local Ollama model name to use. Refresh the list to pick from available local models."))
	content.AddChild(localModelLabel)

	localModelField := NewStringField(nil, "", i18n.Text("Local Model"),
		func() string { return settings.LocalModel },
		func(value string) {
			settings.LocalModel = value
			MarkModified(content)
		})
	modelRow := unison.NewPanel()
	modelRow.SetLayout(&unison.FlowLayout{HSpacing: unison.StdHSpacing})
	modelRow.AddChild(localModelField)
	refreshModelsButton := unison.NewButton()
	refreshModelsButton.SetTitle(i18n.Text("Refresh Models"))
	modelRow.AddChild(refreshModelsButton)
	content.AddChild(modelRow)

	availableModelsLabel := NewFieldLeadingLabel(i18n.Text("Available Models"), false)
	availableModelsLabel.Tooltip = newWrappedTooltip(i18n.Text("Models discovered on the local Ollama server."))
	content.AddChild(availableModelsLabel)

	modelPopup := unison.NewPopupMenu[string]()
	modelPopup.SetLayoutData(&unison.FlexLayoutData{HAlign: align.Fill, HGrab: true})
	content.AddChild(modelPopup)

	refreshLocalModels = func() {
		url := strings.TrimSpace(settings.LocalServerURL)
		if url == "" {
			Workspace.ErrorHandler(i18n.Text("Local server URL is not set."), fmt.Errorf("no local URL configured"))
			return
		}
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			url = "http://" + url
		}
		url = strings.TrimSuffix(url, "/")

		unison.InvokeTask(func() {
			modelPopup.RemoveAllItems()
			modelPopup.AddItem(i18n.Text("Checking models..."))
			modelPopup.SetEnabled(false)
		})

		go func(endpoint string) {
			client := &http.Client{Timeout: 5 * time.Second}
			modelPaths := []string{"/api/tags", "/api/list"}
			var models []string
			var fetchErr error
			for _, path := range modelPaths {
				resp, err := client.Get(endpoint + path)
				if err != nil {
					fetchErr = err
					continue
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					fetchErr = err
					continue
				}
				if resp.StatusCode != http.StatusOK {
					fetchErr = fmt.Errorf("%d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
					continue
				}
				var listResult struct {
					Models []struct {
						Name  string `json:"name"`
						Model string `json:"model"`
					} `json:"models"`
				}
				if err := json.Unmarshal(body, &listResult); err == nil && len(listResult.Models) > 0 {
					for _, obj := range listResult.Models {
						name := obj.Model
						if name == "" {
							name = obj.Name
						}
						if name != "" {
							models = append(models, name)
						}
					}
					if len(models) > 0 {
						fetchErr = nil
						break
					}
				}
				if len(models) == 0 {
					var names []string
					if err := json.Unmarshal(body, &names); err == nil {
						models = names
						fetchErr = nil
						break
					}
					var objects []struct {
						Name  string `json:"name"`
						Model string `json:"model"`
					}
					if err := json.Unmarshal(body, &objects); err == nil {
						for _, obj := range objects {
							name := obj.Model
							if name == "" {
								name = obj.Name
							}
							if name != "" {
								models = append(models, name)
							}
						}
						if len(models) > 0 {
							fetchErr = nil
							break
						}
					}
				}
				if len(models) == 0 {
					fetchErr = fmt.Errorf("unexpected model list response from %s", endpoint+path)
				}
			}

			unison.InvokeTask(func() {
				modelPopup.RemoveAllItems()
				if len(models) == 0 {
					modelPopup.SetEnabled(false)
					modelPopup.AddItem(i18n.Text("No models found"))
					if fetchErr != nil {
						Workspace.ErrorHandler(i18n.Text("Failed to refresh local models."), fetchErr)
					}
					return
				}
				modelPopup.SetEnabled(true)
				for _, one := range models {
					modelPopup.AddItem(one)
				}
				if settings.LocalModel != "" && !slices.Contains(models, settings.LocalModel) {
					modelPopup.AddItem(settings.LocalModel)
				}
				if settings.LocalModel == "" && len(models) > 0 {
					settings.LocalModel = models[0]
					MarkModified(content)
				}
				if settings.LocalModel != "" {
					modelPopup.Select(settings.LocalModel)
				}
			})
		}(url)
	}

	updateFields := func() {
		isGemini := settings.Provider == gurps.AIProviderGemini
		geminiAPIKeyLabel.SetEnabled(isGemini)
		apiKeyField.SetEnabled(isGemini)

		isLocal := settings.Provider == gurps.AIProviderLocal
		localURLLabel.SetEnabled(isLocal)
		localURLField.SetEnabled(isLocal)
		localModelLabel.SetEnabled(isLocal)
		localModelField.SetEnabled(isLocal)
		refreshModelsButton.SetEnabled(isLocal)
		availableModelsLabel.SetEnabled(isLocal)
		modelPopup.SetEnabled(isLocal)
	}

	providerPopup.SelectionChangedCallback = func(p *unison.PopupMenu[gurps.AIProvider]) {
		if item, ok := p.Selected(); ok {
			settings.Provider = item
			MarkModified(content)
			updateFields()
			if item == gurps.AIProviderLocal {
				refreshLocalModels()
			}
		}
	}

	refreshModelsButton.ClickCallback = func() {
		if settings.Provider != gurps.AIProviderLocal {
			return
		}
		refreshLocalModels()
	}

	modelPopup.SelectionChangedCallback = func(p *unison.PopupMenu[string]) {
		if item, ok := p.Selected(); ok {
			settings.LocalModel = item
			MarkModified(content)
		}
	}

	updateFields()
	if settings.Provider == gurps.AIProviderLocal {
		refreshLocalModels()
	}
}
