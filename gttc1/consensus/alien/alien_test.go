package alien

import (
	"github.com/TTCECO/gttc/accounts"
	"github.com/TTCECO/gttc/accounts/keystore"
	"github.com/TTCECO/gttc/common"
	"github.com/TTCECO/gttc/core"
	"github.com/TTCECO/gttc/core/rawdb"
	"github.com/TTCECO/gttc/core/state"
	"github.com/TTCECO/gttc/core/types"
	"github.com/TTCECO/gttc/ethdb"
	"github.com/TTCECO/gttc/params"
	"github.com/TTCECO/gttc/rlp"
	"io/ioutil"
	"math/big"
	"testing"
	"time"
)

const (
	veryLightScryptN = 2
	veryLightScryptP = 1
)


func (r *testerChainReader) GetHeader(hash common.Hash, number uint64) *types.Header {

	//return rawdb.ReadHeader(r.db, hash, number)
	return rawdb.ReadHeader(r.db, rawdb.ReadCanonicalHash(r.db, number), number)
}

func (r *testerChainReader) GetGenesisHeader() *types.Header {

	return rawdb.ReadHeader(r.db, rawdb.ReadCanonicalHash(r.db, 0), 0)
}

//func (r *testerChainReader) Put(key []byte, value []byte) error {
//
//}

func tmpKeyStore(t *testing.T, encrypted bool) (string, *keystore.KeyStore) {
	d, err := ioutil.TempDir("", "alien-keystore-test")
	if err != nil {
		t.Fatal(err)
	}
	//newKs := keystore.NewPlaintextKeyStore
	//if encrypted {
	//	newKs := func(kd string) *keystore.KeyStore {
	//		return keystore.NewKeyStore(kd, veryLightScryptN, veryLightScryptP)
	//	}
	//}
	//ks :=
	return d, keystore.NewKeyStore(d, veryLightScryptN, veryLightScryptP)
}

type testAccountPool struct {

	accounts map[string]*accounts.Account
}

func newTestAccountPool() *testAccountPool {
	return &testAccountPool{
		accounts: make(map[string]*accounts.Account),
	}
}

//
//func SignerFn(accounts.Account, []byte) ([]byte, error) {
//
//
//}

/**
alien要测哪些东西：
	1.prepare,finalize,seal,verify等流程
	2.出块奖励(包括衰减,这里需要区块时间)  + 投票奖励
	3.出块的规则(顺序，实际顺序)等

测试结构的设计：
	1.signer_queue
	2.balance
	3.real_queue
	4.time
*/

