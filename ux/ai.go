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
	"github.com/richardwilkes/toolbox/v2/i18n"
	"github.com/richardwilkes/unison"
)

var (
	// AIChatActionID is the ID for the AI Chat action.
	AIChatActionID                   = "ai.chat"
	AIResolverDebugLogActionID       = "ai.resolver.debug_log"
	AIResolverClearTelemetryActionID = "ai.resolver.clear_telemetry"
	AIResolverAliasMappingsActionID  = "ai.resolver.alias_mappings"
	aiChatAction                     *unison.Action
	aiResolverDebugLogAction         *unison.Action
	aiResolverClearTelemetryAction   *unison.Action
	aiResolverAliasMappingsAction    *unison.Action
)

// registerAIAction creates and registers the AI-related actions.
func registerAIAction() {
	aiChatAction = registerKeyBindableAction(AIChatActionID, &unison.Action{
		ID:              AIChatItemID,
		Title:           i18n.Text("AI Chat..."),
		ExecuteCallback: func(_ *unison.Action, _ any) { ShowAIChat() },
	})
	aiResolverDebugLogAction = registerKeyBindableAction(AIResolverDebugLogActionID, &unison.Action{
		ID:              AIResolverDebugLogItemID,
		Title:           i18n.Text("AI Resolver Debug Log..."),
		ExecuteCallback: func(_ *unison.Action, _ any) { ShowAIResolverDebugLog() },
	})
	aiResolverClearTelemetryAction = registerKeyBindableAction(AIResolverClearTelemetryActionID, &unison.Action{
		ID:              AIResolverClearTelemetryItemID,
		Title:           i18n.Text("Clear AI Resolver Telemetry"),
		ExecuteCallback: func(_ *unison.Action, _ any) { ClearAIResolverDebugTelemetry() },
	})
	aiResolverAliasMappingsAction = registerKeyBindableAction(AIResolverAliasMappingsActionID, &unison.Action{
		ID:              AIResolverAliasMappingsItemID,
		Title:           i18n.Text("AI Resolver Alias Mappings..."),
		ExecuteCallback: func(_ *unison.Action, _ any) { ShowAIResolverAliasMappings() },
	})
}
