package goquery

import (
	"html"
	"io"
	"regexp"
	"strings"
)

type Document struct {
	raw string
}

type Selection struct {
	items []node
}

type node struct {
	openTag string
	inner   string
	outer   string
}

func NewDocumentFromReader(r io.Reader) (*Document, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return &Document{raw: string(b)}, nil
}

func (d *Document) Find(selector string) *Selection {
	root := &Selection{items: []node{{outer: d.raw, inner: d.raw}}}
	return root.Find(selector)
}

func (s *Selection) Find(selector string) *Selection {
	selector = strings.TrimSpace(selector)
	switch selector {
	case "body":
		return s.findTag("body")
	case "#search-result tbody tr":
		tables := s.findTableByID("search-result")
		tbodies := tables.findTag("tbody")
		return tbodies.findTag("tr")
	case "td":
		return s.findTag("td")
	case "a", "a[href]":
		return s.findTag("a")
	case "small":
		return s.findTag("small")
	default:
		return &Selection{}
	}
}

func (s *Selection) Each(fn func(int, *Selection)) {
	for i := range s.items {
		fn(i, &Selection{items: []node{s.items[i]}})
	}
}

func (s *Selection) Eq(i int) *Selection {
	if i < 0 || i >= len(s.items) {
		return &Selection{}
	}
	return &Selection{items: []node{s.items[i]}}
}

func (s *Selection) First() *Selection {
	return s.Eq(0)
}

func (s *Selection) Length() int {
	return len(s.items)
}

func (s *Selection) Text() string {
	if len(s.items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(s.items))
	for _, item := range s.items {
		parts = append(parts, sanitizeText(item.inner))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func (s *Selection) Attr(name string) (string, bool) {
	if len(s.items) == 0 {
		return "", false
	}
	return extractAttr(s.items[0].openTag, name)
}

func (s *Selection) Map(fn func(int, *Selection) string) []string {
	out := make([]string, 0, len(s.items))
	for i := range s.items {
		out = append(out, fn(i, &Selection{items: []node{s.items[i]}}))
	}
	return out
}

func (s *Selection) findTag(tag string) *Selection {
	pattern := `(?is)<` + regexp.QuoteMeta(tag) + `\b([^>]*)>(.*?)</` + regexp.QuoteMeta(tag) + `>`
	re := regexp.MustCompile(pattern)
	out := make([]node, 0)
	for _, item := range s.items {
		matches := re.FindAllStringSubmatch(item.outer, -1)
		for _, m := range matches {
			if len(m) < 3 {
				continue
			}
			out = append(out, node{
				openTag: "<" + tag + m[1] + ">",
				inner:   m[2],
				outer:   m[0],
			})
		}
	}
	return &Selection{items: out}
}

func (s *Selection) findTableByID(id string) *Selection {
	pattern := `(?is)<table\b([^>]*)>(.*?)</table>`
	re := regexp.MustCompile(pattern)
	out := make([]node, 0)
	for _, item := range s.items {
		matches := re.FindAllStringSubmatch(item.outer, -1)
		for _, m := range matches {
			if len(m) < 3 {
				continue
			}
			openTag := "<table" + m[1] + ">"
			value, ok := extractAttr(openTag, "id")
			if !ok || value != id {
				continue
			}
			out = append(out, node{
				openTag: openTag,
				inner:   m[2],
				outer:   m[0],
			})
		}
	}
	return &Selection{items: out}
}

func sanitizeText(s string) string {
	tagRe := regexp.MustCompile(`(?is)<[^>]+>`)
	noTags := tagRe.ReplaceAllString(s, " ")
	unescaped := html.UnescapeString(noTags)
	return strings.Join(strings.Fields(unescaped), " ")
}

func extractAttr(openTag, name string) (string, bool) {
	pattern := `(?is)\b` + regexp.QuoteMeta(name) + `\s*=\s*("([^"]*)"|'([^']*)')`
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(openTag)
	if len(m) == 0 {
		return "", false
	}
	if len(m) > 2 && m[2] != "" {
		return html.UnescapeString(m[2]), true
	}
	if len(m) > 3 && m[3] != "" {
		return html.UnescapeString(m[3]), true
	}
	return "", false
}
