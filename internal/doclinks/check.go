// Package doclinks verifies internal references in the rendered public site.
// It lives outside docs because it is maintainer tooling, not publishable site
// content, and outside cmd because it is not a user-facing Local Agent binary.
package doclinks

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const DefaultOrigin = "https://local-agent.dev"

// Failure is one deterministic broken-reference finding.
type Failure struct {
	Source    string
	Reference string
	Reason    string
}

func (f Failure) String() string {
	return fmt.Sprintf("%s: %s — %s", f.Source, strconv.Quote(f.Reference), f.Reason)
}

type siteDocument struct {
	references []string
	anchors    map[string]struct{}
}

// FindBrokenInternalLinks checks the emitted HTML rather than Markdown source
// so navigation and theme-generated references are covered too. External and
// non-HTTP schemes are deliberately outside this deterministic offline gate.
func FindBrokenInternalLinks(rootDir, origin string) ([]Failure, error) {
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve rendered site root: %w", err)
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Host == "" || (originURL.Scheme != "http" && originURL.Scheme != "https") {
		return nil, fmt.Errorf("invalid site origin %q", origin)
	}
	siteOrigin := normalizedOrigin(originURL)
	if siteOrigin == "" {
		return nil, fmt.Errorf("invalid site origin %q", origin)
	}

	files, err := listRenderedFiles(rootDir)
	if err != nil {
		return nil, err
	}
	existingFiles := make(map[string]struct{}, len(files))
	documents := make(map[string]siteDocument)
	for _, file := range files {
		existingFiles[file] = struct{}{}
		if !strings.HasSuffix(file, ".html") {
			continue
		}
		document, err := readSiteDocument(filepath.Join(rootDir, filepath.FromSlash(file)))
		if err != nil {
			return nil, fmt.Errorf("parse rendered page %s: %w", file, err)
		}
		documents[file] = document
	}

	htmlFiles := make([]string, 0, len(documents))
	for file := range documents {
		htmlFiles = append(htmlFiles, file)
	}
	sort.Strings(htmlFiles)

	var failures []Failure
	for _, source := range htmlFiles {
		base := &url.URL{Scheme: originURL.Scheme, Host: originURL.Host, Path: "/" + source}
		for _, reference := range documents[source].references {
			if reference == "" {
				continue
			}
			parsed, err := url.Parse(reference)
			if err != nil {
				failures = append(failures, Failure{Source: source, Reference: reference, Reason: "invalid URL"})
				continue
			}
			target := base.ResolveReference(parsed)
			scheme := strings.ToLower(target.Scheme)
			if scheme != "http" && scheme != "https" {
				continue
			}
			if normalizedOrigin(target) != siteOrigin {
				continue
			}

			candidates, err := targetCandidates(target)
			if err != nil {
				failures = append(failures, Failure{Source: source, Reference: reference, Reason: err.Error()})
				continue
			}
			targetFile := firstExistingCandidate(candidates, existingFiles)
			if targetFile == "" {
				expected := target.Path
				if len(candidates) > 0 {
					expected = strings.Join(candidates, ", ")
				}
				failures = append(failures, Failure{
					Source: source, Reference: reference,
					Reason: fmt.Sprintf("missing target (tried: %s)", expected),
				})
				continue
			}

			fragment, err := decodedFragment(target)
			if err != nil {
				failures = append(failures, Failure{Source: source, Reference: reference, Reason: "invalid fragment encoding"})
				continue
			}
			if fragment == "" {
				continue
			}
			if !strings.HasSuffix(targetFile, ".html") {
				// SVG element references and media fragments are interpreted by
				// their respective formats. File existence is the strongest
				// format-neutral assertion this offline HTML gate can make.
				continue
			}
			if directive := strings.Index(fragment, ":~:text="); directive >= 0 {
				fragment = fragment[:directive]
				if fragment == "" {
					continue
				}
			}
			if _, ok := documents[targetFile].anchors[fragment]; !ok {
				failures = append(failures, Failure{
					Source: source, Reference: reference,
					Reason: fmt.Sprintf("missing fragment #%s in %s", fragment, targetFile),
				})
			}
		}
	}

	sort.Slice(failures, func(i, j int) bool {
		if failures[i].Source != failures[j].Source {
			return failures[i].Source < failures[j].Source
		}
		if failures[i].Reference != failures[j].Reference {
			return failures[i].Reference < failures[j].Reference
		}
		return failures[i].Reason < failures[j].Reason
	})
	return failures, nil
}

func listRenderedFiles(rootDir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk rendered site %s: %w", rootDir, err)
	}
	sort.Strings(files)
	return files, nil
}

func readSiteDocument(path string) (siteDocument, error) {
	file, err := os.Open(path)
	if err != nil {
		return siteDocument{}, err
	}
	defer func() { _ = file.Close() }()
	root, err := html.Parse(file)
	if err != nil {
		return siteDocument{}, err
	}
	document := siteDocument{anchors: make(map[string]struct{})}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode {
			for _, attribute := range node.Attr {
				switch strings.ToLower(attribute.Key) {
				case "href", "src":
					document.references = append(document.references, attribute.Val)
				case "id", "name":
					document.anchors[attribute.Val] = struct{}{}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return document, nil
}

func normalizedOrigin(value *url.URL) string {
	if value == nil {
		return ""
	}
	scheme := strings.ToLower(value.Scheme)
	host := strings.ToLower(value.Hostname())
	if host == "" || (scheme != "http" && scheme != "https") {
		return ""
	}
	port := value.Port()
	if port == "" {
		if scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host + ":" + port
}

func targetCandidates(target *url.URL) ([]string, error) {
	decoded, err := url.PathUnescape(target.EscapedPath())
	if err != nil || strings.ContainsRune(decoded, '\x00') {
		return nil, fmt.Errorf("invalid URL path encoding")
	}
	trailingSlash := strings.HasSuffix(decoded, "/")
	cleaned := pathpkg.Clean("/" + strings.TrimPrefix(decoded, "/"))
	relative := strings.TrimPrefix(cleaned, "/")
	if relative == "" || relative == "." {
		return []string{"index.html"}, nil
	}
	if trailingSlash {
		return []string{pathpkg.Join(relative, "index.html")}, nil
	}
	candidates := []string{relative}
	// A dot is valid inside a VitePress clean route (for example /v2.17).
	// Only an explicit .html URL is already the emitted page name; every other
	// clean path may map to either <path>.html or <path>/index.html.
	if !strings.HasSuffix(strings.ToLower(relative), ".html") {
		candidates = append(candidates, relative+".html", pathpkg.Join(relative, "index.html"))
	}
	return candidates, nil
}

func firstExistingCandidate(candidates []string, existing map[string]struct{}) string {
	for _, candidate := range candidates {
		if _, ok := existing[candidate]; ok {
			return candidate
		}
	}
	return ""
}

func decodedFragment(target *url.URL) (string, error) {
	if target == nil || target.Fragment == "" {
		return "", nil
	}
	return url.PathUnescape(target.EscapedFragment())
}
