package main

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/bigpicturelabs/consensusPBFT/pbft/network"
	"io/ioutil"
	"log"
	"os"
)

// Hard-coded for test.
var viewID = int64(10000000000)

func main() {

	if len(os.Args) < 2 {
		fmt.Println("Usage:", os.Args[0], "<nodeID> <TOTALNUM> [node.list]")
		return
	}
	nodeID := os.Args[1] 

	// Generate NodeTable
	nodeTable:=GenNodeTable(os.Args[3])

	// Generate SeedNodeTable
	seedNodeTables:=GenSeedNodeTables(nodeTable)

	// Load public key for each node.
	GenPublicKeys(&nodeTable)

	// Make NodeID PriveKey
	decodePrivKey:=GenPrivateKeys(nodeID)

	// Make server object
	server := network.NewServer(nodeID, nodeTable, seedNodeTables, 
		viewID, decodePrivKey)

	// start server
	if server != nil {
		server.Start()
	}
}

func AssertError(err error) {
	if err == nil {
		return
	}
	log.Println(err)
	os.Exit(1)
}
func GenNodeTable(NODELISTPATH string) []*network.NodeInfo{
	// Local: "/tmp/node.list"
	// Remote: "./nodeList/nodeNum"$TOTALNODE"/nodeList_remote.json"
	// AWS: "./nodeList/nodeNum"$TOTALNODE"/nodeList_aws.json"
	var nodeTable []*network.NodeInfo
	jsonFile, err := os.Open(NODELISTPATH)
	AssertError(err)
	defer jsonFile.Close()
	err = json.NewDecoder(jsonFile).Decode(&nodeTable)
	AssertError(err)
	return nodeTable
}
func GenSeedNodeTables(nodeTable []*network.NodeInfo) [][]*network.NodeInfo{
	randomNum:=2
	seedNodeTables := make([][]*network.NodeInfo, randomNum)
	for i:=0; i<randomNum; i++{
		seedNodeTables[i] = make([]*network.NodeInfo,len(nodeTable))
		front:=nodeTable[0:i]
		end:=nodeTable[i:len(nodeTable)]
		seedNodeTables[i]=append(end, front...)
	}
	return seedNodeTables
}
func GenPublicKeys(nodeTable *[]*network.NodeInfo) {
	for _, nodeInfo := range *nodeTable {
		pubKeyFile := fmt.Sprintf("keys/%s.pub", nodeInfo.NodeID)
		pubBytes, err := ioutil.ReadFile(pubKeyFile)
		AssertError(err)

		decodePubKey := PublicKeyDecode(pubBytes)
		nodeInfo.PubKey = decodePubKey
	}
}
func GenPrivateKeys(nodeID string) *ecdsa.PrivateKey {
	privKeyFile := fmt.Sprintf("keys/%s.priv", nodeID)
	privbytes, err := ioutil.ReadFile(privKeyFile)
	AssertError(err)
	decodePrivKey := PrivateKeyDecode(privbytes)
	return decodePrivKey
}
func PrivateKeyDecode(pemEncoded []byte) *ecdsa.PrivateKey {
	blockPriv, _ := pem.Decode(pemEncoded)
	x509Encoded := blockPriv.Bytes
	privateKey, _ := x509.ParseECPrivateKey(x509Encoded)

	return privateKey
}
func PublicKeyDecode(pemEncoded []byte) *ecdsa.PublicKey {
	blockPub, _ := pem.Decode(pemEncoded)
	x509EncodedPub := blockPub.Bytes
	genericPublicKey, _ := x509.ParsePKIXPublicKey(x509EncodedPub)
	publicKey := genericPublicKey.(*ecdsa.PublicKey)

	return publicKey
}
