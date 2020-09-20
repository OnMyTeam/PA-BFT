// TODO: secure connection such as HTTPS, or manual implementation
// from Section 5.2.2 Key Exchanges on TOCS.
package network

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"

	// "net/url"

	"time"

	consensus "consensus"

	"github.com/gorilla/websocket"
)

type Server struct {
	url  string
	hub  *Hub
	node *Node
}

func NewServer(nodeID string, nodeTable []*NodeInfo, seedNodeTables [][]*NodeInfo,
	viewID int64, decodePrivKey *ecdsa.PrivateKey) *Server {

	nodeIdx := int(-1)
	for idx, nodeInfo := range nodeTable {
		if nodeInfo.NodeID == nodeID {
			nodeIdx = idx
			break
		}
	}

	if nodeIdx == -1 {
		log.Printf("Node '%s' does not exist!\n", nodeID)
		return nil
	}

	node := NewNode(nodeTable[nodeIdx], nodeTable, seedNodeTables, viewID, decodePrivKey)
	server := &Server{
		url:  nodeTable[nodeIdx].Url,
		hub:  NewHub(),
		node: node,
	}

	return server
}

func (server *Server) Start() {
	log.Printf("%s Server will be started at %s...\n", server.node.MyInfo.NodeID, server.url)
	path := "/normalmsg"
	// 1. register handler function
	handler := func(w http.ResponseWriter, r *http.Request) {
		ServeWs(server.hub, w, r )
	}
	http.HandleFunc(path, handler)

	go server.hub.run()
	go server.broadcastLoop()

	// 2. run Server - listen a websocket(server.url.. for example, localhost:1020)
	go func() {
		if err := http.ListenAndServe(server.url, nil); err != nil {
			log.Println(err)
			return
		}
	}()
	time.Sleep(time.Second * 3) // Sleep until all nodes perform ListenAndServ()

	// 3. run Client - dial to other nodes's websocket server
	go func() {
		for _, nodeInfo := range server.node.NodeTable {
			// if nodeInfo.NodeID == server.node.MyInfo.NodeID {
			// 	continue
			// }
			u := url.URL{Scheme: "ws", Host: nodeInfo.Url, Path: path}
			c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
			if err != nil {
				log.Fatal("dial:", err)
				return
			}
			log.Printf("connecting to %s from %s for %s", nodeInfo.NodeID, server.node.MyInfo.NodeID, path)
			//log.Println("sRL local addr : ",c.LocalAddr(),"sRL remote addr : ",c.RemoteAddr())
			go server.receiveLoop(c, path, nodeInfo)
		}
		select {} // Wait
	}()

	// 4. trigger consensus if current node is primary
	time.Sleep(time.Second * 5)
	server.sendGenesisMsgIfPrimary()
}
func (server *Server) sendGenesisMsgIfPrimary() {
	var sequenceID int64 = 1
	var seed int = -1

	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = 'A'
	}
	data[len(data)-1] = 0

	primaryNode := server.node.getPrimaryInfoByID(sequenceID)

	if primaryNode.NodeID != server.node.MyInfo.NodeID {
		return
	}
	prepareMsg := PrepareMsgMaking("Op1", "Client1", data,
		server.node.View.ID, int64(sequenceID),
		server.node.MyInfo.NodeID, int(seed))

	log.Printf("Broadcasting dummy message from %s, sequenceId: %d",
		server.node.MyInfo.NodeID, sequenceID)

	log.Println("[StartPrepare]", "seqID", sequenceID, time.Now().UnixNano())
	//time.Sleep(time.Millisecond*150)
	//time.Sleep(time.Millisecond * 100)
	server.node.Broadcast(prepareMsg, "/prepare")
}
func (server *Server) broadcastLoop() {
	sem := make(chan bool, MaxOutboundConnection)

	for {
		msg := <-server.node.MsgOutbound
		// Goroutine for concurrent broadcast() with timeout
		sem <- true
		go func() {
			defer func() { <-sem }()
			// errCh := make(chan error, 10)

			// Goroutine for concurrent broadcast()
			go func() {
				sigMgsBytes := attachSignatureMsg(msg.Msg, server.node.PrivKey, msg.Path)
				server.hub.broadcast <- sigMgsBytes
			}()
			// select {
			// case err := <-errCh:
			// 	if err != nil {
			// 		server.node.MsgError <- []error{err}
			// 		// TODO: view change.
			// 	}
			// }
		}()
	}
}
func (server *Server) receiveLoop(cc *websocket.Conn, path string, nodeInfo *NodeInfo) {
	c := cc
	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			u := url.URL{Scheme: "ws", Host: nodeInfo.Url, Path: path}
			c, _, err = websocket.DefaultDialer.Dial(u.String(), nil)
			if err != nil {
				log.Fatal("dial:", err)
				return
			}
			_, message, err = c.ReadMessage()
			log.Printf("currunpted message size: %d\n", len(message))
			continue
		}
		var rawMsg consensus.SignatureMsg
		rawMsg, err, ok := deattachSignatureMsg(message, nodeInfo.PubKey)
		if err != nil {
			fmt.Println("[receiveLoop-error]", err)
		}
		if ok == false {
			fmt.Println("[receiveLoop-error] signature decoding error")
		}
		time.Sleep(time.Millisecond * 200)
		switch rawMsg.MsgType {
		case "/prepare":
			// ReqPrePareMsgs have RequestMsg and PrepareMsg
			var msg consensus.ReqPrePareMsgs
			_ = json.Unmarshal(rawMsg.MarshalledMsg, &msg)
			if msg.PrepareMsg.SequenceID == 0 {
				fmt.Println("[receiveLoop-error] seq 0 came in")
				continue
			}
			fmt.Println("/[EndPrepare] /", server.node.MyInfo.NodeID, "/", msg.PrepareMsg.SequenceID, "/", time.Now().UnixNano())
			log.Println("/[EndPrepare] /", server.node.MyInfo.NodeID, "/", msg.PrepareMsg.SequenceID, "/", time.Now().UnixNano())
			server.node.MsgEntrance <- &msg
		case "/vote":
			var msg consensus.VoteMsg
			_ = json.Unmarshal(rawMsg.MarshalledMsg, &msg)
			if msg.SequenceID == 0 {
				fmt.Println("[receiveLoop-error] seq 0 came in")
				continue
			}
			server.node.MsgEntrance <- &msg
		case "/collate":
			var msg consensus.CollateMsg
			_ = json.Unmarshal(rawMsg.MarshalledMsg, &msg)
			if msg.SequenceID == 0 {
				fmt.Println("[receiveLoop-error] seq 0 came in")
				continue
			}
			server.node.MsgEntrance <- &msg
		/*
			case "/checkpoint":
				var msg consensus.CheckPointMsg
				server.node.MsgEntrance <- &msg
		*/
		case "/viewchange":
			var msg consensus.ViewChangeMsg
			_ = json.Unmarshal(rawMsg.MarshalledMsg, &msg)
			server.node.ViewMsgEntrance <- &msg
		case "/newview":
			var msg consensus.NewViewMsg
			_ = json.Unmarshal(rawMsg.MarshalledMsg, &msg)
			server.node.ViewMsgEntrance <- &msg
		}
	}
}
