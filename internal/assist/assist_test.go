package assist

import (
	"strings"
	"testing"
)

func TestValidateAndBuildPrompt(t *testing.T) {
	req, err := Validate(Request{
		Mode: "EDIT", Instruction: " shorter ", Selection: "Hello world",
		Prefix: "pre", Suffix: "suf", Title: "Doc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.Mode != ModeEdit || req.Instruction != "shorter" {
		t.Fatalf("%+v", req)
	}
	prompt := BuildPrompt(req)
	for _, needle := range []string{"ONLY the replacement", "【选区】", "Hello world", "shorter", "Doc"} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("prompt missing %q:\n%s", needle, prompt)
		}
	}
	ask, err := Validate(Request{Mode: "ask", Instruction: "what?", Selection: "x"})
	if err != nil {
		t.Fatal(err)
	}
	p2 := BuildPrompt(ask)
	if !strings.Contains(p2, "reading assistant") || strings.Contains(p2, "ONLY the replacement") {
		t.Fatalf("ask prompt unexpected:\n%s", p2)
	}
}

func TestApplyReplacementUniqueAndContext(t *testing.T) {
	md := "AAA Hello world BBB Hello world CCC"
	if _, err := ApplyReplacement(md, "Hello world", "Hi", "", ""); err == nil {
		t.Fatal("expected ambiguity error")
	}
	out, err := ApplyReplacement(md, "Hello world", "Hi", "AAA ", " BBB")
	if err != nil {
		t.Fatal(err)
	}
	if out != "AAA Hi BBB Hello world CCC" {
		t.Fatalf("got %q", out)
	}
	out, err = ApplyReplacement("only once here", "once", "twice", "", "")
	if err != nil || out != "only twice here" {
		t.Fatalf("got %q err=%v", out, err)
	}
}

func TestStripEditWrapping(t *testing.T) {
	if got := stripEditWrapping("```markdown\nHello\n```"); got != "Hello" {
		t.Fatalf("%q", got)
	}
	if got := stripEditWrapping(`"Hello"`); got != "Hello" {
		t.Fatalf("%q", got)
	}
}
