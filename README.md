# 贴吧签到助手 · Go 版

百度贴吧自动签到，跑在 GitHub Actions 上。

这是 [srcrs/TiebaSignIn](https://github.com/srcrs/TiebaSignIn)（Java）的 Go 移植版本。

- 零第三方依赖，全部标准库
- **协议层与 Java 版逐字节一致**（有测试保证，见下），百度收到的请求完全相同
- 修复了 Java 版的 MD5 缺陷，签到成功率更高
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

调试时可以把间隔调到极小，秒出结果：

```bash
TIEBA_SIGN_DELAY_MIN=10ms TIEBA_SIGN_DELAY_MAX=20ms go run . "你的BDUSS"
```

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

共 15 个用例，其中：

- `TestMD5CompatMatchesJava` —— 拿 4000 条基准数据逐位比对兼容模式与 Java 的签名输出，
  其中 274 条（6.85%）正好覆盖了 Java 前导零被吃掉的情况
- `TestMD5FixRepairsTruncation` —— 验证修复后全部为完整 32 位，且与 Java 的差异仅在前导零
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

### 仍与 Java 版一致、尚未处理的

| 问题 | 位置 | 影响 |
|---|---|---|
| Server 酱用已停服的 `sc.ftqq.com` | `serverChanURL` | 推送发不出去，待换 Telegram |
| 吧名不做 URL 编码 | `signBody()` | 与 Java 版一致，含特殊字符的吧名可能出问题 |
| `followNum` 初值 201 | `var followNum = 201` | 拉取关注列表失败时，末尾汇总会显示"共 201 个贴吧 - 失败: 201"，属于误导性输出（此时程序会立即退出，不再空转） |

### BDUSS 失效时不会报错

目前 BDUSS 过期的话，日志里会出现 `data.like_forum 缺失（BDUSS 是否已失效？）`，
但 workflow 仍然是**绿色的成功状态**。如果不主动看日志，可能几个月都发现不了签到早就停了。
建议后续加上「拉取列表失败则以非零码退出」，让 Actions 直接标红。

## 第三阶段计划

1. 推送从 Server 酱换成 Telegram Bot
2. BDUSS 失效时让 workflow 标红，避免静默失败
3. `followNum` 初值改 0，修掉误导性的汇总输出
4. 吧名做 URL 编码

## 关于 workflow 被自动禁用

GitHub 官方机制：**公开仓库连续 60 天没有活动，`schedule` 触发的 workflow 会被自动禁用**，
仓库所有者会收到邮件通知。

本项目的 `.github/workflows/keepalive.yml` 每月推一次空提交来规避这个问题，不需要再手动去戳仓库。
不想要的话直接删掉该文件。

## 其他 workflow

- `delete-runs.yml` —— 手动触发，清空全部历史运行记录。注意 `retain_days: 0` 是**全删**。

## License

MIT，与上游 [srcrs/TiebaSignIn](https://github.com/srcrs/TiebaSignIn) 保持一致。
