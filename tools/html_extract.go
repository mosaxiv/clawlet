package tools

import (
	"bufio"
	htmlstd "html"
	"io"
	"strings"

	xhtml "golang.org/x/net/html"
)

func looksLikeHTML(b []byte) bool {
	// Heuristic: only sample a small prefix to avoid copying large bodies.
	const max = 1024
	if len(b) > max {
		b = b[:max]
	}
	s := strings.TrimSpace(strings.ToLower(string(b)))
	sn := s
	if len(sn) > 512 {
		sn = sn[:512]
	}
	return strings.HasPrefix(s, "<!doctype") || strings.HasPrefix(s, "<html") || strings.Contains(sn, "<html")
}

func extractHTMLText(src string) (title string, text string) {
	doc, err := xhtml.Parse(strings.NewReader(src))
	if err != nil {
		// Fallback: strip tags very roughly by unescaping only.
		return "", normalizeText(htmlstd.UnescapeString(src))
	}

	title = normalizeText(findTitle(doc))
	text = normalizeText(extractText(doc))
	return title, text
}

func findTitle(n *xhtml.Node) string {
	var out string
	var walk func(*xhtml.Node)
	walk = func(cur *xhtml.Node) {
		if out != "" {
			return
		}
		if cur.Type == xhtml.ElementNode && cur.Data == "title" {
			if cur.FirstChild != nil && cur.FirstChild.Type == xhtml.TextNode {
				out = cur.FirstChild.Data
				return
			}
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

func extractText(doc *xhtml.Node) string {
	var b strings.Builder
	w := bufio.NewWriterSize(&b, 32<<10)

	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n == nil {
			return
		}
		if n.Type == xhtml.ElementNode {
			switch n.Data {
			case "script", "style", "noscript":
				return
			case "br":
				_, _ = io.WriteString(w, "\n")
			case "p", "div", "section", "article", "header", "footer", "main", "nav", "aside",
				"h1", "h2", "h3", "h4", "h5", "h6", "li", "ul", "ol", "table", "tr", "td", "th":
				_, _ = io.WriteString(w, "\n")
			}
		}
		if n.Type == xhtml.TextNode {
			s := strings.TrimSpace(htmlstd.UnescapeString(n.Data))
			if s != "" {
				_, _ = io.WriteString(w, s)
				_, _ = io.WriteString(w, "\n")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}

	// Prefer body if present, else entire document.
	body := findElement(doc, "body")
	if body != nil {
		walk(body)
	} else {
		walk(doc)
	}
	_ = w.Flush()
	return b.String()
}

func findElement(n *xhtml.Node, tag string) *xhtml.Node {
	var found *xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(cur *xhtml.Node) {
		if found != nil {
			return
		}
		if cur.Type == xhtml.ElementNode && cur.Data == tag {
			found = cur
			return
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// Collapse spaces and excessive blank lines.
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			blank++
			if blank <= 1 {
				out = append(out, "")
			}
			continue
		}
		blank = 0
		ln = strings.Join(strings.Fields(ln), " ")
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
