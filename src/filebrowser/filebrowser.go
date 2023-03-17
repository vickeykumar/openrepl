package filebrowser

import (
    "os"
    "io/ioutil"
	"path/filepath"
	"sync"
    "github.com/gorilla/websocket"
)

type TreeNode struct {
    Id       string      `json:"id"`
    Text     string      `json:"text"`
    Type     string      `json:"type"`
    Children []TreeNode  `json:"children,omitempty"`
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
        node.Type = "folder"
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

// TBD: to implement real time browser with the help of websocket connections

type Filebrowser struct {
	Root string
	Conn *websocket.Conn
	writeMutex sync.Mutex

}


func New(path string, conn *websocket.Conn) *Filebrowser {
	fb := &Filebrowser {
		Root: path,
		Conn: conn,
	}
	return fb
}

func (fb *Filebrowser) GetJsonTree() (tree TreeNode, err error) {
	tree, err = BuildTree(fb.Root)
    return
}




