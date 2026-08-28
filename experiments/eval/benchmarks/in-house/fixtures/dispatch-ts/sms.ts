import { Notifier } from "./notifier";

export class SmsNotifier implements Notifier {
  send(msg: string): void {
    void msg;
  }
}
