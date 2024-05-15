package filebrowser

import (
    "fmt"
    "os"
    "io/ioutil"
	"path/filepath"
    "github.com/fsnotify/fsnotify"
    "log"
    "utils"
    "net/http"
    "archive/zip"
    "io"
    "strings"
    "errors"
)

const oneMB = 1024*1024
const MAXDISKUSAGE_MB = 50

type TreeNode struct {
    Id       string      `json:"id"`
    Text     string      `json:"text"`
    Type     string      `json:"type"`
    Children []TreeNode  `json:"children,omitempty"`
}

type Event struct {
    Name       string      `json:"Name"`
    Op         fsnotify.Op `json:"Op"`
    Type       string      `json:"type"`
    NewName    string      `json:"NewName,omitempty"`   // usefull in case of rename
}

func NewEvent(name string, op fsnotify.Op, t string, newname string) *Event {
    localevent :=  &Event {
        Name:       name,
        Op:         op,
        Type:       t,
        NewName:    newname,
    }
    return localevent
}

func BuildTree(path string) (TreeNode, error) {
    info, err := os.Stat(path)
    if err != nil {
        return TreeNode{}, err
    }
    node := TreeNode{
        Id:   path,
        Text: filepath.Base(path),
    }
    if info.IsDir() {
        node.Type = "default"
        fileInfos, err := ioutil.ReadDir(path)
        if err != nil {
            return TreeNode{}, err
        }
        for _, fileInfo := range fileInfos {
            childNode, err := BuildTree(filepath.Join(path, fileInfo.Name()))
            if err != nil {
                return TreeNode{}, err
            }
            node.Children = append(node.Children, childNode)
        }
    } else {
        node.Type = "file"
    }
    return node, nil
}

type Eventwriter interface {
    WriteEvent(data []byte) error
}

// TBD: to implement real time browser with the help of websocket connections

type Filebrowser struct {
	Root                string
    watcher             *fsnotify.Watcher
    done                chan bool
    eventwriter         Eventwriter
    watcherMap          map[string]bool     // track the watcher already installed
    deferwatch          bool                // deferwatch flag to defer the notification till the (close)
                                            // (important in case of transient processes like optionrun)
    pendingnotiflist    []*Event            // list of pending notif events
    size                float64             // current size of this file browser in MB              
}


func New(fbpath string, eventwriter Eventwriter, watch bool, deferwatch bool) (*Filebrowser, error) {
    var watcher *fsnotify.Watcher = nil
    var dirSize int64 = 0
    var done chan bool
    var newEventsNotifList []*Event
    var err error
    var recursivewatch bool = true
    if fbpath=="" {
        return nil, errors.New("Invalid Path: "+fbpath)
    }
    // if path is file.. only watch its parent
    if utils.IsFile(fbpath) {
        fbpath = filepath.Dir(fbpath)   // parent
        recursivewatch = false
    }

    // in case of defer watch also, no recursive watch
    recursivewatch=!deferwatch

    if watch {
        watcher, err = fsnotify.NewWatcher()
        if err != nil {
            return nil, err
        }
    }

    // Traverse the directory tree recursively, calculate the the size on the way
    filepath.Walk(fbpath, func(path string, info os.FileInfo, err error) error {
	if err == nil {
        	// calculate the total size of directory
        	dirSize += info.Size()
	}
        if watch && info.IsDir() {
            if recursivewatch {
                err = watcher.Add(path)
                if err != nil {
                    log.Println("error adding watcher to path: ", path, err)
                }
            } else if path==fbpath {
                // only watch this file browser path
                err = watcher.Add(path)
                if err != nil {
                    log.Println("error adding watcher to path: ", path, err)
                }
            }
        }
        return nil
    })

    sizeMB := float64(dirSize) / oneMB
    //log.Println("Calculated browser size for ", fbpath, sizeMB)

    done = make(chan bool)

    watcherMap := make(map[string]bool)
	fb := &Filebrowser {
		Root:             fbpath,
        watcher:          watcher,
        done:             done,
        eventwriter:      eventwriter,
        watcherMap:       watcherMap,
        deferwatch:       deferwatch,
        pendingnotiflist: newEventsNotifList,
        size:             sizeMB,         
	}
    
    if fb.GetSize() > float64(MAXDISKUSAGE_MB) {
        errstr := fmt.Sprintf("Max Disk Usage Limit of %dMB Reached for : %s Please delete unwanted files and try again.\n", MAXDISKUSAGE_MB, fb.Root)
        err = errors.New(errstr)
        return fb, err
    }

	return fb, nil
}

func (fb *Filebrowser) GetSize() float64 {
    return float64(fb.size)
}

