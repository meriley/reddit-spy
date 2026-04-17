package piped

import "testing"

func TestQueryKey(t *testing.T) {
	cases := map[[2]string]string{
		{"Wage War", "It Calls Me By Name"}:        "wage war it calls me by name",
		{"  electric callboy ", "  Hypercharged "}: "electric callboy hypercharged",
		{"A", ""}:           "a",
		{"", ""}:            "",
		{"thrown", "Split"}: "thrown split",
	}
	for in, want := range cases {
		if got := QueryKey(in[0], in[1]); got != want {
			t.Errorf("QueryKey(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestVideoIDFromURL(t *testing.T) {
	cases := map[string]string{
		"/watch?v=abcd1234":                                "abcd1234",
		"https://www.youtube.com/watch?v=abcd1234":         "abcd1234",
		"https://www.youtube.com/watch?v=abcd1234&list=LL": "abcd1234",
		"https://youtu.be/abcd1234":                        "abcd1234",
		"/watch":                                           "",
		"":                                                 "",
	}
	for in, want := range cases {
		if got := videoIDFromURL(in); got != want {
			t.Errorf("videoIDFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestYoutubeURL(t *testing.T) {
	if got := YoutubeURL("abcd1234"); got != "https://music.youtube.com/watch?v=abcd1234" {
		t.Errorf("got %q", got)
	}
	if got := YoutubeURL(""); got != "" {
		t.Errorf("empty id should yield empty url, got %q", got)
	}
}
