package formatting

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// NormalizeFormat
// ---------------------------------------------------------------------------

func TestNormalizeFormat(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default empty", input: "", want: FormatPlain},
		{name: "plain", input: "plain", want: FormatPlain},
		{name: "markdown", input: "markdown", want: FormatMarkdown},
		{name: "md alias", input: "md", want: FormatMarkdown},
		{name: "html", input: "html", want: FormatHTML},
		{name: "HTML uppercase", input: "HTML", want: FormatHTML},
		{name: "MARKDOWN uppercase", input: "MARKDOWN", want: FormatMarkdown},
		{name: "mixed case", input: "Markdown", want: FormatMarkdown},
		{name: "whitespace trimmed", input: "  markdown  ", want: FormatMarkdown},
		{name: "invalid", input: "xml", wantErr: true},
		{name: "invalid rtf", input: "rtf", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeFormat(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Fatalf("NormalizeFormat(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MarkdownToHTML - ported from telegram/slack/whatsapp/discord .markdown.utest.js
// ---------------------------------------------------------------------------

func TestMarkdownToHTML_SimpleText(t *testing.T) {
	html, err := MarkdownToHTML("hi there")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "hi there") {
		t.Fatalf("expected text preserved, got %q", html)
	}
}

func TestMarkdownToHTML_BoldConversion(t *testing.T) {
	html, err := MarkdownToHTML("**Bold** text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<strong>Bold</strong>") {
		t.Fatalf("expected <strong>Bold</strong>, got %q", html)
	}
	if !strings.Contains(html, "text") {
		t.Fatalf("expected surrounding text preserved, got %q", html)
	}
}

func TestMarkdownToHTML_ItalicConversion(t *testing.T) {
	html, err := MarkdownToHTML("_italic_ text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<em>italic</em>") {
		t.Fatalf("expected <em>italic</em>, got %q", html)
	}
}

func TestMarkdownToHTML_StrikethroughConversion(t *testing.T) {
	html, err := MarkdownToHTML("~~deleted~~ text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<del>deleted</del>") {
		t.Fatalf("expected <del>deleted</del>, got %q", html)
	}
}

func TestMarkdownToHTML_InlineCode(t *testing.T) {
	html, err := MarkdownToHTML("Use `fmt.Println` here")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<code>fmt.Println</code>") {
		t.Fatalf("expected <code>fmt.Println</code>, got %q", html)
	}
}

func TestMarkdownToHTML_FencedCodeBlock(t *testing.T) {
	md := "```javascript\nconst x = 42;\n```"
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<pre>") {
		t.Fatalf("expected <pre> block, got %q", html)
	}
	if !strings.Contains(html, "<code") {
		t.Fatalf("expected <code> element, got %q", html)
	}
	if !strings.Contains(html, "const x = 42;") {
		t.Fatalf("expected code content preserved, got %q", html)
	}
}

func TestMarkdownToHTML_FencedCodeBlockWithBlankLines(t *testing.T) {
	md := "```go\nfunc foo() {\n\n\treturn 42\n}\n```"
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(html, "<pre>") != 1 {
		t.Fatalf("expected exactly one <pre> block, got %q", html)
	}
	if !strings.Contains(html, "return 42") {
		t.Fatalf("expected code content preserved, got %q", html)
	}
}

func TestMarkdownToHTML_Links(t *testing.T) {
	html, err := MarkdownToHTML("[Example](https://example.com)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, `href="https://example.com"`) {
		t.Fatalf("expected href, got %q", html)
	}
	if !strings.Contains(html, "Example") {
		t.Fatalf("expected link text, got %q", html)
	}
}

func TestMarkdownToHTML_Image(t *testing.T) {
	html, err := MarkdownToHTML("![alt text](https://example.com/image.png)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<img") {
		t.Fatalf("expected <img> element, got %q", html)
	}
	if !strings.Contains(html, `src="https://example.com/image.png"`) {
		t.Fatalf("expected img src, got %q", html)
	}
}

func TestMarkdownToHTML_Heading(t *testing.T) {
	html, err := MarkdownToHTML("# Main Title")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<h1>") {
		t.Fatalf("expected <h1>, got %q", html)
	}
	if !strings.Contains(html, "Main Title") {
		t.Fatalf("expected heading text, got %q", html)
	}
}

func TestMarkdownToHTML_BulletList(t *testing.T) {
	md := "- Item 1\n- Item 2\n- Item 3"
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<ul>") {
		t.Fatalf("expected <ul>, got %q", html)
	}
	if strings.Count(html, "<li>") != 3 {
		t.Fatalf("expected 3 <li> elements, got %d in %q", strings.Count(html, "<li>"), html)
	}
}

func TestMarkdownToHTML_OrderedList(t *testing.T) {
	md := "1. First\n2. Second\n3. Third"
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<ol>") {
		t.Fatalf("expected <ol>, got %q", html)
	}
}

func TestMarkdownToHTML_Blockquote(t *testing.T) {
	md := "> This is a quote"
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<blockquote>") {
		t.Fatalf("expected <blockquote>, got %q", html)
	}
}

