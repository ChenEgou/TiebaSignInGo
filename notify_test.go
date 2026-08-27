package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNotifierEnabled(t *testing.T) {
	for _, c := range []struct {
		token, chat string
		want        bool
	}{
		{"tok", "chat", true},
		{"", "chat", false},
		{"tok", "", false},
		{"", "", false},
		{"  ", "chat", false}, // 只有空白视为未配置
	} {
		if got := newNotifier(c.token, c.chat).enabled(); got != c.want {
			t.Errorf("newNotifier(%q, %q).enabled() = %v, want %v", c.token, c.chat, got, c.want)
		}
	}
}

// TestNotifierRedactsToken Token 绝不能出现在日志里。
func TestNotifierRedactsToken(t *testing.T) {
	const tok = "123456:AAHsuperSecretTokenValue"
	n := newNotifier(tok, "chat")
	msg := "Post \"https://api.telegram.org/bot" + tok + "/sendMessage\": dial tcp: timeout"
	got := n.redact(msg)
	if strings.Contains(got, tok) {
		t.Fatalf("token 没有被抹掉: %s", got)
	}
	if !strings.Contains(got, "<TOKEN>") {
		t.Errorf("应替换为 <TOKEN>: %s", got)
	}
}

// TestNotifierSendPayload 验证发给 Telegram 的请求体格式正确。
func TestNotifierSendPayload(t *testing.T) {
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	old := telegramAPI
	telegramAPI = srv.URL + "/bot%s/sendMessage"
	defer func() { telegramAPI = old }()

	oldClient := client
	client = &http.Client{Timeout: 5 * time.Second}
	defer func() { client = oldClient }()

	newNotifier("TESTTOKEN", "99887766").send("你好 世界")

	if want := "/botTESTTOKEN/sendMessage"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotBody["chat_id"] != "99887766" {
		t.Errorf("chat_id = %v, want 99887766", gotBody["chat_id"])
	}
	if gotBody["text"] != "你好 世界" {
		t.Errorf("text = %v", gotBody["text"])
	}
	// 不应带 parse_mode：吧名里的 _ * [ ` 会导致 Telegram 因转义问题拒收
	if _, ok := gotBody["parse_mode"]; ok {
		t.Error("不应设置 parse_mode")
	}
}

// TestNotifierSkipsWhenUnconfigured 没配置时不应该发出任何请求。
func TestNotifierSkipsWhenUnconfigured(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	old := telegramAPI
	telegramAPI = srv.URL + "/bot%s/sendMessage"
	defer func() { telegramAPI = old }()

	newNotifier("", "").send("不该发出去")
	newNotifier("tok", "").send("不该发出去")
	newNotifier("", "chat").send("不该发出去")

	if called {
		t.Error("未配置推送时不应发出请求")
	}
}

// TestNotifierTruncatesLongMessage 超长消息必须截断，否则 Telegram 会拒收。
func TestNotifierTruncatesLongMessage(t *testing.T) {
	var gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		gotText, _ = body["text"].(string)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	old := telegramAPI
	telegramAPI = srv.URL + "/bot%s/sendMessage"
	defer func() { telegramAPI = old }()
	oldClient := client
	client = &http.Client{Timeout: 5 * time.Second}
	defer func() { client = oldClient }()

	newNotifier("tok", "chat").send(strings.Repeat("a", 10000))

	if len(gotText) > 4096 {
		t.Errorf("消息长度 %d，超过 Telegram 上限 4096", len(gotText))
	}
	if !strings.HasSuffix(gotText, "(已截断)") {
		t.Error("截断后应有提示")
	}
}

func TestAlertText(t *testing.T) {
	got := alertText(3 * time.Second)
	for _, want := range []string{"签到失败", "BDUSS", "Secrets"} {
		if !strings.Contains(got, want) {
			t.Errorf("告警文案里应包含 %q:\n%s", want, got)
		}
	}
}

func TestResultText(t *testing.T) {
	// 全部成功
	got := resultText(3, 3, nil, nil, 54*time.Second)
	if !strings.Contains(got, "✅") {
		t.Errorf("全部成功应有 ✅:\n%s", got)
	}
	if strings.Contains(got, "未签到成功") {
		t.Errorf("没有失败时不该出现失败列表:\n%s", got)
	}

	// 有失败
	got = resultText(3, 1, []string{"吧A", "吧B"}, nil, time.Minute)
	for _, want := range []string{"⚠️", "共 3 个吧", "成功 1", "失败 2", "吧A", "吧B"} {
		if !strings.Contains(got, want) {
			t.Errorf("汇总里应包含 %q:\n%s", want, got)
		}
	}
}

