package server

import (
	"encoding/json"
	"encoding/xml"
	"log"
	"net/http"
)

const PREFIX = "static"
const MetaPath = "/meta"

func JsonMarshal(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		log.Println("ERROR: while Marshalling : ", v, "Error: ", err)
	}
	return data
}

func JsonUnMarshal(data []byte, v interface{}) {
	err := json.Unmarshal(data, v)
	if err != nil {
		log.Println("ERROR: while unMarshalling : ", data, " Error: ", err)
	}
}

type Usage struct {
	Command     string
	Description string
}

type Code struct {
	Prompt    string
	Statement string
	Result    string
}

type Codes struct {
	Name string
	Code []Code
}

type Demo struct {
	Name   string
	Github string
	Codes  []Codes
	Usage  []Usage
}

type DemoResp struct {
	Status   string
	ErrorMsg string
	Demo     Demo
}

type DemoList struct {
	Demos []Demo `xml:"Demo"`
}

var Commands2DemoMap map[string]Demo

func InitCommands2DemoMap() {
	var demos DemoList
	Commands2DemoMap = make(map[string]Demo)
	data, err := Asset(PREFIX + MetaPath + "/demos.xml")
	if err != nil {
		log.Println("ERROR: Meta file Not Found: ", err)
		return
	}
	err = xml.Unmarshal(data, &demos)
	if err != nil {
		log.Println("ERROR: Unmarshal failed for meta xml: ", err)
		return
	}
	log.Println("meta output: ", demos)
	for _, demo := range demos.Demos {
		Commands2DemoMap[demo.Name] = demo
	}
}

func handleDemo(rw http.ResponseWriter, req *http.Request) {
	var demoResponse DemoResp
	var demo Demo
	if Commands2DemoMap == nil {
		InitCommands2DemoMap()
	}
	log.Println("method:", req.Method)
	if req.Method == "GET" {
		req.ParseForm()
		log.Println("Data recieved in Form: ", req.Form)
		query, ok := req.Form["q"]
		if !ok || len(query) == 0 {
			log.Println("ERROR: invalid query detected: ", query)
			demoResponse.Status = STATUS_FAILED
			demoResponse.ErrorMsg = "Invalid query detected."
		} else {
			if demo, ok = Commands2DemoMap[query[0]]; !ok {
				log.Println("ERROR: invalid query detected: ", query)
				demoResponse.Status = STATUS_FAILED
				demoResponse.ErrorMsg = "Invalid query detected."
			} else {
				demoResponse.Status = STATUS_SUCCESS
				demoResponse.Demo = demo
			}
		}
		rw.Write(JsonMarshal(demoResponse))
	} else {
		demoResponse.ErrorMsg = "Invalid Request Method."
		demoResponse.Status = STATUS_FAILED
		rw.Write(JsonMarshal(demoResponse))
	}
}