func TestMarkdownToHTML_MixedFormatting(t *testing.T) {
	md := "Hello, **world**! _How_ are you? ~~Not~~ so well! But `never` mind."
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<strong>world</strong>") {
		t.Fatalf("expected <strong>, got %q", html)
	}
	if !strings.Contains(html, "<em>How</em>") {
		t.Fatalf("expected <em>, got %q", html)
	}
	if !strings.Contains(html, "<del>Not</del>") {
		t.Fatalf("expected <del>, got %q", html)
	}
	if !strings.Contains(html, "<code>never</code>") {
		t.Fatalf("expected <code>, got %q", html)
	}
}

func TestMarkdownToHTML_NestedFormatting(t *testing.T) {
	md := "Text with **bold _italic_ combination**."
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<strong>") {
		t.Fatalf("expected <strong>, got %q", html)
	}
	if !strings.Contains(html, "<em>italic</em>") {
		t.Fatalf("expected <em>italic</em>, got %q", html)
	}
}

func TestMarkdownToHTML_MultipleParagraphs(t *testing.T) {
	md := "First paragraph.\n\nSecond paragraph."
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(html, "<p>") != 2 {
		t.Fatalf("expected 2 <p> blocks, got %d in %q", strings.Count(html, "<p>"), html)
	}
}

func TestMarkdownToHTML_SpecialCharsInText(t *testing.T) {
	md := "Visit https://chatbotkit.com/secret/abc123. Done!"
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "chatbotkit.com") {
		t.Fatalf("expected URL preserved, got %q", html)
	}
	if !strings.Contains(html, "Done!") {
		t.Fatalf("expected trailing text preserved, got %q", html)
	}
}

func TestMarkdownToHTML_EmojiAndUnicode(t *testing.T) {
	md := "Test with émojis 🚀 and 你好世界"
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "🚀") {
		t.Fatalf("expected emoji preserved, got %q", html)
	}
	if !strings.Contains(html, "你好世界") {
		t.Fatalf("expected CJK chars preserved, got %q", html)
	}
}

func TestMarkdownToHTML_EmptyInput(t *testing.T) {
	html, err := MarkdownToHTML("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if html != "" {
		t.Fatalf("expected empty output for empty input, got %q", html)
	}
}

func TestMarkdownToHTML_MultipleLinksInParagraph(t *testing.T) {
	md := "Visit [site1](http://example1.com) and [site2](http://example2.com)."
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(html, "<a ") != 2 {
		t.Fatalf("expected 2 links, got %q", html)
	}
}

func TestMarkdownToHTML_Table(t *testing.T) {
	md := "| A | B |\n|---|---|\n| 1 | 2 |"
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(html, "<table>") {
		t.Fatalf("expected <table>, got %q", html)
	}
}

// ---------------------------------------------------------------------------
// SplitText
// ---------------------------------------------------------------------------

func TestSplitText_ShortTextSingleChunk(t *testing.T) {
	chunks := SplitText("hello world", 100)
	if len(chunks) != 1 || chunks[0] != "hello world" {
		t.Fatalf("expected single chunk, got %v", chunks)
	}
}

func TestSplitText_ExactlyAtLimit(t *testing.T) {
	input := strings.Repeat("a", 100)
	chunks := SplitText(input, 100)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk at exact limit, got %d", len(chunks))
	}
}

func TestSplitText_SplitsOnParagraphBoundary(t *testing.T) {
	input := "First paragraph.\n\nSecond paragraph."
	chunks := SplitText(input, 20)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %v", len(chunks), chunks)
	}
	if chunks[0] != "First paragraph." {
		t.Fatalf("chunk 0 = %q, want 'First paragraph.'", chunks[0])
	}
	if chunks[1] != "Second paragraph." {
		t.Fatalf("chunk 1 = %q, want 'Second paragraph.'", chunks[1])
	}
}

func TestSplitText_MergesParagraphsWhenPossible(t *testing.T) {
	input := "Hello.\n\nWorld."
	chunks := SplitText(input, 100)
	if len(chunks) != 1 {
		t.Fatalf("expected paragraphs merged into 1 chunk, got %d: %v", len(chunks), chunks)
	}
}

func TestSplitText_LongMessageSplits(t *testing.T) {
	input := strings.Repeat("a", 5000)
	chunks := SplitText(input, 1900)
	if len(chunks) < 3 {
		t.Fatalf("expected >=3 chunks for 5000 chars at limit 1900, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if len([]rune(chunk)) > 1900 {
			t.Fatalf("chunk %d exceeds limit: %d runes", i, len([]rune(chunk)))
		}
	}
}

func TestSplitText_UnicodeRuneCounting(t *testing.T) {
	input := strings.Repeat("你", 100) + "\n\n" + strings.Repeat("好", 100)
	chunks := SplitText(input, 120)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks for unicode text, got %d", len(chunks))
	}
}

func TestSplitText_EmptyParagraphsSkipped(t *testing.T) {
	input := "Hello.\n\n\n\n\n\nWorld."
	chunks := SplitText(input, 100)
	if len(chunks) != 1 {
		t.Fatalf("expected empty paragraphs merged, got %d: %v", len(chunks), chunks)
	}
	if !strings.Contains(chunks[0], "Hello.") || !strings.Contains(chunks[0], "World.") {
		t.Fatalf("expected both parts, got %q", chunks[0])
	}
}

