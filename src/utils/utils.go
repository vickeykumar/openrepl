package utils

import (
	"encoding/json"
	"github.com/natefinch/lumberjack"
	"log"
	"net/url"
	"os"
)

const LOG_PATH = "/gottyTraces"
const IdeLangKey = "IdeLang"
const IdeContentKey = "IdeContent"

func InitLogging(name string) {
	err := os.MkdirAll(LOG_PATH, 0755)
	if err == nil {
		log.Printf("Writting gotty logs in : " + LOG_PATH + "/" + name + ".log")
		log.SetFlags(log.LstdFlags | log.Lshortfile)
		log.SetOutput(&lumberjack.Logger{
			Filename:   LOG_PATH + "/" + name + ".log",
			MaxSize:    10, // megabytes
			MaxBackups: 5,
			MaxAge:     30, //days
		})
		log.Printf("Writting gotty logs in : " + LOG_PATH + "/" + name + ".log")
	} else {
		log.Printf("Error: unable to create log directory: " + LOG_PATH)
		os.Exit(3)
	}
}


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

func Iscompiled(params map[string][]string) bool {
	_, ok1 := params[IdeLangKey]
	_, ok2 := params[IdeContentKey]
	return ok1 && ok2
}

func GetCompilerLang(params url.Values) string {
	return params.Get(IdeLangKey)
}

func GetIdeContent(params url.Values) string {
	return params.Get(IdeContentKey)
}