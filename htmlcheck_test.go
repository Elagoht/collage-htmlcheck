package htmlcheck_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	htmlcheck "github.com/Elagoht/collage-htmlcheck"
	"github.com/Elagoht/collage/pkg/collage"
	"github.com/Elagoht/collage/pkg/collagetest"
)

const clean = `<!doctype html><html lang="en"><head><title>A fine page</title>
<meta name="description" content="What the page is about.">
<script src="/app.js" defer></script></head>
<body><header><a href="/"><img src="/logo.svg" alt="Home" width="40" height="40"></a></header>
<main><h1>A fine page</h1><h2>Section</h2><h3>Detail</h3>
<form><label for="q">Search</label><input id="q" name="q"><label>Name <input name="n"></label>
<input type="hidden" name="t"><button type="submit">Go</button></form>
<a href="/about">About</a> <a href="/fine"><svg aria-label="Fine"></svg></a>
<button type="button" aria-label="Close"><svg></svg></button>
<img src="/deco.png" alt="" width="1" height="1"></main></body></html>`

const broken = `<html><head><script src="/block.js"></script></head><body>
<h2>No h1</h2><h4>Skipped</h4>
<div id="x"></div><div id="x"></div>
<img src="/a.png">
<form><input name="unlabelled"><button>Send</button></form>
<button></button>
<a href="/somewhere"></a>
<a href="/outer"><button type="button">inner</button></a>
<div tabindex="3">jumpy</div>
</body></html>`

// siteOptions are a site with the plugin and one page per entry, at /name, or
// at / for "index".
func siteOptions(dev bool, opts htmlcheck.Options, pages map[string]string) []collagetest.SiteOption {
	templates := map[string]string{}
	names := make([]string, 0, len(pages))
	for name, body := range pages {
		templates[name+".html"] = body
		names = append(names, name)
	}
	sort.Strings(names)
	var items []collage.Registrable
	for _, name := range names {
		path := "/" + name
		if name == "index" {
			path = "/"
		}
		items = append(items, collage.NewPage(name).WithContent(collage.NewFragment(name, name+".html").Build()).WithPath("en", path).Build())
	}
	return []collagetest.SiteOption{
		collagetest.WithoutHome(),
		collagetest.WithPlugins(htmlcheck.New(opts)),
		collagetest.WithTemplates(templates),
		collagetest.WithRegister(items...),
		collagetest.WithConfig(func(c *collage.Config) { c.DevMode = dev }),
	}
}

func app(t *testing.T, dev bool, opts htmlcheck.Options, pages map[string]string) *collage.App {
	t.Helper()
	return collagetest.NewSite(t, siteOptions(dev, opts, pages)...).App
}

// findings builds the site and returns its findings as "rule level path".
func findings(t *testing.T, a *collage.App) ([]collage.Finding, error) {
	t.Helper()
	report, err := collage.Build(context.Background(), a, collage.BuildOptions{OutDir: t.TempDir()})
	return report.Findings, err
}

func rules(fs []collage.Finding, path string) map[string]collage.FindingLevel {
	out := map[string]collage.FindingLevel{}
	for _, f := range fs {
		if f.Path == path {
			out[f.Rule] = f.Level
		}
	}
	return out
}

// A careful page has nothing to report.
func TestCleanPage(t *testing.T) {
	variant := func(title string) string {
		s := strings.Replace(clean, "A fine page</title>", title+"</title>", 1)
		return strings.Replace(s, "What the page is about.", "What "+title+" is about.", 1)
	}
	a := app(t, false, htmlcheck.Options{}, map[string]string{"index": clean, "about": variant("About"), "fine": variant("Fine")})
	fs, err := findings(t, a)
	for _, f := range fs {
		t.Errorf("unexpected finding: %s %s at %s: %s", f.Level, f.Rule, f.Path, f.Message)
	}
	if err != nil && !errors.Is(err, collage.ErrBuildFindings) {
		t.Fatal(err)
	}
}

// Each rule catches what it is for, at its default level.
func TestBrokenPage(t *testing.T) {
	a := app(t, false, htmlcheck.Options{}, map[string]string{"bad": broken})
	fs, err := findings(t, a)
	if !errors.Is(err, collage.ErrBuildFindings) {
		t.Fatalf("Build = %v, want ErrBuildFindings", err)
	}
	got := rules(fs, "/bad")
	want := map[string]collage.FindingLevel{
		"html-lang":              collage.FindingError,
		"title":                  collage.FindingError,
		"meta-description":       collage.FindingWarning,
		"one-h1":                 collage.FindingWarning,
		"heading-order":          collage.FindingWarning,
		"landmark-main":          collage.FindingWarning,
		"duplicate-id":           collage.FindingError,
		"img-alt":                collage.FindingError,
		"img-dimensions":         collage.FindingWarning,
		"input-label":            collage.FindingError,
		"button-text":            collage.FindingError,
		"button-type":            collage.FindingWarning,
		"link-text":              collage.FindingWarning,
		"nested-interactive":     collage.FindingError,
		"positive-tabindex":      collage.FindingWarning,
		"render-blocking-script": collage.FindingWarning,
		"broken-link":            collage.FindingError,
	}
	for rule, level := range want {
		if got[rule] != level {
			t.Errorf("%s: got level %v, want %v", rule, got[rule], level)
		}
	}
	for rule := range got {
		if _, ok := want[rule]; !ok {
			t.Errorf("unexpected rule %s", rule)
		}
	}
}

