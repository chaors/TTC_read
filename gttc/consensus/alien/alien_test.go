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

type testerBlockHeader struct {
	number uint64
	txs []testerTransaction
}

type testerRewardResult struct {
	balance map[string]*big.Int
}

type testerProposal struct {
	typeOfProposal string
	countOfAgree []string
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
blockReward	    当前一个块的奖励
minerRewardPerT	矿工奖励千分比
minerCount	    当前自己挖出的块个数
vCounts	        每次所投票节点挖块个数的集合
bals            每次所投票的票数集合
allStakes	    每次所投票的节点总票数集合
vCounts,bals,allStakes按下标一一对应  eg:
vCounts{1,2}/bals{100,100}/allStakes{100,300}:当前地址第1次投票节点挖出1个块，票数100都来自当前地址;第2次投票节点挖出2个块，票数300中有100来自当前地址
*/
func CalReward(blockReward *big.Int, minerRewardPerT uint64, minerCount uint64, vCounts []uint64, bals []uint64, allStakes []uint64) *big.Int {

	//s := make(map[string][]map[uint64]map[uint64]uint64)

	minerReward := new(big.Int).Set(blockReward)
	minerReward.Mul(minerReward, new(big.Int).SetUint64(minerRewardPerT))
	minerReward.Div(minerReward, big.NewInt(1000))
	votersReward := big.NewInt(0).Sub(blockReward, minerReward)

	//矿工奖励
	asMinerReward := big.NewInt(0).Mul(minerReward, new(big.Int).SetUint64(minerCount))

	//m*2 + v*200.0/300.0*1 + v*200.0/200.0*1,
	//[200,200] [300 200] [1,1]
	//投票奖励
	asVoterReward := big.NewInt(0)
	for i := 0; i < len(vCounts); i++ {

		v := big.NewInt(0).Mul(votersReward, new(big.Int).SetUint64(bals[i]))
		v.Div(v, new(big.Int).SetUint64(allStakes[i]))
		vReward := big.NewInt(0)
		for j := 0; j < int(vCounts[i]) ; j++ {
			vReward.Add(vReward, v)
		}
		asVoterReward.Add(asVoterReward, vReward)
	}

	return big.NewInt(0).Add(asMinerReward, asVoterReward)
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


// TODO:need to rewrite:出块顺序如果不符和什么的，产生短暂分叉的，出块对应时间片正确性
// 暂时测得是一个块的mining过程和验证：Prepare->Finalize->Seal->VerifyHeader->VerifySeal几个重要流程
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
				{2,[]testerTransaction{}},//a
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

				t.Errorf("alienTest case: failed to prepare: %v", err)
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

				t.Errorf("alienTest case%v: failed to Finalize: %v", i, err)
			}

			b, err = alien.Seal(chainReader, b, nil)
			if err != nil {

				t.Errorf("alienTest case%v: failed to seal: %v", i, err)
			}

			//save block
			rawdb.WriteCanonicalHash(db, b.Header().Hash(), b.Header().Number.Uint64())
			rawdb.WriteHeader(db, b.Header())

			//check blocks
			err = alien.VerifyHeader(chainReader, b.Header(), true)
			if err != nil {

				t.Errorf("alienTest case%v: failed to VerifyHeader: %v", i, err)
			}

			err = alien.VerifySeal(chainReader, b.Header())
			if err != nil {

				t.Errorf("alienTest case%v: failed to VerifySeal: %v", i, err)
			}
		}
	}
}

