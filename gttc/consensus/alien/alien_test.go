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
//V1.2.0
const (
	veryLightScryptN = 2
	veryLightScryptP = 1
)

const (
	veryLightScryptN1 = 2
	veryLightScryptP1 = 1
)

type testerBlockHeader struct {
	number uint64
	txs []testerTransaction
}

type testerRewardResult struct {
	balance map[string]*big.Int
}

func (r *testerChainReader) GetHeader(hash common.Hash, number uint64) *types.Header {
	return rawdb.ReadHeader(r.db, rawdb.ReadCanonicalHash(r.db, number), number)
}

func (r *testerChainReader) GetGenesisHeader() *types.Header {
	return rawdb.ReadHeader(r.db, rawdb.ReadCanonicalHash(r.db, 0), 0)
}

//KeyStore用于创建地址
func tmpKeyStore(t *testing.T, encrypted bool) (string, *keystore.KeyStore) {
	d, err := ioutil.TempDir("", "alien-keystore-test")
	if err != nil {
		t.Fatal(err)
	}

	return d, keystore.NewKeyStore(d, veryLightScryptN, veryLightScryptP)
}

//地址池 key:A,B,C(同时作为Account密码)  value:Account
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
func calReward(blockReward *big.Int, minerRewardPerT uint64, minerCount uint64, vCounts []uint64, bals []uint64, allStakes []uint64) *big.Int {
	minerReward := new(big.Int).Set(blockReward)
	minerReward.Mul(minerReward, new(big.Int).SetUint64(minerRewardPerT))
	minerReward.Div(minerReward, big.NewInt(1000))
	votersReward := big.NewInt(0).Sub(blockReward, minerReward)

	//矿工奖励
	asMinerReward := big.NewInt(0).Mul(minerReward, new(big.Int).SetUint64(minerCount))
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
				{2,[]testerTransaction{{from: "C", to: "B", balance:100, isVote: true}}}, // B
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

// Test that reward decrease correctly year by year
func TestRewardDecreaseByYears(t *testing.T) {

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
				{"A": calReward(big.NewInt(0).Div(SignerBlockReward, big.NewInt(2)), minerRewardPerThousand, 1, []uint64{1}, []uint64{100}, []uint64{100})},
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
				{"A": calReward(big.NewInt(0).Div(SignerBlockReward, big.NewInt(4)), minerRewardPerThousand, 1, []uint64{1}, []uint64{100}, []uint64{100})},
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

// TODO:need add sidechain & trantorBlock fork
// Test that reward is issued correctly to miner and voters per block
func TestReward(t *testing.T)  {
	// Define the various reward scenarios to test
	tests := []struct {
		addrNames        []string
		period           uint64
		epoch            uint64
		minVoterBalance  uint64
		maxSignerCount   uint64
		selfVoters       []testerSelfVoter
		txHeaders        []testerBlockHeader
		historyHashes    []string
		result           testerRewardResult
	}{
		// case 0:balance0 A,B两个自选签名者(A,B)，目前只出了2个块 分别由A,B挖出
		// 签名者都是只有自己选出，所以出块奖励都属于对应的签名者
		{
			addrNames:[]string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
			selfVoters:[]testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{}}, // B
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
		},

		// case 1:balance0 A,B两个自选签名者(A,B)，目前出了2个块 在区块2C投票给B
		//每次计算奖励是按照上一个块的投票快照计算的 因此块2C还分不到投票奖励
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "C", to: "B", balance:100, isVote: true}}}, // B
				//A
				//B
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
		},

		// case 2: A,B两个自选签名者(A,B)，区块2A转投给B 块2之后会获得B出块产生的投票奖励 但新的签名轮次未到来A还是签名者(无地址给A投票)
		//因此块1块2的出块奖励分别属于A,B,块3A只能得到矿工奖励
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "B", balance:100, isVote: true}}}, // B
				{3,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
		},


		// case3: C投票给B但是余额不满足投票限制  因此投票失败
		// 所以C不会拿到B出块产生的投票奖励
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				// 80 < minVoterBalance vote不会成功
				{2,[]testerTransaction{{from: "C", to: "B", balance:80, isVote: true}}}, // B
				{3,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
		},

		// case4:A在区块2把票转投给C(无地址给A投票) 以后A出块将只获得矿工奖励
		// 此时C还未被加入到签名者队列(新的签名轮次没来),c并没有任何奖励
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "C", balance:280, isVote: true}}}, // B
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{}},//b
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
		},

		// case5:base4 新的签名轮次到达  C参与出块会获得矿工奖励,同时块6的投票奖励由A所得
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
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
		},

		// case6:在区块2 A把票转投给了B,B把票投给了A 但是在此前区块1A还是A的投票者会有投票奖励
		// 块2之后B出块产生的投票奖励由A，B按比例所得
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "B", balance:100, isVote: true}, {from: "B", to: "A", balance:200, isVote: true}}}, // B
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{}},//b
				{5,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
		},

		// case7:在块2 A把票转投给了B,块4B把票投给了A 这个时候要注意在投票前各自的投票奖励与自己相关和投票比例变化
		// 块2之后块5之前b出块的投票奖励由A,B按比例所得，块5开始,A出块投票奖励属B所得,B出块投票奖励属A所得
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{{from: "B", to: "A", balance: 200, isVote: true}}},//b
				{5,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
		},

		// case8:在区块2 A把票转投给了B,  在区块4 A把票又投回给自己
		// 块2之后块5之前B出块的投票奖励由A,B按比例所得,A出块只有矿工奖励 块5开始A出块的投票奖励由A所得,B出块的投票奖励由B所得
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{{from: "A", to: "A", balance: 200, isVote: true}}},//b
				{5,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
		},

		// case9:base8 在区块5 A把票又投回给自己  此时只出块到7 A被投但还未加入出块者中
		//块2之后块6之前B出块的投票奖励由A,B按比例所得,A出块只有矿工奖励 块6开始b出块投票奖励归自己
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
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
		},

		// case10:base9 此时只出块到12 块11开始A被加入出块者中，之后A出块投票奖励归A
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
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
		},

		// case11:区块4产生多个新的投票  区块6新的签名队列开始出块
		//到块8为止c,d未出块且他们选出的签名者也没有出块,因此没有奖励
		{
			addrNames:        []string{"A", "B", "C", "D", "E", "F", "H"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
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
		},

		// case12:发起提议改变矿工奖励比例为千分之816,发起提议但无人响应,故依旧使用默认来计算奖励
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from:"A", to:"A", isProposal:true, txHash:"1234", minerRewardPerT:816, validationLoopCnt:1, proposalType:3, receivedNumber:2}}},//b
				{3,[]testerTransaction{}},//a
				{4,[]testerTransaction{}},//b
				{5,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
		},

		// case13:base12 此时同意节点只有A,不足2/3,故依旧使用默认来计算奖励
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from:"A", to:"A", isProposal:true, txHash:"1234", minerRewardPerT:816, validationLoopCnt:1, proposalType:3, receivedNumber:2}}},//b
				{3,[]testerTransaction{{from:"A", to:"A", isDeclare:true, txHash:"1234", decision:true}}},//a
				{4,[]testerTransaction{}},//b
				{5,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
		},

		// case14:base13 同意节点超过2/3 但是没到新的奖励周期,故依旧使用默认来计算奖励
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from:"A", to:"A", isProposal:true, txHash:"1234", minerRewardPerT:816,validationLoopCnt:1, proposalType:3, receivedNumber:2}}},//b
				{3,[]testerTransaction{{from:"A", to:"A", isDeclare:true, txHash:"1234", decision:true}, {from:"B", to:"B", isDeclare:true, txHash:"1234", decision:true}}},//a
				{4,[]testerTransaction{}},//b
				{5,[]testerTransaction{}},//a
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
		},

		// case15:base14 块2发起提议,也得到2/3地址通过,到块8提议开始生效  所以计算奖励要按照新的比例千分之816计算
		{
			addrNames:        []string{"A", "B", "C"},
			period:3,
			epoch:10,
			minVoterBalance:100,
			maxSignerCount:5,
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerBlockHeader{
				{1,[]testerTransaction{}}, // 1 A
				{2,[]testerTransaction{{from:"A", to:"A", isProposal:true, txHash:"1234", minerRewardPerT:816, validationLoopCnt:1, proposalType:3, receivedNumber:2}}},//b
				{3,[]testerTransaction{{from:"A", to:"A", isDeclare:true, txHash:"1234", decision:true}, {from:"B", to:"B", isDeclare:true, txHash:"1234", decision:true}}},//a
				{4,[]testerTransaction{}},//b  3+3+1=7 >7后提议生效
				{5,[]testerTransaction{}},//a 5
				{6,[]testerTransaction{}},//b 6
				{7,[]testerTransaction{}},//a 7
				{8,[]testerTransaction{}},//b 8   //这里开始按新的奖励比例计算
			},
			historyHashes: []string{"a", "b", "c", "d", "e"},
		},
	}

	for i ,tt := range tests {

		genesis := &core.Genesis{
			ExtraData: make([]byte, extraVanity+extraSeal),
		}
		db := ethdb.NewMemDatabase()
		genesis.Commit(db)
		// state
		state, _ := state.New(common.Hash{}, state.NewDatabase(db))

		// Create the account pool and generate the initial set of all address in addrNames
		accountsPool := newTestAccountPool()
		// A reverse map which can get addrName by address(common.address)
		addrReverseMap := make(map[common.Address]string)
		_, ks := tmpKeyStore(t, true)
		for _, name := range tt.addrNames {
			account, _ := ks.NewAccount(name)
			accountsPool.accounts[name] = &account
			addrReverseMap[account.Address] = name
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

		// Record vote,proposal and declares from per block.used to calculate reward by test data
		headers := make([]*types.Header, len(tt.txHeaders))
		currentVotes := make(map[string]testerTransaction)
		currentProposals := make(map[string]testerTransaction)
		currentDeclares := make(map[string]testerTransaction)
		for _, vote := range tt.selfVoters {
			currentVotes[vote.voter] = testerTransaction{
				from:vote.voter, to:vote.voter, balance:vote.balance, isVote:true}
		}

		// Assemble headerExtra for headers from test data
		for j, header := range tt.txHeaders {

			var currentBlockVotes []Vote
			var currentBlockProposals []Proposal
			var currentBlockDeclares []Declare
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
				}
			}
			currentHeaderExtra := HeaderExtra{}
			signer := common.Address{}

			if j > 0 {
				for _, trans := range tt.txHeaders[j-1].txs {
					if trans.isVote && big.NewInt(int64(trans.balance)).Cmp(alienCfg.MinVoterBalance) >= 0 {
						currentVotes[trans.from] = trans
					} else if trans.isProposal {
						currentProposals[trans.txHash] = trans
					} else if trans.isDeclare {
						if snap.isCandidate(accountsPool.accounts[trans.from].Address) {
							currentDeclares[trans.txHash] = trans
						}
					}
				}
			}

			// (j==0) means firstNumber block
			chainReader := &testerChainReader{db: db}
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
					snap, err := alien.snapshot(chainReader, headers[j-1].Number.Uint64(), headers[j-1].Hash(), nil, genesisVotes, uint64(1))
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
				}
			}

			currentHeaderExtra.CurrentBlockVotes = currentBlockVotes
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
				ParentHash:chainReader.GetHeaderByNumber(uint64(j)).Hash(),
			}

			// Sign the current header
			sig, err := ks.SignHash(accounts.Account{Address: signer}, sigHash(headers[j]).Bytes())
			copy(headers[j].Extra[len(headers[j].Extra)-65:], sig)

			rawdb.WriteCanonicalHash(db, headers[j].Hash(), headers[j].Number.Uint64())
			rawdb.WriteHeader(db, headers[j])

			snap, err = alien.snapshot(chainReader, headers[j].Number.Uint64()-1, headers[j].ParentHash, nil, genesisVotes, uint64(1))
			if err != nil {
				t.Errorf("testReward case%d: failed to create voting snapshot: %v", i, err)
				continue
			}

			// Reward before current block accumulateReward
			lastBances := testerRewardResult{}
			lastBances.balance = make(map[string]*big.Int)
			for _, name := range tt.addrNames {
				lastBances.balance[name] = state.GetBalance(accountsPool.accounts[name].Address)
			}

			// accumulateReward
			accumulateRewards(chainCfg, state, headers[j], snap, nil)

			// Reward generate by current block
			currentRewardsfromStateDb := testerRewardResult{}
			currentRewardsfromStateDb.balance = make(map[string]*big.Int)
			for _, name := range tt.addrNames {
				currentRewardsfromStateDb.balance[name] = big.NewInt(0).Sub(state.GetBalance(accountsPool.accounts[name].Address), lastBances.balance[name])
			}

			// Construct historyHash
			for _, string := range tt.historyHashes {

				var hash common.Hash
				hash.SetString(string)
				snap.HistoryHash = append(snap.HistoryHash[1:len(snap.HistoryHash)], hash)
				snap.Hash = hash
			}

			// AccumulateReward by test data
			currentTally := uint64(0)
			currentSignerTally := uint64(0)
			var currentVotersForSigner []map[string]uint64
			for voter,vote := range currentVotes {
				currentTally += uint64(vote.balance)
				if vote.to == addrReverseMap[headers[j].Coinbase] {
					currentSignerTally += uint64(vote.balance)
					voteCount := make(map[string]uint64)
					voteCount[voter] = uint64(vote.balance)
					currentVotersForSigner = append(currentVotersForSigner, voteCount)
				}
			}

			// default minerRewardPerThousand
			minerRewardPerT := minerRewardPerThousand
			for _, proposal := range currentProposals {
				yesDeclareStake := uint64(0)
				for _, declare := range currentDeclares {
					if declare.txHash == proposal.txHash && declare.decision {
						yesDeclareStake += uint64(currentVotes[declare.from].balance)
					}
				}
				if yesDeclareStake > currentTally/3*2 && proposal.proposalType == 3 && uint64(j+1) == proposal.receivedNumber+proposal.validationLoopCnt*tt.maxSignerCount+1 {
					minerRewardPerT = proposal.minerRewardPerT
				}
			}

			var currentRewardTest testerRewardResult
			currentRewardTest.balance = make(map[string]*big.Int)
			for _,name := range tt.addrNames {
				currentRewardTest.balance[name] = big.NewInt(0)
				// block reward
				if name == addrReverseMap[headers[j].Coinbase] {
					currentRewardTest.balance[name].Add(currentRewardTest.balance[name], calReward(SignerBlockReward, minerRewardPerT, 1, []uint64{0},[]uint64{0}, []uint64{100}))
				}
				// vote reward
				for _,voteCount := range currentVotersForSigner {
					for key,count := range voteCount {
						if name  == key {
							currentRewardTest.balance[name].Add(currentRewardTest.balance[name], calReward(SignerBlockReward, minerRewardPerT, 0, []uint64{1},[]uint64{count}, []uint64{currentSignerTally}))
						}
					}
				}
			}

			// Verify two value of reward from state and testdata
			for name, reward := range currentRewardsfromStateDb.balance {
				if reward.Cmp(currentRewardTest.balance[name]) != 0 {
					t.Errorf("tesetReward case%d-blockNumber:%v fail:%s balance:%v in BLC dismatch %v in test result\n", i, j+1, name, state.GetBalance(accountsPool.accounts[name].Address), tt.result.balance[name])
				}
			}
		}
	}
}