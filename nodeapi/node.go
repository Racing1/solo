package nodeapi

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/flexpool/solo/jsonrpc"
	"github.com/flexpool/solo/log"
	"github.com/flexpool/solo/types"
	"github.com/flexpool/solo/utils"

	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Node is the base OpenEthereum API struct
type Node struct {
	httpRPCEndpoint string
	Type            types.NodeType
}

// Block is a block body representation
type Block struct {
	Number       string   `json:"number"`
	Hash         string   `json:"hash"`
	Nonce        string   `json:"nonce"`
	Difficulty   string   `json:"difficulty"`
	Timestamp    string   `json:"timestamp"`
	Transactions []string `json:"transactions"`
}

// NewNode creates a new Node instance
func NewNode(httpRPCEndpoint string) (*Node, error) {
	if _, err := url.Parse(httpRPCEndpoint); err != nil {
		return nil, errors.New("invalid HTTP URL")
	}

	node := Node{httpRPCEndpoint: httpRPCEndpoint}

	// Detecting node type
	clientVersion, err := node.ClientVersion()
	if err != nil {
		return nil, errors.Wrap(err, "failed detecting node type")
	}

	clientVersionSplitted := strings.Split(clientVersion, "/")
	if len(clientVersionSplitted) < 1 {
		log.Logger.WithFields(logrus.Fields{
			"prefix": "node",
		}).Warn("Unable to detect node type: received invalid client version. Falling back to Geth.")
	} else {
		clientVersion = clientVersionSplitted[0]
		switch strings.ToLower(clientVersion) {
		case "geth":
			node.Type = types.GethNode
		case "openethereum":
			node.Type = types.OpenEthereumNode
		default:
			log.Logger.WithFields(logrus.Fields{
				"prefix": "node",
			}).Warn("Unknown node \"" + clientVersion + "\". Falling back to Geth.")
		}
	}

	log.Logger.WithFields(logrus.Fields{
		"prefix": "node",
	}).Info("Configured for " + types.NodeStringMap[node.Type] + " node")

	return &node, nil
}

func (n *Node) makeHTTPRPCRequest(method string, params interface{}) (interface{}, error) {
	req := jsonrpc.MarshalRequest(jsonrpc.Request{
		JSONRPCVersion: jsonrpc.Version,
		ID:             rand.Intn(99999999),
		Method:         method,
		Params:         params,
	})

	response, err := http.Post(n.httpRPCEndpoint, "application/json", bytes.NewBuffer(req))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	data, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	// Additional error check
	parsedData, err := jsonrpc.UnmarshalResponse(data)
	if err != nil {
		return nil, errors.Wrap(err, "unable to unmarshal node's response ("+string(data)+")")
	}

	if parsedData.Error != nil {
		return nil, errors.New("unexpected node response: " + string(data))
	}

	return parsedData.Result, nil
}

// SubmitWork delegates to `eth_submitWork` API method, and submits work
func (n *Node) SubmitWork(work []string) (bool, error) {
	data, err := n.makeHTTPRPCRequest("eth_submitWork", work)
	if err != nil {
		return false, err
	}

	return data.(bool), nil
}

// BlockNumber delegates to `eth_blockNumber` API method, and returns the current block number
func (n *Node) BlockNumber() (uint64, error) {
	data, err := n.makeHTTPRPCRequest("eth_blockNumber", nil)
	if err != nil {
		return 0, err
	}

	blockNumber, err := strconv.ParseUint(utils.Clear0x(data.(string)), 16, 64)
	if err != nil {
		return 0, err
	}

	return blockNumber, nil
}

// ClientVersion delegates to `eth_blockNumber` API method, and returns the current block number
func (n *Node) ClientVersion() (string, error) {
	data, err := n.makeHTTPRPCRequest("web3_clientVersion", nil)
	if err != nil {
		return "", err
	}

	return data.(string), nil
}

// GetBlockByNumber delegates to `eth_getBlockByNumber` RPC method, and returns block by number
func (n *Node) GetBlockByNumber(blockNumber uint64) (Block, error) {
	data, err := n.makeHTTPRPCRequest("eth_getBlockByNumber", []interface{}{fmt.Sprintf("0x%x", blockNumber), false})
	if err != nil {
		return Block{}, err
	}

	var block Block
	err = mapstructure.Decode(data, &block)
	return block, err
}

// GetUncleByBlockNumberAndIndex delegates to `eth_getUncleByBlockNumberAndIndex` RPC method, and returns uncle by block number and index
func (n *Node) GetUncleByBlockNumberAndIndex(blockNumber uint64, uncleIndex int) (Block, error) {
	data, err := n.makeHTTPRPCRequest("eth_getUncleByBlockNumberAndIndex", []interface{}{fmt.Sprintf("0x%x", blockNumber), fmt.Sprintf("0x%x", uncleIndex)})
	if err != nil {
		return Block{}, err
	}

	var block Block
	err = mapstructure.Decode(data, &block)
	return block, err
}

// GetUncleCountByBlockNumber delegates to `eth_getUncleCountByBlockNumber` RPC method, and returns amount of uncles by given block number
func (n *Node) GetUncleCountByBlockNumber(blockNumber uint64) (uint64, error) {
	data, err := n.makeHTTPRPCRequest("eth_getUncleCountByBlockNumber", []interface{}{fmt.Sprintf("0x%x", blockNumber)})
	if err != nil {
		return 0, err
	}

	uncleCount, err := strconv.ParseUint(utils.Clear0x(data.(string)), 16, 64)
	if err != nil {
		return 0, err
	}

	return uncleCount, nil
}

// HarvestBlockByNonce is the most compatible way to find out the block hash
func (n *Node) HarvestBlockByNonce(givenNonceHex string, givenNumber uint64) (block Block, uncleParent uint64, err error) {
	maxRecursion := 10
	blocksPerLoop := 100

	givenNumberHex := fmt.Sprintf("0x%x", givenNumber)

	for i := 0; i <= maxRecursion; i++ {
		currentBlockNumber, err := n.BlockNumber()
		if err != nil {
			panic(err)
		}

		for i := 0; i <= blocksPerLoop; i++ {
			blockNumber := currentBlockNumber - uint64(i)
			block, _ := n.GetBlockByNumber(blockNumber)
			if block.Nonce == givenNonceHex {
				return block, 0, nil
			}

			uncleCount, _ := n.GetUncleCountByBlockNumber(blockNumber)
			uncleCountInt := int(uncleCount)

			var uncle Block

			for i := 0; i < uncleCountInt; i++ {
				uncle, _ = n.GetUncleByBlockNumberAndIndex(blockNumber, i)
				if uncle.Nonce == givenNonceHex {
					return uncle, blockNumber, nil
				}
			}
		}
		time.Sleep(time.Second * 5)
	}

	return Block{}, 0, errors.New("unable to harvest block by hash (nonce: " + givenNonceHex + ", number:" + givenNumberHex + ")")
}