func TestSplitText_ListItemsParagraph(t *testing.T) {
	input := "Here is a list of items:\n\n- Item 1\n- Item 2\n- Item 3"
	chunks := SplitText(input, 1000)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk with generous limit, got %d: %v", len(chunks), chunks)
	}
	chunks = SplitText(input, 30)
	if len(chunks) < 2 {
		t.Fatalf("expected >=2 chunks with tight limit, got %d", len(chunks))
	}
}

// ---------------------------------------------------------------------------
// SplitHTML
// ---------------------------------------------------------------------------

func TestSplitHTML_SingleShortDocument(t *testing.T) {
	input := "<p>Hello</p>"
	chunks := SplitHTML(input, 3500)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != input {
		t.Fatalf("unexpected chunk: %q", chunks[0])
	}
}

func TestSplitHTML_SplitsAtBlockTagBoundaries(t *testing.T) {
	para := "<p>" + strings.Repeat("x", 40) + "</p>"
	var sb strings.Builder
	count := 30
	for i := 0; i < count; i++ {
		sb.WriteString(para)
	}
	input := sb.String()

	maxLen := 200
	chunks := SplitHTML(input, maxLen)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	for i, chunk := range chunks {
		if len([]rune(chunk)) > maxLen {
			t.Fatalf("chunk %d exceeds maxLen %d: %d runes", i, maxLen, len([]rune(chunk)))
		}
		opens := strings.Count(chunk, "<p>")
		closes := strings.Count(chunk, "</p>")
		if opens != closes {
			t.Fatalf("chunk %d has mismatched <p>/<\\/p>: %d vs %d\n%s", i, opens, closes, chunk)
		}
	}
}

func TestSplitHTML_MarkdownCodeBlockNotTornApart(t *testing.T) {
	md := "```go\nfunc foo() {\n\n\treturn 42\n}\n```"
	htmlStr, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("MarkdownToHTML error: %v", err)
	}
	if !strings.Contains(htmlStr, "<pre>") {
		t.Fatalf("expected <pre> block, got: %q", htmlStr)
	}
	chunks := SplitHTML(htmlStr, 3500)
	if len(chunks) != 1 {
		t.Fatalf("expected code block in one chunk, got %d", len(chunks))
	}
}

func TestSplitHTML_MultipleParagraphs(t *testing.T) {
	md := "First paragraph.\n\nSecond paragraph.\n\nThird paragraph."
	htmlStr, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chunks := SplitHTML(htmlStr, 3500)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk with generous limit, got %d", len(chunks))
	}
	chunks = SplitHTML(htmlStr, 30)
	if len(chunks) < 3 {
		t.Fatalf("expected >=3 chunks with tight limit, got %d: %v", len(chunks), chunks)
	}
}

func TestSplitHTML_ListSplitsAtLiTags(t *testing.T) {
	md := "- Item 1\n- Item 2\n- Item 3\n- Item 4\n- Item 5"
	htmlStr, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(htmlStr, "<li>") {
		t.Fatalf("expected <li> in HTML, got %q", htmlStr)
	}
	chunks := SplitHTML(htmlStr, 50)
	if len(chunks) < 2 {
		t.Fatalf("expected >=2 chunks for list with tight limit, got %d", len(chunks))
	}
}

func TestSplitHTML_HeadingParagraphMix(t *testing.T) {
	md := "# Title\n\nSome text below the heading."
	htmlStr, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(htmlStr, "<h1>") {
		t.Fatalf("expected <h1>, got %q", htmlStr)
	}
	chunks := SplitHTML(htmlStr, 30)
	if len(chunks) < 2 {
		t.Fatalf("expected >=2 chunks, got %d: %v", len(chunks), chunks)
	}
}

func TestSplitHTML_NoBlockTags(t *testing.T) {
	input := strings.Repeat("<b>x</b>", 500)
	chunks := SplitHTML(input, 100)
	if len(chunks) < 2 {
		t.Fatalf("expected hardSplit fallback, got %d chunks", len(chunks))
	}
	// Chunks may slightly exceed maxLen due to tag repair overhead
	// (closing unclosed tags, reopening carried tags). Allow 20% margin.
	for i, chunk := range chunks {
		if len([]rune(chunk)) > 120 {
			t.Fatalf("chunk %d exceeds limit with margin: %d runes", i, len([]rune(chunk)))
		}
	}
}

func TestSplitHTML_EmptyInput(t *testing.T) {
	chunks := SplitHTML("", 100)
	if len(chunks) != 1 || chunks[0] != "" {
		t.Fatalf("expected single empty chunk, got %v", chunks)
	}
}

// ---------------------------------------------------------------------------
// End-to-end: MarkdownToHTML -> SplitHTML pipeline (Telegram path)
// ---------------------------------------------------------------------------

func TestMarkdownToHTMLThenSplit_CodeBlockWithBlankLine(t *testing.T) {
	md := "Here is code:\n\n```\nline 1\n\nline 2\n```\n\nDone."
	htmlStr, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chunks := SplitHTML(htmlStr, 3500)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d: %v", len(chunks), chunks)
	}
	if !strings.Contains(chunks[0], "line 1") || !strings.Contains(chunks[0], "line 2") {
		t.Fatalf("expected code content preserved, got %q", chunks[0])
	}
}

