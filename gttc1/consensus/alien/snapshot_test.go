// Copyright 2018 The gttc Authors
// This file is part of the gttc library.
//
// The gttc library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The gttc library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the gttc library. If not, see <http://www.gnu.org/licenses/>.

// Package alien implements the delegated-proof-of-stake consensus engine.

package alien

import (
	"bytes"
	"crypto/ecdsa"
	"fmt"
	"github.com/TTCECO/gttc/core/state"
	"math/big"
	"testing"

	"github.com/TTCECO/gttc/common"
	"github.com/TTCECO/gttc/core"
	"github.com/TTCECO/gttc/core/rawdb"
	"github.com/TTCECO/gttc/core/types"
	"github.com/TTCECO/gttc/crypto"
	"github.com/TTCECO/gttc/ethdb"
	"github.com/TTCECO/gttc/params"
	"github.com/TTCECO/gttc/rlp"
)

type testerTransaction struct {
	from         string // name of from address
	to           string // name of to address
	balance      int    // balance address in snap.voter
	isVote       bool   // "ufo:1:event:vote"
	isProposal   bool   // "ufo:1:event:proposal:..."
	proposalType uint64 // proposalTypeCandidateAdd or proposalTypeCandidateRemove
	isDeclare    bool   // "ufo:1:event:declare:..."
	candidate    string // name of candidate in proposal
	txHash       string // hash of tx
	decision     bool   // decision of declare
}

type testerSingleHeader struct {
	txs []testerTransaction
}

type testerSelfVoter struct {
	voter   string // name of self voter address in genesis block
	balance int    // balance
}

type testerVote struct {
	voter     string
	candidate string
	stake     int
}

type testerSnapshot struct {
	Signers []string
	Votes   map[string]*testerVote
	Tally   map[string]int
	Voters  map[string]int
}

// testerAccountPool is a pool to maintain currently active tester accounts,
// mapped from textual names used in the tests below to actual Ethereum private
// keys capable of signing transactions.
type testerAccountPool struct {
	accounts map[string]*ecdsa.PrivateKey
}

func newTesterAccountPool() *testerAccountPool {
	return &testerAccountPool{
		accounts: make(map[string]*ecdsa.PrivateKey),
	}
}

func (ap *testerAccountPool) sign(header *types.Header, signer string) {
	// Ensure we have a persistent key for the signer
	if ap.accounts[signer] == nil {
		ap.accounts[signer], _ = crypto.GenerateKey()
	}
	// Sign the header and embed the signature in extra data
	sig, _ := crypto.Sign(sigHash(header).Bytes(), ap.accounts[signer])
	copy(header.Extra[len(header.Extra)-65:], sig)
}

func (ap *testerAccountPool) address(account string) common.Address {
	// Ensure we have a persistent key for the account
	if ap.accounts[account] == nil {
		ap.accounts[account], _ = crypto.GenerateKey()
	}
	// Resolve and return the Ethereum address
	return crypto.PubkeyToAddress(ap.accounts[account].PublicKey)
}

func (ap *testerAccountPool) name(address common.Address) string {
	for name, v := range ap.accounts {
		if crypto.PubkeyToAddress(v.PublicKey) == address {
			return name
		}
	}
	return ""
}

//accumulateRewards
func (ap *testerAccountPool)accumulateRewards(config *params.ChainConfig, header *types.Header, snap *Snapshot, state *state.StateDB) {

	// Calculate the block reword by year
	blockNumPerYear := secondsPerYear / config.Alien.Period
	yearCount := header.Number.Uint64() / blockNumPerYear
	blockReward := new(big.Int).Rsh(SignerBlockReward, uint(yearCount))

	minerReward := new(big.Int).Set(blockReward)
	minerReward.Mul(minerReward, big.NewInt(int64(minerRewardPerThousand)))
	minerReward.Div(minerReward, big.NewInt(1000))

	votersReward := blockReward.Sub(blockReward, minerReward)

	fmt.Printf("accumulateRewards for singer:%s---%d---%d---%d\n", header.Coinbase.Hex(), minerReward, votersReward, SignerBlockReward)

	state.AddBalance(header.Coinbase, minerReward)
	for voter, reward := range snap.calculateReward(header.Coinbase, votersReward) {
		state.AddBalance(voter, reward)
		fmt.Printf("calculateVoteReward for voter:%s-----%d\n", voter.Hex(), reward)
	}
}

