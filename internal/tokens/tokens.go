// Package tokens normalizes like-for-like native counter observations.
package tokens

import "errors"

const AlgorithmVersion = "counter-delta-v1"

type Observation struct {
	CounterType string
	Unit        string
	Value       uint64
	Sequence    uint64
	Source      string
}

type Delta struct {
	CounterType      string
	Unit             string
	Value            uint64
	Start            Observation
	End              Observation
	State            string
	Confidence       string
	AlgorithmVersion string
}

func Derive(start, end Observation, wrapAt uint64) (Delta, error) {
	result := Delta{CounterType: end.CounterType, Unit: end.Unit, Start: start, End: end, State: "unknown", Confidence: "low", AlgorithmVersion: AlgorithmVersion}
	if start.CounterType == "" || start.CounterType != end.CounterType || start.Unit == "" || start.Unit != end.Unit || start.Source != end.Source {
		return result, errors.New("unlike counters cannot be combined")
	}
	if end.Sequence <= start.Sequence {
		result.State = "duplicate_or_out_of_order"
		return result, nil
	}
	if end.Value >= start.Value {
		result.Value = end.Value - start.Value
		result.State = "derived"
		result.Confidence = "high"
		return result, nil
	}
	if wrapAt > 0 && start.Value <= wrapAt && end.Value <= wrapAt {
		result.Value = wrapAt - start.Value + end.Value + 1
		result.State = "wrap"
		result.Confidence = "medium"
		return result, nil
	}
	result.State = "reset"
	result.Confidence = "medium"
	return result, nil
}
