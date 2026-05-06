package helps

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	bundleMaxTarBytes  = 50 * 1024 * 1024 // 50 MB cap on tarball download
	bundleMaxJSBytes   = 20 * 1024 * 1024 // 20 MB cap per JS entry
	bundleFetchTimeout = 90 * time.Second
)

// betaStringPattern matches the comma-joined Anthropic-Beta value embedded in the Claude
// Code JS bundle. Must start with "claude-code-20<6 digits>" and contain ≥4 more betas.
var betaStringPattern = regexp.MustCompile("[\"`](claude-code-20\\d{6}(?:,[a-z][a-z0-9-]+){4,})[\"`]")

// jsSectionPrefixes maps logical section keys to their distinctive opening phrases
// as they appear in compiled JS (newlines are literal \n escape sequences).
var jsSectionPrefixes = map[string]string{
	"intro":      `You are an interactive agent that helps users with software engineering tasks.`,
	"system":     `# System\n- All text you output`,
	"doing":      `# Doing tasks`,
	"tone":       `# Tone and style`,
	"output_eff": `# Output efficiency`,
}

// jsSectionOrder is the join order used when assembling the static system prompt.
var jsSectionOrder = []string{"intro", "system", "doing", "tone", "output_eff"}

type npmDistMeta struct {
	Dist struct {
		Tarball string `json:"tarball"`
	} `json:"dist"`
}

// FetchBundleExtras downloads the @anthropic-ai/claude-code tarball for the given
// version and extracts the Anthropic-Beta string and system-prompt section texts.
// Returns empty strings/maps on any failure — callers must use static fallbacks.
func FetchBundleExtras(version string) (betas string, sections map[string]string) {
	tarballURL, err := resolveNPMTarballURL(version)
	if err != nil {
		log.Debugf("claude bundle: tarball URL resolve failed: %v", err)
		return "", nil
	}
	betas, sections = scanNPMTarball(tarballURL)
	return
}

func resolveNPMTarballURL(version string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	url := "https://registry.npmjs.org/@anthropic-ai/claude-code/" + version
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("npm registry HTTP %d for version %s", resp.StatusCode, version)
	}

	var meta npmDistMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", err
	}
	if meta.Dist.Tarball == "" {
		return "", fmt.Errorf("empty tarball URL in npm response")
	}
	return meta.Dist.Tarball, nil
}

func scanNPMTarball(tarballURL string) (betas string, sections map[string]string) {
	sections = make(map[string]string)

	ctx, cancel := context.WithTimeout(context.Background(), bundleFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarballURL, nil)
	if err != nil {
		return
	}
	resp, err := (&http.Client{Timeout: bundleFetchTimeout}).Do(req)
	if err != nil {
		log.Debugf("claude bundle: tarball download failed: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	gr, err := gzip.NewReader(io.LimitReader(resp.Body, bundleMaxTarBytes))
	if err != nil {
		return
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if !strings.HasSuffix(hdr.Name, ".js") {
			continue
		}

		content, err := io.ReadAll(io.LimitReader(tr, bundleMaxJSBytes))
		if err != nil || len(content) == 0 {
			continue
		}

		if betas == "" {
			betas = extractBetasFromJS(content)
		}
		for key, prefix := range jsSectionPrefixes {
			if _, found := sections[key]; found {
				continue
			}
			if text := extractJSSectionByPrefix(content, prefix); text != "" {
				sections[key] = text
			}
		}

		if betas != "" && len(sections) == len(jsSectionPrefixes) {
			break
		}
	}

	if betas != "" {
		preview := betas
		if len(preview) > 60 {
			preview = preview[:60]
		}
		log.Debugf("claude bundle: extracted betas: %s", preview)
	}
	log.Debugf("claude bundle: extracted %d/%d system prompt sections", len(sections), len(jsSectionPrefixes))
	return
}

func extractBetasFromJS(content []byte) string {
	match := betaStringPattern.FindSubmatch(content)
	if len(match) > 1 {
		return string(match[1])
	}
	return ""
}

// extractJSSectionByPrefix finds a JS string literal containing prefix and returns
// the unescaped content. The prefix may use literal \n (as in compiled JS output).
func extractJSSectionByPrefix(content []byte, prefix string) string {
	needle := []byte(prefix)
	idx := bytes.Index(content, needle)
	if idx < 0 {
		return ""
	}

	// Walk backwards from idx to find the opening quote character (" or `).
	start := idx
	for start > 0 {
		ch := content[start-1]
		if ch == '"' || ch == '`' {
			break
		}
		if idx-start > 256 {
			return ""
		}
		start--
	}
	if start == 0 {
		return ""
	}
	openQuote := content[start-1]

	// Walk forward from start, handling escape sequences, to find the closing quote.
	i := start
	for i < len(content) {
		if content[i] == '\\' {
			i += 2
			continue
		}
		if content[i] == openQuote {
			break
		}
		i++
		if i-start > 100_000 { // section unexpectedly large
			return ""
		}
	}
	if i >= len(content) {
		return ""
	}

	return unescapeJSString(string(content[start:i]))
}

// unescapeJSString converts JS string escape sequences (as produced by esbuild) to
// their actual Unicode characters.
func unescapeJSString(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '"':
			b.WriteByte('"')
		case '\'':
			b.WriteByte('\'')
		case '`':
			b.WriteByte('`')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

