package network

import (
	"github.com/bigpicturelabs/consensusPBFT/pbft/consensus"
	"encoding/json"
	"fmt"
	"time"
	//"errors"
	"context"
	"sync"
	"sync/atomic"
	"crypto/ecdsa"
)

type Node struct {
	MyInfo          *NodeInfo
	PrivKey         *ecdsa.PrivateKey
	NodeTable       []*NodeInfo
	View            *View
	States          map[int64]consensus.PBFT // key: sequenceID, value: state
	VCStates		map[int64]*consensus.VCState
	CommittedMsgs   []*consensus.RequestMsg // kinda block.
	TotalConsensus  int64 // atomic. number of consensus started so far.
	IsViewChanging  bool

	// Channels
	MsgEntrance   chan interface{}
	MsgDelivery   chan interface{}
	MsgExecution  chan *MsgPair
	MsgOutbound   chan *MsgOut
	MsgError      chan []error
	ViewMsgEntrance chan interface{}

	// Mutexes for preventing from concurrent access
	StatesMutex sync.RWMutex
	VCStatesMutex sync.RWMutex

	// Saved checkpoint messages on this node
	// key: sequenceID, value: map(key: nodeID, value: checkpointMsg)
	CheckPointMutex     sync.RWMutex
	CheckPointMsgsLog   map[int64]map[string]*consensus.CheckPointMsg

	// The stable checkpoint that 2f + 1 nodes agreed
	StableCheckPoint    int64
}

type NodeInfo struct {
	NodeID string `json:"nodeID"`
	Url    string `json:"url"`
	PubKey *ecdsa.PublicKey
}

type View struct {
	ID      int64
	Primary *NodeInfo
}

type MsgPair struct {
	replyMsg     *consensus.ReplyMsg
	committedMsg *consensus.RequestMsg
}

// Outbound message
type MsgOut struct {
	Path string
	Msg  []byte
}

// Number of parallel goroutines for resolving messages.
const NumResolveMsgGo = 6

// Deadline for the consensus state.
const ConsensusDeadline = time.Millisecond * 50

// Cooling time to escape frequent error, or message sending retry.
const CoolingTime = time.Millisecond * 2

// Number of error messages to start cooling.
const CoolingTotalErrMsg = 5

// Number of outbound connection for a node.
const MaxOutboundConnection = 1000

func NewNode(myInfo *NodeInfo, nodeTable []*NodeInfo, viewID int64, decodePrivKey *ecdsa.PrivateKey) *Node {
	node := &Node{
		MyInfo:    myInfo,
		PrivKey: decodePrivKey,
		NodeTable: nodeTable,
		View:      &View{},
		IsViewChanging: false,

		// Consensus-related struct
		States:          make(map[int64]consensus.PBFT),
		CommittedMsgs:   make([]*consensus.RequestMsg, 0),
		VCStates: 		 make(map[int64]*consensus.VCState),

		// Channels
		MsgEntrance: make(chan interface{}, len(nodeTable) * 3),
		MsgDelivery: make(chan interface{}, len(nodeTable) * 3), // TODO: enough?
		MsgExecution: make(chan *MsgPair),
		MsgOutbound: make(chan *MsgOut),
		MsgError: make(chan []error),
		ViewMsgEntrance: make(chan interface{}, len(nodeTable)*3),

		CheckPointMsgsLog: make(map[int64]map[string]*consensus.CheckPointMsg),
		StableCheckPoint:  0,
	}

	atomic.StoreInt64(&node.TotalConsensus, 0)
	node.updateView(viewID)

	// Start message dispatcher
	go node.dispatchMsg()

	for i := 0; i < NumResolveMsgGo; i++ {
		// Start message resolver
		go node.resolveMsg()
	}

	// Start message executor
	go node.executeMsg()

	// Start outbound message sender
	go node.sendMsg()

	// Start message error logger
	go node.logErrorMsg()

	return node
}

// Broadcast marshalled message.
func (node *Node) Broadcast(msg interface{}, path string) {
	jsonMsg, err := json.Marshal(msg)
	if err != nil {
		node.MsgError <- []error{err}
		return
	}

	node.MsgOutbound <- &MsgOut{Path: node.MyInfo.Url + path, Msg: jsonMsg}
}

