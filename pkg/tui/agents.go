package tui

import (
	"fmt"

	"github.com/gfx-labs/wist/pkg/registry"
)

// pickAgent shows the registry. Choosing an agent opens it in a new pane
// (or tells the user how to start it).
func (u *UI) pickAgent() {
	go func() {
		r, err := registry.Load()
		u.post(func(u *UI) {
			if err != nil {
				u.errorf("%v", err)
				return
			}
			items := []pickItem{{label: "towa", detail: "default", value: "towa", current: u.opts.Agent == "" || u.opts.Agent == "towa"}}
			for _, a := range r.Agents {
				items = append(items, pickItem{label: a.Name + " (" + a.ID + ")", detail: a.Version + " · " + a.Description, value: a.ID, current: a.ID == u.opts.Agent})
			}
			u.openPicker("ACP agents", items, 0, func(it pickItem) {
				args := []string{"--agent", it.value}
				if err := u.spawnPane(args); err != nil {
					u.info(fmt.Sprintf("%v\nrun it with: wist --agent %s", err, it.value))
				}
			})
		})
	}()
}