func TestAlien(t *testing.T)  {

	tests := []struct {
		AddrNames        []string             // used for accounts password in this case
		Period         uint64
		Epoch           uint64
		MinVoterBalance *big.Int
		MaxSignerCount  uint64
		SelfVoteSigners []testerSelfVoter
		txHeaders        []testerSingleHeader
	}{
		{[]string{"A", "B", "C"},
			uint64(1),
			uint64(10),
			big.NewInt(int64(50)),
			uint64(3),
			[]testerSelfVoter{{"B", 100}, {"A", 100}, {"C", 120}},
			[]testerSingleHeader{
				{[]testerTransaction{}},
				{[]testerTransaction{}},
				{[]testerTransaction{}},
				//{[]testerTransaction{}},
				//{[]testerTransaction{}},
				//{[]testerTransaction{}},
				//{[]testerTransaction{}},
			},
		},
	}

	for i, tt := range tests{

		//ttc account from AddrNames
		accountsPool := newTestAccountPool()

		_, ks := tmpKeyStore(t, true)

		for _, name := range tt.AddrNames {

			account, _ := ks.NewAccount(name)
			accountsPool.accounts[name] = &account
			ks.Unlock(account, name)
		}

		// extend length of extra, so address of CoinBase can keep signature .
		genesis := &core.Genesis{
			ExtraData: make([]byte, extraVanity+extraSeal),
			Timestamp:uint64(time.Now().Unix()),
		}

		// Create a pristine blockchain with the genesis injected
		db := ethdb.NewMemDatabase()
		genesis.Commit(db)

		// Create a new state
		state, _ := state.New(common.Hash{}, state.NewDatabase(db))

		// Create a chainReader
		chainReader := &testerChainReader{db:db}

		// Create new alien
		selfVoteSigners := make([]common.Address, 0)
		for _, selfVoter := range tt.SelfVoteSigners {

			selfVoteSigners = append(selfVoteSigners, accountsPool.accounts[selfVoter.voter].Address)
			state.AddBalance(accountsPool.accounts[selfVoter.voter].Address, big.NewInt(0).Mul(big.NewInt(int64(selfVoter.balance)), big.NewInt(1e+18)))
		}

		alienCfg := &params.AlienConfig{
			Period:          tt.Period,
			Epoch:           tt.Epoch,
			MinVoterBalance: tt.MinVoterBalance,
			MaxSignerCount:  tt.MaxSignerCount,
			SelfVoteSigners: selfVoteSigners,
			GenesisTimestamp:chainReader.GetHeaderByNumber(0).Time.Uint64()+tt.Period,
		}

		alien := New(alienCfg, db)

		currentHeaderExtra := HeaderExtra{}
		signer := common.Address{}

		for j, txHeader := range tt.txHeaders {
			//(j==0) means (header.Number==1)
			if j == 0 {

				signer = alienCfg.SelfVoteSigners[0]
			}else {

				// decode signer message from last blockHeader.Extra
				header := chainReader.GetHeaderByNumber(uint64(j))
				rlp.DecodeBytes(header.Extra[extraVanity:len(header.Extra)-extraSeal], &currentHeaderExtra)
				loopCount := uint64(j-1)/alienCfg.Period
				currentHeaderExtra.LoopStartTime = currentHeaderExtra.LoopStartTime+ loopCount*alienCfg.Period*alienCfg.MaxSignerCount
				signer = currentHeaderExtra.SignerQueue[uint64(j)%alienCfg.MaxSignerCount]
			}

			currentHeaderExtraEnc, err := rlp.EncodeToBytes(currentHeaderExtra)
			// Create the genesis block with the initial set of signers
			ExtraData := make([]byte, extraVanity+len(currentHeaderExtraEnc)+extraSeal)
			copy(ExtraData[extraVanity:], currentHeaderExtraEnc)

			header := &types.Header{
				Number:   big.NewInt(int64(j+1)),
				Time:     big.NewInt(0),
				Coinbase: signer,
				Extra:    ExtraData,
				ParentHash:chainReader.GetHeaderByNumber(uint64(j)).Hash(),
			}
			alien.Authorize(signer, ks.SignHash)

			//start alien to sealing block
			err = alien.Prepare(chainReader, header)
			if err != nil {

				t.Errorf("test: failed to prepare: %v", err)
			}

			//tx per block
			var txs types.Transactions
			for k, _ := range txHeader.txs {
				txs = append(txs, types.NewTransaction(
					uint64(k),
					common.Address{},
					big.NewInt(0), 0, big.NewInt(0),
					nil,
				))
			}

			b, err := alien.Finalize(chainReader, header, state, txs, []*types.Header{}, []*types.Receipt{})
			if err != nil {

				t.Errorf("test%v: failed to Finalize: %v", i, err)
			}

			// in:= make(chan struct{})
			//out:
			//	for {
			//		select {
			//		case <- in:
			//			b, err = alien.Seal(chainReader, b, in)
			//		case <-chan struct{}:
			//			//close(stop)
			//			break out
			//		}
			//	}
			b, err = alien.Seal(chainReader, b, nil)
			if err != nil {

				t.Errorf("test%v: failed to seal: %v", i, err)
			}

			//save block
			rawdb.WriteCanonicalHash(db, b.Header().Hash(), b.Header().Number.Uint64())
			rawdb.WriteHeader(db, b.Header())

			//check blocks
			err = alien.VerifyHeader(chainReader, b.Header(), true)
			if err != nil {

				t.Errorf("test%v: failed to VerifyHeader: %v", i, err)
			}
		}


	}

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



}