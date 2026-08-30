package dispatch

// SMSNotifier is the second Notifier implementation, likewise never named at a
// call site. Two implementations make the fan-out real: resolving n.Send() to
// one concrete method would be a guess, not an answer.
type SMSNotifier struct {
	number string
}

func (s *SMSNotifier) Send(msg string) error {
	_ = s.number
	_ = msg
	return nil
}