// 测试块奖励的逐年衰减
func TestWeakenRewardsByYears1(t *testing.T) {

	/*
	tests := []struct {
		period     uint64
		addNames   []string
		number     uint64
		coinBase   string
		votes      []testerVote
		selfVoters []testerSelfVoter
		result     []map[string]*big.Int
	}{
		// case0:奖励第一年产生衰减 5-->2.5
		{
			period:3,
			addNames: []string{"A", "B", "C"},
			number:   24*60*60*365*1/3 + 33, //第二年的区块
			coinBase: string("A"),
			votes: []testerVote{
				{"A", "A", 100},
			},
			result: []map[string]*big.Int{
				{"A": CalReward(big.NewInt(0).Div(SignerBlockReward, big.NewInt(2)), minerRewardPerThousand, 1, []uint64{1}, []uint64{100}, []uint64{100})},
				{"B": big.NewInt(0)},
				{"C": big.NewInt(0)},
			},
		},
	}*/


}
func TestWeakenRewardsByYears(t *testing.T) {

	tests := []struct {
		period 	   uint64
		time       uint64
		coinBase   string
		votes      []testerVote
		result     []map[string]*big.Int
	}{
		// case0:奖励第一年产生衰减 5-->2.5
		{
			period:	3,
			time:   secondsPerYear + 33, //第二年的区块
			coinBase: string("A"),
			votes: []testerVote{
				{"A", "A", 100},
			},
			result: []map[string]*big.Int{
				{"A": CalReward(big.NewInt(0).Div(SignerBlockReward, big.NewInt(2)), minerRewardPerThousand, 1, []uint64{1}, []uint64{100}, []uint64{100})},
			},
		},

		//case1
		{
			period:	3,
			time:   secondsPerYear*2 + 50, //第二年的区块
			coinBase: string("A"),
			votes: []testerVote{
				{"A", "A", 100},
			},

			result: []map[string]*big.Int{
				{"A": CalReward(big.NewInt(0).Div(SignerBlockReward, big.NewInt(4)), minerRewardPerThousand, 1, []uint64{1}, []uint64{100}, []uint64{100})},
			},
		},
	}

	for i, tt := range tests {
		//账户池(Address)
		accountsPool := newTestAccountPool()
		_, ks := tmpKeyStore(t, true)
		account, _ := ks.NewAccount(tt.coinBase)
		accountsPool.accounts[tt.coinBase] = &account

		genesis := &core.Genesis{
			ExtraData: make([]byte, extraVanity+extraSeal),
		}
		//Create a pristine blockchain with the genesis injected
		db := ethdb.NewMemDatabase()
		genesis.Commit(db)
		//state
		state, _ := state.New(common.Hash{}, state.NewDatabase(db))

		currentSigners := append([]common.Address{}, accountsPool.accounts[tt.coinBase].Address)
		alienCfg := &params.AlienConfig{
			Period:          uint64(3),
			Epoch:           uint64(10),
			MinVoterBalance: big.NewInt(int64(50)),
			MaxSignerCount:  uint64(3),
			SelfVoteSigners: currentSigners, //这里实际指的是当前的签名者
		}
		alien := New(alienCfg, db)

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

		header := &types.Header{
			Number:   new(big.Int).SetUint64(tt.time/tt.period),
			Time:     big.NewInt(time.Now().Unix()),
			Coinbase: accountsPool.accounts[tt.coinBase].Address,
		}

		votes := []*Vote{}
		for _, vote := range tt.votes {

			snapVote := Vote{accountsPool.accounts[vote.voter].Address, accountsPool.accounts[vote.candidate].Address, big.NewInt(int64(vote.stake))}
			votes = append(votes, &snapVote)
		}
		snap := newSnapshot(alien.config, nil, header.Hash(), votes, 2)
		//test
		accumulateRewards(chainCfg, state, header, snap, RefundGas{})

		//verify
		for _, result := range tt.result {
			for k, v := range result {
				balance := state.GetBalance(accountsPool.accounts[k].Address)
				if balance.Cmp(v) != 0 {
					t.Errorf("TestWeakenRewards case%d fail:%s balance:%v in BLC dismatch %v in test result ", i, k, balance, v)
				}
			}
		}
	}
}

