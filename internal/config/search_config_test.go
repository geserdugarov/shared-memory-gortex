package config

import "testing"

func TestEffectiveKeywordSoupRewrite(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", KeywordSoupSplit},
		{"split", KeywordSoupSplit},
		{"SPLIT", KeywordSoupSplit},
		{" split ", KeywordSoupSplit},
		{"nudge", KeywordSoupNudge},
		{"Nudge", KeywordSoupNudge},
		{"off", KeywordSoupOff},
		{"OFF", KeywordSoupOff},
		{"bogus", KeywordSoupSplit}, // unknown folds to the safe default
	}
	for _, tc := range cases {
		got := SearchConfig{KeywordSoupRewrite: tc.in}.EffectiveKeywordSoupRewrite()
		if got != tc.want {
			t.Errorf("EffectiveKeywordSoupRewrite(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
