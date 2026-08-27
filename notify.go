package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Telegram 推送。
//
// 凭证只从环境变量读取，不走命令行参数 —— 命令行在进程列表里是可见的，
// 环境变量相对安全。Token 绝不会出现在任何日志里。
const (
	envTGToken  = "TG_BOT_TOKEN"
	envTGChatID = "TG_CHAT_ID"

	// Telegram 单条消息上限 4096 字符，留些余量
	telegramMaxLen = 3800
)

// telegramAPI 声明为 var 只是为了让测试指向本地假服务器，运行时不会被修改。
var telegramAPI = "https://api.telegram.org/bot%s/sendMessage"

type notifier struct {
	token  string
	chatID string
}

// enabled 报告是否配置了推送。两个都填了才算。
func (n notifier) enabled() bool { return n.token != "" && n.chatID != "" }

// redact 把可能混进错误信息里的 token 抹掉，避免泄漏到 Actions 日志。
func (n notifier) redact(s string) string {
	if n.token == "" {
		return s
	}
	return strings.ReplaceAll(s, n.token, "<TOKEN>")
}

// send 推送一条纯文本消息。
//
// 刻意不使用 parse_mode：贴吧名里可能含有 _ * [ ` 等字符，
// 用 Markdown/HTML 模式会因为转义问题被 Telegram 拒收。
func (n notifier) send(text string) {
	if !n.enabled() {
		return
	}
	if len(text) > telegramMaxLen {
		text = text[:telegramMaxLen] + "\n…(已截断)"
	}

	payload, err := json.Marshal(map[string]any{
		"chat_id":                  n.chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	})
	if err != nil {
		logError("Telegram 推送失败 -- 构造请求体出错: %v", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf(telegramAPI, n.token), strings.NewReader(string(payload)))
	if err != nil {
		logError("Telegram 推送失败 -- %s", n.redact(err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		logError("Telegram 推送失败 -- %s", n.redact(err.Error()))
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Telegram 的错误响应里不含 token，但仍然过一遍 redact 保险
		logError("Telegram 推送失败 -- HTTP %d %s", resp.StatusCode,
			n.redact(strings.TrimSpace(string(body))))
		return
	}
	logInfo("Telegram 推送成功")
}

func newNotifier(token, chatID string) notifier {
	n := notifier{token: strings.TrimSpace(token), chatID: strings.TrimSpace(chatID)}
	switch {
	case n.enabled():
		logInfo("已启用 Telegram 推送")
	case n.token == "" && n.chatID == "":
		logInfo("未配置 Telegram 推送，跳过（需要 %s 和 %s）", envTGToken, envTGChatID)
	case n.token == "":
		logWarn("只配置了 %s，缺少 %s，推送不会生效", envTGChatID, envTGToken)
	default:
		logWarn("只配置了 %s，缺少 %s，推送不会生效", envTGToken, envTGChatID)
	}
	return n
}

// ---------------------------------------------------------------------------
// 消息内容
// ---------------------------------------------------------------------------

// alertText 拉取关注列表失败时的告警。这是最需要被看到的一条。
func alertText(elapsed time.Duration) string {
	var b strings.Builder
	b.WriteString("🚨 贴吧签到失败\n\n")
	b.WriteString("未能获取关注贴吧列表，本次一个吧都没签到。\n\n")
	b.WriteString("最常见的原因是 BDUSS 已失效。\n")
	b.WriteString("处理方法：重新登录贴吧，从 Cookie 里取出新的 BDUSS，\n")
	b.WriteString("更新到仓库 Settings → Secrets → Actions 的 BDUSS 里。\n\n")
	b.WriteString(fmt.Sprintf("耗时 %s", elapsed))
	return b.String()
}

// resultText 正常跑完后的汇总。
func resultText(total, ok int, failed []string, elapsed time.Duration) string {
	var b strings.Builder
	if len(failed) == 0 {
		b.WriteString("✅ 贴吧签到完成\n\n")
	} else {
		b.WriteString("⚠️ 贴吧签到完成（有失败）\n\n")
	}
	b.WriteString(fmt.Sprintf("共 %d 个吧　成功 %d　失败 %d\n", total, ok, total-ok))
	b.WriteString(fmt.Sprintf("耗时 %s\n", elapsed))

	if len(failed) > 0 {
		b.WriteString("\n未签到成功：\n")
		// 失败的吧可能很多，最多列 30 个
		const maxList = 30
		for i, name := range failed {
			if i >= maxList {
				b.WriteString(fmt.Sprintf("…另有 %d 个\n", len(failed)-maxList))
				break
			}
			b.WriteString("· " + name + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