// When REQUEST message is broadcasted, start consensus.
func (node *Node) GetReq(reqMsg *consensus.RequestMsg) {
	LogMsg(reqMsg)
	// Create a new state object.
	state := node.createState(reqMsg.Timestamp)

	// Increment the number of request message atomically.
	// TODO: Currently, StartConsensus must succeed.
	newTotalConsensus := atomic.AddInt64(&node.TotalConsensus, 1)
	prePrepareMsg := state.StartConsensus(reqMsg, newTotalConsensus)

	// Register state into node and update last sequence number.
	node.StatesMutex.Lock()
	node.States[prePrepareMsg.SequenceID] = state
	node.StatesMutex.Unlock()

	fmt.Printf("Consensus Process (ViewID: %d, SequenceID: %d)\n",
	           prePrepareMsg.ViewID, prePrepareMsg.SequenceID)

	// Broadcast PrePrepare message.
	LogStage("Request", true)
	if node.isMyNodePrimary() {
		node.Broadcast(prePrepareMsg, "/preprepare")
	}
	LogStage("Pre-prepare", false)

	// From TOCS: The backups check the sequence numbers assigned by
	// the primary and use timeouts to detect when it stops.
	// They trigger view changes to select a new primary when it
	// appears that the current one has failed.
	// 
	// Deadline is determined by the timestamp of the current node.
	go node.startTransitionWithDeadline(state, time.Now().UnixNano())
}

func (node *Node) startTransitionWithDeadline(state consensus.PBFT, timeStamp int64) {
	// Set deadline based on the given timestamp.
	sec := timeStamp / int64(time.Second)
	nsec := timeStamp % int64(time.Second)
	d := time.Unix(sec, nsec).Add(ConsensusDeadline)
	ctx, cancel := context.WithDeadline(context.Background(), d)

	// Check the time is skewed.
	timeDiff := time.Until(d).Nanoseconds()
	fmt.Printf("The deadline for sequenceID %d is %d ms. (Skewed %d ms)\n",
	           state.GetSequenceID(),
	           timeDiff / int64(time.Millisecond),
	           (ConsensusDeadline.Nanoseconds() - timeDiff) / int64(time.Millisecond))

	defer cancel()

	// The node can receive messages for any consensus stage,
	// regardless of the current stage for the state.
	ch := state.GetMsgReceiveChannel()

	for {
		select {
		case msgState := <-ch:
			switch msg := msgState.(type) {
			case *consensus.PrePrepareMsg:
				node.GetPrePrepare(state, msg)
			case *consensus.VoteMsg:
				if msg.MsgType == consensus.PrepareMsg {
					node.GetPrepare(state, msg)
				} else if msg.MsgType == consensus.CommitMsg {
					node.GetCommit(state, msg)
				}
			}
		case <-ctx.Done():
			// Check the consensus of the current state precedes
			// that of the last committed message in this node.
			var lastCommittedMsg *consensus.RequestMsg = nil
			msgTotalCnt := len(node.CommittedMsgs)
			if msgTotalCnt > 0 {
				lastCommittedMsg = node.CommittedMsgs[msgTotalCnt - 1]
			}

			if msgTotalCnt == 0 ||
			   lastCommittedMsg.SequenceID < state.GetSequenceID() {
				//startviewchange
				node.IsViewChanging = true
				// Broadcast view change message.
				node.MsgError <- []error{ctx.Err()}
				node.StartViewChange()
			}
			return
		}
	}
}

func (node *Node) GetPrePrepare(state consensus.PBFT, prePrepareMsg *consensus.PrePrepareMsg) {
	// TODO: From TOCS: sequence number n is between a low water mark h
	// and a high water mark H. The last condition is necessary to enable
	// garbage collection and to prevent a faulty primary from exhausting
	// the space of sequence numbers by selecting a very large one.

	prepareMsg, err := state.PrePrepare(prePrepareMsg)
	if err != nil {
		node.MsgError <- []error{err}
	}

	// Check PREPARE message created.
	if prepareMsg == nil {
		return
	}

	// Attach node ID to the message.
	prepareMsg.NodeID = node.MyInfo.NodeID

	LogStage("Pre-prepare", true)
	node.Broadcast(prepareMsg, "/prepare")
	LogStage("Prepare", false)

	// Step next.
	node.GetPrepare(state, prepareMsg)
}

