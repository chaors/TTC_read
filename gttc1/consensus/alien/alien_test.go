package alien

import (
	"fmt"
	"github.com/TTCECO/gttc/common"
	"github.com/TTCECO/gttc/params"
	"math/big"
	"testing"

	"github.com/TTCECO/gttc/core"
	"github.com/TTCECO/gttc/core/rawdb"
	"github.com/TTCECO/gttc/core/state"
	"github.com/TTCECO/gttc/core/types"
	"github.com/TTCECO/gttc/ethdb"
	"github.com/TTCECO/gttc/rlp"
)

//func signHash(account accounts.Account, hash common.Hash) common.Hash {
//
//	return crypto.Sign(hash, account.Address)
//}

func (r *testerChainReader) GetHeader(hash common.Hash, number uint64) *types.Header {

	return rawdb.ReadHeader(r.db, rawdb.ReadCanonicalHash(r.db, 0), 0)
}
//
//func SignerFn(accounts.Account, []byte) ([]byte, error) {
//
//
//}


func TestAlien(t *testing.T)  {

	// extend length of extra, so address of CoinBase can keep signature .
	genesis := &core.Genesis{
		ExtraData: make([]byte, extraVanity+extraSeal),
	}

	// Create a pristine blockchain with the genesis injected
	db := ethdb.NewMemDatabase()
	genesis.Commit(db)

	// Create a new state
	state, _ := state.New(common.Hash{}, state.NewDatabase(db))

	accounts := newTesterAccountPool()

	// Create new alien
	alienCfg := &params.AlienConfig{
		Period:          uint64(3),
		Epoch:           uint64(10),
		MinVoterBalance: big.NewInt(int64(50)),
		MaxSignerCount:  uint64(3),
		SelfVoteSigners: []common.Address{accounts.address("A")},
	}
	state.SetBalance(accounts.address("A"), big.NewInt(100))

	alien := New(alienCfg, db)
	alien.Authorize(alienCfg.SelfVoteSigners[0], nil)

	currentHeaderExtra := HeaderExtra{}
	for i := 0; i < int(alienCfg.MaxSignerCount); i++ {
		currentHeaderExtra.SignerQueue = append(currentHeaderExtra.SignerQueue, alienCfg.SelfVoteSigners[i%len(alienCfg.SelfVoteSigners)])
	}
	currentHeaderExtra.LoopStartTime = 0
	alien.signer = alienCfg.SelfVoteSigners[0]
	currentHeaderExtraEnc, err := rlp.EncodeToBytes(currentHeaderExtra)

	// Create the genesis block with the initial set of signers
	ExtraData := make([]byte, extraVanity+len(currentHeaderExtraEnc)+extraSeal)
	copy(ExtraData[extraVanity:], currentHeaderExtraEnc)

	// chainCfg
	//chainCfg := &params.ChainConfig{
	//	nil,
	//	nil,
	//	nil,
	//	common.Hash{},
	//	nil,
	//	nil,
	//	nil,
	//	nil,
	//	&params.EthashConfig{},
	//	&params.CliqueConfig{},
	//	alienCfg,
	//}

	header := &types.Header{
		Number:   big.NewInt(1),
		Time:     big.NewInt((int64(0)+1)*int64(defaultBlockPeriod) - 1),
		Coinbase: alienCfg.SelfVoteSigners[0],
		Extra:    ExtraData,
	}

	err = alien.Prepare(&testerChainReader{db:db}, header)
	if err != nil {

		t.Errorf("test: failed to prepare: %v", err)
	}

	var txs types.Transactions
	txs = append(txs, types.NewTransaction(
		0,
		common.HexToAddress("095e7baea6a6c7c4c2dfeb977efac326af552d87"),
		big.NewInt(0), 0, big.NewInt(0),
		nil,
	))

	block, err := alien.Finalize(&testerChainReader{db:db}, header, state, txs, []*types.Header{}, []*types.Receipt{})
	if err != nil {

		t.Errorf("test: failed to Finalize: %v", err)
	}

	block, err = alien.Seal(&testerChainReader{db:db}, block, nil)
	if err != nil {

		t.Errorf("test: failed to seal: %v", err)
	}

	fmt.Printf("%v-----%v",block.Header().Coinbase, state.GetBalance(block.Header().Coinbase))

}