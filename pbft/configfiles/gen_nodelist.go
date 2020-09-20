package main
import (
	"encoding/json"
	"fmt"
	"strconv"
	"log"
	"os"
)
// Don't use lower case at the name of member of struct
type IpStruct struct {
	Ip				string 					`json:"ip"`
}
type NumOfNodeStruct struct {
	NumOfNode		int 					`json:"NumOfNode"`
}
type InstanceInfo struct {
	TotalNodeNum	int						`json:"totalNodeNum"`
	PerInstanceNum	[]*NumOfNodeStruct		`json:"numOfNode`
}
type Configuration struct {
	InstanceIpList	[]*IpStruct				`json:"instanceIpList`
	InstanceInfos	[]*InstanceInfo			`json:"instanceInfos`
}

func main(){
	var configuration Configuration
	infoNum := GetInstanceInfo(&configuration, "./config_aws.json")
	for i:=0; i<len(infoNum); i++{
		GenerateNodeFile(configuration, infoNum[i],"./config_aws.json")
		//fmt.Println(infoNum[i])
	}
}

func GetInstanceInfo(configuration *Configuration, 
					CONFIGURATIONPATH string)[]int{
	jsonFile, err := os.Open(CONFIGURATIONPATH)
	AssertError(err)
	defer jsonFile.Close()
	err = json.NewDecoder(jsonFile).Decode(configuration)
	AssertError(err)
	var infoNum []int = make([]int,len(configuration.InstanceInfos))
	for i:=0; i<len(configuration.InstanceInfos); i++{
		infoNum[i]= configuration.InstanceInfos[i].TotalNodeNum
	}
	return infoNum
}
func GenerateNodeFile(config Configuration, totNum int, mode string){
	var currentTotNumConfig []*NumOfNodeStruct
	for i:=0; i<len(config.InstanceInfos); i++{
		if config.InstanceInfos[i].TotalNodeNum == totNum {
			currentTotNumConfig = config.InstanceInfos[i].PerInstanceNum
			break
		}
	}
	FILENAME:= "./nodeList/nodeNum"+strconv.Itoa(totNum) +".json"
	outputFile, _ := os.Create(FILENAME)
	defer outputFile.Close()
	nodeNum:=0
	portnumber:=1110
	COMMA:=","
	fmt.Fprintln(outputFile, "[")
	for i:=0; i<len(currentTotNumConfig); i++{
		ip:=config.InstanceIpList[i].Ip
		for j:=0; j<currentTotNumConfig[i].NumOfNode; j++{
			nodeNum+=1
			if nodeNum == totNum {
				COMMA = ""
			}
			fmt.Fprintf(outputFile, 
				"\t{\n\t\t\"nodeID\": \"Node%d\", \n\t\t\"url\": \"%s:%d\"\n\t}%s\n", 
				nodeNum, ip, portnumber + nodeNum, COMMA)
		}
	}
	fmt.Fprintln(outputFile, "]")
}
func AssertError(err error) {
	if err == nil {
		return
	}
	log.Println(err)
	os.Exit(1)
}
