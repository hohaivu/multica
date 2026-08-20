package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/multica-ai/multica/server/internal/cli"
)

// MentionRe (server/internal/util/mention.go) recognizes only the full
// [@Name](mention://agent/<id>) link form — it is the sole input to both the
// task-trigger scan and the notification listener. A bare "@name" notifies
// nobody and enqueues no task; the runtime brief now says so (writeMentions),
// but a prompt is advisory, so this is the enforcement backstop on the one
// command that publishes agent-authored comments (VUH-140's second collateral
// bug: the fixer's handoff said plain "@tech-lead" and the escalation went
// nowhere).
//
// agentOrSquadKinds excludes members deliberately: a dropped mention of a
// human still notifies nobody, but only an agent/squad miss also drops a task
// trigger — the more severe, in-scope failure this guard targets.
var agentOrSquadKinds = assigneeKinds{agent: true, squad: true}

// bareMentionRe matches a candidate "@token". '/' and '.' are excluded from
// the token body itself so an npm scope's package half and a domain's later
// labels never even get captured; findBareMentionCandidates below rejects the
// match entirely when '/' or '.' follows, rather than silently truncating it.
var bareMentionRe = regexp.MustCompile(`@[A-Za-z0-9][A-Za-z0-9_-]*`)

// proseTextSegments returns body's plain text with link/image/autolink text
// and code (spans, fenced or indented blocks) removed. A real mention link's
// "@Name" lives inside a Link node — excluded the same as a code span
// quoting "@Name" as an example; neither is a bare mention in prose.
func proseTextSegments(body string) []string {
	source := []byte(body)
	doc := goldmark.New().Parser().Parse(text.NewReader(source))

	var segments []string
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindCodeSpan, ast.KindCodeBlock, ast.KindFencedCodeBlock,
			ast.KindLink, ast.KindImage, ast.KindAutoLink:
			return ast.WalkSkipChildren, nil
		}
		if t, ok := n.(*ast.Text); ok {
			segments = append(segments, string(t.Segment.Value(source)))
		}
		return ast.WalkContinue, nil
	})
	return segments
}

// findBareMentionCandidates extracts "@token" names from plain prose text,
// rejecting the two shapes real mentions never take: an '@' glued to a
// preceding letter/digit (an email's "user@example.com"), and a token
// immediately followed by '/' or '.' (an npm scope "@anthropic-ai/sdk", a
// domain, or a CSS "@media" query — the latter simply won't resolve to any
// agent/squad either, but rejecting it here skips the wasted lookup).
func findBareMentionCandidates(s string) []string {
	var out []string
	for _, m := range bareMentionRe.FindAllStringIndex(s, -1) {
		start, end := m[0], m[1]
		if start > 0 {
			prev, _ := utf8.DecodeLastRuneInString(s[:start])
			if unicode.IsLetter(prev) || unicode.IsDigit(prev) {
				continue
			}
		}
		if end < len(s) && (s[end] == '/' || s[end] == '.') {
			continue
		}
		out = append(out, s[start+1:end])
	}
	return out
}

// guardBareMentions fails the command when an agent-authored body writes a
// bare "@name" that resolves to a real agent or squad but carries no
// mention:// link. Returns nil outside an agent task context. A candidate
// that resolves to nothing is normal prose (a word that happens to start
// with '@') and is not an error — the resolve step, not token shape, is what
// keeps this from false-positiving.
func guardBareMentions(ctx context.Context, client *cli.APIClient, body, field string) error {
	if !inAgentExecutionContext() {
		return nil
	}

	var dropped []string
	seen := make(map[string]struct{})
	for _, segment := range proseTextSegments(body) {
		for _, candidate := range findBareMentionCandidates(segment) {
			key := strings.ToLower(candidate)
			if _, dup := seen[key]; dup {
				continue
			}
			if _, _, err := resolveAssignee(ctx, client, candidate, agentOrSquadKinds); err != nil {
				continue
			}
			seen[key] = struct{}{}
			dropped = append(dropped, "@"+candidate)
		}
	}
	if len(dropped) == 0 {
		return nil
	}

	return fmt.Errorf("%s contains a bare mention with no mention:// link — it notifies nobody and enqueues nothing: %s. "+
		"Use the full link form instead: [@Name](mention://agent/<agent-id>) or [@Name](mention://squad/<squad-id>) "+
		"(get the id from `multica agent get`/`multica squad get`), or drop the @ if you did not mean to mention anyone.",
		field, strings.Join(dropped, ", "))
}
