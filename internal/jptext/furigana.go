// Package jptext provides Japanese reading aids backed by a morphological
// analyzer (kagome), not an LLM. Furigana for kanji and phrase spacing are
// deterministic; the local model is only used for grammar notes and translation.
package jptext

import (
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/ikawaha/kagome-dict/ipa"
	"github.com/ikawaha/kagome/v2/tokenizer"
)

var (
	tokenizerOnce sync.Once
	sharedTok     *tokenizer.Tokenizer
	tokenizerErr  error
)

func getTokenizer() (*tokenizer.Tokenizer, error) {
	tokenizerOnce.Do(func() {
		sharedTok, tokenizerErr = tokenizer.New(ipa.Dict(), tokenizer.OmitBosEos())
	})
	return sharedTok, tokenizerErr
}

type rawToken struct {
	Surface string
	Pos     string
	Pos2    string
	Pos3    string
	CType   string // 活用型
	CForm   string // 活用形
	Base    string // 原形
	Reading string // katakana reading from dict, or empty
}

// GrammarHints returns a compact morphological sketch for the local tutor
// model: POS, conjugation, and lemma. Deterministic — not LLM output.
func GrammarHints(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	tok, err := getTokenizer()
	if err != nil {
		return "", err
	}
	raw := tokenize(tok, text)
	var b strings.Builder
	n := 0
	for _, t := range raw {
		if !interestingGrammarToken(t) {
			continue
		}
		n++
		b.WriteString("- 「")
		b.WriteString(t.Surface)
		b.WriteString("」 ")
		b.WriteString(joinPos(t))
		if t.CType != "" && t.CType != "*" {
			b.WriteString(" 活用型=")
			b.WriteString(t.CType)
		}
		if t.CForm != "" && t.CForm != "*" {
			b.WriteString(" 活用形=")
			b.WriteString(t.CForm)
		}
		if t.Base != "" && t.Base != "*" && t.Base != t.Surface {
			b.WriteString(" 原形=")
			b.WriteString(t.Base)
		}
		b.WriteByte('\n')
		if n >= 40 {
			break
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func interestingGrammarToken(t rawToken) bool {
	switch t.Pos {
	case "助詞", "助動詞", "動詞", "形容詞", "連体詞", "副詞", "接続詞":
		return t.Surface != "" && t.Surface != "。" && t.Surface != "、"
	case "名詞":
		// Keep formal-noun / non-independent nouns that act like grammar (の/こと/もの/ため…).
		return t.Pos2 == "非自立" || t.Pos2 == "サ変接続" || t.Pos2 == "接尾"
	default:
		return false
	}
}

func joinPos(t rawToken) string {
	parts := make([]string, 0, 3)
	for _, p := range []string{t.Pos, t.Pos2, t.Pos3} {
		if p != "" && p != "*" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "-")
}

// Annotate returns the source with:
//   - hiragana in parentheses only after spans that contain kanji
//   - fullwidth spaces between bunsetsu-ish units (content word after particle)
//
// Number/date compounds (一九五一年, 四日) are merged and read as wholes.
func Annotate(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	tok, err := getTokenizer()
	if err != nil {
		return "", err
	}
	raw := tokenize(tok, text)
	merged := mergeNumberCompounds(raw)
	return render(merged), nil
}

func tokenize(tok *tokenizer.Tokenizer, text string) []rawToken {
	tokens := tok.Tokenize(text)
	out := make([]rawToken, 0, len(tokens))
	for _, t := range tokens {
		if t.Surface == "" {
			continue
		}
		f := t.Features()
		// IPA feature layout:
		// 0-3 POS hierarchy, 4 c-type, 5 c-form, 6 base, 7 reading, 8 pronunciation.
		feat := func(i int) string {
			if i < len(f) && f[i] != "" {
				return f[i]
			}
			return "*"
		}
		reading := feat(7)
		if reading == "*" {
			reading = ""
		}
		out = append(out, rawToken{
			Surface: t.Surface,
			Pos:     feat(0),
			Pos2:    feat(1),
			Pos3:    feat(2),
			CType:   feat(4),
			CForm:   feat(5),
			Base:    feat(6),
			Reading: reading,
		})
	}
	return out
}

func mergeNumberCompounds(in []rawToken) []rawToken {
	var out []rawToken
	for i := 0; i < len(in); {
		if isNumberToken(in[i]) {
			j := i
			var surf strings.Builder
			for j < len(in) && isNumberToken(in[j]) {
				surf.WriteString(in[j].Surface)
				j++
			}
			s := surf.String()
			if n, ok := parseKanjiNum(s); ok {
				// 四日 / 二十日 …
				if j < len(in) && in[j].Surface == "日" {
					if dr, ok := dayReadings[n]; ok {
						out = append(out, rawToken{Surface: s + "日", Pos: "名詞", Pos2: "数", Reading: "HIRA:" + dr})
						i = j + 1
						continue
					}
				}
				// 一九五一年
				if j < len(in) && in[j].Surface == "年" {
					out = append(out, rawToken{Surface: s + "年", Pos: "名詞", Pos2: "数", Reading: "HIRA:" + readNumber(n) + "ねん"})
					i = j + 1
					continue
				}
				// bare number compound
				out = append(out, rawToken{Surface: s, Pos: "名詞", Pos2: "数", Reading: "HIRA:" + readNumber(n)})
				i = j
				continue
			}
		}
		out = append(out, in[i])
		i++
	}
	return out
}

func isNumberToken(t rawToken) bool {
	if t.Pos == "名詞" && t.Pos2 == "数" {
		return true
	}
	// pure arabic digits sometimes classified differently
	if t.Surface != "" && isAllNumRunes(t.Surface) {
		return true
	}
	return false
}

func isAllNumRunes(s string) bool {
	for _, r := range s {
		if _, ok := kanjiDigits[r]; !ok {
			return false
		}
		if kanjiDigits[r] > 9 {
			return false
		}
	}
	return utf8.RuneCountInString(s) > 0
}

func render(tokens []rawToken) string {
	var b strings.Builder
	prevPos := ""
	for _, t := range tokens {
		if t.Surface == "" {
			continue
		}
		if b.Len() > 0 && shouldSpace(prevPos, t.Pos, t.Surface) {
			b.WriteString("　")
		}
		writeAnnotated(&b, t)
		prevPos = t.Pos
	}
	return b.String()
}

func shouldSpace(prevPos, pos, surface string) bool {
	if surface == "。" || surface == "、" || surface == "」" || surface == "』" || surface == "）" {
		return false
	}
	if prevPos == "記号" {
		return false
	}
	// bunsetsu boundary: particle/aux → content word
	if (prevPos == "助詞" || prevPos == "助動詞") && isContentPos(pos) {
		return true
	}
	return false
}

func isContentPos(pos string) bool {
	switch pos {
	case "名詞", "動詞", "形容詞", "副詞", "連体詞", "接頭詞":
		return true
	default:
		return false
	}
}

func writeAnnotated(b *strings.Builder, t rawToken) {
	reading := t.Reading
	if strings.HasPrefix(reading, "HIRA:") {
		hira := strings.TrimPrefix(reading, "HIRA:")
		if hasKanji(t.Surface) && hira != "" {
			b.WriteString(t.Surface)
			b.WriteString("（")
			b.WriteString(hira)
			b.WriteString("）")
			return
		}
		b.WriteString(t.Surface)
		return
	}
	if hasKanji(t.Surface) && reading != "" {
		b.WriteString(t.Surface)
		b.WriteString("（")
		b.WriteString(kataToHira(reading))
		b.WriteString("）")
		return
	}
	b.WriteString(t.Surface)
}

func hasKanji(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Han) {
			return true
		}
	}
	return false
}

func kataToHira(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'ァ' && r <= 'ヶ':
			b.WriteRune(r - 'ァ' + 'ぁ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

var kanjiDigits = map[rune]int{
	'〇': 0, '零': 0, '０': 0, '0': 0,
	'一': 1, '壱': 1, '１': 1, '1': 1,
	'二': 2, '弐': 2, '２': 2, '2': 2,
	'三': 3, '参': 3, '３': 3, '3': 3,
	'四': 4, '４': 4, '4': 4,
	'五': 5, '５': 5, '5': 5,
	'六': 6, '６': 6, '6': 6,
	'七': 7, '７': 7, '7': 7,
	'八': 8, '８': 8, '8': 8,
	'九': 9, '９': 9, '9': 9,
	'十': 10, '百': 100, '千': 1000, '万': 10000,
}

var dayReadings = map[int]string{
	1: "ついたち", 2: "ふつか", 3: "みっか", 4: "よっか", 5: "いつか",
	6: "むいか", 7: "なのか", 8: "ようか", 9: "ここのか", 10: "とおか",
	11: "じゅういちにち", 12: "じゅうににち", 13: "じゅうさんにち", 14: "じゅうよっか",
	15: "じゅうごにち", 16: "じゅうろくにち", 17: "じゅうしちにち", 18: "じゅうはちにち",
	19: "じゅうくにち", 20: "はつか", 21: "にじゅういちにち", 22: "にじゅうににち",
	23: "にじゅうさんにち", 24: "にじゅうよっか", 25: "にじゅうごにち", 26: "にじゅうろくにち",
	27: "にじゅうしちにち", 28: "にじゅうはちにち", 29: "にじゅうくにち", 30: "さんじゅうにち",
	31: "さんじゅういちにち",
}

var digitHira = []string{"ぜろ", "いち", "に", "さん", "よん", "ご", "ろく", "なな", "はち", "きゅう"}

func parseKanjiNum(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	// consecutive digit style: 一九五一 / 2024
	if !strings.ContainsAny(s, "十百千万") {
		total := 0
		for _, r := range s {
			d, ok := kanjiDigits[r]
			if !ok || d > 9 {
				return 0, false
			}
			total = total*10 + d
		}
		return total, true
	}
	// traditional: 二十一, 三百五
	total, cur := 0, 0
	for _, r := range s {
		d, ok := kanjiDigits[r]
		if !ok {
			return 0, false
		}
		if d < 10 {
			cur = cur*10 + d
			continue
		}
		if cur == 0 {
			cur = 1
		}
		total += cur * d
		cur = 0
	}
	return total + cur, true
}

func readNumber(n int) string {
	if n < 0 {
		return ""
	}
	if n < 10 {
		return digitHira[n]
	}
	if n < 20 {
		if n == 10 {
			return "じゅう"
		}
		return "じゅう" + digitHira[n%10]
	}
	if n < 100 {
		tens, ones := n/10, n%10
		var b strings.Builder
		b.WriteString(digitHira[tens])
		b.WriteString("じゅう")
		if ones > 0 {
			b.WriteString(digitHira[ones])
		}
		return b.String()
	}
	if n < 1000 {
		h, rest := n/100, n%100
		var b strings.Builder
		switch h {
		case 1:
			b.WriteString("ひゃく")
		case 3:
			b.WriteString("さんびゃく")
		case 6:
			b.WriteString("ろっぴゃく")
		case 8:
			b.WriteString("はっぴゃく")
		default:
			b.WriteString(digitHira[h])
			b.WriteString("ひゃく")
		}
		if rest > 0 {
			b.WriteString(readNumber(rest))
		}
		return b.String()
	}
	if n < 10000 {
		th, rest := n/1000, n%1000
		var b strings.Builder
		switch th {
		case 1:
			b.WriteString("せん")
		case 3:
			b.WriteString("さんぜん")
		case 8:
			b.WriteString("はっせん")
		default:
			b.WriteString(digitHira[th])
			b.WriteString("せん")
		}
		if rest > 0 {
			b.WriteString(readNumber(rest))
		}
		return b.String()
	}
	if n < 100000000 {
		w, rest := n/10000, n%10000
		var b strings.Builder
		if w == 1 {
			b.WriteString("いちまん")
		} else {
			b.WriteString(readNumber(w))
			b.WriteString("まん")
		}
		if rest > 0 {
			b.WriteString(readNumber(rest))
		}
		return b.String()
	}
	// Fallback: digit-by-digit for very large values.
	var b strings.Builder
	for _, r := range fmt.Sprintf("%d", n) {
		if r >= '0' && r <= '9' {
			b.WriteString(digitHira[int(r-'0')])
		}
	}
	return b.String()
}
