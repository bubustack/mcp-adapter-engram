package main

import (
	"fmt"
	"testing"

	"github.com/bubustack/bubu-sdk-go/conformance"
	"github.com/bubustack/mcp-adapter-engram/pkg/adapter"
	"github.com/bubustack/mcp-adapter-engram/pkg/config"
)

func TestConformance(t *testing.T) {
	suite := conformance.BatchSuite[config.Config, adapter.Inputs]{
		Engram:      adapter.New(),
		Config:      config.Config{},
		Inputs:      adapter.Inputs{},
		ExpectError: true,
		ValidateError: func(err error) error {
			if err == nil {
				return nil
			}
			if err.Error() != "unsupported transport: " {
				return fmt.Errorf("unexpected conformance error: %w", err)
			}
			return nil
		},
	}
	suite.Run(t)
}