// TestResultTextCapsFailureList 失败的吧太多时只列前 30 个，避免消息超长。
func TestResultTextCapsFailureList(t *testing.T) {
	failed := make([]string, 100)
	for i := range failed {
		failed[i] = "吧" + strings.Repeat("x", 20)
	}
	got := resultText(100, 0, failed, nil, time.Minute)
	if strings.Count(got, "·") != 30 {
		t.Errorf("应只列出 30 个，实际 %d 个", strings.Count(got, "·"))
	}
	if !strings.Contains(got, "另有 70 个") {
		t.Errorf("应提示剩余数量:\n%s", got)
	}
}

// TestResultTextTotalFailure 一个都没签上时措辞要比"有失败"更重。
func TestResultTextTotalFailure(t *testing.T) {
	got := resultText(30, 0, []string{"吧A", "吧B"}, nil, time.Minute)
	if !strings.Contains(got, "🚨") {
		t.Errorf("全部失败应有 🚨:\n%s", got)
	}
	if !strings.Contains(got, "全部失败") {
		t.Errorf("应写明全部失败:\n%s", got)
	}
	// 全部成功和部分失败不应误用这个措辞
	if strings.Contains(resultText(30, 30, nil, nil, time.Minute), "🚨") {
		t.Error("全部成功不该出现 🚨")
	}
	if strings.Contains(resultText(30, 28, []string{"吧A"}, nil, time.Minute), "🚨") {
		t.Error("部分失败不该出现 🚨")
	}
}

// TestResultTextShowsExp 汇总里要报出本次实际挣到的经验。
func TestResultTextShowsExp(t *testing.T) {
	signed := []signResult{
		{name: "抗压背锅", exp: 8, cont: 120},
		{name: "孙笑川", exp: 12, cont: 5},
		{name: "李毅", exp: 4},
	}
	got := resultText(84, 84, nil, signed, 3*time.Minute)
	for _, want := range []string{"本次新签 3 个", "获得 24 经验", "经验最多", "孙笑川 +12"} {
		if !strings.Contains(got, want) {
			t.Errorf("汇总里应包含 %q:\n%s", want, got)
		}
	}
	// 经验最多的排在最前
	iSun := strings.Index(got, "孙笑川")
	iKang := strings.Index(got, "· 抗压背锅")
	if iSun < 0 || iKang < 0 || iSun > iKang {
		t.Errorf("应按经验从高到低排序:\n%s", got)
	}
}

// TestResultTextNoNewSigns 今天全部已签过时，不能报"获得 0 经验"这种误导话。
func TestResultTextNoNewSigns(t *testing.T) {
	got := resultText(84, 84, nil, nil, time.Second)
	if !strings.Contains(got, "本次没有新签的吧") {
		t.Errorf("应说明没有新签:\n%s", got)
	}
	if strings.Contains(got, "获得 0 经验") {
		t.Errorf("不该出现\"获得 0 经验\":\n%s", got)
	}
}

// TestResultTextExpUnparsed 经验字段解析不到时只报数量，不编造数字。
func TestResultTextExpUnparsed(t *testing.T) {
	signed := []signResult{{name: "吧A"}, {name: "吧B"}} // exp 全为 0
	got := resultText(84, 84, nil, signed, time.Second)
	if !strings.Contains(got, "本次新签 2 个") {
		t.Errorf("应报出数量:\n%s", got)
	}
	if strings.Contains(got, "经验") {
		t.Errorf("解析不到经验时不该提经验:\n%s", got)
	}
}

func TestTopByExp(t *testing.T) {
	rs := []signResult{
		{name: "a", exp: 3}, {name: "b", exp: 10},
		{name: "c", exp: 0}, {name: "d", exp: 7},
	}
	top := topByExp(rs, 2)
	if len(top) != 2 {
		t.Fatalf("应返回 2 个，实际 %d 个", len(top))
	}
	if top[0].name != "b" || top[1].name != "d" {
		t.Errorf("排序错误: %v", top)
	}
	// 经验为 0 的不该出现
	for _, r := range topByExp(rs, 10) {
		if r.exp == 0 {
			t.Errorf("经验为 0 的不该入选: %s", r.name)
		}
	}
	if got := topByExp(nil, 5); got != nil {
		t.Errorf("空输入应返回 nil，实际 %v", got)
	}
}
