# 命令行

> 本文译自 [cli.md](../cli.md)，对应提交 `04bd26c`。译文与英文版不一致时，以英文版为准。本译文尚未经过母语者审校，欢迎提交修正。

不带命令运行 `tele` 会打开一个窗口。所有命令及其参数见 [commands.md](commands.md)。本页说明所有命令共同遵守的约定：如何命名、以什么退出码结束、JSON 输出是什么样子。

<a id="a-command-needs-a-tele-to-ask"></a>
## 命令需要一个可以询问的 `tele`

命令会询问持有账号的 `tele`（你打开的某个窗口，或者服务），自己从不启动 `tele`。如果没有正在运行的 `tele`，命令以退出码 3 结束，并提示如何获得一个：打开 `tele`，或者用 `tele daemon install` 安装服务。因此脚本适合配合服务使用：无论有没有打开窗口，服务从登录系统起就在运行。参见[无窗口运行](configuration.md#running-without-a-window)。

正在运行的命令不算有人在看这个账号：它不会阻止桌面通知的显示，关闭窗口时也不会把它算作会一并关闭的窗口。

<a id="how-commands-are-named"></a>
## 命令的命名规则

1. 顶层命令作用于 Telegram；`daemon` 子命令作用于持有账号的 `tele`。
2. 命令都是完整的单词：没有 `tele ls`，也没有 `tele d`。
3. 长参数使用两个短横线，如 `--name`。只有需要手动输入的参数才有短形式：`-c` 对应 `--config`，`-h` 对应 `--help`，`-e` 对应 `--debug`。
4. 执行难以撤销操作的参数没有短形式。
5. 面向程序的输出一律用 `--json`，只要命令有这种输出。
6. 同一含义在所有命令中使用同一名称，例如 `--limit`。
7. 所有命令以相同方式指定聊天。
8. 动作是命令，参数只用来调整命令。所以是 `tele version` 和 `tele theme check`，而不是 `--version` 和 `--theme-check`。这两个参数以及 `--theme-dump` 仍然有效，供调用它们的包管理器使用，但不在帮助中列出。

<a id="exit-codes"></a>
## 退出码

| 代码 | 含义                                                                            |
| ---- | ------------------------------------------------------------------------------- |
| 0    | 完成                                                                            |
| 1    | 其他任何错误；具体原因输出在 stderr                                             |
| 2    | 无法解析命令行：未知的参数、命令或实参                                          |
| 3    | 没有持有账号、可供询问的 `tele`                                                 |
| 4    | 账号未登录                                                                      |
| 5    | 没有这个聊天或话题                                                              |
| 6    | 消息无法发送，重试也不会改变结果                                                |
| 75   | 消息仍在队列中时放弃等待，或者 Telegram 要求等待；稍后可能会成功                |

4、5、6 和 75 留给作用于账号的命令（列出聊天、读取和发送），这些命令会在之后的版本中推出。届时脚本可以区分 75（值得稍后重试）和 6（不值得）。

<a id="json"></a>
## JSON

- `--json` 是唯一面向程序的格式。需要 YAML 时，用 `| yq -P`。
- 回答单个结果的命令输出一个对象。回答列表的命令输出 JSON Lines：每行一个对象，在顺序代表时间的地方按从旧到新排列。
- 字段名使用 `snake_case`。时间使用 RFC 3339 格式。
- 不适用的字段直接省略，而不是输出零或 null。脚本应把缺失的字段理解为“不存在”，而不是“零”。
- 本页描述的内容在同一个主版本内保持不变：可能新增字段，但在 tele 3 之前不会重命名或删除任何字段。
- 命令失败时 stdout 保持为空：原因由退出码和 stderr 说明。

<a id="tele-daemon-status---json"></a>
## `tele daemon status --json`

```json
{
  "state_dir": "/home/ada/.local/state/tele",
  "host": {"binary": "2.0.0", "protocol": 5, "pid": 4242, "kept": "service", "config": "/home/ada/.config/tele/config.yml"},
  "since": "2026-10-21T09:00:00Z",
  "windows": 1,
  "commands": 0,
  "account": {
    "phase": "ready",
    "user": "ada",
    "link": "up",
    "link_since": "2026-10-21T09:00:04Z",
    "last_update": "2026-10-21T12:41:10Z",
    "outbox": 0,
    "throttle": {"method": "messages.getHistory", "until": "2026-10-21T12:41:42Z"}
  },
  "this": {"binary": "2.0.0", "protocol": 5},
  "service_warning": "The service runs /opt/homebrew/bin/tele, which is not there any more."
}
```

| 字段               | 含义                                                                                           |
| ------------------ | ---------------------------------------------------------------------------------------------- |
| `state_dir`        | 被询问的状态目录                                                                               |
| `host`             | 持有它的 `tele`：版本、协议、进程 id、配置文件，以及 `kept`：`window`、`daemon` 或 `service`   |
| `since`            | 该 `tele` 的启动时间                                                                           |
| `windows`          | 连接到它的窗口数                                                                               |
| `commands`         | 此刻正在询问它的其他命令数                                                                     |
| `account`          | 账号；在前一个账号结束、下一个账号开始之前省略                                                 |
| `account.phase`    | `connecting`、`logging_in`、`loading` 或 `ready`                                               |
| `account.user`     | 已登录用户的用户名；Telegram 尚未告知或用户没有用户名时省略                                    |
| `account.ended`    | 连接以什么结束；连接运行期间省略                                                               |
| `account.link`     | `up`、`down` 或 `unknown`，从何时起由 `link_since` 给出                                        |
| `account.last_update` | 最近一次收到 Telegram 更新的时间                                                            |
| `account.outbox`   | 等待发送的消息数                                                                               |
| `account.throttle` | Telegram 要求的、正在阻塞某项操作的等待，以及持续到何时；没有等待时省略                        |
| `this`             | 发起询问的 `tele`：它的版本和协议                                                              |
| `service_warning`  | 已安装的服务有什么问题；没有问题时省略                                                         |

使用其他协议版本的 `tele` 只会被点名：只有 `host` 和 `this`，不包含任何没被问到的内容。如果没有 `tele` 持有该状态目录，则不输出任何内容，退出码为 3。
