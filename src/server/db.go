package server

import (
	"encoding/json"
	"fmt"
	"github.com/nobonobo/unqlitego"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const GOTTY_PATH = "/opt/gotty"

const FEEDBACK_DB = GOTTY_PATH + "/feedback.db"

var feedback_db_handle *unqlitego.Database

type feedback struct {
	Name    string
	Email   string
	Message string
}

func InitFeedbackDBHandle() {
	var err error
	feedback_db_handle, err = unqlitego.NewDatabase(FEEDBACK_DB)
	if err != nil {
		log.Println("ERROR: Error while creating feedback DB handle : ", err.Error())
		os.Exit(3)
	}
	log.Println("Successfully initialized fb handle: ", feedback_db_handle)
}

func CloseFeedbackDBHandle() {
	err := feedback_db_handle.Close()
	if err != nil {
		log.Println("ERROR: Error while closing feedback DB handle : ", err.Error())
	}
}

func StoreFeedbackData(fb *feedback) error {
	data, err := json.Marshal(*fb)
	if err != nil {
		log.Println("ERROR: Error while marshalling feedback data. Error: ", err.Error())
		return err
	}
	log.Println("got feedback data: ", fb)
	timestamp := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	err = feedback_db_handle.Store([]byte(timestamp), data)
	if err != nil {
		log.Println("ERROR: Failed to store the feedbackdata, error: ", err.Error())
		return err
	}
	err = feedback_db_handle.Commit()
	if err != nil {
		log.Println("ERROR: Failed to commit the feedbackdata to disk, error: ", err.Error())
	}
	return err
}

func handleFeedback(rw http.ResponseWriter, req *http.Request) {
	log.Println("method:", req.Method)
	if req.Method == "POST" {
		req.ParseForm()
		var fb feedback
		fb.Name = strings.Join(req.Form["name"], "")
		fb.Email = strings.Join(req.Form["email"], "")
		fb.Message = strings.Join(req.Form["message"], "")

		err := StoreFeedbackData(&fb)
		if err == nil {
			fmt.Fprintf(rw, "Thanks for your Feedback !!")
		}
	}
}