func (node *Node) GetPrepare(state consensus.PBFT, prepareMsg *consensus.VoteMsg) {
	commitMsg, err := state.Prepare(prepareMsg)
	if err != nil {
		node.MsgError <- []error{err}
	}

	// Check COMMIT message created.
	if commitMsg == nil {
		return
	}

	// Attach node ID to the message.
	commitMsg.NodeID = node.MyInfo.NodeID

	LogStage("Prepare", true)
	node.Broadcast(commitMsg, "/commit")
	LogStage("Commit", false)

	// Step next.
	node.GetCommit(state, commitMsg)
}

func (node *Node) GetCommit(state consensus.PBFT, commitMsg *consensus.VoteMsg) {
	replyMsg, committedMsg, err := state.Commit(commitMsg)
	if err != nil {
		node.MsgError <- []error{err}
	}

	// Check REPLY message created.
	if replyMsg == nil {
		return
	}

	// Attach node ID to the message.
	replyMsg.NodeID = node.MyInfo.NodeID

	// Pass the incomplete reply message through MsgExecution
	// channel to run its operation sequentially.
	node.MsgExecution <- &MsgPair{replyMsg, committedMsg}
}

func (node *Node) GetReply(msg *consensus.ReplyMsg) {
	LogMsg(msg)
}

func (node *Node) createState(timeStamp int64) consensus.PBFT {
	// TODO: From TOCS: To guarantee exactly once semantics,
	// replicas discard requests whose timestamp is lower than
	// the timestamp in the last reply they sent to the client.

	return consensus.CreateState(node.View.ID, node.MyInfo.NodeID, len(node.NodeTable))
}

func (node *Node) dispatchMsg() {
	for {
		select {
		case msg := <-node.MsgEntrance:
			if !node.IsViewChanging {
				node.routeMsg(msg)
			}
		case viewmsg := <-node.ViewMsgEntrance:
			node.routeMsg(viewmsg)
		}
	}
}

func (node *Node) routeMsg(msgEntered interface{}) {
	switch msg := msgEntered.(type) {
	case *consensus.RequestMsg:
		node.MsgDelivery <- msg
	case *consensus.PrePrepareMsg:
		// Receive pre-prepare message only if 1. the node is not primary,
		// and 2. stable checkpoint for this node is lower than
		// sequence number of this message.
		if !node.isMyNodePrimary() &&
		   node.StableCheckPoint <= msg.SequenceID {
			node.MsgDelivery <- msg
		}
	case *consensus.VoteMsg:
		// Messages are broadcasted from the node, so
		// the message sent to itself can exist.
		// Skip the message if stable checkpoint for this node is
		// lower than sequence number of this message.
		if node.MyInfo.NodeID != msg.NodeID &&
		   node.StableCheckPoint <= msg.SequenceID {
			node.MsgDelivery <- msg
		}
	case *consensus.ReplyMsg:
		node.MsgDelivery <- msg
	case *consensus.ViewChangeMsg:
		node.MsgDelivery <- msg
	case *consensus.NewViewMsg:
		node.MsgDelivery <- msg
	}
	// Messages are broadcasted from the node, so
	// the message sent to itself can exist.
	switch msg := msgEntered.(type) {
	case *consensus.CheckPointMsg:
		if node.MyInfo.NodeID != msg.NodeID {
			node.MsgDelivery <- msg
		}
	}
}

func (node *Node) resolveMsg() {
	for {
		var state consensus.PBFT
		var err error = nil
		msgDelivered := <-node.MsgDelivery

		// Resolve the message.
		switch msg := msgDelivered.(type) {
		case *consensus.RequestMsg:
			node.GetReq(msg)
		case *consensus.PrePrepareMsg:
			state, err = node.getState(msg.SequenceID)
			if state != nil {
				ch := state.GetMsgSendChannel()
				ch <- msg
			}
		case *consensus.VoteMsg:
			state, err = node.getState(msg.SequenceID)
			if state != nil {
				ch := state.GetMsgSendChannel()
				ch <- msg
			}
		case *consensus.ReplyMsg:
			node.GetReply(msg)
		case *consensus.CheckPointMsg:
			node.GetCheckPoint(msg)
		case *consensus.ViewChangeMsg:
			err = node.GetViewChange(msg)
		case *consensus.NewViewMsg:
			err = node.GetNewView(msg)
		}

		if err != nil {
			// Print error.
			node.MsgError <- []error{err}
			// Send message into dispatcher.
			node.MsgEntrance <- msgDelivered
		}
	}
}

