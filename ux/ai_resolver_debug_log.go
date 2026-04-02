package ux

import (
	"fmt"
	"os"
	"strings"

	"github.com/richardwilkes/toolbox/v2/i18n"
	"github.com/richardwilkes/toolbox/v2/xstrings"
	"github.com/richardwilkes/unison"
)

const aiResolverDebugLogTailLines = 200

func ShowAIResolverDebugLog() {
	refreshAIResolverDebugLogView(true)
}

func ClearAIResolverDebugTelemetry() {
	if unison.QuestionDialog(
		i18n.Text("Clear AI resolver telemetry?"),
		i18n.Text("This removes both the raw resolver debug log and the deduped counter summary."),
	) != unison.ModalResponseOK {
		return
	}
	aiResolverDebugLogLock.Lock()
	err := aiClearResolverDebugTelemetry()
	aiResolverDebugLogLock.Unlock()
	if err != nil {
		Workspace.ErrorHandler(i18n.Text("Unable to clear AI resolver telemetry"), err)
		return
	}
	refreshAIResolverDebugLogView(false)
}

func showOrRefreshReadOnlyMarkdown(title, content string) {
	path := markdownContentOnlyPrefix + title
	if existing := LocateFileBackedDockable(path); existing != nil {
		if d, ok := existing.(*MarkdownDockable); ok {
			normalized := xstrings.NormalizeLineEndings(content)
			d.original = normalized
			d.content = normalized
			d.markdown.SetContent(normalized, 0)
			d.MarkForLayoutAndRedraw()
			ActivateDockable(d)
			return
		}
	}
	ShowReadOnlyMarkdown(title, content)
}

func refreshAIResolverDebugLogView(openIfNeeded bool) {
	title := i18n.Text("AI Resolver Debug Log")
	if !openIfNeeded && LocateFileBackedDockable(markdownContentOnlyPrefix+title) == nil {
		return
	}
	showOrRefreshReadOnlyMarkdown(title, aiResolverDebugLogMarkdown())
}

func aiResolverDebugLogMarkdown() string {
	state := aiLoadResolverDebugCounterState()
	rawLines := aiTailResolverDebugLog(aiResolverDebugLogTailLines)

	var builder strings.Builder
	builder.WriteString("# ")
	builder.WriteString(i18n.Text("AI Resolver Debug Log"))
	builder.WriteString("\n\n")
	builder.WriteString(i18n.Text("Open this Tools menu item again to refresh the current view."))
	builder.WriteString(" ")
	builder.WriteString(i18n.Text("Use the Tools menu item to clear resolver telemetry when you want to reset the log and counters."))
	builder.WriteString("\n\n")
	builder.WriteString("## ")
	builder.WriteString(i18n.Text("Deduped Counters"))
	builder.WriteString("\n\n")
	if len(state.Entries) == 0 {
		builder.WriteString("_")
		builder.WriteString(i18n.Text("No resolver telemetry recorded yet."))
		builder.WriteString("_\n")
	} else {
		builder.WriteString("| Count | Kind | Details | Last Seen |\n")
		builder.WriteString("| ---: | --- | --- | --- |\n")
		limit := min(len(state.Entries), 100)
		for _, entry := range state.Entries[:limit] {
			builder.WriteString(fmt.Sprintf("| %d | %s | %s | %s |\n",
				entry.Count,
				aiResolverMarkdownEscape(entry.Kind),
				aiResolverMarkdownEscape(strings.Join(entry.Fields, " | ")),
				aiResolverMarkdownEscape(entry.LastSeen),
			))
		}
		if len(state.Entries) > limit {
			builder.WriteString("\n_")
			builder.WriteString(fmt.Sprintf(i18n.Text("Showing the top %d of %d deduped entries."), limit, len(state.Entries)))
			builder.WriteString("_\n")
		}
	}

	builder.WriteString("\n## ")
	builder.WriteString(i18n.Text("Recent Raw Log Entries"))
	builder.WriteString("\n\n")
	if len(rawLines) == 0 {
		builder.WriteString("_")
		builder.WriteString(i18n.Text("No raw log entries yet."))
		builder.WriteString("_\n")
		return builder.String()
	}
	builder.WriteString("```text\n")
	for _, line := range rawLines {
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	builder.WriteString("```\n")
	return builder.String()
}

func aiResolverMarkdownEscape(text string) string {
	text = strings.ReplaceAll(text, "|", "\\|")
	return strings.ReplaceAll(text, "\n", " ")
}

func aiTailResolverDebugLog(limit int) []string {
	data, err := os.ReadFile(aiResolverDebugLogFile)
	if err != nil {
		return nil
	}
	text := strings.TrimSpace(xstrings.NormalizeLineEndings(string(data)))
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines
}
