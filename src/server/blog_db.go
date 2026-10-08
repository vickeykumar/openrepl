package server

import (
	"encoding/json"
	"fmt"
	"persist"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
	"utils"
	"html/template"
	"bytes"
	"github.com/pkg/errors"
)

const BLOG_DB = utils.GOTTY_PATH + "/blog.db"

var blog_db_handle persist.Store

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
	blog_db_handle, err = persist.Open(BLOG_DB)
	if err != nil {
		log.Println("ERROR: Error while creating blog DB handle : ", err.Error())
		os.Exit(3)
	}
	log.Println("Successfully initialized blog db handle: ", blog_db_handle)
}

func CloseBlogDBHandle() {
	if blog_db_handle == nil {
		return
	}
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
	knowledgeInvalidate()
	return err
}

func deleteBlogData(blogname string) error {
	err := blog_db_handle.Delete([]byte(blogname))
	if err!= nil {
		return err
	}
	err = blog_db_handle.Commit()
	knowledgeInvalidate()
	return err
}

func FetchBlogDataMap() (bloglistmap map[string]BlogPost) {
	bloglistmap = make(map[string]BlogPost)
	err := blog_db_handle.Each(func(key, value []byte) bool {
		var blog BlogPost // a field missing from the record must not keep the last one's value
		if err := json.Unmarshal(value, &blog); err != nil {
			log.Println("ERROR: while unMarshalling for key: ", string(key), value, " Error: ", err)
			return true
		}
		bloglistmap[string(key)] = blog
		return true
	})
	if err != nil {
		log.Println("Error reading the blog records: ", err.Error())
	}
	return
}

// Define a template function to format the date
func formatDate(t time.Time) string {
    return t.Format("January 2, 2006") // Example: "January 1, 2024"
}

// ---- what visitors and the editor see -------------------------------------------

const (
	blogMaxBody  = 2 << 20 // a post with a few pictures inline
	blogNameMax  = 200
	blogTitleMax = 200
	blogDescMax  = 500
)

var tagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

// blogMinutes is the reading time of a post, at 200 words a minute, at least 1.
func blogMinutes(html string) int {
	words := len(strings.Fields(tagPattern.ReplaceAllString(html, " ")))
	if m := (words + 199) / 200; m > 1 {
		return m
	}
	return 1
}

// blogSummary is one post as the list and the editor's sidebar show it.
type blogSummary struct {
	Name        string    `json:"name"`
	Title       string    `json:"title"`
	Desc        string    `json:"desc"`
	Lastupdated time.Time `json:"lastupdated"`
	Minutes     int       `json:"minutes"`
}

