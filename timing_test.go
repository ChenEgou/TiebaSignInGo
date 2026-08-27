package main

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestTimingRealistic 用真实的默认节奏配置跑一遍，测量实际耗时。
// 因为会真的等待，默认跳过；需要时用环境变量打开：
//
//	TIEBA_TIMING_TEST=1 go test -run TestTimingRealistic -v -timeout 20m
//
// FORUMS 可以指定贴吧数量，默认 30。
func TestTimingRealistic(t *testing.T) {
	if os.Getenv("TIEBA_TIMING_TEST") == "" {
		t.Skip("设置 TIEBA_TIMING_TEST=1 运行本用例")
	}

	n := 30
	if v := os.Getenv("FORUMS"); v != "" {
		fmt.Sscanf(v, "%d", &n)
	}

	forums := make([]string, n)
	for i := range forums {
		forums[i] = fmt.Sprintf("测试吧%d", i)
	}

	fake := newFakeTieba(nil, nil)
	resetState(t, defaultConfig()) // 真实默认值：间隔 1~3s
	fake.start(t, forums)

	start := time.Now()
	getTbs()
	getFollow()
	runSign()
	elapsed := time.Since(start)

	if len(success) != n {
		t.Fatalf("成功 %d 个，应为 %d 个", len(success), n)
	}
	t.Logf("%d 个贴吧全部签到成功，实际耗时 %s（平均每个 %s）",
		n, elapsed.Round(time.Second), (elapsed / time.Duration(n)).Round(time.Millisecond))
	t.Logf("对照：Java 版同样场景固定耗时约 25 分钟（5 轮 × 5 分钟等待）")
}