func (fb *Filebrowser) GetJsonTree() (tree TreeNode, err error) {
	tree, err = BuildTree(fb.Root)
    return
}

func (fb *Filebrowser) PurgePendingEventNotifications() {
    // purge and send pending events list to the clients
    for _, localevent := range fb.pendingnotiflist {
        err := fb.eventwriter.WriteEvent(utils.JsonMarshal(localevent))
        if err != nil {
            log.Println("Error sending pending event message to browser: ", localevent.Name, localevent.Op, err)
        } else {
            log.Println("Successfully sent pending event message to browser: ", localevent.Name, localevent.Op)
        }
    }
}

func (fb *Filebrowser) eventHandler(event fsnotify.Event) {
    path := event.Name
    var isdir bool = utils.IsDir(path)
    var newEventsNotifList []*Event
    if event.Op&fsnotify.Create == fsnotify.Create {
        log.Println("File created:", path)
        if isdir {
            // install watcher only not installed already
            // Traverse the directory tree recursively, usefull in case deflating of archives
            filepath.Walk(path, func(walkpath string, info os.FileInfo, err error) error {
                if info.IsDir() {
                    if _, ok := fb.watcherMap[walkpath]; !ok {
                        err = fb.watcher.Add(walkpath)
                        if err != nil {
                            log.Println("error adding watcher to path: ", walkpath, err)
                        } else {
                            fb.watcherMap[walkpath]=true
                            newEventsNotifList  = append(newEventsNotifList , NewEvent(
                                walkpath,
                                event.Op,
                                "default",  // for folder
                                "",
                            ))   // update the watched list for folder
                        }
                    }
                } else {
                    newEventsNotifList  = append(newEventsNotifList , NewEvent(
                                walkpath,
                                event.Op,
                                "file",  // for files
                                "",
                     ))   // update the notificationlist for files
                }
                return nil
            })
        }
    }
    if event.Op&fsnotify.Write == fsnotify.Write {
        log.Println("File modified:", path)
    }
    if event.Op&fsnotify.Remove == fsnotify.Remove {
        log.Println("File removed:", path)
        if isdir {
            err := fb.watcher.Remove(path)
            if err != nil {
            log.Println("error adding watcher to path: ", path, err)
            } else {
                fb.watcherMap[path]=false
                delete(fb.watcherMap, path)
            }
        }
    }
    if event.Op&fsnotify.Rename == fsnotify.Rename {
        log.Println("File renamed:", path)
    }
    if event.Op&fsnotify.Chmod == fsnotify.Chmod {
        log.Println("File permissions modified, event not handled, skipping...", path)
        // not handled now
        return
    }

    var t string = "file"
    if isdir {
        t = "default"
    }
    localevent :=  &Event {
        Name: event.Name,
        Op:   event.Op,
        Type: t,
    }

    // if send notification is deferred, lets hold the notifications
    if fb.deferwatch {
        fb.pendingnotiflist = append(fb.pendingnotiflist, localevent)
    } else {
        // send event to browser
        err := fb.eventwriter.WriteEvent(utils.JsonMarshal(localevent))
        if err != nil {
            log.Println("Error sending event message to browser: ", localevent.Name, localevent.Op, err)

        }
    }

    // send further events for nested directories in create flow
    for _, localevent := range newEventsNotifList {
        // event already sent for event.Name, send other pending notifications in this call
        if localevent.Name != event.Name {
            // if send notification is deferred, lets hold the notifications
            if fb.deferwatch {
                fb.pendingnotiflist = append(fb.pendingnotiflist, localevent)
            } else {
                // send event to browser
                err := fb.eventwriter.WriteEvent(utils.JsonMarshal(localevent))
                if err != nil {
                    log.Println("Error sending event message to browser: ", localevent.Name, localevent.Op, err)

                }
            }
        }
    }
}

func (fb *Filebrowser) StartWatching() {
    go func() {
        log.Println("Started watching browser:", fb.Root)
        for {
            select {
            case event := <- fb.watcher.Events:
                log.Println("Event:", event )
                fb.eventHandler(event)
            case err := <- fb.watcher.Errors:
                log.Println("Watcher Error:", err)
            case <- fb.done: // stop the goroutine when the done channel is closed
                log.Println("Stopped watcher")
                return
            }
        }
    }()
}

// stop watching and close the browser
func (fb *Filebrowser) Close() {
    log.Println("purge pending Notifications before Exiting the browser:")
    fb.PurgePendingEventNotifications()
    log.Println("closing browser:", fb.Root)
    if fb.done != nil {
        close(fb.done)
    }
    log.Println("browser closed:", fb.Root)
    log.Println("closing watcher:", fb.Root)
    if fb.watcher != nil {
        err := fb.watcher.Close()
        if err != nil {
            log.Println("error closing watcher: ", err)
        }
    }
    // clear the watcherMap
    fb.watcherMap = make(map[string]bool)
}

