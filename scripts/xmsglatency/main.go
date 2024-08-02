package main

import (
	libcmd "github.com/omni-network/omni/lib/cmd"
	"github.com/omni-network/omni/scripts/xmsglatency/cmd"
)

func main() {
	libcmd.Main(cmd.New())
}
