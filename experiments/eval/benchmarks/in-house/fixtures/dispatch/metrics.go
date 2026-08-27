package dispatch

// MetricsBuffer is the decoy. It has a Send method and is therefore
// indistinguishable from a Notifier to any name-based matcher, but it is never
// used as one: FlushMetrics calls it directly on the concrete type.
//
// It exists to make precision measurable. An arm that answers "who dispatches
// through Notifier?" with FlushMetrics has confused a shared method name for a
// shared interface — the failure mode both text search and name-only interface
// resolution are prone to.
type MetricsBuffer struct {
	pending int
}

func (m *MetricsBuffer) Send(msg string) error {
	_ = m.pending
	_ = msg
	return nil
}

func FlushMetrics(m *MetricsBuffer, msg string) error {
	return m.Send(msg)
}
