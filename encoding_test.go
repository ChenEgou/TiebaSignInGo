package main

import (
	"net/url"
	"strings"
	"testing"
)

// TestFormEscapeMinimalLeavesNormalNamesUntouched 是本次改动最关键的保证：
// 正常吧名（中文、字母、数字）编码前后必须字节完全相同，
// 否则就会让现在能正常签到的贴吧出问题。
func TestFormEscapeMinimalLeavesNormalNamesUntouched(t *testing.T) {
	names := []string{
		"抗压背锅", "孙笑川", "李毅", "英雄联盟", "steam",
		"csgo", "原神", "崩坏3", "vscode", "golang",
		"日本語", "한국어", "emoji😀吧", "a_b-c.d", "吧务",
	}
	for _, n := range names {
		if got := formEscapeMinimal(n); got != n {
			t.Errorf("正常吧名被改动了！%q -> %q", n, got)
		}
	}
}

// TestFormEscapeMinimalFixesBrokenNames 会破坏表单解析的字符必须被转义。
func TestFormEscapeMinimalFixesBrokenNames(t *testing.T) {
	cases := []struct{ in, want string }{
		{"c++", "c%2B%2B"},   // 真实存在的吧，+ 会被解成空格
		{"a&b", "a%26b"},     // & 会被当成参数分隔符
		{"100%", "100%25"},   // % 会被当成转义开头
		{"a b", "a%20b"},     // 空格
		{"a#b", "a%23b"},     // # 会被当成 fragment
		{"c++吧", "c%2B%2B吧"}, // 中文部分仍原样保留
		{"a\nb", "a%0Ab"},    // 控制字符
	}
	for _, c := range cases {
		if got := formEscapeMinimal(c.in); got != c.want {
			t.Errorf("formEscapeMinimal(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormEscapeMinimalIsIdempotent 已经编码过的串再编码一次不应该被二次转义。
// （这里验证的是编码结果里不含裸露的 % 以外的问题字符）
func TestFormEscapeMinimalNoRawSpecials(t *testing.T) {
	for _, n := range []string{"c++", "a&b", "100%", "a b", "a#b"} {
		got := formEscapeMinimal(n)
		for i := 0; i < len(got); i++ {
			c := got[i]
			if c == '%' {
				continue // 转义序列的引导符，合法
			}
			if needsFormEscape(c) {
				t.Errorf("formEscapeMinimal(%q) = %q 中仍有未转义的 %q", n, got, string(c))
			}
		}
	}
}

// TestSignIsAlwaysOverRawName 无论请求体怎么编码，
// 签名都必须基于【原始】吧名 —— 这是百度客户端的算法，改了就全挂。
func TestSignIsAlwaysOverRawName(t *testing.T) {
	const kw, tb = "c++", "abc123"
	wantSign := md5Hex("kw=c++tbs=abc123tiebaclient!!!", false)

	for _, mode := range []string{EncodeNone, EncodeMinimal, EncodeFull} {
		old := cfg.FormEncoding
		cfg.FormEncoding = mode
		body := signBody(kw, tb)
		cfg.FormEncoding = old

		idx := strings.Index(body, "&sign=")
		if idx < 0 {
			t.Fatalf("[%s] 请求体里没有 sign 参数: %s", mode, body)
		}
		if gotSign := body[idx+len("&sign="):]; gotSign != wantSign {
			t.Errorf("[%s] 签名基于了编码后的吧名\nwant: %s\ngot : %s", mode, wantSign, gotSign)
		}
	}
}

// TestSignBodyPerMode 三种编码模式下请求体各自应有的样子。
func TestSignBodyPerMode(t *testing.T) {
	const kw, tb = "c++吧", "abc123"
	sign := md5Hex("kw="+kw+"tbs="+tb+signSalt, false)

	cases := []struct{ mode, wantKw string }{
		{EncodeNone, "c++吧"},                  // 与 Java 版一致，会挂
		{EncodeMinimal, "c%2B%2B吧"},           // 只转义 +
		{EncodeFull, url.QueryEscape("c++吧")}, // 中文也转义
	}
	for _, c := range cases {
		old := cfg.FormEncoding
		cfg.FormEncoding = c.mode
		got := signBody(kw, tb)
		cfg.FormEncoding = old

		want := "kw=" + c.wantKw + "&tbs=" + tb + "&sign=" + sign
		if got != want {
			t.Errorf("[%s]\nwant: %s\ngot : %s", c.mode, want, got)
		}
	}
}

// TestFormEncodingNoneMatchesJava none 模式必须与 Java 版逐字节一致。
func TestFormEncodingNoneMatchesJava(t *testing.T) {
	old := cfg.FormEncoding
	oldMD5 := cfg.CompatJavaMD5
	cfg.FormEncoding = EncodeNone
	cfg.CompatJavaMD5 = true
	defer func() { cfg.FormEncoding, cfg.CompatJavaMD5 = old, oldMD5 }()

	const kw, tb = "抗压背锅", "1a2b3c4d"
	// Java: "kw="+s+"&tbs="+tbs+"&sign="+ Encryption.enCodeMd5("kw="+s+"tbs="+tbs+"tiebaclient!!!")
	want := "kw=抗压背锅&tbs=1a2b3c4d&sign=" + md5Hex("kw=抗压背锅tbs=1a2b3c4dtiebaclient!!!", true)
	if got := signBody(kw, tb); got != want {
		t.Errorf("none 模式与 Java 版不一致\nwant: %s\ngot : %s", want, got)
	}
}

// TestConfigFormEncodingValidation 非法的 formEncoding 必须退回默认值。
func TestConfigFormEncodingValidation(t *testing.T) {
	c := defaultConfig()
	c.FormEncoding = "乱填的"
	c.parseDurations()
	if c.FormEncoding != EncodeMinimal {
		t.Errorf("非法 formEncoding 应退回 %s，实际 %s", EncodeMinimal, c.FormEncoding)
	}

	for _, mode := range []string{EncodeNone, EncodeMinimal, EncodeFull} {
		c := defaultConfig()
		c.FormEncoding = mode
		c.parseDurations()
		if c.FormEncoding != mode {
			t.Errorf("合法值 %s 被改成了 %s", mode, c.FormEncoding)
		}
	}
}

// TestExitCode 决定 Actions 上这次 run 是绿还是红。
//
// 关键保证：部分贴吧失败【不能】标红。有些吧永远签不上，
// 天天标红会让这个信号彻底失去意义。
func TestExitCode(t *testing.T) {
	cases := []struct {
		name     string
		followOK bool
		total    int
		ok       int
		want     int
		why      string
	}{
		{"拉不到关注列表", false, 0, 0, 1, "BDUSS 失效或网络不通"},
		{"拉到列表但一个没签上", true, 30, 0, 1, "通常是 tbs 获取失败，整体故障"},
		{"全部签到成功", true, 30, 30, 0, ""},
		{"部分失败", true, 30, 28, 0, "个别吧签不上属正常，不该标红"},
		{"只成功一个", true, 30, 1, 0, "有进展就不算整体故障"},
		{"一个吧都没关注", true, 0, 0, 0, "没关注贴吧不是错误"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := exitCode(c.followOK, c.total, c.ok); got != c.want {
				t.Errorf("exitCode(followOK=%v, total=%d, ok=%d) = %d, want %d  %s",
					c.followOK, c.total, c.ok, got, c.want, c.why)
			}
		})
	}
}
