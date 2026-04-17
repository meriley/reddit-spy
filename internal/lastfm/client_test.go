package lastfm

import "testing"

func TestExtractListeners_ListenersHeaderPattern(t *testing.T) {
	// Shape observed on https://www.last.fm/music/Electric+Callboy (2026-04).
	html := `
		<h4>
			Listeners
		</h4>
		<div>
			<p><abbr class="intabbr js-abbreviated-counter" title="305,227">305.2K</abbr></p>
		</div>`
	got, err := extractListeners(html)
	if err != nil {
		t.Fatalf("extractListeners: %v", err)
	}
	if got != 305227 {
		t.Errorf("got %d, want 305227", got)
	}
}

func TestExtractListeners_BareIntabbr(t *testing.T) {
	// If the header is restructured but the intabbr element survives, the
	// second pattern still wins (first occurrence = listeners, second =
	// scrobbles).
	html := `<abbr class="intabbr js-abbreviated-counter" title="305,227">305.2K</abbr>
	         <abbr class="intabbr js-abbreviated-counter" title="19,572,641">19.6M</abbr>`
	got, err := extractListeners(html)
	if err != nil {
		t.Fatalf("extractListeners: %v", err)
	}
	if got != 305227 {
		t.Errorf("got %d, want 305227 (listeners first, not scrobbles)", got)
	}
}

func TestExtractListeners_LegacyIntroStats(t *testing.T) {
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

func TestExtractTags(t *testing.T) {
	html := `
<section class="catalogue-tags ">
	<h2 class="sr-only">Related Tags</h2>
	<ul class="tags-list tags-list--global">
		<li class="tag"><a href="/tag/metalcore">metalcore</a></li>
		<li class="tag"><a href="/tag/trancecore">trancecore</a></li>
		<li class="tag"><a href="/tag/german">german</a></li>
		<li class="tag"><a href="/tag/electronic">electronic</a></li>
		<li class="tag"><a href="/tag/electronicore">electronicore</a></li>
	</ul>
</section>`
	got := extractTags(html, 3)
	want := []string{"metalcore", "trancecore", "german"}
	if len(got) != len(want) {
		t.Fatalf("len %d, want %d; got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tag %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractTags_NoBlock(t *testing.T) {
	if got := extractTags(`<html>no tags at all</html>`, 3); got != nil {
		t.Errorf("expected nil, got %v", got)
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
