package upstream

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// prepareTelegramSegments - ported from telegram.markdown.utest.js
// ---------------------------------------------------------------------------

func TestPrepareTelegramSegments_PlainText(t *testing.T) {
	// telegram.markdown.utest.js: "must return an array of messages"
	segments, err := prepareTelegramSegments("plain", "hi there")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}
	if segments[0].Text != "hi there" {
		t.Fatalf("expected 'hi there', got %q", segments[0].Text)
	}
	if segments[0].ParseMode != "" {
		t.Fatalf("plain text should have no ParseMode, got %q", segments[0].ParseMode)
	}
}

func TestPrepareTelegramSegments_MarkdownBold(t *testing.T) {
	// telegram.markdown.utest.js: bold conversion
	segments, err := prepareTelegramSegments("markdown", "**Bold** text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}
	if segments[0].ParseMode != "HTML" {
		t.Fatalf("expected ParseMode 'HTML', got %q", segments[0].ParseMode)
	}
	if !strings.Contains(segments[0].Text, "<strong>Bold</strong>") {
		t.Fatalf("expected <strong>Bold</strong> in output, got %q", segments[0].Text)
	}
}

func TestPrepareTelegramSegments_MarkdownItalic(t *testing.T) {
	segments, err := prepareTelegramSegments("markdown", "_italic_ text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0].ParseMode != "HTML" {
		t.Fatalf("expected ParseMode 'HTML', got %q", segments[0].ParseMode)
	}
	if !strings.Contains(segments[0].Text, "<em>italic</em>") {
		t.Fatalf("expected <em>italic</em>, got %q", segments[0].Text)
	}
}

func TestPrepareTelegramSegments_MarkdownStrikethrough(t *testing.T) {
	segments, err := prepareTelegramSegments("markdown", "~~deleted~~ text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0].Text, "<del>deleted</del>") {
		t.Fatalf("expected <del>deleted</del>, got %q", segments[0].Text)
	}
}

func TestPrepareTelegramSegments_MarkdownMixedFormatting(t *testing.T) {
	// slack.markdown.utest.js: mixed formatting in one paragraph
	md := "Hello, **world**! _How_ are you? ~~Not~~ so well! But `never` mind."
	segments, err := prepareTelegramSegments("markdown", md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := segments[0].Text
	if !strings.Contains(text, "<strong>world</strong>") {
		t.Fatalf("expected <strong>, got %q", text)
	}
	if !strings.Contains(text, "<em>How</em>") {
		t.Fatalf("expected <em>, got %q", text)
	}
	if !strings.Contains(text, "<del>Not</del>") {
		t.Fatalf("expected <del>, got %q", text)
	}
	if !strings.Contains(text, "<code>never</code>") {
		t.Fatalf("expected <code>, got %q", text)
	}
}

func TestPrepareTelegramSegments_MarkdownCodeBlock(t *testing.T) {
	md := "```javascript\nconst x = 42;\n```"
	segments, err := prepareTelegramSegments("markdown", md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0].Text, "<pre>") {
		t.Fatalf("expected <pre> in output, got %q", segments[0].Text)
	}
	if !strings.Contains(segments[0].Text, "const x = 42;") {
		t.Fatalf("expected code content preserved, got %q", segments[0].Text)
	}
	if segments[0].ParseMode != "HTML" {
		t.Fatalf("expected ParseMode 'HTML', got %q", segments[0].ParseMode)
	}
}

func TestPrepareTelegramSegments_MarkdownLink(t *testing.T) {
	md := "Visit [Example](https://example.com)"
	segments, err := prepareTelegramSegments("markdown", md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0].Text, "https://example.com") {
		t.Fatalf("expected link URL in output, got %q", segments[0].Text)
	}
}

func TestPrepareTelegramSegments_MarkdownList(t *testing.T) {
	// slack.markdown.utest.js: list conversion
	md := "- Item 1\n- Item 2\n- Item 3"
	segments, err := prepareTelegramSegments("markdown", md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0].Text, "<li>") {
		t.Fatalf("expected <li> elements, got %q", segments[0].Text)
	}
}

func TestPrepareTelegramSegments_MarkdownHeading(t *testing.T) {
	segments, err := prepareTelegramSegments("markdown", "# Hello World")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0].Text, "<h1>") {
		t.Fatalf("expected <h1>, got %q", segments[0].Text)
	}
}

func TestPrepareTelegramSegments_HTMLPassthrough(t *testing.T) {
	html := "<b>Hello</b> <i>world</i>"
	segments, err := prepareTelegramSegments("html", html)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0].Text != html {
		t.Fatalf("expected HTML passed through, got %q", segments[0].Text)
	}
	if segments[0].ParseMode != "HTML" {
		t.Fatalf("expected ParseMode 'HTML', got %q", segments[0].ParseMode)
	}
}

func TestPrepareTelegramSegments_EmptyTextError(t *testing.T) {
	_, err := prepareTelegramSegments("plain", "")
	if err == nil {
		t.Fatal("expected error for empty text")
	}
	_, err = prepareTelegramSegments("plain", "   ")
	if err == nil {
		t.Fatal("expected error for whitespace-only text")
	}
}

