package llm

import (
	"fmt"
	"testing"

	redditJSON "github.com/meriley/reddit-spy/internal/redditJSON"
)

// TestExamplePrompts is a render-only test that prints what the shaper sends
// to vLLM for a representative Metalcore weekly-release match. Run with:
//
//	go test -run TestExamplePrompts -v ./internal/llm/
func TestExamplePrompts(t *testing.T) {
	post := &redditJSON.RedditPost{
		ID:        "xyz123",
		Author:    "sink_or_swim1",
		Subreddit: "Metalcore",
		Title:     "Weekly Release Thread April 17th, 2026",
		Selftext: "**Singles/ICYMI**\n\n" +
			"Irken Armada - Nail In The Coffin\n" +
			"What Lies Below - Leech\n" +
			"Continents - Gatekeeper\n" +
			"Ice Sealed Eyes - Acid Tears\n" +
			"thrown - Split\n" +
			"Electric Callboy - Hypercharged feat Brawl Stars\n" +
			"Mayflower - Caution To The Wind\n" +
			"Erkmen - Halogen Heart\n" +
			"Lafayette - This Prison In Me\n" +
			"Rise Of Nebula - Asylum Afterparty\n" +
			"Cryblood - Bury Me Alive",
		Score:       2,
		NumComments: 1,
		URL:         "https://reddit.com/r/Metalcore/comments/xyz123",
	}

	fmt.Println("\n====================== SYSTEM PROMPT ======================")
	fmt.Println(systemPrompt)

	fmt.Println("\n====================== FRESH USER PROMPT ======================")
	fmt.Println(promptFresh(FreshInput{
		Post:         post,
		RuleID:       2,
		RuleTargetID: "title",
		RuleExact:    false,
	}, "", SummaryCharBudget))

	fmt.Println("\n====================== UPDATE USER PROMPT ======================")
	fmt.Println(promptUpdate(UpdateInput{
		PriorTitle: "Metalcore fans got eleven fresh singles this week",
		PriorSummary: "Irken Armada kicked the week off with Nail In The Coffin, " +
			"while Electric Callboy went genre-tourist with Hypercharged (feat. Brawl Stars). " +
			"Mayflower, Erkmen, and Lafayette each released standalone tracks before " +
			"Rise Of Nebula's Asylum Afterparty and Cryblood's Bury Me Alive closed the " +
			"roundup on the heavier side.",
		PriorPostCount: 1,
		NewPost: &redditJSON.RedditPost{
			ID:        "abc789",
			Author:    "elderemothings",
			Subreddit: "poppunkers",
			Title:     "Weekly New Releases - April 17, 2026",
			Selftext: "Happy Friday all, here is this weeks run down of new music!\n\n" +
				"The Maine - Joy Next Door (Album)\n" +
				"Broadside - Nowhere, At Last (Album)\n" +
				"Yellowcard (feat. Blippi) - Bedroom Posters (Single)",
			Score:       1,
			NumComments: 0,
		},
		NewRuleID:       3,
		NewRuleTargetID: "title",
		NewRuleExact:    false,
	}, "", SummaryCharBudget))
}
