package dispatch

// EmailNotifier is a Notifier implementation. It is never named at a call site.
type EmailNotifier struct {
	addr string
}

func (e *EmailNotifier) Send(msg string) error {
	_ = e.addr
	_ = msg
	return nil
}
