// Package eval records reproducible audit evaluation decisions.
package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/nijanthan-dev/codex-prompt-better/internal/replay"
)

type Run struct {
	Version      string            `json:"version"`
	FixtureHash  string            `json:"fixture_hash"`
	ConfigHash   string            `json:"config_hash"`
	ModelHash    string            `json:"model_hash"`
	CompilerHash string            `json:"compiler_hash"`
	PolicyHash   string            `json:"policy_hash"`
	MetricHash   string            `json:"metric_hash"`
	Comparison   replay.Comparison `json:"comparison"`
	Decision     string            `json:"decision"`
	RunHash      string            `json:"run_hash"`
}

type Components struct {
	Model    any
	Compiler any
	Policy   any
	Metrics  any
}

func NewRun(version string, fixture, config any, components Components,
	comparison replay.Comparison,
) (Run, error) {
	if version == "" {
		return Run{}, errors.New("evaluation version required")
	}
	fixtureHash, err := digest(fixture)
	if err != nil {
		return Run{}, err
	}
	configHash, err := digest(config)
	if err != nil {
		return Run{}, err
	}
	modelHash, err := digest(components.Model)
	if err != nil {
		return Run{}, err
	}
	compilerHash, err := digest(components.Compiler)
	if err != nil {
		return Run{}, err
	}
	policyHash, err := digest(components.Policy)
	if err != nil {
		return Run{}, err
	}
	metricHash, err := digest(components.Metrics)
	if err != nil {
		return Run{}, err
	}
	decision := "hold"
	if comparison.Outcome == "improved" && comparison.QualityGateState == "pass" {
		decision = "promote"
	}
	if comparison.Outcome == "regressed" || comparison.QualityGateState == "fail" {
		decision = "rollback"
	}
	run := Run{
		Version: version, FixtureHash: fixtureHash, ConfigHash: configHash,
		ModelHash: modelHash, CompilerHash: compilerHash,
		PolicyHash: policyHash, MetricHash: metricHash,
		Comparison: comparison, Decision: decision,
	}
	run.RunHash, err = digest(run)
	return run, err
}

func digest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("encode evaluation input")
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
