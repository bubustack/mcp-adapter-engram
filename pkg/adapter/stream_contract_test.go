package adapter

import (
	"bytes"
	"testing"

	sdkengram "github.com/bubustack/bubu-sdk-go/engram"
)

func TestDecodeStreamInputsPrefersPayloadOverBinary(t *testing.T) {
	msg := sdkengram.NewInboundMessage(sdkengram.StreamMessage{
		Payload: []byte(`{"action":"call","tool":"payload"}`),
		Binary: &sdkengram.BinaryFrame{
			Payload:  []byte(`{"action":"call","tool":"binary"}`),
			MimeType: "application/json",
		},
	})

	inputs, skip, err := decodeStreamInputs(msg)
	if err != nil {
		t.Fatalf("decodeStreamInputs returned error: %v", err)
	}
	if skip {
		t.Fatal("expected payload-backed input to be processed")
	}
	if inputs.Tool != "payload" {
		t.Fatalf("expected payload tool to win, got %q", inputs.Tool)
	}
}

func TestNewStreamResponsePublishesStructuredJSONAcrossChannels(t *testing.T) {
	msg := sdkengram.NewInboundMessage(sdkengram.StreamMessage{
		Metadata: map[string]string{"type": "demo"},
	})
	payload := []byte(`{"data":{"ok":true}}`)

	response := newStreamResponse(msg, payload)
	if !bytes.Equal(response.Inputs, payload) {
		t.Fatalf("expected inputs to mirror payload, got %q", string(response.Inputs))
	}
	if !bytes.Equal(response.Payload, payload) {
		t.Fatalf("expected payload to be preserved, got %q", string(response.Payload))
	}
	if response.Binary == nil {
		t.Fatal("expected binary mirror for structured JSON")
	}
	if !bytes.Equal(response.Binary.Payload, payload) {
		t.Fatalf("expected binary payload to mirror payload, got %q", string(response.Binary.Payload))
	}
}
