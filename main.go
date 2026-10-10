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
	"sort"
	"strconv"
	"strings"
	"time"
)

// 接口地址 —— 与 Java 版 top.srcrs.Run 完全一致，不要改动。
// 这几个字符串决定了百度把这次请求当成"手机客户端签到"（经验更高），
// 与使用什么编程语言无关。

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
	// 签到接口返回「亲，你之前已经签过了」时的错误码
	codeAlreadySigned = "160002"
	// Java 版 POST 请求硬编码的 Host 头（与 signURL 的实际域名 c.tieba.baidu.com
	// 不一致，这是原版行为，照搬）
	postHost = "tieba.baidu.com"
)

// 全局状态 —— 对应 Java 版 Run 类的字段

var (
	cfg      Config
	client   *http.Client
	bduss    string   // 对应 Cookie 单例里的 BDUSS
	tbs      string   // 用户的 tbs
	follow   []string // 待签到的贴吧
	success  []string // 已签到成功的贴吧
	followOK bool     // 关注列表是否成功拉取到
	ignored  []string // 命中忽略名单、直接跳过的贴吧

	// signedNow 本次运行实际签上的贴吧及其经验信息。
	// 今天之前已签到的吧不在此列 —— 它们的经验不是这次挣的。
	signedNow []signResult
	// expWarned 保证"解析不到经验字段"的告警只打一次
	expWarned bool

	// followNum 关注的贴吧总数。Java 版初值是 201，拉取失败时会让末尾汇总
	// 输出"共 201 个贴吧 - 失败: 201"这种误导信息，这里改为 0。
	followNum = 0
)

func cookie() string { return "BDUSS=" + bduss }

// 日志

func logAt(level, format string, a ...any) {
	fmt.Printf("%s %-5s %s\n", time.Now().Format("2006-01-02 15:04:05"), level, fmt.Sprintf(format, a...))
}

func logInfo(format string, a ...any)  { logAt("INFO", format, a...) }
func logWarn(format string, a ...any)  { logAt("WARN", format, a...) }
func logError(format string, a ...any) { logAt("ERROR", format, a...) }

// JSON helper —— 模拟 fastjson 的 getString（数字/布尔都会被转成字符串）。
// 百度的 is_login / is_sign / error_code 在不同接口里有时是数字有时是字符串，
// 这里必须两种都能处理。

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

// signResult 一次成功签到的收获。字段缺失时为 0。
type signResult struct {
	name string
	exp  int // 本次获得的经验
	cont int // 连续签到天数
	rank int // 今日本吧签到排名
}

