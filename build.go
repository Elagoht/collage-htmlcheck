package htmlcheck

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/Elagoht/collage/pkg/collage"
	"golang.org/x/net/html"
)

// site is what the build-wide rules read: every page's title, description and
// links, and every path the build wrote.
type site struct {
	pages   []sitePage
	written map[string]bool // normalised URL paths
}

type sitePage struct {
	name, locale string
	path         string
	title        string
	description  string
	links        []string // href values of <a> and <link rel=alternate|canonical>
}

func readSite(files []collage.BuiltFile) (*site, error) {
	s := &site{written: make(map[string]bool, len(files))}
	for _, f := range files {
		s.written[normalise(f.Path)] = true
	}
	for _, f := range files {
		if f.Kind != "page" || f.Name == "" {
			continue
		}
		body, err := os.ReadFile(f.File)
		if err != nil {
			return nil, fmt.Errorf("htmlcheck: read %s: %w", f.File, err)
		}
		doc, err := html.Parse(bytes.NewReader(body))
		if err != nil {
			continue
		}
		pg := newPage(doc, len(body))
		sp := sitePage{name: f.Name, locale: f.Locale, path: f.Path}
		for _, n := range pg.elements("title") {
			if ancestor(n, func(a *html.Node) bool { return a.Data == "svg" }) == nil {
				sp.title = text(n)
				break
			}
		}
		if d := description(pg); d != nil {
			sp.description = strings.TrimSpace(attr(d, "content"))
		}
		for _, n := range pg.elements("a") {
			if href, ok := attrOK(n, "href"); ok {
				sp.links = append(sp.links, href)
			}
		}
		s.pages = append(s.pages, sp)
	}
	sort.Slice(s.pages, func(i, j int) bool { return s.pages[i].path < s.pages[j].path })
	return s, nil
}

func attrOK(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// normalise reduces a URL path to one spelling: decoded, without "index.html" or
// a trailing slash.
func normalise(p string) string {
	if decoded, err := url.PathUnescape(p); err == nil {
		p = decoded
	}
	p = strings.TrimSuffix(p, "index.html")
	if p != "/" {
		p = strings.TrimSuffix(p, "/")
	}
	if p == "" {
		p = "/"
	}
	return p
}

type buildRule struct {
	name  string
	level Level
	doc   string
	check func(p *Plugin, s *site, report func(path, message string))
}

var buildRules = []buildRule{
	{"duplicate-title", Warn, "no two pages share a title", func(_ *Plugin, s *site, report func(string, string)) {
		duplicates(s, func(sp sitePage) string { return sp.title }, func(sp sitePage, others []string) {
			report(sp.path, fmt.Sprintf("the title %q is also the title of %s: a search result cannot tell them apart", clip(sp.title), strings.Join(others, ", ")))
		})
	}},
	{"duplicate-description", Warn, "no two pages share a description", func(_ *Plugin, s *site, report func(string, string)) {
		duplicates(s, func(sp sitePage) string { return sp.description }, func(sp sitePage, others []string) {
			report(sp.path, "the meta description is also that of "+strings.Join(others, ", "))
		})
	}},
	{"broken-link", Error, "every link on the site leads to something the build wrote", func(p *Plugin, s *site, report func(string, string)) {
		for _, sp := range s.pages {
			seen := make(map[string]bool)
			for _, href := range sp.links {
				target, ok := internalPath(sp.path, href)
				if !ok || seen[target] || p.ignored(target) {
					continue
				}
				seen[target] = true
				if !s.written[normalise(target)] {
					report(sp.path, fmt.Sprintf("links to %s, which the build did not write", target))
				}
			}
		}
	}},
}

// duplicates reports, for every page, the other pages sharing its value of key.
//
// The same page in another locale is not counted: a title left untranslated, or
// a name that is the same in every language, is one page in two languages, which
// hreflang already tells a search engine.
func duplicates(s *site, key func(sitePage) string, report func(sitePage, []string)) {
	by := make(map[string][]sitePage)
	for _, sp := range s.pages {
		if v := key(sp); v != "" {
			by[v] = append(by[v], sp)
		}
	}
	for _, sp := range s.pages {
		v := key(sp)
		if v == "" {
			continue
		}
		var others []string
		for _, other := range by[v] {
			if other.path == sp.path || (other.name == sp.name && other.locale != sp.locale) {
				continue
			}
			others = append(others, other.path)
		}
		if len(others) > 0 {
			report(sp, others)
		}
	}
}

// internalPath resolves href against the page at from, and returns its path when
// it points somewhere on this site.
func internalPath(from, href string) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return "", false
	}
	u, err := url.Parse(href)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return "", false // mailto:, tel:, javascript:, another host
	}
	base, err := url.Parse(from)
	if err != nil {
		return "", false
	}
	resolved := base.ResolveReference(u)
	if resolved.Path == "" {
		return "", false
	}
	return resolved.Path, true
}

func (p *Plugin) ignored(path string) bool {
	if strings.HasPrefix(path, "/_collage/") {
		return true
	}
	for _, prefix := range p.opts.IgnoreLinks {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
