package main

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"time"
)

// Config 控制签到节奏与兼容性。
//
// 优先级：环境变量 > config.json > 内置默认值。
// 三者都可以只提供一部分，缺的部分自动落到下一级。
type Config struct {
	// CompatJavaMD5 为 true 时复刻 Java 版 new BigInteger(1, digest).toString(16)
	// 的行为（会吃掉哈希开头的 0，约 6.2% 的签名因此残缺）。
	// 正常使用应保持 false。
	CompatJavaMD5 bool `json:"compatJavaMD5"`

	// RoundLimit 最多签到轮数。
	RoundLimit int `json:"roundLimit"`

	// MaxNoProgressRounds 连续多少轮没有任何新增签到成功就提前结束。
	// 有些贴吧（例如已封禁的）是永远签不上的，继续重试只是浪费时间。
	MaxNoProgressRounds int `json:"maxNoProgressRounds"`

	// SignDelayMin/Max 每两个贴吧的签到请求之间的随机间隔。
	// 这是最重要的一项：原 Java 版是 0，会在几秒内把所有请求打完。
	SignDelayMin string `json:"signDelayMin"`
	SignDelayMax string `json:"signDelayMax"`

	// RoundSleepMin/Max 每轮之间的随机等待。
	RoundSleepMin string `json:"roundSleepMin"`
	RoundSleepMax string `json:"roundSleepMax"`

	// StartupJitterMax 启动前的随机延迟上限，用来打散每天固定的执行时刻。
	// 默认 0：GitHub 的 cron 本身就经常延迟几分钟到几十分钟，已有天然抖动。
	StartupJitterMax string `json:"startupJitterMax"`

	// HTTPTimeout 单个 HTTP 请求的超时。
	HTTPTimeout string `json:"httpTimeout"`

	// IgnoreForums 里的贴吧直接跳过，不发签到请求，也不计入总数。
	// 用于那些永远签不上的条目（例如"贴吧热议"这种并非真实贴吧的聚合项），
	// 避免每天的推送都因为它们而变成"有失败"。
	IgnoreForums []string `json:"ignoreForums"`

	// LogSignResponse 为 true 时打印每次签到的原始响应，用于排查
	// 百度返回结构变化。会包含 user_id 等信息，平时关闭。
	LogSignResponse bool `json:"logSignResponse"`

	// FormEncoding 决定请求体里 kw= 后面的吧名怎么写。签名始终基于原始吧名，
	// 这一项只影响请求体，不影响签名。
	//
	//   none    —— 原始字节直接拼接，与 Java 版完全一致。
	//              像 "c++" 这样含 + 的吧名会被服务器解析成空格而签到失败。
	//   minimal —— 只编码会破坏表单解析的字符（% & + # 空格和控制字符），
	//              中文等其它字节原样保留。对正常吧名与 none 的字节完全相同，
	//              没有回归风险，同时修好含特殊字符的吧名。（默认）
	//   full    —— 标准 url.QueryEscape，中文也会变成 %XX。
	//              这是百度官方客户端的做法，但与现有可用行为差异最大。
	FormEncoding string `json:"formEncoding"`

	// 以下为解析后的时长，不参与 JSON
	signDelayMin  time.Duration
	signDelayMax  time.Duration
	roundSleepMin time.Duration
	roundSleepMax time.Duration
	startupJitter time.Duration
	httpTimeout   time.Duration
}

func defaultConfig() Config {
	return Config{
		CompatJavaMD5:       false,
		RoundLimit:          5,
		MaxNoProgressRounds: 2,
		SignDelayMin:        "1s",
		SignDelayMax:        "3s",
		RoundSleepMin:       "30s",
		RoundSleepMax:       "90s",
		StartupJitterMax:    "0s",
		HTTPTimeout:         "30s",
		FormEncoding:        EncodeMinimal,
		IgnoreForums:        []string{"贴吧热议"},
		LogSignResponse:     false,
	}
}

const configPathEnv = "TIEBA_CONFIG"

// FormEncoding 的三个取值
const (
	EncodeNone    = "none"
	EncodeMinimal = "minimal"
	EncodeFull    = "full"
)

// loadConfig 读取默认值 -> config.json -> 环境变量，逐层覆盖。
// config.json 不存在不算错误，直接用默认值。
func loadConfig() Config {
	cfg := defaultConfig()

	path := os.Getenv(configPathEnv)
	if path == "" {
		path = "config.json"
	}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			logWarn("%s 解析失败，改用默认配置 -- %v", path, err)
			cfg = defaultConfig()
		} else {
			logInfo("已加载配置文件 %s", path)
		}
	} else if !os.IsNotExist(err) {
		logWarn("读取 %s 失败，改用默认配置 -- %v", path, err)
	}

	applyEnv(&cfg)
	cfg.parseDurations()
	return cfg
}

