package alien

import (
	"fmt"
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

	// extend length of extra, so address of CoinBase can keep signature .
	genesis := &core.Genesis{
		ExtraData: make([]byte, extraVanity+extraSeal),
	}

	// Create a pristine blockchain with the genesis injected
	db := ethdb.NewMemDatabase()
	genesis.Commit(db)

	// Create a new state
	state, _ := state.New(common.Hash{}, state.NewDatabase(db))

	// Create a chainReader
	chainReader := &testerChainReader{db:db}

	//accounts := newTesterAccountPool()
	// Create TTC account
	_, ks := tmpKeyStore(t, true)
	account, _ := ks.NewAccount("A")

	// Create new alien
	alienCfg := &params.AlienConfig{
		Period:          uint64(3),
		Epoch:           uint64(10),
		MinVoterBalance: big.NewInt(int64(50)),
		MaxSignerCount:  uint64(3),
		SelfVoteSigners: []common.Address{account.Address},
	}
	state.SetBalance(account.Address, big.NewInt(100))

	alien := New(alienCfg, db)
	alien.Authorize(alienCfg.SelfVoteSigners[0], ks.SignHash)
	ks.Unlock(account, "A")

	fmt.Printf("ccc A:%v\n\n", account.Address.Hex())

	currentHeaderExtra := HeaderExtra{}
	signer := common.Address{}
	//var parents types.Transactions
	for i := 1; i < 2; i++ {
		//(i==0) means (header.Number==1)
		if i == 1 {
			for k := 0; k < int(alienCfg.MaxSignerCount); k++ {
				currentHeaderExtra.SignerQueue = append(currentHeaderExtra.SignerQueue, alienCfg.SelfVoteSigners[k%len(alienCfg.SelfVoteSigners)])
			}
			currentHeaderExtra.LoopStartTime = uint64(0)
			signer = alienCfg.SelfVoteSigners[0]
		}else {
			// decode signer message from last blockHeader.Extra
			header := rawdb.ReadHeader(db, rawdb.ReadCanonicalHash(db, uint64(i-1)), uint64(i-1))
			//fmt.Printf("ccc header i%v:%v\n", i-1, header)

			//rlp.DecodeBytes(header.Extra[extraVanity:len(header.Extra)-extraSeal], &currentHeaderExtra)
			rlp.DecodeBytes(header.Extra[extraVanity:len(header.Extra)-extraSeal], &currentHeaderExtra)
			signer = currentHeaderExtra.SignerQueue[uint64(i)%alienCfg.MaxSignerCount]
			currentHeaderExtra.LoopStartTime = currentHeaderExtra.LoopStartTime+alienCfg.Period*alienCfg.MaxSignerCount
		}

		currentHeaderExtraEnc, err := rlp.EncodeToBytes(currentHeaderExtra)

		// Create the genesis block with the initial set of signers
		ExtraData := make([]byte, extraVanity+len(currentHeaderExtraEnc)+extraSeal)
		copy(ExtraData[extraVanity:], currentHeaderExtraEnc)

		fmt.Printf("ccc signer-%v:%v\n---%v\n", i, signer.Hex(), currentHeaderExtra.SignerQueue)
		header := &types.Header{
			Number:   big.NewInt(int64(i)),
			Time:     big.NewInt((int64(i))*int64(defaultBlockPeriod) - 1),
			Coinbase: signer,
			Extra:    ExtraData,
			ParentHash:chainReader.GetHeaderByNumber(uint64(i-1)).Hash(),
		}
		rawdb.WriteCanonicalHash(db, header.Hash(), header.Number.Uint64())
		rawdb.WriteHeader(db, header)

		fmt.Printf("ccc out header-%v:%v\n", header.Number, header)
		err = alien.Prepare(chainReader, header)
		if err != nil {

			t.Errorf("test: failed to prepare: %v", err)
		}

		var txs types.Transactions
		//to, _ := ks.NewAccount(strconv.Itoa(i))
		txs = append(txs, types.NewTransaction(
			uint64(i-1),
			common.Address{},
			big.NewInt(0), 0, big.NewInt(0),
			nil,
		))

		_, err = alien.Finalize(chainReader, header, state, txs, []*types.Header{}, []*types.Receipt{})
		if err != nil {

			t.Errorf("test: failed to Finalize: %v", err)
		}

		//b, err = alien.Seal(chainReader, b, nil)
		//if err != nil {
		//
		//	t.Errorf("test: failed to seal: %v", err)
		//}
		//
		//fmt.Printf("%v-----%v-----%vn\n",b.Header().Coinbase.Hex(), state.GetBalance(b.Header().Coinbase), b.Header().Extra[len(b.Header().Extra)-extraSeal:])

		// check blocks
		//err = alien.VerifyHeader(chainReader, b.Header(), true)
		//if err != nil {
		//
		//	t.Errorf("test: failed to VerifyHeader: %v", err)
		//}

		//time.Sleep(3*time.Second)
		//add to chain
		//rawdb.WriteBlock(db, b)
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