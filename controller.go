// Copyright 2013 Jari Takkala and Brian Dignan. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"log"
	"launchpad.net/tomb"
	"sort"
)

type RarityMap struct {
	data map[int][]int
}

func NewRarityMap() *RarityMap {
	r := new(RarityMap)
	r.data = make(map[int][]int)
	return r
}

// Add a new rarity -> pieceNum mapping. 
func (r *RarityMap) put(rarity int, pieceNum int) {
	if _, ok := r.data[rarity]; !ok {
		r.data[rarity] = make([]int, 0)
	}

	r.data[rarity] = append(r.data[rarity], pieceNum)
}

// Flatten out the map into a slice that's sorted by rarity. 
func (r *RarityMap) getPiecesByRarity() []int {
	pieces := make([]int, 0)

	// put all rarity map keys into a temporary unsorted slice
	keys := make([]int, 0)
	for rarity, _ := range r.data {
		keys = append(keys, rarity)
	}

	// sort the slice of keys (rarity) in ascending order
	sort.Ints(keys)

	// Get the map value for each key (starting with the lowest) and 
	// concatenate that slice of pieces (for that rarity) to the result
	for _, rarity := range keys {
		pieceNums := r.data[rarity]
		pieces = append(pieces, pieceNums...)
	}

	return pieces
}

type Controller struct {
	finishedPieces []bool
	pieceHashes []string
	activeRequestsTotals []int 
	peerPieceTotals []int
	peers map[string]PeerInfo
	rxChannels *ControllerRxChannels 
	t tomb.Tomb
}

// Sent from the controller to the peer to request a particular piece
type RequestPiece struct {
	pieceNum int
	expectedHash string
}

// Sent by the peer to the controller when it receives a HAVE message
type HavePiece struct {
	pieceNum int
	peerId string  
	haveMore bool  // set to 1 when the peer is initially breaking a bitfield into individual HAVE messages
}

// Sent from the controller to the peer to cancel an outstanding request
// Also sent from the peer to the controller when it's been choked or 
// when it loses its network connection 
type CancelPiece struct {
	pieceNum int
	peerId string // needed for when the peer sends a cancel to the controller
}

// Sent from IO to the controller indicating that a piece has been 
// received and written to disk
type ReceivedPiece struct {
	pieceNum int
	peerId string
}

type ControllerRxChannels struct {
	receivedPieceCh <-chan ReceivedPiece // Other end is IO 
	newPeerCh <-chan PeerInfo  // Other end is the PeerManager
	cancelPieceCh <-chan CancelPiece  // Other end is Peer. Used when the peer is unable to retrieve a piece
	havePieceCh <-chan HavePiece  // Other end is Peer. used When the peer receives a HAVE message
}

func NewController(finishedPieces []bool, 
					pieceHashes []string, 
					receivedPieceCh chan ReceivedPiece) *Controller {

	rxChannels := new(ControllerRxChannels)
	rxChannels.receivedPieceCh = receivedPieceCh
	rxChannels.cancelPieceCh = make(chan CancelPiece)
	rxChannels.newPeerCh = make(chan PeerInfo)
	rxChannels.havePieceCh = make(chan HavePiece)

	cont := new(Controller)
	cont.finishedPieces = finishedPieces
	cont.pieceHashes = pieceHashes
	cont.rxChannels = rxChannels
	cont.peers = make(map[string]PeerInfo)
	cont.activeRequestsTotals = make([]int, len(finishedPieces))
	return cont
}

func (cont *Controller) Stop() error {
	log.Println("Controller : Stop : Stopping")
	cont.t.Kill(nil)
	return cont.t.Wait()
}

func (cont *Controller) removePieceFromActiveRequests(piece ReceivedPiece) {
	finishingPeer := cont.peers[piece.peerId]
	if _, ok := finishingPeer.activeRequests[piece.pieceNum]; ok {
		// Remove this piece from the peer's activeRequests set
		delete(finishingPeer.activeRequests, piece.pieceNum)

		// Decrement activeRequestsTotals for this piece by one (one less peer is downloading it)
		cont.activeRequestsTotals[piece.pieceNum]--

		// Check every peer to see if they're also downloading this piece.  
		for peerId, peerInfo := range cont.peers {
			if _, ok := peerInfo.activeRequests[piece.pieceNum]; ok {
				// This peer was also working on the same piece
				log.Printf("Controller: removePieceFromActiveRequests : %s was also working on piece %d which is finished. Sending a CANCEL", peerId, piece.pieceNum)
				
				// Remove this piece from the peer's activeRequests set
				delete(peerInfo.activeRequests, piece.pieceNum)
				
				// Decrement activeRequestsTotals for this piece by one (one less peer is downloading it)
				cont.activeRequestsTotals[piece.pieceNum]--

				cancelMessage := new(CancelPiece)
				cancelMessage.pieceNum = piece.pieceNum
				cancelMessage.peerId = peerId

				// Tell this peer to stop downloading this piece because it's already finished. 
				go func() { peerInfo.cancelPieceCh <- *cancelMessage }()
			}
		}


		stuckRequests := cont.activeRequestsTotals[piece.pieceNum]
		if stuckRequests != 0 {
			log.Fatalf("Controller: removePieceFromActiveRequests : Somehow there are %d stuck requests for piece number %d", stuckRequests, piece.pieceNum)
		}

	} else {
		// The peer just finished this piece, but it wasn't in its active request list
		log.Printf("Controller : removePieceFromActiveRequests : %s finished piece %d, but that piece wasn't in its active request list", piece.peerId, piece.pieceNum)
	}
}

