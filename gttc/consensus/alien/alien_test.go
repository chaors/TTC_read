package alien

import (
	"fmt"
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

type testerBlockHeader struct {
	number uint64
	txs []testerTransaction
}

type testerRewardResult struct {
	balance map[string]*big.Int
}


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

/*
string account密码
  */
func newTestAccountPool() *testAccountPool {
	return &testAccountPool{
		accounts: make(map[string]*accounts.Account),
	}
}
/*
m	出块个数
mCount	出块个数
v	支持的candidate出块个数

*/
func CalReward(m uint64, mCount uint64, v uint64, vCounts []uint64, bals []uint64, allStakes []uint64) *big.Int {

	//s := make(map[string][]map[uint64]map[uint64]uint64)

	//m*2 + v*200.0/300.0*1 + v*200.0/200.0*1,
	//[200,200] [300 200] [1,1]
	m1 := new(big.Int).SetUint64(m)
	mCount1 := new(big.Int).SetUint64(mCount)
	v1 := new(big.Int).SetUint64(v)

	//矿工奖励
	m1.Mul(m1, mCount1)
	v2 := new(big.Int).Set(v1)
	//投票奖励
	for i := 0; i < len(vCounts); i++ {

		v2.Mul(v1, new(big.Int).SetUint64(bals[i]))
		v2.Div(v2, new(big.Int).SetUint64(allStakes[i]))
		v3 := big.NewInt(0)
		for j := 0; j < int(vCounts[i]) ; j++ {
			v3.Add(v3, v2)
		}
		//v2.Mul(v2, new(big.Int).SetUint64(vCounts[i]))
		m1.Add(m1, v3)
	}
	//fmt.Printf("v**/----%v\n", v1)
	//fmt.Printf("+++----%v\n", m1.Add(m1, v1))

	return m1
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


// TODO need to rewrite
func TestAlien(t *testing.T)  {

	//s := big.NewInt(5e+18)
	//r := new(big.Int).Set(s)
	//m1 := r.Mul(r, big.NewInt(618))
	//m1 = m1.Div(m1, big.NewInt(1000))
	//m := m1.Uint64()
	//v := s.Sub(s, m1).Uint64()

	tests := []struct {
		AddrNames        []string             // used for accounts password in this case
		Period         uint64
		Epoch           uint64
		MinVoterBalance *big.Int
		MaxSignerCount  uint64
		SelfVoteSigners []testerSelfVoter
		txHeaders        []testerBlockHeader
		//result testerRewardResult
	}{
		{[]string{"A", "B", "C"},
			uint64(1),
			uint64(10),
			big.NewInt(int64(50)),
			uint64(3),
			[]testerSelfVoter{{"A", 200}},
			[]testerBlockHeader{ // BA
				{1, []testerTransaction{}}, //b
				//{2,[]testerTransaction{}},//a
				//{3,[]testerTransaction{}},//b
			},
			//testerRewardResult{
			//	map[string]*big.Int{
			//		"A": big.NewInt(0).Add(CalReward(m, 1, v, []uint64{1}, []uint64{100}, []uint64{100}), big.NewInt(0).Mul(big.NewInt(200), big.NewInt(1e+18))), //m*1 + v*1*100/100,
			//		"B": big.NewInt(0),
			//		"C": big.NewInt(0),
			//	},
			//},
		},
	}

	for i, tt := range tests{

		//590702
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
			alien.Authorize(signer, ks.SignHash, ks.SignTx)

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

		////verify 总balance
		//balance := state.GetBalance(accountsPool.accounts["A"].Address)
		//fmt.Println("check balance", balance)
		//
		//for _, name := range tt.AddrNames {
		//
		//	if state.GetBalance(accountsPool.accounts[name].Address).Cmp(tt.result.balance[name]) != 0 {
		//
		//		t.Errorf("balance%d tset fail:%s balance:%v in BLC dismatch %v in test result ", i, name, state.GetBalance(accountsPool.accounts[name].Address), tt.result.balance[name])
		//	}
		//}

	}
}

func TestRewardBySnap(t *testing.T)  {

	tests := []struct {

	}

	for _, tt := range tests {

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

		accountsPool := newTestAccountPool()

		alienCfg := &params.AlienConfig{
			Period:          3,
			Epoch:           20,
			MinVoterBalance: big.NewInt(50),
			MaxSignerCount:  5,
			SelfVoteSigners: []common.Address{accountsPool.accounts["A"].Address},
			GenesisTimestamp:chainReader.GetHeaderByNumber(0).Time.Uint64()+3,
		}

		// chainCfg
		chainCfg := &params.ChainConfig{
			nil,
			nil,
			nil,
			common.Hash{},
			nil,
			nil,
			nil,
			nil,
			&params.EthashConfig{},
			&params.CliqueConfig{},
			alienCfg,
		}

		alien := New(alienCfg, db)

		alien.accumulateRewards
	}
}

func TestReward(t *testing.T)  {

	s := big.NewInt(5e+18)
	r := new(big.Int).Set(s)
	m1 := r.Mul(r, big.NewInt(618))
	m1 = m1.Div(m1, big.NewInt(1000))
	m := m1.Uint64()
	v := s.Sub(s, m1).Uint64()

	tests := []struct {
		addrNames        []string             // accounts used in this case
		period           uint64               // default 3
		epoch            uint64               // default 30000
		maxSignerCount   uint64               // default 5 for test
		minVoterBalance  int                  // default 50
		selfVoters       []testerSelfVoter    //
		txHeaders        []testerBlockHeader //
		historyHashes []string
		result           testerRewardResult
	}{
		//balance0 A,B两个自选签名者(A,B)，目前只出了一个块 所以还没轮到b出块 A自己选自己所以奖励都是自己的
		{
			addrNames:        []string{"A", "B"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(3),
			minVoterBalance:  50,
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(m, 1, v, []uint64{1}, []uint64{100}, []uint64{100}), //m*1 + v*1*100/100,
					"B": big.NewInt(0),
				},
			},
		},
	}

	for _ ,t := range tests {

		fmt.Printf("%v", t)
	}
}

