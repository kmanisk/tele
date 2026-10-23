// Package docs holds no code: its tests keep the documentation and its
// translations in step with each other.
package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// root is the repository, seen from this package's directory.
const root = "../.."

// translated lists the English documents every language has a translation of.
// The documents for those working on tele stay in English only.
var translated = []string{
	"README.md",
	"docs/README.md",
	"docs/app-key.md",
	"docs/cli.md",
	"docs/commands.md",
	"docs/configuration.md",
	"docs/keybindings.md",
	"docs/media.md",
	"docs/themes.md",
}

// indexes are the documents that name the other languages at the top.
var indexes = []string{"README.md", "docs/README.md"}

// languages lists the translations by the code their files are named with.
var languages = []string{"ru", "zh-CN"}

// translation is where a language keeps its copy of an English document.
func translation(english, lang string) string {
	if english == "README.md" {
		return "README." + lang + ".md"
	}
	return filepath.Join("docs", lang, strings.TrimPrefix(english, "docs/"))
}

func TestEveryDocumentIsTranslated(t *testing.T) {
	for _, lang := range languages {
		var want []string
		for _, english := range translated {
			path := translation(english, lang)
			assert.FileExists(t, filepath.Join(root, path), "%s has no %s translation", english, lang)
			if strings.HasPrefix(path, "docs/") {
				want = append(want, filepath.Base(path))
			}
		}

		entries, err := os.ReadDir(filepath.Join(root, "docs", lang))
		require.NoError(t, err)
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		assert.ElementsMatch(t, want, got, "docs/%s holds a document that is not translated from English, or misses one", lang)
	}
}

var commitRe = regexp.MustCompile("`[0-9a-f]{7,40}`")

// A translation says at the top which English document it was made from, and
// at which commit, so that a reader can tell how far behind it may be.
func TestATranslationNamesItsOriginalAndCommit(t *testing.T) {
	for _, lang := range languages {
		for _, english := range translated {
			path := translation(english, lang)
			text := read(t, path)
			original, err := filepath.Rel(filepath.Dir(path), english)
			require.NoError(t, err)

			found := false
			for _, line := range strings.Split(text, "\n") {
				if strings.HasPrefix(line, ">") && strings.Contains(line, "]("+original+")") && commitRe.MatchString(line) {
					found = true
					break
				}
			}
			assert.True(t, found, "%s does not name %s and the commit it was translated at", path, original)
		}
	}
}

// A translation keeps every section the English document has under the same
// anchor, so a link into one language works in any other.
func TestATranslationKeepsEveryEnglishAnchor(t *testing.T) {
	for _, lang := range languages {
		for _, english := range translated {
			path := translation(english, lang)
			have := anchors(read(t, path))
			for _, anchor := range anchors(read(t, english))[1:] {
				assert.Contains(t, have, anchor, "%s has no section under %q, which %s has", path, anchor, english)
			}
		}
	}
}

func TestIndexesLinkEveryOtherLanguage(t *testing.T) {
	all := append([]string{""}, languages...)
	in := func(english, lang string) string {
		if lang == "" {
			return english
		}
		return translation(english, lang)
	}
	for _, english := range indexes {
		for _, lang := range all {
			path := in(english, lang)
			targets := linkTargets(read(t, path), path)
			for _, other := range all {
				if other == lang {
					continue
				}
				assert.Contains(t, targets, in(english, other), "%s does not link its %q counterpart", path, other)
			}
		}
	}
}

// Every link between documents leads somewhere, in every language: to a file
// that exists, and to a section that file has.
func TestLinksResolve(t *testing.T) {
	docs := slices.Clone(translated)
	for _, lang := range languages {
		for _, english := range translated {
			docs = append(docs, translation(english, lang))
		}
	}

	for _, path := range docs {
		text := read(t, path)
		for _, link := range links(text) {
			file, anchor, _ := strings.Cut(link, "#")
			target := path
			if file != "" {
				target = filepath.Join(filepath.Dir(path), file)
			}
			if _, err := os.Stat(filepath.Join(root, target)); !assert.NoError(t, err, "%s links %s", path, link) {
				continue
			}
			if anchor == "" || filepath.Ext(target) != ".md" {
				continue
			}
			assert.Contains(t, anchors(read(t, target)), anchor, "%s links %s, and %s has no such section", path, link, target)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, path))
	require.NoError(t, err)
	return string(b)
}

var (
	mdLinkRe = regexp.MustCompile(`\]\(([^)\s]+)`)
	hrefRe   = regexp.MustCompile(`href="([^"]+)"`)
	headRe   = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*#*\s*$`)
	idRe     = regexp.MustCompile(`<a\s+(?:id|name)="([^"]+)"`)
	inlineRe = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
)

// links lists the local targets a document links, leaving out the web and
// whatever sits in a code block.
func links(text string) []string {
	var out []string
	for _, line := range prose(text) {
		for _, re := range []*regexp.Regexp{mdLinkRe, hrefRe} {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				link := m[1]
				if strings.Contains(link, "://") || strings.HasPrefix(link, "mailto:") {
					continue
				}
				out = append(out, link)
			}
		}
	}
	return out
}

// linkTargets lists the files a document links, as paths from the repository.
func linkTargets(text, path string) []string {
	var out []string
	for _, link := range links(text) {
		file, _, _ := strings.Cut(link, "#")
		if file != "" {
			out = append(out, filepath.Join(filepath.Dir(path), file))
		}
	}
	return out
}

// anchors lists the sections a link may name: the slug GitHub makes of every
// heading, and every explicit anchor, which is how a translated heading keeps
// the English one's slug.
func anchors(text string) []string {
	var out []string
	seen := map[string]int{}
	for _, line := range prose(text) {
		for _, m := range idRe.FindAllStringSubmatch(line, -1) {
			out = append(out, m[1])
		}
		m := headRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		s := slug(m[1])
		if n := seen[s]; n > 0 {
			out = append(out, fmt.Sprintf("%s-%d", s, n))
		} else {
			out = append(out, s)
		}
		seen[s]++
	}
	return out
}

// slug is GitHub's anchor for a heading: its text in lower case, with
// punctuation dropped and spaces turned into hyphens.
func slug(heading string) string {
	heading = inlineRe.ReplaceAllString(heading, "$1")
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// prose is a document's lines outside fenced code blocks.
func prose(text string) []string {
	var out []string
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced {
			out = append(out, line)
		}
	}
	return out
}
