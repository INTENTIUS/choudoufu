// Command site generates the static docs site for choudoufu: a landing
// page plus rendered pages for the fork-unique docs (the FAQ, live
// markers, AWS provider coverage, the marker spec, limitations, the
// admission survey, receipts, the e2e harness). It
// is its own Go
// module so the root module's go.mod/go.sum never change on its account.
//
// Usage:
//
//	cd site && go run . -out public/
package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

//go:embed templates/*.html.tmpl templates/pages/*.html.tmpl
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

//go:embed content/*.md
var contentFS embed.FS

// docPage is one page of the site. Its markdown comes from one of two
// places, and exactly one of ContentFile or SourcePath names it.
//
// ContentFile names a file under site/content/, written for this site and
// for no other reader, embedded into the generator binary. That is where
// pages aimed at the two user paths live.
//
// SourcePath names a file elsewhere in the repository, read in place. Those
// files are written for contributors and have their own job; this generator
// never edits or moves them.
type docPage struct {
	Slug        string // output basename, without .html
	NavLabel    string
	Title       string
	Section     string // sidebar group the page is listed under
	ContentFile string // basename under site/content/
	SourcePath  string // relative to the repo root
	IsMDX       bool   // needs frontmatter + admonition preprocessing

	// Template names a template under templates/pages/ instead of any
	// markdown source. It is for a page whose body is a rendering of a
	// committed artifact rather than prose, so its figures come from the
	// same JSON the landing charts read and the two can never disagree.
	Template string
}

// read returns the page's markdown, from whichever of the two sources it
// declares, and the location to name in an error.
func (p docPage) read(root string) ([]byte, string, error) {
	switch {
	case p.ContentFile != "" && p.SourcePath != "":
		return nil, "", fmt.Errorf("page %q sets both ContentFile and SourcePath", p.Slug)
	case p.ContentFile != "":
		where := "site/content/" + p.ContentFile
		data, err := contentFS.ReadFile("content/" + p.ContentFile)
		return data, where, err
	case p.SourcePath != "":
		where := filepath.Join(root, p.SourcePath)
		data, err := os.ReadFile(where)
		return data, where, err
	default:
		return nil, "", fmt.Errorf("page %q sets neither ContentFile nor SourcePath", p.Slug)
	}
}

// docPages is also the nav. Sections are grouped in declaration order, so the
// order here is the order a reader meets them: what the thing is, who may
// touch it, how far it goes, then how to use it.
var docPages = []docPage{
	{
		Slug:        "model",
		NavLabel:    "The three pieces",
		Title:       "The three pieces",
		Section:     "The model",
		ContentFile: "model.md",
	},
	{
		Slug:        "model-identity",
		NavLabel:    "Identity",
		Title:       "Identity",
		Section:     "The model",
		ContentFile: "model-identity.md",
	},
	{
		Slug:        "model-values",
		NavLabel:    "Values",
		Title:       "Values",
		Section:     "The model",
		ContentFile: "model-values.md",
	},
	{
		Slug:        "model-effects",
		NavLabel:    "Effects",
		Title:       "Effects",
		Section:     "The model",
		ContentFile: "model-effects.md",
	},
	{
		Slug:     "governance",
		NavLabel: "Scoping a role",
		Title:    "Scoping a role",
		Section:  "Governance",
		Template: "governance",
	},
	{
		Slug:     "progress",
		NavLabel: "How far it goes",
		Title:    "How far it goes",
		Section:  "Progress",
		Template: "progress",
	},
	{
		Slug:     "estates",
		NavLabel: "Estates",
		Title:    "Estates",
		Section:  "Progress",
		Template: "estates",
	},
	{
		Slug:        "compatibility",
		NavLabel:    "Will my config work?",
		Title:       "Will my config work?",
		Section:     "Use it",
		ContentFile: "compatibility.md",
	},
	{
		Slug:        "migrate",
		NavLabel:    "Migrate an existing estate",
		Title:       "Migrate an existing estate",
		Section:     "Use it",
		ContentFile: "migrate.md",
	},
	{
		Slug:        "start",
		NavLabel:    "Start a new estate",
		Title:       "Start a new estate",
		Section:     "Use it",
		ContentFile: "start.md",
	},
	{
		Slug:        "day2",
		NavLabel:    "Day-2 operations",
		Title:       "Day-2 operations",
		Section:     "Use it",
		ContentFile: "day2.md",
	},
	{
		Slug:        "storage",
		NavLabel:    "Where things are stored",
		Title:       "Where things are stored",
		Section:     "Use it",
		ContentFile: "storage.md",
	},
	{
		Slug:        "faq",
		NavLabel:    "Questions",
		Title:       "Questions",
		Section:     "Use it",
		ContentFile: "faq.md",
	},
	{
		Slug:        "reference",
		NavLabel:    "Reference",
		Title:       "Reference",
		Section:     "Use it",
		ContentFile: "reference.md",
	},
}

