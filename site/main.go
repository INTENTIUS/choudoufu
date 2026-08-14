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
)

//go:embed templates/*.html.tmpl
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

var docPages = []docPage{
	{
		Slug:        "migrate",
		NavLabel:    "Migrate an existing estate",
		Title:       "Migrate an existing estate",
		Section:     "Start Here",
		ContentFile: "migrate.md",
	},
	{
		Slug:        "start",
		NavLabel:    "Start a new estate",
		Title:       "Start a new estate",
		Section:     "Start Here",
		ContentFile: "start.md",
	},
	{
		Slug:       "faq",
		NavLabel:   "FAQ",
		Title:      "FAQ",
		Section:    "Start Here",
		SourcePath: "live/FAQ.md",
	},
	{
		Slug:       "live-markers",
		NavLabel:   "Live Markers",
		Title:      "Live Resource Markers",
		Section:    "Guides",
		SourcePath: "website/docs/language/live-markers.mdx",
		IsMDX:      true,
	},
	{
		Slug:       "aws",
		NavLabel:   "AWS",
		Title:      "AWS Provider Coverage",
		Section:    "Providers",
		SourcePath: "live/COVERAGE.md",
	},
	{
		Slug:       "markers",
		NavLabel:   "Marker Spec",
		Title:      "Marker Spec",
		Section:    "Reference",
		SourcePath: "live/MARKERS.md",
	},
	{
		Slug:       "limitations",
		NavLabel:   "Limitations",
		Title:      "Limitations",
		Section:    "Reference",
		SourcePath: "live/LIMITATIONS.md",
	},
	{
		Slug:       "survey",
		NavLabel:   "AWS Admission Survey",
		Title:      "AWS Admission Survey",
		Section:    "Operations",
		SourcePath: "live/SURVEY.md",
	},
	{
		Slug:       "receipts",
		NavLabel:   "Receipts",
		Title:      "Receipts",
		Section:    "Operations",
		SourcePath: "live/RECEIPTS.md",
	},
	{
		Slug:       "e2e",
		NavLabel:   "E2E Harness",
		Title:      "The e2e harness",
		Section:    "Operations",
		SourcePath: "live/e2e/README.md",
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

// landingData is what templates/landing.html.tmpl renders, then gets
// embedded as Content in layoutData.
type landingData struct {
	Sections []sidebarSection

	// UpstreamVersion is the OpenTofu release this fork is built on, read
	// from version/VERSION at build time so the landing page cannot drift
	// from the tree it was generated in.
	UpstreamVersion string
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

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
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

	// Landing page.
	var landingBody bytes.Buffer
	upstream, err := upstreamVersion(root)
	if err != nil {
		return err
	}
	if err := landingTmpl.Execute(&landingBody, landingData{Sections: buildSidebar(""), UpstreamVersion: upstream}); err != nil {
		return fmt.Errorf("rendering landing content: %w", err)
	}
	if err := writePage(out, "index.html", layoutTmpl, layoutData{
		Title:      "choudoufu",
		CSSVersion: cssVer,
		Content:    template.HTML(landingBody.String()), //nolint:gosec // fixed, locally-authored template
	}); err != nil {
		return err
	}

	// Doc pages.
	for _, p := range docPages {
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

		htmlOut, err := renderMarkdown(src)
		if err != nil {
			return fmt.Errorf("rendering %s: %w", srcPath, err)
		}

		if err := writePage(out, p.Slug+".html", layoutTmpl, layoutData{
			Title:      p.Title,
			CSSVersion: cssVer,
			Sidebar:    buildSidebar(p.Slug + ".html"),
			Content:    template.HTML(htmlOut), //nolint:gosec // rendered from repo-local trusted markdown
		}); err != nil {
			return err
		}
	}

	// The live-markers page was first published at stateless-mode.html;
	// keep a redirect stub at the old URL so existing links still land.
	redirect := []byte(`<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0; url=live-markers.html"><link rel="canonical" href="live-markers.html"><title>Live Resource Markers</title><p>Moved to <a href="live-markers.html">live-markers.html</a>.</p>` + "\n")
	if err := os.WriteFile(filepath.Join(out, "stateless-mode.html"), redirect, 0o644); err != nil {
		return fmt.Errorf("writing stateless-mode.html redirect: %w", err)
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
