// A collage plugin that checks the HTML a site renders — structure,
// accessibility, SEO, performance, and the links between pages — and reports what
// it finds as warnings and errors: over the page in development, in the report of
// a static build, which an error fails.
module github.com/Elagoht/collage-htmlcheck

go 1.26.0

require github.com/Elagoht/collage v0.50.0

require golang.org/x/net v0.59.0
