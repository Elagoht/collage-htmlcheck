# elagoht/htmlcheck

A collage plugin that checks the HTML a site renders — structure, accessibility,
what a search engine reads, what slows a page down, and the links between pages —
and reports what it finds as warnings and errors.

```go
app, err := collage.New(&collage.Config{
	Plugins: []collage.Plugin{htmlcheck.New(htmlcheck.Options{})},
})
```

Requires collage v0.22.0 or later.

## Where it runs

- **In development** every page is checked as it renders, and what is found is
  shown over the page, in the panel collage uses for a failed fragment. The page is
  served as it is.
- **In a static build** (`collage export`) every page is checked, then the build as
  a whole: titles two pages share, links to pages the build did not write. The
  findings are listed in the build's report under the page they are about, and an
  error fails the build.
- **On a production server** nothing is checked. The build already knew.

```
◆ 3 findings
  /about
    error img-alt  <img src="/team.jpg"> has no alt: give it one, or alt="" if it is decoration  elagoht/htmlcheck
    warning heading-order  <h4> "Our values" follows <h2>: a level is skipped, and the outline has a hole  elagoht/htmlcheck
  /
    error broken-link  links to /pricing, which the build did not write  elagoht/htmlcheck
```

## Rules

| Rule | Default | Checks |
| --- | --- | --- |
| `html-lang` | error | `<html>` names the page's language |
| `title` | error | the page has one non-empty `<title>` |
| `title-length` | warn | the title fits a search result (`titleMax`, 60) |
| `meta-description` | warn | the page has a meta description |
| `description-length` | warn | it fits a search result (`descriptionMax`, 160) |
| `one-h1` | warn | exactly one `<h1>` |
| `heading-order` | warn | heading levels do not skip |
| `landmark-main` | warn | one `<main>` |
| `duplicate-id` | error | every `id` is unique |
| `img-alt` | error | every image has `alt`, or `alt=""` when it is decoration |
| `img-dimensions` | warn | images declare `width` and `height`, so the page does not jump |
| `input-label` | error | every form field has a label |
| `button-text` | error | every button has a name |
| `button-type` | warn | a button in a form says what it does |
| `link-text` | warn | every link has a name |
| `nested-interactive` | error | no link or button inside another |
| `positive-tabindex` | warn | `tabindex` is 0 or -1 |
| `render-blocking-script` | warn | a script in the head has `defer`, `async` or `type="module"` |
| `page-size` | warn | the page is within `pageBudget` bytes; silent while it is 0 |
| `duplicate-title` | warn | *build:* no two pages share a title |
| `duplicate-description` | warn | *build:* no two pages share a description |
| `broken-link` | error | *build:* every link on the site leads to something the build wrote |

A name is what assistive technology would announce: `aria-label`,
`aria-labelledby`, the text inside, the `alt` of an image or the title of an SVG
inside, the `title` attribute. The same page in two locales may share a title and a
description. `broken-link` resolves relative links, ignores other hosts, `mailto:`
and fragments, and skips the prefixes in `ignoreLinks` — a route a handler of your
own answers.

`htmlcheck.Rules()` lists every rule with its default.

## Configuration

Every level can be changed, and a rule can be turned off:

```go
htmlcheck.New(htmlcheck.Options{
	Rules: map[string]htmlcheck.Level{
		"img-dimensions": htmlcheck.Off,
		"heading-order":  htmlcheck.Error,
	},
	PageBudget:  200_000,
	IgnoreLinks: []string{"/api/"},
})
```

```json
{
  "elagoht/htmlcheck": {
    "rules": { "img-dimensions": "off", "heading-order": "error" },
    "titleMax": 60,
    "descriptionMax": 160,
    "pageBudget": 200000,
    "ignoreLinks": ["/api/"]
  }
}
```

A rule name the plugin does not know stops the application from starting, so a
misspelt rule is not one silently left on.
