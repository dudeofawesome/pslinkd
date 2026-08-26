package state

type Connection struct {
  AdapterPresent   bool
  HeadsetConnected bool
}

type Debouncer struct {
  threshold   int
  state       Connection
  established bool
  failures    int
}

func NewDebouncer(disconnectFailures int) *Debouncer {
  if disconnectFailures < 1 {
    panic("disconnect failure threshold must be positive")
  }
  return &Debouncer{threshold: disconnectFailures}
}

func (debouncer *Debouncer) AdapterAdded() {
  debouncer.state.AdapterPresent = true
  debouncer.failures = 0
}

func (debouncer *Debouncer) AdapterAbsent() (Connection, bool) {
  changed := !debouncer.established ||
    debouncer.state.AdapterPresent ||
    debouncer.state.HeadsetConnected
  debouncer.state = Connection{}
  debouncer.established = true
  debouncer.failures = 0
  return debouncer.state, changed
}

func (debouncer *Debouncer) Sample(connected bool) (Connection, bool) {
  if !debouncer.state.AdapterPresent {
    return debouncer.state, false
  }
  if connected {
    debouncer.failures = 0
    changed := !debouncer.established || !debouncer.state.HeadsetConnected
    debouncer.established = true
    debouncer.state.HeadsetConnected = true
    return debouncer.state, changed
  }

  debouncer.failures++
  if debouncer.failures < debouncer.threshold {
    return debouncer.state, false
  }
  changed := !debouncer.established || debouncer.state.HeadsetConnected
  debouncer.established = true
  debouncer.state.HeadsetConnected = false
  return debouncer.state, changed
}

func (debouncer *Debouncer) State() (Connection, bool) {
  return debouncer.state, debouncer.established
}
