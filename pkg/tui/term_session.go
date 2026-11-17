package tui

import (
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/gfx-labs/wist/pkg/notify"
)

// Terminal integration: focus tracking and away notifications, job control
// suspend, composing in $EDITOR and OSC 52 clipboard reads.

// --- notifications -------------------------------------------------------

// alert notifies the user when the terminal is not focused.
func (u *UI) alert(body string) {
	if u.focused {
		return
	}
	mode := u.cfg.Notify
	if mode == "" {
		mode = "auto"
	}
	if mode == "off" {
		return
	}
	title := "wist"
	if u.title != "" {
		title = "wist · " + u.title
	}
	if mode == "auto" || mode == "terminal" {
		if tty, ok := u.sc.Tty(); ok {
			_, _ = tty.Write([]byte(notify.OSC(title, body, os.Getenv("TMUX") != "")))
		}
	}
	if mode == "auto" || mode == "desktop" {
		if argv := notify.DesktopCmd(runtime.GOOS, title, body, exec.LookPath); argv != nil {
			go func() { _ = exec.Command(argv[0], argv[1:]...).Run() }()
		}
	}
}

func (u *UI) handleFocus(ev *tcell.EventFocus) {
	u.focused = ev.Focused
	if u.tick != nil {
		u.tick.Reset(u.tickInterval())
	}
}

// tickInterval slows the spinner while the terminal is in the background.
func (u *UI) tickInterval() time.Duration {
	if u.focused {
		return 80 * time.Millisecond
	}
	return time.Second
}

// --- suspend -------------------------------------------------------------

// suspend stops wist (not towa, which runs in its own process group) and
// resumes when the shell continues it.
func (u *UI) suspend() {
	cont := make(chan os.Signal, 1)
	signal.Notify(cont, syscall.SIGCONT)
	defer signal.Stop(cont)
	if err := u.sc.Suspend(); err != nil {
		u.notify("suspend: " + err.Error())
		return
	}
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGTSTP)
	select {
	case <-cont:
	case <-time.After(50 * time.Millisecond):
		// SIGCONT may have arrived before Notify saw it, or the stop was
		// ignored (no job control). Either way we are running again.
	}
	_ = u.sc.Resume()
	u.sc.Sync()
	renderSynced()
}

// --- $EDITOR -------------------------------------------------------------

func editorCommand() string {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return "vi"
}

// writeComposeFile writes text to a new temp file and returns its path.
func writeComposeFile(text string) (string, error) {
	f, err := os.CreateTemp("", "wist-*.md")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// composeResult strips the single trailing newline editors add.
func composeResult(data []byte) string {
	s := strings.TrimSuffix(string(data), "\n")
	return strings.TrimSuffix(s, "\r")
}

// editInEditor composes the input in $VISUAL/$EDITOR. The command runs
// through sh so values like "code --wait" work.
func (u *UI) editInEditor() {
	path, err := writeComposeFile(u.ed.Text())
	if err != nil {
		u.notify("editor: " + err.Error())
		return
	}
	defer os.Remove(path)
	if err := u.sc.Suspend(); err != nil {
		u.notify("editor: " + err.Error())
		return
	}
	cmd := exec.Command("sh", "-c", editorCommand()+` "$1"`, "sh", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()
	_ = u.sc.Resume()
	u.sc.Sync()
	renderSynced()
	if runErr != nil {
		u.notify("editor: " + runErr.Error())
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		u.notify("editor: " + err.Error())
		return
	}
	u.ed.Set(composeResult(data))
}

// --- OSC 52 clipboard ----------------------------------------------------

// requestClipboard asks the terminal for the clipboard over OSC 52. The
// answer arrives as an EventClipboard. image is whether an image is wanted.
func (u *UI) requestClipboard(image bool) {
	u.clipWant = "text"
	if image {
		u.clipWant = "image"
	}
	u.clipAt = time.Now()
	u.sc.GetClipboard()
}

func (u *UI) handleClipboard(ev *tcell.EventClipboard) {
	want := u.clipWant
	u.clipWant = ""
	if want == "" || time.Since(u.clipAt) > 10*time.Second {
		return
	}
	data := ev.Data()
	if len(data) == 0 {
		u.notify("the clipboard is empty (or the terminal does not allow OSC 52 reads)")
		return
	}
	if m := imageMime(data); m != "" {
		if !u.imagesOK {
			u.notify("this agent does not accept images")
			return
		}
		if len(data) > maxImageBytes {
			u.notify("clipboard image is over the size limit")
			return
		}
		u.attach(attachment{name: "clipboard", mime: m, data: data})
		return
	}
	u.ed.Insert(strings.ReplaceAll(string(data), "\r\n", "\n"))
}
