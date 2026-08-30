package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

func TestWriteStartupArt(t *testing.T) {
	var output bytes.Buffer
	writeStartupArt(&output)
	text := output.String()
	if !strings.Contains(text, "| |__) |__") || !strings.Contains(text, "|  \\/  (_)") || !strings.Contains(text, constants.ProductName) {
		t.Fatalf("startup art is missing project identity")
	}
}
