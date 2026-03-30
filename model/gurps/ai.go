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

	"github.com/richardwilkes/toolbox/v2/xhash"
)

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
	Provider       AIProvider `json:"provider,omitempty"`
	GeminiAPIKey   string     `json:"gemini_api_key,omitempty"`
	LocalServerURL string     `json:"local_server_url,omitempty"`
	LocalModel     string     `json:"local_model,omitempty"`
}

// IsZero implements json.isZero.
func (a AISettings) IsZero() bool {
	return a == AISettings{}
}

// Hash implements xhash.Hashable.
func (a AISettings) Hash(h hash.Hash) {
	xhash.StringWithLen(h, string(a.Provider))
	xhash.StringWithLen(h, a.GeminiAPIKey)
	xhash.StringWithLen(h, a.LocalServerURL)
	xhash.StringWithLen(h, a.LocalModel)
}