// Fill the result field, after all execution for
// other states which the sequence number is smaller,
// i.e., the sequence number of the last committed message is
// one smaller than the current message.
func (node *Node) executeMsg() {
	var committedMsgs []*consensus.RequestMsg
	pairs := make(map[int64]*MsgPair)

	for {
		msgPair := <-node.MsgExecution
		pairs[msgPair.committedMsg.SequenceID] = msgPair
		committedMsgs = make([]*consensus.RequestMsg, 0)

		// Execute operation for all the consecutive messages.
		for {
			var lastSequenceID int64 = 0

			// Find the last committed message.
			msgTotalCnt := len(node.CommittedMsgs)
			if msgTotalCnt > 0 {
				lastCommittedMsg := node.CommittedMsgs[msgTotalCnt - 1]
				lastSequenceID = lastCommittedMsg.SequenceID
			}

			// Stop execution if the message for the
			// current sequence is not ready to execute.
			p := pairs[lastSequenceID + 1]
			if p == nil {
				break
			}

			// Add the committed message in a private log queue
			// to print the orderly executed messages.
			committedMsgs = append(committedMsgs, p.committedMsg)
			LogStage("Commit", true)

			// TODO: execute appropriate operation.
			p.replyMsg.Result = "Executed"

			// After executing the operation, log the
			// corresponding committed message to node.
			node.CommittedMsgs = append(node.CommittedMsgs, p.committedMsg)

			// Broadcast reply.
			node.Broadcast(p.replyMsg, "/reply")
			LogStage("Reply", true)

			// Create checkpoint every `periodCheckPoint` committed message.
			if (lastSequenceID + 1) % periodCheckPoint == 0 {
				LogStage("CHECKPOINT", false)
				// Send CHECKPOINT message until it is possible.
				for sequenceid := node.StableCheckPoint;
				    sequenceid < lastSequenceID + 1;
				    sequenceid += periodCheckPoint {
					if !node.CheckPointMissCheck(sequenceid) {
						break
					}
					checkPointMsg := node.createCheckPointMsg(sequenceid + periodCheckPoint, node.MyInfo.NodeID)
					node.Broadcast(checkPointMsg, "/checkpoint")
					node.CheckPoint(checkPointMsg)
				}
			}

			delete(pairs, lastSequenceID + 1)
		}

		// Print all committed messages.
		for _, v := range committedMsgs {
			state, _ := node.getState(v.SequenceID) // Must succeed.
			digest := state.GetDigest()
			fmt.Printf("***committedMsgs[%d]: clientID=%s, operation=%s, timestamp=%d, ReqMsg (digest)=%s***\n",
			           v.SequenceID, v.ClientID, v.Operation, v.Timestamp, digest)
		}
	}
}

func (node *Node) sendMsg() {
	sem := make(chan bool, MaxOutboundConnection)

	for {
		msg := <-node.MsgOutbound

		// Goroutine for concurrent broadcast() with timeout
		sem <- true
		go func() {
			defer func() { <-sem }()
			errCh := make(chan error, 1)

			// Goroutine for concurrent broadcast()
			go func() {
				broadcast(errCh, msg.Path, msg.Msg, node.PrivKey)
			}()
			select {
			case err := <-errCh:
				if err != nil {
					node.MsgError <- []error{err}
					// TODO: view change.
				}
			}
		}()
	}
}

func (node *Node) logErrorMsg() {
	coolingMsgLeft := CoolingTotalErrMsg

	for {
		errs := <-node.MsgError
		for _, err := range errs {
			coolingMsgLeft--
			if coolingMsgLeft == 0 {
				fmt.Printf("%d error messages detected! cool down for %d milliseconds\n",
				           CoolingTotalErrMsg, CoolingTime / time.Millisecond)
				time.Sleep(CoolingTime)
				coolingMsgLeft = CoolingTotalErrMsg
			}
			fmt.Println(err)
		}
	}
}

func (node *Node) getState(sequenceID int64) (consensus.PBFT, error) {
	node.StatesMutex.RLock()
	state := node.States[sequenceID]
	node.StatesMutex.RUnlock()

	if state == nil {
		return nil, fmt.Errorf("State for sequence number %d has not created yet.", sequenceID)
	}

	return state, nil
}
