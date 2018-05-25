package server

import (
	"encoding/base64"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"strconv"
	"webtty"
)

const MAX_CONN_PER_BROWSER = 1

// handling for cookies and incrementing session cookie counter
func IncrementCounterCookies(rw http.ResponseWriter, req *http.Request) {
	cookie, err := req.Cookie("Session-Counter")
	if err == http.ErrNoCookie {
		cookie = &http.Cookie{
			Name:  "Session-Counter",
			Value: "0",
		}
	} else if err != nil {
		log.Println("unknown error while getting cookie: ", err.Error())
		return
	}
	sessionCount, _ := strconv.Atoi(cookie.Value)
	sessionCount++
	cookie.Value = strconv.Itoa(sessionCount)
	http.SetCookie(rw, cookie)
	log.Println("cookie set: ", cookie)
}

func DecrementCounterCookies(rw http.ResponseWriter, req *http.Request) {
	// decrementing cookie
	cookie, err := req.Cookie("Session-Counter")
	if err == http.ErrNoCookie {
		cookie = &http.Cookie{
			Name:  "Session-Counter",
			Value: "0",
		}
	} else {
		log.Println("unknown error while getting cookie: ", err.Error())
		return
	}
	sessionCount, _ := strconv.Atoi(cookie.Value)
	if sessionCount > 0 {
		sessionCount--
		cookie.Value = strconv.Itoa(sessionCount)
		http.SetCookie(rw, cookie)
		log.Println("cookie dec: ", cookie)
	}
}

func GetCounterCookieValue(rw http.ResponseWriter, req *http.Request) int {
	cookie, err := req.Cookie("Session-Counter")
	if err != nil {
		log.Println("Error while getting cookie: ", err.Error())
		return 0
	}
	sessionCount, _ := strconv.Atoi(cookie.Value)
	return sessionCount
}

func WriteMessageToTerminal(conn *websocket.Conn, message string) {
	safeMessage := base64.StdEncoding.EncodeToString([]byte(message))
	err := conn.WriteMessage(websocket.TextMessage, []byte(append([]byte{webtty.Output}, []byte(safeMessage)...)))
	if err != nil {
		log.Println("err while writing: ", err)
	}
}
