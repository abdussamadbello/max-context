import { Notifier } from "./notifier";

// The gold caller set. Neither names a concrete implementation: the call site
// text is n.send(msg).
export function deliverAlert(n: Notifier, msg: string): void {
  n.send(msg);
}

export function broadcastAll(ns: Notifier[], msg: string): void {
  for (const n of ns) {
    n.send(msg);
  }
}