// Levels are the application's to change; a misspelt rule is refused, not ignored.
func TestLevels(t *testing.T) {
	a := app(t, false, htmlcheck.Options{Rules: map[string]htmlcheck.Level{
		"img-alt": htmlcheck.Warn, "heading-order": htmlcheck.Error, "img-dimensions": htmlcheck.Off,
	}}, map[string]string{"bad": broken})
	fs, _ := findings(t, a)
	got := rules(fs, "/bad")
	if got["img-alt"] != collage.FindingWarning || got["heading-order"] != collage.FindingError {
		t.Errorf("levels not applied: %v", got)
	}
	if _, ok := got["img-dimensions"]; ok {
		t.Error("an Off rule reported")
	}

	_, err := collagetest.TrySite(t, siteOptions(false, htmlcheck.Options{Rules: map[string]htmlcheck.Level{"img-alts": htmlcheck.Off}}, map[string]string{"index": clean})...)
	if err == nil || !strings.Contains(err.Error(), "img-alts") {
		t.Errorf("a misspelt rule started the application: %v", err)
	}
}

// In development the findings are over the page; a production server checks
// nothing.
func TestWhereItRuns(t *testing.T) {
	dev := app(t, true, htmlcheck.Options{}, map[string]string{"bad": broken})
	rec := httptest.NewRecorder()
	dev.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bad", nil))
	if body := rec.Body.String(); !strings.Contains(body, "collage-dev-overlay") || !strings.Contains(body, "img-alt") {
		t.Errorf("development page without the findings:\n%s", body)
	}
	prod := app(t, false, htmlcheck.Options{}, map[string]string{"bad": broken})
	rec = httptest.NewRecorder()
	prod.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bad", nil))
	if strings.Contains(rec.Body.String(), "img-alt") {
		t.Error("a production page carries findings")
	}
}

// Across the build: shared titles and descriptions, links to nothing — resolved
// relative links included, other hosts and ignored prefixes left alone.
func TestBuildRules(t *testing.T) {
	page := func(title, links string) string {
		return strings.Replace(strings.Replace(clean, "<title>A fine page</title>", "<title>"+title+"</title>", 1), "</main>", links+"</main>", 1)
	}
	a := app(t, false, htmlcheck.Options{IgnoreLinks: []string{"/api/"}}, map[string]string{
		"index": page("Same", `<a href="gone">x</a><a href="https://elsewhere.example/nope">x</a><a href="/api/v1">x</a><a href="mailto:a@b.c">x</a><a href="#top">x</a>`),
		"about": page("Same", ""),
		"fine":  page("Fine", `<a href="/about?x=1#y">x</a>`),
	})
	fs, _ := findings(t, a)
	var got []string
	for _, f := range fs {
		got = append(got, f.Rule+" "+f.Path)
	}
	sort.Strings(got)
	want := []string{"broken-link /", "duplicate-description /", "duplicate-description /about", "duplicate-description /fine", "duplicate-title /", "duplicate-title /about"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("findings:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRulesAreDocumented(t *testing.T) {
	for _, r := range htmlcheck.Rules() {
		if r.Doc == "" || (r.Default != htmlcheck.Warn && r.Default != htmlcheck.Error) {
			t.Errorf("rule %+v", r)
		}
	}
}

// A negative length or budget is an error, not the default; zero is the default.
func TestNegativeNumbers(t *testing.T) {
	for name, opts := range map[string]htmlcheck.Options{
		"titleMax":       {TitleMax: -1},
		"descriptionMax": {DescriptionMax: -1},
		"pageBudget":     {PageBudget: -1},
	} {
		_, err := collagetest.TrySite(t, siteOptions(false, opts, map[string]string{"index": clean})...)
		if err == nil || !strings.Contains(err.Error(), "htmlcheck: "+name) {
			t.Errorf("%s -1: err = %v, want an htmlcheck: %s error", name, err, name)
		}
	}
}

// inProduction is gone: the key now stops the app.
func TestInProductionRemoved(t *testing.T) {
	_, err := collagetest.TrySite(t, append(siteOptions(false, htmlcheck.Options{}, map[string]string{"index": clean}),
		collagetest.WithPluginConfig(htmlcheck.Name, `{"inProduction":true}`))...)
	if err == nil || !strings.Contains(err.Error(), `unknown key "inProduction"`) {
		t.Errorf("err = %v, want unknown key \"inProduction\"", err)
	}
}

// An ignoreLinks prefix matches with or without its trailing slash, at a
// segment boundary.
func TestIgnoreLinksSlash(t *testing.T) {
	links := `<a href="/api">x</a><a href="/api/v1">x</a><a href="/apiary">x</a>`
	for _, prefix := range []string{"/api", "/api/"} {
		a := app(t, false, htmlcheck.Options{IgnoreLinks: []string{prefix}}, map[string]string{
			"index": strings.Replace(clean, "</main>", links+"</main>", 1),
		})
		fs, _ := findings(t, a)
		var broken []string
		for _, f := range fs {
			if f.Rule == "broken-link" && strings.Contains(f.Message, "/api") {
				broken = append(broken, f.Message)
			}
		}
		if len(broken) != 1 || !strings.Contains(broken[0], "/apiary") {
			t.Errorf("ignoreLinks %q: broken links %q, want only /apiary", prefix, broken)
		}
	}
}
