package content

import (
	"net/url"
	"strings"
)

const srcsetStepOne = 1

func rewriteSrcset(value string, base *url.URL) (string, bool) {
	parts := parseSrcsetCandidates(value)
	if parts == nil {
		return value, false
	}

	changed := false

	var rewritten []string

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

	var i int
	for i < len(value) {
		i = skipSrcsetSeparators(value, i)
		if i >= len(value) {
			break
		}

		candidate, next, ok := parseSrcsetCandidate(value, i)
		i = next

		if ok {
			candidates = append(candidates, candidate)
		}
	}

	return candidates
}

func skipSrcsetSeparators(value string, index int) int {
	i := index
	for i < len(value) && (isASCIISpace(value[i]) || value[i] == ',') {
		i++
	}

	return i
}

func parseSrcsetCandidate(value string, start int) (srcsetCandidate, int, bool) {
	urlEnd := scanSrcsetURL(value, start)
	imageURL := strings.TrimSpace(value[start:urlEnd])

	if imageURL == "" {
		return srcsetCandidate{
			imageURL:   "",
			descriptor: "",
		}, advanceIndex(value, urlEnd), false
	}

	if isSrcsetDelimiter(value, urlEnd) {
		return srcsetCandidate{
			imageURL:   imageURL,
			descriptor: "",
		}, urlEnd + srcsetStepOne, true
	}

	descEnd := scanSrcsetDescriptor(value, urlEnd)
	next := descEnd

	if isSrcsetDelimiter(value, descEnd) {
		next += srcsetStepOne
	}

	return srcsetCandidate{
		imageURL:   imageURL,
		descriptor: strings.TrimSpace(value[urlEnd:descEnd]),
	}, next, true
}

func advanceIndex(value string, index int) int {
	if index < len(value) {
		return index + srcsetStepOne
	}

	return index
}

func scanSrcsetURL(value string, start int) int {
	i := start
	for i < len(value) {
		if isASCIISpace(value[i]) {
			break
		}

		if value[i] == ',' && isSrcsetURLDelimiter(value, i) {
			break
		}

		i++
	}

	return i
}

func isSrcsetURLDelimiter(value string, index int) bool {
	// Preserve commas within URLs when no space follows, used by some CDNs.
	next := index + srcsetStepOne
	for next < len(value) && isASCIISpace(value[next]) {
		next++
	}

	return next >= len(value) || next > index+srcsetStepOne
}

func scanSrcsetDescriptor(value string, start int) int {
	i := start
	for i < len(value) && value[i] != ',' {
		i++
	}

	return i
}

func isSrcsetDelimiter(value string, index int) bool {
	return index < len(value) && value[index] == ','
}

func isASCIISpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r'
}
