package alien

import (
	"github.com/TTCECO/gttc/common"
	"github.com/TTCECO/gttc/params"
	"math/big"
	"testing"

	"github.com/TTCECO/gttc/core"
	"github.com/TTCECO/gttc/core/types"
	"github.com/TTCECO/gttc/ethdb"
	"github.com/TTCECO/gttc/core/state"
	"github.com/TTCECO/gttc/core/rawdb"
)

//func signHash(account accounts.Account, hash common.Hash) common.Hash {
//
//	return crypto.Sign(hash, account.Address)
//}

func (r *testerChainReader) GetHeader(hash common.Hash, number uint64) *types.Header {

	return rawdb.ReadHeader(r.db, hash, number)
}

func TestAlien(t *testing.T)  {

	// extend length of extra, so address of CoinBase can keep signature .
	genesis := &core.Genesis{
		ExtraData: make([]byte, extraVanity+extraSeal),
	}

	// Create a pristine blockchain with the genesis injected
	db := ethdb.NewMemDatabase()
	genesis.Commit(db)

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
	alien := New(alienCfg, db)
	alien.Authorize(alienCfg.SelfVoteSigners[0], nil)

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

	header := &types.Header{Number: big.NewInt(1), Difficulty: big.NewInt(100)}

	err := alien.Prepare(&testerChainReader{db:db}, header)
	if err != nil {

		t.Errorf("test: failed to prepare: %v", err)
	}

	var txs types.Transactions
	txs = append(txs, &types.Transaction{})
	_, err = alien.Finalize(&testerChainReader{db:db}, header, state, txs, []*types.Header{}, []*types.Receipt{})

	//_, err = alien.Seal(&testerChainReader{db:db}, types.NewBlockWithHeader(header), nil)
	if err != nil {

		t.Errorf("test: failed to seal: %v", err)
	}
}