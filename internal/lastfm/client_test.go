package lastfm

import "testing"

func TestExtractListeners_PrimaryPattern(t *testing.T) {
	html := `...<abbr title="5,323,765" class="intro-stats-number">5.3M</abbr>...`
	got, err := extractListeners(html)
	if err != nil {
		t.Fatalf("extractListeners: %v", err)
	}
	if got != 5323765 {
		t.Errorf("got %d, want 5323765", got)
	}
}

func TestExtractListeners_JSONLD(t *testing.T) {
	html := `...{"userInteractionCount":"42000"}...`
	got, err := extractListeners(html)
	if err != nil {
		t.Fatalf("extractListeners: %v", err)
	}
	if got != 42000 {
		t.Errorf("got %d, want 42000", got)
	}
}

func TestExtractListeners_FallbackAbbrListeners(t *testing.T) {
	html := `<abbr>123,456</abbr> Listeners`
	got, err := extractListeners(html)
	if err != nil {
		t.Fatalf("extractListeners: %v", err)
	}
	if got != 123456 {
		t.Errorf("got %d, want 123456", got)
	}
}

func TestExtractListeners_NoMatch(t *testing.T) {
	html := `<html>no relevant fields here</html>`
	_, err := extractListeners(html)
	if err == nil {
		t.Fatal("expected error on missing pattern, got nil")
	}
}

func TestArtistKey_Normalization(t *testing.T) {
	cases := map[string]string{
		"  Irken Armada ":  "irken armada",
		"Electric Callboy": "electric callboy",
		"The\tMaine":       "the maine",
		"":                 "",
	}
	for in, want := range cases {
		if got := ArtistKey(in); got != want {
			t.Errorf("ArtistKey(%q) = %q, want %q", in, got, want)
		}
	}
}
