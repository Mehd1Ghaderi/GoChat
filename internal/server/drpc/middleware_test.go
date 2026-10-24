package drpc

import (
	"strings"
	"testing"
)

func TestKeysOpenOnlyTheirCalls(t *testing.T) {
	gateway, voice := strings.Repeat("g", 32), strings.Repeat("v", 32)
	cfg := &Config{GatewayKey: gateway, VoiceServerKey: voice}
	cases := []struct {
		rpc, key string
		want     bool
	}{
		{"/gochat.v1.MessageService/PostMessage", gateway, true},
		{"/gochat.v1.GatewayService/Connect", gateway, true},
		{voiceServerRPC, voice, true},
		// A voice server cannot act for users, nor a gateway pose as a voice
		// server.
		{"/gochat.v1.MessageService/PostMessage", voice, false},
		{"/gochat.v1.GatewayService/Connect", voice, false},
		{voiceServerRPC, gateway, false},
		{voiceServerRPC, "", false},
		{"/gochat.v1.MessageService/PostMessage", "", false},
	}
	for _, c := range cases {
		if got := keyAllows(cfg, c.rpc, c.key); got != c.want {
			t.Errorf("%s with %.1s…: %v, want %v", c.rpc, c.key, got, c.want)
		}
	}
	// Without a voice server key, no key opens the voice servers' link.
	cfg.VoiceServerKey = ""
	if keyAllows(cfg, voiceServerRPC, "") || keyAllows(cfg, voiceServerRPC, gateway) {
		t.Error("the link opened without a voice server key")
	}
}

func TestConfigRefusesASharedVoiceServerKey(t *testing.T) {
	key := strings.Repeat("k", 32)
	for _, voice := range []string{key, "short"} {
		cfg := &Config{Address: "127.0.0.1:7070", GatewayKey: key, VoiceServerKey: voice}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "voice_server_key") {
			t.Errorf("voice_server_key %q: %v", voice, err)
		}
	}
}
