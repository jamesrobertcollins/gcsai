// Copyright (c) 1998-2025 by Richard A. Wilkes. All rights reserved.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, version 2.0. If a copy of the MPL was not distributed with
// this file, You can obtain one at http://mozilla.org/MPL/2.0/.
//
// This Source Code Form is "Incompatible With Secondary Licenses", as
// defined by the Mozilla Public License, version 2.0.

package gurps

import (
	"hash"
	"io/fs"
	"sort"
	"strings"

	"github.com/richardwilkes/gcs/v5/model/jio"
	"github.com/richardwilkes/toolbox/v2/xhash"
)

// AIResolverAliasesExt is the file extension for exported AI resolver alias mappings.
const AIResolverAliasesExt = ".aliases"

// AIProvider is the type for the AI provider.
type AIProvider string

// Possible values for AIProvider.
const (
	AIProviderNone   AIProvider = ""
	AIProviderGemini AIProvider = "gemini"
	AIProviderLocal  AIProvider = "local"
)

// AIProviders is the list of available AI providers.
var AIProviders = []AIProvider{AIProviderNone, AIProviderGemini, AIProviderLocal}

// DefaultAIResolverAliases returns the built-in alias mappings used by the AI resolver.
func DefaultAIResolverAliases() map[string]map[string]string {
	return map[string]map[string]string{
		"skills": {
			"pistol shooting":  "Guns (Pistol)",
			"handguns":         "Guns (Pistol)",
			"shotgun shooting": "Guns (Shotgun)",
			"knife fighting":   "Knife",
			"gunnery":          "Gunner",
		},
	}
}

func cloneAIResolverAliases(src map[string]map[string]string) map[string]map[string]string {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]map[string]string, len(src))
	for category, aliases := range src {
		if len(aliases) == 0 {
			cloned[category] = map[string]string{}
			continue
		}
		clonedAliases := make(map[string]string, len(aliases))
		for alias, mapped := range aliases {
			clonedAliases[alias] = mapped
		}
		cloned[category] = clonedAliases
	}
	return cloned
}

// String implements fmt.Stringer.
func (a AIProvider) String() string {
	switch a {
	case AIProviderGemini:
		return "Google Gemini"
	case AIProviderLocal:
		return "Local Model"
	default:
		return "None"
	}
}

// AISettings holds the AI settings.
type AISettings struct {
	Provider        AIProvider                   `json:"provider,omitempty"`
	GeminiAPIKey    string                       `json:"gemini_api_key,omitempty"`
	LocalServerURL  string                       `json:"local_server_url,omitempty"`
	LocalModel      string                       `json:"local_model,omitempty"`
	ResolverAliases map[string]map[string]string `json:"resolver_aliases"`
}

// IsZero implements json.isZero.
func (a AISettings) IsZero() bool {
	return a.Provider == AIProviderNone &&
		strings.TrimSpace(a.GeminiAPIKey) == "" &&
		strings.TrimSpace(a.LocalServerURL) == "" &&
		strings.TrimSpace(a.LocalModel) == "" &&
		a.ResolverAliases == nil
}

// Hash implements xhash.Hashable.
func (a AISettings) Hash(h hash.Hash) {
	xhash.StringWithLen(h, string(a.Provider))
	xhash.StringWithLen(h, a.GeminiAPIKey)
	xhash.StringWithLen(h, a.LocalServerURL)
	xhash.StringWithLen(h, a.LocalModel)
	categories := make([]string, 0, len(a.ResolverAliases))
	for category := range a.ResolverAliases {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	for _, category := range categories {
		xhash.StringWithLen(h, category)
		aliases := a.ResolverAliases[category]
		keys := make([]string, 0, len(aliases))
		for alias := range aliases {
			keys = append(keys, alias)
		}
		sort.Strings(keys)
		for _, alias := range keys {
			xhash.StringWithLen(h, alias)
			xhash.StringWithLen(h, aliases[alias])
		}
	}
}

func normalizeAIResolverAliases(aliases map[string]map[string]string) map[string]map[string]string {
	normalized := make(map[string]map[string]string)
	for category, entries := range aliases {
		category = strings.ToLower(strings.TrimSpace(category))
		if category == "" {
			continue
		}
		for alias, mapped := range entries {
			alias = strings.ToLower(strings.TrimSpace(alias))
			mapped = strings.TrimSpace(mapped)
			if alias == "" || mapped == "" {
				continue
			}
			bucket := normalized[category]
			if bucket == nil {
				bucket = make(map[string]string)
				normalized[category] = bucket
			}
			bucket[alias] = mapped
		}
	}
	return normalized
}

type aiResolverAliasesFileData struct {
	ResolverAliases map[string]map[string]string `json:"resolver_aliases"`
}

// LoadAIResolverAliases loads exported AI resolver alias mappings from disk.
func LoadAIResolverAliases(fileSystem fs.FS, filePath string) (map[string]map[string]string, error) {
	var data aiResolverAliasesFileData
	if err := jio.Load(fileSystem, filePath, &data); err != nil {
		return nil, err
	}
	if data.ResolverAliases != nil {
		return normalizeAIResolverAliases(data.ResolverAliases), nil
	}
	var raw map[string]map[string]string
	if err := jio.Load(fileSystem, filePath, &raw); err != nil {
		return nil, err
	}
	return normalizeAIResolverAliases(raw), nil
}

// SaveAIResolverAliases writes exported AI resolver alias mappings to disk.
func SaveAIResolverAliases(filePath string, aliases map[string]map[string]string) error {
	return jio.SaveToFile(filePath, aiResolverAliasesFileData{ResolverAliases: normalizeAIResolverAliases(aliases)})
}

// EnsureValidity checks the current settings for validity and if they aren't valid, makes them so.
func (a *AISettings) EnsureValidity() {
	if a == nil {
		return
	}
	if a.ResolverAliases == nil {
		a.ResolverAliases = cloneAIResolverAliases(DefaultAIResolverAliases())
		return
	}
	a.ResolverAliases = normalizeAIResolverAliases(a.ResolverAliases)
}
