package doclinks

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var renderedSiteRoot = flag.String("dist", "", "rendered VitePress directory to verify")

func TestFindBrokenInternalLinksAcceptsCleanSite(t *testing.T) {
	root := createRenderedSite(t, map[string]string{
		"assets/app.js":   "",
		"assets/logo.svg": `<svg id="mark"></svg>`,
		"guide.html":      `<main id="install"><a href="/">Home</a></main>`,
		"v2.17.html":      `<main>Versioned guide</main>`,
		"index.html": `
      <main id="top">
        <a href="#top">Top</a>
        <a href="/guide">Guide</a>
        <a href="/guide#install">Install</a>
		<a href="/guide#install:~:text=Install">Install text fragment</a>
		<a href="/v2.17">Dotted clean route</a>
		<a href="/assets/logo.svg#mark">SVG fragment</a>
        <a href="https://local-agent.dev:443/guide#install">Canonical guide</a>
        <a href="https://example.com/missing">External</a>
        <a href="mailto:maintainer@example.com">Email</a>
        <script src="/assets/app.js"></script>
      </main>
    `,
	})

	failures, err := FindBrokenInternalLinks(root, DefaultOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("clean site failures = %#v", failures)
	}
}

func TestFindBrokenInternalLinksSortsMissingRoutes(t *testing.T) {
	root := createRenderedSite(t, map[string]string{
		"index.html": `<a href="/z-last">Z</a><a href="/a-first">A</a>`,
	})

	failures, err := FindBrokenInternalLinks(root, DefaultOrigin)
	if err != nil {
		t.Fatal(err)
	}
	want := []Failure{
		{Source: "index.html", Reference: "/a-first", Reason: "missing target (tried: a-first, a-first.html, a-first/index.html)"},
		{Source: "index.html", Reference: "/z-last", Reason: "missing target (tried: z-last, z-last.html, z-last/index.html)"},
	}
	if !reflect.DeepEqual(failures, want) {
		t.Fatalf("missing route failures = %#v, want %#v", failures, want)
	}
}

func TestFindBrokenInternalLinksReportsMissingFragment(t *testing.T) {
	root := createRenderedSite(t, map[string]string{
		"guide.html": `<main id="present"></main>`,
		"index.html": `<a href="/guide#absent">Missing section</a>`,
	})

	failures, err := FindBrokenInternalLinks(root, DefaultOrigin)
	if err != nil {
		t.Fatal(err)
	}
	want := []Failure{{
		Source: "index.html", Reference: "/guide#absent",
		Reason: "missing fragment #absent in guide.html",
	}}
	if !reflect.DeepEqual(failures, want) {
		t.Fatalf("fragment failures = %#v, want %#v", failures, want)
	}
}

func TestFindBrokenInternalLinksChecksSameOriginAbsoluteURLs(t *testing.T) {
	root := createRenderedSite(t, map[string]string{
		"index.html": `
      <a href="https://local-agent.dev/missing">Internal absolute link</a>
      <a href="https://example.com/missing">External absolute link</a>
    `,
	})

	failures, err := FindBrokenInternalLinks(root, DefaultOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].Reference != "https://local-agent.dev/missing" {
		t.Fatalf("absolute URL failures = %#v", failures)
	}
}

func TestRenderedSiteInternalLinks(t *testing.T) {
	if strings.TrimSpace(*renderedSiteRoot) == "" {
		t.Skip("pass -dist after building the VitePress site to run the rendered-site gate")
	}
	failures, err := FindBrokenInternalLinks(*renderedSiteRoot, DefaultOrigin)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range failures {
		t.Error(failure.String())
	}
}

func createRenderedSite(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for relative, contents := range files {
		destination := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
