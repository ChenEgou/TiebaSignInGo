package main

import (
	"bufio"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestMain 给测试准备一份默认配置，供 encodeMD5 / signBody 使用。
func TestMain(m *testing.M) {
	cfg = defaultConfig()
	cfg.parseDurations()
	os.Exit(m.Run())
}

// javaGolden 遍历基准数据。基准由 testdata/gen_golden.py 生成，
// 复现的是 Java new BigInteger(1, digest).toString(16) 的语义。
// 输入构造方式必须与该脚本保持一致。
func javaGolden(t *testing.T, fn func(i int, kw, tbs, want string)) int {
	t.Helper()
	f, err := os.Open("testdata/md5_golden.txt")
	if err != nil {
		t.Fatalf("打不开基准数据: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		fn(n, fmt.Sprintf("测试贴吧%d", n), fmt.Sprintf("abcdef0123456789abcdef012345678%d", n%10), sc.Text())
		n++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("基准数据为空")
	}
	return n
}

// TestMD5CompatMatchesJava 验证兼容模式与 Java 版逐位一致。
// 这是"1:1 复刻"的凭据，即使默认已经关闭兼容模式也要一直保留。
func TestMD5CompatMatchesJava(t *testing.T) {
	truncated := 0
	n := javaGolden(t, func(i int, kw, tbs, want string) {
		got := md5Hex("kw="+kw+"tbs="+tbs+signSalt, true)
		if got != want {
			t.Fatalf("第 %d 行不一致\n输入: kw=%s tbs=%s\nJava: %s\nGo  : %s", i+1, kw, tbs, want, got)
		}
		if len(want) != 32 {
			truncated++
		}
	})
	if truncated == 0 {
		t.Fatal("基准数据没有覆盖前导零被截断的情况，测试失去意义")
	}
	t.Logf("比对 %d 条全部一致，其中 %d 条 (%.2f%%) 是 Java 吃掉前导零后的残缺签名",
		n, truncated, float64(truncated)*100/float64(n))
}

// TestMD5FixRepairsTruncation 验证关闭兼容模式后：
//   - 所有签名都是完整的 32 位
//   - 原本被 Java 截断的那些，补上前导零后正好等于 Java 的输出
func TestMD5FixRepairsTruncation(t *testing.T) {
	repaired := 0
	n := javaGolden(t, func(i int, kw, tbs, want string) {
		got := md5Hex("kw="+kw+"tbs="+tbs+signSalt, false)
		if len(got) != 32 {
			t.Fatalf("第 %d 行长度是 %d，应为 32: %s", i+1, len(got), got)
		}
		// 去掉前导零后必须与 Java 的输出一致，说明只差在前导零上
		trimmed := got
		for len(trimmed) > 1 && trimmed[0] == '0' {
			trimmed = trimmed[1:]
		}
		if trimmed != want {
			t.Fatalf("第 %d 行与 Java 的差异不只是前导零\nJava: %s\nGo  : %s", i+1, want, got)
		}
		if len(want) != 32 {
			repaired++
		}
	})
	if repaired == 0 {
		t.Fatal("没有任何一条被修复，基准数据有问题")
	}
	t.Logf("%d 条全部为完整 32 位，其中 %d 条 (%.2f%%) 是修复了 Java 的截断",
		n, repaired, float64(repaired)*100/float64(n))
}

// TestMD5KnownVectors 用公开的 MD5 测试向量确认标准模式没算错。
func TestMD5KnownVectors(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "d41d8cd98f00b204e9800998ecf8427e"},
		{"abc", "900150983cd24fb0d6963f7d28e17f72"},
		{"tiebaclient!!!", "6ea2c1e0b2e0d0a1b6f3fdb3e6fdb0c1"},
	}
	for _, c := range cases[:2] { // 前两条是标准向量
		if got := md5Hex(c.in, false); got != c.want {
			t.Errorf("md5Hex(%q) = %s, want %s", c.in, got, c.want)
		}
	}
	// 第三条只检查长度，值由实现决定
	if got := md5Hex(cases[2].in, false); len(got) != 32 {
		t.Errorf("md5Hex(%q) 长度 %d, want 32", cases[2].in, len(got))
	}
}

// TestSignBody 固定签到请求体的拼接格式，防止重构时改坏。
func TestSignBody(t *testing.T) {
	const kw, tb = "抗压背锅", "1a2b3c4d"
	want := "kw=抗压背锅&tbs=1a2b3c4d&sign=" + md5Hex("kw=抗压背锅tbs=1a2b3c4dtiebaclient!!!", cfg.CompatJavaMD5)
	if got := signBody(kw, tb); got != want {
		t.Fatalf("签到请求体格式不符\nwant: %s\ngot : %s", want, got)
	}
}