func (cont *Controller) createRaritySlice() []int {
	rarityMap := NewRarityMap()

	for pieceNum, total := range cont.peerPieceTotals {
		if cont.finishedPieces[pieceNum] {
			continue
		} 

		rarityMap.put(total, pieceNum)
	}
	return rarityMap.getPiecesByRarity()
}

func (cont *Controller) updateQuantityNeededForAllPeers() {
	for _, peerInfo := range cont.peers {
		qtyPiecesNeeded := 0
		for pieceNum, pieceFinished := range cont.finishedPieces {
			if !pieceFinished && peerInfo.availablePieces[pieceNum] == 1 {
				qtyPiecesNeeded++
			}
		}
		// Overwrite the old value with the new value just computed
		peerInfo.qtyPiecesNeeded = qtyPiecesNeeded
	}
}

/*
func (cont *Controller) recreateDownloadPriorities(raritySlice []int) {
	for _, peerInfo := range cont.peers {
		downloadPriority := make([]int, 0)
		for _, pieceNum := range raritySlice {
			if peerInfo.availablePieces[pieceNum] == 1 {
				downloadPriority = append(downloadPriority, pieceNum)
			}
		}
		peerInfo.downloadPriority = downloadPriority
	}
}*/

/*
func (cont *Controller) sendRequests(peersSortedByDownloadLen []peerInfo) {
	for _, peerInfo := range peersSortedByDownloadLen {

		// Confirm that this peer is still connected and is available to take requests
		// and also that the peer needs more requests
		if peerInfo.isActive && peerInfo.activeRequests < maxSimultaneousDownloadsPerPeer {

			// 

		// Need to keep track of which pieces were already requested to be downloaded by
		// this peer

		// Need to loop through pieces that haven't been asked of anyone else first, 
		// then loop through pieces that have been asked of 1 person, etc. 

		// While the number if active requests is less than the max simultaneous for a single peer,
		// tell the peer to send more requests
			for peerInfo.activeRequests < maxSimultaneousDownloadsPerPeer
				// Track the number of pieces that are requested in this iteration of the loop. If none are
				// requestd,
				piecesRequestCount := 0

		}
	}
}*/

const (
	maxSimultaneousDownloadsPerPeer = 3
)

func (cont *Controller) Run() {
	log.Println("Controller : Run : Started")
	defer cont.t.Done()
	defer log.Println("Controller : Run : Completed")

	for {
		select {
		case piece := <- cont.rxChannels.receivedPieceCh:
			log.Printf("Controller: Run: %s just finished downloading piece number %d", piece.peerId, piece.pieceNum)

			// Update our bitfield to show that we now have that piece
			cont.finishedPieces[piece.pieceNum] = true

			// Remove this piece from the active request list for the peer that 
			// finished the download, along with all other peers who were downloading
			// it. 
			cont.removePieceFromActiveRequests(piece)

			// Create a slice of pieces sorted by rarity
			raritySlice := cont.createRaritySlice()

			// PLACEHOLDER
			log.Println(raritySlice)

			// Given the updated finishedPieces slice, update the quantity of pieces
			// that are needed from each peer. This step is required to later sort 
			// peerInfo slices by the quantity of needed pieces. 
			cont.updateQuantityNeededForAllPeers()

			// Create a PeerInfo slice sorted by qtyPiecesNeeded
			//sortedPeers := sortedPeersByQtyPiecesNeeded(cont.peers)

			// Iterate through the sorted peerInfo slice. For each Peer that isn't 
			// currently requesting the max amount of pieces, send more piece requests. 
			//cont.sendRequests(sortedPeers, raritySlice)


		case piece := <- cont.rxChannels.cancelPieceCh:
			// PLACEHOLDER
			log.Println(piece)

		case peerInfo := <- cont.rxChannels.newPeerCh:
			// PLACEHOLDER
			log.Println(peerInfo)

			// Throw an error if the peer is duplicate (same IP/Port. should never happen)

			// Create a new PeerInfo struct 

			// Add PeerInfo to the peers map using IP:Port as the key

			// 

		case piece := <- cont.rxChannels.havePieceCh:
			// PLACEHOLDER
			log.Println(piece)

		case <- cont.t.Dying():
			return
		}
	}

}