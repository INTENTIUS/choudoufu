// Command site generates the static docs site for choudoufu: a landing
// page plus rendered pages for the fork-unique docs (the FAQ, live
// markers, the marker spec, limitations, the admission survey, receipts,
// the e2e harness). It
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

// docPage is one fork-unique doc rendered from a source markdown/mdx file
// that lives elsewhere in the repository. Source files are read in place;
// this generator never edits or moves them.
type docPage struct {
	Slug       string // output basename, without .html
	NavLabel   string
	Title      string
	SourcePath string // relative to the repo root
	IsMDX      bool   // needs frontmatter + admonition preprocessing
}

var docPages = []docPage{
	{
		Slug:       "faq",
		NavLabel:   "FAQ",
		Title:      "FAQ",
		SourcePath: "stateless/FAQ.md",
	},
	{
		Slug:       "live-markers",
		NavLabel:   "Live Markers",
		Title:      "Live Resource Markers",
		SourcePath: "website/docs/language/stateless-mode.mdx",
		IsMDX:      true,
	},
	{
		Slug:       "markers",
		NavLabel:   "Markers",
		Title:      "Marker Spec",
		SourcePath: "stateless/MARKERS.md",
	},
	{
		Slug:       "limitations",
		NavLabel:   "Limitations",
		Title:      "Limitations",
		SourcePath: "stateless/LIMITATIONS.md",
	},
	{
		Slug:       "survey",
		NavLabel:   "Survey",
		Title:      "AWS Admission Survey",
		SourcePath: "stateless/SURVEY.md",
	},
	{
		Slug:       "receipts",
		NavLabel:   "Receipts",
		Title:      "Receipts",
		SourcePath: "stateless/RECEIPTS.md",
	},
	{
		Slug:       "e2e",
		NavLabel:   "E2E Harness",
		Title:      "The e2e harness",
		SourcePath: "stateless/e2e/README.md",
	},
}

// navItem is one entry in the site header nav.
type navItem struct {
	Href   string
	Label  string
	Active bool
}

// layoutData is what templates/layout.html.tmpl renders.
type layoutData struct {
	Title       string
	AssetPrefix string // always "" — every output file lives directly under -out
	CSSVersion  string // content hash of style.css, busts browser caches
	Nav         []navItem
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
	Pages []navItem
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
// only inside stateless-mode.mdx) to their absolute opentofu.org URLs,
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
	if err := landingTmpl.Execute(&landingBody, landingData{Pages: docNavItems()}); err != nil {
		return fmt.Errorf("rendering landing content: %w", err)
	}
	if err := writePage(out, "index.html", layoutTmpl, layoutData{
		Title:      "choudoufu",
		CSSVersion: cssVer,
		Nav:        buildNav("index.html"),
		Content:    template.HTML(landingBody.String()), //nolint:gosec // fixed, locally-authored template
	}); err != nil {
		return err
	}

	// Doc pages.
	for _, p := range docPages {
		srcPath := filepath.Join(root, p.SourcePath)
		raw, err := os.ReadFile(srcPath)
		if err != nil {
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
			Nav:        buildNav(p.Slug + ".html"),
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

// docNavItems is the list of doc pages as landing-page links (relative,
// no active state — the landing page is never "active" among them).
func docNavItems() []navItem {
	items := make([]navItem, 0, len(docPages))
	for _, p := range docPages {
		items = append(items, navItem{Href: p.Slug + ".html", Label: p.NavLabel})
	}
	return items
}

// buildNav returns the full header nav (Home + every doc page), marking
// current as active by output filename.
func buildNav(current string) []navItem {
	items := []navItem{{Href: "index.html", Label: "Home", Active: current == "index.html"}}
	for _, p := range docPages {
		href := p.Slug + ".html"
		items = append(items, navItem{Href: href, Label: p.NavLabel, Active: href == current})
	}
	return items
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
