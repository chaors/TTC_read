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
	"fmt"
	"github.com/TTCECO/gttc/common"
	"github.com/TTCECO/gttc/consensus"
	"github.com/TTCECO/gttc/core/types"
	"github.com/TTCECO/gttc/rpc"
	"math/big"
)

// API is a user facing RPC API to allow controlling the signer and voting
// mechanisms of the delegated-proof-of-stake scheme.
type API struct {
	chain consensus.ChainReader
	alien *Alien
}

// GetSnapshot retrieves the state snapshot at a given block.
func (api *API) GetSnapshot(number *rpc.BlockNumber) (*Snapshot, error) {
	// Retrieve the requested block number (or current if none requested)
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	// Ensure we have an actually valid block and return its snapshot
	if header == nil {
		return nil, errUnknownBlock
	}
	return api.alien.snapshot(api.chain, header.Number.Uint64(), header.Hash(), nil, nil, defaultLoopCntRecalculateSigners)

}

// GetSnapshotAtHash retrieves the state snapshot at a given block.
func (api *API) GetSnapshotAtHash(hash common.Hash) (*Snapshot, error) {
	header := api.chain.GetHeaderByHash(hash)
	if header == nil {
		return nil, errUnknownBlock
	}
	return api.alien.snapshot(api.chain, header.Number.Uint64(), header.Hash(), nil, nil, defaultLoopCntRecalculateSigners)
}

// GetSnapshotAtNumber retrieves the state snapshot at a given block.
func (api *API) GetSnapshotAtNumber(number uint64) (*Snapshot, error) {
	header := api.chain.GetHeaderByNumber(number)
	if header == nil {
		return nil, errUnknownBlock
	}
	return api.alien.snapshot(api.chain, header.Number.Uint64(), header.Hash(), nil, nil, defaultLoopCntRecalculateSigners)
}

// GetSnapshotByHeaderTime retrieves the state snapshot by timestamp of header.
// snapshot.header.time <= targetTime < snapshot.header.time + period
// todo: add confirm headertime in return snapshot, to minimize the request from side chain
func (api *API) GetSnapshotByHeaderTime(targetTime uint64, scHash common.Hash) (*Snapshot, error) {
	header := api.chain.CurrentHeader()

	// return errUnknownBlock if current is not reach trantorBlock
	if !api.chain.Config().Alien.IsTrantor(header.Number) {
		return nil, errUnknownBlock
	}
	period := new(big.Int).SetUint64(api.chain.Config().Alien.Period)
	target := new(big.Int).SetUint64(targetTime)
	fmt.Printf("ccc GetSnapshotByHeaderTime:%v---%v---%v\n",targetTime, header.Number, new(big.Int).Add(header.Time, period))
	if ceil := new(big.Int).Add(header.Time, period); header == nil || target.Cmp(ceil) > 0 {
		return nil, errUnknownBlock
	}

	minN := new(big.Int).Set(api.chain.Config().Alien.TrantorBlock)
	maxN := new(big.Int).Set(header.Number)
	nextN := new(big.Int).SetInt64(0)
	for {
		if ceil := new(big.Int).Add(header.Time, period); target.Cmp(header.Time) >= 0 && target.Cmp(ceil) < 0 {
			snap, err := api.alien.snapshot(api.chain, header.Number.Uint64(), header.Hash(), nil, nil, defaultLoopCntRecalculateSigners)

			// replace coinbase by signer settings
			var scSigners []*common.Address
			for _, signer := range snap.Signers {
				replaced := false
				if _, ok := snap.SCCoinbase[*signer]; ok {
					if addr, ok := snap.SCCoinbase[*signer][scHash]; ok {
						replaced = true
						scSigners = append(scSigners, &addr)
					}
				}
				if !replaced {
					scSigners = append(scSigners, signer)
				}
			}
			return &Snapshot{LoopStartTime: snap.LoopStartTime, Period: snap.Period, Signers: scSigners}, err
		} else {

			if minNext := new(big.Int).Add(minN, big.NewInt(1)); maxN.Cmp(minN) == 0 || maxN.Cmp(minNext) == 0 {
				break
			}
			// calculate next number
			nextN.Sub(target, header.Time)
			nextN.Div(nextN, period)
			nextN.Add(nextN, header.Number)

			// if nextN beyond the [minN,maxN] then set nextN = (min+max)/2
			if nextN.Cmp(maxN) >= 0 || nextN.Cmp(minN) <= 0 {
				nextN.Add(maxN, minN)
				nextN.Div(nextN, big.NewInt(2))
			}
			// get new header
			header = api.chain.GetHeaderByNumber(nextN.Uint64())
			if header == nil {
				break
			}
			// update maxN & minN
			if header.Time.Cmp(target) >= 0 {
				if header.Number.Cmp(maxN) < 0 {
					maxN.Set(header.Number)
				}
			} else if header.Time.Cmp(target) <= 0 {
				if header.Number.Cmp(minN) > 0 {
					minN.Set(header.Number)
				}
			}

		}
	}
	return nil, errUnknownBlock
}
