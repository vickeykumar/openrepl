package utils

import (
	"encoding/json"
	"encoding/base64"
	"github.com/natefinch/lumberjack"
	"log"
	"net/url"
	"os"
	"os/exec"
	"time"
	"bufio"
	"path/filepath"
	"io"
	"io/ioutil"
	"bytes"
	"strings"
	"errors"
)

const HOME_DIR = "/tmp/home/"
const GOTTY_PATH = "/opt/gotty"
const SYSTEM_CONFIG_PATH = "/etc"
const GLOBAL_PATH = "~/"
const GitConfigFile = ".gitconfig"
const LOG_PATH = "/gottyTraces"
const IdeLangKey = "IdeLang"
const IdeContentKey = "IdeContent"
const IdeFileNameKey = "IdeFileName"
const CompilerOptionKey = "CompilerOption"
const CompilerFlagsKey = "CompilerFlags"
const EnvFlagsKey = "EnvFlags"
const UidKey = "uid"
const HOME_DIR_KEY = "homedir"
const RequestContextKey = "RequestContextKey"
const DEADLINE_MINUTES = 60 	// keep the deadline to delete the homedir for guest as 1hr
const JobFile = "jobfile"
const REMOVE_JOB_KEY = "REMOVE-"

const USER_PRIVILEGE_KEY = "usermode"
const ADMIN = "admin"
const GUEST = "guest"

var GitConfig map[string]string

func GetGitConfig() (config map[string]string) {
	config = make(map[string]string)
	systemfile := filepath.Join(SYSTEM_CONFIG_PATH, GitConfigFile)
	globalfile := filepath.Join(GLOBAL_PATH, GitConfigFile)
	gottyfile := filepath.Join(GOTTY_PATH, GitConfigFile)

	filelist := []string {
		gottyfile,
		globalfile,
		systemfile,
	}
	var data []byte
	var err error
	for _, file := range(filelist) {
		// Open the .gitconfig file for reading
		data, err = ioutil.ReadFile(file)
		if err != nil {
			log.Printf("Error: failed reading file : %s, error: %s, trying next file.", file, err.Error())
		} else {
			// else break as i already have data from highest priority config file
			break
		}
	}
	// Create a scanner to read the file line-by-line
	scanner := bufio.NewScanner(bytes.NewReader(data))

	// Keep track of the current section name
	currentSection := ""

	// Loop over each line in the file
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check if the line starts a new section
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			// Set the current section name
			currentSection = line[1 : len(line)-1]
			parts := strings.Split(strings.TrimSpace(currentSection), " ")
			for i, part := range parts {
            	parts[i] = strings.Trim(part, "\"")
        	}
            key := strings.Join(parts, ".")
			currentSection = key
			continue
		}

		// Split the line into a key-value pair
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		// Add the key-value pair to the config map
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if currentSection != "" {
			key = currentSection + "." + key
		}
		config[key] = value
	}
	return config
}

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

func init() {
	// init logging
	InitLogging("gotty")
	// populate gitconfig
	GitConfig = GetGitConfig()
	log.Println("config read: ", GitConfig)

	// init the global scheduler
	InitGottyJobs()
}

func JsonMarshal(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		log.Println("ERROR: while Marshalling : ", v, "Error: ", err)
	}
	return data
}

func JsonUnMarshal(data []byte, v interface{}) error {
	err := json.Unmarshal(data, v)
	if err != nil {
		log.Println("ERROR: while unMarshalling : ", data, " Error: ", err)
	}
	return err
}

func Iscompiled(params map[string][]string) bool {
	_, ok1 := params[IdeLangKey]
	_, ok2 := params[IdeContentKey]
	return ok1 && ok2
}

func GetCompilerLang(params url.Values) string {
	return params.Get(IdeLangKey)
}

func GetUid(params url.Values) string {
	return params.Get(UidKey)
}

func GetHomeDir(params url.Values) string {
	return params.Get(HOME_DIR_KEY)
}

func GetIdeContent(params url.Values) string {
	return params.Get(IdeContentKey)
}

