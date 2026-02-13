package telegram

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	reMarkdownCodeBlock  = regexp.MustCompile("(?s)```[\\w-]*\\n?([\\s\\S]*?)```")
	reMarkdownInlineCode = regexp.MustCompile("`([^`]+)`")
	reMarkdownHeading    = regexp.MustCompile("(?m)^#{1,6}\\s+(.+)$")
	reMarkdownQuote      = regexp.MustCompile("(?m)^>\\s*(.*)$")
	reMarkdownLink       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reMarkdownBoldA      = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reMarkdownBoldB      = regexp.MustCompile(`__(.+?)__`)
	reMarkdownItalic     = regexp.MustCompile(`(^|[^a-zA-Z0-9])_([^_\n]+)_([^a-zA-Z0-9]|$)`)
	reMarkdownStrike     = regexp.MustCompile(`~~(.+?)~~`)
	reMarkdownBullet     = regexp.MustCompile(`(?m)^[-*]\s+`)
)

func markdownToTelegramHTML(text string) string {
	if text == "" {
		return ""
	}
	if !strings.ContainsAny(text, "`*_~[]()#>-") {
		return html.EscapeString(text)
	}

	type replacement struct {
		token string
		html  string
	}
	replacements := make([]replacement, 0, 8)

	text = reMarkdownCodeBlock.ReplaceAllStringFunc(text, func(src string) string {
		m := reMarkdownCodeBlock.FindStringSubmatch(src)
		code := ""
		if len(m) >= 2 {
			code = m[1]
		}
		token := fmt.Sprintf("\x00CB%d\x00", len(replacements))
		replacements = append(replacements, replacement{
			token: token,
			html:  "<pre><code>" + html.EscapeString(code) + "</code></pre>",
		})
		return token
	})

	text = reMarkdownInlineCode.ReplaceAllStringFunc(text, func(src string) string {
		m := reMarkdownInlineCode.FindStringSubmatch(src)
		code := ""
		if len(m) >= 2 {
			code = m[1]
		}
		token := fmt.Sprintf("\x00IC%d\x00", len(replacements))
		replacements = append(replacements, replacement{
			token: token,
			html:  "<code>" + html.EscapeString(code) + "</code>",
		})
		return token
	})

	text = reMarkdownHeading.ReplaceAllString(text, "$1")
	text = reMarkdownQuote.ReplaceAllString(text, "$1")
	text = html.EscapeString(text)
	text = reMarkdownLink.ReplaceAllString(text, `<a href="$2">$1</a>`)
	text = reMarkdownBoldA.ReplaceAllString(text, "<b>$1</b>")
	text = reMarkdownBoldB.ReplaceAllString(text, "<b>$1</b>")
	text = reMarkdownItalic.ReplaceAllString(text, "$1<i>$2</i>$3")
	text = reMarkdownStrike.ReplaceAllString(text, "<s>$1</s>")
	text = reMarkdownBullet.ReplaceAllString(text, "• ")

	for _, r := range replacements {
		text = strings.ReplaceAll(text, r.token, r.html)
	}
	return text
}
