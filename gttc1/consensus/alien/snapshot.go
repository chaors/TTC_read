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
	"encoding/json"
	"errors"
	"fmt"
	"github.com/TTCECO/gttc/log"
	"github.com/hashicorp/golang-lru"
	"math/big"
	"sort"
	"time"

	"github.com/TTCECO/gttc/common"
	"github.com/TTCECO/gttc/core/types"
	"github.com/TTCECO/gttc/ethdb"
	"github.com/TTCECO/gttc/params"
	"github.com/TTCECO/gttc/rlp"
)

const (
	defaultFullCredit               = 1000 // no punished
	missingPublishCredit            = 100  // punished for missing one block seal
	signRewardCredit                = 10   // seal one block
	autoRewardCredit                = 1    // credit auto recover for each block
	minCalSignerQueueCredit         = 300  // when calculate the signerQueue
	defaultOfficialMaxSignerCount   = 21   // official max signer count
	defaultOfficialFirstLevelCount  = 10   // official first level , 100% in signer queue
	defaultOfficialSecondLevelCount = 20   // official second level, 60% in signer queue
	defaultOfficialThirdLevelCount  = 30   // official third level, 40% in signer queue
	// the credit of one signer is at least minCalSignerQueueCredit
	candidateStateNormal = 1
	candidateMaxLen      = 500 // if candidateNeedPD is false and candidate is more than candidateMaxLen, then minimum tickets candidates will be remove in each LCRS*loop
)

var (
	errIncorrectTallyCount = errors.New("incorrect tally count")
)

// Snapshot is the state of the authorization voting at a given point in time.
type Snapshot struct {
	config   *params.AlienConfig // Consensus engine parameters to fine tune behavior
	sigcache *lru.ARCCache       // Cache of recent block signatures to speed up ecrecover
	LCRS     uint64              // Loop count to recreate signers from top tally

	//出一个块要用的时间
	Period          uint64                       `json:"period"`          // Period of seal each block
	Number          uint64                       `json:"number"`          // Block number where the snapshot was created
	ConfirmedNumber uint64                       `json:"confirmedNumber"` // Block number confirmed when the snapshot was created
	Hash            common.Hash                  `json:"hash"`            // Block hash where the snapshot was created
	HistoryHash     []common.Hash                `json:"historyHash"`     // Block hash list for two recent loop
	Signers         []*common.Address            `json:"signers"`         // Signers queue in current header
	Votes           map[common.Address]*Vote     `json:"votes"`           // All validate votes from genesis block
	Tally           map[common.Address]*big.Int  `json:"tally"`           // Stake for each candidate address
	Voters          map[common.Address]*big.Int  `json:"voters"`          // Block number for each voter address
	Candidates      map[common.Address]uint64    `json:"candidates"`      // Candidates for Signers (0- adding procedure 1- normal 2- removing procedure)
	Punished        map[common.Address]uint64    `json:"punished"`        // The signer be punished count cause of missing seal
	Confirmations   map[uint64][]*common.Address `json:"confirms"`        // The signer confirm given block number
	Proposals       map[common.Hash]*Proposal    `json:"proposals"`       // The Proposals going or success (failed proposal will be removed)
	HeaderTime      uint64                       `json:"headerTime"`      // Time of the current header
	LoopStartTime   uint64                       `json:"loopStartTime"`   // Start Time of the current loop
}

