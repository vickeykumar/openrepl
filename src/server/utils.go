package server

import (
	"encoding/xml"
	"log"
	"net/http"
	"utils"
	"html/template"
	"bytes"
)

const PREFIX = "static"
const MetaPath = "/meta"

func InitCommands2DemoMap() {
	log.Printf("InitCommands2DemoMap")
	var demos utils.DemoList
	utils.Commands2DemoMap = make(map[string]utils.Demo)
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
		utils.Commands2DemoMap[demo.Name] = demo
	}
}

func handleDemo(rw http.ResponseWriter, req *http.Request) {
	var demoResponse utils.DemoResp
	var demo utils.Demo
	if utils.Commands2DemoMap == nil {
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
			if demo, ok = utils.Commands2DemoMap[query[0]]; !ok {
				log.Println("ERROR: invalid query detected: ", query)
				demoResponse.Status = STATUS_FAILED
				demoResponse.ErrorMsg = "Invalid query detected."
			} else {
				demoResponse.Status = STATUS_SUCCESS
				demoResponse.Demo = demo
			}
		}
		rw.Write(utils.JsonMarshal(demoResponse))
	} else {
		demoResponse.ErrorMsg = "Invalid Request Method."
		demoResponse.Status = STATUS_FAILED
		rw.Write(utils.JsonMarshal(demoResponse))
	}
}

var CommonTemplate = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <meta name="description" content="">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <base target="_top">
    <title>{{.title}}</title>
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/4.7.0/css/font-awesome.min.css">
    <link href="https://fonts.googleapis.com/css?family=Nunito+Sans:300,400,600,700,800,900" rel="stylesheet">
    <link rel="stylesheet" href="./css/scribbler-global.css">
    <link rel="stylesheet" href="./css/scribbler-doc.css">
    <script src="./js/preprocessing.js"></script>
    <link rel="author" href="humans.txt">
  </head>
  <body>
    <div class="doc__bg"></div>
    <nav class="header">
      <h1 class="logo"><span class="go__color">Open</span>REPL</h1>
      <ul class="menu">
        <div class="menu__item toggle"><span></span></div>
        <li class="menu__item"><a href="../" class="link link--dark"><i class="fa fa-home"></i> Home</a></li>
      </ul>
    </nav>
  <div class="wrapper">
  {{.body}}
  </div>
  <footer class="footer">
    <a href="./about.html" class="link link--light">About</a> <span class="dot"></span>
    <span id="copyright_year">
        <script>document.getElementById('copyright_year').appendChild(document.createTextNode(new Date().getFullYear()))</script>
    </span>&copy;<span class="go__color">Open</span>REPL 
    </footer>
    <script src="./js/common.js"></script>
  </body>
</html>`



// common errorHandler
func errorHandler(w http.ResponseWriter, r *http.Request, body string, status int) {
		w.WriteHeader(status)
		indexVars := map[string]interface{}{
			"title": "ERROR",
			"body":  template.HTML("<h1>"+body+"</h1>"),
		}
		indexTemplate, err := template.New("index").Parse(CommonTemplate)
		if err != nil {
			log.Println("index template parse failed") // must be valid
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		indexBuf := new(bytes.Buffer)
		err = indexTemplate.Execute(indexBuf, indexVars)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.Write(indexBuf.Bytes())
}
