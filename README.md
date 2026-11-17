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
- prompts you send during a turn get queued and run in order. `esc` or `ctrl+c` interrupts
- a status bar with the model, a context meter, cost and the last turn's tokens
- fuzzy pickers for the model (`ctrl+g`), sessions (`ctrl+s`), history (`ctrl+r`) and `/config`
- `shift+tab` cycles the permission mode (auto/ask)
- `/` completion for local commands and the commands towa advertises
- `@` file completion from git. A mentioned file gets attached as a resource link
- a multi-line editor with emacs keys, a kill ring, history and bracketed paste
- mouse wheel and page scrolling, with a terminal title that follows the session title, and `/copy` to copy over OSC 52

`/help` inside the app lists every key and command.