// newSnapshot creates a new snapshot with the specified startup parameters. only ever use if for
// the genesis block.
func newSnapshot(config *params.AlienConfig, sigcache *lru.ARCCache, hash common.Hash, votes []*Vote, lcrs uint64) *Snapshot {

	snap := &Snapshot{
		config:          config,
		sigcache:        sigcache,
		LCRS:            lcrs,
		Period:          config.Period,
		Number:          0,
		ConfirmedNumber: 0,
		Hash:            hash,
		HistoryHash:     []common.Hash{},
		Signers:         []*common.Address{},
		Votes:           make(map[common.Address]*Vote),
		Tally:           make(map[common.Address]*big.Int),
		Voters:          make(map[common.Address]*big.Int),
		Punished:        make(map[common.Address]uint64),
		Candidates:      make(map[common.Address]uint64),
		Confirmations:   make(map[uint64][]*common.Address),
		Proposals:       make(map[common.Hash]*Proposal),
		HeaderTime:      uint64(time.Now().Unix()) - 1,
		LoopStartTime:   config.GenesisTimestamp,
	}
	snap.HistoryHash = append(snap.HistoryHash, hash)

	// 统计票数
	for _, vote := range votes {
		// init Votes from each vote
		snap.Votes[vote.Voter] = vote
		// init Tally
		_, ok := snap.Tally[vote.Candidate]
		if !ok {
			snap.Tally[vote.Candidate] = big.NewInt(0)
		}
		snap.Tally[vote.Candidate].Add(snap.Tally[vote.Candidate], vote.Stake)
		// init Voters
		snap.Voters[vote.Voter] = big.NewInt(0) // block number is 0 , vote in genesis block
		// init Candidates
		snap.Candidates[vote.Voter] = candidateStateNormal
	}

	for i := 0; i < int(config.MaxSignerCount); i++ {
		snap.Signers = append(snap.Signers, &config.SelfVoteSigners[i%len(config.SelfVoteSigners)])
	}

	return snap
}

// loadSnapshot loads an existing snapshot from the database.
func loadSnapshot(config *params.AlienConfig, sigcache *lru.ARCCache, db ethdb.Database, hash common.Hash) (*Snapshot, error) {
	blob, err := db.Get(append([]byte("alien-"), hash[:]...))
	if err != nil {
		return nil, err
	}
	snap := new(Snapshot)
	if err := json.Unmarshal(blob, snap); err != nil {
		return nil, err
	}
	snap.config = config
	snap.sigcache = sigcache
	return snap, nil
}

// store inserts the snapshot into the database.
func (s *Snapshot) store(db ethdb.Database) error {
	blob, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return db.Put(append([]byte("alien-"), s.Hash[:]...), blob)
}

// copy creates a deep copy of the snapshot, though not the individual votes.
func (s *Snapshot) copy() *Snapshot {
	cpy := &Snapshot{
		config:          s.config,
		sigcache:        s.sigcache,
		LCRS:            s.LCRS,
		Period:          s.Period,
		Number:          s.Number,
		ConfirmedNumber: s.ConfirmedNumber,
		Hash:            s.Hash,
		HistoryHash:     make([]common.Hash, len(s.HistoryHash)),

		Signers:       make([]*common.Address, len(s.Signers)),
		Votes:         make(map[common.Address]*Vote),
		Tally:         make(map[common.Address]*big.Int),
		Voters:        make(map[common.Address]*big.Int),
		Candidates:    make(map[common.Address]uint64),
		Punished:      make(map[common.Address]uint64),
		Proposals:     make(map[common.Hash]*Proposal),
		Confirmations: make(map[uint64][]*common.Address),

		HeaderTime:    s.HeaderTime,
		LoopStartTime: s.LoopStartTime,
	}
	copy(cpy.HistoryHash, s.HistoryHash)
	copy(cpy.Signers, s.Signers)
	for voter, vote := range s.Votes {
		cpy.Votes[voter] = &Vote{
			Voter:     vote.Voter,
			Candidate: vote.Candidate,
			Stake:     new(big.Int).Set(vote.Stake),
		}
	}
	for candidate, tally := range s.Tally {
		cpy.Tally[candidate] = new(big.Int).Set(tally)
	}
	for voter, number := range s.Voters {
		cpy.Voters[voter] = new(big.Int).Set(number)
	}
	for candidate, state := range s.Candidates {
		cpy.Candidates[candidate] = state
	}
	for signer, cnt := range s.Punished {
		cpy.Punished[signer] = cnt
	}
	for blockNumber, confirmers := range s.Confirmations {
		cpy.Confirmations[blockNumber] = make([]*common.Address, len(confirmers))
		copy(cpy.Confirmations[blockNumber], confirmers)
	}
	for txHash, proposal := range s.Proposals {
		cpy.Proposals[txHash] = proposal.copy()
	}

	return cpy
}