// getInt 取整数字段。百度的数字有时是 JSON number，有时是字符串，两种都要认。
func getInt(m map[string]any, key string) int {
	s := getString(m, key)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// parseSignResult 从签到响应里提取经验等信息。
//
// 百度把这些放在 user_info 下面。字段名不保证长期稳定，所以解析不到时
// 不报错，只在第一次打一条告警并列出实际存在的字段，方便对照修正。
func parseSignResult(name string, post map[string]any) signResult {
	r := signResult{name: name}
	info := getObject(post, "user_info")
	if info == nil {
		warnExpOnce("签到响应里没有 user_info", post)
		return r
	}
	r.exp = getInt(info, "sign_bonus_point")
	r.cont = getInt(info, "cont_sign_num")
	r.rank = getInt(info, "user_sign_rank")
	if r.exp == 0 {
		warnExpOnce("user_info 里取不到 sign_bonus_point", info)
	}
	return r
}

// warnExpOnce 打印一次经验解析失败的告警，附上实际可用的字段名。
func warnExpOnce(reason string, m map[string]any) {
	if expWarned {
		return
	}
	expWarned = true
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	logWarn("经验值解析失败（%s），签到本身不受影响。实际字段: %s", reason, strings.Join(keys, ", "))
	logWarn("把 config.json 的 logSignResponse 改成 true 可以打印完整响应，据此修正字段名")
}

// MD5 —— 对应 Java 版 top.srcrs.util.Encryption

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

// 网络请求 —— 对应 Java 版 top.srcrs.util.Request

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

// 业务逻辑 —— 对应 Java 版 Run 的四个方法

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

// forumState 关注列表里一个吧今天的签到状态。
type forumState struct {
	name   string
	signed bool // 列表里 is_sign != "0"
}

// fetchFollow 拉一次关注列表，忽略名单里的吧不返回。拉取失败时 ok 为 false。
func fetchFollow() (forums []forumState, ok bool) {
	jsonObject := requestGet(likeURL)
	if jsonObject == nil {
		logError("获取贴吧列表部分出现错误 -- 响应为空或解析失败")
		return nil, false
	}
	jsonArray := getArray(getObject(jsonObject, "data"), "like_forum")
	if jsonArray == nil {
		logError("获取贴吧列表部分出现错误 -- data.like_forum 缺失（BDUSS 是否已失效？）")
		return nil, false
	}
	ignored = nil // 每次重新统计，多次拉取不会重复累加
	for _, item := range jsonArray {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := getString(obj, "forum_name")
		if cfg.ignored(name) {
			// 忽略名单里的吧完全不参与统计，否则 success 永远追不上 followNum
			ignored = append(ignored, name)
			continue
		}
		forums = append(forums, forumState{name: name, signed: getString(obj, "is_sign") != "0"})
	}
	return forums, true
}

// getFollow 获取用户关注的贴吧列表，今天已签的直接计入 success，其余进待签名单。
func getFollow() {
	forums, ok := fetchFollow()
	if !ok {
		return
	}
	logInfo("获取贴吧列表成功")
	followOK = true
	followNum = len(forums)
	for _, f := range forums {
		if f.signed {
			success = append(success, f.name) // 今天已经签过了
		} else {
			follow = append(follow, f.name) // 未签到，待签
		}
	}
	msg := fmt.Sprintf("共关注 %d 个贴吧，其中 %d 个今天已签到，待签 %d 个",
		followNum, len(success), len(follow))
	if len(ignored) > 0 {
		msg += fmt.Sprintf("（另有 %d 个在忽略名单）", len(ignored))
	}
	logInfo("%s", msg)
}

// runSign 最多签 3 轮：
//
//  1. 第 1 轮：签全部待签的吧
//  2. 第 2 轮：只补第 1 轮失败的
//  3. 第 3 轮：先重新拉关注列表，列表显示已签的直接算成功，剩下的再补一次；
//     补完如果还有失败的，最后再拉一次列表核对
//
// 哪一轮之后全部签完就直接结束，不再等待、不再拉列表。
//
// 列表核对只会把「失败」改成「成功」，不会反过来：刚签上的吧，
// 百度的列表不一定马上更新，以列表为准反而会误报失败。
func runSign() {
	for round := 1; round <= 3 && len(follow) > 0; round++ {
		if round > 1 {
			pauseBeforeNextRound()
		}
		if round == 3 {
			// 前两轮失败的，可能其实已经签上了（例如被报「签得太快」但百度已记上）
			reconcileWithList()
			if len(follow) == 0 {
				return
			}
		}
		signRound(round)
	}
	if len(follow) > 0 {
		reconcileWithList()
	}
	if len(follow) > 0 {
		logWarn("3 轮之后仍有 %d 个未签上", len(follow))
	}
}

// signRound 把待签名单里的吧挨个签一遍，失败的留在名单里给下一轮。
func signRound(round int) {
	logInfo("-----第 %d 轮签到开始，本轮 %d 个贴吧-----", round, len(follow))
	before := len(success)

	var remain []string
	for i, s := range follow {
		if i > 0 {
			time.Sleep(cfg.signDelay())
		}
		post := requestPost(signURL, signBody(s, tbs))
		if cfg.LogSignResponse {
			raw, _ := json.Marshal(post)
			logInfo("[%d] 原始响应 %s", i+1, raw)
		}
		switch getString(post, "error_code") {
		case "0":
			success = append(success, s)
			r := parseSignResult(s, post)
			signedNow = append(signedNow, r)
		case codeAlreadySigned:
			// 百度明确说今天已经签过了，算成功。常见于上一轮被报「签得太快」(340011)，
			// 但百度其实已经把那次签到记上了。不计入 signedNow：这次拿不到经验数。
			success = append(success, s)
			logInfo("[%d/%d] 百度返回今天已签过，计为成功 -- %s", i+1, len(follow), describeSignError(post))
		default:
			remain = append(remain, s)
			logWarn("[%d/%d] 签到失败 -- %s", i+1, len(follow), describeSignError(post))
		}
	}
	follow = remain
	logInfo("第 %d 轮成功 %d 个，累计 %d/%d", round, len(success)-before, len(success), followNum)
}

// pauseBeforeNextRound 两轮之间随机等一会儿，免得又被百度提示「签得太快」，
// 再重新获取 tbs。
func pauseBeforeNextRound() {
	d := cfg.roundSleep()
	// 打印时取整到秒，随机值本身不取整
	logInfo("等待 %s 后开始下一轮，并重新获取 tbs", d.Round(time.Second))
	time.Sleep(d)
	getTbs()
}

// reconcileWithList 重新拉一次关注列表，把列表显示已签的从待签名单里划掉、计为成功。
// 拉取失败就沿用签到时的记录，不影响后续流程。
func reconcileWithList() {
	forums, ok := fetchFollow()
	if !ok {
		logWarn("核对用的关注列表拉取失败，沿用签到时的记录")
		return
	}
	signedInList := make(map[string]bool, len(forums))
	for _, f := range forums {
		if f.signed {
			signedInList[f.name] = true
		}
	}
	var remain []string
	for _, s := range follow {
		if signedInList[s] {
			success = append(success, s)
		} else {
			remain = append(remain, s)
		}
	}
	logInfo("核对关注列表：%d 个其实已签上，计为成功；仍有 %d 个未签", len(follow)-len(remain), len(remain))
	follow = remain
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
	if s := cfg.ignoreSummary(); s != "" {
		logInfo("%s", s)
	}

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
		os.Exit(1)
	}

	logInfo("共 %d 个贴吧 - 成功: %d - 失败: %d - 耗时 %s",
		followNum, len(success), followNum-len(success), elapsed)
	if len(signedNow) > 0 {
		logInfo("本次新签 %d 个贴吧，共获得 %d 经验", len(signedNow), totalExp(signedNow))
	}
	notify.send(resultText(followNum, len(success), follow, signedNow, elapsed))

	// 推送发完再决定退出码，保证告警一定送得出去
	if code := exitCode(followOK, followNum, len(success)); code != 0 {
		logError("本次运行判定为失败，以退出码 %d 结束，workflow 将标红", code)
		os.Exit(code)
	}
}

// exitCode 决定进程退出码，也就是 GitHub Actions 上这次 run 是绿还是红。
//
// 判定为失败（红）的只有两种情况，都代表"整体坏了"：
//
//   - 关注列表拉不到：BDUSS 失效或网络不通，一个吧都没签
//   - 拉到了列表但一个都没签成功：通常是 tbs 获取失败导致全军覆没
//
// 部分贴吧失败【不】标红。有些吧（已封禁的、有特殊规则的）本来就永远签不上，
// 让它天天标红会把这个信号彻底废掉 —— 那种情况看 Telegram 里的失败列表即可。
func exitCode(followOK bool, total, ok int) int {
	if !followOK {
		return 1
	}
	if total > 0 && ok == 0 {
		return 1
	}
	return 0
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

// totalExp 本次运行一共挣到的经验。
func totalExp(rs []signResult) int {
	n := 0
	for _, r := range rs {
		n += r.exp
	}
	return n
}
