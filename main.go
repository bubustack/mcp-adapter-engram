package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	sdk "github.com/bubustack/bubu-sdk-go"
	"github.com/bubustack/mcp-adapter-engram/pkg/adapter"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := sdk.Start(ctx, adapter.New()); err != nil {
		log.Fatalf("mcp-adapter engram failed: %v", err)
	}
}