func CalReward(m uint64, mCount uint64, v uint64, vCounts []uint64, bals []uint64, allStakes []uint64) *big.Int {

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


// testerChainReader implements consensus.ChainReader to access the genesis
// block. All other methods and requests will panic.
type testerChainReader struct {
	db ethdb.Database
}

func (r *testerChainReader) Config() *params.ChainConfig                 { return params.AllAlienProtocolChanges }
func (r *testerChainReader) CurrentHeader() *types.Header                { panic("not supported") }
//func (r *testerChainReader) GetHeader(common.Hash, uint64) *types.Header { panic("not supported") }
func (r *testerChainReader) GetBlock(common.Hash, uint64) *types.Block   { panic("not supported") }
func (r *testerChainReader) GetHeaderByHash(common.Hash) *types.Header   { panic("not supported") }
func (r *testerChainReader) GetHeaderByNumber(number uint64) *types.Header {
	if number == 0 {
		return rawdb.ReadHeader(r.db, rawdb.ReadCanonicalHash(r.db, 0), 0)
	}
	panic("not supported")
}

// Tests that voting is evaluated correctly for various simple and complex scenarios.
func TestVoting(t *testing.T) {
	//// Define the various voting scenarios to test
	//tests := []struct {
	//	addrNames        []string             // accounts used in this case
	//	candidateNeedPD  bool                 // candidate from POA
	//	period           uint64               // default 3
	//	epoch            uint64               // default 30000
	//	maxSignerCount   uint64               // default 5 for test
	//	minVoterBalance  int                  // default 50
	//	genesisTimestamp uint64               // default time.now() - period + 1
	//	lcrs             uint64               // loop count to recreate signers from top tally
	//	selfVoters       []testerSelfVoter    //
	//	txHeaders        []testerSingleHeader //
	//	result           testerSnapshot       // the result of current snapshot
	//	vlCnt            uint64
	//}{
	//	{
	//		/* 	Case 0:
	//		*	Just two self vote address A B in genesis
	//		*  	No votes or transactions through blocks
	//		 */
	//		addrNames:        []string{"A", "B"},
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//5
	//			{[]testerTransaction{}},
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B"},
	//			Tally:   map[string]int{"A": 100, "B": 200},
	//			Voters:  map[string]int{"A": 0, "B": 0},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//			},
	//		},
	//	},
	//	{
	//		/*	Case 1:
	//		*	Two self vote address A B in  genesis
	//		* 	C vote D to be signer in block 3
	//		* 	But current loop do not finish, so D is not signer,
	//		* 	the vote info already in Tally, Voters and Votes
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D"},
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(7),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 200, isVote: true}}},
	//			{[]testerTransaction{}},//4
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B"},
	//			Tally:   map[string]int{"A": 100, "B": 200, "D": 200},
	//			Voters:  map[string]int{"A": 0, "B": 0, "C": 3},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//				"C": {"C", "D", 200},
	//			},
	//		},
	//	},
	//	{
	//		/*	Case 2:
	//		*	Two self vote address in  genesis
	//		* 	C vote D to be signer in block 2
	//		* 	But balance of C is lower than minVoterBalance,
	//		*   so this vote not processed, D is not signer
	//		* 	the vote info is dropped .
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D"},
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 20, isVote: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//5
	//			{[]testerTransaction{}},
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B"},
	//			Tally:   map[string]int{"A": 100, "B": 200},
	//			Voters:  map[string]int{"A": 0, "B": 0},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//			},
	//		},
	//	},
	//	{
	//		/*	Case 3:
	//		*	Two self vote address A B in  genesis
	//		* 	C vote D to be signer in block 3
	//		* 	balance of C is higher than minVoterBalance
	//		* 	D is signer in next loop
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D"},
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 200, isVote: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//5 ???xxx 为什么到4那里更新signers列表而不是5
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B", "D"},
	//			Tally:   map[string]int{"A": 100, "B": 200, "D": 200},
	//			Voters:  map[string]int{"A": 0, "B": 0, "C": 3},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//				"C": {"C", "D", 200},
	//			},
	//		},
	//	},
	//
	//	{
	//		/*	Case 4:
	//		*	Two self vote address A B in  genesis
	//		* 	C vote D to be signer in block 2
	//		*  	C vote B to be signer in block 3
	//		* 	balance of C is higher minVoterBalance
	//		* 	the first vote from C is dropped
	//		* 	the signers are still A and B
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D"},
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 200, isVote: true}}},
	//			{[]testerTransaction{{from: "C", to: "B", balance: 180, isVote: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//5
	//			{[]testerTransaction{}},
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B"},
	//			Tally:   map[string]int{"A": 100, "B": 380},
	//			Voters:  map[string]int{"A": 0, "B": 0, "C": 3},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//				"C": {"C", "B", 180},
	//			},
	//		},
	//	},
	//	{
	//		/*	Case 5:
	//		*	Two self vote address A B in  genesis
	//		* 	C vote D to be signer in block 2
	//		*  	C transaction to E 20 in block 3
	//		*	In Voters, the vote block number of C is still 2, not 4
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D", "E"},
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 100, isVote: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "E", balance: 20, isVote: false}}}, // when C transaction to E, the balance of C is 20
	//			{[]testerTransaction{}},//5
	//			{[]testerTransaction{}},//投票限制和后来的计票是分离的，没有关系
	//			//????计票方式按投票时的余额算，而不是实时的余额  会有什么问题？？？
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B", "D"},
	//			Tally:   map[string]int{"A": 100, "B": 200, "D": 20},
	//			Voters:  map[string]int{"A": 0, "B": 0, "C": 2},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//				"C": {"C", "D", 20},
	//			},
	//		},
	//	},
	//	{
	//		/*	Case 6:
	//		*	Two self vote address A B in  genesis
	//		* 	C vote D , J vote K, H vote I  to be signer in block 2
	//		*   E vote F in block 3
	//		* 	The signers in the next loop is A,B,D,F,I but not K
	//		*	K is not top 5(maxsigercount) in Tally
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D", "E", "F", "H", "I", "J", "K"},
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 110, isVote: true}, {from: "J", to: "K", balance: 80, isVote: true}, {from: "H", to: "I", balance: 160, isVote: true}}},
	//			{[]testerTransaction{{from: "E", to: "F", balance: 130, isVote: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//5
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B", "D", "F", "I"},
	//			Tally:   map[string]int{"A": 100, "B": 200, "D": 110, "I": 160, "F": 130, "K": 80},//bifda
	//			Voters:  map[string]int{"A": 0, "B": 0, "C": 2, "H": 2, "J": 2, "E": 3},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//				"C": {"C", "D", 110},
	//				"J": {"J", "K", 80},
	//				"H": {"H", "I", 160},
	//				"E": {"E", "F", 130},
	//			},
	//		},
	//	},
	//	{
	//		//!!!!下次从这里看(周三早上看到case6)
	//		/*	Case 7:
	//		*	one self vote address A in  genesis
	//		* 	C vote D , J vote K, H vote I  to be signer in block 3
	//		*   E vote F in block 4
	//		* 	B vote B in block 5
	//		* 	The signers in the next loop is A, B, D,F,I,K
	//		*	current number - The block number of vote for A > epoch expired
	//		*
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D", "E", "F", "H", "I", "J", "K"},
	//		period:           uint64(3),
	//		epoch:            uint64(8),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//5
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//10
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 110, isVote: true}, {from: "J", to: "K", balance: 80, isVote: true}, {from: "H", to: "I", balance: 160, isVote: true}}},
	//			{[]testerTransaction{{from: "E", to: "F", balance: 130, isVote: true}}},
	//			{[]testerTransaction{{from: "B", to: "B", balance: 200, isVote: true}}},
	//			{[]testerTransaction{}},//15
	//			{[]testerTransaction{}},//16
	//		},//a 100  d 110(12) k 80(12) I 160(12)  F 130(13)  B 200(14)  bfdi a k
	//		result: testerSnapshot{
	//			Signers: []string{"B", "D", "F", "I", "K"},
	//			Tally:   map[string]int{"B": 200, "D": 110, "I": 160, "F": 130, "K": 80},
	//			Voters:  map[string]int{"B": 14, "C": 12, "H": 12, "J": 12, "E": 13},
	//			Votes: map[string]*testerVote{
	//				"B": {"B", "B", 200},
	//				"C": {"C", "D", 110},
	//				"J": {"J", "K", 80},
	//				"H": {"H", "I", 160},
	//				"E": {"E", "F", 130},
	//			},
	//		},
	//	},
	//	{
	//		/*	Case 8:
	//		*	Two self vote address A,B in  genesis
	//		* 	C vote D , D vote C to be signer in block 3
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D", "E"},
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 110, isVote: true}, {from: "D", to: "C", balance: 80, isVote: true}, {from: "C", to: "E", balance: 110, isVote: false}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//5
	//			{[]testerTransaction{}},
	//		},// a 100(1)  b 200(1) D  110(3)  C  80(3)  都是
	//		result: testerSnapshot{
	//			Signers: []string{"B", "A", "C", "D"},
	//			Tally:   map[string]int{"B": 200, "D": 110, "A": 100, "C": 80},
	//			Voters:  map[string]int{"B": 0, "C": 3, "D": 3, "A": 0},
	//			Votes: map[string]*testerVote{
	//				"B": {"B", "B", 200},
	//				"A": {"A", "A", 100},
	//				"C": {"C", "D", 110},
	//				"D": {"D", "C", 80},
	//			},
	//		},//t++多个账号给一个candidate投，内分新轮次是否到来
	//	},
	//	{
	//		/*	Case 9:
	//		*	Two self vote address A B in  genesis
	//		* 	C vote D to be signer in block 3
	//		* 	lcrs  is 2, so the signers will recalculate after 5 *2 block
	//		* 	D is still not signer
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D"},
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             2,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 200, isVote: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//5
	//			{[]testerTransaction{}},// 720083
	//			{[]testerTransaction{}},
	//		},// a 100(1) b 200(1)  D 200(3)  > 2*5 -- > 11那重算队列  ab
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B"},
	//			Tally:   map[string]int{"A": 100, "B": 200, "D": 200},
	//			Voters:  map[string]int{"A": 0, "B": 0, "C": 3},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//				"C": {"C", "D", 200},
	//			},
	//		},
	//	},
	//	{
	//		/*	Case 10:
	//		*	Two self vote address A B in  genesis
	//		* 	C vote D to be signer in block 3
	//		* 	lcrs  is 2, so the signers will recalculate after 5 *2 block
	//		* 	D is signer
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D"},
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             2,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 200, isVote: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//5
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//10
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B", "D"},
	//			Tally:   map[string]int{"A": 100, "B": 200, "D": 200},
	//			Voters:  map[string]int{"A": 0, "B": 0, "C": 3},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//				"C": {"C", "D", 200},
	//			},
	//		},
	//	},
	//	{
	//
	//		/*	Case 11:
	//		*	All self vote in  genesis
	//		* 	lcrs  is 1, so the signers will recalculate after 5 block
	//		*   official 21 node test case
	//		 */
	//		addrNames: []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10",
	//			"A11", "A12", "A13", "A14", "A15", "A16", "A17", "A18", "A19", "A20",
	//			"A21", "A22", "A23", "A24", "A25", "A26", "A27", "A28", "A29", "A30",
	//			"A31", "A32", "A33", "A34", "A35", "A36", "A37", "A38", "A39", "A40"},
	//		period:           uint64(3),
	//		epoch:            uint64(300),
	//		maxSignerCount:   uint64(21),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters: []testerSelfVoter{{"A1", 5000}, {"A2", 5000}, {"A3", 5000}, {"A4", 5000}, {"A5", 5000},
	//			{"A6", 5000}, {"A7", 5000}, {"A8", 5000}, {"A9", 5000}, {"A10", 5000},
	//			{"A11", 4000}, {"A12", 4000}, {"A13", 4000}, {"A14", 4000}, {"A15", 4000},
	//			{"A16", 4000}, {"A17", 4000}, {"A18", 4000}, {"A19", 4000}, {"A20", 4000},
	//			{"A21", 3000}, {"A22", 3000}, {"A23", 3000}, {"A24", 3000}, {"A25", 3000},
	//			{"A26", 3000}, {"A27", 3000}, {"A28", 3000}, {"A29", 3000}, {"A30", 3000},
	//			{"A31", 2000}, {"A32", 2000}, {"A33", 2000}, {"A34", 2000}, {"A35", 2000},
	//			{"A36", 2000}, {"A37", 2000}, {"A38", 2000}, {"A39", 2000}, {"A40", 2000}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}},//21
	//			{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}}, //42
	//			{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
	//			{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
	//			//50
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{},
	//			Tally: map[string]int{"A1": 5000, "A2": 5000, "A3": 5000, "A4": 5000, "A5": 5000, "A6": 5000, "A7": 5000, "A8": 5000, "A9": 5000, "A10": 5000,
	//				"A11": 4000, "A12": 4000, "A13": 4000, "A14": 4000, "A15": 4000, "A16": 4000, "A17": 4000, "A18": 4000, "A19": 4000, "A20": 4000,
	//				"A21": 3000, "A22": 3000, "A23": 3000, "A24": 3000, "A25": 3000, "A26": 3000, "A27": 3000, "A28": 3000, "A29": 3000, "A30": 3000,
	//				"A31": 2000, "A32": 2000, "A33": 2000, "A34": 2000, "A35": 2000, "A36": 2000, "A37": 2000, "A38": 2000, "A39": 2000, "A40": 2000},
	//			Voters: map[string]int{"A1": 0, "A2": 0, "A3": 0, "A4": 0, "A5": 0, "A6": 0, "A7": 0, "A8": 0, "A9": 0, "A10": 0,
	//				"A11": 0, "A12": 0, "A13": 0, "A14": 0, "A15": 0, "A16": 0, "A17": 0, "A18": 0, "A19": 0, "A20": 0,
	//				"A21": 0, "A22": 0, "A23": 0, "A24": 0, "A25": 0, "A26": 0, "A27": 0, "A28": 0, "A29": 0, "A30": 0,
	//				"A31": 0, "A32": 0, "A33": 0, "A34": 0, "A35": 0, "A36": 0, "A37": 0, "A38": 0, "A39": 0, "A40": 0},
	//			Votes: map[string]*testerVote{
	//				"A1":  {"A1", "A1", 5000},
	//				"A2":  {"A2", "A2", 5000},
	//				"A3":  {"A3", "A3", 5000},
	//				"A4":  {"A4", "A4", 5000},
	//				"A5":  {"A5", "A5", 5000},
	//				"A6":  {"A6", "A6", 5000},
	//				"A7":  {"A7", "A7", 5000},
	//				"A8":  {"A8", "A8", 5000},
	//				"A9":  {"A9", "A9", 5000},
	//				"A10": {"A10", "A10", 5000},
	//				"A11": {"A11", "A11", 4000},
	//				"A12": {"A12", "A12", 4000},
	//				"A13": {"A13", "A13", 4000},
	//				"A14": {"A14", "A14", 4000},
	//				"A15": {"A15", "A15", 4000},
	//				"A16": {"A16", "A16", 4000},
	//				"A17": {"A17", "A17", 4000},
	//				"A18": {"A18", "A18", 4000},
	//				"A19": {"A19", "A19", 4000},
	//				"A20": {"A20", "A20", 4000},
	//				"A21": {"A21", "A21", 3000},
	//				"A22": {"A22", "A22", 3000},
	//				"A23": {"A23", "A23", 3000},
	//				"A24": {"A24", "A24", 3000},
	//				"A25": {"A25", "A25", 3000},
	//				"A26": {"A26", "A26", 3000},
	//				"A27": {"A27", "A27", 3000},
	//				"A28": {"A28", "A28", 3000},
	//				"A29": {"A29", "A29", 3000},
	//				"A30": {"A30", "A30", 3000},
	//				"A31": {"A31", "A31", 2000},
	//				"A32": {"A32", "A32", 2000},
	//				"A33": {"A33", "A33", 2000},
	//				"A34": {"A34", "A34", 2000},
	//				"A35": {"A35", "A35", 2000},
	//				"A36": {"A36", "A36", 2000},
	//				"A37": {"A37", "A37", 2000},
	//				"A38": {"A38", "A38", 2000},
	//				"A39": {"A39", "A39", 2000},
	//				"A40": {"A40", "A40", 2000},
	//			},
	//		},
	//	},
	//	{
	//		//??? vote选举不是可以直接选为candidate了吗？？？
	//		/*	Case 12:
	//		*   Candidate from Poa is enable
	//		*	Two self vote address A B in  genesis
	//		* 	C vote D to be signer in block 3, but D is not in candidates ,so this vote not valid
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D"},
	//		candidateNeedPD:  true,
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 200, isVote: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//5
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//		},// a 100(1) b 200(1) D 200(3)
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B"},
	//			Tally:   map[string]int{"A": 100, "B": 200},
	//			Voters:  map[string]int{"A": 0, "B": 0},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//				//"C": {"C", "D", 200},
	//			},
	//		},
	//	},//t+++ candidateNeedPD 开关测试vote
	//	{
	//		/*	Case 13:
	//		*   Candidate from Poa is enable
	//		*	Two self vote address A B in  genesis
	//		*   A proposal D to candidates, B declare agree to this proposal ,but not pass 2/3 * all stake, so fail
	//		* 	C vote D to be signer in block 3, but D is not in candidates ,so this vote not valid
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D"},
	//		candidateNeedPD:  true,
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "A", to: "A", isProposal: true, candidate: "D", txHash: "a", proposalType: proposalTypeCandidateAdd}}},
	//			{[]testerTransaction{{from: "B", to: "B", isDeclare: true, txHash: "a", decision: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 250, isVote: true}}},
	//			{[]testerTransaction{}},//6
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//10
	//			{[]testerTransaction{}},
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B"},
	//			Tally:   map[string]int{"A": 100, "B": 200},
	//			Voters:  map[string]int{"A": 0, "B": 0},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//			},
	//		},
	//	},
	//	{
	//		/*	Case 14:
	//		*   Candidate from Poa is enable
	//		*	Two self vote address A B in  genesis
	//		*   A proposal D to candidates, and A,B declare agree to this proposal, so D is in candidates
	//		* 	C vote D to be signer in block 5
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D"},
	//		candidateNeedPD:  true,
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "A", to: "A", isProposal: true, candidate: "D", txHash: "a", proposalType: proposalTypeCandidateAdd}}},
	//			{[]testerTransaction{{from: "A", to: "A", isDeclare: true, txHash: "a", decision: true}, {from: "B", to: "B", isDeclare: true, txHash: "a", decision: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//5
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//10
	//			{[]testerTransaction{{from: "C", to: "D", balance: 250, isVote: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//15
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B", "D"},
	//			Tally:   map[string]int{"A": 100, "B": 200, "D": 250},
	//			Voters:  map[string]int{"A": 0, "B": 0, "C": 11},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//				"C": {"C", "D", 250},
	//			},
	//		},
	//	},
	//	{
	//		/*	Case 15:
	//		*   Candidate from Poa is enable
	//		*	Two self vote address A B E F in  genesis
	//		*   A proposal D to candidates, and A,B,F declare agree to this proposal,
	//		*   but the sum stake of A B F is less than 2/3 of all stake, so D is not in candidates
	//		* 	C vote D to be signer in block 5
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D", "E", "F"},
	//		candidateNeedPD:  true,
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}, {"E", 2000}, {"F", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "A", to: "A", isProposal: true, candidate: "D", txHash: "a", proposalType: proposalTypeCandidateAdd}}},
	//			{[]testerTransaction{{from: "A", to: "A", isDeclare: true, txHash: "a", decision: true}, {from: "B", to: "B", isDeclare: true, txHash: "a", decision: true}, {from: "F", to: "F", isDeclare: true, txHash: "a", decision: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "C", to: "D", balance: 250, isVote: true}}},
	//			{[]testerTransaction{}},//6
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//10
	//			{[]testerTransaction{}},
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{"A", "B", "E", "F"},
	//			Tally:   map[string]int{"A": 100, "B": 200, "E": 2000, "F": 200},
	//			Voters:  map[string]int{"A": 0, "B": 0, "E": 0, "F": 0},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//				"E": {"E", "E", 2000},
	//				"F": {"F", "F", 200},
	//			},
	//		},
	//	},
	//	{
	//		/*	Case 16:
	//		*   Candidate from Poa is enable
	//		*	Two self vote address A B E F in  genesis
	//		*   A proposal B remove from candidates, and A, E ,F declare agree to this proposal,
	//		*   the sum stake of A E F is more than 2/3 of all stake, so B is not in candidates
	//		*   Now do not change the vote automatically,
	//		 */
	//		addrNames:        []string{"A", "B", "C", "D", "E", "F"},
	//		candidateNeedPD:  true,
	//		period:           uint64(3),
	//		epoch:            uint64(31),
	//		maxSignerCount:   uint64(5),
	//		minVoterBalance:  50,
	//		lcrs:             1,
	//		genesisTimestamp: uint64(0),
	//		selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}, {"E", 2000}, {"F", 200}},
	//		txHeaders: []testerSingleHeader{
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{{from: "A", to: "A", isProposal: true, candidate: "B", txHash: "a", proposalType: proposalTypeCandidateRemove}}},
	//			{[]testerTransaction{{from: "A", to: "A", isDeclare: true, txHash: "a", decision: true}, {from: "E", to: "E", isDeclare: true, txHash: "a", decision: true}, {from: "F", to: "F", isDeclare: true, txHash: "a", decision: true}}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//5
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},
	//			{[]testerTransaction{}},//10
	//		},
	//		result: testerSnapshot{
	//			Signers: []string{"A", "E", "F"},
	//			Tally:   map[string]int{"A": 100, "B": 200, "E": 2000, "F": 200},
	//			Voters:  map[string]int{"A": 0, "B": 0, "E": 0, "F": 0},
	//			Votes: map[string]*testerVote{
	//				"A": {"A", "A", 100},
	//				"B": {"B", "B", 200},
	//				"E": {"E", "E", 2000},
	//				"F": {"F", "F", 200},
	//			},
	//		},
	//	},
	//}

	//*chaorstest
	tests := []struct {
		addrNames        []string             // accounts used in this case
		candidateNeedPD  bool                 // candidate from POA
		period           uint64               // default 3
		epoch            uint64               // default 30000
		maxSignerCount   uint64               // default 5 for test
		minVoterBalance  int                  // default 50
		genesisTimestamp uint64               // default time.now() - period + 1
		lcrs             uint64               // loop count to recreate signers from top tally
		selfVoters       []testerSelfVoter    //
		txHeaders        []testerSingleHeader //
		result           testerSnapshot       // the result of current snapshot
		vlCnt            uint64
	}{

		//+++10 A在区块2投票给B,B在区块4投票 区块5开始重计签名者  但是这里计票使用的是区块4的快照 b-->a的投票并未计入签名者
		{
			addrNames:        []string{"A", "B", "C"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(5),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//a
				{[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
				{[]testerTransaction{}},//a 3
				{[]testerTransaction{}},//b
				{[]testerTransaction{{from: "B", to: "A", balance: 200, isVote: true}}},//a 5
				{[]testerTransaction{}},//b
				{[]testerTransaction{}},//b
				{[]testerTransaction{}},//b
				{[]testerTransaction{}},//b
				{[]testerTransaction{}},//b  //10
			},
			result: testerSnapshot{
				Signers: []string{"B", "A"},
				Tally:   map[string]int{"B": 100, "A":200},
				Voters:  map[string]int{"A": 2, "B": 5},
				Votes: map[string]*testerVote{
					"A": {"A", "B", 100},
					"B": {"B", "A", 200},
				},
			},
		},

		////+++10 A在区块2投票给B,B在区块4投票 区块5开始重计签名者  但是这里计票使用的是区块4的快照 b-->a的投票并未计入签名者
		//{
		//	addrNames:        []string{"A", "B", "C"},
		//	period:           uint64(3),
		//	epoch:            uint64(31),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}},//a
		//		{[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
		//		{[]testerTransaction{}},//a 3
		//		{[]testerTransaction{{from: "B", to: "A", balance: 200, isVote: true}}},//a 5
		//		{[]testerTransaction{}},//b
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"B", "A"},
		//		Tally:   map[string]int{"B": 100, "A":200},
		//		Voters:  map[string]int{"A": 2, "B": 4},
		//		Votes: map[string]*testerVote{
		//			"A": {"A", "B", 100},
		//			"B": {"B", "A", 200},
		//		},
		//	},
		//},

		////+++9 A在区块2投票给B,B在区块5投票 区块5开始重计签名者  但是这里计票使用的是区块4的快照 b-->a的投票并未计入签名者
		//{
		//	addrNames:        []string{"A", "B", "C"},
		//	period:           uint64(3),
		//	epoch:            uint64(31),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}},//a
		//		{[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
		//		{[]testerTransaction{}},//a 3
		//		{[]testerTransaction{}},//b
		//		{[]testerTransaction{{from: "B", to: "A", balance: 200, isVote: true}}},//a 5
		//		{[]testerTransaction{}},//b
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"B"},
		//		Tally:   map[string]int{"B": 100, "A":200},
		//		Voters:  map[string]int{"A": 2, "B": 5},
		//		Votes: map[string]*testerVote{
		//			"A": {"A", "B", 100},
		//			"B": {"B", "A", 200},
		//		},
		//	},
		//},

		//+++8 A Vote到期但是还没有加入signers
		{
			//addrNames:        []string{"A", "B", "C"},
			addrNames:        []string{"A", "B", "C", "D", "E", "F", "H", "I", "J"},
			period:           uint64(3),
			epoch:            uint64(8),
			maxSignerCount:   uint64(5),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 220}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//{from: "B", to: "B", balance: 205, isVote: true}
				{[]testerTransaction{}},
				{[]testerTransaction{}},
				{[]testerTransaction{{from: "C", to: "D", balance: 200, isVote: true}, {from: "D", to: "C", balance: 260, isVote: true}, {from: "E", to: "E", balance: 280, isVote: true}, {from: "F", to: "H", balance: 320, isVote: true}, {from: "H", to: "I", balance: 210, isVote: true}}},
				{[]testerTransaction{}},//5
				{[]testerTransaction{}}, //
				{[]testerTransaction{}},
				{[]testerTransaction{}},
				{[]testerTransaction{}},//9
			},
			result: testerSnapshot{
				Signers: []string{"A", "C", "E", "H", "I"},
				Tally:   map[string]int{"D": 200, "C":260, "E":280, "H":320, "I":210},
				Voters:  map[string]int{"C": 4, "D":4, "E":4, "F":4, "H":4},
				Votes: map[string]*testerVote{
					"C": {"C", "D", 200},
					"D": {"D", "C", 260},
					"E": {"E", "E", 280},
					//"B": {"B", "B", 200},
					"F": {"F", "H", 320},
					"H": {"H", "I", 210},
				},
			},

		// +++7A到期同时已经加入signers
		//{
		//	//addrNames:        []string{"A", "B", "C"},
		//	addrNames:        []string{"A", "B", "C", "D", "E", "F", "H", "I", "J"},
		//	period:           uint64(3),
		//	epoch:            uint64(8),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 220}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}},//{from: "B", to: "B", balance: 205, isVote: true}
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{{from: "C", to: "D", balance: 200, isVote: true}, {from: "D", to: "C", balance: 260, isVote: true}, {from: "E", to: "E", balance: 280, isVote: true}, {from: "F", to: "H", balance: 320, isVote: true}, {from: "H", to: "I", balance: 210, isVote: true}}},
		//		{[]testerTransaction{}},//5
		//		{[]testerTransaction{}}, //
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},//9
		//		{[]testerTransaction{}},
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"D", "C", "E", "H", "I"},
		//		Tally:   map[string]int{"D": 200, "C":260, "E":280, "H":320, "I":210},
		//		Voters:  map[string]int{"C": 4, "D":4, "E":4, "F":4, "H":4},
		//		Votes: map[string]*testerVote{
		//			"C": {"C", "D", 200},
		//			"D": {"D", "C", 260},
		//			"E": {"E", "E", 280},
		//			//"B": {"B", "B", 200},
		//			"F": {"F", "H", 320},
		//			"H": {"H", "I", 210},
		//		},
		//	},

		// +++6A到期但不是因为到期而更新出signers(不在top5)
		//{
		//	//addrNames:        []string{"A", "B", "C"},
		//	addrNames:        []string{"A", "B", "C", "D", "E", "F", "H", "I", "J"},
		//	period:           uint64(3),
		//	epoch:            uint64(8),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 180}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}},//{from: "B", to: "B", balance: 205, isVote: true}
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{{from: "C", to: "D", balance: 200, isVote: true}, {from: "D", to: "C", balance: 260, isVote: true}, {from: "E", to: "E", balance: 280, isVote: true}, {from: "F", to: "H", balance: 320, isVote: true}, {from: "H", to: "I", balance: 210, isVote: true}}},
		//		{[]testerTransaction{}},//5
		//		{[]testerTransaction{}}, //
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},//9
		//		{[]testerTransaction{}},  //有没有这个结果都一样
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"D", "C", "E", "H", "I"},
		//		Tally:   map[string]int{"D": 200, "C":260, "E":280, "H":320, "I":210},
		//		Voters:  map[string]int{"C": 4, "D":4, "E":4, "F":4, "H":4},
		//		Votes: map[string]*testerVote{
		//			"C": {"C", "D", 200},
		//			"D": {"D", "C", 260},
		//			"E": {"E", "E", 280},
		//			//"B": {"B", "B", 200},
		//			"F": {"F", "H", 320},
		//			"H": {"H", "I", 210},
		//		},
		//	},

		// +++5有投票过期但是有效投票数未达到maxSignerCount
		//{
		//	//addrNames:        []string{"A", "B", "C"},
		//	addrNames:        []string{"A", "B", "C", "D", "E"},
		//	period:           uint64(3),
		//	epoch:            uint64(8),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 200}, {"B", 100}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}}, //{from: "B", to: "B", balance: 205, isVote: true}
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{{from: "C", to: "D", balance: 210, isVote: true}, {from: "D", to: "C", balance: 260, isVote: true}, {from: "E", to: "E", balance: 280, isVote: true}}},
		//		{[]testerTransaction{}}, //5
		//		{[]testerTransaction{}}, //
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}}, //9
		//		{[]testerTransaction{}}, //有没有这个结果都一样
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"D", "C", "E", "A", "B"},
		//		Tally:   map[string]int{"D": 210, "C": 260, "E": 280, "A": 200, "B": 100},
		//		Voters:  map[string]int{"C": 4, "D": 4, "E": 4, "A": 0, "B": 0},
		//		Votes: map[string]*testerVote{
		//			"C": {"C", "D", 210},
		//			"D": {"D", "C", 260},
		//			"E": {"E", "E", 280},
		//			"B": {"B", "B", 100},
		//			"A": {"A", "A", 200},
		//		},
		//	},
		//},

		// +++4有投票过期但是总候选人数未达到maxSignerCount
		//{
		//	//addrNames:        []string{"A", "B", "C"},
		//	addrNames:        []string{"A", "B", "C"},
		//	period:           uint64(3),
		//	epoch:            uint64(7),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 200}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{{from: "B", to: "C", balance: 220, isVote: true}, {from: "C", to: "B", balance: 160, isVote: true}}},
		//		{[]testerTransaction{}},//5
		//		{[]testerTransaction{}}, //
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},//8
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"A", "B", "C"},
		//		Tally:   map[string]int{"A":200, "B": 160, "C":220},
		//		Voters:  map[string]int{"A":0, "B": 4, "C":4},
		//		Votes: map[string]*testerVote{
		//			"B": {"B", "C", 220},
		//			"C": {"C", "B", 160},
		//			"A": {"A", "A", 200},
		//		},
		//	},
		//},

		// +++3初始投票后  中途转账，再改投别人
		//{
		//	addrNames:        []string{"A", "B", "C", "D", "E"},
		//	period:           uint64(3),
		//	epoch:            uint64(31),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{{from: "C", to: "D", balance: 100, isVote: true}}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{{from: "C", to: "E", balance: 20, isVote: false}}}, // when C transaction to E, the balance of C is 20
		//		{[]testerTransaction{{from: "C", to: "E", balance: 20, isVote: true}}}, //5
		//		{[]testerTransaction{}},//6
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"A", "B", "D"},
		//		Tally:   map[string]int{"A": 100, "B": 200, "D": 20},
		//		Voters:  map[string]int{"A": 0, "B": 0, "C": 2},
		//		Votes: map[string]*testerVote{
		//			"A": {"A", "A", 100},
		//			"B": {"B", "B", 200},
		//			"C": {"C", "D", 20},
		//		},
		//	},
		//},

		//+++1创世区块设定的自投票的A中途转投了别人(当前周期是否结束)
		//{
		//	addrNames:        []string{"A", "B", "C", "D", "E"},
		//	period:           uint64(3),
		//	epoch:            uint64(31),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{{from: "C", to: "D", balance: 300, isVote: true}}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}}, //5
		//		{[]testerTransaction{{from: "A", to: "C", balance: 100, isVote: true}}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},//10
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"C", "B", "D"},
		//		Tally:   map[string]int{"C": 100, "B": 200, "D": 300},
		//		Voters:  map[string]int{"A": 6, "B": 0, "C": 2},
		//		Votes: map[string]*testerVote{
		//			"A": {"A", "C", 100},
		//			"B": {"B", "B", 200},
		//			"C": {"C", "D", 300},
		//		},
		//	},
		//},
		//{
		//	addrNames:        []string{"A", "B", "C", "D", "E"},
		//	period:           uint64(3),
		//	epoch:            uint64(31),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{{from: "C", to: "D", balance: 300, isVote: true}}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}}, //5
		//		{[]testerTransaction{{from: "A", to: "C", balance: 100, isVote: true}}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"A", "B", "D"},
		//		Tally:   map[string]int{"C": 100, "B": 200, "D": 300},
		//		Voters:  map[string]int{"A": 6, "B": 0, "C": 2},
		//		Votes: map[string]*testerVote{
		//			"A": {"A", "C", 100},
		//			"B": {"B", "B", 200},
		//			"C": {"C", "D", 300},
		//		},
		//	},
		//},

		//+++0 一个区块内，A连着给两个不同的人投票
		//{
		//	addrNames:        []string{"A", "B", "C", "D", "E", "F"},
		//	period:           uint64(3),
		//	epoch:            uint64(31),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{{from: "C", to: "D", balance: 300, isVote: true}, {from: "C", to: "E", balance: 300, isVote: true}, {from: "D", to: "F", balance: 150, isVote: true}}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}}, //5
		//		{[]testerTransaction{}},
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"A", "B", "E", "F"},
		//		Tally:   map[string]int{"A": 100, "B": 200, "E": 300, "F":150},
		//		Voters:  map[string]int{"A": 0, "B": 0, "C": 2, "D":2},
		//		Votes: map[string]*testerVote{
		//			"A": {"A", "A", 100},
		//			"B": {"B", "B", 200},
		//			"C": {"C", "E", 300},
		//			"D": {"D", "F", 150},
		//		},
		//	},
		//},

		// base21 case
