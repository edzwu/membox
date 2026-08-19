package jptext

import (
	"strings"
	"testing"
)

func TestAnnotateYearAndDay(t *testing.T) {
	got, err := Annotate("僕が生まれたのは一九五一年の一月四日だ。")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"僕（ぼく）",
		"生まれ（うまれ）",
		"一九五一年（せんきゅうひゃくごじゅういちねん）",
		"一月（いちがつ）",
		"四日（よっか）",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// Pure kana must not be parenthetically annotated.
	if strings.Contains(got, "が（") || strings.Contains(got, "だ（") {
		t.Fatalf("annotated pure kana: %s", got)
	}
	// Must not digit-split the year the way the LLM did.
	if strings.Contains(got, "一（いち）九（きゅう）") || strings.Contains(got, "いっこうごねん") {
		t.Fatalf("year was digit-split or hallucinated: %s", got)
	}
}

func TestAnnotateNoKanjiPassthrough(t *testing.T) {
	got, err := Annotate("ありがとう。")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ありがとう。" {
		t.Fatalf("got %q", got)
	}
}

func TestGrammarHintsIncludesConjugation(t *testing.T) {
	got, err := GrammarHints("生まれたのは学生だ。")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"「生まれ」", "動詞", "原形=", "「た」", "助動詞"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in hints:\n%s", want, got)
		}
	}
}

func TestReadNumber(t *testing.T) {
	cases := map[int]string{
		4:    "よん",
		10:   "じゅう",
		14:   "じゅうよん",
		20:   "にじゅう",
		1951: "せんきゅうひゃくごじゅういち",
	}
	for n, want := range cases {
		if got := readNumber(n); got != want {
			t.Fatalf("readNumber(%d)=%q want %q", n, got, want)
		}
	}
}
