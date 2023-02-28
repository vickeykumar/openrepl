package localcommand

import (
	"user"
	"containers"
	"strconv"
	"time"
	"strings"
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

// get working directory for a user
func GetWorkingDir(command, uid string) (wd string) {
	if uid == "" {
		wd = containers.HOME_DIR + command + "/" + strconv.Itoa(int(time.Now().Unix()))
	} else {
		// get user profile and name(4 char from name and 4 char from )
		up, _ := user.FetchUserProfileData(uid)
		wd = containers.HOME_DIR + generateHomeDirectoryID(strings.Split(up.Name," ")[0], uid)+"/"+command
	}
	return 
}