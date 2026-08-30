package main

import (
	"fmt"
	"io"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

const startupArt = `
  _______        _ _       _       _  __ _      _
 |__   __|      (_) |     | |     | |/ /(_)    | |
    | |_      ___| |_ ___ | |__   | ' /  _  ___| | __
    | \ \ /\ / / | __/ __|| '_ \  |  <  | |/ __| |/ /
    | |\ V  V /| | || (__ | | | | | . \ | | (__|   <
    |_| \_/\_/ |_|\__\___||_| |_| |_|\_\|_|\___|_|\_\

  _____      _       _         __  __ _
 |  __ \    (_)     | |       |  \/  (_)
 | |__) |__  _ _ __ | |_ ___  | \  / |_ _ __   ___ _ __
 |  ___/ _ \| | '_ \| __/ __| | |\/| | | '_ \ / _ \ '__|
 | |  | (_) | | | | | |_\__ \ | |  | | | | | |  __/ |
 |_|   \___/|_|_| |_|\__|___/ |_|  |_|_|_| |_|\___|_|

                    [ MINING ONLINE ]
                         \ | /
                          \|/
                           V
`

func writeStartupArt(output io.Writer) {
	if output == nil {
		return
	}
	fmt.Fprintf(output, "%s%s%s", constants.ColorPurple, startupArt, constants.ColorReset)
	fmt.Fprintf(output, "%s%s%s\n\n", constants.ColorGreen, constants.ProductName, constants.ColorReset)
}
