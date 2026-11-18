# wist

wist is a terminal UI for [towa](https://github.com/gfx-labs/towa), built with [tcell](https://github.com/gdamore/tcell). The design takes a lot from [jcode](https://github.com/1jehuang/jcode).

wist starts `towa` as a child process and talks the Agent Client Protocol to it over stdio, using [go-acp](https://github.com/gfx-labs/go-acp). towa's stderr goes to `$XDG_STATE_HOME/wist/towa.log`.

```sh
go install github.com/gfx-labs/wist/cmd/wist@latest
wist                                  # new session in the current directory
wist --resume <session-id>            # load a stored session
wist -p "fix the failing test"        # send a first prompt right away
wist -- --models=codex:pro/gpt-6.2-sol   # anything after -- goes to towa
wist ask "explain this repository"    # one prompt, no UI (see below)
wist agents                           # list ACP registry agents
wist --help                           # all flags; wist ask --help for ask
```

## wist ask

`wist ask` runs one prompt against the agent without the terminal UI and exits. It never draws a screen, so it works in scripts, CI and pipes.

```sh
wist ask "explain what this repository does"
printf "summarize pkg/tui\n" | wist ask           # prompt from stdin
git diff | wist ask "review this change"          # args, a blank line, then stdin
wist ask -- "-v means what in towa?"              # everything after -- is prompt text
wist ask --image ./ui.png "describe this interface"   # repeat --image for more
wist ask --resume last "now add tests"            # continue the latest session in cwd
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

Flags: `--resume <id|last>`, `--no-save` (deletes the session after the run via `session/delete`; if the agent cannot delete sessions it is kept and a note goes to stderr), `--model <value>` (a value, name, or unique suffix of the agent's model option; a wrong value lists the choices), `--mode`/`--permission-mode ask|auto` with `--ask`/`--auto` shorthands, `--image <path>`, `--system <text>` (replaces the system prompt; needs the agent's `systemPrompt.mode.override` capability), `--timeout <dur>` (overall wall clock), `--cwd`, `--agent`, `--towa`, `--towa-args`, `--log`.

Permissions: when the agent asks (for example in `--ask` mode) and both stdin and stderr are a terminal without `--json`/`--quiet`, wist prompts on stderr and reads the answer from `/dev/tty` (`y`, `a`, `n` or an option number). `--prompt-permissions` allows that prompt with `--json`/`--quiet`. Otherwise the request is rejected (the agent's reject-once option) and stderr says so. Agent stderr goes to the same log as the UI.

## features

- streaming markdown: headings, lists, task lists, quotes, tables, inline styles, and fenced code with syntax highlighting
- tool cards that show a status spinner, kind icon, duration and a collapsible output preview. `ctrl+o` expands the last card, `alt+e` expands all of them
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
- images in the side panel: an image placeholder (`🖼 #N …`) opens it on click or `alt+i`, and `[` / `]` cycle. Kitty graphics in kitty, ghostty and WezTerm (unicode placeholders inside tmux, so panes can split), sixel in foot and xterm, colored half blocks elsewhere. `WIST_IMAGES=kitty|sixel|blocks|off` overrides
- clickable links (OSC 8) for markdown links, URLs and tool file paths, curly underlines on errors
- away notifications: when the terminal is unfocused, a finished turn or permission request sends a desktop and terminal notification (`notify = auto|off|desktop|terminal`)
- `ctrl+z` suspends wist (towa keeps running), `alt+x` or `/edit` writes the prompt in `$EDITOR`, `/paste` reads the terminal clipboard over OSC 52
- ASCII fallbacks for glyphs the terminal cannot show, and a 256-color palette without truecolor
- per-language syntax highlighting, nested lists, reference links
- sessions: `/fork`, `/rename`, `/archive`, `/delete`, `/back`, and delete/archive/rename keys in the session picker
- `/btw <question>` forks the session and asks the question in a new tmux pane. `/split` and `/split new` open a fork or a fresh session in a new pane
- `/stats` and the status bar show files and lines changed this session
- `~/.config/wist/config.toml`: theme presets (dark, light, catppuccin-mocha, gruvbox, tokyonight, terminal, xresources; also `wist --theme NAME`), colors, remappable keys with conflict warnings, `spawn_hook`. `/theme`, `/keys`
  - `terminal`: only ANSI colors 0-15 and the terminal's default fg/bg, so it follows your terminal palette.
  - `xresources`: reads `xrdb -query` (500ms timeout), else parses `~/.Xresources` then `~/.Xdefaults` directly (works on Wayland; handles `!` comments, `#define`, `#include "file"`; `URxvt*color4` beats `*color4`). `WIST_XRESOURCES=path` overrides. Mapping: accent=color4, green=color2, red=color1, yellow=color3, user=color5, cyan=color6, dim=color8, text=foreground, code/panel/selection/diff backgrounds blended from background; missing keys fall back to `dark`.
- other ACP agents from the [ACP registry](https://github.com/agentclientprotocol/registry): `wist agents`, `wist --agent <id>`, `/agents`
- a status bar with the model, a context meter, cost and the last turn's tokens
- fuzzy pickers for the model (`ctrl+g`), sessions (`ctrl+s`), history (`ctrl+r`) and `/config`
- `shift+tab` cycles the permission mode (auto/ask)
- `/` completion for local commands and the commands towa advertises
- `@` file completion from git. A mentioned file gets attached as a resource link
- a multi-line editor with emacs keys, a kill ring, history and bracketed paste
- mouse wheel and page scrolling, with a terminal title that follows the session title, and `/copy` to copy over OSC 52

`/help` inside the app lists every key and command.

## development

```sh
go install ./cmd/wist   # or: go build ./cmd/wist
go vet ./... && go test ./...
```

`cmd/wist` only parses flags; the client lives under `pkg/`.
