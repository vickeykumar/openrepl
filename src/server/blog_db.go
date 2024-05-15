package server

import (
	"encoding/json"
	"fmt"
	"github.com/nobonobo/unqlitego"
	"log"
	"net/http"
	"os"
	"time"
	"utils"
	"html/template"
	"bytes"
	"github.com/pkg/errors"
)

const BLOG_DB = utils.GOTTY_PATH + "/blog.db"

var blog_db_handle *unqlitego.Database

type BlogPost struct {
	Name        string    	`json:"name"`
	Title       string    	`json:"title"`
	Desc       	string      `json:"desc"`
	Content     string    	`json:"content"`
	Lastupdated time.Time 	`json:"lastupdated,omitempty"`
}

func NewBlogPost(name, title, desc, content string) *BlogPost {
	return &BlogPost {
		Name: name,
		Title: title,
		Desc: desc,
		Content: content,
		Lastupdated: time.Now(),
	}
}

func InitBlogDBHandle() {
	var err error
	blog_db_handle, err = unqlitego.NewDatabase(BLOG_DB)
	if err != nil {
		log.Println("ERROR: Error while creating blog DB handle : ", err.Error())
		os.Exit(3)
	}
	log.Println("Successfully initialized blog db handle: ", blog_db_handle)
}

func CloseBlogDBHandle() {
	err := blog_db_handle.Close()
	if err != nil {
		log.Println("ERROR: Error while closing blog DB handle : ", err.Error())
	}
	blog_db_handle = nil
}

func StoreBlogData(blog *BlogPost) error {
	data, err := json.Marshal(*blog)
	if err != nil {
		log.Println("ERROR: Error while marshalling blog data. Error: ", err.Error())
		return err
	}
	log.Println("got blog data: ", blog)
	if blog.Title=="" || blog.Desc=="" || blog.Content=="" {
		return errors.New("Empty data recieved in blog")
	}
	err = blog_db_handle.Store([]byte(blog.Name), data)
	if err != nil {
		log.Println("ERROR: Failed to store the blog data, error: ", err.Error())
		return err
	}
	err = blog_db_handle.Commit()
	if err != nil {
		log.Println("ERROR: Failed to commit the blog data to disk, error: ", err.Error())
	}
	return err
}

func deleteBlogData(blogname string) error {
	err := blog_db_handle.Delete([]byte(blogname))
	if err!= nil {
		return err
	}
	err = blog_db_handle.Commit()
	return err
}

func FetchBlogDataMap() (bloglistmap map[string]BlogPost) {
	bloglistmap = make(map[string]BlogPost)
	cursor, err := blog_db_handle.NewCursor()
	if err != nil {
		log.Println("Error creating cursor: ",err.Error())
		return
	}
	defer cursor.Close()

	err = cursor.First()
	if err != nil {
		log.Println("Error Fetching cursor: ",err.Error())
		return
	}
	var timestamp int64
	var blog BlogPost
	for cursor.IsValid() {
		func (cursor *unqlitego.Cursor) {
			key, err := cursor.Key()
			if err != nil {
				log.Println("Error Fetching cursor key: ",err.Error())
				return
			}
			value, err := cursor.Value()
			if err != nil {
				log.Println("Error Fetching cursor value for key: ", key, err.Error())
				return
			}
			err = json.Unmarshal(value, &blog)
			if err != nil {
				log.Println("ERROR: while unMarshalling for key: ", string(key), value, " Error: ", err)
				return
			}
			bloglistmap[string(key)] = blog
			defer func(cursor *unqlitego.Cursor) {
				err := cursor.Next()
				if err != nil {
					// handle error
					log.Println("Failed finding next cursor for key: ", key, timestamp, err.Error())
					return
				}
			}(cursor)
		}(cursor)
	}
	return
}

// Define a template function to format the date
func formatDate(t time.Time) string {
    return t.Format("January 2, 2006") // Example: "January 1, 2024"
}