// apply creates a new authorization snapshot by applying the given headers to
// the original one.
func (s *Snapshot) apply(headers []*types.Header) (*Snapshot, error) {

	//chaorstest
	fmt.Printf("ccc snap apply startting...\n")

	// Allow passing in no headers for cleaner code
	if len(headers) == 0 {
		return s, nil
	}
	// Sanity check that the headers can be applied
	for i := 0; i < len(headers)-1; i++ {
		if headers[i+1].Number.Uint64() != headers[i].Number.Uint64()+1 {
			return nil, errInvalidVotingChain
		}
	}
	if headers[0].Number.Uint64() != s.Number+1 {
		return nil, errInvalidVotingChain
	}
	// Iterate through the headers and create a new snapshot
	snap := s.copy()

	for _, header := range headers {
		// Resolve the authorization key and check against signers
		coinbase, err := ecrecover(header, s.sigcache)
		if err != nil {
			return nil, err
		}
		if coinbase.Str() != header.Coinbase.Str() {
			return nil, errUnauthorized
		}

		headerExtra := HeaderExtra{}
		err = rlp.DecodeBytes(header.Extra[extraVanity:len(header.Extra)-extraSeal], &headerExtra)
		if err != nil {
			return nil, err
		}
		snap.HeaderTime = header.Time.Uint64()
		snap.LoopStartTime = headerExtra.LoopStartTime
		snap.Signers = nil
		for i := range headerExtra.SignerQueue {
			snap.Signers = append(snap.Signers, &headerExtra.SignerQueue[i])
		}

		snap.ConfirmedNumber = headerExtra.ConfirmedBlockNumber

		// HistoryHash 维持个数不超过MaxSignerCount*2
		// 这里为什么要两轮？？？
		if len(snap.HistoryHash) >= int(s.config.MaxSignerCount)*2 {
			snap.HistoryHash = snap.HistoryHash[1 : int(s.config.MaxSignerCount)*2]
		}
		snap.HistoryHash = append(snap.HistoryHash, header.Hash())

		/*chaorstest
		fmt.Printf("ccc current HistoryHash:\n")
		for _, hash := range snap.HistoryHash {

			fmt.Println(hash.Hex())
		}
		fmt.Printf("ccc current Signers:\n")
		for _, signer := range snap.Signers {

			fmt.Println(signer.Hex())
		}
		chaorstest*/



		// deal the new confirmation in this block
		snap.updateSnapshotByConfirmations(headerExtra.CurrentBlockConfirmations)

		// deal the new vote from voter
		snap.updateSnapshotByVotes(headerExtra.CurrentBlockVotes, header.Number)

		// deal the voter which balance modified
		snap.updateSnapshotByMPVotes(headerExtra.ModifyPredecessorVotes)

		// deal the snap related with punished
		snap.updateSnapshotForPunish(headerExtra.SignerMissing, header.Number, header.Coinbase)

		// deal proposals
		snap.updateSnapshotByProposals(headerExtra.CurrentBlockProposals, header.Number)

		// deal declares
		snap.updateSnapshotByDeclares(headerExtra.CurrentBlockDeclares, header.Number)

		// calculate proposal result

		//chaorstest
		fmt.Printf("calProposal will start...")
		snap.calculateProposalResult(header.Number)

		// check the len of candidate if not candidateNeedPD
		if !candidateNeedPD && (snap.Number+1)%(snap.config.MaxSignerCount*snap.LCRS) == 0 && len(snap.Candidates) > candidateMaxLen {
			snap.removeExtraCandidate()
		}

	}
	snap.Number += uint64(len(headers))
	snap.Hash = headers[len(headers)-1].Hash()

	snap.updateSnapshotForExpired()
	err := snap.verifyTallyCnt()

	if err != nil {
		return nil, err
	}
	return snap, nil
}