//		{
//			addrNames: []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10",
//					"A11", "A12", "A13", "A14", "A15", "A16", "A17", "A18", "A19", "A20",
//					"A21", "A22", "A23", "A24", "A25", "A26", "A27", "A28", "A29", "A30",
//					"A31", "A32", "A33", "A34", "A35", "A36", "A37", "A38", "A39", "A40"},
//			period:           uint64(3),
//			epoch:            uint64(300),
//			maxSignerCount:   uint64(21),
//			minVoterBalance:  50,
//			lcrs:             1,
//			genesisTimestamp: uint64(0),
//			selfVoters: []testerSelfVoter{{"A1", 5000}, {"A2", 5000}, {"A3", 5000}, {"A4", 5000}, {"A5", 5000},
//									{"A6", 5000}, {"A7", 5000}, {"A8", 5000}, {"A9", 5000}, {"A10", 5000},
//									{"A11", 4000}, {"A12", 4000}, {"A13", 4000}, {"A14", 4000}, {"A15", 4000},
//									{"A16", 4000}, {"A17", 4000}, {"A18", 4000}, {"A19", 4000}, {"A20", 4000},
//									{"A21", 3000}, {"A22", 3000}, {"A23", 3000}, {"A24", 3000}, {"A25", 3000},
//									{"A26", 3000}, {"A27", 3000}, {"A28", 3000}, {"A29", 3000}, {"A30", 3000},
//									{"A31", 2000}, {"A32", 2000}, {"A33", 2000}, {"A34", 2000}, {"A35", 2000},
//									{"A36", 2000}, {"A37", 2000}, {"A38", 2000}, {"A39", 2000}, {"A40", 2000}},
//			txHeaders: []testerSingleHeader{
//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}},//21
//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}}, //42
//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
//					//50
//		},
//		result: testerSnapshot{
//		Signers: []string{},
//		Tally: map[string]int{"A1": 5000, "A2": 5000, "A3": 5000, "A4": 5000, "A5": 5000, "A6": 5000, "A7": 5000, "A8": 5000, "A9": 5000, "A10": 5000,
//			"A11": 4000, "A12": 4000, "A13": 4000, "A14": 4000, "A15": 4000, "A16": 4000, "A17": 4000, "A18": 4000, "A19": 4000, "A20": 4000,
//			"A21": 3000, "A22": 3000, "A23": 3000, "A24": 3000, "A25": 3000, "A26": 3000, "A27": 3000, "A28": 3000, "A29": 3000, "A30": 3000,
//			"A31": 2000, "A32": 2000, "A33": 2000, "A34": 2000, "A35": 2000, "A36": 2000, "A37": 2000, "A38": 2000, "A39": 2000, "A40": 2000},
//		Voters: map[string]int{"A1": 0, "A2": 0, "A3": 0, "A4": 0, "A5": 0, "A6": 0, "A7": 0, "A8": 0, "A9": 0, "A10": 0,
//			"A11": 0, "A12": 0, "A13": 0, "A14": 0, "A15": 0, "A16": 0, "A17": 0, "A18": 0, "A19": 0, "A20": 0,
//			"A21": 0, "A22": 0, "A23": 0, "A24": 0, "A25": 0, "A26": 0, "A27": 0, "A28": 0, "A29": 0, "A30": 0,
//			"A31": 0, "A32": 0, "A33": 0, "A34": 0, "A35": 0, "A36": 0, "A37": 0, "A38": 0, "A39": 0, "A40": 0},
//		Votes: map[string]*testerVote{
//			"A1":  {"A1", "A1", 5000},
//			"A2":  {"A2", "A2", 5000},
//			"A3":  {"A3", "A3", 5000},
//			"A4":  {"A4", "A4", 5000},
//			"A5":  {"A5", "A5", 5000},
//			"A6":  {"A6", "A6", 5000},
//			"A7":  {"A7", "A7", 5000},
//			"A8":  {"A8", "A8", 5000},
//			"A9":  {"A9", "A9", 5000},
//			"A10": {"A10", "A10", 5000},
//			"A11": {"A11", "A11", 4000},
//			"A12": {"A12", "A12", 4000},
//			"A13": {"A13", "A13", 4000},
//			"A14": {"A14", "A14", 4000},
//			"A15": {"A15", "A15", 4000},
//			"A16": {"A16", "A16", 4000},
//			"A17": {"A17", "A17", 4000},
//			"A18": {"A18", "A18", 4000},
//			"A19": {"A19", "A19", 4000},
//			"A20": {"A20", "A20", 4000},
//			"A21": {"A21", "A21", 3000},
//			"A22": {"A22", "A22", 3000},
//			"A23": {"A23", "A23", 3000},
//			"A24": {"A24", "A24", 3000},
//			"A25": {"A25", "A25", 3000},
//			"A26": {"A26", "A26", 3000},
//			"A27": {"A27", "A27", 3000},
//			"A28": {"A28", "A28", 3000},
//			"A29": {"A29", "A29", 3000},
//			"A30": {"A30", "A30", 3000},
//			"A31": {"A31", "A31", 2000},
//			"A32": {"A32", "A32", 2000},
//			"A33": {"A33", "A33", 2000},
//			"A34": {"A34", "A34", 2000},
//			"A35": {"A35", "A35", 2000},
//			"A36": {"A36", "A36", 2000},
//			"A37": {"A37", "A37", 2000},
//			"A38": {"A38", "A38", 2000},
//			"A39": {"A39", "A39", 2000},
//			"A40": {"A40", "A40", 2000},
//			},
//		},
//		},

		// 21++1未开始新的一轮签名队列更新
		//{
		//	addrNames: []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10",
		//		"A11", "A12", "A13", "A14", "A15", "A16", "A17", "A18", "A19", "A20",
		//		"A21", "A22", "A23", "A24", "A25", "A26", "A27", "A28", "A29", "A30",
		//		"A31", "A32", "A33", "A34", "A35", "A36", "A37", "A38", "A39", "A40"},
		//	period:           uint64(3),
		//	epoch:            uint64(300),
		//	maxSignerCount:   uint64(21),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters: []testerSelfVoter{{"A1", 5000}, {"A2", 5000}, {"A3", 5000}, {"A4", 5000}, {"A5", 5000},
		//		{"A6", 5000}, {"A7", 5000}, {"A8", 5000}, {"A9", 5000}, {"A10", 5000},
		//		{"A11", 4000}, {"A12", 4000}, {"A13", 4000}, {"A14", 4000}, {"A15", 4000},
		//		{"A16", 4000}, {"A17", 4000}, {"A18", 4000}, {"A19", 4000}, {"A20", 4000},
		//		{"A21", 3000}, {"A22", 3000}, {"A23", 3000}, {"A24", 3000}, {"A25", 3000},
		//		{"A26", 3000}, {"A27", 3000}, {"A28", 3000}, {"A29", 3000}, {"A30", 3000},
		//		{"A31", 2000}, {"A32", 2000}, {"A33", 2000}, {"A34", 2000}, {"A35", 2000},
		//		{"A36", 2000}, {"A37", 2000}, {"A38", 2000}, {"A39", 2000}, {"A40", 2000}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}},//20
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10",
		//			"A11", "A12", "A13", "A14", "A15", "A16", "A17", "A18", "A19", "A20",
		//			"A21"},
		//		Tally: map[string]int{"A1": 5000, "A2": 5000, "A3": 5000, "A4": 5000, "A5": 5000, "A6": 5000, "A7": 5000, "A8": 5000, "A9": 5000, "A10": 5000,
		//			"A11": 4000, "A12": 4000, "A13": 4000, "A14": 4000, "A15": 4000, "A16": 4000, "A17": 4000, "A18": 4000, "A19": 4000, "A20": 4000,
		//			"A21": 3000, "A22": 3000, "A23": 3000, "A24": 3000, "A25": 3000, "A26": 3000, "A27": 3000, "A28": 3000, "A29": 3000, "A30": 3000,
		//			"A31": 2000, "A32": 2000, "A33": 2000, "A34": 2000, "A35": 2000, "A36": 2000, "A37": 2000, "A38": 2000, "A39": 2000, "A40": 2000},
		//		Voters: map[string]int{"A1": 0, "A2": 0, "A3": 0, "A4": 0, "A5": 0, "A6": 0, "A7": 0, "A8": 0, "A9": 0, "A10": 0,
		//			"A11": 0, "A12": 0, "A13": 0, "A14": 0, "A15": 0, "A16": 0, "A17": 0, "A18": 0, "A19": 0, "A20": 0,
		//			"A21": 0, "A22": 0, "A23": 0, "A24": 0, "A25": 0, "A26": 0, "A27": 0, "A28": 0, "A29": 0, "A30": 0,
		//			"A31": 0, "A32": 0, "A33": 0, "A34": 0, "A35": 0, "A36": 0, "A37": 0, "A38": 0, "A39": 0, "A40": 0},
		//		Votes: map[string]*testerVote{
		//			"A1":  {"A1", "A1", 5000},
		//			"A2":  {"A2", "A2", 5000},
		//			"A3":  {"A3", "A3", 5000},
		//			"A4":  {"A4", "A4", 5000},
		//			"A5":  {"A5", "A5", 5000},
		//			"A6":  {"A6", "A6", 5000},
		//			"A7":  {"A7", "A7", 5000},
		//			"A8":  {"A8", "A8", 5000},
		//			"A9":  {"A9", "A9", 5000},
		//			"A10": {"A10", "A10", 5000},
		//			"A11": {"A11", "A11", 4000},
		//			"A12": {"A12", "A12", 4000},
		//			"A13": {"A13", "A13", 4000},
		//			"A14": {"A14", "A14", 4000},
		//			"A15": {"A15", "A15", 4000},
		//			"A16": {"A16", "A16", 4000},
		//			"A17": {"A17", "A17", 4000},
		//			"A18": {"A18", "A18", 4000},
		//			"A19": {"A19", "A19", 4000},
		//			"A20": {"A20", "A20", 4000},
		//			"A21": {"A21", "A21", 3000},
		//			"A22": {"A22", "A22", 3000},
		//			"A23": {"A23", "A23", 3000},
		//			"A24": {"A24", "A24", 3000},
		//			"A25": {"A25", "A25", 3000},
		//			"A26": {"A26", "A26", 3000},
		//			"A27": {"A27", "A27", 3000},
		//			"A28": {"A28", "A28", 3000},
		//			"A29": {"A29", "A29", 3000},
		//			"A30": {"A30", "A30", 3000},
		//			"A31": {"A31", "A31", 2000},
		//			"A32": {"A32", "A32", 2000},
		//			"A33": {"A33", "A33", 2000},
		//			"A34": {"A34", "A34", 2000},
		//			"A35": {"A35", "A35", 2000},
		//			"A36": {"A36", "A36", 2000},
		//			"A37": {"A37", "A37", 2000},
		//			"A38": {"A38", "A38", 2000},
		//			"A39": {"A39", "A39", 2000},
		//			"A40": {"A40", "A40", 2000},
		//		},
		//	},
		//},

		//21++2同上
		//		{
		//			addrNames: []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10",
		//					"A11", "A12", "A13", "A14", "A15", "A16", "A17", "A18", "A19", "A20",
		//					"A21", "A22", "A23", "A24", "A25", "A26", "A27", "A28", "A29", "A30",
		//					"A31", "A32", "A33", "A34", "A35", "A36", "A37", "A38", "A39", "A40"},
		//			period:           uint64(3),
		//			epoch:            uint64(300),
		//			maxSignerCount:   uint64(21),
		//			minVoterBalance:  50,
		//			lcrs:             2,
		//			genesisTimestamp: uint64(0),
		//			selfVoters: []testerSelfVoter{{"A1", 5000}, {"A2", 5000}, {"A3", 5000}, {"A4", 5000}, {"A5", 5000},
		//									{"A6", 5000}, {"A7", 5000}, {"A8", 5000}, {"A9", 5000}, {"A10", 5000},
		//									{"A11", 4000}, {"A12", 4000}, {"A13", 4000}, {"A14", 4000}, {"A15", 4000},
		//									{"A16", 4000}, {"A17", 4000}, {"A18", 4000}, {"A19", 4000}, {"A20", 4000},
		//									{"A21", 3000}, {"A22", 3000}, {"A23", 3000}, {"A24", 3000}, {"A25", 3000},
		//									{"A26", 3000}, {"A27", 3000}, {"A28", 3000}, {"A29", 3000}, {"A30", 3000},
		//									{"A31", 2000}, {"A32", 2000}, {"A33", 2000}, {"A34", 2000}, {"A35", 2000},
		//									{"A36", 2000}, {"A37", 2000}, {"A38", 2000}, {"A39", 2000}, {"A40", 2000}},
		//			txHeaders: []testerSingleHeader{
		//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//					{[]testerTransaction{}}, {[]testerTransaction{}},
		//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//					{[]testerTransaction{}}, {[]testerTransaction{}},
		//					{[]testerTransaction{}},//21
		//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//					{[]testerTransaction{}}, {[]testerTransaction{}},
		//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//					{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//					{[]testerTransaction{}}, {[]testerTransaction{}},//41
		//				//{[]testerTransaction{}},
		//		},
		//			result: testerSnapshot{
		//		Signers: []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10",
		//			"A11", "A12", "A13", "A14", "A15", "A16", "A17", "A18", "A19", "A20",
		//			"A21"},
		//		Tally: map[string]int{"A1": 5000, "A2": 5000, "A3": 5000, "A4": 5000, "A5": 5000, "A6": 5000, "A7": 5000, "A8": 5000, "A9": 5000, "A10": 5000,
		//			"A11": 4000, "A12": 4000, "A13": 4000, "A14": 4000, "A15": 4000, "A16": 4000, "A17": 4000, "A18": 4000, "A19": 4000, "A20": 4000,
		//			"A21": 3000, "A22": 3000, "A23": 3000, "A24": 3000, "A25": 3000, "A26": 3000, "A27": 3000, "A28": 3000, "A29": 3000, "A30": 3000,
		//			"A31": 2000, "A32": 2000, "A33": 2000, "A34": 2000, "A35": 2000, "A36": 2000, "A37": 2000, "A38": 2000, "A39": 2000, "A40": 2000},
		//		Voters: map[string]int{"A1": 0, "A2": 0, "A3": 0, "A4": 0, "A5": 0, "A6": 0, "A7": 0, "A8": 0, "A9": 0, "A10": 0,
		//			"A11": 0, "A12": 0, "A13": 0, "A14": 0, "A15": 0, "A16": 0, "A17": 0, "A18": 0, "A19": 0, "A20": 0,
		//			"A21": 0, "A22": 0, "A23": 0, "A24": 0, "A25": 0, "A26": 0, "A27": 0, "A28": 0, "A29": 0, "A30": 0,
		//			"A31": 0, "A32": 0, "A33": 0, "A34": 0, "A35": 0, "A36": 0, "A37": 0, "A38": 0, "A39": 0, "A40": 0},
		//		Votes: map[string]*testerVote{
		//			"A1":  {"A1", "A1", 5000},
		//			"A2":  {"A2", "A2", 5000},
		//			"A3":  {"A3", "A3", 5000},
		//			"A4":  {"A4", "A4", 5000},
		//			"A5":  {"A5", "A5", 5000},
		//			"A6":  {"A6", "A6", 5000},
		//			"A7":  {"A7", "A7", 5000},
		//			"A8":  {"A8", "A8", 5000},
		//			"A9":  {"A9", "A9", 5000},
		//			"A10": {"A10", "A10", 5000},
		//			"A11": {"A11", "A11", 4000},
		//			"A12": {"A12", "A12", 4000},
		//			"A13": {"A13", "A13", 4000},
		//			"A14": {"A14", "A14", 4000},
		//			"A15": {"A15", "A15", 4000},
		//			"A16": {"A16", "A16", 4000},
		//			"A17": {"A17", "A17", 4000},
		//			"A18": {"A18", "A18", 4000},
		//			"A19": {"A19", "A19", 4000},
		//			"A20": {"A20", "A20", 4000},
		//			"A21": {"A21", "A21", 3000},
		//			"A22": {"A22", "A22", 3000},
		//			"A23": {"A23", "A23", 3000},
		//			"A24": {"A24", "A24", 3000},
		//			"A25": {"A25", "A25", 3000},
		//			"A26": {"A26", "A26", 3000},
		//			"A27": {"A27", "A27", 3000},
		//			"A28": {"A28", "A28", 3000},
		//			"A29": {"A29", "A29", 3000},
		//			"A30": {"A30", "A30", 3000},
		//			"A31": {"A31", "A31", 2000},
		//			"A32": {"A32", "A32", 2000},
		//			"A33": {"A33", "A33", 2000},
		//			"A34": {"A34", "A34", 2000},
		//			"A35": {"A35", "A35", 2000},
		//			"A36": {"A36", "A36", 2000},
		//			"A37": {"A37", "A37", 2000},
		//			"A38": {"A38", "A38", 2000},
		//			"A39": {"A39", "A39", 2000},
		//			"A40": {"A40", "A40", 2000},
		//		},
		//	},
		//},

		//// 21++3 maxSignerCount < defaultOfficialMaxSignerCount
		//{
		//	addrNames: []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10",
		//		"A11", "A12", "A13", "A14", "A15", "A16", "A17", "A18", "A19", "A20",
		//		"A21", "A22", "A23", "A24", "A25", "A26", "A27", "A28", "A29", "A30",
		//		"A31", "A32", "A33", "A34", "A35", "A36", "A37", "A38", "A39", "A40"},
		//	period:           uint64(3),
		//	epoch:            uint64(300),
		//	maxSignerCount:   uint64(20),//22呢
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: ui nt64(0),
		//	selfVoters: []testerSelfVoter{{"A1", 5000}, {"A2", 5000}, {"A3", 5000}, {"A4", 5000}, {"A5", 5000},
		//		{"A6", 5000}, {"A7", 5000}, {"A8", 5000}, {"A9", 5000}, {"A10", 5000},
		//		{"A11", 4000}, {"A12", 4000}, {"A13", 4000}, {"A14", 4000}, {"A15", 4000},
		//		{"A16", 4000}, {"A17", 4000}, {"A18", 4000}, {"A19", 4000}, {"A20", 4000},
		//		{"A21", 3000}, {"A22", 3000}, {"A23", 3000}, {"A24", 3000}, {"A25", 3000},
		//		{"A26", 3000}, {"A27", 3000}, {"A28", 3000}, {"A29", 3000}, {"A30", 3000},
		//		{"A31", 2000}, {"A32", 2000}, {"A33", 2000}, {"A34", 2000}, {"A35", 2000},
		//		{"A36", 2000}, {"A37", 2000}, {"A38", 2000}, {"A39", 2000}, {"A40", 2000}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}},//21
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, //42
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		//50
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10",
		//			"A11", "A12", "A13", "A14", "A15", "A16", "A17", "A18", "A19", "A20"},
		//		Tally: map[string]int{"A1": 5000, "A2": 5000, "A3": 5000, "A4": 5000, "A5": 5000, "A6": 5000, "A7": 5000, "A8": 5000, "A9": 5000, "A10": 5000,
		//			"A11": 4000, "A12": 4000, "A13": 4000, "A14": 4000, "A15": 4000, "A16": 4000, "A17": 4000, "A18": 4000, "A19": 4000, "A20": 4000,
		//			"A21": 3000, "A22": 3000, "A23": 3000, "A24": 3000, "A25": 3000, "A26": 3000, "A27": 3000, "A28": 3000, "A29": 3000, "A30": 3000,
		//			"A31": 2000, "A32": 2000, "A33": 2000, "A34": 2000, "A35": 2000, "A36": 2000, "A37": 2000, "A38": 2000, "A39": 2000, "A40": 2000},
		//		Voters: map[string]int{"A1": 0, "A2": 0, "A3": 0, "A4": 0, "A5": 0, "A6": 0, "A7": 0, "A8": 0, "A9": 0, "A10": 0,
		//			"A11": 0, "A12": 0, "A13": 0, "A14": 0, "A15": 0, "A16": 0, "A17": 0, "A18": 0, "A19": 0, "A20": 0,
		//			"A21": 0, "A22": 0, "A23": 0, "A24": 0, "A25": 0, "A26": 0, "A27": 0, "A28": 0, "A29": 0, "A30": 0,
		//			"A31": 0, "A32": 0, "A33": 0, "A34": 0, "A35": 0, "A36": 0, "A37": 0, "A38": 0, "A39": 0, "A40": 0},
		//		Votes: map[string]*testerVote{
		//			"A1":  {"A1", "A1", 5000},
		//			"A2":  {"A2", "A2", 5000},
		//			"A3":  {"A3", "A3", 5000},
		//			"A4":  {"A4", "A4", 5000},
		//			"A5":  {"A5", "A5", 5000},
		//			"A6":  {"A6", "A6", 5000},
		//			"A7":  {"A7", "A7", 5000},
		//			"A8":  {"A8", "A8", 5000},
		//			"A9":  {"A9", "A9", 5000},
		//			"A10": {"A10", "A10", 5000},
		//			"A11": {"A11", "A11", 4000},
		//			"A12": {"A12", "A12", 4000},
		//			"A13": {"A13", "A13", 4000},
		//			"A14": {"A14", "A14", 4000},
		//			"A15": {"A15", "A15", 4000},
		//			"A16": {"A16", "A16", 4000},
		//			"A17": {"A17", "A17", 4000},
		//			"A18": {"A18", "A18", 4000},
		//			"A19": {"A19", "A19", 4000},
		//			"A20": {"A20", "A20", 4000},
		//			"A21": {"A21", "A21", 3000},
		//			"A22": {"A22", "A22", 3000},
		//			"A23": {"A23", "A23", 3000},
		//			"A24": {"A24", "A24", 3000},
		//			"A25": {"A25", "A25", 3000},
		//			"A26": {"A26", "A26", 3000},
		//			"A27": {"A27", "A27", 3000},
		//			"A28": {"A28", "A28", 3000},
		//			"A29": {"A29", "A29", 3000},
		//			"A30": {"A30", "A30", 3000},
		//			"A31": {"A31", "A31", 2000},
		//			"A32": {"A32", "A32", 2000},
		//			"A33": {"A33", "A33", 2000},
		//			"A34": {"A34", "A34", 2000},
		//			"A35": {"A35", "A35", 2000},
		//			"A36": {"A36", "A36", 2000},
		//			"A37": {"A37", "A37", 2000},
		//			"A38": {"A38", "A38", 2000},
		//			"A39": {"A39", "A39", 2000},
		//			"A40": {"A40", "A40", 2000},
		//		},
		//	},
		//},

		// 21++4len(tallySlice) <= defaultOfficialThirdLevelCount（30）
		//{
		//	addrNames: []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10",
		//		"A11", "A12", "A13", "A14", "A15", "A16", "A17", "A18", "A19", "A20",
		//		"A21", "A22", "A23", "A24", "A25", "A26", "A27", "A28", "A29", "A30"},
		//	period:           uint64(3),
		//	epoch:            uint64(300),
		//	maxSignerCount:   uint64(21),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters: []testerSelfVoter{{"A1", 5000}, {"A2", 5000}, {"A3", 5000}, {"A4", 5000}, {"A5", 5000},
		//		{"A6", 5000}, {"A7", 5000}, {"A8", 5000}, {"A9", 5000}, {"A10", 5000},
		//		{"A11", 4000}, {"A12", 4000}, {"A13", 4000}, {"A14", 4000}, {"A15", 4000},
		//		{"A16", 4000}, {"A17", 4000}, {"A18", 4000}, {"A19", 4000}, {"A20", 4000},
		//		{"A21", 3100}, {"A22", 3000}, {"A23", 3000}, {"A24", 3000}, {"A25", 3000},
		//		{"A26", 3000}, {"A27", 3000}, {"A28", 3000}, {"A29", 3000}, {"A30", 3000}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}},//21
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, //42
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		{[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}}, {[]testerTransaction{}},
		//		//50
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10",
		//			"A11", "A12", "A13", "A14", "A15", "A16", "A17", "A18", "A19", "A20", "A21"},
		//		Tally: map[string]int{"A1": 5000, "A2": 5000, "A3": 5000, "A4": 5000, "A5": 5000, "A6": 5000, "A7": 5000, "A8": 5000, "A9": 5000, "A10": 5000,
		//			"A11": 4000, "A12": 4000, "A13": 4000, "A14": 4000, "A15": 4000, "A16": 4000, "A17": 4000, "A18": 4000, "A19": 4000, "A20": 4000,
		//			"A21": 3100, "A22": 3000, "A23": 3000, "A24": 3000, "A25": 3000, "A26": 3000, "A27": 3000, "A28": 3000, "A29": 3000, "A30": 3000},
		//		Voters: map[string]int{"A1": 0, "A2": 0, "A3": 0, "A4": 0, "A5": 0, "A6": 0, "A7": 0, "A8": 0, "A9": 0, "A10": 0,
		//			"A11": 0, "A12": 0, "A13": 0, "A14": 0, "A15": 0, "A16": 0, "A17": 0, "A18": 0, "A19": 0, "A20": 0,
		//			"A21": 0, "A22": 0, "A23": 0, "A24": 0, "A25": 0, "A26": 0, "A27": 0, "A28": 0, "A29": 0, "A30": 0},
		//		Votes: map[string]*testerVote{
		//			"A1":  {"A1", "A1", 5000},
		//			"A2":  {"A2", "A2", 5000},
		//			"A3":  {"A3", "A3", 5000},
		//			"A4":  {"A4", "A4", 5000},
		//			"A5":  {"A5", "A5", 5000},
		//			"A6":  {"A6", "A6", 5000},
		//			"A7":  {"A7", "A7", 5000},
		//			"A8":  {"A8", "A8", 5000},
		//			"A9":  {"A9", "A9", 5000},
		//			"A10": {"A10", "A10", 5000},
		//			"A11": {"A11", "A11", 4000},
		//			"A12": {"A12", "A12", 4000},
		//			"A13": {"A13", "A13", 4000},
		//			"A14": {"A14", "A14", 4000},
		//			"A15": {"A15", "A15", 4000},
		//			"A16": {"A16", "A16", 4000},
		//			"A17": {"A17", "A17", 4000},
		//			"A18": {"A18", "A18", 4000},
		//			"A19": {"A19", "A19", 4000},
		//			"A20": {"A20", "A20", 4000},
		//			"A21": {"A21", "A21", 3100},
		//			"A22": {"A22", "A22", 3000},
		//			"A23": {"A23", "A23", 3000},
		//			"A24": {"A24", "A24", 3000},
		//			"A25": {"A25", "A25", 3000},
		//			"A26": {"A26", "A26", 3000},
		//			"A27": {"A27", "A27", 3000},
		//			"A28": {"A28", "A28", 3000},
		//			"A29": {"A29", "A29", 3000},
		//			"A30": {"A30", "A30", 3000},
		//		},
		//	},
		},
	}
	//chaorstest**/

	// Run through the scenarios and test them
	for i, tt := range tests {
		candidateNeedPD = tt.candidateNeedPD
		if tt.vlCnt == 0 {
			tt.vlCnt = 1
		}
		// Create the account pool and generate the initial set of all address in addrNames
		accounts := newTesterAccountPool()
		addrNames := make([]common.Address, len(tt.addrNames))
		for j, signer := range tt.addrNames {
			addrNames[j] = accounts.address(signer)
		}
		for j := 0; j < len(addrNames); j++ {
			for k := j + 1; k < len(addrNames); k++ {
				if bytes.Compare(addrNames[j][:], addrNames[k][:]) > 0 {
					addrNames[j], addrNames[k] = addrNames[k], addrNames[j]
				}
			}
		}

		var snap *Snapshot
		// Prepare data for the genesis block
		var genesisVotes []*Vote             // for create the new snapshot of genesis block
		var selfVoteSigners []common.Address // for header extra
		alreadyVote := make(map[common.Address]struct{})
		for _, voter := range tt.selfVoters {
			if _, ok := alreadyVote[accounts.address(voter.voter)]; !ok {
				genesisVotes = append(genesisVotes, &Vote{
					Voter:     accounts.address(voter.voter),
					Candidate: accounts.address(voter.voter),
					Stake:     big.NewInt(int64(voter.balance)),
				})
				selfVoteSigners = append(selfVoteSigners, accounts.address(voter.voter))
				alreadyVote[accounts.address(voter.voter)] = struct{}{}
			}
		}

		// extend length of extra, so address of CoinBase can keep signature .
		genesis := &core.Genesis{
			ExtraData: make([]byte, extraVanity+extraSeal),
		}

		// Create a pristine blockchain with the genesis injected
		db := ethdb.NewMemDatabase()
		genesis.Commit(db)

		// Create new alien
		alien := New(&params.AlienConfig{
			Period:          tt.period,
			Epoch:           tt.epoch,
			MinVoterBalance: big.NewInt(int64(tt.minVoterBalance)),
			MaxSignerCount:  tt.maxSignerCount,
			SelfVoteSigners: selfVoteSigners,
		}, db)

		// Assemble a chain of headers from the cast votes
		headers := make([]*types.Header, len(tt.txHeaders))
		for j, header := range tt.txHeaders {

			var currentBlockVotes []Vote
			var currentBlockProposals []Proposal
			var currentBlockDeclares []Declare
			var modifyPredecessorVotes []Vote
			for _, trans := range header.txs {
				if trans.isVote {
					if trans.balance >= tt.minVoterBalance && (!candidateNeedPD || snap.isCandidate(accounts.address(trans.to))) {
						// vote event
						currentBlockVotes = append(currentBlockVotes, Vote{
							Voter:     accounts.address(trans.from),
							Candidate: accounts.address(trans.to),
							Stake:     big.NewInt(int64(trans.balance)),
						})
					}
				} else if trans.isProposal {
					if snap.isCandidate(accounts.address(trans.from)) {
						currentBlockProposals = append(currentBlockProposals, Proposal{
							Hash:                   common.HexToHash(trans.txHash),
							ValidationLoopCnt:      tt.vlCnt,
							ImplementNumber:        big.NewInt(1),
							ProposalType:           trans.proposalType,
							Proposer:               accounts.address(trans.from),
							Candidate:              accounts.address(trans.candidate),
							MinerRewardPerThousand: minerRewardPerThousand,
							Declares:               []*Declare{},
							ReceivedNumber:         big.NewInt(int64(j)),
						})
					}
				} else if trans.isDeclare {
					if snap.isCandidate(accounts.address(trans.from)) {

						currentBlockDeclares = append(currentBlockDeclares, Declare{
							ProposalHash: common.HexToHash(trans.txHash),
							Declarer:     accounts.address(trans.from),
							Decision:     trans.decision,
						})

					}
				} else {
					// modify balance
					// modifyPredecessorVotes
					// only consider the voter
					modifyPredecessorVotes = append(modifyPredecessorVotes, Vote{
						Voter: accounts.address(trans.from),
						Stake: big.NewInt(int64(trans.balance)),
					})
				}

				////构造交易
				//tx := types.Transaction{
				//	dd
				//}
			}
			currentHeaderExtra := HeaderExtra{}
			signer := common.Address{}

			// (j==0) means (header.Number==1)
			if j == 0 {
				for k := 0; k < int(tt.maxSignerCount); k++ {
					currentHeaderExtra.SignerQueue = append(currentHeaderExtra.SignerQueue, selfVoteSigners[k%len(selfVoteSigners)])
				}
				currentHeaderExtra.LoopStartTime = tt.genesisTimestamp // here should be parent genesisTimestamp
				signer = selfVoteSigners[0]

			} else {
				// decode parent header.extra
				rlp.DecodeBytes(headers[j-1].Extra[extraVanity:len(headers[j-1].Extra)-extraSeal], &currentHeaderExtra)
				signer = currentHeaderExtra.SignerQueue[uint64(j)%tt.maxSignerCount]
				// means header.Number % tt.maxSignerCount == 0
				if (j+1)%int(tt.maxSignerCount) == 0 {
					snap, err := alien.snapshot(&testerChainReader{db: db}, headers[j-1].Number.Uint64(), headers[j-1].Hash(), headers, nil, uint64(tt.lcrs))
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

					currentHeaderExtra.LoopStartTime = currentHeaderExtra.LoopStartTime + tt.period*tt.maxSignerCount
				} else {

				}
			}

			currentHeaderExtra.CurrentBlockVotes = currentBlockVotes
			currentHeaderExtra.ModifyPredecessorVotes = modifyPredecessorVotes
			currentHeaderExtra.CurrentBlockProposals = currentBlockProposals
			currentHeaderExtra.CurrentBlockDeclares = currentBlockDeclares
			currentHeaderExtraEnc, err := rlp.EncodeToBytes(currentHeaderExtra)
			if err != nil {
				t.Errorf("test %d: failed to rlp encode to bytes: %v", i, err)
				continue
			}
			// Create the genesis block with the initial set of signers
			ExtraData := make([]byte, extraVanity+len(currentHeaderExtraEnc)+extraSeal)
			copy(ExtraData[extraVanity:], currentHeaderExtraEnc)

			headers[j] = &types.Header{
				Number:   big.NewInt(int64(j) + 1),
				Time:     big.NewInt((int64(j)+1)*int64(defaultBlockPeriod) - 1),
				Coinbase: signer,
				Extra:    ExtraData,
			}
			if j > 0 {
				headers[j].ParentHash = headers[j-1].Hash()
			}
			accounts.sign(headers[j], accounts.name(signer))

			// Pass all the headers through alien and ensure tallying succeeds
			snap, err = alien.snapshot(&testerChainReader{db: db}, headers[j].Number.Uint64(), headers[j].Hash(), headers[:j+1], genesisVotes, uint64(tt.lcrs))
			genesisVotes = []*Vote{}
			if err != nil {
				t.Errorf("test %d: failed to create voting snapshot: %v", i, err)
				continue
			}
		}

		// verify the result in test case
		head := headers[len(headers)-1]
		snap, err := alien.snapshot(&testerChainReader{db: db}, head.Number.Uint64(), head.Hash(), headers, nil, uint64(tt.lcrs))
		//
		if err != nil {
			t.Errorf("test %d: failed to create voting snapshot: %v", i, err)
			continue
		}

		//cct
		for _, name := range tt.addrNames {

			fmt.Printf("ccc name:%s for addr:%s\n", name, accounts.address(name).Hex())
		}

		// check signers ???
		if len(tt.result.Signers) > 0 {

			signers := map[common.Address]int{}
			// snap里的signer数量
			for _, signer := range snap.Signers {
				signers[*signer] = 1
				fmt.Printf("ccc signer in snap:%s\n", signer.Hex())

			}

			//测试结果的signers队列与snap的一一对应
			for _, signer := range tt.result.Signers {
				signers[accounts.address(signer)] += 2
				fmt.Printf("ccc result signer:%s\n", accounts.address(signer).Hex())
			}


			for signer, cnt := range signers {

				fmt.Printf("ccc cnt:%d for signer::%s\n", cnt, signer.Hex())
			}
			// 检测测试结果的signers队列与snap的是否一致
			for address, cnt := range signers {
				if cnt != 3 {
					t.Errorf("test %d: signer %v address: %v not in result signers %d", i, accounts.name(address), address.Hex(), cnt)
					continue
				}
			}
		} else {
			// check signers official 21 node
			firstLevel := map[common.Address]int{}
			secondLevel := map[common.Address]int{}
			thirdLevel := map[common.Address]int{}
			otherLevel := map[common.Address]int{}

			for signer, tally := range tt.result.Tally {
				switch tally {
				case 5000:
					firstLevel[accounts.address(signer)] = 0
				case 4000:
					secondLevel[accounts.address(signer)] = 0
				case 3000:
					thirdLevel[accounts.address(signer)] = 0
				case 2000:
					otherLevel[accounts.address(signer)] = 0

				}

			}
			var l1, l2, l3, l4 int
			for _, signer := range snap.Signers {
				if _, ok := firstLevel[*signer]; ok {
					l1 += 1
					continue
				}
				if _, ok := secondLevel[*signer]; ok {
					l2 += 1
					continue
				}
				if _, ok := thirdLevel[*signer]; ok {
					l3 += 1
					continue
				}
				if _, ok := otherLevel[*signer]; ok {
					l4 += 1
				}
			}
			if l1 != 10 || l2 != 6 || l3 != 4 || l4 != 1 {
				t.Errorf("test %d: signer not select right count from different level l1 = %d, l2 = %d, l3 = %d, l4 = %d", i, l1, l2, l3, l4)
			}

		}

		// check tally
		if len(tt.result.Tally) != len(snap.Tally) {
			t.Errorf("test %d: tally length result %d, snap %d dismatch", i, len(tt.result.Tally), len(snap.Tally))
		}
		for name, tally := range tt.result.Tally {
			if big.NewInt(int64(tally)).Cmp(snap.Tally[accounts.address(name)]) != 0 {
				t.Errorf("test %d: tally %v address: %v, tally:%v ,result: %v", i, name, accounts.address(name), snap.Tally[accounts.address(name)], big.NewInt(int64(tally)))
				continue
			}
		}
		// check voters
		if len(tt.result.Voters) != len(snap.Voters) {
			t.Errorf("test %d: voter length result %d, snap %d dismatch", i, len(tt.result.Voters), len(snap.Voters))
		}
		for name, number := range tt.result.Voters {
			if snap.Voters[accounts.address(name)].Cmp(big.NewInt(int64(number))) != 0 {
				t.Errorf("test %d: voter %v address: %v, number:%v ,result: %v", i, name, accounts.address(name).Hex(), snap.Voters[accounts.address(name)], big.NewInt(int64(number)))
				continue
			}
		}
		// check votes

		if len(tt.result.Votes) != len(snap.Votes) {
			t.Errorf("test %d: votes length result %d, snap %d dismatch", i, len(tt.result.Votes), len(snap.Votes))
		}
		for name, vote := range tt.result.Votes {
			snapVote, ok := snap.Votes[accounts.address(name)]
			if !ok {
				t.Errorf("test %d: votes %v address: %v can not found", i, name, accounts.address(name))

			}
			if snapVote.Voter != accounts.address(vote.voter) {
				t.Errorf("test %d: votes voter dismatch %v address: %v  , show in snap is %v address: %v", i, vote.voter, accounts.address(vote.voter), accounts.name(snapVote.Voter), snapVote.Voter)
			}
			if snapVote.Candidate != accounts.address(vote.candidate) {
				t.Errorf("test %d: votes candidate dismatch %v address: %v , show in snap is %v address: %v ", i, vote.candidate, accounts.address(vote.candidate), accounts.name(snapVote.Candidate), snapVote.Candidate)
			}
			if snapVote.Stake.Cmp(big.NewInt(int64(vote.stake))) != 0 {
				t.Errorf("test %d: votes stake dismatch %v ,show in snap is %v ", i, vote.stake, snapVote.Stake)
			}
		}
	}
}

