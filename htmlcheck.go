// Package htmlcheck is a collage plugin that checks the HTML a site renders and
// reports what it finds as warnings and errors.
//
//	app, err := collage.New(&collage.Config{
//		Plugins: []collage.Plugin{htmlcheck.New(htmlcheck.Options{})},
//	})
//
// Every page is checked as it renders — its structure, its accessibility, what a
// search engine reads from it, what slows it down — and a static build is checked
// as a whole once it is written: two pages with one title, a link to a page the
// build did not write. What is found goes where collage puts findings: over the
// page in development, into the report of a static build, and an error-level
// finding fails the build. A production server is left alone.
//
// Each rule has a level, "off", "warn" or "error", and every level can be changed:
//
//	htmlcheck.New(htmlcheck.Options{Rules: map[string]htmlcheck.Level{
//		"img-dimensions": htmlcheck.Off,
//		"heading-order":  htmlcheck.Error,
//	}})
package htmlcheck

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Elagoht/collage/pkg/collage"
	"golang.org/x/net/html"
)

// Name is the plugin's name, and the key its configuration is found under.
const Name = "elagoht/htmlcheck"

// Level is how a rule reports.
type Level string

const (
	// Off disables a rule.
	Off Level = "off"
	// Warn reports a warning: shown, listed, and nothing fails.
	Warn Level = "warn"
	// Error reports an error: shown, listed, and it fails a static build.
	Error Level = "error"
)

// Options configures the plugin.
type Options struct {
	// Rules changes rules' levels, by name. A rule not named keeps its default;
	// see Rules() for every rule and its default.
	Rules map[string]Level `json:"rules"`
	// TitleMax and DescriptionMax are the lengths, in characters, past which a
	// search engine cuts a title or a description short. Default 60 and 160.
	TitleMax       int `json:"titleMax"`
	DescriptionMax int `json:"descriptionMax"`
	// PageBudget is the size in bytes past which a page is reported by
	// "page-size". Zero leaves the rule silent.
	PageBudget int `json:"pageBudget"`
	// IgnoreLinks are path prefixes "broken-link" does not check: a route served
	// by a handler of your own, an API.
	IgnoreLinks []string `json:"ignoreLinks"`
	// InProduction checks pages on a production server too. Its findings go
	// nowhere there — collage shows them only in development and in builds — so
	// this is only for a check of your own reading them in a hook.
	InProduction bool `json:"inProduction"`
}

// RuleInfo describes one rule.
type RuleInfo struct {
	Name    string
	Default Level
	// Build reports that the rule checks a static build as a whole, rather than
	// each page as it renders.
	Build bool
	Doc   string
}

// Rules lists every rule the plugin has, in order.
func Rules() []RuleInfo {
	out := make([]RuleInfo, 0, len(pageRules)+len(buildRules))
	for _, r := range pageRules {
		out = append(out, RuleInfo{Name: r.name, Default: r.level, Doc: r.doc})
	}
	for _, r := range buildRules {
		out = append(out, RuleInfo{Name: r.name, Default: r.level, Build: true, Doc: r.doc})
	}
	return out
}

// Plugin checks pages.
type Plugin struct {
	opts Options
	dev  bool
}

// New returns a plugin with opts as its starting point, which the application's
// own configuration is then decoded over.
func New(opts Options) *Plugin { return &Plugin{opts: opts} }

func (p *Plugin) Name() string                   { return Name }
func (p *Plugin) Version() string                { return "0.1.2" }
func (p *Plugin) Shutdown(context.Context) error { return nil }

// Init reads the configuration and refuses a rule or level it does not know, so a
// misspelt rule is not a rule silently left on.
func (p *Plugin) Init(_ context.Context, host collage.Host) error {
	if err := host.Config(&p.opts); err != nil {
		return err
	}
	p.dev = host.DevMode()
	if p.opts.TitleMax <= 0 {
		p.opts.TitleMax = 60
	}
	if p.opts.DescriptionMax <= 0 {
		p.opts.DescriptionMax = 160
	}
	known := make(map[string]bool)
	for _, r := range Rules() {
		known[r.Name] = true
	}
	for name, level := range p.opts.Rules {
		if !known[name] {
			return fmt.Errorf("htmlcheck: no rule named %q", name)
		}
		if level != Off && level != Warn && level != Error {
			return fmt.Errorf("htmlcheck: rule %q: level %q is none of off, warn, error", name, level)
		}
	}
	return nil
}

func (p *Plugin) level(name string, def Level) Level {
	if l, ok := p.opts.Rules[name]; ok {
		return l
	}
	return def
}

