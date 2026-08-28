import { Notifier } from "./notifier";

// EmailNotifier states its satisfaction outright. TypeScript records what Go
// leaves structural, which is why this fixture's satisfaction is exact.
export class EmailNotifier implements Notifier {
  send(msg: string): void {
    void msg;
  }
}