// sortedBlogs lists the posts, the most recently updated first.
func sortedBlogs(posts map[string]BlogPost) []blogSummary {
	out := make([]blogSummary, 0, len(posts))
	for name, p := range posts {
		title := p.Title
		if title == "" {
			title = name
		}
		out = append(out, blogSummary{Name: name, Title: title, Desc: p.Desc, Lastupdated: p.Lastupdated, Minutes: blogMinutes(p.Content)})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Lastupdated.Equal(out[j].Lastupdated) {
			return out[i].Lastupdated.After(out[j].Lastupdated)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// blogPage is a post with what the page adds to it.
type blogPage struct {
	BlogPost
	Minutes int
}

// blogReply answers the editor, in JSON when it asks for it.
func blogReply(w http.ResponseWriter, r *http.Request, status int, message, name string) {
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": status < 300, "message": message, "name": name})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintln(w, message)
}

// checkBlogFields cleans and checks what the editor sends.
func checkBlogFields(name, title, desc, content string) (BlogPost, string) {
	post := BlogPost{Name: cleanLine(name), Title: cleanLine(title), Desc: cleanText(desc), Content: strings.TrimSpace(content)}
	switch {
	case post.Name == "":
		return post, "A post needs an address (its name)."
	case utf8.RuneCountInString(post.Name) > blogNameMax:
		return post, fmt.Sprintf("The address is too long (at most %d characters).", blogNameMax)
	case post.Title == "":
		return post, "A post needs a title."
	case utf8.RuneCountInString(post.Title) > blogTitleMax:
		return post, fmt.Sprintf("The title is too long (at most %d characters).", blogTitleMax)
	case post.Desc == "":
		return post, "A post needs a short description."
	case utf8.RuneCountInString(post.Desc) > blogDescMax:
		return post, fmt.Sprintf("The description is too long (at most %d characters).", blogDescMax)
	case post.Content == "":
		return post, "Write something in the post first."
	}
	return post, ""
}

func handleBlog(rw http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodPost {
		// Only an admin changes posts, and only from the editor: a page of another
		// site cannot add the header (the same rule as the dashboard's API).
		if !IsUserAdmin(rw, req) {
			blogReply(rw, req, http.StatusUnauthorized, "Please sign in again as an admin.", "")
			return
		}
		if !adminRequestOK(req) {
			blogReply(rw, req, http.StatusForbidden, "Posts are changed from the blog editor.", "")
			return
		}
		req.Body = http.MaxBytesReader(rw, req.Body, blogMaxBody)
		if err := req.ParseForm(); err != nil {
			status, msg := http.StatusBadRequest, "The form could not be read."
			if strings.Contains(err.Error(), "too large") {
				status, msg = http.StatusRequestEntityTooLarge, "That post is too big (at most 2 MB, pictures included)."
			}
			blogReply(rw, req, status, msg, "")
			return
		}
		if blog_db_handle == nil {
			blogReply(rw, req, http.StatusServiceUnavailable, "The blog can't be saved right now.", "")
			return
		}
		if req.Form.Get("q") == "delete" {
			name := cleanLine(req.Form.Get("name"))
			if err := deleteBlogData(name); err != nil {
				log.Println("blog delete failed for key: ", name, err.Error())
				blogReply(rw, req, http.StatusNotFound, "There is no post called "+name+".", name)
				return
			}
			blogReply(rw, req, http.StatusOK, "Deleted "+name+".", name)
			return
		}
		post, problem := checkBlogFields(req.Form.Get("name"), req.Form.Get("title"), req.Form.Get("desc"), req.Form.Get("content"))
		if problem != "" {
			blogReply(rw, req, http.StatusBadRequest, problem, post.Name)
			return
		}
		blog := NewBlogPost(post.Name, post.Title, post.Desc, post.Content)
		if err := StoreBlogData(blog); err != nil {
			log.Println("blog save failed for key: ", post.Name, err.Error())
			blogReply(rw, req, http.StatusInternalServerError, "The post could not be saved. Please try again.", post.Name)
			return
		}
		blogReply(rw, req, http.StatusOK, "Saved "+post.Name+".", post.Name)
		return
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		rw.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(rw, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	req.ParseForm()
	blogdatamap := FetchBlogDataMap()
	query := req.Form.Get("q")
	writeJSON := func(v interface{}) {
		data, err := json.Marshal(v)
		if err != nil {
			log.Println("error encoding blog JSON:", err)
			http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		rw.Write(data)
	}
	switch query {
	case "list": // the names only (older callers)
		names := []string{}
		for _, b := range sortedBlogs(blogdatamap) {
			names = append(names, b.Name)
		}
		writeJSON(names)
		return
	case "index": // the editor's list: newest first, with the titles and dates
		writeJSON(sortedBlogs(blogdatamap))
		return
	}

	if len(blogdatamap) == 0 {
		log.Println("blog data not found")
		errorHandler(rw, req, "No Blogs To Show", http.StatusNotFound)
		return
	}
	name := req.Form.Get("name")
	if name == "" {
		// the list of posts
		blogtmpl := template.Must(template.New("BlogsTemplate").Funcs(template.FuncMap{"formatDate": formatDate}).Parse(BlogList_Template))
		buf := new(bytes.Buffer)
		if err := blogtmpl.Execute(buf, sortedBlogs(blogdatamap)); err != nil {
			log.Println("blog list template Execute failed", err.Error()) // must be valid
			errorHandler(rw, req, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		commonHandler(rw, req, "OpenREPL Blog", buf.String(), http.StatusOK)
		return
	}
	blog, ok := blogdatamap[name]
	if !ok {
		log.Println("blog data not found for: ", name)
		errorHandler(rw, req, template.HTMLEscapeString(name)+" Not Found.", http.StatusNotFound)
		return
	}
	if query == "json" {
		writeJSON(blog)
		return
	}
	blogtmpl := template.Must(template.New("BlogTemplate").Funcs(template.FuncMap{
		"formatDate": formatDate,
		"htmlify":    htmlify,
	}).Parse(Blog_Template))
	buf := new(bytes.Buffer)
	if err := blogtmpl.Execute(buf, blogPage{BlogPost: blog, Minutes: blogMinutes(blog.Content)}); err != nil {
		log.Println("blog template Execute failed", err.Error()) // must be valid
		errorHandler(rw, req, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	commonHandler(rw, req, blog.Title, buf.String(), http.StatusOK)
}
