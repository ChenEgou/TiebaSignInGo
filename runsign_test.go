package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTieba 是一个模拟百度贴吧接口的本地服务器，用来端到端验证 runSign 的
// 轮次与节奏逻辑，不会碰到真实的百度接口。
type fakeTieba struct {
	mu sync.Mutex
	// alwaysFail 里的贴吧永远签不上，模拟已封禁的吧
	alwaysFail map[string]bool
	// failOnce 里的贴吧第一次失败，之后成功，模拟偶发失败
	failOnce map[string]int
	// signedAt 记录每次签到请求到达的时刻，用于检查请求间隔
	signedAt []time.Time
	signCnt  int
}

func newFakeTieba(alwaysFail []string, failOnce map[string]int) *fakeTieba {
	f := &fakeTieba{alwaysFail: map[string]bool{}, failOnce: map[string]int{}}
	for _, s := range alwaysFail {
		f.alwaysFail[s] = true
	}
	for k, v := range failOnce {
		f.failOnce[k] = v
	}
	return f
}

func (f *fakeTieba) start(t *testing.T, forums []string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/tbs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"is_login":1,"tbs":"faketbs123"}`)
	})

	mux.HandleFunc("/like", func(w http.ResponseWriter, r *http.Request) {
		var items []string
		for _, name := range forums {
			items = append(items, fmt.Sprintf(`{"forum_name":%q,"is_sign":0}`, name))
		}
		fmt.Fprintf(w, `{"data":{"like_forum":[%s]}}`, strings.Join(items, ","))
	})

	mux.HandleFunc("/sign", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		kw := r.FormValue("kw")

		f.mu.Lock()
		f.signedAt = append(f.signedAt, time.Now())
		f.signCnt++
		fail := f.alwaysFail[kw]
		if n := f.failOnce[kw]; n > 0 {
			f.failOnce[kw] = n - 1
			fail = true
		}
		f.mu.Unlock()

		if fail {
			fmt.Fprint(w, `{"error_code":"160002","error_msg":"签到失败"}`)
			return
		}
		fmt.Fprint(w, `{"error_code":"0"}`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// 把接口指向假服务器，测试结束后自动还原
	oldTbs, oldLike, oldSign := tbsURL, likeURL, signURL
	tbsURL, likeURL, signURL = srv.URL+"/tbs", srv.URL+"/like", srv.URL+"/sign"
	t.Cleanup(func() { tbsURL, likeURL, signURL = oldTbs, oldLike, oldSign })

	return srv
}

// resetState 清空全局状态，让每个用例互不干扰。
func resetState(t *testing.T, c Config) {
	t.Helper()
	old := cfg
	cfg = c
	cfg.parseDurations()
	client = &http.Client{Timeout: cfg.httpTimeout}
	bduss, tbs, follow, success, followNum, followOK = "fake", "", nil, nil, 0, false
	t.Cleanup(func() {
		cfg = old
		follow, success, followNum, followOK = nil, nil, 0, false
	})
}

func fastConfig() Config {
	c := defaultConfig()
	c.SignDelayMin = "1ms"
	c.SignDelayMax = "3ms"
	c.RoundSleepMin = "10ms"
	c.RoundSleepMax = "20ms"
	return c
}

// TestRunSignAllSucceedInOneRound 全部签到成功时必须只跑 1 轮、不做轮间等待。
// 这正是 Java 版做不到的地方：它即使全部成功也会先睡满 5 分钟。
func TestRunSignAllSucceedInOneRound(t *testing.T) {
	forums := []string{"抗压背锅", "孙笑川", "李毅"}
	fake := newFakeTieba(nil, nil)
	resetState(t, fastConfig())
	fake.start(t, forums)

	getTbs()
	getFollow()

	start := time.Now()
	runSign()
	elapsed := time.Since(start)

	if len(success) != len(forums) {
		t.Fatalf("成功 %d 个，应为 %d 个", len(success), len(forums))
	}
	if fake.signCnt != len(forums) {
		t.Errorf("发出 %d 个签到请求，应为 %d 个（不应重复签）", fake.signCnt, len(forums))
	}
	// 3 个贴吧、2 个间隔，每个 1-3ms，不应触发任何轮间等待(10ms+)
	if elapsed > 100*time.Millisecond {
		t.Errorf("全部成功却耗时 %s，疑似执行了多余的轮间等待", elapsed)
	}
}

// TestRunSignRetriesThenSucceeds 偶发失败的贴吧应该在后续轮次里被补签上。
func TestRunSignRetriesThenSucceeds(t *testing.T) {
	forums := []string{"抗压背锅", "孙笑川", "李毅"}
	fake := newFakeTieba(nil, map[string]int{"孙笑川": 1}) // 第一次失败，第二次成功
	resetState(t, fastConfig())
	fake.start(t, forums)

	getTbs()
	getFollow()
	runSign()

	if len(success) != len(forums) {
		t.Fatalf("成功 %d 个，应为 %d 个（重试后应全部签上）", len(success), len(forums))
	}
	// 3 个第一轮 + 1 个第二轮重试
	if fake.signCnt != 4 {
		t.Errorf("发出 %d 个签到请求，应为 4 个", fake.signCnt)
	}
}

// TestRunSignStopsAfterNoProgress 永远签不上的贴吧不应该把 5 轮跑满。
// Java 版在这种情况下会跑满 5 轮并睡满 25 分钟，这正是"每天 27 分钟"的根因。
func TestRunSignStopsAfterNoProgress(t *testing.T) {
	forums := []string{"抗压背锅", "已封禁的吧"}
	fake := newFakeTieba([]string{"已封禁的吧"}, nil)
	c := fastConfig()
	c.RoundLimit = 5
	c.MaxNoProgressRounds = 2
	resetState(t, c)
	fake.start(t, forums)

	getTbs()
	getFollow()
	runSign()

	if len(success) != 1 {
		t.Fatalf("成功 %d 个，应为 1 个", len(success))
	}
	// 第 1 轮: 2 个请求(1成功1失败) / 第 2 轮: 1 个失败 / 第 3 轮: 1 个失败 -> 连续 2 轮无进展，停
	// 总计 4 个请求，绝不该达到跑满 5 轮的 6 个
	if fake.signCnt != 4 {
		t.Errorf("发出 %d 个签到请求，应为 4 个（跑满 5 轮会是 6 个）", fake.signCnt)
	}
}

// TestRunSignRespectsDelay 每两个签到请求之间必须真的有间隔，
// 不能像 Java 版那样在几秒内突发打完。
func TestRunSignRespectsDelay(t *testing.T) {
	forums := []string{"吧A", "吧B", "吧C", "吧D", "吧E"}
	fake := newFakeTieba(nil, nil)
	c := fastConfig()
	c.SignDelayMin = "20ms"
	c.SignDelayMax = "40ms"
	resetState(t, c)
	fake.start(t, forums)

	getTbs()
	getFollow()
	runSign()

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.signedAt) != len(forums) {
		t.Fatalf("记录到 %d 个请求，应为 %d 个", len(fake.signedAt), len(forums))
	}
	for i := 1; i < len(fake.signedAt); i++ {
		gap := fake.signedAt[i].Sub(fake.signedAt[i-1])
		if gap < 15*time.Millisecond {
			t.Errorf("第 %d 与第 %d 个请求间隔仅 %s，小于配置的最小间隔 20ms", i, i+1, gap)
		}
	}
}

// TestRunSignSkipsAlreadySigned 已经签过的贴吧不应该再发请求。
func TestRunSignSkipsAlreadySigned(t *testing.T) {
	fake := newFakeTieba(nil, nil)
	resetState(t, fastConfig())

	mux := http.NewServeMux()
	mux.HandleFunc("/tbs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"is_login":1,"tbs":"faketbs123"}`)
	})
	// 3 个吧里有 2 个今天已经签过
	mux.HandleFunc("/like", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"like_forum":[
			{"forum_name":"已签A","is_sign":1},
			{"forum_name":"已签B","is_sign":1},
			{"forum_name":"待签C","is_sign":0}]}}`)
	})
	mux.HandleFunc("/sign", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.signCnt++
		fake.mu.Unlock()
		fmt.Fprint(w, `{"error_code":"0"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	oldTbs, oldLike, oldSign := tbsURL, likeURL, signURL
	tbsURL, likeURL, signURL = srv.URL+"/tbs", srv.URL+"/like", srv.URL+"/sign"
	t.Cleanup(func() { tbsURL, likeURL, signURL = oldTbs, oldLike, oldSign })

	getTbs()
	getFollow()
	runSign()

	if fake.signCnt != 1 {
		t.Errorf("发出 %d 个签到请求，应为 1 个（另外 2 个今天已签）", fake.signCnt)
	}
	if len(success) != 3 {
		t.Errorf("成功计数 %d，应为 3（含已签的 2 个）", len(success))
	}
}
