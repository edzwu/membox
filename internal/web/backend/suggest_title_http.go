package backend

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"
)

// Title suggestions share the outline channel's constraints: same tiny
// payload (headings + samples + a lead paragraph sample), same models.
var suggestTitleMu sync.Mutex

const (
	suggestTitleMaxLeadRunes   = 500
	suggestTitleMaxResultRunes = 80
)

// runSuggestTitle reuses the outline model seam (Pi cloud / mmd local).
var runSuggestTitle polishOutlineFunc = defaultPolishOutline

// handleSuggestTitle asks the model for a better document title given the
// outline (headings + section samples), a lead-paragraph sample, and the
// current title. Same privacy shape as /api/polish-outline: the body never
// leaves the browser beyond those samples.
func (s *Server) handleSuggestTitle(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if request.Header.Get(miruClientHeader) != "1" {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	if s.home == "" {
		http.Error(writer, "membox home is not configured", http.StatusServiceUnavailable)
		return
	}
	var payload struct {
		Headings []polishOutlineHeading `json:"headings"`
		Lead     string                 `json:"lead"`
		Current  string                 `json:"current"`
		Model    string                 `json:"model"`
	}
	if !decodeJSON(writer, request, &payload) {
		return
	}
	if err := validateOutlineHeadings(payload.Headings); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(payload.Lead) > suggestTitleMaxLeadRunes ||
		utf8.RuneCountInString(payload.Current) > polishOutlineMaxTextLen {
		http.Error(writer, "lead/current too long", http.StatusBadRequest)
		return
	}
	if !suggestTitleMu.TryLock() {
		http.Error(writer, "another title suggestion is running", http.StatusConflict)
		return
	}
	defer suggestTitleMu.Unlock()

	label, text, err := runSuggestTitle(request.Context(), s.home, strings.TrimSpace(payload.Model),
		buildSuggestTitlePrompt(payload.Headings, payload.Lead, payload.Current))
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadGateway)
		return
	}
	title, err := cleanSuggestedTitle(text)
	if err != nil {
		http.Error(writer, fmt.Sprintf("model returned unusable title: %s", err), http.StatusBadGateway)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(map[string]string{"title": title, "label": label})
}

func buildSuggestTitlePrompt(headings []polishOutlineHeading, lead, current string) string {
	var b strings.Builder
	b.WriteString(`你是知识管理编辑。根据文档的导语和标题大纲，为文档起一个简洁准确的标题。
要求：
- 不超过 30 个字符；不要书名号、引号、表情符号和结尾标点。
- 语言跟随文档主体（中文文档用中文，英文文档用英文）。
- 概括文档的具体主题，避免“笔记”“总结”“文档”这类空泛的词。
- 如果当前标题已经准确简洁，原样返回它。
输出：只输出标题文本本身，不要解释，不要 JSON，不要代码围栏。

`)
	if current = strings.TrimSpace(current); current != "" {
		fmt.Fprintf(&b, "当前标题：%s\n\n", current)
	}
	if lead = strings.TrimSpace(lead); lead != "" {
		fmt.Fprintf(&b, "导语：%s\n\n", lead)
	}
	b.WriteString("标题大纲：\n")
	for i, h := range headings {
		fmt.Fprintf(&b, "%d. H%d %s\n", i, h.Level, h.Text)
	}
	return b.String()
}

// cleanSuggestedTitle normalizes the model's answer to one bare title line:
// first line only, 《…》/引号/结尾标点 stripped, common boilerplate prefixes
// ("好的，标题是：" …) removed. A legit title containing ：survives because
// only known-boilerplate prefixes are cut.
func cleanSuggestedTitle(text string) (string, error) {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i] // first line only when the model rambles
	}
	// Some models wrap the answer as a JSON array despite the prompt.
	if strings.HasPrefix(text, "[") {
		var titles []string
		if err := json.Unmarshal([]byte(text), &titles); err == nil && len(titles) > 0 {
			text = titles[0]
		}
	}
	if start := strings.Index(text, "《"); start >= 0 {
		if end := strings.Index(text[start+len("《"):], "》"); end >= 0 {
			text = text[start+len("《") : start+len("《")+end]
		}
	}
	// Only cut before a colon when the prefix is clearly boilerplate.
	if i := strings.IndexAny(text, ":："); i > 0 {
		prefix := text[:i]
		if boilerplatePrefix(prefix) {
			text = text[i+1:]
		}
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "标题是") {
		text = strings.TrimSpace(strings.TrimPrefix(text, "标题是"))
	}
	text = strings.Trim(text, "\"'「」《》`*。．. \t")
	if text == "" {
		return "", fmt.Errorf("empty title")
	}
	if utf8.RuneCountInString(text) > suggestTitleMaxResultRunes {
		return "", fmt.Errorf("title too long (%d runes)", utf8.RuneCountInString(text))
	}
	return text, nil
}

func boilerplatePrefix(prefix string) bool {
	p := strings.ToLower(strings.TrimSpace(prefix))
	for _, word := range []string{"好的", "好", "标题", "新标题", "建议标题", "答案是", "title", "suggested title", "answer", "here"} {
		if p == word || strings.HasPrefix(p, word+" ") || strings.HasSuffix(p, word) && len([]rune(p)) <= 8 {
			return true
		}
	}
	return false
}
