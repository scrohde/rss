//nolint:testpackage // Content tests exercise package-internal helpers directly.
package content

import "testing"

func TestParseSrcsetCandidatesEmpty(t *testing.T) {
	t.Parallel()

	got := parseSrcsetCandidates("")
	if got != nil {
		t.Fatalf("expected nil candidates, got %+v", got)
	}
}

func TestParseSrcsetCandidatesSimpleDescriptors(t *testing.T) {
	t.Parallel()

	input := "https://example.com/a.jpg 1x, https://example.com/b.jpg 2x"
	got := parseSrcsetCandidates(input)
	want := []srcsetCandidate{
		{
			imageURL:   "https://example.com/a.jpg",
			descriptor: "1x",
		},
		{
			imageURL:   "https://example.com/b.jpg",
			descriptor: "2x",
		},
	}

	assertSrcsetCandidates(t, got, want)
}

func TestParseSrcsetCandidatesWithCommasInURL(t *testing.T) {
	t.Parallel()

	url424 := substackURL424Prefix + substackURLSuffix
	url848 := substackURL848Prefix + substackURLSuffix
	input := url424 + " 424w, " + url848 + " 848w"
	got := parseSrcsetCandidates(input)
	want := []srcsetCandidate{
		{
			imageURL:   url424,
			descriptor: "424w",
		},
		{
			imageURL:   url848,
			descriptor: "848w",
		},
	}

	assertSrcsetCandidates(t, got, want)
}

func assertSrcsetCandidates(
	t *testing.T,
	got []srcsetCandidate,
	want []srcsetCandidate,
) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("expected %d candidates, got %d", len(want), len(got))
	}

	for candidateIndex := range got {
		if got[candidateIndex] != want[candidateIndex] {
			t.Fatalf(
				"candidate %d mismatch: got %+v want %+v",
				candidateIndex,
				got[candidateIndex],
				want[candidateIndex],
			)
		}
	}
}
