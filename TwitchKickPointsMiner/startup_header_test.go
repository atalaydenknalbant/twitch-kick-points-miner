package twitchchannelpointsminer

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

func TestStartupHeaderUsesCurrentVersion(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerSettings{}, "")
	logger.base.SetOutput(&output)
	miner := &Miner{
		TwitchEnabled: true,
		KickSettings: KickSettings{
			Enabled:  true,
			Accounts: []KickAccountConfig{{Token: "token"}},
		},
		logger: logger,
	}

	miner.logStartupHeader(nil, true)
	logs := output.String()
	want := fmt.Sprintf("%s | v%s", constants.ProductName, constants.Version)
	if !strings.Contains(logs, want) {
		t.Fatalf("startup header missing %q:\n%s", want, logs)
	}
	if strings.Contains(logs, "v2.0.0") {
		t.Fatalf("startup header contains stale version:\n%s", logs)
	}
}
