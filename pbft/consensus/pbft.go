package consensus

type PBFT interface {
	StartConsensus(request *RequestMsg, sequenceID int64) *PrePrepareMsg
	PrePrepare(prePrepareMsg *PrePrepareMsg) (*VoteMsg, error)
	Prepare(prepareMsg *VoteMsg) (*VoteMsg, error)
	Commit(commitMsg *VoteMsg) (*ReplyMsg, *RequestMsg, error)

	GetSequenceID() int64
	GetDigest() string
	GetF() int

	GetMsgReceiveChannel() <-chan interface{}
	GetMsgSendChannel() chan<- interface{}

	GetReqMsg() *RequestMsg
	GetPrePrepareMsg() *PrePrepareMsg
	GetPrepareMsgs() map[string]*VoteMsg
	GetCommitMsgs() map[string]*VoteMsg
	GetSuccChkPoint() int64
	SetSuccChkPoint(int64)

	ClearMsgLogs()
}
