package dispatch

// DeliverAlert and BroadcastAll are the gold caller set. Both reach every
// Notifier implementation, and neither spells the name of any of them: the call
// site text is n.Send(msg), which shares no substring with EmailNotifier or
// SMSNotifier.
func DeliverAlert(n Notifier, msg string) error {
	return n.Send(msg)
}

func BroadcastAll(ns []Notifier, msg string) error {
	for _, n := range ns {
		if err := n.Send(msg); err != nil {
			return err
		}
	}
	return nil
}
