// MetricsBuffer is the decoy. It has a send method with a matching signature,
// so a structural rule would call it a Notifier — but it declares no
// `implements`, and nothing ever uses it as one.
//
// This is where TypeScript differs from Go: the declared rule excludes it, so
// the decoy that costs every arm precision on the Go fixture costs nothing here.
export class MetricsBuffer {
  send(msg: string): void {
    void msg;
  }
}

export function flushMetrics(m: MetricsBuffer, msg: string): void {
  m.send(msg);
}