// navItem is one link in the sidebar or on the landing page. A Soon item
// has no page yet and renders greyed out.
type navItem struct {
	Href   string
	Label  string
	Active bool
	Soon   bool
}

// sidebarSection is one titled group of doc links in the sidebar.
type sidebarSection struct {
	Title string
	Items []navItem
}

// layoutData is what templates/layout.html.tmpl renders. A nil Sidebar
// renders the full-width landing layout; a non-nil one renders the
// two-column docs layout.
type layoutData struct {
	Title       string
	AssetPrefix string // always "" — every output file lives directly under -out
	CSSVersion  string // content hash of style.css, busts browser caches
	Sidebar     []sidebarSection
	Content     template.HTML

	// AssetVersion is a content hash of the logo and favicon files. The
	// stylesheet has had one of these for a while; the images did not, so a
	// returning visitor kept seeing the previous artwork out of cache long
	// after it was replaced. Favicons are the worst offender, since browsers
	// hold them well past an ordinary page reload.
	AssetVersion string
}

// cssVersion returns a short content hash of the embedded stylesheet so the
// layout can append it as a query string and browsers refetch on change.
func cssVersion() string {
	data, err := staticFS.ReadFile("static/style.css")
	if err != nil {
		return "0"
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:4])
}

// assetVersion hashes the logo and favicon files together, so retinting the
// artwork changes every image URL at once and no browser serves the old one
// from cache.
func assetVersion() string {
	h := sha256.New()
	for _, name := range []string{
		"static/favicon.ico",
		"static/choudoufu-favicon-16.png",
		"static/choudoufu-favicon-32.png",
		"static/choudoufu-favicon-48.png",
		"static/choudoufu-inline-64.png",
		"static/choudoufu-hero.png",
	} {
		data, err := staticFS.ReadFile(name)
		if err != nil {
			return "0"
		}
		h.Write(data)
	}
	return fmt.Sprintf("%x", h.Sum(nil)[:4])
}

// landingData is what templates/landing.html.tmpl renders, then gets
// embedded as Content in layoutData.
type landingData struct {
	Sections []sidebarSection

	// UpstreamVersion is the OpenTofu release this fork is built on, read
	// from version/VERSION at build time so the landing page cannot drift
	// from the tree it was generated in.
	UpstreamVersion string

	// AssetVersion busts image caches, same hash the layout uses.
	AssetVersion string

	// The three progress charts, each read from its own committed artifact
	// and each with its own denominator. They are shown stacked and never
	// combined: "passes lint", "survives a run" and "IAM can scope it" are
	// different questions and one score over all three would mean nothing.
	Lint     chart
	Crossing chart
	IAM      chart

	// Estates that have been taken through the crossing pipeline, best
	// first. The landing page shows the top few; estates.html shows all.
	Estates []estate
}

// TopEstates returns the first n estates, for the landing page's short list.
func (d landingData) TopEstates(n int) []estate {
	if len(d.Estates) < n {
		return d.Estates
	}
	return d.Estates[:n]
}

// upstreamVersion reads version/VERSION and strips the -dev suffix that
// marks an unreleased upstream tree, the same way the release workflow
// derives the number it writes into release notes.
func upstreamVersion(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "version", "VERSION"))
	if err != nil {
		return "", fmt.Errorf("reading version/VERSION: %w", err)
	}
	return strings.TrimSuffix(strings.TrimSpace(string(data)), "-dev"), nil
}

// Heading IDs are on so pages can deep-link to each other's sections, and
// so a reader can link someone else straight to the paragraph that answers
// them. Without this, every in-page anchor silently resolves to nowhere.
var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
)

// frontmatterRE strips a leading YAML frontmatter block ("---\n...\n---\n").
var frontmatterRE = regexp.MustCompile(`(?s)\A---\n.*?\n---\n`)

// admonitionRE matches a Docusaurus-style admonition block:
//
//	:::note
//	body
//	:::
//
// or
//
//	:::warning Title
//	body
//	:::
var admonitionRE = regexp.MustCompile(`(?s):::(note|warning)( [^\n]*)?\n(.*?)\n:::`)

// mdxLinkRewrites maps relative links to stock OpenTofu docs pages (used
// only inside live-markers.mdx) to their absolute opentofu.org URLs,
// since this site does not render the stock docs tree those links point
// into.
var mdxLinkRewrites = map[string]string{
	"(state/locking.mdx)":                   "(https://opentofu.org/docs/language/state/locking/)",
	"(state/workspaces.mdx)":                "(https://opentofu.org/docs/language/state/workspaces/)",
	"(settings/backends/configuration.mdx)": "(https://opentofu.org/docs/language/settings/backends/configuration/)",
}

