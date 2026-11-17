# wist

wist is a terminal UI for [towa](https://github.com/gfx-labs/towa), built with [tcell](https://github.com/gdamore/tcell). The design takes a lot from [jcode](https://github.com/1jehuang/jcode).

wist starts `towa` as a child process and talks the Agent Client Protocol to it over stdio, using [go-acp](https://github.com/gfx-labs/go-acp). towa's stderr goes to `$XDG_STATE_HOME/wist/towa.log`.

```sh
go install github.com/abs3ntdev/wist@latest
wist                                  # new session in the current directory
wist --resume <session-id>            # load a stored session
wist -p "fix the failing test"        # send a first prompt right away
wist -- --models=codex:pro/gpt-6.2-sol   # anything after -- goes to towa
```

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
- `~/.config/wist/config.toml`: theme presets (dark, light, catppuccin-mocha, gruvbox, tokyonight), colors, remappable keys with conflict warnings, `spawn_hook`. `/theme`, `/keys`
- other ACP agents from the [ACP registry](https://github.com/agentclientprotocol/registry): `wist agents`, `wist --agent <id>`, `/agents`
- a status bar with the model, a context meter, cost and the last turn's tokens
- fuzzy pickers for the model (`ctrl+g`), sessions (`ctrl+s`), history (`ctrl+r`) and `/config`
- `shift+tab` cycles the permission mode (auto/ask)
- `/` completion for local commands and the commands towa advertises
- `@` file completion from git. A mentioned file gets attached as a resource link
- a multi-line editor with emacs keys, a kill ring, history and bracketed paste
- mouse wheel and page scrolling, with a terminal title that follows the session title, and `/copy` to copy over OSC 52

`/help` inside the app lists every key and command.