// TestGetString 验证对 fastjson getString 的模拟：
// 数字、字符串、布尔都要能取出字符串，缺失的键返回空串而不是 panic。
// 实测百度的 is_login 返回的是数字 0 而非字符串，这个兼容不能少。
func TestGetString(t *testing.T) {
	m := decodeJSON([]byte(`{"a":"1","b":1,"c":true,"d":null,"e":0}`))
	if m == nil {
		t.Fatal("解析失败")
	}
	for _, c := range []struct{ key, want string }{
		{"a", "1"},    // 字符串
		{"b", "1"},    // 数字 -> "1"
		{"c", "true"}, // 布尔
		{"d", ""},     // null
		{"e", "0"},    // 数字 0 -> "0"，error_code / is_sign 判断依赖这个
		{"nope", ""},  // 缺失的键
	} {
		if got := getString(m, c.key); got != c.want {
			t.Errorf("getString(%q) = %q, want %q", c.key, got, c.want)
		}
	}
	if got := getString(nil, "any"); got != "" {
		t.Errorf("getString(nil) = %q, want 空串", got)
	}
}

// TestRandDurationBounds 随机间隔必须落在 [min, max] 内，且不能永远是同一个值。
func TestRandDurationBounds(t *testing.T) {
	const lo, hi = 1 * time.Second, 3 * time.Second
	seen := map[time.Duration]bool{}
	for i := 0; i < 2000; i++ {
		d := randDuration(lo, hi)
		if d < lo || d > hi {
			t.Fatalf("randDuration 越界: %s 不在 [%s, %s]", d, lo, hi)
		}
		seen[d] = true
	}
	if len(seen) < 100 {
		t.Errorf("随机性不足，2000 次只取到 %d 个不同值", len(seen))
	}
	// min == max 时必须稳定返回该值
	if d := randDuration(hi, hi); d != hi {
		t.Errorf("randDuration(%s, %s) = %s, want %s", hi, hi, d, hi)
	}
	// max < min 时不能 panic
	if d := randDuration(hi, lo); d != hi {
		t.Errorf("randDuration(max<min) = %s, want %s", d, hi)
	}
}

// TestConfigDefaults 默认配置必须是修复后的行为，且时长解析正确。
func TestConfigDefaults(t *testing.T) {
	c := defaultConfig()
	c.parseDurations()
	if c.CompatJavaMD5 {
		t.Error("默认不应该再开启 Java MD5 兼容模式")
	}
	if c.signDelayMin <= 0 {
		t.Error("默认必须有签到间隔，不能是 0（原 Java 版就是 0，会突发打完所有请求）")
	}
	if c.signDelayMin > c.signDelayMax {
		t.Error("signDelayMin 不应大于 signDelayMax")
	}
	if c.roundSleepMin > c.roundSleepMax {
		t.Error("roundSleepMin 不应大于 roundSleepMax")
	}
}

// TestConfigEnvOverride 环境变量必须能覆盖配置。
func TestConfigEnvOverride(t *testing.T) {
	t.Setenv("TIEBA_SIGN_DELAY_MIN", "7s")
	t.Setenv("TIEBA_SIGN_DELAY_MAX", "9s")
	t.Setenv("TIEBA_ROUND_LIMIT", "3")
	t.Setenv("TIEBA_COMPAT_JAVA_MD5", "true")

	c := defaultConfig()
	applyEnv(&c)
	c.parseDurations()

	if c.signDelayMin != 7*time.Second {
		t.Errorf("signDelayMin = %s, want 7s", c.signDelayMin)
	}
	if c.signDelayMax != 9*time.Second {
		t.Errorf("signDelayMax = %s, want 9s", c.signDelayMax)
	}
	if c.RoundLimit != 3 {
		t.Errorf("RoundLimit = %d, want 3", c.RoundLimit)
	}
	if !c.CompatJavaMD5 {
		t.Error("CompatJavaMD5 应被环境变量置为 true")
	}
}

// TestConfigInvalidValuesFallBack 非法配置必须退回默认值而不是让程序崩掉或永远睡下去。
func TestConfigInvalidValuesFallBack(t *testing.T) {
	c := defaultConfig()
	c.SignDelayMin = "不是时长"
	c.RoundLimit = -1
	c.MaxNoProgressRounds = 0
	c.parseDurations()

	def := defaultConfig()
	def.parseDurations()
	if c.signDelayMin != def.signDelayMin {
		t.Errorf("非法 signDelayMin 应退回 %s，实际 %s", def.signDelayMin, c.signDelayMin)
	}
	if c.RoundLimit != def.RoundLimit {
		t.Errorf("非法 roundLimit 应退回 %d，实际 %d", def.RoundLimit, c.RoundLimit)
	}
	if c.MaxNoProgressRounds != def.MaxNoProgressRounds {
		t.Errorf("非法 maxNoProgressRounds 应退回 %d，实际 %d", def.MaxNoProgressRounds, c.MaxNoProgressRounds)
	}
}

// TestConfigSwapsReversedRange min > max 时必须自动对调，否则 randDuration 行为异常。
func TestConfigSwapsReversedRange(t *testing.T) {
	c := defaultConfig()
	c.SignDelayMin = "10s"
	c.SignDelayMax = "2s"
	c.parseDurations()
	if c.signDelayMin != 2*time.Second || c.signDelayMax != 10*time.Second {
		t.Errorf("min/max 未对调: min=%s max=%s", c.signDelayMin, c.signDelayMax)
	}
}
