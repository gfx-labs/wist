# wist

wist is a terminal UI for [towa](https://github.com/gfx-labs/towa), built with [tcell](https://github.com/gdamore/tcell). The design takes a lot from [jcode](https://github.com/1jehuang/jcode).

wist windows talk the Agent Client Protocol, using [go-acp](https://github.com/gfx-labs/go-acp), to the wist daemon, which runs one `towa` process for all of them (see below). towa's stderr goes to `$XDG_STATE_HOME/wist/towa.log`.

```sh
go install github.com/gfx-labs/wist/cmd/wist@latest
wist                                  # a new session, created by the first prompt
wist --resume <session-id>            # load a stored session
wist --resume 'fix the parser'        # ... or find it by title (see sessions below)
wist --title 'fix the parser'         # title the session this window starts
wist -p "fix the failing test"        # send a first prompt right away
wist -- --models=codex:pro/gpt-6.2-sol   # anything after -- goes to towa
wist ask "explain this repository"    # one prompt, no UI (see below)
wist sessions -w 'gfx-labs'           # stored sessions, filtered by workdir (see below)
wist agents                           # list ACP registry agents
wist daemon stop                      # stop the daemon and its agent
wist --help                           # all flags; wist ask --help for ask
```

## wist daemon

The first wist window starts the daemon in the background, and later windows connect to it. The daemon runs one agent process and serves ACP on a unix socket: `--socket`, else `$WIST_SOCKET`, else `$XDG_RUNTIME_DIR/wist/daemon.sock`. Pass `--socket` or set `WIST_SOCKET` to run a separate daemon.

- A turn keeps running when the window that started it closes. `wist --resume <id or title>` attaches again.
- Several windows can open one session. Each one sees the others' prompts and output, prompts run in the order they arrive, and a permission request goes to every window, where the first answer wins.
- Every session's updates are stored in SQLite (`$XDG_STATE_HOME/wist/daemon.db`), so attaching replays from the daemon instead of from the agent. Messages are indexed for full text search.
- The agent flags (`--towa`, `--agent`, `--towa-args`, args after `--`) take effect only when they start the daemon. A window that finds a daemon already running uses its agent and says so. Run `wist daemon stop` to restart with other flags.
- Other ACP clients, such as a web UI, connect to the same socket. Besides ACP, the daemon answers `_wist/sessions` (stored sessions with busy state and viewer count), `_wist/search` (`{"query": "...", "limit": n, "sessionId": "..."}`, sessionId optional), `_wist/reload`, `_wist/shutdown` and `_wist/watch`, after which the connection gets a `_wist/session` notification whenever a session's title, busy state, viewers or pending permission requests change.
- `wist daemon` runs it in the foreground and logs to stderr. When a window starts it, the log is `$XDG_STATE_HOME/wist/daemon.log`.

## wist sessions

`wist sessions` lists the stored sessions the daemon's agent reports across every working directory, newest first, and exits. It asks the daemon on `--socket` (else `$WIST_SOCKET`, else the default socket) instead of running an agent of its own, so it sees the same sessions every window does and never races a second `towa` over the session store. When nothing listens on the socket, it starts the daemon the way a window would; that daemon stays running.

```sh
wist sessions                              # table: updated, id, title, workdir
wist sessions -w '/gfx-labs/(wist|towa)$'  # only matching working directories
wist sessions -t parser                    # only titles matching, ignoring case
wist sessions --json | jq -r '.[].session_id'
wist sessions --archived only              # archived sessions only
```

`--workdir`/`-w` takes a Go regular expression matched against the session's working directory, unanchored, so `-w wist` matches any path containing `wist`. `--title`/`-t` does the same for the title, ignoring case. `--archived` is `exclude` (default), `include` or `only` and needs an agent that advertises the archive capability. `--json` prints `session_id`, `cwd`, `updated_at`, `title` and `archived` fields. `--towa`, `--agent`, `--towa-args` and `--log` describe the daemon's agent, like the other commands, and only apply to a daemon this call starts. `--timeout` (default 30s) bounds the whole listing.

Exit status: `0` listing succeeded (also when nothing matched), `1` the daemon or its agent failed, `2` a bad flag, regular expression or `--archived` value.

## session titles

A session's title is stored by the agent (towa's `session/set_title`) and mirrored in the daemon's database, so every window, `wist sessions` and `_wist/sessions` see it.

- Set it with `wist --title <title>` or `/rename <title>`. Before the first prompt, both name the session that prompt creates. The session picker's `ctrl+r` renames any listed session.
- `wist --resume <ref>`, `/resume <ref>` and `wist ask --resume <ref>` take a session id, a whole title or part of a title, in any working directory. Titles match ignoring case: an id wins, then a whole title, then titles that contain the text. When several sessions match, the window opens a picker of them, showing their directories, and `wist ask` fails and lists them. A window that resumes a session from another directory moves to that directory unless `--cwd` was given.
- `wist sessions -t <regexp>` filters the listing by title, and the session picker (`ctrl+s`) filters on titles as you type.
- Nothing titles a session automatically: towa does not generate titles, and wist leaves untitled sessions untitled.

## wist ask

`wist ask` runs one prompt against the agent without the terminal UI and exits. It never draws a screen, so it works in scripts, CI and pipes.

```sh
wist ask "explain what this repository does"
printf "summarize pkg/tui\n" | wist ask           # prompt from stdin
git diff | wist ask "review this change"          # args, a blank line, then stdin
wist ask -- "-v means what in towa?"              # everything after -- is prompt text
wist ask --image ./ui.png "describe this interface"   # repeat --image for more
wist ask --resume last "now add tests"            # continue the latest session in cwd
wist ask --resume "fix the parser" "add a test"   # continue a session found by title
wist ask --model codex:pro/gpt-6.2-sol --auto "fix the failing test"
wist ask --json "what is 2+2" | jq -r .final_output
```

stdout receives the assistant's raw markdown as it streams. stderr receives progress: one line per tool call (`· Read file`, `✓ Edit a.go +3 -1`, `✗ Run command: failed`), plan updates, notices and a usage line at the end. On a terminal stderr gets colors and a spinner; `--no-color`, `NO_COLOR` or `TERM=dumb` make it plain. `--quiet` prints no assistant text; `--json` wins over `--quiet` and prints exactly one object at the end:

```json
{
  "output": "4",
  "final_output": "4",
  "exit_code": 0,
  "model": "claude:max/claude-opus-5-5#medium",
  "session_id": "01a1ec3b-3dac-741f-89b3-ac623ee382ef",
  "stop_reason": "end_turn",
  "steps": 1,
  "usage": {"input_tokens": 4, "output_tokens": 3, "cache_read_tokens": 16645, "cache_write_tokens": 82},
  "tool_calls": []
}
```

`output` is all assistant text of the turn, `final_output` the text after the last tool call (empty unless the turn ended with `end_turn`). `steps` counts model responses: a step starts with the first text or tool call, and again with each text that follows a tool call. `tool_calls` lists `name` (the tool title), `kind` and `status`. On failure `error` holds the message. Cache counts appear only when the agent reports them.

Exit status: `0` when the turn ends with `end_turn`; `1` for any failure (agent error, agent exit, `--timeout`, or any other stop reason such as `refusal`, `max_tokens` or `cancelled`); `2` for usage mistakes (unknown flag, empty prompt, `--no-save` with `--resume`, a bad `--model`/`--mode` value, an unreadable `--image`). Ctrl+C sends `session/cancel`, waits briefly and exits `130` (`143` on SIGTERM); an interrupted `--json` run writes no result.

Flags: `--resume <id|title|last>` (a title resolves as in [session titles](#session-titles); several matches are an error that lists them), `--no-save` (deletes the session after the run via `session/delete`; if the agent cannot delete sessions it is kept and a note goes to stderr), `--model <value>` (a value, name, or unique suffix of the agent's model option; a wrong value lists the choices), `--mode`/`--permission-mode ask|auto` with `--ask`/`--auto` shorthands, `--image <path>`, `--system <text>` (replaces the system prompt; needs the agent's `systemPrompt.mode.override` capability), `--timeout <dur>` (overall wall clock), `--cwd`, `--agent`, `--towa`, `--towa-args`, `--log`.

Permissions: when the agent asks (for example in `--ask` mode) and both stdin and stderr are a terminal without `--json`/`--quiet`, wist prompts on stderr and reads the answer from `/dev/tty` (`y`, `a`, `n` or an option number). `--prompt-permissions` allows that prompt with `--json`/`--quiet`. Otherwise the request is rejected (the agent's reject-once option) and stderr says so. Agent stderr goes to the same log as the UI.

## features

- streaming markdown: headings, lists, task lists, quotes, tables, inline styles, and fenced code with syntax highlighting
- tool cards that show a status spinner, kind icon, duration and a collapsible output preview. `ctrl+o` expands the last card, `alt+e` expands all of them. `alt+o` or `/output hidden|preview|full` sets how much output cards show: `hidden` keeps only the head line (status, title, path, duration), for a compact transcript. `tool_output:` in the config sets the default, and `alt+i` still shows the last tool's full output in the side panel
- edits shown as colored line diffs with context and +/- counts
- collapsed reasoning (`ctrl+t` shows it in full)
- a plan panel: a sidebar on wide terminals, inline on narrow ones (`alt+p`)
- a permission card with the diff or input, answered with `y` / `a` / `n`, the arrow keys or enter
- soft interrupt: enter during a turn queues the message and sends everything queued as one prompt when the turn ends. `shift+enter` interrupts and sends right away. `esc` or `ctrl+c` interrupts
- code blocks get a `⧉ copy` button as soon as they finish. Click it, press `alt+1..9`, or run `/copy-code [n]`
- click and drag in the transcript to select text, which is copied on release
- `alt+/` or `/search` searches the transcript
- mermaid flowcharts and sequence diagrams drawn as boxes and arrows, with a side panel on `alt+i`
- images: `ctrl+v` attaches an image from the clipboard (wl-paste or xclip), dragging an image file into the terminal or pasting its path attaches it, and `@shot.png` sends it as an image. Backspace on an empty input removes the last one
- images in the side panel: an image placeholder (`🖼 #N …`) opens it on click or `alt+i`, and `[` / `]` cycle. Kitty graphics in kitty, ghostty and WezTerm (unicode placeholders inside tmux, so panes can split), sixel in foot, xterm and tmux clients that advertise it, colored half blocks elsewhere. `WIST_IMAGES=kitty|sixel|blocks|off` overrides
- clickable links (OSC 8) for markdown links, URLs and tool file paths, curly underlines on errors
- away notifications: when the terminal is unfocused, a finished turn or permission request sends a desktop and terminal notification (`notify = auto|off|desktop|terminal`)
- `ctrl+z` suspends wist (towa keeps running), `alt+x` or `/edit` writes the prompt in `$EDITOR`, `/paste` reads the terminal clipboard over OSC 52
- ASCII fallbacks for glyphs the terminal cannot show, and a 256-color palette without truecolor
- per-language syntax highlighting, nested lists, reference links
- sessions: a window creates its session with the first prompt, so opening and quitting wist stores none. `/fork`, `/rename`, `/archive`, `/delete`, `/back`, and delete/archive/rename keys in the session picker. The picker marks the sessions the daemon already has attached to a window ("connected", with the viewer count), so resuming one joins it instead of starting it fresh
- `/btw <question>` forks the session and asks the question in a new tmux pane. `/split` and `/split new` open a fork or a fresh session in a new pane
- `/stats` and the status bar show files and lines changed this session
- `/reload` restarts the wist window in place and resumes its session, so a newly installed wist binary and changes to `config.yaml` take effect. A running turn keeps going in the daemon and is attached again. The daemon keeps running its old binary; run `wist daemon stop` to restart it
- `/reload-agent` restarts the daemon's agent process and loads the current session again, so a change to the agent's config (its model list, for example) takes effect without leaving wist
- `~/.config/wist/config.yaml` (or `config.yml`; `config.toml` is no longer read): theme presets (dark, light, catppuccin-mocha, gruvbox, tokyonight, terminal, xresources; also `wist --theme NAME`), colors, remappable keys with conflict warnings, `spawn_hook`. `/theme`, `/keys`

  ```yaml
  towa_args: ["--models=claude:max/claude-opus-5-5"]
  agent: towa
  spawn_hook: "tmux split-window -h {cmd}"
  notify: auto
  input_mode: vim   # or emacs (default)
  tool_output: hidden   # or preview (default) or full
  theme:
    preset: xresources
    accent: "#7aa2f7"
  keys:
    model_picker: ctrl+g
    theme_picker: [f2, alt+t]
  ```

  - `terminal`: only ANSI colors 0-15 and the terminal's default fg/bg, so it follows your terminal palette.
  - `xresources`: reads `xrdb -query` (500ms timeout), else parses `~/.Xresources` then `~/.Xdefaults` directly (works on Wayland; handles `!` comments, `#define`, `#include "file"`; `URxvt*color4` beats `*color4`). `WIST_XRESOURCES=path` overrides. Mapping: accent=color4, green=color2, red=color1, yellow=color3, user=color5, cyan=color6, dim=color8, text=foreground, code/panel/selection/diff backgrounds blended from background; missing keys fall back to `dark`.
- other ACP agents from the [ACP registry](https://github.com/agentclientprotocol/registry): `wist agents`, `wist --agent <id>`, `/agents`
- a status bar with the model, a context meter, cost and the last turn's tokens
- fuzzy pickers for the model (`ctrl+g`), sessions (`ctrl+s`), history (`ctrl+r`) and `/config`
- `shift+tab` cycles the permission mode (auto/ask)
- `/` completion for local commands and the commands towa advertises
- `@` file completion from git. A mentioned file gets attached as a resource link
- a multi-line editor with emacs keys, a kill ring, history and bracketed paste
- vim keys in the input with `input_mode: vim` or `/vim`. Each prompt starts in insert mode, and esc switches to normal mode, which has a block cursor and a mode tag on the input border. Normal mode supports counts; motions `h l j k w b e W B E ge 0 ^ $ f t F T ; , gg G`; operators `d c y` with motions, `dd cc yy` and text objects (`iw aw i" a' i( a{ i[ i<` and similar); `x X D C s S Y p P r ~ J`; `i a I A o O`; `u`, `ctrl+r` and `.`; and visual mode with `v` and `V`. `j`/`k` on the first or last line browse history. Enter sends from either mode. In normal mode, esc interrupts a running turn. Other keys (arrows, ctrl keys) work as in emacs mode, but in normal mode `ctrl+r` redoes instead of searching history
- mouse wheel and page scrolling, with a terminal title that follows the session title, and `/copy` to copy over OSC 52

`/help` inside the app lists every key and command.

## development

```sh
go install ./cmd/wist   # or: go build ./cmd/wist
go vet ./... && go test ./...
```

`cmd/wist` only parses flags; the client lives under `pkg/`.
