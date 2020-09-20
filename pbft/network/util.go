package network

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"time"

	consensus "consensus"
)

func attachSignatureMsg(msg []byte, privKey *ecdsa.PrivateKey, path string) []byte {
	var sigMgs consensus.SignatureMsg
	r, s, signature, err := consensus.Sign(privKey, msg)
	if err == nil {
		sigMgs = consensus.SignatureMsg{
			Signature:     signature,
			R:             r,
			S:             s,
			MarshalledMsg: msg,
			MsgType:       path,
		}
	}
	sigMgsBytes, _ := json.Marshal(&sigMgs)
	return sigMgsBytes
}
func deattachSignatureMsg(msg []byte, pubkey *ecdsa.PublicKey) (consensus.SignatureMsg,
	error, bool) {
	var sigMgs consensus.SignatureMsg
	err := json.Unmarshal(msg, &sigMgs)
	ok := false
	if err != nil {
		//log.Println("dettachSignature error ", err)
		return sigMgs, err, true
	}
	ok = consensus.Verify(pubkey, sigMgs.R, sigMgs.S, sigMgs.MarshalledMsg)
	return sigMgs, nil, ok
}
func PrepareMsgMaking(operation string, clientID string, data []byte,
	viewID int64, sID int64, nodeID string, Seed int) *consensus.ReqPrePareMsgs {
	var RequestMsg consensus.RequestMsg
	RequestMsg.Timestamp = time.Now().UnixNano()
	RequestMsg.Operation = operation
	RequestMsg.ClientID = clientID
	RequestMsg.Data = string(data)
	RequestMsg.SequenceID = sID

	digest, err := consensus.Digest(RequestMsg)

	if err != nil {
		fmt.Println(err)
	}

	var PrepareMsg consensus.PrepareMsg
	PrepareMsg.ViewID = viewID
	PrepareMsg.SequenceID = sID
	PrepareMsg.Digest = digest
	PrepareMsg.EpochID = 0
	PrepareMsg.NodeID = nodeID
	PrepareMsg.Seed = Seed

	var ReqPrePareMsgs consensus.ReqPrePareMsgs
	ReqPrePareMsgs.RequestMsg = &RequestMsg
	ReqPrePareMsgs.PrepareMsg = &PrepareMsg

	return &ReqPrePareMsgs
}
