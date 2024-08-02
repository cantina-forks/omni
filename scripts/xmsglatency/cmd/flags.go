package cmd

import (
	"github.com/spf13/pflag"
)

func bindFlags(flags *pflag.FlagSet, cfg *Config) {
	flags.StringVar((*string)(&cfg.Network), "network", string(cfg.Network), "Network ID")
	flags.StringToStringVar(&cfg.RPCs, "rpcs", cfg.RPCs, "Chain rpc endpoints: '<chain1>=<url1>,<url2>'")
}