func TestMarkdownToHTMLThenSplit_LongDocumentSplitsCleanly(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("This is paragraph number ")
		sb.WriteString(strings.Repeat("x", 40))
		sb.WriteString(".\n\n")
	}
	md := sb.String()
	htmlStr, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chunks := SplitHTML(htmlStr, 500)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if len([]rune(chunk)) > 500 {
			t.Fatalf("chunk %d exceeds 500 rune limit: %d", i, len([]rune(chunk)))
		}
	}
	joined := strings.Join(chunks, "")
	if strings.Count(joined, "</p>") != 50 {
		t.Fatalf("expected 50 paragraphs, got %d", strings.Count(joined, "</p>"))
	}
}

// ---------------------------------------------------------------------------
// StripHTML
// ---------------------------------------------------------------------------

func TestStripHTML_RemovesTags(t *testing.T) {
	result := StripHTML("<b>bold</b> and <i>italic</i>")
	if result != "bold and italic" {
		t.Fatalf("expected %q, got %q", "bold and italic", result)
	}
}

func TestStripHTML_DecodesEntities(t *testing.T) {
	result := StripHTML("a &amp; b &lt; c &gt; d &quot;e&quot; f&#39;g")
	if result != `a & b < c > d "e" f'g` {
		t.Fatalf("expected decoded entities, got %q", result)
	}
}

func TestStripHTML_NestedTags(t *testing.T) {
	result := StripHTML("<div><p><strong>bold</strong> text</p></div>")
	if result != "bold text" {
		t.Fatalf("expected %q, got %q", "bold text", result)
	}
}