func main() {
	root := flag.String("root", "..", "path to the repository root")
	out := flag.String("out", "public", "output directory for the generated site")
	flag.Parse()

	if err := run(*root, *out); err != nil {
		log.Fatal(err)
	}
}

func run(root, out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	if err := copyDiagrams(root, out); err != nil {
		return err
	}
	if err := copyStatic(out); err != nil {
		return fmt.Errorf("copying static assets: %w", err)
	}

	layoutTmpl, err := template.ParseFS(templatesFS, "templates/layout.html.tmpl")
	if err != nil {
		return fmt.Errorf("parsing layout template: %w", err)
	}
	landingTmpl, err := template.ParseFS(templatesFS, "templates/landing.html.tmpl")
	if err != nil {
		return fmt.Errorf("parsing landing template: %w", err)
	}

	cssVer := cssVersion()
	assetVer := assetVersion()

	// Landing page.
	var landingBody bytes.Buffer
	upstream, err := upstreamVersion(root)
	if err != nil {
		return err
	}
	// The three progress charts. A missing or malformed artifact is fatal:
	// a site that silently drops a chart would read as "no limits here",
	// which is the one impression this project must never give.
	lint, err := loadLintLadder(root)
	if err != nil {
		return err
	}
	crossing, estates, err := loadCrossingLadder(root)
	if err != nil {
		return err
	}
	iam, err := loadIAMReach(root)
	if err != nil {
		return err
	}
	if err := landingTmpl.Execute(&landingBody, landingData{
		Sections:        buildSidebar(""),
		UpstreamVersion: upstream,
		AssetVersion:    assetVer,
		Lint:            lint,
		Crossing:        crossing,
		IAM:             iam,
		Estates:         estates,
	}); err != nil {
		return fmt.Errorf("rendering landing content: %w", err)
	}
	if err := writePage(out, "index.html", layoutTmpl, layoutData{
		Title:        "choudoufu",
		CSSVersion:   cssVer,
		AssetVersion: assetVer,
		Content:      template.HTML(landingBody.String()), //nolint:gosec // fixed, locally-authored template
	}); err != nil {
		return err
	}

	// Pages rendered from an artifact rather than from prose. Their content
	// is a template over the same JSON the landing charts read, so a figure
	// on a destination page can never disagree with the headline that links
	// to it.
	dataPages, err := loadDataPages(root)
	if err != nil {
		return err
	}
	dataTmpl, err := template.ParseFS(templatesFS, "templates/pages/*.html.tmpl")
	if err != nil {
		return fmt.Errorf("parsing data-page templates: %w", err)
	}

	// Doc pages.
	for _, p := range docPages {
		var htmlOut string

		if p.Template != "" {
			var body bytes.Buffer
			if err := dataTmpl.ExecuteTemplate(&body, p.Template, dataPages); err != nil {
				return fmt.Errorf("rendering %s: %w", p.Template, err)
			}
			htmlOut = body.String()
		} else {
			raw, srcPath, err := p.read(root)
			switch {
			case err != nil && srcPath == "":
				// The page is misdeclared; there is no location to name.
				return err
			case err != nil:
				return fmt.Errorf("reading %s: %w", srcPath, err)
			}

			src := string(raw)
			if p.IsMDX {
				src = preprocessMDX(src)
			}

			htmlOut, err = renderMarkdown(src)
			if err != nil {
				return fmt.Errorf("rendering %s: %w", srcPath, err)
			}
		}

		if err := writePage(out, p.Slug+".html", layoutTmpl, layoutData{
			Title:        p.Title,
			CSSVersion:   cssVer,
			AssetVersion: assetVer,
			Sidebar:      buildSidebar(p.Slug + ".html"),
			Content:      template.HTML(htmlOut), //nolint:gosec // rendered from repo-local trusted markdown and templates
		}); err != nil {
			return err
		}
	}

	for from, to := range legacyRedirects {
		if err := writeRedirect(out, from, to); err != nil {
			return err
		}
	}

	return nil
}

// legacyRedirects maps a previously published URL to the page that
// succeeded it. Every one of these was live at some point, so dropping them
// would break inbound links and search results rather than merely tidying
// the output directory.
//
// The repo-doc pages (aws, markers, limitations, survey, receipts, e2e) are
// no longer rendered here at all: they are contributor material, and issue
// #79 moved them back to the repository with the Reference page as the
// index into them.
var legacyRedirects = map[string]string{
	"stateless-mode.html": "index.html",
	"live-markers.html":   "index.html",
	"aws.html":            "reference.html",
	"markers.html":        "reference.html",
	"limitations.html":    "reference.html",
	"survey.html":         "reference.html",
	"receipts.html":       "reference.html",
	"e2e.html":            "reference.html",
}