func GetIdeFileName(params url.Values) string {
	filename := params.Get(IdeFileNameKey)
	if filename == "" || !IsFile(filename) {
		return ""
	}
	return filename
}

func GetCompilerOption(params url.Values) string {
	return params.Get(CompilerOptionKey)
}

func GetCompilerFlags(params url.Values) string {
	return params.Get(CompilerFlagsKey)
}

func GetEnvFlags(params url.Values) string {
	return params.Get(EnvFlagsKey)
}

func GetUnixMilli() int64 {
 	return time.Now().UnixNano() / int64(time.Millisecond)
}

func IsUserAdmin(params url.Values) bool {
	return params.Get(USER_PRIVILEGE_KEY)==ADMIN
}
func IsDir(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fileInfo.IsDir()
}

func IsFile(path string) bool {
    fileInfo, err := os.Stat(path)
    if err != nil {
        return false
    }
    return !fileInfo.IsDir()
}

// IsDirEmpty returns true if the directory is empty, false otherwise.
func IsDirEmpty(dirPath string) bool {
	f, err := os.Open(dirPath)
	if err != nil {
		log.Println("Error checking directory IsDirEmpty: ", err)
		return false
	}
	defer f.Close()

	_, err = f.Readdir(1)
	if err == nil {
		// There is at least one file or directory in the directory
		return false
	}

	if err == os.ErrNotExist {
		// The directory does not exist, so it's considered empty
		return true
	}

	if err == io.EOF {
		// The directory is empty
		return true
	}

	log.Println("Error checking directory IsDirEmpty: ", err)
	// Some other error occurred
	return false
}

// attempts to remove the directories recursively
func RemoveDir(dirPath string) {
	absDir, err := filepath.Abs(dirPath)
    	if err != nil {
        	log.Printf("Error getting absolute path: %s\n", err)
        	return
    	}
	// Check that the absolute path is under the root directory to prevent accidental deletion of system files
    	root := string(HOME_DIR)
    	if !filepath.HasPrefix(absDir, root) {
        	log.Println("Error: directory path is not under root directory", HOME_DIR)
        	return
    	}
	// attempts to delete
	err = os.RemoveAll(absDir)
	if err != nil {
		log.Printf("Error removing directory, trying rm -rf : %s\n", err)
		    // Execute the rm -rf command
    		cmd := exec.Command("rm", "-rf", absDir)
    		err = cmd.Run()
    		if err != nil {
        		log.Printf("Error removing directory: %s\n", err)
    		} else {
       	 		log.Printf("Directory %s successfully removed\n", absDir)
    		}
	} else {
		log.Printf("Directory %s successfully removed\n", absDir)
	}
}

func CopyFile(srcPath string, dstPath string) error {
    src, err := os.Open(srcPath)
    if err != nil {
        return err
    }
    defer src.Close()

    dst, err := os.Create(dstPath)
    if err != nil {
        return err
    }
    defer dst.Close()

    if _, err := io.Copy(dst, src); err != nil {
        return err
    }

    if err := dst.Sync(); err != nil {
        return err
    }

    return nil
}

func CopyDir(src string, dst string) error {
    // create the destination directory if it doesn't exist
    if err := os.MkdirAll(dst, os.ModePerm); err != nil {
        return err
    }

    // walk the source directory and copy its files and directories
    return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }

        // calculate the destination path
        relPath, err := filepath.Rel(src, path)
        if err != nil {
            return err
        }
        dstPath := filepath.Join(dst, relPath)

        // copy the file or create the directory
        if info.IsDir() {
            return os.MkdirAll(dstPath, os.ModePerm)
        } else {
            return CopyFile(path, dstPath)
        }
    })
}

func SaveIdeContentToFile(params url.Values, filename string) error {
	if filename == "" || !IsFile(filename) {
		return errors.New(filename+" Not a Valid File")
	}
	decoded, err := base64.StdEncoding.DecodeString(GetIdeContent(params))
	if err != nil {
		return err
	}

	// Write the decoded data to a file
	err = ioutil.WriteFile(filename, decoded, os.ModePerm)
	if err != nil {
		return err
	}
	return nil
}

