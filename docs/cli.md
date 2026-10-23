# Command line

`tele` with no command opens a window. Every command and its flags are in
[commands.md](commands.md); this page is the contract they keep: how they are
named, what they exit with, and what their JSON looks like.

## A command needs a `tele` to ask

A command asks the `tele` that holds the account (a window you have open, or
the service) and never starts one of its own. With none running it exits 3
and says how to get one: open `tele`, or install the service with
`tele daemon install`. Scripting therefore wants the service: it is there from
login onwards, whether or not a window is open. See
[Running without a window](configuration.md#running-without-a-window).

A command running is not someone looking at the account: it does not stop a
desktop notification from being shown, and quitting a window does not count
it among the windows it would close.

## How commands are named

1. The top level acts on Telegram; the `daemon` subtree acts on the `tele`
   that holds the account.
2. Commands are whole words: no `tele ls`, no `tele d`.
3. Long flags take two dashes, `--name`. A short form exists only for what is
   typed by hand: `-c` for `--config`, `-h` for `--help`, `-e` for `--debug`.
4. A flag that does something hard to undo has no short form.
5. Machine output is `--json`, in every command that has any.
6. The same meaning has the same name in every command, such as `--limit`.
7. A chat is named the same way in every command.
8. An action is a command; a flag only shapes one. That is why it is
   `tele version` and `tele theme check`, not `--version` and `--theme-check`.
   Those two flags, and `--theme-dump`, still answer for the package managers
   that run them, but are not offered.

## Exit codes

| Code | Meaning                                                                         |
| ---- | ------------------------------------------------------------------------------- |
| 0    | Done                                                                            |
| 1    | Anything else went wrong; what it was is said on stderr                         |
| 2    | The command line could not be read: an unknown flag, command or argument        |
| 3    | No `tele` holds the account to ask                                              |
| 4    | The account is not logged in                                                    |
| 5    | No such chat or topic                                                           |
| 6    | A message could not be sent, and trying again will not change that              |
| 75   | Gave up waiting while a message was still queued, or Telegram asked to wait; it may work later |

4, 5, 6 and 75 are reserved for the commands that act on the account
(listing chats, reading and sending), which come in a later release. A script
will be able to tell 75, worth trying again later, from 6, which is not.

## JSON

- `--json` is the only machine format. For YAML, `| yq -P`.
- A command answering with one thing prints one object. One answering with a
  list prints JSON Lines: an object per line, oldest first where order means
  time.
- Field names are `snake_case`. Times are RFC 3339.
- A field that does not apply is left out rather than given as zero or null.
  A script reads a missing field as "not there", never as "zero".
- What is described here holds within a major version: fields may be added,
  none is renamed or removed before tele 3.
- When a command fails, stdout stays empty: the exit code and stderr say why.

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

| Field              | What it is                                                                                     |
| ------------------ | ---------------------------------------------------------------------------------------------- |
| `state_dir`        | The state directory asked about                                                                |
| `host`             | The `tele` that holds it: its version, protocol, process id, config file, and `kept`: `window`, `daemon` or `service` |
| `since`            | When that `tele` started                                                                       |
| `windows`          | How many windows are joined to it                                                              |
| `commands`         | How many other commands are asking it right now                                                |
| `account`          | The account, left out between one and the next                                                 |
| `account.phase`    | `connecting`, `logging_in`, `loading` or `ready`                                               |
| `account.user`     | The logged-in user's username, left out until Telegram has said or when there is none         |
| `account.ended`    | What the connection ended with, left out while it runs                                         |
| `account.link`     | `up`, `down` or `unknown`, and `link_since` since when                                         |
| `account.last_update` | When the last update from Telegram arrived                                                  |
| `account.outbox`   | How many messages are waiting to be sent                                                       |
| `account.throttle` | A wait Telegram asked for that something is held by, and until when; left out when there is none |
| `this`             | The `tele` that asked: its version and protocol                                                |
| `service_warning`  | What is wrong with the installed service, left out when nothing is                             |

A `tele` speaking another protocol is only named: `host` and `this` are there,
and nothing it was not asked. With no `tele` holding the state directory,
nothing is printed and the exit code is 3.
