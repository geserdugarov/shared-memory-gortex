package rerank

import (
	"strings"
	"unicode"
)

// soupStopWords is a tight set of English function words used by the
// keyword-density heuristic to tell a list of keywords ("parse encode
// decode marshal ...") apart from a genuine sentence ("how does the
// parser decode the payload"). Kept deliberately small -- the density
// check is already gated behind a boolean-operator co-signal, so a
// few misses here cannot trip a false positive on prose.
var soupStopWords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "is": {}, "are": {}, "be": {},
	"in": {}, "of": {}, "to": {}, "for": {}, "with": {}, "by": {},
	"from": {}, "on": {}, "at": {}, "as": {}, "do": {}, "does": {},
	"how": {}, "what": {}, "where": {}, "when": {}, "why": {},
	"which": {}, "and": {}, "or": {}, "not": {}, "we": {}, "it": {},
	"this": {}, "that": {}, "into": {}, "get": {}, "gets": {},
}

// LooksLikeKeywordSoup reports whether a query is a degenerate
// boolean / OR-soup string rather than a usable search query -- the
// shape an agent produces when it pastes a list of disjuncts
// (`A OR B OR 'no access' OR error`) instead of describing intent.
// Such queries defeat BM25: every disjunct competes, the rare term
// drowns, and LLM expansion just amplifies the noise.
//
// The second return value is a short human-readable reason, suitable
// for a `query_advice` nudge on the search response.
//
// The detector is deliberately conservative -- a genuine multi-clause
// natural-language query ("how do we validate the auth token and
// refresh it") must NOT be flagged. Soup is identified only when
// several independent signals agree:
//
//   - Two or more standalone boolean operators -- uppercase OR / AND,
//     or a pipe (| / ||). A single lowercase "or" in prose does not
//     count; the operator must be a separate token.
//   - Three or more quoted fragments ('no access', "rate limit"),
//     which is how an agent fences multi-word disjuncts.
//   - A long token run (8+) that is mostly identifier-shaped tokens
//     with almost no English stop-words -- list-of-keywords texture.
//
// Any single strong signal (>=2 boolean operators, or >=3 quoted
// fragments) trips the detector; the keyword-density heuristic only
// trips when it ALSO sees at least one boolean operator, so a long
// genuine sentence stays classified as a concept query.
func LooksLikeKeywordSoup(query string) (bool, string) {
	q := strings.TrimSpace(query)
	if q == "" {
		return false, ""
	}

	boolOps := countBooleanOperators(q)
	quoted := countQuotedFragments(q)

	// Strong signal 1: an explicit boolean disjunction of several
	// clauses. One operator can be a typo or a literal; two-plus is a
	// deliberate (and retrieval-hostile) construction.
	if boolOps >= 2 {
		return true, soupReasonBoolean
	}

	// Strong signal 2: many fenced fragments. Three or more quoted
	// substrings is an agent enumerating disjuncts, not prose.
	if quoted >= 3 {
		return true, "query stacks several quoted fragments; split them into separate searches or describe the target in plain words"
	}

	// Density signal: a long run of identifier-shaped tokens with a
	// near-zero stop-word ratio. On its own this can be a legitimate
	// path-ish or signature-ish query, so it only counts as soup when
	// at least one boolean operator is also present.
	tokens := strings.Fields(q)
	if len(tokens) >= 8 && boolOps >= 1 {
		stop, identShaped := 0, 0
		for _, t := range tokens {
			lower := strings.ToLower(strings.Trim(t, "'\"`,;()[]{}"))
			if lower == "" {
				continue
			}
			if _, ok := soupStopWords[lower]; ok {
				stop++
			}
			if isIdentifierShapedToken(lower) {
				identShaped++
			}
		}
		stopRatio := float64(stop) / float64(len(tokens))
		identRatio := float64(identShaped) / float64(len(tokens))
		if stopRatio < 0.15 && identRatio > 0.6 {
			return true, "query is a long list of keywords with little connecting language; describe the target in a short phrase, or search one keyword at a time"
		}
	}

	return false, ""
}

// soupReasonBoolean is the default advice string for a boolean-OR
// soup query; reused by the search handler when it pins the
// keyword_soup class without re-running the detector.
const soupReasonBoolean = "query reads as a boolean OR-list; search ranks best on a single concept or symbol name -- run one query per disjunct, or describe the intent in plain words"

// countBooleanOperators counts standalone boolean-operator tokens in
// the query. Recognised: uppercase OR / AND as whole words, and a
// pipe (| or ||) as a whole token or wedged between two word
// characters (`a|b`). A lowercase "or"/"and" is treated as prose and
// does NOT count -- that is what keeps genuine sentences out of the
// soup bucket.
func countBooleanOperators(q string) int {
	n := 0
	for _, tok := range strings.Fields(q) {
		switch tok {
		case "OR", "AND", "||", "|":
			n++
			continue
		}
		// An interior pipe (`auth|login`) is a disjunction even when
		// not whitespace-separated.
		if strings.Contains(tok, "|") && tok != "|" {
			// Count each pipe -- `a|b|c` is two disjunctions.
			n += strings.Count(tok, "|")
		}
	}
	return n
}

// countQuotedFragments counts balanced single- or double-quoted
// substrings. An unbalanced trailing quote is ignored.
func countQuotedFragments(q string) int {
	n := 0
	for _, quote := range []byte{'\'', '"'} {
		count := strings.Count(q, string(quote))
		n += count / 2
	}
	return n
}

// isIdentifierShapedToken reports whether a token looks like a code
// identifier or a single content word rather than connective tissue
// -- it is non-empty and made only of letters, digits, underscores,
// and the common namespace separators. Used by the keyword-density
// heuristic; intentionally permissive because the density check is
// already gated behind a boolean-operator co-signal.
func isIdentifierShapedToken(t string) bool {
	if t == "" {
		return false
	}
	for _, r := range t {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '_', '.', '/', '-', ':':
			continue
		}
		return false
	}
	return true
}

// SplitSoupFragments breaks a keyword-soup query into its disjunct
// fragments. It splits on the boolean operators LooksLikeKeywordSoup
// recognises (OR / AND / | / ||), unwraps surrounding quotes, trims
// each fragment, and drops empties and duplicates. The result feeds
// the BM25 OR-merge path so each disjunct is retrieved on its own
// terms instead of competing inside one mangled query.
func SplitSoupFragments(query string) []string {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	// Normalise the pipe operators to a word boundary so a single
	// Fields pass handles both `a OR b` and `a|b`.
	q = strings.ReplaceAll(q, "||", " | ")
	q = strings.ReplaceAll(q, "|", " | ")
	raw := strings.Fields(q)

	var (
		out  []string
		cur  []string
		seen = map[string]struct{}{}
	)
	flush := func() {
		if len(cur) == 0 {
			return
		}
		frag := unquoteFragment(strings.Join(cur, " "))
		cur = cur[:0]
		frag = strings.TrimSpace(frag)
		if frag == "" {
			return
		}
		key := strings.ToLower(frag)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, frag)
	}
	for _, tok := range raw {
		switch tok {
		case "OR", "AND", "|":
			flush()
			continue
		}
		cur = append(cur, tok)
	}
	flush()
	return out
}

// unquoteFragment strips a single matched pair of surrounding single
// or double quotes from a fragment. An unbalanced or absent quote
// leaves the string unchanged.
func unquoteFragment(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
