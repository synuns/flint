// frontmatter_test.go — the leading `---` block parser: the three value
// forms (flow / block / quoted — the spec §ruling acceptance set), the
// empty-frontmatter and no-frontmatter boundaries, CRLF line endings, and
// an unterminated block (treated as body, not frontmatter).
package vault

import (
	"reflect"
	"testing"
)

func TestParseFrontmatterFlowList(t *testing.T) {
	fm, body := ParseFrontmatter("---\ntags: [alpha, \"beta gamma\", demo]\ntitle: Demo Note\n---\n\nBody text.\n")
	if !reflect.DeepEqual(fm.Tags, []string{"alpha", "beta gamma", "demo"}) {
		t.Fatalf("Tags = %v, want [alpha beta gamma demo]", fm.Tags)
	}
	if fm.Fields["title"] != "Demo Note" {
		t.Fatalf("Fields[title] = %q, want %q", fm.Fields["title"], "Demo Note")
	}
	if got := firstParagraph(body); got != "Body text." {
		t.Fatalf("body first paragraph = %q, want %q", got, "Body text.")
	}
}

func TestParseFrontmatterBlockList(t *testing.T) {
	fm, body := ParseFrontmatter("---\ntags:\n  - alpha\n  - beta\nauthor: demo\n---\nbody")
	if !reflect.DeepEqual(fm.Tags, []string{"alpha", "beta"}) {
		t.Fatalf("Tags = %v, want [alpha beta]", fm.Tags)
	}
	if fm.Fields["author"] != "demo" {
		t.Fatalf("Fields[author] = %q, want %q", fm.Fields["author"], "demo")
	}
	if body != "body" {
		t.Fatalf("body = %q, want %q", body, "body")
	}
}

func TestParseFrontmatterQuotedWikilink(t *testing.T) {
	fm, _ := ParseFrontmatter("---\ntags: \"[[demo-link]]\"\n---\nbody")
	if !reflect.DeepEqual(fm.Tags, []string{"[[demo-link]]"}) {
		t.Fatalf("Tags = %v, want [[[demo-link]]]", fm.Tags)
	}
}

func TestParseFrontmatterQuotedInsideFlow(t *testing.T) {
	fm, _ := ParseFrontmatter("---\ntags: [\"[[x]]\", demo]\n---\nbody")
	if !reflect.DeepEqual(fm.Tags, []string{"[[x]]", "demo"}) {
		t.Fatalf("Tags = %v, want [[[x]] demo]", fm.Tags)
	}
}

func TestParseFrontmatterEmpty(t *testing.T) {
	fm, body := ParseFrontmatter("---\n---\nbody")
	if len(fm.Fields) != 0 || len(fm.Tags) != 0 {
		t.Fatalf("empty frontmatter: got %+v, want zero fields", fm)
	}
	if body != "body" {
		t.Fatalf("body = %q, want %q", body, "body")
	}
}

func TestParseFrontmatterCRLF(t *testing.T) {
	fm, body := ParseFrontmatter("---\r\ntags: [a, b]\r\n---\r\nBody line.\r\n")
	if !reflect.DeepEqual(fm.Tags, []string{"a", "b"}) {
		t.Fatalf("Tags = %v, want [a b]", fm.Tags)
	}
	if got := firstParagraph(body); got != "Body line." {
		t.Fatalf("body first paragraph = %q, want %q", got, "Body line.")
	}
}

func TestParseFrontmatterNone(t *testing.T) {
	in := "no frontmatter here\n---\nnot a block\n"
	fm, body := ParseFrontmatter(in)
	if len(fm.Fields) != 0 || len(fm.Tags) != 0 {
		t.Fatalf("no-frontmatter input: got %+v, want zero fields", fm)
	}
	if body != in {
		t.Fatalf("body must be unchanged: %q", body)
	}
}

func TestParseFrontmatterUnterminated(t *testing.T) {
	in := "---\ntags: [a]\nbody without a close\n"
	fm, body := ParseFrontmatter(in)
	if len(fm.Fields) != 0 {
		t.Fatalf("unterminated block must not parse as frontmatter: %+v", fm)
	}
	if body != in {
		t.Fatalf("body must be unchanged: %q", body)
	}
}
