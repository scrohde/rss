package content

import (
	"net/url"
	"strings"
)

func rewriteSrcset(value string, base *url.URL) (string, bool) {
	parts := parseSrcsetCandidates(value)
	if len(parts) == 0 {
		return value, false
	}
	changed := false
	rewritten := make([]string, 0, len(parts))
	for _, part := range parts {
		imageURL := part.imageURL
		if updated, ok := ProxyImageURL(imageURL, base); ok {
			imageURL = updated
			changed = true
		}
		if part.descriptor == "" {
			rewritten = append(rewritten, imageURL)
			continue
		}
		rewritten = append(rewritten, imageURL+" "+part.descriptor)
	}
	if !changed {
		return value, false
	}
	return strings.Join(rewritten, ", "), true
}

type srcsetCandidate struct {
	imageURL   string
	descriptor string
}

func parseSrcsetCandidates(value string) []srcsetCandidate {
	var candidates []srcsetCandidate
	i := 0
	for i < len(value) {
		for i < len(value) && (isASCIISpace(value[i]) || value[i] == ',') {
			i++
		}
		if i >= len(value) {
			break
		}

		urlStart := i
		for i < len(value) {
			if isASCIISpace(value[i]) {
				break
			}
			if value[i] == ',' {
				// Keep commas that are directly followed by non-space characters
				// as part of the URL. Many image CDNs encode transforms this way.
				j := i + 1
				for j < len(value) && isASCIISpace(value[j]) {
					j++
				}
				if j >= len(value) || j > i+1 {
					break
				}
			}
			i++
		}

		imageURL := strings.TrimSpace(value[urlStart:i])
		if imageURL == "" {
			i++
			continue
		}

		if i < len(value) && value[i] == ',' {
			candidates = append(candidates, srcsetCandidate{imageURL: imageURL})
			i++
			continue
		}

		descStart := i
		for i < len(value) && value[i] != ',' {
			i++
		}
		descriptor := strings.TrimSpace(value[descStart:i])
		candidates = append(candidates, srcsetCandidate{
			imageURL:   imageURL,
			descriptor: descriptor,
		})
		if i < len(value) && value[i] == ',' {
			i++
		}
	}
	return candidates
}

func isASCIISpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r'
}