func TestPrepareTelegramSegments_InvalidFormat(t *testing.T) {
	_, err := prepareTelegramSegments("xml", "text")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestPrepareTelegramSegments_LongMarkdownSplits(t *testing.T) {
	// Build a long markdown document that exceeds the 3500 char limit.
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("This is paragraph ")
		sb.WriteString(strings.Repeat("x", 30))
		sb.WriteString(".\n\n")
	}
	segments, err := prepareTelegramSegments("markdown", sb.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) < 2 {
		t.Fatalf("expected multiple segments for long text, got %d", len(segments))
	}
	for i, seg := range segments {
		if seg.ParseMode != "HTML" {
			t.Fatalf("segment %d: expected ParseMode 'HTML', got %q", i, seg.ParseMode)
		}
		if len([]rune(seg.Text)) > 3500 {
			t.Fatalf("segment %d exceeds 3500 rune limit: %d", i, len([]rune(seg.Text)))
		}
	}
}

func TestPrepareTelegramSegments_SpecialCharsPreserved(t *testing.T) {
	// telegram.markdown.utest.js: text with dots and URLs
	md := "I'm unable to access your calendar. Please visit https://chatbotkit.com/secret/abc123 for info."
	segments, err := prepareTelegramSegments("markdown", md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0].Text, "chatbotkit.com") {
		t.Fatalf("expected URL preserved, got %q", segments[0].Text)
	}
}

func TestPrepareTelegramSegments_EmojiPreserved(t *testing.T) {
	// discord.markdown.utest.js: emoji handling
	segments, err := prepareTelegramSegments("markdown", "Hello 🚀 world 你好")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0].Text, "🚀") {
		t.Fatalf("expected emoji preserved, got %q", segments[0].Text)
	}
	if !strings.Contains(segments[0].Text, "你好") {
		t.Fatalf("expected CJK chars preserved, got %q", segments[0].Text)
	}
}

func TestPrepareTelegramSegments_MarkdownCodeBlockWithBlankLines(t *testing.T) {
	// Validates code blocks with blank lines aren't split prematurely.
	md := "Intro.\n\n```\nline 1\n\nline 2\n```\n\nOutro."
	segments, err := prepareTelegramSegments("markdown", md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With the 3500 limit, everything fits in one segment.
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}
	if !strings.Contains(segments[0].Text, "line 1") || !strings.Contains(segments[0].Text, "line 2") {
		t.Fatalf("expected code block intact, got %q", segments[0].Text)
	}
}

func TestPrepareTelegramSegments_MarkdownTable(t *testing.T) {
	md := "| A | B |\n|---|---|\n| 1 | 2 |"
	segments, err := prepareTelegramSegments("markdown", md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0].Text, "<table>") {
		t.Fatalf("expected <table> in output, got %q", segments[0].Text)
	}
}

func TestPrepareTelegramSegments_MarkdownBlockquote(t *testing.T) {
	md := "> This is a quote"
	segments, err := prepareTelegramSegments("markdown", md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0].Text, "<blockquote>") {
		t.Fatalf("expected <blockquote>, got %q", segments[0].Text)
	}
}

// ---------------------------------------------------------------------------
// prepareDiscordSegments - ported from discord.markdown.utest.js
// ---------------------------------------------------------------------------

func TestPrepareDiscordSegments_PlainText(t *testing.T) {
	// discord.markdown.utest.js: "should create message with text type"
	segments, err := prepareDiscordSegments("plain", "Test message")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}
	if segments[0] != "Test message" {
		t.Fatalf("expected 'Test message', got %q", segments[0])
	}
}

func TestPrepareDiscordSegments_MarkdownPreserved(t *testing.T) {
	// discord.markdown.utest.js: "should preserve original markdown text"
	// Discord natively supports markdown, so it passes through.
	md := "# Heading\n\n**bold** and *italic*"
	segments, err := prepareDiscordSegments("markdown", md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Text should be preserved (not converted to HTML).
	joined := strings.Join(segments, "\n\n")
	if !strings.Contains(joined, "**bold**") {
		t.Fatalf("expected markdown preserved, got %q", joined)
	}
	if !strings.Contains(joined, "*italic*") {
		t.Fatalf("expected markdown preserved, got %q", joined)
	}
}

func TestPrepareDiscordSegments_HTMLStripped(t *testing.T) {
	// Discord does not render HTML; tags are stripped to plain text.
	segments, err := prepareDiscordSegments("html", "<b>bold</b>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0] != "bold" {
		t.Fatalf("expected HTML stripped to %q, got %q", "bold", segments[0])
	}
}

func TestPrepareDiscordSegments_EmptyTextError(t *testing.T) {
	_, err := prepareDiscordSegments("plain", "")
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestPrepareDiscordSegments_LongMessageSplits(t *testing.T) {
	// discord.markdown.utest.js: "should handle very long text"
	input := strings.Repeat("a", 5000)
	segments, err := prepareDiscordSegments("plain", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) < 3 {
		t.Fatalf("expected >=3 segments for 5000 chars at 1900 limit, got %d", len(segments))
	}
	for i, seg := range segments {
		if len([]rune(seg)) > 1900 {
			t.Fatalf("segment %d exceeds Discord 1900 limit: %d runes", i, len([]rune(seg)))
		}
	}
}

func TestPrepareDiscordSegments_EmojiPreserved(t *testing.T) {
	// discord.markdown.utest.js: "should handle markdown with special characters"
	segments, err := prepareDiscordSegments("plain", "Test with émojis 🚀 and spëcial çharacters")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0], "🚀") {
		t.Fatalf("expected emoji preserved, got %q", segments[0])
	}
}

func TestPrepareDiscordSegments_UnicodePreserved(t *testing.T) {
	// discord.markdown.utest.js: "should handle markdown with unicode"
	segments, err := prepareDiscordSegments("plain", "Unicode: 你好世界 مرحبا العالم")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0], "你好世界") {
		t.Fatalf("expected CJK chars, got %q", segments[0])
	}
}