func (s *Snapshot) removeExtraCandidate() {
	// remove minimum tickets tally beyond candidateMaxLen
	tallySlice := s.buildTallySlice()
	sort.Sort(TallySlice(tallySlice))
	if len(tallySlice) > candidateMaxLen {
		removeNeedTally := tallySlice[candidateMaxLen:]
		for _, tallySlice := range removeNeedTally {
			delete(s.Candidates, tallySlice.addr)
		}
	}
}

func (s *Snapshot) verifyTallyCnt() error {

	tallyTarget := make(map[common.Address]*big.Int)
	for _, v := range s.Votes {
		if _, ok := tallyTarget[v.Candidate]; ok {
			tallyTarget[v.Candidate].Add(tallyTarget[v.Candidate], v.Stake)
		} else {
			tallyTarget[v.Candidate] = new(big.Int).Set(v.Stake)
		}
	}

	for address, tally := range s.Tally {
		if targetTally, ok := tallyTarget[address]; ok && targetTally.Cmp(tally) == 0 {
			continue
		} else {
			return errIncorrectTallyCount
		}
	}

	return nil
}

func (s *Snapshot) updateSnapshotByDeclares(declares []Declare, headerNumber *big.Int) {

	//chaorstest
	fmt.Println("ccc update declares:", declares)

	for _, declare := range declares {
		if proposal, ok := s.Proposals[declare.ProposalHash]; ok {
			// check the proposal enable status and valid block number
			if proposal.ReceivedNumber.Uint64()+proposal.ValidationLoopCnt*s.config.MaxSignerCount < headerNumber.Uint64() || !s.isCandidate(declare.Declarer) {
				continue
			}
			// check if this signer already declare on this proposal
			alreadyDeclare := false
			for _, v := range proposal.Declares {
				if v.Declarer.Str() == declare.Declarer.Str() {
					// this declarer already declare for this proposal
					alreadyDeclare = true
					break
				}
			}
			if alreadyDeclare {
				continue
			}
			// add declare to proposal
			s.Proposals[declare.ProposalHash].Declares = append(s.Proposals[declare.ProposalHash].Declares,
				&Declare{declare.ProposalHash, declare.Declarer, declare.Decision})

		}
	}
}

func (s *Snapshot) calculateProposalResult(headerNumber *big.Int) {

	//chaorstest
	fmt.Printf("ccc calProposal startting...\n")
	for hashKey, proposal := range s.Proposals {
		// the result will be calculate at receiverdNumber + vlcnt + 1
		//chaorstest
		fmt.Printf("ReceivedNumber:%d ValidationLoopCnt:%d----%d  headerNumber:%d\n", proposal.ReceivedNumber.Uint64(), proposal.ValidationLoopCnt, s.config.MaxSignerCount, headerNumber.Uint64())

		if proposal.ReceivedNumber.Uint64()+proposal.ValidationLoopCnt*s.config.MaxSignerCount+1 == headerNumber.Uint64() {
			// calculate the current stake of this proposal
			judegmentStake := big.NewInt(0)
			for _, tally := range s.Tally {
				judegmentStake.Add(judegmentStake, tally)
			}
			judegmentStake.Mul(judegmentStake, big.NewInt(2))
			judegmentStake.Div(judegmentStake, big.NewInt(3))
			// calculate declare stake
			yesDeclareStake := big.NewInt(0)
			for _, declare := range proposal.Declares {
				if declare.Decision {
					if _, ok := s.Tally[declare.Declarer]; ok {
						yesDeclareStake.Add(yesDeclareStake, s.Tally[declare.Declarer])
					}
				}
			}
			//chaorstest
			fmt.Printf("yesDeclareStake:%d judegmentStake:%d  result:%d\n", yesDeclareStake, judegmentStake, yesDeclareStake.Cmp(judegmentStake))
			if yesDeclareStake.Cmp(judegmentStake) > 0 {
				// process add candidate
				switch proposal.ProposalType {
				case proposalTypeCandidateAdd:
					//chaorstest
					fmt.Printf("ccc proposalType add...\n")
					if candidateNeedPD {
						fmt.Printf("ccc candidate added...\n")
						s.Candidates[s.Proposals[hashKey].Candidate] = candidateStateNormal
					}else {
						fmt.Printf("ccc not add:%t...\n", candidateNeedPD)
					}
				case proposalTypeCandidateRemove:
					if _, ok := s.Candidates[proposal.Candidate]; ok && candidateNeedPD {
						delete(s.Candidates, proposal.Candidate)
					}
				case proposalTypeMinerRewardDistributionModify:
					minerRewardPerThousand = s.Proposals[hashKey].MinerRewardPerThousand

				}
			}

		}

	}

}