// writeRedirect emits a meta-refresh stub at from pointing at to.
func writeRedirect(out, from, to string) error {
	body := fmt.Sprintf(
		`<!doctype html><meta charset="utf-8">`+
			`<meta http-equiv="refresh" content="0; url=%[1]s">`+
			`<link rel="canonical" href="%[1]s">`+
			`<title>Moved</title>`+
			`<p>This page moved to <a href="%[1]s">%[1]s</a>.</p>`+"\n", to)
	if err := os.WriteFile(filepath.Join(out, from), []byte(body), 0o644); err != nil {
		return fmt.Errorf("writing %s redirect: %w", from, err)
	}
	return nil
}

// preprocessMDX strips YAML frontmatter, rewrites the handful of relative
// links to stock OpenTofu docs into absolute opentofu.org URLs, and turns
// :::note / :::warning admonitions into placeholder paragraphs that
// renderMarkdown swaps for styled HTML after the surrounding markdown has
// been rendered normally.
func preprocessMDX(src string) string {
	src = frontmatterRE.ReplaceAllString(src, "")

	for from, to := range mdxLinkRewrites {
		src = strings.ReplaceAll(src, from, to)
	}

	return src
}

// admonition holds one extracted :::note/:::warning block, rendered
// separately from the surrounding document.
type admonition struct {
	kind  string // "note" or "warning"
	title string
	body  string
}

// renderMarkdown converts markdown (already MDX-preprocessed, if
// applicable) to HTML. Admonition blocks are pulled out before the main
// render, rendered independently, and spliced back in as styled <div>s —
// this avoids needing goldmark's unsafe raw-HTML mode anywhere.
func renderMarkdown(src string) (string, error) {
	var admonitions []admonition

	src = admonitionRE.ReplaceAllStringFunc(src, func(block string) string {
		m := admonitionRE.FindStringSubmatch(block)
		a := admonition{
			kind:  m[1],
			title: strings.TrimSpace(m[2]),
			body:  m[3],
		}
		idx := len(admonitions)
		admonitions = append(admonitions, a)
		return fmt.Sprintf("\n\nADMONITION-PLACEHOLDER-%d\n\n", idx)
	})

	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	out := buf.String()

	for i, a := range admonitions {
		var bodyBuf bytes.Buffer
		if err := md.Convert([]byte(a.body), &bodyBuf); err != nil {
			return "", err
		}

		title := a.title
		if title == "" {
			if a.kind == "warning" {
				title = "Warning"
			} else {
				title = "Note"
			}
		}

		div := fmt.Sprintf(
			"<div class=\"admonition %s\"><p class=\"admonition-title\">%s</p>%s</div>",
			a.kind, template.HTMLEscapeString(title), bodyBuf.String(),
		)

		placeholder := fmt.Sprintf("<p>ADMONITION-PLACEHOLDER-%d</p>", i)
		out = strings.Replace(out, placeholder, div, 1)
	}

	return out, nil
}

func writePage(out, filename string, tmpl *template.Template, data layoutData) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("rendering %s: %w", filename, err)
	}
	dest := filepath.Join(out, filename)
	if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", dest, err)
	}
	return nil
}

// buildSidebar groups the doc pages by section, in declaration order,
// marking current as active by output filename. Pass "" for no active
// page (the landing page's docs listing).
func buildSidebar(current string) []sidebarSection {
	var sections []sidebarSection
	for _, p := range docPages {
		href := p.Slug + ".html"
		item := navItem{Href: href, Label: p.NavLabel, Active: href == current}
		if n := len(sections); n > 0 && sections[n-1].Title == p.Section {
			sections[n-1].Items = append(sections[n-1].Items, item)
			continue
		}
		sections = append(sections, sidebarSection{Title: p.Section, Items: []navItem{item}})
	}
	return sections
}

// copyDiagrams writes the hand-authored SVGs from docs/diagrams into out.
//
// They are read from the repository rather than embedded so there is one copy
// of each diagram, editable in place and diffable in review. A page
// referencing a diagram that is not there is a broken image nobody notices, so
// a missing directory is an error rather than a skip.
func copyDiagrams(root, out string) error {
	dir := filepath.Join(root, "docs", "diagrams")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".svg") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, e.Name()), data, 0o644); err != nil {
			return err
		}
		n++
	}
	if n == 0 {
		return fmt.Errorf("%s holds no .svg diagrams", dir)
	}
	return nil
}

// copyStatic writes every file embedded under static/ into out.
func copyStatic(out string) error {
	entries, err := staticFS.ReadDir("static")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := staticFS.ReadFile("static/" + e.Name())
		if err != nil {
			return err
		}
		dest := filepath.Join(out, e.Name())
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
