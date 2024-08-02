package cmd

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	libcmd "github.com/omni-network/omni/lib/cmd"
	"github.com/omni-network/omni/lib/errors"
	"github.com/omni-network/omni/lib/evmchain"
	"github.com/omni-network/omni/lib/log"
	"github.com/omni-network/omni/lib/netconf"
	"github.com/omni-network/omni/lib/xchain"
	"github.com/omni-network/omni/lib/xchain/connect"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

type Config struct {
	Network netconf.ID
	RPCs    map[string]string
}

func (c *Config) Validate() error {
	if err := c.Network.Verify(); err != nil {
		return err
	}

	return nil
}

type timedXMsg struct {
	xchain.Msg
	timestamp time.Time
}

type timedXReceipt struct {
	xchain.Receipt
	timestamp time.Time
}

type latencyStats struct {
	totalSent     int     // total messages sent
	totalReceived int     // total messages received
	avg           float64 // average latency
}

func New() *cobra.Command {
	cfg := &Config{}
	logCfg := log.DefaultConfig()

	cmd := libcmd.NewRootCmd("xmsglatency", "Monitor xmsg latency")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		if _, err := log.Init(ctx, logCfg); err != nil {
			return err
		}

		if err := libcmd.LogFlags(ctx, cmd.Flags()); err != nil {
			return err
		}

		if err := cfg.Validate(); err != nil {
			return err
		}

		endpoints := make(xchain.RPCEndpoints)
		for chain, rpc := range cfg.RPCs {
			endpoints[chain] = rpc
		}

		log.Info(ctx, "Endpoints", "endpoints", endpoints)

		connector, err := connect.New(ctx, cfg.Network, endpoints)
		if err != nil {
			return err
		}

		// now use noop logger
		// BEWARE: this hides xprovider error logs (in the name of pretty tabular output)
		ctx = log.WithNoopLogger(ctx)

		xprov := connector.XProvider
		network := connector.Network
		cchain, ok := network.OmniConsensusChain()
		if !ok {
			return errors.New("omni consensus chain not found")
		}

		var msgStore sync.Map
		var recStore sync.Map

		for _, chain := range connector.Network.Chains {
			if chain.ID != evmchain.IDArbSepolia && chain.ID != evmchain.IDOpSepolia {
				continue
			}

			req := xchain.ProviderRequest{
				ChainID:   chain.ID,
				ConfLevel: xchain.ConfLatest,
			}

			fmt.Println("Starting stream for chain", network.ChainName(chain.ID))

			err := xprov.StreamAsync(ctx, req, func(ctx context.Context, block xchain.Block) error {
				for _, msg := range block.Msgs {
					if msg.SourceChainID == cchain.ID {
						continue
					}

					msgStore.Store(msg.MsgID, timedXMsg{msg, block.Timestamp})
				}

				for _, receipt := range block.Receipts {
					if receipt.SourceChainID == cchain.ID {
						continue
					}

					recStore.Store(receipt.MsgID, timedXReceipt{receipt, block.Timestamp})
				}

				return nil
			})

			if err != nil {
				return err
			}
		}

		// log stats periodically
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		table := tablewriter.NewWriter(cmd.OutOrStdout())
		table.SetHeader([]string{"Stream", "Sent", "Recevied", "Avg Latency"})
		table.SetAutoWrapText(false)
		table.SetAutoFormatHeaders(true)
		table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
		table.SetAlignment(tablewriter.ALIGN_LEFT)
		table.SetCenterSeparator("")
		table.SetColumnSeparator("")
		table.SetRowSeparator("")
		table.SetHeaderLine(false)
		table.SetBorder(false)
		table.SetTablePadding("\t") // pad with tabs
		table.SetNoWhiteSpace(true)

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				// Clear the screen
				fmt.Print("\033[H\033[2J")
				table.ClearRows()

				stats := make(map[xchain.StreamID]*latencyStats)

				msgStore.Range(func(key, value any) bool {
					msg := value.(timedXMsg)

					st := stats[msg.StreamID]
					if st == nil {
						stats[msg.StreamID] = &latencyStats{}
						st = stats[msg.StreamID]
					}

					st.totalSent++

					r, ok := recStore.Load(msg.MsgID)
					if !ok {
						return true
					}

					receipt, ok := r.(timedXReceipt)
					if !ok {
						return true
					}

					st.totalReceived++
					st.avg = (st.avg*float64(st.totalReceived-1) + latency(receipt.timestamp, msg.timestamp)) / float64(st.totalReceived)

					return true
				})

				for streamID, st := range stats {
					table.Append([]string{
						network.StreamName(streamID),
						strconv.Itoa(st.totalSent),
						strconv.Itoa(st.totalReceived),
						strconv.FormatFloat(st.avg, 'f', 2, 64),
					})
				}

				table.Render()
			}
		}
	}

	bindFlags(cmd.Flags(), cfg)
	log.BindFlags(cmd.PersistentFlags(), &logCfg)

	return cmd
}

// returns latency in seconds.
func latency(receipt time.Time, msg time.Time) float64 {
	return float64(receipt.Unix() - msg.Unix())
}