func (s *Snapshot) updateSnapshotByProposals(proposals []Proposal, headerNumber *big.Int) {
	for _, proposal := range proposals {
		proposal.ReceivedNumber = new(big.Int).Set(headerNumber)
		s.Proposals[proposal.Hash] = &proposal
	}
}

func (s *Snapshot) updateSnapshotForExpired() {

	// deal the expired vote
	// 每到一个新的epoch，上一个epoch产生的投票作废
	var expiredVotes []*Vote
	for voterAddress, voteNumber := range s.Voters {

		// chaorstest
		//fmt.Printf("ccc updateSnapshotForExpired number:%d----voteNumber:%d---Epoch:%d\n", s.Number, voteNumber.Uint64(), s.config.Epoch)
		if s.Number-voteNumber.Uint64() > s.config.Epoch {
			// clear the vote
			fmt.Printf("ccc Expired count...")
			if expiredVote, ok := s.Votes[voterAddress]; ok {
				expiredVotes = append(expiredVotes, expiredVote)
			}
		}
	}
	// remove expiredVotes only enough voters left
	//fmt.Printf("ccc expiredVotes VotersCount:%d----expiredVotesCount:%d---maxSig:%d\n", len(s.Voters), len(expiredVotes), s.config.MaxSignerCount)
	// 过期投票的处理原则：并不是过期票就作废，过期票的处理是为了当投票数超过最大签名者数的时候，在投票中能优先选出
	// 票数最多投票最近的那些被投的那些候选者作为签名者  eg：5个投票其中3个过期，最大签名者为4，这个时候过期的投票并不会立即更新其候选者的
	// 票数(因为如果这样就可能只剩2个候选人签名，但一个轮次还是出4个块)  即当有效投票数大于等于最大签名者数量时才会去执行过期的操作
	if uint64(len(s.Voters)-len(expiredVotes)) >= s.config.MaxSignerCount {
		fmt.Printf("ccc Expired...")
		for _, expiredVote := range expiredVotes {
			s.Tally[expiredVote.Candidate].Sub(s.Tally[expiredVote.Candidate], expiredVote.Stake)
			if s.Tally[expiredVote.Candidate].Cmp(big.NewInt(0)) == 0 {
				delete(s.Tally, expiredVote.Candidate)
				fmt.Printf("ccc delete candidate:%s from voter:%s \n", expiredVote.Candidate.Hex(), expiredVote.Voter.Hex())
			}
			//
			fmt.Printf("ccc delete vote:%s \n", expiredVote.Voter.Hex())

			delete(s.Votes, expiredVote.Voter)
			delete(s.Voters, expiredVote.Voter)
		}
	}

	// deal the expired confirmation
	for blockNumber := range s.Confirmations {

		fmt.Printf("ccc expiredconfimation snapNumber:%d----blockNumber:%d---maxSig:%d\n", s.Number, blockNumber, s.config.MaxSignerCount)
		if s.Number-blockNumber > s.config.MaxSignerCount {
			delete(s.Confirmations, blockNumber)
		}
	}

	// remove 0 stake tally
	for address, tally := range s.Tally {
		if tally.Cmp(big.NewInt(0)) <= 0 {
			delete(s.Tally, address)
		}
	}
}