// OnAfterRender checks one page.
func (p *Plugin) OnAfterRender(_ context.Context, ev *collage.AfterRenderEvent) error {
	if !ev.Static && !p.dev && !p.opts.InProduction {
		return nil
	}
	doc, err := html.Parse(bytes.NewReader(ev.HTML))
	if err != nil {
		// The tokenizer recovers from anything; an error here is a reader
		// failing, which a byte slice does not.
		return nil
	}
	page := newPage(doc, len(ev.HTML))
	for _, r := range pageRules {
		level := p.level(r.name, r.level)
		if level == Off {
			continue
		}
		r.check(p, page, func(message string) {
			if level == Error {
				ev.Error(r.name, message)
			} else {
				ev.Warn(r.name, message)
			}
		})
	}
	return nil
}

// OnBuildFinished checks the build as a whole.
func (p *Plugin) OnBuildFinished(_ context.Context, ev *collage.BuildFinishedEvent) error {
	site, err := readSite(ev.Files)
	if err != nil {
		return err
	}
	for _, r := range buildRules {
		level := p.level(r.name, r.level)
		if level == Off {
			continue
		}
		r.check(p, site, func(path, message string) {
			if level == Error {
				ev.Error(path, r.name, message)
			} else {
				ev.Warn(path, r.name, message)
			}
		})
	}
	return nil
}

// --- the parsed page -------------------------------------------------------------

// page is one parsed page, with what several rules look up indexed once.
type page struct {
	root  *html.Node
	size  int
	all   []*html.Node // every element, in document order
	byID  map[string][]*html.Node
	label map[string]bool // ids a <label for> names
}