func (fb *Filebrowser) GetWatcher() *fsnotify.Watcher {
    return fb.watcher
}

// this handler processes events recieved from request body and reflect it locally 
func (fb *Filebrowser) ProcessEventRequests(w http.ResponseWriter, req *http.Request, event Event) {
    var isdir bool = false
    if event.Type == "folder" || event.Type == "default" {
        isdir = true
    }
    if event.Op&fsnotify.Create == fsnotify.Create {
        // prevent create requests if dir quota is reached
        if fb.GetSize() > float64(MAXDISKUSAGE_MB) {
            errstr := fmt.Sprintf("Max Disk Usage Limit of %dMB Reached for : %s Please delete unwanted files and try again.\n", MAXDISKUSAGE_MB, fb.Root)
            http.Error(w, errstr, http.StatusInsufficientStorage)
            return
        }
        if isdir {
            if event.NewName == "" { // create directory operation
                err := os.MkdirAll(event.Name, 0755)
                if err != nil {
                    log.Println("Error creating directory:", event.Name, err.Error())
                    http.Error(w, err.Error(), http.StatusInternalServerError)
                    return
                }
                log.Println("created directory:", event.Name)
            } else {    // its a copy operation from event.Name to event.NewName
                err := utils.CopyDir(event.Name, event.NewName)
                if err != nil {
                    log.Println("Error copying directory:", event.Name, event.NewName, err.Error())
                    http.Error(w, err.Error(), http.StatusInternalServerError)
                    return
                }
                log.Println("copied directory: "+event.Name+" to "+event.NewName )
            }
        } else {
            // create file
            if event.NewName == "" { // create file operation
                file, err := os.Create(event.Name)
                if err != nil {
                    log.Println("Error creating file:", event.Name, err.Error())
                    http.Error(w, err.Error(), http.StatusInternalServerError)
                    return
                }
                defer file.Close()
                log.Println("created file:", event.Name)
            } else { // its a copy operation from event.Name to event.NewName
                err := utils.CopyFile(event.Name, event.NewName)
                if err != nil {
                    log.Println("Error copying directory:", event.Name, event.NewName, err.Error())
                    http.Error(w, err.Error(), http.StatusInternalServerError)
                    return
                }
                log.Println("copied file: "+event.Name+" to "+event.NewName )
            }
        }
    }

    if event.Op&fsnotify.Write == fsnotify.Write {
        log.Println("File modified:", event.Name)
    }
    if event.Op&fsnotify.Remove == fsnotify.Remove {
        log.Println("File removed:", event.Name)
        if isdir {
            // remove directory
            err := os.RemoveAll(event.Name)
            if err != nil {
                log.Println("Error removing directory:", event.Name, err.Error())
                http.Error(w, err.Error(), http.StatusInternalServerError)
                return
            }
            log.Println("removed directory:", event.Name)
        } else {
            // remove file
            err := os.Remove(event.Name)
            if err != nil {
                log.Println("Error removing file:", event.Name, err.Error())
                http.Error(w, err.Error(), http.StatusInternalServerError)
                return
            }
            log.Println("removed file:", event.Name)
        }
    }
    if event.Op&fsnotify.Rename == fsnotify.Rename {
        err := os.Rename(event.Name, event.NewName)
        if err != nil {
            log.Println("Error renaming :", event.Name, err.Error())
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        log.Println("File renamed:", event.Name, event.NewName)
    }
    if event.Op&fsnotify.Chmod == fsnotify.Chmod {
        log.Println("File permissions modified:", event.Name)
    }

}


func (fb *Filebrowser) Writezip(zipWriter *zip.Writer) error {
    // Open the directory to be zipped
    dir := fb.Root
    parentDir := dir
    if utils.IsFile(dir) {
        parentDir = filepath.Dir(dir)
    }
    err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }

        // Skip directories
        if info.IsDir() {
            return nil
        }

        // Open the file to be added to the zip archive
        file, err := os.Open(path)
        if err != nil {
            return err
        }
        defer file.Close()

        // Create a new file header and set its name with respect to the main parent
        header := &zip.FileHeader{
            Name: strings.TrimPrefix(path, parentDir+"/"),
        }

        // Set the file mode and modification time
        mode := info.Mode()
        header.SetModTime(info.ModTime())
        header.SetMode(mode)

        // Add the file header to the zip archive
        writer, err := zipWriter.CreateHeader(header)
        if err != nil {
            return err
        }

        // Copy the file contents to the zip archive
        if _, err := io.Copy(writer, file); err != nil {
            return err
        }

        return nil
    })
    return err
}
