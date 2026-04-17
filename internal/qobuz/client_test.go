package qobuz

import "testing"

func TestNormArtistSlug(t *testing.T) {
	cases := map[string]string{
		"Electric Callboy":             "electric-callboy",
		"The Last Ten Seconds Of Life": "the-last-ten-seconds-of-life",
		"  .gif from god  ":            "gif-from-god",
		"Lø Spirit":                    "l-spirit", // non-ASCII dropped
		"":                             "",
		"thrown":                       "thrown",
	}
	for in, want := range cases {
		if got := normArtistSlug(in); got != want {
			t.Errorf("normArtistSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugContainsArtist(t *testing.T) {
	yes := []struct{ path, artist string }{
		{"/us-en/album/tekkno-electric-callboy/njbfh0", "electric-callboy"},
		{"/us-en/album/electric-callboy-live/abc", "electric-callboy"},
		{"/us-en/album/thrown/xyz", "thrown"},
	}
	for _, c := range yes {
		if !slugContainsArtist(c.path, c.artist) {
			t.Errorf("expected match: path=%q artist=%q", c.path, c.artist)
		}
	}
	no := []struct{ path, artist string }{
		// Qobuz's fuzzy fallback when searching a nonsense query
		{"/us-en/album/the-house-that-doesnt-exist-melodys-echo-chamber/abc", "electric-callboy"},
		// Substring without hyphen boundary
		{"/us-en/album/carousel-of-doom/abc", "ca"},
	}
	for _, c := range no {
		if slugContainsArtist(c.path, c.artist) {
			t.Errorf("expected rejection: path=%q artist=%q", c.path, c.artist)
		}
	}
}

func TestPickFirstMatchingAlbum(t *testing.T) {
	html := `
		<a href="/us-en/album/something-else-wrongband/aaa">wrong</a>
		<a href="/us-en/album/tekkno-electric-callboy/njbfh0gfpsaub">Tekkno</a>
		<a href="/us-en/album/tekkno-tour-edition-electric-callboy/hr2zf95upphhb">Tekkno Tour</a>
	`
	got := pickFirstMatchingAlbum(html, "Electric Callboy")
	want := "/us-en/album/tekkno-electric-callboy/njbfh0gfpsaub"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPickFirstMatchingAlbum_NoMatch(t *testing.T) {
	html := `<a href="/us-en/album/unrelated-album-other-artist/xyz">unrelated</a>`
	if got := pickFirstMatchingAlbum(html, "Electric Callboy"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestQueryKey(t *testing.T) {
	if got := QueryKey("Electric Callboy", " TEKKNO "); got != "electric callboy tekkno" {
		t.Errorf("got %q", got)
	}
}
