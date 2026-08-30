package dispatch

// Notifier is the interface every delivery path dispatches through. Call sites
// hold a Notifier, never a concrete type, so the text at a call site names this
// method and nothing about which implementation runs.
type Notifier interface {
	Send(msg string) error
}
