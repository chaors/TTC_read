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

func calMinerRewardPerBlock(number uint64) *big.Int {

	s := big.NewInt(5e+18)
	blockNumPerYear := secondsPerYear / 3
	yearCount := number / uint64(blockNumPerYear)
	blockReward := new(big.Int).Rsh(s, uint(yearCount))

	minerReward := new(big.Int).Set(blockReward)
	minerReward.Mul(minerReward, big.NewInt(int64(minerRewardPerThousand)))
	minerReward.Div(minerReward, big.NewInt(1000))

	return minerReward
}

func calVoteRewardPerBlock(number uint64) *big.Int {

	s := big.NewInt(5e+18)
	blockNumPerYear := secondsPerYear / 3
	yearCount := number / uint64(blockNumPerYear)
	blockReward := new(big.Int).Rsh(s, uint(yearCount))

	minerReward := new(big.Int).Set(blockReward)
	minerReward.Mul(minerReward, big.NewInt(int64(minerRewardPerThousand)))
	minerReward.Div(minerReward, big.NewInt(1000))

	votersReward := big.NewInt(0).Sub(blockReward, minerReward)

	return votersReward
}


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

			err = alien.VerifySeal(chainReader, b.Header())
			if err != nil {

				t.Errorf("test%v: failed to VerifySeal: %v", i, err)
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

func TestAccumulateRewards(t *testing.T)  {

	//但是这个只能针某一个块，有局限性  需要重新想测试结构！！！

	s := big.NewInt(5e+18)
	r := new(big.Int).Set(s)
	m1 := r.Mul(r, big.NewInt(618))
	m1 = m1.Div(m1, big.NewInt(1000))
	m := m1.Uint64()
	v := s.Sub(s, m1).Uint64()

	tests := []struct {
		addNames	[]string
		number	uint64
		coinBase	string
		votes	[]testerVote
		proposals []testerProposal
		selfVoters []testerSelfVoter
		result []map[string]*big.Int
	}{
		//case0 A的选票全部来自A自己，故奖励被全部属于A
		{
			addNames:[]string{"A", "B", "C"},
			number:3,
			coinBase:string("A"),
			votes:[]testerVote{
				{"A", "A", 100},
			},
			proposals:[]testerProposal{},
			selfVoters:[]testerSelfVoter{{"A", 100}, {"B", 100}, {"C", 160}},

			result: []map[string]*big.Int{
				{"A":CalReward(m,1, v, []uint64{1}, []uint64{100}, []uint64{100})},
				{"B":big.NewInt(0)},
				{"C":big.NewInt(0)},
			},
		},

		//case B的投票来自A,B,C 因此，B，C会拿到相应投票奖励 投票数越多奖励越多
		{
			addNames:[]string{"A", "B", "C"},
			number:3,
			coinBase:string("B"),
			votes:[]testerVote{
				{"A", "B", 150},
				{"B", "B", 100},
				{"C", "B", 200},
			},
			proposals:[]testerProposal{},
			selfVoters:[]testerSelfVoter{{"A", 100}, {"B", 100}, {"C", 160}},

			result: []map[string]*big.Int{
				{"A":CalReward(m,0, v, []uint64{1}, []uint64{150}, []uint64{450})},
				{"B":CalReward(m,1, v, []uint64{1}, []uint64{100}, []uint64{450})},
				{"C":CalReward(m,0, v, []uint64{1}, []uint64{200}, []uint64{450})},
			},
		},

		//case2 奖励产生衰减
		{
			addNames:[]string{"A", "B", "C"},
			number:24*60*60*365/3+33,  //逐年减半
			coinBase:string("A"),
			votes:[]testerVote{
				{"A", "A", 100},
			},
			proposals:[]testerProposal{},
			selfVoters:[]testerSelfVoter{{"A", 100}, {"B", 100}, {"C", 160}},

			result: []map[string]*big.Int{
				{"A":CalReward(calMinerRewardPerBlock(uint64(24*60*60*365/3+33)).Uint64(),1, calVoteRewardPerBlock(uint64(24*60*60*365/3+33)).Uint64(), []uint64{1}, []uint64{100}, []uint64{100})},
				{"B":big.NewInt(0)},
				{"C":big.NewInt(0)},
			},
		},
	}


	for i, tt := range tests {

		fmt.Printf("%v", tt.number)

		//账户池
		accountsPool := newTestAccountPool()
		_, ks := tmpKeyStore(t, true)

		for _, name := range tt.addNames {

			account, _ := ks.NewAccount(name)
			accountsPool.accounts[name] = &account
		}

		genesis := &core.Genesis{
			ExtraData: make([]byte, extraVanity+extraSeal),
		}

		// Create a pristine blockchain with the genesis injected
		db := ethdb.NewMemDatabase()
		genesis.Commit(db)

		//state
		state, _ := state.New(common.Hash{}, state.NewDatabase(db))

		var currentSigners []common.Address
		for _, selfVoter := range tt.selfVoters {
			currentSigners = append(currentSigners, accountsPool.accounts[selfVoter.voter].Address)
		}
		alienCfg := &params.AlienConfig{
			Period:          uint64(3),
			Epoch:           uint64(10),
			MinVoterBalance: big.NewInt(int64(50)),
			MaxSignerCount:  uint64(3),
			SelfVoteSigners: currentSigners,  //这里实际指的是当前的签名者
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
			Number:   new(big.Int).SetUint64(tt.number),
			Time:     big.NewInt(time.Now().Unix()),
			Coinbase: accountsPool.accounts[tt.coinBase].Address,
		}

		votes := []*Vote{}
		for _, vote := range tt.votes {

			snapVote := Vote{accountsPool.accounts[vote.voter].Address, accountsPool.accounts[vote.candidate].Address, big.NewInt(int64(vote.stake))}
			votes = append(votes, &snapVote)
		}

		snap := newSnapshot(alien.config, alien.signatures, header.Hash(), votes, 2)

		//test
		accumulateRewards(chainCfg, state, header, snap, RefundGas{})

		//verify
		for _, result := range tt.result  {
			for k, v := range result {
				balance := state.GetBalance(accountsPool.accounts[k].Address)
				if balance.Cmp(v) != 0{
					t.Errorf("balance%d tset fail:%s balance:%v in BLC dismatch %v in test result ", i, k, balance, v)
				}
			}
		}
	}
	}

func TestReward(t *testing.T)  {

	//不能写死这
	s := big.NewInt(5e+18)
	r := new(big.Int).Set(s)
	m1 := r.Mul(r, big.NewInt(618))
	m1 = m1.Div(m1, big.NewInt(1000))
	m := m1.Uint64()
	v := s.Sub(s, m1).Uint64()

	//defaultBlockReward := big.NewInt(5e+18)
	//defaultMinerRewardPerThousand := big.NewInt(618)


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
					"A": CalReward(m, 1, v, []uint64{1}, []uint64{100}, []uint64{100}),
					"B": CalReward(m, 1, v, []uint64{1}, []uint64{100}, []uint64{100}),
					"C": big.NewInt(0),
				},
			},
		},

		/**
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
					"A": CalReward(m, 1, v, []uint64{1}, []uint64{100}, []uint64{100}),
					"B": CalReward(m, 1, v, []uint64{1}, []uint64{200}, []uint64{300}),
					"C": CalReward(m, 0, v, []uint64{1}, []uint64{100}, []uint64{300}),
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
					"A": CalReward(m, 2, v, []uint64{1,1}, []uint64{100,100}, []uint64{100,300}),
					"B": CalReward(m, 1, v, []uint64{1}, []uint64{200}, []uint64{300}),
					"C": big.NewInt(0),
				},
			},
		},

		//case 3:奖励衰减怎么测 如果针对一个块块号简单  这里连续块 块号跳到一年后的块程序怎么处理？？？？
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
					"A": big.NewInt(0).Add(CalReward(m, 2, v, []uint64{1,1}, []uint64{100,100}, []uint64{100,300}), big.NewInt(0).Mul(big.NewInt(0), big.NewInt(1e18))), //m*1 + v*1*100/100,
					"B": big.NewInt(0).Add(CalReward(m, 1, v, []uint64{1}, []uint64{200}, []uint64{300}), big.NewInt(0).Mul(big.NewInt(0), big.NewInt(1e18))), //m*1 + v*1*100/100,
					"C": big.NewInt(0).Add(CalReward(m, 0, v, []uint64{0}, []uint64{100}, []uint64{200}), big.NewInt(0).Mul(big.NewInt(0), big.NewInt(1e18))), //m*1 + v*1*100/100,
				},
			},
		},
		*/

		//case 4:提议改变奖励的比例

		/*  之前测试结构的！！！！
		// balance3 A,B两个自选签名者(A,B)，目前出了2个块 a,b轮流出块
		// 在区块2 A把票转投给了B, 但是在此前区块1A还是出块者A的投票者  会有投票奖励 后面依然有自己的挖矿奖励和来自B的投票奖励
		// 此时新一轮签名轮次还未到来  还是由A，B轮流出块

		// balance4 A,B两个自选签名者(A,B)
		// 在区块2 A把票转投给了B, 但是在此前区块1A还是出块者A的投票者  会有投票奖励 后面依然有自己的挖矿奖励和来自B的投票奖励
		// 新一轮签名轮次已经到来  签名者只有b 区块3后只有B能出块，A获得的只是B挖矿的投票奖励


		// balance5 A,B两个自选签名者(A,B)，目前出了2个块 a,b轮流出块
		// 在区块2 A把票转投给了C, 但是在此前区块1A还是出块者A的投票者  会有投票奖励 后面依然有自己的挖矿奖励和来自B的投票奖励
		// 新一轮签名轮次还未到来  所以C并没有参与挖矿故没有余额

		// balance6 A,B两个自选签名者(A,B)，目前出了2个块 a,b轮流出块
		// 在区块2 A把票转投给了C, 但是在此前区块1A还是出块者A的投票者  会有投票奖励 后面依然有自己的挖矿奖励和来自B的投票奖励
		// 新一轮签名轮次已经到来  所以C也参与1次挖矿有相应的奖励

		//balance7 A,B两个自选签名者(A,B)，目前出了2个块 a,b轮流出块
		// 在区块2 A把票转投给了B,B把票投给了A 但是在此前区块1A还是出块者A的投票者  会有投票奖励
		// 后面他们各自得到自己的挖矿奖励和对方挖矿的投票奖励

		//balance8 A,B两个自选签名者(A,B)，目前出了2个块 a,b轮流出块
		// 在区块2 A把票转投给了B,  区块4B把票投给了A 这个时候要注意在投票前各自的投票奖励与自己相关和投票比例变化
		// 后面他们各自得到自己的挖矿奖励和对方挖矿的投票奖励

		//balance9 A,B两个自选签名者(A,B)，目前出了2个块 a,b轮流出块
		// 在区块2 C把票转投给了B,  虽然C不是出块者，但是他能够分到b出块的投票奖励

		//balance10 A,B两个自选签名者(A,B)，
		// 在区块2 A把票转投给了B,  在区块4 A把票又投回给自己

		//balance11 A,B两个自选签名者(A,B)，
		// 在区块2 A把票转投给了B,  在区块5 A把票又投回给自己

		//balance12 A,B两个自选签名者(A,B) 区块4产生多个新的投票  区块6新的签名队列开始出块

		*/
	}

	for i ,tt := range tests {

		//fmt.Printf("%v", t)

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

			//初始余额设置
			//state.AddBalance(account.Address, big.NewInt(0).Mul(big.NewInt(100), big.NewInt(1e+18)))
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

			//b := state.GetBalance(vote.Candidate)
			//b.Sub(big.NewInt(0).Mul(vote.Stake, big.NewInt(1e+18)), b)
			//state.AddBalance(vote.Candidate, b)
			}
		// Create new alien
		alienCfg := &params.AlienConfig{
			Period:          uint64(3),
			Epoch:           uint64(10),
			MinVoterBalance: big.NewInt(100),
			MaxSignerCount:  uint64(3),
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
		lastNumber := uint64(0) //用于块号跳跃以测区块衰减的的case
		for j, header := range tt.txHeaders {

			var currentBlockVotes []Vote
			var currentBlockProposals []Proposal
			var currentBlockDeclares []Declare
			var modifyPredecessorVotes []Vote
			for _, trans := range header.txs {
				if trans.isVote {
					currentBlockVotes = append(currentBlockVotes, Vote{
						Voter:     accountsPool.accounts[trans.from].Address,
						Candidate: accountsPool.accounts[trans.to].Address,
						Stake:     big.NewInt(int64(trans.balance)),
					})
				} else if trans.isProposal {
					minerRewardPT := minerRewardPerThousand
					if trans.minerRewardPerT != 0 {
						minerRewardPT = trans.minerRewardPerT
					}
					if snap.isCandidate(accountsPool.accounts[trans.from].Address) {
						currentBlockProposals = append(currentBlockProposals, Proposal{
							Hash:                   common.HexToHash(trans.txHash),
							ValidationLoopCnt:      uint64(1),
							ProposalType:           trans.proposalType,
							Proposer:               accountsPool.accounts[trans.from].Address,
							Candidate:              accountsPool.accounts[trans.candidate].Address,
							MinerRewardPerThousand: minerRewardPT,
							Declares:               []*Declare{},
							ReceivedNumber:         big.NewInt(int64(j)),
						})
					}
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
				//fmt.Printf("ccc signer get:j---%d======maxSig----%d\n SignerQueue:%v", j, tt.maxSignerCount, currentHeaderExtra.SignerQueue)
				signer = currentHeaderExtra.SignerQueue[uint64(j)%alienCfg.MaxSignerCount]
				// means header.Number % tt.maxSignerCount == 0
				if (j+1)%int(alienCfg.MaxSignerCount) == 0 {
					snap, err := alien.snapshot(&testerChainReader{db: db}, headers[j-1].Number.Uint64(), headers[j-1].Hash(), headers, nil, uint64(1))
					if err != nil {
						t.Errorf("test %d: failed to create voting snapshot: %v", i, err)
						continue
					}

					currentHeaderExtra.SignerQueue = []common.Address{}
					newSignerQueue, err := snap.createSignerQueue()
					if err != nil {
						t.Errorf("test %d: failed to create signer queue: %v", i, err)
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
				t.Errorf("test %d: failed to rlp encode to bytes: %v", i, err)
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

			// Pass all the headers through alien and ensure tallying succeeds
			if headers[j].Number.Uint64() == lastNumber+1 {
				snap, err = alien.snapshot(&testerChainReader{db: db}, headers[j].Number.Uint64(), headers[j].Hash(), headers[:j+1], genesisVotes, uint64(1))
			}else {
				snap, err = alien.snapshot(&testerChainReader{db: db}, headers[j].Number.Uint64(), headers[j].Hash(), headers[:j+1], genesisVotes, uint64(1))
			}

			lastNumber = header.number

			genesisVotes = []*Vote{}
			if err != nil {
				t.Errorf("test %d: failed to create voting snapshot: %v", i, err)
				continue
			}

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

		//balance := state.GetBalance(accountsPool.accounts["A"].Address)
		//fmt.Println("check balance", balance.Uint64())
		//fmt.Println("check balance", balance.Div(balance, new(big.Int).SetUint64(1e+18)))

		for _, name := range tt.addrNames {
			if state.GetBalance(accountsPool.accounts[name].Address).Cmp(tt.result.balance[name]) != 0{
				t.Errorf("balance%d tset fail:%s balance:%v in BLC dismatch %v in test result ", i, name, state.GetBalance(accountsPool.accounts[name].Address), tt.result.balance[name])
			}
		}

	}
}

