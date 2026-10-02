package server

import (
	"encoding/base64"
	"encoding/xml"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"utils"
	"html/template"
	"bytes"
	"cookie"
	"user"
	"webtty"
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

func WriteMessageToTerminal(conn *websocket.Conn, message string) {
	safeMessage := base64.StdEncoding.EncodeToString([]byte(message))
	err := conn.WriteMessage(websocket.TextMessage, []byte(append([]byte{webtty.Output}, []byte(safeMessage)...)))
	if err != nil {
		log.Println("err while writing: ", err)
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

var FeedbackTemplate =`
<table id="feedback_table">
    <thead>
        <tr>
	    <th>ID</th>
            <th>Name</th>
            <th>Email</th>
            <th>Message</th>
            <th>Date And Time</th>
	    <th>Delete</th>
        </tr>
    </thead>
    <tbody>
    	{{range $key, $value := .}}
        <tr>
	    <td>{{$key}}</td>
            <td>{{$value.Name}}</td>
            <td>
            <a href="https://mail.google.com/mail/?view=cm&fs=1&tf=1&to={{$value.Email}}&su=Greetings%20from%20OpenREPL" target="_blank" > {{$value.Email}} </a>
            </td>
            <td>{{$value.Message}}</td>
            <td></td>
	    <td></td>
        </tr>
      {{end}}
    </tbody>
</table>
<script>
    $(document).ready(function () {
        var table = $('#feedback_table').DataTable({
	    "columnDefs": [
	      {
		"targets": 4, // Fifth column
		"render": function(data, type, row) {
		  // Assumes timestamp is in first column and nanosec
		  return new Date(parseInt(row[0])/1000000);
		}
	      },
	      {
                "targets": -1, // the last column
		"data": null, // render data from the whole row
                "render": function(data, type, row) {
                  // 1st col is id/key , that is timestamp
                  return "<button data-key='" + row[0] + "'>Delete</button>";
               }
              }
	    ]
	});

	$('#feedback_table tbody').on('click', 'button', function() {
	    var row = $(this).parents('tr');
	    var data = table.row(row).data();
	    $.ajax({
		url: '/feedback?q=delete&key='+data[0],
		method: 'POST',
		success: function(response) {
		    // delete the row data and redraw the table
		    table.row(row).remove().draw();
		},
		error: function(xhr, status, error) {
		    alert('Error deleting row: ' + error);
		}
	    });
	});
    });
</script>
`

var BlogList_Template = `<article class="doc__content">
			{{range $key, $value := .}}
        <section class="blog-post">
              <h2>{{$value.Title}}</h2>
              <p class="post-meta">Last updated on {{formatDate $value.Lastupdated}}</p>
              <p>{{$value.Desc}}</p>
              <a href="/blog?name={{$key}}" class="read-more">Read More</a>
              <hr />
        </section>
      {{end}}
      </article>`

var Blog_Template = `<article class="doc__content">
        <h2>{{.Title}}</h2>
        <p class="post-meta">Last updated on {{formatDate .Lastupdated}}</p>
        {{htmlify .Content}}
      </article>`

var CommonTemplate = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="description" content="">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <base target="_top">
    <title>{{.title}}</title>
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/4.7.0/css/font-awesome.min.css">
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/datatables/1.10.21/css/jquery.dataTables.min.css">
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Bricolage+Grotesque:opsz,wght@12..96,600;12..96,800&amp;family=IBM+Plex+Sans:ital,wght@0,400;0,500;0,600;0,700;1,400&amp;family=JetBrains+Mono:wght@400;600&amp;display=swap" rel="stylesheet">
    <link rel="stylesheet" href="/css/scribbler-global.css">
    <link rel="stylesheet" href="/css/scribbler-doc.css">
    <script type="text/javascript" src="https://cdnjs.cloudflare.com/ajax/libs/jquery/3.1.1/jquery.min.js"></script>
    <script type="text/javascript" src="https://cdnjs.cloudflare.com/ajax/libs/datatables/1.10.21/js/jquery.dataTables.min.js"></script>
    <script src="/js/theme.js"></script>
    <script src="/settings.js"></script>
    <script src="/js/preprocessing.js"></script>
    <link rel="author" href="humans.txt">
  </head>
  <body>
    <nav class="header">
      <h1 class="logo"><span class="go__color">Open</span>REPL</h1>
      <ul class="menu">
        <div class="menu__item toggle"><span></span></div>
        <li class="menu__item"><button type="button" class="theme-toggle" data-theme-toggle aria-label="Switch to dark theme"><svg class="theme-toggle__moon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"></path></svg><svg class="theme-toggle__sun" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4"></circle><path d="M12 2v2 M12 20v2 M4.9 4.9l1.4 1.4 M17.7 17.7l1.4 1.4 M2 12h2 M20 12h2 M4.9 19.1l1.4-1.4 M17.7 6.3l1.4-1.4"></path></svg></button></li>
        <li class="menu__item"><a href="../" class="link link--dark"><i class="fa fa-home"></i> Home</a></li>
      </ul>
    </nav>
  <div class="wrapper">
  {{.body}}
  </div>
  <footer class="footer" id="footer">
    <a href="./about.html" class="link link--light">About</a> <span class="dot"></span>
    <span id="copyright_year">
        <script>document.getElementById('copyright_year').appendChild(document.createTextNode(new Date().getFullYear()))</script>
    </span>&copy;<span class="go__color">Open</span>REPL 
    </footer>
    <script src="/js/common.js"></script>
  </body>
</html>`

func htmlify(content string) template.HTML {
	return template.HTML(content)
}

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


// common Handler
func commonHandler(w http.ResponseWriter, r *http.Request, 
	title string, htmlbody string, status int) {
		w.WriteHeader(status)
		indexVars := map[string]interface{}{
			"title": title,
			"body":  template.HTML(htmlbody),
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


// IsUserAdmin determines whether the request comes from an admin: a live
// session whose profile email is one of the configured admin accounts
// (OPENREPL_ADMIN_EMAILS, or the file's user.email; see utils/config.go).
// Without a configured admin nobody is one.

func IsUserAdmin(rw http.ResponseWriter, req *http.Request) (isadmin bool) {
		isadmin = false // testing
		var session user.UserSession
		session = cookie.Get_SessionCookie(req)
		up, err := user.FetchUserProfileData(session.Uid)
		// verify the session in local db, and the logged in user's email
		// against the configured admin accounts
		if err != nil || user.IsSessionExpired(session.Uid, session.SessionID)==true ||
		  !utils.IsAdminEmail(up.Email) {
			session.LogOut()
			return
		}
		return true
}




