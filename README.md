# 贴吧签到助手 · Go 版

百度贴吧自动签到，跑在 GitHub Actions 上。

这是 [srcrs/TiebaSignIn](https://github.com/srcrs/TiebaSignIn)（Java）的 Go 移植版本。
**当前为 v1，与 Java 版逻辑严格 1:1**，包括原版的若干缺陷 —— 目的是先确认行为完全一致，再分阶段优化。

- 零第三方依赖，全部标准库
- 单文件 `main.go`
- 协议层与 Java 版逐位一致（有测试保证，见下）

## 使用

### 1. 拿到 BDUSS

浏览器登录贴吧 → `F12` → Application / 存储 → Cookie → 找到 `BDUSS`，复制它的 Value。

> BDUSS 等同于你的贴吧登录凭证，别发给任何人，也别提交进代码里。

### 2. 配置 Secrets

仓库 → Settings → Secrets and variables → Actions → New repository secret：

| Name | 必填 | Value |
|---|---|---|
| `BDUSS` | 是 | 上一步复制的值 |
| `SCKEY` | 否 | Server 酱的 key，不填就跳过推送 |

### 3. 开启 Actions

Fork 或新建的仓库默认禁用 Actions，需要在 Actions 页面手动启用。

### 4. 手动触发一次验证

Actions → `tiebaSign` → Run workflow。确认日志里出现 `获取tbs成功` 和各个贴吧的签到结果。

之后每天北京时间 **08:05** 自动执行（`cron: '05 00 * * *'` 是 UTC 时间）。

## 本地运行

```bash
go run . "你的BDUSS"
go run . "你的BDUSS" "你的SCKEY"
```

调试时可以缩短每轮之间的等待，避免干等 25 分钟：

```bash
TIEBA_ROUND_SLEEP=2s go run . "你的BDUSS"
```

`TIEBA_ROUND_SLEEP` 只影响本地调试，workflow 里不设置它，线上等待时间与 Java 版一致。

## 与 Java 版的对应关系

| Java | Go |
|---|---|
| `top.srcrs.Run.getTbs` | `getTbs()` |
| `top.srcrs.Run.getFollow` | `getFollow()` |
| `top.srcrs.Run.runSign` | `runSign()` |
| `top.srcrs.Run.send` | `send()` |
| `top.srcrs.util.Request` | `requestGet()` / `requestPost()` |
| `top.srcrs.util.Encryption` | `encodeMD5()` |
| `top.srcrs.domain.Cookie` | 全局变量 `bduss` + `cookie()` |
| `httpclient` / `fastjson` / `logback` | `net/http` / `encoding/json` / `fmt` |

请求层照搬了 Java 版的全部细节，包括：

- 完全相同的 URL、User-Agent、`connection` / `charset` 等非标准请求头
- POST 请求硬编码 `Host: tieba.baidu.com`（与实际域名 `c.tieba.baidu.com` 不一致，是原版行为）
- 请求体 `kw=<吧名>&tbs=<tbs>&sign=<md5>` 不做 URL 编码
- 签名算法 `md5("kw=" + 吧名 + "tbs=" + tbs + "tiebaclient!!!")`

## 验证

```bash
go test -v ./...
```

`TestMD5MatchesJava` 拿 4000 条基准数据逐位比对 Go 与 Java 的签名输出，
其中 274 条（6.85%）正好覆盖了 Java 前导零被吃掉的情况。基准数据由
`testdata/gen_golden.py` 生成，复现的是 Java `new BigInteger(1, digest).toString(16)` 的语义。

## v1 已知问题（**故意保留**，等第二阶段处理）

这些都是从 Java 版原样复刻过来的，不是移植引入的：

| # | 问题 | 位置 | 后果 |
|---|---|---|---|
| 1 | MD5 前导零被吃掉 | `compatJavaMD5 = true` | 约 6.2% 的签名残缺，对应贴吧签到失败 |
| 2 | 每轮固定等 5 分钟，且最后一轮白等 | `roundSleep` | 只要有一个吧没签上就跑满 25 分钟 |
| 3 | `followNum` 初值 201 | `var followNum = 201` | 拉取关注列表失败时会空跑 5 轮 |
| 4 | Server 酱用已停服的 `sc.ftqq.com` | `serverChanURL` | 推送实际发不出去 |
| 5 | 吧名不做 URL 编码 | `runSign()` | 含特殊字符的吧名可能出问题 |

问题 1、2 是「每天跑 27 分钟」的根因：MD5 有缺陷 → 必有贴吧失败 → 循环无法提前退出 → 每轮都睡满。

## 第二阶段计划

1. `compatJavaMD5` 改 `false` —— 修掉签名截断，签到成功率上升
2. `roundSleep` 调短 + 全部签完立即退出 + 去掉最后一轮的无用等待 —— 运行时间从 27 分钟降到 1 分钟内
3. 推送从 Server 酱换成 Telegram Bot
4. `followNum` 初值改 0，拉取失败时直接退出而不是空跑

前两项只需要改常量，不需要动逻辑。

## 关于 workflow 被自动禁用

GitHub 官方机制：**公开仓库连续 60 天没有活动，`schedule` 触发的 workflow 会被自动禁用**，
仓库所有者会收到邮件通知。

本项目的 `.github/workflows/keepalive.yml` 每月推一次空提交来规避这个问题，不需要再手动去戳仓库。
不想要的话直接删掉该文件。

## 其他 workflow

- `delete-runs.yml` —— 手动触发，清空全部历史运行记录。注意 `retain_days: 0` 是**全删**。

## License

MIT，与上游 [srcrs/TiebaSignIn](https://github.com/srcrs/TiebaSignIn) 保持一致。