func TestBalance(t *testing.T)  {

	s := big.NewInt(5e+18)
	r := new(big.Int).Set(s)
	m1 := r.Mul(r, big.NewInt(618))
	m1 = m1.Div(m1, big.NewInt(1000))
	m := m1.Uint64()
	v := s.Sub(s, m1).Uint64()

	//a := big.NewInt(0)
	//b := big.NewInt(0)
	//c := big.NewInt(0)
	//d := big.NewInt(0)
	//e := big.NewInt(0)
	//f := big.NewInt(0)
	//g := big.NewInt(0)


	//v = float64(v)

	//s := 5.0
	//m := s * 618.0/1000.0
	//v := s - m


	fmt.Printf("ccc Block :%v-----%v", m, v)

	tests := []struct {
		addrNames        []string             // accounts used in this case
		candidateNeedPD  bool                 // candidate from POA
		period           uint64               // default 3
		epoch            uint64               // default 30000
		maxSignerCount   uint64               // default 5 for test
		minVoterBalance  int                  // default 50
		genesisTimestamp uint64               // default time.now() - period + 1
		lcrs             uint64               // loop count to recreate signers from top tally
		selfVoters       []testerSelfVoter    //
		txHeaders        []testerSingleHeader //
		result           testerSnapshot       // the result of current snapshot
		vlCnt            uint64
		testBalances 		 map[string]*big.Int
		historyHashes []string
	}{
		//balance0 A,B两个自选签名者(A,B)，目前只出了一个块 所以还没轮到b出块 A自己选自己所以奖励都是自己的
		{
			addrNames:        []string{"A", "B"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(3),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},// 1 A
			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"A","B"},
				Tally:   map[string]int{"A":100, "B": 200},
				Voters:  map[string]int{"A": 0, "B": 0},
				Votes: map[string]*testerVote{
					"A": {"A", "A", 100},
					"B": {"B", "B", 200},
				},
			},
			testBalances: map[string]*big.Int {
				"A":CalReward(m, 1, v, []uint64{1}, []uint64{100}, []uint64{100}), //m*1 + v*1*100/100,
				"B":big.NewInt(0),
			},
		},

		//balance1 A,B两个自选签名者(A,B)，目前出了2个块 a,b各自出了一个  他们都是自自己选自己所有块奖励都是对应签名者
		{
			addrNames:        []string{"A", "B"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(3),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//a
				{[]testerTransaction{}},//b
			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"A", "B"},
				Tally:   map[string]int{"A":100, "B": 200},
				Voters:  map[string]int{"A": 0, "B": 0},
				Votes: map[string]*testerVote{
					"A": {"A", "A", 100},
					"B": {"B", "B", 200},
				},
			},
			testBalances: map[string]*big.Int {
				"A":CalReward(m, 1, v, []uint64{1}, []uint64{100}, []uint64{100}), //m*1 + v*1,
				"B":CalReward(m, 1, v, []uint64{1}, []uint64{100}, []uint64{100}), //m*1 + v*1,
			},
		},

		// balance2 A,B两个自选签名者(A,B)
		// 在区块1 A把票转投给了B, 此前并没有A挖出的块 因此A只能获得A的挖矿奖励和B挖矿的投票奖励
		// 此时新一轮签名轮次还未到来  还是由A，B轮流出块
		{
			addrNames:        []string{"A", "B"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(5),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//a
				{[]testerTransaction{}},//b
				{[]testerTransaction{}},//a 3
				{[]testerTransaction{}},//b
				{[]testerTransaction{}}, //a  //5
			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"B"},
				Tally:   map[string]int{"B": 300},
				Voters:  map[string]int{"A": 1, "B": 0},
				Votes: map[string]*testerVote{
					"A": {"A", "B", 100},
					"B": {"B", "B", 200},
				},
			},
			testBalances: map[string]*big.Int {
				"A":CalReward(m, 3, v, []uint64{2}, []uint64{100}, []uint64{300}), //m*3 + v*100.0/300.0*2,
				"B":CalReward(m, 2, v, []uint64{2}, []uint64{200}, []uint64{300}),//m*2 + v*200.0/300.0*2,
			},
		},

		// balance3 A,B两个自选签名者(A,B)，目前出了2个块 a,b轮流出块
		// 在区块2 A把票转投给了B, 但是在此前区块1A还是出块者A的投票者  会有投票奖励 后面依然有自己的挖矿奖励和来自B的投票奖励
		// 此时新一轮签名轮次还未到来  还是由A，B轮流出块
		{
			addrNames:        []string{"A", "B"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(5),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//a
				{[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
				{[]testerTransaction{}},//a 3
				{[]testerTransaction{}},//b
				{[]testerTransaction{}}, //a  //5
			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"B"},
				Tally:   map[string]int{"B": 300},
				Voters:  map[string]int{"A": 2, "B": 0},
				Votes: map[string]*testerVote{
					"A": {"A", "B", 100},
					"B": {"B", "B", 200},
				},
			},
			testBalances: map[string]*big.Int {
				"A":CalReward(m, 3, v, []uint64{1,2}, []uint64{100,100}, []uint64{100,300}), //m*3 + v*100.0/100.0*1 + v*100.0/300.0*2,
				"B":CalReward(m, 2, v, []uint64{2}, []uint64{200}, []uint64{300}), //m*2 + v*200.0/300.0*2,
			},
		},

		// balance4 A,B两个自选签名者(A,B)
		// 在区块2 A把票转投给了B, 但是在此前区块1A还是出块者A的投票者  会有投票奖励 后面依然有自己的挖矿奖励和来自B的投票奖励
		// 新一轮签名轮次已经到来  签名者只有b 区块3后只有B能出块，A获得的只是B挖矿的投票奖励
		{
			addrNames:        []string{"A", "B"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(3),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//a
				{[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
				{[]testerTransaction{}},//a 3
				{[]testerTransaction{}},//b
				{[]testerTransaction{}}, //b  //5
			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"B"},
				Tally:   map[string]int{"B": 300},
				Voters:  map[string]int{"A": 2, "B": 0},
				Votes: map[string]*testerVote{
					"A": {"A", "B", 100},
					"B": {"B", "B", 200},
				},
			},
			testBalances: map[string]*big.Int {
				"A":CalReward(m, 2, v, []uint64{1,3}, []uint64{100,100}, []uint64{100,300}), //m*2 + v*100.0/100.0*1 + v*100.0/300.0*3,
				"B":CalReward(m, 3, v, []uint64{3}, []uint64{200}, []uint64{300}), //m*3 + v*200.0/300.0*3,
			},
		},

		// balance5 A,B两个自选签名者(A,B)，目前出了2个块 a,b轮流出块
		// 在区块2 A把票转投给了C, 但是在此前区块1A还是出块者A的投票者  会有投票奖励 后面依然有自己的挖矿奖励和来自B的投票奖励
		// 新一轮签名轮次还未到来  所以C并没有参与挖矿故没有余额
		{
			addrNames:        []string{"A", "B", "C"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(5),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//a
				{[]testerTransaction{{from: "A", to: "C", balance: 100, isVote: true}}},//b
				{[]testerTransaction{}},//a 3
				{[]testerTransaction{}},//b

			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"B", "A"},
				Tally:   map[string]int{"B": 200, "C":100},
				Voters:  map[string]int{"A": 2, "B": 0},
				Votes: map[string]*testerVote{
					"A": {"A", "C", 100},
					"B": {"B", "B", 200},
				},
			},
			testBalances: map[string]*big.Int {
				"A":CalReward(m, 2, v, []uint64{1}, []uint64{100}, []uint64{100}), //m*2 + v*100.0/100.0*1,
				"B":CalReward(m, 2, v, []uint64{2}, []uint64{200}, []uint64{200}), //m*2 + v*200.0/200.0*2,
				"C":big.NewInt(0),
			},
		},

		// balance6 A,B两个自选签名者(A,B)，目前出了2个块 a,b轮流出块
		// 在区块2 A把票转投给了C, 但是在此前区块1A还是出块者A的投票者  会有投票奖励 后面依然有自己的挖矿奖励和来自B的投票奖励
		// 新一轮签名轮次已经到来  所以C也参与1次挖矿有相应的奖励
		{
			addrNames:        []string{"A", "B", "C"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(5),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//a
				{[]testerTransaction{{from: "A", to: "C", balance: 100, isVote: true}}},//b
				{[]testerTransaction{}},//a 3
				{[]testerTransaction{}},//b
				{[]testerTransaction{}},//a//5
				{[]testerTransaction{}},//b
				{[]testerTransaction{}},//c

			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"B", "C"},
				Tally:   map[string]int{"B": 200, "C":100},
				Voters:  map[string]int{"A": 2, "B": 0},
				Votes: map[string]*testerVote{
					"A": {"A", "C", 100},
					"B": {"B", "B", 200},
				},
			},
			testBalances: map[string]*big.Int {
				"A":CalReward(m, 3, v, []uint64{2}, []uint64{100}, []uint64{100}), //m*3 + v*100.0/100.0*2,
				"B":CalReward(m, 3, v, []uint64{3}, []uint64{200}, []uint64{200}), //m*3 + v*200.0/200.0*3,
				"C":CalReward(m, 1, v, []uint64{0}, []uint64{100}, []uint64{100}), //m*1,
			},
		},

		//balance7 A,B两个自选签名者(A,B)，目前出了2个块 a,b轮流出块
		// 在区块2 A把票转投给了B,B把票投给了A 但是在此前区块1A还是出块者A的投票者  会有投票奖励
		// 后面他们各自得到自己的挖矿奖励和对方挖矿的投票奖励
		{
			addrNames:        []string{"A", "B", "C"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(5),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//a
				{[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}, {from: "B", to: "A", balance: 200, isVote: true}}},//b
				{[]testerTransaction{}},//a 3
				{[]testerTransaction{}},//b
				{[]testerTransaction{}},//a//5
			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"B", "A"},
				Tally:   map[string]int{"B": 100, "A":200},
				Voters:  map[string]int{"A": 2, "B": 2},
				Votes: map[string]*testerVote{
					"A": {"A", "B", 100},
					"B": {"B", "A", 200},
				},
			},
			testBalances: map[string]*big.Int {
				"A":CalReward(m, 3, v, []uint64{1,2}, []uint64{100,100}, []uint64{100,100}), //m*3 + v*100.0/100.0*1 + v*100/100*2,
				"B":CalReward(m, 2, v, []uint64{2}, []uint64{200}, []uint64{200}), //m*2 + v*200.0/200.0*2,
				"C":big.NewInt(0.0),
			},
		},

		//balance8 A,B两个自选签名者(A,B)，目前出了2个块 a,b轮流出块
		// 在区块2 A把票转投给了B,  区块4B把票投给了A 这个时候要注意在投票前各自的投票奖励与自己相关和投票比例变化
		// 后面他们各自得到自己的挖矿奖励和对方挖矿的投票奖励
		{
			addrNames:        []string{"A", "B", "C"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(5),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//a
				{[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
				{[]testerTransaction{}},//a 3
				{[]testerTransaction{}},//b
				{[]testerTransaction{{from: "B", to: "A", balance: 200, isVote: true}}},//a 5
			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"B"},
				Tally:   map[string]int{"B": 100, "A":200},
				Voters:  map[string]int{"A": 2, "B": 5},
				Votes: map[string]*testerVote{
					"A": {"A", "B", 100},
					"B": {"B", "A", 200},
				},
			},
			testBalances: map[string]*big.Int {
				"A":CalReward(m, 3, v, []uint64{1,2}, []uint64{100,100}, []uint64{100,300}), //m*3 + v*100.0/100.0*1 + v*100/300.0*2,
				"B":CalReward(m, 2, v, []uint64{2,1}, []uint64{200,200}, []uint64{300,200}), //m*2 + v*200.0/300.0*2 + v*200.0/200.0*1,
				"C":big.NewInt(0.0),
			},
		},

		//balance9 A,B两个自选签名者(A,B)，目前出了2个块 a,b轮流出块
		// 在区块2 C把票转投给了B,  虽然C不是出块者，但是他能够分到b出块的投票奖励
		{
			addrNames:        []string{"A", "B", "C"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(5),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//a
				{[]testerTransaction{{from: "C", to: "B", balance: 300, isVote: true}}},//b
				{[]testerTransaction{}},//a 3
				{[]testerTransaction{}},//b
				{[]testerTransaction{}},//a 5
			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"B", "A"},
				Tally:   map[string]int{"B": 500, "A":100},
				Voters:  map[string]int{"A": 0, "B": 0, "C":2},
				Votes: map[string]*testerVote{
					"A": {"A", "A", 100},
					"B": {"B", "B", 200},
					"C": {"C", "B", 300},
				},
			},
			testBalances: map[string]*big.Int {
				"A":CalReward(m, 3, v, []uint64{3}, []uint64{100}, []uint64{100}), //m*3 + v*100.0/100.0*3,
				"B":CalReward(m, 2, v, []uint64{2}, []uint64{200}, []uint64{500}), //m*2 + v*200.0/500.0*2,
				"C":CalReward(m, 0, v, []uint64{2}, []uint64{300}, []uint64{500}), //v*300/500*2,
			},
		},

		//balance10 A,B两个自选签名者(A,B)，
		// 在区块2 A把票转投给了B,  在区块4 A把票又投回给自己
		{
			addrNames:        []string{"A", "B", "C"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(5),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//a
				{[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
				{[]testerTransaction{}},//a 3
				{[]testerTransaction{{from: "A", to: "A", balance: 100, isVote: true}}},//b
				{[]testerTransaction{}},//a 5
				{[]testerTransaction{}},//b 6

			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"B", "A"},
				Tally:   map[string]int{"B":200, "A":100},
				Voters:  map[string]int{"A":4, "B":0},
				Votes: map[string]*testerVote{
					"A": {"A", "A", 100},
					"B": {"B", "B", 200},
				},
			},
			//m*4 + v*100.0/100.0*3 + v*100.0/300.0*1,
			//g.Add(a.Mul(m, big.NewInt(4)), f.Add(c.Div(b.Mul(v,big.NewInt(100*3)),big.NewInt(100)),e.Div(d.Mul(v,big.NewInt(100*1)), big.NewInt(300)))),	//m*2 + v*200.0/300.0*1 + v*200.0/200.0*1,
			testBalances: map[string]*big.Int {//m*2 + v*1*200.0/300.0 + v*1*200.0/200.0,
				"B":CalReward(m, 3, v, []uint64{1, 2}, []uint64{200, 200}, []uint64{300, 200}), //m*2 + v*200.0/300.0*1 + v*200.0/200.0*1,
				"A":CalReward(m, 3, v, []uint64{2, 1}, []uint64{100, 100}, []uint64{100, 300}), //m*2 + v*200.0/300.0*1 + v*200.0/200.0*1,
				"C":big.NewInt(0),
			},
		},

		//balance11 A,B两个自选签名者(A,B)，
		// 在区块2 A把票转投给了B,  在区块5 A把票又投回给自己
		{
			addrNames:        []string{"A", "B", "C"},
			period:           uint64(3),
			epoch:            uint64(31),
			maxSignerCount:   uint64(5),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 100}, {"B", 200}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//a
				{[]testerTransaction{{from: "A", to: "B", balance: 100, isVote: true}}},//b
				{[]testerTransaction{}},//a 3
				{[]testerTransaction{}},//b
				{[]testerTransaction{{from: "A", to: "A", balance: 100, isVote: true}}},//a 5
				{[]testerTransaction{}},//b 6
				{[]testerTransaction{}},//b 7
				{[]testerTransaction{}},//b 8

			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"B"},
				Tally:   map[string]int{"B":200, "A":100},
				Voters:  map[string]int{"A":5, "B":0},
				Votes: map[string]*testerVote{
					"A": {"A", "A", 100},
					"B": {"B", "B", 200},
				},
			},
			//m*4 + v*100.0/100.0*3 + v*100.0/300.0*1,
			//g.Add(a.Mul(m, big.NewInt(4)), f.Add(c.Div(b.Mul(v,big.NewInt(100*3)),big.NewInt(100)),e.Div(d.Mul(v,big.NewInt(100*1)), big.NewInt(300)))),	//m*2 + v*200.0/300.0*1 + v*200.0/200.0*1,
			testBalances: map[string]*big.Int {//m*2 + v*1*200.0/300.0 + v*1*200.0/200.0,
				"A":CalReward(m, 3, v, []uint64{2, 2}, []uint64{100, 100}, []uint64{300, 100}), //m*3 + v*100.0/300.0*1 + v*100.0/100.0*2,
				"B":CalReward(m, 5, v, []uint64{2, 3}, []uint64{200, 200}, []uint64{300, 200}), //m*5 + v*200.0/300.0*2 + v*200.0/200.0*3,
				"C":big.NewInt(0),
			},
		},

		//balance12 A,B两个自选签名者(A,B) 区块4产生多个新的投票  区块6新的签名队列开始出块
		{
			//addrNames:        []string{"A", "B", "C"},
			addrNames:        []string{"A", "B", "C", "D", "E", "F", "H"},
			period:           uint64(3),
			epoch:            uint64(8),
			maxSignerCount:   uint64(5),
			minVoterBalance:  50,
			lcrs:             1,
			genesisTimestamp: uint64(0),
			selfVoters:       []testerSelfVoter{{"A", 260}, {"B", 205}},
			txHeaders: []testerSingleHeader{
				{[]testerTransaction{}},//{from: "B", to: "B", balance: 205, isVote: true}a
				{[]testerTransaction{}},//b
				{[]testerTransaction{}},//a
				{[]testerTransaction{{from: "C", to: "D", balance: 200, isVote: true}, {from: "D", to: "C", balance: 220, isVote: true}, {from: "E", to: "E", balance: 280, isVote: true}, {from: "F", to: "H", balance: 320, isVote: true}}},//b
				{[]testerTransaction{}},//5 HEACB  a
				{[]testerTransaction{}}, // h
				{[]testerTransaction{}}, //e
				{[]testerTransaction{}},//8 a

			},
			historyHashes:[]string{"a", "b", "c", "d", "e"},
			result: testerSnapshot{
				Signers: []string{"H", "E", "A", "C", "B"},
				Tally:   map[string]int{"D": 200, "C":220, "E":280, "H":320, "A":260, "B":205},
				Voters:  map[string]int{"C": 4, "D":4, "E":4, "F":4, "A":0, "B":0},
				Votes: map[string]*testerVote{
					"C": {"C", "D", 200},
					"D": {"D", "C", 220},
					"E": {"E", "E", 280},
					"B": {"B", "B", 205},
					"F": {"F", "H", 320},
					"A": {"A", "A", 260},
				},
			},
			testBalances: map[string]*big.Int{
				"A":CalReward(m, 4, v, []uint64{4}, []uint64{260}, []uint64{260}), //m*1 + v*4,
				"B":CalReward(m, 2, v, []uint64{2}, []uint64{205}, []uint64{205}), //m*2 + v*2,
				"C":big.NewInt(0),
				"D":big.NewInt(0),
				"E":CalReward(m, 1, v, []uint64{1}, []uint64{260}, []uint64{260}), //m*1 + v*1,
				"F":CalReward(m, 0, v, []uint64{1}, []uint64{320}, []uint64{320}), //m*1,
				"H":CalReward(m, 1, v, []uint64{0}, []uint64{200}, []uint64{200}), //m*1,
			},
		},


		// +++6A到期但不是因为到期而更新出signers(不在top5)
		//{
		//	//addrNames:        []string{"A", "B", "C"},
		//	addrNames:        []string{"A", "B", "C", "D", "E", "F", "H", "I", "J"},
		//	period:           uint64(3),
		//	epoch:            uint64(8),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 180}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}},//{from: "B", to: "B", balance: 205, isVote: true}
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{{from: "C", to: "D", balance: 200, isVote: true}, {from: "D", to: "C", balance: 260, isVote: true}, {from: "E", to: "E", balance: 280, isVote: true}, {from: "F", to: "H", balance: 320, isVote: true}, {from: "H", to: "I", balance: 210, isVote: true}}},
		//		{[]testerTransaction{}},//5
		//		{[]testerTransaction{}}, //
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},//9
		//		{[]testerTransaction{}},  //有没有这个结果都一样
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"D", "C", "E", "H", "I"},
		//		Tally:   map[string]int{"D": 200, "C":260, "E":280, "H":320, "I":210},
		//		Voters:  map[string]int{"C": 4, "D":4, "E":4, "F":4, "H":4},
		//		Votes: map[string]*testerVote{
		//			"C": {"C", "D", 200},
		//			"D": {"D", "C", 260},
		//			"E": {"E", "E", 280},
		//			//"B": {"B", "B", 200},
		//			"F": {"F", "H", 320},
		//			"H": {"H", "I", 210},
		//		},
		//	},

		// +++5有投票过期但是有效投票数未达到maxSignerCount
		//{
		//	//addrNames:        []string{"A", "B", "C"},
		//	addrNames:        []string{"A", "B", "C", "D", "E"},
		//	period:           uint64(3),
		//	epoch:            uint64(8),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 200}, {"B", 100}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}}, //{from: "B", to: "B", balance: 205, isVote: true}
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{{from: "C", to: "D", balance: 210, isVote: true}, {from: "D", to: "C", balance: 260, isVote: true}, {from: "E", to: "E", balance: 280, isVote: true}}},
		//		{[]testerTransaction{}}, //5
		//		{[]testerTransaction{}}, //
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}}, //9
		//		{[]testerTransaction{}}, //有没有这个结果都一样
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"D", "C", "E", "A", "B"},
		//		Tally:   map[string]int{"D": 210, "C": 260, "E": 280, "A": 200, "B": 100},
		//		Voters:  map[string]int{"C": 4, "D": 4, "E": 4, "A": 0, "B": 0},
		//		Votes: map[string]*testerVote{
		//			"C": {"C", "D", 210},
		//			"D": {"D", "C", 260},
		//			"E": {"E", "E", 280},
		//			"B": {"B", "B", 100},
		//			"A": {"A", "A", 200},
		//		},
		//	},
		//},

		// +++4有投票过期但是总候选人数未达到maxSignerCount
		//{
		//	//addrNames:        []string{"A", "B", "C"},
		//	addrNames:        []string{"A", "B", "C"},
		//	period:           uint64(3),
		//	epoch:            uint64(7),
		//	maxSignerCount:   uint64(5),
		//	minVoterBalance:  50,
		//	lcrs:             1,
		//	genesisTimestamp: uint64(0),
		//	selfVoters:       []testerSelfVoter{{"A", 200}},
		//	txHeaders: []testerSingleHeader{
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{{from: "B", to: "C", balance: 220, isVote: true}, {from: "C", to: "B", balance: 160, isVote: true}}},
		//		{[]testerTransaction{}},//5
		//		{[]testerTransaction{}}, //
		//		{[]testerTransaction{}},
		//		{[]testerTransaction{}},//8
		//	},
		//	result: testerSnapshot{
		//		Signers: []string{"A", "B", "C"},
		//		Tally:   map[string]int{"A":200, "B": 160, "C":220},
		//		Voters:  map[string]int{"A":0, "B": 4, "C":4},
		//		Votes: map[string]*testerVote{
		//			"B": {"B", "C", 220},
		//			"C": {"C", "B", 160},
		//			"A": {"A", "A", 200},
		//		},
		//	},
		//},
	}

	//fmt.Printf("test balance:%v---%v---%v----%v\n", m*3 + v*(100/100*1) + v*(100/300.0*2), m*3, v*(100/100*1), v*(100/300*2))


	// Run through the scenarios and test them
	for i, tt := range tests {
		candidateNeedPD = tt.candidateNeedPD
		if tt.vlCnt == 0 {
			tt.vlCnt = 1
		}
		// Create the account pool and generate the initial set of all address in addrNames
		accounts := newTesterAccountPool()
		addrNames := make([]common.Address, len(tt.addrNames))
		for j, signer := range tt.addrNames {
			addrNames[j] = accounts.address(signer)
		}
		for j := 0; j < len(addrNames); j++ {
			for k := j + 1; k < len(addrNames); k++ {
				if bytes.Compare(addrNames[j][:], addrNames[k][:]) > 0 {
					addrNames[j], addrNames[k] = addrNames[k], addrNames[j]
				}
			}
		}

		var snap *Snapshot
		//var lastSnap *Snapshot
		// Prepare data for the genesis block
		var genesisVotes []*Vote             // for create the new snapshot of genesis block
		var selfVoteSigners []common.Address // for header extra
		alreadyVote := make(map[common.Address]struct{})
		for _, voter := range tt.selfVoters {
			if _, ok := alreadyVote[accounts.address(voter.voter)]; !ok {
				genesisVotes = append(genesisVotes, &Vote{
					Voter:     accounts.address(voter.voter),
					Candidate: accounts.address(voter.voter),
					Stake:     big.NewInt(int64(voter.balance)),
				})
				selfVoteSigners = append(selfVoteSigners, accounts.address(voter.voter))
				alreadyVote[accounts.address(voter.voter)] = struct{}{}
			}
		}

		// extend length of extra, so address of CoinBase can keep signature .
		genesis := &core.Genesis{
			ExtraData: make([]byte, extraVanity+extraSeal),
		}

		// Create a pristine blockchain with the genesis injected
		db := ethdb.NewMemDatabase()
		genesis.Commit(db)
		//stateDB
		stateDB, _ := state.New(common.Hash{}, state.NewDatabase(db))

		// Create new alien
		alien := New(&params.AlienConfig{
			Period:          tt.period,
			Epoch:           tt.epoch,
			MinVoterBalance: big.NewInt(int64(tt.minVoterBalance)),
			MaxSignerCount:  tt.maxSignerCount,
			SelfVoteSigners: selfVoteSigners,
		}, db)

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
			&params.AlienConfig{
				Period:          tt.period,
				Epoch:           tt.epoch,
				MinVoterBalance: big.NewInt(int64(tt.minVoterBalance)),
				MaxSignerCount:  tt.maxSignerCount,
				SelfVoteSigners: selfVoteSigners,
			},
		}


		// Assemble a chain of headers from the cast votes
		headers := make([]*types.Header, len(tt.txHeaders))
		for j, header := range tt.txHeaders {

			var currentBlockVotes []Vote
			var currentBlockProposals []Proposal
			var currentBlockDeclares []Declare
			var modifyPredecessorVotes []Vote
			for _, trans := range header.txs {
				if trans.isVote {
					if trans.balance >= tt.minVoterBalance && (!candidateNeedPD || snap.isCandidate(accounts.address(trans.to))) {
						// vote event
						currentBlockVotes = append(currentBlockVotes, Vote{
							Voter:     accounts.address(trans.from),
							Candidate: accounts.address(trans.to),
							Stake:     big.NewInt(int64(trans.balance)),
						})
					}
				} else if trans.isProposal {
					if snap.isCandidate(accounts.address(trans.from)) {
						currentBlockProposals = append(currentBlockProposals, Proposal{
							Hash:                   common.HexToHash(trans.txHash),
							ValidationLoopCnt:      tt.vlCnt,
							ImplementNumber:        big.NewInt(1),
							ProposalType:           trans.proposalType,
							Proposer:               accounts.address(trans.from),
							Candidate:              accounts.address(trans.candidate),
							MinerRewardPerThousand: minerRewardPerThousand,
							Declares:               []*Declare{},
							ReceivedNumber:         big.NewInt(int64(j)),
						})
					}
				} else if trans.isDeclare {
					if snap.isCandidate(accounts.address(trans.from)) {

						currentBlockDeclares = append(currentBlockDeclares, Declare{
							ProposalHash: common.HexToHash(trans.txHash),
							Declarer:     accounts.address(trans.from),
							Decision:     trans.decision,
						})

					}
				} else {
					// modify balance
					// modifyPredecessorVotes
					// only consider the voter
					modifyPredecessorVotes = append(modifyPredecessorVotes, Vote{
						Voter: accounts.address(trans.from),
						Stake: big.NewInt(int64(trans.balance)),
					})
				}

				////构造交易
				//tx := types.Transaction{
				//	dd
				//}
			}
			currentHeaderExtra := HeaderExtra{}
			signer := common.Address{}

			// (j==0) means (header.Number==1)
			if j == 0 {
				for k := 0; k < int(tt.maxSignerCount); k++ {
					currentHeaderExtra.SignerQueue = append(currentHeaderExtra.SignerQueue, selfVoteSigners[k%len(selfVoteSigners)])
				}
				currentHeaderExtra.LoopStartTime = tt.genesisTimestamp // here should be parent genesisTimestamp
				signer = selfVoteSigners[0]

			} else {
				// decode parent header.extra
				rlp.DecodeBytes(headers[j-1].Extra[extraVanity:len(headers[j-1].Extra)-extraSeal], &currentHeaderExtra)
				//fmt.Printf("ccc signer get:j---%d======maxSig----%d\n SignerQueue:%v", j, tt.maxSignerCount, currentHeaderExtra.SignerQueue)
				signer = currentHeaderExtra.SignerQueue[uint64(j)%tt.maxSignerCount]
				// means header.Number % tt.maxSignerCount == 0
				if (j+1)%int(tt.maxSignerCount) == 0 {
					snap, err := alien.snapshot(&testerChainReader{db: db}, headers[j-1].Number.Uint64(), headers[j-1].Hash(), headers, nil, uint64(tt.lcrs))
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

					currentHeaderExtra.LoopStartTime = currentHeaderExtra.LoopStartTime + tt.period*tt.maxSignerCount
				} else {

				}
			}

			currentHeaderExtra.CurrentBlockVotes = currentBlockVotes
			currentHeaderExtra.ModifyPredecessorVotes = modifyPredecessorVotes
			currentHeaderExtra.CurrentBlockProposals = currentBlockProposals
			currentHeaderExtra.CurrentBlockDeclares = currentBlockDeclares
			currentHeaderExtraEnc, err := rlp.EncodeToBytes(currentHeaderExtra)
			if err != nil {
				t.Errorf("test %d: failed to rlp encode to bytes: %v", i, err)
				continue
			}
			// Create the genesis block with the initial set of signers
			ExtraData := make([]byte, extraVanity+len(currentHeaderExtraEnc)+extraSeal)
			copy(ExtraData[extraVanity:], currentHeaderExtraEnc)

			headers[j] = &types.Header{
				Number:   big.NewInt(int64(j) + 1),
				Time:     big.NewInt((int64(j)+1)*int64(defaultBlockPeriod) - 1),
				Coinbase: signer,
				Extra:    ExtraData,
			}
			if j > 0 {
				headers[j].ParentHash = headers[j-1].Hash()
			}
			accounts.sign(headers[j], accounts.name(signer))

			// Pass all the headers through alien and ensure tallying succeeds
			snap, err = alien.snapshot(&testerChainReader{db: db}, headers[j].Number.Uint64(), headers[j].Hash(), headers[:j+1], genesisVotes, uint64(tt.lcrs))
			genesisVotes = []*Vote{}
			if err != nil {
				t.Errorf("test %d: failed to create voting snapshot: %v", i, err)
				continue
			}

			// 构造historyHash
			for _, string := range tt.historyHashes {

				var hash common.Hash
				hash.SetString(string)
				snap.HistoryHash = append(snap.HistoryHash[1:len(snap.HistoryHash)], hash)
				//snap.HistoryHash = append(snap.HistoryHash, hash)
				snap.Hash = hash
			}

			//reward
			accounts.accumulateRewards(chainCfg, headers[j], snap, stateDB)
		}

		// verify the result in test case
		head := headers[len(headers)-1]
		snap, err := alien.snapshot(&testerChainReader{db: db}, head.Number.Uint64(), head.Hash(), headers, nil, uint64(tt.lcrs))
		//
		if err != nil {
			t.Errorf("test %d: failed to create voting snapshot: %v", i, err)
			continue
		}

		//cct
		for _, name := range tt.addrNames {

			fmt.Printf("ccc name:%s for addr:%s\n", name, accounts.address(name).Hex())
		}

		// check signers ???
		if len(tt.result.Signers) > 0 {

			signers := map[common.Address]int{}
			// snap里的signer数量
			for _, signer := range snap.Signers {
				signers[*signer] = 1
				fmt.Printf("ccc signer in snap:%s\n", signer.Hex())

			}

			//测试结果的signers队列与snap的一一对应
			for _, signer := range tt.result.Signers {
				signers[accounts.address(signer)] += 2
				fmt.Printf("ccc result signer:%s\n", accounts.address(signer).Hex())
			}


			for signer, cnt := range signers {

				fmt.Printf("ccc cnt:%d for signer::%s\n", cnt, signer.Hex())
			}
			// 检测测试结果的signers队列与snap的是否一致
			for address, cnt := range signers {
				if cnt != 3 {
					t.Errorf("test %d: signer %v address: %v not in result signers %d", i, accounts.name(address), address.Hex(), cnt)
					continue
				}
			}
		} else {
			// check signers official 21 node
			firstLevel := map[common.Address]int{}
			secondLevel := map[common.Address]int{}
			thirdLevel := map[common.Address]int{}
			otherLevel := map[common.Address]int{}

			for signer, tally := range tt.result.Tally {
				switch tally {
				case 5000:
					firstLevel[accounts.address(signer)] = 0
				case 4000:
					secondLevel[accounts.address(signer)] = 0
				case 3000:
					thirdLevel[accounts.address(signer)] = 0
				case 2000:
					otherLevel[accounts.address(signer)] = 0

				}

			}
			var l1, l2, l3, l4 int
			for _, signer := range snap.Signers {
				if _, ok := firstLevel[*signer]; ok {
					l1 += 1
					continue
				}
				if _, ok := secondLevel[*signer]; ok {
					l2 += 1
					continue
				}
				if _, ok := thirdLevel[*signer]; ok {
					l3 += 1
					continue
				}
				if _, ok := otherLevel[*signer]; ok {
					l4 += 1
				}
			}
			if l1 != 10 || l2 != 6 || l3 != 4 || l4 != 1 {
				t.Errorf("test %d: signer not select right count from different level l1 = %d, l2 = %d, l3 = %d, l4 = %d", i, l1, l2, l3, l4)
			}

		}

		// check tally
		if len(tt.result.Tally) != len(snap.Tally) {
			t.Errorf("test %d: tally length result %d, snap %d dismatch", i, len(tt.result.Tally), len(snap.Tally))
		}
		for name, tally := range tt.result.Tally {
			if big.NewInt(int64(tally)).Cmp(snap.Tally[accounts.address(name)]) != 0 {
				t.Errorf("test %d: tally %v address: %v, tally:%v ,result: %v", i, name, accounts.address(name), snap.Tally[accounts.address(name)], big.NewInt(int64(tally)))
				continue
			}
		}
		// check voters
		if len(tt.result.Voters) != len(snap.Voters) {
			t.Errorf("test %d: voter length result %d, snap %d dismatch", i, len(tt.result.Voters), len(snap.Voters))
		}
		for name, number := range tt.result.Voters {
			if snap.Voters[accounts.address(name)].Cmp(big.NewInt(int64(number))) != 0 {
				t.Errorf("test %d: voter %v address: %v, number:%v ,result: %v", i, name, accounts.address(name).Hex(), snap.Voters[accounts.address(name)], big.NewInt(int64(number)))
				continue
			}
		}
		// check votes

		if len(tt.result.Votes) != len(snap.Votes) {
			t.Errorf("test %d: votes length result %d, snap %d dismatch", i, len(tt.result.Votes), len(snap.Votes))
		}
		for name, vote := range tt.result.Votes {
			snapVote, ok := snap.Votes[accounts.address(name)]
			if !ok {
				t.Errorf("test %d: votes %v address: %v can not found", i, name, accounts.address(name))

			}
			if snapVote.Voter != accounts.address(vote.voter) {
				t.Errorf("test %d: votes voter dismatch %v address: %v  , show in snap is %v address: %v", i, vote.voter, accounts.address(vote.voter), accounts.name(snapVote.Voter), snapVote.Voter)
			}
			if snapVote.Candidate != accounts.address(vote.candidate) {
				t.Errorf("test %d: votes candidate dismatch %v address: %v , show in snap is %v address: %v ", i, vote.candidate, accounts.address(vote.candidate), accounts.name(snapVote.Candidate), snapVote.Candidate)
			}
			if snapVote.Stake.Cmp(big.NewInt(int64(vote.stake))) != 0 {
				t.Errorf("test %d: votes stake dismatch %v ,show in snap is %v ", i, vote.stake, snapVote.Stake)
			}
		}

		//check balance  by chaors 20181211
		//stake := state.GetBalance(voter)
		balance := stateDB.GetBalance(accounts.address("A"))
		fmt.Println("check balance", balance.Uint64())
		//fmt.Println("check balance", balance.Div(balance, new(big.Int).SetUint64(1e+18)))

		for _, name := range tt.addrNames {
			if stateDB.GetBalance(accounts.address(name)).Cmp(tt.testBalances[name]) != 0{
				t.Errorf("balance%d tset fail:%s balance:%v in BLC dismatch %v in test result ", i, name, stateDB.GetBalance(accounts.address(name)), tt.testBalances[name])
			}
		}
	}
}
