# 命令

> 本文译自 [commands.md](../commands.md)，对应提交 `04bd26c`。译文与英文版不一致时，以英文版为准。本译文尚未经过母语者审校，欢迎提交修正。

`tele` 的全部命令及其参数。命令的共同点（如何命名、以什么退出码结束、JSON 输出是什么样子）见 [cli.md](cli.md)。`tele help COMMAND` 或在任意命令后加 `--help`，可以直接从程序中看到同样的内容（英文）。

- [所有命令都接受的参数](#flags-every-command-takes)
- [`tele`](#tele)
- [`tele version`](#tele-version)
- [`tele theme check`](#tele-theme-check)
- [`tele theme dump`](#tele-theme-dump)
- [`tele daemon`](#tele-daemon)
- [`tele daemon status`](#tele-daemon-status)
- [`tele daemon install`](#tele-daemon-install)
- [`tele daemon uninstall`](#tele-daemon-uninstall)

<a id="flags-every-command-takes"></a>
## 所有命令都接受的参数

| 参数                    | 作用                                                                                 |
| ----------------------- | ------------------------------------------------------------------------------------ |
| `-c`, `--config <path>` | 要读取的配置文件，默认为 `~/.config/tele/config.yml`，Windows 上也是如此。它同时决定状态目录，从而决定命令针对的是哪个账号 |
| `-e`, `--debug`         | 以 debug 级别记录日志                                                                |
| `--trace`               | 额外记录能识别聊天和人的信息：对方 id、消息长度。切勿在共享或同步的文件系统上使用    |
| `-h`, `--help`          | 说明命令的作用和它接受的参数                                                         |

如果配置文件还不存在，会先用默认值写入一份。日志是 `$XDG_STATE_HOME/tele` 中的 `tele.log`，未设置该变量时位于 `~/.local/state/tele`，与账号无关。

<a id="tele"></a>
## `tele`

```console
tele [-c PATH]
```

打开一个显示账号的窗口。如果没有其他 `tele` 持有该账号，就由这个窗口持有，直到窗口关闭；如果已有 `tele` 持有（某个窗口、`tele daemon` 或服务），这个窗口会连接到它并共用它的连接。关闭一个持有账号、且还有其他窗口连接着的窗口时，会先询问，然后把那些窗口一起关闭。

持有账号的 `tele` 第一次在可以安装服务的机器上登录时，会询问一次是否安装服务。参见[无窗口运行](configuration.md#running-without-a-window)。

<a id="tele-version"></a>
## `tele version`

```console
tele version
```

只输出版本号，便于脚本读取：`2.0.0`、`2.0.0-beta.11`，或者对于没有版本号的源码构建输出 `dev`。

`tele --version` 作用相同。保留它是为了那些在测试中调用它的包管理器，帮助中不会列出。

<a id="tele-theme-check"></a>
## `tele theme check`

```console
tele theme check [NAME]
```

说明每个槽位（深色和浅色）最终解析成了哪个主题、它沿着哪条基础主题链继承而来，以及有多少 token 来自何处，包括那些回退到内置主题的 token。如果主题设定了自己的画布，还会列出在该画布上难以辨认的 token。指定 `NAME` 时，只检查这一个主题：槽位（`dark`、`light`）或按名称指定的主题（`nord`、`mine`）。关于主题文件的警告列在最后。

`tele --theme-check[=NAME]` 作用相同，帮助中不会列出。参见 [themes.md](themes.md#when-something-looks-wrong)。

<a id="tele-theme-dump"></a>
## `tele theme dump`

```console
tele theme dump [NAME] > ~/.config/tele/themes/mine.yml
```

把一个主题输出为完整的主题文件，每个 token 都有值，可以直接编辑：不指定 `NAME` 时输出深色槽位的主题，否则输出 `NAME` 指定的槽位（`dark`、`light`）或主题。stdout 中只有文件内容，因此可以直接重定向；出错时信息输出到 stderr，退出码为 1。

`tele --theme-dump[=NAME]` 作用相同，帮助中不会列出。参见 [themes.md](themes.md)。

<a id="tele-daemon"></a>
## `tele daemon`

```console
tele daemon [--service]
```

在没有窗口的情况下持有账号，直到被停止（Ctrl+C，或服务管理器发送的信号）。它保持连接、发送桌面通知，窗口和命令可以像连接到窗口一样连接到它。它只会持有账号：如果已有其他 `tele` 持有该账号，它以退出码 1 结束，并给出那个 `tele` 的版本和进程 id。

| 参数        | 作用                                                                                               |
| ----------- | -------------------------------------------------------------------------------------------------- |
| `--service` | 作为服务运行：由服务管理器启动，不需要手动输入。以这种方式启动的 `tele` 在账号被其他 `tele` 持有时不会退出，而是等待，并在那个 `tele` 退出后接管。当更新版本的 `tele` 要求它重启时，它以退出码 75 结束，以便服务管理器启动当前安装的版本 |

<a id="tele-daemon-status"></a>
## `tele daemon status`

```console
tele daemon status [--json]
```

说明持有账号的 `tele`（包括窗口）的运行状况：它的版本与当前版本的对比，是什么让它保持运行（窗口、`tele daemon` 或服务）以及从何时开始，连接到它的窗口和其他命令的数量，账号所处的状态，与 Telegram 的连接是否正常，最近一次更新何时到达，有多少消息等待发送，以及 Telegram 要求的任何等待。如果已安装的服务运行的命令已不存在，或者版本与当前版本不同，它也会发出警告。

有 `tele` 回应时以退出码 0 结束，没有任何回应时以 3 结束。

| 参数     | 作用                                                                                             |
| -------- | ------------------------------------------------------------------------------------------------ |
| `--json` | 以单个 JSON 对象输出结果，格式见 [cli.md](cli.md#tele-daemon-status---json)。没有回应时不输出任何内容 |

<a id="tele-daemon-install"></a>
## `tele daemon install`

```console
tele daemon install [--path COMMAND]
```

让操作系统以服务的形式保持 `tele` 运行，对应传入的配置：macOS 上是 LaunchAgent，Linux 上是 systemd 用户单元，Windows 上是任务计划程序中的任务。服务在每次登录系统时启动，退出后会再次启动，并且安装后立即启动。如果此时有 `tele` 持有账号，服务会等待，并在那个 `tele` 退出后接管。

服务按 `PATH` 找到的方式运行 `tele`，这样升级会替换它启动的程序。如果 `PATH` 中的 `tele` 不是正在执行该命令的那个，`install` 会拒绝并给出两者。重新安装会替换服务并重新启动它。每个配置文件对应一个服务。在没有服务管理器可用的系统上，它会说明如何在你自己的会话中启动 `tele daemon`，并以退出码 1 结束。

| 参数               | 作用                                                                                 |
| ------------------ | ------------------------------------------------------------------------------------ |
| `--path <command>` | 服务运行的命令，用来代替按 `PATH` 找到的 `tele`。使用 Scoop 时需要它，因为 Scoop 放在 `PATH` 中的 `tele` 是一个 shim |

参见[作为服务运行](configuration.md#as-a-service)。

<a id="tele-daemon-uninstall"></a>
## `tele daemon uninstall`

```console
tele daemon uninstall
```

停止并删除传入配置对应的服务。连接到该服务的窗口会收到服务已被卸载的通知，并停止等待它。状态和配置保持原样。如果没有安装服务，它会如实说明，并以退出码 0 结束。
