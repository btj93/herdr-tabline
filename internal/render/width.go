package render

import (
	"strings"

	"github.com/rivo/uniseg"
)

const ellipsis = "…"

func displayWidth(value string) int {
	width := 0
	graphemes := uniseg.NewGraphemes(value)
	for graphemes.Next() {
		width += graphemes.Width()
	}
	return width
}

func truncate(value string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if displayWidth(value) <= maxWidth {
		return value
	}
	if maxWidth == 1 {
		return ellipsis
	}

	budget := maxWidth - displayWidth(ellipsis)
	return takePrefix(value, budget) + ellipsis
}

func truncateMiddle(value string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if displayWidth(value) <= maxWidth {
		return value
	}
	if maxWidth == 1 {
		return ellipsis
	}

	budget := maxWidth - displayWidth(ellipsis)
	leftBudget := (budget + 1) / 2
	rightBudget := budget / 2
	return takePrefix(value, leftBudget) + ellipsis + takeSuffix(value, rightBudget)
}

func padLeft(value string, width int) string {
	return strings.Repeat(" ", max(0, width-displayWidth(value))) + value
}

func padRight(value string, width int) string {
	return value + strings.Repeat(" ", max(0, width-displayWidth(value)))
}

func takePrefix(value string, maxWidth int) string {
	var result strings.Builder
	width := 0
	graphemes := uniseg.NewGraphemes(value)
	for graphemes.Next() {
		graphemeWidth := graphemes.Width()
		if width+graphemeWidth > maxWidth {
			break
		}
		result.WriteString(graphemes.Str())
		width += graphemeWidth
	}
	return result.String()
}

func takeSuffix(value string, maxWidth int) string {
	graphemes := uniseg.NewGraphemes(value)
	parts := make([]string, 0)
	widths := make([]int, 0)
	for graphemes.Next() {
		parts = append(parts, graphemes.Str())
		widths = append(widths, graphemes.Width())
	}

	var result strings.Builder
	width := 0
	for i := len(parts) - 1; i >= 0; i-- {
		if width+widths[i] > maxWidth {
			break
		}
		result.WriteString(parts[i])
		width += widths[i]
	}
	return reverseGraphemes(result.String())
}

func reverseGraphemes(value string) string {
	graphemes := uniseg.NewGraphemes(value)
	parts := make([]string, 0)
	for graphemes.Next() {
		parts = append(parts, graphemes.Str())
	}
	var result strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		result.WriteString(parts[i])
	}
	return result.String()
}
