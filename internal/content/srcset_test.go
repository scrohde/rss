package content

import "testing"

func TestParseSrcsetCandidates(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []srcsetCandidate
	}{
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name:  "simple descriptors",
			input: "https://example.com/a.jpg 1x, https://example.com/b.jpg 2x",
			want: []srcsetCandidate{
				{imageURL: "https://example.com/a.jpg", descriptor: "1x"},
				{imageURL: "https://example.com/b.jpg", descriptor: "2x"},
			},
		},
		{
			name:  "url with commas",
			input: "https://substackcdn.com/image/fetch/$s_!sBbM!,w_424,c_limit,f_auto,q_auto:good/https%3A%2F%2Fsubstack-post-media.s3.amazonaws.com%2Fpublic%2Fimages%2Fa.png 424w, https://substackcdn.com/image/fetch/$s_!sBbM!,w_848,c_limit,f_auto,q_auto:good/https%3A%2F%2Fsubstack-post-media.s3.amazonaws.com%2Fpublic%2Fimages%2Fa.png 848w",
			want: []srcsetCandidate{
				{
					imageURL:   "https://substackcdn.com/image/fetch/$s_!sBbM!,w_424,c_limit,f_auto,q_auto:good/https%3A%2F%2Fsubstack-post-media.s3.amazonaws.com%2Fpublic%2Fimages%2Fa.png",
					descriptor: "424w",
				},
				{
					imageURL:   "https://substackcdn.com/image/fetch/$s_!sBbM!,w_848,c_limit,f_auto,q_auto:good/https%3A%2F%2Fsubstack-post-media.s3.amazonaws.com%2Fpublic%2Fimages%2Fa.png",
					descriptor: "848w",
				},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := parseSrcsetCandidates(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("expected %d candidates, got %d", len(tc.want), len(got))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("candidate %d mismatch: got %+v want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
