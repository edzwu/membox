package docrewrite

import (
	"strings"
	"testing"
)

func TestParseModelDefaultIsLocalQwen(t *testing.T) {
	for _, raw := range []string{"", "local", "qwen3:14b", "ollama"} {
		spec, err := ParseModel(raw)
		if err != nil {
			t.Fatalf("ParseModel(%q): %v", raw, err)
		}
		if !spec.Local || spec.Model != LocalModel {
			t.Fatalf("ParseModel(%q)=%+v want local %s", raw, spec, LocalModel)
		}
	}
}

func TestParseModelDeepseekAliases(t *testing.T) {
	for _, raw := range []string{"deepseek", "deepseek-flash", "deepseek/deepseek-v4-flash"} {
		spec, err := ParseModel(raw)
		if err != nil {
			t.Fatalf("ParseModel(%q): %v", raw, err)
		}
		if spec.Local || spec.Provider != DeepseekProvider || spec.Model != DeepseekModel {
			t.Fatalf("ParseModel(%q)=%+v", raw, spec)
		}
	}
}

func TestBuildPromptContainsSource(t *testing.T) {
	p := BuildPrompt("Demo", "# Hi\n\nbody\n")
	if !strings.Contains(p, "---原文开始---") || !strings.Contains(p, "# Hi") {
		t.Fatalf("prompt missing source: %s", p)
	}
}

func TestStripWrapperFences(t *testing.T) {
	in := "```markdown\n# T\n\nok\n```"
	if got := stripWrapperFences(in); got != "# T\n\nok" {
		t.Fatalf("got %q", got)
	}
}

func TestSplitPrompt(t *testing.T) {
	full := BuildPrompt("T", "hello world")
	inst, sel := splitPrompt(full)
	if !strings.Contains(inst, "Output ONLY") {
		t.Fatalf("instruction: %s", inst)
	}
	if sel != "hello world" {
		t.Fatalf("selection=%q", sel)
	}
}

func TestBuildPromptRequiresCppFences(t *testing.T) {
	p := BuildPrompt("C++ Agent", "code")
	if !strings.Contains(p, "一律用 cpp") || !strings.Contains(p, "禁止 txt") {
		t.Fatalf("prompt should ban txt fences: %s", p)
	}
}

func TestBuildPromptWithHintInjectsUserBlock(t *testing.T) {
	p := BuildPromptWithHint("T", "body", "代码围栏必须用 cpp，不要 txt")
	if !strings.Contains(p, "【用户额外要求——优先遵守】") {
		t.Fatalf("missing hint header: %s", p)
	}
	if !strings.Contains(p, "代码围栏必须用 cpp，不要 txt") {
		t.Fatalf("missing hint body: %s", p)
	}
	// hint should appear before source, not after
	iHint := strings.Index(p, "【用户额外要求")
	iSrc := strings.Index(p, "---原文开始---")
	if iHint < 0 || iSrc < 0 || iHint > iSrc {
		t.Fatalf("hint should precede source: hint=%d src=%d", iHint, iSrc)
	}
}

func TestNormalizeCodeFencesRetagsTxtAndC(t *testing.T) {
	in := "# T\n\n```txt\n#include <stdio.h>\nchar *reminder = build_system_promptreminder();\nreturn;\n```\n\n```c\nworker_note_system_prompt_seen(w);\nreturn;\n```\n\n```bash\necho hi\n```\n"
	got := NormalizeCodeFences(in)
	if strings.Contains(got, "```txt") || strings.Contains(got, "```c\n") {
		t.Fatalf("expected cpp retag, got:\n%s", got)
	}
	if !strings.Contains(got, "```cpp\n#include") {
		t.Fatalf("txt block not cpp:\n%s", got)
	}
	if !strings.Contains(got, "```cpp\nworker_note") {
		t.Fatalf("c block not cpp:\n%s", got)
	}
	if !strings.Contains(got, "```bash\necho") {
		t.Fatalf("bash should stay:\n%s", got)
	}
}
