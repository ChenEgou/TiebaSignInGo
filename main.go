package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// 接口地址 —— 与 Java 版 top.srcrs.Run 完全一致，不要改动。
// 这几个字符串决定了百度把这次请求当成"手机客户端签到"（经验更高），
// 与使用什么编程语言无关。
// ---------------------------------------------------------------------------

// 三个接口地址。声明为 var 而非 const，只是为了让测试能指向本地假服务器，
// 运行时不会被修改。
var (
	// 获取用户所有关注贴吧
	likeURL = "https://tieba.baidu.com/mo/q/newmoindex"
	// 获取用户的 tbs
	tbsURL = "http://tieba.baidu.com/dc/common/tbs"
	// 贴吧签到接口（手机客户端 API）
	signURL = "http://c.tieba.baidu.com/c/c/forum/sign"
)

const (
	// Java 版 Request 类里写死的 User-Agent（其实是桌面版 Chrome，原样保留）
	userAgent = "Mozilla/5.0 (Windows NT 6.1; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/39.0.2171.71 Safari/537.36"
	// 签名盐值，来自贴吧安卓客户端
	signSalt = "tiebaclient!!!"
	// Java 版 POST 请求硬编码的 Host 头（与 signURL 的实际域名 c.tieba.baidu.com
	// 不一致，这是原版行为，照搬）
	postHost = "tieba.baidu.com"
)

// ---------------------------------------------------------------------------
// 全局状态 —— 对应 Java 版 Run 类的字段
// ---------------------------------------------------------------------------

var (
	cfg      Config
	client   *http.Client
	bduss    string   // 对应 Cookie 单例里的 BDUSS
	tbs      string   // 用户的 tbs
	follow   []string // 待签到的贴吧
	success  []string // 已签到成功的贴吧
	followOK bool     // 关注列表是否成功拉取到

	// followNum 关注的贴吧总数。Java 版初值是 201，拉取失败时会让末尾汇总
	// 输出"共 201 个贴吧 - 失败: 201"这种误导信息，这里改为 0。
	followNum = 0
)

func cookie() string { return "BDUSS=" + bduss }

// ---------------------------------------------------------------------------
// 日志
// ---------------------------------------------------------------------------

func logAt(level, format string, a ...any) {
	fmt.Printf("%s %-5s %s\n", time.Now().Format("2006-01-02 15:04:05"), level, fmt.Sprintf(format, a...))
}

func logInfo(format string, a ...any)  { logAt("INFO", format, a...) }
func logWarn(format string, a ...any)  { logAt("WARN", format, a...) }
func logError(format string, a ...any) { logAt("ERROR", format, a...) }

// ---------------------------------------------------------------------------
// JSON helper —— 模拟 fastjson 的 getString（数字/布尔都会被转成字符串）。
// 百度的 is_login / is_sign / error_code 在不同接口里有时是数字有时是字符串，
// 这里必须两种都能处理。
// ---------------------------------------------------------------------------

func decodeJSON(b []byte) map[string]any {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil
	}
	return m
}

func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}

func getObject(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

func getArray(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].([]any); ok {
		return v
	}
	return nil
}

// ---------------------------------------------------------------------------
// MD5 —— 对应 Java 版 top.srcrs.util.Encryption
// ---------------------------------------------------------------------------

// md5Hex 计算 MD5。javaCompat 为 true 时复刻 Java 的
// new BigInteger(1, digest).toString(16)：结果按正整数打印，前导零全部消失，
// 约 6.2% 的输入会得到不足 32 位的残缺签名，被百度判为签名错误。
func md5Hex(str string, javaCompat bool) string {
	sum := md5.Sum([]byte(str))
	h := hex.EncodeToString(sum[:])
	if !javaCompat {
		return h
	}
	t := strings.TrimLeft(h, "0")
	if t == "" {
		return "0"
	}
	return t
}

func encodeMD5(str string) string { return md5Hex(str, cfg.CompatJavaMD5) }

