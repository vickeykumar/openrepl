package user

import (
    "io/ioutil"
    "os"
    "utils"
    "strconv"
    "time"
    "strings"
    "log"
)


func generateHomeDirectoryID(firstName string, uniqueID string) string {
    // Take the first four characters of the first name or pad with zeros
    var prefix string
    if len(firstName) >= 4 {
        prefix = firstName[:4]
    } else {
        prefix = firstName + strings.Repeat("0", 4-len(firstName))
    }
    
    // Concatenate the prefix and the unique ID
    id := prefix + uniqueID
    
    // Take first 10 characters
    id = id[:8]
    
    return strings.ToLower(id)
}

// get home directory for a user
func GetHomeDir(uid string) (homedir string) {
    var err error
	if uid == "" {
        homedir, err = ioutil.TempDir(utils.HOME_DIR, "guest-")
        if err != nil {
            log.Println("Error creating temporary directory:", err)
            homedir = utils.HOME_DIR + strconv.Itoa(int(time.Now().Unix()))
        }
		
	} else {
		// get user profile and name(4 char from name and 4 char from uid)
		up, _ := FetchUserProfileData(uid)
		homedir = utils.HOME_DIR + generateHomeDirectoryID(strings.Split(up.Name," ")[0], uid)
	}
    if _, err := os.Stat(homedir); os.IsNotExist(err) {
        os.MkdirAll(homedir, 0755)
    }
    log.Println("New homedir generated: ", homedir)
	return 
}