func (s *Snapshot) updateSnapshotByConfirmations(confirmations []Confirmation) {

	//chaorstest
	fmt.Printf("ccc updateSnapshotByConfirmations...")
	for _, confirmation := range confirmations {
		_, ok := s.Confirmations[confirmation.BlockNumber.Uint64()]
		if !ok {
			s.Confirmations[confirmation.BlockNumber.Uint64()] = []*common.Address{}
		}
		addConfirmation := true
		for _, address := range s.Confirmations[confirmation.BlockNumber.Uint64()] {
			if confirmation.Signer.Str() == address.Str() {
				addConfirmation = false
				break
			}
		}
		if addConfirmation == true {
			// chaorstest
			fmt.Println("cccconfirms before add:")
			for number, confirmAddrs := range s.Confirmations {
				for _, addr := range confirmAddrs {

					fmt.Println(number,addr.Hex())
				}
			}
			var confirmSigner common.Address
			confirmSigner.Set(confirmation.Signer)
			s.Confirmations[confirmation.BlockNumber.Uint64()] = append(s.Confirmations[confirmation.BlockNumber.Uint64()], &confirmSigner)

			// chaorstest
			fmt.Println("cccconfirms after add:")
			for number, confirmAddrs := range s.Confirmations {
				for _, addr := range confirmAddrs {

					fmt.Println(number, addr.Hex())
				}
			}
		}
	}
}

func (s *Snapshot) updateSnapshotByVotes(votes []Vote, headerNumber *big.Int) {
	log.Info("ccc updateSnapshot\n", "number", headerNumber)

	for _, vote := range votes {
		// update Votes, Tally, Voters data

		log.Info("ccc updateSnapshotByVotes\n", "number", headerNumber)

		if lastVote, ok := s.Votes[vote.Voter]; ok {
			s.Tally[lastVote.Candidate].Sub(s.Tally[lastVote.Candidate], lastVote.Stake)
		}
		if _, ok := s.Tally[vote.Candidate]; ok {

			log.Info("ccc addstake\n", "number", vote.Stake)
			s.Tally[vote.Candidate].Add(s.Tally[vote.Candidate], vote.Stake)
		} else {
			s.Tally[vote.Candidate] = vote.Stake
			if !candidateNeedPD {
				log.Info("ccc candidateStateNormal\n", "number", vote.Stake)
				s.Candidates[vote.Candidate] = candidateStateNormal
			}
		}

		s.Votes[vote.Voter] = &Vote{vote.Voter, vote.Candidate, vote.Stake}
		s.Voters[vote.Voter] = headerNumber
	}
}

//这个是指一个地址投票后，当其余额发生变化会引起投票的stake变化
func (s *Snapshot) updateSnapshotByMPVotes(votes []Vote) {

	//chaorstest
	fmt.Printf("updateSnapshotByMPVotes...\n")

	for _, txVote := range votes {

		if lastVote, ok := s.Votes[txVote.Voter]; ok {
			s.Tally[lastVote.Candidate].Sub(s.Tally[lastVote.Candidate], lastVote.Stake)
			s.Tally[lastVote.Candidate].Add(s.Tally[lastVote.Candidate], txVote.Stake)
			s.Votes[txVote.Voter] = &Vote{Voter: txVote.Voter, Candidate: lastVote.Candidate, Stake: txVote.Stake}
			// do not modify header number of snap.Voters
		}
	}
}