// needsFormEscape 报告某个字节在 application/x-www-form-urlencoded 的请求体里
// 是否会破坏解析。
//
// 只挑真正有害的：
//   - '&' 会被当成参数分隔符
//   - '+' 会被解码成空格（"c++"吧 就是栽在这里）
//   - '%' 会被当成百分号转义的开头
//   - '#' 会被当成 fragment 起点
//   - 空格和控制字符在请求体里本就非法
//
// 中文等多字节 UTF-8 的每个字节都 >= 0x80，不在此列，会原样保留 ——
// 这保证了正常吧名编码前后字节完全相同，与 Java 版没有任何差异。
func needsFormEscape(c byte) bool {
	switch c {
	case '&', '+', '%', '#', ' ':
		return true
	}
	return c < 0x20 || c == 0x7f
}

// formEscapeMinimal 只对 needsFormEscape 认定的字节做百分号编码。
func formEscapeMinimal(s string) string {
	need := false
	for i := 0; i < len(s); i++ {
		if needsFormEscape(s[i]) {
			need = true
			break
		}
	}
	if !need {
		return s // 绝大多数吧名走这条路，零改动
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		if c := s[i]; needsFormEscape(c) {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// formEncode 按配置对请求体里的参数值做编码。
func formEncode(s string) string {
	switch cfg.FormEncoding {
	case EncodeNone:
		return s
	case EncodeFull:
		return url.QueryEscape(s)
	default:
		return formEscapeMinimal(s)
	}
}

// signBody 拼接签到请求体。
//
// 注意签名与请求体用的是不同的值：
//   - 签名基于【原始】吧名，这是百度客户端的算法，任何情况下都不能改
//   - 请求体里的吧名按 cfg.FormEncoding 编码，默认只转义会破坏解析的字符
func signBody(kw, tbs string) string {
	sign := encodeMD5("kw=" + kw + "tbs=" + tbs + signSalt)
	return "kw=" + formEncode(kw) + "&tbs=" + tbs + "&sign=" + sign
}

// ---------------------------------------------------------------------------
// 网络请求 —— 对应 Java 版 top.srcrs.util.Request
// ---------------------------------------------------------------------------

func requestGet(url string) map[string]any {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		logInfo("get请求错误 -- %v", err)
		return nil
	}
	req.Header.Set("connection", "keep-alive")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("charset", "UTF-8")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Cookie", cookie())

	resp, err := client.Do(req)
	if err != nil {
		logInfo("get请求错误 -- %v", err)
		return nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logInfo("get请求错误 -- %v", err)
		return nil
	}
	return decodeJSON(body)
}

func requestPost(url, body string) map[string]any {
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		logInfo("post请求错误 -- %v", err)
		return nil
	}
	req.Header.Set("connection", "keep-alive")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("charset", "UTF-8")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Cookie", cookie())
	// Java 版用 addHeader("Host", ...) 硬写，Go 里必须走 req.Host 才会生效
	req.Host = postHost

	resp, err := client.Do(req)
	if err != nil {
		logInfo("post请求错误 -- %v", err)
		return nil
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logInfo("post请求错误 -- %v", err)
		return nil
	}
	return decodeJSON(respBody)
}

// ---------------------------------------------------------------------------
// 业务逻辑 —— 对应 Java 版 Run 的四个方法
// ---------------------------------------------------------------------------

// getTbs 登录校验并取得 tbs，签到时需要这个参数。
func getTbs() {
	jsonObject := requestGet(tbsURL)
	if jsonObject == nil {
		logError("获取tbs部分出现错误 -- 响应为空或解析失败")
		return
	}
	if getString(jsonObject, "is_login") == "1" {
		logInfo("获取tbs成功")
		tbs = getString(jsonObject, "tbs")
	} else {
		logWarn("获取tbs失败 -- %v", jsonObject)
	}
}

// getFollow 获取用户关注的贴吧列表，已签到的直接计入 success。
func getFollow() {
	jsonObject := requestGet(likeURL)
	if jsonObject == nil {
		logError("获取贴吧列表部分出现错误 -- 响应为空或解析失败")
		return
	}
	jsonArray := getArray(getObject(jsonObject, "data"), "like_forum")
	if jsonArray == nil {
		logError("获取贴吧列表部分出现错误 -- data.like_forum 缺失（BDUSS 是否已失效？）")
		return
	}
	logInfo("获取贴吧列表成功")
	followOK = true
	followNum = len(jsonArray)
	for _, item := range jsonArray {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := getString(obj, "forum_name")
		if getString(obj, "is_sign") == "0" {
			follow = append(follow, name) // 未签到，待签
		} else {
			success = append(success, name) // 今天已经签过了
		}
	}
	logInfo("共关注 %d 个贴吧，其中 %d 个今天已签到，待签 %d 个",
		followNum, len(success), len(follow))
}

// runSign 逐个签到。与 Java 版相比有三处节奏上的改动：
//
//  1. 每两个贴吧之间插入随机间隔（Java 版是 0，会在几秒内把所有请求打完）
//  2. 全部签完后立即退出，不再做无意义的等待
//  3. 连续若干轮没有新增成功就提前结束（有些吧永远签不上，重试无用）
func runSign() {
	noProgress := 0

	for round := 1; round <= cfg.RoundLimit; round++ {
		if len(success) >= followNum || len(follow) == 0 {
			break
		}

		logInfo("-----第 %d 轮签到开始，本轮 %d 个贴吧-----", round, len(follow))
		before := len(success)

		var remain []string
		for i, s := range follow {
			if i > 0 {
				time.Sleep(cfg.signDelay())
			}
			post := requestPost(signURL, signBody(s, tbs))
			if getString(post, "error_code") == "0" {
				success = append(success, s)
				logInfo("%s: 签到成功", s)
			} else {
				remain = append(remain, s)
				logWarn("%s: 签到失败 -- %s", s, describeSignError(post))
			}
		}
		follow = remain

		if gained := len(success) - before; gained > 0 {
			noProgress = 0
			logInfo("第 %d 轮新增成功 %d 个，累计 %d/%d", round, gained, len(success), followNum)
		} else {
			noProgress++
			logWarn("第 %d 轮没有新增成功（连续 %d 轮）", round, noProgress)
		}

		// 全部签完，收工
		if len(success) >= followNum || len(follow) == 0 {
			break
		}
		// 连续多轮零进展，再试下去也是浪费时间
		if noProgress >= cfg.MaxNoProgressRounds {
			logWarn("连续 %d 轮没有新增成功，提前结束。剩余 %d 个贴吧: %s",
				noProgress, len(follow), strings.Join(follow, ", "))
			break
		}
		// 已经是最后一轮，不需要再等待（Java 版这里会白白多睡 5 分钟）
		if round == cfg.RoundLimit {
			break
		}

		d := cfg.roundSleep()
		logInfo("等待 %s 后开始下一轮，并重新获取 tbs", d)
		time.Sleep(d)
		getTbs()
	}
}

// describeSignError 从签到响应里提取可读的失败原因，方便排查。
func describeSignError(post map[string]any) string {
	if post == nil {
		return "无响应或响应解析失败"
	}
	code := getString(post, "error_code")
	msg := getString(post, "error_msg")
	if msg == "" {
		msg = getString(post, "error")
	}
	if msg == "" {
		return "error_code=" + code
	}
	return "error_code=" + code + " " + msg
}

// ---------------------------------------------------------------------------

func main() {
	bduss = firstNonEmpty(argAt(0), os.Getenv("BDUSS"))
	if bduss == "" {
		logError("未提供 BDUSS。请设置环境变量 BDUSS，或作为第一个命令行参数传入")
		os.Exit(1)
	}

	cfg = loadConfig()
	client = &http.Client{Timeout: cfg.httpTimeout}
	notify := newNotifier(os.Getenv(envTGToken), os.Getenv(envTGChatID))
	logInfo("配置: %s", cfg.summary())

	if cfg.startupJitter > 0 {
		d := randDuration(0, cfg.startupJitter)
		logInfo("启动抖动: 等待 %s 后开始", d)
		time.Sleep(d)
	}

	start := time.Now()
	getTbs()
	getFollow()
	runSign()
	elapsed := time.Since(start).Round(time.Second)

	if !followOK {
		logError("未能获取关注贴吧列表，本次没有签到任何贴吧 - 耗时 %s", elapsed)
		logError("最常见的原因是 BDUSS 已失效，需要重新登录贴吧获取并更新 Secrets")
		notify.send(alertText(elapsed))
		return
	}

	logInfo("共 %d 个贴吧 - 成功: %d - 失败: %d - 耗时 %s",
		followNum, len(success), followNum-len(success), elapsed)
	notify.send(resultText(followNum, len(success), follow, elapsed))
}

// argAt 取第 i 个命令行参数，越界返回空串。
func argAt(i int) string {
	args := os.Args[1:]
	if i < len(args) {
		return args[i]
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
