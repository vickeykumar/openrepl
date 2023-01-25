package encoder

import (
	"encoding/base64"
	"log"
	"strconv"
)

var RawStdEncoding = base64.StdEncoding.WithPadding(base64.NoPadding)

func EncodePID(pid interface{}) string {
	ppid, _ := pid.(int)
	return RawStdEncoding.EncodeToString([]byte(strconv.Itoa(ppid)))
}

func DecodeToPID(jid string) int {
	var pid int = -1
	pidstr,err := RawStdEncoding.DecodeString(jid)
	if err == nil {
		pid, err = strconv.Atoi(string(pidstr))
		if err == nil {
			return pid
		}
	}
	log.Println("invalid pid recieved :"+string(pidstr), err.Error())
	return -1
}