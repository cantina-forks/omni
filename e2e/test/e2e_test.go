package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/omni-network/omni/contracts/bindings"
	"github.com/omni-network/omni/e2e/app"
	"github.com/omni-network/omni/e2e/docker"
	"github.com/omni-network/omni/e2e/types"
	"github.com/omni-network/omni/e2e/vmcompose"
	"github.com/omni-network/omni/lib/ethclient"
	"github.com/omni-network/omni/lib/log"
	"github.com/omni-network/omni/lib/netconf"
	"github.com/omni-network/omni/lib/tutil"
	"github.com/omni-network/omni/lib/xchain"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	rpctypes "github.com/cometbft/cometbft/rpc/core/types"
	e2e "github.com/cometbft/cometbft/test/e2e/pkg"
	cmttypes "github.com/cometbft/cometbft/types"

	"github.com/stretchr/testify/require"
)

//nolint:gochecknoglobals // This was copied from cometbft/test/e2e/test/e2e_test.go
var (
	endopintsCache  = map[string]xchain.RPCEndpoints{}
	networkCache    = map[string]netconf.Network{}
	deployInfoCache = map[string]types.DeployInfos{}
	testnetCache    = map[string]types.Testnet{}
	testnetCacheMtx = sync.Mutex{}
	blocksCache     = map[string][]*cmttypes.Block{}
	blocksCacheMtx  = sync.Mutex{}
)

// Portal is a struct that contains the chain, client and contract for a portal.
type Portal struct {
	Chain    netconf.Chain
	Client   ethclient.Client
	Contract *bindings.OmniPortal
}

type testFunc struct {
	TestNode    func(*testing.T, e2e.Node, []Portal)
	TestPortal  func(*testing.T, Portal, []Portal)
	TestOmniEVM func(*testing.T, ethclient.Client)
	TestNetwork func(*testing.T, netconf.Network, xchain.RPCEndpoints)
}

func testNode(t *testing.T, fn func(*testing.T, e2e.Node, []Portal)) {
	t.Helper()
	test(t, testFunc{TestNode: fn})
}

func testPortal(t *testing.T, fn func(*testing.T, Portal, []Portal)) {
	t.Helper()
	test(t, testFunc{TestPortal: fn})
}

func testOmniEVM(t *testing.T, fn func(*testing.T, ethclient.Client)) {
	t.Helper()
	test(t, testFunc{TestOmniEVM: fn})
}

func testNetwork(t *testing.T, fn func(*testing.T, netconf.Network, xchain.RPCEndpoints)) {
	t.Helper()
	test(t, testFunc{TestNetwork: fn})
}

// test runs tests for testnet nodes. The callback functions are respectively given a
// single node to test, and a single portal to test, running as a subtest in parallel with other subtests.
//
// The testnet manifest must be given as the envvar E2E_MANIFEST. If not set,
// these tests are skipped so that they're not picked up during normal unit
// test runs. If E2E_NODE is also set, only the specified node is tested,
// otherwise all nodes are tested.
func test(t *testing.T, testFunc testFunc) {
	t.Helper()

	testnet, network, _, endpoints := loadEnv(t)
	nodes := testnet.Nodes

	if name := os.Getenv(app.EnvE2ENode); name != "" {
		node := testnet.LookupNode(name)
		require.NotNil(t, node, "node %q not found in testnet %q", name, testnet.Name)
		nodes = []*e2e.Node{node}
	}

	portals := makePortals(t, network, endpoints)
	log.Info(context.Background(), "Running tests for testnet",
		"testnet", testnet.Name,
		"nodes", len(nodes),
		"portals", len(portals),
	)
	for _, node := range nodes {
		if node.Stateless() {
			continue
		} else if testFunc.TestNode == nil {
			continue
		}

		node := *node
		t.Run(node.Name, func(t *testing.T) {
			t.Parallel()
			testFunc.TestNode(t, node, portals)
		})
	}

	if testFunc.TestPortal != nil {
		for _, portal := range portals {
			t.Run(portal.Chain.Name, func(t *testing.T) {
				t.Parallel()
				testFunc.TestPortal(t, portal, portals)
			})
		}
	}

	if testFunc.TestOmniEVM != nil {
		for _, chain := range network.Chains {
			if !netconf.IsOmniExecution(network.ID, chain.ID) {
				continue
			}

			rpc, err := endpoints.GetByNameOrID(chain.Name, chain.ID)
			require.NoError(t, err)

			client, err := ethclient.Dial(chain.Name, rpc)
			require.NoError(t, err)

			t.Run(chain.Name, func(t *testing.T) {
				t.Parallel()
				testFunc.TestOmniEVM(t, client)
			})
		}
	}

	if testFunc.TestNetwork != nil {
		t.Run("network", func(t *testing.T) {
			t.Parallel()
			testFunc.TestNetwork(t, network, endpoints)
		})
	}
}

// makePortals creates a portal struct for each chain in the network.
func makePortals(t *testing.T, network netconf.Network, endpoints xchain.RPCEndpoints) []Portal {
	t.Helper()
	resp := make([]Portal, 0, len(network.EVMChains()))
	for _, chain := range network.EVMChains() {
		rpc, err := endpoints.GetByNameOrID(chain.Name, chain.ID)
		tutil.RequireNoError(t, err)

		ethClient, err := ethclient.Dial(chain.Name, rpc)
		require.NoError(t, err)

		// create our Omni Portal Contract
		contract, err := bindings.NewOmniPortal(chain.PortalAddress, ethClient)
		require.NoError(t, err)
		require.NotNil(t, contract, "contract is nil")

		resp = append(resp, Portal{
			Chain:    chain,
			Client:   ethClient,
			Contract: contract,
		})
	}

	return resp
}

