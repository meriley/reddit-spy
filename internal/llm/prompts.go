package llm

import (
	"encoding/json"
	"fmt"
)

// /no_think at the end of Qwen3 system prompts suppresses its <think>…</think>
// reasoning block — we're extracting structured data, not exploring, so the
// reasoning phase is pure latency + parse risk.

const systemPrompt = `You are the editorial voice of a private Discord digest
bot that summarizes Reddit posts for one reader. Write in compact, third-person
prose. Prefer plain English over jargon. Never use first-person ("I", "we"),
never address the reader, never use emoji unless the tone directive explicitly
allows it. Keep output within the requested character budget. Output plain text
only — no Markdown headings, no bullet lists unless the source content is
explicitly a list of track names or similar; in that case, keep the list
compact. /no_think`

const systemPromptMusic = `You extract music releases from Reddit weekly-release
threads. You return JSON only, never prose. Each entry is an artist plus a
title plus a kind marker ("single" | "album" | "ep"). Treat lines that end
with "(Album)" / "(LP)" as album, "(EP)" as ep, everything else as single.
Keep any "feat. X" inside the title, not the artist. Skip section headers,
playlists, and commentary. If the input contains no releases, return an
empty entries array. Never invent entries that aren't in the post body.`

// promptFresh builds the user prompt for the first matching post of a
// (subreddit, Phoenix-day) pair.
func promptFresh(in FreshInput, tone string, charBudget int) string {
	t := toneLine(tone)
	return fmt.Sprintf(`%s

A reddit-spy rule just matched its first post of the day from r/%s.

Produce a JSON object with exactly two keys:
  "title":   a punchy one-line title (max 120 chars, no quotes)
  "summary": a narrative body (max %d chars) that captures what the post is
             about for a reader who will not click through. Preserve concrete
             names (bands, games, people) when they appear in the source.

Source post:
  author:   u/%s
  title:    %s
  rule:     #%d matched on %s (%s)
  selftext: %s

Return ONLY the JSON object, nothing else.`,
		t,
		in.Post.Subreddit,
		charBudget,
		in.Post.Author,
		quoteSingleLine(in.Post.Title),
		in.RuleID, in.RuleTargetID, ruleMatchType(in.RuleExact),
		clipForPrompt(in.Post.Selftext, 6000),
	)
}

// promptUpdate builds the user prompt for a later match that folds into an
// existing rolling digest.
func promptUpdate(in UpdateInput, tone string, charBudget int) string {
	t := toneLine(tone)
	return fmt.Sprintf(`%s

A reddit-spy rule just matched another post from r/%s today. A running digest
already exists — rewrite it so the new post is woven into the narrative, not
appended as a separate paragraph. Keep the overall tone consistent.

Produce a JSON object with exactly two keys:
  "title":   an updated one-line title (max 120 chars, no quotes)
  "summary": the rewritten narrative body (max %d chars).

Existing digest:
  title:    %s
  summary:  %s
  posts so far: %d

New post:
  author:   u/%s
  title:    %s
  rule:     #%d matched on %s (%s)
  selftext: %s

Return ONLY the JSON object, nothing else.`,
		t,
		in.NewPost.Subreddit,
		charBudget,
		quoteSingleLine(in.PriorTitle),
		clipForPrompt(in.PriorSummary, charBudget),
		in.PriorPostCount,
		in.NewPost.Author,
		quoteSingleLine(in.NewPost.Title),
		in.NewRuleID, in.NewRuleTargetID, ruleMatchType(in.NewRuleExact),
		clipForPrompt(in.NewPost.Selftext, 6000),
	)
}

// promptMusicExtract is the user prompt for the music-digest shaper. Passes
// the already-known entries so the model can skip duplicates across days /
// threads / subreddits. body is the post selftext for this call; the caller
// owns sizing (via chunking or direct pass-through).
func promptMusicExtract(in MusicInput, body string) string {
	knownJSON, _ := json.Marshal(shrinkForSkipList(in.KnownEntries))
	return fmt.Sprintf(`Extract music releases from the Reddit post below.

Return a JSON object of the form:
  {"entries": [{"artist": "...", "title": "...", "kind": "single"|"album"|"ep"}, ...]}

Do NOT include any entry whose normalized (artist, title, kind) already
appears in the skip list; case and whitespace do not matter, and any
"(feat. ...)" fragment is ignored when comparing.

Skip list (%d entries):
%s

Source post:
  author:    u/%s
  subreddit: r/%s
  title:     %s
  body:
%s

Return ONLY the JSON object, nothing else.`,
		len(in.KnownEntries),
		string(knownJSON),
		in.Post.Author,
		in.Post.Subreddit,
		quoteSingleLine(in.Post.Title),
		body,
	)
}

// shrinkForSkipList trims fields we don't need inside the prompt so the skip
// list stays compact even with hundreds of entries.
func shrinkForSkipList(in []MusicEntry) []MusicEntry {
	out := make([]MusicEntry, len(in))
	for i, e := range in {
		out[i] = MusicEntry{Artist: e.Artist, Title: e.Title, Kind: e.Kind}
	}
	return out
}

func toneLine(tone string) string {
	switch tone {
	case "snarky":
		return "TONE: dry and mildly snarky, but never mean-spirited. No emoji."
	case "playful":
		return "TONE: warm and playful. Sparing use of emoji is acceptable."
	default:
		return "TONE: neutral and informative. No emoji."
	}
}

func ruleMatchType(exact bool) string {
	if exact {
		return "exact"
	}
	return "partial"
}

// clipForPrompt caps a source string at maxRunes runes so we don't stuff a
// multi-kilobyte selftext into the prompt. Returns the string unchanged if
// it's already short enough.
func clipForPrompt(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…[truncated]"
}

// quoteSingleLine replaces newlines with spaces so a title never breaks the
// prompt's field-per-line structure.
func quoteSingleLine(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' {
			out = append(out, ' ')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