func TestPrepareDiscordSegments_CodeBlockPreserved(t *testing.T) {
	// discord.markdown.utest.js: "should handle markdown with code blocks"
	md := "```javascript\nconst x = 1;\n```"
	segments, err := prepareDiscordSegments("markdown", md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0], "```javascript") {
		t.Fatalf("expected code block preserved, got %q", segments[0])
	}
}

func TestPrepareDiscordSegments_MultilineSplitsOnParagraph(t *testing.T) {
	// discord.markdown.utest.js: "should handle multiline markdown"
	input := "Line 1\nLine 2\nLine 3\n\nLine 5"
	segments, err := prepareDiscordSegments("plain", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Everything fits in one chunk.
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}
}

func TestPrepareDiscordSegments_InvalidFormat(t *testing.T) {
	_, err := prepareDiscordSegments("xml", "text")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

// ---------------------------------------------------------------------------
// prepareSlackSegments - ported from slack.markdown.utest.js
// ---------------------------------------------------------------------------

func TestPrepareSlackSegments_PlainText(t *testing.T) {
	segments, err := prepareSlackSegments("plain", "Hello, world!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) != 1 || segments[0] != "Hello, world!" {
		t.Fatalf("expected 'Hello, world!' in 1 segment, got %v", segments)
	}
}

func TestPrepareSlackSegments_MarkdownPreserved(t *testing.T) {
	// Slack has its own mrkdwn but our connector passes through markdown as-is.
	md := "**Bold** and _italic_"
	segments, err := prepareSlackSegments("markdown", md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0] != md {
		t.Fatalf("expected markdown preserved, got %q", segments[0])
	}
}

func TestPrepareSlackSegments_EmptyTextError(t *testing.T) {
	_, err := prepareSlackSegments("plain", "")
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestPrepareSlackSegments_LongMessageSplits(t *testing.T) {
	// Slack supports up to 40000 chars per message, our limit is 30000.
	input := strings.Repeat("a", 60000)
	segments, err := prepareSlackSegments("plain", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) < 2 {
		t.Fatalf("expected >=2 segments for 60000 chars at 30000 limit, got %d", len(segments))
	}
	for i, seg := range segments {
		if len([]rune(seg)) > 30000 {
			t.Fatalf("segment %d exceeds Slack 30000 limit: %d runes", i, len([]rune(seg)))
		}
	}
}

func TestPrepareSlackSegments_InvalidFormat(t *testing.T) {
	_, err := prepareSlackSegments("xml", "text")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

// ---------------------------------------------------------------------------
// prepareMattermostSegments - ported from generic patterns
// ---------------------------------------------------------------------------

func TestPrepareMattermostSegments_PlainText(t *testing.T) {
	segments, err := prepareMattermostSegments("plain", "Hello Mattermost!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) != 1 || segments[0] != "Hello Mattermost!" {
		t.Fatalf("expected single segment, got %v", segments)
	}
}

func TestPrepareMattermostSegments_MarkdownPreserved(t *testing.T) {
	// Mattermost natively supports markdown, so it passes through.
	md := "# Heading\n\n**bold** and _italic_"
	segments, err := prepareMattermostSegments("markdown", md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(segments, "\n\n")
	if !strings.Contains(joined, "**bold**") {
		t.Fatalf("expected markdown preserved, got %q", joined)
	}
}

func TestPrepareMattermostSegments_EmptyTextError(t *testing.T) {
	_, err := prepareMattermostSegments("plain", "")
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestPrepareMattermostSegments_LongMessageSplits(t *testing.T) {
	input := strings.Repeat("a", 25000)
	segments, err := prepareMattermostSegments("plain", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) < 2 {
		t.Fatalf("expected >=2 segments for 25000 chars at 12000 limit, got %d", len(segments))
	}
	for i, seg := range segments {
		if len([]rune(seg)) > 12000 {
			t.Fatalf("segment %d exceeds Mattermost 12000 limit: %d runes", i, len([]rune(seg)))
		}
	}
}

func TestPrepareMattermostSegments_InvalidFormat(t *testing.T) {
	_, err := prepareMattermostSegments("rtf", "text")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

// ---------------------------------------------------------------------------
// Cross-connector consistency: all connectors reject empty text
// ---------------------------------------------------------------------------

func TestAllConnectors_RejectEmptyText(t *testing.T) {
	for _, format := range []string{"plain", "markdown", "html"} {
		if _, err := prepareTelegramSegments(format, "   "); err == nil {
			t.Fatalf("telegram: expected error for whitespace with format %q", format)
		}
		if _, err := prepareDiscordSegments(format, "   "); err == nil {
			t.Fatalf("discord: expected error for whitespace with format %q", format)
		}
		if _, err := prepareSlackSegments(format, "   "); err == nil {
			t.Fatalf("slack: expected error for whitespace with format %q", format)
		}
		if _, err := prepareMattermostSegments(format, "   "); err == nil {
			t.Fatalf("mattermost: expected error for whitespace with format %q", format)
		}
	}
}

// ---------------------------------------------------------------------------
// Cross-connector consistency: all connectors reject invalid format
// ---------------------------------------------------------------------------

func TestAllConnectors_RejectInvalidFormat(t *testing.T) {
	if _, err := prepareTelegramSegments("xml", "text"); err == nil {
		t.Fatal("telegram: expected error for invalid format")
	}
	if _, err := prepareDiscordSegments("xml", "text"); err == nil {
		t.Fatal("discord: expected error for invalid format")
	}
	if _, err := prepareSlackSegments("xml", "text"); err == nil {
		t.Fatal("slack: expected error for invalid format")
	}
	if _, err := prepareMattermostSegments("xml", "text"); err == nil {
		t.Fatal("mattermost: expected error for invalid format")
	}
}

// ---------------------------------------------------------------------------
// Cross-connector: default format (empty string) treated as plain
// ---------------------------------------------------------------------------

func TestAllConnectors_EmptyFormatDefaultsToPlain(t *testing.T) {
	// NormalizeFormat("") → "plain", so all connectors should handle it.
	tgSegs, err := prepareTelegramSegments("", "hello")
	if err != nil {
		t.Fatalf("telegram: unexpected error: %v", err)
	}
	if tgSegs[0].ParseMode != "" {
		t.Fatalf("telegram: expected no ParseMode for default format, got %q", tgSegs[0].ParseMode)
	}

	dcSegs, err := prepareDiscordSegments("", "hello")
	if err != nil {
		t.Fatalf("discord: unexpected error: %v", err)
	}
	if dcSegs[0] != "hello" {
		t.Fatalf("discord: expected 'hello', got %q", dcSegs[0])
	}

	slSegs, err := prepareSlackSegments("", "hello")
	if err != nil {
		t.Fatalf("slack: unexpected error: %v", err)
	}
	if slSegs[0] != "hello" {
		t.Fatalf("slack: expected 'hello', got %q", slSegs[0])
	}

	mmSegs, err := prepareMattermostSegments("", "hello")
	if err != nil {
		t.Fatalf("mattermost: unexpected error: %v", err)
	}
	if mmSegs[0] != "hello" {
		t.Fatalf("mattermost: expected 'hello', got %q", mmSegs[0])
	}
}

// ---------------------------------------------------------------------------
// IRC segments
// ---------------------------------------------------------------------------

func TestPrepareIRCSegments_PlainPassthrough(t *testing.T) {
	segments, err := prepareIRCSegments("plain", "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) != 1 || segments[0] != "hello world" {
		t.Fatalf("expected [hello world], got %v", segments)
	}
}

func TestPrepareIRCSegments_MarkdownStripped(t *testing.T) {
	segments, err := prepareIRCSegments("markdown", "**bold** and _italic_")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(segments[0], "bold") && !strings.Contains(segments[0], "italic") {
		t.Fatalf("expected stripped text with bold/italic, got %q", segments[0])
	}
	if strings.Contains(segments[0], "**") || strings.Contains(segments[0], "<") {
		t.Fatalf("expected no markdown or HTML in output, got %q", segments[0])
	}
}

func TestPrepareIRCSegments_HTMLStripped(t *testing.T) {
	segments, err := prepareIRCSegments("html", "<b>bold</b> and <i>italic</i>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0] != "bold and italic" {
		t.Fatalf("expected %q, got %q", "bold and italic", segments[0])
	}
}

func TestPrepareIRCSegments_NewlinesSplitIntoLines(t *testing.T) {
	segments, err := prepareIRCSegments("plain", "line1\nline2\nline3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) != 3 {
		t.Fatalf("expected 3 segments for 3 lines, got %d", len(segments))
	}
}

func TestPrepareIRCSegments_LongLineSplits(t *testing.T) {
	longLine := strings.Repeat("a", 500)
	segments, err := prepareIRCSegments("plain", longLine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) < 2 {
		t.Fatalf("expected long line to split, got %d segments", len(segments))
	}
}

func TestPrepareIRCSegments_EmptyTextError(t *testing.T) {
	_, err := prepareIRCSegments("plain", "   ")
	if err == nil {
		t.Fatal("expected error for whitespace text")
	}
}

// ---------------------------------------------------------------------------
// Matrix segments
// ---------------------------------------------------------------------------

func TestPrepareMatrixSegments_PlainNoFormat(t *testing.T) {
	segments, err := prepareMatrixSegments("plain", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}
	if segments[0].Body != "hello" {
		t.Fatalf("expected body %q, got %q", "hello", segments[0].Body)
	}
	if segments[0].Format != "" {
		t.Fatalf("expected no format for plain, got %q", segments[0].Format)
	}
}

func TestPrepareMatrixSegments_MarkdownToHTML(t *testing.T) {
	segments, err := prepareMatrixSegments("markdown", "**bold**")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0].Format != "org.matrix.custom.html" {
		t.Fatalf("expected org.matrix.custom.html, got %q", segments[0].Format)
	}
	if !strings.Contains(segments[0].FormattedBody, "<strong>") {
		t.Fatalf("expected <strong> in FormattedBody, got %q", segments[0].FormattedBody)
	}
	// Body should be plain-text fallback
	if strings.Contains(segments[0].Body, "<") {
		t.Fatalf("Body should be plain text, got %q", segments[0].Body)
	}
}

func TestPrepareMatrixSegments_HTMLPassthrough(t *testing.T) {
	segments, err := prepareMatrixSegments("html", "<b>bold</b>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0].Format != "org.matrix.custom.html" {
		t.Fatalf("expected org.matrix.custom.html, got %q", segments[0].Format)
	}
	if segments[0].FormattedBody != "<b>bold</b>" {
		t.Fatalf("expected FormattedBody %q, got %q", "<b>bold</b>", segments[0].FormattedBody)
	}
	if segments[0].Body != "bold" {
		t.Fatalf("expected Body %q, got %q", "bold", segments[0].Body)
	}
}

func TestPrepareMatrixSegments_EmptyTextError(t *testing.T) {
	_, err := prepareMatrixSegments("plain", "   ")
	if err == nil {
		t.Fatal("expected error for whitespace text")
	}
}

// ---------------------------------------------------------------------------
// Zulip segments
// ---------------------------------------------------------------------------

func TestPrepareZulipSegments_MarkdownPassthrough(t *testing.T) {
	segments, err := prepareZulipSegments("markdown", "**bold**")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Zulip renders markdown natively, so it passes through.
	if segments[0] != "**bold**" {
		t.Fatalf("expected markdown passthrough, got %q", segments[0])
	}
}

func TestPrepareZulipSegments_HTMLStripped(t *testing.T) {
	segments, err := prepareZulipSegments("html", "<b>bold</b>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0] != "bold" {
		t.Fatalf("expected stripped HTML, got %q", segments[0])
	}
}

func TestPrepareZulipSegments_EmptyTextError(t *testing.T) {
	_, err := prepareZulipSegments("plain", "   ")
	if err == nil {
		t.Fatal("expected error for whitespace text")
	}
}

// ---------------------------------------------------------------------------
// Twilio segments
// ---------------------------------------------------------------------------

func TestPrepareTwilioSegments_PlainPassthrough(t *testing.T) {
	segments, err := prepareTwilioSegments("plain", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0] != "hello" {
		t.Fatalf("expected %q, got %q", "hello", segments[0])
	}
}

func TestPrepareTwilioSegments_MarkdownStripped(t *testing.T) {
	segments, err := prepareTwilioSegments("markdown", "**bold**")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(segments[0], "**") || strings.Contains(segments[0], "<") {
		t.Fatalf("expected no formatting in output, got %q", segments[0])
	}
}

func TestPrepareTwilioSegments_HTMLStripped(t *testing.T) {
	segments, err := prepareTwilioSegments("html", "<b>bold</b>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0] != "bold" {
		t.Fatalf("expected %q, got %q", "bold", segments[0])
	}
}

func TestPrepareTwilioSegments_LongMessageSplits(t *testing.T) {
	// SMS limit is 1600 chars
	input := strings.Repeat("a", 3500)
	segments, err := prepareTwilioSegments("plain", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) < 2 {
		t.Fatalf("expected splitting at 1600 chars, got %d segments", len(segments))
	}
}

func TestPrepareTwilioSegments_EmptyTextError(t *testing.T) {
	_, err := prepareTwilioSegments("plain", "   ")
	if err == nil {
		t.Fatal("expected error for whitespace text")
	}
}

// ---------------------------------------------------------------------------
// WhatsApp segments
// ---------------------------------------------------------------------------

func TestPrepareWhatsAppSegments_PlainPassthrough(t *testing.T) {
	segments, err := prepareWhatsAppSegments("plain", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0] != "hello" {
		t.Fatalf("expected %q, got %q", "hello", segments[0])
	}
}

func TestPrepareWhatsAppSegments_MarkdownStripped(t *testing.T) {
	segments, err := prepareWhatsAppSegments("markdown", "**bold**")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(segments[0], "**") || strings.Contains(segments[0], "<") {
		t.Fatalf("expected no formatting in output, got %q", segments[0])
	}
}

func TestPrepareWhatsAppSegments_HTMLStripped(t *testing.T) {
	segments, err := prepareWhatsAppSegments("html", "<b>bold</b>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0] != "bold" {
		t.Fatalf("expected %q, got %q", "bold", segments[0])
	}
}

func TestPrepareWhatsAppSegments_EmptyTextError(t *testing.T) {
	_, err := prepareWhatsAppSegments("plain", "   ")
	if err == nil {
		t.Fatal("expected error for whitespace text")
	}
}

// ---------------------------------------------------------------------------
// iMessage segments
// ---------------------------------------------------------------------------

func TestPrepareIMessageSegments_PlainPassthrough(t *testing.T) {
	segments, err := prepareIMessageSegments("plain", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0] != "hello" {
		t.Fatalf("expected %q, got %q", "hello", segments[0])
	}
}

func TestPrepareIMessageSegments_MarkdownStripped(t *testing.T) {
	segments, err := prepareIMessageSegments("markdown", "**bold**")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(segments[0], "**") || strings.Contains(segments[0], "<") {
		t.Fatalf("expected no formatting in output, got %q", segments[0])
	}
}

func TestPrepareIMessageSegments_HTMLStripped(t *testing.T) {
	segments, err := prepareIMessageSegments("html", "<b>bold</b>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0] != "bold" {
		t.Fatalf("expected %q, got %q", "bold", segments[0])
	}
}

func TestPrepareIMessageSegments_EmptyTextError(t *testing.T) {
	_, err := prepareIMessageSegments("plain", "   ")
	if err == nil {
		t.Fatal("expected error for whitespace text")
	}
}

// ---------------------------------------------------------------------------
// Slack HTML stripping
// ---------------------------------------------------------------------------

func TestPrepareSlackSegments_HTMLStripped(t *testing.T) {
	segments, err := prepareSlackSegments("html", "<b>bold</b>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0] != "bold" {
		t.Fatalf("expected %q, got %q", "bold", segments[0])
	}
}

func TestPrepareMattermostSegments_HTMLStripped(t *testing.T) {
	segments, err := prepareMattermostSegments("html", "<b>bold</b>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments[0] != "bold" {
		t.Fatalf("expected %q, got %q", "bold", segments[0])
	}
}

// ---------------------------------------------------------------------------
// Extended cross-connector consistency (all 9 connectors)
// ---------------------------------------------------------------------------

func TestAllConnectors_RejectEmptyText_Extended(t *testing.T) {
	for _, format := range []string{"plain", "markdown", "html"} {
		if _, err := prepareIRCSegments(format, "   "); err == nil {
			t.Fatalf("irc: expected error for whitespace with format %q", format)
		}
		if _, err := prepareMatrixSegments(format, "   "); err == nil {
			t.Fatalf("matrix: expected error for whitespace with format %q", format)
		}
		if _, err := prepareZulipSegments(format, "   "); err == nil {
			t.Fatalf("zulip: expected error for whitespace with format %q", format)
		}
		if _, err := prepareTwilioSegments(format, "   "); err == nil {
			t.Fatalf("twilio: expected error for whitespace with format %q", format)
		}
		if _, err := prepareWhatsAppSegments(format, "   "); err == nil {
			t.Fatalf("whatsapp: expected error for whitespace with format %q", format)
		}
		if _, err := prepareIMessageSegments(format, "   "); err == nil {
			t.Fatalf("imessage: expected error for whitespace with format %q", format)
		}
	}
}

func TestAllConnectors_RejectInvalidFormat_Extended(t *testing.T) {
	if _, err := prepareIRCSegments("xml", "text"); err == nil {
		t.Fatal("irc: expected error for invalid format")
	}
	if _, err := prepareMatrixSegments("xml", "text"); err == nil {
		t.Fatal("matrix: expected error for invalid format")
	}
	if _, err := prepareZulipSegments("xml", "text"); err == nil {
		t.Fatal("zulip: expected error for invalid format")
	}
	if _, err := prepareTwilioSegments("xml", "text"); err == nil {
		t.Fatal("twilio: expected error for invalid format")
	}
	if _, err := prepareWhatsAppSegments("xml", "text"); err == nil {
		t.Fatal("whatsapp: expected error for invalid format")
	}
	if _, err := prepareIMessageSegments("xml", "text"); err == nil {
		t.Fatal("imessage: expected error for invalid format")
	}
}

// ===========================================================================
// BAD PATH / EDGE CASE TESTS - CONNECTORS
// ===========================================================================

// ---------------------------------------------------------------------------
// Malformed HTML input - every connector should handle gracefully
// ---------------------------------------------------------------------------

func TestAllConnectors_MalformedHTML(t *testing.T) {
	malformedInputs := []struct {
		name string
		text string
	}{
		{"unclosed tags", "<b>bold <i>italic"},
		{"orphaned close", "</p></div>hello</span>"},
		{"only tags", "<div><span></span></div>"},
		{"broken attr", `<a href="unclosed>text</a>`},
		{"nested angles", "<<b>>text<</b>>"},
		{"script tag", `<script>alert("xss")</script>visible`},
		{"empty tags", "<><><>"},
	}

	connectors := []struct {
		name    string
		prepare func(string, string) (interface{}, error)
	}{
		{"telegram", func(f, t string) (interface{}, error) { return prepareTelegramSegments(f, t) }},
		{"slack", func(f, t string) (interface{}, error) { return prepareSlackSegments(f, t) }},
		{"discord", func(f, t string) (interface{}, error) { return prepareDiscordSegments(f, t) }},
		{"mattermost", func(f, t string) (interface{}, error) { return prepareMattermostSegments(f, t) }},
		{"irc", func(f, t string) (interface{}, error) { return prepareIRCSegments(f, t) }},
		{"matrix", func(f, t string) (interface{}, error) { return prepareMatrixSegments(f, t) }},
		{"zulip", func(f, t string) (interface{}, error) { return prepareZulipSegments(f, t) }},
		{"twilio", func(f, t string) (interface{}, error) { return prepareTwilioSegments(f, t) }},
		{"whatsapp", func(f, t string) (interface{}, error) { return prepareWhatsAppSegments(f, t) }},
		{"imessage", func(f, t string) (interface{}, error) { return prepareIMessageSegments(f, t) }},
	}

	formats := []string{"plain", "markdown", "html"}

	for _, conn := range connectors {
		for _, input := range malformedInputs {
			for _, format := range formats {
				name := conn.name + "/" + input.name + "/" + format
				t.Run(name, func(t *testing.T) {
					result, err := conn.prepare(format, input.text)
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					if result == nil {
						t.Fatal("expected non-nil result")
					}
				})
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Only-whitespace input - connectors should produce empty segments
// ---------------------------------------------------------------------------

func TestAllConnectors_WhitespaceOnlyText(t *testing.T) {
	whitespaceInputs := []string{
		"   ",
		"\n\n",
		"\t\t",
		" \n \t \n ",
	}

	// Connectors correctly reject whitespace-only text (treated as empty).
	// Verify they return errors, not panics.
	for _, input := range whitespaceInputs {
		t.Run("telegram", func(t *testing.T) {
			_, err := prepareTelegramSegments("plain", input)
			if err == nil {
				t.Fatal("expected error for whitespace-only input")
			}
		})
		t.Run("slack", func(t *testing.T) {
			_, err := prepareSlackSegments("plain", input)
			if err == nil {
				t.Fatal("expected error for whitespace-only input")
			}
		})
		t.Run("irc", func(t *testing.T) {
			_, err := prepareIRCSegments("plain", input)
			if err == nil {
				t.Fatal("expected error for whitespace-only input")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Very large input - close to or exceeding per-connector limits
// ---------------------------------------------------------------------------

func TestTelegram_ExactlyAtLimit(t *testing.T) {
	input := strings.Repeat("x", 3500)
	segs, err := prepareTelegramSegments("plain", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment for text at limit, got %d", len(segs))
	}
}

func TestTelegram_OneOverLimit(t *testing.T) {
	input := strings.Repeat("x", 3501)
	segs, err := prepareTelegramSegments("plain", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) < 2 {
		t.Fatalf("expected split for text over limit, got %d segments", len(segs))
	}
}

func TestDiscord_AtLimit(t *testing.T) {
	input := strings.Repeat("y", 1900)
	segs, err := prepareDiscordSegments("plain", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
}

func TestDiscord_OverLimit(t *testing.T) {
	input := strings.Repeat("y", 1901)
	segs, err := prepareDiscordSegments("plain", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) < 2 {
		t.Fatalf("expected split, got %d segments", len(segs))
	}
}

func TestIRC_LongLine(t *testing.T) {
	// IRC limit is 400 runes per line.
	input := strings.Repeat("z", 1000)
	segs, err := prepareIRCSegments("plain", input)
	if err != nil {
		t.Fatal(err)
	}
	for i, seg := range segs {
		if len([]rune(seg)) > 400 {
			t.Fatalf("IRC segment %d exceeds 400 runes: %d", i, len([]rune(seg)))
		}
	}
}

func TestIRC_CJKMultiByte(t *testing.T) {
	// Each CJK character is 3 bytes but 1 rune. 400 CJK chars = 400 runes.
	input := strings.Repeat("漢", 400)
	segs, err := prepareIRCSegments("plain", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment for 400 CJK runes, got %d", len(segs))
	}
}

func TestIRC_CJKOverLimit(t *testing.T) {
	input := strings.Repeat("漢", 401)
	segs, err := prepareIRCSegments("plain", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) < 2 {
		t.Fatalf("expected split for 401 CJK runes, got %d segments", len(segs))
	}
}

func TestIRC_MultilineInput(t *testing.T) {
	input := "line1\nline2\nline3"
	segs, err := prepareIRCSegments("plain", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments for 3 lines, got %d", len(segs))
	}
	for _, seg := range segs {
		if strings.Contains(seg, "\n") {
			t.Fatalf("IRC segment should not contain newline: %q", seg)
		}
	}
}

func TestTwilio_AtLimit(t *testing.T) {
	input := strings.Repeat("t", 1600)
	segs, err := prepareTwilioSegments("plain", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
}

func TestTwilio_OverLimit(t *testing.T) {
	input := strings.Repeat("t", 1601)
	segs, err := prepareTwilioSegments("plain", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) < 2 {
		t.Fatalf("expected split, got %d", len(segs))
	}
}

func TestWhatsApp_VeryLargeInput(t *testing.T) {
	// WhatsApp limit is 65000.
	input := strings.Repeat("w", 130000)
	segs, err := prepareWhatsAppSegments("plain", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) < 2 {
		t.Fatalf("expected split for 130k chars, got %d", len(segs))
	}
}

func TestIMessage_AtLimit(t *testing.T) {
	input := strings.Repeat("m", 20000)
	segs, err := prepareIMessageSegments("plain", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
}

// ---------------------------------------------------------------------------
// Markdown with embedded raw HTML - connector-specific handling
// ---------------------------------------------------------------------------

func TestTelegram_MarkdownWithRawHTML(t *testing.T) {
	input := "**bold** and <u>underline</u>"
	segs, err := prepareTelegramSegments("markdown", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatal("expected non-empty segments")
	}
	// Telegram markdown path converts to HTML - both <strong> and <u> should be present.
	if segs[0].ParseMode != "HTML" {
		t.Fatalf("expected HTML parse mode, got %q", segs[0].ParseMode)
	}
	if !strings.Contains(segs[0].Text, "<strong>") {
		t.Fatalf("expected <strong> in output, got %q", segs[0].Text)
	}
}

func TestSlack_MarkdownWithRawHTML(t *testing.T) {
	input := "**bold** and <b>html bold</b>"
	segs, err := prepareSlackSegments("markdown", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatal("expected segments")
	}
	// Slack markdown path passes through - raw HTML stays in.
	if !strings.Contains(segs[0], "**bold**") {
		t.Fatalf("expected markdown preserved for slack, got %q", segs[0])
	}
}

func TestIRC_MarkdownStripped(t *testing.T) {
	input := "**bold** and _italic_"
	segs, err := prepareIRCSegments("markdown", input)
	if err != nil {
		t.Fatal(err)
	}
	for _, seg := range segs {
		if strings.Contains(seg, "**") || strings.Contains(seg, "_italic_") {
			t.Fatalf("expected markdown stripped for IRC, got %q", seg)
		}
	}
}

// ---------------------------------------------------------------------------
// Control characters in input
// ---------------------------------------------------------------------------

func TestAllConnectors_ControlCharacters(t *testing.T) {
	input := "hello\x00world\x01\x02\x03end"

	connectors := []struct {
		name    string
		prepare func(string, string) (interface{}, error)
	}{
		{"telegram", func(f, t string) (interface{}, error) { return prepareTelegramSegments(f, t) }},
		{"slack", func(f, t string) (interface{}, error) { return prepareSlackSegments(f, t) }},
		{"discord", func(f, t string) (interface{}, error) { return prepareDiscordSegments(f, t) }},
		{"irc", func(f, t string) (interface{}, error) { return prepareIRCSegments(f, t) }},
		{"matrix", func(f, t string) (interface{}, error) { return prepareMatrixSegments(f, t) }},
		{"twilio", func(f, t string) (interface{}, error) { return prepareTwilioSegments(f, t) }},
		{"whatsapp", func(f, t string) (interface{}, error) { return prepareWhatsAppSegments(f, t) }},
		{"imessage", func(f, t string) (interface{}, error) { return prepareIMessageSegments(f, t) }},
	}

	for _, conn := range connectors {
		t.Run(conn.name, func(t *testing.T) {
			result, err := conn.prepare("plain", input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			_ = result
		})
	}
}

// ---------------------------------------------------------------------------
// Matrix - specific bad paths
// ---------------------------------------------------------------------------

func TestMatrix_MalformedMarkdown(t *testing.T) {
	input := "**unclosed bold and `unclosed code"
	segs, err := prepareMatrixSegments("markdown", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatal("expected segments")
	}
	// formattedBody should be set and non-empty.
	if segs[0].FormattedBody == "" {
		t.Fatal("expected formatted body for markdown input")
	}
	// plaintext body should be set.
	if segs[0].Body == "" {
		t.Fatal("expected plain body")
	}
}

func TestMatrix_HTMLInput(t *testing.T) {
	input := "<p>paragraph</p><p>second</p>"
	segs, err := prepareMatrixSegments("html", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatal("expected segments")
	}
	if segs[0].Format != "org.matrix.custom.html" {
		t.Fatalf("expected org.matrix.custom.html format, got %q", segs[0].Format)
	}
	// Body (plain text fallback) should have HTML stripped.
	if strings.Contains(segs[0].Body, "<p>") {
		t.Fatalf("expected HTML stripped from body, got %q", segs[0].Body)
	}
}

// ---------------------------------------------------------------------------
// Zulip - markdown passthrough with edge cases
// ---------------------------------------------------------------------------

func TestZulip_MarkdownPreserved(t *testing.T) {
	input := "**bold** _italic_ `code` [link](http://x.com)"
	segs, err := prepareZulipSegments("markdown", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatal("expected segments")
	}
	if segs[0] != input {
		t.Fatalf("expected markdown passthrough, got %q", segs[0])
	}
}

func TestZulip_HTMLStripped(t *testing.T) {
	input := "<b>bold</b> <i>italic</i>"
	segs, err := prepareZulipSegments("html", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatal("expected segments")
	}
	if strings.Contains(segs[0], "<b>") || strings.Contains(segs[0], "<i>") {
		t.Fatalf("expected HTML stripped, got %q", segs[0])
	}
}

// ---------------------------------------------------------------------------
// Telegram HTML path - large HTML that triggers SplitHTML + repair
// ---------------------------------------------------------------------------

func TestTelegram_LargeHTMLWithRepair(t *testing.T) {
	// Build HTML that's larger than 3500 with nested tags.
	var sb strings.Builder
	sb.WriteString("<blockquote>")
	for i := 0; i < 50; i++ {
		sb.WriteString("<p>Paragraph " + strings.Repeat("word ", 15) + "</p>")
	}
	sb.WriteString("</blockquote>")

	segs, err := prepareTelegramSegments("html", sb.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) < 2 {
		t.Fatalf("expected splitting for large HTML, got %d segments", len(segs))
	}
	for _, seg := range segs {
		if seg.ParseMode != "HTML" {
			t.Fatalf("expected HTML parse mode, got %q", seg.ParseMode)
		}
	}
}

// ---------------------------------------------------------------------------
// Empty text after format conversion - edge case
// ---------------------------------------------------------------------------

func TestAllConnectors_EmptyTextAfterHTMLStrip(t *testing.T) {
	// HTML that contains only tags - after stripping, text is empty.
	input := "<div><span></span></div>"

	// These connectors strip HTML: slack, discord, mattermost, irc, twilio, whatsapp, imessage, zulip
	type strConnector struct {
		name    string
		prepare func(string, string) ([]string, error)
	}

	connectors := []strConnector{
		{"slack", prepareSlackSegments},
		{"discord", prepareDiscordSegments},
		{"mattermost", prepareMattermostSegments},
		{"zulip", prepareZulipSegments},
		{"twilio", prepareTwilioSegments},
		{"whatsapp", prepareWhatsAppSegments},
		{"imessage", prepareIMessageSegments},
	}

	for _, conn := range connectors {
		t.Run(conn.name, func(t *testing.T) {
			segs, err := conn.prepare("html", input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// The result should be a single segment of empty or whitespace-only text.
			// Should not panic.
			_ = segs
		})
	}
}

// ---------------------------------------------------------------------------
// Unicode edge cases - CJK, emoji, RTL
// ---------------------------------------------------------------------------

func TestTelegram_UnicodeContent(t *testing.T) {
	inputs := []struct {
		name string
		text string
	}{
		{"CJK", strings.Repeat("漢字テスト", 500)},
		{"emoji", strings.Repeat("👨‍👩‍👧‍👦🏠🌍", 200)},
		{"RTL", strings.Repeat("مرحبا بالعالم ", 200)},
		{"mixed", "Hello 世界 مرحبا 🌍 **bold**"},
	}

	for _, tt := range inputs {
		t.Run(tt.name, func(t *testing.T) {
			segs, err := prepareTelegramSegments("markdown", tt.text)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(segs) == 0 {
				t.Fatal("expected non-empty result")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tab and carriage return handling
// ---------------------------------------------------------------------------

func TestIMessage_ControlCharsInInput(t *testing.T) {
	// iMessage AppleScript escaping handles \n, \r, \t.
	input := "line1\tcolumn2\r\nline2\nline3"
	segs, err := prepareIMessageSegments("plain", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatal("expected segments")
	}
	// Verify the content passes through (actual escaping happens in sendViaAppleScript).
	if !strings.Contains(segs[0], "line1") {
		t.Fatalf("expected text preserved, got %q", segs[0])
	}
}
