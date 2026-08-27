package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// 接口地址 —— 与 Java 版 top.srcrs.Run 完全一致
// ---------------------------------------------------------------------------

const (
	// 获取用户所有关注贴吧
	likeURL = "https://tieba.baidu.com/mo/q/newmoindex"
	// 获取用户的 tbs
	tbsURL = "http://tieba.baidu.com/dc/common/tbs"
	// 贴吧签到接口
	signURL = "http://c.tieba.baidu.com/c/c/forum/sign"
	// server 酱推送（Java 版用的旧接口，已停服，v1 原样保留）
	serverChanURL = "https://sc.ftqq.com/%s.send"

	// Java 版 Request 类里写死的 User-Agent
	userAgent = "Mozilla/5.0 (Windows NT 6.1; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/39.0.2171.71 Safari/537.36"
	// 签名盐值
	signSalt = "tiebaclient!!!"
	// Java 版 POST 请求硬编码的 Host 头（注意：与 signURL 的实际域名 c.tieba.baidu.com 不一致，
	// 这是 Java 版原本的行为，照搬）
	postHost = "tieba.baidu.com"
)

// ---------------------------------------------------------------------------
// 兼容开关 —— v1 全部保持 Java 行为。第二阶段优化时只需要改这几个值。
// ---------------------------------------------------------------------------

const (
	// true  = 复刻 Java 的 new BigInteger(1, digest).toString(16)，会吃掉哈希开头的 0（约 6.2% 的签名因此残缺）
	// false = 标准 32 位小写十六进制 MD5
	compatJavaMD5 = true

	// 最多签到轮数（Java: Integer flag = 5）
	roundLimit = 5
)

// roundSleep 每轮结束后的等待时间。默认 5 分钟，与 Java 版 Thread.sleep(1000 * 60 * 5) 一致。
//
// 仅用于本地调试时缩短等待：TIEBA_ROUND_SLEEP=2s go run . <BDUSS>
// workflow 里不设置这个变量，因此线上行为与 Java 版完全相同。
var roundSleep = 5 * time.Minute

func init() {
	if v := os.Getenv("TIEBA_ROUND_SLEEP"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			roundSleep = d
			logWarn("TIEBA_ROUND_SLEEP 已生效，每轮等待时间被改为 %s（仅供调试）", d)
		} else {
			logWarn("TIEBA_ROUND_SLEEP=%q 解析失败，仍使用默认的 %s", v, roundSleep)
		}
	}
}

// HTTP 超时。Java 版没有设超时（理论上可以无限挂住），这里设 30s 防止 workflow 卡死，
// 是本次移植唯一一处主动加的保护，不影响签到逻辑。
var client = &http.Client{Timeout: 30 * time.Second}

// ---------------------------------------------------------------------------
// 全局状态 —— 对应 Java 版 Run 类的字段
// ---------------------------------------------------------------------------

var (
	bduss     string   // 对应 Cookie 单例里的 BDUSS
	tbs       string   // 用户的 tbs
	follow    []string // 待签到的贴吧
	success   []string // 已签到成功的贴吧
	followNum = 201    // Java 版初值就是 201，getFollow 成功后才会被覆盖
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
// JSON helper —— 模拟 fastjson 的 getString（数字/布尔都会被转成字符串）
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

func encodeMD5(str string) string {
	sum := md5.Sum([]byte(str))
	h := hex.EncodeToString(sum[:])
	if !compatJavaMD5 {
		return h
	}
	// 复刻 new BigInteger(1, md.digest()).toString(16)：按正整数打印，前导零全部消失
	t := strings.TrimLeft(h, "0")
	if t == "" {
		return "0"
	}
	return t
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

// 进行登录，获得 tbs，签到的时候需要用到这个参数
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

// 获取用户所关注的贴吧列表
func getFollow() {
	jsonObject := requestGet(likeURL)
	if jsonObject == nil {
		logError("获取贴吧列表部分出现错误 -- 响应为空或解析失败")
		return
	}
	logInfo("获取贴吧列表成功")
	jsonArray := getArray(getObject(jsonObject, "data"), "like_forum")
	if jsonArray == nil {
		logError("获取贴吧列表部分出现错误 -- data.like_forum 缺失")
		return
	}
	followNum = len(jsonArray)
	for _, item := range jsonArray {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := getString(obj, "forum_name")
		if getString(obj, "is_sign") == "0" {
			// 未签到的贴吧加入 follow，待签到
			follow = append(follow, name)
		} else {
			// 已经签到成功的贴吧，加入 success
			success = append(success, name)
		}
	}
}

// 开始签到。每一轮把所有未签到的贴吧签一遍，最多 5 轮。
// 每轮结束后如果还没签完就等待，并重新获取 tbs。
func runSign() {
	flag := roundLimit
	for len(success) < followNum && flag > 0 {
		logInfo("-----第 %d 轮签到开始-----", roundLimit-flag+1)
		logInfo("还剩 %d 贴吧需要签到", followNum-len(success))

		var remain []string
		for _, s := range follow {
			body := "kw=" + s + "&tbs=" + tbs + "&sign=" + encodeMD5("kw="+s+"tbs="+tbs+signSalt)
			post := requestPost(signURL, body)
			if getString(post, "error_code") == "0" {
				success = append(success, s)
				logInfo("%s: 签到成功", s)
			} else {
				remain = append(remain, s)
				logWarn("%s: 签到失败", s)
			}
		}
		follow = remain

		if len(success) != followNum {
			// 为防止短时间内多次请求接口触发风控，每一轮签到完等待
			time.Sleep(roundSleep)
			// 重新获取 tbs，解决第 1 次失败后剩余循环都失败的问题
			getTbs()
		}
		flag--
	}
}

// 发送运行结果到微信，通过 server 酱
func send(sckey string) {
	total := strconv.Itoa(followNum)
	ok := strconv.Itoa(len(success))
	fail := strconv.Itoa(followNum - len(success))

	text := "总: " + total + " - "
	text += "成功: " + ok + " 失败: " + fail
	desp := "共 " + total + " 贴吧\n\n"
	desp += "成功: " + ok + " 失败: " + fail
	body := "text=" + text + "&desp=" + "TiebaSignIn运行结果\n\n" + desp

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf(serverChanURL, sckey), strings.NewReader(body))
	if err != nil {
		logError("server酱发送失败 -- %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		logError("server酱发送失败 -- %v", err)
		return
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		logError("server酱发送失败 -- %v", err)
		return
	}
	logInfo("server酱推送正常")
}

// ---------------------------------------------------------------------------

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		logWarn("请在Secrets中填写BDUSS")
		os.Exit(1)
	}
	bduss = args[0]

	getTbs()
	getFollow()
	runSign()
	logInfo("共 %d 个贴吧 - 成功: %d - 失败: %d", followNum, len(success), followNum-len(success))

	if len(args) == 2 {
		send(args[1])
	}
}
