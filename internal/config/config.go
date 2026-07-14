package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/policy"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

const MaxAllowedInputBytes = 1 << 20
const maxConfigBytes = 64 * 1024

// Config contains resolved CLI settings and their provenance.
type Config struct {
	ExecutionPolicy contracts.ExecutionPolicy `json:"execution_policy"`
	HostPermission  policy.HostPermission     `json:"host_permission"`
	MaxInputBytes   int                       `json:"max_input_bytes"`
	Timeout         time.Duration             `json:"-"`
	TimeoutText     string                    `json:"timeout"`
	Format          string                    `json:"format"`
	Provenance      map[string]string         `json:"-"`
}

// Default returns the safe, local-only configuration.
func Default() Config {
	return Config{
		ExecutionPolicy: contracts.ExecutionPolicyImproveOnly,
		HostPermission:  policy.HostUnknown,
		MaxInputBytes:   16 * 1024,
		Timeout:         2 * time.Second,
		TimeoutText:     "2s",
		Format:          "text",
		Provenance: map[string]string{
			"execution_policy": "default", "host_permission": "default",
			"max_input_bytes": "default", "timeout": "default", "format": "default",
		},
	}
}

func LoadFile(ctx context.Context, path string) (Config, error) {
	cfg := Default()
	if ctx.Err() != nil {
		return Config{}, configError(
			contracts.ErrorCodeBudgetExhausted,
			"configuration read cancelled or timed out",
			true,
		)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Config{}, contracts.NewError(contracts.ErrorCodeNotFound, "configuration file unavailable", "config", false)
	}
	if !info.Mode().IsRegular() {
		return Config{}, configError(
			contracts.ErrorCodeInvalidSchema,
			"configuration must be a regular file",
			false,
		)
	}
	source, err := os.Open(path)
	if err != nil {
		return Config{}, contracts.NewError(contracts.ErrorCodeNotFound, "configuration file unavailable", "config", false)
	}
	data, err := io.ReadAll(io.LimitReader(source, maxConfigBytes+1))
	closeErr := source.Close()
	if err != nil {
		return Config{}, contracts.NewError(contracts.ErrorCodeInternal, "configuration read failed", "config", true)
	}
	if closeErr != nil {
		return Config{}, contracts.NewError(contracts.ErrorCodeInternal, "configuration close failed", "config", true)
	}
	if ctx.Err() != nil {
		return Config{}, configError(
			contracts.ErrorCodeBudgetExhausted,
			"configuration read cancelled or timed out",
			true,
		)
	}
	if len(data) > maxConfigBytes {
		return Config{}, configError(
			contracts.ErrorCodeInvalidSchema,
			"configuration exceeds 65536 bytes",
			false,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var fileConfig struct {
		ExecutionPolicy *contracts.ExecutionPolicy `json:"execution_policy"`
		HostPermission  *policy.HostPermission     `json:"host_permission"`
		MaxInputBytes   *int                       `json:"max_input_bytes"`
		Timeout         *string                    `json:"timeout"`
		Format          *string                    `json:"format"`
	}
	if err := decoder.Decode(&fileConfig); err != nil {
		return Config{}, contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid configuration", "config", false)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return Config{}, configError(
			contracts.ErrorCodeInvalidSchema,
			"configuration contains multiple values",
			false,
		)
	} else if !errors.Is(err, io.EOF) {
		return Config{}, configError(
			contracts.ErrorCodeInvalidSchema,
			"invalid trailing configuration data",
			false,
		)
	}
	if fileConfig.ExecutionPolicy != nil {
		cfg.ExecutionPolicy = *fileConfig.ExecutionPolicy
		cfg.Provenance["execution_policy"] = "file"
	}
	if fileConfig.HostPermission != nil {
		cfg.HostPermission = *fileConfig.HostPermission
		cfg.Provenance["host_permission"] = "file"
	}
	if fileConfig.MaxInputBytes != nil {
		cfg.MaxInputBytes = *fileConfig.MaxInputBytes
		cfg.Provenance["max_input_bytes"] = "file"
	}
	if fileConfig.Timeout != nil {
		cfg.TimeoutText = *fileConfig.Timeout
		cfg.Provenance["timeout"] = "file"
	}
	if fileConfig.Format != nil {
		cfg.Format = *fileConfig.Format
		cfg.Provenance["format"] = "file"
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks resolved settings and parses their typed values.
func (c *Config) Validate() error {
	if !policy.ValidExecutionPolicy(c.ExecutionPolicy) {
		return contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid execution policy", "execution_policy", false)
	}
	if !policy.ValidHostPermission(c.HostPermission) {
		return contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid host permission", "host_permission", false)
	}
	if c.MaxInputBytes < 1 || c.MaxInputBytes > MaxAllowedInputBytes {
		message := fmt.Sprintf(
			"max_input_bytes must be between 1 and %d",
			MaxAllowedInputBytes,
		)
		return contracts.NewError(
			contracts.ErrorCodeInvalidSchema,
			message,
			"max_input_bytes",
			false,
		)
	}
	if c.TimeoutText != "" {
		duration, err := time.ParseDuration(c.TimeoutText)
		if err != nil || duration <= 0 || duration > time.Minute {
			return contracts.NewError(
				contracts.ErrorCodeInvalidSchema,
				"timeout must be positive and at most 1m",
				"timeout",
				false,
			)
		}
		c.Timeout = duration
	}
	if c.Format != "text" && c.Format != "json" {
		return contracts.NewError(contracts.ErrorCodeInvalidSchema, "format must be text or json", "format", false)
	}
	return nil
}

func configError(code contracts.ErrorCode, message string, retryable bool) error {
	return contracts.NewError(code, message, "config", retryable)
}
