package state

import "testing"

func TestImmediateConnectionAndThresholdDisconnect(t *testing.T) {
  debouncer := NewDebouncer(3)
  debouncer.AdapterAdded()
  state, changed := debouncer.Sample(true)
  if !changed || !state.HeadsetConnected {
    t.Fatalf("connection = %#v, changed = %v", state, changed)
  }
  for sample := 1; sample <= 2; sample++ {
    state, changed = debouncer.Sample(false)
    if changed || !state.HeadsetConnected {
      t.Fatalf("failure %d = %#v, changed = %v", sample, state, changed)
    }
  }
  state, changed = debouncer.Sample(false)
  if !changed || state.HeadsetConnected {
    t.Fatalf("third failure = %#v, changed = %v", state, changed)
  }
  if _, changed = debouncer.Sample(false); changed {
    t.Fatal("steady disconnection emitted another transition")
  }
}

func TestSuccessfulSampleResetsFailures(t *testing.T) {
  debouncer := NewDebouncer(3)
  debouncer.AdapterAdded()
  debouncer.Sample(true)
  debouncer.Sample(false)
  debouncer.Sample(false)
  if _, changed := debouncer.Sample(true); changed {
    t.Fatal("steady connected sample emitted a transition")
  }
  if _, changed := debouncer.Sample(false); changed {
    t.Fatal("failure after reset disconnected")
  }
}

func TestAdapterAbsenceEstablishesFallbackImmediately(t *testing.T) {
  debouncer := NewDebouncer(3)
  state, changed := debouncer.AdapterAbsent()
  if !changed || state.AdapterPresent || state.HeadsetConnected {
    t.Fatalf("absence = %#v, changed = %v", state, changed)
  }

  debouncer.AdapterAdded()
  debouncer.Sample(true)
  state, changed = debouncer.AdapterAbsent()
  if !changed || state.AdapterPresent || state.HeadsetConnected {
    t.Fatalf("removal = %#v, changed = %v", state, changed)
  }
}

func TestPoweredOffAdapterEstablishesFallbackAtThreshold(t *testing.T) {
  debouncer := NewDebouncer(3)
  debouncer.AdapterAdded()
  for sample := 1; sample < 3; sample++ {
    if _, changed := debouncer.Sample(false); changed {
      t.Fatalf("sample %d established state early", sample)
    }
  }
  state, changed := debouncer.Sample(false)
  if !changed || !state.AdapterPresent || state.HeadsetConnected {
    t.Fatalf("threshold state = %#v, changed = %v", state, changed)
  }
}
