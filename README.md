# 贴吧签到助手 · Go 版

百度贴吧自动签到，跑在 GitHub Actions 上。[srcrs/TiebaSignIn](https://github.com/srcrs/TiebaSignIn)（Java）的 Go 移植版。

零第三方依赖；协议层与 Java 版逐字节一致（有测试保证）；修掉了 Java 版的 MD5 截断缺陷；结果推送 Telegram。

## 使用

1. **拿 BDUSS**：浏览器登录贴吧 → F12 → Application → Cookie → 复制 `BDUSS` 的值
2. **配 Secrets**：Settings → Secrets and variables → Actions

   | Name | 必填 | 说明 |
   |---|---|---|
   | `BDUSS` | 是 | 上一步的值 |
   | `TG_BOT_TOKEN` | 否 | Telegram Bot Token |
   | `TG_CHAT_ID` | 否 | Telegram Chat ID |

   两个 TG 变量要么都填、要么都不填。

3. **验证**：Actions → `tiebaSign` → Run workflow

定时执行：每天北京时间 **08:37** 和 **13:23** 各一次。

排两个时段是因为 GitHub 的 cron 是 best-effort —— 高负载时会静默丢弃调度，
不留任何记录（无 run、无报错、无通知）。两个槽位互为兜底；已签到的吧会被跳过，
重复执行幂等。

## 本地运行

```bash
BDUSS="你的BDUSS" go run .
```

凭证走环境变量，别用命令行参数 —— 进程列表里可见，也会进 shell 历史。
本地调试用 `.env`：

```bash
cp .env.example .env   # 填真实值，.env 已 gitignore
set -a && source .env && set +a && go run .
```

## Telegram 推送

`@BotFather` 建 bot 拿 Token；给 bot 发条消息后访问
`https://api.telegram.org/bot<TOKEN>/getUpdates` 取 `chat.id`。两个都填进 Secrets 即生效。

正常跑完收到一条汇总：

```
✅ 贴吧签到完成

共 84 个吧　成功 84　失败 0
本次新签 84 个，获得 672 经验
耗时 3m2s

经验最多：
· xxx吧 +12
```

「本次新签」只算这次运行签上的，今天之前已签过的不计入 —— 那部分经验不是这次挣的。
失败的吧名列在推送里（最多 30 个）。

| 情况 | 开头 | workflow |
|---|---|---|
| 全部成功 | ✅ 贴吧签到完成 | 绿 |
| 部分失败 | ⚠️ 贴吧签到完成（有失败） | 绿 |
| 一个都没签上 | 🚨 贴吧签到全部失败 | 红 |
| 拉不到关注列表 | 🚨 贴吧签到失败 + 处理步骤 | 红 |

部分失败不标红 —— 有些吧（已封禁的、有特殊规则的）本来就永远签不上，
天天标红会让这个信号彻底失去意义。退出码在推送发出**之后**才生效，保证告警送得出去。

> **日志里不打吧名**，只有计数和错误码；吧名只出现在 Telegram 推送里。
> 仓库是公开的，Actions 日志任何人可见。

## 配置

`config.json`，改完提交即可生效：

| 字段 | 默认 | 说明 |
|---|---|---|
| `compatJavaMD5` | `false` | `true` 退回 Java 版有缺陷的 MD5，仅对拍用 |
| `roundLimit` | `5` | 最多重试几轮 |
| `maxNoProgressRounds` | `2` | 连续几轮零新增就提前收工 |
| `signDelayMin` / `Max` | `1s` / `3s` | 每两个吧之间的随机间隔 |
| `roundSleepMin` / `Max` | `30s` / `90s` | 轮间随机等待 |
| `startupJitterMax` | `0s` | 启动前随机延迟上限 |
| `httpTimeout` | `30s` | 单请求超时 |
| `formEncoding` | `minimal` | 请求体吧名编码，见下 |
| `ignoreForums` | `["贴吧热议"]` | 跳过且不计入统计的条目，见下 |
| `logSignResponse` | `false` | 打印签到原始响应，排查用 |

每项都能用环境变量覆盖（优先级更高），命名为 `TIEBA_` + 字段名大写下划线，
例如 `TIEBA_SIGN_DELAY_MIN`、`TIEBA_ROUND_LIMIT`。`TIEBA_CONFIG` 指定配置文件路径。
`ignoreForums` 是数组，只能在 JSON 里改。

非法值告警并退回默认；`min > max` 自动对调；配置文件不存在也能跑。

耗时约等于 `贴吧数 × 平均间隔`（默认平均 2 秒），100 个吧约 3.3 分钟。

### 忽略名单

关注列表里有些条目不是真正的贴吧（如「贴吧热议」这种聚合项），签到接口恒返回
`error_code=300004`，永远签不上。放进 `ignoreForums` 后完全退出统计 —— 不发请求、
不计入总数，这样 ⚠️ 出现时才代表真有问题。

### formEncoding

| 值 | 行为 |
|---|---|
| `none` | 原始字节直拼，与 Java 版一致。`c++` 这类含 `+` 的吧名会失败 |
| `minimal` | 默认。只转义 `&` `+` `%` `#` 空格和控制字符，中文原样保留 |
| `full` | 标准 `url.QueryEscape`，中文也变 `%XX` |

签名**始终基于原始吧名**，`formEncoding` 只影响请求体。改完发现签到反而挂了就改回 `none`。

## 与 Java 版的差异

请求内容（URL、请求头、请求体、签名算法）完全一致，差异五处：

- **MD5 不再截断** —— Java 用 `BigInteger.toString(16)` 会吃掉前导零，约 6.2% 的签名不足 32 位，对应贴吧必定签不上
- **请求节奏** —— Java 是 0 间隔突发打完再干等 5 分钟（总耗时 27 分钟）；本项目匀速加随机抖动，瞬时频率低一个数量级，总耗时约 1 分钟
- **吧名表单转义** —— 修掉 `c++` 这类含 `+` 的吧名必失败的问题
- **推送换 Telegram** —— Java 用的 Server 酱旧版已停服；另加 BDUSS 失效告警
- **失败时标红** —— Java 无论出什么事都返回 0，BDUSS 过期照样显示绿色成功

## 测试

```bash
go test ./...
```

覆盖 MD5 兼容性对拍（4000 条基准数据）、表单转义、签名一致性、Telegram 请求格式与
Token 脱敏、退出码语义、轮次逻辑端到端。基准数据由 `testdata/gen_golden.py` 生成。

## 其他 workflow

- `keepalive.yml` —— 每月推一次空提交。公开仓库连续 60 天无活动会被自动禁用 schedule，用这个规避
- `delete-runs.yml` —— 每月 1 号自动清理运行记录，保留 7 天内且至少最近 5 条

## License

MIT，与上游 [srcrs/TiebaSignIn](https://github.com/srcrs/TiebaSignIn) 一致。