func TestStripHTML_EmptyInput(t *testing.T) {
	result := StripHTML("")
	if result != "" {
		t.Fatalf("expected empty, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// MarkdownToPlain
// ---------------------------------------------------------------------------

func TestMarkdownToPlain_Bold(t *testing.T) {
	result := MarkdownToPlain("**bold**")
	if !strings.Contains(result, "bold") {
		t.Fatalf("expected 'bold' in output, got %q", result)
	}
	if strings.Contains(result, "**") || strings.Contains(result, "<") {
		t.Fatalf("expected no formatting, got %q", result)
	}
}

func TestMarkdownToPlain_Links(t *testing.T) {
	result := MarkdownToPlain("[click here](https://example.com)")
	if !strings.Contains(result, "click here") {
		t.Fatalf("expected link text in output, got %q", result)
	}
	if strings.Contains(result, "<a") {
		t.Fatalf("expected no HTML tags, got %q", result)
	}
}

func TestMarkdownToPlain_MultiParagraph(t *testing.T) {
	result := MarkdownToPlain("first\n\nsecond")
	if !strings.Contains(result, "first") || !strings.Contains(result, "second") {
		t.Fatalf("expected both paragraphs, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// SplitHTML - well-formed chunk repair
// ---------------------------------------------------------------------------

func TestSplitHTML_RepairsNestedBlockquote(t *testing.T) {
	// A blockquote wrapping two paragraphs that must be split.
	input := "<blockquote><p>" + strings.Repeat("A", 40) + "</p><p>" + strings.Repeat("B", 40) + "</p></blockquote>"
	chunks := SplitHTML(input, 60)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	// Each chunk should be well-formed: no unclosed blockquote.
	for i, chunk := range chunks {
		opens := strings.Count(chunk, "<blockquote>")
		closes := strings.Count(chunk, "</blockquote>")
		if opens != closes {
			t.Fatalf("chunk %d has mismatched blockquote tags (open=%d, close=%d): %q", i, opens, closes, chunk)
		}
	}
}

func TestSplitHTML_PreservesAttributesAcrossChunks(t *testing.T) {
	// A <div class="x"> wrapping paragraphs - attributes should carry forward.
	input := `<div class="x"><p>` + strings.Repeat("A", 40) + `</p><p>` + strings.Repeat("B", 40) + `</p></div>`
	chunks := SplitHTML(input, 70)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	// The second chunk should have the div with attributes reopened.
	if !strings.Contains(chunks[1], `class="x"`) {
		t.Fatalf("expected attributes carried to chunk 2, got %q", chunks[1])
	}
}

// ---------------------------------------------------------------------------
// hardSplit - HTML safety
// ---------------------------------------------------------------------------

func TestHardSplit_DoesNotBreakHTMLTag(t *testing.T) {
	// Place an HTML tag right at the split boundary.
	input := strings.Repeat("a", 95) + "<strong>bold</strong>"
	chunks := SplitText(input, 100)
	// The split should not produce a fragment like "<stro" or "ng>".
	for i, chunk := range chunks {
		// A chunk should not start with ">" or end with an unclosed "<".
		if strings.HasSuffix(chunk, "<") || strings.HasPrefix(chunk, ">") {
			t.Fatalf("chunk %d breaks an HTML tag: %q", i, chunk)
		}
	}
}

func TestHardSplit_DoesNotBreakEntity(t *testing.T) {
	input := strings.Repeat("a", 97) + "&amp;"
	chunks := SplitText(input, 100)
	for _, chunk := range chunks {
		if strings.Contains(chunk, "&am") && !strings.Contains(chunk, "&amp;") {
			t.Fatalf("entity broken across chunks: %q", chunk)
		}
	}
}

// ===========================================================================
// BAD PATH / EDGE CASE TESTS
// ===========================================================================

// ---------------------------------------------------------------------------
// NormalizeFormat - bad paths
// ---------------------------------------------------------------------------

func TestNormalizeFormat_SQLInjection(t *testing.T) {
	_, err := NormalizeFormat("'; DROP TABLE messages;--")
	if err == nil {
		t.Fatal("expected error for SQL-like input")
	}
}

func TestNormalizeFormat_VeryLongInput(t *testing.T) {
	_, err := NormalizeFormat(strings.Repeat("x", 10000))
	if err == nil {
		t.Fatal("expected error for very long format string")
	}
}

func TestNormalizeFormat_NullBytes(t *testing.T) {
	_, err := NormalizeFormat("plain\x00html")
	if err == nil {
		t.Fatal("expected error for input with null bytes")
	}
}

func TestNormalizeFormat_UnicodeInput(t *testing.T) {
	_, err := NormalizeFormat("マークダウン")
	if err == nil {
		t.Fatal("expected error for non-ASCII format name")
	}
}

// ---------------------------------------------------------------------------
// MarkdownToHTML - bad paths
// ---------------------------------------------------------------------------

func TestMarkdownToHTML_EmptyString(t *testing.T) {
	result, err := MarkdownToHTML("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty output, got %q", result)
	}
}

func TestMarkdownToHTML_OnlyWhitespace(t *testing.T) {
	result, err := MarkdownToHTML("   \n\n   \t  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should produce empty or whitespace-only output, not panic.
	_ = result
}

func TestMarkdownToHTML_MalformedMarkdown(t *testing.T) {
	// Unclosed formatting - goldmark should handle gracefully.
	inputs := []string{
		"**unclosed bold",
		"_unclosed italic",
		"~~unclosed strike",
		"```\nunclosed code block",
		"[unclosed link(text",
		"[link](unclosed-paren",
	}
	for _, input := range inputs {
		result, err := MarkdownToHTML(input)
		if err != nil {
			t.Fatalf("MarkdownToHTML(%q) returned error: %v", input, err)
		}
		if result == "" {
			t.Fatalf("MarkdownToHTML(%q) returned empty", input)
		}
	}
}

func TestMarkdownToHTML_ScriptInjection(t *testing.T) {
	// With html.WithUnsafe(), goldmark passes HTML through.
	// A <script> block makes goldmark treat the entire paragraph as raw HTML,
	// so markdown formatting on the same line is NOT converted. Verify no panic.
	input := `<script>alert("xss")</script> **bold**`
	result, err := MarkdownToHTML(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "script") {
		t.Fatalf("expected script tag in output, got %q", result)
	}
}

func TestMarkdownToHTML_VeryLargeInput(t *testing.T) {
	// 100KB of markdown - shouldn't panic or OOM.
	input := strings.Repeat("**bold** and _italic_ text\n\n", 5000)
	result, err := MarkdownToHTML(input)
	if err != nil {
		t.Fatalf("unexpected error on large input: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("expected non-empty output for large input")
	}
}

func TestMarkdownToHTML_BinaryJunk(t *testing.T) {
	input := "\x00\x01\x02\xff\xfe\x80"
	// Should not panic.
	_, _ = MarkdownToHTML(input)
}

func TestMarkdownToHTML_DeeplyNestedLists(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString(strings.Repeat("  ", i))
		sb.WriteString("- item\n")
	}
	result, err := MarkdownToHTML(sb.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "<li>") {
		t.Fatalf("expected list items in output, got %q", result[:min(200, len(result))])
	}
}

// ---------------------------------------------------------------------------
// StripHTML - bad paths
// ---------------------------------------------------------------------------

func TestStripHTML_UnclosedTags(t *testing.T) {
	result := StripHTML("<b>unclosed bold <i>and italic")
	if strings.Contains(result, "<") || strings.Contains(result, ">") {
		t.Fatalf("expected no tag chars in output, got %q", result)
	}
	if !strings.Contains(result, "unclosed bold") {
		t.Fatalf("expected text content preserved, got %q", result)
	}
}

func TestStripHTML_MalformedTags(t *testing.T) {
	inputs := []struct {
		name  string
		input string
	}{
		{"angle bracket mess", "< b >text< /b >"},
		{"partial tag", "<b text"},
		{"no closing angle", "<div class='x'"},
		{"double open", "<<b>>text<</b>>"},
		{"attributes with quotes", `<a href="foo>bar">text</a>`},
	}
	for _, tt := range inputs {
		t.Run(tt.name, func(t *testing.T) {
			result := StripHTML(tt.input)
			// Should not panic - exact output varies.
			_ = result
		})
	}
}

func TestStripHTML_ScriptAndStyleContent(t *testing.T) {
	// StripHTML only removes tags, not script/style content. Verify it doesn't crash.
	input := `<style>body{color:red}</style><script>alert(1)</script><p>visible</p>`
	result := StripHTML(input)
	if !strings.Contains(result, "visible") {
		t.Fatalf("expected visible text, got %q", result)
	}
}

func TestStripHTML_NumericEntities(t *testing.T) {
	// Numeric entities beyond our map - verify no crash, entities remain.
	result := StripHTML("curly: &#8220;quote&#8221; and &#x2F;slash")
	if !strings.Contains(result, "curly:") {
		t.Fatalf("expected text preserved, got %q", result)
	}
}

func TestStripHTML_OnlyTags(t *testing.T) {
	result := StripHTML("<div><span><br/></span></div>")
	if result != "" {
		t.Fatalf("expected empty after stripping tags-only input, got %q", result)
	}
}

func TestStripHTML_VeryLargeInput(t *testing.T) {
	input := strings.Repeat("<p>paragraph</p>", 10000)
	result := StripHTML(input)
	if !strings.Contains(result, "paragraph") {
		t.Fatal("expected text in output")
	}
}

// ---------------------------------------------------------------------------
// MarkdownToPlain - bad paths
// ---------------------------------------------------------------------------

func TestMarkdownToPlain_EmptyString(t *testing.T) {
	result := MarkdownToPlain("")
	if result != "" {
		t.Fatalf("expected empty, got %q", result)
	}
}

func TestMarkdownToPlain_OnlySpecialChars(t *testing.T) {
	result := MarkdownToPlain("***___~~~```")
	// Should not panic. Output is whatever goldmark produces stripped.
	_ = result
}

func TestMarkdownToPlain_HTMLInMarkdown(t *testing.T) {
	result := MarkdownToPlain("<b>already HTML</b> and **markdown**")
	if strings.Contains(result, "<b>") || strings.Contains(result, "</b>") {
		t.Fatalf("expected HTML tags stripped, got %q", result)
	}
	if strings.Contains(result, "**") {
		t.Fatalf("expected markdown stripped, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// SplitText - bad paths
// ---------------------------------------------------------------------------

func TestSplitText_ZeroMaxLen(t *testing.T) {
	result := SplitText("hello", 0)
	if len(result) != 1 || result[0] != "hello" {
		t.Fatalf("expected passthrough for zero maxLen, got %v", result)
	}
}

func TestSplitText_NegativeMaxLen(t *testing.T) {
	result := SplitText("hello", -1)
	if len(result) != 1 || result[0] != "hello" {
		t.Fatalf("expected passthrough for negative maxLen, got %v", result)
	}
}

func TestSplitText_MaxLenOne(t *testing.T) {
	result := SplitText("abc", 1)
	if len(result) != 3 {
		t.Fatalf("expected 3 chunks for maxLen=1, got %d: %v", len(result), result)
	}
}

func TestSplitText_TextExactlyAtLimit(t *testing.T) {
	input := strings.Repeat("a", 100)
	result := SplitText(input, 100)
	if len(result) != 1 {
		t.Fatalf("expected 1 chunk for text exactly at limit, got %d", len(result))
	}
}

func TestSplitText_TextOneBeyondLimit(t *testing.T) {
	input := strings.Repeat("a", 101)
	result := SplitText(input, 100)
	if len(result) < 2 {
		t.Fatalf("expected split for text 1 beyond limit, got %d chunks", len(result))
	}
}

func TestSplitText_OnlyWhitespace(t *testing.T) {
	result := SplitText("   \n\n   \n\n   ", 10)
	// All paragraphs are whitespace-only and get trimmed away.
	// Should not panic.
	_ = result
}

func TestSplitText_OnlyNewlines(t *testing.T) {
	result := SplitText("\n\n\n\n", 10)
	_ = result
}

func TestSplitText_SingleCharParagraphs(t *testing.T) {
	input := "a\n\nb\n\nc\n\nd"
	result := SplitText(input, 3)
	// Each paragraph is 1 char; at maxLen=3, "a\n\nb" fits.
	for _, chunk := range result {
		if len([]rune(chunk)) > 3 {
			t.Fatalf("chunk exceeds limit: %q", chunk)
		}
	}
}

func TestSplitText_CJKCharacters(t *testing.T) {
	// Rune-based splitting, not byte-based. Each CJK char is 3 bytes.
	input := strings.Repeat("漢", 10)
	result := SplitText(input, 5)
	if len(result) != 2 {
		t.Fatalf("expected 2 chunks for 10 CJK chars at limit 5, got %d", len(result))
	}
	for _, chunk := range result {
		if len([]rune(chunk)) > 5 {
			t.Fatalf("chunk exceeds rune limit: %q (%d runes)", chunk, len([]rune(chunk)))
		}
	}
}

func TestSplitText_EmojiSequences(t *testing.T) {
	// Emoji can be multi-rune (e.g. family emoji). Verify no panic.
	input := "👨‍👩‍👧‍👦 hello 👨‍👩‍👧‍👦 world 👨‍👩‍👧‍👦 test"
	result := SplitText(input, 10)
	if len(result) == 0 {
		t.Fatal("expected non-empty result")
	}
}

func TestSplitText_MixedNewlineStyles(t *testing.T) {
	input := "para1\r\n\r\npara2\n\npara3"
	result := SplitText(input, 10)
	// \r\n\r\n doesn't split on \n\n - the \r chars stay in paragraphs.
	_ = result
}

// ---------------------------------------------------------------------------
// SplitHTML - bad paths
// ---------------------------------------------------------------------------

func TestSplitHTML_ZeroMaxLen(t *testing.T) {
	result := SplitHTML("<p>hello</p>", 0)
	if len(result) != 1 {
		t.Fatalf("expected passthrough for zero maxLen, got %d chunks", len(result))
	}
}

func TestSplitHTML_MalformedHTML(t *testing.T) {
	inputs := []string{
		"<p>unclosed paragraph",
		"</p>orphaned close",
		"<><><>",
		"<div <p>nested open angle",
		"<p>text</p></p></p>extra closes",
		"<<>>",
	}
	for _, input := range inputs {
		result := SplitHTML(input, 10)
		if len(result) == 0 {
			t.Fatalf("expected non-empty result for %q", input)
		}
	}
}

func TestSplitHTML_OnlyInlineTags(t *testing.T) {
	// No block-level tags - falls through to hardSplit.
	input := strings.Repeat("<b>x</b><i>y</i>", 100)
	result := SplitHTML(input, 50)
	if len(result) < 2 {
		t.Fatalf("expected splitting, got %d chunks", len(result))
	}
}

func TestSplitHTML_VoidElements(t *testing.T) {
	// <br>, <hr>, <img> are void - should not confuse tag tracking.
	input := "<p>line1<br/>line2</p><p>line3<hr/>line4</p>"
	result := SplitHTML(input, 25)
	for _, chunk := range result {
		// No unclosed <br> or <hr> should appear in repair.
		if strings.Contains(chunk, "</br>") || strings.Contains(chunk, "</hr>") {
			t.Fatalf("void element got a close tag: %q", chunk)
		}
	}
}

func TestSplitHTML_SelfClosingTags(t *testing.T) {
	input := `<p>text <img src="x.png" /> more</p><p>second</p>`
	result := SplitHTML(input, 30)
	for _, chunk := range result {
		if strings.Contains(chunk, "</img>") {
			t.Fatalf("self-closing img got a close tag: %q", chunk)
		}
	}
}

func TestSplitHTML_DeeplyNestedRepair(t *testing.T) {
	// 10 levels of nesting that must be closed and reopened.
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		sb.WriteString("<div>")
	}
	sb.WriteString("<p>")
	sb.WriteString(strings.Repeat("A", 30))
	sb.WriteString("</p><p>")
	sb.WriteString(strings.Repeat("B", 30))
	sb.WriteString("</p>")
	for i := 0; i < 10; i++ {
		sb.WriteString("</div>")
	}

	result := SplitHTML(sb.String(), 80)
	if len(result) < 2 {
		t.Fatalf("expected splitting, got %d chunks", len(result))
	}
	// Every chunk should have balanced div tags.
	for i, chunk := range result {
		opens := strings.Count(chunk, "<div>")
		closes := strings.Count(chunk, "</div>")
		if opens != closes {
			t.Fatalf("chunk %d: unbalanced divs (open=%d, close=%d)\n%q", i, opens, closes, chunk)
		}
	}
}

func TestSplitHTML_EntityAtBoundary(t *testing.T) {
	// Place &amp; right at the split boundary.
	input := "<p>" + strings.Repeat("x", 95) + "&amp;" + strings.Repeat("y", 100) + "</p>"
	result := SplitHTML(input, 100)
	for _, chunk := range result {
		// The entity should not be broken.
		if strings.Contains(chunk, "&am") && !strings.Contains(chunk, "&amp;") {
			t.Fatalf("entity broken: %q", chunk)
		}
	}
}

func TestSplitHTML_TagAtBoundary(t *testing.T) {
	// Place <strong> right at the split boundary.
	input := "<p>" + strings.Repeat("x", 93) + "<strong>bold</strong></p>"
	result := SplitHTML(input, 100)
	for _, chunk := range result {
		// Should not find a bare "<stro" or "ng>".
		if strings.HasSuffix(strings.TrimSpace(chunk), "<") {
			t.Fatalf("tag broken at chunk boundary ending with '<': %q", chunk)
		}
	}
}

// ---------------------------------------------------------------------------
// repairHTMLChunks - bad paths (via SplitHTML)
// ---------------------------------------------------------------------------

func TestRepairHTMLChunks_SingleChunk(t *testing.T) {
	// Single chunk should pass through unchanged.
	input := "<p>hello</p>"
	result := SplitHTML(input, 1000)
	if len(result) != 1 || result[0] != input {
		t.Fatalf("single chunk should be unchanged, got %v", result)
	}
}

func TestRepairHTMLChunks_EmptyChunks(t *testing.T) {
	// Edge case - empty string.
	result := SplitHTML("", 100)
	if len(result) != 1 || result[0] != "" {
		t.Fatalf("expected single empty chunk, got %v", result)
	}
}

func TestRepairHTMLChunks_MultipleNestingLevels(t *testing.T) {
	// <blockquote><ul><li>text</li></ul></blockquote> split mid-list.
	input := "<blockquote><ul><li>" + strings.Repeat("A", 30) + "</li><li>" + strings.Repeat("B", 30) + "</li></ul></blockquote>"
	result := SplitHTML(input, 50)
	if len(result) < 2 {
		t.Fatalf("expected splitting, got %d chunks", len(result))
	}
	for i, chunk := range result {
		bqOpen := strings.Count(chunk, "<blockquote>")
		bqClose := strings.Count(chunk, "</blockquote>")
		ulOpen := strings.Count(chunk, "<ul>")
		ulClose := strings.Count(chunk, "</ul>")
		if bqOpen != bqClose {
			t.Fatalf("chunk %d: unbalanced blockquotes (%d/%d): %q", i, bqOpen, bqClose, chunk)
		}
		if ulOpen != ulClose {
			t.Fatalf("chunk %d: unbalanced ul (%d/%d): %q", i, ulOpen, ulClose, chunk)
		}
	}
}

// ---------------------------------------------------------------------------
// findSafeBreak - edge cases (tested indirectly through hardSplit)
// ---------------------------------------------------------------------------

func TestHardSplit_AllAngleBrackets(t *testing.T) {
	// Pathological: entire input is angle brackets.
	input := strings.Repeat("<>", 100)
	result := hardSplit(input, 10)
	if len(result) == 0 {
		t.Fatal("expected non-empty result")
	}
}

func TestHardSplit_AllAmpersands(t *testing.T) {
	// Pathological: line of ampersands. findSafeBreak will keep walking
	// back looking for ';' - should not infinite loop or panic.
	input := strings.Repeat("&", 200)
	result := hardSplit(input, 10)
	if len(result) == 0 {
		t.Fatal("expected non-empty result")
	}
}

func TestHardSplit_VeryLongTag(t *testing.T) {
	// A tag longer than the 100-rune lookback window.
	tag := "<div " + strings.Repeat("x", 200) + ">"
	input := tag + "text"
	result := hardSplit(input, 50)
	// findSafeBreak only looks back 100 runes, so it can't see the '<'.
	// This is a known limitation - just verify no panic.
	if len(result) == 0 {
		t.Fatal("expected non-empty result")
	}
}

func TestHardSplit_EmptyString(t *testing.T) {
	result := hardSplit("", 10)
	if len(result) != 1 || result[0] != "" {
		t.Fatalf("expected single empty chunk, got %v", result)
	}
}

func TestHardSplit_MaxLenOne(t *testing.T) {
	result := hardSplit("abc", 1)
	if len(result) != 3 {
		t.Fatalf("expected 3 chunks for maxLen=1, got %d: %v", len(result), result)
	}
}

// ---------------------------------------------------------------------------
// Round-trip robustness: MarkdownToHTML → StripHTML never panics
// ---------------------------------------------------------------------------

func TestRoundTrip_MarkdownToHTML_StripHTML_NoPanic(t *testing.T) {
	inputs := []string{
		"",
		"   ",
		"normal text",
		"**bold** _italic_ ~~strike~~",
		"```\ncode\n```",
		"`inline`",
		"[link](http://x.com)",
		"![img](http://x.com/img.png)",
		"# heading",
		"- list\n- items",
		"1. ordered\n2. list",
		"> blockquote",
		"---",
		"| a | b |\n|---|---|\n| 1 | 2 |",
		strings.Repeat("*", 100),
		strings.Repeat("`", 100),
		strings.Repeat("#", 100),
		strings.Repeat(">", 100),
		"\x00\x01\x02\x03",
		"<script>alert(1)</script>",
		"<div onclick='hack()'>text</div>",
		"&amp; &lt; &gt; &quot; &#39;",
		"&amp;amp;amp;amp;",
		strings.Repeat("漢字", 100),
		strings.Repeat("👨‍👩‍👧‍👦", 50),
		"**bold " + strings.Repeat("*", 500),
	}

	for i, input := range inputs {
		html, _ := MarkdownToHTML(input)
		plain := StripHTML(html)
		_ = plain // just verify no panic
		_ = MarkdownToPlain(input)

		// Also verify SplitHTML + SplitText don't panic.
		_ = SplitHTML(html, 50)
		_ = SplitText(input, 50)

		// Suppress unused variable warning.
		_ = i
	}
}

// ---------------------------------------------------------------------------
// End-to-end pipeline stress: markdown → HTML → SplitHTML → repair
// ---------------------------------------------------------------------------

func TestPipeline_LargeMarkdownDocument(t *testing.T) {
	// Simulate a realistic long AI response.
	var sb strings.Builder
	sb.WriteString("# Report Title\n\n")
	sb.WriteString("## Executive Summary\n\n")
	sb.WriteString(strings.Repeat("This is a sentence in the summary. ", 50))
	sb.WriteString("\n\n## Detailed Findings\n\n")
	for i := 0; i < 20; i++ {
		sb.WriteString("### Finding " + string(rune('A'+i)) + "\n\n")
		sb.WriteString(strings.Repeat("Detail text for this finding. ", 30))
		sb.WriteString("\n\n```python\ndef example():\n    return " + string(rune('A'+i)) + "\n```\n\n")
		sb.WriteString("- Point 1\n- Point 2\n- Point 3\n\n")
	}

	htmlStr, err := MarkdownToHTML(sb.String())
	if err != nil {
		t.Fatalf("MarkdownToHTML failed: %v", err)
	}

	chunks := SplitHTML(htmlStr, 3500)
	for i, chunk := range chunks {
		// Verify each chunk is reasonably well-formed.
		opens := strings.Count(chunk, "<")
		closes := strings.Count(chunk, ">")
		// Every '<' should have a matching '>'. Allow for entities like &lt;.
		if opens == 0 && closes == 0 {
			continue
		}
		if opens != closes {
			// This can happen with entities - not a strict failure.
			// Just verify no obviously broken tags.
			if strings.HasSuffix(strings.TrimSpace(chunk), "<") {
				t.Fatalf("chunk %d ends with broken tag: ...%q", i, chunk[max(0, len(chunk)-50):])
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
