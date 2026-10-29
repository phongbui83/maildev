package mail

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

var previewPolicy = buildPolicy()

func buildPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// Email HTML is table soup with inline styles; keep presentation attributes.
	p.AllowElements("html", "head", "body", "style", "center", "font", "table", "thead", "tbody", "tfoot", "tr", "td", "th",
		"caption", "colgroup", "col", "span", "div", "u", "s", "small", "big", "sup", "sub", "hr", "br", "wbr")
	p.AllowAttrs("style", "class", "id", "dir", "lang", "title").Globally()
	p.AllowAttrs("width", "height", "align", "valign", "bgcolor", "background", "border", "cellpadding", "cellspacing",
		"color", "face", "size", "colspan", "rowspan", "nowrap").Globally()
	p.AllowAttrs("alt", "src", "width", "height").OnElements("img")
	p.AllowAttrs("href", "target", "rel").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto", "tel", "cid", "data")
	p.AllowDataURIImages()
	p.RequireNoFollowOnLinks(false)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.AllowStandardURLs()
	return p
}

var cidRe = regexp.MustCompile(`(?i)(src|background)\s*=\s*(["']?)cid:([^"'\s>]+)`)

// SanitizeHTML strips scripts and event handlers and rewrites cid: references to
// the attachment endpoint prefix. Remote resources are additionally blocked by the
// Content-Security-Policy set on the preview response.
func SanitizeHTML(in, cidPrefix string) string {
	out := previewPolicy.Sanitize(in)
	if cidPrefix != "" {
		out = cidRe.ReplaceAllStringFunc(out, func(m string) string {
			sub := cidRe.FindStringSubmatch(m)
			return sub[1] + "=" + sub[2] + cidPrefix + sub[3]
		})
	}
	return out
}

// HTMLToText produces a rough plain-text rendering used for snippets and search
// when a message has no text/plain part.
func HTMLToText(in string) string {
	doc, err := html.Parse(strings.NewReader(in))
	if err != nil {
		return in
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.ElementNode:
			switch n.Data {
			case "script", "style", "head", "title":
				return
			case "br", "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6", "table":
				b.WriteByte('\n')
			case "td", "th":
				b.WriteByte(' ')
			}
		case html.TextNode:
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	lines := strings.Split(b.String(), "\n")
	var out []string
	for _, l := range lines {
		l = strings.Join(strings.Fields(l), " ")
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
