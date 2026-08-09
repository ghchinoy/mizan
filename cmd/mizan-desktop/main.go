// Command mizan-desktop is the Wails v2 desktop frontend for Mizan.
//
// Module-layout verdict (docs/spikes.md Spike 0): Mizan is a SINGLE Go module,
// so this desktop entrypoint lives alongside the CLI under the same go.mod and
// shares internal/. The Wails runtime wiring (options.App, bindings around
// internal/app.App) is added in the desktop phase (Spike 6); this scaffold
// entrypoint deliberately avoids the Wails dependency so the base module graph
// stays lean until then.
package main

import "fmt"

func main() {
	fmt.Println("mizan-desktop: Wails GUI not yet wired (see docs/spikes.md Spike 6)")
}
