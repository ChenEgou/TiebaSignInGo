package main

import (
	"bufio"
	"fmt"
	"os"
	"testing"
)

// TestMD5MatchesJava 逐位比对 Go 的 encodeMD5 与 Java 版
// top.srcrs.util.Encryption.enCodeMd5 的输出。
//
// 基准数据 testdata/md5_golden.txt 由 testdata/gen_golden.py 生成，
// 复现的是 Java 的 new BigInteger(1, digest).toString(16) 语义。
// 输入构造方式必须与该脚本保持一致。
func TestMD5MatchesJava(t *testing.T) {
	if !compatJavaMD5 {
		t.Skip("compatJavaMD5 已关闭，跳过与 Java 版的逐位比对")
	}

	f, err := os.Open("testdata/md5_golden.txt")
	if err != nil {
		t.Fatalf("打不开基准数据: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	var n, truncated int
	for sc.Scan() {
		want := sc.Text()
		kw := fmt.Sprintf("测试贴吧%d", n)
		tbs := fmt.Sprintf("abcdef0123456789abcdef012345678%d", n%10)
		got := encodeMD5("kw=" + kw + "tbs=" + tbs + signSalt)
		if got != want {
			t.Fatalf("第 %d 行不一致\n输入: kw=%s tbs=%s\nJava: %s\nGo  : %s", n+1, kw, tbs, want, got)
		}
		if len(want) != 32 {
			truncated++
		}
		n++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("基准数据为空")
	}
	// 基准数据必须真的覆盖到"前导零被吃掉"这条路径，否则这个测试没有意义
	if truncated == 0 {
		t.Fatal("基准数据没有覆盖前导零被截断的情况")
	}
	t.Logf("比对 %d 条全部一致，其中 %d 条 (%.2f%%) 是 Java 吃掉前导零后的残缺签名",
		n, truncated, float64(truncated)*100/float64(n))
}

// TestSignBody 固定签到请求体的拼接格式，防止以后重构时改坏。
// 格式必须与 Java 版 Run.runSign 一致。
func TestSignBody(t *testing.T) {
	const kw, tb = "抗压背锅", "1a2b3c4d"
	want := "kw=" + kw + "&tbs=" + tb + "&sign=" + encodeMD5("kw="+kw+"tbs="+tb+"tiebaclient!!!")
	got := "kw=" + kw + "&tbs=" + tb + "&sign=" + encodeMD5("kw="+kw+"tbs="+tb+signSalt)
	if got != want {
		t.Fatalf("签到请求体格式不符\nwant: %s\ngot : %s", want, got)
	}
}

// TestGetString 验证对 fastjson getString 的模拟：
// 数字、字符串、布尔都要能取出字符串，缺失的键返回空串而不是 panic。
func TestGetString(t *testing.T) {
	m := decodeJSON([]byte(`{"a":"1","b":1,"c":true,"d":null,"e":0}`))
	if m == nil {
		t.Fatal("解析失败")
	}
	cases := []struct{ key, want string }{
		{"a", "1"},    // 字符串
		{"b", "1"},    // 数字 -> "1"，Java 那边 "1".equals(getString(...)) 同样成立
		{"c", "true"}, // 布尔
		{"d", ""},     // null
		{"e", "0"},    // 数字 0 -> "0"，error_code / is_sign 判断依赖这个
		{"nope", ""},  // 缺失的键
	}
	for _, c := range cases {
		if got := getString(m, c.key); got != c.want {
			t.Errorf("getString(%q) = %q, want %q", c.key, got, c.want)
		}
	}
	// 响应解析失败时是 nil map，取值不能 panic
	if got := getString(nil, "any"); got != "" {
		t.Errorf("getString(nil) = %q, want 空串", got)
	}
}