func (s *Snapshot) updateSnapshotForPunish(signerMissing []common.Address, headerNumber *big.Int, coinbase common.Address) {
	// set punished count to half of origin in Epoch
	/*
		if headerNumber.Uint64()%s.config.Epoch == 0 {
			for bePublished := range s.Punished {
				if count := s.Punished[bePublished] / 2; count > 0 {
					s.Punished[bePublished] = count
				} else {
					delete(s.Punished, bePublished)
				}
			}
		}
	*/
	// punish the missing signer
	// chaorstest
	fmt.Println("ccc updateSnapshotForPunish", signerMissing)

	for _, signerMissing := range signerMissing {
		fmt.Println("ccc signerPunished before", signerMissing.Hex(), s.Punished[signerMissing])
		if _, ok := s.Punished[signerMissing]; ok {
			s.Punished[signerMissing] += missingPublishCredit
		} else {
			s.Punished[signerMissing] = missingPublishCredit
		}
		fmt.Println("ccc signerPunished after", signerMissing.Hex(), s.Punished[signerMissing])
}
	// reduce the punish of sign signer
	if _, ok := s.Punished[coinbase]; ok {

		if s.Punished[coinbase] > signRewardCredit {
			s.Punished[coinbase] -= signRewardCredit
		} else {
			delete(s.Punished, coinbase)
		}
	}
	// reduce the punish for all punished
	for signerEach := range s.Punished {
		if s.Punished[signerEach] > autoRewardCredit {
			s.Punished[signerEach] -= autoRewardCredit
		} else {
			delete(s.Punished, signerEach)
		}
	}
}

// inturn returns if a signer at a given block height is in-turn or not.
func (s *Snapshot) inturn(signer common.Address, headerTime uint64) bool {

	// if all node stop more than period of one loop
	loopIndex := int((headerTime-s.LoopStartTime)/s.config.Period) % len(s.Signers)
	if loopIndex >= len(s.Signers) {
		return false
	} else if *s.Signers[loopIndex] != signer {
		return false

	}
	return true
}

// check if address belong to voter
func (s *Snapshot) isVoter(address common.Address) bool {
	if _, ok := s.Voters[address]; ok {
		return true
	}
	return false
}

// check if address belong to candidate
func (s *Snapshot) isCandidate(address common.Address) bool {
	if _, ok := s.Candidates[address]; ok {
		return true
	}
	return false
}

// get last block number meet the confirm condition
func (s *Snapshot) getLastConfirmedBlockNumber(confirmations []Confirmation) *big.Int {

	cpyConfirmations := make(map[uint64][]*common.Address)
	for blockNumber, confirmers := range s.Confirmations {
		cpyConfirmations[blockNumber] = make([]*common.Address, len(confirmers))
		copy(cpyConfirmations[blockNumber], confirmers)
	}
	// update confirmation into snapshot
	for _, confirmation := range confirmations {
		_, ok := cpyConfirmations[confirmation.BlockNumber.Uint64()]
		if !ok {
			cpyConfirmations[confirmation.BlockNumber.Uint64()] = []*common.Address{}
		}
		addConfirmation := true
		for _, address := range cpyConfirmations[confirmation.BlockNumber.Uint64()] {
			if confirmation.Signer.Str() == address.Str() {
				addConfirmation = false
				break
			}
		}
		if addConfirmation == true {
			var confirmSigner common.Address
			confirmSigner.Set(confirmation.Signer)
			cpyConfirmations[confirmation.BlockNumber.Uint64()] = append(cpyConfirmations[confirmation.BlockNumber.Uint64()], &confirmSigner)
		}
	}

	i := s.Number
	for ; i > s.Number-s.config.MaxSignerCount*2/3+1; i-- {
		if confirmers, ok := cpyConfirmations[i]; ok {
			if len(confirmers) > int(s.config.MaxSignerCount*2/3) {
				return big.NewInt(int64(i))
			}
		}
	}
	return big.NewInt(int64(i))
}

func (s *Snapshot) calculateReward(coinbase common.Address, votersReward *big.Int) map[common.Address]*big.Int {

	rewards := make(map[common.Address]*big.Int)
	allStake := big.NewInt(0)
	for voter, vote := range s.Votes {
		if vote.Candidate.Str() == coinbase.Str() {
			allStake.Add(allStake, vote.Stake)
			rewards[voter] = new(big.Int).Set(vote.Stake)
		}
	}
	for _, stake := range rewards {
		stake.Mul(stake, votersReward)
		stake.Div(stake, allStake)
	}
	return rewards
}
