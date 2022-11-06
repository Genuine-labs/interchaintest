package ibc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	transfertypes "github.com/cosmos/ibc-go/v6/modules/apps/transfer/types"
	ibctest "github.com/strangelove-ventures/ibctest/v6"
	"github.com/strangelove-ventures/ibctest/v6/chain/cosmos"
	"github.com/strangelove-ventures/ibctest/v6/ibc"
	"github.com/strangelove-ventures/ibctest/v6/relayer"
	"github.com/strangelove-ventures/ibctest/v6/relayer/rly"
	"github.com/strangelove-ventures/ibctest/v6/testreporter"
	"github.com/strangelove-ventures/ibctest/v6/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type PacketMetadata struct {
	Forward *ForwardMetadata `json:"forward"`
}

type ForwardMetadata struct {
	Receiver       string        `json:"receiver"`
	Port           string        `json:"port"`
	Channel        string        `json:"channel"`
	Timeout        time.Duration `json:"timeout"`
	Retries        *uint8        `json:"retries,omitempty"`
	Next           *string       `json:"next,omitempty"`
	RefundSequence *uint64       `json:"refund_sequence,omitempty"`
}

func TestPacketForwardMiddleware(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	client, network := ibctest.DockerSetup(t)

	rep := testreporter.NewNopReporter()
	eRep := rep.RelayerExecReporter(t)

	ctx := context.Background()

	chainID_A, chainID_B, chainID_C, chainID_D := "chain-a", "chain-b", "chain-c", "chain-d"

	cf := ibctest.NewBuiltinChainFactory(zaptest.NewLogger(t), []*ibctest.ChainSpec{
		{Name: "gaia", Version: "strangelove-forward_middleware_memo_v3", ChainConfig: ibc.ChainConfig{ChainID: chainID_A, GasPrices: "0.0uatom"}},
		{Name: "gaia", Version: "strangelove-forward_middleware_memo_v3", ChainConfig: ibc.ChainConfig{ChainID: chainID_B, GasPrices: "0.0uatom"}},
		{Name: "gaia", Version: "strangelove-forward_middleware_memo_v3", ChainConfig: ibc.ChainConfig{ChainID: chainID_C, GasPrices: "0.0uatom"}},
		{Name: "gaia", Version: "strangelove-forward_middleware_memo_v3", ChainConfig: ibc.ChainConfig{ChainID: chainID_D, GasPrices: "0.0uatom"}},
	})

	chains, err := cf.Chains(t.Name())
	require.NoError(t, err)

	chainA, chainB, chainC, chainD := chains[0].(*cosmos.CosmosChain), chains[1].(*cosmos.CosmosChain), chains[2].(*cosmos.CosmosChain), chains[3].(*cosmos.CosmosChain)

	r := ibctest.NewBuiltinRelayerFactory(
		ibc.CosmosRly,
		zaptest.NewLogger(t),
		relayer.CustomDockerImage("ghcr.io/cosmos/relayer", "andrew-recv_packet_write_ack_separation", rly.RlyDefaultUidGid),
	).Build(
		t, client, network,
	)

	const pathAB = "ab"
	const pathBC = "bc"
	const pathCD = "cd"

	ic := ibctest.NewInterchain().
		AddChain(chainA).
		AddChain(chainB).
		AddChain(chainC).
		AddChain(chainD).
		AddRelayer(r, "relayer").
		AddLink(ibctest.InterchainLink{
			Chain1:  chainA,
			Chain2:  chainB,
			Relayer: r,
			Path:    pathAB,
		}).
		AddLink(ibctest.InterchainLink{
			Chain1:  chainB,
			Chain2:  chainC,
			Relayer: r,
			Path:    pathBC,
		}).
		AddLink(ibctest.InterchainLink{
			Chain1:  chainC,
			Chain2:  chainD,
			Relayer: r,
			Path:    pathCD,
		})

	require.NoError(t, ic.Build(ctx, eRep, ibctest.InterchainBuildOptions{
		TestName:          t.Name(),
		Client:            client,
		NetworkID:         network,
		BlockDatabaseFile: ibctest.DefaultBlockDatabaseFilepath(),

		SkipPathCreation: false,
	}))
	t.Cleanup(func() {
		_ = ic.Close()
	})

	const userFunds = int64(10_000_000_000)
	users := ibctest.GetAndFundTestUsers(t, ctx, t.Name(), userFunds, chainA, chainB, chainC, chainD)

	abChan, err := ibc.GetTransferChannel(ctx, r, eRep, chainID_A, chainID_B)
	require.NoError(t, err)

	baChan := abChan.Counterparty

	cbChan, err := ibc.GetTransferChannel(ctx, r, eRep, chainID_C, chainID_B)
	require.NoError(t, err)

	bcChan := cbChan.Counterparty

	dcChan, err := ibc.GetTransferChannel(ctx, r, eRep, chainID_D, chainID_C)
	require.NoError(t, err)

	cdChan := dcChan.Counterparty

	// Start the relayer on both paths
	err = r.StartRelayer(ctx, eRep, pathAB, pathBC, pathCD)
	require.NoError(t, err)

	t.Cleanup(
		func() {
			err := r.StopRelayer(ctx, eRep)
			if err != nil {
				t.Logf("an error occured while stopping the relayer: %s", err)
			}
		},
	)

	// Get original account balances
	userA, userB, userC, userD := users[0], users[1], users[2], users[3]

	const transferAmount int64 = 100000

	// Compose the prefixed denoms and ibc denom for asserting balances
	firstHopDenom := transfertypes.GetPrefixedDenom(baChan.PortID, baChan.ChannelID, chainA.Config().Denom)
	secondHopDenom := transfertypes.GetPrefixedDenom(cbChan.PortID, cbChan.ChannelID, firstHopDenom)
	thirdHopDenom := transfertypes.GetPrefixedDenom(dcChan.PortID, dcChan.ChannelID, secondHopDenom)

	firstHopDenomTrace := transfertypes.ParseDenomTrace(firstHopDenom)
	secondHopDenomTrace := transfertypes.ParseDenomTrace(secondHopDenom)
	thirdHopDenomTrace := transfertypes.ParseDenomTrace(thirdHopDenom)

	firstHopIBCDenom := firstHopDenomTrace.IBCDenom()
	secondHopIBCDenom := secondHopDenomTrace.IBCDenom()
	thirdHopIBCDenom := thirdHopDenomTrace.IBCDenom()

	fmt.Printf("first hop denom: %s, first hop denom trace: %+v, first hop ibc denom: %s", firstHopDenom, firstHopDenomTrace, firstHopIBCDenom)
	fmt.Printf("second hop denom: %s, second hop denom trace: %+v, second hop ibc denom: %s", secondHopDenom, secondHopDenomTrace, secondHopIBCDenom)
	fmt.Printf("third hop denom: %s, third hop denom trace: %+v, third hop ibc denom: %s", thirdHopDenom, thirdHopDenomTrace, thirdHopIBCDenom)

	t.Run("multi-hop a->b->c->d", func(t *testing.T) {
		// Send packet from Chain A->Chain B->Chain C->Chain D

		transfer := ibc.WalletAmount{
			Address: userB.Bech32Address(chainB.Config().Bech32Prefix),
			Denom:   chainA.Config().Denom,
			Amount:  transferAmount,
		}

		secondHopMetadata := &PacketMetadata{
			Forward: &ForwardMetadata{
				Receiver: userD.Bech32Address(chainD.Config().Bech32Prefix),
				Channel:  cdChan.ChannelID,
				Port:     cdChan.PortID,
			},
		}
		nextBz, err := json.Marshal(secondHopMetadata)
		require.NoError(t, err)
		next := string(nextBz)

		firstHopMetadata := &PacketMetadata{
			Forward: &ForwardMetadata{
				Receiver: userC.Bech32Address(chainC.Config().Bech32Prefix),
				Channel:  bcChan.ChannelID,
				Port:     bcChan.PortID,
				Next:     &next,
			},
		}

		memo, err := json.Marshal(firstHopMetadata)
		require.NoError(t, err)

		chainAHeight, err := chainA.Height(ctx)
		require.NoError(t, err)

		transferTx, err := chainA.SendIBCTransfer(ctx, abChan.ChannelID, userA.KeyName, transfer, nil, string(memo))
		require.NoError(t, err)

		_, err = testutil.PollForAck(ctx, chainA, chainAHeight, chainAHeight+30, transferTx.Packet)
		require.NoError(t, err)

		chainABalance, err := chainA.GetBalance(ctx, userA.Bech32Address(chainA.Config().Bech32Prefix), chainA.Config().Denom)
		require.NoError(t, err)
		require.Equal(t, userFunds-transferAmount, chainABalance)

		chainBBalance, err := chainB.GetBalance(ctx, userB.Bech32Address(chainB.Config().Bech32Prefix), firstHopIBCDenom)
		require.NoError(t, err)
		require.Equal(t, int64(0), chainBBalance)

		chainCBalance, err := chainC.GetBalance(ctx, userC.Bech32Address(chainC.Config().Bech32Prefix), secondHopIBCDenom)
		require.NoError(t, err)
		require.Equal(t, int64(0), chainCBalance)

		chainDBalance, err := chainD.GetBalance(ctx, userD.Bech32Address(chainD.Config().Bech32Prefix), thirdHopIBCDenom)
		require.NoError(t, err)
		require.Equal(t, transferAmount, chainDBalance)
	})

	t.Run("multi-hop denom unwind d->c->b->a", func(t *testing.T) {
		// Send packet back from Chain D->Chain C->Chain B->Chain A
		transfer := ibc.WalletAmount{
			Address: userC.Bech32Address(chainC.Config().Bech32Prefix),
			Denom:   thirdHopIBCDenom,
			Amount:  transferAmount,
		}

		secondHopMetadata := &PacketMetadata{
			Forward: &ForwardMetadata{
				Receiver: userA.Bech32Address(chainA.Config().Bech32Prefix),
				Channel:  baChan.ChannelID,
				Port:     baChan.PortID,
			},
		}

		nextBz, err := json.Marshal(secondHopMetadata)
		require.NoError(t, err)

		next := string(nextBz)

		firstHopMetadata := &PacketMetadata{
			Forward: &ForwardMetadata{
				Receiver: userB.Bech32Address(chainB.Config().Bech32Prefix),
				Channel:  cbChan.ChannelID,
				Port:     cbChan.PortID,
				Next:     &next,
			},
		}

		memo, err := json.Marshal(firstHopMetadata)
		require.NoError(t, err)

		chainDHeight, err := chainD.Height(ctx)
		require.NoError(t, err)

		transferTx, err := chainD.SendIBCTransfer(ctx, dcChan.ChannelID, userD.KeyName, transfer, nil, string(memo))
		require.NoError(t, err)

		_, err = testutil.PollForAck(ctx, chainD, chainDHeight, chainDHeight+30, transferTx.Packet)
		require.NoError(t, err)

		chainDBalance, err := chainD.GetBalance(ctx, userD.Bech32Address(chainD.Config().Bech32Prefix), thirdHopIBCDenom)
		require.NoError(t, err)
		require.Equal(t, int64(0), chainDBalance)

		chainCBalance, err := chainC.GetBalance(ctx, userC.Bech32Address(chainC.Config().Bech32Prefix), secondHopIBCDenom)
		require.NoError(t, err)
		require.Equal(t, int64(0), chainCBalance)

		chainBBalance, err := chainB.GetBalance(ctx, userB.Bech32Address(chainB.Config().Bech32Prefix), firstHopIBCDenom)
		require.NoError(t, err)
		require.Equal(t, int64(0), chainBBalance)

		chainABalance, err := chainA.GetBalance(ctx, userA.Bech32Address(chainA.Config().Bech32Prefix), chainA.Config().Denom)
		require.NoError(t, err)
		require.Equal(t, userFunds, chainABalance)
	})

	t.Run("forward ack error refund", func(t *testing.T) {
		// Send a malformed packet with invalid receiver address from Chain A->Chain B->Chain C
		// This should succeed in the first hop and fail to make the second hop; funds should then be refunded to Chain A.
		transfer := ibc.WalletAmount{
			Address: userB.Bech32Address(chainB.Config().Bech32Prefix),
			Denom:   chainA.Config().Denom,
			Amount:  transferAmount,
		}

		metadata := &PacketMetadata{
			Forward: &ForwardMetadata{
				Receiver: "xyz1t8eh66t2w5k67kwurmn5gqhtq6d2ja0vp7jmmq", // malformed receiver address on Chain C
				Channel:  bcChan.ChannelID,
				Port:     bcChan.PortID,
			},
		}

		memo, err := json.Marshal(metadata)
		require.NoError(t, err)

		chainAHeight, err := chainA.Height(ctx)
		require.NoError(t, err)

		transferTx, err := chainA.SendIBCTransfer(ctx, abChan.ChannelID, userA.KeyName, transfer, nil, string(memo))
		require.NoError(t, err)

		_, err = testutil.PollForAck(ctx, chainA, chainAHeight, chainAHeight+25, transferTx.Packet)
		require.NoError(t, err)

		chainABalance, err := chainA.GetBalance(ctx, userA.Bech32Address(chainA.Config().Bech32Prefix), chainA.Config().Denom)
		require.NoError(t, err)
		require.Equal(t, userFunds, chainABalance)

		chainBBalance, err := chainB.GetBalance(ctx, userB.Bech32Address(chainB.Config().Bech32Prefix), firstHopIBCDenom)
		require.NoError(t, err)
		require.Equal(t, int64(0), chainBBalance)

		chainCBalance, err := chainC.GetBalance(ctx, userC.Bech32Address(chainC.Config().Bech32Prefix), secondHopIBCDenom)
		require.NoError(t, err)
		require.Equal(t, int64(0), chainCBalance)
	})

	t.Run("forward timeout refund", func(t *testing.T) {
		// Send packet from Chain A->Chain B->Chain C with the timeout so low for B->C transfer that it can not make it from B to C, which should result in a refund from B to A after two retries.
		transfer := ibc.WalletAmount{
			Address: userB.Bech32Address(chainB.Config().Bech32Prefix),
			Denom:   chainA.Config().Denom,
			Amount:  transferAmount,
		}

		retries := uint8(2)
		metadata := &PacketMetadata{
			Forward: &ForwardMetadata{
				Receiver: userC.Bech32Address(chainC.Config().Bech32Prefix),
				Channel:  bcChan.ChannelID,
				Port:     bcChan.PortID,
				Retries:  &retries,
				Timeout:  1 * time.Second,
			},
		}

		memo, err := json.Marshal(metadata)
		require.NoError(t, err)

		chainAHeight, err := chainA.Height(ctx)
		require.NoError(t, err)

		transferTx, err := chainA.SendIBCTransfer(ctx, abChan.ChannelID, userA.KeyName, transfer, nil, string(memo))
		require.NoError(t, err)

		_, err = testutil.PollForAck(ctx, chainA, chainAHeight, chainAHeight+25, transferTx.Packet)
		require.NoError(t, err)

		chainABalance, err := chainA.GetBalance(ctx, userA.Bech32Address(chainA.Config().Bech32Prefix), chainA.Config().Denom)
		require.NoError(t, err)
		require.Equal(t, userFunds, chainABalance)

		chainBBalance, err := chainB.GetBalance(ctx, userB.Bech32Address(chainB.Config().Bech32Prefix), firstHopIBCDenom)
		require.NoError(t, err)
		require.Equal(t, int64(0), chainBBalance)

		chainCBalance, err := chainC.GetBalance(ctx, userC.Bech32Address(chainC.Config().Bech32Prefix), secondHopIBCDenom)
		require.NoError(t, err)
		require.Equal(t, int64(0), chainCBalance)
	})

	t.Run("multi-hop ack error refund", func(t *testing.T) {
		// Send a malformed packet with invalid receiver address from Chain A->Chain B->Chain C->Chain D
		// This should succeed in the first hop and second hop, then fail to make the third hop.
		// Funds should be refunded to Chain B and then to Chain A via failed acknowledgements.
		transfer := ibc.WalletAmount{
			Address: userB.Bech32Address(chainB.Config().Bech32Prefix),
			Denom:   chainA.Config().Denom,
			Amount:  transferAmount,
		}

		secondHopMetadata := &PacketMetadata{
			Forward: &ForwardMetadata{
				Receiver: "xyz1t8eh66t2w5k67kwurmn5gqhtq6d2ja0vp7jmmq", // malformed receiver address on chain D
				Channel:  cdChan.ChannelID,
				Port:     cdChan.PortID,
			},
		}

		nextBz, err := json.Marshal(secondHopMetadata)
		require.NoError(t, err)

		next := string(nextBz)

		firstHopMetadata := &PacketMetadata{
			Forward: &ForwardMetadata{
				Receiver: userC.Bech32Address(chainC.Config().Bech32Prefix),
				Channel:  bcChan.ChannelID,
				Port:     bcChan.PortID,
				Next:     &next,
			},
		}

		memo, err := json.Marshal(firstHopMetadata)
		require.NoError(t, err)

		chainAHeight, err := chainA.Height(ctx)
		require.NoError(t, err)

		transferTx, err := chainA.SendIBCTransfer(ctx, abChan.ChannelID, userA.KeyName, transfer, nil, string(memo))
		require.NoError(t, err)
		_, err = testutil.PollForAck(ctx, chainA, chainAHeight, chainAHeight+25, transferTx.Packet)
		require.NoError(t, err)

		chainABalance, err := chainA.GetBalance(ctx, userA.Bech32Address(chainA.Config().Bech32Prefix), chainA.Config().Denom)
		require.NoError(t, err)
		require.Equal(t, userFunds, chainABalance)

		chainBBalance, err := chainB.GetBalance(ctx, userB.Bech32Address(chainB.Config().Bech32Prefix), firstHopIBCDenom)
		require.NoError(t, err)
		require.Equal(t, int64(0), chainBBalance)

		chainCBalance, err := chainC.GetBalance(ctx, userC.Bech32Address(chainC.Config().Bech32Prefix), secondHopIBCDenom)
		require.NoError(t, err)
		require.Equal(t, int64(0), chainCBalance)
	})
}