func handleBlog(rw http.ResponseWriter, req *http.Request) {
	var err error
	log.Println("method:", req.Method)
	req.ParseForm()
    log.Println("Data recieved in Form: ", req.Form)
	if req.Method == "POST" {
		if IsUserAdmin(rw, req) == false {
				http.Error(rw, "Unauthorized Access!! Please Sign in again as Admin.", http.StatusUnauthorized)
				return
		}
		query := req.Form.Get("q")
		if query == "delete" {
			key := req.Form.Get("name")
			err = deleteBlogData(key)
			if err != nil {
				log.Println("blogdata delete failed for key: ", key, err.Error()) // must be valid
				fmt.Fprintf(rw, "Error deleteing blog for key: "+key)
				http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
                        	return
            }
            fmt.Fprintf(rw, key+" blog deleted Successfully !!")
			// return after deleting key
			return 
		}
		name := req.Form.Get("name")
		title := req.Form.Get("title")
		desc := req.Form.Get("desc")
		blogcontent := req.Form.Get("content")

		var blog *BlogPost = NewBlogPost(name, title, desc, blogcontent)

		err := StoreBlogData(blog)
		if err == nil {
			// Return a success response
			fmt.Fprintf(rw, name+" blog updated Successfully !!")
			rw.WriteHeader(http.StatusOK)
		} else {
			fmt.Fprintf(rw, "Error updating blog for key: "+name)
			http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	} else if req.Method == "GET" {
		blogdatamap := FetchBlogDataMap()
		query := req.Form.Get("q")
		if query == "list" {
			// Create a slice to store the keys
		    var keys []string
		    // Iterate over the map and append keys to the slice
		    for key := range blogdatamap {
		        keys = append(keys, key)
		    }
		    // Encode the slice into JSON
		    keysJSON, err := json.Marshal(keys)
		    if err != nil {
		        log.Println("error encoding keys to JSON:", err)
		        http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
		        return
		    }
		    // Set the Content-Type header to application/json
		    rw.Header().Set("Content-Type", "application/json")
		    // Write the JSON response
		    rw.WriteHeader(http.StatusOK)
		    rw.Write(keysJSON)
		    return
		}

		if len(blogdatamap)==0 {
			log.Println("blog data not found")
			errorHandler(rw, req, "No Blogs To Show", http.StatusNotFound )
			return
		}
		name := req.Form.Get("name")
		if name != "" {
			blog, ok := blogdatamap[name]
			if !ok {
				log.Println("blog data not found for: ", name)
				errorHandler(rw, req, name+" Not Found.", http.StatusNotFound )
				return
			}
			if query=="json" {
				jsondata, err := json.Marshal(blog)
			    if err != nil {
			        log.Println("error encoding keys to JSON:", err)
			        http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
			        return
			    }
			    // Set the Content-Type header to application/json
			    rw.Header().Set("Content-Type", "application/json")
			    // Write the JSON response
			    rw.WriteHeader(http.StatusOK)
			    rw.Write(jsondata)
			    return
			}
			// render blog template
			blogtmpl := template.Must(template.New("BlogTemplate").Funcs(template.FuncMap{
				"formatDate": formatDate,
				"htmlify": htmlify,
			}).Parse(Blog_Template))
			fbBuf := new(bytes.Buffer)
			err = blogtmpl.Execute(fbBuf, blog)
			if err != nil {
				log.Println("feedbackdata template Execute failed", err.Error()) // must be valid
				errorHandler(rw, req, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			commonHandler(rw, req, blog.Title, fbBuf.String(), http.StatusOK)
		} else {
			// fetch all blog data
			// render bloglists template
			blogtmpl := template.Must(template.New("BlogsTemplate").Funcs(template.FuncMap{"formatDate": formatDate}).Parse(BlogList_Template))
			fbBuf := new(bytes.Buffer)
			err = blogtmpl.Execute(fbBuf, blogdatamap)
			if err != nil {
				log.Println("feedbackdata template Execute failed", err.Error()) // must be valid
				errorHandler(rw, req, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			commonHandler(rw, req, "OpenREPL Blog", fbBuf.String(), http.StatusOK)
		}
	}
}