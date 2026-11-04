package worker

// noopMetrics discards every counter; it is the default until a real sink is wired.
type noopMetrics struct{}

func (noopMetrics) Inc(string, ...string) {}