func newPage(root *html.Node, size int) *page {
	pg := &page{root: root, size: size, byID: make(map[string][]*html.Node), label: make(map[string]bool)}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			pg.all = append(pg.all, n)
			if id := attr(n, "id"); id != "" {
				pg.byID[id] = append(pg.byID[id], n)
			}
			if n.Data == "label" {
				if f := attr(n, "for"); f != "" {
					pg.label[f] = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return pg
}

func (pg *page) elements(tag string) []*html.Node {
	var out []*html.Node
	for _, n := range pg.all {
		if n.Data == tag {
			out = append(out, n)
		}
	}
	return out
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

// text is n's text content, whitespace collapsed.
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && (c.Data == "script" || c.Data == "style" || c.Data == "template") {
				continue
			}
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// accessibleName approximates the name assistive technology announces for n: its
// ARIA label, the text it holds, the alt of an image inside it, its title.
func accessibleName(pg *page, n *html.Node) string {
	if v := strings.TrimSpace(attr(n, "aria-label")); v != "" {
		return v
	}
	if ids := strings.Fields(attr(n, "aria-labelledby")); len(ids) > 0 {
		var parts []string
		for _, id := range ids {
			for _, target := range pg.byID[id] {
				parts = append(parts, text(target))
			}
		}
		if name := strings.TrimSpace(strings.Join(parts, " ")); name != "" {
			return name
		}
	}
	if t := text(n); t != "" {
		return t
	}
	var alt string
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		if alt != "" {
			return
		}
		if c.Type == html.ElementNode {
			if (c.Data == "img" || c.Data == "area") && strings.TrimSpace(attr(c, "alt")) != "" {
				alt = strings.TrimSpace(attr(c, "alt"))
				return
			}
			if c.Data == "svg" {
				for t := c.FirstChild; t != nil; t = t.NextSibling {
					if t.Type == html.ElementNode && t.Data == "title" && text(t) != "" {
						alt = text(t)
						return
					}
				}
				if v := strings.TrimSpace(attr(c, "aria-label")); v != "" {
					alt = v
					return
				}
			}
		}
		for d := c.FirstChild; d != nil; d = d.NextSibling {
			walk(d)
		}
	}
	walk(n)
	if alt != "" {
		return alt
	}
	return strings.TrimSpace(attr(n, "title"))
}

// describe renders n's opening tag, short, so a message can say which element.
func describe(n *html.Node) string {
	var b strings.Builder
	b.WriteString("<" + n.Data)
	for _, a := range n.Attr {
		switch a.Key {
		case "id", "class", "name", "type", "href", "src", "for", "role":
			v := a.Val
			if len(v) > 40 {
				v = v[:37] + "..."
			}
			fmt.Fprintf(&b, " %s=%q", a.Key, v)
		}
	}
	b.WriteString(">")
	return b.String()
}

func ancestor(n *html.Node, match func(*html.Node) bool) *html.Node {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && match(p) {
			return p
		}
	}
	return nil
}

func interactive(n *html.Node) bool {
	switch n.Data {
	case "a":
		return hasAttr(n, "href")
	case "button", "select", "textarea", "details", "embed", "iframe":
		return true
	case "input":
		return attr(n, "type") != "hidden"
	}
	return false
}

// --- rules that check one page ---------------------------------------------------

type pageRule struct {
	name  string
	level Level
	doc   string
	check func(p *Plugin, pg *page, report func(message string))
}

var pageRules = []pageRule{
	{"html-lang", Error, "the <html> element names the page's language", func(_ *Plugin, pg *page, report func(string)) {
		for _, n := range pg.elements("html") {
			if strings.TrimSpace(attr(n, "lang")) == "" {
				report("<html> has no lang: a screen reader cannot tell which language to read the page in")
			}
		}
	}},
	{"title", Error, "the page has a non-empty <title>", func(_ *Plugin, pg *page, report func(string)) {
		titles := pg.elements("title")
		titles = slicesFilter(titles, func(n *html.Node) bool { return ancestor(n, func(a *html.Node) bool { return a.Data == "svg" }) == nil })
		switch {
		case len(titles) == 0:
			report("the page has no <title>")
		case len(titles) > 1:
			report(fmt.Sprintf("the page has %d <title> elements; the browser uses the first", len(titles)))
		case text(titles[0]) == "":
			report("the page's <title> is empty")
		}
	}},
	{"title-length", Warn, "the title fits a search result", func(p *Plugin, pg *page, report func(string)) {
		for _, n := range pg.elements("title") {
			if ancestor(n, func(a *html.Node) bool { return a.Data == "svg" }) != nil {
				continue
			}
			if l := len([]rune(text(n))); l > p.opts.TitleMax {
				report(fmt.Sprintf("the title is %d characters; a search result shows about %d", l, p.opts.TitleMax))
			}
			return
		}
	}},
	{"meta-description", Warn, "the page has a meta description", func(_ *Plugin, pg *page, report func(string)) {
		if description(pg) == nil {
			report(`the page has no <meta name="description">: a search engine will pick a sentence of its own`)
		} else if strings.TrimSpace(attr(description(pg), "content")) == "" {
			report("the page's meta description is empty")
		}
	}},
	{"description-length", Warn, "the description fits a search result", func(p *Plugin, pg *page, report func(string)) {
		if d := description(pg); d != nil {
			if l := len([]rune(strings.TrimSpace(attr(d, "content")))); l > p.opts.DescriptionMax {
				report(fmt.Sprintf("the meta description is %d characters; a search result shows about %d", l, p.opts.DescriptionMax))
			}
		}
	}},
	{"one-h1", Warn, "the page has exactly one <h1>", func(_ *Plugin, pg *page, report func(string)) {
		switch n := len(pg.elements("h1")); {
		case n == 0:
			report("the page has no <h1>: nothing says what it is about")
		case n > 1:
			report(fmt.Sprintf("the page has %d <h1> elements; one says what the page is about", n))
		}
	}},
	{"heading-order", Warn, "heading levels do not skip", func(_ *Plugin, pg *page, report func(string)) {
		last := 0
		for _, n := range pg.all {
			if len(n.Data) != 2 || n.Data[0] != 'h' || n.Data[1] < '1' || n.Data[1] > '6' {
				continue
			}
			level := int(n.Data[1] - '0')
			if last > 0 && level > last+1 {
				report(fmt.Sprintf("<h%d> %q follows <h%d>: a level is skipped, and the outline has a hole", level, clip(text(n)), last))
			}
			last = level
		}
	}},
	{"landmark-main", Warn, "the page has one <main>", func(_ *Plugin, pg *page, report func(string)) {
		mains := len(pg.elements("main"))
		for _, n := range pg.all {
			if attr(n, "role") == "main" && n.Data != "main" {
				mains++
			}
		}
		switch {
		case mains == 0:
			report("the page has no <main>: a screen reader cannot skip straight to its content")
		case mains > 1:
			report(fmt.Sprintf("the page has %d main landmarks; it should have one", mains))
		}
	}},
	{"duplicate-id", Error, "every id is unique", func(_ *Plugin, pg *page, report func(string)) {
		ids := make([]string, 0, len(pg.byID))
		for id, nodes := range pg.byID {
			if len(nodes) > 1 {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			report(fmt.Sprintf("id %q is on %d elements; a label, a link or a script finds only the first", id, len(pg.byID[id])))
		}
	}},
	{"img-alt", Error, "every image has alt text, or alt=\"\" when it is decoration", func(_ *Plugin, pg *page, report func(string)) {
		for _, n := range pg.elements("img") {
			if !hasAttr(n, "alt") && attr(n, "role") != "presentation" && attr(n, "aria-hidden") != "true" {
				report(describe(n) + " has no alt: give it one, or alt=\"\" if it is decoration")
			}
		}
	}},
	{"img-dimensions", Warn, "images declare their width and height", func(_ *Plugin, pg *page, report func(string)) {
		for _, n := range pg.elements("img") {
			if !hasAttr(n, "width") || !hasAttr(n, "height") {
				report(describe(n) + " has no width and height: the page jumps when it loads")
			}
		}
	}},
	{"input-label", Error, "every form field has a label", func(_ *Plugin, pg *page, report func(string)) {
		for _, n := range pg.all {
			switch n.Data {
			case "input":
				switch attr(n, "type") {
				case "hidden", "submit", "reset", "button", "image":
					continue
				}
			case "select", "textarea":
			default:
				continue
			}
			if strings.TrimSpace(attr(n, "aria-label")) != "" || attr(n, "aria-labelledby") != "" || strings.TrimSpace(attr(n, "title")) != "" {
				continue
			}
			if id := attr(n, "id"); id != "" && pg.label[id] {
				continue
			}
			if ancestor(n, func(a *html.Node) bool { return a.Data == "label" }) != nil {
				continue
			}
			report(describe(n) + " has no label: a <label for>, a wrapping <label> or aria-label")
		}
	}},
	{"button-text", Error, "every button has a name", func(_ *Plugin, pg *page, report func(string)) {
		for _, n := range pg.all {
			if n.Data == "button" || attr(n, "role") == "button" {
				if accessibleName(pg, n) == "" {
					report(describe(n) + " has no text or aria-label: a screen reader announces only \"button\"")
				}
			}
		}
	}},
	{"button-type", Warn, "a button in a form says what it does", func(_ *Plugin, pg *page, report func(string)) {
		for _, n := range pg.elements("button") {
			if !hasAttr(n, "type") && ancestor(n, func(a *html.Node) bool { return a.Data == "form" }) != nil {
				report(describe(n) + " in a form has no type: it submits the form, which a button meant to do something else does by surprise")
			}
		}
	}},
	{"link-text", Warn, "every link has a name", func(_ *Plugin, pg *page, report func(string)) {
		for _, n := range pg.elements("a") {
			if hasAttr(n, "href") && accessibleName(pg, n) == "" {
				report(describe(n) + " has no text, alt or aria-label: a screen reader reads out its URL")
			}
		}
	}},
	{"nested-interactive", Error, "no link or button inside another", func(_ *Plugin, pg *page, report func(string)) {
		for _, n := range pg.all {
			if !interactive(n) {
				continue
			}
			if outer := ancestor(n, func(a *html.Node) bool { return (a.Data == "a" && hasAttr(a, "href")) || a.Data == "button" }); outer != nil {
				report(describe(n) + " is inside " + describe(outer) + ": which one a click means is up to the browser")
			}
		}
	}},
	{"positive-tabindex", Warn, "tabindex is 0 or -1", func(_ *Plugin, pg *page, report func(string)) {
		for _, n := range pg.all {
			if v := strings.TrimSpace(attr(n, "tabindex")); v != "" && v != "0" && !strings.HasPrefix(v, "-") {
				report(describe(n) + " has tabindex=" + v + ": it jumps the keyboard order, which rarely stays right")
			}
		}
	}},
	{"render-blocking-script", Warn, "scripts in the head do not block rendering", func(_ *Plugin, pg *page, report func(string)) {
		for _, n := range pg.elements("script") {
			if attr(n, "src") == "" || hasAttr(n, "async") || hasAttr(n, "defer") || attr(n, "type") == "module" {
				continue
			}
			if ancestor(n, func(a *html.Node) bool { return a.Data == "head" }) != nil {
				report(describe(n) + " in the head has neither defer nor async: nothing renders until it has loaded and run")
			}
		}
	}},
	{"page-size", Warn, "the page is within its budget (Options.PageBudget)", func(p *Plugin, pg *page, report func(string)) {
		if p.opts.PageBudget > 0 && pg.size > p.opts.PageBudget {
			report(fmt.Sprintf("the page is %d bytes, over its budget of %d", pg.size, p.opts.PageBudget))
		}
	}},
}

func description(pg *page) *html.Node {
	for _, n := range pg.elements("meta") {
		if strings.EqualFold(attr(n, "name"), "description") {
			return n
		}
	}
	return nil
}

func clip(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:37]) + "..."
	}
	return s
}

func slicesFilter(nodes []*html.Node, keep func(*html.Node) bool) []*html.Node {
	out := nodes[:0:0]
	for _, n := range nodes {
		if keep(n) {
			out = append(out, n)
		}
	}
	return out
}
