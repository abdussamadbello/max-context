// Notifier is the interface every delivery path dispatches through. Call sites
// hold a Notifier, never a concrete class, so the text at a call site names this
// method and nothing about which implementation runs.
export interface Notifier {
  send(msg: string): void;
}