// TODO:need add sidechain
func TestReward(t *testing.T)  {

	defaultBlockReward := big.NewInt(5e+18)
	defaultMinerRewardPerThousand := uint64(618)

	/*共识alien参数基本固定,不在测试结构体现
	alienCfg := &params.AlienConfig{
		Period:          uint64(3),
		Epoch:           uint64(10),
		MinVoterBalance: big.NewInt(100),
		MaxSignerCount:  uint64(5),
		SelfVoteSigners: selfVoteSigners,
	}
	*/
	//投票时的余额只是用于算比例,测试验证的只是出块过程产生的奖励!!!
	//???是否有必要将投票时的余额与产生奖励的余额关联

	tests := []struct {
		addrNames        []string
		selfVoters       []testerSelfVoter
		txHeaders        []testerBlockHeader
		historyHashes []string
		result           testerRewardResult
	}{

		//case 0:balance0 A,B两个自选签名者(A,B)，目前只出了2个块 所以还没轮到b出块 A自己选自己所以奖励都是自己的
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{}}, // B
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					//A:mReward*1+vReward*1   B:mReward*1+vReward*1
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 1, []uint64{1}, []uint64{100}, []uint64{100}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 1, []uint64{1}, []uint64{200}, []uint64{200}),
					"C": big.NewInt(0),
				},
			},
		},


		//case 1:balance0 A,B两个自选签名者(A,B)，目前出了2个块 C虽然没有参与出块，但是c投票的B出块了  所以
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "C", to: "B", balance:100, isVote: true}}}, // B
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A":CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 1, []uint64{1}, []uint64{100}, []uint64{100}),
					"B":CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 1, []uint64{1}, []uint64{200}, []uint64{300}),
					"C":CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 0, []uint64{1}, []uint64{100}, []uint64{300}),
				},
			},
		},

		//case 2:balance0 A,B两个自选签名者(A,B)，区块2A转投给B B挖出区块三A会有投票奖励   同时之前A投A时候出的块也有A的投票奖励
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "B", balance:100, isVote: true}}}, // B
				{3,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 2, []uint64{1,1}, []uint64{100,100}, []uint64{100,300}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 1, []uint64{1}, []uint64{200}, []uint64{300}),
					"C": big.NewInt(0),
				},
			},
		},

		// case3: C投票给B但是余额不满足投票限制  因此投票失败  不会拿到B出块产生的投票奖励
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				// 80 < minVoterBalance vote不会成功
				{2,[]testerTransaction{{from: "C", to: "B", balance:80, isVote: true}}}, // B
				{3,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 2, []uint64{2}, []uint64{100}, []uint64{100}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 1, []uint64{1}, []uint64{200}, []uint64{200}),
					"C": big.NewInt(0),
				},
			},
		},

		// case4:A在区块2把票转投给C 之前A出过一个块  有出块奖励,以后A出块将不会给自己分投票奖励
		//此时C还未被加入到签名者队列(新的签名轮次没来),c并没有任何奖励
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "C", balance:280, isVote: true}}}, // B
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{}},//b
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 2, []uint64{1}, []uint64{100}, []uint64{100}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 2, []uint64{2}, []uint64{200}, []uint64{200}),
					"C": big.NewInt(0),
				},
			},
		},

		// case5:base4 新的签名轮次到达  C参与出块会获得奖励
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "C", balance:280, isVote: true}}}, // B
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{}},//b
				{5,[]testerTransaction{}},//a
				{6,[]testerTransaction{}},//c  new loop:c b a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 3, []uint64{1,1}, []uint64{100,280}, []uint64{100,280}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 2, []uint64{2}, []uint64{200}, []uint64{200}),
					"C": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 1, []uint64{0}, []uint64{200}, []uint64{200}),
				},
			},
		},

		//case6:在区块2 A把票转投给了B,B把票投给了A 但是在此前区块1A还是出块者A的投票者  会有投票奖励
		//后面他们各自得到自己的挖矿奖励和对方挖矿的投票奖励
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "B", balance:100, isVote: true}, {from: "B", to: "A", balance:200, isVote: true}}}, // B
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{}},//b
				{5,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 3, []uint64{1,2}, []uint64{100,100}, []uint64{100,100}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 2, []uint64{1,1}, []uint64{200,200}, []uint64{200,200}),
					"C": big.NewInt(0),
				},
			},
		},

		// case7:在区块2 A把票转投给了B,  区块4B把票投给了A 这个时候要注意在投票前各自的投票奖励与自己相关和投票比例变化
		//后面他们各自得到自己的挖矿奖励和对方挖矿的投票奖励
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{{from: "B", to: "A", balance: 200, isVote: true}}},//b
				{5,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 3, []uint64{1,1,1}, []uint64{100,100,100}, []uint64{100,300,100}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 2, []uint64{1,1}, []uint64{200,200}, []uint64{300,200}),
					"C": big.NewInt(0),
				},
			},
		},

		//case8:在区块2 A把票转投给了B,  在区块4 A把票又投回给自己
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{{from: "A", to: "A", balance: 200, isVote: true}}},//b
				{5,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 3, []uint64{2,1}, []uint64{100,100}, []uint64{100,300}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 2, []uint64{1,1}, []uint64{200,200}, []uint64{300,200}),
					"C": big.NewInt(0),
				},
			},
		},

		// case9:base8 在区块5 A把票又投回给自己  此时只出块到7 A被投但还未加入出块者中
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{}},//b
				{5,[]testerTransaction{{from: "A", to: "A", balance: 100, isVote: true}}},//a
				{6,[]testerTransaction{}},//b
				{7,[]testerTransaction{}},//b
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 3, []uint64{2,2}, []uint64{100,100}, []uint64{100,300}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 4, []uint64{2,2}, []uint64{200,200}, []uint64{300,200}),
					"C": big.NewInt(0),
				},
			},
		},

		// case10:base9 此时只出块到12 A被加入出块者中
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{}},//b
				{5,[]testerTransaction{{from: "A", to: "A", balance: 100, isVote: true}}},//a
				{6,[]testerTransaction{}},//b
				{7,[]testerTransaction{}},//b
				{8,[]testerTransaction{}},//b
				{9,[]testerTransaction{}},//b
				{10,[]testerTransaction{}},//b  在这里A才被重新加入到出块者
				{11,[]testerTransaction{}},//b
				{12,[]testerTransaction{}},//a

			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 4, []uint64{3,2}, []uint64{100,100}, []uint64{100,300}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 8, []uint64{2,6}, []uint64{200,200}, []uint64{300,200}),
					"C": big.NewInt(0),
				},
			},
		},

		// case11:区块4产生多个新的投票  区块6新的签名队列开始出块
		//c,d未出块且他们选出的签名者也没有出块
		{
			addrNames:        []string{"A", "B", "C", "D", "E", "F", "H"},
			selfVoters:       []testerSelfVoter{{"A", 260}, {"B", 205}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{}},//b
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{{from: "C", to: "D", balance: 200, isVote: true}, {from: "D", to: "C", balance: 220, isVote: true}, {from: "E", to: "E", balance: 280, isVote: true}, {from: "F", to: "H", balance: 320, isVote: true}}},//b
				{5,[]testerTransaction{}},//a 产生新的队列 heacb
				{6,[]testerTransaction{}},//h
				{7,[]testerTransaction{}},//e
				{8,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 4, []uint64{4}, []uint64{260}, []uint64{260}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 2, []uint64{2}, []uint64{205}, []uint64{205}),
					"C": big.NewInt(0),
					"D": big.NewInt(0),
					"E": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 1, []uint64{1}, []uint64{260}, []uint64{260}),
					"F": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 0, []uint64{1}, []uint64{320}, []uint64{320}),
					"H": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 1, []uint64{0}, []uint64{205}, []uint64{205}),
				},
			},
		},

		// case12:发起提议改变旷工奖励比例为千分之816,发起提议但无人响应  故依旧使用默认
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from:"A", to:"A", isProposal:true, txHash:"1234", minerRewardPerT:816}}},//b
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{}},//b
				{5,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 3, []uint64{3}, []uint64{100}, []uint64{100}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 2, []uint64{2}, []uint64{200}, []uint64{200}),
					"C": big.NewInt(0),
				},
			},
		},

		// case13:base12 but同意节点只有A不足2/3
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from:"A", to:"A", isProposal:true, txHash:"1234", minerRewardPerT:816,validationLoopCnt:1, proposalType:3}}},//b
				{3,[]testerTransaction{{from:"A", to:"A", isDeclare:true, txHash:"1234", decision:true}}},//a
				{4,[]testerTransaction{}},//b
				{5,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 3, []uint64{3}, []uint64{100}, []uint64{100}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 2, []uint64{2}, []uint64{200}, []uint64{200}),
					"C": big.NewInt(0),
				},
			},
		},

		// case14:base13 同意节点超过2/3 但是没到新的奖励周期 比例海安原来算
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from:"A", to:"A", isProposal:true, txHash:"1234", minerRewardPerT:816,validationLoopCnt:1, proposalType:3}}},//b
				{3,[]testerTransaction{{from:"A", to:"A", isDeclare:true, txHash:"1234", decision:true}, {from:"B", to:"B", isDeclare:true, txHash:"1234", decision:true}}},//a
				{4,[]testerTransaction{}},//b
				{5,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 3, []uint64{3}, []uint64{100}, []uint64{100}),
					"B": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 2, []uint64{2}, []uint64{200}, []uint64{200}),
					"C": big.NewInt(0),
				},
			},
		},

		// case15:base14 新的奖励比例生效
		{
			addrNames:        []string{"A", "B", "C"},
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from:"A", to:"A", isProposal:true, txHash:"1234", minerRewardPerT:816,validationLoopCnt:1, proposalType:3}}},//b
				{3,[]testerTransaction{{from:"A", to:"A", isDeclare:true, txHash:"1234", decision:true}, {from:"B", to:"B", isDeclare:true, txHash:"1234", decision:true}}},//a
				{4,[]testerTransaction{}},//b  3+3+1=7 >7后提议生效
				{5,[]testerTransaction{}},//a 5
				{6,[]testerTransaction{}},//b 6
				{7,[]testerTransaction{}},//a 7
				{8,[]testerTransaction{}},//b 8   //这里开始按新的奖励比例计算
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
			result:testerRewardResult{
				map[string]*big.Int{
					"A": CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 4, []uint64{4}, []uint64{100}, []uint64{100}),
					"B": big.NewInt(0).Add(
						CalReward(defaultBlockReward, defaultMinerRewardPerThousand, 3, []uint64{3}, []uint64{200}, []uint64{200}),
						CalReward(defaultBlockReward, uint64(816), 1, []uint64{1}, []uint64{200}, []uint64{200})),
					"C": big.NewInt(0),
				},
			},
		},

		// case16:奖励衰减怎么测 如果针对一个块块号简单  这里连续块,总不能一直从块1算到一年后的块 再想想
		//
	}

	for i ,tt := range tests {

		genesis := &core.Genesis{
			ExtraData: make([]byte, extraVanity+extraSeal),
		}
		db := ethdb.NewMemDatabase()
		genesis.Commit(db)
		//state
		state, _ := state.New(common.Hash{}, state.NewDatabase(db))

		//账户池
		accountsPool := newTestAccountPool()
		_, ks := tmpKeyStore(t, true)
		for _, name := range tt.addrNames {
			account, _ := ks.NewAccount(name)
			accountsPool.accounts[name] = &account
			ks.Unlock(account, name)
		}

		var snap *Snapshot
		var genesisVotes []*Vote
		var selfVoteSigners []common.Address
		for _, voter := range tt.selfVoters {
			vote := &Vote{
				Voter:     accountsPool.accounts[voter.voter].Address,
				Candidate: accountsPool.accounts[voter.voter].Address,
				Stake:     big.NewInt(int64(voter.balance)),
			}
			genesisVotes = append(genesisVotes, vote)
			selfVoteSigners = append(selfVoteSigners, vote.Candidate)
		}
		// Create new alien
		alienCfg := &params.AlienConfig{
			Period:          uint64(3),
			Epoch:           uint64(10),
			MinVoterBalance: big.NewInt(100),
			MaxSignerCount:  uint64(5),
			SelfVoteSigners: selfVoteSigners,
		}
		alien := New(alienCfg, db)

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

		headers := make([]*types.Header, len(tt.txHeaders))
		for j, header := range tt.txHeaders {

			var currentBlockVotes []Vote
			var currentBlockProposals []Proposal
			var currentBlockDeclares []Declare
			var modifyPredecessorVotes []Vote
			for _, trans := range header.txs {
				if trans.isVote && big.NewInt(int64(trans.balance)).Cmp(alienCfg.MinVoterBalance) >= 0 {
					currentBlockVotes = append(currentBlockVotes, Vote{
						Voter:     accountsPool.accounts[trans.from].Address,
						Candidate: accountsPool.accounts[trans.to].Address,
						Stake:     big.NewInt(int64(trans.balance)),
					})
				} else if trans.isProposal {
					proposal := Proposal{
						Hash:                   common.HexToHash("0000"),
						ValidationLoopCnt:      uint64(1),
						ProposalType:           trans.proposalType,
						Proposer:               common.Address{},
						Candidate:              common.Address{},
						MinerRewardPerThousand: minerRewardPerThousand,
						Declares:               []*Declare{},
						ReceivedNumber:         big.NewInt(int64(header.number)),
					}
					if trans.minerRewardPerT != 0 {
						proposal.MinerRewardPerThousand = trans.minerRewardPerT
					}
					if trans.validationLoopCnt != 0 {
						proposal.ValidationLoopCnt = trans.validationLoopCnt
					}
					if snap.isCandidate(accountsPool.accounts[trans.from].Address) && len(trans.from) !=0 {
						proposal.Proposer = accountsPool.accounts[trans.from].Address
					}
					if snap.isCandidate(accountsPool.accounts[trans.to].Address) && len(trans.to) !=0 {
						proposal.Candidate = accountsPool.accounts[trans.to].Address
					}
					currentBlockProposals = append(currentBlockProposals, proposal)
				} else if trans.isDeclare {
					if snap.isCandidate(accountsPool.accounts[trans.from].Address) {

						currentBlockDeclares = append(currentBlockDeclares, Declare{
							ProposalHash: common.HexToHash(trans.txHash),
							Declarer:     accountsPool.accounts[trans.from].Address,
							Decision:     trans.decision,
						})

					}
				} else {
					modifyPredecessorVotes = append(modifyPredecessorVotes, Vote{
						Voter: accountsPool.accounts[trans.from].Address,
						Stake: big.NewInt(int64(trans.balance)),
					})
				}
			}
			currentHeaderExtra := HeaderExtra{}
			signer := common.Address{}

			// (j==0) means firstNumber
			if j == 0 {
				for k := 0; k < int(alienCfg.MaxSignerCount); k++ {
					currentHeaderExtra.SignerQueue = append(currentHeaderExtra.SignerQueue, selfVoteSigners[k%len(selfVoteSigners)])
				}
				currentHeaderExtra.LoopStartTime = alienCfg.GenesisTimestamp
				signer = selfVoteSigners[0]

			} else {
				// decode parent header.extra
				rlp.DecodeBytes(headers[j-1].Extra[extraVanity:len(headers[j-1].Extra)-extraSeal], &currentHeaderExtra)
				signer = currentHeaderExtra.SignerQueue[uint64(j)%alienCfg.MaxSignerCount]
				// means header.Number % tt.maxSignerCount == 0
				if (j+1)%int(alienCfg.MaxSignerCount) == 0 {
					snap, err := alien.snapshot(&testerChainReader{db: db}, headers[j-1].Number.Uint64(), headers[j-1].Hash(), headers, nil, uint64(1))
					if err != nil {
						t.Errorf("testReward case%d: failed to create voting snapshot: %v", i, err)
						continue
					}

					currentHeaderExtra.SignerQueue = []common.Address{}
					newSignerQueue, err := snap.createSignerQueue()
					if err != nil {
						t.Errorf("testReward case%d: failed to create signer queue: %v", i, err)
					}

					currentHeaderExtra.SignerQueue = newSignerQueue
					currentHeaderExtra.LoopStartTime = currentHeaderExtra.LoopStartTime + alienCfg.Period*alienCfg.MaxSignerCount
				} else {

				}
			}

			currentHeaderExtra.CurrentBlockVotes = currentBlockVotes
			currentHeaderExtra.ModifyPredecessorVotes = modifyPredecessorVotes
			currentHeaderExtra.CurrentBlockProposals = currentBlockProposals
			currentHeaderExtra.CurrentBlockDeclares = currentBlockDeclares
			currentHeaderExtraEnc, err := encodeHeaderExtra(alienCfg, big.NewInt(int64(j)), currentHeaderExtra)
			if err != nil {
				t.Errorf("testReward case%d: failed to rlp encode to bytes: %v", i, err)
				continue
			}

			// Create the genesis block with the initial set of signers
			ExtraData := make([]byte, extraVanity+len(currentHeaderExtraEnc)+extraSeal)
			copy(ExtraData[extraVanity:], currentHeaderExtraEnc)

			headers[j] = &types.Header{
				Number:   new(big.Int).SetUint64(header.number),
				Time:     big.NewInt((int64(j)+1)*int64(defaultBlockPeriod) - 1),
				Coinbase: signer,
				Extra:    ExtraData,
			}
			if j > 0 {
				headers[j].ParentHash = headers[j-1].Hash()
			}
			sig, err := ks.SignHash(accounts.Account{Address: signer}, sigHash(headers[j]).Bytes())
			copy(headers[j].Extra[len(headers[j].Extra)-65:], sig)

			snap, err = alien.snapshot(&testerChainReader{db: db}, headers[j].Number.Uint64(), headers[j].Hash(), headers[:j+1], genesisVotes, uint64(1))
			if err != nil {
				t.Errorf("testReward case%d: failed to create voting snapshot: %v", i, err)
				continue
			}

			//core.NewBlockChain(db, nil, chainCfg, alien, vm.Config{})

			/*var receipts []*types.Receipt
			var blockGen *core.BlockGen
			//todo GasLimit设置头
			blockGen.SetCoinbase(signer)
			for k, _ := range header.txs {

				tx := types.NewTransaction(
					uint64(k),
					common.Address{},
					big.NewInt(0), 0, big.NewInt(0),
					nil,
				)
				receipt, _, err := core.ApplyTransaction(chainCfg, bc, &b.header.Coinbase, b.gasPool, b.statedb, b.header, tx, &b.header.GasUsed, vm.Config{})
			}*/

			//reward
			accumulateRewards(chainCfg, state, headers[j], snap, nil)

			// 构造historyHash
			for _, string := range tt.historyHashes {

				var hash common.Hash
				hash.SetString(string)
				snap.HistoryHash = append(snap.HistoryHash[1:len(snap.HistoryHash)], hash)
				//snap.HistoryHash = append(snap.HistoryHash, hash)
				snap.Hash = hash
			}
		}

		// verify reward
		//balance := state.GetBalance(accountsPool.accounts["A"].Address)
		//fmt.Println("check balance", balance.Uint64())
		//fmt.Println("check balance", balance.Div(balance, new(big.Int).SetUint64(1e+18)))
		for _, name := range tt.addrNames {
			if state.GetBalance(accountsPool.accounts[name].Address).Cmp(tt.result.balance[name]) != 0{
				t.Errorf("tesetReward case%d fail:%s balance:%v in BLC dismatch %v in test result\n", i, name, state.GetBalance(accountsPool.accounts[name].Address), tt.result.balance[name])
			}
		}

	}
}

