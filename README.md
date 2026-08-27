# 贴吧签到助手 · Go 版

百度贴吧自动签到，跑在 GitHub Actions 上。

这是 [srcrs/TiebaSignIn](https://github.com/srcrs/TiebaSignIn)（Java）的 Go 移植版本。

- 零第三方依赖，全部标准库
- **协议层与 Java 版逐字节一致**（有测试保证，见下），百度收到的请求完全相同
- 修复了 Java 版的 MD5 缺陷，签到成功率更高
- 结果推送到 Telegram，BDUSS 失效会主动告警
- 请求节奏可配置、带随机抖动，比 Java 版更平缓
- 单次运行约 1 分钟，Java 版固定 27 分钟

## 使用

### 1. 拿到 BDUSS

浏览器登录贴吧 → `F12` → Application / 存储 → Cookie → 找到 `BDUSS`，复制它的 Value。

> BDUSS 等同于你的贴吧登录凭证，别发给任何人，也别提交进代码里。

### 2. 配置 Secrets

仓库 → Settings → Secrets and variables → Actions → New repository secret：

| Name | 必填 | Value |
|---|---|---|
| `BDUSS` | 是 | 上一步复制的值 |
| `TG_BOT_TOKEN` | 否 | Telegram Bot Token，见下 |
| `TG_CHAT_ID` | 否 | Telegram Chat ID，见下 |

两个 TG 变量要么都填、要么都不填；只填一个会告警并跳过推送。

### 3. 开启 Actions

Fork 或新建的仓库默认禁用 Actions，需要在 Actions 页面手动启用。

### 4. 手动触发一次验证

Actions → `tiebaSign` → Run workflow。确认日志里出现 `获取tbs成功` 和各个贴吧的签到结果。

之后每天北京时间 **08:05** 自动执行（`cron: '05 00 * * *'` 是 UTC 时间）。

## 本地运行

```bash
BDUSS="你的BDUSS" go run .
```

也可以作为第一个命令行参数传入（`go run . "你的BDUSS"`），
但环境变量更安全 —— 命令行参数在进程列表里是可见的。

调试时可以把间隔调到极小，秒出结果：

```bash
BDUSS="你的BDUSS" TIEBA_SIGN_DELAY_MIN=10ms TIEBA_SIGN_DELAY_MAX=20ms go run .
```

## Telegram 推送

### 建 bot

1. Telegram 里搜 `@BotFather`，点开，发 `/newbot`
2. 按提示填显示名，再填 username（**必须以 `bot` 结尾且全局唯一**）
3. 它会返回一串 Token，形如 `123456789:AAHxxxxxxxx`

### 拿 Chat ID

4. 搜到自己刚建的 bot，点进去，**随便发一条消息**
   （不能跳过：你没先跟 bot 说过话，它就无法主动给你发消息）
5. 浏览器打开 `https://api.telegram.org/bot<TOKEN>/getUpdates`
6. 在返回的 JSON 里找 `"chat":{"id":123456789`，这个数字就是 Chat ID

> Token 等同于 bot 的密码。第 5 步的网址里带着 Token，
> 这个网址和它的返回内容都不要外传。
>
> **不要把 Token 直接打在命令行上** —— 它会被写进 shell 历史记录，
> 截图终端时也会连带泄漏。本地调试用 `.env`：
>
> ```bash
> cp .env.example .env   # 填入真实值，.env 已被 gitignore
> set -a && source .env && set +a && go run .
> ```
>
> 万一泄漏了：BotFather 发 `/revoke`，选中 bot，旧 Token 立刻作废并换发新的。

### 填进 Secrets

`TG_BOT_TOKEN` 和 `TG_CHAT_ID` 两个都填上即可生效。

### 会收到什么

正常跑完是一条汇总（成功/失败数、耗时、签不上的吧名，最多列 30 个）。

三种情况的措辞不同：

| 情况 | 开头 |
|---|---|
| 全部签到成功 | ✅ 贴吧签到完成 |
| 部分吧没签上 | ⚠️ 贴吧签到完成（有失败） |
| 一个都没签上 | 🚨 贴吧签到全部失败 |
| 拉不到关注列表（多半是 BDUSS 失效） | 🚨 贴吧签到失败 + 处理步骤 |

后两种同时会让 workflow 标红。

实现细节：

- 凭证只从环境变量读，不走命令行参数
- 万一网络出错，Go 的报错会带上完整请求 URL（内含 Token），
  代码会先脱敏成 `<TOKEN>` 再写日志，不会泄漏到 Actions 日志
- 消息超过 Telegram 的 4096 字符上限会自动截断
- 刻意不使用 Markdown/HTML 格式：吧名里可能含 `_` `*` `[` 等字符，
  会因转义问题导致整条消息被 Telegram 拒收

## 配置

节奏参数都在 `config.json` 里，改完提交即可生效，不需要动代码：

```json
{
  "compatJavaMD5": false,
  "roundLimit": 5,
  "maxNoProgressRounds": 2,
  "signDelayMin": "1s",
  "signDelayMax": "3s",
  "roundSleepMin": "30s",
  "roundSleepMax": "90s",
  "startupJitterMax": "0s",
  "httpTimeout": "30s"
}
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `compatJavaMD5` | `false` | 改 `true` 会退回 Java 版有缺陷的 MD5。只在对拍时才需要 |
| `roundLimit` | `5` | 最多重试几轮 |
| `maxNoProgressRounds` | `2` | 连续几轮零新增就提前收工。有些吧（如已封禁的）永远签不上，重试无用 |
| `signDelayMin` / `Max` | `1s` / `3s` | **每两个贴吧之间的随机间隔**。Java 版这里是 0，会在几秒内把所有请求打完 |
| `roundSleepMin` / `Max` | `30s` / `90s` | 轮与轮之间的随机等待。Java 版固定 5 分钟 |
| `startupJitterMax` | `0s` | 启动前的随机延迟上限，用来打散每天固定的执行时刻。GitHub 的 cron 本身就常延迟几分钟到几十分钟，已有天然抖动，所以默认关闭 |
| `httpTimeout` | `30s` | 单个请求超时。Java 版没有超时，理论上可以永久挂住 |
| `formEncoding` | `minimal` | 请求体里吧名的编码方式，见下 |

`formEncoding` 三档：

| 值 | 行为 |
|---|---|
| `none` | 原始字节直接拼接，与 Java 版逐字节一致。像 `c++` 这种含 `+` 的吧名会被服务器解析成空格而签到失败 |
| `minimal` | **默认**。只转义会破坏表单解析的字符（`&` `+` `%` `#` 空格、控制字符），中文和字母数字原样保留 |
| `full` | 标准 `url.QueryEscape`，中文也变成 `%XX`。这是百度官方客户端的做法，但与现有可用行为差异最大 |

**签名始终基于原始吧名**，`formEncoding` 只影响请求体，不影响签名算法。

每个字段都能用环境变量覆盖（优先级最高），方便在 workflow 里临时调整而不改文件：

| 环境变量 | 对应字段 |
|---|---|
| `TIEBA_COMPAT_JAVA_MD5` | `compatJavaMD5` |
| `TIEBA_ROUND_LIMIT` | `roundLimit` |
| `TIEBA_MAX_NO_PROGRESS_ROUNDS` | `maxNoProgressRounds` |
| `TIEBA_SIGN_DELAY_MIN` / `_MAX` | `signDelayMin` / `Max` |
| `TIEBA_ROUND_SLEEP_MIN` / `_MAX` | `roundSleepMin` / `Max` |
| `TIEBA_STARTUP_JITTER_MAX` | `startupJitterMax` |
| `TIEBA_HTTP_TIMEOUT` | `httpTimeout` |
| `TIEBA_FORM_ENCODING` | `formEncoding` |
| `TIEBA_CONFIG` | 配置文件路径，默认 `config.json` |

非法值会打警告并退回默认值；`min > max` 会自动对调。配置文件不存在也能正常跑，直接用默认值。

## 运行时长

签到顺利时基本等于 `贴吧数 × 平均间隔`（默认平均 2 秒）：

| 关注贴吧数 | 预计耗时 |
|---|---|
| 30 | 约 1 分钟（实测 54 秒） |
| 50 | 约 1.7 分钟 |
| 100 | 约 3.3 分钟 |
| 200 | 约 6.7 分钟 |

想更快就调小 `signDelayMin` / `signDelayMax`，想更保守就调大。

跑一遍实测：

```bash
TIEBA_TIMING_TEST=1 FORUMS=50 go test -run TestTimingRealistic -v -timeout 20m
```

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

共 32 个用例，其中：

- `TestMD5CompatMatchesJava` —— 拿 4000 条基准数据逐位比对兼容模式与 Java 的签名输出，
  其中 274 条（6.85%）正好覆盖了 Java 前导零被吃掉的情况
- `TestMD5FixRepairsTruncation` —— 验证修复后全部为完整 32 位，且与 Java 的差异仅在前导零
- `TestFormEscapeMinimal*` —— 守住"正常吧名编码前后字节相同"，以及 `c++` 这类会挂的名字确实被修好
- `TestSignIsAlwaysOverRawName` —— 三种编码模式下，签名都必须基于原始吧名
- `TestNotifier*` —— 用本地假服务器验证 Telegram 请求格式、Token 脱敏、超长截断、未配置时不发请求
- `TestExitCode` —— 守住"部分贴吧失败不标红"，避免告警信号被日常噪音淹没
- `TestRunSign*` —— 起一个本地假服务器模拟百度接口，端到端验证轮次逻辑：
  全部成功只跑 1 轮、偶发失败会重试补签、永远签不上的吧 2 轮后收工、
  请求间隔真的生效、今天已签过的吧不会重复请求

基准数据由 `testdata/gen_golden.py` 生成，复现的是 Java
`new BigInteger(1, digest).toString(16)` 的语义。

## 与 Java 版的行为差异

请求内容（URL、请求头、请求体、签名算法）**完全一致**，差异只在两处：

### 1. MD5 不再截断（修复）

Java 版用 `new BigInteger(1, digest).toString(16)`，会吃掉哈希开头的 0，
约 **6.2%** 的签名因此不足 32 位，被百度判为签名错误 —— 对应的贴吧当轮必定签不上。

本项目输出标准的 32 位十六进制。`TestMD5FixRepairsTruncation` 验证了修复后的输出
与 Java 的差异**仅在于前导零**，签名算法本身没有任何改动。

### 2. 请求节奏（改进）

| | Java 版 | 本项目 |
|---|---|---|
| 贴吧之间的间隔 | **0 秒，突发打完** | 1~3 秒随机 |
| 轮间等待 | 固定 5 分钟 | 30~90 秒随机 |
| 全部签完后 | 仍会睡满 5 分钟 | 立即退出 |
| 最后一轮结束后 | 白白多睡 5 分钟 | 不等待 |
| 连续多轮零进展 | 照样跑满 5 轮 | 2 轮后提前收工 |
| 典型总耗时 | **27 分钟** | **约 1 分钟** |

注意方向：请求**密度反而降低了**。Java 版会在几秒内朝百度连发几十上百个请求，
然后干等 5 分钟；本项目是匀速加随机抖动，总时长虽然短得多，但瞬时请求频率低了一个数量级。

### 3. 吧名表单转义（修复）

Java 版把吧名的原始字节直接拼进请求体。像 **`c++`吧**（真实存在）
这种含 `+` 的名字，`+` 会被服务器解析成空格，签名对不上，必定失败。

默认的 `minimal` 模式只转义 `&` `+` `%` `#` 空格和控制字符。
对正常吧名（中文、字母、数字）编码前后**字节完全相同**，
与 Java 版没有任何差异，因此没有回归风险。

`TestFormEscapeMinimalLeavesNormalNamesUntouched` 专门守住这条保证。

> 百度是否会对请求体做 URL 解码无法离线验证。
> 如果改完发现签到反而挂了，把 `formEncoding` 改回 `"none"` 即可恢复 Java 版行为。

### 4. 推送换成 Telegram

Java 版用的 `sc.ftqq.com`（Server 酱旧版）早已停服，推送实际发不出去。
本项目改用 Telegram Bot API，并新增了 BDUSS 失效告警。

### 5. 失败时 workflow 标红

Java 版无论出什么事都返回 0，BDUSS 过期了 Actions 页面照样显示绿色成功。

本项目在两种情况下以非零码退出，让 Actions 直接标红：

| 情况 | 说明 |
|---|---|
| 拉不到关注列表 | BDUSS 失效或网络不通，一个吧都没签 |
| 拉到了列表但一个都没签成功 | 通常是 tbs 获取失败导致全军覆没 |

**部分贴吧失败不会标红。** 有些吧（已封禁的、有特殊规则的）本来就永远签不上，
天天标红会让这个信号彻底失去意义 —— 那种情况看 Telegram 里的失败列表即可。

退出码在推送发出【之后】才生效，保证告警一定送得出去。

## 关于 workflow 被自动禁用

GitHub 官方机制：**公开仓库连续 60 天没有活动，`schedule` 触发的 workflow 会被自动禁用**，
仓库所有者会收到邮件通知。

本项目的 `.github/workflows/keepalive.yml` 每月推一次空提交来规避这个问题，不需要再手动去戳仓库。
不想要的话直接删掉该文件。

## 其他 workflow

- `delete-runs.yml` —— 手动触发，清空全部历史运行记录。注意 `retain_days: 0` 是**全删**。

## License

MIT，与上游 [srcrs/TiebaSignIn](https://github.com/srcrs/TiebaSignIn) 保持一致。