// loadEnv loads the testnet and network based on env vars.
//
//nolint:unparam // DeployInfos will be used in future.
func loadEnv(t *testing.T) (types.Testnet, netconf.Network, types.DeployInfos, xchain.RPCEndpoints) {
	t.Helper()

	manifestFile := os.Getenv(app.EnvE2EManifest)
	if manifestFile == "" {
		t.Skip(app.EnvE2EManifest + " not set, not an end-to-end test run")
	}
	if !filepath.IsAbs(manifestFile) {
		require.Fail(t, app.EnvE2EManifest+" must be an absolute path", "got", manifestFile)
	}

	ifdType := os.Getenv(app.EnvInfraType)
	ifdFile := os.Getenv(app.EnvInfraFile)
	if ifdType != docker.ProviderName && ifdFile == "" {
		require.Fail(t, app.EnvInfraFile+" not set while INFRASTRUCTURE_TYPE="+ifdType)
	} else if ifdType != docker.ProviderName && !filepath.IsAbs(ifdFile) {
		require.Fail(t, app.EnvInfraFile+" must be an absolute path", "got", ifdFile)
	}

	testnetCacheMtx.Lock()
	defer testnetCacheMtx.Unlock()
	if testnet, ok := testnetCache[manifestFile]; ok {
		return testnet, networkCache[manifestFile], deployInfoCache[manifestFile], endopintsCache[manifestFile]
	}
	m, err := app.LoadManifest(manifestFile)
	require.NoError(t, err)

	var ifd types.InfrastructureData
	switch ifdType {
	case docker.ProviderName:
		ifd, err = docker.NewInfraData(m)
	case vmcompose.ProviderName:
		ifd, err = vmcompose.LoadData(ifdFile)
	default:
		require.Fail(t, "unsupported infrastructure type", ifdType)
	}
	require.NoError(t, err)

	cfg := app.DefinitionConfig{
		ManifestFile: manifestFile,
	}
	testnet, err := app.TestnetFromManifest(context.Background(), m, ifd, cfg)
	require.NoError(t, err)
	testnetCache[manifestFile] = testnet

	networkFile := os.Getenv(app.EnvE2ENetwork)
	if networkFile == "" {
		t.Fatalf(app.EnvE2ENetwork + " not set")
	}
	network, err := netconf.Load(networkFile)
	require.NoError(t, err)
	networkCache[manifestFile] = network

	endpointsFile := os.Getenv(app.EnvE2ERPCEndpoints)
	if endpointsFile == "" {
		t.Fatalf(app.EnvE2ERPCEndpoints + " not set")
	}
	bz, err := os.ReadFile(endpointsFile)
	require.NoError(t, err)
	var endpoints xchain.RPCEndpoints
	require.NoError(t, json.Unmarshal(bz, &endpoints))
	endopintsCache[manifestFile] = endpoints

	var deployInfo types.DeployInfos
	deployInfoFile := os.Getenv(app.EnvE2EDeployInfo)
	if deployInfoFile != "" {
		deployInfo, err = types.LoadDeployInfos(deployInfoFile)
		require.NoError(t, err)
		deployInfoCache[manifestFile] = deployInfo
	}

	return testnet, network, deployInfo, endpoints
}

// fetchBlockChain fetches a complete, up-to-date block history from
// the freshest testnet archive node.
func fetchBlockChain(ctx context.Context, t *testing.T) []*cmttypes.Block {
	t.Helper()

	testnet, _, _, _ := loadEnv(t) //nolint:dogsled // Fine for testing.

	// Find the freshest archive node
	var (
		client *rpchttp.HTTP
		status *rpctypes.ResultStatus
	)
	for _, node := range testnet.ArchiveNodes() {
		c, err := node.Client()
		require.NoError(t, err)
		s, err := c.Status(ctx)
		require.NoError(t, err)
		if status == nil || s.SyncInfo.LatestBlockHeight > status.SyncInfo.LatestBlockHeight {
			client = c
			status = s
		}
	}
	require.NotNil(t, client, "couldn't find an archive node")

	// Fetch blocks. Look for existing block history in the block cache, and
	// extend it with any new blocks that have been produced.
	blocksCacheMtx.Lock()
	defer blocksCacheMtx.Unlock()

	from := status.SyncInfo.EarliestBlockHeight
	to := status.SyncInfo.LatestBlockHeight
	blocks, ok := blocksCache[testnet.Name]
	if !ok {
		blocks = make([]*cmttypes.Block, 0, to-from+1)
	}
	if len(blocks) > 0 {
		from = blocks[len(blocks)-1].Height + 1
	}

	for h := from; h <= to; h++ {
		resp, err := client.Block(ctx, &(h))
		require.NoError(t, ctx.Err(), "Timeout fetching all blocks: %d of %d", h, to)
		require.NoError(t, err)
		require.NotNil(t, resp.Block)
		require.Equal(t, h, resp.Block.Height, "unexpected block height %v", resp.Block.Height)
		blocks = append(blocks, resp.Block)
	}
	require.NotEmpty(t, blocks, "blockchain does not contain any blocks")
	blocksCache[testnet.Name] = blocks

	return blocks
}