// applyEnv 用环境变量覆盖配置。环境变量优先级最高，方便在 workflow 里临时调整。
func applyEnv(cfg *Config) {
	envStr := func(key string, dst *string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	envStr("TIEBA_SIGN_DELAY_MIN", &cfg.SignDelayMin)
	envStr("TIEBA_SIGN_DELAY_MAX", &cfg.SignDelayMax)
	envStr("TIEBA_ROUND_SLEEP_MIN", &cfg.RoundSleepMin)
	envStr("TIEBA_ROUND_SLEEP_MAX", &cfg.RoundSleepMax)
	envStr("TIEBA_STARTUP_JITTER_MAX", &cfg.StartupJitterMax)
	envStr("TIEBA_HTTP_TIMEOUT", &cfg.HTTPTimeout)
	envStr("TIEBA_FORM_ENCODING", &cfg.FormEncoding)

	if v := os.Getenv("TIEBA_LOG_SIGN_RESPONSE"); v != "" {
		switch v {
		case "1", "true", "TRUE", "True":
			cfg.LogSignResponse = true
		case "0", "false", "FALSE", "False":
			cfg.LogSignResponse = false
		default:
			logWarn("TIEBA_LOG_SIGN_RESPONSE=%q 无效，仍用 %v", v, cfg.LogSignResponse)
		}
	}

	if v := os.Getenv("TIEBA_ROUND_LIMIT"); v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			cfg.RoundLimit = n
		} else {
			logWarn("TIEBA_ROUND_LIMIT=%q 无效，仍用 %d", v, cfg.RoundLimit)
		}
	}
	if v := os.Getenv("TIEBA_MAX_NO_PROGRESS_ROUNDS"); v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			cfg.MaxNoProgressRounds = n
		} else {
			logWarn("TIEBA_MAX_NO_PROGRESS_ROUNDS=%q 无效，仍用 %d", v, cfg.MaxNoProgressRounds)
		}
	}
	if v := os.Getenv("TIEBA_COMPAT_JAVA_MD5"); v != "" {
		switch v {
		case "1", "true", "TRUE", "True":
			cfg.CompatJavaMD5 = true
		case "0", "false", "FALSE", "False":
			cfg.CompatJavaMD5 = false
		default:
			logWarn("TIEBA_COMPAT_JAVA_MD5=%q 无效，仍用 %v", v, cfg.CompatJavaMD5)
		}
	}
}

func parsePositiveInt(s string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("必须为正整数")
	}
	return n, nil
}

// parseDurations 把字符串时长解析成 time.Duration，非法值退回默认值，
// 并保证 min <= max。
func (c *Config) parseDurations() {
	def := defaultConfig()

	pick := func(name, raw, fallback string) time.Duration {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			logWarn("%s=%q 无效，改用默认值 %s", name, raw, fallback)
			d, _ = time.ParseDuration(fallback)
		}
		return d
	}

	c.signDelayMin = pick("signDelayMin", c.SignDelayMin, def.SignDelayMin)
	c.signDelayMax = pick("signDelayMax", c.SignDelayMax, def.SignDelayMax)
	c.roundSleepMin = pick("roundSleepMin", c.RoundSleepMin, def.RoundSleepMin)
	c.roundSleepMax = pick("roundSleepMax", c.RoundSleepMax, def.RoundSleepMax)
	c.startupJitter = pick("startupJitterMax", c.StartupJitterMax, def.StartupJitterMax)
	c.httpTimeout = pick("httpTimeout", c.HTTPTimeout, def.HTTPTimeout)

	if c.signDelayMax < c.signDelayMin {
		logWarn("signDelayMax(%s) 小于 signDelayMin(%s)，已对调", c.signDelayMax, c.signDelayMin)
		c.signDelayMin, c.signDelayMax = c.signDelayMax, c.signDelayMin
	}
	if c.roundSleepMax < c.roundSleepMin {
		logWarn("roundSleepMax(%s) 小于 roundSleepMin(%s)，已对调", c.roundSleepMax, c.roundSleepMin)
		c.roundSleepMin, c.roundSleepMax = c.roundSleepMax, c.roundSleepMin
	}
	if c.RoundLimit <= 0 {
		logWarn("roundLimit=%d 无效，改用 %d", c.RoundLimit, def.RoundLimit)
		c.RoundLimit = def.RoundLimit
	}
	if c.MaxNoProgressRounds <= 0 {
		logWarn("maxNoProgressRounds=%d 无效，改用 %d", c.MaxNoProgressRounds, def.MaxNoProgressRounds)
		c.MaxNoProgressRounds = def.MaxNoProgressRounds
	}
	switch c.FormEncoding {
	case EncodeNone, EncodeMinimal, EncodeFull:
	default:
		logWarn("formEncoding=%q 无效（可选 %s / %s / %s），改用 %s",
			c.FormEncoding, EncodeNone, EncodeMinimal, EncodeFull, def.FormEncoding)
		c.FormEncoding = def.FormEncoding
	}
}

// ignored 报告某个贴吧是否在忽略名单里。
func (c *Config) ignored(name string) bool {
	for _, s := range c.IgnoreForums {
		if s == name {
			return true
		}
	}
	return false
}

func (c *Config) signDelay() time.Duration  { return randDuration(c.signDelayMin, c.signDelayMax) }
func (c *Config) roundSleep() time.Duration { return randDuration(c.roundSleepMin, c.roundSleepMax) }

// randDuration 返回 [min, max] 之间的随机时长。
func randDuration(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + rand.N(max-min+1)
}

// summary 供启动时打印，方便在 Actions 日志里确认这次跑用的是什么参数。
func (c *Config) summary() string {
	return fmt.Sprintf(
		"签到间隔 %s~%s | 轮间等待 %s~%s | 最多 %d 轮 | 连续 %d 轮无进展则提前结束 | 启动抖动 ≤%s | 超时 %s | 表单编码 %s | JavaMD5兼容 %v",
		c.signDelayMin, c.signDelayMax, c.roundSleepMin, c.roundSleepMax,
		c.RoundLimit, c.MaxNoProgressRounds, c.startupJitter, c.httpTimeout,
		c.FormEncoding, c.CompatJavaMD5)
}

// ignoreSummary 供启动时打印忽略名单。
func (c *Config) ignoreSummary() string {
	if len(c.IgnoreForums) == 0 {
		return ""
	}
	return fmt.Sprintf("忽略名单 %d 个", len(c.IgnoreForums))
}
